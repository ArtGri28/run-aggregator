package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/marathonec"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
)

const fixturePath = "sources/marathonec/testdata/calendar-beg.html"

// fixtureNow — дата загрузки фикстуры (testdata/README.md), 12:00 UTC.
var fixtureNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

var testStore *store.Store

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Exit(m.Run())
	}
	// Тесты пишут в базу: только в тестовую, не в dev-БД с данными (D-03).
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		fmt.Fprintln(os.Stderr, "TEST_DATABASE_URL: имя базы должно оканчиваться на _test")
		os.Exit(1)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open test store:", err)
		os.Exit(1)
	}
	if err := s.Migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "migrate test store:", err)
		os.Exit(1)
	}
	testStore = s
	code := m.Run()
	s.Close()
	os.Exit(code)
}

// fixtureSource отдаёт заранее заданное тело без сети. Код источника
// уникален на тест: тесты изолированы без очистки таблиц.
type fixtureSource struct {
	code string
	body []byte
}

func newFixtureSource(t *testing.T, body []byte) *fixtureSource {
	return &fixtureSource{code: fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano()), body: body}
}

func (s *fixtureSource) Code() string { return s.code }

func (s *fixtureSource) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.RUOnlyFallback}
}

func (s *fixtureSource) Fetch(context.Context) ([]ingest.RawDoc, error) {
	// URL — только база для ссылок, запроса нет.
	return []ingest.RawDoc{{URL: "https://marathonec.ru/calendar-beg/", Body: s.body, FetchedAt: fixtureNow}}, nil
}

func (s *fixtureSource) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	return marathonec.Parse(doc)
}

func testRunner(t *testing.T) *ingest.Runner {
	t.Helper()
	if testStore == nil {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	return &ingest.Runner{Store: testStore, Now: func() time.Time { return fixtureNow }}
}

func TestRunFixture(t *testing.T) {
	r := testRunner(t)
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	for _, id := range regexp.MustCompile(`post-row-[0-9]+`).FindAllString(string(body), -1) {
		rows[id] = true
	}
	src := newFixtureSource(t, body)
	ctx := context.Background()

	first, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	t.Logf("TRACER created=%d unchanged=%d out_of_scope=%d expired=%d rejected=%d",
		first.Created, first.Unchanged, first.OutOfScope, first.Expired, first.Rejected)
	if first.Parsed != len(rows) {
		t.Fatalf("Parsed = %d, want %d (unique post-row ids)", first.Parsed, len(rows))
	}
	if first.Created == 0 || first.Created != first.Upserted-first.OutOfScope || first.Changed != 0 {
		t.Fatalf("first Run = %+v, want Created > 0, Created == Upserted-OutOfScope, Changed == 0", first)
	}

	second, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Created != 0 || second.Changed != 0 || second.Unchanged != first.Created || second.Upserted != first.Upserted {
		t.Fatalf("second Run = %+v, want Created 0, Changed 0, Unchanged %d, Upserted %d", second, first.Created, first.Upserted)
	}
}

func TestRunStructureNotFound(t *testing.T) {
	r := testRunner(t)
	src := newFixtureSource(t, []byte(`<html><body><p>нет таблицы</p></body></html>`))
	res, err := r.Run(context.Background(), src)
	if !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Fatalf("Run err = %v, want ErrStructureNotFound", err)
	}
	if res.Upserted != 0 {
		t.Fatalf("Upserted = %d, want 0", res.Upserted)
	}
}
