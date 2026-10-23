package bot

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

// Анкета Phase 9 (09-UX-CONTRACT.md, сценарии S-18…S-22). Кнопки u: берутся с
// экрана как есть: хелперы press и fresh подставляют версию фильтров и
// вызывают Store.Filters, который пишет в users, поэтому здесь не годятся.

// dbExec выполняет запрос без результата — выставить состояние напрямую.
func dbExec(t *testing.T, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}

// userRow и profileRow — строка таблицы целиком (все колонки); нет строки — "".
func userRow(t *testing.T, id int64) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE((SELECT to_jsonb(u)::text FROM users u WHERE tg_id = $1), '')`, id)
}

func profileRow(t *testing.T, id int64) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE((SELECT to_jsonb(p)::text FROM user_profiles p WHERE tg_id = $1), '')`, id)
}

// profileOf — профиль и его версия; строки нет — Fatal.
func profileOf(t *testing.T, b *Bot, id int64) (store.Profile, int) {
	t.Helper()
	p, v, ok, err := b.Store.PeekProfile(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("профиль %d: ok=%v err=%v", id, ok, err)
	}
	return p, v
}

// startAs отправляет /start от имени id и возвращает единственное сообщение.
func startAs(t *testing.T, b *Bot, api *fakeAPI, id int64) call {
	t.Helper()
	api.reset()
	b.api.ProcessUpdate(context.Background(), startUpdate(id))
	s := api.of("sendMessage")
	if len(s) != 1 || api.total() != 1 {
		t.Fatalf("/start %d: ждали одно сообщение, вызовы %+v", id, api.calls)
	}
	return s[0]
}

// uData — данные кнопки экрана по началу подписи (версия не убирается).
func uData(t *testing.T, c call, label string) string {
	t.Helper()
	for _, bt := range c.buttons(t) {
		if strings.HasPrefix(bt.Text, label) {
			return bt.CallbackData
		}
	}
	t.Fatalf("нет кнопки %q: %v", label, c.buttons(t))
	return ""
}

// tap нажимает кнопку как есть; ждёт правку сообщения и один ответ.
func tap(t *testing.T, b *Bot, api *fakeAPI, id int64, data string) (edit call, notice string) {
	t.Helper()
	calls := raw(b, api, id, data)
	edited(t, data, calls)
	for _, c := range calls {
		switch c.Method {
		case "editMessageText":
			edit = c
		case "answerCallbackQuery":
			notice = c.Form["text"]
		}
	}
	return edit, notice
}

// step — tap со сверкой правки и ответа с эталоном.
func step(t *testing.T, b *Bot, api *fakeAPI, id int64, data, snap string) call {
	t.Helper()
	calls := raw(b, api, id, data)
	matchSnapshot(t, snap, edited(t, data, calls))
	for _, c := range calls {
		if c.Method == "editMessageText" {
			return c
		}
	}
	return call{}
}

func isMainMenu(c call) bool {
	return strings.Contains(c.Form["text"], "Собрали старты из разных календарей") && !strings.Contains(c.Form["text"], "4 коротких вопроса")
}

// TestS18Offer — S-18: предложение анкеты на /start (D-05, D-06, PRF-01).
func TestS18Offer(t *testing.T) {
	s, api, b := countryFixture(t)
	logs := captureLogs(t)

	// 1. Нет пользователя: O-1 новым сообщением, созданы users и профиль.
	offer := startAs(t, b, api, 42)
	matchSnapshot(t, "s18_offer_new", renderCall(t, offer))
	checkCallbackLengths(t, offer)
	p, v := profileOf(t, b, 42)
	if p.Status != store.ProfileNotStarted || v != 1 || userRow(t, 42) == "" {
		t.Fatalf("после первого /start: профиль %+v v%d, users %q", p, v, userRow(t, 42))
	}
	if n := dbScalar(t, `SELECT count(*)::text FROM user_profiles WHERE tg_id = 42 AND offered_at IS NOT NULL`); n != "1" {
		t.Errorf("offered_at не задан")
	}

	// 2. Повторный /start (not_started): главное меню, профиль не изменился.
	before := profileRow(t, 42)
	menu := startAs(t, b, api, 42)
	matchSnapshot(t, "s18_start_not_started", renderCall(t, menu))
	if after := profileRow(t, 42); after != before {
		t.Errorf("повторный /start изменил профиль:\n%s\n%s", before, after)
	}

	// 3. Действующий пользователь без строки профиля: O-1 один раз, фильтры целы.
	dbExec(t, `INSERT INTO users (tg_id, filters, filters_v) VALUES (50, '{"type":"trail"}', 3)`)
	filtersBefore := dbScalar(t, `SELECT filters::text || ':' || filters_v FROM users WHERE tg_id = 50`)
	matchSnapshot(t, "s18_offer_existing", renderCall(t, startAs(t, b, api, 50)))
	if p, v := profileOf(t, b, 50); p.Status != store.ProfileNotStarted || v != 1 {
		t.Errorf("профиль действующего пользователя: %+v v%d", p, v)
	}
	if after := dbScalar(t, `SELECT filters::text || ':' || filters_v FROM users WHERE tg_id = 50`); after != filtersBefore {
		t.Errorf("предложение изменило фильтры: %s → %s", filtersBefore, after)
	}
	if !isMainMenu(startAs(t, b, api, 50)) {
		t.Error("второй /start действующего пользователя — не главное меню")
	}

	// 4. Любой статус профиля: /start — главное меню, строка профиля не менялась.
	for i, st := range []string{store.ProfileInProgress, store.ProfileCompleted, store.ProfileSkipped} {
		id := int64(51 + i)
		startAs(t, b, api, id) // O-1
		dbExec(t, `UPDATE user_profiles SET status = $2 WHERE tg_id = $1`, id, st)
		row := profileRow(t, id)
		matchSnapshot(t, "s18_start_"+st, renderCall(t, startAs(t, b, api, id)))
		if after := profileRow(t, id); after != row {
			t.Errorf("%s: /start изменил профиль:\n%s\n%s", st, row, after)
		}
	}

	// 5. Сорвавшаяся отправка предложения: профиль создан, повторного
	// предложения нет (D-06).
	failing := fakeTelegram(t, func(c call, n int) string {
		if c.Method == "sendMessage" && n == 1 {
			return apiErr(500, "Internal Server Error", 0)
		}
		return ""
	})
	b2 := newTestBot(t, failing, s)
	logs.Reset()
	b2.api.ProcessUpdate(context.Background(), startUpdate(60))
	if !strings.Contains(logs.String(), `msg="send start"`) {
		t.Errorf("нет строки лога send start:\n%s", logs)
	}
	if p, _ := profileOf(t, b2, 60); p.Status != store.ProfileNotStarted {
		t.Errorf("после сорвавшейся отправки профиль %+v", p)
	}
	failing.reset()
	b2.api.ProcessUpdate(context.Background(), startUpdate(60))
	again := failing.of("sendMessage")
	if len(again) != 1 || !isMainMenu(again[0]) {
		t.Fatalf("/start после сорвавшегося предложения: %+v", again)
	}
	matchSnapshot(t, "s18_start_after_failed_offer", renderCall(t, again[0]))
}

// TestS19SkipAll — S-19: пропуск анкеты целиком (D-01, PRF-02, PRF-03).
func TestS19SkipAll(t *testing.T) {
	s, api, b := countryFixture(t)
	offer := startAs(t, b, api, 42)
	skip := uData(t, offer, "Пропустить")
	usersBefore := userRow(t, 42)

	// 1. «Пропустить» → главное меню правкой, ответ без текста.
	menu := step(t, b, api, 42, skip, "s19_skip")
	if p, v := profileOf(t, b, 42); p.Status != store.ProfileSkipped || v != 2 {
		t.Fatalf("после «Пропустить»: %+v v%d, want skipped v2", p, v)
	}
	if after := userRow(t, 42); after != usersBefore {
		t.Errorf("кнопка анкеты изменила users:\n%s\n%s", usersBefore, after)
	}

	// 2. Поиск работает как у пользователя без анкеты; ответы профиля пусты.
	matchSnapshot(t, "s19_search", renderCall(t, press(t, b, api, menu.button(t, "🏃 Бег"))))
	list := press(t, b, api, "p:1:v1")
	items, err := s.ListEvents(context.Background(), event.Filter{From: fixtureToday}, pageSize, 0)
	if err != nil || len(items) == 0 || len(listRows(t, list)) == 0 {
		t.Fatalf("список после пропуска анкеты: %d строк, %v", len(listRows(t, list)), err)
	}
	if card := press(t, b, api, "e:"+strconv.FormatInt(items[0].ID, 10)+":1:v1"); card.Form["text"] == "" {
		t.Error("карточка не открылась")
	}
	if p, _ := profileOf(t, b, 42); p.Gender != "" || p.AgeGroup != -1 || p.Country != "" || len(p.Sports) != 0 {
		t.Errorf("ответы профиля не пусты: %+v", p)
	}

	// 3. Повторное «Пропустить»: устарела, ничего не меняется.
	before := dbState(t)
	matchSnapshot(t, "s19_skip_again", renderCall(t, onlyToast(t, skip, raw(b, api, 42, skip))))
	if after := dbState(t); after != before {
		t.Errorf("повторное нажатие изменило данные:\n%s\n%s", before, after)
	}
	if !isMainMenu(startAs(t, b, api, 42)) {
		t.Error("/start после пропуска — не главное меню")
	}
	if !t.Failed() {
		t.Log("TRACER offer=sent skip=menu profile=skipped")
	}
}

// TestS20Wizard — S-20: прохождение анкеты (D-04, PRF-04).
func TestS20Wizard(t *testing.T) {
	_, api, b := countryFixture(t)
	offer := startAs(t, b, api, 42)
	usersAtStart := userRow(t, 42)
	wantV := 1
	saved := func(what string) store.Profile {
		t.Helper()
		wantV++
		p, v := profileOf(t, b, 42)
		if v != wantV {
			t.Fatalf("%s: profile_v = %d, want %d", what, v, wantV)
		}
		return p
	}

	// 1. «Заполнить анкету» → вопрос 1.
	q1 := step(t, b, api, 42, uData(t, offer, "📝 Заполнить анкету"), "s20_q1")
	if p := saved("начало"); p.Status != store.ProfileInProgress {
		t.Fatalf("статус %q", p.Status)
	}
	if dbScalar(t, `SELECT (started_at IS NOT NULL)::text FROM user_profiles WHERE tg_id = 42`) != "true" {
		t.Error("started_at не задан")
	}
	// 2. Пол.
	q2 := step(t, b, api, 42, uData(t, q1, "Мужской"), "s20_q2")
	if p := saved("пол"); p.Gender != "m" {
		t.Fatalf("пол %q", p.Gender)
	}
	// 3. Возраст → экран страны.
	q3 := step(t, b, api, 42, uData(t, q2, "25–34"), "s20_q3_country")
	if p := saved("возраст"); p.AgeGroup != 2 {
		t.Fatalf("возраст %d", p.AgeGroup)
	}
	// 4. Страна → экран города; отметка ожидания на 10 минут, режим w.
	city := step(t, b, api, 42, uData(t, q3, "🇧🇾 Беларусь"), "s20_q3_city")
	p := saved("страна")
	if p.Country != "BY" || !p.AwaitingCityUntil.Equal(fixtureNow.Add(10*time.Minute)) || p.AwaitingCityMode != modeWizard {
		t.Fatalf("страна/отметка: %+v", p)
	}
	// 5. Город кнопкой → вопрос 4; отметка снята.
	q4 := step(t, b, api, 42, uData(t, city, "Минск"), "s20_q4")
	if p = saved("город"); p.City != "Минск" || p.CityKey != "minsk" || !p.AwaitingCityUntil.IsZero() {
		t.Fatalf("город/отметка: %+v", p)
	}
	// 6. Виды спорта: экран остаётся, «✓» у выбранных, версия растёт.
	one, _ := tap(t, b, api, 42, uData(t, q4, "🏃 Бег"))
	saved("бег")
	two := step(t, b, api, 42, uData(t, one, "🚴 Велоспорт"), "s20_q4_two")
	if p = saved("велоспорт"); strings.Join(p.Sports, ",") != "run,bike" {
		t.Fatalf("спорт %v", p.Sports)
	}
	off, _ := tap(t, b, api, 42, uData(t, two, "✓ 🏃 Бег")) // повторное нажатие снимает
	if p = saved("снять бег"); strings.Join(p.Sports, ",") != "bike" {
		t.Fatalf("после снятия %v", p.Sports)
	}
	on, _ := tap(t, b, api, 42, uData(t, off, "🏃 Бег"))
	if p = saved("вернуть бег"); strings.Join(p.Sports, ",") != "run,bike" {
		t.Fatalf("порядок видов спорта %v", p.Sports)
	}
	// 7. «Готово» → вопрос о приглашениях (U-6, Phase 10); меню — после ответа.
	step(t, b, api, 42, uData(t, on, "✅ Готово"), "s20_done")
	p, _ = profileOf(t, b, 42)
	if p.Status != store.ProfileCompleted || dbScalar(t, `SELECT (completed_at IS NOT NULL)::text FROM user_profiles WHERE tg_id = 42`) != "true" {
		t.Fatalf("после «Готово»: %+v", p)
	}
	saved("готово")
	if menu, notice := tap(t, b, api, 42, "u:n:0:v11"); !isMainMenu(menu) || notice != thanksNotice {
		t.Errorf("после ответа на вопрос о приглашениях: меню %v, уведомление %q", isMainMenu(menu), notice)
	}
	if after := userRow(t, 42); after != usersAtStart {
		t.Errorf("анкета изменила users:\n%s\n%s", usersAtStart, after)
	}
	if !isMainMenu(startAs(t, b, api, 42)) {
		t.Error("/start после анкеты — не главное меню")
	}

	// «Другая страна»: экран города без кнопок городов; подпись Казахстана.
	o := startAs(t, b, api, 44)
	e1, _ := tap(t, b, api, 44, uData(t, o, "📝 Заполнить анкету"))
	e2, _ := tap(t, b, api, 44, uData(t, e1, "Пропустить вопрос"))
	e3, _ := tap(t, b, api, 44, uData(t, e2, "Пропустить вопрос"))
	uData(t, e3, "🇰🇿 Казахстан")
	zz, _ := tap(t, b, api, 44, uData(t, e3, "🌍 Другая страна"))
	if !strings.Contains(zz.Form["text"], "Страна: 🌍 Другая страна\n\nНапишите название города сообщением") {
		t.Errorf("экран города для ZZ:\n%s", zz.Form["text"])
	}
	for _, bt := range zz.buttons(t) {
		if strings.HasPrefix(bt.CallbackData, "u:s:c:") {
			t.Errorf("кнопка города на экране «Другая страна»: %q", bt.CallbackData)
		}
	}
	if p, _ := profileOf(t, b, 44); p.Country != "ZZ" || p.City != "" {
		t.Errorf("профиль ZZ: %+v", p)
	}
}

// TestS21SkipQuestion — S-21: пропуск вопроса, «Назад» и прерывание (D-01, PRF-02).
func TestS21SkipQuestion(t *testing.T) {
	_, api, b := countryFixture(t)
	offer := startAs(t, b, api, 42)
	q1, _ := tap(t, b, api, 42, uData(t, offer, "📝 Заполнить анкету")) // v2
	state := profileRow(t, 42)
	same := func(what string) {
		t.Helper()
		if now := profileRow(t, 42); now != state {
			t.Fatalf("%s: профиль изменился\n%s\n%s", what, state, now)
		}
	}

	// 1. Пропустить вопрос 1 → вопрос 2, записей нет.
	q2 := step(t, b, api, 42, uData(t, q1, "Пропустить вопрос"), "s21_skip_q1")
	same("пропуск вопроса 1")
	// 2. «Назад» с вопроса 2 → вопрос 1.
	step(t, b, api, 42, uData(t, q2, "⬅️ Назад"), "s21_back_to_q1")
	same("назад к вопросу 1")
	// 3. «Назад» с первого вопроса → главное меню, статус остаётся.
	step(t, b, api, 42, uData(t, q1, "⬅️ Назад"), "s21_back_to_menu")
	same("назад в меню")
	if p, _ := profileOf(t, b, 42); p.Status != store.ProfileInProgress {
		t.Fatalf("статус после возврата в меню: %q", p.Status)
	}
	// 4. Страна, затем «Пропустить вопрос» на экране города → вопрос 4.
	q3, _ := tap(t, b, api, 42, uData(t, q2, "Пропустить вопрос"))
	same("пропуск вопроса 2")
	city, _ := tap(t, b, api, 42, uData(t, q3, "🇧🇾 Беларусь"))
	if p, v := profileOf(t, b, 42); p.Country != "BY" || p.AwaitingCityUntil.IsZero() || v != 3 {
		t.Fatalf("после выбора страны: %+v v%d", p, v)
	}
	q4 := step(t, b, api, 42, uData(t, city, "Пропустить вопрос"), "s21_skip_city")
	p, v := profileOf(t, b, 42)
	if p.Country != "BY" || p.City != "" || !p.AwaitingCityUntil.IsZero() || v != 3 {
		t.Fatalf("после пропуска города: %+v v%d", p, v)
	}
	state = profileRow(t, 42)
	// 5. «Назад» с вопроса 4 → страна с «✓» у Беларуси.
	back := step(t, b, api, 42, uData(t, q4, "⬅️ Назад"), "s21_q4_back")
	same("назад к стране")
	// 6. На вопросе 2 пользователь пишет /start → главное меню, статус и ответы целы.
	tap(t, b, api, 42, uData(t, back, "⬅️ Назад"))
	same("назад к возрасту")
	matchSnapshot(t, "s21_interrupt", renderCall(t, startAs(t, b, api, 42)))
	same("/start посреди анкеты")
	if p, _ := profileOf(t, b, 42); p.Status != store.ProfileInProgress || p.Country != "BY" {
		t.Fatalf("после /start посреди анкеты: %+v", p)
	}

	// 7. Пропуск всех четырёх вопросов и «Готово»: completed с пустыми полями.
	o := startAs(t, b, api, 43)
	c1, _ := tap(t, b, api, 43, uData(t, o, "📝 Заполнить анкету"))
	c2, _ := tap(t, b, api, 43, uData(t, c1, "Пропустить вопрос"))
	c3, _ := tap(t, b, api, 43, uData(t, c2, "Пропустить вопрос"))
	c4, _ := tap(t, b, api, 43, uData(t, c3, "Пропустить вопрос"))
	step(t, b, api, 43, uData(t, c4, "✅ Готово"), "s21_all_skipped")
	p, v = profileOf(t, b, 43)
	if p.Status != store.ProfileCompleted || p.Gender != "" || p.AgeGroup != -1 || p.Country != "" || p.City != "" || len(p.Sports) != 0 || v != 3 {
		t.Fatalf("пустая анкета: %+v v%d", p, v)
	}
}

// TestS22StaleProfile — S-22: устаревшие, недопустимые и поддельные кнопки u:
// (D-15, PRF-07, T-09-09…T-09-13).
func TestS22StaleProfile(t *testing.T) {
	s, api, b := countryFixture(t)
	logs := captureLogs(t)

	// Пользователь 42 начал анкету и ответил на пол: v1 → v2 → v3; кнопки v2 старые.
	offer := startAs(t, b, api, 42)
	q1, _ := tap(t, b, api, 42, uData(t, offer, "📝 Заполнить анкету"))
	oldMale := uData(t, q1, "Мужской") // u:s:g:m:w:v2
	tap(t, b, api, 42, oldMale)
	if _, v := profileOf(t, b, 42); v != 3 {
		t.Fatalf("profile_v = %d, want 3", v)
	}
	// Пользователи для проверки переходов статусов (версия у всех v1).
	startAs(t, b, api, 62)
	dbExec(t, `UPDATE user_profiles SET status = 'skipped' WHERE tg_id = 62`)
	startAs(t, b, api, 63)
	dbExec(t, `UPDATE user_profiles SET status = 'completed' WHERE tg_id = 63`)
	startAs(t, b, api, 61) // not_started
	// 64: страна выбрана; 65: страны нет.
	o64 := startAs(t, b, api, 64)
	a64, _ := tap(t, b, api, 64, uData(t, o64, "📝 Заполнить анкету")) // v2
	tap(t, b, api, 64, "u:s:k:BY:w:v2")                               // v3
	_ = a64
	o65 := startAs(t, b, api, 65)
	tap(t, b, api, 65, uData(t, o65, "📝 Заполнить анкету")) // v2
	logs.Reset()

	toast := func(snap string, id int64, cases ...string) {
		t.Helper()
		for _, data := range cases {
			before := dbState(t)
			a := onlyToast(t, data, raw(b, api, id, data))
			matchSnapshot(t, snap, renderCall(t, a))
			if after := dbState(t); after != before {
				t.Fatalf("%q изменил данные:\n%s\n%s", data, before, after)
			}
		}
	}

	// 1. Изменяющие кнопки с чужой версией.
	toast("s22_stale_save", 42,
		oldMale, "u:s:g:f:w:v1", "u:s:a:2:w:v2", "u:s:k:BY:w:v2", "u:s:c:minsk:w:v2", "u:s:s:run:w:v2", "u:s:g:m:e:v2",
		"u:s:a:2:e:v2", "u:s:k:BY:e:v1", "u:s:c:minsk:e:v2", "u:s:s:run:e:v2", "u:s:g:f:e:v0",
		"u:b:v2", "u:n:v2", "u:z:v2", "u:b:v0", "u:n:v99", "u:z:v1", "u:z:v99")
	if !strings.Contains(logs.String(), `msg="callback stale" prefix=u`) {
		t.Errorf("нет строки лога callback stale:\n%s", logs)
	}

	// 2. Устаревшая навигация: экран поля по текущему состоянию, без записи.
	before := dbState(t)
	for data, snap := range map[string]string{"u:a:a:e:v2": "s22_stale_nav", "u:a:c:e:v2": "s22_stale_city_nav", "u:a:c:e:v3": "s22_stale_city_nav"} {
		matchSnapshot(t, snap, edited(t, data, raw(b, api, 42, data)))
	}
	if c := raw(b, api, 42, "u:a:c:w:v2"); !strings.Contains(c[0].Form["text"], "Вопрос 3 из 4</b>\nГде вы живёте?") {
		t.Errorf("u:a:c без страны в анкете:\n%s", c[0].Form["text"])
	}
	if after := dbState(t); after != before {
		t.Fatalf("устаревшая навигация записала в базу:\n%s\n%s", before, after)
	}

	for _, data := range []string{"u:a:s:e:v1", "u:a:k:e:v0"} { // навигация режима e: экран по текущему состоянию
		before := dbState(t)
		if c := raw(b, api, 42, data); len(c) != 2 || c[0].Method != "editMessageText" || strings.Contains(c[0].Form["text"], "Вопрос") {
			t.Errorf("%q: %+v", data, c)
		}
		if after := dbState(t); after != before {
			t.Fatalf("%q записал в базу:\n%s\n%s", data, before, after)
		}
	}

	// 3. Нет строки профиля (и users): навигация и u:o показывают экран, изменение — «устарела».
	before = dbState(t)
	for _, data := range []string{"u:a:g:e:v0", "u:a:g:w:v1", "u:a:g:e:v1"} {
		matchSnapshot(t, "s22_no_row_nav", edited(t, data, raw(b, api, 77, data)))
	}
	matchSnapshot(t, "s22_no_row_profile", edited(t, "u:o", raw(b, api, 77, "u:o")))
	toast("s22_no_row_save", 77, "u:b:v0", "u:b:v1", "u:n:v0", "u:n:v1", "u:z:v0", "u:z:v1",
		"u:s:g:m:e:v0", "u:s:g:m:w:v1", "u:s:a:2:w:v1", "u:s:k:BY:w:v1", "u:s:c:minsk:w:v1", "u:s:s:run:w:v1",
		"u:s:a:2:e:v0", "u:s:k:BY:e:v0", "u:s:c:minsk:e:v0", "u:s:s:run:e:v0", "u:s:g:f:e:v1")
	if after := dbState(t); after != before || userRow(t, 77) != "" || profileRow(t, 77) != "" {
		t.Fatalf("кнопки пользователя без строки создали данные:\n%s\n%s", before, after)
	}

	// 4. Актуальная версия, недопустимый для статуса переход.
	toast("s22_bad_transition", 42, "u:b:v3", "u:n:v3")                           // in_progress
	toast("s22_bad_transition", 61, "u:z:v1", "u:s:g:m:w:v1")                     // not_started: готово и ответ в анкете
	toast("s22_bad_transition", 62, "u:b:v1", "u:n:v1", "u:z:v1", "u:s:g:m:w:v1") // skipped
	toast("s22_bad_transition", 63, "u:b:v1", "u:n:v1", "u:z:v1", "u:s:g:m:w:v1") // completed

	// 5. Значения вне грамматики.
	toast("s22_unknown", 42,
		"u:", "u", "u:x:v1", "u:q:v1", "u:s:g:x:w:v3", "u:s:a:6:w:v3", "u:s:a:05:w:v3", "u:s:k:PL:w:v3", "u:s:s:other:w:v3",
		"u:s:c:"+strings.Repeat("a", 50)+":w:v3", "u:z", "u:b", "u:s:g:m:w", "u:s:x:m:w:v3", "u:a:x:w:v3", "u:o:v1",
		"u:"+strings.Repeat("a", 62))

	// 6. Ключ города, которого нет в каталоге выбранной страны, и город без страны.
	toast("s22_city_not_in_catalog", 64, "u:s:c:nowhere:w:v3", "u:s:c:moscow:w:v3") // moscow — в каталоге России, не Беларуси
	toast("s22_city_not_in_catalog", 65, "u:s:c:minsk:w:v2")

	// 7. Отметка ожидания города: устаревшие u: и другие кнопки её не трогают.
	awaiting := func() string {
		return dbScalar(t, `SELECT (awaiting_city_until IS NOT NULL)::text FROM user_profiles WHERE tg_id = 64`)
	}
	if awaiting() != "true" {
		t.Fatalf("отметка ожидания не стоит: %s", profileRow(t, 64))
	}
	for _, data := range []string{"u:s:g:m:w:v1", "u:a:a:w:v1", "u:b:v2", "m", "f:s:v1", "s:o:v1", "p:1:v1"} {
		raw(b, api, 64, data)
		if awaiting() != "true" {
			t.Errorf("%q сняла отметку ожидания", data)
		}
	}

	// 8. Актуальная кнопка того же вида применяется: проверка чувствительна.
	g3, _ := tap(t, b, api, 42, "u:a:g:w:v3")
	if _, notice := tap(t, b, api, 42, uData(t, g3, "Женский")); notice != "" {
		t.Errorf("актуальная кнопка: уведомление %q", notice)
	}
	if p, v := profileOf(t, b, 42); p.Gender != "f" || v != 4 {
		t.Errorf("актуальная кнопка не применилась: %+v v%d", p, v)
	}
	for _, l := range []string{"u:s:", "minsk", "nowhere", "moscow"} {
		if strings.Contains(logs.String(), l) {
			t.Errorf("в логе есть значение %q:\n%s", l, logs)
		}
	}

	// 9. Внутренняя ошибка (пул закрыт): свой текст и своя строка лога.
	s.Close()
	for _, data := range []string{"u:b:v1", "u:a:g:w:v4", "u:s:g:m:w:v4", "u:z:v4"} {
		logs.Reset()
		matchSnapshot(t, "s17_error", renderCall(t, onlyToast(t, data, raw(b, api, 42, data))))
		if !strings.Contains(logs.String(), `msg="callback internal error" prefix=u`) || strings.Contains(logs.String(), "callback stale") {
			t.Errorf("%q: лог:\n%s", data, logs)
		}
	}
}

// profileCmd отправляет команду /profile и возвращает единственное сообщение.
func profileCmd(t *testing.T, b *Bot, api *fakeAPI, id int64) call {
	t.Helper()
	api.reset()
	u := startUpdate(id)
	u.Message.Text = "/profile"
	u.Message.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 8}}
	b.api.ProcessUpdate(context.Background(), u)
	s := api.of("sendMessage")
	if len(s) != 1 || api.total() != 1 {
		t.Fatalf("/profile %d: ждали одно сообщение, вызовы %+v", id, api.calls)
	}
	checkCallbackLengths(t, s[0])
	return s[0]
}

// seedProfile создаёт пользователя и выставляет профиль напрямую (версия
// остаётся 1): основа для сценариев, которым нужны готовые ответы.
func seedProfile(t *testing.T, b *Bot, api *fakeAPI, id int64, set string) {
	t.Helper()
	startAs(t, b, api, id)
	dbExec(t, `UPDATE user_profiles SET `+set+` WHERE tg_id = $1`, id)
}

const fullProfile = `status = 'completed', gender = 'm', age_group = 2, residence_country_code = 'BY',
	residence_city = 'Минск', residence_city_key = 'minsk', sports = '{run,bike}', started_at = now(), completed_at = now()`

// TestS23Profile — S-23: /profile и кнопка главного меню (D-01, D-13, PRF-05, PRF-06).
func TestS23Profile(t *testing.T) {
	_, api, b := countryFixture(t)

	// 1–2. Заполненный профиль: /profile новым сообщением, кнопка меню — правкой.
	seedProfile(t, b, api, 42, fullProfile)
	matchSnapshot(t, "s23_profile_full", renderCall(t, profileCmd(t, b, api, 42)))
	menu := startAs(t, b, api, 42)
	before := dbState(t) // после /start: он обновляет last_seen_at, профиль — нет
	if !isMainMenu(menu) || uData(t, menu, "👤 Мой профиль") != "u:o" {
		t.Fatalf("главное меню без кнопки профиля: %v", menu.buttons(t))
	}
	if got := datas(t, menu); !reflect.DeepEqual(got, []string{"f:a:running", "w:o", "s:o", "u:o"}) {
		t.Errorf("кнопки главного меню: %v", got)
	}
	p1 := step(t, b, api, 42, "u:o", "s23_profile_from_menu")
	if after := dbState(t); after != before {
		t.Errorf("просмотр профиля изменил данные:\n%s\n%s", before, after)
	}
	// 7. «Назад» — главное меню правкой, четыре кнопки.
	back := step(t, b, api, 42, uData(t, p1, "⬅️ Назад"), "s23_back")
	if got := datas(t, back); !reflect.DeepEqual(got, []string{"f:a:running", "w:o", "s:o", "u:o"}) {
		t.Errorf("кнопки главного меню после «Назад»: %v", got)
	}

	// 3. Статус skipped, ответов нет: «не указан» и «не указано».
	offer := startAs(t, b, api, 43)
	tap(t, b, api, 43, uData(t, offer, "Пропустить")) // v2
	usersBefore, profileBefore := userRow(t, 43), profileRow(t, 43)
	empty := profileCmd(t, b, api, 43)
	matchSnapshot(t, "s23_profile_empty", renderCall(t, empty))
	if userRow(t, 43) != usersBefore || profileRow(t, 43) != profileBefore {
		t.Errorf("/profile изменил данные:\n%s\n%s", usersBefore, userRow(t, 43))
	}
	want := []string{"u:a:g:e:v2", "u:a:a:e:v2", "u:a:k:e:v2", "u:a:s:e:v2", "n:o", "k:o", "m"}
	if got := datas(t, empty); !reflect.DeepEqual(got, want) {
		t.Errorf("кнопки P-1: %v, want %v", got, want)
	}

	// 4. «Другая страна» и город.
	seedProfile(t, b, api, 44, `status = 'in_progress', residence_country_code = 'ZZ', residence_city = 'Варшава'`)
	matchSnapshot(t, "s23_profile_zz", renderCall(t, profileCmd(t, b, api, 44)))
	// 5. Город с HTML-символами показан экранированным.
	seedProfile(t, b, api, 45, `status = 'in_progress', residence_country_code = 'BY', residence_city = '<b>X</b> & "Y"'`)
	esc := profileCmd(t, b, api, 45)
	matchSnapshot(t, "s23_profile_escaped", renderCall(t, esc))
	if strings.Contains(esc.Form["text"], "<b>X") {
		t.Errorf("город не экранирован:\n%s", esc.Form["text"])
	}

	// 6. Нет строки профиля (и users): P-1 с пустыми полями и кнопками v0, ничего не создано.
	before = dbState(t)
	none := profileCmd(t, b, api, 77)
	matchSnapshot(t, "s23_profile_no_row", renderCall(t, none))
	if after := dbState(t); after != before || userRow(t, 77) != "" || profileRow(t, 77) != "" {
		t.Errorf("/profile создал данные:\n%s\n%s", before, after)
	}
	if got := datas(t, none); !reflect.DeepEqual(got, []string{"u:a:g:e:v0", "u:a:a:e:v0", "u:a:k:e:v0", "u:a:s:e:v0", "n:o", "k:o", "m"}) {
		t.Errorf("кнопки P-1 без профиля: %v", got)
	}
	// 8. Просмотр с отметкой ожидания города снимает только её: версия и ответы целы.
	seedProfile(t, b, api, 46, `status = 'in_progress', residence_country_code = 'BY', awaiting_city_until = now() + interval '1 hour', awaiting_city_mode = 'e'`)
	answers := func() string {
		return dbScalar(t, `SELECT (to_jsonb(p) - 'awaiting_city_until' - 'awaiting_city_mode')::text FROM user_profiles p WHERE tg_id = 46`)
	}
	state, users46 := answers(), userRow(t, 46)
	profileCmd(t, b, api, 46)
	if answers() != state || userRow(t, 46) != users46 ||
		dbScalar(t, `SELECT (awaiting_city_until IS NULL AND awaiting_city_mode IS NULL)::text FROM user_profiles WHERE tg_id = 46`) != "true" {
		t.Errorf("/profile при отметке ожидания: %s", profileRow(t, 46))
	}
}

// TestS24Edit — S-24: редактирование ответов (D-01, D-03, PRF-05, PRF-06).
func TestS24Edit(t *testing.T) {
	_, api, b := countryFixture(t)
	offer := startAs(t, b, api, 42)
	tap(t, b, api, 42, uData(t, offer, "Пропустить")) // skipped, v2
	usersBefore := userRow(t, 42)
	_, last := profileOf(t, b, 42)
	grew := func(what string, by int) store.Profile {
		t.Helper()
		p, v := profileOf(t, b, 42)
		if v != last+by {
			t.Fatalf("%s: profile_v = %d, want %d", what, v, last+by)
		}
		last = v
		return p
	}
	hasCheck := func(c call, label string) {
		t.Helper()
		for _, bt := range c.buttons(t) {
			if bt.Text == "✓ "+label {
				return
			}
		}
		t.Errorf("нет кнопки «✓ %s»: %v", label, c.buttons(t))
	}

	// 1. Пол: экран вопроса без «Вопрос N из 4», выбор возвращает в P-1.
	v0 := last
	p1 := profileCmd(t, b, api, 42)
	ask := step(t, b, api, 42, uData(t, p1, "🚻 Пол"), "s24_gender")
	p1 = step(t, b, api, 42, uData(t, ask, "Женский"), "s24_gender_saved")
	p := grew("пол", 1)
	if p.Gender != "f" || p.Status != store.ProfileInProgress {
		t.Fatalf("после выбора пола: %+v", p)
	}
	if dbScalar(t, `SELECT (started_at IS NOT NULL)::text FROM user_profiles WHERE tg_id = 42`) != "true" {
		t.Error("started_at не задан при первом ответе через профиль")
	}
	genderGrowth := last - v0
	// Текущее значение отмечено; повторный выбор того же значения версию не меняет.
	ask, _ = tap(t, b, api, 42, uData(t, p1, "🚻 Пол"))
	hasCheck(ask, "Женский")
	p1, _ = tap(t, b, api, 42, uData(t, ask, "✓ Женский"))
	grew("тот же пол", 0)
	if !strings.Contains(p1.Form["text"], "🚻 Пол: Женский") {
		t.Errorf("повторный выбор: %s", p1.Form["text"])
	}
	if !t.Failed() {
		t.Logf("TRACER profile=shown edit=gender v=+%d", genderGrowth)
	}

	// 2. Возраст.
	ask = step(t, b, api, 42, uData(t, p1, "🎂 Возраст"), "s24_age")
	p1 = step(t, b, api, 42, uData(t, ask, "25–34"), "s24_age_saved")
	if p = grew("возраст", 1); p.AgeGroup != 2 {
		t.Fatalf("возраст %d", p.AgeGroup)
	}
	ask, _ = tap(t, b, api, 42, uData(t, p1, "🎂 Возраст"))
	hasCheck(ask, "25–34")
	p1, _ = tap(t, b, api, 42, uData(t, ask, "⬅️ Назад")) // без записи
	grew("назад с возраста", 0)

	// 3. Страна и город: BY → «Минск»; затем смена на Россию очищает город.
	ask, _ = tap(t, b, api, 42, uData(t, p1, "🏠 Где живу"))
	city, _ := tap(t, b, api, 42, uData(t, ask, "🇧🇾 Беларусь"))
	p = grew("страна", 1)
	if p.Country != "BY" || p.AwaitingCityMode != modeEdit || !p.AwaitingCityUntil.Equal(fixtureNow.Add(awaitCityFor)) ||
		!strings.Contains(city.Form["text"], "<b>Где вы живёте?</b>\nСтрана: 🇧🇾 Беларусь") {
		t.Fatalf("после выбора страны: %+v\n%s", p, city.Form["text"])
	}
	p1, _ = tap(t, b, api, 42, uData(t, city, "Минск"))
	p = grew("город", 1)
	if p.City != "Минск" || p.CityKey != "minsk" || !p.AwaitingCityUntil.IsZero() || !strings.Contains(p1.Form["text"], "🏠 Живу: 🇧🇾 Беларусь, Минск") {
		t.Fatalf("после выбора города: %+v\n%s", p, p1.Form["text"])
	}
	ask = step(t, b, api, 42, uData(t, p1, "🏠 Где живу"), "s24_country")
	hasCheck(ask, "🇧🇾 Беларусь")
	city = step(t, b, api, 42, uData(t, ask, "🇷🇺 Россия"), "s24_city")
	p = grew("смена страны", 1)
	if p.Country != "RU" || p.City != "" || p.CityKey != "" || p.AwaitingCityMode != modeEdit || p.AwaitingCityUntil.IsZero() {
		t.Fatalf("после смены страны: %+v", p)
	}
	// «Назад» с города — страна, со страны — P-1; записей нет, отметка снимается страной и профилем.
	ask, _ = tap(t, b, api, 42, uData(t, city, "⬅️ Назад"))
	grew("назад с города", 0)
	hasCheck(ask, "🇷🇺 Россия")
	same, _ := tap(t, b, api, 42, uData(t, ask, "✓ 🇷🇺 Россия")) // та же страна город не меняет
	grew("та же страна", 0)
	p1 = step(t, b, api, 42, uData(t, same, "Москва"), "s24_city_saved")
	if p = grew("город России", 1); p.City != "Москва" || p.CityKey != "moscow" || !p.AwaitingCityUntil.IsZero() {
		t.Fatalf("город России: %+v", p)
	}
	ask, _ = tap(t, b, api, 42, uData(t, p1, "🏠 Где живу"))
	p1, _ = tap(t, b, api, 42, uData(t, ask, "⬅️ Назад"))
	grew("назад со страны", 0)
	if !strings.Contains(p1.Form["text"], "🏠 Живу: 🇷🇺 Россия, Москва") {
		t.Errorf("P-1 после возврата со страны:\n%s", p1.Form["text"])
	}

	// 4. Виды спорта: «✓» у текущих, нажатие переключает и оставляет экран, снять можно все.
	dbExec(t, `UPDATE user_profiles SET sports = '{run,bike}' WHERE tg_id = 42`)
	ask = step(t, b, api, 42, uData(t, p1, "🏅 Спорт"), "s24_sports")
	hasCheck(ask, "🏃 Бег")
	if strings.Contains(ask.Form["text"], "Вопрос") {
		t.Errorf("в правке спорта есть «Вопрос N из 4»:\n%s", ask.Form["text"])
	}
	for _, bt := range ask.buttons(t) {
		if bt.Text == "✅ Готово" || bt.Text == "Пропустить вопрос" {
			t.Errorf("в правке спорта есть %q", bt.Text)
		}
	}
	toggled := step(t, b, api, 42, uData(t, ask, "🏊 Плавание"), "s24_sports_toggle")
	p = grew("плавание", 1)
	if strings.Join(p.Sports, ",") != "run,bike,swim" || p.Status != store.ProfileCompleted {
		t.Fatalf("спорт %v, статус %s: четыре поля заполнены — completed", p.Sports, p.Status)
	}
	if dbScalar(t, `SELECT (completed_at IS NOT NULL)::text FROM user_profiles WHERE tg_id = 42`) != "true" {
		t.Error("completed_at не задан")
	}
	for _, label := range []string{"✓ 🏃 Бег", "✓ 🚴 Велоспорт", "✓ 🏊 Плавание"} {
		toggled, _ = tap(t, b, api, 42, uData(t, toggled, label))
		grew("снять "+label, 1)
	}
	if p, _ = profileOf(t, b, 42); len(p.Sports) != 0 || p.Status != store.ProfileCompleted {
		t.Fatalf("после снятия всех отметок: %+v", p)
	}
	p1 = step(t, b, api, 42, uData(t, toggled, "⬅️ Назад"), "s24_sports_none")
	grew("назад со спорта", 0)
	if !strings.Contains(p1.Form["text"], "🏅 Спорт: не указан") {
		t.Errorf("P-1 без видов спорта:\n%s", p1.Form["text"])
	}

	// 5. «Назад» с экрана поля — P-1 без записи.
	ask, _ = tap(t, b, api, 42, uData(t, p1, "🚻 Пол"))
	step(t, b, api, 42, uData(t, ask, "⬅️ Назад"), "s24_back")
	grew("назад", 0)

	// 6. completed: изменение ответа оставляет completed, растит версию и updated_at.
	ask, _ = tap(t, b, api, 42, uData(t, p1, "🚻 Пол"))
	tap(t, b, api, 42, uData(t, ask, "Мужской"))
	if p = grew("пол в completed", 1); p.Gender != "m" || p.Status != store.ProfileCompleted {
		t.Fatalf("в completed: %+v", p)
	}
	if dbScalar(t, `SELECT (updated_at = $1)::text FROM user_profiles WHERE tg_id = 42`, fixtureNow) != "true" {
		t.Error("updated_at не обновлён")
	}

	// users за всё время не менялась (PRF-06).
	if after := userRow(t, 42); after != usersBefore {
		t.Errorf("правка профиля изменила users:\n%s\n%s", usersBefore, after)
	}

	// Статусы: из not_started и skipped первый ответ даёт in_progress; четыре поля — completed.
	for _, st := range []string{store.ProfileNotStarted, store.ProfileSkipped} {
		id := int64(50)
		if st == store.ProfileSkipped {
			id = 51
		}
		seedProfile(t, b, api, id, `status = '`+st+`'`)
		g, _ := tap(t, b, api, id, uData(t, profileCmd(t, b, api, id), "🚻 Пол"))
		tap(t, b, api, id, uData(t, g, "Мужской"))
		q, v := profileOf(t, b, id)
		if q.Status != store.ProfileInProgress || v != 2 ||
			dbScalar(t, `SELECT (started_at IS NOT NULL AND completed_at IS NULL)::text FROM user_profiles WHERE tg_id = $1`, id) != "true" {
			t.Errorf("%s → первый ответ: %+v v%d", st, q, v)
		}
	}
	seedProfile(t, b, api, 52, `status = 'in_progress', gender = 'f', age_group = 1, residence_country_code = 'ZZ'`)
	sp, _ := tap(t, b, api, 52, uData(t, profileCmd(t, b, api, 52), "🏅 Спорт"))
	tap(t, b, api, 52, uData(t, sp, "🏃 Бег"))
	if q, v := profileOf(t, b, 52); q.Status != store.ProfileCompleted || v != 2 ||
		dbScalar(t, `SELECT (completed_at IS NOT NULL)::text FROM user_profiles WHERE tg_id = 52`) != "true" {
		t.Errorf("четыре поля заполнены: %+v v%d", q, v)
	}
}

// usersState и profilesState — таблицы users и user_profiles целиком (все
// колонки, включая last_seen_at, profile_v и отметку ожидания города).
func usersState(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE(jsonb_agg(to_jsonb(u) ORDER BY u.tg_id), '[]'::jsonb)::text FROM users u`)
}

func profilesState(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.tg_id), '[]'::jsonb)::text FROM user_profiles p`)
}

// sayText отправляет текст и возвращает единственный ответ: новое сообщение.
func sayText(t *testing.T, b *Bot, api *fakeAPI, id int64, text string) call {
	t.Helper()
	api.reset()
	b.api.ProcessUpdate(context.Background(), textUpdate(id, text))
	s := api.of("sendMessage")
	if len(s) != 1 || api.total() != 1 {
		t.Fatalf("%q: ждали одно сообщение, вызовы %+v", text, api.calls)
	}
	checkCallbackLengths(t, s[0])
	return s[0]
}

// toCityScreen доводит нового пользователя до экрана города Беларуси в анкете
// (отметка ожидания стоит, режим w) и возвращает экран города и экран страны.
func toCityScreen(t *testing.T, b *Bot, api *fakeAPI, id int64, country string) call {
	t.Helper()
	q1, _ := tap(t, b, api, id, uData(t, startAs(t, b, api, id), "📝 Заполнить анкету"))
	q2, _ := tap(t, b, api, id, uData(t, q1, "Пропустить вопрос"))
	q3, _ := tap(t, b, api, id, uData(t, q2, "Пропустить вопрос"))
	city, _ := tap(t, b, api, id, uData(t, q3, country))
	return city
}

func awaitingMark(t *testing.T, id int64) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE(awaiting_city_mode, '-') FROM user_profiles WHERE tg_id = $1`, id)
}

// isAck — ответ на принятый текст города.
func isAck(c call) bool {
	return strings.HasPrefix(c.Form["text"], "🏠 Город проживания: ")
}

// TestS25CityText — S-25: город проживания текстом (D-09, D-10, PRF-08).
func TestS25CityText(t *testing.T) {
	_, api, b := countryFixture(t)
	logs := captureLogs(t)

	// 1. Город вне каталога: сохраняется как введён, ключ пуст, в поиск не идёт.
	toCityScreen(t, b, api, 42, "🇧🇾 Беларусь")
	usersBefore := userRow(t, 42)
	_, v := profileOf(t, b, 42)
	ack := sayText(t, b, api, 42, "  Гродно ")
	matchSnapshot(t, "s25_text_outside", renderCall(t, ack))
	p, nv := profileOf(t, b, 42)
	if p.City != "Гродно" || p.CityKey != "" || p.Country != "BY" || !p.AwaitingCityUntil.IsZero() || nv != v+1 {
		t.Fatalf("после текста «Гродно»: %+v v%d (было v%d)", p, nv, v)
	}
	if after := userRow(t, 42); after != usersBefore {
		t.Errorf("город текстом изменил users (фильтры не должны меняться):\n%s\n%s", usersBefore, after)
	}
	if awaitingMark(t, 42) != "-" {
		t.Errorf("отметка не снята: %s", profileRow(t, 42))
	}

	// 2. «Изменить» ставит отметку заново; совпадение с каталогом — название и ключ каталога.
	if mark := awaitingMark(t, 42); mark != "-" {
		t.Fatalf("перед «Изменить» отметка %q", mark)
	}
	tap(t, b, api, 42, uData(t, ack, "Изменить"))
	if p, _ = profileOf(t, b, 42); p.AwaitingCityMode != modeWizard || !p.AwaitingCityUntil.Equal(fixtureNow.Add(awaitCityFor)) {
		t.Fatalf("«Изменить» не поставил отметку: %+v", p)
	}
	ack = sayText(t, b, api, 42, "минск")
	matchSnapshot(t, "s25_text_catalog", renderCall(t, ack))
	if p, v2 := profileOf(t, b, 42); p.City != "Минск" || p.CityKey != "minsk" || v2 != nv+1 {
		t.Fatalf("после текста «минск»: %+v v%d", p, v2)
	}
	q4, _ := tap(t, b, api, 42, uData(t, ack, "Дальше")) // вопрос 4
	if !strings.Contains(q4.Form["text"], "<b>Вопрос 4 из 4</b>") {
		t.Errorf("«Дальше» в анкете:\n%s", q4.Form["text"])
	}

	// 3. «Другая страна»: экран без кнопок городов, текст сохраняется без ключа и без каталога.
	zz := toCityScreen(t, b, api, 43, "🌍 Другая страна")
	matchSnapshot(t, "s25_city_zz", renderCall(t, zz))
	ack = sayText(t, b, api, 43, "Варшава")
	if !isAck(ack) || !strings.Contains(ack.Form["text"], "Варшава") {
		t.Fatalf("ответ на «Варшава»: %s", ack.Form["text"])
	}
	tap(t, b, api, 43, uData(t, ack, "Изменить"))
	sayText(t, b, api, 43, "минск") // название каталога Беларуси стране ZZ не подставляется
	if p, _ := profileOf(t, b, 43); p.Country != "ZZ" || p.City != "минск" || p.CityKey != "" {
		t.Errorf("текст при «Другая страна»: %+v", p)
	}
	if !strings.Contains(profileCmd(t, b, api, 43).Form["text"], "🏠 Живу: 🌍 Другая страна, минск") {
		t.Error("в P-1 нет города при «Другая страна»")
	}

	// 4. Окно истекло: тот же текст идёт в поиск, профиль не меняется.
	toCityScreen(t, b, api, 44, "🇧🇾 Беларусь")
	state := profileRow(t, 44)
	b.Now = func() time.Time { return fixtureNow.Add(awaitCityFor + time.Minute) }
	matchSnapshot(t, "s25_expired", renderCall(t, sayText(t, b, api, 44, "Атлантида")))
	b.Now = func() time.Time { return fixtureNow }
	if profileRow(t, 44) != state {
		t.Errorf("истёкшее окно изменило профиль:\n%s\n%s", state, profileRow(t, 44))
	}

	// 5. Отметку снимают /start, /profile и актуальная кнопка u:; кнопки f:, s:, p:, m
	// и устаревшие u: — нет.
	toCityScreen(t, b, api, 46, "🇧🇾 Беларусь")
	setMark := func() {
		t.Helper()
		_, v := profileOf(t, b, 46)
		raw(b, api, 46, "u:a:c:w:v"+strconv.Itoa(v))
		if awaitingMark(t, 46) != modeWizard {
			t.Fatalf("отметка не поставлена: %s", profileRow(t, 46))
		}
	}
	searched := func(what string) {
		t.Helper()
		if c := sayText(t, b, api, 46, "Атлантида"); isAck(c) || !strings.Contains(c.Form["text"], "По запросу «Атлантида»") {
			t.Errorf("%s: текст не ушёл в поиск: %s", what, c.Form["text"])
		}
		if p, _ := profileOf(t, b, 46); p.City != "" {
			t.Errorf("%s: город сохранён: %+v", what, p)
		}
	}
	startAs(t, b, api, 46)
	if awaitingMark(t, 46) != "-" {
		t.Error("/start не снял отметку")
	}
	searched("/start")
	setMark()
	profileCmd(t, b, api, 46)
	if awaitingMark(t, 46) != "-" {
		t.Error("/profile не снял отметку")
	}
	searched("/profile")
	setMark()
	_, v = profileOf(t, b, 46)
	raw(b, api, 46, "u:a:a:w:v"+strconv.Itoa(v))
	if awaitingMark(t, 46) != "-" {
		t.Error("актуальная кнопка u: не сняла отметку")
	}
	searched("u:")
	setMark()
	for _, data := range []string{"m", "f:s:v1", "s:o:v1", "p:1:v1", "u:a:a:w:v0", "u:s:g:m:w:v0"} {
		raw(b, api, 46, data)
		if awaitingMark(t, 46) != modeWizard {
			t.Errorf("%q сняла отметку ожидания", data)
		}
	}
	if c := sayText(t, b, api, 46, "Брест"); !isAck(c) {
		t.Errorf("после кнопок, не снимающих отметку, текст не принят: %s", c.Form["text"])
	}

	// 6. HTML-символы: сохраняются как введены, показываются экранированными; в
	// callback_data и в логах текста города нет.
	toCityScreen(t, b, api, 45, "🇧🇾 Беларусь")
	const html = `<b>X</b> & "Y"`
	ack = sayText(t, b, api, 45, html)
	matchSnapshot(t, "s25_text_html", renderCall(t, ack))
	if p, _ := profileOf(t, b, 45); p.City != html {
		t.Errorf("город сохранён не как введён: %q", p.City)
	}
	if !strings.Contains(profileCmd(t, b, api, 45).Form["text"], "&lt;b&gt;X&lt;/b&gt; &amp; &#34;Y&#34;") {
		t.Error("P-1: город не экранирован")
	}
	for _, c := range []call{ack, profileCmd(t, b, api, 45), profileCmd(t, b, api, 42)} {
		for _, bt := range c.buttons(t) {
			if strings.ContainsAny(bt.CallbackData, "ХXYГродно<> &\"") {
				t.Errorf("текст города в callback_data: %q", bt.CallbackData)
			}
		}
	}
	for _, secret := range []string{"Гродно", "Варшава", "минск", "Брест", "Атлантида", "<b>X"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("текст %q в логе:\n%s", secret, logs)
		}
	}

	// 7. Режим редактирования: «Дальше» ведёт в P-1; статус — по правилу редактирования.
	seedProfile(t, b, api, 47, `status = 'in_progress', gender = 'f', age_group = 1, residence_country_code = 'BY', sports = '{run}'`)
	ask, _ := tap(t, b, api, 47, uData(t, profileCmd(t, b, api, 47), "🏠 Где живу"))
	tap(t, b, api, 47, uData(t, ask, "✓ 🇧🇾 Беларусь"))
	if awaitingMark(t, 47) != modeEdit {
		t.Fatalf("в режиме правки отметка %q", awaitingMark(t, 47))
	}
	// Четыре поля уже заполнены: первое же сохранение через профиль завершает анкету (v2),
	// город текстом — ещё одно изменение (v3).
	ack = sayText(t, b, api, 47, "Витебск")
	matchSnapshot(t, "s25_edit_next", renderCall(t, ack))
	if p, v := profileOf(t, b, 47); p.City != "Витебск" || p.Status != store.ProfileCompleted || v != 3 ||
		dbScalar(t, `SELECT (completed_at IS NOT NULL)::text FROM user_profiles WHERE tg_id = 47`) != "true" {
		t.Errorf("после города в режиме правки: %+v v%d", p, v)
	}
	next, _ := tap(t, b, api, 47, uData(t, ack, "Дальше"))
	if !strings.Contains(next.Form["text"], "👤 <b>Мой профиль</b>") || !strings.Contains(next.Form["text"], "🏠 Живу: 🇧🇾 Беларусь, Витебск") {
		t.Errorf("«Дальше» в режиме правки:\n%s", next.Form["text"])
	}

	// 8. Текст с «/» и слишком длинный: прежний ответ, ничего не сохраняется, отметка остаётся.
	tap(t, b, api, 47, uData(t, next, "🏠 Где живу"))
	_, v = profileOf(t, b, 47)
	raw(b, api, 47, "u:a:c:e:v"+strconv.Itoa(v))
	state = profileRow(t, 47)
	matchSnapshot(t, "s25_text_rejected", renderCall(t, sayText(t, b, api, 47, "/foo")))
	long := sayText(t, b, api, 47, strings.Repeat("я", maxCityInput+1))
	if long.Form["text"] != "Нажмите /start, чтобы найти старт" || profileRow(t, 47) != state || awaitingMark(t, 47) != modeEdit {
		t.Errorf("длинный текст: %q, профиль %s", long.Form["text"], profileRow(t, 47))
	}

	// 9. Отметка снята или истекла между чтением и записью: перепроверка под блокировкой
	// строки, текст идёт в поиск.
	toCityScreen(t, b, api, 48, "🇧🇾 Беларусь")
	state = profileRow(t, 48)
	n := 0
	b.Now = func() time.Time {
		if n++; n > 1 {
			return fixtureNow.Add(awaitCityFor + time.Minute) // первая проверка видит отметку, запись — уже нет
		}
		return fixtureNow
	}
	if c := sayText(t, b, api, 48, "Атлантида"); isAck(c) || profileRow(t, 48) != state {
		t.Errorf("гонка с истечением окна: %s, профиль %s", c.Form["text"], profileRow(t, 48))
	}
	b.Now = func() time.Time { return fixtureNow }
}

// TestProfileIndependence — PRF-06: действия с профилем не пишут в users, действия
// с фильтрами и поиском не пишут в user_profiles; сравниваются таблицы целиком.
func TestProfileIndependence(t *testing.T) {
	_, api, b := countryFixture(t)

	// (а) Профиль → users. Все пользователи созданы /start до снимка.
	skip := startAs(t, b, api, 44)
	wizard := startAs(t, b, api, 43)
	startAs(t, b, api, 42)
	before := usersState(t)

	tap(t, b, api, 44, uData(t, skip, "Пропустить")) // u:n
	q1, _ := tap(t, b, api, 43, uData(t, wizard, "📝 Заполнить анкету"))
	q2, _ := tap(t, b, api, 43, uData(t, q1, "Мужской"))
	q3, _ := tap(t, b, api, 43, uData(t, q2, "25–34"))
	city, _ := tap(t, b, api, 43, uData(t, q3, "🇧🇾 Беларусь"))
	q4, _ := tap(t, b, api, 43, uData(t, city, "Минск"))
	q4, _ = tap(t, b, api, 43, uData(t, q4, "🏃 Бег"))
	tap(t, b, api, 43, uData(t, q4, "✅ Готово")) // u:z
	for _, field := range []struct{ open, pick string }{
		{"🚻 Пол", "Женский"}, {"🎂 Возраст", "35–44"}, {"🏅 Спорт", "🏊 Плавание"},
	} {
		p1 := profileCmd(t, b, api, 43)
		ask, _ := tap(t, b, api, 43, uData(t, p1, field.open))
		tap(t, b, api, 43, uData(t, ask, field.pick))
	}
	ask, _ := tap(t, b, api, 43, uData(t, profileCmd(t, b, api, 43), "🏠 Где живу"))
	city, _ = tap(t, b, api, 43, uData(t, ask, "🇷🇺 Россия"))
	tap(t, b, api, 43, uData(t, city, "Москва"))
	ask, _ = tap(t, b, api, 43, uData(t, profileCmd(t, b, api, 43), "🏠 Где живу"))
	tap(t, b, api, 43, uData(t, ask, "✓ 🇷🇺 Россия"))
	ack := sayText(t, b, api, 43, "Тверь") // город текстом
	tap(t, b, api, 43, uData(t, ack, "Дальше"))
	if c := raw(b, api, 43, "u:o"); len(c) != 2 {
		t.Fatalf("u:o: %+v", c)
	}
	raw(b, api, 43, "u:s:g:m:e:v1") // устаревшая
	if after := usersState(t); after != before {
		t.Errorf("действия с профилем изменили users:\n%s\n%s", before, after)
	}
	if p, _ := profileOf(t, b, 43); p.City != "Тверь" || p.Gender != "f" {
		t.Fatalf("цепочка действий с профилем не выполнилась: %+v", p)
	}

	// (б) Фильтры и поиск → user_profiles. У пользователя 42 — заполненный профиль
	// и стоящая отметка ожидания города (истекшая к текстовым действиям).
	dbExec(t, `UPDATE user_profiles SET `+fullProfile+`, awaiting_city_until = $2, awaiting_city_mode = 'e' WHERE tg_id = $1`,
		int64(42), fixtureNow.Add(awaitCityFor))
	before = profilesState(t)
	items, err := b.Store.ListEvents(context.Background(), event.Filter{From: fixtureToday}, pageSize, 0)
	if err != nil || len(items) == 0 {
		t.Fatalf("события: %d, %v", len(items), err)
	}
	for _, data := range []string{
		"f:s", "f:d", "f:d:30", "f:c", "f:c:moscow", "f:n", "f:t", "f:t:road", "f:k", "f:k:BY", "f:k:*", "f:k:-", "f:x:c",
		"s:o", "s:r:o", "s:y:o", "p:1", "e:" + strconv.FormatInt(items[0].ID, 10) + ":1", "m",
	} {
		raw(b, api, 42, fresh(t, b, data))
	}
	if after := profilesState(t); after != before {
		t.Errorf("кнопки фильтров изменили user_profiles:\n%s\n%s", before, after)
	}
	b.Now = func() time.Time { return fixtureNow.Add(awaitCityFor + time.Minute) } // отметка истекла: текст — поиск
	for _, text := range []string{"Атлантида", "Москва", "Минский"} {
		sayText(t, b, api, 42, text)
	}
	b.Now = func() time.Time { return fixtureNow }
	if after := profilesState(t); after != before {
		t.Errorf("текстовый поиск и город фильтра текстом изменили user_profiles:\n%s\n%s", before, after)
	}
}

// forcedProfileStatus — статус анкеты, который newTestBot выставляет пользователю
// после каждого /start. Непуст только внутри TestProfileNoLimits.
var forcedProfileStatus string

// Счётчики выставленных статусов и неудач (сверяет TestProfileNoLimits).
var forcedSet, forcedFailed atomic.Int64

// forceProfileStatusMW после обработки личного /start выставляет статус анкеты
// отправителя прямым запросом к тестовой базе. Ответ бота уже отправлен, поэтому
// экран, который видит пользователь на этом /start, не меняется. Неудача
// (в том числе закрытая фикстура) считается, а не роняет тест.
func forceProfileStatusMW(status string) tgbot.Middleware {
	return func(next tgbot.HandlerFunc) tgbot.HandlerFunc {
		return func(ctx context.Context, b *tgbot.Bot, u *models.Update) {
			next(ctx, b, u)
			m := u.Message
			if m == nil || m.From == nil || m.Chat.Type != models.ChatTypePrivate || m.Text != "/start" {
				return
			}
			conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				forcedFailed.Add(1)
				return
			}
			defer conn.Close(ctx)
			tag, err := conn.Exec(ctx, `UPDATE user_profiles SET status = $2 WHERE tg_id = $1`, m.From.ID, status)
			if err != nil || tag.RowsAffected() != 1 {
				forcedFailed.Add(1)
				return
			}
			forcedSet.Add(1)
		}
	}
}

// TestProfileNoLimits — PRF-03, D-01: сценарии S-01…S-17 Phase 8 проходят с теми
// же эталонами, какой бы ни была анкета: отказ от неё ничего не ограничивает.
// Статус `not_started` покрыт обычным запуском этих же сценариев.
func TestProfileNoLimits(t *testing.T) {
	scenarios := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"S01", TestS01MainMenu}, {"S02", TestS02Search}, {"S03", TestS03Date}, {"S04", TestS04City},
		{"S05", TestS05CityText}, {"S06", TestS06Distance}, {"S07", TestS07Type}, {"S08", TestS08Country},
		{"S09", TestS09List}, {"S10", TestS10Card}, {"S11", TestS11Empty}, {"S12", TestS12Settings},
		{"S13", TestS13Reset}, {"S14", TestS14StaleFilter}, {"S15", TestS15StaleNavigation},
		{"S16", TestS16Unknown}, {"S17", TestS17InternalError},
	}
	for _, status := range []string{"skipped", "in_progress", "completed"} {
		t.Run(status, func(t *testing.T) {
			forcedProfileStatus = status
			t.Cleanup(func() { forcedProfileStatus = "" })
			set0 := forcedSet.Load()
			for _, sc := range scenarios {
				failed0 := forcedFailed.Load()
				t.Run(sc.name, func(t *testing.T) {
					sc.fn(t)
					if n := forcedFailed.Load() - failed0; n != 0 {
						t.Errorf("статус %q не выставлен после %d /start", status, n)
					}
				})
			}
			if forcedSet.Load() == set0 {
				t.Errorf("статус %q ни разу не выставлен: проверка ничего не проверила", status)
			}
		})
	}
}

// callbackCharsRE — допустимые знаки callback_data: свободного текста нет (D-09).
var callbackCharsRE = regexp.MustCompile(`^[A-Za-z0-9:_*-]+$`)

// TestProfileNoAnswersInLogs — PRF-09, D-09: ответы анкеты и текст города не
// попадают в логи, а callback_data кнопок всех экранов профиля не несёт
// свободного текста.
func TestProfileNoAnswersInLogs(t *testing.T) {
	_, api, b := countryFixture(t)
	logs := captureLogs(t)
	var screens []call
	see := func(c call) call { screens = append(screens, c); return c }
	press := func(c call, label string) call {
		edit, _ := tap(t, b, api, 42, uData(t, c, label))
		return see(edit)
	}

	// Анкета с приметными ответами; город — текстом.
	q1 := press(see(startAs(t, b, api, 42)), "📝 Заполнить анкету")
	q3 := press(press(q1, "Женский"), "45–54")
	press(q3, "🇧🇾 Беларусь")
	ack := see(sayText(t, b, api, 42, "Секретоград"))
	q4 := press(ack, "Дальше")
	q4 = press(q4, "🚴 Велоспорт")
	q4 = press(q4, "🏊 Плавание")
	press(q4, "✅ Готово")

	// Редактирование: пол, возраст, спорт, страна и снова город текстом.
	for _, f := range []struct{ open, pick string }{
		{"🚻 Пол", "Мужской"}, {"🎂 Возраст", "25–34"}, {"🏅 Спорт", "🏃 Бег"},
	} {
		press(press(see(profileCmd(t, b, api, 42)), f.open), f.pick)
	}
	press(press(see(profileCmd(t, b, api, 42)), "🏠 Где живу"), "🇷🇺 Россия")
	press(see(sayText(t, b, api, 42, "Секретоград")), "Дальше")
	if p, _ := profileOf(t, b, 42); p.City != "Секретоград" || p.Gender != "m" {
		t.Fatalf("цепочка не выполнилась: %+v", p)
	}

	// Устаревшая кнопка и кнопки вне грамматики: данные кнопки в лог не идут.
	for _, data := range []string{"u:s:g:m:e:v1", "u:s:g:Женский:Секретоград:v1", "u:x:Секретоград"} {
		raw(b, api, 42, data)
	}

	for _, leak := range []string{"Секретоград", "Женский", "45–54", "bike", "swim", "u:s:", "u:a:"} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("в логах есть %q:\n%s", leak, logs)
		}
	}
	for _, c := range screens {
		for _, bt := range c.buttons(t) {
			if !callbackCharsRE.MatchString(bt.CallbackData) {
				t.Errorf("callback_data %q экрана %q: свободный текст или недопустимые знаки", bt.CallbackData, c.Form["text"])
			}
		}
	}
}
