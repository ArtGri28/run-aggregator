package bot

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestDateRange(t *testing.T) {
	today := day(2026, 9, 29)
	var none time.Time
	cases := []struct {
		name     string
		preset   string
		today    time.Time
		from, to time.Time
	}{
		{"пусто", "", today, today, none},
		{"a", "a", today, today, none},
		{"вне грамматики", "zzz", today, today, none},
		{"30 дней", "30", today, today, day(2026, 10, 29)},
		{"этот месяц", "cm", today, today, day(2026, 9, 30)},
		{"следующий месяц", "nm", today, day(2026, 10, 1), day(2026, 10, 31)},
		{"месяц в будущем", "m202610", today, day(2026, 10, 1), day(2026, 10, 31)},
		{"текущий месяц не раньше сегодня", "m202609", today, today, day(2026, 9, 30)},
		{"следующий месяц через Новый год", "nm", day(2026, 12, 15), day(2027, 1, 1), day(2027, 1, 31)},
		{"следующий месяц: февраль", "nm", day(2027, 1, 31), day(2027, 2, 1), day(2027, 2, 28)},
	}
	for _, tc := range cases {
		from, to := dateRange(tc.preset, tc.today)
		if !from.Equal(tc.from) || !to.Equal(tc.to) {
			t.Errorf("%s: dateRange(%q) = %v..%v, want %v..%v", tc.name, tc.preset, from, to, tc.from, tc.to)
		}
	}
}

func TestBucketMembership(t *testing.T) {
	// Сплошные полуинтервалы: у каждой дистанции ровно одна корзина.
	for m := 1; m <= 200000; m++ {
		var in []int
		for i, bk := range buckets {
			if m > bk.lo && m <= bk.hi {
				in = append(in, i)
			}
		}
		if len(in) != 1 {
			t.Fatalf("%d м попадает в корзины %v, want ровно одну", m, in)
		}
	}
	// 10 км — в первой корзине, полумарафон — во второй, марафон — в третьей.
	for m, want := range map[int]int{500: 0, 5000: 0, 10000: 0, 10001: 1, 21097: 1, 21100: 1, 21101: 2,
		42000: 2, 42195: 2, 42200: 2, 42201: 3, 100000: 3} {
		for i, bk := range buckets {
			if got := m > bk.lo && m <= bk.hi; got != (i == want) {
				t.Errorf("%d м: корзина %d = %v, want корзина %d", m, i, got, want)
			}
		}
	}
	if buckets[len(buckets)-1].hi != 2147483647 {
		t.Errorf("верхняя граница последней корзины = %d, want максимум INTEGER", buckets[len(buckets)-1].hi)
	}
}

func TestMonthOptions(t *testing.T) {
	opts := monthOptions(day(2026, 9, 29))
	if len(opts) != 12 || opts[0] != (option{"m202609", "сентябрь 2026"}) || opts[11] != (option{"m202708", "август 2027"}) {
		t.Fatalf("monthOptions = %+v", opts)
	}
	for _, o := range opts {
		if _, err := (callback{Kind: cbDate, Value: o.Value}).encode(); err != nil {
			t.Errorf("месяц %q не кодируется: %v", o.Value, err)
		}
	}
}

func TestFilterLabels(t *testing.T) {
	if got, want := summaryLine(userFilters{}, "", false), "📍 Город: Все города\n📅 Дата: Все будущие\n📏 Дистанция: Любая\n🏃 Тип: Любой"; got != want {
		t.Fatalf("summaryLine(default) = %q, want %q", got, want)
	}
	for date, want := range map[string]string{"": "все будущие", "a": "все будущие", "30": "30 дней",
		"cm": "этот месяц", "nm": "следующий месяц", "m202611": "ноябрь 2026"} {
		if got := dateLabel(date); got != want {
			t.Errorf("dateLabel(%q) = %q, want %q", date, got, want)
		}
	}
	got := summaryLine(userFilters{Dist: []int{0, 3}, Type: "trail", City: "x"}, "<b>Тула&", false)
	want := "📍 Город: &lt;b&gt;Тула&amp;\n📅 Дата: Все будущие\n📏 Дистанция: 🟢 До 10 км, 🔴 42+ км\n🏃 Тип: Трейл"
	if got != want {
		t.Fatalf("summaryLine = %q, want %q", got, want)
	}
	if typeLabel("road") != "Шоссе" || typeLabel("") != "любой" {
		t.Fatalf("typeLabel: road=%q, empty=%q", typeLabel("road"), typeLabel(""))
	}
}

func TestFilterMutations(t *testing.T) {
	f := userFilters{}.setDate("a")
	if f.Date != "" {
		t.Fatalf(`setDate("a") = %q, want значение по умолчанию ""`, f.Date)
	}
	f = f.setDate("nm").setCity("moscow").setType("road").toggleBucket(3).toggleBucket(1).toggleBucket(3).toggleBucket(0)
	if want := (userFilters{Dates: []string{"nm"}, City: "moscow", Dist: []int{0, 1}, Type: "road"}); !reflect.DeepEqual(f, want) {
		t.Fatalf("после изменений %+v, want %+v", f, want)
	}
	// Исходное значение не меняется (срез не разделяется).
	g := f.toggleBucket(4)
	if !reflect.DeepEqual(f.Dist, []int{0, 1}) || !reflect.DeepEqual(g.Dist, []int{0, 1, 4}) {
		t.Fatalf("toggleBucket: исходный %v, новый %v", f.Dist, g.Dist)
	}
	for field, want := range map[byte]userFilters{
		'd': {City: "moscow", Dist: []int{0, 1}, Type: "road"},
		'c': {Dates: []string{"nm"}, Dist: []int{0, 1}, Type: "road"},
		'b': {Dates: []string{"nm"}, City: "moscow", Type: "road"},
		't': {Dates: []string{"nm"}, City: "moscow", Dist: []int{0, 1}},
	} {
		if got := f.unset(field); !reflect.DeepEqual(got, want) {
			t.Errorf("unset(%c) = %+v, want %+v", field, got, want)
		}
	}
	if got := f.setCity("-").setType("-").clearBuckets().setDate(""); !reflect.DeepEqual(got, userFilters{Date: "", City: "", Dist: nil, Type: ""}) {
		t.Fatalf("снятие по одному = %+v, want zero", got)
	}
	if got := f.reset(); !reflect.DeepEqual(got, userFilters{}) {
		t.Fatalf("reset = %+v, want zero", got)
	}
	raw, err := json.Marshal(f.toggleBucket(0).toggleBucket(1).reset())
	if err != nil || string(raw) != "{}" {
		t.Fatalf("json нулевых фильтров = %s, %v; want {}", raw, err)
	}
	if raw, _ := json.Marshal(f.toggleBucket(0).toggleBucket(1).unset('d').unset('c').unset('t')); string(raw) != "{}" {
		t.Fatalf("json после снятия всех корзин = %s, want {}", raw)
	}
}

// --- сценарии на фиктивном Bot API и реальных данных ---

func textUpdate(userID int64, text string) *models.Update {
	u := startUpdate(userID)
	u.Message.Text, u.Message.Entities = text, nil
	return u
}

// fresh делает нажатие «актуальным»: пользователь 42 есть в users (как
// после /start), а кнопке f: без версии дописана текущая версия фильтров.
// Устаревшие кнопки и кнопки без пользователя проверяет stale_test.go.
func fresh(t *testing.T, b *Bot, data string) string {
	t.Helper()
	_, v, err := b.Store.Filters(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if versioned(data) && !versionRE.MatchString(data) {
		return data + ":v" + strconv.Itoa(v)
	}
	return data
}

var versionRE = regexp.MustCompile(`:v[0-9]+$`)

// versioned — кнопки, которым версию проставляет stamp: f: и s:.
func versioned(data string) bool {
	return strings.HasPrefix(data, "f:") || strings.HasPrefix(data, "s:")
}

// noV убирает версию из кнопки f: или s: — для сравнения состава кнопок в
// тестах, где версия не предмет проверки (её проверяют снимки и stale_test.go).
func noV(data string) string {
	if versioned(data) {
		return versionRE.ReplaceAllString(data, "")
	}
	return data
}

// date1 — единственное значение даты фильтров: пресет либо один отмеченный
// период; несколько периодов — через запятую.
func date1(f userFilters) string {
	if len(f.Dates) > 0 {
		return strings.Join(f.Dates, ",")
	}
	return f.Date
}

// press нажимает актуальную кнопку (см. fresh) и возвращает единственную
// правку сообщения; на каждый callback — ровно один ответ.
func press(t *testing.T, b *Bot, api *fakeAPI, data string) call {
	t.Helper()
	data = fresh(t, b, data)
	api.reset()
	b.api.ProcessUpdate(context.Background(), cbUpdate(42, 7, data))
	if n := len(api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("%s: answerCallbackQuery = %d, want 1", data, n)
	}
	edits := api.of("editMessageText")
	if len(edits) != 1 {
		t.Fatalf("%s: editMessageText = %d, want 1 (calls %+v)", data, len(edits), api.calls)
	}
	checkCallbackLengths(t, edits[0])
	return edits[0]
}

func datas(t *testing.T, c call) []string {
	t.Helper()
	var out []string
	for _, bt := range c.buttons(t) {
		out = append(out, noV(bt.CallbackData))
	}
	return out
}

func filtersV(t *testing.T, b *Bot) (userFilters, int) {
	t.Helper()
	raw, v, err := b.Store.Filters(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	return loadFilters(raw, fixtureToday), v
}

func TestFilterFlow(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	ctx := context.Background()
	today := day(2026, 9, 29)
	count := func(f event.Filter) string {
		n, err := s.CountEvents(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return "🔎 Показать " + strconv.Itoa(n) + " "
	}

	// Сводка: шесть кнопок D-10.
	c := press(t, b, api, "f:s")
	if got, want := datas(t, c), []string{"f:c", "f:d", "f:n", "f:t", "p:1:v1", "s:r:f", "m"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("f:s: кнопки %v, want %v", got, want)
	}

	// Город: топ по данным, «Все города».
	c = press(t, b, api, "f:c")
	if bt := c.buttons(t)[0]; bt.Text != "Москва (9)" || noV(bt.CallbackData) != "f:c:moscow" {
		t.Fatalf("f:c: первая кнопка %+v, want «Москва (9)» f:c:moscow", bt)
	}
	if c.button(t, "Все города") != "f:c:-" {
		t.Fatalf("f:c: нет «Все города» f:c:-: %v", datas(t, c))
	}
	c = press(t, b, api, "f:c:moscow")
	if !strings.Contains(c.Form["text"], "Город: Москва") {
		t.Fatalf("f:c:moscow: текст %q", c.Form["text"])
	}
	if _, v := filtersV(t, b); v != 2 {
		t.Fatalf("filters_v после города = %d, want 2", v)
	}
	press(t, b, api, "f:c:moscow")
	if _, v := filtersV(t, b); v != 2 {
		t.Fatalf("filters_v после повторного выбора = %d, want 2", v)
	}

	// Дистанция: подменю остаётся, отметка «✓».
	c = press(t, b, api, "f:n")
	if got, want := datas(t, c), []string{"f:n:0", "f:n:1", "f:n:2", "f:n:3", "f:n:-", "f:s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("f:n: кнопки %v, want %v", got, want)
	}
	c = press(t, b, api, "f:n:1")
	if c.button(t, "✓ ") != "f:n:1" || len(c.buttons(t)) != 6 {
		t.Fatalf("f:n:1: подменю без отметки у корзины 1: %+v", c.buttons(t))
	}
	if _, v := filtersV(t, b); v != 3 {
		t.Fatalf("filters_v после корзины = %d, want 3", v)
	}
	c = press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "Дистанция: 🟡 10–21 км") {
		t.Fatalf("f:s: текст %q", c.Form["text"])
	}

	// Тип: только встречающиеся (D-09).
	c = press(t, b, api, "f:t")
	var labels []string
	for _, bt := range c.buttons(t) {
		if bt.Text != "⬅️ Назад" {
			labels = append(labels, bt.Text)
		}
	}
	if want := []string{"🛣 Шоссе", "🏔 Трейл", "✓ 🏃 Любой"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("f:t: кнопки %v, want %v", labels, want)
	}
	road := event.Filter{From: today, CityKey: "moscow", Type: event.TypeRoad,
		Distances: []event.DistanceRange{{Lo: 10000, Hi: 21100}}}
	c = press(t, b, api, "f:t:road")
	if bt := "p:1:v4"; c.button(t, count(road)) != bt || !strings.Contains(c.Form["text"], "Тип: Шоссе") {
		t.Fatalf("f:t:road: кнопки %v, текст %q, want %q и p:1:v4", datas(t, c), c.Form["text"], count(road))
	}

	// [Показать N]: в списке только Москва.
	c = press(t, b, api, "p:1:v4")
	found := 0
	for _, l := range listRows(t, c) {
		found++
		if !strings.Contains(l, "Москва") {
			t.Fatalf("строка списка %q не из Москвы", l)
		}
	}
	if found == 0 {
		t.Fatal("список пуст: проверка города ничего не проверила")
	}

	// Дата: двенадцать месяцев с текущего и «все будущие»; старая кнопка «следующий месяц» — октябрь 2026.
	c = press(t, b, api, "f:d")
	months := 0
	for _, d := range datas(t, c) {
		if strings.HasPrefix(d, "f:w:m") {
			months++
		}
	}
	if strings.Contains(strings.Join(datas(t, c), " "), ":30") {
		t.Fatalf("f:d: в меню остался пресет «30 дней»: %v", datas(t, c))
	}
	if all := strings.Join(datas(t, c), " "); !strings.Contains(all, "f:w:-") || strings.Contains(all, "f:w:cm") || strings.Contains(all, "f:w:nm") {
		t.Fatalf("f:d: кнопки %v, want «все будущие» без «этот/следующий месяц»", datas(t, c))
	}
	if months != 12 {
		t.Fatalf("f:d: месяцев %d, want 12", months)
	}
	nm := road
	nm.From, nm.To = day(2026, 10, 1), day(2026, 10, 31)
	c = press(t, b, api, "f:d:nm")
	if !strings.Contains(c.Form["text"], "Дата: Следующий месяц") || c.button(t, count(nm)) != "p:1:v5" {
		t.Fatalf("f:d:nm: текст %q, кнопки %v, want %q и p:1:v5", c.Form["text"], datas(t, c), count(nm))
	}
	if f, v := filtersV(t, b); v != 5 || date1(f) != "nm" {
		t.Fatalf("фильтры %+v v=%d, want date nm, v=5", f, v)
	}
}

func TestCityText(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	ctx := context.Background()

	// «Питер» → Санкт-Петербург новым сообщением-сводкой.
	b.api.ProcessUpdate(ctx, textUpdate(42, "Питер"))
	sends := api.of("sendMessage")
	if len(sends) != 1 || !strings.Contains(sends[0].Form["text"], "Город: Санкт-Петербург") {
		t.Fatalf("«Питер»: sendMessage = %+v", sends)
	}
	if f, v := filtersV(t, b); f.City != "saint-petersburg" || v != 2 {
		t.Fatalf("после «Питер» фильтры %+v v=%d, want saint-petersburg, v=2", f, v)
	}

	// Города нет: сообщение и похожие.
	api.reset()
	b.api.ProcessUpdate(ctx, textUpdate(42, "Новгород"))
	sends = api.of("sendMessage")
	if len(sends) != 1 || !strings.HasPrefix(sends[0].Form["text"], "Запрос «Новгород»") || !strings.Contains(sends[0].Form["text"], "Города по запросу: 2") {
		t.Fatalf("«Новгород»: sendMessage = %+v", sends)
	}
	// Сначала подсказки городов, затем события по запросу, затем выбор города и возврат.
	got := datas(t, sends[0])
	if len(got) < 5 || !reflect.DeepEqual(got[:2], []string{"f:c:nizhniy-novgorod", "f:c:velikiy-novgorod"}) || !reflect.DeepEqual(got[len(got)-2:], []string{"f:c", "f:s"}) {
		t.Fatalf("«Новгород»: кнопки %v", got)
	}
	for _, r := range listRows(t, sends[0]) {
		if !strings.Contains(strings.ToLower(r), "новгород") {
			t.Errorf("«Новгород»: событие не по запросу: %q", r)
		}
	}

	// Разметка в вводе экранируется.
	api.reset()
	b.api.ProcessUpdate(ctx, textUpdate(42, "<b>Тула&"))
	sends = api.of("sendMessage")
	if len(sends) != 1 || strings.Contains(sends[0].Form["text"], "<b>Тула") || !strings.Contains(sends[0].Form["text"], "«&lt;b&gt;Тула&amp;»") {
		t.Fatalf("«<b>Тула&»: sendMessage = %+v", sends)
	}

	// Команды, слишком длинный и пустой по смыслу ввод — подсказка; фильтры целы.
	_, vBefore := filtersV(t, b)
	for _, in := range []string{"/foo", strings.Repeat("а", 65)} {
		api.reset()
		b.api.ProcessUpdate(ctx, textUpdate(42, in))
		sends = api.of("sendMessage")
		if len(sends) != 1 || !strings.Contains(sends[0].Form["text"], "/start") {
			t.Fatalf("ввод %q: sendMessage = %+v, want подсказку /start", in, sends)
		}
	}
	api.reset()
	b.api.ProcessUpdate(ctx, textUpdate(42, "!!!"))
	if sends = api.of("sendMessage"); len(sends) != 1 || !strings.Contains(sends[0].Form["text"], "Не понял") {
		t.Fatalf("«!!!»: sendMessage = %+v, want «Не понял…»", sends)
	}
	if f, v := filtersV(t, b); f.City != "saint-petersburg" || v != vBefore {
		t.Fatalf("фильтры изменились: %+v v=%d (было %d)", f, v, vBefore)
	}
}

func TestEmptyResult(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)

	press(t, b, api, "f:c:moscow")
	c := press(t, b, api, "f:t:track")
	if c.button(t, "🔎 Показать 0") != "p:1:v3" {
		t.Fatalf("сводка: кнопки %v, want «Показать 0» p:1:v3", datas(t, c))
	}
	c = press(t, b, api, "p:1:v3")
	if c.Form["text"] != "Ничего не нашлось\n\nПопробуйте снять ограничение:" {
		t.Fatalf("пустая выдача: текст %q", c.Form["text"])
	}
	if got, want := datas(t, c), []string{"f:x:c", "f:x:t", "q:s", "f:s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("пустая выдача: кнопки %v, want %v", got, want)
	}
	c = press(t, b, api, "f:x:t")
	if !strings.Contains(c.Form["text"], "Тип: Любой") || !strings.Contains(c.Form["text"], "Город: Москва") {
		t.Fatalf("f:x:t: текст %q", c.Form["text"])
	}
}

func TestResetFilters(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)

	press(t, b, api, "f:c:moscow")
	press(t, b, api, "f:n:2")
	press(t, b, api, "f:t:road")
	press(t, b, api, "f:d:30")
	_, before := filtersV(t, b)

	// «Сбросить» в сводке — только через подтверждение.
	if ask := press(t, b, api, "s:r:f"); ask.button(t, "✅ Да, сбросить") != "s:y:f" {
		t.Fatalf("подтверждение сброса: %v", datas(t, ask))
	}
	if _, v := filtersV(t, b); v != before {
		t.Fatalf("вопрос о сбросе изменил версию: %d → %d", before, v)
	}
	c := press(t, b, api, "s:y:f")
	if !strings.Contains(c.Form["text"], "📍 Город: Все города\n📅 Дата: Все будущие\n📏 Дистанция: Любая\n🏃 Тип: Любой") {
		t.Fatalf("сброс: текст %q", c.Form["text"])
	}
	raw, v, err := s.Filters(context.Background(), 42)
	if err != nil || string(raw) != "{}" || v != before+1 {
		t.Fatalf("после сброса filters=%s v=%d err=%v; want {} и v=%d", raw, v, err, before+1)
	}
}

// TestStaleMonthFilter — LO-05: сохранённый прошедший месяц читается как
// «все будущие» и уходит из users.filters при следующем изменении.
func TestStaleMonthFilter(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	ctx := context.Background()

	_, v, err := b.update(ctx, 42, func(f userFilters) userFilters { f.Date = "m202608"; return f })
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CountEvents(ctx, event.Filter{From: fixtureToday})
	if err != nil || n == 0 {
		t.Fatalf("CountEvents = %d, %v; want > 0", n, err)
	}
	page := "p:1:v" + strconv.Itoa(v)
	c := press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "Дата: Все будущие") || c.button(t, "🔎 Показать "+strconv.Itoa(n)+" ") != page {
		t.Fatalf("сводка: текст %q кнопки %v; want «Дата: Все будущие» и «Показать %d» %s", c.Form["text"], datas(t, c), n, page)
	}
	if c = press(t, b, api, page); !strings.HasPrefix(c.Form["text"], "🔎 <b>Найдено") {
		t.Fatalf("выдача: текст %q", c.Form["text"])
	}
	press(t, b, api, "f:t:road")
	if raw, _, err := s.Filters(ctx, 42); err != nil || strings.Contains(string(raw), "m202608") {
		t.Fatalf("после изменения filters=%s err=%v; want без m202608", raw, err)
	}
}

func TestSetCountry(t *testing.T) {
	f := userFilters{Date: "nm", City: "minsk", Type: "road", Dist: []int{1}, Country: "BY"}
	// Та же страна: город сохраняется.
	if got := f.setCountry("BY"); !reflect.DeepEqual(got, f) {
		t.Errorf("setCountry(BY) при BY = %+v, want без изменений", got)
	}
	// Другая страна, все страны, страна по умолчанию: город сбрасывается, остальное — нет.
	for _, c := range []string{"RU", "PL", "*", "-", ""} {
		got := f.setCountry(c)
		want := userFilters{Date: "nm", Type: "road", Dist: []int{1}, Country: c}
		if c == "-" || c == "RU" { // страна по умолчанию хранится как ""
			want.Country = ""
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("setCountry(%q) = %+v, want %+v", c, got, want)
		}
	}
	// Страна по умолчанию → конкретная и обратно.
	g := userFilters{City: "moscow"}
	if got := g.setCountry("BY"); got.Country != "BY" || got.City != "" {
		t.Errorf("setCountry(BY) из страны по умолчанию = %+v", got)
	}
	if got := g.setCountry(""); !reflect.DeepEqual(got, g) {
		t.Errorf("setCountry(\"\") из страны по умолчанию = %+v, want без изменений", got)
	}
	// Исходное значение не меняется.
	if f.Country != "BY" || f.City != "minsk" {
		t.Errorf("исходные фильтры изменены: %+v", f)
	}
	// Снять страну: возврат к стране по умолчанию, город сбрасывается.
	if got := f.unset('k'); !reflect.DeepEqual(got, userFilters{Date: "nm", Type: "road", Dist: []int{1}}) {
		t.Errorf("unset(k) = %+v", got)
	}
	// Кнопки: f:k:<значение> и f:x:k.
	for data, want := range map[string]userFilters{
		"f:k:BY": {Country: "BY"}, "f:k:*": {Country: "*"},
		"f:k:-": {City: "moscow"}, // страна по умолчанию → по умолчанию: та же страна, город остаётся
	} {
		c, err := parseCallback(data)
		if err != nil {
			t.Fatal(err)
		}
		mut := mutation(c)
		if mut == nil {
			t.Fatalf("%s: mutation = nil", data)
		}
		if got := mut(userFilters{City: "moscow"}); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", data, got, want)
		}
	}
	if c, _ := parseCallback("f:k"); mutation(c) != nil {
		t.Error("f:k без значения должна только открывать подменю")
	}
	c, _ := parseCallback("f:x:k")
	if got := mutation(c)(f); !reflect.DeepEqual(got, userFilters{Date: "nm", Type: "road", Dist: []int{1}}) {
		t.Errorf("f:x:k = %+v", got)
	}
	// JSON страны по умолчанию не отличается от старого.
	if raw, err := json.Marshal(userFilters{Country: "BY"}.setCountry("")); err != nil || string(raw) != "{}" {
		t.Errorf("json страны по умолчанию = %s, %v; want {}", raw, err)
	}
}

func TestLoadFiltersCountry(t *testing.T) {
	today := day(2026, 9, 29)
	for raw, want := range map[string]string{
		`{}`:                            "",
		`{"date":"nm","city":"moscow"}`: "", // старый JSON без country
		`{"country":""}`:                "",
		`{"country":"*"}`:               "*",
		`{"country":"BY"}`:              "BY",
		`{"country":"RU"}`:              "RU",
		`{"country":"PL"}`:              "PL",
		`{"country":"by"}`:              "",
		`{"country":"By"}`:              "",
		`{"country":"XYZ"}`:             "",
		`{"country":"B"}`:               "",
		`{"country":"ВУ"}`:              "",
		`{"country":"-"}`:               "",
		`{"country":"**"}`:              "",
		`{"country":" BY"}`:             "",
		`{"country":5}`:                 "",
		`{"country":["BY"]}`:            "",
		`{"country":null}`:              "",
	} {
		got := loadFilters([]byte(raw), today)
		if got.Country != want {
			t.Errorf("loadFilters(%s).Country = %q, want %q", raw, got.Country, want)
		}
	}
	// Остальные фильтры страной не затрагиваются; значение страны вне
	// формата не сбрасывает их.
	got := loadFilters([]byte(`{"date":"nm","city":"minsk","dist2":[1],"type":"road","country":"BY"}`), today)
	if want := (userFilters{Dates: []string{"nm"}, City: "minsk", Dist: []int{1}, Type: "road", Country: "BY"}); !reflect.DeepEqual(got, want) {
		t.Errorf("loadFilters(полный) = %+v, want %+v", got, want)
	}
	// Город не живёт при «всех странах».
	if got := loadFilters([]byte(`{"country":"*","city":"lyuban","type":"road"}`), today); !reflect.DeepEqual(got, userFilters{Country: "*", Type: "road"}) {
		t.Errorf("loadFilters(* и город) = %+v", got)
	}
	got = loadFilters([]byte(`{"date":"nm","city":"moscow","country":"XYZ"}`), today)
	if want := (userFilters{Dates: []string{"nm"}, City: "moscow"}); !reflect.DeepEqual(got, want) {
		t.Errorf("loadFilters(мусорная страна) = %+v, want %+v", got, want)
	}
}

// Страна в параметрах выдачи: "" — страна по умолчанию RU (подставляет бот),
// "*" — без ограничения по стране, код — как есть; остальные поля прежние.
func TestFilterQueryCountry(t *testing.T) {
	today := day(2026, 9, 29)
	for country, want := range map[string]string{"": "RU", "RU": "RU", "BY": "BY", "PL": "PL", "*": ""} {
		q := userFilters{Country: country, City: "minsk", Type: "trail", Dist: []int{1}}.query(today)
		if q.CountryCode != want || q.CityKey != "minsk" || q.Type != event.TypeTrail ||
			len(q.Distances) != 1 || q.Distances[0] != (event.DistanceRange{Lo: buckets[1].lo, Hi: buckets[1].hi}) {
			t.Errorf("query(country %q) = %+v, want CountryCode %q", country, q, want)
		}
	}
	// Пустые фильтры — Россия, остальное как раньше.
	if q := (userFilters{}).query(today); q.CountryCode != "RU" || q.CityKey != "" || q.Type != "" || q.Distances != nil {
		t.Errorf("query(пусто) = %+v", q)
	}
	// Старые фильтры без country и мусорное значение страны — Россия.
	for _, raw := range []string{`{}`, `{"date":"nm"}`, `{"city":"moscow"}`, `{"city":"lyuban"}`, `{"country":"XYZ"}`, `{"country":"by"}`} {
		if q := loadFilters([]byte(raw), today).query(today); q.CountryCode != "RU" {
			t.Errorf("query(%s).CountryCode = %q, want RU", raw, q.CountryCode)
		}
	}
}

func TestEffectiveCountry(t *testing.T) {
	for country, want := range map[string][2]string{
		"": {"RU", "RU"}, "RU": {"RU", "RU"}, "BY": {"BY", "BY"}, "PL": {"PL", "PL"}, "*": {"*", ""},
	} {
		f := userFilters{Country: country}
		if got := f.effectiveCountry(); got != want[0] {
			t.Errorf("effectiveCountry(%q) = %q, want %q", country, got, want[0])
		}
		if got := f.countryCode(); got != want[1] {
			t.Errorf("countryCode(%q) = %q, want %q", country, got, want[1])
		}
	}
	if defaultCountry != "RU" {
		t.Errorf("defaultCountry = %q, want RU", defaultCountry)
	}
}

// Фильтры старого пользователя (без country) загружаются и сохраняются без
// изменения filters_v; повторный выбор той же страны — тоже.
func TestLegacyFiltersKeepVersion(t *testing.T) {
	s, _, b := seeded(t)
	ctx := context.Background()
	const legacy = `{"date":"nm","city":"moscow","dist2":[1,2],"type":"road"}`
	if _, _, err := s.UpdateFilters(ctx, 42, func([]byte) ([]byte, error) { return []byte(legacy), nil }); err != nil {
		t.Fatal(err)
	}
	f0, v0 := filtersV(t, b)

	f1, v1, err := b.update(ctx, 42, func(f userFilters) userFilters { return f })
	if err != nil || v1 != v0 || !reflect.DeepEqual(f1, f0) || f1.Country != "" {
		t.Fatalf("сохранение без изменений: v %d→%d, %+v→%+v, %v", v0, v1, f0, f1, err)
	}
	// Страна по умолчанию → "-" не меняет ни JSON, ни версию.
	if _, v, err := b.update(ctx, 42, func(f userFilters) userFilters { return f.setCountry("-") }); err != nil || v != v0 {
		t.Fatalf("setCountry(-) v = %d, %v; want %d", v, err, v0)
	}
	// Конкретная страна: версия растёт, город другой страны сброшен.
	f2, v2, err := b.update(ctx, 42, func(f userFilters) userFilters { return f.setCountry("BY") })
	if err != nil || v2 != v0+1 || f2.Country != "BY" || f2.City != "" || date1(f2) != "nm" {
		t.Fatalf("setCountry(BY): v %d, %+v, %v", v2, f2, err)
	}
	// Двойное нажатие той же кнопки версию не меняет.
	if _, v3, err := b.update(ctx, 42, func(f userFilters) userFilters { return f.setCountry("BY") }); err != nil || v3 != v2 {
		t.Fatalf("повторный setCountry(BY) v = %d, %v; want %d", v3, err, v2)
	}
	// Возврат к стране по умолчанию.
	f4, v4, err := b.update(ctx, 42, func(f userFilters) userFilters { return f.unset('k') })
	if err != nil || v4 != v2+1 || f4.Country != "" {
		t.Fatalf("unset(k): v %d, %+v, %v", v4, f4, err)
	}
}

// TestLegacyFiltersTable закрепляет чтение сохранённых фильтров прежнего
// формата и байты их повторной записи: литералы записаны с кода до появления
// выбранного вида спорта и не меняются.
func TestLegacyFiltersTable(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      userFilters
		json      string
	}{
		{"пустой объект", `{}`, userFilters{}, `{}`},
		{"пресет 30", `{"date":"30"}`, userFilters{Date: "30"}, `{"date":"30"}`},
		{"одиночный месяц в date", `{"date":"m202610"}`, userFilters{Dates: []string{"m202610"}}, `{"date":"m202610"}`},
		{"несколько периодов", `{"dates":["cm","nm"]}`, userFilters{Dates: []string{"cm", "nm"}}, `{"dates":["cm","nm"]}`},
		{"город со страной", `{"city":"minsk","country":"BY"}`, userFilters{City: "minsk", Country: "BY"}, `{"city":"minsk","country":"BY"}`},
		{"все страны", `{"country":"*","date":"30","dist2":[1,2],"type":"trail"}`,
			userFilters{Date: "30", Dist: []int{1, 2}, Type: "trail", Country: "*"}, `{"date":"30","dist2":[1,2],"type":"trail","country":"*"}`},
		{"беговые корзины и тип", `{"city":"moscow","dist2":[0,3],"type":"trail"}`,
			userFilters{City: "moscow", Dist: []int{0, 3}, Type: "trail"}, `{"city":"moscow","dist2":[0,3],"type":"trail"}`},
		{"ключ прежних корзин", `{"dist":[1]}`, userFilters{}, `{}`},
		{"лишние ключи", `{"foo":1,"city":"moscow","bar":{"x":[1]}}`, userFilters{City: "moscow"}, `{"city":"moscow"}`},
		{"испорченный JSON", `{broken`, userFilters{}, `{}`},
	} {
		got := loadFilters([]byte(tc.raw), fixtureToday)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: loadFilters(%s) = %+v, want %+v", tc.name, tc.raw, got, tc.want)
		}
		if raw, err := json.Marshal(got); err != nil || string(raw) != tc.json {
			t.Errorf("%s: json = %s, %v; want %s", tc.name, raw, err, tc.json)
		}
	}
}
