package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

// Сценарии контракта Phase 10 (10-UX-CONTRACT.md, раздел 7): мастер рассылки
// администратора, отправка через очередь, отказ из сообщения, экран
// «Уведомления» и вопрос в конце анкеты. Администратор — пользователь 1.

// bcRig — каталог сценариев, пользователи 1, 42, 43 и 44 после /start, без
// избранного: очередь занята только рассылками.
func bcRig(t *testing.T) *notifyRig {
	t.Helper()
	r := newNotifyRig(t)
	dbExec(t, `DELETE FROM favorites`)
	for _, id := range []int64{1, 42, 43, 44} {
		startAs(t, r.b, r.api, id)
	}
	r.api.reset()
	return r
}

// bsay — сообщение пользователя id (текст с сущностями) и все вызовы Bot API.
func bsay(r *notifyRig, id int64, text string, ents ...models.MessageEntity) []call {
	u := textUpdate(id, text)
	u.Message.Entities = ents
	return process(r, u)
}

func command(r *notifyRig, id int64, cmd string) []call { return process(r, commandUpdate(id, cmd)) }

func process(r *notifyRig, u *models.Update) []call {
	r.api.reset()
	r.b.api.ProcessUpdate(context.Background(), u)
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	return append([]call(nil), r.api.calls...)
}

// sends — сообщения из вызовов по порядку.
func sends(t *testing.T, what string, calls []call, n int) []call {
	t.Helper()
	var out []call
	for _, c := range calls {
		if c.Method == "sendMessage" {
			checkCallbackLengths(t, c)
			out = append(out, c)
		}
	}
	if len(out) != n || len(out) != len(calls) {
		t.Fatalf("%s: вызовы %q, want %d sendMessage", what, methods(calls), n)
	}
	return out
}

// tapSnap — нажатие кнопки с правкой сообщения и ответом, сверка с эталоном.
func tapSnap(t *testing.T, r *notifyRig, id int64, data, snap string) {
	t.Helper()
	matchSnapshot(t, snap, edited(t, data, raw(r.b, r.api, id, data)))
}

func bcCount(t *testing.T, q string) string {
	t.Helper()
	return dbScalar(t, `SELECT count(*)::text FROM `+q)
}

// TestS71Announce — S-71: мастер объявления B-1 → B-3 → B-4 → B-5; отказавшиеся
// не считаются; дата прошлого объявления; не администратору — как
// неизвестная команда.
func TestS71Announce(t *testing.T) {
	r := bcRig(t)
	if got := sends(t, "не администратор", command(r, 42, "/broadcast"), 1)[0]; got.Form["text"] != "Нажмите /start, чтобы найти старт" {
		t.Errorf("/broadcast не администратору: %q", got.Form["text"])
	}
	matchSnapshot(t, "s71_start", renderCall(t, sends(t, "B-1", command(r, 1, "/broadcast"), 1)[0]))
	tapSnap(t, r, 1, "b:k:a", "s71_text")

	dbExec(t, `UPDATE user_profiles SET announce_opt_out = true WHERE tg_id = 43`)
	msgs := sends(t, "текст", bsay(r, 1, "Появились лыжи", models.MessageEntity{Type: models.MessageEntityTypeBold, Offset: 10, Length: 4}), 2)
	matchSnapshot(t, "s71_preview", renderCall(t, msgs[0]))
	matchSnapshot(t, "s71_review", renderCall(t, msgs[1]))
	if c := onlyToast(t, "b:p", raw(r.b, r.api, 1, "b:p")); c.Form["text"] != "" {
		t.Errorf("кнопка предпросмотра: %q", c.Form["text"])
	}

	matchSnapshot(t, "s71_queued", renderCall(t, sends(t, "отправить", bsay(r, 1, "отправить"), 1)[0]))
	if got := bcCount(t, `outbox WHERE kind = 'broadcast'`); got != "3" {
		t.Errorf("строк очереди %s, want 3 (без отказавшегося)", got)
	}
	if got := dbScalar(t, `SELECT text FROM broadcasts`); got != "Появились <b>лыжи</b>" {
		t.Errorf("текст в журнале %q", got)
	}

	// Второе объявление: дата прошлого.
	command(r, 1, "/broadcast")
	raw(r.b, r.api, 1, "b:k:a")
	review := sends(t, "второе", bsay(r, 1, "Ещё"), 2)[1]
	if !strings.Contains(review.Form["text"], "🕓 Прошлое объявление: 29.09.2026") {
		t.Errorf("дата прошлого объявления:\n%s", review.Form["text"])
	}
	if c := onlyToast(t, "b:k:a", raw(r.b, r.api, 42, "b:k:a")); c.Form["text"] != staleNotice {
		t.Errorf("кнопка мастера не администратору: %q", c.Form["text"])
	}
}

// TestS72Invite — S-72: отбор приглашения по видам спорта и городу, город вне
// каталога, «Дальше» без признаков, оба признака сразу, нет получателей.
func TestS72Invite(t *testing.T) {
	r := bcRig(t)
	city, key, _ := strings.Cut(dbScalar(t, `SELECT city || '|' || city_key FROM events
		WHERE country_code = 'RU' AND is_listed AND merged_into_id IS NULL AND start_date >= '2026-09-29' AND city_key IS NOT NULL
		GROUP BY city, city_key ORDER BY count(*) DESC, city_key LIMIT 1`), "|")
	for _, p := range []struct {
		id     int64
		sports string
		key    string
		opt    bool
	}{{42, "{run}", key, true}, {43, "{swim}", key, true}, {44, "{run}", key, false}, {1, "{run}", "x-city", true}} {
		dbExec(t, `UPDATE user_profiles SET status = 'completed', residence_country_code = 'RU', residence_city = 'Город',
			residence_city_key = $2, sports = $3, notify_opt_in = $4 WHERE tg_id = $1`, p.id, p.key, p.sports, p.opt)
	}

	command(r, 1, "/broadcast")
	tapSnap(t, r, 1, "b:k:i", "s72_filter")
	if c := onlyToast(t, "b:n", raw(r.b, r.api, 1, "b:n")); c.Form["text"] != bcNoCriteria {
		t.Errorf("«Дальше» без признаков: %q", c.Form["text"])
	}
	tapSnap(t, r, 1, "b:s:run", "s72_sport")
	tapSnap(t, r, 1, "b:c", "s72_city_ask")
	missing := sends(t, "город вне каталога", bsay(r, 1, "Неизвестноград"), 2)
	matchSnapshot(t, "s72_city_missing", renderCall(t, missing[0]))
	if !strings.Contains(missing[1].Form["text"], "🏠 Город: не выбран") {
		t.Errorf("после города вне каталога — не B-2:\n%s", missing[1].Form["text"])
	}
	raw(r.b, r.api, 1, "b:c")
	found := sends(t, "город", bsay(r, 1, city), 1)[0]
	if want := "🏠 Город: " + city + ", Россия"; !strings.Contains(found.Form["text"], want) || !strings.Contains(found.Form["text"], "👥 Подходят сейчас: 1") {
		t.Errorf("город и оба признака (want %q):\n%s", want, found.Form["text"])
	}
	tapSnap(t, r, 1, "b:n", "s72_text")
	review := sends(t, "текст", bsay(r, 1, "Приходите на старт"), 2)
	matchSnapshot(t, "s72_preview", renderCall(t, review[0]))
	if !strings.Contains(review[1].Form["text"], "🎯 Кому: "+city+", Россия · Бег") || !strings.Contains(review[1].Form["text"], "👥 Получателей: 1") {
		t.Errorf("предпросмотр приглашения:\n%s", review[1].Form["text"])
	}

	// Нет получателей: предпросмотр без слова, подтверждение ничего не пишет.
	command(r, 1, "/broadcast")
	raw(r.b, r.api, 1, "b:k:i")
	raw(r.b, r.api, 1, "b:s:row")
	raw(r.b, r.api, 1, "b:n")
	matchSnapshot(t, "s72_review_zero", renderCall(t, sends(t, "ноль", bsay(r, 1, "Гребля"), 2)[1]))
	if got := sends(t, "ноль: отправить", bsay(r, 1, "ОТПРАВИТЬ"), 1)[0]; got.Form["text"] != bcNobody || bcCount(t, "broadcasts") != "0" {
		t.Errorf("отправка без получателей: %q, рассылок %s", got.Form["text"], bcCount(t, "broadcasts"))
	}
}

// TestS73BroadcastQueue — S-73: сообщения U-1 и U-2 из очереди, отказ перед
// отправкой, 403 выключает приглашения, итог B-6 и стирание текста, логи без
// текста и идентификаторов.
func TestS73BroadcastQueue(t *testing.T) {
	r := bcRig(t)
	logs := captureLogs(t)
	ctx := context.Background()
	if _, n, err := r.s.CreateBroadcast(ctx, store.BroadcastAnnounce, 1, store.BroadcastFilter{}, "Секретный <b>текст</b>", fixtureNow); err != nil || n != 4 {
		t.Fatalf("объявление: %d, %v", n, err)
	}
	r.sent(t, "администратору")
	matchSnapshot(t, "s73_announce", renderCall(t, r.sent(t, "42")))
	dbExec(t, `UPDATE user_profiles SET announce_opt_out = true WHERE tg_id = 43`)
	r.silent(t, "43 отказался после записи")
	if got := dbScalar(t, `SELECT outcome FROM outbox WHERE kind = 'broadcast' AND tg_id = 43`); got != store.OutboxStale {
		t.Errorf("строка отказавшегося: %s", got)
	}
	r.sent(t, "44")
	r.silent(t, "очередь пуста")

	r.at(fixtureNow.Add(time.Minute)) // служебная часть: итог администратору
	done := r.sent(t, "итог")
	if done.Form["chat_id"] != "1" {
		t.Errorf("итог не администратору: %s", done.Form["chat_id"])
	}
	matchSnapshot(t, "s73_done", renderCall(t, done))
	if got := dbScalar(t, `SELECT (text IS NULL)::text FROM broadcasts`); got != "true" {
		t.Error("текст рассылки не стёрт")
	}

	dbExec(t, `UPDATE user_profiles SET status = 'completed', sports = '{run}', notify_opt_in = true WHERE tg_id IN (42, 44)`)
	if _, n, err := r.s.CreateBroadcast(ctx, store.BroadcastInvite, 1, store.BroadcastFilter{Sports: []string{"run"}}, "Приглашаю", fixtureNow); err != nil || n != 2 {
		t.Fatalf("приглашение: %d, %v", n, err)
	}
	matchSnapshot(t, "s73_invite", renderCall(t, r.sent(t, "приглашение 42")))
	r.reply = func(c call, _ int) string { return errBlocked }
	r.step()
	if got := dbScalar(t, `SELECT notify_opt_in::text FROM user_profiles WHERE tg_id = 44`); got != "false" {
		t.Errorf("403 не выключил приглашения: %s", got)
	}

	out := logs.String()
	for _, leak := range []string{"Секретный", "Приглашаю", "tg_id", "chat_id=4"} {
		if strings.Contains(out, leak) {
			t.Errorf("в логе %q:\n%s", leak, out)
		}
	}
}

// stopUpdate — нажатие кнопки отказа в сообщении рассылки с этой кнопкой.
func stopUpdate(id int64, data string) *models.Update {
	u := cbUpdate(id, 7, data)
	u.CallbackQuery.Message.Message.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "🔕", CallbackData: data}}}}
	return u
}

// TestS74OptOut — S-74: отказ из сообщения объявления и приглашения, повтор
// без ошибки, виды независимы.
func TestS74OptOut(t *testing.T) {
	r := bcRig(t)
	dbExec(t, `UPDATE user_profiles SET status = 'completed', sports = '{run}', notify_opt_in = true WHERE tg_id = 42`)
	for i := range 2 {
		calls := process(r, stopUpdate(42, "n:x:a"))
		if methods(calls) != "editMessageReplyMarkup answerCallbackQuery" || calls[0].Form["reply_markup"] != `{"inline_keyboard":[]}` {
			t.Fatalf("отказ от объявлений (%d): %q %v", i, methods(calls), calls)
		}
		matchSnapshot(t, "s74_stop_announce", renderCall(t, calls[1]))
	}
	if got := dbScalar(t, `SELECT announce_opt_out::text || notify_opt_in::text FROM user_profiles WHERE tg_id = 42`); got != "truetrue" {
		t.Errorf("после отказа от объявлений: %s", got)
	}
	calls := process(r, stopUpdate(42, "n:x:i"))
	matchSnapshot(t, "s74_stop_invite", renderCall(t, calls[len(calls)-1]))
	if got := dbScalar(t, `SELECT announce_opt_out::text || notify_opt_in::text FROM user_profiles WHERE tg_id = 42`); got != "truefalse" {
		t.Errorf("после отказа от приглашений: %s", got)
	}
}

// TestS75NotifySettings — S-75: кнопка в профиле, экран «Уведомления», оба
// переключателя, пользователь без анкеты; profile_v не меняется.
func TestS75NotifySettings(t *testing.T) {
	r := bcRig(t)
	dbExec(t, `UPDATE user_profiles SET status = 'completed', residence_country_code = 'BY', residence_city = 'Минск',
		residence_city_key = 'minsk', sports = '{run}' WHERE tg_id = 42`)
	if !strings.Contains(kbLines(t, profileCmd(t, r.b, r.api, 42)), "[🔔 Уведомления] → n:o") {
		t.Error("в профиле нет кнопки «Уведомления»")
	}
	v := dbScalar(t, `SELECT profile_v::text FROM user_profiles WHERE tg_id = 42`)
	tapSnap(t, r, 42, "n:o", "s75_notify")
	tapSnap(t, r, 42, "n:i:1", "s75_invite_on")
	tapSnap(t, r, 42, "n:a:0", "s75_news_off")
	tapSnap(t, r, 42, "n:a:1", "s75_news_on")
	tapSnap(t, r, 42, "n:i:0", "s75_invite_off")
	if got := dbScalar(t, `SELECT profile_v::text FROM user_profiles WHERE tg_id = 42`); got != v {
		t.Errorf("переключатели изменили profile_v: %s → %s", v, got)
	}
	tapSnap(t, r, 43, "n:o", "s75_not_eligible")
	if c := onlyToast(t, "n:i:1", raw(r.b, r.api, 43, "n:i:1")); c.Form["text"] != staleNotice ||
		dbScalar(t, `SELECT notify_opt_in::text FROM user_profiles WHERE tg_id = 43`) != "false" {
		t.Errorf("включить без анкеты: %q", c.Form["text"])
	}
}

// TestS76OnboardingConsent — S-76: вопрос U-6 после «Готово», ответы «Да» и
// «Нет», без признаков — без вопроса, устаревшая кнопка.
func TestS76OnboardingConsent(t *testing.T) {
	r := bcRig(t)
	dbExec(t, `UPDATE user_profiles SET status = 'in_progress', sports = '{run}' WHERE tg_id IN (42, 44)`)
	dbExec(t, `UPDATE user_profiles SET status = 'in_progress' WHERE tg_id = 43`)

	tapSnap(t, r, 42, "u:z:v1", "s76_question")
	if c := onlyToast(t, "u:n:1:v1", raw(r.b, r.api, 42, "u:n:1:v1")); c.Form["text"] != staleNotice {
		t.Errorf("устаревшая кнопка: %q", c.Form["text"])
	}
	tapSnap(t, r, 42, "u:n:1:v2", "s76_yes")
	if got := dbScalar(t, `SELECT notify_opt_in::text FROM user_profiles WHERE tg_id = 42`); got != "true" {
		t.Errorf("«Да»: согласие %s", got)
	}

	raw(r.b, r.api, 44, "u:z:v1")
	if menu, notice := tap(t, r.b, r.api, 44, "u:n:0:v2"); !isMainMenu(menu) || notice != thanksNotice ||
		dbScalar(t, `SELECT notify_opt_in::text FROM user_profiles WHERE tg_id = 44`) != "false" {
		t.Errorf("«Нет»: меню %v, уведомление %q", isMainMenu(menu), notice)
	}

	if menu, notice := tap(t, r.b, r.api, 43, "u:z:v1"); !isMainMenu(menu) || notice != thanksNotice {
		t.Errorf("без города и видов спорта: меню %v, уведомление %q", isMainMenu(menu), notice)
	}
	// Анкета, завершённая раньше, вопроса не получает: «Готово» у неё устарело.
	if c := onlyToast(t, "u:z:v2", raw(r.b, r.api, 43, "u:z:v2")); c.Form["text"] != staleNotice {
		t.Errorf("повторное «Готово»: %q", c.Form["text"])
	}
}

// TestS77BroadcastCancel — S-77: отмена на шагах мастера, подсказка вместо
// слова, длинный текст и не текст, /start снимает ожидание, режимы ввода не
// смешиваются.
func TestS77BroadcastCancel(t *testing.T) {
	r := bcRig(t)
	command(r, 1, "/broadcast")
	tapSnap(t, r, 1, "b:x", "s77_cancel")
	if c := onlyToast(t, "b:x", raw(r.b, r.api, 1, "b:x")); c.Form["text"] != staleNotice {
		t.Errorf("отмена без черновика: %q", c.Form["text"])
	}

	command(r, 1, "/broadcast")
	raw(r.b, r.api, 1, "b:k:i")
	raw(r.b, r.api, 1, "b:s:run")
	if c := edited(t, "b:x", raw(r.b, r.api, 1, "b:x")); !strings.Contains(c, bcCancelled) {
		t.Errorf("отмена на отборе:\n%s", c)
	}

	command(r, 1, "/broadcast")
	raw(r.b, r.api, 1, "b:k:a")
	matchSnapshot(t, "s77_too_long", renderCall(t, sends(t, "длинный", bsay(r, 1, strings.Repeat("я", 2001)), 1)[0]))
	photo := textUpdate(1, "")
	photo.Message.Photo = []models.PhotoSize{{FileID: "f"}}
	if got := sends(t, "фото", process(r, photo), 1)[0]; got.Form["text"] != bcNeedText {
		t.Errorf("не текст: %q", got.Form["text"])
	}
	sends(t, "текст", bsay(r, 1, "Новости"), 2)
	matchSnapshot(t, "s77_confirm_hint", renderCall(t, sends(t, "не слово", bsay(r, 1, "давай"), 1)[0]))
	if c := edited(t, "b:x", raw(r.b, r.api, 1, "b:x")); !strings.Contains(c, bcCancelled) || bcCount(t, "broadcasts") != "0" {
		t.Errorf("отмена на предпросмотре:\n%s", c)
	}

	// /start снимает ожидание: текст идёт в поиск.
	command(r, 1, "/broadcast")
	raw(r.b, r.api, 1, "b:k:a")
	startAs(t, r.b, r.api, 1)
	if got := sends(t, "после /start", bsay(r, 1, "давай"), 1)[0]; strings.Contains(got.Form["text"], "ОТПРАВИТЬ") || strings.Contains(got.Form["text"], "Проверьте") {
		t.Errorf("после /start текст ушёл мастеру:\n%s", got.Form["text"])
	}

	// Ожидание города в профиле, поставленное позже, забирает текст у мастера;
	// мастер, поставленный позже, снимает ожидание города.
	command(r, 1, "/broadcast")
	raw(r.b, r.api, 1, "b:k:a")
	dbExec(t, `UPDATE user_profiles SET residence_country_code = 'BY', awaiting_city_until = $2, awaiting_city_mode = 'e' WHERE tg_id = $1`, 1, fixtureNow.Add(5*time.Minute))
	if got := sends(t, "город профиля", bsay(r, 1, "Минск"), 1)[0]; !strings.Contains(got.Form["text"], "🏠 Город проживания") {
		t.Errorf("текст не ушёл в город профиля:\n%s", got.Form["text"])
	}
	dbExec(t, `UPDATE user_profiles SET awaiting_city_until = $2, awaiting_city_mode = 'e' WHERE tg_id = $1`, 1, fixtureNow.Add(5*time.Minute))
	raw(r.b, r.api, 1, "b:k:a")
	if got := sends(t, "мастер позже", bsay(r, 1, "Гомель"), 2)[1]; !strings.Contains(got.Form["text"], "Проверьте рассылку") ||
		dbScalar(t, `SELECT (awaiting_city_until IS NULL)::text FROM user_profiles WHERE tg_id = 1`) != "true" {
		t.Errorf("мастер не снял ожидание города:\n%s", got.Form["text"])
	}
}

// TestEntitiesHTML — разметка текста рассылки: вложенные сущности, ссылка с
// текстом, экранирование, символы вне BMP в смещениях UTF-16.
func TestEntitiesHTML(t *testing.T) {
	e := func(typ models.MessageEntityType, off, n int) models.MessageEntity {
		return models.MessageEntity{Type: typ, Offset: off, Length: n}
	}
	link := e(models.MessageEntityTypeTextLink, 0, 4)
	link.URL = `https://x.ru/?a=1&b="2"`
	for _, c := range []struct {
		text string
		ents []models.MessageEntity
		want string
	}{
		{"a<b>&c", nil, "a&lt;b&gt;&amp;c"},
		{"жирный курсив", []models.MessageEntity{e(models.MessageEntityTypeBold, 0, 13), e(models.MessageEntityTypeItalic, 7, 6)}, "<b>жирный <i>курсив</i></b>"},
		{"сайт тут", []models.MessageEntity{link}, `<a href="https://x.ru/?a=1&amp;b=&#34;2&#34;">сайт</a> тут`},
		{"🏃 бег", []models.MessageEntity{e(models.MessageEntityTypeBold, 3, 3)}, "🏃 <b>бег</b>"},
		{"@user #tag", []models.MessageEntity{e(models.MessageEntityTypeMention, 0, 5)}, "@user #tag"},
		{"вне", []models.MessageEntity{e(models.MessageEntityTypeBold, 2, 9)}, "вне"},
	} {
		if got := entitiesHTML(c.text, c.ents); got != c.want {
			t.Errorf("entitiesHTML(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
