package bot

import (
	"context"
	"fmt"
	"html"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

var fixtureToday = time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)

// seeded — тестовая БД с событиями Марафонца и бот на фиктивном API.
func seeded(t *testing.T, scripts ...script) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t, scripts...)
	return s, api, newTestBot(t, api, s)
}

// listRows — подписи кнопок событий списка (кнопки карточек e:).
func listRows(t *testing.T, c call) []string {
	t.Helper()
	var out []string
	for _, bt := range c.buttons(t) {
		if strings.HasPrefix(bt.CallbackData, "e:") {
			out = append(out, bt.Text)
		}
	}
	return out
}

// listText — текст списка вместе с подписями кнопок событий: всё, что видит
// пользователь на экране списка.
func listText(t *testing.T, c call) string {
	t.Helper()
	return c.Form["text"] + "\n" + strings.Join(listRows(t, c), "\n")
}

// firstCard — callback первой кнопки события в списке.
func firstCard(t *testing.T, c call) string {
	t.Helper()
	for _, bt := range c.buttons(t) {
		if strings.HasPrefix(bt.CallbackData, "e:") {
			return noV(bt.CallbackData)
		}
	}
	return ""
}

// wantRows проверяет, что кнопки страницы — последовательные элементы выдачи
// и ведут на карточки тех же событий.
func wantRows(t *testing.T, c call, items []store.ListItem, page int) {
	t.Helper()
	rows := listRows(t, c)
	if len(rows) != len(items) {
		t.Fatalf("rows = %d, want %d: %v", len(rows), len(items), rows)
	}
	for i, it := range items {
		prefix := listDate(it.StartDate, fixtureToday.Year()) + " · "
		if it.City != "" {
			prefix += it.City + " · "
		}
		if !strings.HasPrefix(rows[i], prefix) || !strings.Contains(rows[i], strings.TrimSuffix(cut(it.Title, 20), "…")) {
			t.Fatalf("row %d = %q, want %q… %s", i, rows[i], prefix, it.Title)
		}
		if got, want := c.button(t, rows[i]), fmt.Sprintf("e:%d:%d:v1", it.ID, page); got != want {
			t.Fatalf("card button %d = %q, want %q", i+1, got, want)
		}
	}
}

func allItems(t *testing.T, s *store.Store, f event.Filter) []store.ListItem {
	t.Helper()
	items, err := s.ListEvents(context.Background(), f, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestListPaging(t *testing.T) {
	s, api, b := seeded(t)
	all := allItems(t, s, event.Filter{From: fixtureToday})
	pages := (len(all) + pageSize - 1) / pageSize

	p1 := press(t, b, api, "p:1:v1")
	wantRows(t, p1, all[:pageSize], 1)
	if p1.button(t, "←") != "" {
		t.Fatalf("page 1 has ← button: %v", datas(t, p1))
	}
	next := p1.button(t, "→")
	if next != "p:2:v1" {
		t.Fatalf("→ = %q, want p:2:v1", next)
	}

	p2 := press(t, b, api, next)
	wantRows(t, p2, all[pageSize:2*pageSize], 2)
	prev := p2.button(t, "←")
	if prev != "p:1:v1" {
		t.Fatalf("← = %q, want p:1:v1", prev)
	}
	wantRows(t, press(t, b, api, prev), all[:pageSize], 1)

	last := press(t, b, api, "p:"+strconv.Itoa(pages)+":v1")
	wantRows(t, last, all[(pages-1)*pageSize:], pages)
	if last.button(t, "→") != "" {
		t.Fatalf("last page has → button: %v", datas(t, last))
	}
	if last.button(t, fmt.Sprintf("%d/%d", pages, pages)) == "" {
		t.Fatalf("last page: нет кнопки %d/%d: %v", pages, pages, last.buttons(t))
	}
	if _, v := filtersV(t, b); v != 1 {
		t.Fatalf("filters_v = %d after paging, want 1", v)
	}
}

func TestListStaleAndClamp(t *testing.T) {
	s, api, b := seeded(t)
	press(t, b, api, "f:c:moscow")
	if f, v := filtersV(t, b); v != 2 || f.City != "moscow" {
		t.Fatalf("after f:c:moscow: %+v v=%d, want moscow v=2", f, v)
	}
	msk := allItems(t, s, event.Filter{From: fixtureToday, CityKey: "moscow"})
	pages := (len(msk) + pageSize - 1) / pageSize

	stale := press(t, b, api, "p:2:v1")
	if got := api.of("answerCallbackQuery")[0].Form["text"]; got != "Фильтры изменились — показываю актуальную выдачу" {
		t.Fatalf("stale notice = %q", got)
	}
	if !strings.HasPrefix(stale.Form["text"], fmt.Sprintf("🔎 Найдено %d\n", len(msk))) || stale.button(t, fmt.Sprintf("1/%d", pages)) == "" {
		t.Fatalf("stale page: text %q buttons %v", stale.Form["text"], stale.buttons(t))
	}
	for i, r := range listRows(t, stale) {
		if !strings.Contains(r, strings.TrimSuffix(cut(msk[i].Title, 20), "…")) {
			t.Fatalf("stale page row %d = %q, want %q", i, r, msk[i].Title)
		}
	}

	clamped := press(t, b, api, "p:999:v2")
	if clamped.button(t, fmt.Sprintf("%d/%d", pages, pages)) == "" {
		t.Fatalf("clamped page: нет кнопки %d/%d: %v", pages, pages, clamped.buttons(t))
	}
	wantN := len(msk) - (pages-1)*pageSize
	if n := len(listRows(t, clamped)); n != wantN {
		t.Fatalf("clamped rows = %d, want %d", n, wantN)
	}
}

// eventWith ищет первое событие выдачи, для которого ok возвращает true.
func eventWith(t *testing.T, s *store.Store, ok func(event.Event) bool) event.Event {
	t.Helper()
	for _, it := range allItems(t, s, event.Filter{From: fixtureToday}) {
		e, err := s.GetEvent(context.Background(), it.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ok(e) {
			return e
		}
	}
	t.Fatal("нет подходящего события в фикстуре")
	return event.Event{}
}

func TestCard(t *testing.T) {
	s, api, b := seeded(t)
	ctx := context.Background()

	withReg := eventWith(t, s, func(e event.Event) bool { return validURL(e.RegistrationURL) })
	card := press(t, b, api, fmt.Sprintf("e:%d:3:v1", withReg.ID))
	text := card.Form["text"]
	srcs, err := s.EventSources(ctx, withReg.ID)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("EventSources = %+v, %v; want sources", srcs, err)
	}
	wantParts := []string{
		"<b>" + html.EscapeString(withReg.Title) + "</b>",
		strconv.Itoa(withReg.StartDate.Day()) + " " + monthsGen[withReg.StartDate.Month()-1] + " " + strconv.Itoa(withReg.StartDate.Year()),
		html.EscapeString(withReg.City),
		typeLabels[withReg.Type],
		" км",
		"🔗 Источник: ",
		`<a href="` + html.EscapeString(srcs[0].URL) + `">Марафонец</a>`,
	}
	for _, part := range wantParts {
		if !strings.Contains(text, part) {
			t.Fatalf("card lacks %q:\n%s", part, text)
		}
	}
	var reg, list string
	for _, bt := range card.buttons(t) {
		switch bt.Text {
		case "🔗 Регистрация":
			reg = bt.URL
		case backLabel:
			list = bt.CallbackData
		}
	}
	if reg != withReg.RegistrationURL || list != "p:3:v1" {
		t.Fatalf("buttons: registration=%q list=%q, want %q and p:3:v1", reg, list, withReg.RegistrationURL)
	}

	// Без ссылки регистрации: кнопки нет, источники есть; пустых полей нет.
	noReg := eventWith(t, s, func(e event.Event) bool { return e.RegistrationURL == "" })
	card = press(t, b, api, fmt.Sprintf("e:%d:1:v1", noReg.ID))
	text = card.Form["text"]
	if hasURLButton(t, card) || card.button(t, backLabel) != "p:1:v1" {
		t.Fatalf("card without registration: buttons %+v", card.buttons(t))
	}
	if !strings.Contains(text, "🔗 Источник: ") {
		t.Fatalf("card without registration lacks sources:\n%s", text)
	}
	for _, bad := range []string{"Старт в", "\n\n\n", "<b></b>"} {
		if strings.Contains(text, bad) {
			t.Fatalf("card has empty-field artifact %q:\n%s", bad, text)
		}
	}
}

func TestHiddenAndMerged(t *testing.T) {
	s, api, b := seeded(t)
	ctx := context.Background()
	f := event.Filter{From: fixtureToday}
	before := allItems(t, s, f)
	hidden, merged := before[0].ID, before[1].ID
	btnHidden, btnMerged := fmt.Sprintf("e:%d:1:v1", hidden), fmt.Sprintf("e:%d:1:v1", merged)

	storetest.HideEvent(t, hidden)
	storetest.MergeEvent(t, merged, before[2].ID)

	if n, err := s.CountEvents(ctx, f); err != nil || n != len(before)-2 {
		t.Fatalf("CountEvents = %d, %v; want %d", n, err, len(before)-2)
	}
	page := press(t, b, api, "p:1:v1")
	wantRows(t, page, allItems(t, s, f)[:pageSize], 1)
	for _, id := range []int64{hidden, merged} {
		if strings.Contains(strings.Join(datas(t, page), " "), fmt.Sprintf("e:%d:", id)) {
			t.Fatalf("list still has a button for event %d", id)
		}
	}
	if gone := press(t, b, api, btnHidden); gone.Form["text"] != "Событие больше не доступно" || gone.button(t, backLabel) != "p:1:v1" {
		t.Fatalf("%s: text %q buttons %v", btnHidden, gone.Form["text"], datas(t, gone))
	}
	// Слитое событие открывает карточку того, в которое слито (D-16).
	if card := press(t, b, api, btnMerged); !strings.Contains(card.Form["text"], "<b>"+html.EscapeString(before[2].Title)+"</b>") ||
		card.button(t, backLabel) != "p:1:v1" {
		t.Fatalf("%s: text %q buttons %v", btnMerged, card.Form["text"], datas(t, card))
	}
	// Несуществующий id — то же сообщение.
	if gone := press(t, b, api, "e:999999:1:v1"); gone.Form["text"] != "Событие больше не доступно" {
		t.Fatalf("unknown id: %q", gone.Form["text"])
	}
}

// TestMergedCardOpensRoot — tracer D-16: старая кнопка слитого события
// (цепочка из двух шагов) открывает карточку корня; скрытый корень недоступен.
func TestMergedCardOpensRoot(t *testing.T) {
	s, api, b := seeded(t)
	items := allItems(t, s, event.Filter{From: fixtureToday})
	x, y, root := items[0].ID, items[1].ID, items[2].ID
	storetest.MergeEvent(t, x, y)
	storetest.MergeEvent(t, y, root)

	want := "<b>" + html.EscapeString(items[2].Title) + "</b>"
	for _, c := range []struct{ data, list string }{
		{fmt.Sprintf("e:%d:2:v1", x), "p:2:v1"},
		{fmt.Sprintf("e:%d:1:v1", y), "p:1:v1"},
	} {
		card := press(t, b, api, c.data)
		text := card.Form["text"]
		if !strings.Contains(strings.SplitN(text, "\n", 2)[0], want) || !strings.Contains(text, "🔗 Источник") || card.button(t, backLabel) != c.list {
			t.Fatalf("%s: text %q buttons %v; want root card and %s", c.data, text, datas(t, card), c.list)
		}
	}

	storetest.HideEvent(t, root)
	if gone := press(t, b, api, fmt.Sprintf("e:%d:1:v1", x)); gone.Form["text"] != "Событие больше не доступно" {
		t.Fatalf("hidden root: %q", gone.Form["text"])
	}
	t.Logf("TRACER merged-card hops=2 root=ok hidden-root=gone")
}

func TestCardSourceNames(t *testing.T) {
	e := event.Event{Title: "Забег", StartDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Type: event.TypeRoad}
	sc := cardScreen(e, []store.EventSource{
		{Code: "marathonec", URL: "https://marathonec.ru/e/1"},
		{Code: "probeg", URL: "https://probeg.org/event/123/"},
	}, 1, 1, placePlain)
	if want := `<a href="https://marathonec.ru/e/1">Марафонец</a>, <a href="https://probeg.org/event/123/">ПроБЕГ</a>`; !strings.Contains(sc.text, want) {
		t.Fatalf("sources: want %q in\n%s", want, sc.text)
	}
	if sourceName("probeg") != "ПроБЕГ" || sourceName("heroleague") != "Лига Героев" || sourceName("zzz") != "zzz" {
		t.Fatalf("sourceName: probeg=%q zzz=%q", sourceName("probeg"), sourceName("zzz"))
	}
	sc = cardScreen(e, []store.EventSource{{Code: "probeg", URL: `https://probeg.org/event/1/"><b>x`}}, 1, 1, placePlain)
	if strings.Contains(sc.text, `"><b>x`) || !strings.Contains(sc.text, "ПроБЕГ") {
		t.Fatalf("probeg source not escaped:\n%s", sc.text)
	}
}

func TestStatusMarks(t *testing.T) {
	s, api, b := seeded(t)
	ctx := context.Background()
	f := event.Filter{From: fixtureToday}
	items := allItems(t, s, f)
	total := len(items)

	storetest.SetStatus(t, items[0].ID, event.StatusCancelled)
	storetest.SetStatus(t, items[1].ID, event.StatusPostponed)
	if n, err := s.CountEvents(ctx, f); err != nil || n != total {
		t.Fatalf("CountEvents = %d, %v; want %d (не скрыты)", n, err, total)
	}

	rows := listRows(t, press(t, b, api, "p:1:v1"))
	if !strings.Contains(rows[0], " · ❌ "+string([]rune(items[0].Title)[:8])) {
		t.Fatalf("row 1 = %q, want ❌ before title", rows[0])
	}
	if !strings.Contains(rows[1], " · ⏸ "+string([]rune(items[1].Title)[:8])) {
		t.Fatalf("row 2 = %q, want ⏸ before title", rows[1])
	}
	if strings.ContainsAny(rows[2], "❌⏸") {
		t.Fatalf("row 3 = %q, want no mark", rows[2])
	}

	for i, mark := range []string{"[ОТМЕНЁН]", "[ПЕРЕНЕСЁН]"} {
		text := press(t, b, api, fmt.Sprintf("e:%d:1:v1", items[i].ID)).Form["text"]
		if _, title, _ := strings.Cut(text, "<b>"); !strings.HasPrefix(title, mark) {
			t.Fatalf("card %d starts with %q, want %s", i, text[:20], mark)
		}
	}
}

func TestCardEscapingAndURLs(t *testing.T) {
	s, api, b := seeded(t)
	e := event.Event{
		Title: `<b>Забег "A&B"</b>`, TitleNorm: "zabeg-ab", StartDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		CountryCode: "RU", City: "Тула", CityKey: "tula", Type: event.TypeRoad, Status: event.StatusScheduled,
		Description: "<script>x</script>", RegistrationURL: "javascript:alert(1)",
		Distances: []event.Distance{{Meters: 5000, Label: "<i>x</i>"}},
	}
	if _, err := s.SaveEvent(context.Background(), &e); err != nil {
		t.Fatal(err)
	}
	card := press(t, b, api, fmt.Sprintf("e:%d:1:v1", e.ID))
	text := card.Form["text"]
	for _, raw := range []string{"<b>Забег", "<script>", "<i>x"} {
		if strings.Contains(text, raw) {
			t.Fatalf("card has raw %q:\n%s", raw, text)
		}
	}
	if !strings.Contains(text, "&lt;b&gt;") || hasURLButton(t, card) {
		t.Fatalf("card: want escaped title and no registration button:\n%s", text)
	}

	// Ссылка источника с чужой схемой — только имя, без href.
	sc := cardScreen(e, []store.EventSource{{Code: "marathonec", URL: "javascript:x"}, {Code: "new-src", URL: "https://ok.test/a?b=1&c=2"}}, 1, 1, placePlain)
	if strings.Contains(sc.text, "javascript") || !strings.Contains(sc.text, "Марафонец, ") ||
		!strings.Contains(sc.text, `<a href="https://ok.test/a?b=1&amp;c=2">new-src</a>`) {
		t.Fatalf("sources: %s", sc.text)
	}
}

func TestValidURL(t *testing.T) {
	long := "https://example.test/" + strings.Repeat("a", 2048)
	cases := map[string]bool{
		"https://example.test/reg?a=1&b=2": true,
		"http://example.test":              true,
		"HTTPS://Example.test/x":           true,
		"javascript:alert(1)":              false,
		"data:text/html,x":                 false,
		"ftp://example.test/x":             false,
		"https://":                         false,
		"https:///path":                    false,
		"//example.test/x":                 false,
		"example.test/x":                   false,
		"https://example.test/a b":         false,
		"https://example.test/a\tb":        false,
		"https://example.test/a\x00b":      false,
		"https://example.test/a\u2028b":    false,
		"":                                 false,
		long:                               false,
		long[:2048]:                        true,
	}
	for in, want := range cases {
		if got := validURL(in); got != want {
			t.Errorf("validURL(%.60q) = %v, want %v", in, got, want)
		}
	}
}

func TestLongTexts(t *testing.T) {
	long := strings.Repeat("Я", 300)
	sc := listScreen([]store.ListItem{{ID: 1, Title: long, StartDate: fixtureToday, City: "Тула"}}, 1, 1, 1, false, 2026, "")
	label := sc.kb.InlineKeyboard[0][0].Text
	if utf8.RuneCountInString(label) != maxListBtn || !strings.HasPrefix(label, "29.09 · Тула · Я") || !strings.HasSuffix(label, "…") {
		t.Fatalf("list button = %d runes %q, want %d ending with …", utf8.RuneCountInString(label), label, maxListBtn)
	}

	card := cardScreen(event.Event{Title: long, StartDate: fixtureToday, City: strings.Repeat("Г", 300), Address: long,
		Description: strings.Repeat("я", 1000), Type: event.TypeTrail, Status: event.StatusScheduled, IsListed: true,
		Distances: []event.Distance{{Meters: 21097, Label: long}}}, nil, 1, 1, placePlain)
	if n := utf8.RuneCountInString(card.text); n > 4096 {
		t.Fatalf("card text = %d runes, want <= 4096", n)
	}
}

func TestListScreenShape(t *testing.T) {
	items := []store.ListItem{
		{ID: 11, Title: "Раз", StartDate: fixtureToday, City: "Тула", Distances: []int{5000, 21097}},
		{ID: 12, Title: "Два", StartDate: fixtureToday, Status: event.StatusCancelled},
	}
	sc := listScreen(items, 12, 2, 4, false, 2026, "")
	var got []string
	for _, r := range sc.kb.InlineKeyboard {
		for _, bt := range r {
			got = append(got, bt.Text+"="+bt.CallbackData)
		}
	}
	// f:s — без версии (v0): её проставляет stamp при показе экрана.
	want := []string{"29.09 · Тула · Раз=e:11:2:v4", "29.09 · ❌ Два=e:12:2:v4", "←=p:1:v4", "2/2=p:2:v4", "⬅️ Назад=f:s:v0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buttons = %v, want %v", got, want)
	}
	if sc.text != "🔎 Найдено 12\nвсе будущие забеги" {
		t.Fatalf("text = %q", sc.text)
	}
	if sc := listScreen(items, 12, 1, 4, false, 2026, "Трейл · Тула"); sc.text != "🔎 Найдено 12\nТрейл · Тула" {
		t.Fatalf("text with filters = %q", sc.text)
	}
}

func TestInaccessibleMessage(t *testing.T) {
	s := storetest.Open(t)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)

	u := &models.Update{ID: 3, CallbackQuery: &models.CallbackQuery{
		ID: "cqx", From: models.User{ID: 42}, Data: "p:1:v1",
		Message: models.MaybeInaccessibleMessage{
			Type: models.MaybeInaccessibleMessageTypeInaccessibleMessage,
			InaccessibleMessage: &models.InaccessibleMessage{
				Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, MessageID: 9},
		},
	}}
	b.api.ProcessUpdate(context.Background(), u)
	edits := api.of("editMessageText")
	if len(edits) != 1 || edits[0].Form["chat_id"] != "42" || edits[0].Form["message_id"] != "9" {
		t.Fatalf("editMessageText = %+v, want one for chat 42 message 9", edits)
	}
	if n := len(api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("answerCallbackQuery = %d, want 1", n)
	}
}

// T-04-17: на каждый callback ровно один ответ, даже когда Telegram
// отвечает ошибкой «query is too old».
func TestAnswerOncePerCallback(t *testing.T) {
	old := only("answerCallbackQuery", func(int) string {
		return apiErr(400, "Bad Request: query is too old and response timeout expired or query ID is invalid", 0)
	})
	s, api, b := seeded(t, old)
	id := allItems(t, s, event.Filter{From: fixtureToday})[0].ID

	data := []string{"f:s", "p:1:v1", "p:2:v1", fmt.Sprintf("e:%d:1:v1", id), "zzz", "p:abc"}
	for i, d := range data {
		b.api.ProcessUpdate(context.Background(), cbUpdate(42, 100+i, d))
	}
	if n := len(api.of("answerCallbackQuery")); n != len(data) {
		t.Fatalf("answerCallbackQuery = %d, want %d", n, len(data))
	}
	seen := map[string]int{}
	for _, c := range api.of("answerCallbackQuery") {
		seen[c.Form["callback_query_id"]]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("callback %s answered %d times", id, n)
		}
	}
}
