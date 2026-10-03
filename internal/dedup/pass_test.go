package dedup_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"aggregator/internal/dedup"
	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// fakeSource — источник без сети: Parse отдаёт заданные записи.
type fakeSource struct {
	code string
	recs []ingest.SourceEvent
}

func (s *fakeSource) Code() string { return s.code }

func (s *fakeSource) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.RUOnlyFallback}
}

func (s *fakeSource) Fetch(context.Context) ([]ingest.RawDoc, error) {
	return []ingest.RawDoc{{URL: "https://example.invalid/" + s.code, FetchedAt: time.Now()}}, nil
}

func (s *fakeSource) Parse(ingest.RawDoc) ([]ingest.SourceEvent, error) {
	return slices.Clone(s.recs), nil
}

// day — дата через n дней от сегодня (UTC) в формате записи источника.
func day(n int) string {
	return time.Now().UTC().AddDate(0, 0, n).Format("02.01.2006")
}

// race — запись забега в Черноисточинске через 10 дней.
func race(ext, title, distances string) ingest.SourceEvent {
	return ingest.SourceEvent{
		ExternalID: ext,
		URL:        "https://example.invalid/race/" + ext,
		Title:      title,
		Date:       day(10),
		Distances:  distances,
		City:       "Черноисточинск",
		Region:     "Свердловская область",
		Country:    "Россия",
		TypeLabel:  "trail",
	}
}

const (
	titleM = "Трейл «Уральский вулкан: гора Липовая»"
	titleP = "Трейл «Гора Липовая — Уральский вулкан»"
)

func marathonec(recs ...ingest.SourceEvent) *fakeSource {
	return &fakeSource{code: "marathonec", recs: recs}
}

func probeg(recs ...ingest.SourceEvent) *fakeSource {
	return &fakeSource{code: "probeg", recs: recs}
}

// passHook — Runner.PostApply с проходом дедупликации.
func passHook(s *store.Store, cfg dedup.Config) func(context.Context) error {
	return func(ctx context.Context) error { return dedup.Pass(ctx, s, cfg) }
}

// runAccept — первый прогон источника (suspect) и его принятие.
func runAccept(t *testing.T, r *ingest.Runner, src ingest.Source) ingest.Result {
	t.Helper()
	ctx := context.Background()
	res, err := r.Run(ctx, src)
	if err != nil || res.Status != ingest.StatusSuspect {
		t.Fatalf("Run %s = %s %v, %v; want suspect", src.Code(), res.Status, res.Reasons, err)
	}
	res, err = r.Accept(ctx, src, 0)
	if err != nil || res.Status != ingest.StatusOK {
		t.Fatalf("Accept %s = %s, %v; want ok", src.Code(), res.Status, err)
	}
	return res
}

func counts(t *testing.T, s *store.Store) (int, int) {
	t.Helper()
	p, f, err := s.CandidateCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p, f
}

func getEvent(t *testing.T, s *store.Store, id int64) event.Event {
	t.Helper()
	e, err := s.GetEvent(context.Background(), id)
	if err != nil {
		t.Fatalf("GetEvent %d: %v", id, err)
	}
	return e
}

func meters(e event.Event) string {
	var out []string
	for _, d := range e.Distances {
		out = append(out, fmt.Sprint(d.Meters))
	}
	return strings.Join(out, ",")
}

func autoConfig() dedup.Config {
	c := dedup.DefaultConfig()
	c.AutoMerge = true
	return c
}

func pass(t *testing.T, s *store.Store, c dedup.Config) {
	t.Helper()
	if err := dedup.Pass(context.Background(), s, c); err != nil {
		t.Fatalf("Pass: %v", err)
	}
}

// allCandidates — вся очередь /dupes.
func allCandidates(t *testing.T, s *store.Store) []store.Candidate {
	t.Helper()
	var out []store.Candidate
	var after int64
	for {
		c, err := s.NextCandidate(context.Background(), after)
		if errors.Is(err, store.ErrNotFound) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
		after = c.ID
	}
}

func TestTracerPass(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	cfg := dedup.DefaultConfig()
	r := &ingest.Runner{Store: s, Fold: cfg.Fold(), PostApply: passHook(s, cfg)}

	runAccept(t, r, marathonec(race("90435", titleM, "20 км, 10 км, 2 км")))
	runAccept(t, r, probeg(race("27", titleP, "20 км, 10 км, 2 км, 1 км")))

	if p, f := counts(t, s); p != 1 || f != 1 {
		t.Fatalf("CandidateCounts = %d, %d; want 1, 1", p, f)
	}
	c, err := s.NextCandidate(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	m, p := c.EventB, c.EventA
	if m >= p {
		t.Fatalf("candidate A %d, B %d: want B (Марафонец) < A (ПроБЕГ)", p, m)
	}
	if getEvent(t, s, m).Title != titleM || getEvent(t, s, p).Title != titleP {
		t.Fatalf("candidate sides %d, %d are not Марафонец, ПроБЕГ", m, p)
	}
	if !strings.Contains(string(c.Reasons), `"name_sim"`) || !strings.Contains(string(c.Reasons), `"score"`) {
		t.Fatalf("reasons = %s", c.Reasons)
	}
	for _, id := range []int64{m, p} {
		if e := getEvent(t, s, id); !e.IsListed || e.MergedIntoID != nil {
			t.Fatalf("event %d listed %v merged %v; want listed, not merged", id, e.IsListed, e.MergedIntoID)
		}
	}
	pass(t, s, cfg)
	if p, _ := counts(t, s); p != 1 {
		t.Fatalf("pending after repeat Pass = %d, want 1", p)
	}

	pass(t, s, autoConfig())
	if p, f := counts(t, s); p != 0 || f != 0 {
		t.Fatalf("CandidateCounts after auto = %d, %d; want 0, 0", p, f)
	}
	loser := getEvent(t, s, p)
	if loser.MergedIntoID == nil || *loser.MergedIntoID != m || loser.IsListed {
		t.Fatalf("loser merged %v listed %v; want merged into %d, hidden", loser.MergedIntoID, loser.IsListed, m)
	}
	if root, err := s.RootEventID(ctx, p); err != nil || root != m {
		t.Fatalf("RootEventID(%d) = %d, %v; want %d", p, root, err, m)
	}
	srcs, err := s.EventSources(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, x := range srcs {
		codes = append(codes, x.Code)
	}
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"marathonec", "probeg"}) {
		t.Fatalf("root sources = %v", codes)
	}
	root := getEvent(t, s, m)
	if got := meters(root); got != "1000,2000,10000,20000" {
		t.Fatalf("root distances = %s", got)
	}
	if root.Title != titleM {
		t.Fatalf("root title = %q, want Марафонец", root.Title)
	}
	t.Logf("TRACER dedup pending=1 merged=auto loser_listed=%t root_sources=%d distances=%s", loser.IsListed, len(codes), meters(root))
}

// addEvent — запись источника code/ext с событием title через days дней
// (Черноисточинск) прямо через store, без прогона; payload — JSON записи.
func addEvent(t *testing.T, s *store.Store, code, ext, title string, days int, payload string) (eventID, rowID int64) {
	t.Helper()
	return addEventMod(t, s, code, ext, title, days, payload, nil)
}

// addEventMod — addEvent с правкой события перед записью (mod может быть nil).
func addEventMod(t *testing.T, s *store.Store, code, ext, title string, days int, payload string, mod func(*event.Event)) (eventID, rowID int64) {
	t.Helper()
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartRun(ctx, src, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, run, store.RunFinish{Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	rowID, _, err = s.UpsertSourceEvent(ctx, src, run, ext, "https://example.invalid/"+code+"/"+ext, []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	e := raceEvent(title, days)
	if mod != nil {
		mod(&e)
	}
	if _, _, err := s.SaveEventFor(ctx, rowID, &e, dedup.DefaultConfig().Fold()); err != nil {
		t.Fatal(err)
	}
	return e.ID, rowID
}

func raceEvent(title string, days int) event.Event {
	y, m, d := time.Now().UTC().AddDate(0, 0, days).Date()
	return event.Event{
		Title: title, TitleNorm: normalize.TitleNorm(title),
		StartDate:   time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
		CountryCode: "RU", Region: "Свердловская область", City: "Черноисточинск",
		CityKey: normalize.CityKey("Черноисточинск"), Type: event.TypeTrail, Status: event.StatusScheduled,
		Distances: []event.Distance{{Meters: 10000}},
	}
}

func isRoot(t *testing.T, s *store.Store, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if e := getEvent(t, s, id); e.MergedIntoID != nil || !e.IsListed {
			t.Fatalf("event %d merged into %v, listed %v; want a listed root", id, e.MergedIntoID, e.IsListed)
		}
	}
}

func TestPassRejected(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	a, _ := addEvent(t, s, "marathonec", "a", titleM, 10, `{}`)
	b, _ := addEvent(t, s, "probeg", "b", titleP, 10, `{}`)
	pass(t, s, dedup.DefaultConfig())
	c, err := s.NextCandidate(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RejectCandidate(ctx, c.ID, 1); err != nil {
		t.Fatal(err)
	}
	pass(t, s, autoConfig())
	isRoot(t, s, a, b)
	if p, f := counts(t, s); p != 0 || f != 0 {
		t.Fatalf("CandidateCounts after reject = %d, %d", p, f)
	}

	// C сливается с A вручную: пара (A∪C, B) остаётся отклонённой по корням.
	cID, _ := addEvent(t, s, "third", "c", titleM, 10, `{}`)
	pass(t, s, dedup.DefaultConfig())
	var ac int64
	for _, x := range allCandidates(t, s) {
		if min(x.EventA, x.EventB) == a && max(x.EventA, x.EventB) == cID {
			ac = x.ID
		}
		if min(x.EventA, x.EventB) == a && max(x.EventA, x.EventB) == b {
			t.Fatal("rejected pair offered again")
		}
	}
	if ac == 0 {
		t.Fatal("no candidate (A, C)")
	}
	if err := s.ConfirmCandidate(ctx, ac, 1, dedup.DefaultConfig().Fold()); err != nil {
		t.Fatal(err)
	}
	pass(t, s, autoConfig())
	isRoot(t, s, a, b)
	if root, err := s.RootEventID(ctx, cID); err != nil || root != a {
		t.Fatalf("RootEventID(C) = %d, %v; want %d", root, err, a)
	}
	if p, _ := counts(t, s); p != 0 {
		t.Fatalf("pending after Pass = %d, want 0: (A∪C, B) is rejected", p)
	}
}

func TestPassSameSource(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	a, rowA := addEvent(t, s, "probeg", "1", titleM, 10, `{}`)
	b, rowB := addEvent(t, s, "probeg", "2", titleM, 10, `{}`)
	pass(t, s, autoConfig())
	isRoot(t, s, a, b)
	if p, _ := counts(t, s); p != 0 {
		t.Fatalf("pending = %d: same-source pair without group_key", p)
	}

	src, _ := s.EnsureSource(ctx, "probeg")
	run, err := s.StartRun(ctx, src, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for ext, row := range map[string]int64{"1": rowA, "2": rowB} {
		id, _, err := s.UpsertSourceEvent(ctx, src, run, ext, "https://example.invalid/probeg/"+ext, []byte(`{"group_key":"series:x"}`))
		if err != nil || id != row {
			t.Fatalf("UpsertSourceEvent %s = %d, %v", ext, id, err)
		}
	}
	pass(t, s, dedup.DefaultConfig())
	c, err := s.NextCandidate(ctx, 0)
	if err != nil {
		t.Fatalf("no candidate with equal group_key: %v", err)
	}
	if !strings.Contains(string(c.Reasons), `"group_key": true`) {
		t.Fatalf("reasons = %s, want group_key true", c.Reasons)
	}
}

func TestPassMaxPerEvent(t *testing.T) {
	s := storetest.Open(t)
	cfg := dedup.DefaultConfig()
	x, _ := addEvent(t, s, "marathonec", "x", titleM, 10, `{}`)
	titles := []string{
		titleP,
		titleM,
		"Трейл «Уральский вулкан»",
		"Уральский вулкан: гора Липовая",
		"Трейл «Гора Липовая»",
	}
	type other struct {
		id  int64
		sim float64
	}
	var others []other
	high := 0
	for i, title := range titles {
		id, _ := addEvent(t, s, "probeg", fmt.Sprint(i), title, 10, `{}`)
		sim := dedup.NameSim(titleM, title)
		if sim < cfg.Low {
			t.Fatalf("precondition: NameSim(%q) = %.2f < Low", title, sim)
		}
		if sim >= cfg.High {
			high++
		}
		others = append(others, other{id, sim})
	}
	if high < 2 {
		t.Fatalf("precondition: %d partners ≥ High, want ≥ 2", high)
	}
	slices.SortStableFunc(others, func(a, b other) int {
		if a.sim != b.sim {
			if a.sim > b.sim {
				return -1
			}
			return 1
		}
		return int(a.id - b.id)
	})
	var want []int64
	for _, o := range others[:cfg.MaxPerEvent] {
		want = append(want, o.id)
	}
	slices.Sort(want)

	pass(t, s, autoConfig())
	isRoot(t, s, x)
	for _, o := range others {
		isRoot(t, s, o.id)
	}
	var got []int64
	for _, c := range allCandidates(t, s) {
		if c.EventB != x {
			t.Fatalf("candidate %+v without X", c)
		}
		got = append(got, c.EventA)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("candidates of X = %v, want top %d by score %v", got, cfg.MaxPerEvent, want)
	}
}

func TestPassStale(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	cfg := dedup.DefaultConfig()
	_, _ = addEvent(t, s, "marathonec", "a", titleM, 10, `{}`)
	_, rowB := addEvent(t, s, "probeg", "b", titleP, 10, `{}`)
	r1, _ := addEvent(t, s, "marathonec", "r", "Ночной забег по набережной", 20, `{}`)
	_, _ = addEvent(t, s, "probeg", "r", "Ночной забег по набережной", 20, `{}`)
	c1, _ := addEvent(t, s, "marathonec", "c", "Осенний кросс у озера", 30, `{}`)
	_, rowC2 := addEvent(t, s, "probeg", "c", "Осенний кросс у озера", 30, `{}`)
	addEvent(t, s, "marathonec", "old", "Весенний полумарафон", -5, `{}`)
	addEvent(t, s, "probeg", "old", "Весенний полумарафон", -5, `{}`)

	pass(t, s, cfg)
	if p, _ := counts(t, s); p != 3 {
		t.Fatalf("pending = %d, want 3 (event out of window not evaluated)", p)
	}
	for _, c := range allCandidates(t, s) {
		switch c.EventB {
		case r1:
			if err := s.RejectCandidate(ctx, c.ID, 1); err != nil {
				t.Fatal(err)
			}
		case c1:
			if err := s.ConfirmCandidate(ctx, c.ID, 1, cfg.Fold()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if p, _ := counts(t, s); p != 1 {
		t.Fatalf("pending = %d, want 1", p)
	}

	other := raceEvent("Совершенно другой старт", 10)
	if _, _, err := s.SaveEventFor(ctx, rowB, &other, cfg.Fold()); err != nil {
		t.Fatal(err)
	}
	pass(t, s, cfg)
	if p, _ := counts(t, s); p != 0 {
		t.Fatalf("pending = %d, want 0: stale candidate kept", p)
	}
	rej, err := s.RejectedPairs(ctx)
	if err != nil || len(rej) != 1 {
		t.Fatalf("RejectedPairs = %v, %v; want the rejected row kept", rej, err)
	}
	if id, err := s.UpsertCandidate(ctx, rowC2, c1, 0.5, []byte(`{}`)); err != nil || id != 0 {
		t.Fatalf("UpsertCandidate(confirmed pair) = %d, %v; want 0: confirmed row kept", id, err)
	}
}

// third — третий источник вне приоритета.
func third(recs ...ingest.SourceEvent) *fakeSource {
	return &fakeSource{code: "third", recs: recs}
}

func TestFoldInvariant(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := storetest.Open(t)
			ctx := context.Background()
			cfg := autoConfig()
			r := &ingest.Runner{Store: s, Fold: cfg.Fold(), PostApply: passHook(s, cfg)}
			p := race("27", titleP, "20 км, 10 км, 2 км, 1 км")
			p.TypeLabel = ""
			srcs := []*fakeSource{
				marathonec(race("90435", titleM, "20 км, 10 км, 2 км")),
				probeg(p),
				third(race("x", titleM, "5 км, 21,1 км")),
			}[:n]
			for _, src := range srcs {
				runAccept(t, r, src)
			}
			for id := int64(1); id <= int64(n); id++ {
				if root, err := s.RootEventID(ctx, id); err != nil || root != 1 {
					t.Fatalf("RootEventID(%d) = %d, %v; want one root 1", id, root, err)
				}
			}
			before := getEvent(t, s, 1)

			order := append(slices.Clone(srcs), srcs...)
			slices.Reverse(order[n:])
			for _, src := range order {
				res, err := r.Run(ctx, src)
				if err != nil || res.Status != ingest.StatusOK {
					t.Fatalf("Run %s = %s %v, %v", src.code, res.Status, res.Reasons, err)
				}
				if res.Changed != 0 || res.Created != 0 {
					t.Fatalf("Run %s: Changed %d, Created %d; want 0", src.code, res.Changed, res.Created)
				}
			}
			after := getEvent(t, s, 1)
			if !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("updated_at moved: %v -> %v", before.UpdatedAt, after.UpdatedAt)
			}
			t.Logf("INVARIANT sources=%d changed=0 updated_at=same", n)
		})
	}
}

func TestParallelRuns(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	cfg := autoConfig()
	r := &ingest.Runner{Store: s, Fold: cfg.Fold(), PostApply: passHook(s, cfg)}
	srcs := []*fakeSource{
		marathonec(race("90435", titleM, "20 км, 10 км, 2 км")),
		probeg(race("27", titleP, "20 км, 10 км, 2 км, 1 км")),
	}
	for _, src := range srcs {
		runAccept(t, r, src)
	}
	if root, err := s.RootEventID(ctx, 2); err != nil || root != 1 {
		t.Fatalf("not merged: RootEventID(2) = %d, %v", root, err)
	}

	errs := make(chan error, 10)
	var wg sync.WaitGroup
	for _, src := range srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				res, err := r.Run(ctx, src)
				if err == nil && res.Status != ingest.StatusOK {
					err = fmt.Errorf("status %s: %v", res.Status, res.Reasons)
				}
				if err != nil {
					errs <- fmt.Errorf("%s: %w", src.code, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestPassFailureKeepsRun(t *testing.T) {
	s := storetest.Open(t)
	var passErr error
	r := &ingest.Runner{Store: s, PostApply: func(ctx context.Context) error {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		passErr = dedup.Pass(cctx, s, dedup.DefaultConfig())
		return passErr
	}}
	src := marathonec(race("1", titleM, "10 км"))
	runAccept(t, r, src)
	passErr = nil
	res, err := r.Run(context.Background(), src)
	if err != nil || res.Status != ingest.StatusOK {
		t.Fatalf("Run = %s, %v; want ok, nil", res.Status, err)
	}
	if passErr == nil {
		t.Fatal("Pass with a cancelled context returned nil")
	}
}
