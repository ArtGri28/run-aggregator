package klbamatar

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
func card(date, place, href, title string) string {
	return `<div class="position"><span>` + date + `</span><br /><b>Место проведения:</b> ` + place +
		`<br /><a href="` + href + `">` + title + `</a></div>`
}

func page(cards ...string) []byte {
	return []byte(`<html><body><h2>Ближайшие пробеги</h2><div id="position">` + strings.Join(cards, "") + `</div></body></html>`)
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
	if len(recs) != 6 {
		t.Fatalf("записей %d, want 6", len(recs))
	}
	want := []ingest.SourceEvent{
		{ExternalID: "/polozheniya/2026/2026_10/trejl-babuliny-kazki-2026.html", URL: "https://klbamatar.by/polozheniya/2026/2026_10/trejl-babuliny-kazki-2026.html",
			Title: "Трэйл «Бабуліны казкі» 2026", Date: "10.10.2026", Country: "BY", Address: "База отдыха «Привал»", Timezone: "Europe/Minsk"},
		{ExternalID: "/polozheniya/2026/2026_10/mezhdunarodnyj-maloritskij-marafon1.html", URL: "https://klbamatar.by/polozheniya/2026/2026_10/mezhdunarodnyj-maloritskij-marafon1.html",
			Title: "Международный Малоритский Марафон", Date: "10.10.2026", City: "Малорита", Country: "BY", Timezone: "Europe/Minsk"},
	}
	if !reflect.DeepEqual(recs[:2], want) {
		t.Fatalf("записи = %+v", recs[:2])
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Valid != 6 || st.Rejected != 0 || st.OutOfScope != 0 || prep[1].Event.CountryCode != "BY" || prep[1].Event.Timezone != "Europe/Minsk" {
		t.Fatalf("Prepare = %+v", st)
	}
	// Дистанций в блоке нет: Prepare берёт их из названия, когда они там есть.
	if d := prep[1].Event.Distances; !slices.Equal(d, []event.Distance{{Meters: 42195}}) || len(prep[0].Event.Distances) != 0 {
		t.Fatalf("дистанции: %v, %v", d, prep[0].Event.Distances)
	}
}

func TestParseEdge(t *testing.T) {
	for name, body := range map[string][]byte{
		"пустой ответ": nil,
		"нет блока":    []byte(`<html><body><div class="position"><span>10 октября 2026</span></div></body></html>`),
	} {
		if recs, err := Parse(ingest.RawDoc{URL: PageURL, Body: body}); !errors.Is(err, ingest.ErrStructureNotFound) || recs != nil {
			t.Errorf("%s: Parse = %+v, %v; want ErrStructureNotFound", name, recs, err)
		}
	}
	if recs := parse(t, page()); recs != nil {
		t.Errorf("пустой блок: %+v", recs)
	}

	recs := parse(t, page(
		card("скоро", "Минск", "/polozheniya/a.html", "Плохая дата"),
		card("31 февраля 2027", "Минск", "/polozheniya/b.html", "Невозможная дата"),
		card("10 октября 2026", "Минск", "", "Без ссылки"),
		card("10 октября 2026", "Минск", "https://example.org/x.html", "Чужой сайт"),
		card("10 октября 2026", "Минск", "/polozheniya/c.html", ""),
		card("10 октября 2026", "Минск", "/polozheniya/d.html", "Забег ОТМЕНЁН"),
		card("10 октября 2026", "Барановичский р-н, д. Колбовичи", "/polozheniya/e.html", "Трейл 5 км, 10 км"),
		card("10 октября 2026", "Минск", "/polozheniya/e.html", "Повтор ссылки"),
	))
	if len(recs) != 7 {
		t.Fatalf("записей %d, want 7 (отменённый пробег записи не даёт)", len(recs))
	}
	if recs[0].Date != "скоро" || recs[1].Date != "31.02.2027" || recs[2].ExternalID != "" || recs[3].ExternalID != "" {
		t.Fatalf("записи = %+v", recs[:4])
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Valid != 1 || st.Rejects["дата не разобрана"].Count != 2 || st.Rejects["нет external_id"].Count != 2 ||
		st.Rejects["нет названия"].Count != 1 || st.Rejects["дубликат external_id"].Count != 1 {
		t.Fatalf("Prepare = %+v", st)
	}
	// Несколько дистанций — из названия; город — из второй части места.
	if e := prep[0].Event; e.City != "Колбовичи" || e.Address != "Барановичский р-н, д. Колбовичи" ||
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
	if want := (ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: 10 * time.Second}); s.Code() != "klbamatar" || s.Policy() != want {
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
	date := day.Format("02 ") + months[day.Month()-1] + day.Format(" 2006")
	body := page(card(date, "Вязье", "/polozheniya/2026/2026_10/dikij-trejl-2026.html", "Дикий трейл 2026"))
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
	if err != nil || e.Title != "Дикий трейл 2026" || e.City != "Вязье" || e.CountryCode != "BY" || e.Timezone != "Europe/Minsk" ||
		!e.StartDate.Equal(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("событие = %+v, %v", e, err)
	}
	srcs, err := st.EventSources(ctx, e.ID)
	// URL события строится от адреса страницы прогона (в тесте — httptest).
	if err != nil || len(srcs) != 1 || srcs[0] != (store.EventSource{Code: Code, URL: srv.URL + "/polozheniya/2026/2026_10/dikij-trejl-2026.html"}) {
		t.Fatalf("EventSources = %+v, %v", srcs, err)
	}
	t.Logf("TRACER klbamatar first=%s created=%d again=%s", res.Status, acc.Created, again.Status)
}
