package normalize

import (
	_ "embed"
	"strings"
)

// Справочник городов РФ (Zenodo 5151423, CC BY 4.0; шапка файла — атрибуция
// и SHA-256 исходника) и неоднозначные имена, которые городом РФ не считаются.
var (
	//go:embed ru_cities.txt
	ruCitiesTxt string
	//go:embed ru_cities_ambiguous.txt
	ruCitiesAmbiguousTxt string
)

// rfCities — индекс по cityLookupKey: города РФ без неоднозначных.
var rfCities = map[string]bool{}

func init() {
	lines := func(s string) []string {
		var out []string
		for l := range strings.Lines(s) {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, cityLookupKey(l))
			}
		}
		return out
	}
	for _, k := range lines(ruCitiesTxt) {
		rfCities[k] = true
	}
	for _, k := range lines(ruCitiesAmbiguousTxt) {
		delete(rfCities, k)
	}
}

// IsRFCity сообщает, что «голый» город (без страны) — город РФ по
// встроенному справочнику (D-01). Сравнение по cityLookupKey: регистр,
// ё/е, префиксы поселения, дефисы. Неоднозначные имена
// (ru_cities_ambiguous.txt) и пустая строка → false.
func IsRFCity(city string) bool {
	k := cityLookupKey(city)
	return k != "" && rfCities[k]
}
