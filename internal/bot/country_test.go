package bot

import (
	"context"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// seedCountryEvent — видимое событие страны country с одной дистанцией.
func seedCountryEvent(t *testing.T, s *store.Store, country, date, city, cityKey string, typ event.Type, title string) {
	t.Helper()
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		t.Fatal(err)
	}
	e := &event.Event{Title: title, TitleNorm: strings.ToLower(title), StartDate: d, CountryCode: country,
		City: city, CityKey: cityKey, Type: typ, Status: event.StatusScheduled,
		Distances: []event.Distance{{Meters: 10000}}}
	if _, err := s.SaveEvent(context.Background(), e); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
}

// countryFixture — одинаковый city_key (lyuban) в России и Беларуси.
func countryFixture(t *testing.T) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Москва", "moscow", event.TypeRoad, "Московский забег")
	seedCountryEvent(t, s, "RU", "2026-10-11", "Любань", "lyuban", event.TypeTrail, "Любанский трейл РФ")
	seedCountryEvent(t, s, "BY", "2026-10-12", "Любань", "lyuban", event.TypeObstacle, "Любанский трейл BY")
	seedCountryEvent(t, s, "BY", "2026-10-13", "Минск", "minsk", event.TypeRoad, "Минский забег")
	api := fakeTelegram(t)
	return s, api, newTestBot(t, api, s)
}

// showCount — число на кнопке «Показать N».
func showCount(t *testing.T, c call) int {
	t.Helper()
	for _, bt := range c.buttons(t) {
		if n, ok := strings.CutPrefix(bt.Text, "🔎 Показать "); ok {
			n, _, _ = strings.Cut(n, " ") // «224 забега»
			v, err := strconv.Atoi(n)
			if err != nil {
				t.Fatalf("кнопка %q: %v", bt.Text, err)
			}
			return v
		}
	}
	t.Fatalf("нет кнопки «Показать N»: %v", datas(t, c))
	return 0
}

// menuKeys — значения кнопок меню с префиксом prefix, кроме «снять» (prefix + "-").
func menuKeys(t *testing.T, c call, prefix string) []string {
	t.Helper()
	out := []string{}
	for _, d := range datas(t, c) {
		if k, ok := strings.CutPrefix(d, prefix); ok && k != "-" {
			out = append(out, k)
		}
	}
	return out
}

// Старые фильтры (без country) — это Россия: сводка, список, меню города и
// типа не показывают Беларусь, даже при одинаковом city_key; явная страна и
// «все страны» работают через те же запросы.
func TestDefaultCountryIsolation(t *testing.T) {
	s, api, b := countryFixture(t)
	ctx := context.Background()
	setFilters := func(raw string) {
		t.Helper()
		if _, _, err := s.UpdateFilters(ctx, 42, func([]byte) ([]byte, error) { return []byte(raw), nil }); err != nil {
			t.Fatal(err)
		}
	}
	const (
		moscow = "Московский забег"
		ruLyu  = "Любанский трейл РФ"
		byLyu  = "Любанский трейл BY"
		minsk  = "Минский забег"
	)
	cases := []struct {
		name, filters string
		count         int
		cities, types []string
		titles        []string
	}{
		{"пустые фильтры = Россия", `{}`, 2, []string{"lyuban", "moscow"}, []string{"road", "trail"}, []string{moscow, ruLyu}},
		{"явная RU = пустые", `{"country":"RU"}`, 2, []string{"lyuban", "moscow"}, []string{"road", "trail"}, []string{moscow, ruLyu}},
		{"только дата", `{"date":"nm"}`, 2, []string{"lyuban", "moscow"}, []string{"road", "trail"}, []string{moscow, ruLyu}},
		{"старый город moscow", `{"city":"moscow"}`, 1, []string{"lyuban", "moscow"}, []string{"road", "trail"}, []string{moscow}},
		{"старый город lyuban — только RU", `{"city":"lyuban"}`, 1, []string{"lyuban", "moscow"}, []string{"road", "trail"}, []string{ruLyu}},
		{"мусорная страна = Россия", `{"country":"XYZ","city":"lyuban"}`, 1, []string{"lyuban", "moscow"}, []string{"road", "trail"}, []string{ruLyu}},
		{"BY", `{"country":"BY"}`, 2, []string{"lyuban", "minsk"}, []string{"road", "obstacle"}, []string{byLyu, minsk}},
		{"BY и lyuban", `{"country":"BY","city":"lyuban"}`, 1, []string{"lyuban", "minsk"}, []string{"road", "obstacle"}, []string{byLyu}},
		// Все страны: город недоступен (меню города заменяется выбором страны),
		// город из фильтра не используется.
		{"все страны", `{"country":"*"}`, 4, []string{}, []string{"road", "trail", "obstacle"}, []string{moscow, ruLyu, byLyu, minsk}},
		{"все страны и lyuban — город не фильтр", `{"country":"*","city":"lyuban"}`, 4, []string{}, []string{"road", "trail", "obstacle"}, []string{moscow, ruLyu, byLyu, minsk}},
		{"страна без событий", `{"country":"PL"}`, 0, []string{}, []string{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setFilters(tc.filters)
			if n := showCount(t, press(t, b, api, "f:s")); n != tc.count {
				t.Errorf("сводка: Показать %d, want %d", n, tc.count)
			}
			if got := menuKeys(t, press(t, b, api, "f:c"), "f:c:"); !reflect.DeepEqual(got, tc.cities) {
				t.Errorf("меню города: %v, want %v", got, tc.cities)
			}
			if got := menuKeys(t, press(t, b, api, "f:t"), "f:t:"); !reflect.DeepEqual(got, tc.types) {
				t.Errorf("меню типа: %v, want %v", got, tc.types)
			}
			_, v := filtersV(t, b)
			lc := press(t, b, api, "p:1:v"+strconv.Itoa(v))
			text := listText(t, lc)
			rows := listRows(t, lc)
			if tc.titles == nil {
				if len(rows) != 0 {
					t.Errorf("список: %v, want пусто", rows)
				}
				return
			}
			if len(rows) != len(tc.titles) {
				t.Fatalf("список: %d строк, want %d:\n%s", len(rows), len(tc.titles), text)
			}
			for _, title := range tc.titles {
				if !strings.Contains(text, title) {
					t.Errorf("в списке нет %q:\n%s", title, text)
				}
			}
			// Ровно перечисленные события: суммарно названий столько же.
			for _, other := range []string{moscow, ruLyu, byLyu, minsk} {
				want := false
				for _, title := range tc.titles {
					want = want || title == other
				}
				if strings.Contains(text, other) != want {
					t.Errorf("%q в списке: %v, want %v", other, !want, want)
				}
			}
		})
	}
}

// Город, написанный текстом, ищется в стране фильтров пользователя.
func TestCityTextByCountry(t *testing.T) {
	s, api, b := countryFixture(t)
	ctx := context.Background()
	setFilters := func(raw string) {
		t.Helper()
		if _, _, err := s.UpdateFilters(ctx, 42, func([]byte) ([]byte, error) { return []byte(raw), nil }); err != nil {
			t.Fatal(err)
		}
	}
	say := func(text string) call {
		t.Helper()
		api.reset()
		b.api.ProcessUpdate(ctx, textUpdate(42, text))
		sends := api.of("sendMessage")
		if len(sends) != 1 {
			t.Fatalf("%q: sendMessage = %+v", text, sends)
		}
		return sends[0]
	}

	// Россия по умолчанию: Минска в ней нет, Любань — российская.
	setFilters(`{}`)
	if c := say("Минск"); !strings.Contains(c.Form["text"], "По запросу «Минск» ничего не нашлось") {
		t.Errorf("Минск при стране по умолчанию: %q", c.Form["text"])
	}
	c := say("Любань")
	if f, _ := filtersV(t, b); f.City != "lyuban" || f.Country != "" || showCount(t, c) != 1 {
		t.Errorf("Любань (RU): %+v, Показать %d", f, showCount(t, c))
	}
	if text := listText(t, press(t, b, api, "p:1:v"+strconv.Itoa(func() int { _, v := filtersV(t, b); return v }()))); !strings.Contains(text, "Любанский трейл РФ") || strings.Contains(text, "BY") {
		t.Errorf("список после «Любань» (RU):\n%s", text)
	}

	// Беларусь: Минск есть, Любань — белорусская.
	setFilters(`{"country":"BY"}`)
	c = say("Минск")
	if f, _ := filtersV(t, b); f.City != "minsk" || f.Country != "BY" || showCount(t, c) != 1 {
		t.Errorf("Минск (BY): %+v, Показать %d", f, showCount(t, c))
	}
	say("Любань")
	_, v := filtersV(t, b)
	if text := listText(t, press(t, b, api, "p:1:v"+strconv.Itoa(v))); !strings.Contains(text, "Любанский трейл BY") || strings.Contains(text, "РФ") {
		t.Errorf("список после «Любань» (BY):\n%s", text)
	}

	// Подсказки «похожие города» тоже из выбранной страны.
	setFilters(`{"country":"BY"}`)
	if c := say("Mosc"); strings.Contains(strings.Join(datas(t, c), ","), "moscow") {
		t.Errorf("похожие города в BY содержат российскую Москву: %v", datas(t, c))
	}
	setFilters(`{}`)
	if c := say("Mosc"); !strings.Contains(strings.Join(datas(t, c), ","), "f:c:moscow") {
		t.Errorf("похожие города в RU без Москвы: %v", datas(t, c))
	}
}

// areaRE — слова области и района в тексте для пользователя.
var areaRE = regexp.MustCompile(`(?i)област|район|р-н`)

func TestVisibleAddress(t *testing.T) {
	for _, tt := range []struct {
		name, region, address, want string
	}{
		{"только район", "Брестская область", "Барановичский район", ""},
		{"только область", "", "Минская область", ""},
		{"область события", "Минская область", "Минская область", ""},
		{"р-н и обл.", "", "Сморгонский р-н, Гродненская обл.", ""},
		{"край и республика", "", "Краснодарский край, Республика Адыгея", ""},
		{"улица остаётся", "", "Дворцовая площадь", "Дворцовая площадь"},
		{"район и улица", "Минская область", "Минский район, ул. Ленина, 5", "ул. Ленина, 5"},
		{"номер дома с районом", "", "район 5", "район 5"},
		{"слово внутри названия", "", "Районный стадион", "Районный стадион"},
		{"пусто", "", "", ""},
		{"пробелы и пустые части", "", " , Барановичский район, ", ""},
	} {
		if got := visibleAddress(event.Event{Region: tt.region, Address: tt.address}); got != tt.want {
			t.Errorf("%s: visibleAddress(%q) = %q, want %q", tt.name, tt.address, got, tt.want)
		}
	}
}

// Карточка: город без области и района; обычный адрес остаётся.
func TestCardHidesArea(t *testing.T) {
	base := event.Event{Title: "Бизон трейл", StartDate: day(2026, 10, 18), CountryCode: "BY", City: "Колбовичи",
		Region: "Брестская область", Address: "Барановичский район", Type: event.TypeTrail}
	text := cardScreen(base, nil, 1, 1, placePlain).text
	if !strings.Contains(text, "Колбовичи") || areaRE.MatchString(text) {
		t.Errorf("карточка Беларуси содержит область/район или нет города:\n%s", text)
	}
	base.Address = "Барановичский район, ул. Лесная, 5"
	text = cardScreen(base, nil, 1, 1, placePlain).text
	if !strings.Contains(text, "Колбовичи, ул. Лесная, 5") || areaRE.MatchString(text) {
		t.Errorf("адрес с районом:\n%s", text)
	}
	base.Address = "Дворцовая площадь"
	if text = cardScreen(base, nil, 1, 1, placePlain).text; !strings.Contains(text, "Колбовичи, Дворцовая площадь") {
		t.Errorf("обычный адрес пропал:\n%s", text)
	}
	// Без города и с районом в адресе: город не указан, района нет.
	base.City, base.Address = "", "Барановичский район"
	if text = cardScreen(base, nil, 1, 1, placePlain).text; !strings.Contains(text, "город не указан") || areaRE.MatchString(text) {
		t.Errorf("без города:\n%s", text)
	}
}

// Беларусь в Telegram: страна → город → забег, без области и района ни в
// фильтрах, ни в меню города, ни в списке, ни в карточке; в БД они остаются.
func TestNoAreaInTelegram(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	save := func(country, date, city, key, region, address, title string) {
		t.Helper()
		d, err := time.Parse(time.DateOnly, date)
		if err != nil {
			t.Fatal(err)
		}
		e := &event.Event{Title: title, TitleNorm: strings.ToLower(title), StartDate: d, CountryCode: country,
			City: city, CityKey: key, Region: region, Address: address, Type: event.TypeTrail, Status: event.StatusScheduled,
			Distances: []event.Distance{{Meters: 10000}}}
		if _, err := s.SaveEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	save("BY", "2026-10-12", "Колбовичи", "kolbovichi", "Брестская область", "Барановичский район", "Жырандоля трейл")
	save("BY", "2026-10-13", "Минск", "minsk", "Минская область", "Минский район, ул. Ленина, 5", "Минский забег")
	save("RU", "2026-10-14", "Химки", "himki", "Московская область", "Химкинский район", "Химкинский лес")
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)

	// Область и район остаются в БД.
	items, err := s.ListEvents(ctx, event.Filter{CountryCode: "BY", CityKey: "kolbovichi", From: day(2026, 9, 29)}, 5, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListEvents = %+v, %v", items, err)
	}
	e, err := s.GetEvent(ctx, items[0].ID)
	if err != nil || e.Region != "Брестская область" || e.Address != "Барановичский район" || e.CityKey != "kolbovichi" {
		t.Fatalf("в БД: region %q, address %q, city_key %q, %v", e.Region, e.Address, e.CityKey, err)
	}

	for _, filters := range []string{`{"country":"BY"}`, `{"country":"*"}`, `{}`} {
		if _, _, err := s.UpdateFilters(ctx, 42, func([]byte) ([]byte, error) { return []byte(filters), nil }); err != nil {
			t.Fatal(err)
		}
		_, v := filtersV(t, b)
		screens := map[string]call{
			"сводка": press(t, b, api, "f:s"), "меню города": press(t, b, api, "f:c"),
			"меню типа": press(t, b, api, "f:t"), "список": press(t, b, api, "p:1:v"+strconv.Itoa(v)),
		}
		for name, c := range screens {
			if areaRE.MatchString(c.Form["text"]) {
				t.Errorf("%s %s: в тексте область/район:\n%s", filters, name, c.Form["text"])
			}
			for _, bt := range c.buttons(t) {
				if areaRE.MatchString(bt.Text) {
					t.Errorf("%s %s: кнопка %q про область/район", filters, name, bt.Text)
				}
			}
		}
		// Фильтры верхнего уровня — только Страна, Город, Дата, Дистанция, Тип.
		for _, bt := range screens["сводка"].buttons(t) {
			switch {
			case bt.Text == "📅 Дата", bt.Text == "📍 Город", bt.Text == "📏 Дистанция", bt.Text == "🏃 Тип",
				bt.Text == "🔄 Сбросить", strings.HasPrefix(bt.Text, "🔎 Показать "), bt.Text == "🌍 Страна", bt.Text == backLabel:
			default:
				t.Errorf("%s: неожиданная кнопка сводки %q", filters, bt.Text)
			}
		}
	}

	// Беларусь: меню города — только города, строка списка и карточки.
	if _, _, err := s.UpdateFilters(ctx, 42, func([]byte) ([]byte, error) { return []byte(`{"country":"BY"}`), nil }); err != nil {
		t.Fatal(err)
	}
	_, v := filtersV(t, b)
	menu := press(t, b, api, "f:c")
	var labels []string
	for _, bt := range menu.buttons(t) {
		if d := noV(bt.CallbackData); strings.HasPrefix(d, "f:c:") && d != "f:c:-" {
			labels = append(labels, bt.Text)
		}
	}
	if want := []string{"Колбовичи (1)", "Минск (1)"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("меню города: %v, want %v", labels, want)
	}
	if row := listRows(t, press(t, b, api, "p:1:v"+strconv.Itoa(v))); len(row) != 2 || !strings.Contains(row[0], "Колбовичи") {
		t.Errorf("список: %v", row)
	}
	card := press(t, b, api, "e:"+strconv.FormatInt(items[0].ID, 10)+":1:v"+strconv.Itoa(v)).Form["text"]
	if !strings.Contains(card, "Колбовичи") || areaRE.MatchString(card) {
		t.Errorf("карточка Колбович:\n%s", card)
	}
	// Уточнение сверх района («ул. Ленина, 5») остаётся.
	items, _ = s.ListEvents(ctx, event.Filter{CountryCode: "BY", CityKey: "minsk", From: day(2026, 9, 29)}, 5, 0)
	card = press(t, b, api, "e:"+strconv.FormatInt(items[0].ID, 10)+":1:v"+strconv.Itoa(v)).Form["text"]
	if !strings.Contains(card, "Минск, ул. Ленина, 5") || areaRE.MatchString(card) {
		t.Errorf("карточка Минска:\n%s", card)
	}
}
