// Package normalize turns raw source strings into canonical event values
// (MVP.md §6). Pure functions: no network, files or database.
package normalize

import (
	"regexp"
	"strings"
)

var parensRe = regexp.MustCompile(`\([^)]*\)`)

// cityPrefixes are stripped case-insensitively from the start of a city
// name, longest first ("г.о. " before "г."). A bare "г" is kept
// (Гатчина): only "г." or "г " is a prefix; "С.-Петербург" is untouched.
var cityPrefixes = []string{"городской округ ", "г.о. ", "город ", "пос. ", "п. ", "д. ", "с. ", "г.", "г "}

// City cleans a display city name: drops parenthesised parts and a
// leading settlement prefix ("г.", "г.о.", "п.", "с.", "д.", "город"),
// collapses spaces. Case and "ё" are kept.
func City(s string) string {
	s = collapseSpaces(parensRe.ReplaceAllString(s, " "))
	for _, p := range cityPrefixes {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			s = collapseSpaces(s[len(p):])
			break
		}
	}
	return s
}

// Region trims and collapses spaces. Region is less reliable than city
// and is not shown in the interface (MVP.md §6).
func Region(s string) string {
	return collapseSpaces(s)
}

// SplitPlace splits ПроБЕГ's "Область, Город" by the last comma; without
// a comma the whole string is the city.
func SplitPlace(s string) (region, city string) {
	i := strings.LastIndex(s, ",")
	if i < 0 {
		return "", City(s)
	}
	return Region(s[:i]), City(s[i+1:])
}

// citySynonyms maps a lookup key (see cityLookupKey) to a canonical
// city_key. D-03: only cities of the approved sources plus obvious
// synonyms; everything else is transliterated.
var citySynonyms = map[string]string{
	"москва": "moscow",
	"moscow": "moscow",
	"мск":    "moscow",

	"санкт петербург":  "saint-petersburg",
	"с петербург":      "saint-petersburg",
	"спб":              "saint-petersburg",
	"питер":            "saint-petersburg",
	"петербург":        "saint-petersburg",
	"saint petersburg": "saint-petersburg",
	"st petersburg":    "saint-petersburg",
}

// cyrillicToLatin is the transliteration table for unknown cities
// (GOST 7.79 System B style).
var cyrillicToLatin = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "c", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

// maxCityKeyBytes keeps city_key inside Telegram callback_data (Phase 4)
// and the events.city_key CHECK.
const maxCityKeyBytes = 64

// CityKey returns the ASCII search slug of a city: a dictionary synonym
// or a transliteration. The result is "" or matches
// ^[a-z0-9]+(-[a-z0-9]+)*$ and is at most 64 bytes.
func CityKey(s string) string {
	key := cityLookupKey(s)
	if canon, ok := citySynonyms[key]; ok {
		return canon
	}
	// Word by word: a word that transliterates to nothing ("ь", other
	// scripts) is dropped instead of leaving "--" (LO-04).
	var words []string
	for _, w := range strings.Fields(key) {
		var b strings.Builder
		for _, r := range w {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			} else {
				b.WriteString(cyrillicToLatin[r]) // other scripts are dropped
			}
		}
		if b.Len() > 0 {
			words = append(words, b.String())
		}
	}
	out := strings.Join(words, "-")
	if len(out) > maxCityKeyBytes {
		out = strings.TrimRight(out[:maxCityKeyBytes], "-")
	}
	return out
}

// cityLookupKey: cleaned city, lower case, ё→е, every run of
// non-letter/non-digit characters replaced by one space, trimmed.
func cityLookupKey(s string) string {
	return strings.Join(strings.FieldsFunc(yoToE(strings.ToLower(City(s))), notLetterOrDigit), " ")
}

// cityZones maps city_key to an IANA zone (MVP.md §4.2).
var cityZones = map[string]string{
	"moscow":           "Europe/Moscow",
	"saint-petersburg": "Europe/Moscow",
}

// Timezone returns the IANA zone for a city_key, falling back to the
// region treated as a city (federal cities), else "".
func Timezone(cityKey, region string) string {
	if z, ok := cityZones[cityKey]; ok {
		return z
	}
	return cityZones[CityKey(region)]
}

var countryNames = map[string]string{
	"россия": "RU",
	"russia": "RU",
	"рф":     "RU",
	"российская федерация": "RU",
	"rus":                "RU",
	"russian federation": "RU",
	"rossiya":            "RU",
	"russian":            "RU",
}

// CountryCode normalizes a country name or ISO alpha-2 code; "" if unknown.
func CountryCode(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if c, ok := countryNames[s]; ok {
		return c
	}
	if len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z' {
		return strings.ToUpper(s)
	}
	return ""
}

// CountryPolicy is a source's rule for events without an explicit country
// (01-SOURCES-REPORT §4.2, country_policy).
type CountryPolicy int

const (
	// NoFallback (no_fallback, ПроБЕГ): without an explicit country the
	// event is out of scope.
	NoFallback CountryPolicy = iota
	// RUOnlyFallback (ru_only_fallback, Марафонец): fallback RU допускается
	// для источника, который Phase 1 ПРЕДВАРИТЕЛЬНО признала кандидатом
	// (01-SOURCES-REPORT §4.2); статус предварительный, окончательно
	// подтверждается санити-проверкой на реальных данных в Phase 3 (ING-11).
	RUOnlyFallback
)

// Country applies MVP.md §6 rules 1–4. A recognised explicit country
// wins: only RU is in scope, any other code or a known foreign name
// (ClassifyRegion → RegionForeign) is out of scope under every policy.
// Without an explicit country, or with one that is not recognised
// (ME-04: "cannot determine" is not "foreign"), the source policy
// decides: RUOnlyFallback yields RU, NoFallback yields out of scope.
// inScope == false means the caller stores the source event with
// match_state = out_of_scope and creates no event.
func Country(explicit string, p CountryPolicy) (code string, inScope bool) {
	if strings.TrimSpace(explicit) != "" {
		if code = CountryCode(explicit); code != "" {
			return code, code == "RU"
		}
		if _, kind := ClassifyRegion(explicit); kind == RegionForeign {
			return "", false
		}
	}
	if p == RUOnlyFallback {
		return "RU", true
	}
	return "", false
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
