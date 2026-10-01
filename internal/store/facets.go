package store

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// CityCount — город каталога и число его событий в выдаче.
type CityCount struct {
	Key, Name string
	N         int
}

// CountryCount — страна каталога (ISO alpha-2) и число её событий в выдаче.
type CountryCount struct {
	Code string
	N    int
}

// Город — это пара (страна, city_key): одинаковый city_key в разных странах
// — разные города. Функции ниже принимают country (ISO alpha-2); пустая
// строка — без ограничения по стране, ключи разных стран тогда объединяются.

// futureListed — видимые события с датой не раньше $1.
const futureListed = `
	WHERE e.is_listed AND e.merged_into_id IS NULL AND e.start_date >= $1::date`

// similarRE — алфавит и длина ключа города для поиска по началу слова.
// Метасимволов LIKE (% и _) в нём нет.
var similarRE = regexp.MustCompile(`^[a-z0-9-]{2,64}$`)

func scanCities(rows pgx.Rows) ([]CityCount, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CityCount, error) {
		var c CityCount
		err := row.Scan(&c.Key, &c.Name, &c.N)
		return c, err
	})
}

// TopCities возвращает города с наибольшим числом событий выдачи по
// фильтру; сам фильтр города не учитывается, чтобы кнопки города не вели
// в пустую выдачу при выбранных дате, типе и дистанции. Name — самое
// частое написание города.
func (s *Store) TopCities(ctx context.Context, f event.Filter, limit int) ([]CityCount, error) {
	const q = `
		SELECT e.city_key, COALESCE(mode() WITHIN GROUP (ORDER BY e.city), e.city_key), count(*)
		FROM events e` + listWhere + `
		  AND e.city_key IS NOT NULL
		GROUP BY e.city_key
		ORDER BY count(*) DESC, e.city_key
		LIMIT $8`
	f.CityKey = ""
	rows, err := s.db.Query(ctx, q, append(filterArgs(f), limit)...)
	if err != nil {
		return nil, fmt.Errorf("top cities: %w", err)
	}
	out, err := scanCities(rows)
	if err != nil {
		return nil, fmt.Errorf("top cities: %w", err)
	}
	return out, nil
}

// SimilarCities возвращает города, ключ которых начинается с prefix или
// содержит его в начале любого слова. prefix вне алфавита [a-z0-9-] или
// короче двух символов — пустой результат без запроса.
func (s *Store) SimilarCities(ctx context.Context, country, prefix string, from time.Time, limit int) ([]CityCount, error) {
	if !similarRE.MatchString(prefix) {
		return nil, nil
	}
	const q = `
		SELECT e.city_key, COALESCE(mode() WITHIN GROUP (ORDER BY e.city), e.city_key), count(*)
		FROM events e` + futureListed + `
		  AND e.city_key IS NOT NULL
		  AND (e.city_key LIKE $2::text || '%' OR e.city_key LIKE '%-' || $2::text || '%')
		  AND ($4::text = '' OR e.country_code = $4::text)
		GROUP BY e.city_key
		ORDER BY count(*) DESC, e.city_key
		LIMIT $3`
	rows, err := s.db.Query(ctx, q, from, prefix, limit, country)
	if err != nil {
		return nil, fmt.Errorf("similar cities: %w", err)
	}
	out, err := scanCities(rows)
	if err != nil {
		return nil, fmt.Errorf("similar cities: %w", err)
	}
	return out, nil
}

// CityName возвращает написание города по ключу и число его событий
// с датой не раньше from; n = 0 — города в выдаче нет (name — сам ключ).
func (s *Store) CityName(ctx context.Context, country, key string, from time.Time) (name string, n int, err error) {
	const q = `
		SELECT COALESCE(mode() WITHIN GROUP (ORDER BY e.city), $2::text), count(*)
		FROM events e` + futureListed + ` AND e.city_key = $2::text
		  AND ($3::text = '' OR e.country_code = $3::text)`
	if err := s.db.QueryRow(ctx, q, from, key, country).Scan(&name, &n); err != nil {
		return "", 0, fmt.Errorf("city name: %w", err)
	}
	return name, n, nil
}

// EventTypes возвращает отсортированные типы видимых событий страны country
// с датой не раньше from.
func (s *Store) EventTypes(ctx context.Context, country string, from time.Time) ([]event.Type, error) {
	const q = `SELECT DISTINCT e.event_type FROM events e` + futureListed + `
		  AND ($2::text = '' OR e.country_code = $2::text) ORDER BY 1`
	rows, err := s.db.Query(ctx, q, from, country)
	if err != nil {
		return nil, fmt.Errorf("event types: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (event.Type, error) {
		var t string
		err := row.Scan(&t)
		return event.Type(t), err
	})
	if err != nil {
		return nil, fmt.Errorf("event types: %w", err)
	}
	return out, nil
}

// Countries возвращает страны выдачи по фильтру с числом событий: по убыванию
// числа, затем по коду. Сам фильтр страны не учитывается, чтобы кнопки
// страны не вели в пустую выдачу при выбранных дате, типе и дистанции.
func (s *Store) Countries(ctx context.Context, f event.Filter) ([]CountryCount, error) {
	const q = `
		SELECT e.country_code, count(*)
		FROM events e` + listWhere + `
		GROUP BY e.country_code
		ORDER BY count(*) DESC, e.country_code`
	f.CountryCode = ""
	rows, err := s.db.Query(ctx, q, filterArgs(f)...)
	if err != nil {
		return nil, fmt.Errorf("countries: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CountryCount, error) {
		var c CountryCount
		err := row.Scan(&c.Code, &c.N)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("countries: %w", err)
	}
	return out, nil
}
