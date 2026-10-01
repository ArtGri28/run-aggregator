package normalize

// countryNames — написания страны (нижний регистр) → ISO 3166-1 alpha-2.
// Справочник общий для всех стран каталога: новая страна — строки здесь,
// без правок логики. Страны из foreignNames (regions.go) покрыты, кроме
// Абхазии и Южной Осетии: у них нет собственного ISO-кода.
var countryNames = map[string]string{
	"россия": "RU", "russia": "RU", "рф": "RU", "российская федерация": "RU",
	"rus": "RU", "russian federation": "RU", "rossiya": "RU", "russian": "RU",

	"беларусь": "BY", "республика беларусь": "BY", "белоруссия": "BY", "belarus": "BY",
	"польша": "PL", "poland": "PL",

	"казахстан": "KZ", "kazakhstan": "KZ",
	"украина": "UA", "ukraine": "UA",
	"армения": "AM", "armenia": "AM",
	"азербайджан": "AZ", "azerbaijan": "AZ",
	"грузия": "GE", "georgia": "GE",
	"узбекистан": "UZ", "uzbekistan": "UZ",
	"киргизия": "KG", "кыргызстан": "KG", "kyrgyzstan": "KG",
	"таджикистан": "TJ", "tajikistan": "TJ",
	"туркменистан": "TM", "turkmenistan": "TM",
	"молдова": "MD", "молдавия": "MD", "moldova": "MD",
	"латвия": "LV", "latvia": "LV",
	"литва": "LT", "lithuania": "LT",
	"эстония": "EE", "estonia": "EE",
	"турция": "TR", "turkey": "TR",
	"сербия": "RS", "serbia": "RS",
	"черногория": "ME", "montenegro": "ME",
	"болгария": "BG", "bulgaria": "BG",
	"кипр": "CY", "cyprus": "CY",
	"оаэ": "AE", "uae": "AE",
	"таиланд": "TH", "thailand": "TH",
	"вьетнам": "VN", "vietnam": "VN",
	"китай": "CN", "china": "CN",
	"монголия": "MN", "mongolia": "MN",
	"финляндия": "FI", "finland": "FI",
	"германия": "DE",
	"франция":  "FR",
	"италия":   "IT",
	"испания":  "ES",
	"греция":   "GR",
	"египет":   "EG",
	"куба":     "CU",
}

// allowedCountries — страны каталога (ISO alpha-2). Запись источника с
// явной страной вне списка сохраняется как out_of_scope. Добавить страну —
// добавить код сюда (и, если нужно, написание в countryNames).
var allowedCountries = map[string]bool{"RU": true, "BY": true}

// IsAllowedCountry — страна входит в каталог.
func IsAllowedCountry(code string) bool { return allowedCountries[code] }
