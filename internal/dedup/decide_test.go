package dedup

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/normalize"
)

func oct(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

// TestTracerPair — реальная пара: Марафонец 90435 (prepared.golden.json) и
// строка «27 03.10» ПроБЕГа (samples/probeg-year_ahead-2026-09-30.html).
func TestTracerPair(t *testing.T) {
	a := event.Event{
		Title: "Трейл «Уральский вулкан: гора Липовая»", TitleNorm: "трейл уральский вулкан гора липовая",
		StartDate: oct(3), CountryCode: "RU", Region: "Свердловская область",
		City: "Черноисточинск", CityKey: "chernoistochinsk", Type: event.TypeTrail,
		Distances:       []event.Distance{{Meters: 2000}, {Meters: 10000}, {Meters: 20000}},
		RegistrationURL: "https://myrace.info/events/2080",
	}
	bTitle := "Трейл «Гора Липовая — Уральский вулкан»"
	bCity := normalize.City("пос. Черноисточинск")
	b := event.Event{
		Title: bTitle, TitleNorm: normalize.TitleNorm(bTitle), StartDate: oct(3), CountryCode: "RU",
		Region: "Свердловская область", City: bCity, CityKey: normalize.CityKey(bCity), Type: event.TypeOther,
		Distances: []event.Distance{{Meters: 20000}, {Meters: 10000}, {Meters: 2000}, {Meters: 1000}},
	}
	side := func(id int64, code string, e event.Event) Side {
		return Side{EventID: id, Sources: []SourceRef{{Code: code}}, Title: e.Title, Start: e.StartDate,
			CountryCode: e.CountryCode, CityKey: e.CityKey, RegURL: e.RegistrationURL}
	}
	p := Pair{A: side(1, "marathonec", a), B: side(2, "probeg", b), UniquePartner: true}

	sim := NameSim(a.Title, b.Title)
	if got := fmt.Sprintf("%.2f", sim); got != "0.89" {
		t.Fatalf("NameSim = %s, want 0.89", got)
	}
	off, r := Decide(p, DefaultConfig())
	if off != Candidate || !r.DatesOverlap || !r.SameCity {
		t.Fatalf("Decide(default) = %v %+v, want candidate with overlap and same city", off, r)
	}
	c := DefaultConfig()
	c.AutoMerge = true
	on, _ := Decide(p, c)
	if on != AutoMerge {
		t.Fatalf("Decide(auto on) = %v, want auto", on)
	}

	f := event.Fold([]event.Part{{Source: "probeg", Event: b}, {Source: "marathonec", Event: a}}, DefaultConfig().Fold())
	var ms []string
	for _, d := range f.Distances {
		ms = append(ms, fmt.Sprint(d.Meters))
	}
	if f.Title != a.Title || f.TitleNorm != a.TitleNorm || f.Type != event.TypeTrail ||
		f.RegistrationURL != a.RegistrationURL || f.CityKey != "chernoistochinsk" ||
		strings.Join(ms, ",") != "1000,2000,10000,20000" {
		t.Fatalf("Fold = %+v", f)
	}
	title := "probeg"
	if f.Title == a.Title {
		title = "marathonec"
	}
	t.Logf("TRACER sim=%.2f off=%v on=%v distances=%s title=%s type=%s", sim, off, on, strings.Join(ms, ","), title, f.Type)
}

// same — две стороны с одинаковыми названиями, всё для автослияния.
func same() Pair {
	s := func(id int64, code string) Side {
		return Side{EventID: id, Sources: []SourceRef{{Code: code}}, Title: "Окская тропа", Start: oct(24),
			CountryCode: "RU", CityKey: "ryazan"}
	}
	return Pair{A: s(1, "marathonec"), B: s(2, "probeg"), UniquePartner: true}
}

func autoOn() Config { c := DefaultConfig(); c.AutoMerge = true; return c }

func TestDecideDates(t *testing.T) {
	tests := []struct {
		name              string
		aStart, aEnd, bSt time.Time
		want              Verdict
		overlap           bool
		gate              string
	}{
		{"один день", oct(24), time.Time{}, oct(24), AutoMerge, true, ""},
		{"соседние дни", oct(24), time.Time{}, oct(25), Candidate, false, ""},
		{"через день", oct(24), time.Time{}, oct(26), Separate, false, "dates"},
		{"многодневное пересечение", oct(24), oct(25), oct(25), AutoMerge, true, ""},
		{"многодневное, соседний день", oct(24), oct(25), oct(26), Candidate, false, ""},
		{"End раньше Start — однодневное", oct(24), oct(20), oct(25), Candidate, false, ""},
		{"End раньше Start, через день", oct(24), oct(20), oct(26), Separate, false, "dates"},
		{"нулевой Start", time.Time{}, time.Time{}, oct(24), Separate, false, "dates"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := same()
			p.A.Start, p.A.End, p.B.Start = tt.aStart, tt.aEnd, tt.bSt
			v, r := Decide(p, autoOn())
			if v != tt.want || r.DatesOverlap != tt.overlap || r.Gate != tt.gate {
				t.Fatalf("Decide = %v %+v, want %v overlap=%v gate=%q", v, r, tt.want, tt.overlap, tt.gate)
			}
			// Зеркально — тот же результат.
			p.A, p.B = p.B, p.A
			if v2, _ := Decide(p, autoOn()); v2 != v {
				t.Fatalf("mirror = %v, want %v", v2, v)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	off := DefaultConfig()
	if off.AutoMerge {
		t.Fatal("DefaultConfig().AutoMerge = true")
	}
	tests := []struct {
		name string
		mod  func(*Pair)
		c    Config
		want Verdict
		gate string
	}{
		{"всё для автослияния", func(*Pair) {}, autoOn(), AutoMerge, ""},
		{"страна пустая", func(p *Pair) { p.A.CountryCode, p.B.CountryCode = "", "" }, autoOn(), Separate, "country"},
		{"страна разная", func(p *Pair) { p.B.CountryCode = "BY" }, autoOn(), Separate, "country"},
		{"город разный (D-06)", func(p *Pair) { p.B.CityKey = "moskva" }, autoOn(), Candidate, ""},
		{"город пустой у обеих (D-06)", func(p *Pair) { p.A.CityKey, p.B.CityKey = "", "" }, autoOn(), Candidate, ""},
		{"партнёр не единственный", func(p *Pair) { p.UniquePartner = false }, autoOn(), Candidate, ""},
		{"автослияние выключено (D-08)", func(*Pair) {}, off, Candidate, ""},
		{"названия разные", func(p *Pair) { p.A.Title, p.B.Title = "Белые ночи", "Алые паруса" }, autoOn(), Separate, "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := same()
			tt.mod(&p)
			if v, r := Decide(p, tt.c); v != tt.want || r.Gate != tt.gate {
				t.Fatalf("Decide = %v %+v, want %v gate=%q", v, r, tt.want, tt.gate)
			}
		})
	}

	t.Run("границы порогов", func(t *testing.T) {
		p := same()
		p.A.Title, p.B.Title = "Жемчужина золотого кольца", "Суздальский жемчужина золотого кольца"
		s := NameSim(p.A.Title, p.B.Title)
		c := autoOn()
		c.Low, c.High = s, s
		if v, r := Decide(p, c); v != AutoMerge || r.Score != s {
			t.Fatalf("score == Low == High: %v %+v, want auto", v, r)
		}
		c.AutoMerge = false
		if v, _ := Decide(p, c); v != Candidate {
			t.Fatalf("score == Low: %v, want candidate", v)
		}
	})

	t.Run("JSON", func(t *testing.T) {
		_, r := Decide(same(), off)
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"name_sim", "score", "dates_overlap", "same_city"} {
			if _, ok := m[k]; !ok {
				t.Errorf("нет ключа %s в %s", k, b)
			}
		}
		for _, k := range []string{"link", "group_key", "gate"} {
			if _, ok := m[k]; ok {
				t.Errorf("лишний ключ %s в %s", k, b)
			}
		}
	})

	for v, want := range map[Verdict]string{Separate: "separate", Candidate: "candidate", AutoMerge: "auto"} {
		if v.String() != want {
			t.Errorf("%d.String() = %q, want %q", v, v.String(), want)
		}
	}
}

func TestDecideSources(t *testing.T) {
	ref := func(code, gk string) SourceRef { return SourceRef{Code: code, GroupKey: gk} }
	tests := []struct {
		name    string
		a, b    []SourceRef
		want    Verdict
		byGroup bool
	}{
		{"marathonec с обеих сторон без ключа", []SourceRef{ref("marathonec", "")}, []SourceRef{ref("marathonec", "")}, Separate, false},
		{"probeg series:x с обеих сторон", []SourceRef{ref("probeg", "series:x")}, []SourceRef{ref("probeg", "series:x")}, AutoMerge, true},
		{"series:x и series:y", []SourceRef{ref("probeg", "series:x")}, []SourceRef{ref("probeg", "series:y")}, Separate, false},
		{"ключ у одной стороны", []SourceRef{ref("probeg", "series:x")}, []SourceRef{ref("probeg", "")}, Separate, false},
		{"marathonec+probeg против probeg без ключа", []SourceRef{ref("marathonec", ""), ref("probeg", "")}, []SourceRef{ref("probeg", "")}, Separate, false},
		{"разные источники", []SourceRef{ref("marathonec", "")}, []SourceRef{ref("probeg", "")}, AutoMerge, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := same()
			p.A.Sources, p.B.Sources = tt.a, tt.b
			v, r := Decide(p, autoOn())
			gate := ""
			if tt.want == Separate {
				gate = "source"
			}
			if v != tt.want || r.GroupKey != tt.byGroup || r.Gate != gate {
				t.Fatalf("Decide = %v %+v, want %v group_key=%v gate=%q", v, r, tt.want, tt.byGroup, gate)
			}
		})
	}
	// Дистанции на решение не влияют структурно: в Side их нет (DDP-05).
}

func TestDecideLink(t *testing.T) {
	base := func() Pair {
		p := same()
		p.A.Title, p.B.Title = "Жемчужина золотого кольца", "Суздальский жемчужина золотого кольца"
		p.A.RegURL, p.B.RegURL = "https://myrace.info/events/2080", "http://www.myrace.info/events/2080/"
		return p
	}
	s := NameSim(base().A.Title, base().B.Title)
	cfg := func(low, high, bonus float64) Config {
		c := autoOn()
		c.Low, c.High, c.LinkBonus = low, high, bonus
		return c
	}
	tests := []struct {
		name  string
		mod   func(*Pair)
		c     Config
		want  Verdict
		link  bool
		score float64
	}{
		{"схожесть ниже Low — ссылка не помогает", func(*Pair) {}, cfg(s+0.01, s+0.05, 0.10), Separate, false, s},
		// D-20 (link-strict): бонус поднимает Score выше High, но
		// автослияние требует NameSim ≥ High — остаётся кандидат.
		{"NameSim в [High−бонус, High) с общей ссылкой — кандидат", func(*Pair) {}, cfg(s-0.01, s+0.05, 0.10), Candidate, true, math.Min(1, s+0.10)},
		{"NameSim = High с общей ссылкой — автослияние", func(*Pair) {}, cfg(s-0.01, s, 0.10), AutoMerge, true, math.Min(1, s+0.10)},
		{"без общей ссылки", func(p *Pair) { p.B.RegURL = "https://myrace.info/events/2081" }, cfg(s-0.01, s+0.05, 0.10), Candidate, false, s},
		{"общая ссылка соцсети", func(p *Pair) { p.A.RegURL, p.B.RegURL = "https://vk.com/club1", "https://vk.com/club1" }, cfg(s-0.01, s+0.05, 0.10), Candidate, false, s},
		{"LinkBonus 0", func(*Pair) {}, cfg(s-0.01, s+0.05, 0), Candidate, true, s},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base()
			tt.mod(&p)
			v, r := Decide(p, tt.c)
			if v != tt.want || r.Link != tt.link || math.Abs(r.Score-tt.score) > 1e-9 || r.NameSim != s {
				t.Fatalf("Decide = %v %+v, want %v link=%v score=%v", v, r, tt.want, tt.link, tt.score)
			}
		})
	}
}

// TestDecideWouldAuto — признак потенциального автослияния не зависит от
// DEDUP_AUTO_MERGE; при выключенном флаге вердикт AutoMerge невозможен (D-21).
func TestDecideWouldAuto(t *testing.T) {
	tests := []struct {
		name  string
		mod   func(*Pair)
		would bool
	}{
		{"все условия", func(*Pair) {}, true},
		{"партнёр не единственный", func(p *Pair) { p.UniquePartner = false }, false},
		{"соседние дни", func(p *Pair) { p.B.Start = oct(25) }, false},
		{"город разный", func(p *Pair) { p.B.CityKey = "moskva" }, false},
		{"схожесть между Low и High", func(p *Pair) {
			p.A.Title, p.B.Title = "Жемчужина золотого кольца", "Суздальский жемчужина золотого кольца"
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := same()
			tt.mod(&p)
			vOff, rOff := Decide(p, DefaultConfig())
			vOn, rOn := Decide(p, autoOn())
			if rOff.WouldAuto != tt.would || rOn.WouldAuto != tt.would {
				t.Fatalf("WouldAuto off=%v on=%v, want %v", rOff.WouldAuto, rOn.WouldAuto, tt.would)
			}
			wantOn, wantOff := Candidate, Candidate
			if tt.would {
				wantOn = AutoMerge
			}
			if vOff != wantOff || vOn != wantOn {
				t.Fatalf("verdict off=%v on=%v, want %v %v", vOff, vOn, wantOff, wantOn)
			}
		})
	}
}

// TestDecideOffNeverAuto — перебор вариантов пары и порогов (D-10, слой 2):
// при выключенном флаге вердикта AutoMerge нет; reasons при обоих
// значениях флага одинаковы; WouldAuto ⇔ AutoMerge при включённом флаге.
func TestDecideOffNeverAuto(t *testing.T) {
	type mod func(*Pair)
	titles := []mod{
		func(*Pair) {},
		func(p *Pair) { p.A.Title, p.B.Title = "Белые ночи", "Алые паруса" },
		func(p *Pair) {
			p.A.Title, p.B.Title = "Жемчужина золотого кольца", "Суздальский жемчужина золотого кольца"
		},
	}
	dates := []mod{
		func(*Pair) {},
		func(p *Pair) { p.B.Start = oct(25) },
		func(p *Pair) { p.B.Start = oct(26) },
		func(p *Pair) { p.A.End, p.B.Start = oct(25), oct(25) },
	}
	cities := []mod{
		func(*Pair) {},
		func(p *Pair) { p.B.CityKey = "moskva" },
		func(p *Pair) { p.A.CityKey, p.B.CityKey = "", "" },
	}
	unique := []mod{func(*Pair) {}, func(p *Pair) { p.UniquePartner = false }}
	sources := []mod{
		func(*Pair) {},
		func(p *Pair) { p.B.Sources = []SourceRef{{Code: "marathonec"}} },
		func(p *Pair) {
			p.A.Sources = []SourceRef{{Code: "probeg", GroupKey: "series:x"}}
			p.B.Sources = []SourceRef{{Code: "probeg", GroupKey: "series:x"}}
		},
	}
	links := []mod{
		func(*Pair) {},
		func(p *Pair) {
			p.A.RegURL, p.B.RegURL = "https://myrace.info/events/2080", "https://myrace.info/events/2080"
		},
	}
	mid := NameSim("Жемчужина золотого кольца", "Суздальский жемчужина золотого кольца")
	configs := []func(*Config){
		func(*Config) {},
		func(c *Config) { c.Low, c.High = mid, mid },
		func(c *Config) { c.LinkBonus = 0.5 },
	}
	n, would := 0, 0
	for _, a := range titles {
		for _, b := range dates {
			for _, c := range cities {
				for _, d := range unique {
					for _, e := range sources {
						for _, f := range links {
							for _, g := range configs {
								p := same()
								for _, m := range []mod{a, b, c, d, e, f} {
									m(&p)
								}
								off := DefaultConfig()
								g(&off)
								on := off
								on.AutoMerge = true
								vOff, rOff := Decide(p, off)
								vOn, rOn := Decide(p, on)
								n++
								if rOn.WouldAuto {
									would++
								}
								if vOff == AutoMerge {
									t.Fatalf("%+v: AutoMerge при выключенном флаге", p)
								}
								if rOff != rOn {
									t.Fatalf("%+v: reasons off %+v != on %+v", p, rOff, rOn)
								}
								if rOn.WouldAuto != (vOn == AutoMerge) {
									t.Fatalf("%+v: WouldAuto %v, verdict on %v", p, rOn.WouldAuto, vOn)
								}
								if want := vOn; want == AutoMerge && vOff != Candidate || want != AutoMerge && vOff != want {
									t.Fatalf("%+v: verdict off %v, on %v", p, vOff, vOn)
								}
							}
						}
					}
				}
			}
		}
	}
	if would == 0 || would == n {
		t.Fatalf("перебор без разнообразия: would_auto %d из %d", would, n)
	}
	t.Logf("OFFNEVERAUTO cases=%d would_auto=%d", n, would)
}
