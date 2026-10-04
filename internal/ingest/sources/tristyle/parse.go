// Package tristyle — источник Tristyle.Клуб (tristyle.club/competition/):
// календарь стартов Беларуси, который ведёт Races.by. Список даёт только
// ссылки на события; дата с годом, зона и место — в ссылке «Добавить в
// календарь» на странице события. Структура страниц — testdata/README.md.
package tristyle

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"aggregator/internal/ingest"
)

const (
	// eventBase — URL страницы события строится из числового id.
	eventBase = "https://tristyle.club/competition/"
	// country — страна всех записей: список отфильтрован по Беларуси.
	country = "BY"
	// calendarPath — начало ссылок «Добавить в календарь».
	calendarPath = "/misc/calendar/"
	// cancelMark — отменённое событие записи не даёт (как D-15 ПроБЕГа).
	cancelMark = "отмен"
	// distPrefix — строка текста события с перечнем дистанций.
	distPrefix = "дистанции:"
)

var (
	// idRe — путь страницы события /competition/<n>/.
	idRe     = regexp.MustCompile(`^/competition/(\d+)/$`)
	parensRe = regexp.MustCompile(`\([^)]*\)`)
)

// Parse разбирает один документ прогона. Чистая функция: без сети, файлов
// и часов.
//   - Страница списка записей не даёт; без блока competitions-page —
//     ingest.ErrStructureNotFound.
//   - Страница события (/competition/<n>/) даёт одну запись: external_id —
//     <n>, название, дата, зона и место — из ссылки календаря. Нет ссылки —
//     ingest.ErrStructureNotFound. Неразборчивая дата передаётся как есть
//     (запись отбракует ingest.Prepare). Событие с «отмен…» в названии
//     записи не даёт.
//   - Дистанции — только из строки текста «Дистанции: …», скобки убраны;
//     нет такой строки — пусто.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	u, err := url.Parse(doc.URL)
	if err != nil {
		return nil, fmt.Errorf("адрес документа: %w", err)
	}
	m := idRe.FindStringSubmatch(u.Path)
	if m == nil {
		_, err := listIDs(doc.Body)
		return nil, err
	}
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	q, ok := calendarQuery(root)
	if !ok {
		return nil, fmt.Errorf("%w: событие %s: нет ссылки календаря в блоке post-event-competition", ingest.ErrStructureNotFound, m[1])
	}
	title := clean(strings.Join(strings.Fields(q.Get("title")), " "))
	if strings.Contains(strings.ToLower(title), cancelMark) {
		return nil, nil
	}
	city, address := place(clean(q.Get("location")))
	return []ingest.SourceEvent{{
		ExternalID: m[1],
		URL:        eventBase + m[1] + "/",
		Title:      title,
		Date:       localDate(q.Get("date")),
		Distances:  distances(root),
		City:       city,
		Country:    country,
		Address:    address,
		Timezone:   q.Get("timezone"),
	}}, nil
}

// listIDs — номера событий страницы списка в порядке страницы, без повторов:
// ссылки заголовков карточек (a.post-title-link); ссылки сетки календаря
// не считаются. Пустой список — не ошибка.
func listIDs(body []byte) ([]string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	var ids []string
	page, seen := false, map[string]bool{}
	for n := range root.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		if hasClass(n, "competitions-page") {
			page = true
		}
		if n.DataAtom != atom.A || !hasClass(n, "post-title-link") {
			continue
		}
		if m := idRe.FindStringSubmatch(attr(n, "href")); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	if !page {
		return nil, fmt.Errorf("%w: нет блока competitions-page", ingest.ErrStructureNotFound)
	}
	return ids, nil
}

// calendarQuery — параметры первой ссылки календаря в блоке события.
func calendarQuery(root *html.Node) (url.Values, bool) {
	for b := range root.Descendants() {
		if b.Type != html.ElementNode || !hasClass(b, "post-event-competition") {
			continue
		}
		for a := range b.Descendants() {
			if a.Type != html.ElementNode || a.DataAtom != atom.A {
				continue
			}
			if u, err := url.Parse(attr(a, "href")); err == nil && strings.HasPrefix(u.Path, calendarPath) {
				return u.Query(), true
			}
		}
	}
	return nil, false
}

// localDate — дата старта yyyy-mm-dd: время в ссылке местное (зона идёт
// отдельным параметром), пересчёт не нужен. Неразборчивое — как есть.
func localDate(s string) string {
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// place — город (ingest.PlaceCity) и уточнение места: вся строка, когда в ней
// есть что-то сверх города.
func place(loc string) (city, address string) {
	loc = strings.Join(strings.Fields(loc), " ")
	if city = ingest.PlaceCity(loc); city == loc {
		return city, ""
	}
	return city, loc
}

// distances — остаток первой строки текста события, начинающейся с
// «Дистанции:», без скобок (в них возрасты и описания трасс).
func distances(root *html.Node) string {
	for n := range root.Descendants() {
		if n.Type != html.ElementNode || !hasClass(n, "e-content") {
			continue
		}
		for _, line := range lines(n) {
			if len(line) >= len(distPrefix) && strings.EqualFold(line[:len(distPrefix)], distPrefix) {
				if d := strings.TrimSpace(parensRe.ReplaceAllString(line[len(distPrefix):], " ")); d != "" {
					return d
				}
			}
		}
	}
	return ""
}

// lines — текст узла по строкам: новая строка на <br> и на начале блочного
// элемента; пробелы схлопнуты, пустые строки выброшены.
func lines(n *html.Node) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if s := clean(strings.Join(strings.Fields(b.String()), " ")); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for c := range n.Descendants() {
		switch {
		case c.Type == html.TextNode:
			b.WriteString(c.Data)
		case c.Type == html.ElementNode:
			switch c.DataAtom {
			case atom.Br, atom.P, atom.Li, atom.Div, atom.Ul, atom.Ol, atom.H1, atom.H2, atom.H3, atom.H4:
				flush()
			}
		}
	}
	flush()
	return out
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

// clean удаляет невалидный UTF-8 и U+0000: PostgreSQL не принимает их в
// TEXT и JSONB.
func clean(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
}
