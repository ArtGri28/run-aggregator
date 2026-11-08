// Package probeg — источник ПроБЕГ (probeg.org/races/year_ahead/): календарь
// на год вперёд, разбитый на страницы ?page=N; старт — карточка
// article.calendar-item, паркраны — блоки details.weekly. Страну определяет
// Parse по строке места и справочнику городов РФ, политика — no_fallback
// (D-01). Структура страницы — testdata/README.md.
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
	regLink   = "Регистрация"
	nextLabel = "Следующая страница"
	// country — страна записи, определённой как российская (CountryCode = RU).
	country = "Россия"
	// weeklyDistance — дистанция паркранов: на странице она только в
	// подписи блока («еженедельных забегов на 5 км»).
	weeklyDistance = "5 км"
)

var (
	// eventHrefRe — ссылка на страницу события /event/<n>/.
	eventHrefRe = regexp.MustCompile(`^/event/([^/]+)/$`)
	// nonRunning — неходовые виды: дистанция с таким словом отбрасывается.
	nonRunning = []string{"ходьба", "триатлон", "прыжок", "толкание", "каникросс"}
	// weeklyCities — город паркранов по концу заголовка блока. Блок «в других
	// городах» записей не даёт: без города они вне каталога (D-14), а сотни
	// таких записей подняли бы долю out_of_scope выше порога.
	weeklyCities = map[string]string{"в Москве": "Москва", "в Санкт-Петербурге": "Санкт-Петербург"}
	// itraRe — баллы ITRA перед дистанцией («ITRA 4 100 км»): иначе число баллов
	// склеивается с дистанцией и даёт фиктивные 4 и 100 км.
	itraRe = regexp.MustCompile(`(?i)^\s*ITRA\s+\d+\s+`)
	// timeRe — время старта в конце строки даты: «…, 10:30».
	timeRe = regexp.MustCompile(`,\s*(\d{1,2}:\d{2})$`)
	// dayRe — часть строки даты «d [месяц [год]]».
	dayRe  = regexp.MustCompile(`^(\d{1,2})(?:\s+(\p{L}+))?(?:\s+(\d{4}))?$`)
	months = map[string]time.Month{
		"января": 1, "февраля": 2, "марта": 3, "апреля": 4, "мая": 5, "июня": 6,
		"июля": 7, "августа": 8, "сентября": 9, "октября": 10, "ноября": 11, "декабря": 12,
	}
)

// Parse разбирает одну страницу календаря. Чистая функция: без сети и
// файлов; FetchedAt нужен только паркранам перед первой карточкой.
//   - карточка article.calendar-item — запись; с классом cancelled записи
//     не даёт (D-04);
//   - блок details.weekly — запись на каждую ссылку с GroupKey серии; день
//     из блока, месяц и год — от предыдущей карточки (D-05);
//   - место — «Город · Регион» (D-06), год и время — из строки даты (D-03).
//
// Нет ни карточек, ни блоков паркранов — ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	if doc.FetchedAt.IsZero() {
		return nil, errors.New("probeg: нет времени загрузки страницы (FetchedAt)")
	}
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	f := doc.FetchedAt.UTC()
	last := time.Date(f.Year(), f.Month(), f.Day()-1, 0, 0, 0, 0, time.UTC)
	var out []ingest.SourceEvent
	blocks := 0
	for n := range root.Descendants() {
		switch {
		case isElem(n, atom.Article) && hasClass(n, "calendar-item"):
			blocks++
			ev, start, ok := parseCard(n, doc.URL)
			if !start.IsZero() {
				last = start
			}
			if ok {
				out = append(out, ev)
			}
		case isElem(n, atom.Details) && hasClass(n, "weekly"):
			blocks++
			out = append(out, parseWeekly(n, &last)...)
		}
	}
	if blocks == 0 {
		return nil, fmt.Errorf("%w: нет карточек article.calendar-item", ingest.ErrStructureNotFound)
	}
	return out, nil
}

// parseCard — запись карточки и дата её начала (нулевая, если дата не
// распознана). ok = false у отменённой карточки.
func parseCard(n *html.Node, pageURL string) (ev ingest.SourceEvent, start time.Time, ok bool) {
	var when, meta, planned, links *html.Node
	for c := range n.Descendants() {
		switch {
		case isElem(c, atom.H3) && ev.Title == "":
			ev.Title, ev.ExternalID, ev.URL = eventLink(c)
		case isElem(c, atom.Div) && hasClass(c, "when-line"):
			when = c
		case isElem(c, atom.Div) && hasClass(c, "meta"):
			meta = c
		case isElem(c, atom.Div) && hasClass(c, "planned"):
			planned = c
		case isElem(c, atom.Div) && hasClass(c, "links"):
			links = c
		}
	}
	ev.Date, ev.EndDate, ev.StartTime, start = parseWhen(text(when))
	if meta != nil {
		ev.Region, ev.City, ev.Country = resolvePlace(meta)
	}
	ev.Distances = distances(planned)
	ev.RegistrationURL = registrationURL(links, pageURL)
	return ev, start, !hasClass(n, "cancelled")
}

// eventLink — название, id и URL по первой ссылке узла. id — <n> из цифр в
// /event/<n>/, иначе пусто (запись отбракует Prepare).
func eventLink(n *html.Node) (title, id, link string) {
	for a := range n.Descendants() {
		if !isElem(a, atom.A) {
			continue
		}
		if m := eventHrefRe.FindStringSubmatch(attr(a, "href")); m != nil && strings.Trim(m[1], "0123456789") == "" {
			id, link = m[1], eventBase+m[1]+"/"
		}
		return text(a), id, link
	}
	return text(n), "", ""
}

// parseWhen разбирает «8 ноября 2026, 10:30», «4–6 сентября 2027»,
// «30 ноября – 2 декабря 2026», «30 декабря 2026 – 2 января 2027».
// Нераспознанная строка — сырой текст в date (Prepare отбракует).
func parseWhen(s string) (date, end, startTime string, start time.Time) {
	raw := s
	if m := timeRe.FindStringSubmatch(s); m != nil {
		startTime = m[1]
		s = strings.TrimSpace(s[:len(s)-len(m[0])])
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '–' || r == '—' || r == '-' })
	if len(parts) == 0 || len(parts) > 2 {
		return raw, "", "", time.Time{}
	}
	e, ok := dayMonthYear(parts[len(parts)-1], 0, 0)
	if !ok {
		return raw, "", "", time.Time{}
	}
	if len(parts) == 1 {
		return e.Format("02.01.2006"), "", startTime, e
	}
	b, ok := dayMonthYear(parts[0], e.Month(), e.Year())
	if !ok || b.After(e) {
		return raw, "", "", time.Time{}
	}
	return b.Format("02.01.2006"), e.Format("02.01.2006"), startTime, b
}

// dayMonthYear — дата «d [месяц [год]]»; недостающие месяц и год берутся из
// m и y (нулевые — обязательны в строке).
func dayMonthYear(s string, m time.Month, y int) (time.Time, bool) {
	p := dayRe.FindStringSubmatch(strings.TrimSpace(s))
	if p == nil {
		return time.Time{}, false
	}
	if p[2] != "" {
		var ok bool
		if m, ok = months[strings.ToLower(p[2])]; !ok {
			return time.Time{}, false
		}
	}
	if p[3] != "" {
		y, _ = strconv.Atoi(p[3])
	}
	d, _ := strconv.Atoi(p[1])
	if m == 0 || y == 0 {
		return time.Time{}, false
	}
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return t, t.Day() == d && t.Month() == m
}

// parseWeekly — записи блока паркранов Москвы или Петербурга. День — из span.num, дата — первая
// с этим днём не раньше *last (тот же или следующий месяц); нераспознанный
// день — сырой текст в Date (Prepare отбракует).
func parseWeekly(n *html.Node, last *time.Time) []ingest.SourceEvent {
	var title, date string
	for c := range n.Descendants() {
		switch {
		case isElem(c, atom.Span) && hasClass(c, "num") && date == "":
			date = text(c)
			if t, ok := nextDay(date, *last); ok {
				*last, date = t, t.Format("02.01.2006")
			}
		case isElem(c, atom.H3) && title == "":
			title = text(c)
		}
	}
	base := ingest.SourceEvent{Date: date, Distances: weeklyDistance}
	for suffix, city := range weeklyCities {
		if strings.HasSuffix(title, suffix) {
			base.City, base.Region, base.Country = city, city, country
		}
	}
	if base.City == "" {
		return nil
	}
	var out []ingest.SourceEvent
	for li := range n.Descendants() {
		if !isElem(li, atom.Li) {
			continue
		}
		ev := base
		ev.Title, ev.ExternalID, ev.URL = eventLink(li)
		ev.GroupKey = "series:" + normalize.TitleNorm(ev.Title)
		out = append(out, ev)
	}
	return out
}

// nextDay — первая дата с днём s не раньше from: в том же месяце или
// следующем.
func nextDay(s string, from time.Time) (time.Time, bool) {
	d, err := strconv.Atoi(s)
	if err != nil {
		return time.Time{}, false
	}
	for _, m := range []time.Month{from.Month(), from.Month() + 1} {
		t := time.Date(from.Year(), m, d, 0, 0, 0, 0, time.UTC)
		if t.Day() == d && !t.Before(from) {
			return t, true
		}
	}
	return time.Time{}, false
}

// resolvePlace — область, город и страна по блоку места «Город · Регион»
// (D-06). Город — первая ссылка (у «Старт → Финиш» — город старта).
//   - регион — страна не РФ («Беларусь»): Country = она, Region пусто;
//   - регион — субъект РФ: каноническое имя, Country «Россия»;
//   - нераспознанный регион: Region = текст, Country пусто;
//   - без региона: «Россия» для субъекта-города или по справочнику городов РФ.
func resolvePlace(meta *html.Node) (region, city, cntry string) {
	full := text(meta)
	for a := range meta.Descendants() {
		if isElem(a, atom.A) {
			city = normalize.City(text(a))
			break
		}
	}
	reg, found := "", false
	if i := strings.LastIndex(full, "·"); i >= 0 {
		reg, found = strings.TrimSpace(full[i+len("·"):]), true
	}
	if city == "" {
		before, _, _ := strings.Cut(full, "·")
		city = normalize.City(strings.TrimSpace(before))
	}
	if !found {
		if canon, kind := normalize.ClassifyRegion(city); kind == normalize.RegionRF {
			return canon, city, country
		}
		if normalize.IsRFCity(city) {
			return "", city, country
		}
		return "", city, ""
	}
	if code := normalize.CountryCode(reg); code != "" && code != "RU" {
		return "", city, reg
	}
	if canon, kind := normalize.ClassifyRegion(reg); kind == normalize.RegionRF {
		return canon, city, country
	}
	return reg, city, ""
}

// distances — значки span.badge блока planned (кроме красных — «Забег
// отменён»), каждый до первой «(», без ITRA и неходовых видов, через ", ".
// Счётчик «Планируют N человек» — ссылка a.badge, не дистанция.
func distances(planned *html.Node) string {
	if planned == nil {
		return ""
	}
	var out []string
	for n := range planned.Descendants() {
		if !isElem(n, atom.Span) || !hasClass(n, "badge") || hasClass(n, "red") {
			continue
		}
		f, _, _ := strings.Cut(text(n), "(")
		f = itraRe.ReplaceAllString(f, "")
		if f = strings.TrimSpace(f); f != "" && !isNonRunning(f) {
			out = append(out, namedToMeters(f))
		}
	}
	return strings.Join(out, ", ")
}

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

// registrationURL — ссылка первой <a> с текстом «Регистрация»; «Сайт»,
// «Положение» и соцсети не используются.
func registrationURL(links *html.Node, pageURL string) string {
	if links == nil {
		return ""
	}
	for a := range links.Descendants() {
		if isElem(a, atom.A) && text(a) == regLink {
			if u, ok := absURL(attr(a, "href"), pageURL); ok {
				return u.String()
			}
			return ""
		}
	}
	return ""
}

// nextPage — адрес ссылки «Следующая страница» или "" на последней странице.
func nextPage(body []byte, pageURL string) (string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("разбор HTML: %w", err)
	}
	for a := range root.Descendants() {
		if isElem(a, atom.A) && attr(a, "aria-label") == nextLabel {
			u, ok := absURL(attr(a, "href"), pageURL)
			if !ok {
				return "", errors.New("probeg: неверная ссылка на следующую страницу")
			}
			return u.String(), nil
		}
	}
	return "", nil
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
