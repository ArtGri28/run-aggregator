package store

import (
	"context"
	"reflect"
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
		got, err := s.SimilarCities(ctx, prefix, from, 5)
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
	got, _ := s.SimilarCities(ctx, "nizh", from, 5)
	if want := (CityCount{Key: "nizhniy-novgorod", Name: "Нижний Новгород", N: 2}); len(got) != 1 || got[0] != want {
		t.Fatalf("SimilarCities(nizh) = %+v, want [%+v]", got, want)
	}
}

func TestCityName(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	listEvent(t, s, "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeRoad, 5000)
	listEvent(t, s, "2026-09-01", "Тула", "tula", event.TypeRoad, 5000) // в прошлом

	from := mustDateFixed("2026-10-01")
	if name, n, err := s.CityName(ctx, "moscow", from); err != nil || name != "Москва" || n != 2 {
		t.Fatalf("CityName(moscow) = %q, %d, %v; want Москва, 2", name, n, err)
	}
	if _, n, err := s.CityName(ctx, "tula", from); err != nil || n != 0 {
		t.Fatalf("CityName(tula) n = %d, %v; want 0", n, err)
	}
	if _, n, err := s.CityName(ctx, "nowhere", from); err != nil || n != 0 {
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

	got, err := s.EventTypes(ctx, mustDateFixed("2026-10-01"))
	if err != nil {
		t.Fatalf("EventTypes: %v", err)
	}
	if want := []event.Type{event.TypeRoad, event.TypeTrail}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EventTypes = %v, want %v", got, want)
	}
}
