package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
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
	ctx := context.Background()
	s, err := openTestStore(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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

// openTestStore открывает базу тестов. Тесты пишут в базу: только в
// тестовую, не в dev-БД с данными (D-03).
// Имя берётся из current_database(), а не из URL: ?dbname= в строке
// подключения переопределяет путь.
func openTestStore(ctx context.Context, dsn string) (*store.Store, error) {
	s, err := store.Open(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open test store: %w", err)
	}
	name, err := s.DatabaseName(ctx)
	if err == nil && !strings.HasSuffix(name, "_test") {
		err = errors.New("TEST_DATABASE_URL: имя базы должно оканчиваться на _test")
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
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
	// W-1: строка принятого прогона — счётчики и metrics по политике
	// принятия, а не по оценке suspect.
	last := lastRun(t, src.code)
	var m struct {
		OutOfScope *int `json:"out_of_scope"`
		Created    *int `json:"created"`
	}
	if err := json.Unmarshal(last.Metrics, &m); err != nil {
		t.Fatal(err)
	}
	if last.OutOfScope != 0 || last.Rejected != acc.Rejected || m.OutOfScope == nil || *m.OutOfScope != 0 ||
		m.Created == nil || *m.Created != acc.Created {
		t.Fatalf("accepted run: out_of_scope %d, rejected %d, metrics %s; want 0, %d, out_of_scope 0, created %d",
			last.OutOfScope, last.Rejected, last.Metrics, acc.Rejected, acc.Created)
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
	// onPolicy вызывается из Policy — точки между проверкой прогона в
	// Accept и записью; cancel отменяет контекст прогона внутри Fetch.
	onPolicy func()
	cancel   context.CancelFunc
	// outOfScopeMax — Policy.OutOfScopeMax источника.
	outOfScopeMax float64
}

func newMemSource(t *testing.T, recs []ingest.SourceEvent) *memSource {
	return &memSource{code: fmt.Sprintf("mem-%s-%d", t.Name(), time.Now().UnixNano()), recs: recs}
}

func (s *memSource) Code() string { return s.code }

func (s *memSource) Policy() ingest.Policy {
	if f := s.onPolicy; f != nil {
		s.onPolicy = nil
		f()
	}
	return ingest.Policy{CountryFallback: normalize.RUOnlyFallback, OutOfScopeMax: s.outOfScopeMax}
}

func (s *memSource) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	s.fetches++
	if s.cancel != nil {
		s.cancel()
		return nil, fmt.Errorf("fetch: %w", ctx.Err())
	}
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

// ME-01: прогон, начатый демоном после проверки в Accept, но до записи,
// делает принятие невозможным — и отказ не должен ничего записать.
func TestAcceptRefusedWritesNothing(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 10))
	ctx := context.Background()
	run(t, r, src, ingest.StatusSuspect)

	var daemon int64
	var startErr error
	src.onPolicy = func() {
		id, err := testStore.EnsureSource(ctx, src.code)
		if err != nil {
			startErr = err
			return
		}
		daemon, startErr = testStore.StartRun(ctx, id, fixtureNow, time.Hour)
	}
	if _, err := r.Accept(ctx, src, 0); !errors.Is(err, store.ErrNotAcceptable) {
		t.Fatalf("Accept err = %v, want ErrNotAcceptable", err)
	}
	if startErr != nil {
		t.Fatalf("StartRun during Accept: %v", startErr)
	}
	// Прогон демона завершился ok: каталог должен остаться пустым.
	if err := testStore.FinishRun(ctx, daemon, store.RunFinish{Status: "ok", Parsed: 10}); err != nil {
		t.Fatal(err)
	}
	if res := run(t, r, src, ingest.StatusOK); res.Created != 10 {
		t.Fatalf("run after refused Accept = %+v, want Created 10: the refused Accept wrote the catalog", res)
	}
}

// ME-02: строка на странице, отбракованная или ставшая expired, —
// найдена прогоном: она не уходит в missing/gone.
func TestRunRejectedRowStaysSeen(t *testing.T) {
	r := testRunner(t)
	r.GoneAfterRuns, r.GoneAfter = 1, time.Nanosecond
	all := records(1, 10)
	src := newMemSource(t, all)
	run(t, r, src, ingest.StatusSuspect)
	accept(t, r, src)

	src.recs = slices.Clone(all)
	src.recs[0].Date = "уточняется"
	src.recs[1].Date = fixtureNow.AddDate(0, 0, -10).Format("02/01/2006")
	res := run(t, r, src, ingest.StatusOK)
	if res.Rejected != 1 || res.Expired != 1 || res.Missing != 0 || res.Gone != 0 {
		t.Fatalf("run with rejected and expired rows = %+v, want Rejected 1, Expired 1, Missing 0, Gone 0", res)
	}
}

// ME-03: сбой БД посреди записи прогона не оставляет каталог применённым
// наполовину: прогон failed, ничего не записано.
func TestRunWriteFailureAppliesNothing(t *testing.T) {
	r := testRunner(t)
	all := records(1, 10)
	src := newMemSource(t, all)
	run(t, r, src, ingest.StatusSuspect)
	accept(t, r, src)

	renamed := slices.Clone(all)
	for i := range renamed {
		renamed[i].Title += " (новое название)"
	}
	// NUL в названии: jsonb payload последней записи отвергается БД.
	bad := records(11, 1)
	bad[0].Title = "Забег\x00"
	src.recs = append(renamed, bad...)
	res, err := r.Run(context.Background(), src)
	if err == nil || res.Status != ingest.StatusFailed {
		t.Fatalf("Run = %s, %+v, err %v; want failed with an error", statusOf(res), res, err)
	}
	if last := lastRun(t, src.code); last.Status != "failed" || last.Upserted != 0 {
		t.Fatalf("failed run row = %+v, want failed, upserted 0", last)
	}

	src.recs = all
	if res := run(t, r, src, ingest.StatusOK); res.Changed != 0 || res.Unchanged != 10 || res.Created != 0 {
		t.Fatalf("run after failed write = %+v, want Changed 0, Unchanged 10: the failed run changed the catalog", res)
	}
}

// LO-08: отмена контекста (SIGTERM) во время загрузки — не сбой
// источника и не удвоение интервала.
func TestRunCancelledIsNotFailure(t *testing.T) {
	r := testRunner(t)
	src := newMemSource(t, records(1, 5))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src.cancel = cancel

	res, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasReason(res, "прерван") {
		t.Fatalf("Run = %s, want the interrupted reason", statusOf(res))
	}
	id, err := testStore.EnsureSource(context.Background(), src.code)
	if err != nil {
		t.Fatal(err)
	}
	h, err := testStore.SourceHealth(context.Background(), id)
	if err != nil || h.ConsecutiveFailures != 0 {
		t.Fatalf("SourceHealth = %+v, %v; want ConsecutiveFailures 0", h, err)
	}
}

// W-3: защита тестовой базы смотрит на current_database(), а не на путь
// URL: ?dbname= не обходит её.
func TestTestDBGuard(t *testing.T) {
	testRunner(t)
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("dbname", "postgres")
	u.RawQuery = q.Encode()
	s, err := openTestStore(context.Background(), u.String())
	if s != nil {
		s.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "_test") {
		t.Fatalf("openTestStore(path *_test, ?dbname=postgres) err = %v, want the _test refusal", err)
	}
}

// TestRunSeriesEndDate — tracer: запись с EndDate и серия с GroupKey
// проходят Run → Accept до событий в PostgreSQL (ING-12, D-03, DDP-05).
func TestRunSeriesEndDate(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()
	city := fmt.Sprintf("Трассерск%d", time.Now().UnixNano())
	day := func(n int) string { return fixtureNow.AddDate(0, 0, n).Format("02/01/2006") }
	rec := func(id, title string, date int) ingest.SourceEvent {
		return ingest.SourceEvent{ExternalID: id, URL: "https://example.invalid/race/" + id, Title: title,
			Date: day(date), Distances: "5 км", City: city, Region: "Москва", TypeLabel: "shosse"}
	}
	single := rec("single", "Трассерский забег", 40)
	single.EndDate = day(42)
	recs := []ingest.SourceEvent{single}
	for i, d := range []int{21, 7, 14, -7} {
		s := rec(fmt.Sprint("s", i), "Трассерский паркран", d)
		s.GroupKey = "series:трассерский паркран"
		recs = append(recs, s)
	}
	src := newMemSource(t, recs)

	run(t, r, src, ingest.StatusSuspect)
	res := accept(t, r, src)
	if res.Created != 2 || res.Upserted != 2 {
		t.Fatalf("Accept = %+v, want Created 2, Upserted 2", res)
	}
	items, err := testStore.ListEvents(ctx, event.Filter{CityKey: normalize.CityKey(city)}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("ListEvents = %+v, want 2 events", items)
	}
	var nearest, endDate string
	for _, it := range items {
		switch it.Title {
		case "Трассерский паркран":
			nearest = it.StartDate.Format(time.DateOnly)
		case "Трассерский забег":
			e, err := testStore.GetEvent(ctx, it.ID)
			if err != nil {
				t.Fatal(err)
			}
			if e.EndDate != nil {
				endDate = e.EndDate.Format(time.DateOnly)
			}
		}
	}
	if nearest != "2026-10-06" || endDate != "2026-11-10" {
		t.Fatalf("серия start_date = %q, end_date одиночного = %q; want 2026-10-06, 2026-11-10", nearest, endDate)
	}

	again := run(t, r, src, ingest.StatusOK)
	if again.Created != 0 || again.Changed != 0 || again.Unchanged != 2 {
		t.Fatalf("second Run = %+v, want Created 0, Changed 0, Unchanged 2", again)
	}
	t.Logf("TRACER created=%d events=%d nearest=%s end_date=%s", res.Created, len(items), nearest, endDate)
}

// TestRunOutOfScopeMax — порог out_of_scope источника заменяет общий
// только для его прогонов.
func TestRunOutOfScopeMax(t *testing.T) {
	r := testRunner(t)
	recs := records(1, 10)
	for i := range 3 {
		recs[i].Country = "Казахстан"
	}
	common, own := newMemSource(t, recs), newMemSource(t, recs)
	own.code += "-own"
	own.outOfScopeMax = 0.40
	for _, src := range []*memSource{common, own} {
		run(t, r, src, ingest.StatusSuspect)
		accept(t, r, src)
	}
	if res := run(t, r, common, ingest.StatusSuspect); !hasReason(res, "доля вне каталога (out_of_scope)") {
		t.Fatalf("общий порог: %s", statusOf(res))
	}
	run(t, r, own, ingest.StatusOK)
}

func TestPostApply(t *testing.T) {
	r := testRunner(t)
	var buf bytes.Buffer
	r.Log = slog.New(slog.NewTextHandler(&buf, nil))
	calls := 0
	var postErr error
	r.PostApply = func(ctx context.Context) error {
		calls++
		if ctx.Err() != nil {
			t.Errorf("PostApply: контекст отменён: %v", ctx.Err())
		}
		return postErr
	}
	src := newMemSource(t, records(1, 10))
	check := func(step string, want int) {
		t.Helper()
		if calls != want {
			t.Fatalf("%s: PostApply вызван %d раз, want %d", step, calls, want)
		}
	}

	run(t, r, src, ingest.StatusSuspect)
	check("suspect", 0)
	accept(t, r, src)
	check("Accept", 1)
	run(t, r, src, ingest.StatusOK)
	check("ok", 2)
	src.fetchErr = errors.New("сеть")
	run(t, r, src, ingest.StatusFailed)
	check("failed", 2)
	if _, err := r.Accept(context.Background(), src, 0); !errors.Is(err, store.ErrNotAcceptable) {
		t.Fatalf("Accept после failed: err = %v, want ErrNotAcceptable", err)
	}
	check("отказ Accept", 2)

	src.fetchErr, postErr = nil, errors.New("дедупликация")
	run(t, r, src, ingest.StatusOK)
	check("ok с ошибкой PostApply", 3)
	if !strings.Contains(buf.String(), "post-apply failed") {
		t.Fatalf("в логе нет post-apply failed:\n%s", buf.String())
	}
	if last := lastRun(t, src.code); last.Status != "ok" {
		t.Fatalf("LastRun = %s, want ok", last.Status)
	}
}
