package tristyle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aggregator/internal/dedup"
	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// fixtureNow — день загрузки фикстур.
var fixtureNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// listPage — страница списка с карточками событий ids.
func listPage(ids ...string) []byte {
	var b strings.Builder
	b.WriteString(`<html><body><div class="content competitions-page">`)
	for _, id := range ids {
		// Ссылка сетки календаря на то же событие в список не входит.
		fmt.Fprintf(&b, `<a href="/competition/%s/" class="events-calendar-event-chip">x</a>`+
			`<div class="h-entry"><a href="/competition/%s/" class="post-title-link">🏆 x</a></div>`, id, id)
	}
	b.WriteString(`</div></body></html>`)
	return []byte(b.String())
}

// eventPage — страница события; date — значение параметра date ссылки.
func eventPage(title, date, location, text string) []byte {
	q := url.Values{"title": {title}, "date": {date}, "timezone": {"Europe/Minsk"}, "location": {location}}
	return []byte(`<html><body><div class="block post-event post-event-competition">` +
		`<div class="post-event-calendar"><a class="button" href="/misc/calendar/ical?` + q.Encode() + `">Apple</a></div></div>` +
		`<div class="text-body e-content">` + text + `</div></body></html>`)
}

func eventDoc(id string, body []byte) ingest.RawDoc {
	return ingest.RawDoc{URL: eventBase + id + "/", Body: body, FetchedAt: fixtureNow}
}

func parseOne(t *testing.T, id string, body []byte) ingest.SourceEvent {
	t.Helper()
	recs, err := Parse(eventDoc(id, body))
	if err != nil || len(recs) != 1 {
		t.Fatalf("Parse(%s) = %+v, %v; want 1 запись", id, recs, err)
	}
	return recs[0]
}

func testSource(srvURL string) *Source {
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent = "RunAggregator/0.5 (+contact: test@example.invalid)"
	cfg.Attempts = 1
	s := New(ingest.NewFetcher(cfg))
	s.listURL, s.base, s.delay = srvURL+"/competition/?only_belarus=on", srvURL+"/competition/", time.Millisecond
	return s
}

func TestParseFixtures(t *testing.T) {
	list := fixture(t, "list.html")
	ids, err := listIDs(list)
	want := []string{"1030", "1090", "1154", "1155", "1170", "1096", "1097", "1168", "1180"}
	if err != nil || !slices.Equal(ids, want) {
		t.Fatalf("listIDs = %v, %v; want %v", ids, err, want)
	}
	if recs, err := Parse(ingest.RawDoc{URL: ListURL, Body: list}); err != nil || recs != nil {
		t.Fatalf("Parse(список) = %+v, %v; want без записей и ошибки", recs, err)
	}

	got := parseOne(t, "1090", fixture(t, "event-1090.html"))
	if want := (ingest.SourceEvent{
		ExternalID: "1090", URL: "https://tristyle.club/competition/1090/", Title: "Гомельский полумарафон",
		Date: "2026-10-10", City: "Гомель", Country: "BY",
		Address: "г. Гомель, ул. Набережная, 27, гребная база", Timezone: "Europe/Minsk",
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("1090 = %+v, want %+v", got, want)
	}

	// Несколько дистанций — из строки «Дистанции: …», скобки убраны.
	got = parseOne(t, "1096", fixture(t, "event-1096.html"))
	if d := normalize.Distances(got.Distances); got.City != "Любань" || !slices.Equal(d, []int{6800, 30000}) {
		t.Fatalf("1096: city %q, distances %q → %v; want Любань, [6800 30000]", got.City, got.Distances, d)
	}

	// Место без города: город пуст, строка целиком — в address.
	got = parseOne(t, "1170", fixture(t, "event-1170.html"))
	if got.City != "" || !strings.HasPrefix(got.Address, "озеро Белое, Полоцкий район") || got.Date != "2026-10-17" {
		t.Fatalf("1170 = %+v", got)
	}

	// Фикстуры проходят Prepare: в каталоге, Беларусь, зона Минска.
	prep, st := ingest.Prepare([]ingest.SourceEvent{got}, fixtureNow, New(nil).Policy())
	if st.Valid != 1 || st.OutOfScope != 0 || prep[0].Event.CountryCode != "BY" || prep[0].Event.Timezone != "Europe/Minsk" {
		t.Fatalf("Prepare = %+v, %+v", prep, st)
	}
}

func TestParseEdge(t *testing.T) {
	// Пустой ответ и страница без ожидаемой структуры.
	for name, doc := range map[string]ingest.RawDoc{
		"пустой список":         {URL: ListURL},
		"список без блока":      {URL: ListURL, Body: []byte("<html><body><p>Технические работы</p></body></html>")},
		"пустое событие":        eventDoc("7", nil),
		"событие без календаря": eventDoc("7", []byte(`<html><body><div class="post-event-competition">10 октября</div></body></html>`)),
		"календарь вне блока":   eventDoc("7", []byte(`<html><body><a href="/misc/calendar/ical?title=x&date=2026-10-10">x</a></body></html>`)),
	} {
		if recs, err := Parse(doc); !errors.Is(err, ingest.ErrStructureNotFound) || recs != nil {
			t.Errorf("%s: Parse = %+v, %v; want ErrStructureNotFound", name, recs, err)
		}
	}
	// Список без событий — не ошибка структуры.
	if ids, err := listIDs(listPage()); err != nil || ids != nil {
		t.Errorf("пустой список: %v, %v", ids, err)
	}
	// Повтор ссылки в списке — один номер.
	if ids, _ := listIDs(listPage("5", "6", "5")); !slices.Equal(ids, []string{"5", "6"}) {
		t.Errorf("повтор в списке: %v", ids)
	}

	// Некорректная дата: запись есть, Prepare её отбраковывает.
	bad := parseOne(t, "7", eventPage("Забег", "скоро", "Минск", ""))
	if _, st := ingest.Prepare([]ingest.SourceEvent{bad}, fixtureNow, New(nil).Policy()); bad.Date != "скоро" || st.Rejected != 1 || st.Rejects["дата не разобрана"].Count != 1 {
		t.Errorf("некорректная дата: %+v, %+v", bad, st)
	}
	if got := parseOne(t, "7", eventPage("Забег", "2026-13-40T10:00:00", "Минск", "")); got.Date != "2026-13-40T10:00:00" {
		t.Errorf("невозможная дата: %q", got.Date)
	}
	if got := parseOne(t, "7", eventPage("Забег", "2026-11-07", "Минск", "")); got.Date != "2026-11-07" {
		t.Errorf("дата без времени: %q", got.Date)
	}

	// Нет дистанций: строки «Дистанции:» нет или она пуста.
	for _, text := range []string{"", "<p>Дистанции и условия</p><p>Старт на 5 км в 11:00</p>", "<p>Дистанции:</p>"} {
		if got := parseOne(t, "7", eventPage("Забег", "2026-11-07T10:00:00", "Минск", text)); got.Distances != "" {
			t.Errorf("текст %q: distances = %q, want пусто", text, got.Distances)
		}
	}
	got := parseOne(t, "7", eventPage("Забег", "2026-11-07T10:00:00", "Минск",
		"<p>Взнос 30 руб.</p><p><strong>Дистанции:</strong> 3&nbsp;км (дети 9-12 лет), Open 5 км (18+), 10 км<br>Старт в 11:00</p>"))
	if d := normalize.Distances(got.Distances); !slices.Equal(d, []int{3000, 5000, 10000}) {
		t.Errorf("distances = %q → %v", got.Distances, d)
	}

	// Отменённое событие записи не даёт.
	for _, title := range []string{"Забег ОТМЕНЁН", "Отмена: Минский забег"} {
		if recs, err := Parse(eventDoc("7", eventPage(title, "2026-11-07T10:00:00", "Минск", ""))); err != nil || recs != nil {
			t.Errorf("%q: Parse = %+v, %v; want без записей", title, recs, err)
		}
	}
	// Без названия запись отбраковывает Prepare.
	noTitle := parseOne(t, "7", eventPage("", "2026-11-07T10:00:00", "Минск", ""))
	if _, st := ingest.Prepare([]ingest.SourceEvent{noTitle}, fixtureNow, New(nil).Policy()); st.Rejects["нет названия"].Count != 1 {
		t.Errorf("без названия: %+v", st)
	}
}

func TestPlace(t *testing.T) {
	for _, c := range []struct{ loc, city, address string }{
		{"Малорита", "Малорита", ""},
		{"г.Любань, городской парк", "Любань", "г.Любань, городской парк"},
		{"Раубичи, Минский район", "Раубичи", "Раубичи, Минский район"},
		{"Гродно - База отдыха «Привал»", "Гродно", "Гродно - База отдыха «Привал»"},
		{"Сморгонь, Мемориальный комплекс (54.49,26.43)", "Сморгонь", "Сморгонь, Мемориальный комплекс (54.49,26.43)"},
		{"озеро Белое, Полоцкий район", "", "озеро Белое, Полоцкий район"},
		{"", "", ""},
	} {
		if city, address := place(c.loc); city != c.city || address != c.address {
			t.Errorf("place(%q) = %q, %q; want %q, %q", c.loc, city, address, c.city, c.address)
		}
	}
}

func TestSourcePolicy(t *testing.T) {
	s := New(nil)
	if want := (ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: 5 * time.Second}); s.Code() != "tristyle" || s.Policy() != want {
		t.Fatalf("Code %q, Policy %+v", s.Code(), s.Policy())
	}
}

func TestSourceFetch(t *testing.T) {
	var hits atomic.Int32
	list := listPage("5", "6", "5")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/competition/":
			_, _ = w.Write(list)
		case "/competition/5/", "/competition/6/":
			_, _ = w.Write([]byte("<html>" + r.URL.Path + "</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := testSource(srv.URL)
	ctx := context.Background()

	docs, err := s.Fetch(ctx)
	if err != nil || len(docs) != 3 || docs[0].URL != s.listURL || docs[1].URL != s.base+"5/" || docs[2].URL != s.base+"6/" {
		t.Fatalf("Fetch = %+v, %v", docs, err)
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("запросов: %d, want 3 (список и по одному на событие)", n)
	}

	// Страница события недоступна — ошибка прогона, а не неполный набор.
	list = listPage("5", "404")
	if docs, err := s.Fetch(ctx); err == nil || docs != nil || !strings.Contains(err.Error(), "событие 404") {
		t.Fatalf("Fetch с недоступным событием = %+v, %v", docs, err)
	}
	// Список без структуры — страницы событий не запрашиваются.
	list, before := []byte("<html></html>"), hits.Load()
	if _, err := s.Fetch(ctx); !errors.Is(err, ingest.ErrStructureNotFound) || hits.Load() != before+1 {
		t.Fatalf("Fetch без структуры: %v, запросов %d", err, hits.Load()-before)
	}
	// Список длиннее предела — ошибка до запросов событий.
	var many []string
	for i := range maxEvents + 1 {
		many = append(many, fmt.Sprint(1000+i))
	}
	list, before = listPage(many...), hits.Load()
	if _, err := s.Fetch(ctx); err == nil || hits.Load() != before+1 {
		t.Fatalf("Fetch длинного списка: %v, запросов %d", err, hits.Load()-before)
	}
	// Сетевая ошибка: сервер недоступен.
	srv.Close()
	if docs, err := s.Fetch(ctx); err == nil || docs != nil {
		t.Fatalf("Fetch без сервера = %+v, %v", docs, err)
	}
}

// fakeSource — запись другого источника для проверки дублей.
type fakeSource struct{ recs []ingest.SourceEvent }

func (f fakeSource) Code() string { return "probeg" }
func (f fakeSource) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback}
}
func (f fakeSource) Fetch(context.Context) ([]ingest.RawDoc, error) {
	return []ingest.RawDoc{{URL: "x"}}, nil
}
func (f fakeSource) Parse(ingest.RawDoc) ([]ingest.SourceEvent, error) { return f.recs, nil }

// Tracer: httptest → Fetch → Parse → Prepare → Run (suspect, каталог не
// тронут) → Accept → события в каталоге; повторный прогон событий не
// создаёт; дубль записи ПроБЕГа — кандидат в очереди, без слияния.
func TestTracerRun(t *testing.T) {
	st := storetest.Open(t)
	day := time.Now().AddDate(0, 0, 20)
	date := day.Format("2006-01-02") + "T11:00:00"
	pages := map[string][]byte{
		"/competition/":    listPage("11", "12"),
		"/competition/11/": eventPage("Гомельский полумарафон", date, "г. Гомель, ул. Набережная, 27", "<p>Дистанции: 7 км и 21,1 км</p>"),
		"/competition/12/": eventPage("Elk Trail «Золотые сады»", date, "озеро Белое, Полоцкий район", ""),
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(pages[r.URL.Path])
	}))
	defer srv.Close()
	src := testSource(srv.URL)

	ctx := context.Background()
	dcfg := dedup.DefaultConfig()
	if dcfg.AutoMerge {
		t.Fatal("автослияние по умолчанию должно быть выключено")
	}
	r := &ingest.Runner{Store: st, Now: time.Now, Fold: dcfg.Fold(),
		PostApply: func(ctx context.Context) error { return dedup.Pass(ctx, st, dcfg) }}

	// Событие ПроБЕГа уже в каталоге.
	pb := fakeSource{[]ingest.SourceEvent{{ExternalID: "83368", URL: "https://probeg.org/event/83368/", Title: "Гомельский полумарафон",
		Date: day.Format("02.01.2006"), Distances: "21098 м, 7 км, 1 км", City: "Гомель", Country: "Беларусь"}}}
	res, err := r.Run(ctx, pb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Accept(ctx, pb, res.RunID); err != nil {
		t.Fatal(err)
	}

	res, err = r.Run(ctx, src)
	if err != nil || res.Status != ingest.StatusSuspect || !slices.Contains(res.Reasons, "первый прогон источника — требуется принятие") {
		t.Fatalf("первый прогон = %+v, %v; want suspect", res, err)
	}
	if items, _ := st.ListEvents(ctx, event.Filter{}, 10, 0); len(items) != 1 {
		t.Fatalf("до принятия в каталоге %d событий, want 1 (только ПроБЕГ)", len(items))
	}
	acc, err := r.Accept(ctx, src, res.RunID)
	if err != nil || acc.Created != 2 {
		t.Fatalf("Accept = %+v, %v; want Created 2", acc, err)
	}

	// Повторный прогон: те же записи, новых событий нет.
	again, err := r.Run(ctx, src)
	if err != nil || again.Status != ingest.StatusOK || again.Created != 0 || again.Unchanged != 2 {
		t.Fatalf("повторный прогон = %+v, %v; want ok, Created 0, Unchanged 2", again, err)
	}
	if n := hits.Load(); n != 6 {
		t.Fatalf("запросов: %d, want 6 (два прогона по три)", n)
	}

	// Дубль не слит: три события в каталоге и один кандидат в очереди.
	items, err := st.ListEvents(ctx, event.Filter{}, 10, 0)
	if err != nil || len(items) != 3 {
		t.Fatalf("ListEvents = %d событий, %v; want 3", len(items), err)
	}
	pending, _, err := st.CandidateCounts(ctx)
	if err != nil || pending != 1 {
		t.Fatalf("кандидатов в очереди: %d, %v; want 1", pending, err)
	}
	for _, it := range items {
		e, err := st.GetEvent(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		srcs, err := st.EventSources(ctx, it.ID)
		if err != nil || len(srcs) != 1 || e.CountryCode != "BY" {
			t.Fatalf("%q: источники %+v, страна %q, %v", e.Title, srcs, e.CountryCode, err)
		}
		if srcs[0].Code != Code {
			continue
		}
		switch e.Title {
		case "Гомельский полумарафон":
			if srcs[0] != (store.EventSource{Code: Code, URL: "https://tristyle.club/competition/11/"}) || e.City != "Гомель" ||
				e.Timezone != "Europe/Minsk" || !e.StartDate.Equal(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)) ||
				!slices.Equal(e.Distances, []event.Distance{{Meters: 7000}, {Meters: 21100}}) {
				t.Errorf("Гомель: %+v, источник %+v", e, srcs[0])
			}
		case "Elk Trail «Золотые сады»":
			if e.City != "" || e.Address != "озеро Белое, Полоцкий район" || len(e.Distances) != 0 {
				t.Errorf("Elk Trail: %+v", e)
			}
		default:
			t.Errorf("неожиданное событие %q", e.Title)
		}
	}
	t.Logf("TRACER tristyle first=%s created=%d again=%s pending_candidates=%d", res.Status, acc.Created, again.Status, pending)
}
