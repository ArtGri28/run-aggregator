package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// listBase — общий отбор выдачи без отбора по включённым видам спорта (его
// добавляет listWhere). Предикат is_listed / merged_into_id
// записан так же, как в частичных индексах events, чтобы планировщик их
// использовал. Параметры: $1 from, $2 to, $3 city_key (пустая строка — любой),
// $4 event_type (пустая строка — любой), $5 lo и $6 hi — пары диапазонов дистанций
// (пусто — любая дистанция), $7 country_code (пустая строка — любая страна),
// $8 начало и $9 конец периодов дат (пусто — без ограничения периодами),
// $10 выбранный вид спорта (пустая строка — любой из включённых).
// Оканчивается sportVisible: выбранный вид отбирает поверх включённых.
const listBase = `
	WHERE e.is_listed AND e.merged_into_id IS NULL
	  AND e.start_date >= $1::date AND e.start_date <= $2::date
	  AND ($3::text = '' OR e.city_key = $3::text)
	  AND ($4::text = '' OR e.event_type = $4::text)
	  AND (COALESCE(cardinality($5::int[]), 0) = 0 OR EXISTS (
	        SELECT 1 FROM event_distances d, unnest($5::int[], $6::int[]) AS r(lo, hi)
	        WHERE d.event_id = e.id AND d.distance_m > r.lo AND d.distance_m <= r.hi))
	  AND ($7::text = '' OR e.country_code = $7::text)
	  AND (COALESCE(cardinality($8::date[]), 0) = 0 OR EXISTS (
	        SELECT 1 FROM unnest($8::date[], $9::date[]) AS p(lo, hi)
	        WHERE e.start_date >= p.lo AND e.start_date <= p.hi))
	  AND ($10::text = '' OR e.sport = $10::text)`

func listWhere() string { return listBase + sportVisible() }

// sportVisible — отбор по включённым видам спорта (D-08): один фрагмент на
// все запросы выдачи, он стоит в конце listWhere и futureListed. Коды — только
// константы справочника event.Sports, их формат закреплён тестом; ввод
// пользователя сюда не попадает. Параметров фрагмент не добавляет. Справочник
// читается при каждом запросе: признак «включён» действует сразу. Ни одного
// включённого вида спорта — условие, которому не отвечает ни одно событие
// (пустой перечень IN () — ошибка SQL).
func sportVisible() string {
	var codes []string
	for _, sp := range event.Sports {
		if sp.Enabled {
			codes = append(codes, "'"+string(sp.Code)+"'")
		}
	}
	if len(codes) == 0 {
		return " AND false"
	}
	return " AND e.sport IN (" + strings.Join(codes, ", ") + ")"
}

// ListItem — строка списка выдачи.
type ListItem struct {
	ID          int64
	Title       string
	StartDate   time.Time
	CountryCode string
	City        string
	Status      event.Status
	Sport       event.Sport
	// Distances — метры по возрастанию.
	Distances []int
}

// noUpperBound — верхняя граница дат для нулевого Filter.To.
var noUpperBound = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// filterArgs строит аргументы $1…$10 listWhere. Срезы диапазонов всегда
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
	plo, phi := []time.Time{}, []time.Time{}
	for _, p := range f.Periods {
		plo, phi = append(plo, p.From), append(phi, p.To)
	}
	return []any{f.From, to, f.CityKey, string(f.Type), lo, hi, f.CountryCode, plo, phi, string(f.Sport)}
}

// ListEvents возвращает страницу выдачи по фильтру в порядке start_date, id.
func (s *Store) ListEvents(ctx context.Context, f event.Filter, limit, offset int) ([]ListItem, error) {
	q := `
		SELECT e.id, e.title, e.start_date, e.country_code, COALESCE(e.city, ''), e.status, e.sport,
		       COALESCE((SELECT array_agg(d.distance_m ORDER BY d.distance_m)
		                 FROM event_distances d WHERE d.event_id = e.id), '{}')
		FROM events e` + listWhere() + `
		ORDER BY e.start_date, e.id
		LIMIT $11 OFFSET $12`
	rows, err := s.db.Query(ctx, q, append(filterArgs(f), limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ListItem, error) {
		var it ListItem
		var status string
		var dist []int32
		if err := row.Scan(&it.ID, &it.Title, &it.StartDate, &it.CountryCode, &it.City, &status, &it.Sport, &dist); err != nil {
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

// SearchCandidates — видимые события страны country (пусто — все страны) с
// датой не раньше from, в порядке start_date, id, не больше limit: каталог,
// по которому бот ищет текстовый запрос. Сравнение слов делает бот: так оно
// не зависит от локали базы. Дистанции не заполняются. Только читает.
func (s *Store) SearchCandidates(ctx context.Context, country string, from time.Time, limit int) ([]ListItem, error) {
	q := `
		SELECT e.id, e.title, e.start_date, e.country_code, COALESCE(e.city, ''), e.status, e.sport
		FROM events e` + futureListed() + `
		  AND ($2::text = '' OR e.country_code = $2::text)
		ORDER BY e.start_date, e.id
		LIMIT $3`
	rows, err := s.db.Query(ctx, q, from, country, limit)
	if err != nil {
		return nil, fmt.Errorf("search candidates: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ListItem, error) {
		var it ListItem
		var status string
		if err := row.Scan(&it.ID, &it.Title, &it.StartDate, &it.CountryCode, &it.City, &status, &it.Sport); err != nil {
			return ListItem{}, err
		}
		it.Status = event.Status(status)
		return it, nil
	})
	if err != nil {
		return nil, fmt.Errorf("search candidates: %w", err)
	}
	return items, nil
}

// CountEvents возвращает число событий выдачи по фильтру.
func (s *Store) CountEvents(ctx context.Context, f event.Filter) (int, error) {
	q := `SELECT count(*) FROM events e` + listWhere()
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
