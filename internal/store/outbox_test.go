package store

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"aggregator/internal/event"
)

// TestWatchStates — в снимок входят будущие видимые корневые старты
// включённых видов спорта, с названием, датой, статусом и ссылкой.
func TestWatchStates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enableSports(t, event.SportRunning)
	today := mustDateFixed("2026-10-01")

	future := listEvent(t, s, "2026-10-05", "Москва", "moscow", event.TypeRoad)
	sameDay := listEvent(t, s, "2026-10-01", "Казань", "kazan", event.TypeRoad)
	past := listEvent(t, s, "2026-09-30", "Тула", "tula", event.TypeRoad)
	hidden := listEvent(t, s, "2026-10-06", "Псков", "pskov", event.TypeRoad)
	merged := listEvent(t, s, "2026-10-07", "Тверь", "tver", event.TypeRoad)
	ski := listEvent(t, s, "2026-10-08", "Минск", "minsk", event.TypeRoad)
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", hidden)
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", future, merged)
	exec(t, "UPDATE events SET sport = 'skiing' WHERE id = $1", ski)
	exec(t, "UPDATE events SET status = 'cancelled', registration_url = NULL WHERE id = $1", sameDay)

	// Время дня у today отбрасывается: старт сегодняшнего дня входит.
	got, err := s.WatchStates(ctx, today.Add(23*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]event.WatchState{
		future: {Title: "Забег 2026-10-05 Москва", StartDate: mustDateFixed("2026-10-05"), Status: event.StatusScheduled,
			URL: "https://example.test/register"},
		sameDay: {Title: "Забег 2026-10-01 Казань", StartDate: mustDateFixed("2026-10-01"), Status: event.StatusCancelled},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WatchStates = %+v\nwant %+v (без прошедшего %d, скрытого %d, слитого %d и выключенного вида спорта %d)",
			got, want, past, hidden, merged, ski)
	}
}

// TestHiddenEvents — из названных событий возвращаются только скрытые и не
// слитые; запрос только читает.
func TestHiddenEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	visible := listEvent(t, s, "2026-10-05", "Москва", "moscow", event.TypeRoad)
	hidden := listEvent(t, s, "2026-10-06", "Псков", "pskov", event.TypeRoad)
	merged := listEvent(t, s, "2026-10-07", "Тверь", "tver", event.TypeRoad)
	other := listEvent(t, s, "2026-10-08", "Тула", "tula", event.TypeRoad) // скрыт, но не назван
	cancelled := listEvent(t, s, "2026-10-09", "Казань", "kazan", event.TypeRoad)
	// Слитое событие не показывается (is_listed = false), но скрытым не считается.
	exec(t, "UPDATE events SET is_listed = false WHERE id = ANY($1)", []int64{hidden, merged, other, cancelled})
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", visible, merged)
	exec(t, "UPDATE events SET status = 'cancelled' WHERE id = $1", cancelled)
	state := func() string {
		return scalar[string](t, "SELECT string_agg(e::text, '|' ORDER BY id) FROM events e")
	}
	was := state()

	got, err := s.HiddenEvents(ctx, []int64{cancelled, visible, merged, hidden, cancelled + 1000})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{hidden, cancelled}; !reflect.DeepEqual(got, want) {
		t.Errorf("HiddenEvents = %v, want %v", got, want)
	}
	if now := state(); now != was {
		t.Errorf("HiddenEvents изменил события:\n%s\nбыло\n%s", now, was)
	}

	// Пустой перечень — пустой ответ без запроса: отменённый контекст не мешает.
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := s.HiddenEvents(dead, nil); err != nil || len(got) != 0 {
		t.Errorf("HiddenEvents(пусто) = %v, %v; want пусто без запроса", got, err)
	}
	if _, err := s.HiddenEvents(dead, []int64{hidden}); err == nil {
		t.Error("HiddenEvents с отменённым контекстом: err = nil — запроса не было")
	}
}

// TestEnqueueChange — строки очереди подписчикам корневого события: одна на
// пользователя, вид по изменению, повтор того же прогона строк не создаёт.
func TestEnqueueChange(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enableSports(t, event.SportRunning)
	today := mustDateFixed("2026-10-01")
	at := time.Date(2026, 10, 1, 4, 40, 0, 0, time.UTC)

	root := listEvent(t, s, "2026-10-05", "Москва", "moscow", event.TypeRoad)
	old := listEvent(t, s, "2026-10-03", "Псков", "pskov", event.TypeRoad) // будет слито в root
	lonely := listEvent(t, s, "2026-10-06", "Тула", "tula", event.TypeRoad)
	for _, f := range [][2]int64{{42, root}, {43, root}, {44, old}, {45, old}, {45, root}} {
		if out, err := s.AddFavorite(ctx, f[0], f[1], today, at, 100); err != nil || out != FavAdded {
			t.Fatalf("AddFavorite%v = %v, %v", f, out, err)
		}
	}
	exec(t, "UPDATE events SET merged_into_id = $1, is_listed = false WHERE id = $2", root, old)

	enqueue := func(run int64, c event.Change) int {
		t.Helper()
		n, err := s.EnqueueChange(ctx, run, c, at)
		if err != nil {
			t.Fatalf("EnqueueChange: %v", err)
		}
		return n
	}
	// rows — строки очереди как «пользователь вид событие ключ содержимое».
	rows := func() string {
		t.Helper()
		return scalar[string](t, `SELECT COALESCE(string_agg(concat_ws(' ', tg_id, kind, event_id, dedup_key, payload), E'\n' ORDER BY id), '') FROM outbox`)
	}
	count := func() int64 { return scalar[int64](t, "SELECT count(*) FROM outbox") }

	link := event.Change{EventID: root, Link: true}
	// Четыре подписчика: двое на корень, один через слитое событие, один с
	// записями и на слитое, и на корень — ему одна строка.
	if n := enqueue(7, link); n != 4 {
		t.Fatalf("EnqueueChange = %d, want 4\n%s", n, rows())
	}
	if n := scalar[int64](t, `SELECT count(*) FROM outbox WHERE kind = 'change' AND event_id = $1 AND dedup_key = $2
		AND payload = '{"link": true}'::jsonb AND created_at = $3 AND tg_id IN (42, 43, 44, 45)
		AND claimed_at IS NULL AND sent_at IS NULL AND done_at IS NULL`,
		root, "change:7:"+strconv.FormatInt(root, 10), at); n != 4 {
		t.Errorf("строк с корнем, ключом, содержимым и переданным временем %d, want 4\n%s", n, rows())
	}

	// Без подписчиков — ноль строк; тот же прогон и событие — ноль новых.
	if n := enqueue(7, event.Change{EventID: lonely, Link: true}); n != 0 {
		t.Errorf("без подписчиков: %d строк", n)
	}
	if n := enqueue(7, link); n != 0 || count() != 4 {
		t.Errorf("повтор прогона: создано %d, всего %d; want 0 и 4", n, count())
	}
	// Другой прогон — новые строки.
	if n := enqueue(8, link); n != 4 || count() != 8 {
		t.Errorf("другой прогон: создано %d, всего %d; want 4 и 8", n, count())
	}
	// «Пропал» — вид gone, свой ключ; с изменением полей того же прогона не
	// сталкивается.
	if n := enqueue(8, event.Change{EventID: root, Gone: true}); n != 4 {
		t.Errorf("«пропал»: создано %d, want 4", n)
	}
	if n := scalar[int64](t, `SELECT count(*) FROM outbox WHERE kind = 'gone' AND event_id = $1 AND dedup_key = $2 AND payload = '{}'::jsonb`,
		root, "gone:8:"+strconv.FormatInt(root, 10)); n != 4 {
		t.Errorf("строк вида gone %d, want 4\n%s", n, rows())
	}

	// Содержимое строки читается обратно в то же изменение.
	exec(t, "DELETE FROM outbox")
	full := event.Change{EventID: root, Link: true,
		Date:   &event.DateChange{From: mustDateFixed("2026-10-01"), To: mustDateFixed("2026-10-05"), FromTBD: true},
		Status: &event.StatusChange{From: event.StatusScheduled, To: event.StatusPostponed}}
	for _, c := range []event.Change{full, {EventID: root, Gone: true}} {
		exec(t, "DELETE FROM outbox")
		enqueue(9, c)
		kind := scalar[string](t, "SELECT kind FROM outbox WHERE tg_id = 42")
		id := scalar[int64](t, "SELECT event_id FROM outbox WHERE tg_id = 42")
		payload := scalar[string](t, "SELECT payload::text FROM outbox WHERE tg_id = 42")
		back, err := event.ParseChange(kind, id, []byte(payload))
		if err != nil || !reflect.DeepEqual(back, c) {
			t.Errorf("строка %s %d %s читается как %+v, %v; want %+v", kind, id, payload, back, err, c)
		}
	}
}
