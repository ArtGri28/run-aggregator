package bot

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Отправка сообщений из очереди (14-UX-CONTRACT.md, сценарии S-48, S-49,
// S-53). Эталоны testdata/screens/s48_*, s49_*, s53_* написаны вручную по
// контракту. Время подставное (Bot.Now): тесты вызывают шаг Notifier.Step
// напрямую и ничего не ждут. Строки очереди создаются так, как их пишет
// прогон: вид, событие и содержимое — по event.Change.

var outboxSeq atomic.Int64

// notifyPutRaw записывает строку очереди как есть и возвращает её id.
func notifyPutRaw(t *testing.T, tg int64, kind string, eventID int64, payload string, created time.Time) int64 {
	t.Helper()
	key := fmt.Sprintf("%s:test:%d", kind, outboxSeq.Add(1))
	var id int64
	if _, err := fmt.Sscan(dbScalar(t, `INSERT INTO outbox (tg_id, kind, event_id, dedup_key, payload, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6) RETURNING id::text`, tg, kind, eventID, key, payload, created), &id); err != nil {
		t.Fatal(err)
	}
	return id
}

// notifyPut записывает строку очереди об изменении c пользователю tg.
func notifyPut(t *testing.T, tg int64, c event.Change, created time.Time) int64 {
	t.Helper()
	return notifyPutRaw(t, tg, c.Kind(), c.EventID, string(c.Payload()), created)
}

// rowState — состояние строки очереди: pending, claimed (исход неизвестен),
// sent либо closed:<исход>; строки нет — «нет».
func rowState(t *testing.T, id int64) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE((SELECT CASE WHEN sent_at IS NOT NULL THEN 'sent'
		WHEN done_at IS NOT NULL THEN 'closed:' || outcome
		WHEN claimed_at IS NOT NULL THEN 'claimed' ELSE 'pending' END FROM outbox WHERE id = $1), 'нет')`, id)
}

// notifyRig — каталог сценариев (133 беговых и пять лыжных событий), бот на
// фиктивном API и цикл отправки. reply подменяет ответы Bot API на время шага;
// notifyReset возвращает исходное состояние: очередь пуста, старт 52 в
// избранном пользователей 42 и 43, часы бота — fixtureNow.
type notifyRig struct {
	s     *store.Store
	api   *fakeAPI
	b     *Bot
	n     *Notifier
	reply script
}

func newNotifyRig(t *testing.T) *notifyRig {
	t.Helper()
	r := &notifyRig{}
	r.s = storetest.Open(t)
	seedMarathonec(t, r.s)
	seedSki(t, r.s)
	storetest.EnableSport(t, event.SportSkiing)
	r.api = fakeTelegram(t, func(c call, n int) string {
		if r.reply != nil {
			return r.reply(c, n)
		}
		return ""
	})
	r.b = newTestBot(t, r.api, r.s)
	r.reset(t)
	return r
}

func (r *notifyRig) reset(t *testing.T) {
	t.Helper()
	dbExec(t, `DELETE FROM outbox`)
	dbExec(t, `DELETE FROM favorites`)
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 52, $1), (43, 52, $1)`, fixtureNow)
	r.reply = nil
	r.at(fixtureNow)
	r.n = NewNotifier(r.b)
	r.api.reset()
}

// at ставит часы бота.
func (r *notifyRig) at(now time.Time) { r.b.Now = func() time.Time { return now } }

// step — один шаг цикла; возвращает вызовы Bot API этого шага.
func (r *notifyRig) step() []call {
	r.api.reset()
	r.n.Step(context.Background())
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	return append([]call(nil), r.api.calls...)
}

// sent — шаг, в котором ушло ровно одно сообщение; возвращает его.
func (r *notifyRig) sent(t *testing.T, what string) call {
	t.Helper()
	calls := r.step()
	if len(calls) != 1 || calls[0].Method != "sendMessage" {
		t.Fatalf("%s: вызовы %q, want один sendMessage", what, methods(calls))
	}
	return calls[0]
}

// silent — шаг без вызовов Bot API.
func (r *notifyRig) silent(t *testing.T, what string) {
	t.Helper()
	if calls := r.step(); len(calls) != 0 {
		t.Fatalf("%s: вызовы %q, want ни одного", what, methods(calls))
	}
}

// promiseRE — слова, которых в сообщениях нет: об открытой регистрации, о
// первенстве и о цене.
var promiseRE = regexp.MustCompile(`(?i)регистрац|первым|лучшей цене|цена|цены`)

// plain проверяет текст сообщения: ни обещаний о регистрации и цене, ни слов о
// данных пользователя.
func plain(t *testing.T, c call) {
	t.Helper()
	if m := promiseRE.FindString(c.Form["text"]); m != "" {
		t.Errorf("в тексте сообщения %q слово %q", c.Form["text"], m)
	}
	if m := regexp.MustCompile(`(?i)данн|хранит`).FindString(c.Form["text"]); m != "" {
		t.Errorf("в тексте сообщения %q слово %q", c.Form["text"], m)
	}
}

// TestNotifyTracer — сквозной путь: строка очереди об изменении, записанная
// функцией прогона → шаг цикла берёт её → сообщение N-1 с двумя кнопками уходит
// пользователю → строка отмечена отправленной; второй шаг ничего не шлёт.
func TestNotifyTracer(t *testing.T) {
	var atCall string // состояние строки в момент вызова Bot API
	s, api, b := seeded(t, func(c call, _ int) string {
		if c.Method == "sendMessage" {
			atCall = dbScalar(t, `SELECT COALESCE(string_agg(CASE WHEN sent_at IS NOT NULL THEN 'sent'
				WHEN done_at IS NOT NULL THEN 'closed' WHEN claimed_at IS NOT NULL THEN 'claimed' ELSE 'pending' END, ','), '') FROM outbox`)
		}
		return ""
	})
	ctx := context.Background()
	if out, err := s.AddFavorite(ctx, 42, 52, b.today(), fixtureNow, favLimit); err != nil || out != store.FavAdded {
		t.Fatalf("AddFavorite = %v, %v", out, err)
	}
	if n, err := s.EnqueueChange(ctx, 1, event.Change{EventID: 52, Link: true}, fixtureNow); err != nil || n != 1 {
		t.Fatalf("EnqueueChange = %d, %v", n, err)
	}
	n := NewNotifier(b)

	api.reset()
	n.Step(ctx)
	calls := api.of("sendMessage")
	if len(calls) != 1 || api.total() != 1 {
		t.Fatalf("первый шаг: вызовы %d из %d, want один sendMessage", len(calls), api.total())
	}
	c := calls[0]
	if got := datas(t, c); strings.Join(got, " ") != "w:n:52 w:x:52" || c.Form["chat_id"] != "42" {
		t.Errorf("сообщение: кнопки %v, получатель %s; want w:n:52 w:x:52 и 42", got, c.Form["chat_id"])
	}
	if atCall != "claimed" {
		t.Errorf("в момент вызова Bot API строка %q, want claimed: отметка взятия ставится до вызова", atCall)
	}
	if st := dbScalar(t, `SELECT CASE WHEN sent_at IS NOT NULL THEN 'sent' ELSE 'нет' END FROM outbox`); st != "sent" {
		t.Errorf("строка после отправки: %s, want sent", st)
	}
	api.reset()
	n.Step(ctx)
	resend := api.total()
	if resend != 0 {
		t.Errorf("второй шаг: вызовов %d, want 0", resend)
	}
	if !t.Failed() {
		t.Logf("TRACER notify=sent calls=%d resend=%d", len(calls), resend)
	}
}

// TestS48ChangeMessage — S-48: сообщение об изменении старта.
func TestS48ChangeMessage(t *testing.T) {
	r := newNotifyRig(t)
	logs := captureLogs(t)
	d := func(s string) time.Time { return mustDate(t, s) }
	put := func(c event.Change) int64 { return notifyPut(t, 42, c, fixtureNow) }

	// 1. «Появилась ссылка»: N-1 с двумя кнопками, строка отмечена отправленной.
	id := put(event.Change{EventID: 52, Link: true})
	link := r.sent(t, "шаг 1")
	matchSnapshot(t, "s48_link", renderCall(t, link))
	plain(t, link)
	checkCallbackLengths(t, link)
	if link.Form["chat_id"] != "42" || rowState(t, id) != "sent" {
		t.Errorf("шаг 1: получатель %s, строка %s", link.Form["chat_id"], rowState(t, id))
	}
	r.silent(t, "шаг 1, повтор")

	// 2. Изменилась дата.
	put(event.Change{EventID: 52, Date: &event.DateChange{From: d("2026-10-10"), To: d("2026-10-03")}})
	date := r.sent(t, "шаг 2")
	matchSnapshot(t, "s48_date", renderCall(t, date))
	plain(t, date)

	// 3. Дата-заглушка заменена точной; заглушка показана с пометкой.
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 135, $1)`, fixtureNow)
	put(event.Change{EventID: 135, Date: &event.DateChange{From: d("2026-12-01"), To: d("2026-12-19"), FromTBD: true}})
	tbd := r.sent(t, "шаг 3")
	matchSnapshot(t, "s48_date_tbd", renderCall(t, tbd))
	plain(t, tbd)

	// 4. Статус: «[ОТМЕНЁН]» в названии и строка статуса.
	storetest.SetStatus(t, 52, event.StatusCancelled)
	put(event.Change{EventID: 52, Status: &event.StatusChange{From: event.StatusScheduled, To: event.StatusCancelled}})
	status := r.sent(t, "шаг 4")
	matchSnapshot(t, "s48_status", renderCall(t, status))
	plain(t, status)

	// 5. Три изменения — одно сообщение, строки в порядке «ссылка, дата, статус».
	put(event.Change{EventID: 52, Link: true,
		Date:   &event.DateChange{From: d("2026-10-10"), To: d("2026-10-03")},
		Status: &event.StatusChange{From: event.StatusScheduled, To: event.StatusCancelled}})
	multi := r.sent(t, "шаг 5")
	matchSnapshot(t, "s48_multi", renderCall(t, multi))
	plain(t, multi)
	storetest.SetStatus(t, 52, event.StatusScheduled)

	// Обратная замена и заглушка того же числа — по строкам контракта.
	put(event.Change{EventID: 135, Date: &event.DateChange{From: d("2026-12-19"), To: d("2026-12-01"), ToTBD: true}})
	if got := r.sent(t, "обратная замена").Form["text"]; !strings.Contains(got, "📅 Дата: 19 декабря 2026 → 1 декабря 2026 (дата уточняется)\n") && !strings.HasSuffix(got, "📅 Дата: 19 декабря 2026 → 1 декабря 2026 (дата уточняется)") {
		t.Errorf("обратная замена: %q", got)
	}
	put(event.Change{EventID: 135, Date: &event.DateChange{From: d("2027-01-01"), To: d("2027-01-01"), FromTBD: true}})
	if got := r.sent(t, "заглушка того же числа").Form["text"]; !strings.HasSuffix(got, "📅 Дата: 1 января 2027 (дата уточняется) → 1 января 2027") {
		t.Errorf("заглушка того же числа: %q", got)
	}

	// 6. «Открыть старт» — карточка новым сообщением, исходное не правится.
	calls := favTap(t, r.b, r.api, 42, &link, "w:n:52")
	if methods(calls) != "sendMessage answerCallbackQuery" {
		t.Fatalf("шаг 6: вызовы %q, want новое сообщение и ответ", methods(calls))
	}
	matchSnapshot(t, "s48_open", renderCall(t, calls[0]))
	storetest.HideEvent(t, 52)
	calls = favTap(t, r.b, r.api, 42, &link, "w:n:52")
	if methods(calls) != "sendMessage answerCallbackQuery" {
		t.Fatalf("шаг 6, скрытый старт: вызовы %q", methods(calls))
	}
	matchSnapshot(t, "s48_open_gone", renderCall(t, calls[0]))
	dbExec(t, `UPDATE events SET is_listed = true WHERE id = 52`)

	// 7. «Убрать из избранного» в сообщении: запись удалена, «Открыть старт» на месте.
	favKeys(t, r.b, r.api, 42, &link, "w:x:52", "s48_removed")
	if n := favCount(t, "tg_id = 42 AND event_id = 52"); n != "0" {
		t.Errorf("шаг 7: записей %s, want 0", n)
	}
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 52, $1)`, fixtureNow)

	// 8. Старт убран из избранного до отправки: вызова нет, строка закрыта.
	id = put(event.Change{EventID: 52, Link: true})
	dbExec(t, `DELETE FROM favorites WHERE tg_id = 42 AND event_id = 52`)
	r.silent(t, "шаг 8")
	if got := rowState(t, id); got != "closed:unfollowed" {
		t.Errorf("шаг 8: строка %s", got)
	}
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 52, $1)`, fixtureNow)

	// 9. Старт скрыт до отправки: вызова нет, строка закрыта.
	id = put(event.Change{EventID: 52, Link: true})
	storetest.HideEvent(t, 52)
	r.silent(t, "шаг 9")
	if got := rowState(t, id); got != "closed:unavailable" {
		t.Errorf("шаг 9: строка %s", got)
	}
	dbExec(t, `UPDATE events SET is_listed = true WHERE id = 52`)

	// Выключенный вид спорта и прошедший старт — как скрытый.
	id = put(event.Change{EventID: 135, Link: true})
	storetest.DisableSport(t, event.SportSkiing)
	r.silent(t, "вид спорта выключен")
	storetest.EnableSport(t, event.SportSkiing)
	oct4 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	id2 := notifyPut(t, 42, event.Change{EventID: 52, Link: true}, oct4)
	r.at(oct4)
	r.silent(t, "старт прошёл")
	r.at(fixtureNow)
	if a, b := rowState(t, id), rowState(t, id2); a != "closed:unavailable" || b != "closed:unavailable" {
		t.Errorf("выключенный вид спорта %s, прошедший старт %s", a, b)
	}

	// Событие слито после записи в очередь: сообщение о корневом событии.
	r.reset(t)
	dbExec(t, `DELETE FROM favorites WHERE event_id = 52`)
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 110, $1)`, fixtureNow)
	notifyPut(t, 42, event.Change{EventID: 110, Link: true}, fixtureNow)
	storetest.MergeEvent(t, 110, 52)
	matchSnapshot(t, "s48_link", renderCall(t, r.sent(t, "слитое событие")))

	// Название с разметкой экранируется, как в карточке.
	const markup = `Забег <b>"A&B"</b> «Ё»`
	dbExec(t, `UPDATE events SET title = $1 WHERE id = 52`, markup)
	notifyPut(t, 42, event.Change{EventID: 52, Link: true}, fixtureNow)
	esc := r.sent(t, "название с разметкой").Form["text"]
	if !strings.Contains(esc, html.EscapeString(markup)) || strings.Contains(esc, "<b>\"A") {
		t.Errorf("название не экранировано: %q", esc)
	}

	// В логе — только вид и исход.
	out := logs.String()
	for _, leak := range []string{"Шарташ", "Карасики", "=42", "=43", "=52", "tg_id", "event_id", "Ссылка"} {
		if strings.Contains(out, leak) {
			t.Errorf("в логе есть %q:\n%s", leak, out)
		}
	}
}

// TestS49Delivery — S-49: порядок, ночь без звука, ответы Bot API, сбой до
// вызова, перезапуск, срок годности, нечитаемые строки.
func TestS49Delivery(t *testing.T) {
	r := newNotifyRig(t)
	logs := captureLogs(t)
	link := event.Change{EventID: 52, Link: true}
	ctx := context.Background()
	put := func(tg int64) int64 { return notifyPut(t, tg, link, fixtureNow) }

	// 1. Три ожидающие строки (42, 43, 42): за шаг одно сообщение, по порядку очереди.
	ids := []int64{put(42), put(43), put(42)}
	for i, want := range []string{"42", "43", "42"} {
		c := r.sent(t, fmt.Sprintf("шаг 1.%d", i+1))
		matchSnapshot(t, "s49_first", renderCall(t, c))
		if c.Form["chat_id"] != want || rowState(t, ids[i]) != "sent" {
			t.Errorf("шаг 1.%d: получатель %s, строка %s; want %s и sent", i+1, c.Form["chat_id"], rowState(t, ids[i]), want)
		}
		if i < 2 && rowState(t, ids[i+1]) != "pending" {
			t.Errorf("шаг 1.%d: следующая строка %s, want pending — за шаг уходит одно сообщение", i+1, rowState(t, ids[i+1]))
		}
	}
	r.silent(t, "шаг 1, очередь пуста")

	// 2. Ночь по Москве: 22:59 — со звуком, 23:00 и 07:59 — без звука, 08:00 — со звуком.
	for _, tc := range []struct {
		h, m  int
		quiet bool
	}{{22, 59, false}, {23, 0, true}, {7, 59, true}, {8, 0, false}, {0, 0, true}, {12, 0, false}} {
		r.at(time.Date(2026, 9, 29, tc.h, tc.m, 0, 0, r.b.loc))
		notifyPut(t, 42, link, time.Date(2026, 9, 29, tc.h, tc.m, 0, 0, r.b.loc))
		c := r.sent(t, fmt.Sprintf("ночь %02d:%02d", tc.h, tc.m))
		matchSnapshot(t, "s49_first", renderCall(t, c))
		if got := c.Form["disable_notification"] == "true"; got != tc.quiet {
			t.Errorf("%02d:%02d: без звука = %v, want %v", tc.h, tc.m, got, tc.quiet)
		}
	}
	for _, tc := range []struct {
		at    time.Time
		quiet bool
	}{
		{time.Date(2026, 9, 29, 19, 59, 0, 0, time.UTC), false}, {time.Date(2026, 9, 29, 20, 0, 0, 0, time.UTC), true},
		{time.Date(2026, 9, 30, 4, 59, 0, 0, time.UTC), true}, {time.Date(2026, 9, 30, 5, 0, 0, 0, time.UTC), false},
	} {
		if got := quietAt(tc.at, r.b.loc); got != tc.quiet {
			t.Errorf("quietAt(%s UTC) = %v, want %v", tc.at.Format("15:04"), got, tc.quiet)
		}
	}

	// 3. Ответ 403: строка и остальные ожидающие строки пользователя 42 закрыты,
	// избранное на месте, сообщение пользователя 43 уходит следующим шагом.
	r.reset(t)
	a, a2, c43 := put(42), put(42), put(43)
	taken := put(42) // уже взятая строка того же пользователя не закрывается как ожидающая
	dbExec(t, `UPDATE outbox SET claimed_at = $2 WHERE id = $1`, taken, fixtureNow)
	r.reply = func(c call, _ int) string {
		if c.Form["chat_id"] == "42" {
			return errBlocked
		}
		return ""
	}
	if calls := r.step(); len(calls) != 1 || calls[0].Form["chat_id"] != "42" {
		t.Fatalf("шаг 3: вызовы %q, want один — пользователю 42", methods(calls))
	}
	if s := [3]string{rowState(t, a), rowState(t, a2), rowState(t, taken)}; s != [3]string{"closed:blocked", "closed:blocked", "claimed"} {
		t.Errorf("шаг 3: строки пользователя 42 %v, want закрыты обе ожидающие, взятая прежняя", s)
	}
	if favCount(t, "tg_id = 42 AND event_id = 52") != "1" {
		t.Error("шаг 3: избранное пользователя 42 не на месте")
	}
	c := r.sent(t, "шаг 3, пользователь 43")
	matchSnapshot(t, "s49_after_blocked", renderCall(t, c))
	if c.Form["chat_id"] != "43" || rowState(t, c43) != "sent" {
		t.Errorf("шаг 3: получатель %s, строка %s", c.Form["chat_id"], rowState(t, c43))
	}

	// 4. Ответ 429 со сроком 30 секунд: строка не потеряна, шаги до срока молчат,
	// после срока сообщение уходит один раз. Ожидания внутри вызова нет.
	r.reset(t)
	id := put(42)
	other := put(43)
	limited := false
	r.reply = func(_ call, _ int) string {
		if !limited {
			limited = true
			return apiErr(429, "Too Many Requests: retry after 30", 30)
		}
		return ""
	}
	start := time.Now()
	if calls := r.step(); len(calls) != 1 {
		t.Fatalf("шаг 4: вызовы %q, want один (без повтора внутри вызова)", methods(calls))
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("шаг 4: шаг занял %v — ожидание внутри вызова", d)
	}
	if rowState(t, id) != "pending" {
		t.Errorf("шаг 4: строка %s, want pending — возвращена в очередь", rowState(t, id))
	}
	for _, at := range []time.Duration{time.Second, 29 * time.Second} {
		r.at(fixtureNow.Add(at))
		r.silent(t, fmt.Sprintf("шаг 4, +%v", at))
	}
	if rowState(t, id) != "pending" || rowState(t, other) != "pending" {
		t.Errorf("шаг 4: во время паузы строки %s и %s, want pending и pending", rowState(t, id), rowState(t, other))
	}
	r.at(fixtureNow.Add(30 * time.Second))
	again := r.sent(t, "шаг 4, +30 с")
	if again.Form["chat_id"] != "42" || rowState(t, id) != "sent" {
		t.Errorf("шаг 4: после срока ушло %s, строка %s; want та же строка пользователю 42", again.Form["chat_id"], rowState(t, id))
	}
	if c := r.sent(t, "шаг 4, следующая"); c.Form["chat_id"] != "43" {
		t.Errorf("шаг 4: следующая строка ушла %s, want 43", c.Form["chat_id"])
	}
	r.silent(t, "шаг 4, очередь пуста")

	// 5. Ответ 400: строка закрыта, повтора нет, следующая уходит.
	r.reset(t)
	bad, next := put(42), put(43)
	r.reply = func(c call, _ int) string {
		if c.Form["chat_id"] == "42" {
			return apiErr(400, "Bad Request: chat not found", 0)
		}
		return ""
	}
	if calls := r.step(); len(calls) != 1 || rowState(t, bad) != "closed:rejected" {
		t.Fatalf("шаг 5: вызовы %q, строка %s", methods(calls), rowState(t, bad))
	}
	if c := r.sent(t, "шаг 5, следующая"); c.Form["chat_id"] != "43" || rowState(t, next) != "sent" {
		t.Errorf("шаг 5: следующая строка %s", rowState(t, next))
	}

	// Иной отказ Telegram (не 400, не 403, не 429): исход неизвестен, повтора нет.
	r.reset(t)
	odd, after := put(42), put(43)
	r.reply = func(c call, _ int) string {
		if c.Form["chat_id"] == "42" {
			return apiErr(500, "Internal Server Error", 0)
		}
		return ""
	}
	if calls := r.step(); len(calls) != 1 || rowState(t, odd) != "claimed" {
		t.Fatalf("иной отказ: вызовы %q, строка %s; want один вызов и claimed", methods(calls), rowState(t, odd))
	}
	r.at(fixtureNow.Add(24 * time.Hour))
	if c := r.sent(t, "иной отказ, следующая"); c.Form["chat_id"] != "43" || rowState(t, after) != "sent" || rowState(t, odd) != "claimed" {
		t.Errorf("иной отказ: следующая строка %s, прежняя %s", rowState(t, after), rowState(t, odd))
	}
	r.silent(t, "иной отказ, повтор")

	// 6. Обрыв связи во время вызова: строка остаётся взятой и больше не отправляется.
	// Настоящий обрыв: сервер закрывает соединение, не ответив.
	r.reset(t)
	var hits atomic.Int64
	drop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(drop.Close)
	lost, lost2 := put(42), put(43)
	bd := newBotWith(t, &fakeAPI{srv: drop}, r.s, Config{Token: "123:TEST"})
	bd.Now = func() time.Time { return fixtureNow }
	nd := NewNotifier(bd)
	nd.Step(ctx)
	if hits.Load() != 1 || rowState(t, lost) != "claimed" || rowState(t, lost2) != "pending" {
		t.Fatalf("шаг 6: обращений %d, строки %s и %s; want 1, claimed и pending", hits.Load(), rowState(t, lost), rowState(t, lost2))
	}
	nd.Step(ctx) // следующая строка — новое сообщение; оборванная не берётся
	bd.Now = func() time.Time { return fixtureNow.Add(48 * time.Hour) }
	nd.Step(ctx)
	if hits.Load() != 2 || rowState(t, lost) != "claimed" || rowState(t, lost2) != "claimed" {
		t.Errorf("шаг 6: обращений %d, строки %s и %s; want 2, claimed и claimed — повторов нет", hits.Load(), rowState(t, lost), rowState(t, lost2))
	}
	if !strings.Contains(logs.String(), "outcome=unknown") {
		t.Errorf("шаг 6: в логе нет исхода unknown:\n%s", logs)
	}

	// 7. Сбой до вызова: база не отвечает на перечитывание состояния после взятия.
	// Вызовов Bot API нет, отметка взятия снята, минуту шаги ничего не берут,
	// затем та же строка уходит ровно один раз.
	r.reset(t)
	id = put(42)
	breakFavorites(t)
	if calls := r.step(); len(calls) != 0 || rowState(t, id) != "pending" {
		t.Fatalf("шаг 7: вызовы %q, строка %s; want ни одного и pending — отметка снята", methods(calls), rowState(t, id))
	}
	if out := logs.String(); !strings.Contains(out, "msg=notify kind=change outcome=released") {
		t.Errorf("шаг 7: в логе нет вида сообщения и исхода:\n%s", out)
	}
	repairFavorites(t)
	r.at(fixtureNow.Add(59 * time.Second))
	r.silent(t, "шаг 7, +59 с")
	if rowState(t, id) != "pending" {
		t.Errorf("шаг 7: за минуту паузы строка %s, want pending", rowState(t, id))
	}
	r.at(fixtureNow.Add(time.Minute))
	if c := r.sent(t, "шаг 7, +60 с"); c.Form["chat_id"] != "42" || rowState(t, id) != "sent" {
		t.Errorf("шаг 7: строка %s", rowState(t, id))
	}
	r.silent(t, "шаг 7, повтор")

	// Граница: сбой до вызова, и снять отметку тоже не удалось (база недоступна) —
	// строка остаётся взятой, сообщение не уходит никогда; в логе — исход.
	r.reset(t)
	id = put(42)
	dbExec(t, `ALTER TABLE outbox ADD CONSTRAINT no_release CHECK (claimed_at IS NOT NULL OR done_at IS NOT NULL) NOT VALID`)
	t.Cleanup(func() { dbExec(t, `ALTER TABLE outbox DROP CONSTRAINT IF EXISTS no_release`) })
	breakFavorites(t)
	r.silent(t, "граница, сбой")
	repairFavorites(t)
	if rowState(t, id) != "claimed" || !strings.Contains(logs.String(), "outcome=release_failed") {
		t.Errorf("граница: строка %s, в логе release_failed: %v", rowState(t, id), strings.Contains(logs.String(), "outcome=release_failed"))
	}
	r.at(fixtureNow.Add(time.Hour))
	r.silent(t, "граница, позже")
	dbExec(t, `ALTER TABLE outbox DROP CONSTRAINT no_release`)

	// 8. «Перезапуск»: строка взята и не отмечена; новый экземпляр бота над той же
	// базой её не берёт — ни сразу, ни через сутки.
	r.reset(t)
	id = put(42)
	dbExec(t, `UPDATE outbox SET claimed_at = $2 WHERE id = $1`, id, fixtureNow)
	nb := newTestBot(t, r.api, r.s)
	nn := NewNotifier(nb)
	for _, shift := range []time.Duration{0, time.Hour, 24 * time.Hour} {
		at := fixtureNow.Add(shift)
		nb.Now = func() time.Time { return at }
		r.api.reset()
		nn.Step(ctx)
		if r.api.total() != 0 {
			t.Fatalf("шаг 8, +%v: вызовов %d, want 0", shift, r.api.total())
		}
	}
	if rowState(t, id) != "claimed" {
		t.Errorf("шаг 8: строка %s, want claimed", rowState(t, id))
	}

	// 9. Сообщение старше срока годности (72 часа) не отправляется; ровно 72 часа — уходит.
	r.reset(t)
	old := notifyPut(t, 42, link, fixtureNow.Add(-72*time.Hour-time.Second))
	r.silent(t, "шаг 9, просрочено")
	if rowState(t, old) != "closed:expired" {
		t.Errorf("шаг 9: строка %s, want closed:expired", rowState(t, old))
	}
	edge := notifyPut(t, 42, link, fixtureNow.Add(-72*time.Hour))
	r.sent(t, "шаг 9, ровно 72 часа")
	if rowState(t, edge) != "sent" {
		t.Errorf("шаг 9: строка на границе %s, want sent", rowState(t, edge))
	}

	// Строки, которые нельзя прочитать, закрываются без вызова и без повтора;
	// следующая строка уходит.
	r.reset(t)
	var bad6 []int64
	for _, row := range []struct {
		kind, payload string
		ev            int64
	}{
		{"change", `{"date":{"from":"не дата","to":"2026-10-03"}}`, 52},
		{"change", `{}`, 52},
		{"change", `{"status":{"from":"scheduled","to":"неизвестный"}}`, 52},
		{"weird", `{"link":true}`, 52},
		{"change", `{"link":true}`, 0},
	} {
		if row.ev == 0 {
			bad6 = append(bad6, notifyPutRaw(t, 42, row.kind, 52, row.payload, fixtureNow))
			dbExec(t, `UPDATE outbox SET event_id = NULL WHERE id = $1`, bad6[len(bad6)-1])
			continue
		}
		bad6 = append(bad6, notifyPutRaw(t, 42, row.kind, row.ev, row.payload, fixtureNow))
	}
	good := put(43)
	for i, id := range bad6 {
		r.silent(t, fmt.Sprintf("нечитаемая строка %d", i))
		if rowState(t, id) != "closed:rejected" {
			t.Errorf("нечитаемая строка %d: %s, want closed:rejected", i, rowState(t, id))
		}
	}
	r.sent(t, "после нечитаемых")
	if rowState(t, good) != "sent" {
		t.Errorf("после нечитаемых: строка %s", rowState(t, good))
	}

	// В логе — только вид сообщения и исход: ни пользователей, ни названий.
	out := logs.String()
	for _, leak := range []string{"Шарташ", "Карасики", "chat_id", "tg_id", "event_id", "Ссылка", "=42", "=43", "=52"} {
		if strings.Contains(out, leak) {
			t.Errorf("в логе есть %q:\n%s", leak, out)
		}
	}
}

// breakFavorites делает таблицу избранного недоступной (перечитывание состояния
// после взятия строки падает, взятие работает); repairFavorites возвращает имя.
// Cleanup возвращает имя и при падении теста.
func breakFavorites(t *testing.T) {
	t.Helper()
	dbExec(t, `ALTER TABLE favorites RENAME TO favorites_broken`)
	t.Cleanup(func() { dbExec(t, `ALTER TABLE IF EXISTS favorites_broken RENAME TO favorites`) })
}

func repairFavorites(t *testing.T) {
	t.Helper()
	dbExec(t, `ALTER TABLE IF EXISTS favorites_broken RENAME TO favorites`)
}

// TestS53GoneMessage — S-53: старт пропал из календаря.
func TestS53GoneMessage(t *testing.T) {
	r := newNotifyRig(t)
	logs := captureLogs(t)
	gone := event.Change{EventID: 52, Gone: true}
	r.reset(t)
	dbExec(t, `DELETE FROM favorites WHERE tg_id = 43`)
	storetest.HideEvent(t, 52)
	statusOf := func() string { return dbScalar(t, `SELECT status FROM events WHERE id = 52`) }

	// 1. Одно сообщение N-4 с одной кнопкой «Убрать из избранного».
	id := notifyPut(t, 42, gone, fixtureNow)
	msg := r.sent(t, "шаг 1")
	matchSnapshot(t, "s53_gone", renderCall(t, msg))
	plain(t, msg)
	if ds := datas(t, msg); strings.Join(ds, " ") != "w:x:52" || rowState(t, id) != "sent" {
		t.Errorf("шаг 1: кнопки %v, строка %s; want одна w:x:52 и sent", ds, rowState(t, id))
	}
	for _, bad := range []string{"[ОТМЕНЁН]", "отменён.", "из-за", "причин"} {
		if strings.Contains(msg.Form["text"], bad) {
			t.Errorf("шаг 1: в тексте %q утверждение %q", msg.Form["text"], bad)
		}
	}

	// 2. Следующие шаги — повтора нет.
	r.silent(t, "шаг 2")
	r.silent(t, "шаг 2, ещё")

	// 3. «Убрать из избранного» в сообщении: запись удалена, кнопка меняется.
	favKeys(t, r.b, r.api, 42, &msg, "w:x:52", "s53_removed")
	if favCount(t, "tg_id = 42") != "0" {
		t.Error("шаг 3: запись избранного не удалена")
	}
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 52, $1)`, fixtureNow)

	// 4. Старт вернулся в выдачу до отправки: вызова нет, строка закрыта.
	id = notifyPut(t, 42, gone, fixtureNow)
	dbExec(t, `UPDATE events SET is_listed = true WHERE id = 52`)
	r.silent(t, "шаг 4")
	if rowState(t, id) != "closed:stale" {
		t.Errorf("шаг 4: строка %s, want closed:stale", rowState(t, id))
	}
	storetest.HideEvent(t, 52)

	// 5. Старт убран из избранного либо его дата прошла до отправки: вызова нет.
	id = notifyPut(t, 42, gone, fixtureNow)
	dbExec(t, `DELETE FROM favorites WHERE tg_id = 42`)
	r.silent(t, "шаг 5, убран")
	if rowState(t, id) != "closed:unfollowed" {
		t.Errorf("шаг 5: строка %s, want closed:unfollowed", rowState(t, id))
	}
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 52, $1)`, fixtureNow)
	oct4 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	id = notifyPut(t, 42, gone, oct4)
	r.at(oct4)
	r.silent(t, "шаг 5, прошёл")
	r.at(fixtureNow)
	if rowState(t, id) != "closed:stale" {
		t.Errorf("шаг 5: строка %s, want closed:stale", rowState(t, id))
	}

	// 6. Событие 110 слито с 52, в избранном пользователя 43 запись на 110: сообщение о корне.
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (43, 110, $1)`, fixtureNow)
	storetest.MergeEvent(t, 110, 52)
	notifyPut(t, 43, event.Change{EventID: 52, Gone: true}, fixtureNow)
	merged := r.sent(t, "шаг 6")
	matchSnapshot(t, "s53_gone", renderCall(t, merged))
	if merged.Form["chat_id"] != "43" {
		t.Errorf("шаг 6: получатель %s, want 43", merged.Form["chat_id"])
	}

	// 7. Отправка в 23:30 по Москве — без звука; днём — со звуком.
	r.at(time.Date(2026, 9, 29, 23, 30, 0, 0, r.b.loc))
	notifyPut(t, 42, gone, fixtureNow)
	if c := r.sent(t, "шаг 7"); c.Form["disable_notification"] != "true" {
		t.Errorf("шаг 7: в 23:30 без звука = %q, want true", c.Form["disable_notification"])
	}
	r.at(fixtureNow)
	notifyPut(t, 42, gone, fixtureNow)
	if c := r.sent(t, "шаг 7, днём"); c.Form["disable_notification"] != "" {
		t.Errorf("шаг 7: днём без звука = %q, want пусто", c.Form["disable_notification"])
	}
	if got := statusOf(); got != "scheduled" {
		t.Errorf("статус события после сообщений %q, want scheduled", got)
	}

	// 8. Старт вернулся в выдачу после сообщения: новых сообщений нет, раздел его показывает.
	dbExec(t, `UPDATE events SET is_listed = true WHERE id = 52`)
	r.silent(t, "шаг 8")
	favSnap(t, r.b, r.api, 42, nil, "w:o", "s53_section_returned")
	if got := statusOf(); got != "scheduled" {
		t.Errorf("статус события %q, want scheduled", got)
	}

	// В логе — только вид и исход.
	out := logs.String()
	for _, leak := range []string{"Шарташ", "Карасики", "пропал", "=42", "=43", "=52", "tg_id", "event_id"} {
		if strings.Contains(out, leak) {
			t.Errorf("в логе есть %q:\n%s", leak, out)
		}
	}
}

// TestNotifyChores — служебная часть шага: срок годности и очистка, не чаще
// раза в минуту; ошибка служебной части отправку не останавливает.
func TestNotifyChores(t *testing.T) {
	r := newNotifyRig(t)
	logs := captureLogs(t)
	link := event.Change{EventID: 52, Link: true}
	old := fixtureNow.Add(-73 * time.Hour)
	longAgo := fixtureNow.Add(-31 * 24 * time.Hour)
	mk := func(state string, created time.Time) int64 {
		id := notifyPut(t, 42, link, created)
		switch state {
		case "sent":
			dbExec(t, `UPDATE outbox SET claimed_at = $2, sent_at = $2 WHERE id = $1`, id, created)
		case "closed":
			dbExec(t, `UPDATE outbox SET claimed_at = $2, done_at = $2, outcome = 'rejected' WHERE id = $1`, id, created)
		case "unknown":
			dbExec(t, `UPDATE outbox SET claimed_at = $2 WHERE id = $1`, id, created)
		}
		return id
	}

	// Служебная минута: просроченные ожидающие строки закрыты, старые удалены,
	// свежие не тронуты; свежая ожидающая строка уходит тем же шагом.
	expiredChange := notifyPut(t, 42, link, old)
	expiredGone := notifyPut(t, 43, event.Change{EventID: 52, Gone: true}, old)
	oldRows := []int64{mk("sent", longAgo), mk("closed", longAgo), mk("unknown", longAgo)}
	freshSent, freshUnknown := mk("sent", fixtureNow.Add(-time.Hour)), mk("unknown", fixtureNow.Add(-time.Hour))
	fresh := notifyPut(t, 42, link, fixtureNow.Add(-time.Hour))
	if c := r.sent(t, "служебная минута"); c.Form["chat_id"] != "42" {
		t.Errorf("служебная минута: получатель %s, want 42", c.Form["chat_id"])
	}
	if a, b := rowState(t, expiredChange), rowState(t, expiredGone); a != "closed:expired" || b != "closed:expired" {
		t.Errorf("просроченные: %s и %s, want closed:expired", a, b)
	}
	for i, id := range oldRows {
		if rowState(t, id) != "нет" {
			t.Errorf("старая строка %d: %s, want удалена", i, rowState(t, id))
		}
	}
	if a, b, c := rowState(t, freshSent), rowState(t, freshUnknown), rowState(t, fresh); a != "sent" || b != "claimed" || c != "sent" {
		t.Errorf("свежие строки: %s, %s, %s; want sent, claimed, sent", a, b, c)
	}
	if want := "msg=\"notify chores\" expired=2 pruned=3"; !strings.Contains(logs.String(), want) {
		t.Errorf("в логе нет счётчиков %q:\n%s", want, logs)
	}
	if out := logs.String(); regexp.MustCompile(`chores.*(Шарташ|=42|=43|=52)`).MatchString(out) {
		t.Errorf("в логе служебной части не только счётчики:\n%s", out)
	}

	// В пределах минуты служебная часть не повторяется; через минуту — снова.
	late := mk("sent", longAgo)
	r.at(fixtureNow.Add(30 * time.Second))
	r.silent(t, "+30 с")
	if rowState(t, late) != "sent" {
		t.Errorf("+30 с: старая строка %s, want sent — служебная часть выполнилась раньше минуты", rowState(t, late))
	}
	r.at(fixtureNow.Add(time.Minute))
	r.silent(t, "+60 с")
	if rowState(t, late) != "нет" {
		t.Errorf("+60 с: старая строка %s, want удалена", rowState(t, late))
	}

	// Ошибка служебной части: пишется в лог, отправка идёт. Удаление старой
	// строки ломает внешний ключ тестовой таблицы.
	r.reset(t)
	logs.Reset()
	pinned := mk("sent", longAgo)
	dbExec(t, `CREATE TABLE chores_pin (outbox_id bigint REFERENCES outbox(id))`)
	t.Cleanup(func() { dbExec(t, `DROP TABLE IF EXISTS chores_pin`) })
	dbExec(t, `INSERT INTO chores_pin VALUES ($1)`, pinned)
	next := notifyPut(t, 42, link, fixtureNow)
	r.at(fixtureNow.Add(2 * time.Hour))
	if c := r.sent(t, "ошибка служебной части"); c.Form["chat_id"] != "42" || rowState(t, next) != "sent" {
		t.Errorf("ошибка служебной части: строка %s", rowState(t, next))
	}
	if !strings.Contains(logs.String(), "notify chores failed") || rowState(t, pinned) != "sent" {
		t.Errorf("ошибка служебной части: лог %q, строка %s", logs, rowState(t, pinned))
	}
}

// mustDate — дата ГГГГ-ММ-ДД как полночь UTC.
func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Напоминание за семь дней и сообщение администратору о предохранителе
// (14-UX-CONTRACT.md, сценарии S-50 и S-52). Эталоны testdata/screens/s50_* и
// s52_* написаны вручную по контракту; главного меню в них нет. Время
// подставное (Bot.Now).

// msk — момент по Москве: сентябрь и октябрь 2026, если месяц не назван.
func (r *notifyRig) msk(mon time.Month, day, hour, min int) time.Time {
	return time.Date(2026, mon, day, hour, min, 0, 0, r.b.loc)
}

// remindStart — исходное состояние сценария S-50: старт 110 (2 октября 2026) в
// избранном пользователя 42 с 24 сентября, очередь пуста.
func (r *notifyRig) remindStart(t *testing.T) {
	t.Helper()
	r.reset(t)
	dbExec(t, `DELETE FROM favorites`)
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 110, $1)`, time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
}

// reminders — число строк очереди вида reminder.
func reminders(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT count(*)::text FROM outbox WHERE kind = 'reminder'`)
}

// lastRow — id последней строки очереди вида kind.
func lastRow(t *testing.T, kind string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscan(dbScalar(t, `SELECT COALESCE(max(id), 0)::text FROM outbox WHERE kind = $1`, kind), &id); err != nil || id == 0 {
		t.Fatalf("строки вида %s нет: %v", kind, err)
	}
	return id
}

// TestRemindTracer — сквозной путь напоминания: день «за семь дней», 10:00 по
// Москве → служебная часть шага создаёт строку → тот же шаг отправляет N-2 с
// двумя кнопками → строка отмечена отправленной; следующие шаги ничего не
// создают и не шлют.
func TestRemindTracer(t *testing.T) {
	r := newNotifyRig(t)
	r.remindStart(t)
	r.at(r.msk(time.September, 25, 10, 0))
	c := r.sent(t, "10:00")
	if got := datas(t, c); strings.Join(got, " ") != "w:n:110 w:x:110" || c.Form["chat_id"] != "42" {
		t.Errorf("сообщение: кнопки %v, получатель %s; want w:n:110 w:x:110 и 42", got, c.Form["chat_id"])
	}
	if reminders(t) != "1" || rowState(t, lastRow(t, "reminder")) != "sent" {
		t.Errorf("строк напоминания %s, состояние %s; want одна и sent", reminders(t), rowState(t, lastRow(t, "reminder")))
	}
	again := 0
	for _, at := range []time.Time{r.msk(time.September, 25, 10, 1), r.msk(time.September, 25, 11, 0)} {
		r.at(at)
		again += len(r.step())
	}
	if reminders(t) != "1" || again != 0 {
		t.Errorf("повторные шаги: строк %s, вызовов %d; want 1 и 0", reminders(t), again)
	}
	if !t.Failed() {
		t.Logf("TRACER remind=sent calls=1 again=%d", again)
	}
}

// TestS50Reminder — S-50: напоминание за семь дней.
func TestS50Reminder(t *testing.T) {
	r := newNotifyRig(t)
	ctx := context.Background()
	sep := func(day, h, m int) time.Time { return r.msk(time.September, day, h, m) }
	title := dbScalar(t, `SELECT title FROM events WHERE id = 110`)
	restore := func() {
		dbExec(t, `UPDATE events SET status = 'scheduled', is_listed = true, title = $1, start_date = '2026-10-02' WHERE id = 110`, title)
	}
	t.Cleanup(restore)

	// 1. 09:59 — строк и вызовов нет.
	r.remindStart(t)
	r.at(sep(25, 9, 59))
	r.silent(t, "шаг 1")
	if reminders(t) != "0" {
		t.Errorf("шаг 1: строк напоминания %s, want 0", reminders(t))
	}

	// 2. 10:00 — одна строка и одно напоминание с двумя кнопками.
	r.at(sep(25, 10, 0))
	msg := r.sent(t, "шаг 2")
	matchSnapshot(t, "s50_reminder", renderCall(t, msg))
	plain(t, msg)
	checkCallbackLengths(t, msg)
	if ds := datas(t, msg); strings.Join(ds, " ") != "w:n:110 w:x:110" || msg.Form["chat_id"] != "42" || msg.Form["disable_notification"] != "" {
		t.Errorf("шаг 2: кнопки %v, получатель %s, без звука %q", ds, msg.Form["chat_id"], msg.Form["disable_notification"])
	}
	if reminders(t) != "1" || rowState(t, lastRow(t, "reminder")) != "sent" {
		t.Errorf("шаг 2: строк %s, состояние %s", reminders(t), rowState(t, lastRow(t, "reminder")))
	}

	// 4. Пользователь 43 добавил старт в 10:01 — напоминания нет.
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (43, 110, $1)`, sep(25, 10, 1))
	r.at(sep(25, 10, 2))
	r.silent(t, "шаг 4")

	// 3. Следующие шаги в тот же день и на следующий — повтора нет.
	for _, at := range []time.Time{sep(25, 10, 3), sep(25, 12, 0), sep(25, 23, 59), sep(26, 10, 0), sep(26, 11, 0)} {
		r.at(at)
		r.silent(t, "шаг 3, "+at.Format("02.01 15:04"))
	}
	if reminders(t) != "1" {
		t.Errorf("шаги 3 и 4: строк напоминания %s, want 1", reminders(t))
	}

	// 5. Старт с датой-заглушкой: кандидатом запрос его вернёт, строку не создаёт и
	// напоминание не уходит — отсев делает event.DateTBD.
	r.remindStart(t)
	dbExec(t, `DELETE FROM favorites`)
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 136, $1)`, time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC))
	dec := time.Date(2026, 12, 25, 10, 0, 0, 0, r.b.loc)
	r.at(dec)
	cands, err := r.s.ReminderCandidates(ctx, r.n.dayOf(dec), remindDays, dec)
	if err != nil || len(cands) != 1 || cands[0].EventID != 136 {
		t.Fatalf("шаг 5: кандидаты %+v, %v; want один — старт 136 (отсев заглушек не дело запроса)", cands, err)
	}
	r.silent(t, "шаг 5")
	if reminders(t) != "0" {
		t.Errorf("шаг 5: строк напоминания %s, want 0", reminders(t))
	}
	// Название с пометкой при точной дате — тот же признак (потолок Q-3).
	r.remindStart(t)
	dbExec(t, `UPDATE events SET title = title || ' (дата уточняется)' WHERE id = 110`)
	r.at(sep(25, 10, 0))
	r.silent(t, "шаг 5, пометка в названии")
	if reminders(t) != "0" {
		t.Errorf("шаг 5, пометка в названии: строк %s, want 0", reminders(t))
	}
	restore()

	// 6. Отменённый и перенесённый старт (Q-11) — напоминания нет.
	for _, st := range []event.Status{event.StatusCancelled, event.StatusPostponed} {
		r.remindStart(t)
		storetest.SetStatus(t, 110, st)
		r.at(sep(25, 10, 0))
		r.silent(t, "шаг 6, "+string(st))
		if reminders(t) != "0" {
			t.Errorf("шаг 6, %s: строк напоминания %s, want 0", st, reminders(t))
		}
	}
	restore()

	// Скрытый старт и выключенный вид спорта — напоминания нет.
	r.remindStart(t)
	storetest.HideEvent(t, 110)
	r.at(sep(25, 10, 0))
	r.silent(t, "скрытый старт")
	restore()
	r.remindStart(t)
	dbExec(t, `DELETE FROM favorites`)
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 136, $1)`, time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC))
	dbExec(t, `UPDATE events SET title = 'Лыжный старт' WHERE id = 136`)
	storetest.DisableSport(t, event.SportSkiing)
	r.at(dec)
	r.silent(t, "выключенный вид спорта")
	storetest.EnableSport(t, event.SportSkiing)
	if reminders(t) != "0" {
		t.Errorf("скрытый старт и выключенный вид спорта: строк %s, want 0", reminders(t))
	}

	// 7. Дата изменена после напоминания — за семь дней до новой придёт второе.
	r.remindStart(t)
	r.at(sep(25, 10, 0))
	r.sent(t, "шаг 7, первое")
	dbExec(t, `UPDATE events SET start_date = '2026-10-09' WHERE id = 110`)
	r.at(r.msk(time.October, 2, 10, 0))
	second := r.sent(t, "шаг 7, второе")
	matchSnapshot(t, "s50_reminder_new_date", renderCall(t, second))
	if reminders(t) != "2" {
		t.Errorf("шаг 7: строк напоминания %s, want 2", reminders(t))
	}
	r.silent(t, "шаг 7, повтор")
	restore()

	// 8. Бот заработал в 15:00 — напоминание уходит в 15:00; заработал на следующий
	// день — напоминания нет.
	r.remindStart(t)
	r.at(sep(25, 15, 0))
	late := r.sent(t, "шаг 8, 15:00")
	matchSnapshot(t, "s50_reminder", renderCall(t, late))
	r.remindStart(t)
	r.at(sep(26, 10, 0))
	r.silent(t, "шаг 8, на следующий день")
	if reminders(t) != "0" {
		t.Errorf("шаг 8: строк напоминания %s, want 0", reminders(t))
	}

	// 9. Старт убран до 10:00 — строки нет; убран между созданием строки и
	// отправкой — вызова нет, строка закрыта.
	r.remindStart(t)
	dbExec(t, `DELETE FROM favorites`)
	r.at(sep(25, 10, 0))
	r.silent(t, "шаг 9, убран до 10:00")
	if reminders(t) != "0" {
		t.Errorf("шаг 9: строк напоминания %s, want 0", reminders(t))
	}
	for _, tc := range []struct {
		name, outcome string
		change        func()
	}{
		{"убран", "closed:unfollowed", func() { dbExec(t, `DELETE FROM favorites`) }},
		{"скрыт", "closed:unavailable", func() { storetest.HideEvent(t, 110) }},
		{"дата изменена", "closed:stale", func() { dbExec(t, `UPDATE events SET start_date = '2026-10-10' WHERE id = 110`) }},
		{"отменён", "closed:stale", func() { storetest.SetStatus(t, 110, event.StatusCancelled) }},
		{"заглушка", "closed:stale", func() {
			dbExec(t, `UPDATE events SET title = title || ' (дата уточняется)' WHERE id = 110`)
		}},
	} {
		r.remindStart(t)
		now := sep(25, 10, 0)
		r.at(now)
		r.n.chores(ctx, now) // строка создана, но не отправлена
		if reminders(t) != "1" || rowState(t, lastRow(t, "reminder")) != "pending" {
			t.Fatalf("шаг 9, %s: строк %s, состояние %s; want одна pending", tc.name, reminders(t), rowState(t, lastRow(t, "reminder")))
		}
		tc.change()
		r.silent(t, "шаг 9, "+tc.name)
		if got := rowState(t, lastRow(t, "reminder")); got != tc.outcome {
			t.Errorf("шаг 9, %s: строка %s, want %s", tc.name, got, tc.outcome)
		}
		restore()
	}

	// Строка создана в 23:59 и не ушла до полуночи: наутро вызова нет, строка закрыта —
	// до старта уже шесть дней.
	r.remindStart(t)
	r.n.chores(ctx, sep(25, 23, 59))
	r.at(sep(26, 0, 1))
	r.silent(t, "после полуночи")
	if got := rowState(t, lastRow(t, "reminder")); got != "closed:stale" {
		t.Errorf("после полуночи: строка %s, want closed:stale", got)
	}

	// Напоминание после 23:00 уходит без звука.
	r.remindStart(t)
	r.at(sep(25, 23, 30))
	if c := r.sent(t, "23:30"); c.Form["disable_notification"] != "true" {
		t.Errorf("23:30: без звука = %q, want true", c.Form["disable_notification"])
	}

	// Ответ 429: строка не потеряна, после срока уходит один раз.
	r.remindStart(t)
	limited := false
	r.reply = func(_ call, _ int) string {
		if !limited {
			limited = true
			return apiErr(429, "Too Many Requests: retry after 30", 30)
		}
		return ""
	}
	r.at(sep(25, 10, 0))
	if calls := r.step(); len(calls) != 1 || rowState(t, lastRow(t, "reminder")) != "pending" {
		t.Fatalf("429: вызовы %q, строка %s; want один вызов и pending", methods(calls), rowState(t, lastRow(t, "reminder")))
	}
	r.at(sep(25, 10, 0).Add(29 * time.Second))
	r.silent(t, "429, до срока")
	r.at(sep(25, 10, 0).Add(30 * time.Second))
	r.sent(t, "429, после срока")
	r.silent(t, "429, повтор")
	if rowState(t, lastRow(t, "reminder")) != "sent" || reminders(t) != "1" {
		t.Errorf("429: строка %s, строк %s; want sent и 1", rowState(t, lastRow(t, "reminder")), reminders(t))
	}
}

// TestS52WatchFuse — S-52: сработавший предохранитель сообщает администраторам
// и ничего не шлёт пользователям.
func TestS52WatchFuse(t *testing.T) {
	r := newNotifyRig(t)
	if got := dbScalar(t, `SELECT r.id || ' ' || s.code FROM ingest_runs r JOIN sources s ON s.id = r.source_id ORDER BY r.id LIMIT 1`); got != "1 marathonec" {
		t.Fatalf("первый прогон каталога %q, want «1 marathonec»", got)
	}
	delete(r.b.admins, 1) // администратор тестового бота: здесь свои
	r.b.admins[7001], r.b.admins[7002] = true, true
	dbExec(t, `UPDATE ingest_runs SET finished_at = $1, metrics = metrics || '{"watch_held": 23}'::jsonb WHERE id = 1`, fixtureNow.Add(-time.Hour))
	fuseRows := func() string {
		return dbScalar(t, `SELECT count(*)::text FROM outbox WHERE kind = 'fuse'`)
	}

	// 1. Каждому из двух администраторов — одно сообщение обычным текстом, по
	// одному за шаг; строк об изменениях нет. Ответ 403 от первого не мешает второму.
	r.reset(t)
	r.reply = func(c call, _ int) string {
		if c.Form["chat_id"] == "7001" {
			return errBlocked
		}
		return ""
	}
	first := r.step()
	if len(first) != 1 || first[0].Form["chat_id"] != "7001" {
		t.Fatalf("шаг 1: вызовы %q, want один — первому администратору", methods(first))
	}
	var blockedRow int64
	if _, err := fmt.Sscan(dbScalar(t, `SELECT id::text FROM outbox WHERE kind = 'fuse' AND tg_id = 7001`), &blockedRow); err != nil {
		t.Fatal(err)
	}
	if got := rowState(t, blockedRow); got != "closed:blocked" {
		t.Errorf("шаг 1: строка первого администратора %s, want closed:blocked", got)
	}
	r.reply = nil
	second := r.sent(t, "шаг 1, второй администратор")
	if second.Form["chat_id"] != "7002" {
		t.Errorf("шаг 1: получатель %s, want 7002", second.Form["chat_id"])
	}
	matchSnapshot(t, "s52_fuse", renderCall(t, second))
	plain(t, second)
	if second.Form["parse_mode"] != "" || second.Form["reply_markup"] != "" || second.Form["disable_notification"] != "" {
		t.Errorf("шаг 1: форма %v; want обычный текст без клавиатуры и со звуком", second.Form)
	}
	for _, bad := range []string{"7001", "7002", "администратор"} {
		if strings.Contains(second.Form["text"], bad) {
			t.Errorf("шаг 1: в тексте %q получатель %q", second.Form["text"], bad)
		}
	}
	// Без блокировки оба администратора получают одно и то же сообщение, по одному за шаг.
	dbExec(t, `DELETE FROM outbox`)
	r.n = NewNotifier(r.b)
	a, b := r.sent(t, "шаг 1, первый"), r.sent(t, "шаг 1, второй")
	if a.Form["chat_id"] != "7001" || b.Form["chat_id"] != "7002" || a.Form["text"] != b.Form["text"] {
		t.Errorf("шаг 1: получатели %s и %s; want 7001 и 7002 с одним текстом", a.Form["chat_id"], b.Form["chat_id"])
	}
	matchSnapshot(t, "s52_fuse", renderCall(t, a))

	// 2. Следующие шаги и служебные минуты — повтора нет. 3. Пользователям — ничего;
	// строк об изменениях и о пропавших стартах от прогона нет.
	for i := 1; i <= 3; i++ {
		r.at(fixtureNow.Add(time.Duration(i) * 2 * time.Minute))
		r.silent(t, fmt.Sprintf("шаг 2, +%d мин", i*2))
	}
	if fuseRows() != "2" || dbScalar(t, `SELECT count(*)::text FROM outbox WHERE kind <> 'fuse'`) != "0" {
		t.Errorf("шаг 3: строк fuse %s, прочих %s; want 2 и 0", fuseRows(), dbScalar(t, `SELECT count(*)::text FROM outbox WHERE kind <> 'fuse'`))
	}
	if dbScalar(t, `SELECT count(*)::text FROM outbox WHERE kind = 'fuse' AND tg_id IN (42, 43)`) != "0" {
		t.Error("шаг 3: сообщение о предохранителе создано пользователю")
	}

	// 4. Администраторов в конфигурации нет — строк и сообщений нет.
	r.reset(t)
	delete(r.b.admins, 7001)
	delete(r.b.admins, 7002)
	r.silent(t, "шаг 4")
	if fuseRows() != "0" {
		t.Errorf("шаг 4: строк fuse %s, want 0", fuseRows())
	}
	r.b.admins[7001], r.b.admins[7002] = true, true

	// 5. Прогон, завершённый больше 24 часов назад, — сообщений нет; ровно 24 часа — есть.
	r.reset(t)
	dbExec(t, `UPDATE ingest_runs SET finished_at = $1 WHERE id = 1`, fixtureNow.Add(-24*time.Hour-time.Second))
	r.silent(t, "шаг 5")
	if fuseRows() != "0" {
		t.Errorf("шаг 5: строк fuse %s, want 0", fuseRows())
	}
	dbExec(t, `UPDATE ingest_runs SET finished_at = $1 WHERE id = 1`, fixtureNow.Add(-24*time.Hour))
	r.n = NewNotifier(r.b)
	r.sent(t, "шаг 5, ровно 24 часа")

	// Нечитаемое содержимое закрывает строку без вызова.
	r.reset(t)
	bad := notifyPutRaw(t, 7001, "fuse", 52, `{"source":"marathonec"}`, fixtureNow)
	dbExec(t, `UPDATE outbox SET event_id = NULL WHERE id = $1`, bad)
	dbExec(t, `UPDATE ingest_runs SET finished_at = $1 WHERE id = 1`, fixtureNow.Add(-48*time.Hour))
	r.silent(t, "нечитаемая строка")
	if got := rowState(t, bad); got != "closed:rejected" {
		t.Errorf("нечитаемая строка: %s, want closed:rejected", got)
	}
}

// TestNotifyPrivacy — в логе отправки и служебной части только вид сообщения,
// исход и счётчики (FAV-11): после сообщений четырёх видов, ответа 403, закрытия
// без отправки, сбоя до вызова Bot API и шага служебной части в логе нет
// названий стартов, идентификаторов событий и пользователей и текста сообщений.
func TestNotifyPrivacy(t *testing.T) {
	r := newNotifyRig(t)
	logs := captureLogs(t)
	const user, blocked, stranger, admin = 424242, 535353, 616161, 646464
	delete(r.b.admins, 1) // администратор тестового бота: здесь свой
	r.b.admins[admin] = true
	r.reset(t)
	dbExec(t, `DELETE FROM favorites`)
	created := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for _, f := range [][2]int64{{user, 52}, {user, 110}, {user, 135}, {blocked, 52}} {
		dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES ($1, $2, $3)`, f[0], f[1], created)
	}
	dbExec(t, `UPDATE ingest_runs SET finished_at = $1, metrics = metrics || '{"watch_held": 23}'::jsonb WHERE id = 1`, r.msk(time.September, 25, 9, 0))
	storetest.HideEvent(t, 135)
	at := r.msk(time.September, 25, 11, 0)
	r.at(at)

	// Сообщения четырёх видов: об изменении, о пропавшем старте, напоминание, о предохранителе.
	notifyPut(t, user, event.Change{EventID: 52, Link: true}, at)
	notifyPut(t, user, event.Change{EventID: 135, Gone: true}, at)
	var texts []string
	for range 8 {
		for _, c := range r.step() {
			texts = append(texts, c.Form["text"])
		}
	}
	if len(texts) != 4 {
		t.Fatalf("отправлено сообщений %d, want 4 (изменение, пропажа, напоминание, предохранитель): %q", len(texts), texts)
	}

	// 403, закрытие без отправки, сбой до вызова.
	r.reply = func(c call, _ int) string {
		if c.Form["chat_id"] == "535353" {
			return errBlocked
		}
		return ""
	}
	notifyPut(t, blocked, event.Change{EventID: 52, Link: true}, at)
	r.step()
	notifyPut(t, stranger, event.Change{EventID: 52, Link: true}, at)
	r.step()
	notifyPut(t, user, event.Change{EventID: 52, Link: true}, at)
	breakFavorites(t)
	r.at(at.Add(2 * time.Minute)) // и служебная часть с ошибкой
	r.step()
	repairFavorites(t)

	// Служебная часть с просроченной строкой: счётчики.
	r.reply = nil
	notifyPut(t, user, event.Change{EventID: 52, Link: true}, at.Add(-73*time.Hour))
	r.at(at.Add(10 * time.Minute))
	r.step()

	out := logs.String()
	for _, want := range []string{
		"msg=notify kind=change outcome=sent", "msg=notify kind=gone outcome=sent", "msg=notify kind=reminder outcome=sent",
		"msg=notify kind=fuse outcome=sent", "outcome=blocked", "outcome=unfollowed", "outcome=released",
		`msg="notify chores" expired=0 pruned=0 reminders=1 fuse=1`, "msg=\"notify chores failed\" steps=remind", "expired=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в логе нет %q:\n%s", want, out)
		}
	}
	// Только известные ключи: вид, исход, счётчики, названия шагов.
	keyRE := regexp.MustCompile(`([A-Za-z_]+)=`)
	allowed := map[string]bool{"time": true, "level": true, "msg": true, "kind": true, "outcome": true, "closed": true,
		"cause": true, "expired": true, "pruned": true, "reminders": true, "fuse": true, "reports": true, "days": true, "digests": true, "steps": true,
		"method": true} // method — запись самого вызова Bot API о 403: только имя метода
	for _, m := range keyRE.FindAllStringSubmatch(out, -1) {
		if !allowed[m[1]] {
			t.Errorf("в логе ключ %q:\n%s", m[1], out)
		}
	}
	// Ни названий, ни идентификаторов, ни текста сообщений.
	leaks := []string{"424242", "535353", "616161", "646464", "tg_id", "event_id", "chat_id", "w:n:", "w:x:", "marathonec", "Изменения", "Через 7", "пропал", "порог"}
	for _, id := range []int{52, 110, 135} {
		leaks = append(leaks, dbScalar(t, `SELECT title FROM events WHERE id = $1`, id), fmt.Sprintf("=%d", id), fmt.Sprintf("event=%d", id))
	}
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			if len([]rune(line)) >= 8 {
				leaks = append(leaks, line)
			}
		}
	}
	for _, leak := range leaks {
		if strings.Contains(out, leak) {
			t.Errorf("в логе есть %q:\n%s", leak, out)
		}
	}
}

// TestNotifyStop — цикл завершается после отмены контекста; шаг, начатый до
// отмены, доводит строку до отметки отправки (вызов Bot API удерживается
// каналом, а не ожиданием времени); после отмены новые шаги не начинаются.
func TestNotifyStop(t *testing.T) {
	if notifyStepMax >= 20*time.Second {
		t.Errorf("предел шага %v не меньше срока остановки контейнера (20 с)", notifyStepMax)
	}
	r := newNotifyRig(t)
	r.reset(t)
	link := event.Change{EventID: 52, Link: true}
	started, next := notifyPut(t, 42, link, fixtureNow), notifyPut(t, 43, link, fixtureNow)
	entered, release := make(chan struct{}), make(chan struct{})
	r.reply = func(_ call, n int) string {
		if n == 1 {
			close(entered)
			<-release
		}
		return ""
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		r.n.Run(ctx)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("шаг не дошёл до вызова Bot API")
	}

	// Отмена приходит во время вызова: цикл ждёт, строка взята, но не отмечена.
	cancel()
	select {
	case <-done:
		t.Fatal("цикл завершился, не дождавшись начатого шага")
	default:
	}
	if got := rowState(t, started); got != "claimed" {
		t.Errorf("во время вызова строка %s, want claimed", got)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("цикл не завершился после ответа Bot API")
	}
	if a, b := rowState(t, started), rowState(t, next); a != "sent" || b != "pending" {
		t.Errorf("после остановки строки %s и %s; want sent и pending — новые шаги не начинаются", a, b)
	}
	if n := len(r.api.of("sendMessage")); n != 1 {
		t.Errorf("отправлено сообщений %d, want 1", n)
	}

	// Контекст уже отменён: цикл возвращается сразу, ни одной строки не берёт.
	r.api.reset()
	r.n.Run(ctx)
	if rowState(t, next) != "pending" || r.api.total() != 0 {
		t.Errorf("после отмены: строка %s, вызовов %d; want pending и 0", rowState(t, next), r.api.total())
	}
}
