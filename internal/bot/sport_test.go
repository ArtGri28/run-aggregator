package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/normalize"
	"aggregator/internal/store/storetest"
)

// TestSportHiddenList — сквозной путь D-08: событие выключенного вида спорта
// пропадает из списка и из счётчика.
func TestSportHiddenList(t *testing.T) {
	s, api, b := seeded(t)
	f := event.Filter{From: fixtureToday}
	items := allItems(t, s, f)
	n, btn := len(items), fmt.Sprintf("e:%d:", items[0].ID)
	listed := func(want int) bool {
		t.Helper()
		page := press(t, b, api, "p:1:v1")
		if !strings.Contains(page.Form["text"], fmt.Sprintf("Найдено: %d<", want)) {
			t.Fatalf("текст списка %q, want «Найдено: %d»", page.Form["text"], want)
		}
		return strings.Contains(" "+strings.Join(datas(t, page), " "), " "+btn)
	}
	if !listed(n) {
		t.Fatalf("в списке нет кнопки %s", btn)
	}

	storetest.SetSport(t, items[0].ID, event.SportSkiing)
	if listed(n - 1) {
		t.Fatalf("в списке осталась кнопка %s", btn)
	}
	if got, err := s.CountEvents(context.Background(), f); err != nil || got != n-1 {
		t.Fatalf("CountEvents = %d, %v; want %d", got, err, n-1)
	}
	t.Logf("TRACER sport=hidden list=absent count=%d", n-1)
}

// TestSportHidden — D-08: событие выключенного вида спорта не видно ни на
// одном экране, не меняет ни одного экрана и недоступно по прямой кнопке.
// Каждая проба сначала находит пробное событие, пока его вид спорта — бег.
func TestSportHidden(t *testing.T) {
	s, api, b := seeded(t)
	ctx := context.Background()
	const (
		city, word = "Оймякон", "Зимогорье"
		titleA     = "Пробный " + word
	)
	key := normalize.CityKey(city)

	// view — всё, что видит пользователь: текст и кнопки (без версии фильтров).
	view := func(c call) string {
		t.Helper()
		out := []string{c.Form["text"]}
		for _, bt := range c.buttons(t) {
			out = append(out, bt.Text+" | "+versionRE.ReplaceAllString(bt.CallbackData, ""))
		}
		return strings.Join(out, "\n")
	}
	has := func(c call, prefix string) bool {
		t.Helper()
		return slices.ContainsFunc(datas(t, c), func(d string) bool { return strings.HasPrefix(d, prefix) })
	}
	// screen — экран по кнопке data при фильтрах filters.
	screen := func(filters, data string) call {
		t.Helper()
		setUserFilters(t, s, filters)
		return press(t, b, api, data)
	}
	// list — все страницы выдачи по всем странам: кнопки и число найденных.
	list := func() (buttons []string, found string) {
		t.Helper()
		setUserFilters(t, s, `{"country":"*"}`)
		_, v := filtersV(t, b)
		for page := 1; ; page++ {
			c := press(t, b, api, fmt.Sprintf("p:%d:v%d", page, v))
			if page == 1 {
				found, _, _ = strings.Cut(c.Form["text"], "\n")
			}
			buttons = append(buttons, datas(t, c)...)
			if !slices.ContainsFunc(c.buttons(t), func(bt models.InlineKeyboardButton) bool { return bt.Text == "→" }) {
				return buttons, found
			}
		}
	}
	text := func(in string) call {
		t.Helper()
		setUserFilters(t, s, `{}`)
		return say(t, b, api, in)
	}

	// До пробных событий: экраны, с которыми сравнивается итог.
	search0, country0 := view(screen(`{}`, "f:s")), view(screen(`{}`, "f:k"))
	_, found0 := list()
	n0 := len(allItems(t, s, event.Filter{From: fixtureToday}))
	none0 := view(text(word))
	if !strings.Contains(none0, "ничего не нашлось") || strings.Contains(search0, "Страна") {
		t.Fatalf("фикстура: запрос %q —\n%s\nэкран поиска —\n%s", word, none0, search0)
	}
	if _, n, err := s.CityName(ctx, "", key, fixtureToday); err != nil || n != 0 {
		t.Fatalf("фикстура: город %s уже в каталоге (%d, %v)", key, n, err)
	}
	if strings.Contains(view(screen(`{}`, "f:t")), "Стадион") {
		t.Fatal("фикстура: тип «Стадион» уже в каталоге")
	}

	// Пробные события без вида спорта: (а) в стране каталога, (б) в другой.
	save := func(country, title, city, cityKey string, typ event.Type) int64 {
		t.Helper()
		e := &event.Event{Title: title, TitleNorm: strings.ToLower(title), StartDate: fixtureToday.AddDate(0, 0, 3),
			CountryCode: country, City: city, CityKey: cityKey, Type: typ, Status: event.StatusScheduled,
			Distances: []event.Distance{{Meters: 10000}}}
		if _, err := s.SaveEvent(ctx, e); err != nil {
			t.Fatalf("SaveEvent: %v", err)
		}
		return e.ID
	}
	a := save("RU", titleA, city, key, event.TypeTrack)
	by := save("BY", "Пробный старт", "Гродно", "grodno", event.TypeRoad)
	btnA, btnBY, searchA := fmt.Sprintf("e:%d:", a), fmt.Sprintf("e:%d:", by), fmt.Sprintf("g:%d", a)

	// probes — что из пробных событий видно на экранах; cardA — кнопка
	// события (а) в списке.
	probes := func() (seen map[string]bool, cardA string) {
		t.Helper()
		buttons, found := list()
		if i := slices.IndexFunc(buttons, func(d string) bool { return strings.HasPrefix(d, btnA) }); i >= 0 {
			cardA = buttons[i]
		}
		byCity := text(city)
		f, _ := filtersV(t, b)
		return map[string]bool{
			"страна в меню страны":                    has(screen(`{}`, "f:k"), "f:k:BY"),
			"экран поиска изменился":                  view(screen(`{}`, "f:s")) != search0,
			"меню страны изменилось":                  view(screen(`{}`, "f:k")) != country0,
			"событие в списке":                        cardA != "",
			"событие другой страны в списке":          slices.ContainsFunc(buttons, func(d string) bool { return strings.HasPrefix(d, btnBY) }),
			"счётчик списка вырос на два":             found == fmt.Sprintf("🔎 <b>Найдено: %d</b>", n0+2),
			"счётчик списка прежний":                  found == found0,
			"город в меню города":                     has(screen(`{"type":"track"}`, "f:c"), "f:c:"+key),
			"«Стадион» в меню типа":                   strings.Contains(view(screen(`{}`, "f:t")), "Стадион"),
			"событие по слову названия":               has(text(word), searchA),
			"по слову названия ничего не нашлось":     view(text(word)) == none0,
			"город в подсказках по началу":            has(text("Оймяк"), "f:c:"+key),
			"текст с названием города выбирает город": strings.Contains(byCity.Form["text"], "Город: "+city) && f.City == key,
		}, cardA
	}
	want := func(step string, got map[string]bool, visible bool) {
		t.Helper()
		for probe, v := range got {
			negative := probe == "счётчик списка прежний" || probe == "по слову названия ничего не нашлось"
			if v != (visible != negative) {
				t.Errorf("%s: «%s» = %v", step, probe, v)
			}
		}
	}

	// Контроль чувствительности: вид спорта — бег, каждая проба находит событие.
	seen, cardA := probes()
	want("бег", seen, true)
	if cardA == "" {
		t.FailNow()
	}
	results := text(word)
	rd := datas(t, results)
	cardS := rd[slices.IndexFunc(rd, func(d string) bool { return strings.HasPrefix(d, searchA) })]
	title := "<b>" + titleA + "</b>"
	if c := press(t, b, api, cardA); !strings.Contains(c.Form["text"], title) {
		t.Errorf("бег: карточка из списка:\n%s", c.Form["text"])
	}
	if c := pressOn(t, b, api, results, cardS); !strings.Contains(c.Form["text"], title) {
		t.Errorf("бег: карточка из поиска:\n%s", c.Form["text"])
	}

	// Выключенный вид спорта.
	storetest.SetSport(t, a, event.SportSkiing)
	storetest.SetSport(t, by, event.SportSkiing)
	seen, _ = probes()
	want("skiing", seen, false)

	// Карточка по прямой кнопке — как у скрытого события.
	page := strings.Split(cardA, ":")[2]
	fromList, fromSearch := press(t, b, api, cardA), pressOn(t, b, api, results, cardS)
	if fromList.Form["text"] != goneNotice || !strings.HasPrefix(fromList.button(t, backLabel), "p:"+page+":v") || len(fromList.buttons(t)) != 1 {
		t.Errorf("skiing: карточка из списка: %q, кнопки %v", fromList.Form["text"], datas(t, fromList))
	}
	if !strings.HasPrefix(fromSearch.Form["text"], goneNotice) || len(fromSearch.buttons(t)) != 1 || fromSearch.buttons(t)[0].Text != backLabel {
		t.Errorf("skiing: карточка из поиска: %q, кнопки %v", fromSearch.Form["text"], datas(t, fromSearch))
	}
	storetest.SetSport(t, a, event.SportRunning)
	storetest.HideEvent(t, a)
	if got := view(press(t, b, api, cardA)); got != view(fromList) {
		t.Errorf("карточка из списка: скрытое событие —\n%s\nвыключенный вид спорта —\n%s", got, view(fromList))
	}
	if got := view(pressOn(t, b, api, results, cardS)); got != view(fromSearch) {
		t.Errorf("карточка из поиска: скрытое событие —\n%s\nвыключенный вид спорта —\n%s", got, view(fromSearch))
	}
}
