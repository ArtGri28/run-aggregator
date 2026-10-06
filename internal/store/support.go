package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SupportOutcome — исход попытки принять сообщение как обращение в поддержку.
type SupportOutcome int

const (
	// SupportTaken — отметка действовала и потреблена, лимит не исчерпан:
	// сообщение пересылается в группу.
	SupportTaken SupportOutcome = iota
	// SupportNotWaiting — отметки нет либо она истекла: сообщение не обращение.
	SupportNotWaiting
	// SupportLimited — отметка потреблена, но лимит обращений за окно исчерпан.
	SupportLimited
)

// Хранилище поддержки (D-13 Phase 11) знает только идентификаторы и время:
// текст обращения, текст ответа, вложения, имя и username ему не передаются
// вовсе. У таблиц нет внешнего ключа на users — поддержка доступна и тому, у
// кого нет строк users и user_profiles, и ни один из методов ниже не создаёт
// их: по факту создания строки профиля StartUser решает, показывать ли анкету.
// Сколько обращений допустимо и какого размера окно, хранилище не знает —
// эти значения передаёт бот.

// SupportBegin ставит отметку «жду обращение» до expiresAt (повторный вызов
// заменяет момент) и снимает отметку ожидания города (D-10): два режима ввода
// не действуют одновременно. profile_v и updated_at не меняются; нет строки
// профиля — это не ошибка. Порядок блокировок (профиль, затем отметка
// поддержки) тот же, что у StartUser и UpdateProfileAt.
func (s *Store) SupportBegin(ctx context.Context, tgID int64, expiresAt time.Time) error {
	return s.InTx(ctx, func(tx *Store) error {
		const clearCity = `
			UPDATE user_profiles SET awaiting_city_until = NULL, awaiting_city_mode = NULL
			WHERE tg_id = $1 AND awaiting_city_until IS NOT NULL`
		if _, err := tx.db.Exec(ctx, clearCity, tgID); err != nil {
			return fmt.Errorf("support begin: clear city: %w", err)
		}
		const upsert = `
			INSERT INTO support_awaiting (tg_id, expires_at) VALUES ($1, $2)
			ON CONFLICT (tg_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`
		if _, err := tx.db.Exec(ctx, upsert, tgID, expiresAt); err != nil {
			return fmt.Errorf("support begin: %w", err)
		}
		return nil
	})
}

// SupportCancel снимает отметку; отсутствие отметки не ошибка.
func (s *Store) SupportCancel(ctx context.Context, tgID int64) error {
	return s.dropSupportAwaiting(ctx, tgID, "support cancel")
}

// SupportAwaiting читает отметку без записи. Истёкшая отметка возвращается
// как есть (ok = true): сравнить момент с часами — дело вызывающего, решает
// же SupportTake.
func (s *Store) SupportAwaiting(ctx context.Context, tgID int64) (expiresAt time.Time, ok bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT expires_at FROM support_awaiting WHERE tg_id = $1`, tgID).Scan(&expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("support awaiting: %w", err)
	}
	return expiresAt, true, nil
}

// SupportTake принимает сообщение пользователя как обращение. Отметка
// читается под блокировкой строки, а срок, лимит и удаление отметки идут в
// той же транзакции: из двух одновременных сообщений одного пользователя
// обращением становится ровно одно (второе ждёт блокировку и находит строку
// уже удалённой). Отметка потребляется в любом исходе, кроме SupportNotWaiting
// при её отсутствии; истёкшая отметка удаляется. Лимит — число связок автора
// с created_at позже since: отдельного счётчика нет (D-12, D-14); связки
// старше окна в счёт не идут, так что обращение снова проходит по его истечении.
// Упрощение: между проверкой лимита и SupportLink окно не закрыто — связка
// появляется только после ответа Telegram, поэтому при быстрых повторных
// входах возможен перебор на число одновременно летящих отправок; чинить —
// резервировать строку до отправки и удалять её при сбое.
func (s *Store) SupportTake(ctx context.Context, tgID int64, now, since time.Time, limit int) (SupportOutcome, error) {
	out := SupportNotWaiting
	err := s.InTx(ctx, func(tx *Store) error {
		var until time.Time
		err := tx.db.QueryRow(ctx, `SELECT expires_at FROM support_awaiting WHERE tg_id = $1 FOR UPDATE`, tgID).Scan(&until)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("support take: lock: %w", err)
		}
		if _, err := tx.db.Exec(ctx, `DELETE FROM support_awaiting WHERE tg_id = $1`, tgID); err != nil {
			return fmt.Errorf("support take: consume: %w", err)
		}
		if !until.After(now) {
			return nil
		}
		var n int
		if err := tx.db.QueryRow(ctx,
			`SELECT count(*) FROM support_messages WHERE tg_id = $1 AND created_at > $2`, tgID, since).Scan(&n); err != nil {
			return fmt.Errorf("support take: limit: %w", err)
		}
		if n >= limit {
			out = SupportLimited
			return nil
		}
		out = SupportTaken
		return nil
	})
	if err != nil {
		return SupportNotWaiting, err
	}
	return out, nil
}

// SupportLink записывает связку «сообщение бота в группе → автор». Конфликт
// по первичному ключу игнорируется: повторная доставка того же апдейта не
// ошибка и строку не меняет.
func (s *Store) SupportLink(ctx context.Context, chatID int64, messageID int, tgID int64, at time.Time) error {
	const q = `
		INSERT INTO support_messages (chat_id, message_id, tg_id, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (chat_id, message_id) DO NOTHING`
	if _, err := s.db.Exec(ctx, q, chatID, messageID, tgID, at); err != nil {
		return fmt.Errorf("support link: %w", err)
	}
	return nil
}

// SupportAuthor находит автора обращения по сообщению бота в группе; для
// другой группы с тем же номером сообщения и для неизвестного сообщения —
// ok = false.
func (s *Store) SupportAuthor(ctx context.Context, chatID int64, messageID int) (tgID int64, ok bool, err error) {
	err = s.db.QueryRow(ctx,
		`SELECT tg_id FROM support_messages WHERE chat_id = $1 AND message_id = $2`, chatID, messageID).Scan(&tgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("support author: %w", err)
	}
	return tgID, true, nil
}
