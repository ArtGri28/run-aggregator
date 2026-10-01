package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/event"
	"aggregator/migrations"
)

var testFold = event.FoldConfig{Priority: []string{"marathonec", "probeg"}, DistanceTolM: 100}

// linked создаёт запись источника code/ext и её событие e; возвращает id
// события и записи.
func linked(t *testing.T, s *Store, code, ext string, e *event.Event) (eventID, rowID int64) {
	t.Helper()
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	rowID, _, err = s.UpsertSourceEvent(ctx, src, newRun(t, s, src), ext, "https://example.test/"+code+"/"+ext, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SaveEventFor(ctx, rowID, e, testFold); err != nil {
		t.Fatalf("SaveEventFor %s/%s: %v", code, ext, err)
	}
	return e.ID, rowID
}

func isNotMergeable(err error) bool { return errors.Is(err, ErrNotMergeable) }

func TestMigration00004(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	cols := func() int64 {
		return scalar[int64](t, `
			SELECT count(*) FROM information_schema.columns
			WHERE is_nullable = 'YES' AND (
				(table_name = 'source_events' AND column_name = 'fields') OR
				(table_name = 'dedup_candidates' AND column_name IN ('decided_at', 'decided_by', 'notified_at')))`)
	}
	index := func() int64 {
		return scalar[int64](t, "SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_dedup_candidates_state'")
	}
	if got := cols(); got != 4 {
		t.Fatalf("nullable new columns = %d, want 4", got)
	}
	if got := index(); got != 1 {
		t.Fatalf("idx_dedup_candidates_state = %d, want 1", got)
	}
	const fk = `
		SELECT c.confdeltype::text FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND c.conrelid = $1::regclass AND a.attname = $2`
	for _, f := range [][2]string{{"source_events", "event_id"}, {"events", "merged_into_id"}} {
		if got := scalar[string](t, fk, f[0], f[1]); got != "r" {
			t.Fatalf("%s.%s on delete = %q, want r (RESTRICT)", f[0], f[1], got)
		}
	}

	db := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	// При падении посередине база не остаётся на версии 3.
	defer func() {
		if _, err := p.Up(context.Background()); err != nil {
			t.Errorf("restore Up: %v", err)
		}
	}()
	if _, err := p.DownTo(ctx, 3); err != nil {
		t.Fatalf("DownTo(3): %v", err)
	}
	if got := scalar[int64](t, `
		SELECT count(*) FROM information_schema.columns
		WHERE (table_name = 'source_events' AND column_name = 'fields') OR
		      (table_name = 'dedup_candidates' AND column_name IN ('decided_at', 'decided_by', 'notified_at'))`); got != 0 {
		t.Fatalf("columns after Down = %d, want 0", got)
	}
	if got := index(); got != 0 {
		t.Fatalf("index after Down = %d, want 0", got)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if cols() != 4 || index() != 1 {
		t.Fatal("Up did not restore columns and index")
	}
}

func TestSaveEventForFold(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := sampleEvent()
	m.Distances = []event.Distance{{Meters: 21100, Label: "21,1 км"}}
	w, rowM := linked(t, s, "marathonec", "1", m)
	newP := func(title string) *event.Event {
		p := sampleEvent()
		p.Title = title
		p.Distances = []event.Distance{{Meters: 21097}, {Meters: 42195}}
		return p
	}
	l, rowP := linked(t, s, "probeg", "1", newP("Марафон Петербурга"))
	if err := s.MergeEvents(ctx, l, w, "auto", nil, testFold); err != nil {
		t.Fatal(err)
	}
	if got := meters(t, s, w); got != "21100,42195" {
		t.Fatalf("distances after merge = %s, want union within 100 m", got)
	}

	p := newP("Совсем другое название")
	created, changed, err := s.SaveEventFor(ctx, rowP, p, testFold)
	if err != nil || created || changed {
		t.Fatalf("SaveEventFor probeg = %v, %v, %v; want unchanged", created, changed, err)
	}
	if p.ID != w || p.Title != m.Title {
		t.Fatalf("probeg save: id %d title %q; want %d %q", p.ID, p.Title, w, m.Title)
	}

	m2 := sampleEvent()
	m2.Title = "Новое название Марафонца"
	m2.Distances = m.Distances
	if _, changed, err := s.SaveEventFor(ctx, rowM, m2, testFold); err != nil || !changed {
		t.Fatalf("SaveEventFor marathonec = %v, %v; want changed", changed, err)
	}
	if got := scalar[string](t, "SELECT title FROM events WHERE id = $1", w); got != m2.Title {
		t.Fatalf("title = %q, want %q", got, m2.Title)
	}
	if !scalar[bool](t, `SELECT fields ? 'title' AND fields ? 'start_date' AND fields ? 'city_key'
		AND fields ? 'distances' AND NOT fields ? 'status' AND NOT fields ? 'id' FROM source_events WHERE id = $1`, rowM) {
		t.Fatalf("fields = %s", scalar[string](t, "SELECT fields::text FROM source_events WHERE id = $1", rowM))
	}

	// Запись gone не участвует в чужой записи, своя — участвует всегда.
	exec(t, "UPDATE source_events SET state = 'gone' WHERE id = $1", rowP)
	m3 := sampleEvent()
	m3.Title, m3.Distances = m2.Title, m.Distances
	if _, _, err := s.SaveEventFor(ctx, rowM, m3, testFold); err != nil {
		t.Fatal(err)
	}
	if got := meters(t, s, w); got != "21100" {
		t.Fatalf("distances without gone row = %s, want 21100", got)
	}
	if _, _, err := s.SaveEventFor(ctx, rowP, newP("x"), testFold); err != nil {
		t.Fatal(err)
	}
	if got := meters(t, s, w); got != "21100,42195" {
		t.Fatalf("distances with own gone row = %s, want 21100,42195", got)
	}
}

func meters(t *testing.T, s *Store, id int64) string {
	t.Helper()
	e, err := s.GetEvent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range e.Distances {
		out = append(out, fmt.Sprint(d.Meters))
	}
	return strings.Join(out, ",")
}

func TestMergeEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	w, _ := linked(t, s, "marathonec", "w", sampleEvent())
	l, rowL := linked(t, s, "probeg", "l", sampleEvent())
	c, rowC := linked(t, s, "probeg", "c", sampleEvent())
	_, rowX := linked(t, s, "third", "x", sampleEvent())
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", l, c)

	pair, err := s.UpsertCandidate(ctx, rowL, w, 0.9, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.UpsertCandidate(ctx, rowX, l, 0.8, []byte(`{}`))
	rej, _ := s.UpsertCandidate(ctx, rowC, l, 0.5, []byte(`{}`))
	exec(t, "UPDATE dedup_candidates SET state = 'rejected' WHERE id = $1", rej)

	nEvents := scalar[int64](t, "SELECT count(*) FROM events")
	nRows := scalar[int64](t, "SELECT count(*) FROM source_events")
	admin := int64(777)
	if err := s.MergeEvents(ctx, l, w, "manual", &admin, testFold); err != nil {
		t.Fatal(err)
	}
	if scalar[int64](t, "SELECT count(*) FROM events") != nEvents || scalar[int64](t, "SELECT count(*) FROM source_events") != nRows {
		t.Fatal("merge changed the number of events or source_events")
	}
	if got := scalar[string](t, "SELECT event_id || ':' || match_state FROM source_events WHERE id = $1", rowL); got != itoa(w)+":manual" {
		t.Fatalf("loser row = %s, want %d:manual", got, w)
	}
	if got := scalar[string](t, "SELECT merged_into_id || ':' || is_listed FROM events WHERE id = $1", l); got != itoa(w)+":false" {
		t.Fatalf("loser = %s", got)
	}
	if got := scalar[int64](t, "SELECT merged_into_id FROM events WHERE id = $1", c); got != w {
		t.Fatalf("chain not collapsed: C.merged_into_id = %d, want %d", got, w)
	}
	if root, err := s.RootEventID(ctx, c); err != nil || root != w {
		t.Fatalf("RootEventID(C) = %d, %v", root, err)
	}
	if got := scalar[string](t, "SELECT state || ':' || decided_by || ':' || (decided_at IS NOT NULL) FROM dedup_candidates WHERE id = $1", pair); got != "confirmed:777:true" {
		t.Fatalf("pair candidate = %s", got)
	}
	if got := scalar[int64](t, "SELECT count(*) FROM dedup_candidates WHERE id = $1", other); got != 0 {
		t.Fatal("pending (X, L) not removed")
	}
	if got := scalar[string](t, "SELECT state FROM dedup_candidates WHERE id = $1", rej); got != "rejected" {
		t.Fatalf("rejected candidate = %s", got)
	}

	for _, tc := range []struct {
		name          string
		loser, winner int64
		state         string
		check         func(error) bool
	}{
		{"same", l, l, "auto", isNotMergeable},
		{"already merged", l, w, "auto", isNotMergeable},
		{"winner larger", w, l, "auto", func(err error) bool { return err != nil }},
		{"bad state", l, w, "x", func(err error) bool { return err != nil }},
		{"missing", 999999, w, "auto", isNotFound},
	} {
		if err := s.MergeEvents(ctx, tc.loser, tc.winner, tc.state, nil, testFold); !tc.check(err) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func itoa(n int64) string { return fmt.Sprint(n) }

func TestMergeEventsConcurrent(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := range 20 {
		ext := itoa(int64(i))
		w, rowW := linked(t, s, "marathonec", ext, sampleEvent())
		l, rowL := linked(t, s, "probeg", ext, sampleEvent())
		rows := []int64{rowW, rowL}
		if i%2 == 1 {
			rows = []int64{rowL, rowW}
		}
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for _, r := range rows {
				if _, _, err := s.SaveEventFor(ctx, r, sampleEvent(), testFold); err != nil {
					errs <- err
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			if err := s.MergeEvents(ctx, l, w, "auto", nil, testFold); err != nil {
				errs <- err
			}
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
				t.Fatalf("iteration %d: deadlock: %v", i, err)
			}
			t.Fatalf("iteration %d: %v", i, err)
		}
		if got := scalar[int64](t, "SELECT count(*) FROM source_events WHERE id IN ($1, $2) AND event_id = $3", rowW, rowL, w); got != 2 {
			t.Fatalf("iteration %d: %d rows at winner, want 2", i, got)
		}
		if root, err := s.RootEventID(ctx, l); err != nil || root != w {
			t.Fatalf("iteration %d: RootEventID(L) = %d, %v", i, root, err)
		}
	}
}

func TestCandidateQueue(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var ev, row [6]int64
	for i := 1; i <= 5; i++ {
		code := "marathonec"
		if i%2 == 0 {
			code = "probeg"
		}
		ev[i], row[i] = linked(t, s, code, itoa(int64(i)), sampleEvent())
	}
	c1, _ := s.UpsertCandidate(ctx, row[2], ev[1], 0.9, []byte(`{"score":0.9}`))
	c2, _ := s.UpsertCandidate(ctx, row[5], ev[4], 0.8, []byte(`{}`))
	c3, _ := s.UpsertCandidate(ctx, row[4], ev[3], 0.7, []byte(`{}`))
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", ev[3], ev[4]) // сторона c2 слита, c3 — в одном корне

	got, err := s.NextCandidate(ctx, 0)
	if err != nil || got.ID != c1 || got.EventA != ev[2] || got.EventB != ev[1] || got.Score != 0.9 || string(got.Reasons) != `{"score": 0.9}` {
		t.Fatalf("NextCandidate(0) = %+v (%s), %v", got, got.Reasons, err)
	}
	got, err = s.NextCandidate(ctx, c1)
	if err != nil || got.ID != c2 || got.EventA != ev[5] || got.EventB != ev[3] {
		t.Fatalf("NextCandidate(c1) = %+v, %v; want c2 with B = root %d", got, err, ev[3])
	}
	if _, err := s.NextCandidate(ctx, c2); !isNotFound(err) {
		t.Fatalf("NextCandidate(c2) err = %v, want ErrNotFound (c3 sides share a root)", err)
	}
	if _, err := s.NextCandidate(ctx, c3); !isNotFound(err) {
		t.Fatalf("NextCandidate(last) err = %v", err)
	}
	if got, err := s.CandidateByID(ctx, c1); err != nil || got.ID != c1 {
		t.Fatalf("CandidateByID(c1) = %+v, %v", got, err)
	}
	if _, err := s.CandidateByID(ctx, 999999); !isNotFound(err) {
		t.Fatalf("CandidateByID(missing) err = %v", err)
	}

	if err := s.RejectCandidate(ctx, c1, 42); err != nil {
		t.Fatal(err)
	}
	if got := scalar[string](t, "SELECT state || ':' || decided_by || ':' || (decided_at IS NOT NULL) FROM dedup_candidates WHERE id = $1", c1); got != "rejected:42:true" {
		t.Fatalf("rejected = %s", got)
	}
	if _, err := s.CandidateByID(ctx, c1); !isNotFound(err) {
		t.Fatalf("CandidateByID(rejected) err = %v", err)
	}
	if err := s.RejectCandidate(ctx, c1, 42); !isNotMergeable(err) {
		t.Fatalf("RejectCandidate again err = %v", err)
	}
	if err := s.RejectCandidate(ctx, 999999, 42); !isNotFound(err) {
		t.Fatalf("RejectCandidate(missing) err = %v", err)
	}
	if id, err := s.UpsertCandidate(ctx, row[2], ev[1], 0.1, []byte(`{}`)); err != nil || id != 0 {
		t.Fatalf("UpsertCandidate(rejected pair) = %d, %v; want 0", id, err)
	}
	if got := scalar[float64](t, "SELECT score::float8 FROM dedup_candidates WHERE id = $1", c1); got != 0.9 {
		t.Fatalf("rejected score changed to %v", got)
	}

	if err := s.ConfirmCandidate(ctx, c2, 43, testFold); err != nil {
		t.Fatal(err)
	}
	if root, err := s.RootEventID(ctx, ev[5]); err != nil || root != ev[3] {
		t.Fatalf("after confirm RootEventID(5) = %d, %v; want %d", root, err, ev[3])
	}
	if got := scalar[string](t, "SELECT match_state FROM source_events WHERE id = $1", row[5]); got != "manual" {
		t.Fatalf("moved row match_state = %s", got)
	}
	if got := scalar[string](t, "SELECT state || ':' || decided_by FROM dedup_candidates WHERE id = $1", c2); got != "confirmed:43" {
		t.Fatalf("confirmed = %s", got)
	}
	if err := s.ConfirmCandidate(ctx, c2, 43, testFold); !isNotMergeable(err) {
		t.Fatalf("ConfirmCandidate again err = %v", err)
	}
	if err := s.ConfirmCandidate(ctx, 999999, 43, testFold); !isNotFound(err) {
		t.Fatalf("ConfirmCandidate(missing) err = %v", err)
	}
}

func TestCandidateDigest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	check := func(wantP, wantF int) {
		t.Helper()
		p, f, err := s.CandidateCounts(ctx)
		if err != nil || p != wantP || f != wantF {
			t.Fatalf("CandidateCounts = %d, %d, %v; want %d, %d", p, f, err, wantP, wantF)
		}
	}
	check(0, 0)
	if at, err := s.LastDigestAt(ctx); err != nil || !at.IsZero() {
		t.Fatalf("LastDigestAt empty = %v, %v", at, err)
	}
	var ev, row [5]int64
	for i := 1; i <= 4; i++ {
		ev[i], row[i] = linked(t, s, "marathonec", itoa(int64(i)), sampleEvent())
	}
	s.UpsertCandidate(ctx, row[2], ev[1], 0.5, []byte(`{}`))
	c2, _ := s.UpsertCandidate(ctx, row[3], ev[1], 0.5, []byte(`{}`))
	check(2, 2)
	if err := s.MarkCandidatesNotified(ctx); err != nil {
		t.Fatal(err)
	}
	check(2, 0)
	if at, err := s.LastDigestAt(ctx); err != nil || time.Since(at).Abs() > time.Minute {
		t.Fatalf("LastDigestAt = %v, %v; want about now", at, err)
	}
	s.UpsertCandidate(ctx, row[4], ev[1], 0.5, []byte(`{}`))
	check(3, 1)
	if err := s.RejectCandidate(ctx, c2, 1); err != nil {
		t.Fatal(err)
	}
	check(2, 1)
}
