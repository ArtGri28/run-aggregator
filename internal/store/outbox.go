package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// Очередь исходящих сообщений (Phase 14, таблица outbox). Строки об
// изменениях и о пропавших стартах пишет применение прогона источника в своей
// транзакции; отправляет только бот. Текста сообщения в строке нет — только
// что изменилось у старта (event.Change).

// WatchStates — состояние всех будущих видимых корневых стартов включённых
// видов спорта на дату today (futureListed): снимок, который прогон берёт до
// применения и после наблюдения. Только читает.
//
// Упрощение: один запрос по всем будущим стартам каталога (сейчас сотни
// строк), а не по событиям прогона — так предохранитель видит весь каталог.
// При десятках тысяч стартов — сравнивать только события, затронутые прогоном.
func (s *Store) WatchStates(ctx context.Context, today time.Time) (map[int64]event.WatchState, error) {
	q := `
		SELECT e.id, e.title, e.start_date, e.status, COALESCE(e.registration_url, '')
		FROM events e` + futureListed()
	rows, err := s.db.Query(ctx, q, dateOnly(today))
	if err != nil {
		return nil, fmt.Errorf("watch states: %w", err)
	}
	out := map[int64]event.WatchState{}
	var id int64
	var st event.WatchState
	var status string
	_, err = pgx.ForEachRow(rows, []any{&id, &st.Title, &st.StartDate, &status, &st.URL}, func() error {
		st.Status = event.Status(status)
		out[id] = st
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("watch states: %w", err)
	}
	return out, nil
}

// HiddenEvents — какие из событий ids скрыты из выдачи и не слиты
// (is_listed = false без merged_into_id), по возрастанию id. Слитое событие
// тоже не показывается, но скрытым не считается: оно ведёт к корню. Только
// читает; пустой ids — пустой ответ без запроса.
func (s *Store) HiddenEvents(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const q = `SELECT id FROM events WHERE id = ANY($1) AND NOT is_listed AND merged_into_id IS NULL ORDER BY id`
	rows, err := s.db.Query(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("hidden events: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("hidden events: %w", err)
	}
	return out, nil
}

// EnqueueChange записывает изменение c корневого старта, найденное прогоном
// runID, в очередь: по одной строке каждому пользователю, чьё избранное ведёт
// к этому старту — в том числе через слитое событие (favRoot); пользователь с
// двумя такими записями получает одну строку. Вид строки — c.Kind(), ключ
// повтора — «вид:прогон:событие», содержимое — c.Payload(), время — now.
// Повтор той же пары «пользователь — ключ» строки не создаёт. Возвращает
// число созданных строк.
//
// Упрощение: получатели определяются в момент записи. Пользователь,
// добавивший старт в избранное между этой записью и фиксацией транзакции
// прогона, сообщения об этом изменении не получит; потолок — проверка при
// добавлении в избранное.
func (s *Store) EnqueueChange(ctx context.Context, runID int64, c event.Change, now time.Time) (int, error) {
	const q = `
		INSERT INTO outbox (tg_id, kind, event_id, dedup_key, payload, created_at)
		SELECT DISTINCT f.tg_id, $1::text, $2::bigint, $3::text, $4::jsonb, $5::timestamptz
		FROM favorites f JOIN events fe ON fe.id = f.event_id
		WHERE ` + favRoot + ` = $2::bigint
		ON CONFLICT (tg_id, dedup_key) DO NOTHING`
	kind := c.Kind()
	key := fmt.Sprintf("%s:%d:%d", kind, runID, c.EventID)
	cmd, err := s.db.Exec(ctx, q, kind, c.EventID, key, c.Payload(), now)
	if err != nil {
		return 0, fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return int(cmd.RowsAffected()), nil
}

// OutboxRow — строка очереди, взятая в отправку. EventID = 0 — строка без
// события.
type OutboxRow struct {
	ID        int64
	TgID      int64
	Kind      string
	EventID   int64
	Payload   []byte
	CreatedAt time.Time
}

// Исходы строки, закрытой без доставки (колонка outcome).
const (
	OutboxBlocked     = "blocked"     // Telegram ответил 403
	OutboxRejected    = "rejected"    // Telegram ответил 400 либо строку нельзя прочитать
	OutboxExpired     = "expired"     // сообщение старше срока годности
	OutboxStale       = "stale"       // условие сообщения больше не выполняется
	OutboxUnfollowed  = "unfollowed"  // старт убран из избранного
	OutboxUnavailable = "unavailable" // старт недоступен: скрыт, слит, прошёл, вид спорта выключен
)

// ClaimOutbox берёт в отправку самую старую ожидающую строку: одной записью
// выбирает строку без отметок взятия и закрытия в порядке id (заблокированные
// другими транзакциями пропускает) и ставит отметку взятия now. Запись
// фиксируется сама, до вызова Bot API: взятая строка повторно не берётся
// никогда — этим держится гарантия «не более одного раза». Нет ожидающих
// строк — false.
func (s *Store) ClaimOutbox(ctx context.Context, now time.Time) (OutboxRow, bool, error) {
	const q = `
		UPDATE outbox SET claimed_at = $1
		WHERE id = (SELECT id FROM outbox WHERE claimed_at IS NULL AND done_at IS NULL
		            ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, tg_id, kind, COALESCE(event_id, 0), payload, created_at`
	var r OutboxRow
	err := s.db.QueryRow(ctx, q, now).Scan(&r.ID, &r.TgID, &r.Kind, &r.EventID, &r.Payload, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboxRow{}, false, nil
	}
	if err != nil {
		return OutboxRow{}, false, fmt.Errorf("claim outbox: %w", err)
	}
	return r, true, nil
}

// MarkOutboxSent отмечает взятую строку отправленной: Telegram принял
// сообщение.
func (s *Store) MarkOutboxSent(ctx context.Context, id int64, now time.Time) error {
	const q = `UPDATE outbox SET sent_at = $2 WHERE id = $1 AND claimed_at IS NOT NULL AND sent_at IS NULL AND done_at IS NULL`
	if _, err := s.db.Exec(ctx, q, id, now); err != nil {
		return fmt.Errorf("mark outbox sent: %w", err)
	}
	return nil
}

// CloseOutbox закрывает строку без доставки с исходом outcome (формат колонки:
// строчные буквы и «_»). Отправленную и уже закрытую строку не трогает.
func (s *Store) CloseOutbox(ctx context.Context, id int64, outcome string, now time.Time) error {
	const q = `UPDATE outbox SET done_at = $2, outcome = $3 WHERE id = $1 AND sent_at IS NULL AND done_at IS NULL`
	if _, err := s.db.Exec(ctx, q, id, now, outcome); err != nil {
		return fmt.Errorf("close outbox: %w", err)
	}
	return nil
}

// ReleaseOutbox снимает отметку взятия: строка снова ожидает. Вызывается
// только когда сообщение заведомо не отправлялось — после ответа 429 и после
// сбоя до вызова Bot API. Отправленную и закрытую строку не трогает.
func (s *Store) ReleaseOutbox(ctx context.Context, id int64) error {
	const q = `UPDATE outbox SET claimed_at = NULL WHERE id = $1 AND claimed_at IS NOT NULL AND sent_at IS NULL AND done_at IS NULL`
	if _, err := s.db.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("release outbox: %w", err)
	}
	return nil
}

// CloseUserOutbox одной записью закрывает ожидающие строки пользователя и
// строку also (взятая в отправку, получившая 403) с исходом outcome; строки
// других пользователей и другие взятые строки не трогает. Возвращает число
// закрытых строк.
func (s *Store) CloseUserOutbox(ctx context.Context, tgID, also int64, outcome string, now time.Time) (int, error) {
	const q = `
		UPDATE outbox SET done_at = $3, outcome = $4
		WHERE tg_id = $1 AND sent_at IS NULL AND done_at IS NULL AND (claimed_at IS NULL OR id = $2)`
	cmd, err := s.db.Exec(ctx, q, tgID, also, now, outcome)
	if err != nil {
		return 0, fmt.Errorf("close user outbox: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// ExpireOutbox закрывает с исходом «expired» ожидающие строки видов kinds,
// созданные раньше before. Строки других видов и взятые строки не трогает.
// Возвращает число закрытых строк.
func (s *Store) ExpireOutbox(ctx context.Context, kinds []string, before, now time.Time) (int, error) {
	const q = `
		UPDATE outbox SET done_at = $3, outcome = $4
		WHERE claimed_at IS NULL AND done_at IS NULL AND kind = ANY($1) AND created_at < $2`
	cmd, err := s.db.Exec(ctx, q, kinds, before, now, OutboxExpired)
	if err != nil {
		return 0, fmt.Errorf("expire outbox: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// PruneOutbox удаляет строки, созданные раньше before, в любом состоянии:
// отправленные, закрытые и оставшиеся с неизвестным исходом. Возвращает число
// удалённых строк.
func (s *Store) PruneOutbox(ctx context.Context, before time.Time) (int, error) {
	cmd, err := s.db.Exec(ctx, `DELETE FROM outbox WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune outbox: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}
