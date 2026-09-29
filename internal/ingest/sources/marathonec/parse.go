// Package marathonec — источник «Марафонец» (marathonec.ru/calendar-beg/):
// одна страница с таблицей плагина Posts Table Pro, все строки в HTML.
// Карта столбцов — testdata/README.md.
package marathonec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"aggregator/internal/ingest"
)

const (
	rowIDPrefix = "post-row-"
	// maxURLLen — предел длины ссылки из источника (T-03-07).
	maxURLLen = 2048
	ownHost   = "marathonec.ru"
)

// Столбцы таблицы по атрибуту data-name у <th>; все обязательны: без
// любого из них страница считается сломанной (D-02).
const (
	colDate     = "cf_racedate"
	colTitle    = "title"
	colDistance = "tax_distance"
	colCity     = "tax_city"
	colRegion   = "hf_tax_oblast"
	colType     = "hf_tax_racetype"
)

var requiredCols = []string{colDate, colTitle, colDistance, colCity, colRegion, colType}

// Parse разбирает страницу календаря. Чистая функция: без сети, файлов
// и часов. Каждая строка <tr id="post-row-<id>"> даёт ровно одну запись;
// недостающее поле — пустая строка, отбраковка — в ingest.Prepare.
// Нет таблицы с нужными столбцами, словаря областей или строк —
// ingest.ErrStructureNotFound (D-02).
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	table, cols := findTable(root)
	if table == nil {
		return nil, fmt.Errorf("%w: нет таблицы со столбцами %s", ingest.ErrStructureNotFound, strings.Join(requiredCols, ", "))
	}
	regions, err := regionNames(attr(table, "data-filters"))
	if err != nil {
		return nil, err
	}
	var out []ingest.SourceEvent
	for n := range table.Descendants() {
		if isElem(n, atom.Tr) && strings.HasPrefix(attr(n, "id"), rowIDPrefix) {
			out = append(out, parseRow(n, cols, regions, doc.URL))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: нет строк %s<id>", ingest.ErrStructureNotFound, rowIDPrefix)
	}
	return out, nil
}

// findTable возвращает первую таблицу, в заголовке которой есть все
// обязательные столбцы, и индексы столбцов по data-name.
func findTable(root *html.Node) (*html.Node, map[string]int) {
	for t := range root.Descendants() {
		if !isElem(t, atom.Table) {
			continue
		}
		cols := map[string]int{}
		i := 0
		for th := range t.Descendants() {
			if isElem(th, atom.Th) {
				cols[attr(th, "data-name")] = i
				i++
			}
		}
		ok := true
		for _, c := range requiredCols {
			if _, has := cols[c]; !has {
				ok = false
			}
		}
		if ok {
			return t, cols
		}
	}
	return nil, nil
}

// regionNames читает словарь «слаг области → название» из JSON
// атрибута data-filters таблицы (D-14): это единственное место на
// странице, где у слага области есть русское название.
func regionNames(filters string) (map[string]string, error) {
	var f map[string]struct {
		Terms []struct{ Name, Slug string }
	}
	if err := json.Unmarshal([]byte(filters), &f); err != nil || len(f["tax_oblast"].Terms) == 0 {
		return nil, fmt.Errorf("%w: нет словаря областей в data-filters", ingest.ErrStructureNotFound)
	}
	m := make(map[string]string, len(f["tax_oblast"].Terms))
	for _, t := range f["tax_oblast"].Terms {
		m[t.Slug] = strings.Join(strings.Fields(clean(t.Name)), " ")
	}
	return m, nil
}

func parseRow(tr *html.Node, cols map[string]int, regions map[string]string, pageURL string) ingest.SourceEvent {
	var cells []*html.Node
	for c := range tr.ChildNodes() {
		if isElem(c, atom.Td) {
			cells = append(cells, c)
		}
	}
	cell := func(name string) *html.Node {
		if i := cols[name]; i < len(cells) {
			return cells[i]
		}
		return nil
	}

	ev := ingest.SourceEvent{
		ExternalID: externalID(attr(tr, "id")),
		Title:      text(cell(colTitle)),
		Date:       text(cell(colDate)),
		Distances:  text(cell(colDistance)),
		City:       text(cell(colCity)),
		TypeLabel:  text(cell(colType)),
	}
	// D-14: одна область — её название; несколько — Region пуст, все
	// слаги и названия остаются в RegionSlugs/Regions.
	for _, slug := range strings.Fields(text(cell(colRegion))) {
		name := regions[slug]
		if name == "" {
			name = slug
		}
		ev.RegionSlugs = append(ev.RegionSlugs, slug)
		ev.Regions = append(ev.Regions, name)
	}
	if len(ev.Regions) == 1 {
		ev.Region = ev.Regions[0]
	}
	ev.URL, ev.RegistrationURL = links(titleHref(cell(colTitle)), pageURL)
	return ev
}

// externalID — цифры после post-row-; иначе "" (запись отбракует Prepare).
func externalID(id string) string {
	s := strings.TrimPrefix(id, rowIDPrefix)
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return ""
	}
	return s
}

// titleHref — href первой ссылки в ячейке названия: другой ссылки в
// строке у Марафонца нет (D-13).
func titleHref(td *html.Node) string {
	if td == nil {
		return ""
	}
	for a := range td.Descendants() {
		if isElem(a, atom.A) && attr(a, "href") != "" {
			return attr(a, "href")
		}
	}
	return ""
}

// links раскладывает ссылку названия по D-13: своя страница события на
// marathonec.ru — URL события, регистрации нет; внешняя ссылка —
// registration_url, а URL события — страница календаря. Ссылка не
// http/https, без хоста или длиннее maxURLLen отбрасывается.
func links(href, pageURL string) (eventURL, registrationURL string) {
	u, ok := absURL(href, pageURL)
	if !ok {
		return pageURL, ""
	}
	host := strings.ToLower(u.Hostname())
	if host == ownHost || strings.HasSuffix(host, "."+ownHost) {
		return u.String(), ""
	}
	return pageURL, u.String()
}

// absURL: сущности уже раскодированы парсером (&#038; → &), маркер
// плагина #new_tab убирается, относительная ссылка разрешается от
// страницы. Абсолютная ссылка остаётся как есть (vk.com/away.php?to=…).
func absURL(href, pageURL string) (*url.URL, bool) {
	href = strings.TrimSuffix(clean(strings.TrimSpace(href)), "#new_tab")
	u, err := url.Parse(href)
	if href == "" || err != nil {
		return nil, false
	}
	if !u.IsAbs() {
		base, err := url.Parse(pageURL)
		if err != nil {
			return nil, false
		}
		u = base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(u.String()) > maxURLLen {
		return nil, false
	}
	return u, true
}

func isElem(n *html.Node, a atom.Atom) bool {
	return n.Type == html.ElementNode && n.DataAtom == a
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

// text — текст узла без script/style, <br> = пробел, пробельные символы
// (включая NBSP) схлопнуты, края обрезаны, затем clean.
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for c := range n.Descendants() {
		switch {
		case c.Type == html.TextNode && c.Parent.DataAtom != atom.Script && c.Parent.DataAtom != atom.Style:
			b.WriteString(c.Data)
		case isElem(c, atom.Br):
			b.WriteByte(' ')
		}
	}
	return clean(strings.Join(strings.Fields(b.String()), " "))
}

// clean удаляет невалидный UTF-8 и U+0000: PostgreSQL не принимает их
// в TEXT и JSONB (IN-04).
func clean(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
}
