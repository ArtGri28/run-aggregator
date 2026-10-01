// Package probeg — источник ПроБЕГ (probeg.org/races/year_ahead/): общий
// календарь на год вперёд одной таблицей, все строки в HTML. Страну
// определяет Parse по строке места и справочнику городов РФ, политика —
// no_fallback (D-01). Структура страницы — testdata/README.md.
package probeg

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// eventBase — URL страницы события строится из числового id, а не из
	// ссылки страницы (T-05-15).
	eventBase = "https://probeg.org/event/"
	// maxURLLen — предел длины ссылки из источника (T-03-07).
	maxURLLen = 2048
	// rowStyle — признак строки данных; заголовок — <tr class="info">.
	rowStyle   = "font-size: 11px"
	headerCols = 6
	cancelled  = "Забег отменён!"
	regLink    = "Регистрация"
	// country — страна записи, определённой как российская (CountryCode = RU).
	country = "Россия"
)

// Столбцы строки данных: №, Дата, Название, Город, Дистанции, Ссылки.
// У групповой строки без города ячейка названия занимает два столбца
// (colspan="2"), ячеек пять.
const (
	colDate = 1 + iota
	colTitle
	colCity
	colDistance
	colLinks
)

var (
	// participantsRe — ссылка-счётчик «N человек/человека» на страницу
	// того же события: не название и не отдельное событие (Pitfall 2).
	participantsRe = regexp.MustCompile(`^\s*\d+\s+человек`)
	// eventHrefRe — ссылка на страницу события /event/<n>/; вложенные
	// страницы (/event/<n>/reg/ — кнопка регистрации) — не события.
	eventHrefRe = regexp.MustCompile(`^/event/([^/]+)/$`)
	// dateRe — dd.mm, dd–dd.mm, dd.mm–dd.mm; тире —, – или дефис.
	dateRe = regexp.MustCompile(`^(\d{1,2})(?:\.(\d{1,2}))?(?:\s*[–—-]\s*(\d{1,2})\.(\d{1,2}))?$`)
	// nonRunning — неходовые виды: фрагмент дистанций с таким словом
	// отбрасывается целиком (Pitfall 3).
	nonRunning = []string{"ходьба", "триатлон", "прыжок", "толкание", "каникросс"}
	// federalCities — субъекты-города: город групповой строки с таким
	// первым компонентом места — сам этот город (Pitfall 4).
	federalCities = map[string]bool{"Москва": true, "Санкт-Петербург": true, "Севастополь": true}
)

// Parse разбирает календарь на год вперёд. Чистая функция: без сети,
// файлов и часов, год берётся из doc.FetchedAt.
//   - D-13: год даты dd.mm — первый, при котором дата не раньше
//     FetchedAt − 1 день; Date/EndDate — dd.mm.yyyy.
//   - D-15: строки «Забег отменён!» записей не дают.
//   - D-03: групповая строка (≥ 2 ссылок на события) — запись на каждую
//     ссылку с GroupKey серии; одиночная строка — одна запись без ключа.
//   - D-01: страна — по первому компоненту места или справочнику городов
//     РФ для голого города; иначе Country пусто (no_fallback).
//   - D-14: серия без города — City, Region, Country пусты.
//
// Нет таблицы с заголовком из 6 столбцов или строк данных —
// ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	if doc.FetchedAt.IsZero() {
		return nil, errors.New("probeg: нет времени загрузки страницы (FetchedAt)")
	}
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	table := findTable(root)
	if table == nil {
		return nil, fmt.Errorf("%w: нет таблицы с заголовком <tr class=\"info\"> из %d столбцов", ingest.ErrStructureNotFound, headerCols)
	}
	var out []ingest.SourceEvent
	rows := 0
	for tr := range table.Descendants() {
		if !isElem(tr, atom.Tr) || !strings.Contains(attr(tr, "style"), rowStyle) {
			continue
		}
		cells := rowCells(tr)
		if cells == nil {
			continue
		}
		rows++
		out = append(out, parseRow(cells, doc)...)
	}
	if rows == 0 {
		return nil, fmt.Errorf("%w: нет строк данных <tr style=\"%s\">", ingest.ErrStructureNotFound, rowStyle)
	}
	return out, nil
}

// findTable — первая таблица со строкой заголовка <tr class="info">
// ровно из headerCols ячеек <th>.
func findTable(root *html.Node) *html.Node {
	for t := range root.Descendants() {
		if !isElem(t, atom.Table) {
			continue
		}
		for tr := range t.Descendants() {
			if !isElem(tr, atom.Tr) || !hasClass(tr, "info") {
				continue
			}
			th := 0
			for c := range tr.ChildNodes() {
				if isElem(c, atom.Th) {
					th++
				}
			}
			if th == headerCols {
				return t
			}
		}
	}
	return nil
}

// rowCells — ячейки строки по столбцам colDate…colLinks; у групповой
// строки без города ячейка города — nil. Другое число ячеек — nil.
func rowCells(tr *html.Node) []*html.Node {
	var tds []*html.Node
	for c := range tr.ChildNodes() {
		if isElem(c, atom.Td) {
			tds = append(tds, c)
		}
	}
	switch {
	case len(tds) == headerCols:
		return tds
	case len(tds) == headerCols-1 && attr(tds[colTitle], "colspan") == "2":
		return []*html.Node{tds[0], tds[colDate], tds[colTitle], nil, tds[colDistance-1], tds[colLinks-1]}
	}
	return nil
}

type link struct{ id, title string }

func parseRow(cells []*html.Node, doc ingest.RawDoc) []ingest.SourceEvent {
	titleTD := cells[colTitle]
	for n := range titleTD.Descendants() {
		if isElem(n, atom.Strong) && text(n) == cancelled {
			return nil // D-15
		}
	}
	links := titleLinks(titleTD)
	group := len(links) > 1
	if len(links) == 0 {
		// Строка без ссылки на событие не теряется молча: Prepare
		// отбракует её как запись без external_id.
		links = []link{{title: text(titleTD)}}
	}

	date, end := parseDate(text(cells[colDate]), doc.FetchedAt)
	var region, city, cntry, address string
	if cells[colCity] != nil {
		region, city, cntry, address = resolvePlace(text(cells[colCity]), group)
	}
	base := ingest.SourceEvent{
		Date:            date,
		EndDate:         end,
		Distances:       distances(cells[colDistance]),
		City:            city,
		Region:          region,
		Country:         cntry,
		Address:         address,
		RegistrationURL: registrationURL(cells[colLinks], doc.URL),
	}
	out := make([]ingest.SourceEvent, 0, len(links))
	for _, l := range links {
		ev := base
		ev.ExternalID, ev.Title = l.id, l.title
		if l.id != "" {
			ev.URL = eventBase + l.id + "/"
		}
		if group {
			ev.GroupKey = "series:" + normalize.TitleNorm(l.title)
		}
		out = append(out, ev)
	}
	return out
}

// titleLinks — ссылки /event/<n>/ ячейки названия, кроме счётчиков
// участников. id — <n> из цифр, иначе "" (запись отбракует Prepare).
func titleLinks(td *html.Node) []link {
	var out []link
	for a := range td.Descendants() {
		m := eventHrefRe.FindStringSubmatch(attr(a, "href"))
		if !isElem(a, atom.A) || m == nil {
			continue
		}
		t := text(a)
		if participantsRe.MatchString(t) {
			continue
		}
		id := m[1]
		if strings.Trim(id, "0123456789") != "" {
			id = ""
		}
		out = append(out, link{id: id, title: t})
	}
	return out
}

// parseDate переводит дату ячейки в dd.mm.yyyy по fetched (D-13,
// Pitfall 1). Конец диапазона — в году начала, а если он раньше начала —
// в следующем. Нераспознанная форма — сырой текст в date (Prepare
// отбракует); неверная дата конца — end пуст.
func parseDate(s string, fetched time.Time) (date, end string) {
	m := dateRe.FindStringSubmatch(s)
	if m == nil || (m[2] == "" && m[3] == "") {
		return s, ""
	}
	sd, sm := atoi(m[1]), atoi(m[2])
	if m[2] == "" { // dd–dd.mm: месяц начала = месяц конца
		sm = atoi(m[4])
	}
	start, ok := yearDate(sd, sm, fetched)
	if !ok {
		return s, ""
	}
	date = start.Format("02.01.2006")
	if m[3] == "" {
		return date, ""
	}
	ed, em := atoi(m[3]), atoi(m[4])
	for _, y := range []int{start.Year(), start.Year() + 1} {
		if e, ok := mkDate(y, em, ed); ok && !e.Before(start) {
			return date, e.Format("02.01.2006")
		}
	}
	return date, ""
}

// yearDate — первая из дат d.m в годах FetchedAt и следующем, которая не
// раньше FetchedAt − 1 день (UTC).
func yearDate(d, m int, fetched time.Time) (time.Time, bool) {
	y, fm, fd := fetched.UTC().Date()
	minDate := time.Date(y, fm, fd-1, 0, 0, 0, 0, time.UTC)
	for _, year := range []int{y, y + 1} {
		if t, ok := mkDate(year, m, d); ok && !t.Before(minDate) {
			return t, true
		}
	}
	return time.Time{}, false
}

// mkDate — дата без нормализации: 31.02 или месяц 13 → false.
func mkDate(y, m, d int) (time.Time, bool) {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t, t.Month() == time.Month(m) && t.Day() == d
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// resolvePlace — область, город, страна и уточнение места по строке места
// «Страна или субъект, …, населённый пункт» (D-01, Pattern 1):
//   - первый компонент — страна с ISO-кодом не РФ (справочник normalize):
//     Country = компонент, Region = второй компонент (область) при трёх и
//     более компонентах, address = компоненты между областью и населённым
//     пунктом (район). Входит ли страна в каталог, решает Prepare;
//   - иностранная страна без ISO-кода: Region = компонент, Country пусто;
//   - субъект РФ: Region = каноническое имя, Country «Россия»; у групповой
//     строки с городом федерального значения город — он сам (Pitfall 4);
//   - один компонент (голый город): «Россия» только по normalize.IsRFCity;
//   - несколько компонентов с нераспознанным первым: Region = компонент,
//     Country пусто — страна не определена, запись видна как нераспознанная.
func resolvePlace(raw string, group bool) (region, city, cntry, address string) {
	var parts []string
	for p := range strings.SplitSeq(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "", "", "", ""
	}
	city = normalize.City(parts[len(parts)-1])
	canon, kind := normalize.ClassifyRegion(parts[0])
	switch code := normalize.CountryCode(parts[0]); {
	case code != "" && code != "RU":
		if len(parts) > 2 {
			region = parts[1]
			address = strings.Join(parts[2:len(parts)-1], ", ")
		}
		return region, city, parts[0], address
	case kind == normalize.RegionForeign:
		return parts[0], city, "", ""
	case kind == normalize.RegionRF:
		if group && federalCities[canon] {
			city = canon
		}
		return canon, city, country, ""
	case len(parts) > 1:
		return parts[0], city, "", ""
	case normalize.IsRFCity(city):
		return "", city, country, ""
	}
	return "", city, "", ""
}

// distances — фрагменты ячейки (span.nobr, а без них — текст между <br>),
// каждый до первой «(», без неходовых видов, через ", " (Pitfall 3).
func distances(td *html.Node) string {
	var frags []string
	for n := range td.Descendants() {
		if isElem(n, atom.Span) && hasClass(n, "nobr") {
			frags = append(frags, text(n))
		}
	}
	if frags == nil {
		frags = brLines(td)
	}
	var out []string
	for _, f := range frags {
		f, _, _ = strings.Cut(f, "(")
		f = itraRe.ReplaceAllString(f, "")
		if f = strings.TrimSpace(f); f != "" && !isNonRunning(f) {
			out = append(out, namedToMeters(f))
		}
	}
	return strings.Join(out, ", ")
}

// itraRe — баллы ITRA перед дистанцией («ITRA 4 100 км»): иначе число баллов
// склеивается с дистанцией и даёт фиктивные 4 и 100 км.
var itraRe = regexp.MustCompile(`(?i)^\s*ITRA\s+\d+\s+`)

// namedToMeters переводит элемент списка без цифр («марафон», «полумарафон»)
// в метры: normalize.Distances берёт слово, только если в строке нет числовых
// дистанций, а Prepare разбирает весь список одной строкой.
func namedToMeters(f string) string {
	if strings.ContainsAny(f, "0123456789") {
		return f
	}
	if d := normalize.Distances(f); len(d) == 1 {
		return strconv.Itoa(d[0]) + " м"
	}
	return f
}

func isNonRunning(s string) bool {
	s = strings.ToLower(s)
	for _, w := range nonRunning {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// brLines — текст узла, разделённый на <br>; пустые части выброшены.
func brLines(n *html.Node) []string {
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
		case isElem(c, atom.Br):
			flush()
		}
	}
	flush()
	return out
}

// registrationURL — ссылка первой <a> с текстом «Регистрация»; «Сайт»,
// «Положение» и соцсети не используются.
func registrationURL(td *html.Node, pageURL string) string {
	for a := range td.Descendants() {
		if isElem(a, atom.A) && text(a) == regLink {
			if u, ok := absURL(attr(a, "href"), pageURL); ok {
				return u.String()
			}
			return ""
		}
	}
	return ""
}

// absURL: относительная ссылка разрешается от страницы; только http/https
// с хостом и не длиннее maxURLLen.
func absURL(href, pageURL string) (*url.URL, bool) {
	href = clean(strings.TrimSpace(href))
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

func hasClass(n *html.Node, class string) bool {
	for c := range strings.FieldsSeq(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
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
