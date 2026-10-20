package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Избранное (14-UX-CONTRACT.md, сценарии S-44…S-47). Эталоны
// testdata/screens/s44_*…s47_* написаны вручную по контракту. Кнопки w:
// нажимаются как есть: версии фильтров у них нет, а строка users избранному не
// нужна, поэтому press и fresh (они пишут в users) здесь не используются.

// favTap нажимает кнопку data от имени userID в сообщении from — бот получает
// его текст и клавиатуру так, как их отдаёт Telegram; from == nil — сообщение
// без клавиатуры. Возвращает все вызовы Bot API по порядку.
func favTap(t *testing.T, b *Bot, api *fakeAPI, userID int64, from *call, data string) []call {
	t.Helper()
	u := cbUpdate(userID, 7, data)
	if from != nil {
		m := u.CallbackQuery.Message.Message
		m.Text = html.UnescapeString(tagRE.ReplaceAllString(from.Form["text"], ""))
		if raw := from.Form["reply_markup"]; raw != "" {
			m.ReplyMarkup = &models.InlineKeyboardMarkup{}
			if err := json.Unmarshal([]byte(raw), m.ReplyMarkup); err != nil {
				t.Fatalf("reply_markup: %v", err)
			}
		}
	}
	api.reset()
	b.api.ProcessUpdate(context.Background(), u)
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]call(nil), api.calls...)
}

// methods — методы вызовов через пробел: «editMessageReplyMarkup answerCallbackQuery».
func methods(calls []call) string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Method
	}
	return strings.Join(out, " ")
}

// renderCalls — снимок вызовов подряд.
func renderCalls(t *testing.T, calls []call) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range calls {
		sb.WriteString(renderCall(t, c))
	}
	return sb.String()
}

// favCount — число строк избранного, отвечающих условию where ("" — все).
func favCount(t *testing.T, where string, args ...any) string {
	t.Helper()
	q := `SELECT count(*)::text FROM favorites`
	if where != "" {
		q += " WHERE " + where
	}
	return dbScalar(t, q, args...)
}

// favs — все записи избранного: «пользователь:событие» через запятую.
func favs(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE(string_agg(tg_id || ':' || event_id, ',' ORDER BY tg_id, event_id), '') FROM favorites`)
}

// favEdit — нажатие, на которое бот правит текст сообщения: ровно одна правка
// и один ответ без уведомления.
func favEdit(t *testing.T, b *Bot, api *fakeAPI, userID int64, from *call, data string) call {
	t.Helper()
	calls := favTap(t, b, api, userID, from, data)
	if methods(calls) != "editMessageText answerCallbackQuery" || calls[1].Form["text"] != "" {
		t.Fatalf("%s: вызовы %q, уведомление %q; want правку текста и пустой ответ", data, methods(calls), calls[len(calls)-1].Form["text"])
	}
	checkCallbackLengths(t, calls[0])
	return calls[0]
}

// favSnap — favEdit со сверкой правки с эталоном (эталон шага без правки
// клавиатуры — один вызов).
func favSnap(t *testing.T, b *Bot, api *fakeAPI, userID int64, from *call, data, snap string) call {
	t.Helper()
	c := favEdit(t, b, api, userID, from, data)
	matchSnapshot(t, snap, renderCall(t, c))
	return c
}

// favKeys — нажатие w:a либо w:x в сообщении from: все вызовы сверяются с
// эталоном (правка клавиатуры и ответ либо один ответ), правок текста нет.
// Возвращает сообщение from с клавиатурой после нажатия.
func favKeys(t *testing.T, b *Bot, api *fakeAPI, userID int64, from *call, data, snap string) call {
	t.Helper()
	calls := favTap(t, b, api, userID, from, data)
	matchSnapshot(t, snap, renderCalls(t, calls))
	if m := methods(calls); m != "editMessageReplyMarkup answerCallbackQuery" && m != "answerCallbackQuery" {
		t.Fatalf("%s: вызовы %q", data, m)
	}
	after := call{Method: "editMessageText", Form: map[string]string{}}
	if from != nil {
		for k, v := range from.Form {
			after.Form[k] = v
		}
	}
	if len(calls) == 2 {
		checkCallbackLengths(t, calls[0])
		after.Form["reply_markup"] = calls[0].Form["reply_markup"]
	}
	return after
}

// TestFavTracer — сквозной путь: карточка из списка → «⭐ В избранное» →
// правка одной клавиатуры и уведомление → раздел показывает старт.
func TestFavTracer(t *testing.T) {
	_, api, b := seeded(t)
	open := firstCard(t, press(t, b, api, "p:1:v1"))
	id := cardID(t, open)
	card := press(t, b, api, open)
	add, del := fmt.Sprintf("w:a:%d", id), fmt.Sprintf("w:x:%d", id)
	// Ряд избранного — «⭐» и «📤 Поделиться» (ссылка, без callback).
	if ds := datas(t, card); len(ds) < 4 || ds[len(ds)-4] != add || ds[len(ds)-2] != fmt.Sprintf("q:o:%d", id) || ds[len(ds)-1] != "p:1:v1" {
		t.Fatalf("карточка: кнопки %v, want %s и строку заявки перед p:1:v1", ds, add)
	}

	calls := favTap(t, b, api, 42, &card, add)
	if methods(calls) != "editMessageReplyMarkup answerCallbackQuery" {
		t.Fatalf("%s: вызовы %q, want одна правка клавиатуры и один ответ", add, methods(calls))
	}
	if got := calls[1].Form["text"]; got != "Добавлено в избранное. Сообщу об изменениях и напомню за 7 дней до старта" {
		t.Errorf("уведомление %q", got)
	}
	if calls[0].Form["message_id"] != "7" || calls[0].Form["text"] != "" {
		t.Errorf("правка клавиатуры: %+v", calls[0].Form)
	}
	was, now := datas(t, card), datas(t, calls[0])
	was[len(was)-4] = del
	if strings.Join(now, " ") != strings.Join(was, " ") {
		t.Errorf("клавиатура после нажатия %v, want %v", now, was)
	}
	if got := favCount(t, "tg_id = 42 AND event_id = $1", id); got != "1" || favCount(t, "") != "1" {
		t.Fatalf("строк избранного (42, %d) — %s, всего %s; want 1 и 1", id, got, favCount(t, ""))
	}

	sec := favTap(t, b, api, 42, nil, "w:o")
	if methods(sec) != "editMessageText answerCallbackQuery" {
		t.Fatalf("w:o: вызовы %q", methods(sec))
	}
	var starts []string
	for _, d := range datas(t, sec[0]) {
		if strings.HasPrefix(d, "w:e:") {
			starts = append(starts, d)
		}
	}
	if want := fmt.Sprintf("w:e:%d:1", id); len(starts) != 1 || starts[0] != want || !strings.HasPrefix(sec[0].Form["text"], "⭐ <b>Избранное: 1</b>\n") {
		t.Fatalf("раздел: текст %q, старты %v; want один %s", sec[0].Form["text"], starts, want)
	}
	if !t.Failed() {
		t.Logf("TRACER fav=added section=%d", len(starts))
	}
}

// TestS44FavToggle — S-44: строка избранного в карточке, добавление и удаление.
func TestS44FavToggle(t *testing.T) {
	s, api, b := skiBot(t)
	filtersV(t, b) // пользователь 42: фильтры {}, версия 1
	added := func(tg, id int64) string {
		return dbScalar(t, `SELECT COALESCE((SELECT created_at::text FROM favorites WHERE tg_id = $1 AND event_id = $2), '')`, tg, id)
	}

	// 1. Карточка из списка: строка избранного перед «Назад».
	card := favSnap(t, b, api, 42, nil, "e:110:1:v1", "s44_card")
	if favs(t) != "" {
		t.Fatalf("шаг 1: записи %q", favs(t))
	}

	// 2. Добавление: запись со временем бота, правка одной клавиатуры, уведомление.
	inFav := favKeys(t, b, api, 42, &card, "w:a:110", "s44_added")
	first := added(42, 110)
	if favs(t) != "42:110" || first != dbScalar(t, `SELECT $1::timestamptz::text`, fixtureNow) {
		t.Errorf("шаг 2: записи %q, время %q", favs(t), first)
	}
	if n := dbScalar(t, `SELECT count(*)::text FROM outbox`); n != "0" {
		t.Errorf("шаг 2: строк очереди %s, want 0", n)
	}

	// 3. Повторно из старой копии карточки: строка одна, время прежнее.
	b.Now = func() time.Time { return fixtureNow.Add(time.Hour) }
	favKeys(t, b, api, 42, &card, "w:a:110", "s44_already")
	if favs(t) != "42:110" || added(42, 110) != first {
		t.Errorf("шаг 3: записи %q, время %q, want прежнее %q", favs(t), added(42, 110), first)
	}
	// Та же кнопка в актуальной копии: клавиатура уже такая — правки нет.
	if calls := favTap(t, b, api, 42, &inFav, "w:a:110"); methods(calls) != "answerCallbackQuery" || calls[0].Form["text"] != favAlreadyNotice {
		t.Errorf("шаг 3: повтор в актуальной копии: вызовы %q", methods(calls))
	}

	// 4–5. Удаление, затем повторно из старой копии: те же вызовы.
	favKeys(t, b, api, 42, &inFav, "w:x:110", "s44_removed")
	if favs(t) != "" {
		t.Errorf("шаг 4: записи %q", favs(t))
	}
	favKeys(t, b, api, 42, &inFav, "w:x:110", "s44_removed")
	if favs(t) != "" {
		t.Errorf("шаг 5: записи %q", favs(t))
	}

	// 7. Отменённый будущий старт добавить можно.
	storetest.SetStatus(t, 110, event.StatusCancelled)
	cancelled := favSnap(t, b, api, 42, nil, "e:110:1:v1", "s44_card_cancelled")
	if calls := favTap(t, b, api, 42, &cancelled, "w:a:110"); methods(calls) != "editMessageReplyMarkup answerCallbackQuery" || calls[1].Form["text"] != favAddedNotice || favs(t) != "42:110" {
		t.Errorf("шаг 7: добавление отменённого: вызовы %q, записи %q", methods(calls), favs(t))
	}
	favTap(t, b, api, 42, nil, "w:x:110")

	// 8. Недоступное событие: строки избранного нет.
	gone := favSnap(t, b, api, 42, nil, "e:99999:1:v1", "s44_gone")
	if ds := datas(t, gone); len(ds) != 1 || strings.HasPrefix(ds[0], "w:") {
		t.Errorf("шаг 8: кнопки %v", ds)
	}

	// 9. Пользователь без строки users: избранное от неё не зависит.
	card77 := favEdit(t, b, api, 77, nil, "e:110:1:v0")
	favKeys(t, b, api, 77, &card77, "w:a:110", "s44_no_user")
	if favs(t) != "77:110" || userRow(t, 77) != "" {
		t.Errorf("шаг 9: записи %q, строка users %q", favs(t), userRow(t, 77))
	}

	// 6. Карточка из результатов запроса: строка возврата к результатам остаётся
	// последней. Шаг идёт после 7–9: он меняет версию фильтров, а те записаны
	// для версии 1.
	setUserFilters(t, s, `{"country":"*"}`)
	results := say(t, b, api, "этап")
	found := favSnap(t, b, api, 42, &results, "g:135:v2", "s44_search_card")
	found = favKeys(t, b, api, 42, &found, "w:a:135", "s44_search_added")
	if favs(t) != "42:135,77:110" {
		t.Errorf("шаг 6: записи %q", favs(t))
	}
	if back := favEdit(t, b, api, 42, &found, "g:b:v2"); !strings.HasPrefix(back.Form["text"], "Запрос «этап»") {
		t.Errorf("шаг 6: «Назад» открыл не результаты запроса: %q", back.Form["text"])
	}
}

// TestS45FavList — S-45: раздел, страницы и карточка из раздела.
func TestS45FavList(t *testing.T) {
	_, api, b := skiBot(t)
	filtersV(t, b)
	for _, id := range []int64{48, 135, 6, 110, 29, 10, 40, 19, 23} {
		if out, err := b.Store.AddFavorite(context.Background(), 42, id, fixtureToday, fixtureNow, favLimit); err != nil || out != store.FavAdded {
			t.Fatalf("AddFavorite(%d) = %v, %v", id, out, err)
		}
	}
	before := dbState(t) + favs(t)

	// 1–3. Страницы; страница за пределами — последняя.
	p1 := favSnap(t, b, api, 42, nil, "w:o", "s45_page1")
	p2 := favSnap(t, b, api, 42, &p1, "w:p:2", "s45_page2")
	favSnap(t, b, api, 42, nil, "w:p:9", "s45_page2")

	// 4–5. Карточка из раздела и возврат на ту же страницу.
	card := favSnap(t, b, api, 42, &p2, "w:e:135:2", "s45_card")
	favSnap(t, b, api, 42, &card, "w:p:2", "s45_page2")
	if after := dbState(t) + favs(t); after != before {
		t.Errorf("раздел и карточка из раздела записали в базу:\n%s\n%s", before, after)
	}

	// 6. Удаление из карточки раздела и возврат.
	card = favKeys(t, b, api, 42, &card, "w:x:135", "s45_card_removed")
	if strings.Contains(favs(t), "42:135") {
		t.Errorf("шаг 6: записи %q", favs(t))
	}
	favSnap(t, b, api, 42, &card, "w:p:2", "s45_after_remove")

	// 7. Карточка со ссылкой.
	favSnap(t, b, api, 42, nil, "w:e:110:1", "s45_card_link")
}

// favPut записывает строки избранного пользователя 42 напрямую — так тест
// получает записи на событие, которое слито или скрыто позже.
func favPut(t *testing.T, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, $1, $2)`, id, fixtureNow)
	}
}

func favClear(t *testing.T) {
	t.Helper()
	dbExec(t, `DELETE FROM favorites`)
}

// TestS46FavVisibility — S-46: пустой раздел, прошедшие, слитые и скрытые
// старты. Запись о старте остаётся в базе, раздел его не показывает.
func TestS46FavVisibility(t *testing.T) {
	_, api, b := skiBot(t)
	filtersV(t, b)

	// 1. Пустой раздел; страница пустого раздела — тот же экран; возврат в
	// главное меню узнаётся по тексту (эталон меню — только в S-51).
	empty := favSnap(t, b, api, 42, nil, "w:o", "s46_empty")
	favSnap(t, b, api, 42, nil, "w:p:3", "s46_empty")
	if menu := favEdit(t, b, api, 42, &empty, empty.button(t, backLabel)); !isMainMenu(menu) {
		t.Errorf("шаг 1: «Назад» открыл не главное меню: %q", menu.Form["text"])
	}

	// 2. Старт 110 прошёл: в разделе его нет, запись на месте.
	favPut(t, 110, 135)
	b.Now = func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC) }
	favSnap(t, b, api, 42, nil, "w:o", "s46_past_hidden")
	if favs(t) != "42:110,42:135" {
		t.Errorf("шаг 2: записи %q", favs(t))
	}
	b.Now = func() time.Time { return fixtureNow }

	// 3. Записи на слитое и на корневое событие: корень показан один раз;
	// карточка слитого открывает корень; удаление убирает обе записи.
	// Запись только на слитое событие показывает тот же корень.
	favClear(t)
	favPut(t, 110)
	storetest.MergeEvent(t, 110, 52)
	favSnap(t, b, api, 42, nil, "w:o", "s46_merged")
	favPut(t, 52)
	sec := favSnap(t, b, api, 42, nil, "w:o", "s46_merged")
	card := favEdit(t, b, api, 42, &sec, "w:e:110:1")
	if ds := datas(t, card); !strings.Contains(card.Form["text"], "Шарташ") || len(ds) < 4 || ds[len(ds)-4] != "w:x:52" || ds[len(ds)-2] != "q:o:52" || ds[len(ds)-1] != "w:p:1" {
		t.Errorf("шаг 3: карточка слитого: %q, кнопки %v", card.Form["text"], ds)
	}
	if calls := favTap(t, b, api, 42, &card, "w:x:52"); methods(calls) != "editMessageReplyMarkup answerCallbackQuery" || favs(t) != "" {
		t.Errorf("шаг 3: удаление: вызовы %q, записи %q", methods(calls), favs(t))
	}

	// 4. Скрытый старт: пустой раздел, запись на месте; вернулся — снова виден.
	favPut(t, 135)
	storetest.HideEvent(t, 135)
	favSnap(t, b, api, 42, nil, "w:o", "s46_empty")
	if favs(t) != "42:135" {
		t.Errorf("шаг 4: записи %q", favs(t))
	}
	dbExec(t, `UPDATE events SET is_listed = true WHERE id = 135`)
	favSnap(t, b, api, 42, nil, "w:o", "s46_returned")

	// 5. Выключенный вид спорта — как скрытый старт.
	storetest.DisableSport(t, event.SportSkiing)
	favSnap(t, b, api, 42, nil, "w:o", "s46_empty")
	storetest.EnableSport(t, event.SportSkiing)
	favSnap(t, b, api, 42, nil, "w:o", "s46_returned")
	if favs(t) != "42:135" {
		t.Errorf("шаг 5: записи %q", favs(t))
	}
}

// favLogRE — строка лога о действии с избранным: только действие и исход.
var favLogRE = regexp.MustCompile(`msg=favorite action=(add|remove) outcome=(added|already|removed|limit|past|gone)$`)

// TestS47FavLimitStale — S-47: предел, отказы и устаревшие кнопки. Отказ
// ничего не записывает и клавиатуру не правит.
func TestS47FavLimitStale(t *testing.T) {
	s, api, b := skiBot(t)
	filtersV(t, b)
	logs := captureLogs(t)
	ctx := context.Background()
	oct3 := func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC) }
	card := favEdit(t, b, api, 42, nil, "e:110:1:v1")

	// 2. Прошедший старт.
	b.Now = oct3
	favKeys(t, b, api, 42, &card, "w:a:110", "s47_past")
	b.Now = func() time.Time { return fixtureNow }

	// 3. Несуществующее, скрытое событие и событие выключенного вида спорта.
	favKeys(t, b, api, 42, &card, "w:a:99999", "s47_gone")
	storetest.HideEvent(t, 52)
	favKeys(t, b, api, 42, &card, "w:a:52", "s47_gone")
	storetest.DisableSport(t, event.SportSkiing)
	favKeys(t, b, api, 42, &card, "w:a:135", "s47_gone")
	storetest.EnableSport(t, event.SportSkiing)
	if favs(t) != "" {
		t.Fatalf("шаги 2–3: отказ создал запись: %q", favs(t))
	}

	// 4. Скрытый старт убрать можно; добавить обратно — нельзя.
	favPut(t, 135)
	storetest.HideEvent(t, 135)
	one := botMsg(t, []models.InlineKeyboardButton{{Text: "✖️ Убрать из избранного", CallbackData: "w:x:135"}})
	one = favKeys(t, b, api, 42, &one, "w:x:135", "s47_remove_hidden")
	if favs(t) != "" {
		t.Errorf("шаг 4: записи %q", favs(t))
	}
	favKeys(t, b, api, 42, &one, "w:a:135", "s47_gone")
	// Удаление отсутствующей записи — без ошибки, то же уведомление.
	if calls := favTap(t, b, api, 42, nil, "w:x:135"); methods(calls) != "answerCallbackQuery" || calls[0].Form["text"] != favRemovedNotice {
		t.Errorf("шаг 4: удаление отсутствующей записи: вызовы %q", methods(calls))
	}

	// 5. Нажатие в недоступном (старом) сообщении: запись есть, правки нет.
	old := &models.Update{ID: 3, CallbackQuery: &models.CallbackQuery{
		ID: "cqold", From: models.User{ID: 42}, Data: "w:a:110",
		Message: models.MaybeInaccessibleMessage{
			Type: models.MaybeInaccessibleMessageTypeInaccessibleMessage,
			InaccessibleMessage: &models.InaccessibleMessage{
				Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, MessageID: 9},
		},
	}}
	api.reset()
	b.api.ProcessUpdate(ctx, old)
	matchSnapshot(t, "s47_no_message", renderCalls(t, api.calls))
	if favs(t) != "42:110" {
		t.Errorf("шаг 5: записи %q", favs(t))
	}
	favClear(t)

	// 6. Данные вне грамматики.
	before := dbState(t)
	for _, data := range []string{"w:", "w:a", "w:a:0", "w:a:01", "w:a:abc", "w:e:110", "w:p:0", "w:p:10001", "w:z:1", "w:o:1", "w:a:110:v1"} {
		matchSnapshot(t, "s47_stale", renderCall(t, onlyToast(t, data, raw(b, api, 42, data))))
		if dbState(t) != before || favs(t) != "" {
			t.Errorf("шаг 6: %q записал в базу", data)
		}
	}

	// 7. Карточка из раздела для старта не из избранного и для недоступного.
	favSnap(t, b, api, 42, nil, "w:e:110:1", "s47_card_not_fav")
	favSnap(t, b, api, 42, nil, "w:e:99999:1", "s47_card_gone")

	// 1. Предел: пятьдесят будущих стартов, пятьдесят первый не добавляется.
	items := allItems(t, s, event.Filter{From: fixtureToday})
	for _, it := range items[:favLimit] {
		if out, err := s.AddFavorite(ctx, 4242424242, it.ID, fixtureToday, fixtureNow, favLimit); err != nil || out != store.FavAdded {
			t.Fatalf("AddFavorite(%d) = %v, %v", it.ID, out, err)
		}
	}
	next := items[favLimit].ID
	extra := favEdit(t, b, api, 4242424242, nil, fmt.Sprintf("e:%d:1:v0", next))
	favKeys(t, b, api, 4242424242, &extra, fmt.Sprintf("w:a:%d", next), "s47_limit")
	if n := favCount(t, "tg_id = 4242424242"); n != "50" {
		t.Errorf("шаг 1: записей %s, want 50", n)
	}
	// Один из пятидесяти прошёл — добавление проходит.
	b.Now = oct3
	if calls := favTap(t, b, api, 4242424242, &extra, fmt.Sprintf("w:a:%d", next)); methods(calls) != "editMessageReplyMarkup answerCallbackQuery" || calls[1].Form["text"] != favAddedNotice {
		t.Errorf("шаг 1: добавление при прошедшем старте: вызовы %q", methods(calls))
	}
	if n := favCount(t, "tg_id = 4242424242"); n != "51" {
		t.Errorf("шаг 1: записей %s, want 51", n)
	}

	// В логе — только действие и исход: ни пользователя, ни названия старта.
	out := logs.String()
	seen := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "msg=favorite") {
			seen++
			if !favLogRE.MatchString(line) {
				t.Errorf("строка лога об избранном: %q", line)
			}
		}
	}
	for _, leak := range []string{"4242424242", "Кросс памяти", "Знаменских", "Виктория", "Шарташ", "tg_id", "event_id"} {
		if strings.Contains(out, leak) {
			t.Errorf("в логе есть %q:\n%s", leak, out)
		}
	}
	if seen < 8 {
		t.Errorf("строк лога об избранном %d — проверка ничего не проверила:\n%s", seen, out)
	}
}

// botMsg — сообщение бота с клавиатурой rows, собранной вручную по блоку
// контракта (экранов сообщений ещё нет).
func botMsg(t *testing.T, rows ...[]models.InlineKeyboardButton) call {
	t.Helper()
	kb, err := json.Marshal(models.InlineKeyboardMarkup{InlineKeyboard: rows})
	if err != nil {
		t.Fatal(err)
	}
	return call{Method: "sendMessage", Form: map[string]string{"text": "сообщение бота", "reply_markup": string(kb)}}
}

// kbLines — клавиатура вызова строками эталона: «[подпись] → data».
func kbLines(t *testing.T, c call) string {
	t.Helper()
	r := renderCall(t, c)
	return r[strings.LastIndex(r, "---\n")+4:]
}

// TestFavMessageKeyboard — правило правки клавиатуры на сообщениях бота
// (контракт, раздел 4; клавиатуры — по блокам N-1 и N-4) и кнопка «Открыть
// старт» (w:n).
func TestFavMessageKeyboard(t *testing.T) {
	_, api, b := skiBot(t)
	const (
		openL = "📋 Открыть старт"
		delL  = "✖️ Убрать из избранного"
		addL  = "⭐ В избранное"
	)
	k := func(text, data string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: text, CallbackData: data}
	}
	// swap нажимает data в msg и возвращает клавиатуру после правки ("" — правки не было) и уведомление.
	swap := func(msg call, data string) (kb, notice string) {
		t.Helper()
		calls := favTap(t, b, api, 42, &msg, data)
		switch methods(calls) {
		case "editMessageReplyMarkup answerCallbackQuery":
			if calls[0].Form["text"] != "" || calls[0].Form["message_id"] != "7" {
				t.Errorf("%s: правка клавиатуры несёт текст или чужое сообщение: %+v", data, calls[0].Form)
			}
			return kbLines(t, calls[0]), calls[1].Form["text"]
		case "answerCallbackQuery":
			return "", calls[0].Form["text"]
		}
		t.Fatalf("%s: вызовы %q", data, methods(calls))
		return "", ""
	}

	// Вид N-1: две кнопки, каждая в своём ряду.
	favPut(t, 52)
	n1 := botMsg(t, []models.InlineKeyboardButton{k(openL, "w:n:52")}, []models.InlineKeyboardButton{k(delL, "w:x:52")})
	kb, notice := swap(n1, "w:x:52")
	if want := "[" + openL + "] → w:n:52\n[" + addL + "] → w:a:52\n"; kb != want || notice != favRemovedNotice || favs(t) != "" {
		t.Errorf("N-1, w:x: клавиатура %q, уведомление %q, записи %q", kb, notice, favs(t))
	}
	n1after := botMsg(t, []models.InlineKeyboardButton{k(openL, "w:n:52")}, []models.InlineKeyboardButton{k(addL, "w:a:52")})
	kb, notice = swap(n1after, "w:a:52")
	if want := "[" + openL + "] → w:n:52\n[" + delL + "] → w:x:52\n"; kb != want || notice != favAddedNotice || favs(t) != "42:52" {
		t.Errorf("N-1, w:a: клавиатура %q, уведомление %q, записи %q", kb, notice, favs(t))
	}

	// Две кнопки в одном ряду: меняется только кнопка избранного.
	inRow := botMsg(t, []models.InlineKeyboardButton{k(openL, "w:n:52"), k(delL, "w:x:52")})
	if kb, _ := swap(inRow, "w:x:52"); kb != "["+openL+"] → w:n:52 | ["+addL+"] → w:a:52\n" {
		t.Errorf("две кнопки в ряду: клавиатура %q", kb)
	}
	// Кнопка избранного стоит первой, не предпоследней строкой: ищется по данным.
	first := botMsg(t, []models.InlineKeyboardButton{k(addL, "w:a:52")}, []models.InlineKeyboardButton{k(openL, "w:n:52")}, []models.InlineKeyboardButton{k(backLabel, "w:p:1")})
	if kb, _ := swap(first, "w:a:52"); kb != "["+delL+"] → w:x:52\n["+openL+"] → w:n:52\n["+backLabel+"] → w:p:1\n" {
		t.Errorf("кнопка избранного первой строкой: клавиатура %q", kb)
	}
	// Кнопка избранного другого события не трогается.
	favPut(t, 110)
	two := botMsg(t, []models.InlineKeyboardButton{k(delL, "w:x:52")}, []models.InlineKeyboardButton{k(delL, "w:x:110")})
	if kb, _ := swap(two, "w:x:52"); kb != "["+addL+"] → w:a:52\n["+delL+"] → w:x:110\n" || favs(t) != "42:110" {
		t.Errorf("чужая кнопка избранного: клавиатура %q, записи %q", kb, favs(t))
	}
	// Клавиатура без кнопки избранного этого события: правки нет, запись меняется.
	none := botMsg(t, []models.InlineKeyboardButton{k(openL, "w:n:52")}, []models.InlineKeyboardButton{k(delL, "w:x:52")})
	if kb, notice := swap(none, "w:x:110"); kb != "" || notice != favRemovedNotice || favs(t) != "" {
		t.Errorf("нет кнопки события: клавиатура %q, уведомление %q, записи %q", kb, notice, favs(t))
	}

	// Вид N-4: одна кнопка скрытого старта.
	favPut(t, 135)
	storetest.HideEvent(t, 135)
	n4 := botMsg(t, []models.InlineKeyboardButton{k(delL, "w:x:135")})
	if kb, notice := swap(n4, "w:x:135"); kb != "["+addL+"] → w:a:135\n" || notice != favRemovedNotice || favs(t) != "" {
		t.Errorf("N-4, w:x: клавиатура %q, уведомление %q, записи %q", kb, notice, favs(t))
	}
	n4after := botMsg(t, []models.InlineKeyboardButton{k(addL, "w:a:135")})
	if kb, notice := swap(n4after, "w:a:135"); kb != "" || notice != goneNotice || favs(t) != "" {
		t.Errorf("N-4, w:a скрытого: клавиатура %q, уведомление %q, записи %q", kb, notice, favs(t))
	}

	// «Открыть старт»: карточка новым сообщением, исходное не правится.
	share52 := "[" + shareLabel + "] → url:" + shareURL("test_bot", 52, "Трейл «Шарташ Карасики»")
	favPut(t, 52)
	open := func(data string) call {
		t.Helper()
		calls := favTap(t, b, api, 42, &n1, data)
		if methods(calls) != "sendMessage answerCallbackQuery" || calls[0].Form["chat_id"] != "42" || calls[1].Form["text"] != "" {
			t.Fatalf("%s: вызовы %q, want одно новое сообщение и один пустой ответ", data, methods(calls))
		}
		checkCallbackLengths(t, calls[0])
		return calls[0]
	}
	if c := open("w:n:52"); !strings.Contains(c.Form["text"], "<b>Трейл «Шарташ Карасики»</b>") ||
		!strings.HasSuffix(kbLines(t, c), "["+delL+"] → w:x:52 | "+share52+"\n["+reportLabel+"] → q:o:52\n["+backLabel+"] → w:p:1\n") {
		t.Errorf("w:n:52: текст %q, клавиатура %q", c.Form["text"], kbLines(t, c))
	}
	favClear(t)
	if c := open("w:n:52"); !strings.HasSuffix(kbLines(t, c), "["+addL+"] → w:a:52 | "+share52+"\n["+reportLabel+"] → q:o:52\n["+backLabel+"] → w:p:1\n") {
		t.Errorf("w:n:52 не из избранного: клавиатура %q", kbLines(t, c))
	}
	storetest.MergeEvent(t, 110, 52)
	if c := open("w:n:110"); !strings.Contains(c.Form["text"], "<b>Трейл «Шарташ Карасики»</b>") ||
		!strings.HasSuffix(kbLines(t, c), "["+addL+"] → w:a:52 | "+share52+"\n["+reportLabel+"] → q:o:52\n["+backLabel+"] → w:p:1\n") {
		t.Errorf("w:n слитого: текст %q, клавиатура %q", c.Form["text"], kbLines(t, c))
	}
	storetest.HideEvent(t, 52)
	for _, data := range []string{"w:n:52", "w:n:99999"} {
		if c := open(data); c.Form["text"] != goneNotice || kbLines(t, c) != "["+backLabel+"] → w:p:1\n" {
			t.Errorf("%s: текст %q, клавиатура %q", data, c.Form["text"], kbLines(t, c))
		}
	}
}

// TestS51FavMenu — S-51: строка «⭐ Избранное» в главном меню видна всем, стоит
// после рядов видов спорта и перед настройками; вход в раздел и возврат.
// Эталоны s51_* написаны вручную по контракту.
func TestS51FavMenu(t *testing.T) {
	s, api, b := skiBot(t)
	ctx := context.Background()
	filtersV(t, b)
	startSnapless(t, b, api) // первый /start — предложение анкеты, дальше — меню

	// 1. /start: меню новым сообщением, избранное между видами спорта и настройками.
	startSnap(t, b, api, "s51_menu")
	menu := api.of("sendMessage")[0]
	checkMenuFav := func(step string, c call) {
		t.Helper()
		ds := datas(t, c)
		i := slices.Index(ds, "w:o")
		if i < 1 || i+1 >= len(ds) || ds[i+1] != "s:o" || !strings.HasPrefix(ds[i-1], "f:a:") {
			t.Errorf("%s: кнопки %v, want w:o после видов спорта и перед s:o", step, ds)
		}
	}
	checkMenuFav("шаг 1", menu)

	// 2. «m»: то же меню правкой.
	menuBack := func() { matchSnapshot(t, "s51_menu_back", edited(t, "m", raw(b, api, 42, "m"))) }
	menuBack()

	// 3. Будущих лыжных событий нет: один вид спорта, избранное второй строкой.
	b.Now = func() time.Time { return time.Date(2027, 3, 2, 12, 0, 0, 0, time.UTC) }
	startSnap(t, b, api, "s51_one_sport")
	b.Now = func() time.Time { return fixtureNow }

	// 5. Бот с группой поддержки: пять строк, последняя — поддержка.
	startSnap(t, newSupportBot(t, api, s), api, "s51_support")
	support := api.of("sendMessage")[0]
	checkMenuFav("шаг 5", support)
	if ds := datas(t, support); ds[len(ds)-1] != "h:o" || strings.Count(kbLines(t, support), "\n") != 5 {
		t.Errorf("шаг 5: кнопки %v, want пять строк, последняя h:o", ds)
	}

	// 4. Три вида спорта: избранное после второго ряда. Плавание включено
	// только на время теста, у него одно будущее событие.
	storetest.EnableSport(t, event.SportSwimming)
	e := &event.Event{Title: "Проба swimming", TitleNorm: "проба swimming", StartDate: fixtureToday.AddDate(0, 0, 5),
		CountryCode: "RU", Type: event.TypeOther, Sport: event.SportSwimming, Status: event.StatusScheduled, Distances: []event.Distance{{Meters: 1000}}}
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	startSnap(t, b, api, "s51_three")
	checkMenuFav("шаг 4", api.of("sendMessage")[0])
	storetest.DisableSport(t, event.SportSwimming)

	// 6. Избранного нет: строка открывает пустой раздел; «m» возвращает в меню.
	favSnap(t, b, api, 42, &menu, "w:o", "s51_empty")
	menuBack()

	// 7. В избранном 110: раздел со стартом; «m» возвращает в меню.
	favPut(t, 110)
	favSnap(t, b, api, 42, &menu, "w:o", "s51_section")
	menuBack()

	// 8. Каталог пуст: строка избранного в меню есть.
	b2 := newTestBot(t, api, storetest.Open(t))
	startSnapless(t, b2, api)
	api.reset()
	b2.api.ProcessUpdate(ctx, startUpdate(42))
	checkMenuFav("шаг 8", api.of("sendMessage")[0])
}
