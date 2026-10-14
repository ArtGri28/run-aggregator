package bot

import (
	"context"
	"html"
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

// searchFixture — каталог для поиска по тексту и выбора нескольких месяцев.
func searchFixture(t *testing.T) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	s := storetest.Open(t)
	seedCountryEvent(t, s, "RU", "2026-10-10", "Ростов-на-Дону", "rostov-na-donu", event.TypeRoad, "Казачья слава")
	seedCountryEvent(t, s, "RU", "2026-11-05", "Москва", "moscow", event.TypeTrail, "Арена трейл")
	seedCountryEvent(t, s, "RU", "2026-12-06", "Москва", "moscow", event.TypeRoad, "Московский марафон")
	seedCountryEvent(t, s, "RU", "2027-01-15", "Казань", "kazan", event.TypeRoad, "Ночной забег")
	api := fakeTelegram(t)
	return s, api, newTestBot(t, api, s)
}

// say отправляет боту текст и возвращает его единственный ответ.
func say(t *testing.T, b *Bot, api *fakeAPI, text string) call {
	t.Helper()
	api.reset()
	b.api.ProcessUpdate(context.Background(), textUpdate(42, text))
	sends := api.of("sendMessage")
	if len(sends) != 1 {
		t.Fatalf("%q: sendMessage = %d, want 1", text, len(sends))
	}
	checkCallbackLengths(t, sends[0])
	return sends[0]
}

// pressOn нажимает кнопку data в сообщении from: бот получает текст этого
// сообщения так, как его отдаёт Telegram, — без разметки.
func pressOn(t *testing.T, b *Bot, api *fakeAPI, from call, data string) call {
	t.Helper()
	u := cbUpdate(42, 7, fresh(t, b, data))
	u.CallbackQuery.Message.Message.Text = html.UnescapeString(tagRE.ReplaceAllString(from.Form["text"], ""))
	api.reset()
	b.api.ProcessUpdate(context.Background(), u)
	edits := api.of("editMessageText")
	if len(edits) != 1 || len(api.of("answerCallbackQuery")) != 1 {
		t.Fatalf("%s: правок %d, ответов %d", data, len(edits), len(api.of("answerCallbackQuery")))
	}
	checkCallbackLengths(t, edits[0])
	return edits[0]
}

var tagRE = regexp.MustCompile(`<[^>]+>`)

func TestSearchWords(t *testing.T) {
	for in, want := range map[string][]string{
		"Казачья слава":     {"казач", "слав"},
		"КАЗАЧЬЕЙ славы":    {"казач", "слав"}, // другая форма — те же основы
		"Арена":             {"арен"},
		"марафоне":          {"марафон"},
		"Московский":        {"московск"},
		"Берёзовая роща":    {"берез", "рощ"},
		"5 км":              {"км"},
		"забег 2026":        {"забег", "2026"},
		"!!! — я":           nil,
		"трейл, трейл!":     {"трейл"},
		"a b c d e f g h i": nil,
	} {
		if got := SearchWords(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SearchWords(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SearchWords("один два три четыре пять шесть семь восемь"); len(got) != maxSearchWords {
		t.Errorf("слов в запросе %d, want не больше %d", len(got), maxSearchWords)
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"марафон", "марафон", 0}, {"марофон", "марафон", 1}, {"маарфон", "марафон", 1}, // замена, перестановка
		{"марафн", "марафон", 1}, {"мараффон", "марафон", 1}, {"трейл", "кросс", 4}, {"", "бег", 3},
	} {
		if got := editDistance([]rune(tc.a), []rune(tc.b)); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	// Опечатка прощается только в длинном слове: одна — от 6 букв, две — от 10.
	for _, tc := range []struct {
		q, w string
		want int
	}{
		{"марафон", "марафон", 3}, {"мараф", "марафон", 2}, {"марофон", "марафон", 1}, {"марофо", "марафон", 1},
		{"слав", "переславл", 0}, {"марафон", "полумарафон", 2}, {"моск", "московск", 2},
		{"арин", "арен", 0}, {"казач", "казан", 0}, {"трейл", "кросс", 0}, {"полумарофан", "полумарафон", 1}, {"забег", "забор", 0},
	} {
		if got := wordScore(tc.q, tc.w); got != tc.want {
			t.Errorf("wordScore(%q, %q) = %d, want %d", tc.q, tc.w, got, tc.want)
		}
	}
}

// Порядок результатов: все слова раньше части слов, точное совпадение раньше
// опечатки, при равенстве — прежний порядок (дата).
func TestMatchEvents(t *testing.T) {
	items := []store.ListItem{
		{ID: 1, Title: "Марофон дружбы", City: "Тула"}, // опечатка в каталоге
		{ID: 2, Title: "Ночной забег", City: "Казань"},
		{ID: 3, Title: "Казанский марафон", City: "Казань"},
		{ID: 4, Title: "Марафон «Берёзовая роща»", City: "Тула"},
	}
	ids := func(words ...string) []int64 {
		out := []int64{}
		for _, it := range MatchEvents(items, words) {
			out = append(out, it.ID)
		}
		return out
	}
	for _, tc := range []struct {
		words []string
		want  []int64
	}{
		{SearchWords("марафон"), []int64{3, 4, 1}},    // точные раньше опечатки
		{SearchWords("казань марафон"), []int64{3}},   // все слова
		{SearchWords("тула забег"), []int64{1, 2, 4}}, // всех слов нет ни у кого — по одному, порядок прежний
		{SearchWords("березовой рощи"), []int64{4}},   // «ё» и формы слов
		{SearchWords("мрафон"), []int64{3, 4}},        // опечатка в запросе
		{SearchWords("атлантида"), []int64{}},
	} {
		if got := ids(tc.words...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("MatchEvents(%q) = %v, want %v", tc.words, got, tc.want)
		}
	}
}

func TestQueryOf(t *testing.T) {
	for line, want := range map[string]string{
		"Запрос «Казачья слава»":                   "Казачья слава",
		"🔎 Запрос «<b>Тула&»":                      "<b>Тула&",
		"Запрос «забег «Арена»»":                   "забег «Арена»",
		"Запрос «»":                                "",
		"Запрос «без конца":                        "",
		"🔎 Найдено: 5":                             "",
		"":                                         "",
		"Запрос «" + strings.Repeat("я", 65) + "»": "",
	} {
		if got := queryOf(line); got != want {
			t.Errorf("queryOf(%q) = %q, want %q", line, got, want)
		}
	}
}

// Текстовый запрос ищет события по названию и городу: по части слова, без
// учёта регистра, в другой форме слова; несколько событий — кнопками;
// поиск не меняет ни фильтры, ни каталог.
func TestTextSearch(t *testing.T) {
	_, api, b := searchFixture(t)
	say(t, b, api, "x") // создаёт пользователя: дальше сравниваются фильтры и версия
	f0, v0 := filtersV(t, b)
	events0 := dbScalar(t, `SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id)::text FROM events e`)

	for _, tc := range []struct {
		query string
		want  []string // названия найденных событий по порядку
	}{
		{"Казачья слава", []string{"Казачья слава"}},
		{"казачья", []string{"Казачья слава"}},        // часть названия, строчными
		{"КАЗАЧЬЕЙ СЛАВЫ", []string{"Казачья слава"}}, // регистр и другая форма слова
		{"Арена", []string{"Арена трейл"}},
		{"марафон", []string{"Московский марафон"}},
		{"Ростов", []string{"Казачья слава"}},                    // по городу
		{"моск", []string{"Арена трейл", "Московский марафон"}},  // несколько: по городу и по названию
		{"трейл забег", []string{"Арена трейл", "Ночной забег"}}, // всех слов нет ни у кого — хотя бы одно
	} {
		c := say(t, b, api, tc.query)
		rows := listRows(t, c)
		if len(rows) != len(tc.want) {
			t.Errorf("%q: события %q, want %q", tc.query, rows, tc.want)
			continue
		}
		for i, title := range tc.want {
			if !strings.HasSuffix(rows[i], " · "+title) {
				t.Errorf("%q: событие %d = %q, want %q", tc.query, i, rows[i], title)
			}
		}
		if !strings.HasPrefix(c.Form["text"], "Запрос «"+tc.query+"»") || !strings.Contains(c.Form["text"], "Старты по запросу: "+strconv.Itoa(len(tc.want))) {
			t.Errorf("%q: текст %q", tc.query, c.Form["text"])
		}
		if got := datas(t, c); !reflect.DeepEqual(got[len(got)-2:], []string{"f:c", "f:s"}) {
			t.Errorf("%q: последние кнопки %v, want выбор города и «Назад»", tc.query, got)
		}
	}

	// Подсказка города стоит перед событиями и выбирает город фильтром.
	c := say(t, b, api, "моск")
	if got := datas(t, c); got[0] != "f:c:moscow" || !strings.HasPrefix(c.buttons(t)[0].Text, "📍 Москва (2)") {
		t.Errorf("«моск»: первая кнопка %v", c.buttons(t)[0])
	}
	// Кнопка события открывает его карточку; «Назад» возвращает к тем же
	// результатам поиска: запрос карточка несёт последней строкой.
	card := pressOn(t, b, api, c, firstCard(t, c))
	if !strings.Contains(card.Form["text"], "<b>Арена трейл</b>") || !strings.HasSuffix(card.Form["text"], "\n\n🔎 Запрос «моск»") {
		t.Errorf("карточка из поиска:\n%s", card.Form["text"])
	}
	if got := card.button(t, backLabel); got != "g:b:v1" {
		t.Fatalf("«Назад» карточки из поиска = %q, want g:b:v1", got)
	}
	again := pressOn(t, b, api, card, "g:b:v1")
	if again.Form["text"] != c.Form["text"] || !reflect.DeepEqual(datas(t, again), datas(t, c)) {
		t.Errorf("возврат к результатам:\n%s\n%v\nwant\n%s\n%v", again.Form["text"], datas(t, again), c.Form["text"], datas(t, c))
	}
	// Текста сообщения нет (старое сообщение): карточка возвращает в каталог,
	// а возврат открывает первую страницу каталога.
	if got := press(t, b, api, firstCard(t, c)).button(t, backLabel); got != "p:1:v1" {
		t.Errorf("карточка без текста сообщения: «Назад» = %q, want p:1:v1", got)
	}
	if got := press(t, b, api, "g:b:v1").Form["text"]; !strings.HasPrefix(got, "🔎 <b>Найдено: 4</b>") {
		t.Errorf("возврат без текста сообщения: %q", got)
	}
	// Опечатки в запросе.
	for query, want := range map[string]string{"козачья слава": "Казачья слава", "марофон": "Московский марафон", "маарфон в москве": "Московский марафон"} {
		if rows := listRows(t, say(t, b, api, query)); len(rows) != 1 || !strings.HasSuffix(rows[0], " · "+want) {
			t.Errorf("%q: события %q, want %q", query, rows, want)
		}
	}

	// Ничего не найдено: понятное сообщение и каталог.
	c = say(t, b, api, "Атлантида")
	if !strings.HasPrefix(c.Form["text"], "По запросу «Атлантида» ничего не нашлось") {
		t.Errorf("нет совпадений: %q", c.Form["text"])
	}
	if got, want := datas(t, c), []string{"p:1:v1", "f:c", "f:s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("нет совпадений: кнопки %v, want %v", got, want)
	}
	if c.button(t, "📋 Открыть каталог") == "" {
		t.Errorf("нет кнопки каталога: %v", c.buttons(t))
	}

	if f, v := filtersV(t, b); !reflect.DeepEqual(f, f0) || v != v0 {
		t.Errorf("поиск изменил фильтры: %+v v=%d, было %+v v=%d", f, v, f0, v0)
	}
	if got := dbScalar(t, `SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id)::text FROM events e`); got != events0 {
		t.Error("поиск изменил таблицу events")
	}

	// Точное название города по-прежнему выбирает город.
	c = say(t, b, api, "Казань")
	if f, _ := filtersV(t, b); f.City != "kazan" || !strings.Contains(c.Form["text"], "Город: Казань") {
		t.Errorf("«Казань»: %+v, текст %q", f, c.Form["text"])
	}
}

// Результатов больше страницы: показаны первые pageSize и подсказка уточнить.
func TestTextSearchMany(t *testing.T) {
	s := storetest.Open(t)
	for i := 0; i < pageSize+3; i++ {
		seedCountryEvent(t, s, "RU", "2026-10-"+strconv.Itoa(10+i), "Тула", "tula", event.TypeRoad, "Осенний кросс №"+strconv.Itoa(i+1))
	}
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	c := say(t, b, api, "осенний")
	if rows := listRows(t, c); len(rows) != pageSize {
		t.Fatalf("события: %d, want %d", len(rows), pageSize)
	}
	want := "Старты по запросу: " + strconv.Itoa(pageSize+3) + "</b>\nПоказаны первые " + strconv.Itoa(pageSize) + " — уточните запрос"
	if !strings.Contains(c.Form["text"], want) {
		t.Errorf("текст %q, want %q", c.Form["text"], want)
	}
}

// Несколько периодов дат: отметка и снятие по одному, меню остаётся, выбор
// сохраняется сразу; в выдаче — события любого из отмеченных периодов;
// остальные фильтры не сбрасываются.
func TestDatePeriods(t *testing.T) {
	s, api, b := searchFixture(t)
	press(t, b, api, "f:t:road")

	c := press(t, b, api, "f:w:m202610") // «сегодня» теста — сентябрь
	if c.Form["text"] != "Выберите даты (можно несколько)" || c.button(t, "✓ Октябрь 2026") != "f:w:m202610" || c.button(t, "✓ 🔮") != "" {
		t.Fatalf("после f:w:m202610: текст %q кнопки %v", c.Form["text"], c.buttons(t))
	}
	c = press(t, b, api, "f:w:m202612")
	if c.button(t, "✓ Октябрь 2026") == "" || c.button(t, "✓ Декабрь 2026") != "f:w:m202612" {
		t.Fatalf("две отметки: %v", c.buttons(t))
	}
	f, _ := filtersV(t, b)
	if !reflect.DeepEqual(f.Dates, []string{"m202610", "m202612"}) || f.Type != "road" || f.Date != "" {
		t.Fatalf("фильтры: %+v", f)
	}

	// «Назад» сохраняет выбор; в выдаче октябрь и декабрь, ноябрьского трейла и январского забега нет.
	c = press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "📅 Дата: Октябрь 2026, Декабрь 2026") || showCount(t, c) != 2 {
		t.Fatalf("сводка: %q, Показать %d", c.Form["text"], showCount(t, c))
	}
	_, v := filtersV(t, b)
	list := press(t, b, api, "p:1:v"+strconv.Itoa(v))
	if rows := listRows(t, list); len(rows) != 2 || !strings.Contains(rows[0], "Казачья слава") || !strings.Contains(rows[1], "Московский марафон") {
		t.Errorf("выдача: %q", rows)
	}
	if !strings.Contains(list.Form["text"], "⚙️ Фильтры: Октябрь 2026, Декабрь 2026 · Шоссе") {
		t.Errorf("шапка списка: %q", list.Form["text"])
	}
	if got := press(t, b, api, "s:o").Form["text"]; !strings.Contains(got, "Активно фильтров: 2 из 5") {
		t.Errorf("настройки: %q", got)
	}

	// Повторное нажатие снимает отметку.
	press(t, b, api, "f:w:m202610")
	if f, _ := filtersV(t, b); !reflect.DeepEqual(f.Dates, []string{"m202612"}) {
		t.Errorf("после снятия: %+v", f)
	}
	// Один период хранится в прежнем поле date — его читает и прежняя версия бота.
	if raw, _, _ := s.Filters(context.Background(), 42); !strings.Contains(string(raw), `"date": "m202612"`) || strings.Contains(string(raw), "dates") {
		t.Errorf("один период в JSON: %s", raw)
	}

	// Кнопка даты из старого сообщения задаёт одно значение, заменяя выбор.
	press(t, b, api, "f:w:m202701")
	press(t, b, api, "f:d:nm")
	if f, _ := filtersV(t, b); !reflect.DeepEqual(f.Dates, []string{"nm"}) {
		t.Errorf("после f:d:nm: %+v", f)
	}

	// «Все будущие» — отдельный режим: снимает все отметки, тип остаётся.
	press(t, b, api, "f:w:m202612")
	c = press(t, b, api, "f:w:-")
	if f, _ := filtersV(t, b); f.hasDate() || f.Type != "road" || c.button(t, "✓ 🔮 Все будущие") != "f:w:-" {
		t.Errorf("после «Все будущие»: %+v, кнопки %v", f, c.buttons(t))
	}

	// Пресет «30 дней» из сохранённых фильтров снимается первой же отметкой.
	setUserFilters(t, s, `{"date":"30","type":"road"}`)
	press(t, b, api, "f:w:nm")
	if f, _ := filtersV(t, b); f.Date != "" || !reflect.DeepEqual(f.Dates, []string{"nm"}) || f.Type != "road" {
		t.Errorf("после отметки при «30 дней»: %+v", f)
	}
}

func TestLoadFiltersPeriods(t *testing.T) {
	today := day(2026, 9, 29)
	for raw, want := range map[string]userFilters{
		`{"date":"nm"}`:                {Dates: []string{"nm"}},
		`{"date":"30"}`:                {Date: "30"},
		`{"date":"30","dates":["nm"]}`: {Dates: []string{"nm"}},
		`{"dates":["m202612","nm","nm","zzz","m202608","30","cm"]}`: {Dates: []string{"cm", "nm", "m202612"}}, // порядок, повторы, мусор, прошедший месяц
		`{"dates":[]}`:   {},
		`{"dates":"nm"}`: {},
	} {
		if got := loadFilters([]byte(raw), today); !reflect.DeepEqual(got, want) {
			t.Errorf("loadFilters(%s) = %+v, want %+v", raw, got, want)
		}
	}
	// Периоды объединяются: событие подходит, если попадает хотя бы в один.
	q := userFilters{Dates: []string{"nm", "m202612"}}.query(today)
	want := []event.DateRange{{From: day(2026, 10, 1), To: day(2026, 10, 31)}, {From: day(2026, 12, 1), To: day(2026, 12, 31)}}
	if !q.From.Equal(today) || !q.To.IsZero() || !reflect.DeepEqual(q.Periods, want) {
		t.Errorf("query = %+v", q)
	}
	// Отметок не больше maxPeriods.
	f := userFilters{}
	for y := 2027; y < 2030; y++ {
		for m := 1; m <= 12; m++ {
			f = f.togglePeriod("m" + time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC).Format("200601"))
		}
	}
	if len(f.Dates) != maxPeriods {
		t.Errorf("отметок %d, want %d", len(f.Dates), maxPeriods)
	}
}

// Цветные метки корзин одинаковы в меню, сводке и шапке списка; 10, 21 и
// 42 км попадают ровно в одну корзину; можно отметить несколько.
func TestBucketColors(t *testing.T) {
	var got []string
	for _, bk := range buckets {
		got = append(got, bk.emoji+" "+bk.label)
	}
	if want := []string{"🟢 До 10 км", "🟡 10–21 км", "🟠 21–42 км", "🔴 42+ км"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("корзины %q, want %q", got, want)
	}
	for m, want := range map[int]int{10000: 0, 21000: 1, 21097: 1, 42000: 2, 42195: 2, 50000: 3} {
		var in []int
		for i, bk := range buckets {
			if m > bk.lo && m <= bk.hi {
				in = append(in, i)
			}
		}
		if !reflect.DeepEqual(in, []int{want}) {
			t.Errorf("%d м: корзины %v, want только %d", m, in, want)
		}
	}

	_, api, b := searchFixture(t) // у всех событий дистанция 10 км
	press(t, b, api, "f:n:0")
	c := press(t, b, api, "f:n:3")
	var texts []string
	for _, bt := range c.buttons(t) {
		texts = append(texts, bt.Text)
	}
	if want := []string{"✓ 🟢 До 10 км", "🟡 10–21 км", "🟠 21–42 км", "✓ 🔴 42+ км", "🏃 Любая", backLabel}; !reflect.DeepEqual(texts, want) {
		t.Errorf("меню дистанций %q, want %q", texts, want)
	}
	c = press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "📏 Дистанция: 🟢 До 10 км, 🔴 42+ км") || showCount(t, c) != 4 {
		t.Errorf("сводка: %q, Показать %d", c.Form["text"], showCount(t, c))
	}
	_, v := filtersV(t, b)
	if got := press(t, b, api, "p:1:v"+strconv.Itoa(v)).Form["text"]; !strings.Contains(got, "⚙️ Фильтры: 🟢 До 10 км, 🔴 42+ км") {
		t.Errorf("шапка списка: %q", got)
	}
	// Только корзина, куда 10 км не входит: событий нет.
	press(t, b, api, "f:n:0")
	if n := showCount(t, press(t, b, api, "f:s")); n != 0 {
		t.Errorf("при одной корзине 42+: Показать %d, want 0", n)
	}
}

// Длинный город не вытесняет название из кнопки события.
func TestEventButtonLongCity(t *testing.T) {
	it := store.ListItem{ID: 1, Title: "Забег", StartDate: fixtureToday, City: "Петропавловск-Камчатский и окрестности"}
	got := eventBtn(it, callback{Kind: cbCard, ID: 1, Page: 1, V: 1}, false, 2026)[0].Text
	if want := "29.09 · Петропавловск-К… · Забег"; got != want {
		t.Errorf("кнопка %q, want %q", got, want)
	}
}
