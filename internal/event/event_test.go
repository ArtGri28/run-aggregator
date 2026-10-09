package event

import (
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
		if s.Enabled != (s.Code == SportRunning) {
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
