package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMigration00003(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	run := finishedRun(t, s, src, RunFinish{Status: "failed", Error: "x"})

	if n := scalar[int64](t, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'source_alerts' AND column_name IN ('source_id', 'state', 'run_id', 'notified_at', 'reminded_at')`); n != 5 {
		t.Fatalf("колонок source_alerts = %d, want 5", n)
	}
	const ins = "INSERT INTO source_alerts (source_id, state, run_id) VALUES ($1, $2, $3)"
	if _, err := s.pool.Exec(ctx, ins, src, "ok", run); err == nil {
		t.Error("state = 'ok' должен нарушать CHECK")
	}
	if _, err := s.pool.Exec(ctx, ins, src, "failed", run+1000); err == nil {
		t.Error("несуществующий run_id должен нарушать внешний ключ")
	}
	if _, err := s.pool.Exec(ctx, ins, src, "failed", run); err != nil {
		t.Errorf("корректная строка: %v", err)
	}
}

func TestLatestFinishedRuns(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, _ := s.EnsureSource(ctx, "a-source")
	b, _ := s.EnsureSource(ctx, "b-source")
	c, _ := s.EnsureSource(ctx, "c-source")

	finishedRun(t, s, a, RunFinish{Status: "ok", Parsed: 100})
	suspect := finishedRun(t, s, a, RunFinish{
		Status: "suspect", Parsed: 133, Rejected: 1, OutOfScope: 3, Error: "падение",
		Metrics: []byte(`{"unrecognized_region": 2}`), Snapshot: []byte(`[]`),
	})
	if _, err := s.StartRun(ctx, a, time.Now(), time.Hour); err != nil { // третий, running
		t.Fatal(err)
	}
	finishedRun(t, s, b, RunFinish{Status: "failed", Error: "загрузка: timeout"})
	if _, err := s.StartRun(ctx, c, time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}

	got, err := s.LatestFinishedRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	ra, rb := got[0], got[1]
	if ra.Code != "a-source" || ra.RunID != suspect || ra.Status != "suspect" || !ra.HasSnapshot ||
		ra.Unrecognized != 2 || ra.Parsed != 133 || ra.Rejected != 1 || ra.OutOfScope != 3 || ra.Error != "падение" || ra.FinishedAt.IsZero() {
		t.Errorf("A = %+v", ra)
	}
	if rb.Code != "b-source" || rb.Status != "failed" || rb.Error != "загрузка: timeout" || rb.HasSnapshot || rb.Unrecognized != 0 {
		t.Errorf("B = %+v", rb)
	}
}

func TestAlertStateCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, _ := s.EnsureSource(ctx, "marathonec")
	r1 := finishedRun(t, s, src, RunFinish{Status: "failed", Error: "x"})
	r2 := finishedRun(t, s, src, RunFinish{Status: "suspect", Error: "y"})

	if err := s.ClearAlertState(ctx, src); err != nil {
		t.Fatalf("очистка отсутствующей строки: %v", err)
	}
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 123456000, time.UTC)
	if err := s.SaveAlertState(ctx, AlertState{SourceID: src, State: "failed", RunID: r1, NotifiedAt: t0, RemindedAt: t0}); err != nil {
		t.Fatal(err)
	}
	t1 := t0.Add(25 * time.Hour)
	if err := s.SaveAlertState(ctx, AlertState{SourceID: src, State: "suspect", RunID: r2, NotifiedAt: t0, RemindedAt: t1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AlertStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := got[src]
	if len(got) != 1 || !ok || st.State != "suspect" || st.RunID != r2 || !st.NotifiedAt.Equal(t0) || !st.RemindedAt.Equal(t1) {
		t.Fatalf("AlertStates = %+v", got)
	}
	if err := s.ClearAlertState(ctx, src); err != nil {
		t.Fatal(err)
	}
	if got, err = s.AlertStates(ctx); err != nil || len(got) != 0 {
		t.Fatalf("после очистки: %+v, %v", got, err)
	}
}

func TestRecentRuns(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, _ := s.EnsureSource(ctx, "a-source")
	b, _ := s.EnsureSource(ctx, "b-source")
	var aIDs []int64
	for range 4 {
		aIDs = append(aIDs, finishedRun(t, s, a, RunFinish{Status: "ok", Parsed: 5}))
	}
	aIDs = append(aIDs, finishedRun(t, s, a, RunFinish{Status: "suspect", Parsed: 7, Error: "причина", Snapshot: []byte(`[]`)}))
	bID := finishedRun(t, s, b, RunFinish{Status: "failed", Error: "сбой"})

	got, err := s.RecentRuns(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(got), got)
	}
	for i, wantID := range []int64{aIDs[4], aIDs[3], aIDs[2]} {
		if got[i].Code != "a-source" || got[i].ID != wantID {
			t.Errorf("got[%d] = %s #%d, want a-source #%d", i, got[i].Code, got[i].ID, wantID)
		}
	}
	first := got[0]
	if first.Status != "suspect" || first.Parsed != 7 || first.Error != "причина" || !first.HasSnapshot ||
		first.StartedAt.IsZero() || first.FinishedAt == nil {
		t.Errorf("suspect = %+v", first)
	}
	if last := got[3]; last.Code != "b-source" || last.ID != bID || last.Status != "failed" || last.Error != "сбой" || last.HasSnapshot {
		t.Errorf("B = %+v", last)
	}
}

func TestRunSource(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, _ := s.EnsureSource(ctx, "marathonec")
	run := finishedRun(t, s, src, RunFinish{Status: "ok"})

	if code, err := s.RunSource(ctx, run); err != nil || code != "marathonec" {
		t.Fatalf("RunSource = %q, %v", code, err)
	}
	if _, err := s.RunSource(ctx, run+1000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("несуществующий прогон: err = %v, want ErrNotFound", err)
	}
}
