package normalize

import (
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"aggregator/internal/event"
)

func TestTitleNorm(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Московский марафон 2026", "московский"},
		{"XV Забег «Ёлочка» 10 км", "елочка"},
		{"Полумарафон «Белые ночи» 21,1 км", "белые ночи"},
		{"ХХ Пробег памяти", "пробег памяти"}, // Cyrillic Х
		{"Mix Run", "mix run"},
		{"Трейл Буря-2026", "трейл буря"},
		{"10 - км забег", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := TitleNorm(tt.in)
		if got != tt.want {
			t.Errorf("TitleNorm(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if again := TitleNorm(got); again != got {
			t.Errorf("TitleNorm not idempotent for %q: %q -> %q", tt.in, got, again)
		}
	}
}

// words builds Cyrillic text of at least n runes from space-separated words.
func words(n int) string {
	var b strings.Builder
	for i := 0; utf8.RuneCountInString(b.String()) < n; i++ {
		b.WriteString([]string{"пробег ", "вдоль ", "набережной ", "реки ", "Невы "}[i%5])
	}
	return strings.TrimSpace(b.String())
}

func TestDescription(t *testing.T) {
	exact := func(n int) string { return strings.Repeat("ж", n) }

	for _, n := range []int{999, 1000} {
		s := exact(n)
		if got := Description(s); got != s {
			t.Errorf("Description of %d runes changed: %d runes", n, utf8.RuneCountInString(got))
		}
	}

	long := words(1500)
	got := Description(long)
	if n := utf8.RuneCountInString(got); n > MaxDescriptionRunes || n == 0 {
		t.Errorf("Description(long) has %d runes", n)
	}
	if !utf8.ValidString(got) {
		t.Error("Description(long) is not valid UTF-8")
	}
	if !strings.HasPrefix(long, got) {
		t.Error("Description(long) is not a prefix of the input")
	}
	if next, _ := utf8.DecodeRuneInString(long[len(got):]); !unicode.IsSpace(next) {
		t.Errorf("Description(long) cut mid-word: next rune %q", next)
	}

	spaceAt1001 := exact(1000) + " хвост"
	if got := Description(spaceAt1001); got != exact(1000) {
		t.Errorf("Description with space at rune 1001 has %d runes, want 1000", utf8.RuneCountInString(got))
	}

	oneWord := exact(1500)
	if got := Description(oneWord); got != exact(1000) {
		t.Errorf("Description(one word) has %d runes, want 1000", utf8.RuneCountInString(got))
	}

	if got := Description("  текст  "); got != "текст" {
		t.Errorf("Description trims to %q, want %q", got, "текст")
	}
}

func TestType(t *testing.T) {
	tests := []struct {
		in   string
		want event.Type
	}{
		{"shosse", event.TypeRoad},
		{"Шоссе", event.TypeRoad},
		{"road", event.TypeRoad},
		{"trejl", event.TypeTrail},
		{"трейл", event.TypeTrail},
		{"Trail", event.TypeTrail},
		{"кросс", event.TypeCross},
		{"cross", event.TypeCross},
		{"стадион", event.TypeTrack},
		{"track", event.TypeTrack},
		{"OCR", event.TypeObstacle},
		{"obstacle", event.TypeObstacle},
		{"гонка  с препятствиями", event.TypeObstacle},
		{"ultra", event.TypeOther},
		{"горный бег", event.TypeOther},
		{"", event.TypeOther},
	}
	for _, tt := range tests {
		if got := Type(tt.in); got != tt.want {
			t.Errorf("Type(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDate(t *testing.T) {
	now := time.Date(2026, 9, 29, 15, 4, 5, 0, time.FixedZone("MSK", 3*3600))
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"17/10/2026", day(2026, 10, 17), true},
		{"17.10.2026", day(2026, 10, 17), true},
		{"2026-10-17", day(2026, 10, 17), true},
		{"24.10", day(2026, 10, 24), true},
		{"05.01", day(2027, 1, 5), true},  // month before now -> next year
		{"31.08", day(2026, 8, 31), true}, // 29 days ago: past, not next year
		{"28.08", day(2027, 8, 28), true}, // over a month ago -> next year
		{"29.09", day(2026, 9, 29), true},
		{"01.09", day(2026, 9, 1), true},
		{"5.1", day(2027, 1, 5), true},
		{"29.08", day(2026, 8, 29), true}, // exactly a month ago: still past
		{"31.02.2026", time.Time{}, false},
		{"32/01/2026", time.Time{}, false},
		{"10 октября", time.Time{}, false},
		{"", time.Time{}, false},
	}
	for _, tt := range tests {
		got, ok := Date(tt.in, now)
		if ok != tt.ok || !got.Equal(tt.want) || (ok && got.Location() != time.UTC) {
			t.Errorf("Date(%q) = (%v, %v), want (%v, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDateAcrossBoundaries(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		in   string
		now  time.Time
		want time.Time
	}{
		{"30.09", time.Date(2026, 10, 1, 10, 0, 0, 0, msk), day(2026, 9, 30)},
		{"31.12", time.Date(2027, 1, 1, 10, 0, 0, 0, msk), day(2026, 12, 31)},
		{"05.01", time.Date(2026, 12, 20, 10, 0, 0, 0, msk), day(2027, 1, 5)},
		// 00:30 MSK on 1 October is still 30 September in UTC; today is MSK.
		{"30.09", time.Date(2026, 10, 1, 0, 30, 0, 0, msk), day(2026, 9, 30)},
		{"29.02", time.Date(2027, 12, 15, 10, 0, 0, 0, msk), day(2028, 2, 29)},
	}
	for _, tt := range tests {
		got, ok := Date(tt.in, tt.now)
		if !ok || !got.Equal(tt.want) {
			t.Errorf("Date(%q, %v) = (%v, %v), want %v", tt.in, tt.now, got, ok, tt.want)
		}
	}
	if _, ok := Date("29.02", time.Date(2026, 9, 29, 10, 0, 0, 0, msk)); ok {
		t.Errorf("Date(29.02) in a non-leap target year: ok = true, want false")
	}
}

func TestTime(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"10:00", "10:00", true},
		{"9:05", "09:05", true},
		{"10.30", "10:30", true},
		{"24:00", "", false},
		{"10:60", "", false},
		{"10", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := Time(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Time(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
