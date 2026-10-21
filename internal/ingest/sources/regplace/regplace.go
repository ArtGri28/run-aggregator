// Package regplace — источник reg.place: каталог ближайших мероприятий
// площадки регистрации. Читается с разрешения владельца сайта
// (SOURCE-PERMISSION-LETTERS.md), только публичные страницы: список /events и
// страницы событий; программный интерфейс не используется до ответа
// reg.place (23-CONTEXT.md, Q-3). Структура страниц — testdata/README.md.
package regplace

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
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
	Code = "regplace"
	// Site — адрес сайта; список — Site + "/events".
	Site = "https://reg.place"
	// CrawlDelay — robots.txt задержки не задаёт; пауза — правило проекта.
	CrawlDelay = 10 * time.Second
	// maxPages — предел страниц списка за прогон.
	maxPages = 10
)

// sports — вид спорта по названию вида на карточке; первое известное
// название в порядке карточки решает. Мультиспорт собирается, но выключен в
// справочнике.
var sports = map[string]event.Sport{
	"Бег": event.SportRunning, "Лыжи": event.SportSkiing, "Лыжероллеры": event.SportSkiing,
	"Плавание": event.SportSwimming, "Велогонка": event.SportCycling,
	"Триатлон": event.SportMultisport, "Акватлон": event.SportMultisport, "Дуатлон": event.SportMultisport,
}

var (
	slugRe   = regexp.MustCompile(`^/events/([^/?#]+)$`)
	dateRe   = regexp.MustCompile(`^(\d{2}\.\d{2})(?:\.(\d{4}))?(?:\s*[–—-]\s*(\d{2}\.\d{2}\.\d{4}))?$`)
	distRe   = regexp.MustCompile(`^(\d+(?:,\d+)?)\s*(км|м)$`)
	onlineRe = regexp.MustCompile(`(?i)онлайн|zwift`)
	trailRe  = regexp.MustCompile(`(?i)трейл|trail`)
	poolRe   = regexp.MustCompile(`(?i)индор|спринт|минуты|бассейн`)
)

// Source — источник reg.place: страницы списка, затем страница каждого
// отобранного события.
type Source struct {
	f     *ingest.Fetcher
	site  string
	delay time.Duration
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, site: Site, delay: CrawlDelay} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// card — отобранная карточка списка.
type card struct {
	slug  string
	sport event.Sport
	reg   bool
}

// Fetch загружает страницы списка по ссылке «следующая» (не больше
// maxPages) и страницу каждого отобранного события. Вид спорта и открытая
// регистрация передаются в Parse фрагментом адреса («#running+reg»). Ошибка
// любой страницы — ошибка прогона.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	var docs []ingest.RawDoc
	var cards []card
	next := s.site + "/events"
	for page := 1; next != ""; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("в списке больше %d страниц", maxPages)
		}
		doc, err := s.f.Get(ctx, next, s.delay)
		if err != nil {
			return nil, fmt.Errorf("список, страница %d: %w", page, err)
		}
		cs, n, err := listCards(doc.Body)
		if err != nil {
			return nil, fmt.Errorf("список, страница %d: %w", page, err)
		}
		docs, cards = append(docs, doc), append(cards, cs...)
		next = ""
		if n != "" {
			next = s.site + n
		}
	}
	for _, c := range cards {
		frag := string(c.sport)
		if c.reg {
			frag += "+reg"
		}
		doc, err := s.f.Get(ctx, s.site+"/events/"+c.slug+"#"+frag, s.delay)
		if err != nil {
			return nil, fmt.Errorf("событие %s: %w", c.slug, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// Parse разбирает документ (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

// Parse разбирает документ. Чистая функция.
//   - Страница списка записей не даёт: проверяется только её структура.
//   - Страница события — одна запись: дата из div.event-page__date
//     («24.10.2026», «30.10–01.11.2026»), название из h1, место из
//     p.event-page__location-text («Город. адрес, область»), дистанции из
//     карточек race-card («21,0 км», «430,0 м»). Вид спорта и открытая
//     регистрация — из фрагмента адреса, который ставит Fetch.
//   - external_id — slug события.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	u, err := url.Parse(doc.URL)
	if err != nil {
		return nil, fmt.Errorf("адрес документа: %w", err)
	}
	m := slugRe.FindStringSubmatch(u.Path)
	if m == nil {
		_, _, err := listCards(doc.Body)
		return nil, err
	}
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	sportCode, reg, _ := strings.Cut(u.Fragment, "+")
	sport := event.Sport(sportCode)
	rec := ingest.SourceEvent{ExternalID: m[1], URL: Site + "/events/" + m[1], Country: "RU"}
	if reg == "reg" {
		rec.RegistrationURL = rec.URL + "/registration"
	}
	var date, place string
	var dists []string
	for n := range root.Descendants() {
		switch {
		case hasClass(n, "event-page__date"):
			date = text(n)
		case n.DataAtom == atom.H1 && rec.Title == "":
			rec.Title = text(n)
		case hasClass(n, "event-page__location-text"):
			place = text(n)
		case hasClass(n, "race-card__meta-item"):
			if d, ok := raceDistance(n); ok {
				dists = append(dists, d)
			}
		}
	}
	dm := dateRe.FindStringSubmatch(date)
	if dm == nil || rec.Title == "" {
		return nil, fmt.Errorf("%w: событие %s: нет даты или названия", ingest.ErrStructureNotFound, m[1])
	}
	rec.Date = dm[1] + "." + dm[2]
	if dm[3] != "" {
		rec.EndDate = dm[3]
		if dm[2] == "" {
			// «31.12–02.01.2027»: месяц начала позже месяца конца — прошлый год.
			year, _ := strconv.Atoi(dm[3][6:])
			if dm[1][3:] > dm[3][3:5] {
				year--
			}
			rec.Date = fmt.Sprintf("%s.%d", dm[1], year)
		}
	}
	rec.Distances = strings.Join(dists, ", ")
	city, rest, _ := strings.Cut(place, ". ")
	rec.City = ingest.PlaceCity(city)
	for part := range strings.SplitSeq(rest, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "Беларусь") {
			rec.Country = "BY"
		}
		if _, k := normalize.ClassifyRegion(part); k != normalize.RegionUnknown && rec.Region == "" && part != rec.City {
			rec.Region = part
		}
	}
	if rec.City != place {
		rec.Address = place
	}
	switch sport {
	case event.SportRunning:
		if trailRe.MatchString(rec.Title) {
			rec.TypeLabel = "трейл"
		}
	case event.SportSkiing:
		rec.Sport, rec.TypeLabel = string(sport), "Лыжные гонки"
	case event.SportSwimming:
		rec.Sport, rec.TypeLabel = string(sport), rec.Title
		if poolRe.MatchString(rec.Title) {
			rec.TypeLabel = "бассейн"
		}
	case event.SportCycling, event.SportMultisport:
		rec.Sport, rec.TypeLabel = string(sport), rec.Title
	default:
		return nil, fmt.Errorf("событие %s: неизвестный вид спорта %q", m[1], sportCode)
	}
	return []ingest.SourceEvent{rec}, nil
}

// raceDistance — «Дистанция 21,0 км» → «21 км»; меньше 10 м — заглушка
// организатора («1,0 м»).
func raceDistance(item *html.Node) (string, bool) {
	var label, value string
	for c := range item.ChildNodes() {
		if c.DataAtom != atom.Span {
			continue
		}
		if hasClass(c, "race-card__meta-label") {
			label = text(c)
		} else {
			value = text(c)
		}
	}
	m := distRe.FindStringSubmatch(value)
	if label != "Дистанция" || m == nil {
		return "", false
	}
	num := strings.TrimSuffix(m[1], ",0")
	if m[2] == "м" && len(num) < 2 {
		return "", false
	}
	return strings.ReplaceAll(num, ",", ".") + " " + m[2], true
}

// listCards — отобранные карточки страницы списка и ссылка на следующую
// страницу ("" — последняя). Пропускаются карточки без известного вида
// спорта, кемпы и онлайн-старты. Нет ни одной карточки —
// ingest.ErrStructureNotFound.
func listCards(body []byte) ([]card, string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("разбор HTML: %w", err)
	}
	var out []card
	var next string
	total := 0
	for n := range root.Descendants() {
		if n.DataAtom == atom.A && attr(n, "rel") == "next" && hasClass(n, "page-link") {
			next = attr(n, "href")
		}
		if n.DataAtom != atom.Article || !hasClass(n, "b-event-card") {
			continue
		}
		total++
		if c, ok := parseCard(n); ok {
			out = append(out, c)
		}
	}
	if total == 0 {
		return nil, "", fmt.Errorf("%w: нет карточек b-event-card", ingest.ErrStructureNotFound)
	}
	return out, next, nil
}

func parseCard(n *html.Node) (card, bool) {
	c := card{reg: hasClass(n, "b-event-card--registration-open")}
	var title, meta string
	for d := range n.Descendants() {
		switch {
		case hasClass(d, "b-event-card__title-link"):
			title = text(d)
			if u, err := url.Parse(attr(d, "href")); err == nil {
				if m := slugRe.FindStringSubmatch(u.Path); m != nil {
					c.slug = m[1]
				}
			}
		case hasClass(d, "b-event-card__meta"):
			meta = text(d)
		}
	}
	parts := strings.Split(meta, "•")
	names := strings.Split(parts[len(parts)-1], ",")
	for _, name := range names {
		if strings.TrimSpace(name) == "Кемп" {
			return card{}, false
		}
	}
	for _, name := range names {
		if sp, ok := sports[strings.TrimSpace(name)]; ok {
			c.sport = sp
			break
		}
	}
	if c.slug == "" || c.sport == "" || len(parts) < 2 || onlineRe.MatchString(title) {
		return card{}, false
	}
	return c, true
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
	if n.Type != html.ElementNode {
		return false
	}
	for c := range strings.FieldsSeq(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
