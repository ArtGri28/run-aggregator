package marathonec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"aggregator/internal/ingest"
)

var update = flag.Bool("update", false, "переписать golden-файлы")

const (
	pageURL     = "https://marathonec.ru/calendar-beg/"
	fixture     = "testdata/calendar-beg.html"
	goldenParse = "testdata/calendar-beg.golden.json"
)

func parseFile(t *testing.T, path string) ([]ingest.SourceEvent, error) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return Parse(ingest.RawDoc{URL: pageURL, Body: body})
}

func TestParseGolden(t *testing.T) {
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	// D-12: фикстура не менялась после 03-01 — сверка с README.
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

	got, err := Parse(ingest.RawDoc{URL: pageURL, Body: body})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	buf, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	buf = append(buf, '\n')

	again, _ := Parse(ingest.RawDoc{URL: pageURL, Body: body})
	if buf2, _ := json.MarshalIndent(again, "", "  "); !bytes.Equal(buf[:len(buf)-1], buf2) {
		t.Fatal("два вызова Parse дали разный JSON")
	}

	rows := map[string]bool{}
	for _, id := range regexp.MustCompile(`<tr[^>]* id="post-row-[^"]*"`).FindAll(body, -1) {
		rows[string(id)] = true
	}
	if len(got) != len(rows) {
		t.Fatalf("Parse: %d записей, want %d (строки post-row)", len(got), len(rows))
	}
	for _, ev := range got {
		if ev.ExternalID == "" || strings.Trim(ev.ExternalID, "0123456789") != "" {
			t.Errorf("ExternalID = %q, want digits", ev.ExternalID)
		}
		if ev.RegistrationURL != "" {
			u, err := url.Parse(ev.RegistrationURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || strings.HasSuffix(strings.ToLower(u.Hostname()), "marathonec.ru") {
				t.Errorf("%s: RegistrationURL = %q, want external http(s)", ev.ExternalID, ev.RegistrationURL)
			}
		}
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

func TestParseStructureNotFound(t *testing.T) {
	const noDateCol = `<table data-filters='{"tax_oblast":{"terms":[{"name":"Краснодарский край","slug":"krasnodarskij-kraj"}]}}'><thead><tr>` +
		`<th data-name="title">Название</th><th data-name="tax_distance">Дистанции</th><th data-name="tax_city">Город</th>` +
		`<th data-name="hf_tax_oblast">o</th><th data-name="hf_tax_racetype">t</th></tr></thead><tbody>` +
		`<tr id="post-row-1"><td>Забег</td><td>5 км</td><td>Сочи</td><td>krasnodarskij-kraj</td><td>shosse</td></tr></tbody></table>`
	const noFilters = `<table><thead><tr><th data-name="cf_racedate">Дата</th><th data-name="title">Название</th>` +
		`<th data-name="tax_distance">Дистанции</th><th data-name="tax_city">Город</th>` +
		`<th data-name="hf_tax_oblast">o</th><th data-name="hf_tax_racetype">t</th></tr></thead><tbody>` +
		`<tr id="post-row-1"><td>15/11/2026</td><td>Забег</td><td>5 км</td><td>Сочи</td><td>krasnodarskij-kraj</td><td>shosse</td></tr></tbody></table>`

	for _, tc := range []struct {
		name string
		body func(t *testing.T) []byte
	}{
		{"no table", readBody("testdata/edge-no-table.html")},
		{"empty table", readBody("testdata/edge-empty-table.html")},
		{"no date column", func(*testing.T) []byte { return []byte(noDateCol) }},
		{"no region names", func(*testing.T) []byte { return []byte(noFilters) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(ingest.RawDoc{URL: pageURL, Body: tc.body(t)})
			if !errors.Is(err, ingest.ErrStructureNotFound) {
				t.Fatalf("Parse err = %v, want ErrStructureNotFound", err)
			}
		})
	}
}

func readBody(path string) func(t *testing.T) []byte {
	return func(t *testing.T) []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
}

func TestParseEdges(t *testing.T) {
	got, err := parseFile(t, "testdata/edge-rows.html")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	byTitle := map[string]ingest.SourceEvent{}
	for _, ev := range got {
		if strings.ContainsRune(ev.Title, 0) {
			t.Errorf("Title %q содержит U+0000", ev.Title)
		}
		byTitle[ev.Title] = ev
	}
	ev := func(title string) ingest.SourceEvent {
		t.Helper()
		e, ok := byTitle[title]
		if !ok {
			t.Fatalf("нет записи %q", title)
		}
		return e
	}

	if n := len(got); n != 22 {
		t.Fatalf("Parse: %d записей, want 22 (каждая строка post-row — одна запись)", n)
	}
	if e := ev("Нецифровой id"); e.ExternalID != "" {
		t.Errorf("post-row-abc: ExternalID = %q, want \"\"", e.ExternalID)
	}
	if e := ev("Пустая дата"); e.Date != "" || e.ExternalID != "900001" {
		t.Errorf("пустая дата: Date = %q, ExternalID = %q", e.Date, e.ExternalID)
	}
	if e := ev("Короткая строка"); e.Date != "15/11/2026" || e.Distances != "" || e.City != "" || e.Region != "" || e.TypeLabel != "" {
		t.Errorf("строка из двух ячеек = %+v, want только дата и название", e)
	}

	// D-13: ссылка названия; внешняя — registration_url, своя — URL события.
	for _, tc := range []struct{ title, url, reg string }{
		{"Ссылка russiarunning", pageURL, "https://reg.russiarunning.com/event/x"},
		{"Ссылка javascript", pageURL, ""},
		{"Ссылка data", pageURL, ""},
		{"Ссылка длиннее 2048", pageURL, ""},
		{"Ссылка vk away", pageURL, "https://vk.com/away.php?to=https%3A%2F%2Fexample.org%2Frace&utf=1"},
		{"Относительная ссылка", "https://marathonec.ru/relative-race/", ""},
		{"Поддомен marathonec", "https://WWW.Marathonec.ru/sub-race/", ""},
		{"Без ссылки", pageURL, ""},
		{"MIX RUN", "https://marathonec.ru/trail-burya/", ""},
	} {
		if e := ev(tc.title); e.URL != tc.url || e.RegistrationURL != tc.reg {
			t.Errorf("%s: URL, RegistrationURL = %q, %q, want %q, %q", tc.title, e.URL, e.RegistrationURL, tc.url, tc.reg)
		}
	}

	// D-14: область — название из data-filters; несколько областей —
	// Region пуст, слаги и названия в RegionSlugs/Regions.
	for _, tc := range []struct {
		title, region string
		slugs, names  []string
	}{
		{"MIX RUN", "Москва и Московская область", []string{"moskovskaya-oblast"}, []string{"Москва и Московская область"}},
		{"Минский забег", "Беларусь", []string{"belarus"}, []string{"Беларусь"}},
		{"Забег Минской области", "Минская область", []string{"minskaya-oblast"}, []string{"Минская область"}},
		{"Две области", "", []string{"krasnodarskij-kraj", "respublika-adygeya"}, []string{"Краснодарский край", "Республика Адыгея"}},
		{"Область без названия", "neizvestnaya-oblast", []string{"neizvestnaya-oblast"}, []string{"neizvestnaya-oblast"}},
	} {
		e := ev(tc.title)
		if e.Region != tc.region || strings.Join(e.RegionSlugs, "|") != strings.Join(tc.slugs, "|") || strings.Join(e.Regions, "|") != strings.Join(tc.names, "|") {
			t.Errorf("%s: Region %q, RegionSlugs %q, Regions %q; want %q, %q, %q", tc.title, e.Region, e.RegionSlugs, e.Regions, tc.region, tc.slugs, tc.names)
		}
	}

	// Значения для плана 03-03 передаются как есть.
	if e := ev("MIX RUN"); e.Distances != "5K, 10K" || e.TypeLabel != "trejl" || e.Description != "" || e.Country != "" {
		t.Errorf("MIX RUN = %+v", e)
	}
	if e := ev("Стадионный забег"); e.Distances != "100 200 400 м" {
		t.Errorf("Distances = %q, want \"100 200 400 м\"", e.Distances)
	}
	if e := ev("Минский забег"); e.City != "Минск" {
		t.Errorf("City = %q, want Минск", e.City)
	}
	if e := ev("Две области"); e.City != "Майкоп, Солохаул" {
		t.Errorf("City = %q, want \"Майкоп, Солохаул\"", e.City)
	}
	if a, b := ev("Дубликат, первая строка"), ev("Дубликат, вторая строка"); a.ExternalID != "900015" || b.ExternalID != "900015" {
		t.Errorf("дубликат: ExternalID %q, %q, want 900015 оба", a.ExternalID, b.ExternalID)
	}
	if e := ev("Через четыре года"); e.Date != "15/10/2030" {
		t.Errorf("Date = %q", e.Date)
	}
	if e := ev("Прошедший забег"); e.Date != "01/09/2026" {
		t.Errorf("Date = %q", e.Date)
	}
}

func TestCleanText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a\x00b", "ab"},
		{"Ми\xffнск", "Минск"},
		{"Трейл «Буря» — Красногорск", "Трейл «Буря» — Красногорск"},
		{"", ""},
	} {
		got := clean(tc.in)
		if got != tc.want || !utf8.ValidString(got) {
			t.Errorf("clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
