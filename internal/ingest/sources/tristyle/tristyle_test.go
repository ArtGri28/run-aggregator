package tristyle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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
	// Источник с одним списком; бег и лыжи — skiSource, все списки — swimCycleSource.
	s.skiListURL, s.more = "", nil
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
		StartTime: "11:00",
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

// skiNow — день загрузки лыжных фикстур (testdata/README.md, раздел «Лыжи»).
var skiNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// skiIDs — события лыжного списка в порядке страницы.
var skiIDs = []string{"1178", "817", "818", "819", "820"}

// skiDoc — документ лыжного события: адрес с пометкой вида спорта.
func skiDoc(id string, body []byte) ingest.RawDoc {
	return ingest.RawDoc{URL: eventBase + id + "/#" + skiMark, Body: body, FetchedAt: skiNow}
}

func parseSki(t *testing.T, id string, body []byte) ingest.SourceEvent {
	t.Helper()
	recs, err := Parse(skiDoc(id, body))
	if err != nil || len(recs) != 1 {
		t.Fatalf("Parse(%s, лыжи) = %+v, %v; want 1 запись", id, recs, err)
	}
	return recs[0]
}

// skiSource — источник с обоими списками на тестовом сервере: адреса списков
// отличаются от настоящих только хостом.
func skiSource(srvURL string) *Source {
	s := testSource(srvURL)
	s.listURL = strings.Replace(ListURL, "https://tristyle.club", srvURL, 1)
	s.skiListURL = strings.Replace(SkiListURL, "https://tristyle.club", srvURL, 1)
	return s
}

// skiServer — сервер двух списков и страниц событий; hits считает запросы по
// адресу запроса целиком (путь и параметры). Список выбирается параметром
// specialization; nil — 404.
type skiServer struct {
	mu            sync.Mutex
	list, skiList []byte
	pages         map[string][]byte
	hits          map[string]int
}

func (s *skiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[r.URL.RequestURI()]++
	body := s.pages[r.URL.Path]
	if r.URL.Path == "/competition/" {
		switch r.URL.Query().Get("specialization") {
		case "running":
			body = s.list
		case "skiing":
			body = s.skiList
		}
	}
	if body == nil {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(body)
}

// total — число запросов с начала теста.
func (s *skiServer) total() (n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hits {
		n += h
	}
	return n
}

func dbText(t *testing.T, q string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var out string
	if err := conn.QueryRow(ctx, q).Scan(&out); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return out
}

// Tracer лыж: два списка → страница события один раз → запись skiing с
// дисциплиной → событие в каталоге → бот его не показывает, пока лыжи
// выключены в справочнике; беговая запись ключа sport не получает; повторный
// прогон ничего не меняет.
func TestSkiTracerRun(t *testing.T) {
	st := storetest.Open(t)
	srv := &skiServer{hits: map[string]int{}, list: listPage("11"), skiList: listPage("817"), pages: map[string][]byte{
		"/competition/11/":  eventPage("Гомельский полумарафон", "2026-10-24T11:00:00", "г. Гомель, ул. Набережная, 27", "<p>Дистанции: 7 км и 21,1 км</p>"),
		"/competition/817/": fixture(t, "event-817.html"),
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	src := skiSource(ts.URL)

	ctx := context.Background()
	dcfg := dedup.DefaultConfig()
	r := &ingest.Runner{Store: st, Now: func() time.Time { return skiNow }, Fold: dcfg.Fold(),
		PostApply: func(ctx context.Context) error { return dedup.Pass(ctx, st, dcfg) }}

	res, err := r.Run(ctx, src)
	if err != nil || res.Status != ingest.StatusSuspect {
		t.Fatalf("первый прогон = %+v, %v; want suspect", res, err)
	}
	// Каждая страница — один раз; в запросе страницы события нет ни параметров,
	// ни пометки вида спорта.
	const runList, skiList = "/competition/?q=&specialization=running&from=&to=&only_belarus=on", "/competition/?q=&specialization=skiing&from=&to=&only_belarus=on"
	wantHits := func(n int) {
		t.Helper()
		want := map[string]int{runList: n, skiList: n, "/competition/11/": n, "/competition/817/": n}
		if !reflect.DeepEqual(srv.hits, want) {
			t.Fatalf("запросы = %v, want %v", srv.hits, want)
		}
	}
	wantHits(1)
	acc, err := r.Accept(ctx, src, res.RunID)
	if err != nil || acc.Created != 2 {
		t.Fatalf("Accept = %+v, %v; want Created 2", acc, err)
	}

	if got, want := dbText(t, `SELECT string_agg(sport || ':' || event_type || ':' || title, '|' ORDER BY start_date) FROM events`),
		"running:other:Гомельский полумарафон|skiing:ski:V этап Кубка «Виктория» — лыжные гонки"; got != want {
		t.Fatalf("события =\n%s\nwant\n%s", got, want)
	}
	if !slices.Contains(event.SportTypes[event.SportSkiing], event.TypeSki) {
		t.Fatal("дисциплина ski не в перечне лыж")
	}
	// Ключ sport — только у лыжной записи.
	if got, want := dbText(t, `SELECT string_agg(e.sport || ':' || (se.payload ? 'sport') || ':' || (se.fields ? 'sport'), '|' ORDER BY e.start_date)
		FROM source_events se JOIN events e ON e.id = se.event_id`), "running:false:false|skiing:true:true"; got != want {
		t.Fatalf("ключ sport в payload и fields = %s, want %s", got, want)
	}

	// Бот видит лыжное событие тогда и только тогда, когда лыжи включены.
	ski, _ := event.SportByCode(event.SportSkiing)
	want := 1
	if ski.Enabled {
		want = 2
	}
	if n, err := st.CountEvents(ctx, event.Filter{}); err != nil || n != want {
		t.Fatalf("CountEvents = %d, %v; want %d (лыжи включены: %v)", n, err, want, ski.Enabled)
	}

	again, err := r.Run(ctx, src)
	if err != nil || again.Status != ingest.StatusOK || again.Created != 0 || again.Changed != 0 || again.Unchanged != 2 {
		t.Fatalf("повторный прогон = %+v, %v; want ok, Created 0, Changed 0, Unchanged 2", again, err)
	}
	wantHits(2)
	t.Logf("TRACER ski=stored visible=%v rerun=unchanged", ski.Enabled)
}

func TestSkiFetch(t *testing.T) {
	page := func(path string) []byte { return []byte("<html>" + path + "</html>") }
	srv := &skiServer{hits: map[string]int{}, pages: map[string][]byte{}}
	for _, id := range []string{"5", "6", "7", "8"} {
		srv.pages["/competition/"+id+"/"] = page(id)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	s := skiSource(ts.URL)
	ctx := context.Background()
	urls := func(docs []ingest.RawDoc) (out []string) {
		for _, d := range docs {
			out = append(out, strings.TrimPrefix(d.URL, ts.URL))
		}
		return out
	}
	const runList, skiList = "/competition/?q=&specialization=running&from=&to=&only_belarus=on", "/competition/?q=&specialization=skiing&from=&to=&only_belarus=on"

	// Номер 6 есть в обоих списках: запрошен один раз и остаётся беговым.
	srv.list, srv.skiList = listPage("5", "6"), listPage("6", "7", "8")
	docs, err := s.Fetch(ctx)
	want := []string{runList, skiList, "/competition/5/", "/competition/6/", "/competition/7/#skiing", "/competition/8/#skiing"}
	if err != nil || !slices.Equal(urls(docs), want) {
		t.Fatalf("Fetch = %v, %v; want %v", urls(docs), err, want)
	}
	if want := map[string]int{runList: 1, skiList: 1, "/competition/5/": 1, "/competition/6/": 1, "/competition/7/": 1, "/competition/8/": 1}; !reflect.DeepEqual(srv.hits, want) {
		t.Fatalf("запросы = %v, want %v", srv.hits, want)
	}
	// Пометка доходит до Parse: тело страницы 7 — в документе с фрагментом.
	if string(docs[4].Body) != string(page("7")) {
		t.Fatalf("документ лыжного события: %q", docs[4].Body)
	}

	// Пустой лыжный список: прежний набор и документ лыжного списка.
	srv.skiList = listPage()
	docs, err = s.Fetch(ctx)
	if want := []string{runList, skiList, "/competition/5/", "/competition/6/"}; err != nil || !slices.Equal(urls(docs), want) {
		t.Fatalf("Fetch с пустым лыжным списком = %v, %v; want %v", urls(docs), err, want)
	}

	// Ошибки — ошибка прогона, а не неполный набор: лыжный список недоступен,
	// без структуры, страница лыжного события недоступна.
	for name, skiList := range map[string][]byte{
		"лыжный список недоступен":    nil,
		"лыжный список без структуры": []byte("<html></html>"),
		"событие недоступно":          listPage("404"),
	} {
		srv.skiList = skiList
		if docs, err := s.Fetch(ctx); err == nil || docs != nil {
			t.Errorf("%s: Fetch = %v, %v; want ошибку", name, urls(docs), err)
		}
	}
	srv.skiList = []byte("<html></html>")
	if _, err := s.Fetch(ctx); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Errorf("лыжный список без структуры: %v, want ErrStructureNotFound", err)
	}

	// Предел — на уникальные номера обоих списков: ровно maxEvents проходит
	// (один номер общий), на один больше — ошибка до запросов событий.
	var run, ski []string
	for i := range 40 {
		run = append(run, fmt.Sprint(1000+i))
		srv.pages["/competition/"+run[i]+"/"] = page(run[i])
	}
	for i := range maxEvents - 40 + 1 {
		ski = append(ski, fmt.Sprint(1039+i))
		srv.pages["/competition/"+ski[i]+"/"] = page(ski[i])
	}
	srv.list, srv.skiList = listPage(run...), listPage(ski...)
	if docs, err := s.Fetch(ctx); err != nil || len(docs) != 2+maxEvents {
		t.Fatalf("Fetch на пределе: %d документов, %v; want %d", len(docs), err, 2+maxEvents)
	}
	srv.skiList = listPage(append(ski, "2000")...)
	before := srv.total()
	if _, err := s.Fetch(ctx); err == nil || srv.total() != before+2 {
		t.Fatalf("Fetch сверх предела: %v, запросов %d; want ошибку и 2 запроса списков", err, srv.total()-before)
	}
}

func TestParseSkiFixtures(t *testing.T) {
	list := fixture(t, "list-skiing.html")
	if ids, err := listIDs(list); err != nil || !slices.Equal(ids, skiIDs) {
		t.Fatalf("listIDs = %v, %v; want %v", ids, err, skiIDs)
	}
	if recs, err := Parse(ingest.RawDoc{URL: SkiListURL, Body: list}); err != nil || recs != nil {
		t.Fatalf("Parse(лыжный список) = %+v, %v; want без записей и ошибки", recs, err)
	}
	if recs, err := Parse(ingest.RawDoc{URL: SkiListURL, Body: []byte("<html><body><p>Технические работы</p></body></html>")}); !errors.Is(err, ingest.ErrStructureNotFound) || recs != nil {
		t.Fatalf("Parse(лыжный список без блока) = %+v, %v; want ErrStructureNotFound", recs, err)
	}

	var recs []ingest.SourceEvent
	for _, id := range skiIDs {
		recs = append(recs, parseSki(t, id, fixture(t, "event-"+id+".html")))
	}
	// Эталон составлен по таблице фактов README и разделу «Данные» контракта,
	// а не выводом разбора: расхождение — ошибка разбора либо таблицы.
	got, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if want := fixture(t, "skiing.golden.json"); !bytes.Equal(append(got, '\n'), want) {
		t.Errorf("расхождение с testdata/skiing.golden.json:\n%s", got)
	}

	// Дистанции после подготовки — раздел «Данные» контракта, всего 11.
	wantKm := map[string][]int{"1178": {15, 30, 45}, "817": {5, 10}, "818": {5, 10}, "819": {10, 15}, "820": {20, 30}}
	wantType := map[string]event.Type{"1178": event.TypeRollerski, "817": event.TypeSki, "818": event.TypeSki, "819": event.TypeSki, "820": event.TypeSki}
	prep, st := ingest.Prepare(recs, skiNow, New(nil).Policy())
	if st.Valid != 5 || st.OutOfScope != 0 || st.Rejected != 0 {
		t.Fatalf("Prepare: %+v", st)
	}
	total := 0
	for _, p := range prep {
		id, e := p.Source.ExternalID, p.Event
		var km []int
		for _, d := range e.Distances {
			km = append(km, d.Meters/1000)
		}
		total += len(km)
		marked := strings.HasPrefix(e.Title, "(дата уточняется) ")
		if !slices.Equal(km, wantKm[id]) || e.Type != wantType[id] || e.Sport != event.SportSkiing || e.CountryCode != "BY" ||
			marked != slices.Contains([]string{"818", "819", "820"}, id) || strings.Count(e.Title, "дата уточняется") > 1 {
			t.Errorf("%s: %q, дистанции %v км (want %v), дисциплина %s (want %s), вид спорта %s, страна %s", id, e.Title, km, wantKm[id], e.Type, wantType[id], e.Sport, e.CountryCode)
		}
		t.Logf("SKI %s: %q date=%s city=%q km=%v type=%s", id, e.Title, e.StartDate.Format(time.DateOnly), e.City, km, e.Type)
	}
	if total != 11 {
		t.Errorf("дистанций %d, want 11", total)
	}

	// Отменённое событие записи не даёт.
	if recs, err := Parse(skiDoc("7", eventPage("Лыжная гонка отменена", "2026-12-19T12:00:00", "Минск", ""))); err != nil || recs != nil {
		t.Errorf("отменённое: Parse = %+v, %v; want без записей", recs, err)
	}
}

// D-22: перечисление с общей единицей раскрывается только у лыжного документа.
func TestSkiDistanceList(t *testing.T) {
	for _, c := range []struct {
		in, want string
		meters   []int
	}{
		{"15, 30 и 45 км.", "15 км, 30 км и 45 км.", []int{15000, 30000, 45000}},
		{"5 и 10 км свободным стилем.", "5 км и 10 км свободным стилем.", []int{5000, 10000}},
		{"7,5 и 15 км", "7,5 км и 15 км", []int{7500, 15000}},
		{"200, 400 и 800 м", "200 м, 400 м и 800 м", []int{200, 400, 800}},
		// Свои единицы, одно число и строка без чисел не меняются.
		{"5 км, 10 км и 21,1 км", "5 км, 10 км и 21,1 км", []int{5000, 10000, 21100}},
		{"500 м и 5 км", "500 м и 5 км", []int{500, 5000}},
		{"30 км", "30 км", []int{30000}},
		{"классика и конёк", "классика и конёк", nil},
		{"", "", nil},
		// Единица — не начало слова: «5 и 10 минут» не дистанции.
		{"5 и 10 минут", "5 и 10 минут", nil},
	} {
		if got := expandList(c.in); got != c.want || !slices.Equal(normalize.Distances(got), c.meters) {
			t.Errorf("expandList(%q) = %q → %v; want %q → %v", c.in, got, normalize.Distances(got), c.want, c.meters)
		}
	}

	// Одна и та же страница: у лыжного документа перечисление раскрыто, у
	// бегового (без пометки вида спорта) строка прежняя.
	body := eventPage("Гонка", "2026-12-19T12:00:00", "Минск", "<p>Дистанции: 15, 30 и 45 км.</p>")
	if got := parseSki(t, "7", body).Distances; got != "15 км, 30 км и 45 км." {
		t.Errorf("лыжный документ: distances = %q", got)
	}
	if got := parseOne(t, "7", body).Distances; got != "15, 30 и 45 км." {
		t.Errorf("беговой документ: distances = %q, want прежняя строка", got)
	}
}

// D-23, D-30: пометка даты-заглушки — в начале названия, только у лыжного
// документа.
func TestSkiDatePlaceholder(t *testing.T) {
	const mark = "(дата уточняется) "
	for _, c := range []struct {
		name, title, date, text string
		want                    string
	}{
		{"назван только месяц", "Этап кубка", "2027-01-01T12:00:00", "<p>📅 январь 2027 г.</p>", mark + "Этап кубка"},
		{"день назван", "Этап кубка", "2027-01-01T12:00:00", "<p>📅 1 января 2027 г.</p>", "Этап кубка"},
		{"день с нулём", "Этап кубка", "2027-01-01T12:00:00", "<p>Старт 01 Января в 12:00</p>", "Этап кубка"},
		{"другой день месяца", "Этап кубка", "2027-01-01T12:00:00", "<p>11 января 2027 г.</p>", mark + "Этап кубка"},
		{"день другого месяца", "Этап кубка", "2027-01-01T12:00:00", "<p>Заявки до 1 декабря</p>", mark + "Этап кубка"},
		{"не первое число", "Этап кубка", "2026-12-19T12:00:00", "<p>декабрь 2026 г.</p>", "Этап кубка"},
		{"первое число, дат в тексте нет", "Этап кубка", "2027-02-01T12:00:00", "<p>Дистанции: 10 км</p>", mark + "Этап кубка"},
		{"пометка уже в названии", "Этап кубка (дата уточняется)", "2027-01-01T12:00:00", "<p>январь 2027 г.</p>", "Этап кубка (дата уточняется)"},
		{"неразборчивая дата", "Этап кубка", "скоро", "", "Этап кубка"},
	} {
		body := eventPage(c.title, c.date, "Минск", c.text)
		if got := parseSki(t, "7", body).Title; got != c.want {
			t.Errorf("%s: название %q, want %q", c.name, got, c.want)
		}
		// Беговой документ пометку не получает никогда.
		if got := parseOne(t, "7", body).Title; got != c.title {
			t.Errorf("%s: беговое название %q, want %q", c.name, got, c.title)
		}
	}
}

// Время старта — из ссылки «в календарь»; 00:00 и 12:00 — заглушки сайта
// (22-CONTEXT.md, D-03).
func TestStartTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-24T11:30:00": "11:30",
		"2026-12-01T07:00":    "07:00",
		"2026-11-01T00:00:00": "",
		"2026-10-25T12:00:00": "",
		"2026-10-25":          "",
		"":                    "",
	} {
		if got := startTime(in); got != want {
			t.Errorf("startTime(%q) = %q, want %q", in, got, want)
		}
	}
}
