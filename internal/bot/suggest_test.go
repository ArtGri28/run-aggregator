package bot

import (
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

// Предложение старта (16-UX-CONTRACT.md, сценарий S-55). Каталог — как у
// эталона empty_hints (countryFixture); группа задана, администратор 1. Эталоны
// s55_* написаны вручную по контракту: все вызовы шага подряд.

const suggestLink = "https://example.org/races/autumn-10k"

// sayTo — сообщение пользователя 42 (текст, команда или вложение) и все вызовы за него.
func sayTo(t *testing.T, b *Bot, api *fakeAPI, text string) []call {
	t.Helper()
	switch {
	case text == "photo":
		return feed(b, api, mediaUpdate(42, "photo"))
	case strings.HasPrefix(text, "/"):
		return feed(b, api, commandUpdate(42, text))
	}
	return feed(b, api, textUpdate(42, text))
}

// saySnap — sayTo со сверкой методов и эталона.
func saySnap(t *testing.T, b *Bot, api *fakeAPI, text, want, snap string) []call {
	t.Helper()
	calls := sayTo(t, b, api, text)
	if got := methods(calls); got != want {
		t.Fatalf("%.40q: вызовы %q, want %q", text, got, want)
	}
	if snap != "" {
		matchSnapshot(t, snap, renderCalls(t, calls))
	}
	return calls
}

// suggestions — число строк start_suggestions; awaitingSuggest — момент
// окончания отметки «жду ссылку» пользователя 42 ("none" — отметки нет).
func suggestions(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT count(*)::text FROM start_suggestions`)
}

func awaitingSuggest(t *testing.T, until time.Time) bool {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE((SELECT (expires_at = $1)::text FROM suggest_awaiting WHERE tg_id = 42), 'none')`, until) == "true"
}

func noSuggestAwaiting(t *testing.T) bool {
	t.Helper()
	return dbScalar(t, `SELECT count(*)::text FROM suggest_awaiting`) == "0"
}

// inDB — строки всех таблиц схемы, в тексте которых есть s.
func inDB(t *testing.T, s string) string {
	t.Helper()
	tables := strings.Split(dbScalar(t, `SELECT string_agg(tablename, ',') FROM pg_tables WHERE schemaname = 'public'`), ",")
	var hits []string
	for _, tb := range tables {
		if n := dbScalar(t, `SELECT count(*)::text FROM "`+tb+`" x WHERE to_jsonb(x)::text LIKE '%' || $1 || '%'`, s); n != "0" {
			hits = append(hits, tb+":"+n)
		}
	}
	return strings.Join(hits, ",")
}

// TestS55Suggest — S-55: предложить старт.
func TestS55Suggest(t *testing.T) {
	var fail atomic.Bool
	failAdmins := func(c call, _ int) string {
		if fail.Load() && c.Method == "sendMessage" && (c.Form["chat_id"] == "1" || c.Form["chat_id"] == "2") {
			return errBlocked
		}
		return ""
	}
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", event.TypeRoad, "Московский забег")
	seedCountryEvent(t, s, "RU", "2026-10-11", "Любань", "lyuban", event.TypeTrail, "Любанский трейл РФ")
	seedCountryEvent(t, s, "BY", "2026-10-12", "Любань", "lyuban", event.TypeObstacle, "Любанский трейл BY")
	seedCountryEvent(t, s, "BY", "2026-10-13", "Минск", "minsk", event.TypeRoad, "Минский забег")
	api := fakeTelegram(t, failAdmins, groupReplies())
	b := newBotWith(t, api, s, Config{Token: "123:TEST", SupportChatID: supportGroup, Admins: []int64{1}})
	group := strconv.FormatInt(supportGroup, 10)
	logs := captureLogs(t)
	window := fixtureNow.Add(awaitSuggestFor)

	// 1. Пустая выдача — E-1 со строкой «Предложить старт».
	setUserFilters(t, s, `{"date":"m202701"}`)
	_, v := filtersV(t, b)
	empty := pressSnap(t, b, api, "p:1:v"+strconv.Itoa(v), "empty_hints")

	// 2. Кнопка — P-1 новым сообщением, экран остаётся; отметка на 10 минут.
	inv := reportStep(t, b, api, &empty, empty.button(t, suggestLabel), "sendMessage answerCallbackQuery", "s55_invite_button")
	if inv[0].Form["chat_id"] != "42" || !awaitingSuggest(t, window) {
		t.Fatalf("шаг 2: чат %s, отметка не на окно", inv[0].Form["chat_id"])
	}
	invite := inv[0]

	// 3. Команда — тот же P-1.
	saySnap(t, b, api, "/suggest", "sendMessage", "s55_invite_command")

	// 4. Не ссылка, длинная ссылка, две ссылки, фото — P-3; отметка и окно прежние.
	setNow(b, time.Minute)
	long := "https://example.org/" + strings.Repeat("a", maxSuggestURL-19)
	for _, text := range []string{"марафон в Казани", long, "https://a.org https://b.org", "ftp://example.org/x", "photo"} {
		saySnap(t, b, api, text, "sendMessage", "s55_refuse")
	}
	if !awaitingSuggest(t, window) || suggestions(t) != "0" {
		t.Fatalf("шаг 4: отметка сдвинута либо есть запись (%s)", suggestions(t))
	}
	// Команда в режиме не ссылка и отметку не трогает.
	if out := sayTo(t, b, api, "/nope"); methods(out) != "sendMessage" || out[0].Form["text"] != "Нажмите /start, чтобы найти старт" || !awaitingSuggest(t, window) {
		t.Errorf("шаг 4: команда в режиме: %q %q", methods(out), out[0].Form["text"])
	}

	// 5. Ссылка — G-2 в группу и P-2; строка (42, время), ссылки в базе нет.
	setNow(b, 0)
	sent := saySnap(t, b, api, "  "+suggestLink+"\n", "sendMessage sendMessage", "s55_sent")
	if sent[0].Form["chat_id"] != group || sent[0].Form["link_preview_options"] == "" || sent[1].Form["chat_id"] != "42" {
		t.Errorf("шаг 5: получатели %s, %s; форма %v", sent[0].Form["chat_id"], sent[1].Form["chat_id"], sent[0].Form)
	}
	if got := dbScalar(t, `SELECT tg_id || ':' || (created_at = $1)::text FROM start_suggestions`, fixtureNow); got != "42:true" || !noSuggestAwaiting(t) {
		t.Errorf("шаг 5: строка %q, отметка снята: %v", got, noSuggestAwaiting(t))
	}
	if hits := inDB(t, "example.org"); hits != "" {
		t.Errorf("шаг 5: ссылка в базе: %s", hits)
	}
	// После приёма отметки нет: ссылка — обычный текст.
	if out := sayTo(t, b, api, suggestLink); len(out) != 1 || out[0].Form["text"] == reportSent || suggestions(t) != "1" {
		t.Errorf("шаг 5: ссылка без отметки: %q", methods(out))
	}

	// 6. Ещё два предложения (второе — ссылка ровно 500 символов), четвёртое — P-4.
	edge := "https://example.org/" + strings.Repeat("b", maxSuggestURL-20)
	for _, link := range []string{suggestLink + "?2", edge} {
		sayTo(t, b, api, "/suggest")
		if out := sayTo(t, b, api, link); methods(out) != "sendMessage sendMessage" || out[1].Form["text"] != reportSent {
			t.Fatalf("шаг 6: %.40q: %q", link, methods(out))
		}
	}
	sayTo(t, b, api, "/suggest")
	saySnap(t, b, api, suggestLink+"?4", "sendMessage", "s55_limit")
	if suggestions(t) != "3" || !noSuggestAwaiting(t) {
		t.Errorf("шаг 6: строк %s, отметка снята: %v", suggestions(t), noSuggestAwaiting(t))
	}

	// 7. «Отмена» — главное меню правкой, отметки нет; на старом приглашении — тоже меню.
	sayTo(t, b, api, "/suggest")
	for i := range 2 {
		out := reportStep(t, b, api, &invite, "q:c", "editMessageText answerCallbackQuery", "")
		if !strings.Contains(out[0].Form["text"], "Где старт?") || notice(t, out) != "" || !noSuggestAwaiting(t) {
			t.Errorf("шаг 7.%d: %q, уведомление %q", i, out[0].Form["text"], notice(t, out))
		}
	}

	// 8. Режимы взаимоисключающие.
	dbExec(t, `DELETE FROM start_suggestions`)
	sayTo(t, b, api, "/suggest")
	sayTo(t, b, api, "/support")
	if out := sayTo(t, b, api, "Помогите найти старт"); len(out) != 2 || !strings.Contains(out[0].Form["text"], "Обращение") || suggestions(t) != "0" {
		t.Errorf("шаг 8: /suggest, /support: %q", methods(out))
	}
	sayTo(t, b, api, "/support")
	sayTo(t, b, api, "/suggest")
	if out := sayTo(t, b, api, suggestLink); len(out) != 2 || out[1].Form["text"] != reportSent || suggestions(t) != "1" ||
		dbScalar(t, `SELECT count(*)::text FROM support_awaiting`) != "0" {
		t.Errorf("шаг 8: /support, /suggest: %q", methods(out))
	}
	sayTo(t, b, api, "/suggest")
	startAs(t, b, api, 42)
	if out := sayTo(t, b, api, suggestLink); len(out) != 1 || out[0].Form["text"] == reportSent || suggestions(t) != "1" {
		t.Errorf("шаг 8: /suggest, /start: %q", methods(out))
	}
	// Окно истекло — ссылка обычный текст.
	sayTo(t, b, api, "/suggest")
	setNow(b, awaitSuggestFor)
	if out := sayTo(t, b, api, suggestLink); len(out) != 1 || out[0].Form["text"] == reportSent || suggestions(t) != "1" {
		t.Errorf("шаг 8: истёкшее окно: %q", methods(out))
	}
	setNow(b, 0)

	// 9. Группа не задана — G-2 каждому администратору по порядку; сбой обоим —
	// P-5, строка удалена, отметка возвращена на окно.
	dbExec(t, `DELETE FROM start_suggestions`)
	admins := newBotWith(t, api, s, Config{Token: "123:TEST", Admins: []int64{1, 2}})
	sayTo(t, admins, api, "/suggest")
	out := saySnap(t, admins, api, suggestLink, "sendMessage sendMessage sendMessage", "s55_admins")
	if out[0].Form["chat_id"] != "1" || out[1].Form["chat_id"] != "2" || suggestions(t) != "1" {
		t.Errorf("шаг 9: получатели %s, %s; строк %s", out[0].Form["chat_id"], out[1].Form["chat_id"], suggestions(t))
	}
	dbExec(t, `DELETE FROM start_suggestions`)
	sayTo(t, admins, api, "/suggest")
	fail.Store(true)
	admins.Now = func() time.Time { return fixtureNow.Add(time.Minute) }
	saySnap(t, admins, api, suggestLink, "sendMessage sendMessage sendMessage", "s55_failure")
	if suggestions(t) != "0" || !awaitingSuggest(t, fixtureNow.Add(time.Minute+awaitSuggestFor)) {
		t.Errorf("шаг 9: строк %s, отметка не возвращена", suggestions(t))
	}
	fail.Store(false)
	if out := sayTo(t, admins, api, suggestLink); len(out) != 3 || out[2].Form["text"] != reportSent {
		t.Errorf("шаг 9: повтор после сбоя: %q", methods(out))
	}

	// Ни группы, ни администраторов: строки на пустом экране нет, /suggest — неизвестная
	// команда, кнопка q:s устарела.
	off := newBotWith(t, api, s, Config{Token: "123:TEST"})
	if c := press(t, off, api, "p:1:v"+strconv.Itoa(v)); c.button(t, suggestLabel) != "" {
		t.Errorf("без получателей: строка есть")
	}
	if out := sayTo(t, off, api, "/suggest"); methods(out) != "sendMessage" || out[0].Form["text"] != "Нажмите /start, чтобы найти старт" {
		t.Errorf("без получателей: /suggest %q", methods(out))
	}
	if out := favTap(t, off, api, 42, nil, "q:s"); notice(t, out) != staleNotice {
		t.Errorf("без получателей: q:s %q", notice(t, out))
	}

	// Ссылка и текст сообщений не попадают в лог.
	if !strings.Contains(logs.String(), "outcome=sent") || !strings.Contains(logs.String(), "kind=blocked") {
		t.Errorf("в логе нет исхода либо вида ошибки:\n%s", logs.String())
	}
	noLeak(t, "лог", logs.String(), "example.org", "a.org", "Казани", "Помогите")
}
