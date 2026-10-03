package dedup_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/dedup"
	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

// captureLog перенаправляет slog по умолчанию в буфер до конца теста.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

var passField = regexp.MustCompile(`(\w+)=(\d+)`)

// lastPass — числовые поля последней строки «dedup pass» и сброс буфера.
func lastPass(t *testing.T, buf *bytes.Buffer) map[string]int {
	t.Helper()
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, `msg="dedup pass"`) {
			line = l
		}
	}
	buf.Reset()
	if line == "" {
		t.Fatal("нет строки dedup pass в логе")
	}
	out := map[string]int{}
	for _, m := range passField.FindAllStringSubmatch(line, -1) {
		n, _ := strconv.Atoi(m[2])
		out[m[1]] = n
	}
	// Инвариант счётчиков (07-01): сумма исходов равна would_auto.
	if sum := out["would_auto_shown"] + out["would_auto_hidden"] + out["would_auto_decided"] + out["would_auto_merged"] + out["would_auto_deferred"]; sum != out["would_auto"] {
		t.Fatalf("would_auto = %d, сумма исходов = %d: %s", out["would_auto"], sum, line)
	}
	return out
}

func wantPass(t *testing.T, got map[string]int, want map[string]int) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("dedup pass %s = %d, want %d (%v)", k, got[k], v, got)
		}
	}
}

// dbText — результат запроса одной строкой (снимки таблиц).
func dbText(t *testing.T, q string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var out *string
	if err := conn.QueryRow(ctx, q).Scan(&out); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if out == nil {
		return ""
	}
	return *out
}

// catalogSnapshot — данные каталога: события (все колонки), принадлежность и
// поля записей источников, дистанции.
func catalogSnapshot(t *testing.T) string {
	t.Helper()
	return dbText(t, `SELECT jsonb_build_object(
		'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM events e),
		'source_events', (SELECT jsonb_agg(jsonb_build_object('id', id, 'event_id', event_id,
			'match_state', match_state, 'fields', fields, 'state', state) ORDER BY id) FROM source_events),
		'event_distances', (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.event_id, d.distance_m) FROM event_distances d))::text`)
}

func candidatesSnapshot(t *testing.T) string {
	t.Helper()
	return dbText(t, `SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id)::text FROM dedup_candidates c`)
}

// noMerges — фактических слияний нет: ни одно событие не слито и у каждого
// события записи только одного источника. match_state = 'auto' здесь не
// признак: так ingest помечает привязку записи к своему событию.
func noMerges(t *testing.T) {
	t.Helper()
	if n := dbText(t, `SELECT count(*)::text FROM events WHERE merged_into_id IS NOT NULL`); n != "0" {
		t.Fatalf("слитых событий %s, want 0", n)
	}
	if n := dbText(t, `SELECT count(*)::text FROM (SELECT event_id FROM source_events GROUP BY event_id HAVING count(DISTINCT source_id) > 1) x`); n != "0" {
		t.Fatalf("событий с записями нескольких источников %s, want 0", n)
	}
}

func TestPassWouldAutoOff(t *testing.T) {
	s := storetest.Open(t)
	buf := captureLog(t)
	addEvent(t, s, "marathonec", "a", titleM, 10, `{}`)
	addEvent(t, s, "probeg", "b", titleP, 10, `{}`)
	pass(t, s, dedup.DefaultConfig())
	got := lastPass(t, buf)
	wantPass(t, got, map[string]int{"merged": 0, "candidates": 1, "would_auto": 1, "would_auto_shown": 1, "would_auto_hidden": 0, "would_auto_decided": 0})
	if _, ok := got["would_auto_merged"]; ok {
		t.Fatalf("would_auto_merged при выключенном флаге: %v", got)
	}
	c := allCandidates(t, s)
	if len(c) != 1 || !strings.Contains(string(c[0].Reasons), `"would_auto": true`) {
		t.Fatalf("кандидаты = %+v, want один с would_auto", c)
	}
	noMerges(t)
}

// TestPassOffCatalogUnchanged — D-10, слой 3: при DEDUP_AUTO_MERGE=false
// два прохода не меняют каталог; второй не меняет и кандидатов.
func TestPassOffCatalogUnchanged(t *testing.T) {
	s := storetest.Open(t)
	buf := captureLog(t)
	addEvent(t, s, "marathonec", "a", titleM, 10, `{}`)
	addEvent(t, s, "probeg", "a", titleP, 10, `{}`)
	for i, title := range []string{"Ночной забег по набережной", "Осенний кросс у озера"} {
		addEvent(t, s, "marathonec", "w"+strconv.Itoa(i), title, 20+10*i, `{}`)
		addEvent(t, s, "probeg", "w"+strconv.Itoa(i), title, 20+10*i, `{}`)
	}
	// Кандидат без признака — соседние дни; отдельная пара — разные названия.
	addEvent(t, s, "marathonec", "c", "Весенний полумарафон", 40, `{}`)
	addEvent(t, s, "probeg", "c", "Весенний полумарафон", 41, `{}`)
	addEvent(t, s, "marathonec", "x", "Белые ночи", 50, `{}`)
	addEvent(t, s, "probeg", "x", "Алые паруса", 50, `{}`)

	before := catalogSnapshot(t)
	pass(t, s, dedup.DefaultConfig())
	wantPass(t, lastPass(t, buf), map[string]int{"merged": 0, "candidates": 4, "would_auto": 3, "would_auto_shown": 3})
	cands := candidatesSnapshot(t)
	pass(t, s, dedup.DefaultConfig())
	wantPass(t, lastPass(t, buf), map[string]int{"merged": 0, "candidates": 4, "would_auto": 3, "would_auto_shown": 3})
	if after := catalogSnapshot(t); after != before {
		t.Fatalf("каталог изменился:\nдо    %s\nпосле %s", before, after)
	}
	if again := candidatesSnapshot(t); again != cands {
		t.Fatalf("второй проход изменил кандидатов:\n%s\n%s", cands, again)
	}
	noMerges(t)
	n := 0
	for _, c := range allCandidates(t, s) {
		if strings.Contains(string(c.Reasons), `"would_auto": true`) {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("кандидатов с would_auto = %d, want 3", n)
	}

	// Тот же каталог при включённом флаге: признак ведёт к слиянию тех же 3 пар.
	pass(t, s, autoConfig())
	wantPass(t, lastPass(t, buf), map[string]int{"merged": 3, "would_auto": 3, "would_auto_merged": 3, "would_auto_deferred": 0})
}

// TestPassWouldAutoHidden — D-09: лимит MaxPerEvent сохраняется, скрытая им
// потенциальная AUTO-пара считается в would_auto_hidden.
func TestPassWouldAutoHidden(t *testing.T) {
	s := storetest.Open(t)
	buf := captureLog(t)
	c := dedup.DefaultConfig()
	c.MaxPerEvent, c.LinkBonus = 1, 0.5
	high := dedup.NameSim(titleM, titleP)
	var y string
	for _, title := range []string{"Трейл «Уральский вулкан»", "Уральский вулкан: гора Липовая", "Трейл «Гора Липовая»", "Уральский трейл"} {
		sxy, spy := dedup.NameSim(titleM, title), dedup.NameSim(titleP, title)
		if sxy >= c.Low && sxy < c.High && spy < c.High && sxy+c.LinkBonus > high {
			y = title
			break
		}
	}
	if y == "" {
		t.Fatal("precondition: нет названия со схожестью в [Low, High) и оценкой выше пары would_auto")
	}
	link := func(e *event.Event) { e.RegistrationURL = "https://myrace.info/events/2080" }
	x, _ := addEventMod(t, s, "marathonec", "x", titleM, 10, `{}`, link)
	addEvent(t, s, "probeg", "p", titleP, 10, `{}`)
	yID, _ := addEventMod(t, s, "probeg", "y", y, 10, `{}`, link)

	pass(t, s, c)
	wantPass(t, lastPass(t, buf), map[string]int{"candidates": 1, "would_auto": 1, "would_auto_shown": 0, "would_auto_hidden": 1, "would_auto_decided": 0})
	got := allCandidates(t, s)
	if len(got) != 1 || min(got[0].EventA, got[0].EventB) != min(x, yID) || max(got[0].EventA, got[0].EventB) != max(x, yID) {
		t.Fatalf("кандидаты = %+v, want только пара X–Y", got)
	}
	noMerges(t)
}
