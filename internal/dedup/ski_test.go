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
	tssrc "aggregator/internal/ingest/sources/tristyle"
	"aggregator/internal/store/storetest"
	"aggregator/migrations"
)

// TestPassSportSeparate — D-13: беговое и лыжное события одного дня, города
// и названия из разных источников парой не становятся; решённые кандидаты
// проход не меняет.
func TestPassSportSeparate(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	cfg := dedup.DefaultConfig()
	ski := func(e *event.Event) { e.Sport, e.Type = event.SportSkiing, event.TypeSki }
	pending := func(step string, want int) {
		t.Helper()
		pass(t, s, cfg)
		if got, _ := counts(t, s); got != want {
			t.Fatalf("%s: кандидатов в очереди %d, want %d", step, got, want)
		}
	}

	addEvent(t, s, "marathonec", "1", "Гонка Белые ночи", 10, `{}`)
	b, _ := addEventMod(t, s, "probeg", "2", "Гонка Белые ночи", 10, `{}`, ski)
	pending("бег и лыжи", 0)
	storetest.SetSport(t, b, event.SportRunning)
	pending("один вид спорта", 1)
	storetest.SetSport(t, b, event.SportSkiing)
	pending("снова бег и лыжи", 0)

	// Решённые кандидаты: отклонённый и подтверждённый — пока стороны одного
	// вида спорта; затем одна сторона отклонённой пары становится лыжной.
	addEvent(t, s, "marathonec", "3", "Окская тропа", 20, `{}`)
	d, _ := addEvent(t, s, "probeg", "4", "Окская тропа", 20, `{}`)
	addEvent(t, s, "marathonec", "5", "Алые паруса", 30, `{}`)
	addEvent(t, s, "probeg", "6", "Алые паруса", 30, `{}`)
	pending("две беговые пары", 2)
	const admin = 777
	for _, c := range allCandidates(t, s) {
		var err error
		if c.EventA == d || c.EventB == d {
			err = s.RejectCandidate(ctx, c.ID, admin)
		} else {
			err = s.ConfirmCandidate(ctx, c.ID, admin, cfg.Fold())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	storetest.SetSport(t, d, event.SportSkiing)
	before, merges := candidatesSnapshot(t), dbText(t, `SELECT jsonb_agg(to_jsonb(m) ORDER BY m.id)::text FROM event_merges m`)
	pending("после решений", 0)
	if after := candidatesSnapshot(t); after != before {
		t.Errorf("проход изменил решённых кандидатов:\nдо    %s\nпосле %s", before, after)
	}
	if after := dbText(t, `SELECT jsonb_agg(to_jsonb(m) ORDER BY m.id)::text FROM event_merges m`); after != merges {
		t.Errorf("проход изменил журнал слияний:\nдо    %s\nпосле %s", merges, after)
	}
}

// skiSnapshot — беговой каталог: беговые события целиком, их дистанции и
// записи источников; журнал слияний и кандидаты — целиком.
func skiSnapshot(t *testing.T) string {
	t.Helper()
	return dbText(t, `SELECT jsonb_build_object(
		'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM events e WHERE e.sport = 'running'),
		'event_distances', (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.event_id, d.distance_m) FROM event_distances d
			WHERE d.event_id IN (SELECT id FROM events WHERE sport = 'running')),
		'source_events', (SELECT jsonb_agg(jsonb_build_object('id', id, 'event_id', event_id,
			'match_state', match_state, 'state', state, 'fields', fields, 'payload', payload,
			'content_hash', content_hash) ORDER BY id) FROM source_events se
			WHERE NOT EXISTS (SELECT 1 FROM events e WHERE e.id = se.event_id AND e.sport <> 'running')),
		'event_merges', (SELECT jsonb_agg(to_jsonb(m) ORDER BY m.id) FROM event_merges m),
		'event_merge_records', (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.merge_id, r.source_event_id) FROM event_merge_records r),
		'event_merge_chain', (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.merge_id, c.event_id) FROM event_merge_chain c),
		'dedup_candidates', (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM dedup_candidates c))::text`)
}

// tristyleRecs — записи сохранённых страниц Tristyle; mark — пометка вида
// спорта в адресе документа («» — беговой документ).
func tristyleRecs(t *testing.T, mark string, ids ...string) []ingest.SourceEvent {
	t.Helper()
	var out []ingest.SourceEvent
	for _, id := range ids {
		body, err := os.ReadFile("../ingest/sources/tristyle/testdata/event-" + id + ".html")
		if err != nil {
			t.Fatal(err)
		}
		recs, err := tssrc.Parse(ingest.RawDoc{URL: "https://tristyle.club/competition/" + id + "/" + mark, Body: body})
		if err != nil || len(recs) != 1 {
			t.Fatalf("tristyle %s: Parse = %+v, %v", id, recs, err)
		}
		out = append(out, recs[0])
	}
	return out
}

// TestSkiBeforeAfter — D-12: беговой каталог один и тот же до миграции 00009,
// после неё, после первого прогона на новом коде и после прогона, в котором
// у Tristyle появились лыжные записи.
//
// Упрощение: копия данных — каталог из сохранённых страниц источников, а не
// копия production; состояние «до» получено откатом схемы. На production то
// же сравнение делает владелец при выкате.
func TestSkiBeforeAfter(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	cfg := dedup.DefaultConfig()
	// skiNow — день загрузки лыжных страниц; прогоны Tristyle идут с этой
	// датой на всех шагах.
	skiNow := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	type feed struct {
		r   *ingest.Runner
		src *calibSource
	}
	var feeds []feed
	add := func(code string, now time.Time, policy ingest.Policy, recs []ingest.SourceEvent) {
		f := feed{
			r:   &ingest.Runner{Store: s, Fold: cfg.Fold(), Now: func() time.Time { return now }},
			src: &calibSource{fakeSource{code: code, recs: recs}, policy},
		}
		runAccept(t, f.r, f.src)
		feeds = append(feeds, f)
	}
	for _, c := range calibSources {
		body, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.code, err)
		}
		recs, err := c.parse(ingest.RawDoc{URL: c.url, Body: body, FetchedAt: c.now})
		if err != nil {
			t.Fatalf("%s: Parse: %v", c.code, err)
		}
		add(c.code, c.now, c.policy, recs)
	}
	add(tssrc.Code, skiNow, tssrc.New(nil).Policy(), tristyleRecs(t, "", "1090", "1096", "1170"))
	tristyle := feeds[len(feeds)-1]

	// rerun — прогон каждого источника и один проход дедупликации.
	rerun := func(step string) (created, changed int) {
		t.Helper()
		for _, f := range feeds {
			res, err := f.r.Run(ctx, f.src)
			if err == nil && res.Status == ingest.StatusSuspect {
				// Число записей источника выросло — прогон требует принятия.
				res, err = f.r.Accept(ctx, f.src, res.RunID)
			}
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
	const constraint = `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'events'::regclass AND conname = 'events_event_type_check'`
	// counts — беговые и небеговые события, записи с ключом sport всего и у
	// беговых событий, слияния, кандидаты.
	var running, other, withKey, runningKey, merges, candidates int
	counts := func() {
		t.Helper()
		if _, err := fmt.Sscan(dbText(t, `SELECT concat_ws(' ',
			(SELECT count(*) FROM events WHERE sport = 'running'), (SELECT count(*) FROM events WHERE sport <> 'running'),
			(SELECT count(*) FROM source_events WHERE fields ? 'sport' OR payload ? 'sport'),
			(SELECT count(*) FROM source_events se JOIN events e ON e.id = se.event_id
				WHERE e.sport = 'running' AND (se.fields ? 'sport' OR se.payload ? 'sport')),
			(SELECT count(*) FROM event_merges), (SELECT count(*) FROM dedup_candidates))`),
			&running, &other, &withKey, &runningKey, &merges, &candidates); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := p.DownTo(ctx, 8); err != nil {
		t.Fatalf("DownTo(8): %v", err)
	}
	if got := dbText(t, constraint); !strings.HasSuffix(got, "NOT VALID") {
		t.Fatalf("после отката events_event_type_check = %s, want прежний список", got)
	}
	before := skiSnapshot(t)
	counts()
	if running < 133 || other != 0 || withKey != 0 || merges < 1 || candidates < 1 {
		t.Fatalf("до миграции: беговых событий %d (want ≥ 133), прочих %d (0), записей с ключом sport %d (0), слияний %d (≥ 1), кандидатов %d (≥ 1)",
			running, other, withKey, merges, candidates)
	}
	wantRunning := running

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if migrated := skiSnapshot(t); migrated != before {
		t.Errorf("миграция изменила беговой каталог: снимки до и после неё различаются")
	}

	if created, changed := rerun("первый прогон после миграции"); created != 0 || changed != 0 {
		t.Errorf("первый прогон после миграции: created %d, changed %d; want 0, 0", created, changed)
	}
	if after := skiSnapshot(t); after != before {
		t.Errorf("первый прогон изменил беговой каталог: снимки до миграции и после прогона различаются")
	}

	// У Tristyle появились лыжные записи.
	skiRecs := tristyleRecs(t, "#skiing", "1178", "817", "818", "819", "820")
	tristyle.src.recs = append(tristyle.src.recs, skiRecs...)
	created, changed := rerun("прогон с лыжными записями")
	counts()
	if created != len(skiRecs) || changed != 0 || other != len(skiRecs) {
		t.Errorf("прогон с лыжными записями: created %d, changed %d, лыжных событий %d; want %d, 0, %d", created, changed, other, len(skiRecs), len(skiRecs))
	}
	if got := dbText(t, `SELECT string_agg(DISTINCT sport || ':' || (merged_into_id IS NULL), ',') FROM events WHERE sport <> 'running'`); got != "skiing:true" {
		t.Errorf("небеговые события = %q, want только неслитые skiing", got)
	}
	if running != wantRunning || runningKey != 0 || withKey != len(skiRecs) {
		t.Errorf("беговых событий %d (want %d), беговых записей с ключом sport %d (want 0), записей с ключом %d (want %d)",
			running, wantRunning, runningKey, withKey, len(skiRecs))
	}
	// Слияния и кандидаты входят в снимок целиком: совпадение со снимком «до»,
	// где лыжных событий не было, значит, что лыжные события не вошли ни в
	// одного кандидата и ни в одно слияние.
	if after := skiSnapshot(t); after != before {
		t.Errorf("лыжные записи изменили беговой каталог: снимки до миграции и после прогона различаются")
	}
	if !t.Failed() {
		t.Logf("SKI BEFORE-AFTER OK running=%d ski=%d merges=%d candidates=%d", running, other, merges, candidates)
	}
}
