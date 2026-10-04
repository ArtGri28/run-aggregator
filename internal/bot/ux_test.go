package bot

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

// Главное меню: заголовок, один ввод в поиск; кнопок избранного, уведомлений
// и «О боте» нет, пока нет этих сценариев.
func TestStartScreen(t *testing.T) {
	_, api, b := countryFixture(t)
	b.api.ProcessUpdate(context.Background(), startUpdate(42))
	s := api.of("sendMessage")
	if len(s) != 1 || !strings.HasPrefix(s[0].Form["text"], "🏁 <b>Где старт?</b>\nСоревнования по бегу в разных городах\n\nСобрали беговые старты") {
		t.Fatalf("приветствие: %+v", s)
	}
	if got := datas(t, s[0]); !reflect.DeepEqual(got, []string{"f:s", "s:o"}) || s[0].button(t, "🔎 Найти забег") != "f:s" {
		t.Errorf("кнопки: %v", got)
	}
}

// Экран поиска: значения фильтров — в тексте, в кнопках только названия;
// страна отдельной строкой и первой кнопкой, когда её можно выбрать.
func TestSearchScreenStructure(t *testing.T) {
	// Одна страна: без строки и кнопки страны.
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", event.TypeRoad, "Московский забег")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	c := press(t, b, api, "f:s")
	if want := "🔎 <b>Поиск забега</b>\n\n📍 Город: Все города\n📅 Дата: Все будущие\n📏 Дистанция: Любая\n🏃 Тип: Любой"; c.Form["text"] != want {
		t.Errorf("текст:\n%s", c.Form["text"])
	}
	var texts []string
	for _, bt := range c.buttons(t) {
		texts = append(texts, bt.Text)
	}
	if want := []string{"📍 Город", "📅 Дата", "📏 Дистанция", "🏃 Тип", "🔎 Показать 1 забег", "🔄 Сбросить", "⬅️ Назад"}; !reflect.DeepEqual(texts, want) {
		t.Errorf("кнопки %q, want %q", texts, want)
	}

	// Несколько стран: страна — первая строка текста и первая кнопка; значение
	// в кнопке не повторяется.
	_, api2, b2 := countryFixture(t)
	c = press(t, b2, api2, "f:s")
	if !strings.HasPrefix(c.Form["text"], "🔎 <b>Поиск забега</b>\n\n🌍 Страна: 🇷🇺 Россия\n📍 Город:") {
		t.Errorf("текст:\n%s", c.Form["text"])
	}
	if first := c.buttons(t)[0]; first.Text != "🌍 Страна" || noV(first.CallbackData) != "f:k" {
		t.Errorf("первая кнопка %+v", first)
	}
}

// Возврат назад из каждого меню фильтров ведёт к экрану поиска (f:s).
func TestSubmenuBack(t *testing.T) {
	_, api, b := countryFixture(t)
	for _, data := range []string{"f:k", "f:c", "f:d", "f:n", "f:t"} {
		c := press(t, b, api, data)
		if !slices_contains(datas(t, c), "f:s") {
			t.Errorf("%s: нет возврата f:s: %v", data, datas(t, c))
		}
		for _, bt := range c.buttons(t) {
			if noV(bt.CallbackData) == "f:s" && bt.Text != backLabel {
				t.Errorf("%s: кнопка возврата %q", data, bt.Text)
			}
		}
	}
}

func slices_contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Пустая выдача: без фильтров — только возврат; с фильтрами — подсказка и
// кнопки снятия ограничений.
func TestEmptyScreenHints(t *testing.T) {
	_, api, b := countryFixture(t)
	setUserFilters(t, b.Store, `{"date":"m202701"}`)
	_, v := filtersV(t, b)
	c := press(t, b, api, "p:1:v"+strconv.Itoa(v))
	if c.Form["text"] != "Ничего не нашлось\n\nПопробуйте снять ограничение:" ||
		!reflect.DeepEqual(datas(t, c), []string{"f:x:d", "f:k", "f:s"}) || c.button(t, backLabel) != "f:s" {
		t.Errorf("с фильтром: %q %v", c.Form["text"], datas(t, c))
	}
	if em := emptyScreen(userFilters{}, false); em.text != "Ничего не нашлось" || len(em.kb.InlineKeyboard) != 1 {
		t.Errorf("без фильтров: %+v", em)
	}
}

// Карточка: порядок строк, ссылка регистрации и возврат; область и район
// не показываются.
func TestCardStructure(t *testing.T) {
	e := event.Event{Title: "Забег «Тест»", StartDate: fixtureToday.AddDate(0, 0, 5), StartTime: "10:00", CountryCode: "RU",
		City: "Москва", Region: "Московская область", Address: "Московская область, Красногорский район, парк Рублёво",
		Type: event.TypeTrail, Description: "Описание", RegistrationURL: "https://example.org/reg",
		Distances: []event.Distance{{Meters: 10000}, {Meters: 21097, Label: "полумарафон"}}}
	sc := cardScreen(e, nil, 2, 3, placePlain)
	want := []string{"🏔 <b>Забег «Тест»</b>", "", "📅 4 октября 2026", "🕒 Старт в 10:00 (местное время)", "📍 Москва, парк Рублёво",
		"📏 10 км · 21,1 км (полумарафон)", "🏔 Трейл", "", "Описание"}
	if got := strings.Split(sc.text, "\n"); !reflect.DeepEqual(got, want) {
		t.Errorf("карточка:\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(sc.text, "область") || strings.Contains(sc.text, "район") {
		t.Errorf("административная часть в карточке:\n%s", sc.text)
	}
	var btns []string
	for _, r := range sc.kb.InlineKeyboard {
		for _, b := range r {
			btns = append(btns, b.Text)
		}
	}
	if !reflect.DeepEqual(btns, []string{"🔗 Регистрация", backLabel}) {
		t.Errorf("кнопки %q", btns)
	}
	// Режим «Все страны»: страна строкой после города.
	if got := cardScreen(e, nil, 1, 1, placeWithCountry).text; !strings.Contains(got, "📍 Москва, парк Рублёво\n🌍 Россия\n") {
		t.Errorf("все страны:\n%s", got)
	}
}

// Старые сохранённые фильтры (без страны, с городом и корзинами) открывают
// новый экран поиска без ошибок и без страны в тексте при одной стране.
func TestOldFiltersNewScreen(t *testing.T) {
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", event.TypeRoad, "Московский забег")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	// Корзины под прежним ключом dist не читаются: их номера значили другие диапазоны.
	setUserFilters(t, s, `{"date":"a","city":"moscow","dist":[1],"type":"road"}`)
	if f, _ := filtersV(t, b); len(f.Dist) != 0 {
		t.Errorf("прежние корзины прочитаны как новые: %+v", f)
	}
	c := press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "📍 Город: Москва") || strings.Contains(c.Form["text"], "Страна") || showCount(t, c) != 1 {
		t.Errorf("сводка:\n%s", c.Form["text"])
	}
}

// Пустая выдача: «Выбрать страну» (f:k) при невыбранной стране, если выбор
// доступен; в каталоге из одной страны RU её нет.
func TestEmptyPickCountry(t *testing.T) {
	// BY-only при умолчании RU: из пустой выдачи можно перейти к странам и выбрать BY.
	s := storetest.Open(t)
	seedCountryEvent(t, s, "BY", "2026-10-12", "Минск", "minsk", event.TypeRoad, "Минский забег")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	_, v := filtersV(t, b)
	c := press(t, b, api, "p:1:v"+strconv.Itoa(v))
	if c.Form["text"] != "Ничего не нашлось" || c.button(t, "🌍 Выбрать страну") != "f:k" ||
		!reflect.DeepEqual(datas(t, c), []string{"f:k", "f:s"}) {
		t.Fatalf("BY-only: %q %v", c.Form["text"], datas(t, c))
	}
	if m := press(t, b, api, "f:k"); buttonText(t, m, "f:k:BY") != "🇧🇾 Беларусь (1)" {
		t.Errorf("меню: %v", datas(t, m))
	}
	// Страна выбрана явно, но фильтр даёт 0: «Снять страну», а не «Выбрать страну».
	setUserFilters(t, s, `{"country":"BY","type":"trail"}`)
	_, v = filtersV(t, b)
	c = press(t, b, api, "p:1:v"+strconv.Itoa(v))
	if c.button(t, "🌍 Выбрать страну") != "" || !slices_contains(datas(t, c), "f:x:k") {
		t.Errorf("страна выбрана: %v", datas(t, c))
	}

	// RU-only: выбора страны нет.
	s2 := storetest.Open(t)
	seedCountryEvent(t, s2, "RU", "2026-10-12", "Москва", "moscow", event.TypeRoad, "Московский забег")
	api2 := fakeTelegram(t)
	b2 := newTestBot(t, api2, s2)
	setUserFilters(t, s2, `{"type":"trail"}`)
	_, v = filtersV(t, b2)
	c = press(t, b2, api2, "p:1:v"+strconv.Itoa(v))
	if c.button(t, "🌍 Выбрать страну") != "" || strings.Contains(strings.Join(datas(t, c), " "), "f:k") {
		t.Errorf("RU-only: %v", datas(t, c))
	}

	// RU+BY, у RU по фильтру 0: кнопка есть; сам emptyScreen — флаг управляет.
	if em := emptyScreen(userFilters{}, true); len(em.kb.InlineKeyboard) != 2 || em.text != "Ничего не нашлось" {
		t.Errorf("emptyScreen(pick): %+v", em)
	}
}

// Счётчик активных фильтров: считается то, что отличается от значений по
// умолчанию; страна — только выбранная явно.
func TestActiveCount(t *testing.T) {
	for _, tc := range []struct {
		f    userFilters
		want int
	}{
		{userFilters{}, 0},
		{userFilters{Date: "a"}, 0},
		{userFilters{Country: "*"}, 1},
		{userFilters{Country: "BY", City: "minsk"}, 2},
		{userFilters{City: "spb", Date: "nm", Dist: []int{0, 2}, Type: "trail"}, 4},
		{userFilters{Country: "BY", City: "minsk", Date: "cm", Dist: []int{1}, Type: "road"}, 5},
	} {
		if got := tc.f.activeCount(); got != tc.want {
			t.Errorf("activeCount(%+v) = %d, want %d", tc.f, got, tc.want)
		}
	}
	if got := settingsScreen(userFilters{}, "").text; !strings.HasSuffix(got, "🔎 Фильтры не заданы — показываются все будущие забеги") {
		t.Errorf("настройки по умолчанию:\n%s", got)
	}
	if got := settingsScreen(userFilters{Type: "trail", Dist: []int{3}}, "").text; !strings.HasSuffix(got, "🔎 Активно фильтров: 2 из 5") {
		t.Errorf("настройки с фильтрами:\n%s", got)
	}
	if got := activeLine(userFilters{City: "x", Type: "obstacle", Dist: []int{0}}, "<Тула>"); got != "&lt;Тула&gt; · 🟢 До 10 км · OCR · с препятствиями" {
		t.Errorf("activeLine = %q", got)
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "забегов", 1: "забег", 2: "забега", 4: "забега", 5: "забегов", 11: "забегов", 12: "забегов",
		21: "забег", 22: "забега", 111: "забегов", 224: "забега"} {
		if got := plural(n, "забег", "забега", "забегов"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}

// Меню типа: «Другое» не выводится, но события этого типа остаются в выдаче
// при «Любом»; порядок — шоссе, трейл, кросс, OCR.
func TestTypeMenuHidesOther(t *testing.T) {
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", event.TypeOther, "Забег без типа")
	seedCountryEvent(t, s, "RU", "2026-10-11", "Москва", "moscow", event.TypeObstacle, "Гонка")
	seedCountryEvent(t, s, "RU", "2026-10-12", "Москва", "moscow", event.TypeRoad, "Шоссейный")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	var texts []string
	for _, bt := range press(t, b, api, "f:t").buttons(t) {
		texts = append(texts, bt.Text)
	}
	if want := []string{"🛣 Шоссе", "🧗 OCR · с препятствиями", "✓ 🏃 Любой", backLabel}; !reflect.DeepEqual(texts, want) {
		t.Errorf("меню типа %q, want %q", texts, want)
	}
	_, v := filtersV(t, b)
	if rows := listRows(t, press(t, b, api, "p:1:v"+strconv.Itoa(v))); len(rows) != 3 || !strings.Contains(rows[0], "Забег без типа") {
		t.Errorf("выдача при «Любом» типе: %q", rows)
	}
	// Сохранённый тип «Другое» продолжает работать.
	setUserFilters(t, s, `{"type":"other"}`)
	_, v = filtersV(t, b)
	if rows := listRows(t, press(t, b, api, "p:1:v"+strconv.Itoa(v))); len(rows) != 1 {
		t.Errorf("выдача при сохранённом типе «Другое»: %q", rows)
	}
}
