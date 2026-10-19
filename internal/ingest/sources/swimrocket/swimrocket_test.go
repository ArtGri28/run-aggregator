package swimrocket

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

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFixture(t *testing.T) {
	recs, err := Parse(ingest.RawDoc{URL: ListURL, Body: fixture(t, "calendar.html"), FetchedAt: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	var future []ingest.SourceEvent
	for _, r := range recs {
		if d, err := time.Parse("02.01.2006", r.Date); err != nil {
			t.Fatalf("%s: дата %q", r.ExternalID, r.Date)
		} else if !d.Before(fixtureNow.Truncate(24 * time.Hour)) {
			r.TypeLabel = string(normalize.SportType(event.Sport(r.Sport), r.TypeLabel))
			future = append(future, r)
		}
	}
	var want []ingest.SourceEvent
	if err := json.Unmarshal(fixture(t, "future.golden.json"), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(future, want) {
		g, _ := json.MarshalIndent(future, "", "  ")
		t.Fatalf("будущие записи не совпадают с эталоном:\n%s", g)
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Rejected != 0 || len(prep) != len(want) {
		t.Fatalf("Prepare: %d записей в каталоге, отбраковано %d (%v); want %d", len(prep), st.Rejected, st.Rejects, len(want))
	}
}

func TestParseEdge(t *testing.T) {
	page := func(items string) ingest.RawDoc {
		return ingest.RawDoc{Body: []byte("<script>new Vue({ data: function(){ return{ items: " + items + ", x: 1 }}})</script>")}
	}
	recs, err := Parse(page(`[{'id':'1','date':'01.06.2027','title':'Заплыв \'Озеро\'','distance':false,` +
		`'location':{'country':'Беларусь','city':'Беларусь, Минск'},'web':'https:\/\/example.org\/a','banner':'<a href=\"x\">[1]<\/a>'},` +
		`{'id':'2','date':'02.06.2027','title':'Заплыв','distance':['500'],'location':{'country':'Россия','city':'Сочи, Абхазия'},'web':''}]`))
	want := []ingest.SourceEvent{{ExternalID: "1", URL: "https://example.org/a", Title: "Заплыв 'Озеро'", Date: "01.06.2027",
		City: "Минск", Country: "BY", TypeLabel: "открытая вода", Sport: "swimming"}}
	if err != nil || !reflect.DeepEqual(recs, want) {
		t.Fatalf("Parse = %+v, %v; want %+v", recs, err, want)
	}
	for _, body := range []string{"<html>Технические работы</html>", "<script>items: [{'id':'1'</script>"} {
		if _, err := Parse(ingest.RawDoc{Body: []byte(body)}); !errors.Is(err, ingest.ErrStructureNotFound) {
			t.Errorf("%q: %v, want ErrStructureNotFound", body, err)
		}
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
	s.url = srv.URL + "/calendar/"
	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 1 || hits != 1 {
		t.Fatalf("Fetch: %d документов, %v, запросов %d; want 1 и 1", len(docs), err, hits)
	}
	if s.Code() != "swimrocket" || s.Policy().CountryFallback != normalize.NoFallback {
		t.Fatalf("Code %q, Policy %+v", s.Code(), s.Policy())
	}
}
