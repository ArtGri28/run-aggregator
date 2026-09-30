package bot

import (
	"context"
	"fmt"
	"html"
	"reflect"
	"regexp"
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

var listRowRE = regexp.MustCompile(`^\d\. `)

// listRows — строки событий списка.
func listRows(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if listRowRE.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

// wantRows проверяет, что строки страницы — последовательные элементы выдачи,
// а кнопки карточек ведут на те же события.
func wantRows(t *testing.T, c call, items []store.ListItem, page int) {
	t.Helper()
	rows := listRows(c.Form["text"])
	if len(rows) != len(items) {
		t.Fatalf("rows = %d, want %d:\n%s", len(rows), len(items), c.Form["text"])
	}
	for i, it := range items {
		prefix := fmt.Sprintf("%d. %s · %s · ", i+1, it.StartDate.Format("02.01"), html.EscapeString(it.City))
		if !strings.HasPrefix(rows[i], prefix) || !strings.Contains(rows[i], html.EscapeString(it.Title)) {
			t.Fatalf("row %d = %q, want %q… %s", i, rows[i], prefix, it.Title)
		}
		if got, want := c.button(t, strconv.Itoa(i+1)), fmt.Sprintf("e:%d:%d:v1", it.ID, page); got != want {
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
	pages := (len(all) + 4) / 5

	p1 := press(t, b, api, "p:1:v1")
	wantRows(t, p1, all[:5], 1)
	if p1.button(t, "←") != "" {
		t.Fatalf("page 1 has ← button: %v", datas(t, p1))
	}
	next := p1.button(t, "→")
	if next != "p:2:v1" {
		t.Fatalf("→ = %q, want p:2:v1", next)
	}

	p2 := press(t, b, api, next)
	wantRows(t, p2, all[5:10], 2)
	prev := p2.button(t, "←")
	if prev != "p:1:v1" {
		t.Fatalf("← = %q, want p:1:v1", prev)
	}
	wantRows(t, press(t, b, api, prev), all[:5], 1)

	last := press(t, b, api, "p:"+strconv.Itoa(pages)+":v1")
	wantRows(t, last, all[(pages-1)*5:], pages)
	if last.button(t, "→") != "" {
		t.Fatalf("last page has → button: %v", datas(t, last))
	}
	if !strings.Contains(last.Form["text"], fmt.Sprintf("страница %d/%d", pages, pages)) {
		t.Fatalf("last page header: %q", last.Form["text"])
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
	pages := (len(msk) + 4) / 5

	stale := press(t, b, api, "p:2:v1")
	if got := api.of("answerCallbackQuery")[0].Form["text"]; got != "Фильтры изменились — показываю актуальную выдачу" {
		t.Fatalf("stale notice = %q", got)
	}
	if !strings.Contains(stale.Form["text"], fmt.Sprintf("Найдено %d · страница 1/%d", len(msk), pages)) {
		t.Fatalf("stale page header: %q", stale.Form["text"])
	}
	for i, r := range listRows(stale.Form["text"]) {
		if !strings.Contains(r, msk[i].Title) {
			t.Fatalf("stale page row %d = %q, want %q", i, r, msk[i].Title)
		}
	}

	clamped := press(t, b, api, "p:999:v2")
	if !strings.Contains(clamped.Form["text"], fmt.Sprintf("страница %d/%d", pages, pages)) {
		t.Fatalf("clamped page header: %q", clamped.Form["text"])
	}
	wantN := len(msk) - (pages-1)*5
	if n := len(listRows(clamped.Form["text"])); n != wantN {
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
		"Источники: ",
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
		case "Регистрация":
			reg = bt.URL
		case "К списку":
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
	if hasURLButton(t, card) || card.button(t, "К списку") != "p:1:v1" {
		t.Fatalf("card without registration: buttons %+v", card.buttons(t))
	}
	if !strings.Contains(text, "Источники: ") {
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
	wantRows(t, page, allItems(t, s, f)[:5], 1)
	for _, id := range []int64{hidden, merged} {
		if strings.Contains(strings.Join(datas(t, page), " "), fmt.Sprintf("e:%d:", id)) {
			t.Fatalf("list still has a button for event %d", id)
		}
	}
	for _, data := range []string{btnHidden, btnMerged} {
		gone := press(t, b, api, data)
		if gone.Form["text"] != "Событие больше не доступно" || gone.button(t, "К списку") != "p:1:v1" {
			t.Fatalf("%s: text %q buttons %v", data, gone.Form["text"], datas(t, gone))
		}
	}
	// Несуществующий id — то же сообщение.
	if gone := press(t, b, api, "e:999999:1:v1"); gone.Form["text"] != "Событие больше не доступно" {
		t.Fatalf("unknown id: %q", gone.Form["text"])
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

	rows := listRows(press(t, b, api, "p:1:v1").Form["text"])
	if !strings.Contains(rows[0], " · [ОТМЕНЁН] "+html.EscapeString(items[0].Title)) {
		t.Fatalf("row 1 = %q, want [ОТМЕНЁН] before title", rows[0])
	}
	if !strings.Contains(rows[1], " · [ПЕРЕНЕСЁН] "+html.EscapeString(items[1].Title)) {
		t.Fatalf("row 2 = %q, want [ПЕРЕНЕСЁН] before title", rows[1])
	}
	if strings.Contains(rows[2], "[") {
		t.Fatalf("row 3 = %q, want no mark", rows[2])
	}

	for i, mark := range []string{"[ОТМЕНЁН]", "[ПЕРЕНЕСЁН]"} {
		text := press(t, b, api, fmt.Sprintf("e:%d:1:v1", items[i].ID)).Form["text"]
		if !strings.HasPrefix(strings.TrimPrefix(text, "<b>"), mark) {
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
	sc := cardScreen(e, []store.EventSource{{Code: "marathonec", URL: "javascript:x"}, {Code: "new-src", URL: "https://ok.test/a?b=1&c=2"}}, 1, 1)
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
	sc := listScreen([]store.ListItem{{ID: 1, Title: long, StartDate: fixtureToday, City: "Тула"}}, 1, 1, 1)
	row := listRows(sc.text)[0]
	title := strings.TrimPrefix(row, "1. 29.09 · Тула · ")
	if utf8.RuneCountInString(title) != maxListTitle || !strings.HasSuffix(title, "…") {
		t.Fatalf("list title = %d runes %q, want %d ending with …", utf8.RuneCountInString(title), title, maxListTitle)
	}

	card := cardScreen(event.Event{Title: long, StartDate: fixtureToday, City: strings.Repeat("Г", 300), Address: long,
		Description: strings.Repeat("я", 1000), Type: event.TypeTrail, Status: event.StatusScheduled, IsListed: true,
		Distances: []event.Distance{{Meters: 21097, Label: long}}}, nil, 1, 1)
	if n := utf8.RuneCountInString(card.text); n > 4096 {
		t.Fatalf("card text = %d runes, want <= 4096", n)
	}
}

func TestListScreenShape(t *testing.T) {
	items := []store.ListItem{
		{ID: 11, Title: "Раз", StartDate: fixtureToday, City: "Тула", Distances: []int{5000, 21097}},
		{ID: 12, Title: "Два", StartDate: fixtureToday, Status: event.StatusCancelled},
	}
	sc := listScreen(items, 12, 2, 4)
	var got []string
	for _, r := range sc.kb.InlineKeyboard {
		for _, bt := range r {
			got = append(got, bt.Text+"="+bt.CallbackData)
		}
	}
	want := []string{"1=e:11:2:v4", "2=e:12:2:v4", "←=p:1:v4", "стр. 2/3=p:2:v4", "→=p:3:v4", "Фильтры=f:s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buttons = %v, want %v", got, want)
	}
	rows := listRows(sc.text)
	if rows[0] != "1. 29.09 · Тула · Раз · 5/21,1 км" || rows[1] != "2. 29.09 · — · [ОТМЕНЁН] Два" {
		t.Fatalf("rows = %q", rows)
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
