package bot

import (
	"context"
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

	// 1. Главное меню из четырёх кнопок; отметки ещё нет.
	seedSkipped(t, b, api, 1001)
	menu := startAs(t, b, api, 1001)
	matchSnapshot(t, "s26_menu", renderCall(t, menu))
	if got := datas(t, menu); len(got) != 4 || got[3] != "h:o" {
		t.Fatalf("кнопки меню: %v, want четыре, последняя h:o", got)
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

	// 1. «Отмена»: правка на главное меню из четырёх кнопок, отметки нет, текст идёт в поиск.
	feed(b, api, commandUpdate(1001, "/support"))
	cancel := step(t, b, api, 1001, "h:x", "s29_cancel")
	if got := datas(t, cancel); len(got) != 4 || got[3] != "h:o" {
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

	// 1. Главное меню из трёх кнопок — побайтно как прежний эталон.
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
	if c := sayText(t, b, api, 1001, long); c.Form["text"] != "Нажмите /start, чтобы найти забег" {
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
