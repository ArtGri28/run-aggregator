package bot

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

// Сценарии контракта экранов Phase 8 (08-UX-CONTRACT.md, раздел 8): каждый
// шаг — снимок экрана; эталоны testdata/screens/sXX_*.txt правятся вручную.

// startSnap отправляет /start и сверяет единственное новое сообщение.
func startSnap(t *testing.T, b *Bot, api *fakeAPI, name string) {
	t.Helper()
	api.reset()
	b.api.ProcessUpdate(context.Background(), startUpdate(42))
	s := api.of("sendMessage")
	if len(s) != 1 || api.total() != 1 {
		t.Fatalf("/start: ждали одно сообщение, вызовы %+v", api.calls)
	}
	matchSnapshot(t, name, renderCall(t, s[0]))
}

// TestS01MainMenu — S-01: главное меню.
func TestS01MainMenu(t *testing.T) {
	s, api, b := countryFixture(t)

	// 1. Новый пользователь: меню новым сообщением, строка users создана.
	startSnap(t, b, api, "s01_start")
	if _, v := filtersV(t, b); v != 1 {
		t.Fatalf("версия после первого /start = %d, want 1", v)
	}

	// 2. Повторный /start не сбрасывает фильтры.
	setUserFilters(t, s, `{"type":"trail"}`)
	f1, v1 := filtersV(t, b)
	startSnap(t, b, api, "s01_start_again")
	if f2, v2 := filtersV(t, b); v2 != v1 || f2.Type != f1.Type {
		t.Errorf("/start изменил фильтры: %+v v%d → %+v v%d", f1, v1, f2, v2)
	}

	// 3. «Назад» в меню: правка сообщения, записей нет.
	before := dbState(t)
	matchSnapshot(t, "s01_menu", edited(t, "m", raw(b, api, 42, "m")))
	if dbState(t) != before {
		t.Error("кнопка меню записала в базу")
	}
}

// TestS02Search — S-02: поиск и возврат в меню.
func TestS02Search(t *testing.T) {
	_, api, b := countryFixture(t)
	c := pressSnap(t, b, api, "f:s", "s02_summary")
	if got := c.button(t, backLabel); got != "m" {
		t.Fatalf("«Назад» сводки = %q, want m", got)
	}
	pressSnap(t, b, api, "m", "s02_menu")
	if t.Failed() {
		return
	}
	t.Log("TRACER scenario=S-02 steps=2 match=yes")
}

// TestS03Date — S-03: дата.
func TestS03Date(t *testing.T) {
	_, api, b := countryFixture(t)
	_, v := filtersV(t, b)
	pressSnap(t, b, api, "f:d", "s03_menu")
	pressSnap(t, b, api, "f:d:30", "s03_summary_30")
	if f, v2 := filtersV(t, b); f.Date != "30" || v2 != v+1 {
		t.Errorf("после «30 дней»: %+v v%d, want date 30 v%d", f, v2, v+1)
	}
	// «Назад» из подменю фильтры не меняет.
	menu := press(t, b, api, "f:d")
	pressSnap(t, b, api, menu.button(t, backLabel), "s03_summary_30")
	if f, v3 := filtersV(t, b); f.Date != "30" || v3 != v+1 {
		t.Errorf("«Назад» изменил фильтры: %+v v%d", f, v3)
	}
}

// TestS04City — S-04: город из списка.
func TestS04City(t *testing.T) {
	_, api, b := countryFixture(t)
	menu := pressSnap(t, b, api, "f:c", "s04_menu")
	pressSnap(t, b, api, "f:c:moscow", "s04_summary_city")
	pressSnap(t, b, api, "f:c:-", "s04_summary_all")
	pressSnap(t, b, api, menu.button(t, backLabel), "s04_summary_all")
}

// TestS05CityText — S-05: город текстом, подсказки (P8).
func TestS05CityText(t *testing.T) {
	_, api, b := countryFixture(t)
	filtersV(t, b)
	textSnap(t, b, api, "Москва", "s05_found")
	if f, _ := filtersV(t, b); f.City != "moscow" {
		t.Fatalf("город не выбран: %+v", f)
	}
	press(t, b, api, "f:c:-")

	// «Моск»: ключ mosk не совпадает с ключом-синонимом moscow — подсказка
	// находится по началу названия; нажатие выбирает город.
	textSnap(t, b, api, "Моск", "s05_suggest")
	textSnap(t, b, api, "мОСК", "s05_suggest_case")
	pressSnap(t, b, api, "f:c:moscow", "s05_suggest_chosen")

	press(t, b, api, "f:c:-")
	textSnap(t, b, api, "Атлантида", "s05_none")
	textSnap(t, b, api, "/foo", "s05_command")
}

// TestS06Distance — S-06: дистанция.
func TestS06Distance(t *testing.T) {
	_, api, b := countryFixture(t)
	menu := pressSnap(t, b, api, "f:b", "s06_menu")
	press(t, b, api, "f:b:0")
	pressSnap(t, b, api, "f:b:1", "s06_two")
	pressSnap(t, b, api, "f:b:-", "s06_cleared")
	press(t, b, api, "f:b:1")
	pressSnap(t, b, api, menu.button(t, backLabel), "s06_summary")
}

// TestS07Type — S-07: тип.
func TestS07Type(t *testing.T) {
	_, api, b := countryFixture(t)
	pressSnap(t, b, api, "f:t", "s07_menu")
	pressSnap(t, b, api, "f:t:trail", "s07_summary_trail")
	pressSnap(t, b, api, "f:t:-", "s07_summary_any")
}

// TestS08Country — S-08: страна.
func TestS08Country(t *testing.T) {
	_, api, b := countryFixture(t)
	pressSnap(t, b, api, "f:s", "s08_summary")
	pressSnap(t, b, api, "f:k", "s08_menu")
	pressSnap(t, b, api, "f:k:BY", "s08_summary_by")
	pressSnap(t, b, api, "f:k:*", "s08_summary_all")
	// При «всех странах» город недоступен: вместо него — выбор страны.
	c := press(t, b, api, "f:c")
	matchSnapshot(t, "s08_city_needs_country", renderCall(t, c)+renderCall(t, api.of("answerCallbackQuery")[0]))
}

// marathonecBot — бот на календаре Марафонца (133 события), пользователь 42
// создан; возвращает суффикс версии для кнопок страниц.
func marathonecBot(t *testing.T) (*fakeAPI, *Bot, string) {
	t.Helper()
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	_, v := filtersV(t, b)
	return api, b, ":v" + strconv.Itoa(v)
}

// TestS09List — S-09: список, листание, год в дате (P7).
func TestS09List(t *testing.T) {
	api, b, pv := marathonecBot(t)
	p1 := pressSnap(t, b, api, "p:1"+pv, "s09_page1")
	p2 := pressSnap(t, b, api, p1.button(t, "→"), "s09_page2")
	pressSnap(t, b, api, p2.button(t, "←"), "s09_page1")
	pressSnap(t, b, api, p2.button(t, "стр. "), "s09_page2")

	// Последняя страница: события следующего года — с годом.
	last := pressSnap(t, b, api, "p:"+strconv.Itoa(maxPage)+pv, "s09_last")
	if text := last.Form["text"]; !strings.Contains(text, ".2027 · ") || strings.Contains(p1.Form["text"], ".2026 · ") {
		t.Errorf("год в дате: последняя страница\n%s\nпервая\n%s", text, p1.Form["text"])
	}
	pressSnap(t, b, api, last.button(t, backLabel), "s09_back")
	if !t.Failed() {
		t.Log("TRACER scenario=S-09 steps=4 match=yes")
	}
}

// TestS10Card — S-10: карточка, возврат на ту же страницу, статус, скрытое событие.
func TestS10Card(t *testing.T) {
	api, b, pv := marathonecBot(t)
	p2 := press(t, b, api, "p:2"+pv)
	card := pressSnap(t, b, api, p2.button(t, "1"), "s10_card")
	pressSnap(t, b, api, card.button(t, backLabel), "s09_page2")

	id := cardID(t, p2.button(t, "1"))
	storetest.SetStatus(t, id, event.StatusCancelled)
	pressSnap(t, b, api, p2.button(t, "1"), "s10_cancelled")
	storetest.HideEvent(t, id)
	gone := pressSnap(t, b, api, p2.button(t, "1"), "s10_gone")
	pressSnap(t, b, api, gone.button(t, backLabel), "s10_list_after_gone")
}

// TestS11Empty — S-11: пустая выдача.
func TestS11Empty(t *testing.T) {
	s, api, b := countryFixture(t)
	setUserFilters(t, s, `{"date":"m202701"}`)
	_, v := filtersV(t, b)
	empty := pressSnap(t, b, api, "p:1:v"+strconv.Itoa(v), "s11_hints")
	pressSnap(t, b, api, empty.button(t, "Снять дату"), "s11_after_unset")
	if f, v2 := filtersV(t, b); f.Date != "" || v2 != v+1 {
		t.Errorf("после «Снять дату»: %+v v%d, want без даты v%d", f, v2, v+1)
	}

	// Каталог страны по умолчанию пуст: выбор страны и возврат.
	s2 := storetest.Open(t)
	seedCountryEvent(t, s2, "BY", "2026-10-12", "Минск", "minsk", event.TypeRoad, "Минский забег")
	api2 := fakeTelegram(t)
	b2 := newTestBot(t, api2, s2)
	_, v = filtersV(t, b2)
	pick := pressSnap(t, b2, api2, "p:1:v"+strconv.Itoa(v), "s11_pick_country")
	pressSnap(t, b2, api2, pick.button(t, "🌍 Выбрать страну"), "s11_country_menu")
	pressSnap(t, b2, api2, pick.button(t, backLabel), "s11_back")
}
