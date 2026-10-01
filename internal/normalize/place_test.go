package normalize

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCity(t *testing.T) {
	tests := []struct{ in, want string }{
		{"г. Москва", "Москва"},
		{"г.Москва", "Москва"},
		{"Г. Москва", "Москва"},
		{"город Санкт-Петербург", "Санкт-Петербург"},
		{"Москва (Зеленоград)", "Москва"},
		{"  Нижний   Новгород ", "Нижний Новгород"},
		{"Гатчина", "Гатчина"},
		{"Щёлково", "Щёлково"},
		{"г.о. Химки", "Химки"},
		{"городской округ Химки", "Химки"},
		{"п. Токсово", "Токсово"},
		{"пос. Токсово", "Токсово"},
		{"д. Горки", "Горки"},
		{"с. Ильинское", "Ильинское"},
		{"С.-Петербург", "С.-Петербург"},
		{"", ""},
		{"пгт. Ныроб", "Ныроб"},
		{"пгт.Ныроб", "Ныроб"},
		{"ПГТ. Ныроб", "Ныроб"},
		{"п.г.т. Красная поляна", "Красная поляна"},
		{"посёлок Гимолы", "Гимолы"},
		{"Поселок Гимолы", "Гимолы"},
		{"ст-ца Ессентукская", "Ессентукская"},
		{"станица Ессентукская", "Ессентукская"},
		{"хутор Ленина", "Ленина"},
		{"аул Хакуринохабль", "Хакуринохабль"},
		{"c. Белогорское", "Белогорское"}, // латинская c
		{"Аулово", "Аулово"},
		{"Ставрополь", "Ставрополь"},
	}
	for _, tt := range tests {
		if got := City(tt.in); got != tt.want {
			t.Errorf("City(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRegion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Московская   область ", "Московская область"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Region(tt.in); got != tt.want {
			t.Errorf("Region(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitPlace(t *testing.T) {
	tests := []struct{ in, region, city string }{
		{"Московская область, Химки", "Московская область", "Химки"},
		{"Ленинградская обл. , г. Гатчина", "Ленинградская обл.", "Гатчина"},
		{"Москва", "", "Москва"},
		{"", "", ""},
	}
	for _, tt := range tests {
		region, city := SplitPlace(tt.in)
		if region != tt.region || city != tt.city {
			t.Errorf("SplitPlace(%q) = (%q, %q), want (%q, %q)", tt.in, region, city, tt.region, tt.city)
		}
	}
}

// cityKeyRe mirrors the events.city_key CHECK constraint, tightened to
// forbid leading, trailing and doubled hyphens.
var cityKeyRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestCityKey(t *testing.T) {
	long := strings.Repeat("абвгд ", 20) // 100 Cyrillic letters with spaces
	tests := []struct{ in, want string }{
		{"Москва", "moscow"},
		{"г. Москва", "moscow"},
		{"MOSCOW", "moscow"},
		{"мск", "moscow"},
		{"СПб", "saint-petersburg"},
		{"Питер", "saint-petersburg"},
		{"Санкт-Петербург", "saint-petersburg"},
		{"санкт петербург", "saint-petersburg"},
		{"С.-Петербург", "saint-petersburg"},
		{"St. Petersburg", "saint-petersburg"},
		{"Saint Petersburg", "saint-petersburg"},
		{"город Санкт-Петербург (Кронштадт)", "saint-petersburg"},
		{"Урюпинск", "uryupinsk"},
		{"Ростов-на-Дону", "rostov-na-donu"},
		{"Щёлково", "schelkovo"},
		{"Йошкар-Ола", "yoshkar-ola"},
		{"Москва — Сити", "moskva-siti"},
		{"Москва ь Сити", "moskva-siti"}, // слово из одного «ь» не даёт «--» (LO-04)
		{"Усть-Абакан", "ust-abakan"},
		{"пгт. Ныроб", "nyrob"},
		{"Ныроб", "nyrob"},
		{"", ""},
		{"  (—) ", ""},
		{long, ""}, // checked by invariant only
	}
	for _, tt := range tests {
		got := CityKey(tt.in)
		if tt.in != long && got != tt.want {
			t.Errorf("CityKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if got != "" && (len(got) > 60 || !cityKeyRe.MatchString(got)) {
			t.Errorf("CityKey(%q) = %q violates city_key invariant (len %d)", tt.in, got, len(got))
		}
	}
	if got := CityKey(long); got == "" || len(got) > 60 {
		t.Errorf("CityKey(long) = %q (len %d), want non-empty and <= 60 bytes", got, len(got))
	}
}

func TestCityPrefixesOrder(t *testing.T) {
	for i := 1; i < len(cityPrefixes); i++ {
		if len(cityPrefixes[i]) > len(cityPrefixes[i-1]) {
			t.Errorf("cityPrefixes: %q (%d байт) после более короткого %q", cityPrefixes[i], len(cityPrefixes[i]), cityPrefixes[i-1])
		}
	}
}

func TestIsRFCity(t *testing.T) {
	for _, c := range []string{"Москва", "г. Сочи", "Ростов-на-Дону", "ростов на дону", "Екатеринбург", "Орёл", "Орел", "Бийск"} {
		if !IsRFCity(c) {
			t.Errorf("IsRFCity(%q) = false, want true", c)
		}
	}
	// Светлогорск — неоднозначный; Мариуполь — нет в справочнике 2021 года.
	for _, c := range []string{"Донецк", "Светлогорск", "Минск", "Гродно", "Мариуполь", "Где-то", ""} {
		if IsRFCity(c) {
			t.Errorf("IsRFCity(%q) = true, want false", c)
		}
	}
	if n := len(rfCities); n < 1080 {
		t.Errorf("индекс городов РФ: %d, want ≥ 1080", n)
	}
}

func TestTimezone(t *testing.T) {
	tests := []struct{ cityKey, region, want string }{
		{"moscow", "", "Europe/Moscow"},
		{"saint-petersburg", "", "Europe/Moscow"},
		{"uryupinsk", "", ""},
		{"", "Москва", "Europe/Moscow"},
		{"", "Московская область", ""},
	}
	for _, tt := range tests {
		if got := Timezone(tt.cityKey, tt.region); got != tt.want {
			t.Errorf("Timezone(%q, %q) = %q, want %q", tt.cityKey, tt.region, got, tt.want)
		}
	}
	for key, zone := range cityZones {
		if _, err := time.LoadLocation(zone); err != nil {
			t.Errorf("zone %q for %q: %v", zone, key, err)
		}
	}
}

func TestCountryCode(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Россия", "RU"},
		{"Russia", "RU"},
		{"russia", "RU"},
		{"RUSSIA", "RU"},
		{" РФ ", "RU"},
		{"Российская Федерация", "RU"},
		{"RU", "RU"},
		{"rus", "RU"},
		{"by", "BY"},
		{"Беларусь", "BY"},
		{"Республика Беларусь", "BY"},
		{"Белоруссия", "BY"},
		{"Belarus", "BY"},
		{"Польша", "PL"},
		{"Poland", "PL"},
		{"Казахстан", "KZ"},
		{"Абхазия", ""}, // нет собственного ISO-кода
		{"Атлантида", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := CountryCode(tt.in); got != tt.want {
			t.Errorf("CountryCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCountry(t *testing.T) {
	tests := []struct {
		explicit string
		policy   CountryPolicy
		code     string
		inScope  bool
	}{
		// 1. explicit Russia
		{"Россия", NoFallback, "RU", true},
		{"Russia", RUOnlyFallback, "RU", true},
		// 3. explicit allowed country (RU, BY): in scope under any policy
		{"Беларусь", RUOnlyFallback, "BY", true},
		{"Беларусь", NoFallback, "BY", true},
		{"BY", NoFallback, "BY", true},
		{"Belarus", RUOnlyFallback, "BY", true},
		// explicit country outside the allowed list: code kept, out of scope
		{"KZ", RUOnlyFallback, "KZ", false},
		{"Казахстан", RUOnlyFallback, "KZ", false},
		{"Польша", RUOnlyFallback, "PL", false},
		// known foreign name without ISO code: out of scope, no code
		{"Абхазия", RUOnlyFallback, "", false},
		// ME-04: распознанное написание России
		{"Russian Federation", NoFallback, "RU", true},
		{"Rossiya", NoFallback, "RU", true},
		// ME-04: нераспознанная явная строка — по политике источника
		{"Атлантида", RUOnlyFallback, "RU", true},
		{"Атлантида", NoFallback, "", false},
		// 2. no country + preliminary RU fallback
		{"", RUOnlyFallback, "RU", true},
		// 4. no country, no fallback
		{"", NoFallback, "", false},
	}
	for _, tt := range tests {
		code, inScope := Country(tt.explicit, tt.policy)
		if code != tt.code || inScope != tt.inScope {
			t.Errorf("Country(%q, %v) = (%q, %v), want (%q, %v)", tt.explicit, tt.policy, code, inScope, tt.code, tt.inScope)
		}
	}
}

// Справочник названий покрывает все страны из foreignNames (regions.go),
// кроме нескольких без собственного ISO-кода; все значения — ISO alpha-2.
func TestCountryNamesCoverForeign(t *testing.T) {
	noISO := map[string]bool{"Абхазия": true, "Южная Осетия": true, "Abkhazia": true}
	for _, n := range foreignNames {
		if got := CountryCode(n); got == "" && !noISO[n] {
			t.Errorf("foreignNames: %q не найдено в countryNames", n)
		}
	}
	for name, code := range countryNames {
		if len(code) != 2 || code != strings.ToUpper(code) {
			t.Errorf("countryNames[%q] = %q: не ISO alpha-2", name, code)
		}
		if name != strings.ToLower(name) {
			t.Errorf("countryNames[%q]: ключ не в нижнем регистре", name)
		}
	}
}

func TestIsAllowedCountry(t *testing.T) {
	for code, want := range map[string]bool{"RU": true, "BY": true, "PL": false, "KZ": false, "": false, "ru": false} {
		if got := IsAllowedCountry(code); got != want {
			t.Errorf("IsAllowedCountry(%q) = %v, want %v", code, got, want)
		}
	}
}

// Политика определяется списком, а не кодом: добавление страны — данные.
func TestCountryAllowedList(t *testing.T) {
	ruOnly := map[string]bool{"RU": true}
	withPL := map[string]bool{"RU": true, "BY": true, "PL": true}
	noRU := map[string]bool{"BY": true}
	tests := []struct {
		name     string
		allowed  map[string]bool
		explicit string
		policy   CountryPolicy
		code     string
		inScope  bool
	}{
		{"BY вне списка", ruOnly, "Беларусь", NoFallback, "BY", false},
		{"PL вне списка", ruOnly, "Польша", NoFallback, "PL", false},
		{"PL добавлена", withPL, "Польша", NoFallback, "PL", true},
		{"RU остаётся", withPL, "Россия", NoFallback, "RU", true},
		{"KZ не добавлен", withPL, "Казахстан", NoFallback, "KZ", false},
		{"fallback RU без RU в списке", noRU, "", RUOnlyFallback, "RU", false},
		{"явная RU без RU в списке", noRU, "Россия", NoFallback, "RU", false},
		{"нет страны, NoFallback", withPL, "", NoFallback, "", false},
	}
	for _, tt := range tests {
		code, inScope := countryIn(tt.explicit, tt.policy, tt.allowed)
		if code != tt.code || inScope != tt.inScope {
			t.Errorf("%s: countryIn(%q) = (%q, %v), want (%q, %v)", tt.name, tt.explicit, code, inScope, tt.code, tt.inScope)
		}
	}
}
