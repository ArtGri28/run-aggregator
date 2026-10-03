package probeg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

var update = flag.Bool("update", false, "переписать golden-файлы")

const (
	fixture     = "testdata/year_ahead-2026-09-30.html"
	goldenParse = "testdata/year_ahead.golden.json"
)

// fixtureFetched — время загрузки фикстуры (testdata/README.md), 12:00 UTC.
var fixtureFetched = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func fixtureBody(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func parseFixture(t *testing.T) []ingest.SourceEvent {
	t.Helper()
	recs, err := Parse(ingest.RawDoc{URL: CalendarURL, Body: fixtureBody(t), FetchedAt: fixtureFetched})
	if err != nil {
		t.Fatalf("Parse: %v", err)
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

// row — строка данных: 6 ячеек; title — HTML ячейки названия.
func row(date, title, place, dist, links string) string {
	return `<tr style="font-size: 11px;"><td>1</td><td>` + date + `</td><td>` + title + `</td><td>` + place +
		`</td><td>` + dist + `</td><td>` + links + `</td></tr>`
}

func parsePage(t *testing.T, fetched time.Time, rows ...string) ([]ingest.SourceEvent, error) {
	t.Helper()
	return Parse(ingest.RawDoc{URL: CalendarURL, Body: page(rows...), FetchedAt: fetched})
}

func TestParseDates(t *testing.T) {
	cases := []struct{ in, date, end string }{
		{"03.10", "03.10.2026", ""},
		{"29.09", "29.09.2026", ""},
		{"28.09", "28.09.2027", ""},
		{"01.09", "01.09.2027", ""},
		{"02–04.10", "02.10.2026", "04.10.2026"},
		{"02-04.10", "02.10.2026", "04.10.2026"},
		{"02—04.10", "02.10.2026", "04.10.2026"},
		{"31.10–01.11", "31.10.2026", "01.11.2026"},
		{"31.10 — 01.11", "31.10.2026", "01.11.2026"},
		{"28.12–03.01", "28.12.2026", "03.01.2027"},
		{"31.07–04.08", "31.07.2027", "04.08.2027"},
		{"бред", "бред", ""},
		{"15", "15", ""},
		{"31.02", "31.02", ""},
	}
	for _, c := range cases {
		recs, err := parsePage(t, fixtureFetched, row(`<span class="nobr">`+c.in+`</span></a>`, `<a href="/event/1/">Забег</a>`, "Пенза", "5 км", ""))
		if err != nil || len(recs) != 1 {
			t.Fatalf("%q: %+v, %v", c.in, recs, err)
		}
		if recs[0].Date != c.date || recs[0].EndDate != c.end {
			t.Errorf("%q: Date %q EndDate %q, want %q %q", c.in, recs[0].Date, recs[0].EndDate, c.date, c.end)
		}
	}
}

func TestParseDistances(t *testing.T) {
	recs := parseFixture(t)
	r := byID(t, recs, "81994")
	for _, junk := range []string{"910", "640", "12760", "5150", "("} {
		if strings.Contains(r.Distances, junk) {
			t.Errorf("81994: Distances %q содержит %q", r.Distances, junk)
		}
	}
	if got := normalize.Distances(r.Distances); !slices.Equal(got, []int{5200, 12800, 20000, 40300, 70600}) {
		t.Errorf("81994: %v (%q)", got, r.Distances)
	}
	if r := byID(t, recs, "77021"); !slices.Equal(normalize.Distances(r.Distances), []int{30, 60, 200, 400, 800}) {
		t.Errorf("77021: %q", r.Distances)
	}
	if r := byID(t, recs, "75784"); strings.Contains(strings.ToLower(r.Distances), "ходьба") ||
		!slices.Equal(normalize.Distances(r.Distances), []int{2000, 6000, 12000, 26000, 50000}) {
		t.Errorf("75784: %q", r.Distances)
	}
	if r := byID(t, recs, "83327"); r.Distances != "20 км, 10 км, 2 км, 1 км" {
		t.Errorf("83327: %q", r.Distances)
	}
	if r := byID(t, recs, "85120"); !strings.Contains(r.Distances, "21100 м") || strings.Contains(r.Distances, "триатлон") {
		t.Errorf("85120: %q", r.Distances)
	}
	// «марафон», «полумарафон» в списке с числовыми дистанциями не теряются.
	for id, want := range map[string][]int{
		"79960": {1000, 3000, 10000, 21098, 42195},    // марафон, полумарафон, 10 км, 3 км, 1 км
		"75122": {300, 600, 1000, 5000, 10000, 21098}, // «Моя столица»: полумарафон, 10 км, 5 км, 1 км, 600 м, 300 м
		"71738": {5000, 10000, 21098, 42195},          // «Арена Марафон»: марафон, марафон, полумарафон, 10 км, 5 км
		// баллы ITRA не становятся дистанциями: «ITRA 4 100 км, ITRA 2 50 км, ITRA 1 30200 м, …»
		"77976": {500, 800, 2000, 5100, 12700, 30200, 50000, 100000},
		"80722": {51000, 105000}, // ITRA 4 105 км, ITRA 2 51 км
	} {
		if got := normalize.Distances(byID(t, recs, id).Distances); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", id, got, want)
		}
	}
}

func TestNamedToMeters(t *testing.T) {
	for in, want := range map[string]string{
		"марафон":     "42195 м",
		"Полумарафон": "21098 м",
		"10 км":       "10 км",
		"эстафета":    "эстафета",
	} {
		if got := namedToMeters(in); got != want {
			t.Errorf("namedToMeters(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRows(t *testing.T) {
	recs := parseFixture(t)
	type place struct{ date, end, region, city, country string }
	check := func(id string, want place) {
		t.Helper()
		r := byID(t, recs, id)
		got := place{r.Date, r.EndDate, r.Region, r.City, r.Country}
		if want.date == "" {
			got.date, got.end = "", ""
		}
		if got != want {
			t.Errorf("%s: %+v, want %+v", id, got, want)
		}
	}
	check("83327", place{region: "Свердловская область", city: "Черноисточинск", country: "Россия"})
	check("85120", place{"02.10.2026", "04.10.2026", "Краснодарский край", "Сочи", "Россия"})
	check("74418", place{region: "", city: "Хабаровск", country: "Россия"})
	check("76653", place{region: "Москва", city: "Москва", country: "Россия"})
	check("83368", place{region: "", city: "Гомель", country: "Беларусь"})
	check("83357", place{region: "Брестская область", city: "Колбовичи", country: "Беларусь"})
	check("77919", place{region: "", city: "Минск", country: "Беларусь"})
	if a := byID(t, recs, "83357").Address; a != "Барановичский район" {
		t.Errorf("83357: Address %q, want район", a)
	}
	check("77976", place{date: "31.10.2026", end: "01.11.2026", region: byID(t, recs, "77976").Region, city: byID(t, recs, "77976").City, country: "Россия"})
	check("83307", place{date: "31.07.2027", end: "04.08.2027", region: byID(t, recs, "83307").Region, city: byID(t, recs, "83307").City, country: byID(t, recs, "83307").Country})
	check("84871", place{date: "07.09.2027", region: byID(t, recs, "84871").Region, city: "Сахюрта", country: "Россия"})
	check("84039", place{region: "Санкт-Петербург", city: "Санкт-Петербург", country: "Россия"})
	check("84016", place{})

	r := byID(t, recs, "83327")
	if r.RegistrationURL != "https://myrace.info/events/2080" || r.GroupKey != "" || r.URL != "https://probeg.org/event/83327/" {
		t.Errorf("83327: %+v", r)
	}
	if r := byID(t, recs, "85120"); r.RegistrationURL != "" {
		t.Errorf("85120: RegistrationURL %q, want пусто (только «Сайт»)", r.RegistrationURL)
	}
	r = byID(t, recs, "84009")
	if r.GroupKey != "series:"+normalize.TitleNorm("5 вёрст Алёшкинский лес") || r.Region != "Москва" || r.City != "Москва" || r.Distances != "5 км" {
		t.Errorf("84009: %+v", r)
	}
	if r := byID(t, recs, "84016"); r.GroupKey == "" {
		t.Error("84016: GroupKey пуст")
	}
}

func TestParseFixtureShape(t *testing.T) {
	recs := parseFixture(t)
	ids := map[string]bool{}
	single, grouped := 0, 0
	keys, noCity := map[string]bool{}, map[string]bool{}
	for _, r := range recs {
		if r.ExternalID == "" || ids[r.ExternalID] {
			t.Errorf("ExternalID %q пуст или повторяется", r.ExternalID)
		}
		ids[r.ExternalID] = true
		if strings.Contains(r.Title, "человек") {
			t.Errorf("%s: Title %q", r.ExternalID, r.Title)
		}
		if r.GroupKey == "" {
			single++
			continue
		}
		grouped++
		keys[r.GroupKey] = true
		if r.City == "" {
			noCity[r.GroupKey] = true
		}
	}
	for _, id := range []string{"72982", "73219"} {
		if ids[id] {
			t.Errorf("отменённое событие %s в выводе (D-15)", id)
		}
	}
	if single != 362 || grouped < 900 || len(keys) < 180 || len(keys) > 191 || len(noCity) < 135 || len(noCity) > 145 {
		t.Errorf("одиночных %d (want 362), в сериях %d (≥ 900), ключей %d (180–191), без города %d (135–145)",
			single, grouped, len(keys), len(noCity))
	}
	t.Logf("записей %d: одиночных %d, в сериях %d, ключей серий %d, без города %d", len(recs), single, grouped, len(keys), len(noCity))
}

// TestPlaceVerdicts — состав строк места фикстуры (D-01): префикс субъекта
// РФ, префикс иностранной страны, голый город с вердиктом справочника,
// серии без города. Полный список голых городов — в -v выводе: проверка
// того, что спорное место не принято за РФ.
func TestPlaceVerdicts(t *testing.T) {
	root, err := html.Parse(bytes.NewReader(fixtureBody(t)))
	if err != nil {
		t.Fatal(err)
	}
	rfRows, noCityRows := 0, 0
	foreign := map[string]int{}
	bareRU, bareNo := map[string]int{}, map[string]int{}
	for tr := range findTable(root).Descendants() {
		if !isElem(tr, atom.Tr) || !strings.Contains(attr(tr, "style"), rowStyle) {
			continue
		}
		cells := rowCells(tr)
		if cells == nil || parseRow(cells, ingest.RawDoc{FetchedAt: fixtureFetched}) == nil {
			continue // отменённая строка
		}
		if cells[colCity] == nil {
			noCityRows++
			continue
		}
		raw := text(cells[colCity])
		parts := strings.Split(raw, ",")
		_, kind := normalize.ClassifyRegion(parts[0])
		_, city, cntry, _ := resolvePlace(raw, false)
		switch {
		case kind == normalize.RegionForeign:
			foreign[strings.TrimSpace(parts[0])]++
		case len(parts) > 1 && kind == normalize.RegionRF:
			rfRows++
		case len(parts) > 1:
			t.Errorf("место %q: нераспознанный первый компонент", raw)
		case cntry == "Россия":
			v := "справочник городов"
			if kind == normalize.RegionRF {
				v = "субъект РФ"
			}
			bareRU[city+" ("+v+")"]++
		default:
			bareNo[city]++
		}
	}
	list := func(m map[string]int) string {
		var s []string
		for k, n := range m {
			s = append(s, fmt.Sprintf("%s ×%d", k, n))
		}
		sort.Strings(s)
		return strings.Join(s, "; ")
	}
	sum := func(m map[string]int) (n int) {
		for _, v := range m {
			n += v
		}
		return n
	}
	t.Logf("строк с префиксом субъекта РФ: %d", rfRows)
	t.Logf("строк с префиксом иностранной страны: %d (%s)", sum(foreign), list(foreign))
	t.Logf("голый город → RU: %d строк, %d мест: %s", sum(bareRU), len(bareRU), list(bareRU))
	t.Logf("голый город → не RU: %d строк, %d мест: %s", sum(bareNo), len(bareNo), list(bareNo))
	t.Logf("групповых строк без города: %d", noCityRows)
	for c := range bareNo {
		t.Errorf("голый город %q не принят: проверьте справочник", c)
	}
}

func TestParseGolden(t *testing.T) {
	body := fixtureBody(t)
	readme, err := os.ReadFile("testdata/README.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`SHA-256 очищенной фикстуры: ([0-9a-f]{64})`).FindSubmatch(readme)
	if m == nil {
		t.Fatal("README: нет строки «SHA-256 очищенной фикстуры: <hex>»")
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != string(m[1]) {
		t.Fatalf("SHA-256 фикстуры = %s, want %s (README)", got, m[1])
	}

	got := parseFixture(t)
	buf, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	buf = append(buf, '\n')
	if buf2, _ := json.MarshalIndent(parseFixture(t), "", "  "); !bytes.Equal(buf[:len(buf)-1], buf2) {
		t.Fatal("два вызова Parse дали разный JSON")
	}

	if *update {
		if err := os.WriteFile(goldenParse, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenParse)
	if err != nil {
		t.Fatalf("%v (сгенерируйте: go test -run TestParseGolden -update)", err)
	}
	if !bytes.Equal(want, buf) {
		t.Errorf("расхождение с %s; запустите go test -update и просмотрите diff", goldenParse)
	}
}

func TestParseEdges(t *testing.T) {
	for name, body := range map[string]string{
		"нет таблицы":       `<html><body><p>Календарь</p></body></html>`,
		"таблица без строк": `<table>` + headerRow + `</table>`,
		"5 столбцов": `<table><tr class="info"><th>№</th><th>Дата</th><th>Название</th><th>Город</th><th>Дистанции</th></tr>` +
			row("03.10", `<a href="/event/1/">Забег</a>`, "Пенза", "5 км", "") + `</table>`,
	} {
		_, err := Parse(ingest.RawDoc{URL: CalendarURL, Body: []byte(body), FetchedAt: fixtureFetched})
		if !errors.Is(err, ingest.ErrStructureNotFound) {
			t.Errorf("%s: err = %v, want ErrStructureNotFound", name, err)
		}
	}

	if _, err := Parse(ingest.RawDoc{URL: CalendarURL, Body: page(row("03.10", `<a href="/event/1/">Забег</a>`, "Пенза", "5 км", ""))}); err == nil {
		t.Error("нулевой FetchedAt: нет ошибки")
	}

	recs, err := parsePage(t, fixtureFetched,
		row("03.10", `<a href="/event/1/">Забег`+"\xff\x00"+` «Осень»</a>`, "Пенза", "5 км",
			`<a href="javascript:alert(1)">Регистрация</a>`),
		row("10.10", `<a href="/event/2/">Парк А</a><span>, </span><a href="/event/3/">Парк Б</a><br/>Планируют участвовать <a href="/event/2/"> 3 человека </a>`,
			"Москва", "5 км", ""),
		row("11.10", `<a href="/event/x1/">Без числового id</a>`, "Пенза", "5 км", `<a href="/reg/">Регистрация</a>`),
		// Кнопка регистрации на сайте ПроБЕГа (/event/<n>/reg/) — не второе событие.
		row("12.10", `<a href="/event/4/">Забег</a><p><a href="/event/4/reg/"><button>Зарегистрироваться через наш сайт</button></a></p>`, "Пенза", "5 км", ""),
	)
	if err != nil || len(recs) != 5 {
		t.Fatalf("recs = %+v, %v; want 5", recs, err)
	}
	if r := recs[0]; r.Title != "Забег «Осень»" || !utf8.ValidString(r.Title) || r.RegistrationURL != "" {
		t.Errorf("мусор/javascript: %+v", r)
	}
	if recs[1].ExternalID != "2" || recs[2].ExternalID != "3" || recs[1].GroupKey == "" || recs[1].GroupKey == recs[2].GroupKey {
		t.Errorf("группа: %+v %+v", recs[1], recs[2])
	}
	if r := recs[3]; r.ExternalID != "" || r.URL != "" || r.RegistrationURL != "https://probeg.org/reg/" {
		t.Errorf("нечисловой id: %+v", r)
	}
	if r := recs[4]; r.ExternalID != "4" || r.GroupKey != "" {
		t.Errorf("кнопка регистрации: %+v", r)
	}

	// Голый город вне справочника и неоднозначный — без страны (D-01).
	for _, p := range []string{"Гомель", "Кировск", "Минская область, Минск"} {
		recs, err := parsePage(t, fixtureFetched, row("03.10", `<a href="/event/1/">Забег</a>`, p, "5 км", ""))
		if err != nil || recs[0].Country != "" {
			t.Errorf("%q: %+v, %v; want Country пусто", p, recs, err)
		}
	}
}

func TestResolvePlace(t *testing.T) {
	tests := []struct {
		raw                            string
		group                          bool
		region, city, country, address string
	}{
		// Беларусь: страна по справочнику, область, район, город без префикса.
		{"Беларусь, Минск", false, "", "Минск", "Беларусь", ""},
		{"Беларусь, Гомель", false, "", "Гомель", "Беларусь", ""},
		{"Беларусь, Брестская область, Малорита", false, "Брестская область", "Малорита", "Беларусь", ""},
		{"Беларусь, Минская область, Молодечно", false, "Минская область", "Молодечно", "Беларусь", ""},
		{"Беларусь, Минская область, Любань", false, "Минская область", "Любань", "Беларусь", ""},
		{"Беларусь, Минская область, Логойск", false, "Минская область", "Логойск", "Беларусь", ""},
		{"Беларусь, Минская область, Минский район, д.\u00a0Раубичи", false, "Минская область", "Раубичи", "Беларусь", "Минский район"},
		{"Республика Беларусь, Гродненская область, Лида", false, "Гродненская область", "Лида", "Республика Беларусь", ""},
		// другая страна с ISO-кодом: тот же механизм, решение — за Prepare.
		{"Казахстан, Алматы", false, "", "Алматы", "Казахстан", ""},
		// иностранная страна без ISO-кода: как раньше.
		{"Абхазия, Сухум", false, "Абхазия", "Сухум", "", ""},
		// Россия без изменений.
		{"Московская область, Химки", false, "Московская область", "Химки", "Россия", ""},
		{"г. Москва", true, "Москва", "Москва", "Россия", ""},
		{"Ростов-на-Дону", false, "", "Ростов-на-Дону", "Россия", ""},
		{"Атлантида, Центр", false, "Атлантида", "Центр", "", ""},
		{"Неизвестный город", false, "", "Неизвестный город", "", ""},
		{"", false, "", "", "", ""},
	}
	for _, tt := range tests {
		region, city, country, address := resolvePlace(tt.raw, tt.group)
		if region != tt.region || city != tt.city || country != tt.country || address != tt.address {
			t.Errorf("resolvePlace(%q) = %q/%q/%q/%q, want %q/%q/%q/%q",
				tt.raw, region, city, country, address, tt.region, tt.city, tt.country, tt.address)
		}
	}
}

// TestSeriesGroupKeyPlace — L4 (воспроизведение до решения владельца):
// одноимённые серии в разных городах получают один GroupKey, и Prepare
// сворачивает их в одну запись.
func TestSeriesGroupKeyPlace(t *testing.T) {
	recs, err := parsePage(t, fixtureFetched,
		row("10.10", `<a href="/event/1/">Парк А</a><span>, </span><a href="/event/2/">Парк Б</a>`, "Москва", "5 км", ""),
		row("17.10", `<a href="/event/3/">Парк А</a><span>, </span><a href="/event/4/">Парк В</a>`, "Пенза", "5 км", ""),
	)
	if err != nil || len(recs) != 4 {
		t.Fatalf("recs = %+v, %v", recs, err)
	}
	same := recs[0].GroupKey != "" && recs[0].GroupKey == recs[2].GroupKey && recs[0].City != recs[2].City
	prep, st := ingest.Prepare(recs, fixtureFetched, New(nil).Policy())
	kept := 0
	for _, p := range prep {
		if p.Source.ExternalID == "1" || p.Source.ExternalID == "3" {
			kept++
		}
	}
	t.Logf("L4 REPRO same_key=%v cities=%s,%s kept_park_a=%d collapsed=%d", same, recs[0].City, recs[2].City, kept, st.Collapsed)
	if !same {
		t.Fatalf("воспроизведение L4: ключи %q и %q", recs[0].GroupKey, recs[2].GroupKey)
	}
}
