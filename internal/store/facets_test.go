package store

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"aggregator/internal/event"
)

func TestTopCities(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEvent(t, s, "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeTrail, 21097)
	listEvent(t, s, "2026-10-03", "Мск", "moscow", event.TypeRoad, 42195)
	listEvent(t, s, "2026-10-04", "Казань", "kazan", event.TypeRoad, 10000)
	listEvent(t, s, "2026-10-05", "Казань", "kazan", event.TypeTrail, 10000)
	listEvent(t, s, "2026-10-06", "Тула", "tula", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-07", "Где-то", "", event.TypeRoad, 5000)
	hidden := listEvent(t, s, "2026-10-08", "Тула", "tula", event.TypeRoad, 5000)
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", hidden)

	from := mustDateFixed("2026-10-01")
	keys := func(cc []CityCount) []string {
		out := []string{}
		for _, c := range cc {
			out = append(out, c.Key)
		}
		return out
	}
	cases := []struct {
		name  string
		f     event.Filter
		limit int
		want  []string
	}{
		{"порядок: число по убыванию, затем ключ; без city_key и скрытых", event.Filter{From: from}, 10, []string{"moscow", "kazan", "tula"}},
		{"limit", event.Filter{From: from}, 2, []string{"moscow", "kazan"}},
		{"фильтр города не участвует", event.Filter{From: from, CityKey: "tula"}, 10, []string{"moscow", "kazan", "tula"}},
		{"тип учитывается", event.Filter{From: from, Type: event.TypeTrail}, 10, []string{"kazan", "moscow"}},
		{"даты учитываются", event.Filter{From: mustDateFixed("2026-10-04")}, 10, []string{"kazan", "tula"}},
		{"корзины учитываются", event.Filter{From: from, Distances: []event.DistanceRange{{Lo: 30000, Hi: 43000}}}, 10, []string{"moscow"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.TopCities(ctx, tc.f, tc.limit)
			if err != nil {
				t.Fatalf("TopCities: %v", err)
			}
			if !reflect.DeepEqual(keys(got), tc.want) {
				t.Fatalf("TopCities keys = %v, want %v", keys(got), tc.want)
			}
		})
	}

	got, _ := s.TopCities(ctx, event.Filter{From: from}, 10)
	if want := (CityCount{Key: "moscow", Name: "Москва", N: 3}); got[0] != want {
		t.Fatalf("TopCities[0] = %+v, want %+v (самое частое написание)", got[0], want)
	}
}

func TestSimilarCities(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEvent(t, s, "2026-10-01", "Нижний Новгород", "nizhniy-novgorod", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-02", "Нижний Новгород", "nizhniy-novgorod", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-03", "Великий Новгород", "velikiy-novgorod", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-04", "Новосибирск", "novosibirsk", event.TypeRoad, 5000)
	listEvent(t, s, "2026-09-01", "Новоуральск", "novouralsk", event.TypeRoad, 5000) // в прошлом

	from := mustDateFixed("2026-10-01")
	keys := func(prefix string) []string {
		got, err := s.SimilarCities(ctx, "", prefix, "", from, 5)
		if err != nil {
			t.Fatalf("SimilarCities(%q): %v", prefix, err)
		}
		out := []string{}
		for _, c := range got {
			out = append(out, c.Key)
		}
		return out
	}
	cases := []struct {
		prefix string
		want   []string
	}{
		{"nov", []string{"nizhniy-novgorod", "novosibirsk", "velikiy-novgorod"}}, // число, затем ключ
		{"novgorod", []string{"nizhniy-novgorod", "velikiy-novgorod"}},
		{"gorod", []string{}}, // только начало слова
		{"n", []string{}},     // короче двух символов
		{"a%b", []string{}},   // вне алфавита
		{"no_", []string{}},   // _ — метасимвол LIKE
		{"novou", []string{}}, // событие в прошлом
	}
	for _, tc := range cases {
		if got := keys(tc.prefix); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SimilarCities(%q) = %v, want %v", tc.prefix, got, tc.want)
		}
	}
	got, _ := s.SimilarCities(ctx, "", "nizh", "", from, 5)
	if want := (CityCount{Key: "nizhniy-novgorod", Name: "Нижний Новгород", N: 2}); len(got) != 1 || got[0] != want {
		t.Fatalf("SimilarCities(nizh) = %+v, want [%+v]", got, want)
	}
}

// TestSimilarCitiesByName — Phase 8, контракт (раздел 7, P8): подсказки ещё и
// по началу названия без учёта регистра; ключ-синоним не мешает.
func TestSimilarCitiesByName(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	listEvent(t, s, "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000) // ключ-синоним: префикс mosk не совпадает
	listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-03", "Московский", "moskovskiy", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-04", "Подмосковье", "podmoskove", event.TypeRoad, 5000)
	listEvent(t, s, "2026-09-01", "Мосальск", "mosalsk", event.TypeRoad, 5000) // в прошлом
	from := mustDateFixed("2026-10-01")

	for _, tc := range []struct {
		prefix, name string
		want         []string
	}{
		{"mosk", "", []string{"moskovskiy"}},               // только ключ — как раньше
		{"mosk", "Моск", []string{"moscow", "moskovskiy"}}, // оба правила, без повторов, по числу событий
		{"mosk", "мОСК", []string{"moscow", "moskovskiy"}}, // регистр не важен
		{"", "Моск", []string{"moscow", "moskovskiy"}},     // ключ не разобран — работает название
		{"", "М", []string{}},                              // короче двух символов
		{"m", "М", []string{}},                             //
		{"", "%", []string{}},                              //
		{"", "Мо%", []string{}},                            // % и _ — обычные символы
		{"", "Мо_", []string{}},                            //
		{"", "Мос", []string{"moscow", "moskovskiy"}},      // событие в прошлом не подсказывается
		{"", "осков", []string{}},                          // только начало названия
	} {
		got, err := s.SimilarCities(ctx, "", tc.prefix, tc.name, from, 5)
		if err != nil {
			t.Fatalf("SimilarCities(%q, %q): %v", tc.prefix, tc.name, err)
		}
		keys := []string{}
		for _, c := range got {
			keys = append(keys, c.Key)
		}
		if !reflect.DeepEqual(keys, tc.want) {
			t.Errorf("SimilarCities(%q, %q) = %v, want %v", tc.prefix, tc.name, keys, tc.want)
		}
	}
	if got, _ := s.SimilarCities(ctx, "", "", "Моск", from, 1); len(got) != 1 || got[0] != (CityCount{Key: "moscow", Name: "Москва", N: 2}) {
		t.Errorf("limit 1 = %+v", got)
	}
}

func TestCityName(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEvent(t, s, "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-09-01", "Тула", "tula", event.TypeRoad, 5000) // в прошлом

	from := mustDateFixed("2026-10-01")
	if name, n, err := s.CityName(ctx, "", "moscow", from); err != nil || name != "Москва" || n != 2 {
		t.Fatalf("CityName(moscow) = %q, %d, %v; want Москва, 2", name, n, err)
	}
	if _, n, err := s.CityName(ctx, "", "tula", from); err != nil || n != 0 {
		t.Fatalf("CityName(tula) n = %d, %v; want 0", n, err)
	}
	if _, n, err := s.CityName(ctx, "", "nowhere", from); err != nil || n != 0 {
		t.Fatalf("CityName(nowhere) n = %d, %v; want 0", n, err)
	}
}

func TestEventTypes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEvent(t, s, "2026-10-01", "Москва", "moscow", event.TypeTrail, 5000)
	listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-03", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-09-01", "Москва", "moscow", event.TypeCross, 5000) // в прошлом
	hidden := listEvent(t, s, "2026-10-04", "Москва", "moscow", event.TypeTrack, 5000)
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", hidden)

	got, err := s.EventTypes(ctx, "", mustDateFixed("2026-10-01"))
	if err != nil {
		t.Fatalf("EventTypes: %v", err)
	}
	if want := []event.Type{event.TypeRoad, event.TypeTrail}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EventTypes = %v, want %v", got, want)
	}
}

// Города по странам: одинаковый city_key в двух странах — два города.
func TestCitiesByCountry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEventIn(t, s, "RU", "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000)
	listEventIn(t, s, "RU", "2026-10-02", "Любань", "lyuban", event.TypeTrail, 5000)
	listEventIn(t, s, "BY", "2026-10-03", "Минск", "minsk", event.TypeRoad, 5000)
	listEventIn(t, s, "BY", "2026-10-04", "Минск", "minsk", event.TypeRoad, 5000)
	listEventIn(t, s, "BY", "2026-10-05", "Любань", "lyuban", event.TypeTrail, 5000)
	listEventIn(t, s, "BY", "2026-10-06", "Любань", "lyuban", event.TypeCross, 5000)
	listEventIn(t, s, "BY", "2026-10-07", "Любань", "lyuban", event.TypeCross, 5000)

	from := mustDateFixed("2026-10-01")
	keys := func(cc []CityCount) []string {
		out := []string{}
		for _, c := range cc {
			out = append(out, c.Key)
		}
		return out
	}

	t.Run("TopCities: город только своей страны", func(t *testing.T) {
		for _, tc := range []struct {
			country string
			want    []CityCount
		}{
			{"RU", []CityCount{{"lyuban", "Любань", 1}, {"moscow", "Москва", 1}}},
			{"BY", []CityCount{{"lyuban", "Любань", 3}, {"minsk", "Минск", 2}}},
			{"PL", []CityCount{}},
		} {
			got, err := s.TopCities(ctx, event.Filter{From: from, CountryCode: tc.country}, 10)
			if err != nil || !reflect.DeepEqual(append([]CityCount{}, got...), tc.want) {
				t.Errorf("TopCities(%s) = %+v, %v; want %+v", tc.country, got, err, tc.want)
			}
		}
	})

	t.Run("CityName", func(t *testing.T) {
		for _, tc := range []struct {
			country, key string
			n            int
		}{
			{"RU", "lyuban", 1}, {"BY", "lyuban", 3}, {"", "lyuban", 4},
			{"RU", "minsk", 0}, {"BY", "minsk", 2}, {"BY", "moscow", 0},
		} {
			if _, n, err := s.CityName(ctx, tc.country, tc.key, from); err != nil || n != tc.n {
				t.Errorf("CityName(%q, %q) n = %d, %v; want %d", tc.country, tc.key, n, err, tc.n)
			}
		}
	})

	t.Run("SimilarCities", func(t *testing.T) {
		for _, tc := range []struct {
			country string
			prefix  string
			want    []string
		}{
			{"RU", "mi", []string{}}, {"BY", "mi", []string{"minsk"}},
			{"RU", "lyu", []string{"lyuban"}}, {"", "mi", []string{"minsk"}}, {"", "mo", []string{"moscow"}},
			{"BY", "mo", []string{}}, {"", "lyu", []string{"lyuban"}},
		} {
			got, err := s.SimilarCities(ctx, tc.country, tc.prefix, "", from, 5)
			if err != nil || !reflect.DeepEqual(keys(got), tc.want) {
				t.Errorf("SimilarCities(%q, %q) = %v, %v; want %v", tc.country, tc.prefix, keys(got), err, tc.want)
			}
		}
	})
}

// Типы меню — только типы выбранной страны.
func TestEventTypesByCountry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEventIn(t, s, "RU", "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000)
	listEventIn(t, s, "BY", "2026-10-02", "Минск", "minsk", event.TypeObstacle, 5000)
	listEventIn(t, s, "BY", "2026-10-03", "Минск", "minsk", event.TypeRoad, 5000)

	from := mustDateFixed("2026-10-01")
	for country, want := range map[string][]event.Type{
		"RU": {event.TypeRoad},
		"BY": {event.TypeRoad, event.TypeObstacle},
		"":   {event.TypeRoad, event.TypeObstacle},
		"PL": {},
	} {
		got, err := s.EventTypes(ctx, country, from)
		if err != nil || !reflect.DeepEqual(append([]event.Type{}, got...), sortTypes(want)) {
			t.Errorf("EventTypes(%q) = %v, %v; want %v", country, got, err, sortTypes(want))
		}
	}
}

// sortTypes — ожидание в порядке запроса (ORDER BY event_type).
func sortTypes(in []event.Type) []event.Type {
	out := append([]event.Type{}, in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestCountries(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEventIn(t, s, "RU", "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000)
	listEventIn(t, s, "RU", "2026-10-02", "Казань", "kazan", event.TypeTrail, 21097)
	listEventIn(t, s, "RU", "2026-10-03", "Тула", "tula", event.TypeRoad, 42195)
	listEventIn(t, s, "BY", "2026-10-04", "Минск", "minsk", event.TypeRoad, 10000)
	listEventIn(t, s, "BY", "2026-10-05", "Гомель", "gomel", event.TypeTrail, 10000)
	listEventIn(t, s, "PL", "2026-10-06", "Краков", "krakow", event.TypeRoad, 5000)
	listEventIn(t, s, "PL", "2026-09-01", "Краков", "krakow", event.TypeRoad, 5000) // в прошлом
	hidden := listEventIn(t, s, "PL", "2026-10-07", "Краков", "krakow", event.TypeRoad, 5000)
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", hidden)

	from := mustDateFixed("2026-10-01")
	cases := []struct {
		name string
		f    event.Filter
		want []CountryCount
	}{
		{"порядок: число по убыванию, затем код; без скрытых и прошлых",
			event.Filter{From: from}, []CountryCount{{"RU", 3}, {"BY", 2}, {"PL", 1}}},
		{"фильтр страны не участвует", event.Filter{From: from, CountryCode: "BY"},
			[]CountryCount{{"RU", 3}, {"BY", 2}, {"PL", 1}}},
		{"тип учитывается", event.Filter{From: from, Type: event.TypeTrail},
			[]CountryCount{{"BY", 1}, {"RU", 1}}},
		{"даты учитываются", event.Filter{From: mustDateFixed("2026-10-04")},
			[]CountryCount{{"BY", 2}, {"PL", 1}}},
		{"корзины учитываются", event.Filter{From: from, Distances: []event.DistanceRange{{Lo: 30000, Hi: 43000}}},
			[]CountryCount{{"RU", 1}}},
		{"пустая выдача", event.Filter{From: mustDateFixed("2027-01-01")}, []CountryCount{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Countries(ctx, tc.f)
			if err != nil || !reflect.DeepEqual(append([]CountryCount{}, got...), tc.want) {
				t.Fatalf("Countries = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}
