package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/tristyle"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Сценарии контракта экранов Phase 13 (13-UX-CONTRACT.md, раздел 9): лыжные
// экраны на сохранённых страницах Tristyle. Лыжи включаются в справочнике
// только на время теста; эталоны testdata/screens/s36_*…s43_* написаны вручную
// по контракту.

const tristyleData = "../ingest/sources/tristyle/testdata/"

// skiPages — события сохранённого лыжного списка Tristyle в порядке страницы.
var skiPages = []string{"1178", "817", "818", "819", "820"}

// skiFixture отдаёт сохранённые лыжные страницы Tristyle без сети.
type skiFixture struct{ docs []ingest.RawDoc }

func (skiFixture) Code() string          { return tristyle.Code }
func (skiFixture) Policy() ingest.Policy { return tristyle.New(nil).Policy() }
func (skiFixture) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	return tristyle.Parse(doc)
}
func (s skiFixture) Fetch(context.Context) ([]ingest.RawDoc, error) { return s.docs, nil }

// seedSki загружает пять лыжных событий Tristyle тем же путём, что
// seedMarathonec: первый прогон и его принятие. Документ события помечен
// видом спорта так же, как это делает адаптер.
func seedSki(t *testing.T, s *store.Store) {
	t.Helper()
	page := func(name string) []byte {
		body, err := os.ReadFile(tristyleData + name)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	src := skiFixture{docs: []ingest.RawDoc{{URL: tristyle.SkiListURL, Body: page("list-skiing.html"), FetchedAt: fixtureNow}}}
	for _, id := range skiPages {
		src.docs = append(src.docs, ingest.RawDoc{
			URL:  "https://tristyle.club/competition/" + id + "/#" + string(event.SportSkiing),
			Body: page("event-" + id + ".html"), FetchedAt: fixtureNow,
		})
	}
	ctx := context.Background()
	r := &ingest.Runner{Store: s, Now: func() time.Time { return fixtureNow }}
	if _, err := r.Run(ctx, src); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res, err := r.Accept(ctx, src, 0); err != nil || res.Created != len(skiPages) {
		t.Fatalf("Accept: создано %d, %v; want %d", res.Created, err, len(skiPages))
	}
}

// skiBot — каталог сценариев контракта: 133 беговых события Марафонца (Россия)
// и пять лыжных Tristyle (Беларусь), «сегодня» 2026-09-29; лыжи включены на
// время теста.
func skiBot(t *testing.T) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	s := storetest.Open(t)
	seedMarathonec(t, s)
	seedSki(t, s)
	storetest.EnableSport(t, event.SportSkiing)
	api := fakeTelegram(t)
	return s, api, newTestBot(t, api, s)
}

// skiID — id лыжного события по номеру страницы Tristyle.
func skiID(t *testing.T, page string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(dbScalar(t, `SELECT event_id::text FROM source_events WHERE url = $1`,
		"https://tristyle.club/competition/"+page+"/"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// stored — users.filters пользователя 42 и версия как есть в базе, без записи.
func stored(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT filters::text || ' v' || filters_v FROM users WHERE tg_id = 42`)
}

// wantStored сверяет users.filters с JSON want (порядок ключей не важен) и
// версию.
func wantStored(t *testing.T, step, want string, v int) {
	t.Helper()
	raw, gotV, _ := strings.Cut(stored(t), " v")
	var g, w any
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) || gotV != strconv.Itoa(v) {
		t.Errorf("%s: filters = %s v%s, want %s v%d", step, raw, gotV, want, v)
	}
}

// at дописывает кнопке текущую версию фильтров (кнопкам p:, e: и g: её не
// проставляет fresh).
func at(t *testing.T, data string) string {
	t.Helper()
	_, v, _ := strings.Cut(stored(t), " v")
	return data + ":v" + v
}

// staleToast: кнопка отвечает «Кнопка устарела», сообщение не меняется, база
// прежняя.
func staleToast(t *testing.T, b *Bot, api *fakeAPI, data string) call {
	t.Helper()
	before := dbState(t)
	c := onlyToast(t, data, raw(b, api, 42, data))
	if c.Form["text"] != staleNotice {
		t.Errorf("%s: уведомление %q, want %q", data, c.Form["text"], staleNotice)
	}
	if dbState(t) != before {
		t.Errorf("%s: устаревшая кнопка записала в базу", data)
	}
	return c
}

// TestSkiBuckets — корзины лыж: сплошные, граница входит в верхнюю корзину
// (сравнение — как в запросе выдачи: lo < м <= hi).
func TestSkiBuckets(t *testing.T) {
	in := func(m int) []int {
		var out []int
		for i, bk := range skiBuckets {
			if m > bk.lo && m <= bk.hi {
				out = append(out, i)
			}
		}
		return out
	}
	for m := 1; m <= 200000; m++ {
		if len(in(m)) != 1 {
			t.Fatalf("%d м попадает в корзины %v, want ровно одну", m, in(m))
		}
	}
	for m, want := range map[int]int{1: 0, 5000: 0, 14999: 0, 15000: 1, 29999: 1, 30000: 2, 49900: 2, 49999: 2, 50000: 3, 70000: 3} {
		if got := in(m); !reflect.DeepEqual(got, []int{want}) {
			t.Errorf("%d м: корзины %v, want %d", m, got, want)
		}
	}
	if len(skiBuckets) != len(buckets) {
		t.Errorf("корзин лыж %d, бега %d: кодек f:n принимает номера одного набора", len(skiBuckets), len(buckets))
	}
}

// TestSkiTracer — сквозной путь: кнопка вида спорта → users.filters → отбор в
// запросах выдачи → лыжная сводка → список → карточка → возврат к бегу.
func TestSkiTracer(t *testing.T) {
	s, api, b := skiBot(t)
	ctx := context.Background()
	setUserFilters(t, s, `{"country":"*"}`)
	ski := allItems(t, s, event.Filter{From: fixtureToday, Sport: event.SportSkiing})
	running, err := s.CountEvents(ctx, event.Filter{From: fixtureToday, Sport: event.SportRunning})
	if err != nil || len(ski) != len(skiPages) || running == 0 {
		t.Fatalf("фикстуры: лыжных %d, беговых %d, %v", len(ski), running, err)
	}

	sum := press(t, b, api, "f:a:skiing")
	wantStored(t, "f:a:skiing", `{"country":"*","sport":"skiing"}`, 3)
	if !strings.HasPrefix(sum.Form["text"], "⛷ <b>Лыжи: поиск старта</b>\n") || showCount(t, sum) != len(ski) || strings.Contains(sum.Form["text"], "забег") {
		t.Fatalf("лыжная сводка: %q, кнопки %v", sum.Form["text"], sum.buttons(t))
	}

	list := press(t, b, api, sum.button(t, "🔎 Показать"))
	if !strings.HasPrefix(list.Form["text"], fmt.Sprintf("⛷ <b>Лыжи — найдено: %d</b>\n", len(ski))) {
		t.Fatalf("лыжный список: %q", list.Form["text"])
	}
	var cards []string
	for _, d := range datas(t, list) {
		if strings.HasPrefix(d, "e:") {
			cards = append(cards, d)
		}
	}
	for i, it := range ski {
		if i >= len(cards) || cards[i] != fmt.Sprintf("e:%d:1:v3", it.ID) {
			t.Fatalf("кнопки списка %v, want события %v", cards, listIDsOf(ski))
		}
	}
	if len(cards) != len(ski) {
		t.Fatalf("в списке %d событий, want %d", len(cards), len(ski))
	}

	card := press(t, b, api, cards[1])
	if !strings.HasPrefix(card.Form["text"], "⛷ <b>") || !strings.Contains(card.Form["text"], "\n⛷ Лыжи · Лыжные гонки\n") {
		t.Fatalf("лыжная карточка: %q", card.Form["text"])
	}

	back := press(t, b, api, "f:a:running")
	wantStored(t, "f:a:running", `{"country":"*"}`, 4)
	if !strings.HasPrefix(back.Form["text"], "🔎 <b>Поиск забега</b>\n") || showCount(t, back) != running {
		t.Fatalf("беговая сводка: %q, кнопки %v; want %d забегов", back.Form["text"], back.buttons(t), running)
	}
	if !t.Failed() {
		t.Logf("TRACER sport=skiing summary=%d list=%d card=ski running=%d", showCount(t, sum), len(cards), running)
	}
}

func listIDsOf(items []store.ListItem) []int64 {
	var ids []int64
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// TestCardUnknownSport — событие с кодом вида спорта, которого нет в
// справочнике, не показывается: его нет в списке и в результатах запроса,
// карточка по прямой кнопке — «Событие больше не доступно», без паники.
func TestCardUnknownSport(t *testing.T) {
	s, api, b := seeded(t)
	items := allItems(t, s, event.Filter{From: fixtureToday})
	it := items[0]
	card := fmt.Sprintf("e:%d:1:v1", it.ID)
	if c := press(t, b, api, card); !strings.Contains(c.Form["text"], "<b>") {
		t.Fatalf("фикстура: карточка события %d не открылась: %q", it.ID, c.Form["text"])
	}
	storetest.SetSport(t, it.ID, "chess")

	gone := press(t, b, api, card)
	if gone.Form["text"] != goneNotice || toast(api) != "" || len(gone.buttons(t)) != 1 {
		t.Errorf("карточка события вне справочника: %q, уведомление %q, кнопки %v", gone.Form["text"], toast(api), datas(t, gone))
	}
	fromSearch := pressOn(t, b, api, say(t, b, api, "забег"), fmt.Sprintf("g:%d:v1", it.ID))
	if !strings.HasPrefix(fromSearch.Form["text"], goneNotice) || toast(api) != "" {
		t.Errorf("карточка из результатов: %q, уведомление %q", fromSearch.Form["text"], toast(api))
	}
	if left := allItems(t, s, event.Filter{From: fixtureToday}); len(left) != len(items)-1 || slices.Contains(listIDsOf(left), it.ID) {
		t.Errorf("в выдаче %d событий (было %d), событие вне справочника видно: %v", len(left), len(items), slices.Contains(listIDsOf(left), it.ID))
	}
	// Карточка небегового события с дисциплиной вне перечня строится без паники.
	sc := cardScreen(event.Event{Title: "Старт", StartDate: fixtureToday, Sport: "chess", Type: "blitz"}, nil, 1, 1, placePlain)
	if !strings.Contains(sc.text, "Старт") {
		t.Errorf("cardScreen(вид спорта вне справочника) = %q", sc.text)
	}
}

// skiRows — подписи кнопок событий списка, по одной на строку.
func skiRows(t *testing.T, c call) string {
	t.Helper()
	return strings.Join(listRows(t, c), "\n")
}

// TestS36SportMenu — S-36: главное меню с кнопками видов спорта по две в ряд;
// кнопка вида спорта есть, только если у него есть будущие события, бег — всегда.
func TestS36SportMenu(t *testing.T) {
	s, api, b := skiBot(t)
	ctx := context.Background()
	startSnapless(t, b, api) // первый /start — предложение анкеты, дальше — меню

	// 1. /start: меню новым сообщением, два вида спорта в одном ряду; фильтры
	// и версия прежние.
	before := stored(t)
	startSnap(t, b, api, "s36_menu")
	menu := api.of("sendMessage")[0]
	if got := stored(t); got != before || got != "{} v1" {
		t.Errorf("шаг 1: /start изменил фильтры: %s → %s", before, got)
	}
	if strings.Contains(menu.Form["text"], "забег") || menu.button(t, "🔎") != "" {
		t.Errorf("шаг 1: в меню слово «забег» или прежняя кнопка поиска:\n%s", renderCall(t, menu))
	}

	// 2. «m»: то же меню правкой сообщения, записей нет.
	state := dbState(t)
	matchSnapshot(t, "s36_menu_back", edited(t, "m", raw(b, api, 42, "m")))
	if dbState(t) != state {
		t.Error("шаг 2: кнопка меню записала в базу")
	}

	// 3. Бот с группой поддержки: четвёртая строка — поддержка.
	startSnap(t, newSupportBot(t, api, s), api, "s36_support")

	// 4. Будущих лыжных событий нет: одна кнопка вида спорта.
	b.Now = func() time.Time { return time.Date(2027, 3, 2, 12, 0, 0, 0, time.UTC) }
	startSnap(t, b, api, "s36_one_sport")
	b.Now = func() time.Time { return fixtureNow }

	// 6–7. Три и четыре вида спорта — по две кнопки в ряд. Дополнительные виды
	// спорта включены только на время теста, у каждого одно будущее событие.
	for _, step := range []struct {
		sport event.Sport
		snap  string
	}{{event.SportSwimming, "s36_three"}, {event.SportCycling, "s36_four"}} {
		storetest.EnableSport(t, step.sport)
		e := &event.Event{Title: "Проба " + string(step.sport), TitleNorm: "проба " + string(step.sport), StartDate: fixtureToday.AddDate(0, 0, 5),
			CountryCode: "RU", Type: event.TypeOther, Sport: step.sport, Status: event.StatusScheduled, Distances: []event.Distance{{Meters: 1000}}}
		if _, err := s.SaveEvent(ctx, e); err != nil {
			t.Fatalf("SaveEvent: %v", err)
		}
		startSnap(t, b, api, step.snap)
	}

	// 8. Кнопки видов спорта: код и порядок — из справочника, длина — в
	// пределах callback_data.
	four := api.of("sendMessage")[0]
	checkCallbackLengths(t, four)
	want := []string{}
	for _, sp := range event.Sports {
		if sp.Enabled {
			want = append(want, "f:a:"+string(sp.Code))
			if got := buttonText(t, four, want[len(want)-1]); got != sp.Emoji+" "+sp.Title {
				t.Errorf("шаг 8: подпись кнопки %s = %q", sp.Code, got)
			}
		}
	}
	if got := datas(t, four); len(want) != 4 || !reflect.DeepEqual(got, append(want, "w:o", "s:o", "u:o")) {
		t.Errorf("шаг 8: кнопки %v, want виды спорта %v, избранное, настройки и профиль", got, want)
	}

	// Кнопка вида спорта открывает его сводку.
	if c := press(t, b, api, menu.button(t, "⛷ Лыжи")); !strings.HasPrefix(c.Form["text"], "⛷ <b>Лыжи: поиск старта</b>") {
		t.Errorf("«Лыжи» из меню: %q", c.Form["text"])
	}
	press(t, b, api, "m")
	if c := press(t, b, api, menu.button(t, "🏃 Бег")); !strings.HasPrefix(c.Form["text"], "🔎 <b>Поиск забега</b>") {
		t.Errorf("«Бег» из меню: %q", c.Form["text"])
	}

	// Ошибка счётчика не оставляет без меню: один бег, ошибка в логе.
	logs := captureLogs(t)
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if got := b.menuSports(dead); len(got) != 1 || got[0].Code != event.SportRunning || !strings.Contains(logs.String(), "menu sports") {
		t.Errorf("меню при ошибке запроса: %+v, лог %q", got, logs.String())
	}

	// 5. Каталог пуст: кнопка «Бег» остаётся, включённых видов спорта без
	// событий в меню нет.
	b2 := newTestBot(t, api, storetest.Open(t))
	startSnapless(t, b2, api)
	startSnap(t, b2, api, "s36_empty_catalog")
}

// TestS37SkiSearch — S-37: вход кнопкой вида спорта, лыжная сводка,
// переключение бег ↔ лыжи с общими страной и датой.
func TestS37SkiSearch(t *testing.T) {
	s, api, b := skiBot(t)
	setUserFilters(t, s, `{}`)

	// 1. «⛷ Лыжи»: сводка лыж правкой, один пустой ответ.
	c := pressSnap(t, b, api, "f:a:skiing", "s37_summary")
	wantStored(t, "шаг 1", `{"sport":"skiing"}`, 2)
	if toast(api) != "" || strings.Contains(renderCall(t, c), "забег") {
		t.Errorf("шаг 1: уведомление %q, экран\n%s", toast(api), renderCall(t, c))
	}

	// 2. «⬅️ Назад»: главное меню с двумя кнопками и новой версией; фильтры прежние.
	pressSnap(t, b, api, c.button(t, backLabel), "s37_menu")
	wantStored(t, "шаг 2", `{"sport":"skiing"}`, 2)

	// 3. «🏃 Бег»: прежняя беговая сводка, ключа sport нет.
	pressSnap(t, b, api, "f:a:running", "s37_running")
	wantStored(t, "шаг 3", `{}`, 3)

	// 4. Лыжи → меню страны по лыжному каталогу → Беларусь.
	press(t, b, api, "f:a:skiing")
	pressSnap(t, b, api, "f:k", "s37_country_menu")
	pressSnap(t, b, api, "f:k:BY", "s37_summary_by")
	wantStored(t, "шаг 4", `{"country":"BY","sport":"skiing"}`, 5)

	// 5. Дата «Декабрь 2026» и возврат к сводке.
	menu := press(t, b, api, "f:d")
	press(t, b, api, menu.button(t, "Декабрь 2026"))
	pressSnap(t, b, api, menu.button(t, backLabel), "s37_summary_dec")
	wantStored(t, "шаг 5", `{"date":"m202612","country":"BY","sport":"skiing"}`, 6)

	// 6. Меню → «🏃 Бег»: страна и дата из лыж стоят в беговой сводке.
	press(t, b, api, "m")
	pressSnap(t, b, api, "f:a:running", "s37_running_shared")
	wantStored(t, "шаг 6", `{"date":"m202612","country":"BY"}`, 7)

	// 7. Повторное «🏃 Бег»: тот же экран, JSON и версия прежние.
	before := stored(t)
	pressSnap(t, b, api, "f:a:running", "s37_running_shared")
	if got := stored(t); got != before {
		t.Errorf("шаг 7: повторное нажатие изменило фильтры: %s → %s", before, got)
	}
}

// TestS38SkiDistance — S-38: меню корзин лыж со счётчиками, граница в верхней
// корзине, корзина без событий.
func TestS38SkiDistance(t *testing.T) {
	s, api, b := skiBot(t)
	ctx := context.Background()
	setUserFilters(t, s, `{"country":"BY","sport":"skiing"}`)

	// 1–4. Меню, одна корзина, две, сводка.
	menu := pressSnap(t, b, api, "f:n", "s38_menu")
	pressSnap(t, b, api, "f:n:1", "s38_one")
	wantStored(t, "шаг 2", `{"country":"BY","sport":"skiing","sports":{"skiing":{"dist":[1]}}}`, 3)
	pressSnap(t, b, api, "f:n:2", "s38_two")
	wantStored(t, "шаг 3", `{"country":"BY","sport":"skiing","sports":{"skiing":{"dist":[1,2]}}}`, 4)
	pressSnap(t, b, api, menu.button(t, backLabel), "s38_summary")

	// 5. Границы: «VIII этап…» (20 и 30 км) входит в «30–50 км» дистанцией ровно 30 км.
	setUserFilters(t, s, `{"country":"BY","sport":"skiing","sports":{"skiing":{"dist":[2]}}}`)
	pressSnap(t, b, api, at(t, "p:1"), "s38_boundary_list")

	// 6. Выбранная корзина без событий остаётся в меню с нулём.
	setUserFilters(t, s, `{"date":"m202612","country":"BY","sport":"skiing","sports":{"skiing":{"dist":[2]}}}`)
	pressSnap(t, b, api, "f:n", "s38_selected_empty")

	// 7. «Любая»: отметки сняты, ключ sports исчез.
	pressSnap(t, b, api, "f:n:-", "s38_cleared")
	wantStored(t, "шаг 7", `{"date":"m202612","country":"BY","sport":"skiing"}`, 7)

	// 8. Беговые корзины не затронуты.
	setUserFilters(t, s, `{"country":"BY","dist2":[0,3],"sport":"skiing"}`)
	for _, data := range []string{"f:n:1", "f:n:2", "f:n:-"} {
		press(t, b, api, data)
	}
	wantStored(t, "шаг 8", `{"country":"BY","dist2":[0,3],"sport":"skiing"}`, 11)
	// Кнопки беговых корзин при выбранном беге прежние.
	if c := press(t, b, api, "f:a:running"); !strings.Contains(c.Form["text"], "📏 Дистанция: 🟢 До 10 км, 🔴 42+ км") {
		t.Errorf("шаг 8: беговая сводка: %q", c.Form["text"])
	}
	if got, want := datas(t, press(t, b, api, "f:n")), []string{"f:n:0", "f:n:1", "f:n:2", "f:n:3", "f:n:-", "f:s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("шаг 8: беговое меню корзин %v, want %v", got, want)
	}

	// 5 (продолжение). «До 15 км»: три этапа, лыжероллерного марафона (15, 30
	// и 45 км) нет.
	setUserFilters(t, s, `{"country":"BY","sport":"skiing","sports":{"skiing":{"dist":[0]}}}`)
	low := press(t, b, api, at(t, "p:1"))
	if rows := skiRows(t, low); len(listRows(t, low)) != 3 || strings.Contains(rows, "Лыжероллерный") ||
		!strings.Contains(rows, " V этап") || !strings.Contains(rows, " VI этап") || !strings.Contains(rows, " VII этап") {
		t.Errorf("шаг 5: «До 15 км»:\n%s", rows)
	}

	// События теста ровно на 50 км и на 49,9 км.
	save := func(title string, meters int) int64 {
		t.Helper()
		e := &event.Event{Title: title, TitleNorm: strings.ToLower(title), StartDate: fixtureToday.AddDate(0, 0, 5), CountryCode: "BY",
			Type: event.TypeSki, Sport: event.SportSkiing, Status: event.StatusScheduled, Distances: []event.Distance{{Meters: meters}}}
		if _, err := s.SaveEvent(ctx, e); err != nil {
			t.Fatalf("SaveEvent: %v", err)
		}
		return e.ID
	}
	fifty, under := save("Проба ровно 50", 50000), save("Проба 49,9", 49900)
	in := func(bucket int) []int64 {
		t.Helper()
		setUserFilters(t, s, fmt.Sprintf(`{"country":"BY","sport":"skiing","sports":{"skiing":{"dist":[%d]}}}`, bucket))
		f, _ := filtersV(t, b)
		return listIDsOf(allItems(t, s, f.query(fixtureToday)))
	}
	if top, mid := in(3), in(2); !reflect.DeepEqual(top, []int64{fifty}) || slices.Contains(mid, fifty) || !slices.Contains(mid, under) {
		t.Errorf("шаг 5: «50+ км» %v (want только %d), «30–50 км» %v (want с %d, без %d)", top, fifty, mid, under, fifty)
	}
	if c := press(t, b, api, "f:n"); !strings.Contains(strings.Join(datas(t, c), " "), "f:n:3") {
		t.Errorf("шаг 5: корзины «50+ км» с событием нет в меню: %v", datas(t, c))
	}
}

// TestS39SkiDiscipline — S-39: меню дисциплин лыж только из встречающихся.
func TestS39SkiDiscipline(t *testing.T) {
	s, api, b := skiBot(t)
	setUserFilters(t, s, `{"country":"BY","sport":"skiing"}`)

	pressSnap(t, b, api, "f:t", "s39_menu")
	pressSnap(t, b, api, "f:t:rollerski", "s39_summary_roller")
	wantStored(t, "шаг 2", `{"country":"BY","sport":"skiing","sports":{"skiing":{"type":"rollerski"}}}`, 3)
	pressSnap(t, b, api, "f:t", "s39_menu_checked")
	pressSnap(t, b, api, "f:t:-", "s39_summary_any")
	wantStored(t, "шаг 4", `{"country":"BY","sport":"skiing"}`, 4)

	// 5. Лыжероллерное событие в прошлом: одна дисциплина.
	b.Now = func() time.Time { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) }
	pressSnap(t, b, api, "f:t", "s39_menu_one")
	b.Now = func() time.Time { return fixtureNow }

	// 6. Страна без лыжных событий: только «Любая».
	setUserFilters(t, s, `{"sport":"skiing"}`)
	pressSnap(t, b, api, "f:t", "s39_menu_none")

	// 7. Дисциплина другого вида спорта — устаревшая кнопка; беговой тип цел.
	staleToast(t, b, api, fresh(t, b, "f:t:road"))
	setUserFilters(t, s, `{}`)
	staleToast(t, b, api, fresh(t, b, "f:t:ski"))
	setUserFilters(t, s, `{"country":"BY","type":"trail","sport":"skiing"}`)
	press(t, b, api, "f:t:rollerski")
	press(t, b, api, "f:t:-")
	wantStored(t, "шаг 7", `{"country":"BY","type":"trail","sport":"skiing"}`, 9)
}

// TestS40SkiListCard — S-40: список, карточки, пустая выдача, «все страны».
func TestS40SkiListCard(t *testing.T) {
	s, api, b := skiBot(t)
	ctx := context.Background()
	setUserFilters(t, s, `{"country":"BY","sport":"skiing"}`)
	card := func(page string) string { return fmt.Sprintf("e:%d:1", skiID(t, page)) }

	// 1–4, 10. Список, карточки событий 817, 1178 и 818, возврат.
	pressSnap(t, b, api, at(t, "p:1"), "s40_list")
	c := pressSnap(t, b, api, at(t, card("817")), "s40_card")
	pressSnap(t, b, api, c.button(t, backLabel), "s40_list")
	pressSnap(t, b, api, at(t, card("1178")), "s40_card_roller")
	pressSnap(t, b, api, at(t, card("818")), "s40_card_tbd")

	// 5. Страна без лыжных событий.
	setUserFilters(t, s, `{"sport":"skiing"}`)
	pressSnap(t, b, api, at(t, "p:1"), "s40_empty_country")

	// 6. Пустая выдача с фильтрами; «Снять дисциплину».
	setUserFilters(t, s, `{"date":"m202612","country":"BY","sport":"skiing","sports":{"skiing":{"type":"rollerski"}}}`)
	empty := pressSnap(t, b, api, at(t, "p:1"), "s40_empty_hints")
	pressSnap(t, b, api, empty.button(t, "Снять дисциплину"), "s40_after_unset")
	wantStored(t, "шаг 6", `{"date":"m202612","country":"BY","sport":"skiing"}`, 5)

	// 7. «Все страны»: флаг перед городом, страна в карточке.
	setUserFilters(t, s, `{"country":"*","sport":"skiing"}`)
	all := pressSnap(t, b, api, at(t, "p:1"), "s40_list_all")
	pressSnap(t, b, api, at(t, card("817")), "s40_card_all")

	// 9. В лыжной выдаче только лыжные события, в беговой — ни одного лыжного.
	skiIDs := dbScalar(t, `SELECT string_agg(id::text, ',' ORDER BY start_date, id) FROM events WHERE sport = 'skiing'`)
	var got []string
	for _, d := range datas(t, all) {
		if id, ok := strings.CutPrefix(d, "e:"); ok {
			got = append(got, strings.Split(id, ":")[0])
		}
	}
	if strings.Join(got, ",") != skiIDs {
		t.Errorf("шаг 9: лыжный список %v, в базе лыжных %s", got, skiIDs)
	}
	setUserFilters(t, s, `{"country":"*"}`)
	f, _ := filtersV(t, b)
	running := allItems(t, s, f.query(fixtureToday))
	n, err := s.CountEvents(ctx, f.query(fixtureToday))
	if want := dbScalar(t, `SELECT count(*)::text FROM events WHERE sport = 'running'`); err != nil || strconv.Itoa(n) != want || strconv.Itoa(len(running)) != want {
		t.Errorf("шаг 9: беговых в выдаче %d, в счётчике %d, %v; в базе %s", len(running), n, err, want)
	}
	for _, it := range running {
		if it.Sport != event.SportRunning {
			t.Errorf("шаг 9: в беговой выдаче событие %d вида спорта %s", it.ID, it.Sport)
		}
	}
	if c := press(t, b, api, at(t, "p:1")); !strings.HasPrefix(c.Form["text"], "🔎 <b>Найдено: "+strconv.Itoa(n)+"</b>") {
		t.Errorf("шаг 9: беговой список: %q", c.Form["text"])
	}

	// 8. Карточка лыжного события при выбранном беге — в лыжном виде; «Назад»
	// ведёт в беговую выдачу. Скрытое событие недоступно.
	old := press(t, b, api, at(t, card("817")))
	if !strings.HasPrefix(old.Form["text"], "⛷ <b>V этап") || !strings.Contains(old.Form["text"], "⛷ Лыжи · Лыжные гонки") {
		t.Errorf("шаг 8: карточка при выбранном беге: %q", old.Form["text"])
	}
	if back := press(t, b, api, old.button(t, backLabel)); !strings.HasPrefix(back.Form["text"], "🔎 <b>Найдено: ") {
		t.Errorf("шаг 8: «Назад» открыл не беговую выдачу: %q", back.Form["text"])
	}
	storetest.HideEvent(t, skiID(t, "817"))
	if gone := press(t, b, api, at(t, card("817"))); gone.Form["text"] != goneNotice {
		t.Errorf("шаг 8: скрытое событие: %q", gone.Form["text"])
	}
}

// legacyFilters — сохранённые фильтры прежнего формата (S-41, шаг 1).
var legacyFilters = []string{
	`{}`, `{"date":"30"}`, `{"date":"m202610"}`, `{"dates":["cm","nm"]}`,
	`{"city":"moscow","dist2":[0,3],"type":"trail"}`, `{"country":"*","date":"30","dist2":[1,2],"type":"trail"}`,
	`{"country":"BY"}`, `{"dist":[1]}`,
}

// TestS41FiltersCompat — S-41: прежние фильтры читаются без потерь, лыжные
// фильтры хранятся отдельно от беговых, сброс оставляет вид спорта.
func TestS41FiltersCompat(t *testing.T) {
	s, api, b := skiBot(t)
	view := func(c call) string { return versionRE.ReplaceAllString(renderCall(t, c), "") }
	strip := func(sc string) string {
		return regexp.MustCompile(`:v[0-9]+`).ReplaceAllString(sc, "")
	}

	// 1. «🏃 Бег» у прежнего пользователя: тот же экран, что у прежней кнопки
	// поиска; JSON и версия прежние.
	for _, legacy := range legacyFilters {
		setUserFilters(t, s, legacy)
		startSnapless(t, b, api)
		before := stored(t)
		old := strip(view(press(t, b, api, "f:s")))
		got := strip(view(press(t, b, api, "f:a:running")))
		if got != old || !strings.Contains(got, "🔎 <b>Поиск забега</b>") {
			t.Errorf("шаг 1, %s: «Бег» —\n%s\nпрежняя кнопка —\n%s", legacy, got, old)
		}
		if after := stored(t); after != before {
			t.Errorf("шаг 1, %s: «Бег» изменил фильтры: %s → %s", legacy, before, after)
		}
	}

	// 2. В лыжи и обратно без лыжных фильтров: JSON прежний, версия +2. Ключ
	// прежних корзин dist не читается и уходит при первом изменении, как раньше.
	for _, legacy := range legacyFilters {
		if legacy == `{"dist":[1]}` {
			continue
		}
		setUserFilters(t, s, legacy)
		raw, v, _ := strings.Cut(stored(t), " v")
		v0, _ := strconv.Atoi(v)
		press(t, b, api, "f:a:skiing")
		press(t, b, api, "m")
		press(t, b, api, "f:a:running")
		if want := raw + " v" + strconv.Itoa(v0+2); stored(t) != want {
			t.Errorf("шаг 2, %s: после лыж и обратно %s, want %s", legacy, stored(t), want)
		}
	}

	// 3–8 — на чистой базе: версии в эталонах.
	s, api, b = skiBot(t)
	const own = `{"city":"moscow","dist2":[0,3],"type":"trail"}`
	setUserFilters(t, s, own)

	// 3. Беговые дистанция и тип в лыжах не действуют, город общий.
	pressSnap(t, b, api, "f:a:skiing", "s41_ski_own")

	// 4. Лыжная корзина хранится отдельно; беговые фильтры после возврата прежние.
	press(t, b, api, "f:n:0")
	wantStored(t, "шаг 4", `{"city":"moscow","dist2":[0,3],"type":"trail","sport":"skiing","sports":{"skiing":{"dist":[0]}}}`, 4)
	press(t, b, api, "m")
	pressSnap(t, b, api, "f:a:running", "s41_running_kept")
	wantStored(t, "шаг 4, бег", `{"city":"moscow","dist2":[0,3],"type":"trail","sports":{"skiing":{"dist":[0]}}}`, 5)
	// Беговые изменения не меняют лыжные ключи.
	if f, _ := filtersV(t, b); !reflect.DeepEqual(f.toggleBucket(1).setType("road").Sports, f.Sports) {
		t.Errorf("шаг 4: беговое изменение затронуло лыжные фильтры: %+v", f)
	}

	// 5. Сброс в лыжах: всё снято, вид спорта остался.
	sum := press(t, b, api, "f:a:skiing")
	ask := pressSnap(t, b, api, sum.button(t, "🔄 Сбросить"), "s41_reset_ask")
	done := press(t, b, api, ask.button(t, "✅ Да, сбросить"))
	matchSnapshot(t, "s41_reset_done", renderCall(t, done)+renderCall(t, api.of("answerCallbackQuery")[0]))
	wantStored(t, "шаг 5", `{"sport":"skiing"}`, 7)

	// 6. Сброс в беге того же JSON.
	setUserFilters(t, s, own)
	press(t, b, api, "s:y:f")
	wantStored(t, "шаг 6", `{}`, 9)

	// 7. Испорченные значения читаются терпимо, JSON при чтении не переписывается.
	const broken = `{"sport":"skiing","sports":{"skiing":{"dist":[7,-1,0,0],"type":"road"},"chess":{"dist":[1]}}}`
	setUserFilters(t, s, broken)
	before := stored(t)
	if c := press(t, b, api, "f:s"); !strings.Contains(c.Form["text"], "📏 Дистанция: 🟢 До 15 км\n⛷ Дисциплина: Любая") {
		t.Errorf("шаг 7: сводка: %q", c.Form["text"])
	}
	if got := stored(t); got != before {
		t.Errorf("шаг 7: чтение переписало JSON: %s → %s", before, got)
	}

	// 8. «Мои настройки» при выбранных лыжах; при беге — прежний экран.
	setUserFilters(t, s, `{"country":"BY","sport":"skiing","sports":{"skiing":{"type":"ski"}}}`)
	pressSnap(t, b, api, "s:o", "s41_settings_ski")
	setUserFilters(t, s, `{}`)
	// Под экраном настроек — пояснение подписки (Phase 20); остальное прежнее.
	noSub := func(c call) string { return strings.TrimSuffix(c.Form["text"], "\n\n"+subOffAbout) }
	if c := press(t, b, api, "s:o"); noSub(c) != settingsScreen(userFilters{}, "").text || strings.Contains(c.Form["text"], "Вид спорта") ||
		!strings.HasSuffix(noSub(c), "показываются все будущие забеги") {
		t.Errorf("шаг 8: настройки при беге: %q", c.Form["text"])
	}
	setUserFilters(t, s, `{"sport":"skiing"}`)
	if c := press(t, b, api, "s:o"); !strings.HasSuffix(noSub(c), "🔎 Фильтры не заданы — показываются все будущие старты") {
		t.Errorf("шаг 8: настройки лыж без фильтров: %q", c.Form["text"])
	}
}

// startSnapless отправляет /start и проверяет, что пришло одно сообщение.
func startSnapless(t *testing.T, b *Bot, api *fakeAPI) {
	t.Helper()
	api.reset()
	b.api.ProcessUpdate(context.Background(), startUpdate(42))
	if n := len(api.of("sendMessage")); n != 1 {
		t.Fatalf("/start: sendMessage = %d, want 1", n)
	}
}

// TestS42MixedSearch — S-42: текстовый запрос общий по видам спорта;
// заголовок результатов и ответы на текст без запроса нейтральные.
func TestS42MixedSearch(t *testing.T) {
	s, api, b := skiBot(t)
	logs := captureLogs(t)

	// 1–2. «Все страны»: беговое событие и четыре лыжных со значком; карточка
	// лыжного события и возврат к результатам.
	setUserFilters(t, s, `{"country":"*"}`)
	before := stored(t)
	textSnap(t, b, api, "этап", "s42_mixed")
	results := api.of("sendMessage")[0]
	card := pressOn(t, b, api, results, at(t, fmt.Sprintf("g:%d", skiID(t, "817"))))
	matchSnapshot(t, "s42_card", renderCall(t, card))
	matchSnapshot(t, "s42_back", renderCall(t, pressOn(t, b, api, card, card.button(t, backLabel))))
	if got := stored(t); got != before {
		t.Errorf("шаг 1: запрос изменил фильтры: %s → %s", before, got)
	}

	// 3. Найдены только беговые события: заголовок тот же.
	setUserFilters(t, s, `{}`)
	textSnap(t, b, api, "этап", "s42_running_only")

	// 4. Выбраны лыжи: запрос по-прежнему общий, фильтры и вид спорта не меняет.
	setUserFilters(t, s, `{"country":"BY","sport":"skiing"}`)
	before = stored(t)
	textSnap(t, b, api, "Виктория", "s42_ski_only")
	if got := stored(t); got != before {
		t.Errorf("шаг 4: запрос изменил фильтры: %s → %s", before, got)
	}

	// 5. Точное название города выбирает общий город; сводка — выбранного вида спорта.
	textSnap(t, b, api, "Минск", "s42_city")
	wantStored(t, "шаг 5", `{"city":"minsk","country":"BY","sport":"skiing"}`, 5)

	// 6. Ответы на команду и на текст без слов одинаковы при лыжах и при беге.
	for _, filters := range []string{`{"country":"BY","sport":"skiing"}`, `{}`} {
		setUserFilters(t, s, filters)
		if c := say(t, b, api, "/команда"); c.Form["text"] != "Нажмите /start, чтобы найти старт" {
			t.Errorf("шаг 6, %s: ответ на команду %q", filters, c.Form["text"])
		}
		if c := say(t, b, api, "!!!"); c.Form["text"] != "Не понял запрос — напишите название старта или города" {
			t.Errorf("шаг 6, %s: ответ на текст без слов %q", filters, c.Form["text"])
		}
	}

	// 7. Текст запроса в лог не пишется.
	if out := logs.String(); strings.Contains(out, "этап") || strings.Contains(out, "Виктория") || strings.Contains(out, "Минск") {
		t.Errorf("шаг 7: текст запроса в логе:\n%s", out)
	}
}

// TestS43SportStale — S-43: устаревшие кнопки вида спорта и выключенный вид
// спорта (на мультиспорте; в шаге 7 — лыжи, выключенные на время теста).
func TestS43SportStale(t *testing.T) {
	s, api, b := skiBot(t)
	logs := captureLogs(t)
	setUserFilters(t, s, `{}`)
	press(t, b, api, "f:c:moscow")
	press(t, b, api, "f:c:-") // версия 3: кнопка с версией 2 — из старого сообщения

	// 1. Кнопка вида спорта с другой версией.
	matchSnapshot(t, "s43_stale", renderCall(t, staleToast(t, b, api, "f:a:skiing:v2")))

	// 2. Код вне справочника, пустой, в другом регистре, без версии.
	for _, data := range []string{"f:a:chess:v3", "f:a::v3", "f:a:SKIING:v3", "f:a:skiing"} {
		staleToast(t, b, api, data)
	}

	// 3. Выключенный вид спорта.
	staleToast(t, b, api, "f:a:multisport:v3")
	wantStored(t, "шаги 1–3", `{}`, 3)

	// 4. Будущих лыжных событий уже нет: сводка с нулём, в меню кнопки лыж нет.
	b.Now = func() time.Time { return time.Date(2027, 3, 2, 12, 0, 0, 0, time.UTC) }
	pressSnap(t, b, api, "f:a:skiing", "s43_no_events")
	wantStored(t, "шаг 4", `{"sport":"skiing"}`, 4)
	if got, want := datas(t, press(t, b, api, "m")), []string{"f:a:running", "w:o", "s:o", "u:o"}; !reflect.DeepEqual(got, want) {
		t.Errorf("шаг 4: кнопки меню %v, want %v", got, want)
	}
	b.Now = func() time.Time { return fixtureNow }

	// 5. Кнопка поиска из сообщения, отправленного до выката (f:s): у
	// пользователя с лыжами — лыжная сводка, без ключа sport — беговая; при
	// другой версии — тот же экран без записи.
	if c := press(t, b, api, "f:s"); !strings.HasPrefix(c.Form["text"], "⛷ <b>Лыжи: поиск старта</b>") {
		t.Errorf("шаг 5: f:s при лыжах: %q", c.Form["text"])
	}
	setUserFilters(t, s, `{}`)
	fresh := pressSnap(t, b, api, "f:s", "s43_legacy_search")
	state := dbState(t)
	if old := edited(t, "f:s:v1", raw(b, api, 42, "f:s:v1")); !strings.HasPrefix(old, renderCall(t, fresh)) {
		t.Errorf("шаг 5: f:s с другой версией:\n%s", old)
	}
	if dbState(t) != state {
		t.Error("шаг 5: f:s с другой версией записала в базу")
	}

	// 6. Сохранённый вид спорта вне справочника или выключенный читается как бег.
	for _, filters := range []string{`{"sport":"chess"}`, `{"sport":"multisport"}`} {
		setUserFilters(t, s, filters)
		before := stored(t)
		if c := press(t, b, api, "f:s"); !strings.HasPrefix(c.Form["text"], "🔎 <b>Поиск забега</b>") || showCount(t, c) != showCount(t, fresh) {
			t.Errorf("шаг 6, %s: %q", filters, c.Form["text"])
		}
		if got := stored(t); got != before {
			t.Errorf("шаг 6, %s: чтение переписало JSON: %s → %s", filters, before, got)
		}
	}

	// 7. Лыжи выключены в справочнике (на время теста): меню с одной кнопкой,
	// сохранённый вид спорта читается как бег, кнопка лыж устарела, карточка
	// лыжного события недоступна, текстовый запрос лыжных событий не находит.
	setUserFilters(t, s, `{"country":"*","sport":"skiing"}`)
	storetest.DisableSport(t, event.SportSkiing)
	off := stored(t)
	pressSnap(t, b, api, "m", "s43_disabled_menu")
	if c := press(t, b, api, "f:s"); !strings.HasPrefix(c.Form["text"], "🔎 <b>Поиск забега</b>") {
		t.Errorf("шаг 7: сводка при выключенных лыжах: %q", c.Form["text"])
	}
	staleToast(t, b, api, at(t, "f:a:skiing"))
	if c := press(t, b, api, at(t, fmt.Sprintf("e:%d:1", skiID(t, "817")))); c.Form["text"] != goneNotice {
		t.Errorf("шаг 7: карточка лыжного события: %q", c.Form["text"])
	}
	if c := say(t, b, api, "этап"); !strings.Contains(c.Form["text"], "Старты по запросу: 1<") || strings.Contains(renderCall(t, c), "⛷") {
		t.Errorf("шаг 7: текстовый запрос при выключенных лыжах:\n%s", renderCall(t, c))
	}
	if got := stored(t); got != off {
		t.Errorf("шаг 7: фильтры изменились: %s → %s", off, got)
	}

	// 8. В логе — только вид кнопки.
	if out := logs.String(); !strings.Contains(out, "callback stale") || !strings.Contains(out, "prefix=f") ||
		strings.Contains(out, "skiing") || strings.Contains(out, "multisport") || strings.Contains(out, "chess") {
		t.Errorf("шаг 8: лог:\n%s", out)
	}
}
