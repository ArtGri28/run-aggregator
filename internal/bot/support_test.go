package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Поддержка Phase 11 (11-UX-CONTRACT.md, сценарии S-26…S-31 и S-35). Бот
// сценариев S-26…S-31 собран с группой supportGroup (newSupportBot), бот S-35 —
// без неё (newTestBot, как все прежние тесты). Эталоны — файлы, написанные по
// контракту вручную; тест их только читает.

// supportGroup — идентификатор группы поддержки в тестах (выдуманный).
const supportGroup int64 = -1001234567890

// inviteText — текст приглашения H-1 дословно из контракта.
const inviteText = "💬 <b>Поддержка</b>\n\nНапишите вопрос или опишите проблему одним текстовым сообщением. Следующее сообщение в течение 10 минут уйдёт в поддержку."

func newSupportBot(t *testing.T, api *fakeAPI, s *store.Store) *Bot {
	t.Helper()
	return newBotWith(t, api, s, Config{Token: "123:TEST", SupportChatID: supportGroup})
}

// groupReplies — ответы фиктивного API на отправку в группу: сообщение типа
// supergroup с новым идентификатором (101, 102, …) на каждый вызов; остальные
// вызовы — ответ по умолчанию. Ставится после сценариев ошибок.
func groupReplies() script {
	var next atomic.Int64
	next.Store(100)
	return func(c call, _ int) string {
		if c.Method != "sendMessage" || c.Form["chat_id"] != strconv.FormatInt(supportGroup, 10) {
			return ""
		}
		return fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"date":1,"chat":{"id":%d,"type":"supergroup"}}}`, next.Add(1), supportGroup)
	}
}

// groupFails — ошибка на каждую отправку в группу, пока включён флаг.
func groupFails(flag *atomic.Bool, body string) script {
	return func(c call, _ int) string {
		if flag.Load() && c.Method == "sendMessage" && c.Form["chat_id"] == strconv.FormatInt(supportGroup, 10) {
			return body
		}
		return ""
	}
}

// groupSends — сценарий ответов на отправки в группу: reply получает номер
// отправки (с 1 за весь тест); пустая строка — ответ по умолчанию (groupReplies).
func groupSends(reply func(n int) string) script {
	var cnt atomic.Int64
	return func(c call, _ int) string {
		if c.Method != "sendMessage" || c.Form["chat_id"] != strconv.FormatInt(supportGroup, 10) {
			return ""
		}
		return reply(int(cnt.Add(1)))
	}
}

// supportEnv — бот с группой на пустой базе; scripts идут раньше ответов группы.
func supportEnv(t *testing.T, scripts ...script) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	s := storetest.Open(t)
	api := fakeTelegram(t, append(scripts, groupReplies())...)
	return s, api, newSupportBot(t, api, s)
}

// named подставляет автору апдейта имя, фамилию и username.
func named(u *models.Update, first, last, username string) *models.Update {
	u.Message.From.FirstName, u.Message.From.LastName, u.Message.From.Username = first, last, username
	return u
}

// feed обрабатывает апдейт и возвращает все вызовы Bot API за него.
func feed(b *Bot, api *fakeAPI, u *models.Update) []call {
	api.reset()
	b.api.ProcessUpdate(context.Background(), u)
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]call(nil), api.calls...)
}

// split делит вызовы на отправленные в группу и остальные.
func split(calls []call) (group, rest []call) {
	for _, c := range calls {
		if c.Method == "sendMessage" && c.Form["chat_id"] == strconv.FormatInt(supportGroup, 10) {
			group = append(group, c)
		} else {
			rest = append(rest, c)
		}
	}
	return group, rest
}

// supportState — обе таблицы поддержки одной строкой (для сравнения до и после
// и для проверки, что в них нет текста).
func supportState(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT jsonb_build_object(
		'awaiting', (SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY a.tg_id), '[]'::jsonb) FROM support_awaiting a),
		'messages', (SELECT COALESCE(jsonb_agg(to_jsonb(m) ORDER BY m.chat_id, m.message_id), '[]'::jsonb) FROM support_messages m))::text`)
}

// linkCount — число связок; awaiting — стоит ли отметка до момента until.
func linkCount(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT count(*)::text FROM support_messages`)
}

func awaitingUntil(t *testing.T, id int64, until time.Time) bool {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE((SELECT (expires_at = $2)::text FROM support_awaiting WHERE tg_id = $1), 'none')`, id, until) == "true"
}

func noAwaiting(t *testing.T, id int64) bool {
	t.Helper()
	return dbScalar(t, `SELECT count(*)::text FROM support_awaiting WHERE tg_id = $1`, id) == "0"
}

// formsLeak проверяет, что ни в одном поле вызовов нет указанных строк.
func formsLeak(t *testing.T, calls []call, leaks ...string) {
	t.Helper()
	for _, c := range calls {
		for k, v := range c.Form {
			noLeak(t, "вызов "+c.Method+", поле "+k, v, leaks...)
		}
	}
}

// noLeak проверяет, что ни одна из строк не встречается в тексте (логи, таблицы).
func noLeak(t *testing.T, what, text string, leaks ...string) {
	t.Helper()
	for _, l := range leaks {
		if strings.Contains(text, l) {
			t.Errorf("%s: есть %q:\n%s", what, l, text)
		}
	}
}

// idRE ищет число-идентификатор как отдельное слово (чтобы не путать с
// цифрами времени в строке лога).
func idRE(id int64) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^0-9])` + strconv.FormatInt(id, 10) + `([^0-9]|$)`)
}

// TestS27SupportText — S-27: обращение текстом (D-02, D-08, D-13, D-15, D-17).
func TestS27SupportText(t *testing.T) {
	var fail atomic.Bool
	_, api, b := supportEnv(t, groupFails(&fail, apiErr(500, "Internal Server Error: SECRET7 detail", 0)))
	logs := captureLogs(t)
	anna := func(u *models.Update) *models.Update { return named(u, "Анна", "Петрова", "anna_p") }
	until := fixtureNow.Add(awaitSupportFor)

	// 1. /support: приглашение новым сообщением, отметка на 10 минут, ни users,
	// ни профиля.
	calls := feed(b, api, anna(commandUpdate(1001, "/support")))
	if len(calls) != 1 || calls[0].Method != "sendMessage" || calls[0].Form["chat_id"] != "1001" || calls[0].Form["text"] != inviteText {
		t.Fatalf("/support: вызовы %+v, want одно сообщение с приглашением", calls)
	}
	if !awaitingUntil(t, 1001, until) {
		t.Fatalf("после /support отметки на %v нет: %s", until, supportState(t))
	}
	if u, p := usersState(t), profilesState(t); u != "[]" || p != "[]" {
		t.Fatalf("/support создал строки: users %s, user_profiles %s", u, p)
	}

	// 2. Текст с переводом строки: сообщение бота в группе, затем подтверждение.
	calls = feed(b, api, anna(textUpdate(1001, "Здравствуйте!\nНе открывается карточка забега")))
	group, rest := split(calls)
	if len(group) != 1 || len(rest) != 1 || rest[0].Method != "sendMessage" || rest[0].Form["chat_id"] != "1001" || len(calls) != 2 {
		t.Fatalf("обращение: вызовы %+v, want одно сообщение в группу и одно подтверждение", calls)
	}
	if calls[0].Form["chat_id"] != strconv.FormatInt(supportGroup, 10) {
		t.Errorf("подтверждение ушло раньше сообщения в группу: %+v", calls)
	}
	matchSnapshot(t, "s27_group", renderCall(t, group[0]))
	matchSnapshot(t, "s27_confirm", renderCall(t, rest[0]))
	if got := dbScalar(t, `SELECT chat_id::text || ':' || message_id::text || ':' || tg_id::text || ':' || (created_at = $1)::text FROM support_messages`, fixtureNow); got != "-1001234567890:101:1001:true" {
		t.Fatalf("связка = %q, want -1001234567890:101:1001:true", got)
	}
	if !noAwaiting(t, 1001) {
		t.Errorf("после обращения отметка осталась: %s", supportState(t))
	}

	// 3. HTML-символы экранируются, пробелы по краям обрезаются, переводы строк внутри остаются.
	feed(b, api, anna(commandUpdate(1001, "/support")))
	group, _ = split(feed(b, api, anna(textUpdate(1001, "  \n<b>X</b> & \"Y\"\n\nвторая строка  \n "))))
	if len(group) != 1 {
		t.Fatalf("текст с HTML: сообщений в группу %d, want 1", len(group))
	}
	matchSnapshot(t, "s27_group_html", renderCall(t, group[0]))

	// 4. Без фамилии и без username шапка короче.
	feed(b, api, named(commandUpdate(1003, "/support"), "Иван", "", ""))
	group, _ = split(feed(b, api, named(textUpdate(1003, "Привет"), "Иван", "", "")))
	if len(group) != 1 {
		t.Fatalf("без username: сообщений в группу %d, want 1", len(group))
	}
	matchSnapshot(t, "s27_group_nouser", renderCall(t, group[0]))

	// 5. D-13, D-15: в таблицах поддержки и в логах нет текста, имён и Telegram ID;
	// users и профиль не созданы.
	leaks := []string{"Анна", "Петрова", "anna_p", "Иван", "Здравствуйте", "карточка", "вторая строка", "Привет", "SECRET7"}
	noLeak(t, "таблицы поддержки", supportState(t), leaks...)
	noLeak(t, "логи", logs.String(), leaks...)
	for _, id := range []int64{1001, 1003} {
		if idRE(id).MatchString(logs.String()) {
			t.Errorf("в логах есть Telegram ID %d:\n%s", id, logs)
		}
	}
	if u, p := usersState(t), profilesState(t); u != "[]" || p != "[]" {
		t.Fatalf("обращения создали строки: users %s, user_profiles %s", u, p)
	}
	if n := linkCount(t); n != "3" {
		t.Fatalf("связок %s, want 3", n)
	}

	// 6. Сбой отправки в группу: отказ R-4 вместо подтверждения, связка не создана.
	feed(b, api, anna(commandUpdate(1001, "/support")))
	fail.Store(true)
	calls = feed(b, api, anna(textUpdate(1001, "не дойдёт")))
	fail.Store(false)
	group, rest = split(calls)
	if len(group) != 1 || len(rest) != 1 || rest[0].Form["chat_id"] != "1001" {
		t.Fatalf("сбой отправки: вызовы %+v, want одна попытка в группу и один отказ", calls)
	}
	matchSnapshot(t, "s31_failure", renderCall(t, rest[0]))
	if n := linkCount(t); n != "3" {
		t.Fatalf("сбой создал связку: %s", n)
	}
	noLeak(t, "логи после сбоя", logs.String(), "не дойдёт", "SECRET7", "Анна", "anna_p")
	if idRE(1001).MatchString(logs.String()) {
		t.Errorf("в логах есть Telegram ID после сбоя:\n%s", logs)
	}

	// 7. Следующий текст после принятого обращения идёт в поиск (в самом конце:
	// поиск создаёт строку users).
	feed(b, api, named(commandUpdate(1003, "/support"), "Иван", "", ""))
	feed(b, api, named(textUpdate(1003, "Ещё вопрос"), "Иван", "", ""))
	calls = feed(b, api, named(textUpdate(1003, "марафон москва"), "Иван", "", ""))
	group, rest = split(calls)
	if len(group) != 0 || len(rest) != 1 || rest[0].Form["chat_id"] != "1003" {
		t.Fatalf("текст после обращения: вызовы %+v, want один ответ поиска пользователю и ничего в группу", calls)
	}
	if n := linkCount(t); n != "4" {
		t.Fatalf("связок %s, want 4 (поиск связок не создаёт)", n)
	}
	t.Logf("TRACER support=sent link=stored confirm=sent")
}

// renderAll — снимок всех вызовов подряд, в порядке поступления.
func renderAll(t *testing.T, calls []call) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range calls {
		sb.WriteString(renderCall(t, c))
	}
	return sb.String()
}

// setNow сдвигает часы бота от fixtureNow (без ожидания).
func setNow(b *Bot, d time.Duration) {
	b.Now = func() time.Time { return fixtureNow.Add(d) }
}

// mediaUpdate — личное сообщение без текста: вложение указанного вида (только
// чтобы проверить отказ; содержимое ничего не значит).
func mediaUpdate(userID int64, kind string) *models.Update {
	u := startUpdate(userID)
	m := u.Message
	m.Text, m.Entities = "", nil
	switch kind {
	case "photo":
		m.Photo = []models.PhotoSize{{FileID: "f", Width: 1, Height: 1}}
	case "photo+caption":
		m.Photo, m.Caption = []models.PhotoSize{{FileID: "f", Width: 1, Height: 1}}, "приметная подпись"
	case "video":
		m.Video = &models.Video{FileID: "f"}
	case "document":
		m.Document = &models.Document{FileID: "f"}
	case "voice":
		m.Voice = &models.Voice{FileID: "f"}
	case "video_note":
		m.VideoNote = &models.VideoNote{FileID: "f"}
	case "sticker":
		m.Sticker = &models.Sticker{FileID: "f"}
	case "contact":
		m.Contact = &models.Contact{PhoneNumber: "1"}
	case "location":
		m.Location = &models.Location{Latitude: 1, Longitude: 1}
	default:
		panic("mediaUpdate: " + kind)
	}
	return u
}

// seedSkipped — пользователь со строками users и профиля (статус skipped), как
// в исходном состоянии S-26: главное меню на /start, а не предложение анкеты.
func seedSkipped(t *testing.T, b *Bot, api *fakeAPI, id int64) {
	t.Helper()
	startAs(t, b, api, id) // O-1: создаёт users и профиль
	dbExec(t, `UPDATE user_profiles SET status = 'skipped' WHERE tg_id = $1`, id)
}

// supportCountryEnv — фикстура городов countryFixture, но бот с группой.
func supportCountryEnv(t *testing.T) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", "road", "Московский забег")
	seedCountryEvent(t, s, "BY", "2026-10-13", "Минск", "minsk", "road", "Минский забег")
	api := fakeTelegram(t, groupReplies())
	return s, api, newSupportBot(t, api, s)
}

// TestS26SupportEntry — S-26: вход в поддержку кнопкой и командой (D-07, D-08).
func TestS26SupportEntry(t *testing.T) {
	_, api, b := supportCountryEnv(t)
	until := fixtureNow.Add(awaitSupportFor)

	// 1. Главное меню из пяти кнопок; отметки ещё нет.
	seedSkipped(t, b, api, 1001)
	menu := startAs(t, b, api, 1001)
	matchSnapshot(t, "s26_menu", renderCall(t, menu))
	if got := datas(t, menu); len(got) != 5 || got[4] != "h:o" {
		t.Fatalf("кнопки меню: %v, want пять, последняя h:o", got)
	}
	if !noAwaiting(t, 1001) {
		t.Fatalf("/start поставил отметку: %s", supportState(t))
	}

	// 2. «💬 Поддержка»: правка меню на приглашение, один пустой ответ, отметка;
	// users и профиль не меняются.
	usersBefore, profileBefore := userRow(t, 1001), profileRow(t, 1001)
	step(t, b, api, 1001, uData(t, menu, "💬 Поддержка"), "s26_invite_button")
	if !awaitingUntil(t, 1001, until) {
		t.Fatalf("после h:o отметки на %v нет: %s", until, supportState(t))
	}

	// 3. /support у пользователя без строк users и профиля: новое сообщение, строк не создано.
	calls := feed(b, api, commandUpdate(1002, "/support"))
	if len(calls) != 1 || calls[0].Method != "sendMessage" || calls[0].Form["chat_id"] != "1002" {
		t.Fatalf("/support: вызовы %+v, want одно сообщение", calls)
	}
	matchSnapshot(t, "s26_invite_command", renderCall(t, calls[0]))
	checkCallbackLengths(t, calls[0])
	if userRow(t, 1002) != "" || profileRow(t, 1002) != "" || !awaitingUntil(t, 1002, until) {
		t.Fatalf("/support без /start: users %q, профиль %q, отметка: %s", userRow(t, 1002), profileRow(t, 1002), supportState(t))
	}

	// 4. «💬 Ответить» (h:r): приглашение новым сообщением, сообщение с ответом не правится.
	calls = raw(b, api, 1001, "h:r")
	if len(calls) != 2 || calls[0].Method != "sendMessage" || calls[0].Form["chat_id"] != "1001" || calls[1].Method != "answerCallbackQuery" {
		t.Fatalf("h:r: вызовы %+v, want новое сообщение и один ответ", calls)
	}
	matchSnapshot(t, "s26_invite_reply", renderAll(t, calls))

	// 5. Повторный вход при действующем режиме: одна строка, окно заново от нового входа.
	setNow(b, 4*time.Minute)
	feed(b, api, commandUpdate(1001, "/support"))
	if n := dbScalar(t, `SELECT count(*)::text FROM support_awaiting WHERE tg_id = 1001`); n != "1" || !awaitingUntil(t, 1001, fixtureNow.Add(14*time.Minute)) {
		t.Fatalf("повторный вход: %s", supportState(t))
	}
	setNow(b, 0)

	// 6. Ни вход кнопкой, ни командой не пишут в users и профиль.
	if got := userRow(t, 1001); got != usersBefore {
		t.Errorf("вход в поддержку изменил users:\n%s\n%s", usersBefore, got)
	}
	if got := profileRow(t, 1001); got != profileBefore {
		t.Errorf("вход в поддержку изменил профиль:\n%s\n%s", profileBefore, got)
	}
}

// TestS29SupportCancel — S-29: отмена, истечение и взаимоисключение режимов (D-09, D-10).
func TestS29SupportCancel(t *testing.T) {
	_, api, b := supportCountryEnv(t)
	seedSkipped(t, b, api, 1001)
	// reply — единственный ответ на текст пользователя 1001: новое сообщение ему.
	searchReply := func(text string) call {
		t.Helper()
		c := sayText(t, b, api, 1001, text)
		if c.Form["chat_id"] != "1001" || !strings.Contains(c.Form["text"], "ничего не нашлось") {
			t.Fatalf("%q: ожидался ответ поиска, получено %+v", text, c)
		}
		return c
	}

	// 1. «Отмена»: правка на главное меню из пяти кнопок, отметки нет, текст идёт в поиск.
	feed(b, api, commandUpdate(1001, "/support"))
	cancel := step(t, b, api, 1001, "h:x", "s29_cancel")
	if got := datas(t, cancel); len(got) != 5 || got[4] != "h:o" {
		t.Fatalf("меню после «Отмены»: %v", got)
	}
	if !noAwaiting(t, 1001) {
		t.Fatalf("«Отмена» не сняла отметку: %s", supportState(t))
	}
	searchReply("Атлантида")

	// 2. «Отмена» на приглашении, присланном новым сообщением, — то же.
	prompt := feed(b, api, commandUpdate(1001, "/support"))
	if len(prompt) != 1 || prompt[0].button(t, "Отмена") != "h:x" {
		t.Fatalf("приглашение без кнопки «Отмена»: %+v", prompt)
	}
	if edit, _ := tap(t, b, api, 1001, "h:x"); !isMainMenu(edit) || !noAwaiting(t, 1001) {
		t.Fatalf("«Отмена» на новом сообщении: %+v", edit)
	}

	// 3. «Отмена», когда отметки уже нет: меню правкой, записей нет, ошибки нет.
	state := dbState(t)
	if edit, notice := tap(t, b, api, 1001, "h:x"); !isMainMenu(edit) || notice != "" {
		t.Fatalf("«Отмена» без отметки: %+v, уведомление %q", edit, notice)
	}
	if dbState(t) != state || supportState(t) != `{"awaiting": [], "messages": []}` {
		t.Errorf("«Отмена» без отметки изменила состояние: %s", supportState(t))
	}

	// 4. /start в режиме снимает отметку; текст идёт в поиск.
	feed(b, api, commandUpdate(1001, "/support"))
	matchSnapshot(t, "s29_start_menu", renderCall(t, startAs(t, b, api, 1001)))
	if !noAwaiting(t, 1001) {
		t.Fatalf("/start не снял отметку: %s", supportState(t))
	}
	searchReply("Атлантида")

	// 5. Истечение: ровно на момент окончания окна режим уже снят; группе ничего.
	feed(b, api, commandUpdate(1001, "/support"))
	setNow(b, awaitSupportFor)
	matchSnapshot(t, "s29_expired", renderCall(t, searchReply("Атлантида")))
	setNow(b, 0)
	if n := linkCount(t); n != "0" {
		t.Fatalf("после истечения окна появилась связка: %s", n)
	}

	// 6. Отметка города → вход в поддержку снимает её; профиль, кроме отметки, не меняется; текст — обращение.
	toCityScreen(t, b, api, 2001, "🇧🇾 Беларусь")
	if awaitingMark(t, 2001) != "w" {
		t.Fatalf("экран города не поставил отметку: %s", profileRow(t, 2001))
	}
	pBefore, vBefore := profileOf(t, b, 2001)
	feed(b, api, commandUpdate(2001, "/support"))
	pAfter, vAfter := profileOf(t, b, 2001)
	pBefore.AwaitingCityUntil, pBefore.AwaitingCityMode = time.Time{}, ""
	if awaitingMark(t, 2001) != "-" || vAfter != vBefore || !reflect.DeepEqual(pAfter, pBefore) || !awaitingUntil(t, 2001, fixtureNow.Add(awaitSupportFor)) {
		t.Fatalf("/support при отметке города: профиль %+v v%d (было %+v v%d), отметка поддержки: %s", pAfter, vAfter, pBefore, vBefore, supportState(t))
	}
	group, _ := split(feed(b, api, textUpdate(2001, "Гродно")))
	if p, _ := profileOf(t, b, 2001); len(group) != 1 || p.City != "" {
		t.Fatalf("текст при отметке поддержки: в группу %d, город %q; want 1 и пусто", len(group), p.City)
	}
	// То же кнопкой h:o из старого меню.
	toCityScreen(t, b, api, 2002, "🇧🇾 Беларусь")
	tap(t, b, api, 2002, "h:o")
	if awaitingMark(t, 2002) != "-" || !awaitingUntil(t, 2002, fixtureNow.Add(awaitSupportFor)) {
		t.Fatalf("h:o при отметке города: %s / %s", profileRow(t, 2002), supportState(t))
	}

	// 7. Отметка поддержки → экран города актуальной кнопкой снимает её; текст — город проживания.
	offer := startAs(t, b, api, 2003)
	feed(b, api, commandUpdate(2003, "/support"))
	q1, _ := tap(t, b, api, 2003, uData(t, offer, "📝 Заполнить анкету"))
	q2, _ := tap(t, b, api, 2003, uData(t, q1, "Пропустить вопрос"))
	q3, _ := tap(t, b, api, 2003, uData(t, q2, "Пропустить вопрос"))
	if !awaitingUntil(t, 2003, fixtureNow.Add(awaitSupportFor)) {
		t.Fatalf("вопросы анкеты без города сняли отметку поддержки: %s", supportState(t))
	}
	tap(t, b, api, 2003, uData(t, q3, "🇧🇾 Беларусь"))
	if !noAwaiting(t, 2003) || awaitingMark(t, 2003) != "w" {
		t.Fatalf("экран города: отметка поддержки %s, профиль %s", supportState(t), profileRow(t, 2003))
	}
	linksBefore := linkCount(t)
	if ack := sayText(t, b, api, 2003, "минск"); !isAck(ack) || linkCount(t) != linksBefore {
		t.Fatalf("текст при отметке города: %+v", ack)
	}

	// 8. Что режим не снимает: другие команды и кнопки; следующий текст — обращение (Q-5).
	feed(b, api, commandUpdate(1001, "/support"))
	for _, c := range []string{"/profile", "/runs", "/stats", "/dupes"} {
		feed(b, api, commandUpdate(1001, c))
	}
	for _, data := range []string{"f:s:v1", "s:o:v1", "p:1:v1", "g:b:v1", "e:1:1:v1", "m", "u:o"} {
		raw(b, api, 1001, data)
		if !awaitingUntil(t, 1001, fixtureNow.Add(awaitSupportFor)) {
			t.Fatalf("после %q отметка поддержки изменилась: %s", data, supportState(t))
		}
	}
	if group, _ := split(feed(b, api, textUpdate(1001, "Всё-таки пишу сюда"))); len(group) != 1 {
		t.Fatalf("текст после чужих кнопок: в группу %d, want 1", len(group))
	}
}

// TestS35SupportDisabled — S-35: без группы поддержки бот ведёт себя как до фазы (D-03).
func TestS35SupportDisabled(t *testing.T) {
	_, api, b := countryFixture(t) // newTestBot: без группы
	logs := captureLogs(t)
	seedSkipped(t, b, api, 1001)

	// 1. Главное меню из четырёх кнопок — побайтно как прежний эталон.
	menu := renderCall(t, startAs(t, b, api, 1001))
	matchSnapshot(t, "s35_menu", menu)
	matchSnapshot(t, "s18_start_skipped", menu)
	support, state := supportState(t), dbState(t)

	// 2. /support — как любая неизвестная команда; режим не включается.
	calls := feed(b, api, commandUpdate(1001, "/support"))
	if len(calls) != 1 {
		t.Fatalf("/support: вызовы %+v, want одно сообщение", calls)
	}
	matchSnapshot(t, "s35_support_command", renderCall(t, calls[0]))
	if got := supportState(t); got != support || !noAwaiting(t, 1001) || dbState(t) != state {
		t.Fatalf("/support записал состояние: %s", got)
	}

	// 3. Текст — обычный поиск (длинный и короткий), без участия поддержки.
	if c := sayText(t, b, api, 1001, "Атлантида"); !strings.Contains(c.Form["text"], "ничего не нашлось") {
		t.Fatalf("поиск: %s", c.Form["text"])
	}
	long := strings.Repeat("я", 70)
	if c := sayText(t, b, api, 1001, long); c.Form["text"] != "Нажмите /start, чтобы найти старт" {
		t.Fatalf("текст длиннее 64 символов: %q", c.Form["text"])
	}

	// 5. Профиль — как в S-23: строки о данных нет.
	profile, _ := tap(t, b, api, 1001, "u:o")
	if !strings.HasSuffix(profile.Form["text"], "🏅 Спорт: не указан") {
		t.Fatalf("профиль:\n%s", profile.Form["text"])
	}

	// 6. Вложения: бот молчит, ни одного вызова, база не меняется.
	state = dbState(t)
	for _, kind := range []string{"photo", "photo+caption", "document", "sticker"} {
		if calls := feed(b, api, mediaUpdate(1001, kind)); len(calls) != 0 {
			t.Fatalf("%s без поддержки: вызовы %+v, want 0", kind, calls)
		}
	}
	if dbState(t) != state {
		t.Errorf("вложения изменили базу")
	}

	// 7. Кнопки h: из старого сообщения с четырьмя кнопками: только уведомление об устаревшей.
	state = dbState(t)
	for _, data := range []string{"h:o", "h:r", "h:x"} {
		toast := onlyToast(t, data, raw(b, api, 1001, data))
		matchSnapshot(t, "s35_stale", renderCall(t, toast))
	}
	if n := strings.Count(logs.String(), `msg="callback stale" prefix=h`); n != 3 {
		t.Errorf(`строк "callback stale prefix=h" в логе %d, want 3:`+"\n%s", n, logs)
	}
	if dbState(t) != state {
		t.Errorf("кнопки h: изменили базу")
	}

	// 8. Сообщения из любой группы (текст, команда, reply) — игнорируются, как до фазы; на
	// каждое одна запись Info с идентификатором и типом чата, без имён и текста (Q-7).
	state = dbState(t)
	before := strings.Count(logs.String(), "unconfigured group")
	for i, u := range []*models.Update{
		named(inChat(textUpdate(0, "Приметныйтекст9чужой"), strangerGroup, models.ChatTypeSupergroup, 2001, 830, 0), "Чужоеимя", "", "alien_user"),
		inChat(commandUpdate(0, "/start"), strangerGroup, models.ChatTypeGroup, 2001, 831, 0),
		inChat(textUpdate(0, "ответ"), supportGroup, models.ChatTypeSupergroup, 2001, 832, 101),
	} {
		if calls := feed(b, api, u); len(calls) != 0 {
			t.Fatalf("сообщение %d из группы без поддержки: вызовы %+v, want 0", i+1, calls)
		}
	}
	if got := strings.Count(logs.String(), "unconfigured group") - before; got != 3 {
		t.Errorf("записей о сообщениях из групп %d, want 3:\n%s", got, logs)
	}
	noLeak(t, "логи", logs.String(), "Приметныйтекст9чужой", "Чужоеимя", "alien_user")
	if dbState(t) != state {
		t.Errorf("сообщения из групп изменили базу")
	}

	// Таблицы поддержки пусты на всех шагах.
	if got := supportState(t); got != support {
		t.Errorf("таблицы поддержки не пусты: %s", got)
	}
}

// TestS28SupportNonText — S-28: нетекстовое сообщение получает отказ (D-11).
func TestS28SupportNonText(t *testing.T) {
	_, api, b := supportCountryEnv(t)
	logs := captureLogs(t)
	until := fixtureNow.Add(awaitSupportFor)
	kinds := []string{"photo+caption", "photo", "video", "document", "voice", "video_note", "sticker", "contact", "location"}

	// В режиме: на каждое сообщение ровно один отказ R-2, в группу ничего; окно не продлевается.
	feed(b, api, commandUpdate(1001, "/support"))
	setNow(b, 3*time.Minute)
	for i, kind := range kinds {
		calls := feed(b, api, mediaUpdate(1001, kind))
		formsLeak(t, calls, "приметная подпись")
		if len(calls) != 1 || calls[0].Method != "sendMessage" || calls[0].Form["chat_id"] != "1001" {
			t.Fatalf("%s: вызовы %+v, want один отказ пользователю", kind, calls)
		}
		if i == 0 {
			matchSnapshot(t, "s28_refuse", renderCall(t, calls[0]))
		} else if calls[0].Form["text"] != supportNotText {
			t.Fatalf("%s: текст %q, want отказ R-2", kind, calls[0].Form["text"])
		}
	}
	// Альбом: три сообщения с одним media_group_id — три отказа.
	for i := 0; i < 3; i++ {
		u := mediaUpdate(1001, "photo")
		u.Message.MediaGroupID = "album1"
		if calls := feed(b, api, u); len(calls) != 1 || calls[0].Form["text"] != supportNotText {
			t.Fatalf("альбом, сообщение %d: вызовы %+v, want один отказ", i+1, calls)
		}
	}
	if !awaitingUntil(t, 1001, until) || linkCount(t) != "0" {
		t.Fatalf("после отказов: %s, want отметка прежняя и связок нет", supportState(t))
	}
	// Режим остаётся: следующий текст уходит в группу обычным путём.
	group, rest := split(feed(b, api, textUpdate(1001, "Исправил, пишу текстом")))
	if len(group) != 1 || len(rest) != 1 || rest[0].Form["text"] != supportConfirm || linkCount(t) != "1" || !noAwaiting(t, 1001) {
		t.Fatalf("текст после отказов: группа %d, ответ %+v, %s", len(group), rest, supportState(t))
	}

	// Вне режима бот молчит: нет отметки, стоит отметка города, окно истекло — ни одного вызова, база та же.
	toCityScreen(t, b, api, 2001, "🇧🇾 Беларусь")
	feed(b, api, commandUpdate(1002, "/support"))
	setNow(b, 3*time.Minute+awaitSupportFor) // отметка 1002 истекла
	for _, id := range []int64{1001, 2001, 1002} {
		for _, kind := range []string{"photo", "document", "sticker", "photo+caption"} {
			before, support := dbState(t), supportState(t)
			calls := feed(b, api, mediaUpdate(id, kind))
			if len(calls) != 0 {
				t.Fatalf("%s от %d вне режима: вызовы %+v, want 0", kind, id, calls)
			}
			if dbState(t) != before || supportState(t) != support {
				t.Fatalf("%s от %d вне режима изменило базу", kind, id)
			}
		}
	}

	// Подпись вложения никуда не передаётся и не логируется.
	noLeak(t, "логи", logs.String(), "приметная подпись")
	noLeak(t, "таблицы поддержки", supportState(t), "приметная подпись")
}

// TestS30SupportRejects — S-30: отказы по длине и лимиту (D-11, D-12, D-14).
func TestS30SupportRejects(t *testing.T) {
	_, api, b := supportEnv(t)
	enter := func(id int64) { feed(b, api, commandUpdate(id, "/support")) }
	utf16Len := func(s string) int { return len(utf16.Encode([]rune(s))) }
	// accepted: текст ушёл в группу одним сообщением, пользователь получил подтверждение.
	accepted := func(id int64, text string) {
		t.Helper()
		calls := feed(b, api, textUpdate(id, text))
		group, rest := split(calls)
		if len(group) != 1 || len(rest) != 1 || rest[0].Form["text"] != supportConfirm {
			t.Fatalf("обращение из %d знаков: вызовы %+v, want группа и подтверждение", utf16Len(text), calls)
		}
		if n := utf16Len(group[0].Form["text"]); n > 4096 {
			t.Fatalf("сообщение в группе — %d единиц UTF-16, want ≤ 4096", n)
		}
	}
	refused := func(id int64, text, want string) []call {
		t.Helper()
		calls := feed(b, api, textUpdate(id, text))
		if group, _ := split(calls); len(calls) != 1 || len(group) != 0 || calls[0].Form["text"] != want {
			t.Fatalf("отказ: вызовы %+v, want один %q пользователю", calls, want)
		}
		return calls
	}
	until := fixtureNow.Add(awaitSupportFor)

	// 1. 2001 буква — R-1; отметка остаётся, связок нет.
	enter(1001)
	matchSnapshot(t, "s30_too_long", renderCall(t, refused(1001, strings.Repeat("а", 2001), supportTooLong)[0]))
	if !awaitingUntil(t, 1001, until) || linkCount(t) != "0" {
		t.Fatalf("после R-1: %s", supportState(t))
	}

	// 2. Ровно 2000 букв проходят (режим остался от шага 1); 1000 эмодзи (2000 единиц) проходят,
	// 1001 эмодзи (2002 единицы) — отказ; после отказа допустимый текст проходит.
	accepted(1001, strings.Repeat("а", 2000))
	enter(1001)
	accepted(1001, strings.Repeat("😀", 1000))
	enter(1001)
	refused(1001, strings.Repeat("😀", 1001), supportTooLong)
	accepted(1001, "ок")
	if n := linkCount(t); n != "3" {
		t.Fatalf("связок %s, want 3", n)
	}

	// 3. Пятое обращение проходит, шестое (через минуту) — R-3: связок пять, отметка снята, текст идёт в поиск.
	for i := 0; i < 2; i++ {
		enter(1001)
		accepted(1001, "ещё одно")
	}
	setNow(b, time.Minute)
	enter(1001)
	limited := refused(1001, "шестое", supportLimited)
	matchSnapshot(t, "s30_limit", renderCall(t, limited[0]))
	if n := linkCount(t); n != "5" || !noAwaiting(t, 1001) {
		t.Fatalf("после R-3: связок %s, %s", n, supportState(t))
	}
	if calls := feed(b, api, textUpdate(1001, "марафон москва")); len(calls) != 1 || calls[0].Form["chat_id"] != "1001" {
		t.Fatalf("текст после лимита: %+v, want один ответ поиска", calls)
	}

	// 5. Лимит — по пользователю: 1002 в то же время проходит.
	enter(1002)
	accepted(1002, "я другой пользователь")

	// 4. Окно: за секунду до 60 минут от первых обращений — ещё отказ; ровно через 60 минут — проходит.
	setNow(b, time.Hour-time.Second)
	enter(1001)
	refused(1001, "рано", supportLimited)
	setNow(b, time.Hour)
	enter(1001)
	accepted(1001, "теперь можно")
	if n := dbScalar(t, `SELECT count(*)::text FROM support_messages WHERE tg_id = 1001`); n != "6" {
		t.Fatalf("связок пользователя 1001: %s, want 6", n)
	}
}

// TestS31SupportSendFailure — S-31: сбой отправки в группу (D-17, D-15, T-11-17).
func TestS31SupportSendFailure(t *testing.T) {
	shortRetry(t)
	var mode atomic.Value // что отвечает группа на n-ю отправку: ok | 500 | 403 | 429once | 429 | migrate
	mode.Store("ok")
	const migrateTo = "-1009876543210"
	reply := func(int) string {
		switch mode.Load().(string) {
		case "500":
			return apiErr(500, "Internal Server Error: SECRET7 detail", 0)
		case "403":
			return apiErr(403, "Forbidden: bot was kicked from the supergroup chat SECRET7", 0)
		case "429":
			return apiErr(429, "Too Many Requests: retry after 1 SECRET7", 1)
		case "migrate":
			return `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat SECRET7","parameters":{"migrate_to_chat_id":` + migrateTo + `}}`
		}
		return ""
	}
	// 429once: первая отправка после включения режима — 429, повтор успешен.
	var once atomic.Bool
	_, api, b := supportEnv(t, func(c call, n int) string {
		if mode.Load().(string) == "429once" && c.Method == "sendMessage" && c.Form["chat_id"] == strconv.FormatInt(supportGroup, 10) && !once.Swap(true) {
			return apiErr(429, "Too Many Requests: retry after 1 SECRET7", 1)
		}
		return ""
	}, groupSends(func(n int) string { return reply(n) }))
	logs := captureLogs(t)
	const secret = "Приметный7текст"
	const failAt = 3 * time.Minute // сбой случается позже входа: окно возвращается от момента сбоя
	until := fixtureNow.Add(failAt + awaitSupportFor)
	enter := func() { feed(b, api, named(commandUpdate(1001, "/support"), "Анна", "Петрова", "anna_p")) }
	send := func() []call {
		return feed(b, api, named(textUpdate(1001, secret), "Анна", "Петрова", "anna_p"))
	}
	// failed: одна попытка (или wantTries) в группу, R-4 пользователю, подтверждения и связки нет, отметка возвращена.
	failed := func(wantTries int) {
		t.Helper()
		group, rest := split(send())
		if len(group) != wantTries || len(rest) != 1 || rest[0].Form["text"] != supportSendError || linkCount(t) != "0" {
			t.Fatalf("сбой: в группу %d (want %d), прочее %+v, связок %s", len(group), wantTries, rest, linkCount(t))
		}
		matchSnapshot(t, "s31_failure", renderCall(t, rest[0]))
		if !awaitingUntil(t, 1001, until) {
			t.Fatalf("после сбоя отметки на %v нет: %s", until, supportState(t))
		}
	}

	// 1. Ошибка 500: R-4, отметка возвращена; повторное сообщение в то же окно проходит.
	enter()
	mode.Store("500")
	setNow(b, failAt)
	failed(1)
	mode.Store("ok")
	group, rest := split(send())
	if len(group) != 1 || len(rest) != 1 || rest[0].Form["text"] != supportConfirm || linkCount(t) != "1" {
		t.Fatalf("повтор после сбоя: группа %d, прочее %+v, связок %s", len(group), rest, linkCount(t))
	}
	dbExec(t, `DELETE FROM support_messages`)

	// 2. 403 (бота исключили из группы) — то же R-4.
	enter()
	mode.Store("403")
	failed(1)

	// 3. 429: один повтор; успешный — обращение принято; два 429 подряд — R-4.
	mode.Store("429once")
	group, rest = split(send())
	if len(group) != 2 || len(rest) != 1 || rest[0].Form["text"] != supportConfirm || linkCount(t) != "1" {
		t.Fatalf("429 и успешный повтор: группа %d, прочее %+v, связок %s", len(group), rest, linkCount(t))
	}
	dbExec(t, `DELETE FROM support_messages`)
	enter()
	mode.Store("429")
	failed(2)

	// 4. Перенос группы: R-4, одна запись Error с новым идентификатором, без текста.
	mode.Store("migrate")
	failed(1)
	if n := strings.Count(logs.String(), "level=ERROR"); n != 1 || !strings.Contains(logs.String(), `msg="support group migrated" new_chat_id=`+migrateTo) {
		t.Fatalf("перенос группы: ожидалась одна запись Error с идентификатором %s:\n%s", migrateTo, logs)
	}

	// 5. Сбои не расходуют лимит: после пяти сбоев подряд первая отправка проходит, до неё связок нет.
	mode.Store("500")
	for i := 0; i < 5; i++ {
		failed(1)
	}
	mode.Store("ok")
	if group, _ := split(send()); len(group) != 1 || linkCount(t) != "1" {
		t.Fatalf("после пяти сбоев: группа %d, связок %s", len(group), linkCount(t))
	}

	// В логах — только вид ошибки: ни текста обращения, ни имён, ни Telegram ID, ни описания ошибки Bot API.
	noLeak(t, "логи", logs.String(), secret, "Анна", "Петрова", "anna_p", "SECRET7")
	if idRE(1001).MatchString(logs.String()) {
		t.Errorf("в логах есть Telegram ID:\n%s", logs)
	}
	for _, kind := range []string{`kind=other`, `kind=blocked`, `kind="rate limited"`} {
		if !strings.Contains(logs.String(), kind) {
			t.Errorf("в логе нет вида ошибки %s:\n%s", kind, logs)
		}
	}
	noLeak(t, "таблицы поддержки", supportState(t), secret, "Анна", "anna_p")
}

// --- Сторона группы (11-04): сценарии S-32…S-34 ---

// strangerGroup — чужая группа, куда добавили бота: номера её сообщений могут
// совпасть с номерами обращений в настоящей группе.
const strangerGroup int64 = -1005550001111

// inChat переносит сообщение-апдейт в групповой чат: from — отправитель (0 — без
// From, как у анонимного администратора группы), id — номер сообщения, replyTo —
// номер сообщения, на которое оно отвечает (0 — не reply).
func inChat(u *models.Update, chatID int64, typ models.ChatType, from int64, id, replyTo int) *models.Update {
	m := u.Message
	m.Chat = models.Chat{ID: chatID, Type: typ}
	m.ID = id
	m.From, m.SenderChat = &models.User{ID: from}, nil
	if from == 0 {
		m.From, m.SenderChat = nil, &models.Chat{ID: chatID, Type: typ}
	}
	m.ReplyToMessage = nil
	if replyTo != 0 {
		m.ReplyToMessage = &models.Message{ID: replyTo, Chat: m.Chat}
	}
	return u
}

// groupReply — текстовый ответ участника группы поддержки на сообщение replyTo.
func groupReply(from int64, id, replyTo int, text string) *models.Update {
	return inChat(textUpdate(0, text), supportGroup, models.ChatTypeSupergroup, from, id, replyTo)
}

// groupMedia — ответ участника группы вложением указанного вида.
func groupMedia(from int64, id, replyTo int, kind string) *models.Update {
	return inChat(mediaUpdate(0, kind), supportGroup, models.ChatTypeSupergroup, from, id, replyTo)
}

// toUser — вызовы, ушедшие в личный чат пользователя 1001.
func toUser(calls []call) (out []call) {
	for _, c := range calls {
		if c.Form["chat_id"] == "1001" {
			out = append(out, c)
		}
	}
	return out
}

// onlyFields проверяет, что в запросе только перечисленные поля: пользователю
// уходит текст ответа и кнопка, ничего другого (D-06, D-11).
func onlyFields(t *testing.T, c call, fields ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, f := range fields {
		allowed[f] = true
	}
	for k := range c.Form {
		if !allowed[k] {
			t.Errorf("%s: лишнее поле %q (%q)", c.Method, k, c.Form[k])
		}
	}
}

// replyParams разбирает reply_parameters вызова.
func replyParams(t *testing.T, c call) models.ReplyParameters {
	t.Helper()
	var rp models.ReplyParameters
	if err := json.Unmarshal([]byte(c.Form["reply_parameters"]), &rp); err != nil {
		t.Fatalf("reply_parameters %q: %v", c.Form["reply_parameters"], err)
	}
	return rp
}

// userAPI — режим ответов фиктивного Bot API на отправку пользователю 1001:
// "" и ok — успех, 403, 400, 500 — ошибка, 429once — первый вызов 429 с
// retry_after 1, 429long — всегда 429 с retry_after больше предела ожидания.
// В описании каждой ошибки — приметный текст для проверки логов.
type userAPI struct {
	mode atomic.Value
	once atomic.Bool
}

func (u *userAPI) set(mode string) {
	u.once.Store(false)
	u.mode.Store(mode)
}

func (u *userAPI) script(c call, _ int) string {
	if c.Method != "sendMessage" || c.Form["chat_id"] != "1001" {
		return ""
	}
	mode, _ := u.mode.Load().(string)
	switch mode {
	case "403":
		return apiErr(403, "Forbidden: bot was blocked by the user SECRET7", 0)
	case "400":
		return apiErr(400, "Bad Request: chat not found SECRET7", 0)
	case "500":
		return apiErr(500, "Internal Server Error: SECRET7", 0)
	case "429once":
		if !u.once.Swap(true) {
			return apiErr(429, "Too Many Requests: retry after 1 SECRET7", 1)
		}
	case "429long":
		return apiErr(429, "Too Many Requests: retry after 120 SECRET7", 120)
	}
	return ""
}

// groupBroken — ответ группы на каждую отправку в неё, пока в body непустая строка
// (тело ответа Bot API); пусто — успех.
func groupBroken(body *atomic.Value) script {
	return func(c call, _ int) string {
		if c.Method != "sendMessage" || c.Form["chat_id"] != strconv.FormatInt(supportGroup, 10) {
			return ""
		}
		b, _ := body.Load().(string)
		return b
	}
}

// openRequest — обращение пользователя id в группу: возвращает номер сообщения
// бота в группе (его связка ведёт к id).
func openRequest(t *testing.T, b *Bot, api *fakeAPI, id int64, text string) int {
	t.Helper()
	feed(b, api, commandUpdate(id, "/support"))
	feed(b, api, textUpdate(id, text))
	n, err := strconv.Atoi(dbScalar(t, `SELECT message_id::text FROM support_messages WHERE tg_id = $1 ORDER BY message_id DESC LIMIT 1`, id))
	if err != nil {
		t.Fatalf("обращение %d не создало связку: %v", id, err)
	}
	return n
}

// replyEnv — бот с группой и администратором 3001; пользователь 1001 уже написал
// обращение (сообщение M в группе). scripts идут раньше ответов группы.
func replyEnv(t *testing.T, scripts ...script) (*fakeAPI, *Bot, int) {
	t.Helper()
	s := storetest.Open(t)
	api := fakeTelegram(t, append(scripts, groupReplies())...)
	b := newBotWith(t, api, s, Config{Token: "123:TEST", SupportChatID: supportGroup, Admins: []int64{3001}})
	return api, b, openRequest(t, b, api, 1001, "Не открывается карточка забега")
}

// TestS32SupportReply — S-32: ответ участника группы доходит до автора обращения
// новым сообщением бота с кнопкой «💬 Ответить» (D-04, D-06, D-11, D-16, T-11-19,
// T-11-23, T-11-40).
func TestS32SupportReply(t *testing.T) {
	logs := captureLogs(t)
	var user userAPI
	api, b, m := replyEnv(t, user.script)
	olga := func(u *models.Update) *models.Update { return named(u, "Ольга", "Сидорова", "olga_sup") }
	const secret = "Приметный7ответ"
	delivered := 0
	// deliver: один вызов Bot API — сообщение автору обращения с текстом как есть, без
	// разметки, с одной кнопкой; в группу ничего; таблицы не изменились.
	deliver := func(u *models.Update, text string) call {
		t.Helper()
		state, support := dbState(t), supportState(t)
		calls := feed(b, api, u)
		if len(calls) != 1 || calls[0].Method != "sendMessage" || calls[0].Form["chat_id"] != "1001" || calls[0].Form["text"] != text {
			t.Fatalf("ответ: вызовы %+v, want одно сообщение автору обращения с текстом ответа", calls)
		}
		onlyFields(t, calls[0], "chat_id", "text", "reply_markup", "link_preview_options")
		if btns := calls[0].buttons(t); len(btns) != 1 || btns[0].Text != "💬 Ответить" || btns[0].CallbackData != "h:r" {
			t.Fatalf("кнопки ответа: %+v, want одна «💬 Ответить» → h:r", btns)
		}
		formsLeak(t, calls, "Ольга", "Сидорова", "olga_sup", "2001", "3001")
		if dbState(t) != state || supportState(t) != support {
			t.Errorf("ответ изменил таблицы")
		}
		delivered++
		return calls[0]
	}

	// 1. Текст с переводом строки доходит дословно.
	text := "Здравствуйте!\nОткройте /start и попробуйте ещё раз."
	reply := deliver(olga(groupReply(2001, 900, m, text)), text)
	matchSnapshot(t, "s32_reply", renderCall(t, reply))

	// 2. HTML-символы не разбираются как разметка: текст как введён, parse_mode пуст.
	html := `<b>X</b> & "Y"`
	matchSnapshot(t, "s32_reply_html", renderCall(t, deliver(olga(groupReply(2001, 901, m, html)), html)))

	// 3. Второй ответ на то же обращение; ответ анонимного администратора (без From) и
	// администратора из ADMIN_TG_IDS доставляются так же (D-04).
	deliver(olga(groupReply(2002, 902, m, "второй ответ "+secret)), "второй ответ "+secret)
	deliver(groupReply(0, 903, m, "от анонимного администратора"), "от анонимного администратора")
	deliver(groupReply(3001, 904, m, "от администратора"), "от администратора")

	// 4. «💬 Ответить»: приглашение новым сообщением, сообщение с ответом остаётся, отметка стоит.
	calls := raw(b, api, 1001, reply.buttons(t)[0].CallbackData)
	matchSnapshot(t, "s32_invite", renderAll(t, calls))
	if len(calls) != 2 || calls[0].Method != "sendMessage" || calls[1].Method != "answerCallbackQuery" || calls[1].Form["text"] != "" {
		t.Fatalf("«Ответить»: вызовы %+v, want приглашение новым сообщением и один пустой ответ на callback", calls)
	}
	if !awaitingUntil(t, 1001, fixtureNow.Add(awaitSupportFor)) {
		t.Fatalf("после «Ответить» отметки нет: %s", supportState(t))
	}

	// 5. Следующий текст — новое обращение с новой связкой; режим снят.
	group, rest := split(feed(b, api, textUpdate(1001, "Спасибо, ещё вопрос")))
	if len(group) != 1 || len(rest) != 1 || rest[0].Form["text"] != supportConfirm || linkCount(t) != "2" || !noAwaiting(t, 1001) {
		t.Fatalf("новое обращение: группа %d, прочее %+v, %s", len(group), rest, supportState(t))
	}
	if m2 := dbScalar(t, `SELECT max(message_id)::text FROM support_messages`); m2 == strconv.Itoa(m) {
		t.Fatalf("новая связка с тем же номером %d", m)
	}

	// 6. Режима «чат» нет: текст без нажатия кнопки идёт в поиск, в группу не уходит.
	calls = feed(b, api, textUpdate(1001, "марафон москва"))
	if g, r := split(calls); len(g) != 0 || len(r) != 1 || r[0].Form["chat_id"] != "1001" {
		t.Fatalf("текст без кнопки: вызовы %+v, want один ответ поиска", calls)
	}

	// 7. Ответ на старое обращение (30 суток спустя): связка бессрочна (Q-6).
	setNow(b, 30*24*time.Hour)
	deliver(olga(groupReply(2001, 905, m, "ответ через месяц")), "ответ через месяц")

	// 8. Сбой доставки: пользователю ничего не доходит, в группу — одно сообщение ответом
	// на сообщение отвечавшего (подробно — S-33).
	user.set("500")
	calls = feed(b, api, olga(groupReply(2001, 906, m, "не дойдёт")))
	group, _ = split(calls)
	if len(group) != 1 || replyParams(t, group[0]).MessageID != 906 {
		t.Fatalf("сбой доставки: вызовы %+v, want одно сообщение в группу ответом на 906", calls)
	}

	// 9. D-15: в логах нет текста ответа, имени отвечавшего и Telegram ID; есть вид события.
	noLeak(t, "логи", logs.String(), secret, "Ольга", "Сидорова", "olga_sup", "Не открывается", "ещё вопрос", "SECRET7")
	for _, id := range []int64{1001, 2001, 2002, 3001} {
		if idRE(id).MatchString(logs.String()) {
			t.Errorf("в логах есть Telegram ID %d:\n%s", id, logs)
		}
	}
	if n := strings.Count(logs.String(), `event="reply sent"`); n != delivered {
		t.Errorf("записей о доставленном ответе %d, want %d:\n%s", n, delivered, logs)
	}
	t.Logf("TRACER reply=sent button=reply next=sent")
}

// TestS33ReplyNotDelivered — S-33: недоставленный и нетекстовый ответ не теряются
// молча, а отмечаются в группе под сообщением отвечавшего (D-18, D-11, T-11-22,
// T-11-24).
func TestS33ReplyNotDelivered(t *testing.T) {
	shortRetry(t)
	logs := captureLogs(t)
	var user userAPI
	var groupBody atomic.Value // что отвечает группа на отправку сообщения о недоставке; пусто — успех
	api, b, m := replyEnv(t, user.script, groupBroken(&groupBody))
	const secret = "Приметный7ответ"
	const caption = "приметная подпись"
	olga := func(u *models.Update) *models.Update { return named(u, "Ольга", "Сидорова", "olga_sup") }
	last := m // номер последнего сообщения бота в группе: каждое сообщение о недоставке получает следующий
	// notice: ответ отвечавшего (номер id) не дошёл до пользователя; в группу ушло ровно
	// одно сообщение ответом на сообщение отвечавшего, пользователю — attempts вызовов.
	notice := func(u *models.Update, id, attempts int, want string) call {
		t.Helper()
		calls := feed(b, api, u)
		group, _ := split(calls)
		if len(group) != 1 || len(toUser(calls)) != attempts || len(calls) != 1+attempts {
			t.Fatalf("недоставленный ответ: вызовы %+v, want %d к пользователю и одно сообщение в группу", calls, attempts)
		}
		last++
		if rp := replyParams(t, group[0]); rp.MessageID != id || !rp.AllowSendingWithoutReply {
			t.Fatalf("reply_parameters = %+v, want message_id %d (сообщение отвечавшего, не обращение %d) и разрешение отправки без ответа", rp, id, m)
		}
		if group[0].Form["text"] != want || group[0].Form["parse_mode"] != "" || group[0].Form["reply_markup"] != "" {
			t.Fatalf("сообщение в группе: %+v, want обычный текст %q без клавиатуры", group[0].Form, want)
		}
		formsLeak(t, group, secret, caption, "Ольга", "olga_sup") // сообщение в группу не повторяет ни ответ, ни имя
		if linkCount(t) != "1" {
			t.Fatalf("связка пропала: %s", supportState(t))
		}
		return group[0]
	}

	// 1. 403: «пользователь заблокировал бота» — ответом на сообщение отвечавшего.
	user.set("403")
	matchSnapshot(t, "s33_blocked", renderCall(t, notice(olga(groupReply(2001, 900, m, secret)), 900, 1, groupBlocked)))
	firstNotice := last

	// 2. Иная ошибка Bot API (400, 500) — «ошибка отправки».
	user.set("400")
	matchSnapshot(t, "s33_error", renderCall(t, notice(olga(groupReply(2001, 901, m, secret)), 901, 1, groupFailed)))
	user.set("500")
	notice(olga(groupReply(2001, 902, m, secret)), 902, 1, groupFailed)

	// 3. 429 с малым retry_after: один повтор, ответ доставлен, в группе ничего.
	user.set("429once")
	calls := feed(b, api, olga(groupReply(2001, 903, m, secret)))
	if group, _ := split(calls); len(group) != 0 || len(toUser(calls)) != 2 || len(calls) != 2 {
		t.Fatalf("429 и успешный повтор: вызовы %+v, want две попытки пользователю и ничего в группу", calls)
	}

	// 4. 429 с retry_after больше предела ожидания: без повтора, «ошибка отправки».
	user.set("429long")
	notice(olga(groupReply(2001, 904, m, secret)), 904, 1, groupFailed)

	// 5. Нетекстовый ответ (любое вложение, в том числе с подписью): пользователю ни одного
	// вызова, в группу — G-3 ответом на сообщение отвечавшего.
	user.set("ok")
	for i, kind := range []string{"photo+caption", "photo", "document", "sticker", "voice", "video"} {
		u := inChat(mediaUpdate(0, kind), supportGroup, models.ChatTypeSupergroup, 2001, 910+i, m)
		got := notice(named(u, "Ольга", "Сидорова", "olga_sup"), 910+i, 0, groupNotText)
		if i == 0 {
			matchSnapshot(t, "s33_nontext", renderCall(t, got))
		}
	}

	// 6. Ответ на сообщение бота о недоставке и о нетекстовом ответе (связки у них нет) — ноль вызовов.
	for i, id := range []int{firstNotice, last} {
		if calls := feed(b, api, groupReply(2002, 920+i, id, "ответ на служебное сообщение")); len(calls) != 0 {
			t.Fatalf("ответ на сообщение бота %d без связки: вызовы %+v, want 0", id, calls)
		}
	}

	// 7. Если сообщение в группу не отправилось (500, 429): в лог только вид ошибки, без паники
	// и без повторов; связка остаётся.
	user.set("403")
	for _, body := range []string{apiErr(500, "Internal Server Error: SECRET7", 0), apiErr(429, "Too Many Requests: retry after 1 SECRET7", 1)} {
		groupBody.Store(body)
		calls := feed(b, api, olga(groupReply(2001, 930, m, secret)))
		if group, _ := split(calls); len(group) != 1 || len(calls) != 2 {
			t.Fatalf("сбой сообщения в группу: вызовы %+v, want одна попытка в группу без повтора", calls)
		}
	}
	groupBody.Store("")

	// 8. Связка не удалена: пользователь разблокировал бота — следующий ответ на то же обращение доходит.
	user.set("ok")
	calls = feed(b, api, olga(groupReply(2001, 931, m, "после разблокировки")))
	if len(calls) != 1 || calls[0].Form["chat_id"] != "1001" || calls[0].Form["text"] != "после разблокировки" {
		t.Fatalf("ответ после разблокировки: вызовы %+v, want доставка пользователю", calls)
	}

	// D-15: в логах только вид события и вид ошибки.
	noLeak(t, "логи", logs.String(), secret, caption, "Ольга", "Сидорова", "olga_sup", "SECRET7", "Не открывается", "после разблокировки")
	for _, id := range []int64{1001, 2001, 2002} {
		if idRE(id).MatchString(logs.String()) {
			t.Errorf("в логах есть Telegram ID %d:\n%s", id, logs)
		}
	}
	for _, want := range []string{`kind=blocked`, `kind=other`, `kind="rate limited"`, `event="reply not text"`, `msg="send group notice"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("в логе нет %s:\n%s", want, logs)
		}
	}
	noLeak(t, "таблицы поддержки", supportState(t), secret, "Ольга", "olga_sup")
}

// groupCallback — нажатие кнопки в чате группы поддержки (сообщение доступно).
func groupCallback(from int64, data string) *models.Update {
	u := cbUpdate(from, 7, data)
	u.CallbackQuery.Message.Message.Chat = models.Chat{ID: supportGroup, Type: models.ChatTypeSupergroup}
	return u
}

// TestS34GroupIgnored — S-34: в группе работает только ответ на обращение; команды,
// поиск, кнопки, чужие и служебные сообщения игнорируются (D-01, D-05, T-11-20,
// T-11-21, T-11-26, Q-7).
func TestS34GroupIgnored(t *testing.T) {
	api, b, m := replyEnv(t)
	logs := captureLogs(t)
	// Номера сообщений ниже не совпадают с обращением m, кроме тех шагов, где совпадение — суть проверки.
	// quiet: ни одного вызова Bot API, ни users, ни профиль, ни таблицы поддержки не меняются.
	quiet := func(what string, u *models.Update) {
		t.Helper()
		state, support := dbState(t), supportState(t)
		if calls := feed(b, api, u); len(calls) != 0 {
			t.Fatalf("%s: вызовы %+v, want 0", what, calls)
		}
		if dbState(t) != state || supportState(t) != support {
			t.Fatalf("%s изменило базу:\n%s", what, supportState(t))
		}
	}
	foreign := func() int { return strings.Count(logs.String(), "unconfigured group") }
	members := []int64{2001, 3001} // участник и администратор из ADMIN_TG_IDS

	// 1. Обычные сообщения, в том числе то, что в личном чате было бы поиском.
	for _, from := range members {
		quiet("обычное сообщение", inChat(textUpdate(0, "марафон москва"), supportGroup, models.ChatTypeSupergroup, from, 800, 0))
	}

	// 1а. Участник 2001 в личном чате ждёт обращения (режим ввода): в группе его текст не становится
	// обращением, а сообщение без текста не получает отказ R-2 — тот только для личного чата.
	feed(b, api, commandUpdate(2001, "/support"))
	quiet("текст участника в режиме ввода", inChat(textUpdate(0, "Это не обращение"), supportGroup, models.ChatTypeSupergroup, 2001, 802, 0))
	for _, kind := range []string{"photo", "photo+caption", "document", "sticker"} {
		quiet("вложение в группе: "+kind, inChat(mediaUpdate(0, kind), supportGroup, models.ChatTypeSupergroup, 2001, 803, 0))
	}
	if !awaitingUntil(t, 2001, fixtureNow.Add(awaitSupportFor)) {
		t.Fatalf("отметка участника 2001 изменилась: %s", supportState(t))
	}

	// 2. Команды — и у участника, и у администратора; в том числе вида /команда@имя_бота.
	for _, cmd := range []string{"/start", "/support", "/profile", "/runs", "/stats", "/dupes", "/start@run_bot"} {
		for _, from := range members {
			quiet(cmd, inChat(commandUpdate(0, cmd), supportGroup, models.ChatTypeSupergroup, from, 801, 0))
		}
	}

	// 3. Кнопки: ровно один пустой ответ на callback, обработчик не вызывается.
	inaccessible := groupCallback(2001, "h:o")
	inaccessible.CallbackQuery.Message = models.MaybeInaccessibleMessage{
		Type: models.MaybeInaccessibleMessageTypeInaccessibleMessage,
		InaccessibleMessage: &models.InaccessibleMessage{
			Chat: models.Chat{ID: supportGroup, Type: models.ChatTypeSupergroup}, MessageID: 9},
	}
	for i, u := range []*models.Update{groupCallback(2001, "f:s:v1"), groupCallback(3001, "h:o"), groupCallback(3001, "r:1"), inaccessible} {
		state, support := dbState(t), supportState(t)
		toast := onlyToast(t, "кнопка в группе", feed(b, api, u))
		if toast.Form["text"] != "" {
			t.Fatalf("кнопка в группе: уведомление %q, want пустое", toast.Form["text"])
		}
		if i == 0 {
			matchSnapshot(t, "s34_callback", renderCall(t, toast))
		}
		if dbState(t) != state || supportState(t) != support {
			t.Fatalf("кнопка в группе изменила базу")
		}
	}

	// 4. Ответ на сообщение участника, на сообщение бота без связки (номер за пределами связок).
	quiet("ответ на сообщение участника", groupReply(2001, 810, 555, "согласен"))
	quiet("ответ на сообщение бота без связки", groupReply(2001, 811, m+50, "ответ в пустоту"))

	// 5. Служебные сообщения: закрепление (reply на обращение), вход и выход участников,
	// смена названия и фото — ни вызовов, ни G-3 (дополнение А).
	service := func(set func(m *models.Message)) *models.Update {
		u := inChat(textUpdate(0, ""), supportGroup, models.ChatTypeSupergroup, 2001, 820, m)
		set(u.Message)
		return u
	}
	quiet("закрепление", service(func(m *models.Message) {
		m.PinnedMessage = &models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage, Message: &models.Message{ID: m.ReplyToMessage.ID}}
	}))
	quiet("вступление", service(func(m *models.Message) { m.NewChatMembers = []models.User{{ID: 5}} }))
	quiet("выход", service(func(m *models.Message) { m.LeftChatMember = &models.User{ID: 5} }))
	quiet("смена названия", service(func(m *models.Message) { m.NewChatTitle = "Новое название" }))
	quiet("смена фото", service(func(m *models.Message) { m.NewChatPhoto = []models.PhotoSize{{FileID: "f"}} }))
	quiet("удаление фото", service(func(m *models.Message) { m.DeleteChatPhoto = true }))

	// 6. Перенос группы: ни вызовов, одна запись Error с новым идентификатором.
	const migrateTo = int64(-1009876543210)
	migrate := inChat(textUpdate(0, ""), supportGroup, models.ChatTypeSupergroup, 0, 821, 0)
	migrate.Message.MigrateToChatID = migrateTo
	quiet("перенос группы", migrate)
	if n := strings.Count(logs.String(), "level=ERROR"); n != 1 || !strings.Contains(logs.String(), `msg="support group migrated" new_chat_id=`+strconv.FormatInt(migrateTo, 10)) {
		t.Fatalf("перенос группы: ожидалась одна запись Error с новым идентификатором:\n%s", logs)
	}
	if foreign() != 0 {
		t.Fatalf("сообщения из настроенной группы попали в лог о чужой группе:\n%s", logs)
	}

	// 7. Чужая группа (Q-7): текст, команда и reply на сообщение с тем же номером, что у обращения —
	// ни вызовов, ни ответа пользователю; на каждое сообщение одна запись Info с идентификатором и
	// типом чата, без имён и текста.
	for _, typ := range []models.ChatType{models.ChatTypeSupergroup, models.ChatTypeGroup} {
		before := foreign()
		quiet("текст в чужой группе", named(inChat(textUpdate(0, "Приметныйтекст9чужой"), strangerGroup, typ, 2001, 830, 0), "Чужоеимя", "", "alien_user"))
		quiet("команда в чужой группе", inChat(commandUpdate(0, "/support"), strangerGroup, typ, 3001, 831, 0))
		quiet("ответ в чужой группе", named(inChat(textUpdate(0, "Приметныйтекст9чужой"), strangerGroup, typ, 2001, 832, m), "Чужоеимя", "", "alien_user"))
		if got := foreign() - before; got != 3 {
			t.Fatalf("чужая группа %s: записей лога %d, want 3 (по одной на сообщение):\n%s", typ, got, logs)
		}
		if want := "chat_id=" + strconv.FormatInt(strangerGroup, 10) + " chat_type=" + string(typ); strings.Count(logs.String(), want) < 3 {
			t.Fatalf("в логе нет %q:\n%s", want, logs)
		}
	}
	noLeak(t, "логи", logs.String(), "Приметныйтекст9чужой", "Чужоеимя", "alien_user", "марафон", "2001", "3001")
	if strings.Count(logs.String(), "level=INFO") != foreign() {
		t.Fatalf("в логе есть Info, кроме записей о чужой группе:\n%s", logs)
	}

	// 8. После всего перечисленного ответ участника на обращение доходит (H-4).
	calls := feed(b, api, groupReply(2001, 840, m, "Проверка"))
	if len(calls) != 1 || calls[0].Form["chat_id"] != "1001" {
		t.Fatalf("ответ после шума: вызовы %+v, want одно сообщение пользователю 1001", calls)
	}
	matchSnapshot(t, "s34_reply_after_noise", renderCall(t, calls[0]))

	// 9. Контроль: в личном чате те же команды и текст работают.
	if calls := feed(b, api, commandUpdate(3001, "/runs")); len(calls) != 1 || calls[0].Form["chat_id"] != "3001" {
		t.Fatalf("/runs администратора в личном чате: вызовы %+v, want ответ", calls)
	}
	if calls := feed(b, api, commandUpdate(1001, "/support")); len(calls) != 1 || calls[0].Form["text"] != inviteText {
		t.Fatalf("/support в личном чате: вызовы %+v, want приглашение", calls)
	}
	if calls := feed(b, api, textUpdate(1002, "марафон москва")); len(calls) != 1 || calls[0].Form["chat_id"] != "1002" {
		t.Fatalf("текст в личном чате: вызовы %+v, want ответ поиска", calls)
	}
	if calls := raw(b, api, 1001, "f:s"); len(calls) != 2 {
		t.Fatalf("кнопка в личном чате: вызовы %+v, want правка и ответ", calls)
	}
}

// supportDialog — полный диалог с приметными значениями: имя, фамилия, username и
// Telegram ID пользователя, текст обращения, имя отвечавшего, текст ответа,
// приметный текст в описаниях ошибок Bot API. Пройдены вход кнопкой и командой,
// «Отмена», обращение текстом, отказы по длине и на нетекстовое сообщение, лимит,
// сбой отправки в группу, доставленный и недоставленный ответ, нетекстовый ответ,
// игнорируемые сообщения группы и кнопка h: вне грамматики. Возвращает логи.
func supportDialog(t *testing.T) fmt.Stringer {
	t.Helper()
	shortRetry(t)
	logs := captureLogs(t)
	var user userAPI
	var groupBody atomic.Value
	s := storetest.Open(t)
	api := fakeTelegram(t, user.script, groupBroken(&groupBody), groupReplies())
	b := newBotWith(t, api, s, Config{Token: "123:TEST", SupportChatID: supportGroup, Admins: []int64{3001}})
	anna := func(u *models.Update) *models.Update {
		return named(u, "Секретимя", "Секретфамилия", "secret_user7")
	}
	resp := func(u *models.Update) *models.Update {
		return named(u, "Ответчикимя", "Ответчикфамилия", "resp_user8")
	}
	seedSkipped(t, b, api, 1001) // строки users и профиля: проверка таблиц не пуста

	// Вход кнопкой, отказы по длине и на нетекстовое сообщение, «Отмена».
	raw(b, api, 1001, "h:o")
	feed(b, api, anna(textUpdate(1001, strings.Repeat("Длинный7", 300))))
	feed(b, api, anna(mediaUpdate(1001, "photo+caption")))
	raw(b, api, 1001, "h:x")

	// Вход командой и пять обращений; шестое — отказ по лимиту.
	for i := 0; i < 6; i++ {
		feed(b, api, anna(commandUpdate(1001, "/support")))
		feed(b, api, anna(textUpdate(1001, "Приметныйтекст7обращения")))
	}
	if n := linkCount(t); n != "5" {
		t.Fatalf("после лимита связок %s, want 5", n)
	}
	m, err := strconv.Atoi(dbScalar(t, `SELECT min(message_id)::text FROM support_messages`))
	if err != nil {
		t.Fatal(err)
	}

	// Сбой отправки в группу (у другого пользователя: лимит 1001 исчерпан).
	groupBody.Store(apiErr(500, "Internal Server Error: SECRET7", 0))
	feed(b, api, commandUpdate(1002, "/support"))
	feed(b, api, textUpdate(1002, "Приметныйтекст7обращения"))
	groupBody.Store("")

	// Ответы: доставленный, при блокировке бота, при иной ошибке, нетекстовый.
	feed(b, api, resp(groupReply(2002, 900, m, "Приметныйтекст8ответа")))
	user.set("403")
	feed(b, api, resp(groupReply(2002, 901, m, "Приметныйтекст8ответа")))
	user.set("500")
	feed(b, api, resp(groupReply(2002, 902, m, "Приметныйтекст8ответа")))
	user.set("ok")
	feed(b, api, resp(groupMedia(2002, 903, m, "photo+caption")))

	// Игнорируемое в группе: сообщение, команда, кнопка, ответ без связки, чужая группа, перенос.
	feed(b, api, resp(inChat(textUpdate(0, "Приметныйтекст8ответа"), supportGroup, models.ChatTypeSupergroup, 2002, 910, 0)))
	feed(b, api, resp(inChat(commandUpdate(0, "/support"), supportGroup, models.ChatTypeSupergroup, 2002, 911, 0)))
	feed(b, api, groupCallback(2002, "h:o"))
	feed(b, api, resp(groupReply(2002, 912, m+50, "Приметныйтекст8ответа")))
	feed(b, api, resp(inChat(textUpdate(0, "Приметныйтекст8ответа"), strangerGroup, models.ChatTypeSupergroup, 2002, 913, m)))
	migrate := inChat(textUpdate(0, ""), supportGroup, models.ChatTypeSupergroup, 0, 914, 0)
	migrate.Message.MigrateToChatID = -1009876543210
	feed(b, api, migrate)

	// Кнопка h: вне грамматики.
	raw(b, api, 1001, "h:z:secret_cb7")
	return logs
}

// dialogLeaks — приметные значения supportDialog, которых не должно быть ни в логах,
// ни в таблицах.
var dialogLeaks = []string{"Секретимя", "Секретфамилия", "secret_user7", "Приметныйтекст7обращения", "Длинный7",
	"Ответчикимя", "Ответчикфамилия", "resp_user8", "Приметныйтекст8ответа", "приметная подпись", "secret_cb7", "SECRET7"}

// TestSupportNoTextInLogs — D-15, T-11-25: в логах полного диалога нет текста
// обращений и ответов, имён, username, Telegram ID и описаний ошибок Bot API; есть
// вид события и вид ошибки.
func TestSupportNoTextInLogs(t *testing.T) {
	logs := supportDialog(t).String()
	noLeak(t, "логи", logs, dialogLeaks...)
	for _, id := range []int64{1001, 1002, 2002, 3001} {
		if idRE(id).MatchString(logs) {
			t.Errorf("в логах есть Telegram ID %d:\n%s", id, logs)
		}
	}
	for _, want := range []string{`event=sent`, `event=limited`, `event="reply sent"`, `event="reply not text"`,
		`msg="support send failed" kind=other`, `kind=blocked`, `msg="support reply failed" kind=other`,
		`msg="support group migrated"`, `msg="message from unconfigured group"`, `msg="callback stale" prefix=h`} {
		if !strings.Contains(logs, want) {
			t.Errorf("в логе нет записи %s:\n%s", want, logs)
		}
	}
}

// TestSupportStoresOnlyLinks — D-13, T-11-25: после полного диалога в таблицах
// поддержки только идентификаторы и время, а ни в них, ни в users и профилях нет
// текстов, имён и username.
func TestSupportStoresOnlyLinks(t *testing.T) {
	supportDialog(t)
	if cols := dbScalar(t, `SELECT string_agg(column_name || ':' || data_type, ',' ORDER BY column_name) FROM information_schema.columns WHERE table_name = 'support_messages'`); cols !=
		"chat_id:bigint,created_at:timestamp with time zone,message_id:integer,tg_id:bigint" {
		t.Errorf("столбцы support_messages: %s, want только идентификаторы и время", cols)
	}
	if cols := dbScalar(t, `SELECT string_agg(column_name || ':' || data_type, ',' ORDER BY column_name) FROM information_schema.columns WHERE table_name = 'support_awaiting'`); cols !=
		"expires_at:timestamp with time zone,tg_id:bigint" {
		t.Errorf("столбцы support_awaiting: %s, want только идентификатор и время", cols)
	}
	if n := linkCount(t); n != "5" {
		t.Errorf("связок %s, want 5 (сбой отправки и лимит связок не создают)", n)
	}
	for what, state := range map[string]string{"таблицы поддержки": supportState(t), "users": usersState(t), "user_profiles": profilesState(t), "всё": dbState(t)} {
		noLeak(t, what, state, dialogLeaks...)
	}
}
