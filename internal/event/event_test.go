package event

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"aggregator/migrations"
)

// TestSports — справочник видов спорта (D-01, D-02) и его согласие с
// форматом колонки events.sport (миграция 00008).
func TestSports(t *testing.T) {
	const format = `^[a-z][a-z_]{1,31}$`
	sql, err := migrations.FS.ReadFile("00008_events_sport.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sql), "'"+format+"'") {
		t.Fatalf("в миграции 00008 нет выражения '%s'", format)
	}
	re := regexp.MustCompile(format)

	want := []struct {
		code  Sport
		title string
	}{
		{SportRunning, "Бег"}, {SportSkiing, "Лыжи"}, {SportSwimming, "Плавание"},
		{SportCycling, "Велоспорт"}, {SportMultisport, "Мультиспорт"},
	}
	codes := []string{"running", "skiing", "swimming", "cycling", "multisport"}
	if len(Sports) != len(want) {
		t.Fatalf("видов спорта %d, want %d", len(Sports), len(want))
	}
	seen := map[Sport]bool{}
	for i, s := range Sports {
		if s.Code != want[i].code || string(s.Code) != codes[i] || s.Title != want[i].title {
			t.Errorf("Sports[%d] = %q %q, want %q %q", i, s.Code, s.Title, codes[i], want[i].title)
		}
		if s.Emoji == "" {
			t.Errorf("%s: нет значка", s.Code)
		}
		if s.Enabled != (s.Code == SportRunning || s.Code == SportSkiing) {
			t.Errorf("%s: Enabled = %v", s.Code, s.Enabled)
		}
		if seen[s.Code] {
			t.Errorf("%s: код повторяется", s.Code)
		}
		seen[s.Code] = true
		if !re.MatchString(string(s.Code)) {
			t.Errorf("%s: код не проходит формат колонки", s.Code)
		}
		if got, ok := SportByCode(s.Code); !ok || got != s {
			t.Errorf("SportByCode(%q) = %+v, %v", s.Code, got, ok)
		}
	}
	for _, code := range []Sport{"", "chess"} {
		if _, ok := SportByCode(code); ok {
			t.Errorf("SportByCode(%q) нашёл запись", code)
		}
	}
	for in, want := range map[string]Sport{"": SportRunning, "skiing": SportSkiing, "chess": "chess"} {
		if got := SportOf(in); got != want {
			t.Errorf("SportOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSportTypes — перечень дисциплин по видам спорта (D-11) и его согласие с
// форматом колонки events.event_type (миграция 00009).
func TestSportTypes(t *testing.T) {
	const format = `^[a-z][a-z_]{1,31}$`
	sql, err := migrations.FS.ReadFile("00009_event_type_format.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sql), "'"+format+"'") {
		t.Fatalf("в миграции 00009 нет выражения '%s'", format)
	}
	re := regexp.MustCompile(format)

	want := map[Sport][]Type{
		SportRunning:  {"road", "trail", "cross", "track", "obstacle", "other"},
		SportSkiing:   {"ski", "rollerski", "other"},
		SportSwimming: {"open_water", "pool", "other"},
		SportCycling:  {"road_bike", "mtb", "gravel", "other"},
	}
	if !reflect.DeepEqual(SportTypes, want) {
		t.Fatalf("SportTypes = %v, want %v", SportTypes, want)
	}
	owner := map[Type]Sport{}
	for sport, types := range SportTypes {
		if _, ok := SportByCode(sport); !ok {
			t.Errorf("%s: вида спорта нет в справочнике", sport)
		}
		for _, typ := range types {
			if !re.MatchString(string(typ)) {
				t.Errorf("%s/%s: значение не проходит формат колонки", sport, typ)
			}
			if TypeFor(sport, typ) != typ {
				t.Errorf("TypeFor(%s, %s) = %s", sport, typ, TypeFor(sport, typ))
			}
			if prev, ok := owner[typ]; ok && typ != TypeOther {
				t.Errorf("дисциплина %s — у двух видов спорта: %s и %s", typ, prev, sport)
			}
			owner[typ] = sport
		}
	}
	// Дисциплина чужого вида спорта, неизвестная и вид спорта без своих
	// дисциплин — other.
	for _, c := range []struct {
		sport Sport
		typ   Type
	}{{SportRunning, TypeSki}, {SportSkiing, TypeRoad}, {SportSkiing, "biathlon"}, {SportSwimming, TypeRoad}, {SportSwimming, TypeSki}} {
		if got := TypeFor(c.sport, c.typ); got != TypeOther {
			t.Errorf("TypeFor(%s, %s) = %s, want other", c.sport, c.typ, got)
		}
	}
	if TypeFor(SportSwimming, TypeOther) != TypeOther {
		t.Error("other допустим у любого вида спорта")
	}
}
