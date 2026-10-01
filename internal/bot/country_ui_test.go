package bot

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"aggregator/internal/event"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

func setUserFilters(t *testing.T, s *store.Store, raw string) {
	t.Helper()
	if _, _, err := s.UpdateFilters(context.Background(), 42, func([]byte) ([]byte, error) { return []byte(raw), nil }); err != nil {
		t.Fatal(err)
	}
}

// buttonText — подпись кнопки с callback_data data ("" — нет такой).
func buttonText(t *testing.T, c call, data string) string {
	t.Helper()
	for _, bt := range c.buttons(t) {
		if bt.CallbackData == data {
			return bt.Text
		}
	}
	return ""
}

// toast — текст ответа на callback.
func toast(api *fakeAPI) string {
	if a := api.of("answerCallbackQuery"); len(a) > 0 {
		return a[0].Form["text"]
	}
	return ""
}

func TestCountryLabels(t *testing.T) {
	for code, want := range map[string][3]string{
		"RU": {"🇷🇺", "Россия", "🇷🇺 Россия"}, "BY": {"🇧🇾", "Беларусь", "🇧🇾 Беларусь"}, "PL": {"🇵🇱", "Польша", "🇵🇱 Польша"},
		"DE": {"🇩🇪", "DE", "🇩🇪 DE"}, // название неизвестно — код
		"*":  {"", "Все страны", "🌍 Все страны"}, "by": {"", "by", "by"}, "": {"", "", ""},
	} {
		if got := [3]string{countryFlag(code), countryName(code), countryLabel(code)}; got != want {
			t.Errorf("%q: flag/name/label = %q, want %q", code, got, want)
		}
	}
}

// A. Старые фильтры {} в каталоге из нескольких стран: «🌍 Страна: Россия» и
// только российские события.
func TestCountryButtonDefaultRU(t *testing.T) {
	s, api, b := countryFixture(t)
	setUserFilters(t, s, `{}`)
	c := press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "🌍 Страна: 🇷🇺 Россия\n📍 Город:") {
		t.Errorf("в сводке нет страны:\n%s", c.Form["text"])
	}
	if got := buttonText(t, c, "f:k"); got != "🌍 Страна" {
		t.Errorf("кнопка страны = %q", got)
	}
	if first := c.buttons(t)[0]; first.CallbackData != "f:k" {
		t.Errorf("кнопка страны не первая: %q", first.Text)
	}
	if n := showCount(t, c); n != 2 {
		t.Errorf("Показать %d, want 2 (только Россия)", n)
	}
	_, v := filtersV(t, b)
	text := press(t, b, api, "p:1:v"+strconv.Itoa(v)).Form["text"]
	if strings.Contains(text, "🇷🇺") || strings.Contains(text, "BY") || strings.Contains(text, "Минск") {
		t.Errorf("список России:\n%s", text)
	}
}

// Меню страны строится по Countries(), а не по списку в коде.
func TestCountryMenu(t *testing.T) {
	s, api, b := countryFixture(t)
	seedCountryEvent(t, s, "PL", "2026-10-14", "Краков", "krakow", event.TypeRoad, "Краковский забег")
	seedCountryEvent(t, s, "DE", "2026-10-15", "Берлин", "berlin", event.TypeRoad, "Берлинский забег")
	setUserFilters(t, s, `{}`)
	c := press(t, b, api, "f:k")
	if c.Form["text"] != "🌍 Страна" {
		t.Errorf("текст меню = %q", c.Form["text"])
	}
	want := []string{"f:k:BY", "f:k:RU", "f:k:DE", "f:k:PL", "f:k:*", "f:s"}
	if got := datas(t, c); !reflect.DeepEqual(got, want) {
		t.Fatalf("кнопки %v, want %v", got, want)
	}
	for data, label := range map[string]string{
		"f:k:RU": "✓ 🇷🇺 Россия (2)", "f:k:BY": "🇧🇾 Беларусь (2)", "f:k:PL": "🇵🇱 Польша (1)",
		"f:k:DE": "🇩🇪 DE (1)", "f:k:*": "🌍 Все страны (6)",
	} {
		if got := buttonText(t, c, data); got != label {
			t.Errorf("%s: %q, want %q", data, got, label)
		}
	}
	// Выбранная страна — отмечена; «Все страны» — тоже.
	setUserFilters(t, s, `{"country":"*"}`)
	if got := buttonText(t, press(t, b, api, "f:k"), "f:k:*"); got != "✓ 🌍 Все страны (6)" {
		t.Errorf("«Все страны» выбрано: %q", got)
	}
	// Город не сужает список стран: он принадлежит стране.
	setUserFilters(t, s, `{"city":"moscow"}`)
	if got := buttonText(t, press(t, b, api, "f:k"), "f:k:BY"); got != "🇧🇾 Беларусь (2)" {
		t.Errorf("при городе в фильтре: %q, want Беларусь (2)", got)
	}
	// Текущая страна остаётся в меню без событий.
	setUserFilters(t, s, `{"country":"KZ"}`)
	if got := buttonText(t, press(t, b, api, "f:k"), "f:k:KZ"); got != "✓ 🇰🇿 KZ (0)" {
		t.Errorf("страна без событий: %q", got)
	}
}

// B. Выбор страны: города и типы — только этой страны; дата, дистанция и тип
// не сбрасываются, город другой страны — сбрасывается.
func TestCountrySelect(t *testing.T) {
	s, api, b := countryFixture(t)
	setUserFilters(t, s, `{"date":"nm","dist":[1],"city":"moscow"}`)

	c := press(t, b, api, "f:k:BY")
	if !strings.Contains(c.Form["text"], "🌍 Страна: 🇧🇾 Беларусь") || buttonText(t, c, "f:k") != "🌍 Страна" {
		t.Errorf("сводка после выбора BY:\n%s", c.Form["text"])
	}
	f, _ := filtersV(t, b)
	if f.Country != "BY" || f.City != "" || f.Date != "nm" || !reflect.DeepEqual(f.Dist, []int{1}) {
		t.Errorf("фильтры после BY: %+v", f)
	}
	if n := showCount(t, c); n != 2 {
		t.Errorf("Показать %d, want 2", n)
	}
	if got := menuKeys(t, press(t, b, api, "f:c"), "f:c:"); !reflect.DeepEqual(got, []string{"lyuban", "minsk"}) {
		t.Errorf("города BY: %v", got)
	}
	if got := menuKeys(t, press(t, b, api, "f:t"), "f:t:"); !reflect.DeepEqual(got, []string{"obstacle", "road"}) {
		t.Errorf("типы BY: %v", got)
	}

	// Тот же выбор страны город сохраняет; возврат к России — сбрасывает и
	// убирает country из JSON (страна по умолчанию хранится как "").
	press(t, b, api, "f:c:minsk")
	press(t, b, api, "f:k:BY")
	if f, _ := filtersV(t, b); f.City != "minsk" {
		t.Errorf("город после повторного выбора BY: %+v", f)
	}
	press(t, b, api, "f:k:RU")
	f, _ = filtersV(t, b)
	if f.Country != "" || f.City != "" || f.Date != "nm" {
		t.Errorf("фильтры после RU: %+v", f)
	}
	raw, _, _ := b.Store.Filters(context.Background(), 42)
	if strings.Contains(string(raw), "country") {
		t.Errorf("страна по умолчанию записана в JSON: %s", raw)
	}
	if got := menuKeys(t, press(t, b, api, "f:c"), "f:c:"); !reflect.DeepEqual(got, []string{"lyuban", "moscow"}) {
		t.Errorf("города RU: %v", got)
	}
}

// C, D. «Все страны»: город недоступен, текстовый ввод просит выбрать страну,
// список смешанный с флагами.
func TestCountryAllMode(t *testing.T) {
	s, api, b := countryFixture(t)
	setUserFilters(t, s, `{"country":"*"}`)

	c := press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "🌍 Страна: 🌍 Все страны") || buttonText(t, c, "f:k") != "🌍 Страна" || showCount(t, c) != 4 {
		t.Errorf("сводка:\n%s", c.Form["text"])
	}

	// Меню города заменяется выбором страны.
	c = press(t, b, api, "f:c")
	if toast(api) != "Сначала выберите страну" || c.Form["text"] != "🌍 Страна" {
		t.Errorf("f:c: ответ %q, экран %q", toast(api), c.Form["text"])
	}
	// Устаревшая кнопка города не задаёт город и не меняет версию фильтров.
	_, v0 := filtersV(t, b)
	press(t, b, api, "f:c:lyuban")
	if f, v := filtersV(t, b); f.City != "" || v != v0 || toast(api) != "Сначала выберите страну" {
		t.Errorf("f:c:lyuban: %+v v=%d (было %d), ответ %q", f, v, v0, toast(api))
	}

	// Текстовый ввод города.
	api.reset()
	b.api.ProcessUpdate(context.Background(), textUpdate(42, "Любань"))
	sends := api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["text"] != "Сначала выберите страну" || buttonText(t, sends[0], "f:k") != "🌍 Страна" {
		t.Fatalf("ввод города: %+v", sends)
	}
	if f, v := filtersV(t, b); f.City != "" || v != v0 {
		t.Errorf("город после ввода: %+v v=%d", f, v)
	}

	// Список: события обеих стран, у каждого флаг; одинаковый city_key — оба.
	_, v := filtersV(t, b)
	text := press(t, b, api, "p:1:v"+strconv.Itoa(v)).Form["text"]
	rows := listRows(text)
	if len(rows) != 4 {
		t.Fatalf("список: %d строк\n%s", len(rows), text)
	}
	for _, want := range []string{"🇷🇺 Москва", "🇷🇺 Любань", "🇧🇾 Любань", "🇧🇾 Минск"} {
		if !strings.Contains(text, want) {
			t.Errorf("в списке нет %q:\n%s", want, text)
		}
	}
	if areaRE.MatchString(text) {
		t.Errorf("в списке область/район:\n%s", text)
	}

	// Конкретная страна — компактный формат без флагов.
	setUserFilters(t, s, `{"country":"BY"}`)
	_, v = filtersV(t, b)
	if text := press(t, b, api, "p:1:v"+strconv.Itoa(v)).Form["text"]; strings.Contains(text, "🇧🇾") || strings.Contains(text, "🇷🇺") {
		t.Errorf("флаги в списке одной страны:\n%s", text)
	}
}

// E. Карточка: страна выбрана — «📍 город»; все страны — «🌍 страна» и «📍
// город»; область и район не показываются.
func TestCardCountryLines(t *testing.T) {
	s, api, b := countryFixture(t)
	items, err := s.ListEvents(context.Background(), event.Filter{CountryCode: "BY", CityKey: "minsk", From: fixtureToday}, 5, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListEvents = %+v, %v", items, err)
	}
	card := func(filters string) string {
		t.Helper()
		setUserFilters(t, s, filters)
		_, v := filtersV(t, b)
		return press(t, b, api, "e:"+strconv.FormatInt(items[0].ID, 10)+":1:v"+strconv.Itoa(v)).Form["text"]
	}
	if text := card(`{"country":"BY"}`); !strings.Contains(text, "\n📍 Минск\n") || strings.Contains(text, "🌍") || strings.Contains(text, "Беларусь") {
		t.Errorf("страна выбрана:\n%s", text)
	}
	if text := card(`{"country":"*"}`); !strings.Contains(text, "\n📍 Минск\n🌍 Беларусь\n") || areaRE.MatchString(text) {
		t.Errorf("все страны:\n%s", text)
	}
	// Страна по умолчанию при нескольких странах в каталоге — тоже «📍».
	if text := card(`{}`); !strings.Contains(text, "\n📍 Минск\n") || strings.Contains(text, "🌍") {
		t.Errorf("страна по умолчанию:\n%s", text)
	}
}

// F. Одна страна в каталоге: прежний интерфейс без страны.
func TestSingleCountryUX(t *testing.T) {
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", event.TypeRoad, "Московский забег")
	seedCountryEvent(t, s, "RU", "2026-10-11", "Казань", "kazan", event.TypeTrail, "Казанский забег")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)

	c := press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "📍 Город: Все города\n📅 Дата: Все будущие\n📏 Дистанция: Любая\n🏃 Тип: Любой") ||
		strings.Contains(c.Form["text"], "Страна") {
		t.Errorf("сводка:\n%s", c.Form["text"])
	}
	if got := datas(t, c); !reflect.DeepEqual(got, []string{"f:c", "f:d", "f:b", "f:t", "p:1:v1", "f:x"}) {
		t.Errorf("кнопки сводки: %v", got)
	}
	if buttonText(t, press(t, b, api, "f:c"), "f:c:-") != "Все города" {
		t.Error("в меню города нет «Все города»")
	}
	_, v := filtersV(t, b)
	text := press(t, b, api, "p:1:v"+strconv.Itoa(v)).Form["text"]
	if strings.Contains(text, "🇷🇺") || len(listRows(text)) != 2 {
		t.Errorf("список:\n%s", text)
	}
	items, _ := s.ListEvents(context.Background(), event.Filter{From: fixtureToday}, 5, 0)
	card := press(t, b, api, "e:"+strconv.FormatInt(items[0].ID, 10)+":1:v"+strconv.Itoa(v)).Form["text"]
	if strings.Contains(card, "🌍") || !strings.Contains(card, "\n📍 Москва\n") {
		t.Errorf("карточка:\n%s", card)
	}
	// Приветствие прежнее.
	api.reset()
	b.api.ProcessUpdate(context.Background(), startUpdate(42))
	if s := api.of("sendMessage"); len(s) != 1 || !strings.Contains(s[0].Form["text"], "Найди свой следующий старт!") {
		t.Errorf("приветствие: %+v", s)
	}

	// Страна выбрана явно, а событий в ней нет: страна видна, пустая выдача
	// предлагает снять страну.
	setUserFilters(t, s, `{"country":"BY"}`)
	c = press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "🌍 Страна: 🇧🇾 Беларусь") || showCount(t, c) != 0 {
		t.Errorf("сводка с BY без событий:\n%s", c.Form["text"])
	}
	_, v = filtersV(t, b)
	empty := press(t, b, api, "p:1:v"+strconv.Itoa(v))
	if empty.Form["text"] != "Ничего не нашлось\n\nПопробуйте снять ограничение:" || buttonText(t, empty, "f:x:k") != "Снять страну" {
		t.Errorf("пустая выдача: %q, кнопки %v", empty.Form["text"], datas(t, empty))
	}
	press(t, b, api, "f:x:k")
	if f, _ := filtersV(t, b); f.Country != "" {
		t.Errorf("после «Снять страну»: %+v", f)
	}
}

// Приветствие при нескольких странах не называет Россию.
func TestStartGreetingMultiCountry(t *testing.T) {
	_, api, b := countryFixture(t)
	b.api.ProcessUpdate(context.Background(), startUpdate(42))
	s := api.of("sendMessage")
	if len(s) != 1 || strings.Contains(s[0].Form["text"], "в России") || !strings.Contains(s[0].Form["text"], "КАТАЛОГ ЗАБЕГОВ") {
		t.Errorf("приветствие: %+v", s)
	}
}

// Беларусь только в прошлом: стран в будущем каталоге одна — кнопки страны нет.
func TestCountryButtonIgnoresPast(t *testing.T) {
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", event.TypeRoad, "Московский забег")
	seedCountryEvent(t, s, "BY", "2026-09-01", "Минск", "minsk", event.TypeRoad, "Прошедший минский забег")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	c := press(t, b, api, "f:s")
	if buttonText(t, c, "f:k") != "" || strings.Contains(c.Form["text"], "Страна") {
		t.Errorf("кнопка страны при одной будущей стране:\n%s %v", c.Form["text"], datas(t, c))
	}
}

// Меню показывает все страны будущего каталога, даже если текущие фильтры не
// оставляют в стране событий; числа — при текущих фильтрах, включая (0).
func TestCountryMenuIgnoresFilters(t *testing.T) {
	s, api, b := countryFixture(t)
	for _, tc := range []struct {
		filters         string
		ru, by, all     string
		wantMenuButtons []string
	}{
		{`{}`, "✓ 🇷🇺 Россия (2)", "🇧🇾 Беларусь (2)", "🌍 Все страны (4)", nil},
		{`{"type":"road"}`, "✓ 🇷🇺 Россия (1)", "🇧🇾 Беларусь (1)", "🌍 Все страны (2)", nil},
		{`{"type":"trail"}`, "✓ 🇷🇺 Россия (1)", "🇧🇾 Беларусь (0)", "🌍 Все страны (1)", nil}, // у Беларуси трейлов нет
		{`{"type":"obstacle"}`, "✓ 🇷🇺 Россия (0)", "🇧🇾 Беларусь (1)", "🌍 Все страны (1)", nil},
		{`{"date":"m202612"}`, "✓ 🇷🇺 Россия (0)", "🇧🇾 Беларусь (0)", "🌍 Все страны (0)", nil}, // ни одного события
	} {
		setUserFilters(t, s, tc.filters)
		c := press(t, b, api, "f:k")
		got := []string{buttonText(t, c, "f:k:RU"), buttonText(t, c, "f:k:BY"), buttonText(t, c, "f:k:*")}
		if want := []string{tc.ru, tc.by, tc.all}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: меню %q, want %q", tc.filters, got, want)
		}
		if d := datas(t, c); !reflect.DeepEqual(d, []string{"f:k:BY", "f:k:RU", "f:k:*", "f:s"}) {
			t.Errorf("%s: кнопки %v (порядок не должен зависеть от фильтров)", tc.filters, d)
		}
	}
	// Переключение на Беларусь при фильтре, где её 0 событий, работает.
	setUserFilters(t, s, `{"type":"trail"}`)
	press(t, b, api, "f:k:BY")
	if f, _ := filtersV(t, b); f.Country != "BY" || f.Type != "trail" {
		t.Errorf("после выбора BY: %+v", f)
	}
}

// «Все страны»: строка списка — флаг и город, без названия страны.
func TestCountryAllListFormat(t *testing.T) {
	s, api, b := countryFixture(t)
	setUserFilters(t, s, `{"country":"*"}`)
	_, v := filtersV(t, b)
	text := press(t, b, api, "p:1:v"+strconv.Itoa(v)).Form["text"]
	for _, want := range []string{"🇧🇾 Минск", "🇧🇾 Любань", "🇷🇺 Москва", "🇷🇺 Любань"} {
		if !strings.Contains(text, want) {
			t.Errorf("нет %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Беларусь") || strings.Contains(text, "Россия") {
		t.Errorf("в списке названия стран:\n%s", text)
	}
}

// Каталог из одной страны BY, у пользователя страна по умолчанию (RU): кнопка
// страны есть, меню предлагает Беларусь, после выбора — события.
func TestCountryOnlyBYDefaultRU(t *testing.T) {
	s := storetest.Open(t)
	seedCountryEvent(t, s, "BY", "2026-10-12", "Минск", "minsk", event.TypeRoad, "Минский забег")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	setUserFilters(t, s, `{"date":"30","type":"road"}`)

	c := press(t, b, api, "f:s")
	if buttonText(t, c, "f:k") != "🌍 Страна" || !strings.Contains(c.Form["text"], "🌍 Страна: 🇷🇺 Россия") || showCount(t, c) != 0 {
		t.Fatalf("сводка: %q кнопки %v", c.Form["text"], datas(t, c))
	}
	cm := press(t, b, api, "f:k")
	if !reflect.DeepEqual(datas(t, cm), []string{"f:k:BY", "f:k:RU", "f:k:*", "f:s"}) ||
		buttonText(t, cm, "f:k:BY") != "🇧🇾 Беларусь (1)" || buttonText(t, cm, "f:k:RU") != "✓ 🇷🇺 Россия (0)" {
		t.Fatalf("меню: %v", datas(t, cm))
	}
	c = press(t, b, api, "f:k:BY")
	if showCount(t, c) != 1 || !strings.Contains(c.Form["text"], "🌍 Страна: 🇧🇾 Беларусь") {
		t.Fatalf("после выбора BY: %q", c.Form["text"])
	}
	if f, _ := filtersV(t, b); f.Country != "BY" || f.Date != "30" || f.Type != "road" { // остальные фильтры целы
		t.Fatalf("фильтры: %+v", f)
	}
	_, v := filtersV(t, b)
	if rows := listRows(press(t, b, api, "p:1:v"+strconv.Itoa(v)).Form["text"]); len(rows) != 1 || !strings.Contains(rows[0], "Минск") {
		t.Fatalf("выдача BY: %v", rows)
	}
	// Страна выбрана явно: кнопка остаётся и когда Беларусь единственная.
	if buttonText(t, press(t, b, api, "f:s"), "f:k") != "🌍 Страна" {
		t.Error("кнопка страны пропала после выбора единственной страны")
	}
}

// Каталог из одной страны RU при умолчании RU — кнопки страны нет; при
// «Все страны» и единственной стране кнопка остаётся.
func TestCountryOnlyRUDefaultRU(t *testing.T) {
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-12", "Москва", "moscow", event.TypeRoad, "Московский забег")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	c := press(t, b, api, "f:s")
	if buttonText(t, c, "f:k") != "" || strings.Contains(c.Form["text"], "Страна") {
		t.Errorf("RU-only: кнопка страны есть: %q %v", c.Form["text"], datas(t, c))
	}
	setUserFilters(t, s, `{"country":"*"}`)
	if buttonText(t, press(t, b, api, "f:s"), "f:k") != "🌍 Страна" {
		t.Error("«Все страны»: кнопки страны нет")
	}
}
