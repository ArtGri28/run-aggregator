package myrace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

var fixtures = []string{"testdata/calendar-1.json", "testdata/calendar-2.json"}

// fixtureNow — день загрузки образца.
var fixtureNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseAll(t *testing.T) map[string]ingest.SourceEvent {
	t.Helper()
	m := map[string]ingest.SourceEvent{}
	for _, f := range fixtures {
		recs, err := Parse(ingest.RawDoc{Body: readFixture(t, f)})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			if _, dup := m[r.ExternalID]; dup {
				t.Fatalf("external_id %q повторён", r.ExternalID)
			}
			m[r.ExternalID] = r
		}
	}
	return m
}

// D-02: 14 событий образца, акватлон без бегового вида пропущен.
func TestParse(t *testing.T) {
	recs := parseAll(t)
	if len(recs) != 13 {
		t.Fatalf("записей %d, want 13", len(recs))
	}
	if _, ok := recs["893497545085464746"]; ok {
		t.Fatal("акватлон попал в выдачу")
	}
	cases := []struct {
		id, label, city, country, end, distances string
	}{
		// трейл, дистанции в названиях активностей
		{"869562175839070418", "trail", "Ишеево", "RU", "", "Автономная ультра ~66 km, Лонг ~23 km, Лайт ~12 km, Cпринт ~6 km, Детский забег"},
		// с препятствиями, возрастные группы вместо дистанций
		{"888337446180285561", "obstacle", "Жуковский", "RU", "2026-10-25", ""},
		// вело и трейл: активности вело в дистанции не идут
		{"801463821951627510", "trail", "Москва", "RU", "", "Basic, Medium, Hard, Boss, Детский билет, Подростковый билет"},
		// running → пустая подпись (other)
		{"897756985691268226", "", "Вологда", "RU", "", "Ночной забег"},
		// страны нет — пусто, не RU
		{"834766431731117241", "", "Ереван", "", "2026-10-18", ""},
		// location отсутствует совсем
		{"801463822270394500", "", "", "", "", ""},
	}
	for _, c := range cases {
		r, ok := recs[c.id]
		if !ok {
			t.Fatalf("%s: нет записи", c.id)
		}
		if r.TypeLabel != c.label || r.City != c.city || r.Country != c.country || r.EndDate != c.end {
			t.Fatalf("%s: %+v", c.id, r)
		}
		if c.distances != "" && r.Distances != c.distances {
			t.Fatalf("%s: distances %q", c.id, r.Distances)
		}
		if !strings.HasPrefix(r.URL, "https://myrace.info/events/") || r.RegistrationURL != r.URL {
			t.Fatalf("%s: ссылки %q %q", c.id, r.URL, r.RegistrationURL)
		}
	}
}

// Весь путь через Prepare на день загрузки образца.
func TestPrepare(t *testing.T) {
	var recs []ingest.SourceEvent
	for _, r := range parseAll(t) {
		recs = append(recs, r)
	}
	slices.SortFunc(recs, func(a, b ingest.SourceEvent) int { return strings.Compare(a.ExternalID, b.ExternalID) })
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil, "").Policy())
	// абонемент 2025-12-17 — прошедший; Ереван и событие без location — вне каталога.
	if st.Rejected != 0 || st.Expired != 1 || st.OutOfScope != 2 || st.Valid != 12 {
		t.Fatalf("Stats = %+v", st)
	}
	got := map[string]event.Event{}
	for _, p := range prep {
		if p.InScope {
			got[p.Source.ExternalID] = p.Event
		}
	}
	if len(got) != 10 {
		t.Fatalf("в каталоге %d, want 10", len(got))
	}
	want := map[string]struct {
		typ   event.Type
		dists []int
	}{
		"869562175839070418": {event.TypeTrail, []int{6000, 12000, 23000, 66000}},
		"888337446180285561": {event.TypeObstacle, nil},
		"897756985691268226": {event.TypeOther, nil},
		"860136636388782141": {event.TypeTrail, nil}, // шифры «ARKHYZ 15» не угадываются
		"862637914956279961": {event.TypeTrail, []int{6500, 19500}},
		"897781630058423522": {event.TypeOther, nil}, // активностей нет
	}
	for id, w := range want {
		e, ok := got[id]
		if !ok {
			t.Fatalf("%s не в каталоге", id)
		}
		var m []int
		for _, d := range e.Distances {
			m = append(m, d.Meters)
		}
		if e.Type != w.typ || e.CountryCode != "RU" || !slices.Equal(m, w.dists) {
			t.Fatalf("%s: type %v country %s distances %v", id, e.Type, e.CountryCode, m)
		}
	}
}

func TestParseEdges(t *testing.T) {
	if _, err := Parse(ingest.RawDoc{Body: []byte(`{"items": []}`)}); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Fatalf("без events: %v", err)
	}
	if _, err := Parse(ingest.RawDoc{Body: []byte(`<html>`)}); err == nil {
		t.Fatal("не JSON принят")
	}
	if recs, err := Parse(ingest.RawDoc{Body: []byte(`{"events": []}`)}); err != nil || len(recs) != 0 {
		t.Fatalf("пустой список: %v, %v", recs, err)
	}
	body := `{"events":[{"id":"1","name":"A","sports":[{"code":"running"},{"code":"running-with-obstacles"},{"code":"trail-running"}],
	 "start_date":"2027-01-01","end_date":"2027-01-01","url":"javascript:alert(1)","activities":[]}]}`
	recs, err := Parse(ingest.RawDoc{Body: []byte(body)})
	if err != nil || len(recs) != 1 || recs[0].TypeLabel != "obstacle" || recs[0].URL != "" || recs[0].RegistrationURL != "" {
		t.Fatalf("%+v, %v", recs, err)
	}
}

func TestSource(t *testing.T) {
	s := New(nil, "")
	if s.Code() != "myrace" {
		t.Fatalf("Code = %q", s.Code())
	}
	if p := s.Policy(); p.CountryFallback != normalize.NoFallback || p.CrawlDelay != CrawlDelay {
		t.Fatalf("Policy = %+v", p)
	}
	doc := ingest.RawDoc{Body: readFixture(t, fixtures[0])}
	got, err1 := s.Parse(doc)
	want, err2 := Parse(doc)
	if err1 != nil || err2 != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("method Parse differs: %v %v", err1, err2)
	}
}

func testSource(t *testing.T, h http.HandlerFunc) *Source {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent = "RunAggregator/0.3 (+contact: test@example.invalid)"
	s := New(ingest.NewFetcher(cfg), "test-token")
	s.url = srv.URL + "/api/1.0/calendar?limit=100"
	s.delay = time.Millisecond
	return s
}

// Обе страницы по курсору, токен в заголовке и только там.
func TestSourceFetch(t *testing.T) {
	var tokens, auths []string
	s := testSource(t, func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.URL.Query().Get("page_token"))
		auths = append(auths, r.Header.Get("Authorization"))
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write(readFixture(t, fixtures[0]))
			return
		}
		_, _ = w.Write(readFixture(t, fixtures[1]))
	})
	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 2 {
		t.Fatalf("Fetch = %d docs, %v", len(docs), err)
	}
	if !slices.Equal(tokens, []string{"", "cGFnZTI"}) || !slices.Equal(auths, []string{"Bearer test-token", "Bearer test-token"}) {
		t.Fatalf("tokens %v auths %v", tokens, auths)
	}
	for _, d := range docs {
		if strings.Contains(d.URL, "test-token") {
			t.Fatalf("токен в адресе: %s", d.URL)
		}
	}
}

// Бесконечный курсор — ошибка на пределе страниц, а не усечение; 401 — ошибка прогона.
func TestSourceFetchErrors(t *testing.T) {
	var n int
	s := testSource(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		_, _ = w.Write([]byte(`{"events":[],"next_page_token":"again"}`))
	})
	if docs, err := s.Fetch(context.Background()); err == nil || docs != nil || n != maxPages {
		t.Fatalf("предел: docs=%d err=%v запросов=%d", len(docs), err, n)
	}
	s = testSource(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	if _, err := s.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("401: %v", err)
	}
}
