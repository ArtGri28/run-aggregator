// Package russialoppet — источник Russialoppet (russialoppet.ru/events/<сезон>/):
// календарь лыжных марафонов России. Читается с разрешения владельца сайта
// (SOURCE-PERMISSION-LETTERS.md). Структура страницы — testdata/README.md.
package russialoppet

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "russialoppet"
	// Site — адрес сайта; календарь сезона — Site + "/events/<сезон>/".
	Site = "https://russialoppet.ru"
	// CrawlDelay — пауза между запросами к хосту.
	CrawlDelay = 10 * time.Second
)

// TLSConfig — TLS 1.2 и только шифры ECDSA (23-CONTEXT.md, D-07): с ними
// сервер отдаёт сертификат Let's Encrypt, а не самоподписанный RSA по
// умолчанию. Проверка сертификата — стандартная.
func TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},
	}
}

// federalCities — города, которые календарь пишет на месте области.
var federalCities = map[string]bool{"Москва": true, "Санкт-Петербург": true, "Севастополь": true}

var (
	oneDayRe = regexp.MustCompile(`^(\d{2})\.(\d{2})\.(\d{4})$`)
	// spanRe — «21-22.02.2027» и «28.02-01.03.2027».
	spanRe = regexp.MustCompile(`^(\d{1,2})(?:\.(\d{2}))?-(\d{1,2})\.(\d{2})\.(\d{4})$`)
	distRe = regexp.MustCompile(`^(\d+(?:[.,]\d+)?)\s*(км|м)?`)
	unitRe = regexp.MustCompile(`(км|м)\s*$`)
	idRe   = regexp.MustCompile(`_(\d+)$`)
)

// Source — источник Russialoppet: один GET календаря сезона за прогон.
type Source struct {
	f    *ingest.Fetcher
	site string
	now  func() time.Time
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник; загрузчик — копия f с TLSConfig и своим затвором хоста.
func New(f *ingest.Fetcher) *Source {
	return &Source{f: f.WithTLS(TLSConfig()), site: Site, now: time.Now}
}

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse: календарь только российский.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Season — сезон календаря на момент now: с мая — следующий год (сезон 2027 —
// зима 2026/27).
func Season(now time.Time) int {
	if now.Month() >= time.May {
		return now.Year() + 1
	}
	return now.Year()
}

// Fetch загружает календарь текущего сезона.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, fmt.Sprintf("%s/events/%d/", s.site, Season(s.now())), CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает календарь (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

// Parse разбирает календарь. Чистая функция.
//   - Старт — строка tr.item: в td.item-name-cell ссылка с названием, в
//     td.but-reg кнопка регистрации (есть не у всех), в td.but-cell дата,
//     три span (округ, область, город) и после последнего <br> дистанции со
//     стилем («50/25 км СВ»).
//   - external_id — номер из id строки (bx_…_805692).
//   - Строки без дистанций (церемония награждения) и без даты пропускаются.
//
// Нет ни одной строки — ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	var out []ingest.SourceEvent
	rows := 0
	for n := range root.Descendants() {
		if n.DataAtom != atom.Tr || !hasClass(n, "item") {
			continue
		}
		rows++
		if rec, ok := parseRow(n); ok {
			out = append(out, rec)
		}
	}
	if rows == 0 {
		return nil, fmt.Errorf("%w: нет строк tr.item", ingest.ErrStructureNotFound)
	}
	return out, nil
}

func parseRow(tr *html.Node) (ingest.SourceEvent, bool) {
	var rec ingest.SourceEvent
	var date, region, place, dist string
	for td := range tr.ChildNodes() {
		switch {
		case hasClass(td, "item-name-cell"):
			if a := first(td, atom.A); a != nil {
				rec.Title, rec.URL = text(a), absURL(attr(a, "href"))
			}
		case hasClass(td, "but-reg"):
			if a := first(td, atom.A); a != nil {
				rec.RegistrationURL = absURL(attr(a, "href"))
			}
		case hasClass(td, "but-cell"):
			date, region, place, dist = cell(td)
		}
	}
	m := idRe.FindStringSubmatch(attr(tr, "id"))
	if m == nil || rec.Title == "" || rec.URL == "" {
		return ingest.SourceEvent{}, false
	}
	rec.ExternalID = m[1]
	if !dates(&rec, date) {
		return ingest.SourceEvent{}, false
	}
	if rec.Distances = distances(dist); rec.Distances == "" {
		return ingest.SourceEvent{}, false
	}
	rec.Country, rec.Sport, rec.TypeLabel = "RU", string(event.SportSkiing), "Лыжные гонки"
	place = strings.TrimSpace(place)
	// Без области в своём поле календарь пишет «Тамбовская область, Котовск».
	if region == "" {
		if r, c, ok := strings.Cut(place, ","); ok {
			region, place = strings.TrimSpace(r), strings.TrimSpace(c)
		}
	}
	if federalCities[region] {
		region = ""
	}
	rec.Region = regionName(region)
	if rec.City = ingest.PlaceCity(place); rec.City != place {
		rec.Address = place
	}
	return rec, true
}

// cell — дата, область, место и строка дистанций из td.but-cell.
func cell(td *html.Node) (date, region, place, dist string) {
	var spans []string
	var lastBr *html.Node
	for c := range td.ChildNodes() {
		switch {
		case c.DataAtom == atom.Span && hasClass(c, "date"):
			date = text(c)
		case c.DataAtom == atom.Span:
			spans = append(spans, text(c))
		case c.DataAtom == atom.Br:
			lastBr = c
		}
	}
	if len(spans) >= 3 {
		region, place = spans[1], spans[2]
	}
	if lastBr != nil {
		var b strings.Builder
		for c := lastBr.NextSibling; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.B && (text(c) == "СВ" || text(c) == "КЛ") {
				continue
			}
			b.WriteString(textOf(c) + " ")
		}
		dist = strings.Join(strings.Fields(b.String()), " ")
	}
	return date, region, place, dist
}

func dates(rec *ingest.SourceEvent, s string) bool {
	if oneDayRe.MatchString(s) {
		rec.Date = s
		return true
	}
	m := spanRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	startMonth := m[2]
	if startMonth == "" {
		startMonth = m[4]
	}
	d1, _ := strconv.Atoi(m[1])
	d2, _ := strconv.Atoi(m[3])
	rec.Date = fmt.Sprintf("%02d.%s.%s", d1, startMonth, m[5])
	rec.EndDate = fmt.Sprintf("%02d.%s.%s", d2, m[4], m[5])
	return true
}

// distances — «50/25/1/600 м/5 км» → «50 км, 25 км, 1 км, 600 м, 5 км»: число
// без единицы — в единице конца строки.
func distances(s string) string {
	unit := "км"
	if m := unitRe.FindStringSubmatch(s); m != nil {
		unit = m[1]
	}
	var out []string
	for part := range strings.SplitSeq(s, "/") {
		m := distRe.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil {
			continue
		}
		u := m[2]
		if u == "" {
			u = unit
		}
		out = append(out, m[1]+" "+u)
	}
	return strings.Join(out, ", ")
}

// regionName — «Р. Карелия» → «Республика Карелия», «Р. Удмуртская» →
// «Удмуртская Республика» (название из перечня субъектов); остальное — как в
// календаре.
func regionName(s string) string {
	r, ok := strings.CutPrefix(s, "Р. ")
	if !ok {
		return s
	}
	if c, k := normalize.ClassifyRegion("Республика " + r); k != normalize.RegionUnknown {
		return c
	}
	return "Республика " + r
}

func absURL(href string) string {
	href = strings.TrimSpace(href)
	if strings.HasPrefix(href, "/") {
		return Site + href
	}
	return href
}

func first(n *html.Node, a atom.Atom) *html.Node {
	for c := range n.Descendants() {
		if c.DataAtom == a {
			return c
		}
	}
	return nil
}

func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	return text(n)
}

func text(n *html.Node) string {
	var b strings.Builder
	for c := range n.Descendants() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data + " ")
		}
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(b.String(), " ", " ")), " ")
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
