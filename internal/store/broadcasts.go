package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Рассылки администратора (Phase 10, 10-CONTEXT.md, D-19…D-21): объявление —
// всем, кроме отказавшихся; приглашение — включившим уведомления, по городу
// и видам спорта профиля. Получатели — строки очереди вида broadcast с
// содержимым {"b": <id>}; списка получателей отдельно нет. Текст рассылки
// лежит в broadcasts, пока строки рассылки не закрыты (Q-2 а).

// Виды рассылки (broadcasts.kind).
const (
	BroadcastAnnounce = "announce"
	BroadcastInvite   = "invite"
)

// Виды строк очереди рассылки: сообщение получателю и итог администратору (B-6).
const (
	OutboxBroadcast     = "broadcast"
	OutboxBroadcastDone = "broadcast_done"
)

// BroadcastFilter — отбор приглашения: город с ключом каталога в паре со
// страной и/или виды спорта анкеты. Задано и то и другое — получатель должен
// подходить по обоим (D-09).
type BroadcastFilter struct {
	Country string   `json:"country,omitempty"`
	CityKey string   `json:"city_key,omitempty"`
	Sports  []string `json:"sports,omitempty"`
}

// Empty — нет ни города, ни видов спорта: приглашению отбирать не по чему.
func (f BroadcastFilter) Empty() bool { return f.CityKey == "" && len(f.Sports) == 0 }

// recipientsSQL — получатели рассылки: $1 — страна, $2 — ключ города, $3 —
// виды спорта отбора, $4 — вид рассылки. Объявление — все пользователи без
// отказа (строки профиля может не быть); приглашение — согласие, завершённая
// анкета и отбор.
const recipientsSQL = `
	SELECT u.tg_id FROM users u LEFT JOIN user_profiles p ON p.tg_id = u.tg_id
	WHERE CASE WHEN $4::text = 'announce' THEN NOT COALESCE(p.announce_opt_out, false)
	      ELSE COALESCE(p.notify_opt_in AND p.status = 'completed', false)
	           AND ($2::text = '' OR (p.residence_country_code = $1::text AND p.residence_city_key = $2::text))
	           AND (cardinality($3::text[]) = 0 OR p.sports && $3::text[]) END`

func recipientArgs(kind string, f BroadcastFilter) []any {
	sports := f.Sports
	if sports == nil {
		sports = []string{}
	}
	return []any{f.Country, f.CityKey, sports, kind}
}

// CountRecipients — сколько получателей у рассылки вида kind с отбором f
// сейчас. Только читает.
func (s *Store) CountRecipients(ctx context.Context, kind string, f BroadcastFilter) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM (`+recipientsSQL+`) r`, recipientArgs(kind, f)...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count recipients: %w", err)
	}
	return n, nil
}

// LastAnnounce — время последнего объявления; ok = false — объявлений не было.
func (s *Store) LastAnnounce(ctx context.Context) (at time.Time, ok bool, err error) {
	var t *time.Time
	if err := s.db.QueryRow(ctx, `SELECT max(created_at) FROM broadcasts WHERE kind = $1`, BroadcastAnnounce).Scan(&t); err != nil {
		return time.Time{}, false, fmt.Errorf("last announce: %w", err)
	}
	if t == nil {
		return time.Time{}, false, nil
	}
	return *t, true, nil
}

// CreateBroadcast одной транзакцией записывает рассылку в журнал и по строке
// очереди broadcast каждому получателю, отобранному в этот момент. Получателей
// нет — ничего не пишется, id = 0. Возвращает номер рассылки и число строк.
func (s *Store) CreateBroadcast(ctx context.Context, kind string, admin int64, f BroadcastFilter, text string, now time.Time) (id int64, n int, err error) {
	filter := []byte(`{}`)
	if kind == BroadcastInvite {
		if filter, err = json.Marshal(f); err != nil {
			return 0, 0, fmt.Errorf("create broadcast: %w", err)
		}
	}
	err = s.InTx(ctx, func(tx *Store) error {
		const ins = `
			INSERT INTO broadcasts (kind, admin_tg_id, created_at, filter, text, recipients)
			VALUES ($1, $2, $3, $4, $5, 0) RETURNING id`
		if err := tx.db.QueryRow(ctx, ins, kind, admin, now, filter, text).Scan(&id); err != nil {
			return fmt.Errorf("create broadcast: %w", err)
		}
		q := `
			INSERT INTO outbox (tg_id, kind, dedup_key, payload, created_at)
			SELECT r.tg_id, $5::text, $5::text || ':' || $6::bigint::text, jsonb_build_object('b', $6::bigint), $7::timestamptz
			FROM (` + recipientsSQL + `) r ORDER BY r.tg_id
			ON CONFLICT (tg_id, dedup_key) DO NOTHING`
		cmd, err := tx.db.Exec(ctx, q, append(recipientArgs(kind, f), OutboxBroadcast, id, now)...)
		if err != nil {
			return fmt.Errorf("create broadcast: enqueue: %w", err)
		}
		if n = int(cmd.RowsAffected()); n == 0 {
			return errNoRecipients
		}
		if _, err := tx.db.Exec(ctx, `UPDATE broadcasts SET recipients = $2 WHERE id = $1`, id, n); err != nil {
			return fmt.Errorf("create broadcast: recipients: %w", err)
		}
		return nil
	})
	if errors.Is(err, errNoRecipients) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	return id, n, nil
}

var errNoRecipients = errors.New("broadcast: нет получателей")

// BroadcastSend — что нужно для отправки строки рассылки получателю tgID:
// вид и текст рассылки и выполняется ли условие сейчас (объявление — отказа
// нет, приглашение — согласие включено). Текст пуст — рассылка завершена или
// её нет. Только читает.
func (s *Store) BroadcastSend(ctx context.Context, id, tgID int64) (kind, text string, allowed bool, err error) {
	const q = `
		SELECT b.kind, COALESCE(b.text, ''),
		       CASE WHEN b.kind = 'announce' THEN NOT COALESCE(p.announce_opt_out, false)
		            ELSE COALESCE(p.notify_opt_in, false) END
		FROM broadcasts b LEFT JOIN user_profiles p ON p.tg_id = $2
		WHERE b.id = $1`
	err = s.db.QueryRow(ctx, q, id, tgID).Scan(&kind, &text, &allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("broadcast send: %w", err)
	}
	return kind, text, allowed, nil
}

// FinishBroadcasts завершает рассылки, у которых не осталось ожидающих строк:
// ставит время завершения, стирает текст и одной записью создаёт администратору,
// запустившему рассылку, строку broadcast_done с видом и итогом — доставлено
// (Telegram принял) и не доставлено (остальные строки, в том числе с
// неизвестным исходом). Возвращает число завершённых рассылок.
func (s *Store) FinishBroadcasts(ctx context.Context, now time.Time) (int, error) {
	const q = `
		WITH done AS (
			UPDATE broadcasts b SET finished_at = $1, text = NULL
			WHERE b.finished_at IS NULL AND NOT EXISTS (
				SELECT 1 FROM outbox o WHERE o.kind = $2 AND (o.payload->>'b')::bigint = b.id
				  AND o.claimed_at IS NULL AND o.done_at IS NULL)
			RETURNING b.id, b.kind, b.admin_tg_id
		), counts AS (
			SELECT d.id, d.kind, d.admin_tg_id,
			       count(o.id) FILTER (WHERE o.sent_at IS NOT NULL) AS sent,
			       count(o.id) FILTER (WHERE o.sent_at IS NULL) AS failed
			FROM done d LEFT JOIN outbox o ON o.kind = $2 AND (o.payload->>'b')::bigint = d.id
			GROUP BY d.id, d.kind, d.admin_tg_id
		)
		INSERT INTO outbox (tg_id, kind, dedup_key, payload, created_at)
		SELECT c.admin_tg_id, $3::text, $3::text || ':' || c.id::text,
		       jsonb_build_object('b', c.id, 'kind', c.kind, 'sent', c.sent, 'failed', c.failed), $1
		FROM counts c
		ON CONFLICT (tg_id, dedup_key) DO NOTHING`
	cmd, err := s.db.Exec(ctx, q, now, OutboxBroadcast, OutboxBroadcastDone)
	if err != nil {
		return 0, fmt.Errorf("finish broadcasts: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// NotifyPrefs — уведомления пользователя: приглашения включены, новости
// выключены, и можно ли включить приглашения (анкета завершена, есть город с
// ключом каталога или вид спорта). Нет строки профиля — всё выключено, новости
// включены.
type NotifyPrefs struct {
	Invite, AnnounceOff, Eligible bool
}

// NotifyPrefs читает уведомления пользователя. Только читает.
func (s *Store) NotifyPrefs(ctx context.Context, tgID int64) (NotifyPrefs, error) {
	const q = `
		SELECT notify_opt_in, announce_opt_out,
		       status = 'completed' AND (residence_city_key IS NOT NULL OR sports <> '{}')
		FROM user_profiles WHERE tg_id = $1`
	var p NotifyPrefs
	err := s.db.QueryRow(ctx, q, tgID).Scan(&p.Invite, &p.AnnounceOff, &p.Eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotifyPrefs{}, nil
	}
	if err != nil {
		return NotifyPrefs{}, fmt.Errorf("notify prefs: %w", err)
	}
	return p, nil
}

// SetInvite включает или выключает приглашения; profile_v не меняется.
// Включить можно только при завершённой анкете с городом с ключом каталога
// или видом спорта: иначе (и без строки профиля) — false без записи.
// Выключение проходит всегда; строки профиля нет — false.
func (s *Store) SetInvite(ctx context.Context, tgID int64, on bool, now time.Time) (bool, error) {
	const q = `
		UPDATE user_profiles
		SET notify_changed_at = CASE WHEN notify_opt_in <> $2 THEN $3 ELSE notify_changed_at END,
		    notify_opt_in = $2
		WHERE tg_id = $1 AND (NOT $2 OR (status = 'completed' AND (residence_city_key IS NOT NULL OR sports <> '{}')))`
	cmd, err := s.db.Exec(ctx, q, tgID, on, now)
	if err != nil {
		return false, fmt.Errorf("set invite: %w", err)
	}
	return cmd.RowsAffected() > 0, nil
}

// AnswerInviteAt — ответ на вопрос в конце анкеты (U-6): при версии профиля
// wantV и завершённой анкете приглашения включаются (on) или остаются
// выключенными; profile_v не меняется. Другая версия, другой статус или нет
// строки — ErrStaleProfile без записи.
func (s *Store) AnswerInviteAt(ctx context.Context, tgID int64, wantV int, on bool, now time.Time) error {
	const q = `
		UPDATE user_profiles
		SET notify_changed_at = CASE WHEN notify_opt_in <> $3 THEN $4 ELSE notify_changed_at END,
		    notify_opt_in = $3
		WHERE tg_id = $1 AND profile_v = $2 AND status = 'completed'`
	cmd, err := s.db.Exec(ctx, q, tgID, wantV, on, now)
	if err != nil {
		return fmt.Errorf("answer invite: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrStaleProfile
	}
	return nil
}

// SetAnnounceOff ставит или снимает отказ от объявлений; profile_v не
// меняется.
//
// Упрощение: у пользователя без строки профиля (не нажимал /start, например
// пришёл по ссылке на старт) строка создаётся здесь, и предложение анкеты на
// его первом /start не появится; потолок — отдельная таблица отказов, если
// таких пользователей станет заметно.
func (s *Store) SetAnnounceOff(ctx context.Context, tgID int64, off bool, now time.Time) error {
	const q = `
		INSERT INTO user_profiles (tg_id, announce_opt_out, announce_changed_at) VALUES ($1, $2, $3)
		ON CONFLICT (tg_id) DO UPDATE
		SET announce_changed_at = CASE WHEN user_profiles.announce_opt_out <> $2 THEN $3 ELSE user_profiles.announce_changed_at END,
		    announce_opt_out = $2`
	if _, err := s.db.Exec(ctx, q, tgID, off, now); err != nil {
		return fmt.Errorf("set announce: %w", err)
	}
	return nil
}

// DropAwaiting снимает все отметки «жду текст» пользователя: город проживания,
// обращение в поддержку и ссылку на старт. Вызывается, когда текста ждёт
// мастер рассылки (режимы ввода взаимоисключающие).
func (s *Store) DropAwaiting(ctx context.Context, tgID int64) error {
	return s.InTx(ctx, func(tx *Store) error {
		const clearCity = `
			UPDATE user_profiles SET awaiting_city_until = NULL, awaiting_city_mode = NULL
			WHERE tg_id = $1 AND awaiting_city_until IS NOT NULL`
		if _, err := tx.db.Exec(ctx, clearCity, tgID); err != nil {
			return fmt.Errorf("drop awaiting: clear city: %w", err)
		}
		return tx.dropAwaiting(ctx, tgID, "drop awaiting")
	})
}

// Awaiting — стоит ли у пользователя действующая на момент now отметка «жду
// текст» (город, обращение или ссылка). Только читает.
func (s *Store) Awaiting(ctx context.Context, tgID int64, now time.Time) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM user_profiles WHERE tg_id = $1 AND awaiting_city_until > $2)
		OR EXISTS (SELECT 1 FROM support_awaiting WHERE tg_id = $1 AND expires_at > $2)
		OR EXISTS (SELECT 1 FROM suggest_awaiting WHERE tg_id = $1 AND expires_at > $2)`
	var ok bool
	if err := s.db.QueryRow(ctx, q, tgID, now).Scan(&ok); err != nil {
		return false, fmt.Errorf("awaiting: %w", err)
	}
	return ok, nil
}
