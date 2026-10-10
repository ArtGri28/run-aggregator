package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"aggregator/internal/event"
)

// enableSports задаёт признак «включён» видам спорта справочника на время
// теста: включены только codes. Прежние значения возвращаются по завершении.
func enableSports(t *testing.T, codes ...event.Sport) {
	t.Helper()
	was := event.Sports
	t.Cleanup(func() { event.Sports = was })
	for i := range event.Sports {
		event.Sports[i].Enabled = false
		for _, c := range codes {
			if event.Sports[i].Code == c {
				event.Sports[i].Enabled = true
			}
		}
	}
}

// TestSportFilter — выбранный вид спорта отбирает выдачу поверх включённых:
// значение уходит параметром запроса, выключенный вид спорта не даёт ничего и
// будучи выбранным. «Выключено» проверяется на плавании.
func TestSportFilter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	from := mustDateFixed("2026-10-01")

	run := listEvent(t, s, "2026-10-02", "Раменское", "ramenskoe", event.TypeRoad, 10000)
	ski := listEventIn(t, s, "BY", "2026-10-03", "Раубичи", "raubichi", event.TypeRoad, 15000)
	swim := listEventIn(t, s, "BY", "2026-10-04", "Брест", "brest", event.TypeRoad, 1000)
	const setSport = "UPDATE events SET sport = $2 WHERE id = $1"
	exec(t, setSport, ski, "skiing")
	exec(t, setSport, swim, "swimming")
	enableSports(t, event.SportRunning, event.SportSkiing)

	type view struct {
		IDs       []int64
		Count     int
		Cities    []string
		Countries []CountryCount
	}
	get := func(sport event.Sport) view {
		t.Helper()
		f := event.Filter{From: from, Sport: sport}
		list, err1 := s.ListEvents(ctx, f, 100, 0)
		count, err2 := s.CountEvents(ctx, f)
		top, err3 := s.TopCities(ctx, f, 100)
		countries, err4 := s.Countries(ctx, f)
		if err := errors.Join(err1, err2, err3, err4); err != nil {
			t.Fatalf("вид спорта %q: %v", sport, err)
		}
		v := view{IDs: listIDs(list), Count: count, Cities: []string{}, Countries: countries}
		for _, c := range top {
			v.Cities = append(v.Cities, c.Key)
		}
		for _, it := range list {
			if want := map[int64]event.Sport{run: event.SportRunning, ski: event.SportSkiing}[it.ID]; it.Sport != want {
				t.Errorf("вид спорта %q: событие %d со Sport = %q, want %q", sport, it.ID, it.Sport, want)
			}
		}
		return v
	}
	none := view{IDs: []int64{}, Cities: []string{}, Countries: []CountryCount{}}
	for sport, want := range map[event.Sport]view{
		event.SportSkiing:  {[]int64{ski}, 1, []string{"raubichi"}, []CountryCount{{"BY", 1}}},
		event.SportRunning: {[]int64{run}, 1, []string{"ramenskoe"}, []CountryCount{{"RU", 1}}},
		"":                 {[]int64{run, ski}, 2, []string{"ramenskoe", "raubichi"}, []CountryCount{{"BY", 1}, {"RU", 1}}},
		// Выключенный вид спорта не виден и выбранным; кода вне справочника нет
		// ни у одного события; текст вместо кода остаётся значением параметра.
		event.SportSwimming:    none,
		"chess":                none,
		"skiing' OR '1'='1":    none,
		"') OR e.sport IN ('x": none,
	} {
		if got := get(sport); !reflect.DeepEqual(got, want) {
			t.Errorf("вид спорта %q: %+v, want %+v", sport, got, want)
		}
	}

	// Ни одного включённого вида спорта: запросы выдачи работают и пусты.
	enableSports(t)
	if got := sportVisible(); got != " AND false" {
		t.Fatalf("sportVisible без включённых видов спорта = %q", got)
	}
	if got := get(""); !reflect.DeepEqual(got, none) {
		t.Errorf("без включённых видов спорта: %+v, want пусто", got)
	}
	search, err1 := s.SearchCandidates(ctx, "", from, 100)
	types, err2 := s.EventTypes(ctx, "", from)
	_, n, err3 := s.CityName(ctx, "", "ramenskoe", from)
	similar, err4 := s.SimilarCities(ctx, "", "ra", "Ра", from, 100)
	if err := errors.Join(err1, err2, err3, err4); err != nil || len(search)+len(types)+n+len(similar) != 0 {
		t.Errorf("без включённых видов спорта: поиск %d, типы %d, город %d, подсказки %d, %v; want пусто", len(search), len(types), n, len(similar), err)
	}
}
