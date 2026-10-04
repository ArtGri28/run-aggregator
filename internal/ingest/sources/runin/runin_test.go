package runin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

// fixtureNow — день загрузки фикстуры.
var fixtureNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// card — карточка блока.
func card(date, href, title, place string) string {
	return `<div class="events__item"><div class="events__item-preview"><a href="` + href + `" class="events__item-img"></a></div>` +
		`<div class="events__item-info"><div class="events__item-date">` + date + `</div>` +
		`<a href="` + href + `" class="events__item-title" title="` + title + `">` + title + `</a>` +
		`<div class="events__item-bottom"><span class="events__item-location" title="` + place + `"> ` + place + ` </span></div></div></div>`
}

func page(cards ...string) []byte {
	return []byte(`<html><body><div class="events__container"><div class="events__list">` + strings.Join(cards, "") + `</div></div></body></html>`)
}

func parse(t *testing.T, body []byte) []ingest.SourceEvent {
	t.Helper()
	recs, err := Parse(ingest.RawDoc{URL: PageURL, Body: body, FetchedAt: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func testSource(srvURL string) *Source {
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent = "RunAggregator/0.5 (+contact: test@example.invalid)"
	cfg.Attempts = 1
	s := New(ingest.NewFetcher(cfg))
	s.url, s.delay = srvURL+"/", time.Millisecond
	return s
}

func TestParseFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/home.html")
	if err != nil {
		t.Fatal(err)
	}
	recs := parse(t, body)
	want := []ingest.SourceEvent{
		{ExternalID: "MezhdunarodnyyMaloritskiyMarafon", URL: "https://runin.by/event/MezhdunarodnyyMaloritskiyMarafon",
			Title: "Международный Малоритский Марафон", Date: "10.10", City: "Малорита", Country: "BY",
			RegistrationURL: "https://runin.by/event/MezhdunarodnyyMaloritskiyMarafon", Address: "г. Малорита", Timezone: "Europe/Minsk"},
		{ExternalID: "Gomelskiypolumarafon", URL: "https://runin.by/event/Gomelskiypolumarafon",
			Title: "Гомельский полумарафон", Date: "10.10", City: "Гомель", Country: "BY",
			RegistrationURL: "https://runin.by/event/Gomelskiypolumarafon", Address: "г. Гомель, ул. Набережная, 27, гребная база", Timezone: "Europe/Minsk"},
	}
	if !reflect.DeepEqual(recs, want) {
		t.Fatalf("записи = %+v", recs)
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Valid != 2 || st.OutOfScope != 0 || prep[0].Event.CountryCode != "BY" || prep[0].Event.Timezone != "Europe/Minsk" ||
		!prep[0].Event.StartDate.Equal(time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Prepare = %+v, %+v", prep, st)
	}
}

func TestParseEdge(t *testing.T) {
	for name, body := range map[string][]byte{
		"пустой ответ": nil,
		"нет блока":    []byte(`<html><body><div id="app"></div></body></html>`),
	} {
		if recs, err := Parse(ingest.RawDoc{URL: PageURL, Body: body}); !errors.Is(err, ingest.ErrStructureNotFound) || recs != nil {
			t.Errorf("%s: Parse = %+v, %v; want ErrStructureNotFound", name, recs, err)
		}
	}
	if recs := parse(t, page()); recs != nil {
		t.Errorf("пустой блок: %+v", recs)
	}

	recs := parse(t, page(
		card("10 - 11 октября", "/event/a", "Диапазон дат", "Минск"),
		card("31 февраля", "/event/b", "Невозможная дата", "Минск"),
		card("10 октября", "/Account/Login", "Чужая ссылка", "Минск"),
		card("10 октября", "/event/c", "", "Минск"),
		card("10 октября", "/event/d", "Забег отменён", "Минск"),
		card("10 октября", "/event/e", "Забег 5 км, 10 км", "Парк-отель «Яркі»"),
		card("10 октября", "/event/e", "Повтор ссылки", "Минск"),
	))
	if len(recs) != 6 {
		t.Fatalf("записей %d, want 6 (отменённое событие записи не даёт)", len(recs))
	}
	if recs[0].Date != "10 - 11 октября" || recs[1].Date != "31.02" || recs[2].ExternalID != "" {
		t.Fatalf("записи = %+v", recs[:3])
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Valid != 1 || st.Rejects["дата не разобрана"].Count != 2 || st.Rejects["нет external_id"].Count != 1 ||
		st.Rejects["нет названия"].Count != 1 || st.Rejects["дубликат external_id"].Count != 1 {
		t.Fatalf("Prepare = %+v", st)
	}
	// Несколько дистанций — из названия; место без города — только в address.
	if e := prep[0].Event; e.City != "" || e.Address != "Парк-отель «Яркі»" ||
		!slices.Equal(e.Distances, []event.Distance{{Meters: 5000}, {Meters: 10000}}) {
		t.Fatalf("событие = %+v", e)
	}
}

func TestSourceFetch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("<html>home</html>"))
	}))
	s := testSource(srv.URL)
	if want := (ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: 10 * time.Second}); s.Code() != "runin" || s.Policy() != want {
		t.Fatalf("Code %q, Policy %+v", s.Code(), s.Policy())
	}
	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 1 || string(docs[0].Body) != "<html>home</html>" || hits.Load() != 1 {
		t.Fatalf("Fetch = %+v, %v, запросов %d", docs, err, hits.Load())
	}
	// Сетевая ошибка: сервер недоступен.
	srv.Close()
	if docs, err := s.Fetch(context.Background()); err == nil || docs != nil {
		t.Fatalf("Fetch без сервера = %+v, %v", docs, err)
	}
}

// Tracer: httptest → Fetch → Parse → Prepare → Run (suspect, каталог не
// тронут) → Accept → событие в каталоге; повторный прогон событий не создаёт.
func TestTracerRun(t *testing.T) {
	st := storetest.Open(t)
	day := time.Now().AddDate(0, 0, 20)
	months := []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
	body := page(card(day.Format("2 ")+months[day.Month()-1], "/event/Gomelskiypolumarafon", "Гомельский полумарафон", "г. Гомель, ул. Набережная, 27"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	src := testSource(srv.URL)

	ctx := context.Background()
	r := &ingest.Runner{Store: st, Now: time.Now}
	res, err := r.Run(ctx, src)
	if err != nil || res.Status != ingest.StatusSuspect || !slices.Contains(res.Reasons, "первый прогон источника — требуется принятие") {
		t.Fatalf("первый прогон = %+v, %v; want suspect", res, err)
	}
	if items, _ := st.ListEvents(ctx, event.Filter{}, 10, 0); len(items) != 0 {
		t.Fatalf("до принятия в каталоге %d событий", len(items))
	}
	acc, err := r.Accept(ctx, src, res.RunID)
	if err != nil || acc.Created != 1 {
		t.Fatalf("Accept = %+v, %v; want Created 1", acc, err)
	}
	again, err := r.Run(ctx, src)
	if err != nil || again.Status != ingest.StatusOK || again.Created != 0 || again.Unchanged != 1 {
		t.Fatalf("повторный прогон = %+v, %v; want ok, Created 0, Unchanged 1", again, err)
	}
	items, err := st.ListEvents(ctx, event.Filter{}, 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListEvents = %d, %v; want 1", len(items), err)
	}
	e, err := st.GetEvent(ctx, items[0].ID)
	if err != nil || e.Title != "Гомельский полумарафон" || e.City != "Гомель" || e.CountryCode != "BY" || e.Timezone != "Europe/Minsk" ||
		e.RegistrationURL != "https://runin.by/event/Gomelskiypolumarafon" ||
		!e.StartDate.Equal(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)) ||
		!slices.Equal(e.Distances, []event.Distance{{Meters: 21098}}) {
		t.Fatalf("событие = %+v, %v", e, err)
	}
	srcs, err := st.EventSources(ctx, e.ID)
	if err != nil || len(srcs) != 1 || srcs[0] != (store.EventSource{Code: Code, URL: "https://runin.by/event/Gomelskiypolumarafon"}) {
		t.Fatalf("EventSources = %+v, %v", srcs, err)
	}
	t.Logf("TRACER runin first=%s created=%d again=%s", res.Status, acc.Created, again.Status)
}
