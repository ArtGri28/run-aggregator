package bot

import (
	"context"
	"encoding/json"
	"reflect"
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
	for m, want := range map[int]int{5000: 0, 5500: 0, 5501: 1, 15000: 1, 21097: 2, 30000: 2,
		42000: 3, 42195: 3, 43000: 3, 43001: 4, 100000: 4} {
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
	if got, want := summaryLine(userFilters{}, ""), "Дата: все будущие · Город: вся Россия · Дистанция: любая · Тип: любой"; got != want {
		t.Fatalf("summaryLine(default) = %q, want %q", got, want)
	}
	for date, want := range map[string]string{"": "все будущие", "a": "все будущие", "30": "30 дней",
		"cm": "этот месяц", "nm": "следующий месяц", "m202611": "ноябрь 2026"} {
		if got := dateLabel(date); got != want {
			t.Errorf("dateLabel(%q) = %q, want %q", date, got, want)
		}
	}
	got := summaryLine(userFilters{Dist: []int{0, 3}, Type: "trail", City: "x"}, "<b>Тула&")
	want := "Дата: все будущие · Город: &lt;b&gt;Тула&amp; · Дистанция: до 5 км, 30–43 км, марафон · Тип: Трейл"
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
	if want := (userFilters{Date: "nm", City: "moscow", Dist: []int{0, 1}, Type: "road"}); !reflect.DeepEqual(f, want) {
		t.Fatalf("после изменений %+v, want %+v", f, want)
	}
	// Исходное значение не меняется (срез не разделяется).
	g := f.toggleBucket(4)
	if !reflect.DeepEqual(f.Dist, []int{0, 1}) || !reflect.DeepEqual(g.Dist, []int{0, 1, 4}) {
		t.Fatalf("toggleBucket: исходный %v, новый %v", f.Dist, g.Dist)
	}
	for field, want := range map[byte]userFilters{
		'd': {City: "moscow", Dist: []int{0, 1}, Type: "road"},
		'c': {Date: "nm", Dist: []int{0, 1}, Type: "road"},
		'b': {Date: "nm", City: "moscow", Type: "road"},
		't': {Date: "nm", City: "moscow", Dist: []int{0, 1}},
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

// press нажимает кнопку и возвращает единственную правку сообщения;
// на каждый callback — ровно один ответ.
func press(t *testing.T, b *Bot, api *fakeAPI, data string) call {
	t.Helper()
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
		out = append(out, bt.CallbackData)
	}
	return out
}

func filtersV(t *testing.T, b *Bot) (userFilters, int) {
	t.Helper()
	raw, v, err := b.Store.Filters(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	return loadFilters(raw), v
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
		return "Показать " + strconv.Itoa(n)
	}

	// Сводка: шесть кнопок D-10.
	c := press(t, b, api, "f:s")
	if got, want := datas(t, c), []string{"f:d", "f:c", "f:b", "f:t", "p:1:v1", "f:x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("f:s: кнопки %v, want %v", got, want)
	}

	// Город: топ по данным, «Вся Россия».
	c = press(t, b, api, "f:c")
	if bt := c.buttons(t)[0]; bt.Text != "Москва (9)" || bt.CallbackData != "f:c:moscow" {
		t.Fatalf("f:c: первая кнопка %+v, want «Москва (9)» f:c:moscow", bt)
	}
	if c.button(t, "Вся Россия") != "f:c:-" {
		t.Fatalf("f:c: нет «Вся Россия» f:c:-: %v", datas(t, c))
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
	c = press(t, b, api, "f:b")
	if got, want := datas(t, c), []string{"f:b:0", "f:b:1", "f:b:2", "f:b:3", "f:b:4", "f:b:-", "f:s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("f:b: кнопки %v, want %v", got, want)
	}
	c = press(t, b, api, "f:b:1")
	if c.button(t, "✓ ") != "f:b:1" || len(c.buttons(t)) != 7 {
		t.Fatalf("f:b:1: подменю без отметки у корзины 1: %+v", c.buttons(t))
	}
	if _, v := filtersV(t, b); v != 3 {
		t.Fatalf("filters_v после корзины = %d, want 3", v)
	}
	c = press(t, b, api, "f:s")
	if !strings.Contains(c.Form["text"], "Дистанция: 5–15 км") {
		t.Fatalf("f:s: текст %q", c.Form["text"])
	}

	// Тип: только встречающиеся (D-09).
	c = press(t, b, api, "f:t")
	var labels []string
	for _, bt := range c.buttons(t) {
		if bt.Text != "Назад" {
			labels = append(labels, bt.Text)
		}
	}
	if want := []string{"Шоссе", "Трейл", "Любой"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("f:t: кнопки %v, want %v", labels, want)
	}
	road := event.Filter{From: today, CityKey: "moscow", Type: event.TypeRoad,
		Distances: []event.DistanceRange{{Lo: 5500, Hi: 15000}}}
	c = press(t, b, api, "f:t:road")
	if bt := "p:1:v4"; c.button(t, count(road)) != bt || !strings.Contains(c.Form["text"], "Тип: Шоссе") {
		t.Fatalf("f:t:road: кнопки %v, текст %q, want %q и p:1:v4", datas(t, c), c.Form["text"], count(road))
	}

	// [Показать N]: в списке только Москва.
	c = press(t, b, api, "p:1:v4")
	rows := strings.Split(c.Form["text"], "\n")[1:]
	found := 0
	for _, l := range rows {
		if l == "" {
			continue
		}
		found++
		if !strings.Contains(l, "Москва") {
			t.Fatalf("строка списка %q не из Москвы", l)
		}
	}
	if found == 0 {
		t.Fatal("список пуст: проверка города ничего не проверила")
	}

	// Дата: пресеты и 12 месяцев; «следующий месяц» — октябрь 2026.
	c = press(t, b, api, "f:d")
	months := 0
	for _, d := range datas(t, c) {
		if strings.HasPrefix(d, "f:d:m") {
			months++
		}
	}
	for _, want := range []string{"f:d:30", "f:d:cm", "f:d:nm", "f:d:a"} {
		if !strings.Contains(strings.Join(datas(t, c), " "), want) {
			t.Fatalf("f:d: нет кнопки %s: %v", want, datas(t, c))
		}
	}
	if months != 12 {
		t.Fatalf("f:d: месяцев %d, want 12", months)
	}
	nm := road
	nm.From, nm.To = day(2026, 10, 1), day(2026, 10, 31)
	c = press(t, b, api, "f:d:nm")
	if !strings.Contains(c.Form["text"], "Дата: следующий месяц") || c.button(t, count(nm)) != "p:1:v5" {
		t.Fatalf("f:d:nm: текст %q, кнопки %v, want %q и p:1:v5", c.Form["text"], datas(t, c), count(nm))
	}
	if f, v := filtersV(t, b); v != 5 || f.Date != "nm" {
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
	if len(sends) != 1 || !strings.Contains(sends[0].Form["text"], "В каталоге нет забегов в «Новгород»") {
		t.Fatalf("«Новгород»: sendMessage = %+v", sends)
	}
	if got, want := datas(t, sends[0]), []string{"f:c:nizhniy-novgorod", "f:c:velikiy-novgorod", "f:s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("«Новгород»: кнопки %v, want %v", got, want)
	}

	// Разметка в вводе экранируется.
	api.reset()
	b.api.ProcessUpdate(ctx, textUpdate(42, "<b>Тула&"))
	sends = api.of("sendMessage")
	if len(sends) != 1 || strings.Contains(sends[0].Form["text"], "<b>") || !strings.Contains(sends[0].Form["text"], "&lt;b&gt;") {
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
	if c.button(t, "Показать 0") != "p:1:v3" {
		t.Fatalf("сводка: кнопки %v, want «Показать 0» p:1:v3", datas(t, c))
	}
	c = press(t, b, api, "p:1:v3")
	if c.Form["text"] != "Ничего не нашлось" {
		t.Fatalf("пустая выдача: текст %q", c.Form["text"])
	}
	if got, want := datas(t, c), []string{"f:x:c", "f:x:t", "f:s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("пустая выдача: кнопки %v, want %v", got, want)
	}
	c = press(t, b, api, "f:x:t")
	if !strings.Contains(c.Form["text"], "Тип: любой") || !strings.Contains(c.Form["text"], "Город: Москва") {
		t.Fatalf("f:x:t: текст %q", c.Form["text"])
	}
}

func TestResetFilters(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)

	press(t, b, api, "f:c:moscow")
	press(t, b, api, "f:b:2")
	press(t, b, api, "f:t:road")
	press(t, b, api, "f:d:30")
	_, before := filtersV(t, b)

	c := press(t, b, api, "f:x")
	if !strings.Contains(c.Form["text"], "Дата: все будущие · Город: вся Россия · Дистанция: любая · Тип: любой") {
		t.Fatalf("f:x: текст %q", c.Form["text"])
	}
	raw, v, err := s.Filters(context.Background(), 42)
	if err != nil || string(raw) != "{}" || v != before+1 {
		t.Fatalf("после сброса filters=%s v=%d err=%v; want {} и v=%d", raw, v, err, before+1)
	}
}
