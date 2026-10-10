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
	"strconv"
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
	// dateMark — пометка названия лыжного события с датой-заглушкой.
	dateMark = "(дата уточняется)"
	// Подписи дисциплин лыж (normalize.SportType).
	labelSki, labelRollerski = "Лыжные гонки", "Лыжероллеры"
)

var (
	// idRe — путь страницы события /competition/<n>/.
	idRe     = regexp.MustCompile(`^/competition/(\d+)/$`)
	parensRe = regexp.MustCompile(`\([^)]*\)`)
	// listRe — перечисление дистанций с общей единицей: числа без единицы
	// через «, » или « и » (группа 1) перед числом с единицей (группа 2);
	// за единицей не буква. Десятичная запятая — часть числа.
	listRe = regexp.MustCompile(`(?i)((?:\d+(?:[.,]\d+)?(?:, | и ))+)\d+(?:[.,]\d+)?\s*(км|km|м|m)(?:[^\p{L}]|$)`)
	// listItemRe — число перечисления и разделитель после него.
	listItemRe = regexp.MustCompile(`(\d+(?:[.,]\d+)?)(, | и )`)
	// dayRe — «1 января»: день числом и слово после него (нижний регистр).
	dayRe = regexp.MustCompile(`(?:^|\D)(\d{1,2}) ([а-я]+)`)
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
//   - Вид спорта страница события не называет: он следует из списка, в
//     котором событие найдено. Fetch помечает документ лыжного события
//     фрагментом адреса skiMark; документ без пометки — беговой и разбирается
//     как раньше. У лыжного документа запись получает вид спорта skiing и ещё
//     три правила: дисциплина — по слову в названии (skiLabel); перечисление
//     дистанций с общей единицей раскрывается (expandList); название события
//     с датой-заглушкой получает пометку dateMark (datePlaceholder).
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
	rec := ingest.SourceEvent{
		ExternalID: m[1],
		URL:        eventBase + m[1] + "/",
		Title:      title,
		Date:       localDate(q.Get("date")),
		Distances:  distances(root),
		City:       city,
		Country:    country,
		Address:    address,
		Timezone:   q.Get("timezone"),
	}
	if u.Fragment == skiMark {
		rec.Sport = skiMark
		rec.TypeLabel = skiLabel(title)
		rec.Distances = expandList(rec.Distances)
		if !strings.Contains(strings.ToLower(title), "дата уточняется") && datePlaceholder(rec.Date, root) {
			rec.Title = dateMark + " " + title
		}
	}
	return []ingest.SourceEvent{rec}, nil
}

// skiLabel — подпись дисциплины лыжного события по слову в названии: признака
// лыжероллеров в разметке нет.
//
// Упрощение: лыжероллерный старт без слова «лыжеролл…» или «роллер…» в
// названии получит «Лыжные гонки». Точнее — когда источник даст признак.
func skiLabel(title string) string {
	if t := strings.ToLower(title); strings.Contains(t, "лыжеролл") || strings.Contains(t, "роллер") {
		return labelRollerski
	}
	return labelSki
}

// expandList раскрывает перечисление дистанций с общей единицей: каждое число
// без единицы получает единицу числа, которым перечисление кончается, —
// «15, 30 и 45 км» → «15 км, 30 км и 45 км». Общий разбор дистанций берёт
// число только вместе с единицей и оставил бы одно последнее. Остальной текст
// строки, в том числе слова о стиле, не меняется.
//
// Упрощение: перечисление иного вида (через «/» или «-», с единицей в начале)
// не раскрывается и даёт, как раньше, одно число.
func expandList(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range listRe.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[2]])
		b.WriteString(listItemRe.ReplaceAllString(s[m[2]:m[3]], "${1} "+s[m[4]:m[5]]+"${2}"))
		last = m[3]
	}
	b.WriteString(s[last:])
	return b.String()
}

// datePlaceholder — дата-заглушка: date (yyyy-mm-dd) — первое число месяца, и
// ни в одной строке текста события этот день не назван числом с месяцем
// («январь 2027 г.» — заглушка, «1 января 2027 г.» — точная дата). Пометка —
// часть названия записи: когда источник назовёт день, следующий прогон даст
// название без неё.
//
// Упрощение: событие первого числа без строки с датой в тексте тоже считается
// заглушкой; дата, записанная в тексте цифрами («01.01.2027»), днём не
// считается.
func datePlaceholder(date string, root *html.Node) bool {
	t, err := time.Parse("2006-01-02", date)
	if err != nil || t.Day() != 1 {
		return false
	}
	want := "1." + t.Format("01") // форма ingest.RuDate
	for n := range root.Descendants() {
		if n.Type != html.ElementNode || !hasClass(n, "e-content") {
			continue
		}
		for _, line := range lines(n) {
			for _, m := range dayRe.FindAllStringSubmatch(strings.ToLower(line), -1) {
				if d, _ := strconv.Atoi(m[1]); d == 1 && ingest.RuDate("1 "+m[2]) == want {
					return false
				}
			}
		}
	}
	return true
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
