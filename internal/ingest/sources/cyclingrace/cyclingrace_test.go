package cyclingrace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

// fixtureNow — день загрузки образца (testdata/README.md).
var fixtureNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func TestParseFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/racers.html")
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Parse(ingest.RawDoc{URL: ListURL, Body: body, FetchedAt: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Rejected != 0 || len(prep) != len(recs) {
		t.Fatalf("Prepare: %d из %d записей, %v", len(prep), len(recs), st.Rejects)
	}
	for i := range recs {
		recs[i].TypeLabel = string(normalize.SportType(event.SportCycling, recs[i].TypeLabel))
	}
	b, err := os.ReadFile("testdata/racers.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []ingest.SourceEvent
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recs, want) {
		g, _ := json.MarshalIndent(recs, "", "  ")
		t.Fatalf("записи не совпадают с эталоном:\n%s", g)
	}
}

func TestParseEdge(t *testing.T) {
	// Заголовок без карточек — пустой список; без заголовка — ошибка структуры.
	if recs, err := Parse(ingest.RawDoc{Body: []byte("<html><h2>Ближайшие гонки:</h2></html>")}); err != nil || recs != nil {
		t.Fatalf("без гонок: %+v, %v", recs, err)
	}
	if _, err := Parse(ingest.RawDoc{Body: []byte("<html>Технические работы</html>")}); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Fatalf("%v, want ErrStructureNotFound", err)
	}
	// Ссылки меню и внешние ссылки гонками не считаются.
	body := `<h2>Ближайшие гонки:</h2><a href="/news"><p>Новости</p><p>01.01.2027</p><p>Москва</p></a>` +
		`<a href="https://t.me/x"><p>a</p><p>01.01.2027</p><p>b</p></a><a href="gravel-100"><p>Грэвел 100</p><p>01.06.2027</p><p>Дмитров</p></a>`
	recs, err := Parse(ingest.RawDoc{Body: []byte(body)})
	if err != nil || len(recs) != 1 || recs[0].ExternalID != "gravel-100" || normalize.SportType(event.SportCycling, recs[0].TypeLabel) != event.TypeGravel {
		t.Fatalf("Parse = %+v, %v", recs, err)
	}
}

func TestFetch(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent, cfg.Attempts = "test", 1
	s := New(ingest.NewFetcher(cfg))
	s.url = srv.URL + "/racers"
	if docs, err := s.Fetch(context.Background()); err != nil || len(docs) != 1 || hits != 1 || s.Code() != "cyclingrace" {
		t.Fatalf("Fetch: %d документов, %v, запросов %d", len(docs), err, hits)
	}
}
