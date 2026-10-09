package dedup_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/dedup"
	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/store/storetest"
	"aggregator/migrations"
)

// TestSportTracer — сквозной путь вида спорта (D-04…D-06): запись источника
// без вида спорта даёт событие с sport = running, ключа sport нет ни в
// fields, ни в payload, повторный прогон строку события не меняет.
func TestSportTracer(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	r := &ingest.Runner{Store: s, Fold: dedup.DefaultConfig().Fold()}
	src := marathonec(race("90435", titleM, "20 км, 10 км, 2 км"))

	if res := runAccept(t, r, src); res.Created != 1 {
		t.Fatalf("Created = %d, want 1", res.Created)
	}
	if got := dbText(t, `SELECT data_type || ' ' || is_nullable || ' ' || column_default
		FROM information_schema.columns WHERE table_name = 'events' AND column_name = 'sport'`); got != "text NO 'running'::text" {
		t.Fatalf("колонка sport = %q", got)
	}
	if got := dbText(t, `SELECT string_agg(sport, ',') FROM events`); got != "running" {
		t.Fatalf("events.sport = %q, want running", got)
	}
	if e := getEvent(t, s, 1); e.Sport != event.SportRunning {
		t.Fatalf("GetEvent.Sport = %q, want running", e.Sport)
	}
	if got := dbText(t, `SELECT (count(*) FILTER (WHERE fields ? 'sport' OR payload ? 'sport'))::text FROM source_events`); got != "0" {
		t.Fatalf("записей с ключом sport в fields или payload: %s", got)
	}

	const row = `SELECT to_jsonb(e)::text FROM events e`
	before := dbText(t, row)
	res, err := r.Run(ctx, src)
	if err != nil || res.Status != ingest.StatusOK || res.Created != 0 || res.Changed != 0 || res.Unchanged != 1 {
		t.Fatalf("повторный прогон = %+v, %v; want ok, 0 created, 0 changed, 1 unchanged", res, err)
	}
	if after := dbText(t, row); after != before {
		t.Fatalf("строка события изменилась:\nдо    %s\nпосле %s", before, after)
	}
	t.Log("TRACER sport=running fields_key=absent rerun=unchanged")
}

// calibSource — источник из сохранённой страницы: записи фикстуры и
// политика настоящего источника.
type calibSource struct {
	fakeSource
	policy ingest.Policy
}

func (s *calibSource) Policy() ingest.Policy { return s.policy }

// sportSnapshot — каталог без колонки events.sport; запрос годится и до
// миграции 00008, и после неё (D-11).
func sportSnapshot(t *testing.T) string {
	t.Helper()
	return dbText(t, `SELECT jsonb_build_object(
		'events', (SELECT jsonb_agg(to_jsonb(e) - 'sport' ORDER BY e.id) FROM events e),
		'event_distances', (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.event_id, d.distance_m) FROM event_distances d),
		'source_events', (SELECT jsonb_agg(jsonb_build_object('id', id, 'event_id', event_id,
			'match_state', match_state, 'state', state, 'fields', fields, 'payload', payload,
			'content_hash', content_hash) ORDER BY id) FROM source_events),
		'event_merges', (SELECT jsonb_agg(to_jsonb(m) ORDER BY m.id) FROM event_merges m),
		'event_merge_records', (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.merge_id, r.source_event_id) FROM event_merge_records r),
		'event_merge_chain', (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.merge_id, c.event_id) FROM event_merge_chain c),
		'dedup_candidates', (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM dedup_candidates c))::text`)
}

const rolloutDoc, rolloutSection = "../../deploy/README.md", "## Phase 12: вид спорта\n"

// rolloutSQL — запросы проверки из памятки выката (блок ```sql раздела
// rolloutSection): тест выполняет ровно тот текст, что записан в документе.
// Каждая непустая строка блока — запрос; он должен только читать.
func rolloutSQL(t *testing.T) []string {
	t.Helper()
	doc, err := os.ReadFile(rolloutDoc)
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(doc), "\n"+rolloutSection)
	sec, _, _ = strings.Cut(sec, "\n## ")
	_, block, ok2 := strings.Cut(sec, "```sql\n")
	block, _, ok3 := strings.Cut(block, "```")
	if !ok || !ok2 || !ok3 {
		t.Fatalf("%s: нет блока sql в разделе %q", rolloutDoc, rolloutSection)
	}
	var out []string
	for _, q := range strings.Split(block, "\n") {
		if q = strings.TrimSpace(q); q == "" {
			continue
		}
		if !strings.HasPrefix(q, "SELECT ") {
			t.Fatalf("%s: запрос проверки не SELECT: %s", rolloutDoc, q)
		}
		out = append(out, q)
	}
	return out
}

// queryLines — строки ответа запроса из одной текстовой колонки.
func queryLines(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return out
}

// TestSportBeforeAfter — D-06, D-11: каталог без колонки sport один и тот же
// до миграции 00008, после неё и после первого прогона на новом коде.
//
// Упрощение: копия данных — каталог из сохранённых страниц источников, а не
// копия production; состояние «до» получено откатом схемы. На production то
// же сравнение делает владелец при выкате.
func TestSportBeforeAfter(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	cfg := dedup.DefaultConfig()

	type feed struct {
		r   *ingest.Runner
		src ingest.Source
	}
	var feeds []feed
	for _, c := range calibSources {
		body, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.code, err)
		}
		recs, err := c.parse(ingest.RawDoc{URL: c.url, Body: body, FetchedAt: c.now})
		if err != nil {
			t.Fatalf("%s: Parse: %v", c.code, err)
		}
		f := feed{
			r:   &ingest.Runner{Store: s, Fold: cfg.Fold(), Now: func() time.Time { return c.now }},
			src: &calibSource{fakeSource{code: c.code, recs: recs}, c.policy},
		}
		runAccept(t, f.r, f.src)
		feeds = append(feeds, f)
	}
	// rerun — прогон каждого источника и один проход дедупликации.
	rerun := func(step string) (created, changed int) {
		t.Helper()
		for _, f := range feeds {
			res, err := f.r.Run(ctx, f.src)
			if err != nil || res.Status != ingest.StatusOK {
				t.Fatalf("%s: Run %s = %s %v, %v; want ok", step, f.src.Code(), res.Status, res.Reasons, err)
			}
			created, changed = created+res.Created, changed+res.Changed
		}
		pass(t, s, cfg)
		return created, changed
	}

	// Решения по реальным парам калибровки: c01 слита вручную, c02 отклонена.
	const admin = 777
	candidate := func(a, b string) int64 {
		t.Helper()
		var rowA, evA, rowB, evB int64
		const q = `SELECT se.id || ' ' || se.event_id FROM source_events se JOIN sources s ON s.id = se.source_id
			WHERE s.code || '/' || se.external_id = '%s'`
		if _, err := fmt.Sscan(dbText(t, fmt.Sprintf(q, a))+" "+dbText(t, fmt.Sprintf(q, b)), &rowA, &evA, &rowB, &evB); err != nil {
			t.Fatalf("пара %s, %s: %v", a, b, err)
		}
		row, ev := rowB, evA
		if evA > evB {
			row, ev = rowA, evB
		}
		id, err := s.UpsertCandidate(ctx, row, ev, 1, []byte(`{}`))
		if err != nil || id == 0 {
			t.Fatalf("UpsertCandidate %s, %s = %d, %v", a, b, id, err)
		}
		return id
	}
	if err := s.ConfirmCandidate(ctx, candidate("marathonec/90435", "probeg/83327"), admin, cfg.Fold()); err != nil {
		t.Fatal(err)
	}
	if err := s.RejectCandidate(ctx, candidate("marathonec/81411", "probeg/76978"), admin); err != nil {
		t.Fatal(err)
	}
	rerun("установившееся состояние")

	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
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
	const hasColumn = `SELECT count(*)::text FROM information_schema.columns WHERE table_name = 'events' AND column_name = 'sport'`
	if _, err := p.DownTo(ctx, 7); err != nil {
		t.Fatalf("DownTo(7): %v", err)
	}
	if got := dbText(t, hasColumn); got != "0" {
		t.Fatalf("после отката колонок sport %s, want 0", got)
	}
	before := sportSnapshot(t)

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	migrated := sportSnapshot(t)
	if got := dbText(t, `SELECT string_agg(DISTINCT sport, ',') FROM events`); got != "running" {
		t.Fatalf("после миграции sport = %q, want running у всех событий", got)
	}

	created, changed := rerun("первый прогон после миграции")
	after := sportSnapshot(t)
	if created != 0 || changed != 0 {
		t.Errorf("первый прогон после миграции: created %d, changed %d; want 0, 0", created, changed)
	}
	if migrated != before {
		t.Errorf("миграция изменила каталог: снимки до и после неё различаются")
	}
	if after != before {
		t.Errorf("первый прогон изменил каталог: снимки до миграции и после прогона различаются")
	}

	var events, records, merges, candidates, rejected, withKey int
	if _, err := fmt.Sscan(dbText(t, `SELECT concat_ws(' ',
		(SELECT count(*) FROM events), (SELECT count(*) FROM source_events),
		(SELECT count(*) FROM event_merges), (SELECT count(*) FROM dedup_candidates),
		(SELECT count(*) FROM dedup_candidates WHERE state = 'rejected'),
		(SELECT count(*) FROM source_events WHERE fields ? 'sport' OR payload ? 'sport'))`),
		&events, &records, &merges, &candidates, &rejected, &withKey); err != nil {
		t.Fatal(err)
	}
	if events < 133 || merges < 1 || rejected < 1 || withKey != 0 {
		t.Fatalf("events %d (want ≥ 133), merges %d (≥ 1), rejected %d (≥ 1), записей с ключом sport %d (0)", events, merges, rejected, withKey)
	}

	// Запросы проверки из памятки выката — на этой же копии, после первого
	// прогона, следующего за миграцией.
	qs := rolloutSQL(t)
	if len(qs) != 2 {
		t.Fatalf("запросов проверки в памятке %d, want 2", len(qs))
	}
	sports, runs := queryLines(t, db, qs[0]), queryLines(t, db, qs[1])
	if want := fmt.Sprintf("sport=running|%d", events); len(sports) != 1 || sports[0] != want {
		t.Errorf("запрос видов спорта = %q, want одну строку %q", sports, want)
	}
	last := map[string]string{} // последний прогон источника: строки идут по коду и номеру
	for _, line := range runs {
		code, rest, _ := strings.Cut(strings.TrimPrefix(line, "run_counts="), "|")
		last[code] = rest
	}
	if len(last) != len(feeds) {
		t.Errorf("источников в запросе прогонов %d, want %d: %q", len(last), len(feeds), runs)
	}
	for code, rest := range last {
		if _, counts, _ := strings.Cut(rest, "|"); !strings.HasPrefix(counts, "ok|created:0|changed:0|unchanged:") {
			t.Errorf("последний прогон %s = %q, want ok, created:0, changed:0", code, rest)
		}
	}
	if !t.Failed() {
		t.Logf("BEFORE-AFTER OK events=%d records=%d merges=%d candidates=%d", events, records, merges, candidates)
		t.Logf("ROLLOUT SQL OK\n%s\n%s", strings.Join(sports, "\n"), strings.Join(runs, "\n"))
	}
}
