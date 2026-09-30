package store

import (
	"context"
	"reflect"
	"testing"

	"aggregator/internal/event"
)

// listEvent сохраняет событие выдачи; id растут в порядке вызовов.
func listEvent(t *testing.T, s *Store, date, city, cityKey string, typ event.Type, meters ...int) int64 {
	t.Helper()
	e := sampleEvent()
	e.Title, e.TitleNorm = "Забег "+date+" "+city, "zabeg "+date+cityKey
	e.StartDate, e.EndDate = mustDateFixed(date), nil
	e.City, e.CityKey, e.Type = city, cityKey, typ
	e.Distances = nil
	for _, m := range meters {
		e.Distances = append(e.Distances, event.Distance{Meters: m})
	}
	if _, err := s.SaveEvent(context.Background(), e); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	return e.ID
}

func listIDs(items []ListItem) []int64 {
	ids := []int64{}
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestListEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	a := listEvent(t, s, "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000, 10000)
	b := listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeTrail, 21097)
	c := listEvent(t, s, "2026-10-02", "Санкт-Петербург", "saint-petersburg", event.TypeRoad, 42195)
	d := listEvent(t, s, "2026-10-05", "Казань", "kazan", event.TypeRoad, 5500, 15000)
	e := listEvent(t, s, "2026-10-06", "Москва", "moscow", event.TypeRoad) // без дистанций
	f := listEvent(t, s, "2026-10-07", "Казань", "kazan", event.TypeTrail, 50000)
	g := listEvent(t, s, "2026-10-08", "Москва", "moscow", event.TypeRoad, 15000)
	j := listEvent(t, s, "2026-10-09", "Москва", "moscow", event.TypeRoad, 5500)
	hidden := listEvent(t, s, "2026-10-03", "Москва", "moscow", event.TypeRoad, 5000)
	merged := listEvent(t, s, "2026-10-04", "Москва", "moscow", event.TypeRoad, 5000)
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", hidden)
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", a, merged)

	from := mustDateFixed("2026-10-01")
	all := []int64{a, b, c, d, e, f, g, j}

	cases := []struct {
		name string
		f    event.Filter
		want []int64
	}{
		{"по умолчанию: только видимые, start_date, id", event.Filter{From: from}, all},
		{"nil-корзины как без корзин (Pitfall 3)", event.Filter{From: from, Distances: nil}, all},
		{"пустые корзины как без корзин (Pitfall 3)", event.Filter{From: from, Distances: []event.DistanceRange{}}, all},
		{"From включительно", event.Filter{From: mustDateFixed("2026-10-02")}, all[1:]},
		{"To включительно", event.Filter{From: from, To: mustDateFixed("2026-10-05")}, []int64{a, b, c, d}},
		{"корзина (5500, 15000] включает 15000, не включает 5500", event.Filter{From: from,
			Distances: []event.DistanceRange{{Lo: 5500, Hi: 15000}}}, []int64{a, d, g}},
		{"корзина (0, 5500]", event.Filter{From: from,
			Distances: []event.DistanceRange{{Lo: 0, Hi: 5500}}}, []int64{a, d, j}},
		{"две корзины — объединение", event.Filter{From: from,
			Distances: []event.DistanceRange{{Lo: 0, Hi: 5500}, {Lo: 30000, Hi: 43000}}}, []int64{a, c, d, j}},
		{"город", event.Filter{From: from, CityKey: "kazan"}, []int64{d, f}},
		{"тип", event.Filter{From: from, Type: event.TypeTrail}, []int64{b, f}},
		{"город, тип и корзина", event.Filter{From: from, CityKey: "moscow", Type: event.TypeRoad,
			Distances: []event.DistanceRange{{Lo: 5500, Hi: 15000}}}, []int64{a, g}},
		{"ничего не подошло", event.Filter{From: mustDateFixed("2027-01-01")}, []int64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := s.ListEvents(ctx, tc.f, 100, 0)
			if err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			if got := listIDs(items); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ListEvents ids = %v, want %v", got, tc.want)
			}
			n, err := s.CountEvents(ctx, tc.f)
			if err != nil || n != len(tc.want) {
				t.Fatalf("CountEvents = %d, %v; want %d", n, err, len(tc.want))
			}
		})
	}

	t.Run("LIMIT/OFFSET без пропусков и повторов", func(t *testing.T) {
		got := []int64{}
		for off := 0; off < len(all)+3; off += 3 {
			items, err := s.ListEvents(ctx, event.Filter{From: from}, 3, off)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, listIDs(items)...)
		}
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("pages ids = %v, want %v", got, all)
		}
	})

	t.Run("поля строки", func(t *testing.T) {
		items, err := s.ListEvents(ctx, event.Filter{From: from}, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		first, noDist := items[0], items[4]
		if first.Title != "Забег 2026-10-01 Москва" || first.City != "Москва" || first.Status != event.StatusScheduled ||
			!first.StartDate.Equal(from) || !reflect.DeepEqual(first.Distances, []int{5000, 10000}) {
			t.Fatalf("first = %+v", first)
		}
		if noDist.ID != e || len(noDist.Distances) != 0 {
			t.Fatalf("event without distances = %+v", noDist)
		}
	})
}

func TestEventSources(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	id := listEvent(t, s, "2026-10-01", "Москва", "moscow", event.TypeRoad, 5000)
	empty := listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeRoad, 5000)

	add := func(code, ext, url, state, match string) {
		t.Helper()
		src, err := s.EnsureSource(ctx, code)
		if err != nil {
			t.Fatalf("EnsureSource: %v", err)
		}
		exec(t, `INSERT INTO source_events (source_id, external_id, url, content_hash, state, match_state, event_id)
			VALUES ($1, $2, $3, 'h', $4, $5, $6)`, src, ext, url, state, match, id)
	}
	add("b-src", "1", "https://b.example.test/1", "active", "auto")
	add("a-src", "1", "https://a.example.test/1", "missing", "manual")
	add("c-src", "1", "https://c.example.test/1", "gone", "auto")
	add("d-src", "1", "https://d.example.test/1", "active", "out_of_scope")

	got, err := s.EventSources(ctx, id)
	if err != nil {
		t.Fatalf("EventSources: %v", err)
	}
	want := []EventSource{{"a-src", "https://a.example.test/1"}, {"b-src", "https://b.example.test/1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EventSources = %+v, want %+v", got, want)
	}
	if got, err := s.EventSources(ctx, empty); err != nil || len(got) != 0 {
		t.Fatalf("EventSources without sources = %+v, %v; want empty", got, err)
	}
}
