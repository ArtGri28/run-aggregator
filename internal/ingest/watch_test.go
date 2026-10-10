package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// watchFuse — порог предохранителя шага сообщений (14-UX-CONTRACT.md, Q-7):
// больше стольких стартов с изменением или скрытием за прогон — строк нет.
const watchFuse = 20

// dbScalar читает одно значение прямым запросом к тестовой базе: очередь
// читает только бот, методов чтения для тестов прогона у хранилища нет.
func dbScalar[T any](t *testing.T, q string, args ...any) T {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var v T
	if err := conn.QueryRow(ctx, q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func dbExec(t *testing.T, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// watchRunner — testRunner с пустыми избранным и очередью: остальные тесты
// пакета эти таблицы не трогают.
func watchRunner(t *testing.T) *ingest.Runner {
	t.Helper()
	r := testRunner(t)
	dbExec(t, "DELETE FROM outbox")
	dbExec(t, "DELETE FROM favorites")
	return r
}

// seeded — источник из записей recs, принятый как база: записи в каталоге.
func seeded(t *testing.T, r *ingest.Runner, tag string, recs []ingest.SourceEvent) *memSource {
	t.Helper()
	src := newMemSource(t, recs)
	src.code += "-" + tag
	run(t, r, src, ingest.StatusSuspect)
	accept(t, r, src)
	return src
}

// eventOf — событие записи ext источника src.
func eventOf(t *testing.T, src *memSource, ext string) int64 {
	t.Helper()
	return dbScalar[int64](t, `SELECT se.event_id FROM source_events se JOIN sources s ON s.id = se.source_id
		WHERE s.code = $1 AND se.external_id = $2`, src.code, ext)
}

func fav(t *testing.T, tg, eventID int64) {
	t.Helper()
	out, err := testStore.AddFavorite(context.Background(), tg, eventID, fixtureNow, fixtureNow, 100)
	if err != nil || out != store.FavAdded {
		t.Fatalf("AddFavorite(%d, %d) = %v, %v", tg, eventID, out, err)
	}
}

type queueRow struct {
	TG      int64           `json:"tg"`
	Kind    string          `json:"kind"`
	Event   int64           `json:"event"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

func queueRows(t *testing.T, tgs ...int64) []queueRow {
	t.Helper()
	raw := dbScalar[string](t, `SELECT COALESCE(json_agg(json_build_object('tg', tg_id, 'kind', kind, 'event', event_id,
		'key', dedup_key, 'payload', payload) ORDER BY id), '[]')::text FROM outbox WHERE tg_id = ANY($1)`, tgs)
	var rows []queueRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// queue — строки очереди пользователей tgs как «пользователь вид событие
// ключ», отсортированные.
func queue(t *testing.T, tgs ...int64) []string {
	t.Helper()
	out := []string{}
	for _, r := range queueRows(t, tgs...) {
		out = append(out, fmt.Sprintf("%d %s %d %s", r.TG, r.Kind, r.Event, r.Key))
	}
	slices.Sort(out)
	return out
}

// row — ожидаемая строка queue: ключ «вид:прогон:событие».
func row(tg int64, kind string, runID, eventID int64) string {
	return fmt.Sprintf("%d %s %d %s:%d:%d", tg, kind, eventID, kind, runID, eventID)
}

func wantQueue(t *testing.T, step string, tgs []int64, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := queue(t, tgs...); !slices.Equal(got, want) {
		t.Fatalf("%s: очередь %q, want %q", step, got, want)
	}
}

// changeOf — изменение из единственной строки очереди пользователя tg.
func changeOf(t *testing.T, tg int64) event.Change {
	t.Helper()
	rows := queueRows(t, tg)
	if len(rows) != 1 {
		t.Fatalf("строк очереди пользователя %d — %d, want 1", tg, len(rows))
	}
	c, err := event.ParseChange(rows[0].Kind, rows[0].Event, rows[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func queueSize(t *testing.T) int64 { return dbScalar[int64](t, "SELECT count(*) FROM outbox") }

func eventRow(t *testing.T, id int64) event.Event {
	t.Helper()
	e, err := testStore.GetEvent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// shift сдвигает дату записи на days дней.
func shift(rec *ingest.SourceEvent, days int) {
	d, err := time.Parse("02/01/2006", rec.Date)
	if err != nil {
		panic(err)
	}
	rec.Date = d.AddDate(0, 0, days).Format("02/01/2006")
}

// counts — счётчики записи и наблюдения прогона.
func counts(res ingest.Result) [6]int {
	return [6]int{res.Upserted, res.Created, res.Changed, res.Unchanged, res.Missing, res.Gone}
}

// watchHeld — значение ключа watch_held в метриках последнего прогона; -1 —
// ключа нет.
func watchHeld(t *testing.T, src *memSource) int {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(lastRun(t, src.code).Metrics, &m); err != nil {
		t.Fatal(err)
	}
	raw, ok := m["watch_held"]
	if !ok {
		return -1
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// watchLogRE — строка лога шага сообщений: только номер прогона и счётчики.
var watchLogRE = regexp.MustCompile(`msg=("watch held" run_id=\d+ changed=\d+ gone=\d+ queued=0 threshold=\d+|watch run_id=\d+ changed=\d+ gone=\d+ queued=\d+)$`)

// checkWatchLog — в логе есть строка шага сообщений вида want, и каждая
// строка шага состоит только из номера прогона и счётчиков.
func checkWatchLog(t *testing.T, log, want string) {
	t.Helper()
	found := false
	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, "msg=watch") && !strings.Contains(line, `msg="watch`) {
			continue
		}
		if !watchLogRE.MatchString(line) {
			t.Errorf("строка лога шага сообщений не из одних счётчиков: %s", line)
		}
		found = found || strings.Contains(line, want)
	}
	if !found {
		t.Errorf("в логе нет строки %q:\n%s", want, log)
	}
}

// TestWatchTracer — сквозной путь: прогон источника → у избранного старта
// появилась ссылка → одна строка очереди подписчику; повторный прогон строк
// не добавляет; каталог пишется так же, как без избранного.
func TestWatchTracer(t *testing.T) {
	r := watchRunner(t)
	with, without := seeded(t, r, "a", records(1, 10)), seeded(t, r, "b", records(1, 10))
	id := eventOf(t, with, "1")
	fav(t, 42, id)

	for _, src := range []*memSource{with, without} {
		src.recs = records(1, 10)
		src.recs[0].RegistrationURL = "https://example.test/reg"
	}
	got, plain := run(t, r, with, ingest.StatusOK), run(t, r, without, ingest.StatusOK)
	if counts(got) != counts(plain) || got.Changed != 1 || got.Unchanged != 9 {
		t.Fatalf("прогон с избранным %v, без избранного %v; want одинаковые, Changed 1, Unchanged 9", counts(got), counts(plain))
	}
	if got.WatchChanged != 1 || got.WatchQueued != 1 || plain.WatchChanged != 1 || plain.WatchQueued != 0 || got.WatchHeld != 0 {
		t.Fatalf("шаг сообщений: с избранным %+v, без избранного %+v", got, plain)
	}
	wantQueue(t, "появилась ссылка", []int64{42}, row(42, "change", got.RunID, id))
	if c := changeOf(t, 42); !reflect.DeepEqual(c, event.Change{EventID: id, Link: true}) {
		t.Fatalf("изменение = %+v, want только появление ссылки", c)
	}
	if n := watchHeld(t, with); n != -1 {
		t.Fatalf("в метриках прогона без предохранителя есть watch_held = %d", n)
	}
	outbox := queueSize(t)

	again := run(t, r, with, ingest.StatusOK)
	rerun := queueSize(t) - outbox
	if rerun != 0 || again.WatchChanged != 0 || again.WatchQueued != 0 || again.Unchanged != 10 {
		t.Fatalf("повторный прогон: новых строк %d, %+v", rerun, again)
	}
	t.Logf("TRACER watch=link outbox=%d rerun=%d", outbox, rerun)
}

// TestWatchRun — шаг сообщений в прогоне: какие прогоны создают строки
// очереди, кому, сколько, и что шаг не мешает каталогу.
func TestWatchRun(t *testing.T) {
	ctx := context.Background()
	const url1, url2 = "https://example.test/reg", "https://example.test/other"
	// rejected — записи без названия: больше 30 % отбракованных → suspect.
	rejected := func() []ingest.SourceEvent {
		bad := records(900, 10)
		for i := range bad {
			bad[i].Title = ""
		}
		return bad
	}
	// goneAt — runner, у которого запись становится gone после n пропусков.
	goneAt := func(t *testing.T, n int) *ingest.Runner {
		r := watchRunner(t)
		r.GoneAfterRuns, r.GoneAfter = n, time.Nanosecond
		return r
	}

	// (1) suspect строк не создаёт; принятие создаёт как здоровый прогон.
	t.Run("suspect-accept", func(t *testing.T) {
		r := watchRunner(t)
		src := seeded(t, r, "a", records(1, 10))
		id := eventOf(t, src, "1")
		fav(t, 11, id)

		src.recs = append(records(1, 10), rejected()...)
		shift(&src.recs[0], 3)
		sus := run(t, r, src, ingest.StatusSuspect)
		if sus.WatchChanged != 0 || queueSize(t) != 0 {
			t.Fatalf("suspect: %+v, строк очереди %d; want 0", sus, queueSize(t))
		}
		acc := accept(t, r, src)
		if acc.RunID != sus.RunID || acc.WatchChanged != 1 || acc.WatchQueued != 1 {
			t.Fatalf("принятие: %+v", acc)
		}
		wantQueue(t, "принятие", []int64{11}, row(11, "change", acc.RunID, id))
		if c := changeOf(t, 11); c.Date == nil || c.Link || c.Status != nil {
			t.Fatalf("изменение = %+v, want только дата", c)
		}
	})

	// (2) сбой записи: транзакция откатилась, очередь пуста, прогон failed.
	t.Run("failed", func(t *testing.T) {
		r := watchRunner(t)
		src := seeded(t, r, "a", records(1, 10))
		fav(t, 21, eventOf(t, src, "1"))

		bad := records(11, 1)
		bad[0].Title = "Забег\x00"
		src.recs = append(records(1, 10), bad...)
		shift(&src.recs[0], 3)
		res, err := r.Run(ctx, src)
		if err == nil || res.Status != ingest.StatusFailed || res.WatchChanged != 0 || res.WatchQueued != 0 {
			t.Fatalf("Run = %s, %+v, err %v; want failed", statusOf(res), res, err)
		}
		if queueSize(t) != 0 {
			t.Fatalf("failed: строк очереди %d", queueSize(t))
		}
	})

	// (3) дата и ссылка в одном прогоне — одна строка с обеими частями;
	// (4) замена и исчезновение ссылки строк не дают.
	t.Run("fields", func(t *testing.T) {
		r := watchRunner(t)
		src := seeded(t, r, "a", records(1, 10))
		id := eventOf(t, src, "1")
		fav(t, 31, id)
		was := eventRow(t, id).StartDate

		src.recs = records(1, 10)
		src.recs[0].RegistrationURL = url1
		shift(&src.recs[0], 5)
		res := run(t, r, src, ingest.StatusOK)
		wantQueue(t, "дата и ссылка", []int64{31}, row(31, "change", res.RunID, id))
		want := event.Change{EventID: id, Link: true, Date: &event.DateChange{From: was, To: was.AddDate(0, 0, 5)}}
		if c := changeOf(t, 31); !reflect.DeepEqual(c, want) || res.WatchChanged != 1 {
			t.Fatalf("изменение = %+v (дата %+v), want %+v (дата %+v)", c, c.Date, want, want.Date)
		}

		src.recs[0].RegistrationURL = url2
		swap := run(t, r, src, ingest.StatusOK)
		src.recs[0].RegistrationURL = ""
		drop := run(t, r, src, ingest.StatusOK)
		if swap.Changed != 1 || drop.Changed != 1 || swap.WatchChanged != 0 || drop.WatchChanged != 0 || queueSize(t) != 1 {
			t.Fatalf("замена ссылки %+v, исчезновение %+v, строк %d; want каталог изменён, строк прежняя одна", swap, drop, queueSize(t))
		}
	})

	// (5) без избранного в базе очередь пуста, изменение каталога считается.
	t.Run("no-favorites", func(t *testing.T) {
		r := watchRunner(t)
		src := seeded(t, r, "a", records(1, 10))
		src.recs = records(1, 10)
		shift(&src.recs[0], 5)
		res := run(t, r, src, ingest.StatusOK)
		if res.Changed != 1 || res.Unchanged != 9 || res.WatchChanged != 1 || res.WatchQueued != 0 || queueSize(t) != 0 {
			t.Fatalf("без избранного: %+v, строк %d", res, queueSize(t))
		}
	})

	// (6) два подписчика и подписчик через слитое событие — по строке о корне;
	// поля события с записями двух источников берутся свёрткой.
	t.Run("merged-two-sources", func(t *testing.T) {
		r := watchRunner(t)
		a, b := seeded(t, r, "a", records(1, 10)), seeded(t, r, "b", records(201, 10))
		root, loser := eventOf(t, a, "1"), eventOf(t, b, "201")
		fav(t, 61, root)
		fav(t, 62, root)
		fav(t, 63, loser)
		fav(t, 64, loser)
		fav(t, 64, root)
		if err := testStore.MergeEvents(ctx, loser, root, string(event.MatchManual), nil, r.Fold); err != nil {
			t.Fatal(err)
		}
		all := []int64{61, 62, 63, 64}

		// Дата берётся из записи первого источника: смена даты во втором
		// событие не меняет.
		b.recs = records(201, 10)
		shift(&b.recs[0], 7)
		if res := run(t, r, b, ingest.StatusOK); res.WatchChanged != 0 || queueSize(t) != 0 {
			t.Fatalf("дата второй записи: %+v, строк %d; want 0", res, queueSize(t))
		}
		// Ссылки у первой записи нет — берётся из второй: «появилась ссылка».
		b.recs[0].RegistrationURL = url1
		link := run(t, r, b, ingest.StatusOK)
		wantQueue(t, "ссылка второй записи", all,
			row(61, "change", link.RunID, root), row(62, "change", link.RunID, root),
			row(63, "change", link.RunID, root), row(64, "change", link.RunID, root))
		if c := changeOf(t, 63); !reflect.DeepEqual(c, event.Change{EventID: root, Link: true}) {
			t.Fatalf("изменение подписчика слитого = %+v", c)
		}
		// Дата первой записи — дата события.
		a.recs = records(1, 10)
		shift(&a.recs[0], 4)
		date := run(t, r, a, ingest.StatusOK)
		if n := len(queue(t, all...)); n != 8 || date.WatchChanged != 1 || date.WatchQueued != 4 {
			t.Fatalf("дата первой записи: строк %d, %+v; want 8", n, date)
		}
	})

	// (7) изменившийся старт выключенного вида спорта строк не даёт.
	t.Run("sport-off", func(t *testing.T) {
		r := watchRunner(t)
		recs := records(1, 10)
		recs[0].Sport = string(event.SportSkiing)
		src := seeded(t, r, "a", recs)
		fav(t, 71, eventOf(t, src, "1"))
		storetest.DisableSport(t, event.SportSkiing)

		src.recs = slices.Clone(recs)
		shift(&src.recs[0], 5)
		res := run(t, r, src, ingest.StatusOK)
		if res.Changed != 1 || res.WatchChanged != 0 || queueSize(t) != 0 {
			t.Fatalf("выключенный вид спорта: %+v, строк %d", res, queueSize(t))
		}
	})

	// (8) предохранитель считает изменения и скрытия вместе; (12) ключа
	// watch_held нет в метриках, пока он не сработал.
	t.Run("fuse", func(t *testing.T) {
		r := goneAt(t, 1)
		var buf bytes.Buffer
		r.Log = slog.New(slog.NewTextHandler(&buf, nil))
		recs := records(1, 60)
		src := seeded(t, r, "a", recs)
		ev := func(i int) int64 { return eventOf(t, src, fmt.Sprint(i)) }
		fav(t, 81, ev(1))
		fav(t, 82, ev(60))
		fav(t, 83, ev(59))
		fav(t, 84, ev(30))

		// Ровно порог: 19 изменений и одно скрытие — строки созданы.
		recs = slices.Clone(recs[:59])
		for i := range watchFuse - 1 {
			shift(&recs[i], 1)
		}
		src.recs = recs
		at := run(t, r, src, ingest.StatusOK)
		if at.WatchChanged != watchFuse-1 || at.WatchGone != 1 || at.WatchQueued != 2 || at.WatchHeld != 0 || watchHeld(t, src) != -1 {
			t.Fatalf("ровно порог: %+v, watch_held %d", at, watchHeld(t, src))
		}
		wantQueue(t, "ровно порог", []int64{81, 82, 83, 84}, row(81, "change", at.RunID, ev(1)), row(82, "gone", at.RunID, ev(60)))

		// На единицу больше: 20 изменений и одно скрытие — строк нет.
		recs = slices.Clone(recs[:58])
		for i := range watchFuse {
			shift(&recs[i], 1)
		}
		src.recs = recs
		was := eventRow(t, ev(1)).StartDate
		over := run(t, r, src, ingest.StatusOK)
		if over.WatchChanged != watchFuse || over.WatchGone != 1 || over.WatchQueued != 0 || over.WatchHeld != watchFuse+1 ||
			watchHeld(t, src) != watchFuse+1 || queueSize(t) != 2 {
			t.Fatalf("порог превышен: %+v, watch_held %d, строк %d; want строк прежние 2", over, watchHeld(t, src), queueSize(t))
		}
		// Каталог применён: дата изменена, старт скрыт.
		if e := eventRow(t, ev(1)); over.Changed != watchFuse || !e.StartDate.Equal(was.AddDate(0, 0, 1)) || eventRow(t, ev(59)).IsListed {
			t.Fatalf("порог превышен: каталог не применён: %+v, дата %s", over, e.StartDate)
		}
		checkWatchLog(t, buf.String(), fmt.Sprintf(`msg="watch held" run_id=%d changed=%d gone=1 queued=0 threshold=%d`, over.RunID, watchFuse, watchFuse))

		// Одних скрытий больше порога — то же.
		src.recs = slices.Concat(slices.Clone(recs[:21]), slices.Clone(recs[42:]))
		hide := run(t, r, src, ingest.StatusOK)
		if hide.WatchChanged != 0 || hide.WatchGone != watchFuse+1 || hide.WatchQueued != 0 || hide.WatchHeld != watchFuse+1 ||
			watchHeld(t, src) != watchFuse+1 || queueSize(t) != 2 || eventRow(t, ev(30)).IsListed {
			t.Fatalf("одни скрытия: %+v, watch_held %d, строк %d", hide, watchHeld(t, src), queueSize(t))
		}
		// Следующий обычный прогон — снова без ключа.
		if quiet := run(t, r, src, ingest.StatusOK); quiet.WatchHeld != 0 || watchHeld(t, src) != -1 {
			t.Fatalf("прогон после предохранителя: %+v, watch_held %d", quiet, watchHeld(t, src))
		}
	})

	// (9) таблица очереди недоступна: прогон ok, каталог применён, ошибка шага
	// в логе.
	t.Run("step-failure", func(t *testing.T) {
		r := watchRunner(t)
		var buf bytes.Buffer
		r.Log = slog.New(slog.NewTextHandler(&buf, nil))
		src := seeded(t, r, "a", records(1, 10))
		id := eventOf(t, src, "1")
		fav(t, 91, id)
		was := eventRow(t, id).StartDate

		// Схему тест после себя не меняет: имя возвращается.
		back := func() { dbExec(t, "ALTER TABLE IF EXISTS outbox_off RENAME TO outbox") }
		t.Cleanup(back)
		dbExec(t, "ALTER TABLE outbox RENAME TO outbox_off")
		src.recs = records(1, 10)
		shift(&src.recs[0], 5)
		res := run(t, r, src, ingest.StatusOK)
		back()

		if res.Changed != 1 || !eventRow(t, id).StartDate.Equal(was.AddDate(0, 0, 5)) {
			t.Fatalf("сбой шага: каталог не применён: %+v", res)
		}
		if res.WatchChanged != 0 || res.WatchQueued != 0 || queueSize(t) != 0 || lastRun(t, src.code).Status != "ok" {
			t.Fatalf("сбой шага: %+v, строк %d", res, queueSize(t))
		}
		if log := buf.String(); !strings.Contains(log, `msg="watch failed"`) || !strings.Contains(log, "outbox") {
			t.Fatalf("в логе нет ошибки шага:\n%s", log)
		}
		// Следующий прогон изменения уже не видит: сообщение потеряно.
		if again := run(t, r, src, ingest.StatusOK); again.WatchChanged != 0 || queueSize(t) != 0 {
			t.Fatalf("прогон после сбоя шага: %+v, строк %d", again, queueSize(t))
		}
	})

	// (10) слияние и отмена слияния вне прогона строк не создают.
	t.Run("merge-outside-run", func(t *testing.T) {
		r := goneAt(t, 1)
		src := seeded(t, r, "a", records(1, 10))
		root, loser := eventOf(t, src, "1"), eventOf(t, src, "2")
		fav(t, 101, root)
		fav(t, 102, loser)
		if err := testStore.MergeEvents(ctx, loser, root, string(event.MatchManual), nil, r.Fold); err != nil {
			t.Fatal(err)
		}
		if queueSize(t) != 0 {
			t.Fatalf("слияние: строк %d", queueSize(t))
		}
		if res := run(t, r, src, ingest.StatusOK); res.WatchChanged != 0 || res.WatchGone != 0 || queueSize(t) != 0 {
			t.Fatalf("прогон после слияния: %+v, строк %d", res, queueSize(t))
		}
		if _, err := testStore.UndoMerge(ctx, loser, 1, r.Fold, true); err != nil {
			t.Fatal(err)
		}
		if queueSize(t) != 0 {
			t.Fatalf("отмена слияния: строк %d", queueSize(t))
		}
		if res := run(t, r, src, ingest.StatusOK); res.WatchChanged != 0 || res.WatchGone != 0 || queueSize(t) != 0 {
			t.Fatalf("прогон после отмены слияния: %+v, строк %d", res, queueSize(t))
		}
	})

	// (11) лог прогона с изменением и скрытием — без названия старта,
	// идентификаторов события и пользователя.
	t.Run("log", func(t *testing.T) {
		r := goneAt(t, 1)
		var buf bytes.Buffer
		recs := records(1, 10)
		recs[0].Title, recs[1].Title = "Тайныйзабег первый", "Тайныйзабег второй"
		src := seeded(t, r, "a", recs)
		const user = 4242424242
		fav(t, user, eventOf(t, src, "1"))
		fav(t, user, eventOf(t, src, "2"))

		r.Log = slog.New(slog.NewTextHandler(&buf, nil))
		src.recs = slices.Concat(slices.Clone(recs[:1]), slices.Clone(recs[2:]))
		shift(&src.recs[0], 5)
		res := run(t, r, src, ingest.StatusOK)
		if res.WatchQueued != 2 {
			t.Fatalf("прогон: %+v, want две строки очереди", res)
		}
		log := buf.String()
		checkWatchLog(t, log, fmt.Sprintf("msg=watch run_id=%d changed=1 gone=1 queued=2", res.RunID))
		for _, secret := range []string{"Тайныйзабег", fmt.Sprint(user), "tg_id", "event_id", "title"} {
			if strings.Contains(log, secret) {
				t.Errorf("в логе прогона есть %q:\n%s", secret, log)
			}
		}
	})

	// (13) запись пропала: missing — строк нет; gone и скрытие — одна строка
	// каждому подписчику; дальше строк нет; статус прежний. (14) возвращение —
	// без строки; повторное скрытие — новая строка.
	t.Run("gone", func(t *testing.T) {
		r := goneAt(t, 2)
		all := records(1, 10)
		src := seeded(t, r, "a", all)
		id := eventOf(t, src, "1")
		fav(t, 131, id)
		fav(t, 132, id)
		users := []int64{131, 132}

		src.recs = all[1:]
		if res := run(t, r, src, ingest.StatusOK); res.Missing != 1 || res.WatchGone != 0 || !eventRow(t, id).IsListed || queueSize(t) != 0 {
			t.Fatalf("missing: %+v, строк %d", res, queueSize(t))
		}
		first := run(t, r, src, ingest.StatusOK)
		if e := eventRow(t, id); first.Gone != 1 || first.WatchGone != 1 || first.WatchQueued != 2 || e.IsListed || e.Status != event.StatusScheduled {
			t.Fatalf("gone: %+v, событие %+v", first, e)
		}
		wantQueue(t, "скрытие", users, row(131, "gone", first.RunID, id), row(132, "gone", first.RunID, id))
		if c := changeOf(t, 131); !reflect.DeepEqual(c, event.Change{EventID: id, Gone: true}) {
			t.Fatalf("изменение = %+v, want «пропал»", c)
		}
		if res := run(t, r, src, ingest.StatusOK); res.WatchGone != 0 || queueSize(t) != 2 {
			t.Fatalf("пока старт скрыт: %+v, строк %d", res, queueSize(t))
		}

		src.recs = all
		if res := run(t, r, src, ingest.StatusOK); res.WatchChanged != 0 || res.WatchGone != 0 || !eventRow(t, id).IsListed || queueSize(t) != 2 {
			t.Fatalf("возвращение: %+v, строк %d", res, queueSize(t))
		}
		src.recs = all[1:]
		run(t, r, src, ingest.StatusOK)
		second := run(t, r, src, ingest.StatusOK)
		if second.WatchGone != 1 || eventRow(t, id).IsListed || eventRow(t, id).Status != event.StatusScheduled {
			t.Fatalf("повторное скрытие: %+v", second)
		}
		wantQueue(t, "повторное скрытие", users,
			row(131, "gone", first.RunID, id), row(132, "gone", first.RunID, id),
			row(131, "gone", second.RunID, id), row(132, "gone", second.RunID, id))
	})

	// (15) событие с записями двух источников скрывается, только когда пропало
	// из обоих; (18) подписчик слитого события получает строку о корне.
	t.Run("gone-two-sources", func(t *testing.T) {
		r := goneAt(t, 1)
		a, b := seeded(t, r, "a", records(1, 10)), seeded(t, r, "b", records(201, 10))
		root, loser := eventOf(t, a, "1"), eventOf(t, b, "201")
		fav(t, 151, root)
		fav(t, 152, loser)
		if err := testStore.MergeEvents(ctx, loser, root, string(event.MatchManual), nil, r.Fold); err != nil {
			t.Fatal(err)
		}

		a.recs = records(2, 9)
		if res := run(t, r, a, ingest.StatusOK); res.Gone != 1 || res.WatchGone != 0 || !eventRow(t, root).IsListed || queueSize(t) != 0 {
			t.Fatalf("пропал из первого источника: %+v, строк %d", res, queueSize(t))
		}
		b.recs = records(202, 9)
		res := run(t, r, b, ingest.StatusOK)
		if res.Gone != 1 || res.WatchGone != 1 || eventRow(t, root).IsListed {
			t.Fatalf("пропал из обоих: %+v", res)
		}
		wantQueue(t, "пропал из обоих", []int64{151, 152}, row(151, "gone", res.RunID, root), row(152, "gone", res.RunID, root))
	})

	// (16) suspect без записи избранного старта: пропуск не считается, старт
	// виден, строк нет.
	t.Run("gone-suspect", func(t *testing.T) {
		r := goneAt(t, 1)
		src := seeded(t, r, "a", records(1, 10))
		id := eventOf(t, src, "1")
		fav(t, 161, id)

		src.recs = append(records(2, 9), rejected()...)
		res := run(t, r, src, ingest.StatusSuspect)
		missed := dbScalar[int](t, `SELECT se.missed_runs FROM source_events se JOIN sources s ON s.id = se.source_id
			WHERE s.code = $1 AND se.external_id = '1'`, src.code)
		if missed != 0 || res.WatchGone != 0 || !eventRow(t, id).IsListed || queueSize(t) != 0 {
			t.Fatalf("suspect: пропусков %d, %+v, строк %d", missed, res, queueSize(t))
		}
	})

	// (17) запись вышла за пределы каталога — старт скрыт в том же прогоне.
	t.Run("gone-out-of-scope", func(t *testing.T) {
		r := watchRunner(t)
		src := seeded(t, r, "a", records(1, 10))
		id := eventOf(t, src, "1")
		fav(t, 171, id)

		src.recs = records(1, 10)
		src.recs[0].Country = "Казахстан"
		res := run(t, r, src, ingest.StatusOK)
		if e := eventRow(t, id); res.OutOfScope != 1 || res.WatchGone != 1 || e.IsListed || e.Status != event.StatusScheduled {
			t.Fatalf("вне каталога: %+v, событие %+v", res, e)
		}
		wantQueue(t, "вне каталога", []int64{171}, row(171, "gone", res.RunID, id))
	})

	// (19) запись прошедшего события не найдена — событие не скрывается.
	t.Run("gone-past", func(t *testing.T) {
		r := goneAt(t, 1)
		soon := records(1, 1)
		soon[0].Date = fixtureNow.AddDate(0, 0, 5).Format("02/01/2006")
		src := seeded(t, r, "a", append(soon, records(20, 9)...))
		id := eventOf(t, src, "1")
		fav(t, 191, id)

		r.Now = func() time.Time { return fixtureNow.AddDate(0, 0, 10) }
		src.recs = records(20, 9)
		res := run(t, r, src, ingest.StatusOK)
		if res.Missing != 0 || res.Gone != 0 || res.WatchGone != 0 || !eventRow(t, id).IsListed || queueSize(t) != 0 {
			t.Fatalf("прошедший старт: %+v, строк %d", res, queueSize(t))
		}
	})

	// (20) счётчики записи и наблюдения прогона со скрытием — как без
	// избранного.
	t.Run("gone-counters", func(t *testing.T) {
		r := goneAt(t, 1)
		with, without := seeded(t, r, "a", records(1, 10)), seeded(t, r, "b", records(1, 10))
		fav(t, 201, eventOf(t, with, "1"))
		with.recs, without.recs = records(2, 9), records(2, 9)
		got, plain := run(t, r, with, ingest.StatusOK), run(t, r, without, ingest.StatusOK)
		if counts(got) != counts(plain) || got.Gone != 1 || got.WatchGone != 1 || plain.WatchGone != 1 || got.WatchQueued != 1 || plain.WatchQueued != 0 {
			t.Fatalf("со скрытием: с избранным %+v, без избранного %+v", got, plain)
		}
	})
}
