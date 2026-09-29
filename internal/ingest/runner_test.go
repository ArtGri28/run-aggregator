package ingest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	// noFallback — политика до решения D-07 (без fallback RU).
	noFallback bool
}

func newFixtureSource(t *testing.T, body []byte) *fixtureSource {
	return &fixtureSource{code: fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano()), body: body}
}

func (s *fixtureSource) Code() string { return s.code }

func (s *fixtureSource) Policy() ingest.Policy {
	if s.noFallback {
		return ingest.Policy{CountryFallback: normalize.NoFallback}
	}
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

// statusOf — итог прогона как «статус: причины» для сообщений тестов.
func statusOf(res ingest.Result) string {
	return fmt.Sprintf("%s: %s", res.Status, strings.Join(res.Reasons, "; "))
}

func hasReason(res ingest.Result, part string) bool {
	for _, r := range res.Reasons {
		if strings.Contains(r, part) {
			return true
		}
	}
	return false
}

const firstRunReason = "первый прогон источника — требуется принятие"

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
	if first.Parsed != len(rows) {
		t.Fatalf("Parsed = %d, want %d (unique post-row ids)", first.Parsed, len(rows))
	}
	if first.Status != ingest.StatusSuspect || !hasReason(first, firstRunReason) || first.Created != 0 || first.Upserted != 0 {
		t.Fatalf("first Run = %s, %+v; want suspect (D-05), nothing applied", statusOf(first), first)
	}

	acc, err := r.Accept(ctx, src, 0)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	t.Logf("TRACER created=%d unchanged=%d out_of_scope=%d expired=%d rejected=%d",
		acc.Created, acc.Unchanged, acc.OutOfScope, acc.Expired, acc.Rejected)
	if acc.Status != ingest.StatusOK || acc.Created == 0 || acc.Created != acc.Upserted-acc.OutOfScope || acc.Changed != 0 {
		t.Fatalf("Accept = %+v, want ok, Created > 0, Created == Upserted-OutOfScope, Changed == 0", acc)
	}

	second, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Status != ingest.StatusOK || second.Created != 0 || second.Changed != 0 || second.Unchanged != acc.Created || second.Upserted != acc.Upserted {
		t.Fatalf("second Run = %s, %+v; want ok, Created 0, Changed 0, Unchanged %d, Upserted %d",
			statusOf(second), second, acc.Created, acc.Upserted)
	}
}

// TestAcceptAfterPolicySwitch — снимок хранит разобранные записи, а не
// классификацию: прогон suspect без fallback RU (всё out_of_scope)
// принимается уже по текущей политике источника (D-07 → политика → принятие).
func TestAcceptAfterPolicySwitch(t *testing.T) {
	r := testRunner(t)
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	src := newFixtureSource(t, body)
	src.noFallback = true
	ctx := context.Background()

	first, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Status != ingest.StatusSuspect || first.OutOfScope == 0 || first.OutOfScope != first.Parsed-first.Rejected-first.Expired {
		t.Fatalf("first Run = %s, %+v; want suspect, every valid record out_of_scope", statusOf(first), first)
	}

	src.noFallback = false
	acc, err := r.Accept(ctx, src, 0)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if acc.OutOfScope != 0 || acc.Created == 0 || acc.Created != acc.Upserted {
		t.Fatalf("Accept = %+v, want OutOfScope 0, Created == Upserted > 0 under RUOnlyFallback", acc)
	}
}

func TestRunStructureNotFound(t *testing.T) {
	r := testRunner(t)
	src := newFixtureSource(t, []byte(`<html><body><p>нет таблицы</p></body></html>`))
	ctx := context.Background()
	res, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("Run err = %v, want nil (failed is a run outcome)", err)
	}
	if res.Status != ingest.StatusFailed || len(res.Reasons) == 0 || !strings.Contains(res.Reasons[0], "структура не найдена") || res.Upserted != 0 {
		t.Fatalf("Run = %s, %+v; want failed «структура не найдена», Upserted 0", statusOf(res), res)
	}
	if last := lastRun(t, src.code); last.Status != "failed" {
		t.Fatalf("LastRun.Status = %q, want failed", last.Status)
	}
}

// memSource — синтетический источник без сети и HTML: Parse отдаёт
// заданные записи. Режимы: ошибка Fetch, паника Parse, ноль записей.
type memSource struct {
	code       string
	recs       []ingest.SourceEvent
	fetchErr   error
	panicParse bool
	fetches    int
}

func newMemSource(t *testing.T, recs []ingest.SourceEvent) *memSource {
	return &memSource{code: fmt.Sprintf("mem-%s-%d", t.Name(), time.Now().UnixNano()), recs: recs}
}

func (s *memSource) Code() string { return s.code }

func (s *memSource) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.RUOnlyFallback}
}

func (s *memSource) Fetch(context.Context) ([]ingest.RawDoc, error) {
	s.fetches++
	if s.fetchErr != nil {
		return nil, s.fetchErr
	}
	return []ingest.RawDoc{{URL: "https://example.invalid/" + s.code, FetchedAt: fixtureNow}}, nil
}

func (s *memSource) Parse(ingest.RawDoc) ([]ingest.SourceEvent, error) {
	if s.panicParse {
		panic("сломанный парсер")
	}
	return slices.Clone(s.recs), nil
}

// records — n валидных записей с id from..from+n-1 и датами в будущем
// относительно fixtureNow.
func records(from, n int) []ingest.SourceEvent {
	out := make([]ingest.SourceEvent, 0, n)
	for i := from; i < from+n; i++ {
		out = append(out, ingest.SourceEvent{
			ExternalID: fmt.Sprint(i),
			URL:        fmt.Sprintf("https://example.invalid/race/%d", i),
			Title:      fmt.Sprintf("Забег номер %d", i),
			Date:       fixtureNow.AddDate(0, 0, 30+i%300).Format("02/01/2006"),
			Distances:  "10 км",
			City:       "Москва",
			Region:     "Москва",
			TypeLabel:  "shosse",
		})
	}
	return out
}

func lastRun(t *testing.T, code string) store.Run {
	t.Helper()
	ctx := context.Background()
	id, err := testStore.EnsureSource(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	run, err := testStore.LastRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// run выполняет прогон и проверяет статус.
func run(t *testing.T, r *ingest.Runner, src ingest.Source, want ingest.RunStatus) ingest.Result {
	t.Helper()
	res, err := r.Run(context.Background(), src)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != want {
		t.Fatalf("Run = %s, %+v; want %s", statusOf(res), res, want)
	}
	return res
}

func accept(t *testing.T, r *ingest.Runner, src ingest.Source) ingest.Result {
	t.Helper()
	res, err := r.Accept(context.Background(), src, 0)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if res.Status != ingest.StatusOK {
		t.Fatalf("Accept status = %s, want ok", res.Status)
	}
	return res
}

func TestRunFirstRunSuspect(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 10))

	res := run(t, r, src, ingest.StatusSuspect)
	if !hasReason(res, firstRunReason) || res.Created != 0 || res.Upserted != 0 || res.Missing != 0 {
		t.Fatalf("first Run = %s, %+v; want D-05 reason, nothing applied", statusOf(res), res)
	}
	last := lastRun(t, src.code)
	var snap []ingest.SourceEvent
	if err := json.Unmarshal(last.Snapshot, &snap); err != nil || len(snap) != 10 {
		t.Fatalf("snapshot = %d records, %v; want 10", len(snap), err)
	}
	if !strings.Contains(last.Error, firstRunReason) {
		t.Fatalf("ingest_runs.error = %q, want D-05 reason", last.Error)
	}

	again := run(t, r, src, ingest.StatusSuspect)
	if !hasReason(again, firstRunReason) || again.Created != 0 {
		t.Fatalf("second Run before accept = %s, %+v", statusOf(again), again)
	}
}

func TestAcceptBaseline(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 10))
	run(t, r, src, ingest.StatusSuspect)

	res := accept(t, r, src)
	if res.Created != 10 || res.Upserted != 10 {
		t.Fatalf("Accept = %+v, want Created 10, Upserted 10", res)
	}
	if src.fetches != 1 {
		t.Fatalf("Fetch called %d times, want 1: Accept must not contact the site", src.fetches)
	}
	last := lastRun(t, src.code)
	if last.Status != "ok" || last.AcceptedAt == nil || last.Snapshot != nil || last.Upserted != 10 ||
		!strings.Contains(last.Error, "принят вручную") || !strings.Contains(last.Error, firstRunReason) {
		t.Fatalf("LastRun after accept = %+v", last)
	}

	if _, err := r.Accept(context.Background(), src, 0); !errors.Is(err, store.ErrNotAcceptable) {
		t.Fatalf("second Accept err = %v, want ErrNotAcceptable", err)
	}
}

func TestRunSecondRunOK(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 10))
	run(t, r, src, ingest.StatusSuspect)
	accept(t, r, src)

	res := run(t, r, src, ingest.StatusOK)
	if res.Created != 0 || res.Changed != 0 || res.Unchanged != 10 || res.Upserted != 10 {
		t.Fatalf("second Run = %+v, want Created 0, Changed 0, Unchanged 10", res)
	}
}

func TestRunVolumeSuspect(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 30))
	run(t, r, src, ingest.StatusSuspect)
	accept(t, r, src)
	for range 3 {
		run(t, r, src, ingest.StatusOK)
	}

	src.recs = records(1, 10)
	low := run(t, r, src, ingest.StatusSuspect)
	if !hasReason(low, "меньше 50 %") || low.Upserted != 0 || low.Missing != 0 {
		t.Fatalf("drop run = %s, %+v; want «меньше 50 %%», nothing applied", statusOf(low), low)
	}

	src.recs = records(1, 30)
	back := run(t, r, src, ingest.StatusOK)
	if back.Unchanged != 30 || back.Created != 0 || back.Missing != 0 {
		t.Fatalf("run after drop = %+v, want Unchanged 30: the catalog was not touched", back)
	}

	src.recs = records(1, 200)
	high := run(t, r, src, ingest.StatusSuspect)
	if !hasReason(high, "больше 5") || high.Upserted != 0 {
		t.Fatalf("jump run = %s, %+v; want «больше 5×», nothing applied", statusOf(high), high)
	}
	if acc := accept(t, r, src); acc.Created != 170 {
		t.Fatalf("Accept after jump = %+v, want Created 170", acc)
	}
	// Окно базы начинается с принятого прогона (Pitfall 11).
	if next := run(t, r, src, ingest.StatusOK); next.Unchanged != 200 {
		t.Fatalf("run after accepted jump = %+v, want Unchanged 200", next)
	}
}

func TestRunObservation(t *testing.T) {
	r := testRunner(t)
	all := records(1, 10)
	src := newMemSource(t, all)
	run(t, r, src, ingest.StatusSuspect)
	accept(t, r, src)

	src.recs = all[1:]
	if res := run(t, r, src, ingest.StatusOK); res.Missing != 1 || res.Gone != 0 {
		t.Fatalf("ok run without one record = %+v, want Missing 1", res)
	}

	// Отбракованных больше 30 % → suspect: наблюдение не выполняется.
	bad := records(100, 10)
	for i := range bad {
		bad[i].Title = ""
	}
	src.recs = append(slices.Clone(all[1:]), bad...)
	if res := run(t, r, src, ingest.StatusSuspect); res.Missing != 0 || res.Upserted != 0 {
		t.Fatalf("suspect run = %s, %+v; want Missing 0, nothing applied", statusOf(res), res)
	}

	src.recs = all
	if res := run(t, r, src, ingest.StatusOK); res.Missing != 0 || res.Unchanged != 10 {
		t.Fatalf("run with the record back = %+v, want Missing 0, Unchanged 10", res)
	}
}

func TestRunFailures(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 10))
	ctx := context.Background()

	cases := []struct {
		name   string
		set    func()
		reason string
	}{
		{"fetch error", func() { src.fetchErr = errors.New("connection refused") }, "загрузка:"},
		{"parser panic", func() { src.fetchErr, src.panicParse = nil, true }, "паника парсера"},
		{"zero records", func() { src.panicParse, src.recs = false, nil }, "parsed = 0"},
	}
	for _, c := range cases {
		c.set()
		res, err := r.Run(ctx, src)
		if err != nil {
			t.Fatalf("%s: Run err = %v, want nil", c.name, err)
		}
		if res.Status != ingest.StatusFailed || len(res.Reasons) != 1 || !strings.HasPrefix(res.Reasons[0], c.reason) || res.Upserted != 0 {
			t.Fatalf("%s: Run = %s, %+v; want failed «%s…», Upserted 0", c.name, statusOf(res), res, c.reason)
		}
	}

	id, err := testStore.EnsureSource(ctx, src.code)
	if err != nil {
		t.Fatal(err)
	}
	h, err := testStore.SourceHealth(ctx, id)
	if err != nil || h.ConsecutiveFailures != 3 || h.LastStatus != "failed" || h.LastReason != "parsed = 0" {
		t.Fatalf("SourceHealth = %+v, %v; want 3 failures in a row", h, err)
	}
}

func TestRunLongDescription(t *testing.T) {
	r := testRunner(t)
	recs := records(1, 1)
	recs[0].Description = strings.Repeat("ж", 1500)
	src := newMemSource(t, recs)
	run(t, r, src, ingest.StatusSuspect)
	// CHECK (char_length(description) <= 1000) не нарушен (ING-13).
	if res := accept(t, r, src); res.Created != 1 {
		t.Fatalf("Accept = %+v, want Created 1", res)
	}
}

func TestAcceptRefusals(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 5))
	ctx := context.Background()

	if _, err := r.Accept(ctx, src, 0); err == nil {
		t.Fatal("Accept without runs: err = nil")
	}
	first := run(t, r, src, ingest.StatusSuspect)
	second := run(t, r, src, ingest.StatusSuspect)
	_, err := r.Accept(ctx, src, first.RunID)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprint(second.RunID)) {
		t.Fatalf("Accept(not last) err = %v, want the last run id %d", err, second.RunID)
	}
	if _, err := r.Accept(ctx, src, second.RunID); err != nil {
		t.Fatalf("Accept(last): %v", err)
	}
	if _, err := r.Accept(ctx, src, 0); !errors.Is(err, store.ErrNotAcceptable) {
		t.Fatalf("Accept(ok run) err = %v, want ErrNotAcceptable", err)
	}
}

func TestRunSavesRaw(t *testing.T) {
	r := testRunner(t)
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r.Raw = &ingest.RawStore{Dir: dir}
	// Код источника — только [a-z0-9-] (имя каталога сырых ответов).
	src := &fixtureSource{code: fmt.Sprintf("test-raw-%d", time.Now().UnixNano()), body: body}
	res, err := r.Run(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, src.code, fmt.Sprintf("%d-0.html", res.RunID)))
	if err != nil || !slices.Equal(got, body) {
		t.Fatalf("raw file: %d bytes, %v; want the fixture body", len(got), err)
	}

	r.Raw = nil
	src2 := &fixtureSource{code: fmt.Sprintf("test-noraw-%d", time.Now().UnixNano()), body: body}
	if _, err := r.Run(context.Background(), src2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, src2.code)); !os.IsNotExist(err) {
		t.Fatalf("Raw == nil wrote files: %v", err)
	}
}
