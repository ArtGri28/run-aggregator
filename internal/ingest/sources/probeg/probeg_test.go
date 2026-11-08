package probeg

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

func testFetcher() *ingest.Fetcher {
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent = "RunAggregator/0.5 (+contact: test@example.invalid)"
	return ingest.NewFetcher(cfg)
}

func TestSourcePolicy(t *testing.T) {
	s := New(nil)
	if s.Code() != "probeg" {
		t.Fatalf("Code = %q", s.Code())
	}
	want := ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: 30 * time.Second, OutOfScopeMax: 0.40}
	if p := s.Policy(); p != want {
		t.Fatalf("Policy = %+v, want %+v", p, want)
	}
	doc := ingest.RawDoc{URL: CalendarURL, Body: []byte(card("", "1", "8 ноября 2026", "", "", "")), FetchedAt: fixtureNow}
	got, err1 := s.Parse(doc)
	want2, err2 := Parse(doc)
	if err1 != nil || err2 != nil || !reflect.DeepEqual(got, want2) {
		t.Fatalf("method Parse differs from Parse: %v, %v", err1, err2)
	}
}

// calendar — сервер календаря из pages страниц; страница N ссылается на N+1.
func calendar(t *testing.T, pages int, body func(n int) string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		n := 1
		if p := r.URL.Query().Get("page"); p != "" {
			_, _ = fmt.Sscan(p, &n)
		}
		next := ""
		if n < pages {
			next = fmt.Sprintf(`<a href="?page=%d" aria-label="Следующая страница">→</a>`, n+1)
		}
		_, _ = w.Write([]byte(`<html><body><nav class="pages">` + next + `</nav>` + body(n) + `</body></html>`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestSourceFetch(t *testing.T) {
	srv, hits := calendar(t, 3, func(n int) string { return fmt.Sprint("страница ", n) })
	s := New(testFetcher())
	s.url, s.delay = srv.URL+"/races/year_ahead/", time.Millisecond
	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 3 || hits.Load() != 3 {
		t.Fatalf("Fetch = %d страниц, %d запросов, %v", len(docs), hits.Load(), err)
	}
	for i, d := range docs {
		if !strings.Contains(string(d.Body), fmt.Sprint("страница ", i+1)) || d.FetchedAt.IsZero() {
			t.Errorf("страница %d: %s", i+1, d.URL)
		}
	}
	if docs[2].URL != s.url+"?page=3" {
		t.Errorf("URL страницы 3 = %s", docs[2].URL)
	}
}

func TestSourceFetchTooManyPages(t *testing.T) {
	srv, hits := calendar(t, maxPages+1, func(int) string { return "" })
	s := New(testFetcher())
	s.url, s.delay = srv.URL+"/races/year_ahead/", time.Millisecond
	if docs, err := s.Fetch(context.Background()); err == nil || docs != nil || hits.Load() != maxPages {
		t.Fatalf("Fetch = %d страниц, %d запросов, %v; want ошибку после %d", len(docs), hits.Load(), err, maxPages)
	}
}

// Tracer: httptest → Fetch (2 страницы) → Parse → Prepare → Run (suspect) →
// Accept → события ПроБЕГа в каталоге, Беларусь с кодом BY, другая страна —
// вне каталога.
func TestTracerRun(t *testing.T) {
	st := storetest.Open(t)
	d := time.Now().AddDate(0, 0, 20)
	when := fmt.Sprintf("%d %s %d, 10:00", d.Day(), monthName(d.Month()), d.Year())
	srv, _ := calendar(t, 2, func(n int) string {
		if n == 1 {
			return card("", "83327", when, `<a href="/races/city/1/">Черноисточинск</a> · Свердловская`,
				`<span class="badge">20 км</span><span class="badge">10 км</span><span class="badge">2 км</span><span class="badge">1 км</span>`, "")
		}
		return card("", "77919", when, `<a href="/races/city/2/">Минск</a> · Беларусь`, `<span class="badge">10 км</span>`, "") +
			card("", "90002", when, `<a href="/races/city/3/">Алматы</a> · Казахстан`, `<span class="badge">10 км</span>`, "")
	})
	src := New(testFetcher())
	src.url, src.delay = srv.URL+"/races/year_ahead/", time.Millisecond

	ctx := context.Background()
	r := &ingest.Runner{Store: st, Now: time.Now}
	res, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != ingest.StatusSuspect || !slices.Contains(res.Reasons, "первый прогон источника — требуется принятие") {
		t.Fatalf("первый прогон = %s %v, want suspect с причиной первого прогона", res.Status, res.Reasons)
	}
	acc, err := r.Accept(ctx, src, res.RunID)
	if err != nil || acc.Created != 2 || acc.OutOfScope != 1 {
		t.Fatalf("Accept = %+v, %v; want Created 2, OutOfScope 1", acc, err)
	}
	items, err := st.ListEvents(ctx, event.Filter{CityKey: normalize.CityKey("Черноисточинск")}, 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListEvents = %+v, %v; want 1 событие", items, err)
	}
	it := items[0]
	if it.Title != "Старт 83327" || !slices.Equal(it.Distances, []int{1000, 2000, 10000, 20000}) {
		t.Fatalf("событие = %+v", it)
	}
	e, err := st.GetEvent(ctx, it.ID)
	if err != nil || e.Region != "Свердловская область" || e.CountryCode != "RU" || e.StartTime != "10:00" {
		t.Fatalf("GetEvent = %+v, %v", e, err)
	}
	srcs, err := st.EventSources(ctx, it.ID)
	if err != nil || !slices.Equal(srcs, []store.EventSource{{Code: "probeg", URL: "https://probeg.org/event/83327/"}}) {
		t.Fatalf("EventSources = %+v, %v", srcs, err)
	}
	by, err := st.ListEvents(ctx, event.Filter{CityKey: normalize.CityKey("Минск")}, 10, 0)
	if err != nil || len(by) != 1 {
		t.Fatalf("Минск = %+v, %v", by, err)
	}
	if e, err := st.GetEvent(ctx, by[0].ID); err != nil || e.CountryCode != "BY" {
		t.Fatalf("Минск = %+v, %v", e, err)
	}
}

func monthName(m time.Month) string {
	for k, v := range months {
		if v == m {
			return k
		}
	}
	return ""
}
