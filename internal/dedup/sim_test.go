package dedup

import (
	"testing"
	"unicode/utf8"
)

func TestGramsRunes(t *testing.T) {
	g := grams("абв")
	if len(g) != 4 {
		t.Fatalf("grams(абв) = %d элементов, want 4", len(g))
	}
	for k := range g {
		if n := utf8.RuneCountInString(k); n != 3 {
			t.Errorf("триграмма %q: %d рун", k, n)
		}
	}
}

func TestSimKey(t *testing.T) {
	for in, want := range map[string]string{
		"55 й пробег":  "пробег",
		"2 я лыжня":    "лыжня",
		"5 ый кросс":   "кросс",
		"кросс в лесу": "кросс лесу",
		"кросс 7 озер": "кросс озер",
		"5 верст":      "верст",
		"в 7":          "",
		"окская тропа": "окская тропа",
	} {
		if got := simKey(in); got != want {
			t.Errorf("simKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNameSim(t *testing.T) {
	exact := []struct {
		a, b string
		want float64
	}{
		{"Окская тропа", "Окская тропа", 1},
		{"Орёл", "ОРЕЛ", 1},
		{"Новотроицкий полумарафон 2026", "XV Новотроицкий полумарафон", 1},
		{"Марафон", "Марафон 2026", 0}, // пустой ключ
		// Pitfall 8: известное поведение, негативы — в калибровке 05-07.
		{"Саратовский марафон", "Саратовский полумарафон", 1},
	}
	for _, tt := range exact {
		if got := NameSim(tt.a, tt.b); got != tt.want {
			t.Errorf("NameSim(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
	bands := []struct {
		a, b   string
		lo, hi float64 // [lo, hi)
	}{
		{"окская тропа дневной", "окская тропа ночной", 0.4, 0.7},
		{"жемчужина золотого кольца", "суздальский жемчужина золотого кольца", 0.4, 0.7},
		{"белые ночи", "алые паруса", 0, 0.4},
	}
	for _, tt := range bands {
		got := NameSim(tt.a, tt.b)
		if got < tt.lo || got >= tt.hi {
			t.Errorf("NameSim(%q, %q) = %.3f, want [%v, %v)", tt.a, tt.b, got, tt.lo, tt.hi)
		}
		t.Logf("NameSim(%q, %q) = %.3f", tt.a, tt.b, got)
	}
	pairs := [][2]string{
		{"Трейл «Уральский вулкан: гора Липовая»", "Трейл «Гора Липовая — Уральский вулкан»"},
		{"Окская тропа дневной", "Окская тропа ночной"},
		{"Белые ночи", "Алые паруса"},
		{"Этноран в Калмыкии", "Этноран Республика Калмыкия"},
		{"55-й пробег", "Пробег памяти"},
	}
	for _, p := range pairs {
		ab, ba := NameSim(p[0], p[1]), NameSim(p[1], p[0])
		if ab != ba || ab < 0 || ab > 1 {
			t.Errorf("NameSim(%q, %q) = %v / %v: несимметрична или вне [0, 1]", p[0], p[1], ab, ba)
		}
	}
}

func TestLinkKey(t *testing.T) {
	for in, want := range map[string]string{
		"https://myrace.info/events/2080":       "myrace.info/events/2080",
		"http://www.myrace.info/events/2080/":   "myrace.info/events/2080",
		" HTTPS://MyRace.Info/events/2080 ":     "myrace.info/events/2080",
		"":                                      "",
		"://bad":                                "",
		"ftp://myrace.info/events/2080":         "",
		"https://myrace.info":                   "",
		"https://myrace.info/":                  "",
		"https://myrace.info/events/2080?utm=1": "",
		"https://myrace.info/events/2080?":      "",
		"https://myrace.info/events/2080#reg":   "",
		"https://vk.com/club1":                  "",
		"https://m.vk.com/x":                    "",
		"https://vk.ru/x":                       "",
		"https://t.me/x":                        "",
		"https://telegram.me/x":                 "",
		"https://ok.ru/group/1":                 "",
		"https://instagram.com/x":               "",
		"https://facebook.com/x":                "",
		"https://youtube.com/x":                 "",
	} {
		if got := LinkKey(in); got != want {
			t.Errorf("LinkKey(%q) = %q, want %q", in, got, want)
		}
	}
}
