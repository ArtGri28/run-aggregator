// Package cyclingrace — источник CyclingRace (cyclingrace.ru/racers):
// ближайшие велогонки организатора в Москве. Карточка гонки даёт название,
// дату и место; структура — testdata/README.md.
package cyclingrace

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
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
	Code = "cyclingrace"
	// ListURL — страница ближайших гонок.
	ListURL = "https://cyclingrace.ru/racers"
	// CrawlDelay — пауза между запросами к хосту.
	CrawlDelay = 5 * time.Second
	base       = "https://cyclingrace.ru/"
	// listTitle — заголовок блока гонок на странице.
	listTitle = "Ближайшие гонки"
)

var dateRe = regexp.MustCompile(`^\d{2}\.\d{2}\.\d{4}$`)

// Source — источник CyclingRace: один GET страницы гонок за прогон.
type Source struct {
	f   *ingest.Fetcher
	url string
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: ListURL} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse: гонки организатора в России.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает страницу гонок.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает страницу гонок (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

// Parse разбирает страницу гонок. Чистая функция. Гонка — ссылка <a> на
// страницу гонки (относительный адрес без «/»), в которой три абзаца:
// название, дата дд.мм.гггг и место. Вид спорта — велоспорт, дисциплина — по
// словам названия (normalize.SportType). Нет заголовка «Ближайшие гонки» —
// ingest.ErrStructureNotFound; заголовок без карточек — пустой список (между
// сезонами).
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	if !bytes.Contains(doc.Body, []byte(listTitle)) {
		return nil, fmt.Errorf("%w: нет заголовка %q", ingest.ErrStructureNotFound, listTitle)
	}
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	var out []ingest.SourceEvent
	for a := range root.Descendants() {
		if a.Type != html.ElementNode || a.DataAtom != atom.A {
			continue
		}
		slug := attr(a, "href")
		if u, err := url.Parse(slug); err != nil || slug == "" || u.IsAbs() || strings.ContainsAny(slug, "/?#") {
			continue
		}
		var ps []string
		for n := range a.Descendants() {
			if n.Type == html.ElementNode && n.DataAtom == atom.P {
				ps = append(ps, text(n))
			}
		}
		if len(ps) != 3 || !dateRe.MatchString(ps[1]) {
			continue
		}
		place := ps[2]
		city := ingest.PlaceCity(place)
		rec := ingest.SourceEvent{
			ExternalID: slug,
			URL:        base + slug,
			Title:      ps[0],
			Date:       ps[1],
			City:       city,
			Country:    "RU",
			TypeLabel:  ps[0],
			Sport:      string(event.SportCycling),
		}
		if place != city {
			rec.Address = place
		}
		out = append(out, rec)
	}
	return out, nil
}

func text(n *html.Node) string {
	var b strings.Builder
	for c := range n.Descendants() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}
