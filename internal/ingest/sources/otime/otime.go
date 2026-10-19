// Package otime — источник O-time (reg.o-time.ru/calendar): календарь
// стартов Санкт-Петербурга и Северо-Запада площадки регистрации O-time.
// Читается с письменного разрешения владельца сайта
// (SOURCE-PERMISSION-LETTERS.md). Страница в windows-1251; структура —
// testdata/README.md.
package otime

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/text/encoding/charmap"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "otime"
	// ListURL — календарь всех регионов.
	ListURL = "https://reg.o-time.ru/calendar"
	// CrawlDelay — пауза между запросами к хосту.
	CrawlDelay = 5 * time.Second
)

// sports — вид спорта по номеру значка старта; других номеров (триатлон и
// прочее) в каталоге нет — старт пропускается.
var sports = map[string]event.Sport{"1": event.SportSkiing, "2": event.SportRunning}

// federalCities — города, которые календарь пишет на месте области.
var federalCities = map[string]bool{"Санкт-Петербург": true, "Москва": true}

var (
	iconRe  = regexp.MustCompile(`/img/sport/(\d+)\.png$`)
	raceRe  = regexp.MustCompile(`^https://reg\.o-time\.ru/race/(\d+)$`)
	monthRe = regexp.MustCompile(`^([A-Za-z]+) (\d{4})$`)
	// spanRe — даты многодневного старта в названии: «(05.12-06.12)».
	spanRe  = regexp.MustCompile(`\((\d{2})\.(\d{2})-(\d{2})\.(\d{2})\)`)
	trailRe = regexp.MustCompile(`(?i)трейл|trail`)
	months  = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
)

// Source — источник O-time: один GET календаря за прогон.
type Source struct {
	f   *ingest.Fetcher
	url string
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: ListURL} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse: все регионы календаря российские.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает календарь.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает календарь (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

// Parse разбирает календарь. Чистая функция; «сегодня» — doc.FetchedAt.
//   - День — блок div.all: в article.alldate число и месяц по-английски
//     («24», «Oct»), год — из заголовка H2 выше («November 2026»); под
//     заголовками «This week», «Next week» года нет — ближайший год, при
//     котором дата не раньше недели до дня загрузки.
//   - Старт — div.allinner1 или div.allinner2: значок вида спорта, ссылка с
//     названием (страница старта на O-time или сайт организатора), место —
//     область жирным и остальное.
//   - external_id — номер страницы старта на O-time; без неё — дата и
//     название.
//
// Нет ни одного дня — ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	body, err := charmap.Windows1251.NewDecoder().Bytes(doc.Body)
	if err != nil {
		return nil, fmt.Errorf("кодировка windows-1251: %w", err)
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	var out []ingest.SourceEvent
	year, days := 0, 0
	for n := range root.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		if n.DataAtom == atom.H2 {
			year = 0
			if m := monthRe.FindStringSubmatch(text(n)); m != nil {
				year, _ = strconv.Atoi(m[2])
			}
			continue
		}
		if !hasClass(n, "all") {
			continue
		}
		days++
		date, ok := dayDate(n, year, doc.FetchedAt)
		if !ok {
			continue
		}
		for c := range n.Descendants() {
			if c.Type == html.ElementNode && (hasClass(c, "allinner1") || hasClass(c, "allinner2")) {
				if rec, ok := parseStart(c, date); ok {
					out = append(out, rec)
				}
			}
		}
	}
	if days == 0 {
		return nil, fmt.Errorf("%w: нет блоков дней div.all", ingest.ErrStructureNotFound)
	}
	return out, nil
}

// dayDate — дата дня блока; year 0 — год по дню загрузки.
func dayDate(day *html.Node, year int, now time.Time) (time.Time, bool) {
	for n := range day.Descendants() {
		if n.Type != html.ElementNode || !hasClass(n, "alldate") {
			continue
		}
		f := strings.Fields(text(n))
		if len(f) < 2 {
			return time.Time{}, false
		}
		d, err := strconv.Atoi(f[0])
		m, ok := months[strings.ToLower(f[1])]
		if err != nil || !ok {
			return time.Time{}, false
		}
		if year == 0 {
			year = now.Year()
			if time.Date(year, m, d, 0, 0, 0, 0, time.UTC).Before(now.AddDate(0, 0, -7)) {
				year++
			}
		}
		return time.Date(year, m, d, 0, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

func parseStart(inner *html.Node, date time.Time) (ingest.SourceEvent, bool) {
	var sport event.Sport
	var known bool
	var title, href, region, rest string
	for n := range inner.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		switch {
		case hasClass(n, "alllogo"):
			for img := range n.Descendants() {
				if m := iconRe.FindStringSubmatch(attr(img, "src")); img.DataAtom == atom.Img && m != nil {
					sport, known = sports[m[1]]
				}
			}
		case hasClass(n, "allname"):
			for a := range n.Descendants() {
				if a.DataAtom == atom.A {
					title, href = text(a), strings.TrimSpace(attr(a, "href"))
				}
			}
		case hasClass(n, "allplace"):
			for b := range n.Descendants() {
				if b.DataAtom == atom.B {
					region = text(b)
				}
			}
			rest = strings.TrimSpace(strings.TrimPrefix(text(n), region))
		}
	}
	if !known || title == "" {
		return ingest.SourceEvent{}, false
	}
	id := date.Format("2006-01-02") + " " + title
	if m := raceRe.FindStringSubmatch(href); m != nil {
		id = m[1]
	}
	rec := ingest.SourceEvent{
		ExternalID: id,
		URL:        href,
		Title:      title,
		Date:       date.Format("02.01.2006"),
		Country:    "RU",
	}
	if m := spanRe.FindStringSubmatch(title); m != nil {
		d2, _ := strconv.Atoi(m[3])
		m2, _ := strconv.Atoi(m[4])
		end := time.Date(date.Year(), time.Month(m2), d2, 0, 0, 0, 0, time.UTC)
		if end.Before(date) {
			end = end.AddDate(1, 0, 0)
		}
		rec.EndDate = end.Format("02.01.2006")
	}
	switch sport {
	case event.SportSkiing:
		rec.Sport, rec.TypeLabel = string(sport), "Лыжные гонки"
	default:
		if trailRe.MatchString(title) {
			rec.TypeLabel = "трейл"
		}
	}
	// Жирным — область либо город федерального значения; во втором случае
	// остальное — место в городе («Елагин остров»).
	if federalCities[region] {
		rec.City = region
		if rest != region {
			rec.Address = rest
		}
	} else {
		// «р. Карелия» — республика: перечень субъектов РФ знает полное название.
		rec.City, rec.Region = ingest.PlaceCity(rest), region
		if r, ok := strings.CutPrefix(region, "р. "); ok {
			rec.Region = "Республика " + r
		}
		if rec.City != rest {
			rec.Address = rest
		}
	}
	return rec, true
}

func text(n *html.Node) string {
	var b strings.Builder
	for c := range n.Descendants() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data + " ")
		}
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(b.String(), " ", " ")), " ")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for c := range strings.FieldsSeq(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
