package store

import (
	"context"
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
