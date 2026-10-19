// Package swimcup — источник Swimcup (swimcup.ru/series/): заплывы
// организатора по сериям — открытая вода, «Минуты», «Индор», «Спринт»,
// ФВВСР, детская лига, ласты. Одна страница даёт название, дату, место,
// дистанции и ссылки; структура — testdata/README.md.
package swimcup

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "swimcup"
	// ListURL — страница серий: все старты в одном документе. Адреса с
	// параметрами и detail.php закрыты в robots.txt и не запрашиваются.
	ListURL = "https://swimcup.ru/series/"
	// CrawlDelay — пауза между запросами к хосту.
	CrawlDelay = 5 * time.Second
	base       = "https://swimcup.ru"
)

// poolSeries — серии в бассейне; остальные — открытая вода. Детская лига
// повторяет старты других серий: запись берётся из первой серии страницы.
var poolSeries = map[string]bool{"minutes": true, "indoor": true, "sprint": true}

var (
	// detailRe — ссылка «Подробнее»: /series/<n>-<slug>/.
	detailRe = regexp.MustCompile(`^/series/(\d+)-[^/]+/$`)
	// dateRe — «30 Мая», «13-14 Июня», «31 Июля - 1 Августа».
	dateRe = regexp.MustCompile(`^(\d{1,2})(?:\s*-\s*(\d{1,2}))?\s+(\p{L}+)(?:\s*-\s*(\d{1,2})\s+(\p{L}+))?$`)
	yearRe = regexp.MustCompile(`(?:^|\D)(20\d\d)(?:\D|$)`)
	numRe  = regexp.MustCompile(`\d[\d ]*`)
)

// Source — источник Swimcup: один GET страницы серий за прогон.
type Source struct {
	f   *ingest.Fetcher
	url string
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: ListURL} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse: все старты организатора в России.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает страницу серий.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает страницу серий (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

// Parse разбирает страницу серий. Чистая функция; «сегодня» — doc.FetchedAt.
//   - Серия — якорь div.page-anchor (id) перед её стартами; старт —
//     li.results__list. Старт из нескольких серий даёт одну запись — по
//     номеру ссылки «Подробнее», дисциплина — по первой серии.
//   - Год — из названия («Крылатское 2027»); без года — ближайшая дата с этим
//     днём и месяцем не раньше дня загрузки.
//   - Дистанции — числа столбца в метрах; эстафеты («3х300м») и время
//     («60 мин») дистанций не дают.
//   - Город — последняя часть места через запятую, если это название с
//     прописной и строчными кириллицей («Лужники, Москва» → «Москва»); «ЗИЛ»,
//     «SportStation» — площадки, не города.
//
// Нет ни одного старта — ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	root, err := html.Parse(bytes.NewReader(doc.Body))
	if err != nil {
		return nil, fmt.Errorf("разбор HTML: %w", err)
	}
	var out []ingest.SourceEvent
	series, seen, items := "", map[string]bool{}, 0
	for n := range root.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		if hasClass(n, "page-anchor") {
			series = attr(n, "id")
			continue
		}
		if !hasClass(n, "results__list") {
			continue
		}
		items++
		rec, ok := parseItem(n, series, doc.FetchedAt)
		if ok && !seen[rec.ExternalID] {
			seen[rec.ExternalID] = true
			out = append(out, rec)
		}
	}
	if items == 0 {
		return nil, fmt.Errorf("%w: нет стартов results__list", ingest.ErrStructureNotFound)
	}
	return out, nil
}

func parseItem(li *html.Node, series string, now time.Time) (ingest.SourceEvent, bool) {
	col := map[string]string{}
	var links []string
	for n := range li.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		for _, c := range []string{"name", "cal", "city", "clock"} {
			if hasClass(n, "results__col--"+c) {
				col[c] = text(n)
			}
		}
		if n.Data == "a" {
			links = append(links, attr(n, "href"))
		}
	}
	var id, detail, reg string
	for _, l := range links {
		if m := detailRe.FindStringSubmatch(l); m != nil && id == "" {
			id, detail = m[1], base+l
		} else if strings.HasPrefix(l, "https://") {
			reg = l
		}
	}
	if id == "" {
		return ingest.SourceEvent{}, false
	}
	start, end := dates(col["cal"], col["name"], now)
	label := "открытая вода"
	if poolSeries[series] {
		label = "бассейн"
	}
	parts := strings.Split(col["city"], ",")
	city := strings.TrimSuffix(strings.TrimSpace(parts[len(parts)-1]), " МО")
	if !cityName(city) {
		city = ""
	}
	rec := ingest.SourceEvent{
		ExternalID:      id,
		URL:             detail,
		Title:           col["name"],
		Date:            start,
		EndDate:         end,
		Distances:       distances(col["clock"]),
		City:            city,
		Country:         "RU",
		TypeLabel:       label,
		RegistrationURL: reg,
		Sport:           string(event.SportSwimming),
	}
	if col["city"] != city {
		rec.Address = col["city"]
	}
	return rec, true
}

// dates — дата старта и окончания (dd.mm.yyyy; окончание "" у однодневного).
// Неразборчивая дата возвращается как есть: запись отбракует Prepare.
func dates(s, title string, now time.Time) (start, end string) {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return s, ""
	}
	d1, _ := strconv.Atoi(m[1])
	month := ingest.RuDate("1 " + strings.ToLower(m[3]))
	mon, err := strconv.Atoi(strings.TrimPrefix(month, "1."))
	if err != nil {
		return s, ""
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	year := now.Year()
	if y := yearRe.FindStringSubmatch(title); y != nil {
		year, _ = strconv.Atoi(y[1])
	} else if time.Date(year, time.Month(mon), d1, 0, 0, 0, 0, time.UTC).Before(today) {
		year++
	}
	t1 := time.Date(year, time.Month(mon), d1, 0, 0, 0, 0, time.UTC)
	start = t1.Format("02.01.2006")
	switch {
	case m[2] != "":
		d2, _ := strconv.Atoi(m[2])
		end = time.Date(year, time.Month(mon), d2, 0, 0, 0, 0, time.UTC).Format("02.01.2006")
	case m[4] != "":
		d2, _ := strconv.Atoi(m[4])
		mon2, err := strconv.Atoi(strings.TrimPrefix(ingest.RuDate("1 "+strings.ToLower(m[5])), "1."))
		if err != nil {
			return start, ""
		}
		t2 := time.Date(year, time.Month(mon2), d2, 0, 0, 0, 0, time.UTC)
		if t2.Before(t1) {
			t2 = t2.AddDate(1, 0, 0)
		}
		end = t2.Format("02.01.2006")
	}
	return start, end
}

// distances — числа столбца дистанций как метры: «500, 1000, 1852, 5000 м» →
// «500 м, 1000 м, 1852 м, 5000 м»; «10 000» — одно число. Эстафета и время
// пропускаются.
func distances(s string) string {
	var out []string
	for _, part := range strings.Split(strings.ReplaceAll(s, " и ", ","), ",") {
		p := strings.ToLower(part)
		if strings.ContainsAny(p, "хx") || strings.Contains(p, "мин") || strings.Contains(p, "час") {
			continue
		}
		if n := numRe.FindString(p); n != "" {
			out = append(out, strings.ReplaceAll(strings.TrimSpace(n), " ", "")+" м")
		}
	}
	return strings.Join(out, ", ")
}

// cityName — название с прописной, в котором есть строчные буквы кириллицы.
func cityName(s string) bool {
	r := []rune(s)
	if len(r) == 0 || !unicode.IsUpper(r[0]) {
		return false
	}
	for _, c := range r {
		if unicode.Is(unicode.Cyrillic, c) && unicode.IsLower(c) {
			return true
		}
	}
	return false
}

// text — текст узла со схлопнутыми пробелами (&nbsp; — пробел).
func text(n *html.Node) string {
	var b strings.Builder
	for c := range n.Descendants() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
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
