package swimcup

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
	body, err := os.ReadFile("testdata/series.html")
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
		recs[i].TypeLabel = string(normalize.SportType(event.SportSwimming, recs[i].TypeLabel))
	}
	b, err := os.ReadFile("testdata/series.golden.json")
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

func TestDates(t *testing.T) {
	for _, c := range []struct{ cal, title, start, end string }{
		{"30 Мая", "Крылатское 2027", "30.05.2027", ""},
		{"13-14 Июня", "Белое Озеро 2027", "13.06.2027", "14.06.2027"},
		{"31 Июля - 1 Августа", "Онего 2027", "31.07.2027", "01.08.2027"},
		{"6 Декабря", "Минуты Москва Декабрь", "06.12.2026", ""},
		{"19 Октября", "Минуты", "19.10.2026", ""},
		{"18 Октября", "Минуты", "18.10.2027", ""},
		{"31 Декабря - 2 Января", "Новогодний", "31.12.2026", "02.01.2027"},
		{"скоро", "Заплыв", "скоро", ""},
	} {
		if s, e := dates(c.cal, c.title, fixtureNow); s != c.start || e != c.end {
			t.Errorf("dates(%q, %q) = %q, %q; want %q, %q", c.cal, c.title, s, e, c.start, c.end)
		}
	}
	for in, want := range map[string]string{
		"500, 1000, 1852, 5000 м":        "500 м, 1000 м, 1852 м, 5000 м",
		"10 000, 5 000, 1 500":           "10000 м, 5000 м, 1500 м",
		"1000, 2000, 4000, 3х300м":       "1000 м, 2000 м, 4000 м",
		"500, 1000, 2000, 3x300":         "500 м, 1000 м, 2000 м",
		"60 мин, 30 мин, 15 мин, 10 мин": "",
		"750, 1800 и 3600":               "750 м, 1800 м, 3600 м",
		"50 и 100 все стили":             "50 м, 100 м",
	} {
		if got := distances(in); got != want {
			t.Errorf("distances(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse(ingest.RawDoc{Body: []byte("<html><body>Технические работы</body></html>")}); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Fatalf("%v, want ErrStructureNotFound", err)
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
	s.url = srv.URL + "/series/"
	if docs, err := s.Fetch(context.Background()); err != nil || len(docs) != 1 || hits != 1 {
		t.Fatalf("Fetch: %d документов, %v, запросов %d; want 1 и 1", len(docs), err, hits)
	}
	if s.Code() != "swimcup" {
		t.Fatalf("Code %q", s.Code())
	}
}
