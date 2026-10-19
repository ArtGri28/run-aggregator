package otime

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

	"golang.org/x/text/encoding/charmap"

	"aggregator/internal/ingest"
)

// fixtureNow — день загрузки образца (testdata/README.md).
var fixtureNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func TestParseFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/calendar.html")
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Parse(ingest.RawDoc{URL: ListURL, Body: body, FetchedAt: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("testdata/calendar.golden.json")
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
	// Все записи проходят Prepare, области распознаны.
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Rejected != 0 || len(prep) != len(recs) || st.UnrecognizedRegion != 0 {
		t.Fatalf("Prepare: %d из %d, отбраковано %d, нераспознанных областей %d (%v)", len(prep), len(recs), st.Rejected, st.UnrecognizedRegion, st.Unrecognized)
	}
}

func cp1251(t *testing.T, s string) []byte {
	t.Helper()
	b, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseEdge(t *testing.T) {
	day := func(d, mon, icon, name, place string) string {
		return `<div class="all"><article class="alldate"><b><font color="gray">` + d + `<br><font size="-1">` + mon + `</font></font><br>SA</b></article>` +
			`<article class="allbutton"><div class="allinner1"><article class="alllogo"><img src="https://reg.o-time.ru/img/sport/` + icon + `.png"></article>` +
			`<article class="allname"><a href="https://example.org/x">` + name + `</a></article><article class="allplace">` + place + `</article></div></article></div>`
	}
	// «This week» без года: 15 октября при загрузке 19 октября — этот год
	// (неделя назад), 1 октября — следующий. Значок триатлона — без записи.
	body := `<H2>This week</H2>` + day("15", "Oct", "2", "Кросс", "<b>Москва</b>") + day("01", "Oct", "1", "Лыжи", "<b>Москва</b>") +
		`<H2>January 2027</H2>` + day("09", "Jan", "9", "Триатлон", "<b>Москва</b>")
	recs, err := Parse(ingest.RawDoc{Body: cp1251(t, body), FetchedAt: fixtureNow})
	if err != nil || len(recs) != 2 || recs[0].Date != "15.10.2026" || recs[1].Date != "01.10.2027" || recs[1].Sport != "skiing" || recs[0].City != "Москва" {
		t.Fatalf("Parse = %+v, %v", recs, err)
	}
	if _, err := Parse(ingest.RawDoc{Body: []byte("<html>Технические работы</html>")}); !errors.Is(err, ingest.ErrStructureNotFound) {
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
	s.url = srv.URL + "/calendar"
	if docs, err := s.Fetch(context.Background()); err != nil || len(docs) != 1 || hits != 1 || s.Code() != "otime" {
		t.Fatalf("Fetch: %d документов, %v, запросов %d", len(docs), err, hits)
	}
}
