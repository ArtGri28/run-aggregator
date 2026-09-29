package marathonec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

func TestSourcePolicy(t *testing.T) {
	s := New(nil)
	if s.Code() != "marathonec" {
		t.Fatalf("Code = %q", s.Code())
	}
	if p := s.Policy(); p.CountryFallback != normalize.NoFallback || p.CrawlDelay != 40*time.Second {
		t.Fatalf("Policy = %+v, want NoFallback (D-07), 40s", p)
	}
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	doc := ingest.RawDoc{URL: CalendarURL, Body: body}
	got, err1 := s.Parse(doc)
	want, err2 := Parse(doc)
	if err1 != nil || err2 != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("method Parse differs from Parse: %v, %v", err1, err2)
	}
}

func TestSourceFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>calendar</html>"))
	}))
	defer srv.Close()
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent = "RunAggregator/0.3 (+contact: test@example.invalid)"
	s := New(ingest.NewFetcher(cfg))
	s.url = srv.URL + "/calendar-beg/"
	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 1 || string(docs[0].Body) != "<html>calendar</html>" || docs[0].URL != s.url {
		t.Fatalf("Fetch = %+v, %v", docs, err)
	}
}
