package bot

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Заявки об ошибке (16-UX-CONTRACT.md, сценарий S-54). Каталог и дата — как в
// Phase 14 (skiBot); группа задана, администратор 1. Эталоны s54_* написаны
// вручную по контракту: все вызовы шага подряд.

// reportBot — бот сценария на каталоге skiBot с заданной конфигурацией.
func reportBot(t *testing.T, cfg Config, scripts ...script) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	s := storetest.Open(t)
	seedMarathonec(t, s)
	seedSki(t, s)
	storetest.EnableSport(t, event.SportSkiing)
	api := fakeTelegram(t, append(scripts, groupReplies())...)
	b := newBotWith(t, api, s, cfg)
	filtersV(t, b) // пользователь 42: фильтры {}, версия 1
	return s, api, b
}

// reports — заявки «пользователь:событие:причина» через запятую.
func reports(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE(string_agg(tg_id || ':' || event_id || ':' || reason, ',' ORDER BY tg_id, event_id, reason), '') FROM error_reports`)
}

// reportStep — нажатие со сверкой всех вызовов с эталоном и методов.
func reportStep(t *testing.T, b *Bot, api *fakeAPI, from *call, data, want, snap string) []call {
	t.Helper()
	calls := favTap(t, b, api, 42, from, data)
	if got := methods(calls); got != want {
		t.Fatalf("%s: вызовы %q, want %q", data, got, want)
	}
	for _, c := range calls {
		checkCallbackLengths(t, c)
	}
	if snap != "" {
		matchSnapshot(t, snap, renderCalls(t, calls))
	}
	return calls
}

// notice — текст единственного ответа на нажатие.
func notice(t *testing.T, calls []call) string {
	t.Helper()
	last := calls[len(calls)-1]
	if last.Method != "answerCallbackQuery" {
		t.Fatalf("последний вызов %s", last.Method)
	}
	return last.Form["text"]
}

// TestS54Report — S-54: сообщить об ошибке в карточке.
func TestS54Report(t *testing.T) {
	var fail atomic.Bool
	failAdmins := func(c call, _ int) string {
		if fail.Load() && c.Method == "sendMessage" && (c.Form["chat_id"] == "1" || c.Form["chat_id"] == "2") {
			return errBlocked
		}
		return ""
	}
	s, api, b := reportBot(t, Config{Token: "123:TEST", SupportChatID: supportGroup, Admins: []int64{1}}, failAdmins)
	group := strconv.FormatInt(supportGroup, 10)
	logs := captureLogs(t)

	// 1. Карточка со строкой заявки.
	card := favSnap(t, b, api, 42, nil, "e:110:1:v1", "card")

	// 2. Выбор причины — новым сообщением, карточка не правится, записей нет.
	open := reportStep(t, b, api, &card, "q:o:110", "sendMessage answerCallbackQuery", "s54_reasons")
	if open[0].Form["chat_id"] != "42" || notice(t, open) != "" || reports(t) != "" {
		t.Errorf("шаг 2: чат %s, уведомление %q, заявки %q", open[0].Form["chat_id"], notice(t, open), reports(t))
	}
	reasons := open[0]

	// 3. Причина: G-1 в группу, правка R-1 на R-2, строка заявки.
	sent := reportStep(t, b, api, &reasons, "q:r:110:d", "sendMessage editMessageText answerCallbackQuery", "s54_sent")
	if sent[0].Form["chat_id"] != group || sent[0].Form["link_preview_options"] == "" || notice(t, sent) != "" {
		t.Errorf("шаг 3: чат %s, форма %v", sent[0].Form["chat_id"], sent[0].Form)
	}
	if reports(t) != "42:110:date" {
		t.Fatalf("шаг 3: заявки %q", reports(t))
	}
	if !strings.Contains(sent[1].Form["text"], "✅ Передал.") || strings.Contains(sent[0].Form["text"], "42") {
		t.Errorf("шаг 3: итог %q, в группе %q", sent[1].Form["text"], sent[0].Form["text"])
	}

	// 4. Повтор из старой копии R-1: в группу ничего, строка одна.
	b.Now = func() time.Time { return fixtureNow.Add(time.Minute) }
	reportStep(t, b, api, &reasons, "q:r:110:d", "editMessageText answerCallbackQuery", "s54_repeat")
	if reports(t) != "42:110:date" || dbScalar(t, `SELECT (created_at = $1)::text FROM error_reports`, fixtureNow) != "true" {
		t.Errorf("шаг 4: заявки %q", reports(t))
	}

	// 5. Отмена.
	reportStep(t, b, api, &reasons, "q:x", "editMessageText answerCallbackQuery", "s54_cancel")

	// 6. Десять заявок за 24 часа — одиннадцатая не принимается; заявка старше
	// окна в счёт не идёт.
	for _, r := range [][2]any{{110, "place"}, {110, "distance"}, {110, "link"}, {110, "cancelled"}, {110, "duplicate"},
		{110, "other"}, {52, "date"}, {52, "place"}, {52, "distance"}} {
		dbExec(t, `INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES (42, $1, $2, $3)`, r[0], r[1], fixtureNow.Add(-23*time.Hour))
	}
	dbExec(t, `INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES (42, 52, 'link', $1)`, fixtureNow.Add(-25*time.Hour))
	reportStep(t, b, api, &reasons, "q:r:135:d", "editMessageText answerCallbackQuery", "s54_limit")
	if n := dbScalar(t, `SELECT count(*)::text FROM error_reports`); n != "11" {
		t.Errorf("шаг 6: строк %s, want 11", n)
	}
	dbExec(t, `DELETE FROM error_reports WHERE created_at < $1`, fixtureNow.Add(-24*time.Hour))
	if out := favTap(t, b, api, 42, &reasons, "q:r:110:d"); methods(out) != "editMessageText answerCallbackQuery" {
		t.Errorf("шаг 6: повтор на пределе: %q", methods(out))
	} else if !strings.Contains(out[0].Form["text"], reportRepeat) {
		t.Errorf("шаг 6: повтор на пределе — не R-3: %q", out[0].Form["text"])
	}
	dbExec(t, `DELETE FROM error_reports`)

	// 7. Недоступное событие.
	reportStep(t, b, api, &reasons, "q:o:99999", "answerCallbackQuery", "s54_open_gone")
	storetest.HideEvent(t, 135)
	reportStep(t, b, api, &reasons, "q:r:135:p", "editMessageText answerCallbackQuery", "s54_gone")
	if out := favTap(t, b, api, 42, &reasons, "q:o:135"); methods(out) != "answerCallbackQuery" || notice(t, out) != goneNotice {
		t.Errorf("шаг 7: q:o скрытого: %q %q", methods(out), notice(t, out))
	}
	storetest.DisableSport(t, event.SportSkiing)
	if out := favTap(t, b, api, 42, &reasons, "q:o:134"); methods(out) != "answerCallbackQuery" || notice(t, out) != goneNotice {
		t.Errorf("шаг 7: q:o выключенного вида спорта: %q %q", methods(out), notice(t, out))
	}
	storetest.EnableSport(t, event.SportSkiing)

	// 8. Старт прошёл.
	b.Now = func() time.Time { return time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC) }
	reportStep(t, b, api, &reasons, "q:r:110:k", "editMessageText answerCallbackQuery", "s54_past")
	if out := favTap(t, b, api, 42, &reasons, "q:o:110"); methods(out) != "answerCallbackQuery" || notice(t, out) != favPastNotice {
		t.Errorf("шаг 8: q:o прошедшего: %q %q", methods(out), notice(t, out))
	}
	if reports(t) != "" {
		t.Errorf("шаги 7–8: заявки %q", reports(t))
	}
	b.Now = func() time.Time { return fixtureNow }

	// 9. Слитое событие: заявка и номер в G-1 — корня.
	storetest.MergeEvent(t, 52, 110)
	merged := reportStep(t, b, api, &reasons, "q:r:52:m", "sendMessage editMessageText answerCallbackQuery", "s54_merged")
	if reports(t) != "42:110:duplicate" || !strings.Contains(merged[0].Form["text"], "<code>110</code>") {
		t.Errorf("шаг 9: заявки %q", reports(t))
	}

	// 12. Ответ в группе на G-1 — ни одного вызова.
	if calls := feed(b, api, groupReply(2001, 900, 101, "Спасибо")); len(calls) != 0 {
		t.Errorf("шаг 12: вызовы %q", methods(calls))
	}

	// 13. Кнопки вне грамматики — «Кнопка устарела», без записи; самая длинная
	// кнопка помещается в 64 байта.
	before := reports(t)
	for _, data := range []string{"q:", "q:o", "q:o:0", "q:o:01", "q:o:abc", "q:r:110", "q:r:110:z", "q:r:110:dd",
		"q:x:1", "q:s:1", "q:c:110", "q:z", "q:o:110:1", "q:r:0:d", "q:r:-1:d", "q:r:110:D"} {
		if out := favTap(t, b, api, 42, &reasons, data); methods(out) != "answerCallbackQuery" || notice(t, out) != staleNotice {
			t.Errorf("шаг 13: %q: вызовы %q, уведомление %q", data, methods(out), notice(t, out))
		}
	}
	if reports(t) != before {
		t.Errorf("шаг 13: заявки %q, want %q", reports(t), before)
	}
	for _, c := range []callback{{Kind: cbReportOpen, ID: 1<<63 - 1}, {Kind: cbReport, ID: 1<<63 - 1, Value: "o"}, {Kind: cbReportCancel}} {
		data, err := c.encode()
		if err != nil || len(data) > 25 {
			t.Errorf("шаг 13: %+v → %q, %v", c, data, err)
		}
		if got, err := parseCallback(data); err != nil || got != c {
			t.Errorf("шаг 13: разбор %q = %+v, %v", data, got, err)
		}
	}

	// 10. Группа не задана, администраторы 1 и 2: G-1 каждому по порядку
	// ADMIN_TG_IDS (повтор в перечне — одно сообщение), R-2.
	dbExec(t, `DELETE FROM error_reports`)
	order := newBotWith(t, api, s, Config{Token: "123:TEST", Admins: []int64{2, 1, 2}})
	sent = reportStep(t, order, api, nil, "q:r:110:d", "sendMessage sendMessage editMessageText answerCallbackQuery", "")
	if sent[0].Form["chat_id"] != "2" || sent[1].Form["chat_id"] != "1" || sent[0].Form["text"] != sent[1].Form["text"] {
		t.Errorf("шаг 10: получатели %s, %s", sent[0].Form["chat_id"], sent[1].Form["chat_id"])
	}
	dbExec(t, `DELETE FROM error_reports`)
	admins := newBotWith(t, api, s, Config{Token: "123:TEST", Admins: []int64{1, 2}})
	reportStep(t, admins, api, nil, "q:r:110:d", "sendMessage sendMessage editMessageText answerCallbackQuery", "s54_admins")
	if reports(t) != "42:110:date" {
		t.Errorf("шаг 10: заявки %q", reports(t))
	}
	// Отказ обоим — R-8 уведомлением, R-1 не правится, записи нет.
	dbExec(t, `DELETE FROM error_reports`)
	fail.Store(true)
	out := reportStep(t, admins, api, nil, "q:r:110:d", "sendMessage sendMessage answerCallbackQuery", "s54_failure")
	if notice(t, out) != reportFailed || reports(t) != "" {
		t.Errorf("шаг 10: отказ обоим: уведомление %q, заявки %q", notice(t, out), reports(t))
	}
	fail.Store(false)
	if !strings.Contains(logs.String(), "kind=blocked") {
		t.Errorf("шаг 10: в логе нет вида ошибки:\n%s", logs.String())
	}
	for _, leak := range []string{"Кросс", "Знаменских", "=110", "=42", "tg_id", "event_id", "chat_id"} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("в логе %q:\n%s", leak, logs.String())
		}
	}

	// 11. Ни группы, ни администраторов: строки в карточке нет, кнопка q: устарела.
	off := newBotWith(t, api, s, Config{Token: "123:TEST"})
	favSnap(t, off, api, 42, nil, "e:110:1:v1", "s54_card_off")
	if out := favTap(t, off, api, 42, nil, "q:o:110"); methods(out) != "answerCallbackQuery" || notice(t, out) != staleNotice {
		t.Errorf("шаг 11: q:o без получателей: %q %q", methods(out), notice(t, out))
	}
}

// TestReportPartialDelivery — заявка принята, если дошло хотя бы одно сообщение
// администратору; отказ в группе — R-8, запись удалена, R-1 не правится.
func TestReportPartialDelivery(t *testing.T) {
	var groupDown atomic.Bool
	s, api, b := reportBot(t, Config{Token: "123:TEST", Admins: []int64{1, 2}}, func(c call, _ int) string {
		if c.Method == "sendMessage" && c.Form["chat_id"] == "1" {
			return errBlocked
		}
		return ""
	}, groupFails(&groupDown, apiErr(400, "Bad Request: chat not found", 0)))
	out := reportStep(t, b, api, nil, "q:r:110:u", "sendMessage sendMessage editMessageText answerCallbackQuery", "")
	if notice(t, out) != "" || reports(t) != "42:110:link" {
		t.Errorf("один отказ: уведомление %q, заявки %q", notice(t, out), reports(t))
	}

	dbExec(t, `DELETE FROM error_reports`)
	groupDown.Store(true)
	g := newBotWith(t, api, s, Config{Token: "123:TEST", SupportChatID: supportGroup, Admins: []int64{1}})
	out = reportStep(t, g, api, nil, "q:r:110:u", "sendMessage answerCallbackQuery", "")
	if out[0].Form["chat_id"] != strconv.FormatInt(supportGroup, 10) || notice(t, out) != reportFailed || reports(t) != "" {
		t.Errorf("отказ группы: чат %s, уведомление %q, заявки %q", out[0].Form["chat_id"], notice(t, out), reports(t))
	}
	groupDown.Store(false)
	if out := reportStep(t, g, api, nil, "q:r:110:u", "sendMessage editMessageText answerCallbackQuery", ""); reports(t) != "42:110:link" {
		t.Errorf("повтор после сбоя: %q, заявки %q", methods(out), reports(t))
	}
}

// TestReportPrune — служебная часть уведомлений удаляет заявки и предложения
// старше 90 дней; моложе — остаются.
func TestReportPrune(t *testing.T) {
	_, _, b := reportBot(t, Config{Token: "123:TEST", Admins: []int64{1}})
	old, fresh := fixtureNow.Add(-reportKeep-time.Minute), fixtureNow.Add(-reportKeep+time.Minute)
	dbExec(t, `INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES (42, 110, 'date', $1), (42, 110, 'place', $2)`, old, fresh)
	dbExec(t, `INSERT INTO start_suggestions (tg_id, created_at) VALUES (42, $1), (43, $2)`, old, fresh)
	NewNotifier(b).chores(context.Background(), fixtureNow)
	if got := reports(t) + " " + dbScalar(t, `SELECT string_agg(tg_id::text, ',') FROM start_suggestions`); got != "42:110:place 43" {
		t.Errorf("после очистки %q, want «42:110:place 43»", got)
	}
}
