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
		{"", ""},
		{"  (—) ", ""},
		{long, ""}, // checked by invariant only
	}
	for _, tt := range tests {
		got := CityKey(tt.in)
		if tt.in != long && got != tt.want {
			t.Errorf("CityKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if got != "" && (len(got) > 64 || !cityKeyRe.MatchString(got)) {
			t.Errorf("CityKey(%q) = %q violates city_key invariant (len %d)", tt.in, got, len(got))
		}
	}
	if got := CityKey(long); got == "" || len(got) > 64 {
		t.Errorf("CityKey(long) = %q (len %d), want non-empty and <= 64 bytes", got, len(got))
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
		{"Беларусь", ""},
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
		// 3. explicit other country: out of scope under any policy
		{"Беларусь", RUOnlyFallback, "", false},
		{"BY", NoFallback, "BY", false},
		{"KZ", RUOnlyFallback, "KZ", false},
		{"Belarus", RUOnlyFallback, "", false},
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
