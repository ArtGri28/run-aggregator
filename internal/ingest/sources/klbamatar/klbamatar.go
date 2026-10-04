// Package klbamatar — источник КЛБ «Аматар» (klbamatar.by): блок «Ближайшие
// пробеги» с датой, местом и ссылкой на положение. Горизонт — около месяца;
// дистанций блок не даёт. Структура страницы — testdata/README.md.
package klbamatar

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "klbamatar"
	// PageURL — единственная страница прогона: блок есть на каждой странице сайта.
	PageURL = "https://klbamatar.by/"
	// CrawlDelay — robots.txt сайта не задаёт Crawl-delay; запрос один за прогон.
	CrawlDelay = 10 * time.Second
	// country — сайт ведёт календарь пробегов Беларуси; зарубежные — в
	// отдельном разделе, в блок не попадают.
	country = "BY"
	zone    = "Europe/Minsk"
	// placeLabel — подпись места в карточке блока.
	placeLabel = "Место проведения:"
	// cancelMark — отменённый пробег записи не даёт (как D-15 ПроБЕГа).
	cancelMark = "отмен"
	// maxURLLen — предел длины ссылки из источника.
	maxURLLen = 2048
)

// Source — источник КЛБ «Аматар»: один GET PageURL за прогон.
type Source struct {
	f     *ingest.Fetcher
	url   string
	delay time.Duration
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: PageURL, delay: CrawlDelay} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse; запись без страны — вне каталога.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает страницу с блоком.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, s.delay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает страницу (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

// Parse разбирает блок «Ближайшие пробеги» (div#position): каждая карточка
// div.position даёт одну запись. Чистая функция: без сети, файлов и часов.
// external_id — путь ссылки на положение; дата «03 октября 2026» —
// dd.mm.yyyy, нераспознанная передаётся как есть (запись отбракует
// ingest.Prepare); город — ingest.PlaceCity по строке места, сама строка — в
// address. Пробег с «отмен…» в названии записи не даёт. Нет блока —
// ingest.ErrStructureNotFound; блок без карточек — записей нет.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	var block *html.Node
	for n := range root.Descendants() {
		if n.Type == html.ElementNode && n.DataAtom == atom.Div && attr(n, "id") == "position" {
			block = n
			break
		}
	}
	if block == nil {
		return nil, fmt.Errorf("%w: нет блока div#position", ingest.ErrStructureNotFound)
	}
	var out []ingest.SourceEvent
	for card := range block.ChildNodes() {
		if card.Type != html.ElementNode || card.DataAtom != atom.Div || attr(card, "class") != "position" {
			continue
		}
		var ev ingest.SourceEvent
		var loc string
		for c := range card.ChildNodes() {
			switch {
			case c.Type == html.ElementNode && c.DataAtom == atom.Span:
				ev.Date = ingest.RuDate(text(c))
			case c.Type == html.ElementNode && c.DataAtom == atom.B && text(c) == placeLabel && c.NextSibling != nil && c.NextSibling.Type == html.TextNode:
				loc = clean(c.NextSibling.Data)
			case c.Type == html.ElementNode && c.DataAtom == atom.A:
				ev.Title = text(c)
				ev.ExternalID, ev.URL = link(attr(c, "href"), doc.URL)
			}
		}
		if strings.Contains(strings.ToLower(ev.Title), cancelMark) {
			continue
		}
		ev.Country, ev.Timezone = country, zone
		if ev.City = ingest.PlaceCity(loc); ev.City != loc {
			ev.Address = loc
		}
		out = append(out, ev)
	}
	return out, nil
}

// link — external_id (путь) и абсолютный адрес ссылки на положение на том же
// сайте; иначе пусто (запись без external_id отбракует Prepare).
func link(href, pageURL string) (id, abs string) {
	base, err1 := url.Parse(pageURL)
	u, err2 := url.Parse(strings.TrimSpace(href))
	if err1 != nil || err2 != nil || href == "" {
		return "", ""
	}
	u = base.ResolveReference(u)
	if u.Host != base.Host || u.Path == "" || u.Path == "/" || len(u.String()) > maxURLLen {
		return "", ""
	}
	return u.Path, u.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

// text — текст узла, пробелы схлопнуты.
func text(n *html.Node) string {
	var b strings.Builder
	for c := range n.Descendants() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return clean(b.String())
}

// clean схлопывает пробелы и удаляет невалидный UTF-8 и U+0000.
func clean(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")), " ")
}
