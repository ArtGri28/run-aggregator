// Package runin — источник RunIn (runin.by), платформа регистрации Белорусской
// федерации лёгкой атлетики: блок «Ближайшие события» главной страницы.
// Полный список событий сайт подгружает скриптом и здесь не используется;
// года и дистанций блок не даёт. Структура страницы — testdata/README.md.
package runin

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "runin"
	// PageURL — единственная страница прогона.
	PageURL = "https://runin.by/"
	// CrawlDelay — robots.txt сайта не задаёт Crawl-delay; запрос один за прогон.
	CrawlDelay = 10 * time.Second
	// eventBase — URL страницы события (она же страница регистрации)
	// строится из кода события.
	eventBase = "https://runin.by/event/"
	// country — площадка белорусской федерации; страны в карточке нет.
	country = "BY"
	zone    = "Europe/Minsk"
	// cancelMark — отменённое событие записи не даёт (как D-15 ПроБЕГа).
	cancelMark = "отмен"
)

// codeRe — ссылка карточки /event/<код>.
var codeRe = regexp.MustCompile(`^/event/([A-Za-z0-9_-]{1,100})/?$`)

// Source — источник RunIn: один GET PageURL за прогон.
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

// Fetch загружает главную страницу.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, s.delay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает страницу (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

// Parse разбирает блок «Ближайшие события» (.events__list): каждая карточка
// .events__item даёт одну запись. Чистая функция: без сети, файлов и часов.
// external_id — код события из ссылки /event/<код>; дата «10 октября» — dd.mm
// (год выбирает normalize.Date), нераспознанная передаётся как есть (запись
// отбракует ingest.Prepare); город — ingest.PlaceCity по строке места, сама
// строка — в address. Событие с «отмен…» в названии записи не даёт. Нет
// блока — ingest.ErrStructureNotFound; блок без карточек — записей нет.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	list := find(root, "events__list")
	if list == nil {
		return nil, fmt.Errorf("%w: нет блока .events__list", ingest.ErrStructureNotFound)
	}
	var out []ingest.SourceEvent
	for card := range list.Descendants() {
		if card.Type != html.ElementNode || !hasClass(card, "events__item") {
			continue
		}
		ev := ingest.SourceEvent{Country: country, Timezone: zone}
		if n := find(card, "events__item-date"); n != nil {
			ev.Date = ingest.RuDate(text(n))
		}
		if n := find(card, "events__item-title"); n != nil {
			ev.Title = text(n)
			if m := codeRe.FindStringSubmatch(attr(n, "href")); m != nil {
				ev.ExternalID, ev.URL = m[1], eventBase+m[1]
				ev.RegistrationURL = ev.URL
			}
		}
		if strings.Contains(strings.ToLower(ev.Title), cancelMark) {
			continue
		}
		if n := find(card, "events__item-location"); n != nil {
			loc := text(n)
			if ev.City = ingest.PlaceCity(loc); ev.City != loc {
				ev.Address = loc
			}
		}
		out = append(out, ev)
	}
	return out, nil
}

// find — первый потомок-элемент с классом class.
func find(n *html.Node, class string) *html.Node {
	for c := range n.Descendants() {
		if c.Type == html.ElementNode && hasClass(c, class) {
			return c
		}
	}
	return nil
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

// text — текст узла: пробелы схлопнуты, невалидный UTF-8 и U+0000 удалены.
func text(n *html.Node) string {
	var b strings.Builder
	for c := range n.Descendants() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ToValidUTF8(b.String(), ""), "\x00", "")), " ")
}
