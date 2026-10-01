package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// listWhere — общий отбор выдачи. Предикат is_listed / merged_into_id
// записан так же, как в частичных индексах events, чтобы планировщик их
// использовал. Параметры: $1 from, $2 to, $3 city_key (пустая строка — любой),
// $4 event_type (пустая строка — любой), $5 lo и $6 hi — пары диапазонов дистанций
// (пусто — любая дистанция).
const listWhere = `
	WHERE e.is_listed AND e.merged_into_id IS NULL
	  AND e.start_date >= $1::date AND e.start_date <= $2::date
	  AND ($3::text = '' OR e.city_key = $3::text)
	  AND ($4::text = '' OR e.event_type = $4::text)
	  AND (COALESCE(cardinality($5::int[]), 0) = 0 OR EXISTS (
	        SELECT 1 FROM event_distances d, unnest($5::int[], $6::int[]) AS r(lo, hi)
	        WHERE d.event_id = e.id AND d.distance_m > r.lo AND d.distance_m <= r.hi))`

// ListItem — строка списка выдачи.
type ListItem struct {
	ID        int64
	Title     string
	StartDate time.Time
	City      string
	Status    event.Status
	// Distances — метры по возрастанию.
	Distances []int
}

// noUpperBound — верхняя граница дат для нулевого Filter.To.
var noUpperBound = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// filterArgs строит аргументы $1…$6 listWhere. Срезы диапазонов всегда
// непустые по типу ([]int32{}, не nil): pgx кодирует nil как NULL.
func filterArgs(f event.Filter) []any {
	to := f.To
	if to.IsZero() {
		to = noUpperBound
	}
	lo, hi := []int32{}, []int32{}
	for _, r := range f.Distances {
		lo, hi = append(lo, int32(r.Lo)), append(hi, int32(r.Hi))
	}
	return []any{f.From, to, f.CityKey, string(f.Type), lo, hi}
}

// ListEvents возвращает страницу выдачи по фильтру в порядке start_date, id.
func (s *Store) ListEvents(ctx context.Context, f event.Filter, limit, offset int) ([]ListItem, error) {
	const q = `
		SELECT e.id, e.title, e.start_date, COALESCE(e.city, ''), e.status,
		       COALESCE((SELECT array_agg(d.distance_m ORDER BY d.distance_m)
		                 FROM event_distances d WHERE d.event_id = e.id), '{}')
		FROM events e` + listWhere + `
		ORDER BY e.start_date, e.id
		LIMIT $7 OFFSET $8`
	rows, err := s.db.Query(ctx, q, append(filterArgs(f), limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ListItem, error) {
		var it ListItem
		var status string
		var dist []int32
		if err := row.Scan(&it.ID, &it.Title, &it.StartDate, &it.City, &status, &dist); err != nil {
			return ListItem{}, err
		}
		it.Status = event.Status(status)
		for _, m := range dist {
			it.Distances = append(it.Distances, int(m))
		}
		return it, nil
	})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return items, nil
}

// CountEvents возвращает число событий выдачи по фильтру.
func (s *Store) CountEvents(ctx context.Context, f event.Filter) (int, error) {
	const q = `SELECT count(*) FROM events e` + listWhere
	var n int
	if err := s.db.QueryRow(ctx, q, filterArgs(f)...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count events: %w", err)
	}
	return n, nil
}

// EventSource — источник события со ссылкой на его страницу.
type EventSource struct{ Code, URL string }

// EventSources возвращает источники события для карточки: без исчезнувших
// (state = 'gone') и вне охвата (match_state = 'out_of_scope'), по коду.
func (s *Store) EventSources(ctx context.Context, eventID int64) ([]EventSource, error) {
	const q = `
		SELECT s.code, se.url
		FROM source_events se JOIN sources s ON s.id = se.source_id
		WHERE se.event_id = $1 AND se.state <> 'gone' AND se.match_state <> 'out_of_scope'
		ORDER BY s.code, se.id`
	rows, err := s.db.Query(ctx, q, eventID)
	if err != nil {
		return nil, fmt.Errorf("event sources: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (EventSource, error) {
		var es EventSource
		return es, row.Scan(&es.Code, &es.URL)
	})
	if err != nil {
		return nil, fmt.Errorf("event sources: %w", err)
	}
	return out, nil
}

// maxMergeHops — предел длины цепочки merged_into_id. Слияние схлопывает
// цепочки до одного шага; предел защищает от цикла (CHECK в БД нет).
const maxMergeHops = 32

// RootEventID — общий резолвер корня слияний (D-17): бот (D-16),
// дедупликация, будущие избранное и подписки. Корень возвращает себя,
// слитое событие — id конца цепочки merged_into_id одним запросом.
// Нет события — ErrNotFound; цикл или цепочка длиннее maxMergeHops — другая
// ошибка. Только читает events.
func (s *Store) RootEventID(ctx context.Context, id int64) (int64, error) {
	const q = `
		WITH RECURSIVE chain (id, next, depth) AS (
			SELECT id, merged_into_id, 0 FROM events WHERE id = $1
			UNION ALL
			SELECT e.id, e.merged_into_id, c.depth + 1
			FROM chain c JOIN events e ON e.id = c.next
			WHERE c.depth < $2
		)
		SELECT id, next IS NULL FROM chain ORDER BY depth DESC LIMIT 1`
	var root int64
	var isRoot bool
	err := s.db.QueryRow(ctx, q, id, maxMergeHops).Scan(&root, &isRoot)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, fmt.Errorf("root event %d: %w", id, ErrNotFound)
	case err != nil:
		return 0, fmt.Errorf("root event %d: %w", id, err)
	case !isRoot:
		return 0, fmt.Errorf("root event %d: merge chain longer than %d or cyclic", id, maxMergeHops)
	}
	return root, nil
}
