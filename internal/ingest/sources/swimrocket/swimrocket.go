// Package swimrocket — источник SwimRocket (swimrocket.ru/calendar/):
// календарь заплывов на открытой воде разных организаторов. Записи встроены в
// страницу литералом JavaScript; структура — testdata/README.md. Читается с
// письменного разрешения владельца сайта (SOURCE-PERMISSION-LETTERS.md).
package swimrocket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "swimrocket"
	// ListURL — страница календаря: все записи в одном документе.
	ListURL = "https://www.swimrocket.ru/calendar/"
	// CrawlDelay — пауза между запросами к хосту.
	CrawlDelay = 5 * time.Second
	// itemsMark — начало массива записей в странице.
	itemsMark = "items: ["
)

// Source — источник SwimRocket: один GET страницы календаря за прогон.
type Source struct {
	f   *ingest.Fetcher
	url string
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: ListURL} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse; запись без страны — вне каталога.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает страницу календаря.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает страницу календаря (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }

type item struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Title string `json:"title"`
	// Distance — дистанции в метрах строками; у части записей — false.
	Distance json.RawMessage `json:"distance"`
	Location struct {
		Country string `json:"country"`
		City    string `json:"city"`
	} `json:"location"`
	Web string `json:"web"`
}

// Parse разбирает страницу календаря. Чистая функция. Каждая запись — заплыв
// на открытой воде (календарь «заплывов на открытой воде»). Страна: «Россия»
// и российские регионы в поле страны — RU, «Беларусь» — BY; заплыв, у которого
// в поле страны, в начале места или словом названия стоит другая страна
// (normalize.ForeignCountry), записи не даёт. Прошедшие даты отсеивает
// ingest.Prepare. Нет массива записей — ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	raw, err := itemsJSON(doc.Body)
	if err != nil {
		return nil, err
	}
	var items []item
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("разбор записей календаря: %w", err)
	}
	var out []ingest.SourceEvent
	for _, it := range items {
		title := strings.Join(strings.Fields(it.Title), " ")
		country, parts := countryCode(it.Location.Country), strings.Split(it.Location.City, ",")
		for _, p := range parts {
			if countryCode(p) == "" {
				country = ""
			}
		}
		if country == "" || foreignWord(title) {
			continue
		}
		// «Россия, Уфа» — страна в начале места: место без неё.
		if n := strings.ToLower(strings.TrimSpace(parts[0])); n == "россия" || n == "беларусь" {
			country, parts = countryCode(n), parts[1:]
		}
		place := strings.Join(strings.Fields(strings.Join(parts, ",")), " ")
		city := ingest.PlaceCity(place)
		if city == place {
			place = ""
		}
		if city == strings.ToUpper(city) {
			city = capitalize(city)
		}
		var dists []string
		_ = json.Unmarshal(it.Distance, &dists)
		for i, d := range dists {
			dists[i] = strings.TrimSpace(d) + " м"
		}
		out = append(out, ingest.SourceEvent{
			ExternalID: it.ID,
			URL:        strings.TrimSpace(it.Web),
			Title:      title,
			Date:       strings.TrimSpace(it.Date),
			Distances:  strings.Join(dists, ", "),
			City:       city,
			Country:    country,
			TypeLabel:  "открытая вода",
			Address:    place,
			Sport:      string(event.SportSwimming),
		})
	}
	return out, nil
}

// capitalize — «НИЖНИЙ НОВГОРОД» → «Нижний Новгород»: каждая часть слова
// через пробел или дефис — с прописной, остальное строчными.
func capitalize(s string) string {
	r := []rune(strings.ToLower(s))
	for i := range r {
		if i == 0 || r[i-1] == ' ' || r[i-1] == '-' {
			r[i] = unicode.ToUpper(r[i])
		}
	}
	return string(r)
}

// countryCode — RU для России и для значения, которое страной не является
// (регион, город), BY для Беларуси, "" для другой страны.
func countryCode(s string) string {
	switch n := strings.ToLower(strings.TrimSpace(s)); {
	case n == "беларусь":
		return "BY"
	case n != "россия" && normalize.ForeignCountry(n):
		return ""
	}
	return "RU"
}

// foreignWord — слово названия — другая страна («X-WATERS Словения»).
func foreignWord(title string) bool {
	for _, w := range strings.Fields(title) {
		if countryCode(w) == "" {
			return true
		}
	}
	return false
}

// itemsJSON — массив записей после itemsMark, приведённый к JSON: строки в
// одинарных кавычках становятся строками JSON, \' и \/ — символами.
func itemsJSON(body []byte) ([]byte, error) {
	i := bytes.Index(body, []byte(itemsMark))
	if i < 0 {
		return nil, fmt.Errorf("%w: нет массива записей календаря", ingest.ErrStructureNotFound)
	}
	var out bytes.Buffer
	depth, inStr := 0, false
	for j := i + len(itemsMark) - 1; j < len(body); j++ {
		c := body[j]
		if inStr {
			switch {
			case c == '\'':
				out.WriteByte('"')
				inStr = false
			case c == '"':
				out.WriteString(`\"`)
			case c == '\\' && j+1 < len(body):
				j++
				switch n := body[j]; n {
				case '\'', '/':
					out.WriteByte(n)
				case '"', '\\', 'n', 'r', 't', 'b', 'f', 'u':
					out.WriteByte('\\')
					out.WriteByte(n)
				default:
					out.WriteByte(n)
				}
			case c < 0x20:
				fmt.Fprintf(&out, `\u%04x`, c)
			default:
				out.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'':
			out.WriteByte('"')
			inStr = true
			continue
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
		out.WriteByte(c)
		if depth == 0 {
			return out.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("%w: массив записей календаря не закрыт", ingest.ErrStructureNotFound)
}
