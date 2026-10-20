package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// Подписка на новые старты (Phase 20, 20-CONTEXT.md): одна на пользователя,
// копия фильтров поиска без дат, время включения и время последнего сообщения.

// OutboxDigest — вид строки очереди: сообщение подписки (N-5). У строки нет
// события и содержимого: список собирается при отправке.
const OutboxDigest = "digest"

// Subscribe включает подписку с копией фильтров filters либо заменяет копию
// у включённой. Новые старты считаются с момента now.
func (s *Store) Subscribe(ctx context.Context, tgID int64, filters []byte, now time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO subscriptions (tg_id, filters, created_at) VALUES ($1, $2, $3)
		ON CONFLICT (tg_id) DO UPDATE SET filters = EXCLUDED.filters, created_at = EXCLUDED.created_at, sent_at = NULL`,
		tgID, filters, now)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	return nil
}

// Unsubscribe удаляет подписку; её не было — без ошибки.
func (s *Store) Unsubscribe(ctx context.Context, tgID int64) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM subscriptions WHERE tg_id = $1`, tgID); err != nil {
		return fmt.Errorf("unsubscribe: %w", err)
	}
	return nil
}

// Subscription — копия фильтров и момент, после которого старты новые
// (последнее сообщение либо включение). ok = false — подписки нет.
func (s *Store) Subscription(ctx context.Context, tgID int64) (filters []byte, since time.Time, ok bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT filters, COALESCE(sent_at, created_at) FROM subscriptions WHERE tg_id = $1`, tgID).
		Scan(&filters, &since)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, fmt.Errorf("subscription: %w", err)
	}
	return filters, since, true, nil
}

// MarkDigestSent запоминает момент отправленного сообщения подписки.
func (s *Store) MarkDigestSent(ctx context.Context, tgID int64, at time.Time) error {
	if _, err := s.db.Exec(ctx, `UPDATE subscriptions SET sent_at = $2 WHERE tg_id = $1`, tgID, at); err != nil {
		return fmt.Errorf("mark digest sent: %w", err)
	}
	return nil
}

// EnqueueDigests записывает по строке вида digest каждой подписке, включённой
// раньше moment. Ключ повтора — week: на неделю одна строка. Возвращает число
// созданных строк.
func (s *Store) EnqueueDigests(ctx context.Context, week string, moment, now time.Time) (int, error) {
	cmd, err := s.db.Exec(ctx, `
		INSERT INTO outbox (tg_id, kind, dedup_key, created_at)
		SELECT tg_id, $1::text, $1::text || ':' || $2::text, $4::timestamptz
		FROM subscriptions WHERE created_at < $3::timestamptz
		ORDER BY tg_id
		ON CONFLICT (tg_id, dedup_key) DO NOTHING`, OutboxDigest, week, moment, now)
	if err != nil {
		return 0, fmt.Errorf("enqueue digests: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// NewEvents — выдача по фильтру f из событий, появившихся в каталоге после
// since и не позже until: первые limit в порядке выдачи и их общее число.
// Только читает.
func (s *Store) NewEvents(ctx context.Context, f event.Filter, since, until time.Time, limit int) ([]ListItem, int, error) {
	q := `
		SELECT e.id, e.title, e.start_date, e.country_code, COALESCE(e.city, ''), e.status, e.sport,
		       count(*) OVER ()
		FROM events e` + listWhere() + `
		  AND e.created_at > $11 AND e.created_at <= $12
		ORDER BY e.start_date, e.id
		LIMIT $13`
	rows, err := s.db.Query(ctx, q, append(filterArgs(f), since, until, limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("new events: %w", err)
	}
	total := 0
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ListItem, error) {
		var it ListItem
		var status string
		err := row.Scan(&it.ID, &it.Title, &it.StartDate, &it.CountryCode, &it.City, &status, &it.Sport, &total)
		it.Status = event.Status(status)
		return it, err
	})
	if err != nil {
		return nil, 0, fmt.Errorf("new events: %w", err)
	}
	return items, total, nil
}
