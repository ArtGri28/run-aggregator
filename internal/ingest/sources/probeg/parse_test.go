package probeg

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"aggregator/internal/ingest"
)

// fixtureNow — время загрузки образцов (testdata/README.md).
var fixtureNow = time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

func parseFile(t *testing.T, name string) []ingest.SourceEvent {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Parse(ingest.RawDoc{URL: CalendarURL, Body: body, FetchedAt: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func byID(t *testing.T, recs []ingest.SourceEvent, id string) ingest.SourceEvent {
	t.Helper()
	for _, r := range recs {
		if r.ExternalID == id {
			return r
		}
	}
	t.Fatalf("нет записи %s", id)
	return ingest.SourceEvent{}
}

// Страница 6 целиком — по таблице фактов testdata/README.md.
func TestParseGolden(t *testing.T) {
	recs := parseFile(t, "year_ahead-2026-11-08-p6.html")
	b, err := os.ReadFile("testdata/year_ahead-2026-11-08-p6.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []ingest.SourceEvent
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recs, want) {
		g, _ := json.MarshalIndent(recs, "", "  ")
		t.Fatalf("записи не совпадают с эталоном:\n%s", g)
	}
}

// Страницы 1 и 2: число записей, паркраны, отменённый старт, Беларусь.
func TestParseFixtures(t *testing.T) {
	tests := []struct {
		file          string
		cards, weekly int
	}{
		{"year_ahead-2026-11-08-p1.html", 46, 2 * (40 + 11)},
		{"year_ahead-2026-11-08-p2.html", 49 - 1, (40 + 11) + (39 + 11)},
	}
	var all []ingest.SourceEvent
	for _, tt := range tests {
		recs := parseFile(t, tt.file)
		weekly := 0
		for _, r := range recs {
			if r.GroupKey != "" {
				weekly++
			}
		}
		if len(recs)-weekly != tt.cards || weekly != tt.weekly {
			t.Errorf("%s: карточек %d, паркранов %d; want %d, %d", tt.file, len(recs)-weekly, weekly, tt.cards, tt.weekly)
		}
		all = append(all, recs...)
	}
	for _, r := range all {
		if r.ExternalID == "73219" {
			t.Errorf("отменённый старт 73219 дал запись")
		}
	}
	got := byID(t, all, "78465")
	want := ingest.SourceEvent{
		ExternalID: "78465", Title: "XVII Гатчинский международный полумарафон", URL: "https://probeg.org/event/78465/",
		Date: "08.11.2026", StartTime: "10:30", City: "Гатчина", Region: "Ленинградская область", Country: "Россия",
		Distances: "21098 м, 10 км, 2 км", RegistrationURL: "https://reg.russiarunning.com/event/GatchinaHalfMarathon2026",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("78465 = %+v\nwant %+v", got, want)
	}
	if r := byID(t, all, "85887"); r.City != "Минск" || r.Country != "Беларусь" || r.Region != "" {
		t.Errorf("85887 Минск = %+v", r)
	}
	// Паркран Москвы 14 ноября и Петербурга 5 декабря (переход месяца).
	if r := byID(t, all, "85818"); r.Date != "14.11.2026" || r.City != "Москва" || r.Distances != "5 км" || r.GroupKey == "" {
		t.Errorf("паркран 85818 = %+v", r)
	}
	n := 0
	for _, r := range all {
		if r.GroupKey != "" && r.Date == "05.12.2026" {
			n++
		}
	}
	if n != 39+11 {
		t.Errorf("паркранов 05.12.2026: %d, want 50", n)
	}

	prep, st := ingest.Prepare(all, fixtureNow, New(nil).Policy())
	if st.Rejected != 0 || st.UnrecognizedRegion != 0 || st.OutOfScope != 0 || len(prep) == 0 {
		t.Fatalf("Prepare: %+v", st)
	}
}

func TestParseWhen(t *testing.T) {
	tests := []struct{ in, date, end, tm string }{
		{"8 ноября 2026, 10:30", "08.11.2026", "", "10:30"},
		{"14 ноября 2026", "14.11.2026", "", ""},
		{"4–6 сентября 2027, 09:00", "04.09.2027", "06.09.2027", "09:00"},
		{"30 ноября – 2 декабря 2026", "30.11.2026", "02.12.2026", ""},
		{"30 декабря 2026 – 2 января 2027, 9:00", "30.12.2026", "02.01.2027", "9:00"},
		{"31 февраля 2027", "31 февраля 2027", "", ""},
		{"5 брюмера 2027", "5 брюмера 2027", "", ""},
		{"6–4 сентября 2027", "6–4 сентября 2027", "", ""},
		{"", "", "", ""},
	}
	for _, tt := range tests {
		date, end, tm, _ := parseWhen(tt.in)
		if date != tt.date || end != tt.end || tm != tt.tm {
			t.Errorf("parseWhen(%q) = %q, %q, %q; want %q, %q, %q", tt.in, date, end, tm, tt.date, tt.end, tt.tm)
		}
	}
}

func card(class, id, when, meta, badges, links string) string {
	return `<article class="glass calendar-item` + class + `"><div class="calendar-body">` +
		`<h3><a href="/event/` + id + `/">Старт ` + id + `</a></h3>` +
		`<div class="when-line">` + when + `</div>` +
		`<div class="muted small meta">` + meta + `</div>` +
		`<div class="planned">` + badges + `<a class="badge done" href="/event/` + id + `/">Планируют 3 человека</a></div>` +
		`<div class="links">` + links + `</div></div></article>`
}

func weekly(day, where string, ids ...string) string {
	li := ""
	for _, id := range ids {
		li += `<li><a href="/event/` + id + `/">5 вёрст ` + id + `</a></li>`
	}
	return `<details class="glass weekly"><summary><span class="calendar-date"><span class="num">` + day +
		`</span></span><span class="calendar-body"><h3>Паркраны и похожие забеги ` + where + `</h3></span></summary><ul>` + li + `</ul></details>`
}

func parseHTML(t *testing.T, blocks ...string) []ingest.SourceEvent {
	t.Helper()
	recs, err := Parse(ingest.RawDoc{URL: CalendarURL, Body: []byte(`<main>` + strings.Join(blocks, "") + `</main>`), FetchedAt: fixtureNow})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestParseEdges(t *testing.T) {
	recs := parseHTML(t,
		weekly("8", "в Москве", "1"), // до первой карточки — от FetchedAt
		card("", "2", "28 ноября 2026, 09:00", `<a href="/races/city/1/">Раменское</a> → <a href="/races/city/1/">Жуковский</a> · Московская`,
			`<span class="badge">ITRA 4 100 км</span><span class="badge">ходьба 10 км</span><span class="badge">марафон</span><span class="badge">20 км (+900м)</span>`,
			`<a href="https://example.org/">Сайт</a><a href="/reg/2/">Регистрация</a>`),
		card(" cancelled", "3", "29 ноября 2026", `<a href="/races/city/2/">Тула</a> · Тульская`, `<span class="badge red">Забег отменён</span>`, ""),
		weekly("5", "в Санкт-Петербурге", "4"),
		weekly("5", "в других городах", "5", "6"),
		card("", "7", "6 декабря 2026", `<a href="/races/city/3/">Москва</a>`, "", ""),
		card("", "8", "6 декабря 2026", `<a href="/races/city/4/">Тверь</a>`, "", ""),
		card("", "9", "6 декабря 2026", `<a href="/races/city/5/">Сухум</a> · Абхазия`, "", ""),
		card("", "10", "6 декабря 2026", `<a href="/races/city/6/">Алматы</a> · Казахстан`, "", ""),
	)
	type row struct{ id, date, city, region, country, dist, reg, group string }
	want := []row{
		{"1", "08.11.2026", "Москва", "Москва", "Россия", "5 км", "", "series:5 верст 1"},
		{"2", "28.11.2026", "Раменское", "Московская область", "Россия", "100 км, 42195 м, 20 км", "https://probeg.org/reg/2/", ""},
		{"4", "05.12.2026", "Санкт-Петербург", "Санкт-Петербург", "Россия", "5 км", "", "series:5 верст 4"},
		{"7", "06.12.2026", "Москва", "Москва", "Россия", "", "", ""},
		{"8", "06.12.2026", "Тверь", "", "Россия", "", "", ""},
		{"9", "06.12.2026", "Сухум", "Абхазия", "", "", "", ""},
		{"10", "06.12.2026", "Алматы", "", "Казахстан", "", "", ""},
	}
	var got []row
	for _, r := range recs {
		got = append(got, row{r.ExternalID, r.Date, r.City, r.Region, r.Country, r.Distances, r.RegistrationURL, r.GroupKey})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("записи:\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseNoCards(t *testing.T) {
	for _, body := range []string{`<main><p>Ничего не найдено</p></main>`, `<main>` + weekly("8", "в других городах", "1") + `</main>`} {
		recs, err := Parse(ingest.RawDoc{URL: CalendarURL, Body: []byte(body), FetchedAt: fixtureNow})
		if body == `<main><p>Ничего не найдено</p></main>` {
			if !errors.Is(err, ingest.ErrStructureNotFound) {
				t.Errorf("пустая страница: %v, %v", recs, err)
			}
			continue
		}
		if err != nil || len(recs) != 0 {
			t.Errorf("только паркраны других городов: %v, %v", recs, err)
		}
	}
	if _, err := Parse(ingest.RawDoc{Body: []byte(card("", "1", "8 ноября 2026", "", "", ""))}); err == nil {
		t.Error("без FetchedAt нет ошибки")
	}
}
