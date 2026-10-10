package dedup_test

import (
	"database/sql"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"aggregator/internal/dedup"
	"aggregator/internal/ingest"
	"aggregator/internal/store/storetest"
)

// favRolloutSection — раздел памятки выката с запросами проверки Phase 14.
const favRolloutSection = "## Phase 14: избранное\n"

// favLineRE — допустимый вид строки ответа: только счётчики и коды, без
// идентификаторов пользователей и без состава избранного.
var favLineRE = regexp.MustCompile(`^(favorites=[0-9]+\|users=[0-9]+|outbox=[a-z_]+\|[a-z_]+\|[0-9]+|run_counts=[a-z0-9_]+\|[0-9]+\|[a-z_]+\|created:[0-9?]+\|changed:[0-9?]+\|unchanged:[0-9?]+)$`)

// TestFavRolloutSQL — запросы проверки из памятки выката Phase 14 выполняются
// дословно на локальной базе с каталогом из сохранённых страниц источников:
// сначала на пустых таблицах избранного и очереди, затем с двумя
// пользователями с избранным и строками очереди во всех состояниях.
//
// Упрощение: каталог — сохранённые страницы источников, а не копия
// production; строки очереди записаны напрямую, а не прогоном и ботом.
func TestFavRolloutSQL(t *testing.T) {
	s := storetest.Open(t)
	cfg := dedup.DefaultConfig()
	for _, c := range calibSources {
		body, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.code, err)
		}
		recs, err := c.parse(ingest.RawDoc{URL: c.url, Body: body, FetchedAt: c.now})
		if err != nil {
			t.Fatalf("%s: Parse: %v", c.code, err)
		}
		at := c.now
		r := &ingest.Runner{Store: s, Fold: cfg.Fold(), Now: func() time.Time { return at }}
		runAccept(t, r, &calibSource{fakeSource{code: c.code, recs: recs}, c.policy})
	}

	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	qs := rolloutSQL(t, favRolloutSection)
	if len(qs) != 3 {
		t.Fatalf("запросов проверки в памятке %d, want 3", len(qs))
	}
	check := func(step string, lines ...[]string) {
		t.Helper()
		for _, ls := range lines {
			for _, l := range ls {
				if !favLineRE.MatchString(l) {
					t.Errorf("%s: строка ответа %q не счётчик", step, l)
				}
			}
		}
	}

	// Пустые таблицы: первая строка — нули, строк очереди нет.
	favs, outbox, runs := queryLines(t, db, qs[0]), queryLines(t, db, qs[1]), queryLines(t, db, qs[2])
	if !slices.Equal(favs, []string{"favorites=0|users=0"}) || len(outbox) != 0 {
		t.Errorf("пустые таблицы: %q, %q; want favorites=0|users=0 и ни одной строки outbox=", favs, outbox)
	}
	check("пустые таблицы", favs, outbox, runs)

	// Два пользователя: у первого два старта, у второго один.
	ids := strings.Fields(dbText(t, `SELECT string_agg(id::text, ' ' ORDER BY id) FROM (SELECT id FROM events ORDER BY id LIMIT 3) e`))
	if len(ids) != 3 {
		t.Fatalf("событий в каталоге меньше трёх: %v", ids)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const user1, user2 = 910001, 910002
	for _, f := range []struct {
		tg int64
		ev string
	}{{user1, ids[0]}, {user1, ids[1]}, {user2, ids[0]}} {
		exec(`INSERT INTO favorites (tg_id, event_id, created_at) VALUES ($1, $2, now())`, f.tg, f.ev)
	}
	// Очередь: два ожидающих изменения, одно отправленное, одно взятое без
	// исхода, одно закрытое напоминание и одно закрытое сообщение о скрытии.
	const ins = `INSERT INTO outbox (tg_id, kind, event_id, dedup_key, created_at, claimed_at, sent_at, done_at, outcome)
		VALUES ($1, $2, $3, $4, now(), $5, $6, $7, $8)`
	var now, none any = time.Now(), nil
	row := func(tg int64, kind, ev, key string, claimed, sent, done, outcome any) {
		t.Helper()
		exec(ins, tg, kind, ev, key, claimed, sent, done, outcome)
	}
	row(user1, "change", ids[0], "c1", none, none, none, none)
	row(user2, "change", ids[0], "c2", none, none, none, none)
	row(user1, "change", ids[1], "c3", now, now, none, none)
	row(user1, "gone", ids[1], "g1", now, none, none, none)
	row(user2, "reminder", ids[0], "r1", now, none, now, "blocked")
	row(user2, "gone", ids[0], "g2", none, none, now, "stale")

	favs, outbox, runs = queryLines(t, db, qs[0]), queryLines(t, db, qs[1]), queryLines(t, db, qs[2])
	if !slices.Equal(favs, []string{"favorites=3|users=2"}) {
		t.Errorf("избранное: %q, want favorites=3|users=2", favs)
	}
	wantOutbox := []string{
		"outbox=change|pending|2",
		"outbox=change|sent|1",
		"outbox=gone|stale|1",
		"outbox=gone|unknown|1",
		"outbox=reminder|blocked|1",
	}
	if !slices.Equal(outbox, wantOutbox) {
		t.Errorf("очередь: %q, want %q", outbox, wantOutbox)
	}
	check("с данными", favs, outbox, runs)
	codes := map[string]bool{}
	for _, l := range runs {
		code, _, _ := strings.Cut(strings.TrimPrefix(l, "run_counts="), "|")
		codes[code] = true
	}
	if len(codes) != len(calibSources) {
		t.Errorf("источников в запросе прогонов %d, want %d: %q", len(codes), len(calibSources), runs)
	}
	for _, l := range append(append(favs, outbox...), runs...) {
		if strings.Contains(l, "910001") || strings.Contains(l, "910002") {
			t.Errorf("в ответе идентификатор пользователя: %q", l)
		}
	}
	if !t.Failed() {
		t.Logf("ROLLOUT SQL 14 OK\n%s\n%s\n%s", strings.Join(favs, "\n"), strings.Join(outbox, "\n"), strings.Join(runs, "\n"))
	}
}
