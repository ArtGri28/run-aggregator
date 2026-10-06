package ingest

import (
	"regexp"
	"strings"
	"unicode"

	"aggregator/internal/normalize"
)

// ruDateRe — «10 октября» или «10 октября 2026» (уже в нижнем регистре).
var ruDateRe = regexp.MustCompile(`^(\d{1,2}) ([а-я]+)(?: (\d{4}))?$`)

var ruMonths = map[string]string{
	"января": "01", "февраля": "02", "марта": "03", "апреля": "04", "мая": "05", "июня": "06",
	"июля": "07", "августа": "08", "сентября": "09", "октября": "10", "ноября": "11", "декабря": "12",
}

// RuDate переводит дату словами («10 октября», «03 октября 2026») в dd.mm или
// dd.mm.yyyy — формы, которые разбирает normalize.Date. Другая строка
// возвращается как есть: запись с ней отбракует Prepare.
func RuDate(s string) string {
	p := ruDateRe.FindStringSubmatch(strings.ToLower(strings.Join(strings.Fields(s), " ")))
	if p == nil || ruMonths[p[2]] == "" {
		return s
	}
	out := p[1] + "." + ruMonths[p[2]]
	if p[3] != "" {
		out += "." + p[3]
	}
	return out
}

// PlaceCity выбирает город из свободной строки места («г. Гомель, ул.
// Набережная, 27», «Барановичский р-н, д. Колбовичи», «Гродно - База отдыха»):
// первая часть между запятыми (до « - »), которая после normalize.City
// состоит только из слов с прописной буквы и не является названием страны.
// Район, улица, «озеро Белое», название в кавычках — не город; нет
// подходящей части — "".
// Упрощение: эвристика без справочника — «Парк Победы, Минск» даст «Парк
// Победы»; при заметной доле ошибок нужен справочник городов Беларуси.
func PlaceCity(loc string) string {
	for part := range strings.SplitSeq(loc, ",") {
		part, _, _ = strings.Cut(part, " - ")
		if c := normalize.City(part); isCityName(c) && normalize.CountryCode(c) == "" {
			return c
		}
	}
	return ""
}

func isCityName(s string) bool {
	words := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '-' })
	for _, w := range words {
		for i, r := range w {
			if i == 0 && !unicode.IsUpper(r) || !unicode.IsLetter(r) {
				return false
			}
		}
	}
	return len(words) > 0
}
