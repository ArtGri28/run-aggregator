package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/event"
	"aggregator/migrations"
)

const unmergeDoc = "../../.planning/phases/07-deduplication-transparency/07-UNMERGE.md"

// inventorySQL — запрос инвентаризации из 07-UNMERGE.md (блок ```sql):
// тест выполняет ровно тот текст, что описан в документе.
func inventorySQL(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(unmergeDoc)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(doc), "```sql\n")
	q, _, ok2 := strings.Cut(rest, "```")
	if !ok || !ok2 {
		t.Fatalf("%s: нет блока sql", unmergeDoc)
	}
	q = strings.TrimSuffix(strings.TrimSpace(q), ";")
	if strings.Join(strings.Fields(q), " ") != strings.Join(strings.Fields(inventoryQuery), " ") {
		t.Fatal("запрос в 07-UNMERGE.md расходится с inventoryQuery в unmerge.go")
	}
	return q
}

// ingested — запись источника и её событие одной транзакцией, как в прогоне
// (Runner.apply): created_at события = first_seen_at записи.
func ingested(t *testing.T, s *Store, code, ext, title string, meters ...int) (eventID, rowID int64) {
	t.Helper()
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	run := newRun(t, s, src)
	e := sampleEvent()
	e.Title, e.TitleNorm = title, strings.ToLower(title)
	for _, m := range meters {
		e.Distances = append(e.Distances, event.Distance{Meters: m})
	}
	err = s.InTx(ctx, func(tx *Store) error {
		var err error
		if rowID, _, err = tx.UpsertSourceEvent(ctx, src, run, ext, "https://example.test/"+code+"/"+ext, []byte(`{}`)); err != nil {
			return err
		}
		_, _, err = tx.SaveEventFor(ctx, rowID, e, testFold)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return e.ID, rowID
}

// confirm — ручное слияние через кандидата, как /dupes: запись стороны с
// бо́льшим id, событие — сторона с меньшим.
func confirm(t *testing.T, s *Store, row, into int64) {
	t.Helper()
	ctx := context.Background()
	cid, err := s.UpsertCandidate(ctx, row, into, 0.9, []byte(`{}`))
	if err != nil || cid == 0 {
		t.Fatalf("UpsertCandidate = %d, %v", cid, err)
	}
	if err := s.ConfirmCandidate(ctx, cid, 42, testFold); err != nil {
		t.Fatal(err)
	}
}

func dbSnapshot(t *testing.T) string {
	t.Helper()
	return scalar[string](t, `SELECT jsonb_build_object(
		'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM events e),
		'source_events', (SELECT jsonb_agg(to_jsonb(se) ORDER BY se.id) FROM source_events se),
		'distances', (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.event_id, d.distance_m) FROM event_distances d),
		'candidates', (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM dedup_candidates c))::text`)
}

// TestUnmergeInventory — 07-04, задача 1: запрос инвентаризации относит
// слияния к классам A, B, C и ничего не меняет.
func TestUnmergeInventory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// A: Q слит в P вручную, у Q одна запись.
	p, _ := ingested(t, s, "marathonec", "p", "Марафон А")
	q, rowQ := ingested(t, s, "probeg", "q", "Марафон А")
	confirm(t, s, rowQ, p)
	// Цепочка: N слит в M, затем M в K. N — A (одна запись), M — B (поглощал N).
	k, _ := ingested(t, s, "marathonec", "k", "Забег Б")
	m, rowM := ingested(t, s, "probeg", "m", "Забег Б")
	n, rowN := ingested(t, s, "third", "n", "Забег Б")
	confirm(t, s, rowN, m)
	confirm(t, s, rowM, k)
	// C: автослияние без кандидата.
	tt, _ := ingested(t, s, "marathonec", "t", "Кросс В")
	u, _ := ingested(t, s, "probeg", "u", "Кросс В")
	if err := s.MergeEvents(ctx, u, tt, string(event.MatchAuto), nil, testFold); err != nil {
		t.Fatal(err)
	}

	before := dbSnapshot(t)
	rows, err := s.pool.Query(ctx, inventorySQL(t))
	if err != nil {
		t.Fatal(err)
	}
	type inv struct {
		Event, Root int64
		Class       string
		Record      *int64
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[inv])
	if err != nil {
		t.Fatal(err)
	}
	if after := dbSnapshot(t); after != before {
		t.Fatal("запрос инвентаризации изменил данные")
	}
	class := map[int64]string{}
	n2 := map[string]int{}
	for _, x := range got {
		class[x.Event] = x.Class
		n2[x.Class]++
	}
	want := map[int64]string{q: "A", n: "A", m: "B", u: "C"}
	if len(got) != len(want) {
		t.Fatalf("слитых событий в инвентаризации %d, want %d: %+v", len(got), len(want), got)
	}
	for id, c := range want {
		if class[id] != c {
			t.Errorf("событие %d: класс %q, want %q (%+v)", id, class[id], c, got)
		}
	}
	for _, x := range got {
		if x.Event == q && (x.Record == nil || *x.Record != rowQ) || x.Event == n && (x.Record == nil || *x.Record != rowN) {
			t.Errorf("запись класса A для %d = %v", x.Event, x.Record)
		}
	}
	t.Logf("INVENTORY A=%d B=%d C=%d", n2["A"], n2["B"], n2["C"])
}

// catalog — каталог для сравнения до слияния и после отмены: события (без
// updated_at), записи (принадлежность, match_state, поля), дистанции.
func catalog(t *testing.T) string {
	t.Helper()
	return scalar[string](t, `SELECT jsonb_build_object(
		'events', (SELECT jsonb_agg(to_jsonb(e) - 'updated_at' ORDER BY e.id) FROM events e),
		'source_events', (SELECT jsonb_agg(jsonb_build_object('id', id, 'event_id', event_id, 'match_state', match_state,
			'fields', fields, 'state', state) ORDER BY id) FROM source_events),
		'distances', (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.event_id, d.distance_m) FROM event_distances d))::text`)
}

func dropJournal(t *testing.T, loser int64) {
	t.Helper()
	exec(t, "DELETE FROM event_merge_records WHERE merge_id IN (SELECT id FROM event_merges WHERE loser_id = $1)", loser)
	exec(t, "DELETE FROM event_merge_chain WHERE merge_id IN (SELECT id FROM event_merges WHERE loser_id = $1)", loser)
	exec(t, "DELETE FROM event_merges WHERE loser_id = $1", loser)
}

func undo(t *testing.T, s *Store, loser int64, apply bool) (UndoResult, error) {
	t.Helper()
	return s.UndoMerge(context.Background(), loser, 99, testFold, apply)
}

func TestMergeJournal(t *testing.T) {
	s := testStore(t)
	k, _ := ingested(t, s, "marathonec", "k", "Забег Б")
	m, rowM := ingested(t, s, "probeg", "m", "Забег Б")
	n, rowN := ingested(t, s, "third", "n", "Забег Б")
	confirm(t, s, rowN, m)
	confirm(t, s, rowM, k)
	got := scalar[string](t, `SELECT string_agg(format('%s>%s:%s:%s', loser_id, winner_id, match_state, decided_by), ' ' ORDER BY id) FROM event_merges`)
	if want := fmt.Sprintf("%d>%d:manual:42 %d>%d:manual:42", n, m, m, k); got != want {
		t.Fatalf("журнал = %q, want %q", got, want)
	}
	recs := scalar[string](t, `SELECT string_agg(format('%s:%s', source_event_id, prev_match_state), ' ' ORDER BY source_event_id)
		FROM event_merge_records WHERE merge_id = 2`)
	if want := fmt.Sprintf("%d:auto %d:manual", rowM, rowN); recs != want {
		t.Fatalf("записи второго слияния = %q, want %q", recs, want)
	}
	if chain := scalar[string](t, "SELECT coalesce(string_agg(event_id::text, ' '), '') FROM event_merge_chain WHERE merge_id = 2"); chain != fmt.Sprint(n) {
		t.Fatalf("цепочка второго слияния = %q, want %d", chain, n)
	}
}

// TestUnmergeRoundTrip — D-14 b+c: отмена по журналу и без журнала (класс A)
// возвращает каталог к состоянию до слияния; цепочка — с последнего.
func TestUnmergeRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	p, _ := ingested(t, s, "marathonec", "p", "Марафон А")
	q, rowQ := ingested(t, s, "probeg", "q", "Марафон А", 7777) // своя дистанция: корень после слияния её получит
	s0 := catalog(t)
	confirm(t, s, rowQ, p)
	merged := catalog(t)
	if scalar[int64](t, "SELECT count(*) FROM event_distances WHERE event_id = $1 AND distance_m = 7777", p) != 1 {
		t.Fatal("precondition: корень не получил дистанцию проигравшего")
	}
	if r, err := undo(t, s, q, false); err != nil || r.Mode != "journal" || r.Root != p || len(r.Records) != 1 || r.Records[0] != rowQ {
		t.Fatalf("проверка без применения = %+v, %v", r, err)
	}
	if catalog(t) != merged {
		t.Fatal("проверка без применения изменила каталог")
	}
	if _, err := undo(t, s, q, true); err != nil {
		t.Fatal(err)
	}
	if got := catalog(t); got != s0 {
		t.Fatalf("после отмены по журналу:\n%s\nwant\n%s", got, s0)
	}
	if st := scalar[string](t, "SELECT state || ':' || decided_by FROM dedup_candidates WHERE source_event_id = $1 AND event_id = $2", rowQ, p); st != "rejected:99" {
		t.Fatalf("кандидат пары = %s, want rejected:99", st)
	}
	if _, err := undo(t, s, q, true); !errors.Is(err, ErrNotMergeable) {
		t.Fatalf("повторная отмена: %v, want ErrNotMergeable", err)
	}

	// Цепочка N → M → K: сначала последнее слияние.
	k, _ := ingested(t, s, "marathonec", "k", "Забег Б")
	m, rowM := ingested(t, s, "probeg", "m", "Забег Б")
	n, rowN := ingested(t, s, "third", "n", "Забег Б")
	c0 := catalog(t)
	confirm(t, s, rowN, m)
	c1 := catalog(t)
	confirm(t, s, rowM, k)
	c2 := catalog(t)
	if _, err := undo(t, s, n, true); !errors.Is(err, ErrNotUndoable) {
		t.Fatalf("отмена раннего слияния до позднего: %v, want ErrNotUndoable", err)
	}
	if catalog(t) != c2 {
		t.Fatal("отказ изменил каталог")
	}
	if _, err := undo(t, s, m, true); err != nil {
		t.Fatal(err)
	}
	if catalog(t) != c1 {
		t.Fatal("после отмены M → K каталог не равен состоянию после N → M")
	}
	if _, err := undo(t, s, n, true); err != nil {
		t.Fatal(err)
	}
	if catalog(t) != c0 {
		t.Fatal("после отмены N → M каталог не равен исходному")
	}

	// Без журнала (слияние до миграции 00005), класс A.
	p2, _ := ingested(t, s, "marathonec", "p2", "Кросс Г")
	q2, rowQ2 := ingested(t, s, "probeg", "q2", "Кросс Г")
	l0 := catalog(t)
	confirm(t, s, rowQ2, p2)
	dropJournal(t, q2)
	r, err := s.UndoMerge(ctx, q2, 99, testFold, true)
	if err != nil || r.Mode != "legacy" || r.Class != "A" {
		t.Fatalf("отмена без журнала = %+v, %v", r, err)
	}
	if catalog(t) != l0 {
		t.Fatal("после отмены класса A каталог не равен исходному")
	}
	t.Logf("UNMERGE journal=exact chain=lifo legacy_a=exact")
}

// TestUnmergeRefuses — без журнала классы B и C, а также не слитое и
// отсутствующее событие: отказ, данные не меняются.
func TestUnmergeRefuses(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	k, _ := ingested(t, s, "marathonec", "k", "Забег Б")
	m, rowM := ingested(t, s, "probeg", "m", "Забег Б")
	_, rowN := ingested(t, s, "third", "n", "Забег Б")
	confirm(t, s, rowN, m)
	confirm(t, s, rowM, k)
	tt, _ := ingested(t, s, "marathonec", "t", "Кросс В")
	u, _ := ingested(t, s, "probeg", "u", "Кросс В")
	if err := s.MergeEvents(ctx, u, tt, string(event.MatchAuto), nil, testFold); err != nil {
		t.Fatal(err)
	}
	exec(t, "DELETE FROM event_merge_records")
	exec(t, "DELETE FROM event_merge_chain")
	exec(t, "DELETE FROM event_merges")
	before := dbSnapshot(t)
	for _, c := range []struct {
		id    int64
		want  error
		class string
	}{{m, ErrNotUndoable, "B"}, {u, ErrNotUndoable, "C"}, {k, ErrNotMergeable, ""}, {999999, ErrNotFound, ""}} {
		for _, apply := range []bool{false, true} {
			r, err := s.UndoMerge(ctx, c.id, 99, testFold, apply)
			if !errors.Is(err, c.want) || (c.class != "" && !strings.Contains(err.Error(), "class "+c.class)) {
				t.Fatalf("событие %d (apply=%v): %+v, %v; want %v class %s", c.id, apply, r, err, c.want, c.class)
			}
		}
	}
	if dbSnapshot(t) != before {
		t.Fatal("отказ изменил данные")
	}
}

// TestUnmergeAtomic — сбой посередине отмены откатывает всё: записи,
// события, журнал и кандидаты остаются как до попытки.
func TestUnmergeAtomic(t *testing.T) {
	s := testStore(t)
	p, rowP := ingested(t, s, "marathonec", "p", "Марафон А")
	q, rowQ := ingested(t, s, "probeg", "q", "Марафон А")
	confirm(t, s, rowQ, p)
	// Пересборка полей корня упадёт: поле записи корня испорчено.
	exec(t, `UPDATE source_events SET fields = jsonb_set(fields, '{start_date}', '"bad"') WHERE id = $1`, rowP)
	before := dbSnapshot(t)
	if _, err := undo(t, s, q, true); err == nil {
		t.Fatal("отмена при испорченных полях прошла")
	}
	if dbSnapshot(t) != before {
		t.Fatal("частичная отмена: данные изменились")
	}
	if n := scalar[int64](t, "SELECT count(*) FROM event_merges WHERE undone_at IS NOT NULL"); n != 0 {
		t.Fatalf("журнал отмечен отменённым: %d", n)
	}
}

func TestMigration00005(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tables := func() int64 {
		return scalar[int64](t, `SELECT count(*) FROM information_schema.tables
			WHERE table_name IN ('event_merges', 'event_merge_records', 'event_merge_chain')`)
	}
	if got := tables(); got != 3 {
		t.Fatalf("таблиц журнала %d, want 3", got)
	}
	db := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := p.Up(context.Background()); err != nil {
			t.Errorf("restore Up: %v", err)
		}
	}()
	if _, err := p.DownTo(ctx, 4); err != nil {
		t.Fatalf("DownTo(4): %v", err)
	}
	if got := tables(); got != 0 {
		t.Fatalf("после Down таблиц журнала %d, want 0", got)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := tables(); got != 3 {
		t.Fatalf("после Up таблиц журнала %d, want 3", got)
	}
}
