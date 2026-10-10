package store

import (
	"context"
	"fmt"
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

// TestOutbox — операции отправки: порядок взятия, «не более одного раза»,
// возврат, закрытие, срок годности и очистка.
func TestOutbox(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ev := listEvent(t, s, "2026-10-05", "Москва", "moscow", event.TypeRoad)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seq := 0
	put := func(tg int64, kind string, created time.Time) int64 {
		t.Helper()
		seq++
		return scalar[int64](t, `INSERT INTO outbox (tg_id, kind, event_id, dedup_key, payload, created_at)
			VALUES ($1, $2, $3, $4, '{"link": true}', $5) RETURNING id`, tg, kind, ev, fmt.Sprintf("%s:%d", kind, seq), created)
	}
	// state — pending, claimed (исход неизвестен), sent, closed:<исход> или «нет».
	state := func(id int64) string {
		t.Helper()
		return scalar[string](t, `SELECT COALESCE((SELECT CASE WHEN sent_at IS NOT NULL THEN 'sent'
			WHEN done_at IS NOT NULL THEN 'closed:' || outcome
			WHEN claimed_at IS NOT NULL THEN 'claimed' ELSE 'pending' END FROM outbox WHERE id = $1), 'нет')`, id)
	}
	claim := func(now time.Time) (OutboxRow, bool) {
		t.Helper()
		r, ok, err := s.ClaimOutbox(ctx, now)
		if err != nil {
			t.Fatalf("ClaimOutbox: %v", err)
		}
		return r, ok
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Взятие — в порядке id; строка приходит с содержимым; отметка ставится
	// временем из параметра.
	a, b, c := put(42, "change", at), put(43, "gone", at), put(42, "change", at)
	r, ok := claim(at)
	if !ok || r.ID != a || r.TgID != 42 || r.Kind != "change" || r.EventID != ev || string(r.Payload) != `{"link": true}` || !r.CreatedAt.Equal(at) {
		t.Fatalf("первое взятие = %+v, %v; want строка %d", r, ok, a)
	}
	if got := scalar[bool](t, `SELECT claimed_at = $2 FROM outbox WHERE id = $1`, a, at); !got {
		t.Error("отметка взятия не равна переданному времени")
	}
	if r, ok = claim(at); !ok || r.ID != b {
		t.Fatalf("второе взятие = %+v, %v; want строка %d", r, ok, b)
	}
	// Взятая и не отмеченная строка повторно не берётся — ни сразу, ни при сдвиге
	// часов на сутки; «исход неизвестен» видна запросом проверки.
	if r, ok = claim(at); !ok || r.ID != c {
		t.Fatalf("третье взятие = %+v, %v; want строка %d", r, ok, c)
	}
	for _, shift := range []time.Duration{0, time.Hour, 24 * time.Hour, 30 * 24 * time.Hour} {
		if r, ok = claim(at.Add(shift)); ok {
			t.Fatalf("взятие со сдвигом %v вернуло %+v: взятая строка взята повторно", shift, r)
		}
	}
	const unknownQ = `SELECT count(*) FROM outbox WHERE claimed_at IS NOT NULL AND sent_at IS NULL AND done_at IS NULL`
	if n := scalar[int64](t, unknownQ); n != 3 {
		t.Errorf("строк с неизвестным исходом %d, want 3", n)
	}

	// Возврат делает строку снова ожидающей; она берётся первой.
	must(s.ReleaseOutbox(ctx, b))
	if state(b) != "pending" {
		t.Fatalf("после возврата строка %s", state(b))
	}
	if r, ok = claim(at); !ok || r.ID != b {
		t.Fatalf("взятие после возврата = %+v, %v; want строка %d", r, ok, b)
	}

	// Отправка и закрытие убирают строку из ожидающих; возврат их не трогает.
	must(s.MarkOutboxSent(ctx, a, at))
	must(s.CloseOutbox(ctx, b, OutboxRejected, at))
	must(s.ReleaseOutbox(ctx, a))
	must(s.ReleaseOutbox(ctx, b))
	must(s.CloseOutbox(ctx, a, OutboxRejected, at)) // отправленную не закрыть
	must(s.MarkOutboxSent(ctx, b, at))              // закрытую не отметить отправленной
	if state(a) != "sent" || state(b) != "closed:rejected" {
		t.Errorf("строки %s и %s; want sent и closed:rejected", state(a), state(b))
	}
	if _, ok = claim(at); ok {
		t.Error("отправленная или закрытая строка взята")
	}
	// Отметка отправки ставится только взятой строке.
	fresh := put(42, "change", at)
	must(s.MarkOutboxSent(ctx, fresh, at))
	must(s.CloseOutbox(ctx, fresh, OutboxStale, at))
	if state(fresh) != "closed:stale" {
		t.Errorf("ожидающая строка %s; want closed:stale — закрыть можно и невзятую", state(fresh))
	}
	// Исход вне формата база отвергает.
	bad := put(42, "change", at)
	if err := s.CloseOutbox(ctx, bad, "Не по формату", at); err == nil {
		t.Error("исход вне формата принят")
	}
	if state(bad) != "pending" {
		t.Errorf("после отказа строка %s, want pending", state(bad))
	}

	// Закрытие ожидающих строк пользователя: чужие и другие взятые не трогаются,
	// переданная взятая закрывается.
	exec(t, "DELETE FROM outbox")
	p1, p2, other := put(42, "change", at), put(42, "gone", at), put(43, "change", at)
	taken, current := put(42, "change", at), put(42, "change", at)
	exec(t, `UPDATE outbox SET claimed_at = $1 WHERE id = ANY($2)`, at, []int64{taken, current})
	n, err := s.CloseUserOutbox(ctx, 42, current, OutboxBlocked, at)
	if err != nil || n != 3 {
		t.Fatalf("CloseUserOutbox = %d, %v; want 3", n, err)
	}
	if got := [5]string{state(p1), state(p2), state(current), state(taken), state(other)}; got !=
		[5]string{"closed:blocked", "closed:blocked", "closed:blocked", "claimed", "pending"} {
		t.Errorf("после закрытия: %v", got)
	}

	// Срок годности: закрываются только ожидающие строки названных видов, созданные
	// раньше границы; напоминания, свежие и взятые строки не затронуты.
	exec(t, "DELETE FROM outbox")
	cut := at.Add(-72 * time.Hour)
	oldChange, oldGone := put(42, "change", cut.Add(-time.Second)), put(42, "gone", cut.Add(-time.Hour))
	edge, freshChange := put(42, "change", cut), put(43, "change", at)
	oldReminder, oldTaken := put(42, "reminder", cut.Add(-time.Hour)), put(43, "gone", cut.Add(-time.Hour))
	exec(t, `UPDATE outbox SET claimed_at = $1 WHERE id = $2`, at, oldTaken)
	if n, err = s.ExpireOutbox(ctx, []string{"change", "gone"}, cut, at); err != nil || n != 2 {
		t.Fatalf("ExpireOutbox = %d, %v; want 2", n, err)
	}
	if got := [6]string{state(oldChange), state(oldGone), state(edge), state(freshChange), state(oldReminder), state(oldTaken)}; got !=
		[6]string{"closed:expired", "closed:expired", "pending", "pending", "pending", "claimed"} {
		t.Errorf("после срока годности: %v", got)
	}

	// Очистка: строки старше срока хранения удаляются в любом состоянии, свежие — нет.
	exec(t, "DELETE FROM outbox")
	keep := at.Add(-30 * 24 * time.Hour)
	var olds, news []int64
	for _, created := range []time.Time{keep.Add(-time.Second), keep} {
		sent, closed, unknown, pending := put(42, "change", created), put(42, "change", created), put(42, "change", created), put(42, "change", created)
		exec(t, `UPDATE outbox SET claimed_at = $2, sent_at = $2 WHERE id = $1`, sent, at)
		exec(t, `UPDATE outbox SET claimed_at = $2, done_at = $2, outcome = 'rejected' WHERE id = $1`, closed, at)
		exec(t, `UPDATE outbox SET claimed_at = $2 WHERE id = $1`, unknown, at)
		if created.Before(keep) {
			olds = append(olds, sent, closed, unknown, pending)
		} else {
			news = append(news, sent, closed, unknown, pending)
		}
	}
	if n, err = s.PruneOutbox(ctx, keep); err != nil || n != 4 {
		t.Fatalf("PruneOutbox = %d, %v; want 4", n, err)
	}
	for _, id := range olds {
		if state(id) != "нет" {
			t.Errorf("старая строка %d: %s, want удалена", id, state(id))
		}
	}
	for _, id := range news {
		if state(id) == "нет" {
			t.Errorf("свежая строка %d удалена", id)
		}
	}

	// Заблокированная другой транзакцией строка пропускается: два обработчика не
	// ждут друг друга и не берут одну строку. Предел ожидания — на случай поломки.
	exec(t, "DELETE FROM outbox")
	x1, x2 := put(42, "change", at), put(42, "change", at)
	err = s.InTx(ctx, func(tx *Store) error {
		got, ok, err := tx.ClaimOutbox(ctx, at)
		if err != nil || !ok || got.ID != x1 {
			t.Errorf("взятие в транзакции = %+v, %v, %v; want строка %d", got, ok, err, x1)
		}
		short, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		got, ok, err = s.ClaimOutbox(short, at)
		if err != nil || !ok || got.ID != x2 {
			t.Errorf("взятие при заблокированной строке = %+v, %v, %v; want строка %d", got, ok, err, x2)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("reminders", func(t *testing.T) { testOutboxReminders(t, s) })
	t.Run("fuse", func(t *testing.T) { testOutboxFuse(t, s) })
}

// testOutboxReminders — кандидаты на напоминание и запись строк (D-09…D-11,
// Q-9, Q-11): ровно семь дней, запись избранного строго раньше момента,
// видимый корень, статус «состоится»; заглушку запрос не отсеивает.
func testOutboxReminders(t *testing.T, s *Store) {
	ctx := context.Background()
	exec(t, "DELETE FROM outbox")
	exec(t, "DELETE FROM favorites")
	enableSports(t, event.SportRunning)
	today := mustDateFixed("2026-09-25")
	moment := time.Date(2026, 9, 25, 7, 0, 0, 0, time.UTC) // 10:00 по Москве
	early := moment.Add(-20 * time.Hour)
	fav := func(tg, ev int64, at time.Time) {
		t.Helper()
		exec(t, "INSERT INTO favorites (tg_id, event_id, created_at) VALUES ($1, $2, $3)", tg, ev, at)
	}

	seven := listEvent(t, s, "2026-10-02", "Москва", "moscow", event.TypeRoad)
	six := listEvent(t, s, "2026-10-01", "Тула", "tula", event.TypeRoad)
	eight := listEvent(t, s, "2026-10-03", "Псков", "pskov", event.TypeRoad)
	cancelled := listEvent(t, s, "2026-10-02", "Казань", "kazan", event.TypeRoad)
	postponed := listEvent(t, s, "2026-10-02", "Омск", "omsk", event.TypeRoad)
	hidden := listEvent(t, s, "2026-10-02", "Тверь", "tver", event.TypeRoad)
	ski := listEvent(t, s, "2026-10-02", "Минск", "minsk", event.TypeRoad)
	tbd := listEvent(t, s, "2026-10-02", "Сочи", "sochi", event.TypeRoad)
	root := listEvent(t, s, "2026-10-02", "Пермь", "perm", event.TypeRoad)
	old := listEvent(t, s, "2026-10-02", "Томск", "tomsk", event.TypeRoad) // будет слито в root
	exec(t, "UPDATE events SET status = 'cancelled' WHERE id = $1", cancelled)
	exec(t, "UPDATE events SET status = 'postponed' WHERE id = $1", postponed)
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", hidden)
	exec(t, "UPDATE events SET sport = 'skiing' WHERE id = $1", ski)
	exec(t, "UPDATE events SET title = title || ' (дата уточняется)' WHERE id = $1", tbd)
	exec(t, "UPDATE events SET merged_into_id = $1, is_listed = false WHERE id = $2", root, old)

	fav(42, seven, early)
	fav(42, six, early)
	fav(42, eight, early)
	fav(43, seven, moment.Add(time.Minute)) // позже момента
	fav(44, seven, moment)                  // ровно в момент: не раньше
	fav(45, seven, moment.Add(-time.Second))
	for _, ev := range []int64{cancelled, postponed, hidden, ski} {
		fav(46, ev, early)
	}
	fav(47, tbd, early)
	fav(48, old, early) // слитое событие даёт кандидата о корне
	fav(49, old, early) // записи и на слитое, и на корневое — один кандидат
	fav(49, root, early)
	fav(50, old, moment.Add(time.Minute)) // одна запись раньше момента, другая позже: кандидат
	fav(50, root, early)

	pairs := func(cs []ReminderCandidate) string {
		out := ""
		for _, c := range cs {
			out += fmt.Sprintf("%d:%d ", c.TgID, c.EventID)
		}
		return out
	}
	got, err := s.ReminderCandidates(ctx, today, 7, moment)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("42:%d 45:%d 47:%d 48:%d 49:%d 50:%d ", seven, seven, tbd, root, root, root)
	if pairs(got) != want {
		t.Fatalf("кандидаты %q, want %q", pairs(got), want)
	}
	// Заглушку запрос не отсеивает — название приходит боту, отсев за ним.
	for _, c := range got {
		if c.EventID == tbd && c.Title != "Забег 2026-10-02 Сочи (дата уточняется)" {
			t.Errorf("название кандидата-заглушки %q", c.Title)
		}
		if !c.StartDate.Equal(mustDateFixed("2026-10-02")) {
			t.Errorf("дата старта кандидата %v", c.StartDate)
		}
	}
	// Другое число дней — другие старты.
	if got, err = s.ReminderCandidates(ctx, today, 6, moment); err != nil || pairs(got) != fmt.Sprintf("42:%d ", six) {
		t.Errorf("кандидаты за 6 дней %q, %v", pairs(got), err)
	}

	// Запись строк: по строке на кандидата, повтор строк не добавляет.
	cands, _ := s.ReminderCandidates(ctx, today, 7, moment)
	now := time.Date(2026, 9, 25, 7, 5, 0, 0, time.UTC)
	if n, err := s.EnqueueReminders(ctx, cands, now); err != nil || n != len(cands) {
		t.Fatalf("EnqueueReminders = %d, %v; want %d", n, err, len(cands))
	}
	if n, err := s.EnqueueReminders(ctx, cands, now.Add(time.Minute)); err != nil || n != 0 {
		t.Errorf("повторная запись = %d, %v; want 0", n, err)
	}
	if n := scalar[int64](t, `SELECT count(*) FROM outbox WHERE kind = 'reminder' AND payload = '{}'::jsonb AND created_at = $1
		AND dedup_key = 'reminder:' || event_id || ':2026-10-02' AND claimed_at IS NULL AND sent_at IS NULL AND done_at IS NULL`, now); n != int64(len(cands)) {
		t.Errorf("строк с видом, ключом и временем %d, want %d", n, len(cands))
	}
	// Дату перенесли: тот же старт, другая дата — новый ключ и новая строка.
	moved := []ReminderCandidate{{TgID: 42, EventID: seven, StartDate: mustDateFixed("2026-10-09")}}
	if n, err := s.EnqueueReminders(ctx, moved, now); err != nil || n != 1 {
		t.Errorf("запись при новой дате = %d, %v; want 1", n, err)
	}
	// Пустой список — без запроса.
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if n, err := s.EnqueueReminders(dead, nil, now); err != nil || n != 0 {
		t.Errorf("пустой список = %d, %v", n, err)
	}
}

// testOutboxFuse — сообщения администраторам о сработавшем предохранителе
// (Q-7): по строке на администратора и завершённый прогон с watch_held за срок.
func testOutboxFuse(t *testing.T, s *Store) {
	ctx := context.Background()
	exec(t, "DELETE FROM outbox")
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	since := at.Add(-24 * time.Hour)
	run := func(metrics string, finished time.Time) int64 {
		t.Helper()
		id := finishedRun(t, s, src, RunFinish{Status: "ok", Metrics: []byte(metrics)})
		exec(t, "UPDATE ingest_runs SET finished_at = $2 WHERE id = $1", id, finished)
		return id
	}
	first := run(`{"watch_held": 23}`, at.Add(-time.Hour))
	second := run(`{"watch_held": 31}`, at.Add(-2*time.Hour))
	edge := run(`{"watch_held": 21}`, since)           // ровно на границе срока: входит
	run(`{}`, at.Add(-time.Hour))                      // без ключа
	run(`{"watch_held": "много"}`, at.Add(-time.Hour)) // ключ не число
	run(`{"watch_held": 25}`, since.Add(-time.Second)) // старше срока
	unfinished, err := s.StartRun(ctx, src, at, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, `UPDATE ingest_runs SET metrics = '{"watch_held": 40}' WHERE id = $1`, unfinished) // не завершён

	if n, err := s.EnqueueFuseNotices(ctx, nil, since, at); err != nil || n != 0 {
		t.Fatalf("без администраторов = %d, %v; want 0", n, err)
	}
	if n, err := s.EnqueueFuseNotices(ctx, []int64{-5, 0}, since, at); err != nil || n != 0 {
		t.Fatalf("идентификаторы не больше нуля = %d, %v; want 0", n, err)
	}
	if n, err := s.EnqueueFuseNotices(ctx, []int64{7001, 7002}, since, at); err != nil || n != 6 {
		t.Fatalf("EnqueueFuseNotices = %d, %v; want 6 (три прогона, два администратора)", n, err)
	}
	if n, err := s.EnqueueFuseNotices(ctx, []int64{7001, 7002}, since, at.Add(time.Minute)); err != nil || n != 0 {
		t.Errorf("повторный вызов = %d, %v; want 0", n, err)
	}
	for _, c := range []struct {
		run  int64
		held int
	}{{first, 23}, {second, 31}, {edge, 21}} {
		for _, tg := range []int64{7001, 7002} {
			want := fmt.Sprintf(`{"source": "marathonec", "run": %d, "held": %d}`, c.run, c.held)
			if n := scalar[int64](t, `SELECT count(*) FROM outbox WHERE tg_id = $1 AND kind = 'fuse' AND event_id IS NULL
				AND dedup_key = 'fuse:' || $2::bigint::text AND payload = $3::jsonb AND created_at = $4
				AND claimed_at IS NULL AND done_at IS NULL`, tg, c.run, want, at); n != 1 {
				t.Errorf("строка администратора %d о прогоне %d: %d, want 1", tg, c.run, n)
			}
		}
	}
	// Новый администратор получает строки только для себя.
	if n, err := s.EnqueueFuseNotices(ctx, []int64{7001, 7002, 7003}, since, at); err != nil || n != 3 {
		t.Errorf("третий администратор = %d, %v; want 3", n, err)
	}
	if n := scalar[int64](t, "SELECT count(*) FROM outbox WHERE kind IN ('change', 'gone', 'reminder')"); n != 0 {
		t.Errorf("строк об изменениях и напоминаний %d, want 0", n)
	}
}
