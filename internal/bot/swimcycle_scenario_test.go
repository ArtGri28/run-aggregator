package bot

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/myrace"
	"aggregator/internal/ingest/sources/tristyle"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Сценарии контракта экранов Phase 18 (18-UX-CONTRACT.md, раздел 7):
// плавание и велоспорт на сохранённых страницах Tristyle и образце MyRace.
// Виды спорта включаются в справочнике на время теста; эталоны
// testdata/screens/s57_*…s61_* написаны вручную по контракту.

// swimCyclePages — страницы плавания и велоспорта Tristyle с видом спорта; 1163
// (Стамбул, Турция) записи не даёт.
var swimCyclePages = []struct {
	id    string
	sport event.Sport
}{
	{"1098", event.SportSwimming}, {"1172", event.SportSwimming}, {"1179", event.SportSwimming}, {"1163", event.SportSwimming},
	{"1099", event.SportCycling}, {"1214", event.SportCycling}, {"673", event.SportCycling}, {"674", event.SportCycling},
}

// docsSource отдаёт сохранённые документы источника code без сети.
type docsSource struct {
	code  string
	docs  []ingest.RawDoc
	parse func(ingest.RawDoc) ([]ingest.SourceEvent, error)
	pol   ingest.Policy
}

func (s docsSource) Code() string                                        { return s.code }
func (s docsSource) Policy() ingest.Policy                               { return s.pol }
func (s docsSource) Parse(d ingest.RawDoc) ([]ingest.SourceEvent, error) { return s.parse(d) }
func (s docsSource) Fetch(context.Context) ([]ingest.RawDoc, error)      { return s.docs, nil }

// swimCycleBot — каталог сценариев контракта Phase 18: 133 беговых события
// Марафонца, лыжи, плавание и велоспорт Tristyle одним прогоном и событие
// MyRace; «сегодня» 2026-09-29; лыжи, плавание и велоспорт включены на время
// теста.
func swimCycleBot(t *testing.T) (*store.Store, *fakeAPI, *Bot) {
	t.Helper()
	read := func(path string) []byte {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	ts := docsSource{code: tristyle.Code, parse: tristyle.Parse, pol: tristyle.New(nil).Policy(), docs: []ingest.RawDoc{
		{URL: tristyle.SkiListURL, Body: read(tristyleData + "list-skiing.html"), FetchedAt: fixtureNow},
		{URL: tristyle.SwimListURL, Body: read(tristyleData + "list-swimming.html"), FetchedAt: fixtureNow},
		{URL: tristyle.CycleListURL, Body: read(tristyleData + "list-cycling.html"), FetchedAt: fixtureNow},
	}}
	for _, id := range skiPages {
		ts.docs = append(ts.docs, ingest.RawDoc{URL: "https://tristyle.club/competition/" + id + "/#" + string(event.SportSkiing),
			Body: read(tristyleData + "event-" + id + ".html"), FetchedAt: fixtureNow})
	}
	for _, p := range swimCyclePages {
		ts.docs = append(ts.docs, ingest.RawDoc{URL: "https://tristyle.club/competition/" + p.id + "/#" + string(p.sport),
			Body: read(tristyleData + "event-" + p.id + ".html"), FetchedAt: fixtureNow})
	}
	mr := docsSource{code: myrace.Code, parse: myrace.Parse, pol: myrace.New(nil, "").Policy(), docs: []ingest.RawDoc{
		{URL: myrace.ListURL, Body: read("../ingest/sources/myrace/testdata/calendar-cycling.json"), FetchedAt: fixtureNow},
	}}
	s := storetest.Open(t)
	seedMarathonec(t, s)
	ctx := context.Background()
	r := &ingest.Runner{Store: s, Now: func() time.Time { return fixtureNow }}
	for _, c := range []struct {
		src  docsSource
		want int
	}{{ts, len(skiPages) + 7}, {mr, 1}} {
		if _, err := r.Run(ctx, c.src); err != nil {
			t.Fatalf("Run %s: %v", c.src.code, err)
		}
		if res, err := r.Accept(ctx, c.src, 0); err != nil || res.Created != c.want {
			t.Fatalf("Accept %s: создано %d, %v; want %d", c.src.code, res.Created, err, c.want)
		}
	}
	for _, sp := range []event.Sport{event.SportSkiing, event.SportSwimming, event.SportCycling} {
		storetest.EnableSport(t, sp)
	}
	api := fakeTelegram(t)
	return s, api, newTestBot(t, api, s)
}

// eventID — id события по адресу записи источника.
func eventID(t *testing.T, url string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(dbScalar(t, `SELECT event_id::text FROM source_events WHERE url = $1`, url), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func cardData(t *testing.T, url string) string { return fmt.Sprintf("e:%d:1", eventID(t, url)) }

// TestS57FourSports — S-57: главное меню с четырьмя видами спорта; без будущих
// велостартов — три.
func TestS57FourSports(t *testing.T) {
	_, api, b := swimCycleBot(t)
	startSnapless(t, b, api)
	startSnap(t, b, api, "s57_menu")

	b.Now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }
	startSnap(t, b, api, "s57_menu_no_cycle")
	if got := datas(t, api.of("sendMessage")[0]); strings.Contains(strings.Join(got, " "), "f:a:cycling") {
		t.Errorf("шаг 2: кнопка велоспорта без будущих стартов: %v", got)
	}
}

// TestS58Swim — S-58: сводка, страна, дистанция, дисциплина, список, карточка
// и пустая выдача плавания.
func TestS58Swim(t *testing.T) {
	s, api, b := swimCycleBot(t)
	setUserFilters(t, s, `{}`)

	pressSnap(t, b, api, "f:a:swimming", "s58_summary")
	pressSnap(t, b, api, "f:k:BY", "s58_summary_by")
	wantStored(t, "шаг 2", `{"country":"BY","sport":"swimming"}`, 3)
	pressSnap(t, b, api, "f:n", "s58_dist")
	pressSnap(t, b, api, "f:t", "s58_type")
	press(t, b, api, "f:t:pool")
	wantStored(t, "шаг 4", `{"country":"BY","sport":"swimming","sports":{"swimming":{"type":"pool"}}}`, 4)

	pressSnap(t, b, api, at(t, "p:1"), "s58_list")
	pressSnap(t, b, api, at(t, cardData(t, "https://tristyle.club/competition/1179/")), "s58_card")

	setUserFilters(t, s, `{"date":"m202701","country":"BY","sport":"swimming","sports":{"swimming":{"type":"pool"}}}`)
	pressSnap(t, b, api, at(t, "p:1"), "s58_empty")
}

// TestS59Cycle — S-59: сводка, корзины, карточки MyRace и Tristyle, дисциплина
// и список велоспорта.
func TestS59Cycle(t *testing.T) {
	s, api, b := swimCycleBot(t)
	setUserFilters(t, s, `{}`)

	pressSnap(t, b, api, "f:a:cycling", "s59_summary")
	pressSnap(t, b, api, "f:n", "s59_dist")
	press(t, b, api, "f:n:1")
	wantStored(t, "шаг 2", `{"sport":"cycling","sports":{"cycling":{"dist":[1]}}}`, 3)
	pressSnap(t, b, api, at(t, cardData(t, "https://myrace.info/events/1907")), "s59_card_myrace")

	setUserFilters(t, s, `{"country":"BY","sport":"cycling"}`)
	pressSnap(t, b, api, "f:t", "s59_type")
	pressSnap(t, b, api, at(t, "p:1"), "s59_list")
	pressSnap(t, b, api, at(t, cardData(t, "https://tristyle.club/competition/674/")), "s59_card")
}

// TestS60SwimCycleMixed — S-60: текстовый запрос по всем видам спорта; фильтры
// велоспорта хранятся отдельно от бега и лыж.
func TestS60SwimCycleMixed(t *testing.T) {
	s, api, b := swimCycleBot(t)
	setUserFilters(t, s, `{"country":"*"}`)
	matchSnapshot(t, "s60_search", renderCall(t, say(t, b, api, "Брест")))

	setUserFilters(t, s, `{"country":"BY","dist2":[0],"type":"trail","sport":"skiing","sports":{"skiing":{"type":"ski"}}}`)
	press(t, b, api, "f:a:cycling")
	press(t, b, api, "f:n:1")
	wantStored(t, "шаг 2", `{"country":"BY","dist2":[0],"type":"trail","sport":"cycling","sports":{"skiing":{"type":"ski"},"cycling":{"dist":[1]}}}`, 5)
	if c := press(t, b, api, "f:a:skiing"); !strings.Contains(c.Form["text"], "⛷ Дисциплина: Лыжные гонки") {
		t.Errorf("шаг 2: лыжная сводка: %q", c.Form["text"])
	}
	if c := press(t, b, api, "f:a:running"); !strings.Contains(c.Form["text"], "📏 Дистанция: 🟢 До 10 км") {
		t.Errorf("шаг 2: беговая сводка: %q", c.Form["text"])
	}
}

// TestS61SwimCycleDisabled — S-61: плавание и велоспорт выключены — в меню
// их нет, кнопки устарели, база не меняется.
func TestS61SwimCycleDisabled(t *testing.T) {
	s, api, b := swimCycleBot(t)
	storetest.DisableSport(t, event.SportSwimming)
	storetest.DisableSport(t, event.SportCycling)
	startSnapless(t, b, api)
	startSnap(t, b, api, "s61_menu")
	if got, want := datas(t, api.of("sendMessage")[0]), []string{"f:a:running", "f:a:skiing", "w:o", "s:o", "u:o"}; !reflect.DeepEqual(got, want) {
		t.Errorf("кнопки меню %v, want %v", got, want)
	}
	setUserFilters(t, s, `{}`)
	for _, data := range []string{"f:a:swimming", "f:a:cycling"} {
		staleToast(t, b, api, at(t, data))
	}
}
