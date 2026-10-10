package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/marathonec"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Тест CountByDay на фикстуре Марафонца. Пакет внешний: приём фикстуры
// (ingest) сам зависит от store.

var calNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

type calSource struct{ body []byte }

func (s calSource) Code() string          { return marathonec.Code }
func (s calSource) Policy() ingest.Policy { return marathonec.New(nil).Policy() }
func (s calSource) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	return marathonec.Parse(doc)
}
func (s calSource) Fetch(context.Context) ([]ingest.RawDoc, error) {
	return []ingest.RawDoc{{URL: marathonec.CalendarURL, Body: s.body, FetchedAt: calNow}}, nil
}

func calDate(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestCountByDay(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	body, err := os.ReadFile("../ingest/sources/marathonec/testdata/calendar-beg.html")
	if err != nil {
		t.Fatal(err)
	}
	src := calSource{body: body}
	r := &ingest.Runner{Store: s, Now: func() time.Time { return calNow }}
	if _, err := r.Run(ctx, src); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res, err := r.Accept(ctx, src, 0); err != nil || res.Created <= 100 {
		t.Fatalf("Accept: created=%d err=%v, want > 100", res.Created, err)
	}

	days := func(f event.Filter) []store.DayCount {
		t.Helper()
		out, err := s.CountByDay(ctx, f)
		if err != nil {
			t.Fatalf("CountByDay: %v", err)
		}
		return out
	}
	count := func(f event.Filter) int {
		t.Helper()
		n, err := s.CountEvents(ctx, f)
		if err != nil {
			t.Fatalf("CountEvents: %v", err)
		}
		return n
	}
	// on — счётчик дня d в выдаче CountByDay (0 — дня нет).
	on := func(out []store.DayCount, d time.Time) int {
		for _, dc := range out {
			if dc.Date.Equal(d) {
				return dc.Count
			}
		}
		return 0
	}

	from, to := calDate("2026-09-29"), calDate("2026-11-29")
	distances := []event.DistanceRange{{Lo: 0, Hi: 10000}}
	for name, f := range map[string]event.Filter{
		"диапазон":            {From: from, To: to},
		"без верхней границы": {From: from},
		"страна":              {From: from, To: to, CountryCode: "RU"},
		"чужая страна":        {From: from, To: to, CountryCode: "BY"},
		"город":               {From: from, To: to, CountryCode: "RU", CityKey: "moscow"},
		"дисциплина":          {From: from, To: to, Type: event.TypeTrail},
		"вид спорта, корзина": {From: from, To: to, Sport: event.SportRunning, Distances: distances},
		"периоды":             {From: from, Periods: []event.DateRange{{From: calDate("2026-10-01"), To: calDate("2026-10-31")}}},
	} {
		out := days(f)
		sum := 0
		for i, dc := range out {
			sum += dc.Count
			one := f
			one.From, one.To = dc.Date, dc.Date
			if dc.Count == 0 || dc.Count != count(one) {
				t.Errorf("%s: %s = %d, CountEvents дня = %d", name, dc.Date.Format(time.DateOnly), dc.Count, count(one))
			}
			if i > 0 && !dc.Date.After(out[i-1].Date) {
				t.Errorf("%s: порядок дат нарушен на %s", name, dc.Date.Format(time.DateOnly))
			}
			if dc.Date.Before(from) || (!f.To.IsZero() && dc.Date.After(f.To)) {
				t.Errorf("%s: день %s вне диапазона", name, dc.Date.Format(time.DateOnly))
			}
		}
		if want := count(f); sum != want || (name != "чужая страна") == (sum == 0) {
			t.Errorf("%s: сумма по дням = %d, CountEvents = %d", name, sum, want)
		}
	}

	// День с несколькими событиями: скрытое, слитое и событие выключенного
	// вида спорта из счёта уходят по одному.
	all := event.Filter{From: from, To: to}
	var day time.Time
	for _, dc := range days(all) {
		if dc.Count >= 4 {
			day = dc.Date
			break
		}
	}
	if day.IsZero() {
		t.Fatal("в фикстуре нет дня с четырьмя событиями")
	}
	items, err := s.ListEvents(ctx, event.Filter{From: day, To: day}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := len(items)
	if got := on(days(all), day); got != n {
		t.Fatalf("день %s: %d, ListEvents = %d", day.Format(time.DateOnly), got, n)
	}
	storetest.HideEvent(t, items[0].ID)
	if got := on(days(all), day); got != n-1 {
		t.Errorf("скрытое событие в счёте: %d, want %d", got, n-1)
	}
	storetest.MergeEvent(t, items[1].ID, items[2].ID)
	if got := on(days(all), day); got != n-2 {
		t.Errorf("слитое событие в счёте: %d, want %d", got, n-2)
	}
	storetest.SetSport(t, items[2].ID, event.SportSkiing)
	if got := on(days(all), day); got != n-2 {
		t.Errorf("событие включённого вида спорта: %d, want %d", got, n-2)
	}
	storetest.DisableSport(t, event.SportSkiing)
	if got := on(days(all), day); got != n-3 {
		t.Errorf("событие выключенного вида спорта в счёте: %d, want %d", got, n-3)
	}
	if got, want := on(days(all), day), count(event.Filter{From: day, To: day}); got != want {
		t.Errorf("после правок: день = %d, CountEvents = %d", got, want)
	}

	// Пустой диапазон — пустой срез, не ошибка.
	for _, f := range []event.Filter{
		{From: calDate("2031-01-01"), To: calDate("2031-01-31")},
		{From: to, To: from},
	} {
		if out := days(f); len(out) != 0 {
			t.Errorf("пустой диапазон: %+v", out)
		}
	}
}
