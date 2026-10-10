package webapp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/marathonec"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
	"aggregator/web"
)

// Тесты API на фикстуре Марафонца (как internal/bot/bot_test.go): «сегодня»
// 2026-09-29 по Москве, 133 беговых события России.

var fixtureNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

var fixtureToday = time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)

const calendarFixture = "../ingest/sources/marathonec/testdata/calendar-beg.html"

type fixtureSource struct{ body []byte }

func (s fixtureSource) Code() string          { return marathonec.Code }
func (s fixtureSource) Policy() ingest.Policy { return marathonec.New(nil).Policy() }
func (s fixtureSource) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	return marathonec.Parse(doc)
}
func (s fixtureSource) Fetch(context.Context) ([]ingest.RawDoc, error) {
	return []ingest.RawDoc{{URL: marathonec.CalendarURL, Body: s.body, FetchedAt: fixtureNow}}, nil
}

func seedMarathonec(t *testing.T, s *store.Store) {
	t.Helper()
	body, err := os.ReadFile(calendarFixture)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	src := fixtureSource{body: body}
	r := &ingest.Runner{Store: s, Now: func() time.Time { return fixtureNow }}
	if _, err := r.Run(ctx, src); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res, err := r.Accept(ctx, src, 0); err != nil || res.Created <= 100 {
		t.Fatalf("Accept: created=%d err=%v, want > 100", res.Created, err)
	}
}

func dbExec(t *testing.T, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}

// rig — сервер API над тестовой базой и его лог.
type rig struct {
	t    *testing.T
	s    *store.Store
	srv  *httptest.Server
	logs *bytes.Buffer
}

func newRig(t *testing.T, s *store.Store) *rig {
	t.Helper()
	var logs bytes.Buffer
	h := New(s, SecretFromToken(testToken), func() time.Time { return fixtureNow }, slog.New(slog.NewTextHandler(&logs, nil)))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &rig{t: t, s: s, srv: srv, logs: &logs}
}

// auth — заголовок Authorization пользователя tgID.
func (r *rig) auth(tgID int64) string {
	return "tma " + sign(r.t, testToken, fields(fixtureNow, `{"id":`+strconv.FormatInt(tgID, 10)+`}`))
}

// get выполняет запрос с заголовком auth ("" — без заголовка).
func (r *rig) get(path, auth string) (*http.Response, []byte) {
	r.t.Helper()
	return r.do(http.MethodGet, path, auth, "")
}

// do — запрос методом method с телом body ("" — без тела).
func (r *rig) do(method, path, auth, body string) (*http.Response, []byte) {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.srv.URL+path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// put — PUT /favorites/<id> пользователя tgID: код ответа и исход либо код ошибки.
func (r *rig) put(tgID int64, id string) (int, string) {
	r.t.Helper()
	resp, body := r.do(http.MethodPut, "/api/v1/favorites/"+id, r.auth(tgID), "")
	var v struct{ Outcome, Error string }
	if err := json.Unmarshal(body, &v); err != nil {
		r.t.Fatalf("PUT favorites/%s: %v: %s", id, err, body)
	}
	if resp.StatusCode == http.StatusOK && resp.Header.Get("Cache-Control") != noStore {
		r.t.Fatalf("PUT favorites/%s: Cache-Control = %q", id, resp.Header.Get("Cache-Control"))
	}
	return resp.StatusCode, v.Outcome + v.Error
}

// del — DELETE /favorites/<id> пользователя tgID: всегда 204 без тела.
func (r *rig) del(tgID int64, id string) {
	r.t.Helper()
	resp, body := r.do(http.MethodDelete, "/api/v1/favorites/"+id, r.auth(tgID), "")
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 || strings.Contains(resp.Header.Get("Cache-Control"), "max-age") {
		r.t.Fatalf("DELETE favorites/%s: code=%d body=%q Cache-Control=%q, want 204 без тела", id, resp.StatusCode, body, resp.Header.Get("Cache-Control"))
	}
}

// favIDs — идентификаторы избранного пользователя tgID через запятую и total.
func (r *rig) favIDs(tgID int64, query string) (string, int) {
	r.t.Helper()
	var l favListJSON
	resp := r.getJSON(tgID, "/api/v1/favorites"+query, http.StatusOK, &l)
	if cc := resp.Header.Get("Cache-Control"); cc != noStore {
		r.t.Fatalf("GET favorites: Cache-Control = %q", cc)
	}
	ids := make([]string, 0, len(l.Items))
	for _, it := range l.Items {
		ids = append(ids, strconv.FormatInt(it.ID, 10))
	}
	return strings.Join(ids, ","), l.Total
}

// dbCount — число из запроса count(*) отдельным соединением.
func dbCount(t *testing.T, q string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// userRows — число строк users и user_profiles: Mini App их не создаёт (D-10).
func userRows(t *testing.T) [2]int {
	t.Helper()
	return [2]int{dbCount(t, `SELECT count(*) FROM users`), dbCount(t, `SELECT count(*) FROM user_profiles`)}
}

// getJSON — запрос пользователя tgID, ждёт код want; тело разбирается в v.
func (r *rig) getJSON(tgID int64, path string, want int, v any) *http.Response {
	r.t.Helper()
	resp, body := r.get(path, r.auth(tgID))
	if resp.StatusCode != want {
		r.t.Fatalf("GET %s: code=%d body=%s, want %d", path, resp.StatusCode, body, want)
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			r.t.Fatalf("GET %s: %v: %s", path, err, body)
		}
	}
	return resp
}

// errCode — код ошибки ответа {"error":…} при ожидаемом статусе.
func (r *rig) errCode(tgID int64, path string, want int) string {
	r.t.Helper()
	var e struct{ Error string }
	r.getJSON(tgID, path, want, &e)
	return e.Error
}

// count — CountEvents напрямую: с ним сравнивается total API.
func (r *rig) count(f event.Filter) int {
	r.t.Helper()
	n, err := r.s.CountEvents(context.Background(), f)
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

func TestAuthRequired(t *testing.T) {
	r := newRig(t, nil) // до обработчиков запрос не доходит
	for _, auth := range []string{"", "Bearer " + r.auth(1)[4:]} {
		if resp, _ := r.get("/api/v1/meta", auth); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%q: code=%d, want 401", auth, resp.StatusCode)
		}
	}
	if code := r.errCode(1, "/api/v1/nope", http.StatusNotFound); code != "not_found" {
		t.Fatalf("неизвестный путь: %q, want not_found", code)
	}
	// Статика без заголовка — 200; API статикой не перекрыт.
	if resp, _ := r.get("/", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("корень: code=%d, want 200", resp.StatusCode)
	}
	if resp, _ := r.get("/api/v1/nope", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/v1/nope без заголовка: code=%d, want 401", resp.StatusCode)
	}
}

// secretLikeRE — строки, похожие на ключ или токен бота.
var secretLikeRE = regexp.MustCompile(`[0-9a-fA-F]{32,}|\d{6,}:[A-Za-z0-9_-]{30,}`)

func TestStatic(t *testing.T) {
	r := newRig(t, nil) // до базы статика не доходит
	resp, body := r.get("/", "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /: code=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{`lang="ru"`, "telegram-web-app.js", `type="module"`, "app.js", `name="viewport"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("index.html без %q:\n%s", want, body)
		}
	}
	if m := secretLikeRE.FindString(string(body)); m != "" {
		t.Fatalf("index.html: строка, похожая на ключ: %q", m)
	}
	for k, want := range map[string]string{
		"Content-Security-Policy": csp,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           staticCacheControl,
	} {
		if got := resp.Header.Get(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("csp = %q", csp)
	}

	// Файлы с правильным типом; неизвестный путь — index.html (hash-маршруты).
	resp, body = r.get("/app.js", "")
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || !(strings.HasPrefix(ct, "application/javascript") || strings.HasPrefix(ct, "text/javascript")) {
		t.Fatalf("GET /app.js: code=%d type=%q", resp.StatusCode, ct)
	}
	if resp.Header.Get("Cache-Control") != staticCacheControl || !strings.Contains(string(body), "hashchange") {
		t.Fatalf("GET /app.js: Cache-Control=%q body=%q", resp.Header.Get("Cache-Control"), body[:min(len(body), 80)])
	}
	resp, _ = r.get("/app.css", "")
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("GET /app.css: code=%d type=%q", resp.StatusCode, ct)
	}
	index, _ := fs.ReadFile(web.FS, "static/index.html")
	for _, p := range []string{"/nope", "/event/5", "/catalog/", "/screens/", "/screens"} {
		resp, body = r.get(p, "")
		if resp.StatusCode != http.StatusOK || !bytes.Equal(body, index) || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("GET %s: code=%d type=%q, want index.html", p, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	// Лог статики — в том же формате, что у API, без пути запроса.
	if logs := r.logs.String(); !strings.Contains(logs, `route=/ status=200`) || strings.Contains(logs, "/nope") {
		t.Fatalf("лог статики:\n%s", logs)
	}
}

// TestStaticSources — состав и гигиена файлов web/static: только ожидаемые
// типы; в скриптах нет innerHTML/eval, незащищённых ссылок и строк, похожих
// на ключи; localStorage — только в screens/filters.js (D-10).
func TestStaticSources(t *testing.T) {
	allowed := map[string]bool{".html": true, ".css": true, ".js": true, ".svg": true}
	forbidden := []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "http://", "initDataUnsafe"}
	var files int
	err := fs.WalkDir(web.FS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		if !allowed[path.Ext(p)] {
			t.Errorf("%s: неожиданный тип файла", p)
		}
		src, err := fs.ReadFile(web.FS, p)
		if err != nil {
			return err
		}
		text := string(src)
		if m := secretLikeRE.FindString(text); m != "" {
			t.Errorf("%s: строка, похожая на ключ: %q", p, m)
		}
		if path.Ext(p) != ".js" {
			return nil
		}
		// Единственный допустимый «http://» — пространство имён SVG для createElementNS.
		scan := strings.ReplaceAll(text, "http://www.w3.org/2000/svg", "")
		for _, bad := range forbidden {
			if strings.Contains(scan, bad) {
				t.Errorf("%s: содержит %q", p, bad)
			}
		}
		if strings.Contains(text, "localStorage") && p != "static/screens/filters.js" {
			t.Errorf("%s: localStorage вне screens/filters.js", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 13 {
		t.Fatalf("файлов статики %d, want ≥ 13", files)
	}
	for _, p := range []string{"static/screens/favorites.js", "static/screens/profile.js", "static/screens/calendar.js"} {
		if _, err := fs.Stat(web.FS, p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestMeta(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	var m metaJSON
	resp := r.getJSON(2, "/api/v1/meta", http.StatusOK, &m)
	if cc := resp.Header.Get("Cache-Control"); cc != cacheControl {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if m.Today != "2026-09-29" {
		t.Fatalf("today = %q", m.Today)
	}
	var codes []string
	for _, sp := range m.Sports {
		codes = append(codes, string(sp.Code))
	}
	if strings.Join(codes, ",") != "running,skiing" {
		t.Fatalf("sports = %v, want оба включённых вида спорта", codes)
	}
	if m.Sports[0].Count != r.count(event.Filter{From: fixtureToday, Sport: event.SportRunning}) || m.Sports[0].Count == 0 {
		t.Fatalf("running count = %d", m.Sports[0].Count)
	}
	if len(m.Buckets["running"]) != 4 || m.Buckets["running"][1].Label != "10–21 км" || m.Buckets["running"][1].Lo != 10000 || m.Buckets["running"][1].Hi != 21100 {
		t.Fatalf("buckets.running = %+v", m.Buckets["running"])
	}
	if len(m.Buckets["skiing"]) != 4 || m.Buckets["skiing"][0].Hi != 14999 {
		t.Fatalf("buckets.skiing = %+v", m.Buckets["skiing"])
	}
	if len(m.Types["running"]) != 5 || m.Types["running"][0] != (typeJSON{"road", "Шоссе"}) || len(m.Types["skiing"]) != 2 {
		t.Fatalf("types = %+v", m.Types)
	}
	if len(m.Countries) != 1 || m.Countries[0] != (countryJSON{"RU", "Россия", r.count(event.Filter{From: fixtureToday})}) {
		t.Fatalf("countries = %+v", m.Countries)
	}
	if len(m.Months) != 12 || m.Months[0] != (optionJSON{"m202609", "сентябрь 2026"}) {
		t.Fatalf("months = %+v", m.Months)
	}
	if m.Limits != (limitsJSON{20, 50}) {
		t.Fatalf("limits = %+v", m.Limits)
	}
}

func TestEvents(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	const user = 3
	ru := event.Filter{From: fixtureToday, CountryCode: "RU"}

	// Страна по умолчанию RU, страница 20; вторая страница — другие события.
	var p1, p2 listJSON
	r.getJSON(user, "/api/v1/events", http.StatusOK, &p1)
	if want := r.count(ru); p1.Total != want || p1.Page != 1 || p1.Pages != (want+19)/20 || len(p1.Items) != 20 {
		t.Fatalf("page 1: total=%d page=%d pages=%d items=%d, want total %d", p1.Total, p1.Page, p1.Pages, len(p1.Items), want)
	}
	if it := p1.Items[0]; it.ID == 0 || it.Title == "" || it.StartDate < "2026-09-29" || it.CountryCode != "RU" || it.Sport != "running" || it.Distances == nil {
		t.Fatalf("item = %+v", it)
	}
	r.getJSON(user, "/api/v1/events?page=2&limit=5", http.StatusOK, &p2)
	if p2.Page != 2 || p2.Pages != (p1.Total+4)/5 || len(p2.Items) != 5 || p2.Items[0].ID != p1.Items[5].ID {
		t.Fatalf("page 2 limit 5: %+v", p2)
	}
	// Последняя страница за пределом — пусто, но не ошибка.
	r.getJSON(user, "/api/v1/events?page=10000", http.StatusOK, &p2)
	if len(p2.Items) != 0 || p2.Total != p1.Total {
		t.Fatalf("page 10000: %+v", p2)
	}

	// Неверные параметры → 400 bad_request.
	for _, q := range []string{"limit=51", "limit=0", "page=10001", "page=x", "country=*&city=moscow", "country=ru", "sport=swimming", "sport=football",
		"dist=0", "sport=running&dist=4", "sport=running&dist=a", "type=football", "sport=skiing&type=road", "periods=xx", "periods=cm&from=2026-10-01", "from=2026-13-01", "to=01.10.2026", "city=Moscow"} {
		if code := r.errCode(user, "/api/v1/events?"+q, http.StatusBadRequest); code != "bad_request" {
			t.Fatalf("%s: %q", q, code)
		}
	}

	// Фильтры: total равен CountEvents при том же event.Filter.
	all := event.Filter{From: fixtureToday}
	cm := event.DateRange{From: fixtureToday, To: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	nm := event.DateRange{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)}
	for q, f := range map[string]event.Filter{
		"country=*":                     all,
		"country=RU&city=moscow":        {From: fixtureToday, CountryCode: "RU", CityKey: "moscow"},
		"city=moscow":                   {From: fixtureToday, CountryCode: "RU", CityKey: "moscow"},
		"sport=running&dist=0":          {From: fixtureToday, CountryCode: "RU", Sport: "running", Distances: []event.DistanceRange{{Lo: 0, Hi: 10000}}},
		"sport=running&dist=2,3":        {From: fixtureToday, CountryCode: "RU", Sport: "running", Distances: []event.DistanceRange{{Lo: 21100, Hi: 42200}, {Lo: 42200, Hi: 2147483647}}},
		"type=trail":                    {From: fixtureToday, CountryCode: "RU", Type: event.TypeTrail},
		"periods=cm":                    {From: fixtureToday, CountryCode: "RU", Periods: []event.DateRange{cm}},
		"periods=cm,nm":                 {From: fixtureToday, CountryCode: "RU", Periods: []event.DateRange{cm, nm}},
		"periods=m202611":               {From: fixtureToday, CountryCode: "RU", Periods: []event.DateRange{{From: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)}}},
		"from=2026-10-01&to=2026-10-31": {From: nm.From, To: nm.To, CountryCode: "RU"},
		"from=2020-01-01":               ru, // не раньше «сегодня»
		"country=BY":                    {From: fixtureToday, CountryCode: "BY"},
	} {
		var p listJSON
		r.getJSON(user, "/api/v1/events?"+q, http.StatusOK, &p)
		if want := r.count(f); p.Total != want {
			t.Fatalf("%s: total=%d, want %d", q, p.Total, want)
		}
		if q == "country=RU&city=moscow" && (p.Total == 0 || p.Total == r.count(ru)) {
			t.Fatalf("город не отфильтровал: %d", p.Total)
		}
		if q == "periods=cm,nm" && (p.Total == 0 || p.Total == r.count(ru)) {
			t.Fatalf("период не отфильтровал: %d", p.Total)
		}
	}
}

func TestEventCard(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	ctx := context.Background()
	const user, other = 4, 5
	dbExec(t, `DELETE FROM favorites`)

	// Со ссылкой регистрации и без неё — по данным фикстуры.
	items, err := s.ListEvents(ctx, event.Filter{From: fixtureToday}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var withReg, noReg event.Event
	for _, it := range items {
		e, err := s.GetEvent(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if event.ValidURL(e.RegistrationURL) && withReg.ID == 0 {
			withReg = e
		}
		if e.RegistrationURL == "" && noReg.ID == 0 {
			noReg = e
		}
	}
	if withReg.ID == 0 || noReg.ID == 0 {
		t.Fatal("в фикстуре нет событий со ссылкой и без неё")
	}
	// card — карточка свежей структурой: пропущенные поля не наследуются.
	card := func(tgID, id int64) cardJSON {
		var c cardJSON
		r.getJSON(tgID, "/api/v1/events/"+strconv.FormatInt(id, 10), http.StatusOK, &c)
		return c
	}
	c := card(user, withReg.ID)
	if c.ID != withReg.ID || c.Title != withReg.Title || c.RegistrationURL != withReg.RegistrationURL || c.StartDate != withReg.StartDate.Format(time.DateOnly) ||
		c.Sport != "running" || c.TypeLabel == "" || c.CountryName != "Россия" || c.IsFavorite || len(c.Sources) != 1 || c.Sources[0].Name != "Марафонец" || c.Sources[0].URL == "" {
		t.Fatalf("card = %+v", c)
	}
	if len(c.Distances) != len(withReg.Distances) || c.Distances[0].M != withReg.Distances[0].Meters {
		t.Fatalf("distances = %+v, want %+v", c.Distances, withReg.Distances)
	}
	raw, _ := json.Marshal(c)
	if !bytes.Contains(raw, []byte(`"registration_url"`)) || !bytes.Contains(raw, []byte(`"end_date"`)) == (withReg.EndDate != nil) {
		t.Fatalf("json = %s", raw)
	}
	// Без ссылки — поля нет; негодная ссылка — тоже.
	if c = card(user, noReg.ID); c.RegistrationURL != "" {
		t.Fatalf("registration_url = %q, want пусто", c.RegistrationURL)
	}
	dbExec(t, `UPDATE events SET registration_url = 'javascript:alert(1)' WHERE id = $1`, noReg.ID)
	c = card(user, noReg.ID)
	if raw, _ = json.Marshal(c); c.RegistrationURL != "" || bytes.Contains(raw, []byte("javascript")) {
		t.Fatalf("негодная ссылка ушла клиенту: %s", raw)
	}

	// Адрес — через VisibleAddress: без частей из одного района.
	dbExec(t, `UPDATE events SET address = 'Одинцовский район, ул. Ленина, 5' WHERE id = $1`, noReg.ID)
	if c = card(user, noReg.ID); c.Address != "ул. Ленина, 5" {
		t.Fatalf("address = %q", c.Address)
	}

	// Избранное — состояние пользователя запроса.
	if out, err := s.AddFavorite(ctx, user, withReg.ID, fixtureToday, fixtureNow, 50); err != nil || out != store.FavAdded {
		t.Fatalf("AddFavorite: %v %v", out, err)
	}
	if c = card(user, withReg.ID); !c.IsFavorite {
		t.Fatal("is_favorite = false после AddFavorite")
	}
	if c = card(other, withReg.ID); c.IsFavorite {
		t.Fatal("is_favorite = true у другого пользователя")
	}

	// Слитое → корень; скрытое, несуществующее → 404 gone; не число → 400.
	merged, hidden := items[10].ID, items[11].ID
	storetest.MergeEvent(t, merged, withReg.ID)
	if c = card(user, merged); c.ID != withReg.ID {
		t.Fatalf("слитое: id=%d, want корень %d", c.ID, withReg.ID)
	}
	storetest.HideEvent(t, hidden)
	for _, id := range []string{strconv.FormatInt(hidden, 10), "999999"} {
		if code := r.errCode(user, "/api/v1/events/"+id, http.StatusNotFound); code != "gone" {
			t.Fatalf("%s: %q, want gone", id, code)
		}
	}
	if code := r.errCode(user, "/api/v1/events/abc", http.StatusBadRequest); code != "bad_request" {
		t.Fatalf("abc: %q", code)
	}
	// Выключенный вид спорта — gone.
	storetest.DisableSport(t, event.SportRunning)
	if code := r.errCode(user, "/api/v1/events/"+strconv.FormatInt(withReg.ID, 10), http.StatusNotFound); code != "gone" {
		t.Fatalf("выключенный вид спорта: %q, want gone", code)
	}
}

func TestCities(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	const user = 6
	var cities []cityJSON
	r.getJSON(user, "/api/v1/cities?limit=3", http.StatusOK, &cities)
	want, err := s.TopCities(context.Background(), event.Filter{From: fixtureToday, CountryCode: "RU"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(cities) != 3 || cities[0] != (cityJSON{want[0].Key, want[0].Name, want[0].N}) || cities[0].Key != "moscow" {
		t.Fatalf("cities = %+v, want %+v", cities, want)
	}
	if code := r.errCode(user, "/api/v1/cities?limit=51", http.StatusBadRequest); code != "bad_request" {
		t.Fatalf("limit 51: %q", code)
	}

	r.getJSON(user, "/api/v1/cities/suggest?q=Моск", http.StatusOK, &cities)
	if len(cities) == 0 || cities[0].Key != "moscow" {
		t.Fatalf("suggest Моск = %+v", cities)
	}
	r.getJSON(user, "/api/v1/cities/suggest?q=zzzzzz", http.StatusOK, &cities)
	if len(cities) != 0 {
		t.Fatalf("suggest zzzzzz = %+v, want пусто", cities)
	}
	for _, q := range []string{"q=" + strings.Repeat("а", 65), "q=", "q=Моск&country=ru"} {
		if code := r.errCode(user, "/api/v1/cities/suggest?"+q, http.StatusBadRequest); code != "bad_request" {
			t.Fatalf("%s: %q", q, code)
		}
	}
	r.getJSON(user, "/api/v1/cities/suggest?q="+strings.Repeat("а", 64), http.StatusOK, &cities)

	// Приватность лога (D-12): есть строки запросов с методом, маршрутом и
	// кодом; нет tg_id, текста q, initData и заголовка авторизации.
	logs := r.logs.String()
	if !strings.Contains(logs, `route="GET /api/v1/cities/suggest" status=400`) || !strings.Contains(logs, "method=GET") {
		t.Fatalf("в логе нет строк запросов:\n%s", logs)
	}
	for _, bad := range []string{"tg_id", "Моск", "zzzzzz", "initData", "Authorization", "auth_date", "hash=", "q="} {
		if strings.Contains(logs, bad) {
			t.Fatalf("в логе есть %q:\n%s", bad, logs)
		}
	}
}

// TestCalendar — счётчики по дням согласованы с events при тех же параметрах:
// сумма равна total, счётчик дня — total у events?from=день&to=день.
func TestCalendar(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	// Предел частоты — 60 запросов на пользователя: у каждой части свой.
	const user, dayUser, filterUser = 21, 22, 23
	const span = "from=2026-09-29&to=2026-11-29" // 62 дня — предел

	if resp, body := r.get("/api/v1/calendar?"+span, ""); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), `"unauthorized"`) {
		t.Fatalf("без заголовка: code=%d body=%s, want 401", resp.StatusCode, body)
	}

	// sum — ответ calendar: сумма счётчиков; даты по возрастанию, в диапазоне
	// lo…hi, счётчики больше нуля.
	sum := func(tgID int64, query, lo, hi string) (int, []dayJSON) {
		t.Helper()
		var days []dayJSON
		resp := r.getJSON(tgID, "/api/v1/calendar?"+query, http.StatusOK, &days)
		if cc := resp.Header.Get("Cache-Control"); cc != cacheControl {
			t.Fatalf("%s: Cache-Control = %q", query, cc)
		}
		n := 0
		for i, d := range days {
			if d.Count <= 0 || d.Date < lo || d.Date > hi || (i > 0 && d.Date <= days[i-1].Date) {
				t.Fatalf("%s: день %+v (диапазон %s…%s)", query, d, lo, hi)
			}
			n += d.Count
		}
		return n, days
	}
	total := func(tgID int64, query string) int {
		t.Helper()
		var p listJSON
		r.getJSON(tgID, "/api/v1/events?"+query, http.StatusOK, &p)
		return p.Total
	}

	n, days := sum(user, span, "2026-09-29", "2026-11-29")
	if want := total(user, span); n != want || n == 0 || len(days) < 10 {
		t.Fatalf("сумма по дням = %d (дней %d), total events = %d", n, len(days), want)
	}
	if want := r.count(event.Filter{From: fixtureToday, To: time.Date(2026, 11, 29, 0, 0, 0, 0, time.UTC), CountryCode: "RU"}); n != want {
		t.Fatalf("сумма по дням = %d, CountEvents = %d", n, want)
	}
	for _, d := range days {
		if want := total(dayUser, "from="+d.Date+"&to="+d.Date); d.Count != want {
			t.Fatalf("%s: count=%d, total events дня = %d", d.Date, d.Count, want)
		}
	}

	// Неверные параметры → 400 bad_request; 63 дня — уже за пределом.
	for _, q := range []string{"from=2026-09-29&to=2026-11-30", "from=2026-10-02&to=2026-10-01", "to=2026-10-31", "from=2026-10-01", "",
		"from=2026-13-01&to=2026-10-31", "from=2026-10-01&to=31.10.2026", "from=01.10.2026&to=2026-10-31", "from=2026-10-1&to=2026-10-31",
		span + "&periods=cm", span + "&sport=football", span + "&country=ru", span + "&country=*&city=moscow", span + "&dist=0"} {
		if code := r.errCode(user, "/api/v1/calendar?"+q, http.StatusBadRequest); code != "bad_request" {
			t.Fatalf("%s: %q", q, code)
		}
	}

	// Прошедшие дни не отдаются — как у events, from поднимается до «сегодня».
	if n, _ := sum(user, "from=2026-09-01&to=2026-10-15", "2026-09-29", "2026-10-15"); n == 0 || n != total(user, "from=2026-09-01&to=2026-10-15") {
		t.Fatalf("from в прошлом: сумма %d", n)
	}
	if resp, body := r.get("/api/v1/calendar?from=2026-09-01&to=2026-09-10", r.auth(user)); resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("диапазон в прошлом: code=%d body=%s, want []", resp.StatusCode, body)
	}

	// Фильтры действуют согласованно с events.
	storetest.SetSport(t, func() int64 {
		var p listJSON
		r.getJSON(filterUser, "/api/v1/events?limit=1&"+span, http.StatusOK, &p)
		return p.Items[0].ID
	}(), event.SportSkiing)
	got := map[string]int{}
	for _, q := range []string{"", "&country=RU", "&country=*", "&country=BY", "&sport=running", "&sport=skiing", "&city=moscow", "&type=trail", "&sport=running&dist=0", "&sport=running&dist=2,3&type=road"} {
		n, _ := sum(filterUser, span+q, "2026-09-29", "2026-11-29")
		if want := total(filterUser, span+q); n != want {
			t.Fatalf("%s: сумма по дням = %d, total events = %d", q, n, want)
		}
		got[q] = n
	}
	if got[""] != got["&country=RU"] || got["&country=BY"] != 0 || got["&sport=skiing"] != 1 || got["&sport=running"] != got[""]-1 ||
		got["&city=moscow"] == 0 || got["&city=moscow"] >= got[""] || got["&type=trail"] == 0 || got["&type=trail"] >= got[""] {
		t.Fatalf("фильтры: %+v", got)
	}
	if resp, body := r.get("/api/v1/calendar?country=BY&"+span, r.auth(filterUser)); resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("страна без событий: code=%d body=%s, want []", resp.StatusCode, body)
	}

	// Скрытое событие из счёта дня уходит.
	var p listJSON
	first := days[0].Date
	r.getJSON(filterUser, "/api/v1/events?from="+first+"&to="+first, http.StatusOK, &p)
	storetest.HideEvent(t, p.Items[0].ID)
	if n, _ := sum(filterUser, "from="+first+"&to="+first, first, first); n != p.Total-1 {
		t.Fatalf("после скрытия: %d, want %d", n, p.Total-1)
	}

	// Лог (D-12): шаблон маршрута и код; без параметров запроса и пользователя.
	logs := r.logs.String()
	if !strings.Contains(logs, `route="GET /api/v1/calendar" status=200`) || !strings.Contains(logs, `route="GET /api/v1/calendar" status=400`) {
		t.Fatalf("в логе нет строк запросов:\n%s", logs)
	}
	for _, bad := range []string{"tg_id", "from=", "to=", "2026-11-29", "moscow", "initData", "Authorization", "hash="} {
		if strings.Contains(logs, bad) {
			t.Fatalf("в логе есть %q:\n%s", bad, logs)
		}
	}
}

func TestRateLimit(t *testing.T) {
	r := newRig(t, nil) // неизвестный путь: до базы не доходит, но считается
	const user, other = 7, 8
	for i := 1; i <= rateLimit; i++ {
		if resp, _ := r.get("/api/v1/nope", r.auth(user)); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("запрос %d: code=%d, want 404", i, resp.StatusCode)
		}
	}
	resp, body := r.get("/api/v1/nope", r.auth(user))
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "60" || !strings.Contains(string(body), `"rate_limited"`) {
		t.Fatalf("61-й запрос: code=%d Retry-After=%q body=%s", resp.StatusCode, resp.Header.Get("Retry-After"), body)
	}
	if resp, _ := r.get("/api/v1/nope", r.auth(other)); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("другой пользователь: code=%d, want 404", resp.StatusCode)
	}
	if !strings.Contains(r.logs.String(), "status=429") || strings.Contains(r.logs.String(), "tg_id") {
		t.Fatalf("лог:\n%s", r.logs.String())
	}
}

// Пользователи тестов избранного и профиля: приметные идентификаторы, чтобы
// их появление в логе было видно.
const (
	favA   int64 = 7700000011
	favB   int64 = 7700000012
	favBad int64 = 7700000013
	favLim int64 = 7700000014
)

// TestFavorites — избранное по пользователю из initData: исходы добавления
// (как internal/bot/fav_test.go), идемпотентность, изоляция пользователей,
// видимость списка и то, что Mini App не создаёт строк users/user_profiles.
func TestFavorites(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	dbExec(t, `DELETE FROM favorites`)
	r := newRig(t, s)
	ctx := context.Background()
	before := userRows(t)

	items, err := s.ListEvents(ctx, event.Filter{From: fixtureToday}, 60, 0)
	if err != nil || len(items) < 60 {
		t.Fatalf("ListEvents: %d событий, err=%v", len(items), err)
	}
	id := func(i int) string { return strconv.FormatInt(items[i].ID, 10) }
	isFav := func(tgID int64, i int) bool {
		var c cardJSON
		resp := r.getJSON(tgID, "/api/v1/events/"+id(i), http.StatusOK, &c)
		if cc := resp.Header.Get("Cache-Control"); cc != noStore {
			t.Fatalf("карточка: Cache-Control = %q", cc)
		}
		return c.IsFavorite
	}
	want := func(step string, tgID int64, ids string) {
		t.Helper()
		n := 0
		if ids != "" {
			n = strings.Count(ids, ",") + 1
		}
		if got, total := r.favIDs(tgID, ""); got != ids || total != n {
			t.Fatalf("%s: избранное %d = %q (total %d), want %q", step, tgID, got, total, ids)
		}
	}

	// Пусто: массив, а не null.
	if _, body := r.get("/api/v1/favorites", r.auth(favA)); !strings.Contains(string(body), `"total":0`) || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("пустое избранное: %s", body)
	}

	// added, повтор — already; карточка видит состояние своего пользователя.
	if code, out := r.put(favA, id(1)); code != http.StatusOK || out != "added" {
		t.Fatalf("PUT: %d %q, want 200 added", code, out)
	}
	if code, out := r.put(favA, id(1)); code != http.StatusOK || out != "already" {
		t.Fatalf("повторный PUT: %d %q, want 200 already", code, out)
	}
	if n := dbCount(t, `SELECT count(*) FROM favorites`); n != 1 {
		t.Fatalf("после двух PUT записей %d, want 1", n)
	}
	if !isFav(favA, 1) || isFav(favB, 1) {
		t.Fatal("is_favorite: want true у своего пользователя и false у другого")
	}
	want("после PUT", favA, id(1))
	want("чужое избранное", favB, "")

	// Пользователя нельзя подменить: параметр tg_id и тело запроса
	// игнорируются, маршрутов с идентификатором пользователя нет.
	a := strconv.FormatInt(favA, 10)
	if got, total := r.favIDs(favB, "?tg_id="+a); got != "" || total != 0 {
		t.Fatalf("?tg_id= показал чужое избранное: %q (%d)", got, total)
	}
	if resp, body := r.do(http.MethodPut, "/api/v1/favorites/"+id(2)+"?tg_id="+a, r.auth(favB), `{"tg_id":`+a+`}`); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"added"`) {
		t.Fatalf("PUT с ?tg_id= и телом: %d %s", resp.StatusCode, body)
	}
	want("PUT с чужим tg_id — себе", favB, id(2))
	want("PUT с чужим tg_id — не владельцу", favA, id(1))
	if resp, _ := r.do(http.MethodDelete, "/api/v1/favorites/"+id(1)+"?tg_id="+a, r.auth(favB), `{"tg_id":`+a+`}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE с ?tg_id=: %d", resp.StatusCode)
	}
	want("DELETE с чужим tg_id", favA, id(1))
	for _, p := range []string{"/api/v1/favorites/" + a + "/" + id(1), "/api/v1/users/" + a + "/favorites", "/api/v1/profile/" + a} {
		for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			if resp, body := r.do(m, p, r.auth(favB), ""); resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"not_found"`) {
				t.Fatalf("%s %s: %d %s, want 404 not_found", m, p, resp.StatusCode, body)
			}
		}
	}
	want("после запросов по чужим путям", favA, id(1))

	// DELETE идемпотентен: всегда 204, и для несуществующего события.
	r.del(favA, id(1))
	if isFav(favA, 1) {
		t.Fatal("is_favorite = true после DELETE")
	}
	r.del(favA, id(1))
	r.del(favA, "999999")
	want("после DELETE", favA, "")
	want("DELETE не тронул другого", favB, id(2))

	// Идентификатор в пути: число больше нуля без знака и ведущих нулей.
	for _, bad := range []string{"0", "01", "+5", "-5", "abc", "1.0", "99999999999999999999"} {
		if code, out := r.put(favBad, bad); code != http.StatusBadRequest || out != "bad_request" {
			t.Fatalf("PUT %q: %d %q, want 400", bad, code, out)
		}
		if resp, _ := r.do(http.MethodDelete, "/api/v1/favorites/"+bad, r.auth(favBad), ""); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("DELETE %q: %d, want 400", bad, resp.StatusCode)
		}
	}
	for _, q := range []string{"?limit=51", "?limit=0", "?page=0", "?page=10001", "?page=x"} {
		if code := r.errCode(favBad, "/api/v1/favorites"+q, http.StatusBadRequest); code != "bad_request" {
			t.Fatalf("favorites%s: %q", q, code)
		}
	}

	// gone: нет события, скрыто. past: дата прошла. Отказ записи не создаёт.
	storetest.HideEvent(t, items[3].ID)
	for _, gone := range []string{"999999", id(3)} {
		if code, out := r.put(favA, gone); code != http.StatusNotFound || out != "gone" {
			t.Fatalf("PUT %s: %d %q, want 404 gone", gone, code, out)
		}
	}
	dbExec(t, `UPDATE events SET start_date = '2026-09-01' WHERE id = $1`, items[4].ID)
	if code, out := r.put(favA, id(4)); code != http.StatusOK || out != "past" {
		t.Fatalf("PUT прошедшего: %d %q, want 200 past", code, out)
	}
	want("после отказов", favA, "")

	// Список: только будущие; запись на слитое событие — корнем, один раз;
	// порядок — по дате; страницы.
	for _, i := range []int{7, 5, 6} {
		if code, out := r.put(favA, id(i)); out != "added" {
			t.Fatalf("PUT %s: %d %q", id(i), code, out)
		}
	}
	want("три старта", favA, id(5)+","+id(6)+","+id(7))
	if got, total := r.favIDs(favA, "?page=2&limit=2"); got != id(7) || total != 3 {
		t.Fatalf("страница 2: %q (total %d)", got, total)
	}
	var l favListJSON
	r.getJSON(favA, "/api/v1/favorites?limit=2", http.StatusOK, &l)
	if l.Page != 1 || l.Pages != 2 || l.Items[0].Title != items[5].Title || l.Items[0].StartDate != items[5].StartDate.Format(time.DateOnly) || l.Items[0].Sport != "running" || l.Items[0].CountryCode != "RU" {
		t.Fatalf("страница 1: %+v", l)
	}
	if _, body := r.get("/api/v1/favorites", r.auth(favA)); strings.Contains(string(body), "distances") || strings.Contains(string(body), "tg_id") {
		t.Fatalf("лишние поля в ответе: %s", body)
	}
	dbExec(t, `UPDATE events SET start_date = '2026-09-02' WHERE id = $1`, items[5].ID)
	want("прошедший не показан", favA, id(6)+","+id(7))
	storetest.MergeEvent(t, items[6].ID, items[8].ID)
	want("слитое — корнем", favA, id(7)+","+id(8))
	if code, out := r.put(favA, id(6)); code != http.StatusOK || out != "already" {
		t.Fatalf("PUT слитого: %d %q, want already (запись ведёт к корню)", code, out)
	}
	if !isFav(favA, 8) {
		t.Fatal("карточка корня: is_favorite = false")
	}
	r.del(favA, id(8))
	want("DELETE корня убирает запись на слитое", favA, id(7))

	// limit: предел бота; пятьдесят первый не добавляется, после удаления — да.
	const limit = 50
	var filled int
	for _, it := range items[9:] {
		if filled == limit {
			break
		}
		if out, err := s.AddFavorite(ctx, favLim, it.ID, fixtureToday, fixtureNow, limit); err != nil || out != store.FavAdded {
			t.Fatalf("AddFavorite(%d) = %v, %v", it.ID, out, err)
		}
		filled++
	}
	if _, total := r.favIDs(favLim, "?limit=1"); filled != limit || total != limit {
		t.Fatalf("заполнено %d, total %d, want %d", filled, total, limit)
	}
	if code, out := r.put(favLim, id(7)); code != http.StatusOK || out != "limit" {
		t.Fatalf("PUT сверх предела: %d %q, want 200 limit", code, out)
	}
	if code, out := r.put(favLim, id(9)); code != http.StatusOK || out != "already" {
		t.Fatalf("PUT имеющегося при пределе: %d %q, want already", code, out)
	}
	r.del(favLim, id(9))
	if code, out := r.put(favLim, id(7)); code != http.StatusOK || out != "added" {
		t.Fatalf("PUT после удаления: %d %q, want added", code, out)
	}

	// Mini App не создал строк users и user_profiles (D-10).
	if after := userRows(t); after != before {
		t.Fatalf("строки users/user_profiles: %v → %v", before, after)
	}

	// Приватность лога (D-12): метод, шаблон маршрута, код; без
	// пользователя, идентификаторов событий и initData.
	logs := r.logs.String()
	for _, need := range []string{`method=PUT route="PUT /api/v1/favorites/{id}" status=200`, `route="PUT /api/v1/favorites/{id}" status=404`,
		`method=DELETE route="DELETE /api/v1/favorites/{id}" status=204`, `route="GET /api/v1/favorites" status=200`} {
		if !strings.Contains(logs, need) {
			t.Fatalf("в логе нет %q:\n%s", need, logs)
		}
	}
	for _, bad := range []string{"tg_id", "77000000", "initData", "Authorization", "auth_date", "hash=", "999999", "outcome"} {
		if strings.Contains(logs, bad) {
			t.Fatalf("в логе есть %q:\n%s", bad, logs)
		}
	}
	if m := regexp.MustCompile(`favorites/\d|events/\d|\?`).FindString(logs); m != "" {
		t.Fatalf("в логе путь запроса: %q\n%s", m, logs)
	}
}

// TestWriteLimit — запись считается отдельным, более строгим счётчиком:
// 20 запросов в минуту на пользователя, чтение его не расходует.
func TestWriteLimit(t *testing.T) {
	r := newRig(t, nil) // негодный id: до базы не доходит, но считается
	const user, other = 7700000031, 7700000032
	for i := 1; i <= writeLimit; i++ {
		m := http.MethodPut
		if i%2 == 0 {
			m = http.MethodDelete
		}
		if resp, _ := r.do(m, "/api/v1/favorites/abc", r.auth(user), ""); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("запрос %d: code=%d, want 400", i, resp.StatusCode)
		}
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		resp, body := r.do(m, "/api/v1/favorites/abc", r.auth(user), "")
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "60" || !strings.Contains(string(body), `"rate_limited"`) {
			t.Fatalf("%s сверх предела: code=%d Retry-After=%q body=%s", m, resp.StatusCode, resp.Header.Get("Retry-After"), body)
		}
	}
	// Чтение того же пользователя и запись другого не затронуты.
	if resp, _ := r.get("/api/v1/nope", r.auth(user)); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("чтение после предела записи: code=%d, want 404", resp.StatusCode)
	}
	if resp, _ := r.do(http.MethodPut, "/api/v1/favorites/abc", r.auth(other), ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("другой пользователь: code=%d, want 400", resp.StatusCode)
	}
	if logs := r.logs.String(); !strings.Contains(logs, `route="PUT /api/v1/favorites/{id}" status=429`) || strings.Contains(logs, "tg_id") || strings.Contains(logs, "77000000") {
		t.Fatalf("лог:\n%s", logs)
	}
}

// TestProfile — анкета только для чтения: без строки — exists:false, со
// строкой — коды с подписями; служебные поля и идентификатор не отдаются;
// запросы ничего не создают.
func TestProfile(t *testing.T) {
	s := storetest.Open(t)
	r := newRig(t, s)
	ctx := context.Background()
	const user, fresh, other int64 = 7700000021, 7700000022, 7700000023

	resp, body := r.get("/api/v1/profile?tg_id="+strconv.FormatInt(user, 10), r.auth(other))
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"exists":false}` || resp.Header.Get("Cache-Control") != noStore {
		t.Fatalf("без строки: code=%d Cache-Control=%q body=%s", resp.StatusCode, resp.Header.Get("Cache-Control"), body)
	}
	if rows := userRows(t); rows != [2]int{0, 0} {
		t.Fatalf("GET /profile создал строки: %v", rows)
	}

	// Строки создаёт бот (/start); здесь — тем же методом store.
	for _, id := range []int64{user, fresh} {
		if _, _, _, err := s.StartUser(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	dbExec(t, `UPDATE user_profiles SET status = 'completed', gender = 'f', age_group = 2, residence_country_code = 'RU',
		residence_city = 'Нижний Тагил', residence_city_key = 'nizhny-tagil', sports = '{ski,run}',
		awaiting_city_until = now(), awaiting_city_mode = 'e' WHERE tg_id = $1`, user)
	before := userRows(t)

	var p profileJSON
	resp, body = r.get("/api/v1/profile", r.auth(user))
	if err := json.Unmarshal(body, &p); err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != noStore {
		t.Fatalf("профиль: code=%d err=%v body=%s", resp.StatusCode, err, body)
	}
	if !p.Exists || p.Status != store.ProfileCompleted || p.Gender == nil || *p.Gender != (optionJSON{"f", "Женский"}) ||
		p.AgeGroup == nil || *p.AgeGroup != (optionJSON{"2", "25–34"}) || p.Country == nil || p.Country.Code != "RU" || !strings.Contains(p.Country.Label, "Россия") ||
		p.City != "Нижний Тагил" || len(p.Sports) != 2 || p.Sports[0].Code != "run" || !strings.Contains(p.Sports[0].Label, "Бег") || p.Sports[1].Code != "ski" {
		t.Fatalf("профиль = %s", body)
	}
	for _, bad := range []string{"awaiting", "tg_id", "nizhny-tagil", "77000000", `"v"`, "profile_v"} {
		if strings.Contains(string(body), bad) {
			t.Fatalf("в ответе есть %q: %s", bad, body)
		}
	}

	// Незаполненная анкета: поля null, виды спорта — пустой массив.
	_, body = r.get("/api/v1/profile", r.auth(fresh))
	for _, need := range []string{`"exists":true`, `"status":"not_started"`, `"gender":null`, `"age_group":null`, `"country":null`, `"city":""`, `"sports":[]`} {
		if !strings.Contains(string(body), need) {
			t.Fatalf("незаполненная анкета без %s: %s", need, body)
		}
	}
	// Чужой профиль не виден: у третьего пользователя строки нет.
	if _, body = r.get("/api/v1/profile", r.auth(other)); strings.TrimSpace(string(body)) != `{"exists":false}` {
		t.Fatalf("третий пользователь: %s", body)
	}
	if after := userRows(t); after != before {
		t.Fatalf("строки users/user_profiles: %v → %v", before, after)
	}
	if logs := r.logs.String(); !strings.Contains(logs, `route="GET /api/v1/profile" status=200`) || strings.Contains(logs, "tg_id") || strings.Contains(logs, "77000000") || strings.Contains(logs, "Тагил") {
		t.Fatalf("лог:\n%s", logs)
	}
}
