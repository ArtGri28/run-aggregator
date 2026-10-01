package bot

// Слой отображения стран: код ISO alpha-2 → флаг и название. Первичный ключ
// страны везде — код; неизвестный код показывается как есть, добавить страну —
// строка в countryNames.

// allCountries — значение фильтра «все страны».
const allCountries = "*"

var countryNames = map[string]string{"RU": "Россия", "BY": "Беларусь", "PL": "Польша"}

// countryName — название страны; "*" — «Все страны», неизвестный код — он сам.
func countryName(code string) string {
	if code == allCountries {
		return "Все страны"
	}
	if n, ok := countryNames[code]; ok {
		return n
	}
	return code
}

// countryFlag — флаг из двух букв ISO (региональные индикаторы); "" для
// значения не из двух заглавных латинских букв.
func countryFlag(code string) string {
	if !countryRE.MatchString(code) {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(code[0]-'A'), 0x1F1E6 + rune(code[1]-'A')})
}

// countryLabel — «флаг название»; для "*" — «🌍 Все страны».
func countryLabel(code string) string {
	if code == allCountries {
		return "🌍 " + countryName(code)
	}
	if f := countryFlag(code); f != "" {
		return f + " " + countryName(code)
	}
	return countryName(code)
}
