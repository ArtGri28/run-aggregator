package normalize

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"aggregator/internal/event"
)

var yoReplacer = strings.NewReplacer("ё", "е", "Ё", "Е")

func yoToE(s string) string { return yoReplacer.Replace(s) }

func notLetterOrDigit(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

var (
	// romanRe: edition numbers up to 299, no M or D ("MIX", "DI" are words).
	romanRe = regexp.MustCompile(`^C{0,2}(XC|XL|L?X{0,3})(IX|IV|V?I{0,3})$`)
	yearRe  = regexp.MustCompile(`^(19|20)\d\d$`)

	titleStopWords = map[string]bool{"забег": true, "марафон": true, "полумарафон": true}
)

// TitleNorm is the dedup form of a title (MVP.md §6): lower case, ё→е,
// without year, roman edition number, distance mentions and stop words.
// It is idempotent: removals can bring new neighbours together ("10 - км"
// becomes "10 км"), so steps repeat until nothing changes.
func TitleNorm(s string) string {
	for {
		next := titleNormStep(s)
		if next == s {
			return s
		}
		s = next
	}
}

func titleNormStep(s string) string {
	var out []string
	cyr := strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) })
	for _, w := range strings.FieldsFunc(removeDistances(yoToE(s)), notLetterOrDigit) {
		if isEdition(w, cyr) {
			continue
		}
		w = strings.ToLower(w)
		if yearRe.MatchString(w) || titleStopWords[w] {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// isEdition reports an upper-case roman edition number ("XV"; Cyrillic Х
// counts as X). A single I, V or X is an edition only in a title with
// Cyrillic ("X Московский марафон"): in "Run I Love" or "X-Race" it is a
// word. "Mix" and "MIX" are not numerals.
func isEdition(w string, cyrillicTitle bool) bool {
	w = strings.ReplaceAll(w, "Х", "X")
	if w == "" || !romanRe.MatchString(w) {
		return false
	}
	return len(w) >= 2 || cyrillicTitle && strings.Contains("IVX", w)
}

// MaxDescriptionRunes is the events.description limit in characters.
const MaxDescriptionRunes = 1000

// Description trims s and cuts it to MaxDescriptionRunes runes on a word
// boundary; a single over-long word is cut at the limit. No ellipsis.
func Description(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= MaxDescriptionRunes {
		return s
	}
	cut := r[:MaxDescriptionRunes]
	if !unicode.IsSpace(r[MaxDescriptionRunes]) {
		for i := len(cut) - 1; i > 0; i-- {
			if unicode.IsSpace(cut[i]) {
				cut = cut[:i]
				break
			}
		}
	}
	return strings.TrimRightFunc(string(cut), unicode.IsSpace)
}

var types = map[string]event.Type{
	"шоссе": event.TypeRoad, "shosse": event.TypeRoad, "road": event.TypeRoad,
	"трейл": event.TypeTrail, "trejl": event.TypeTrail, "trail": event.TypeTrail,
	"кросс": event.TypeCross, "cross": event.TypeCross,
	"стадион": event.TypeTrack, "track": event.TypeTrack,
	"ocr": event.TypeObstacle, "obstacle": event.TypeObstacle, "гонка с препятствиями": event.TypeObstacle,
}

// Type maps a source's type label to one of the six types (MVP.md §2.2);
// unknown labels, ultra included, are TypeOther.
func Type(s string) event.Type {
	if t, ok := types[collapseSpaces(yoToE(strings.ToLower(s)))]; ok {
		return t
	}
	return event.TypeOther
}

// skiTypes — подписи дисциплин лыж.
var skiTypes = map[string]event.Type{
	"лыжные гонки": event.TypeSki,
	"лыжероллеры":  event.TypeRollerski,
}

// SportType — дисциплина по паре «вид спорта — подпись источника». У бега —
// Type; у лыж — подписи skiTypes. Неизвестная подпись, подпись чужого вида
// спорта и вид спорта без своих дисциплин дают TypeOther.
func SportType(sport event.Sport, label string) event.Type {
	switch sport {
	case event.SportRunning:
		return Type(label)
	case event.SportSkiing:
		if t, ok := skiTypes[collapseSpaces(yoToE(strings.ToLower(label)))]; ok {
			return t
		}
	}
	return event.TypeOther
}

// NumericDistances — Distances без именованных дистанций: для записей не
// бега, где «марафон» и «полумарафон» — слова названия, а не 42 195 и
// 21 098 м.
//
// Упрощение: текст без единого числа с единицей дистанций не даёт; текст, где
// такое число есть, но не даёт положительной дистанции («0 км»), разбирается
// как у бега. Точнее — при переносе правила в сам разбор Distances.
func NumericDistances(s string) []int {
	if len(distanceMatches(asciiSpaces(s))) == 0 {
		return nil
	}
	return Distances(s)
}

var (
	dmyRe = regexp.MustCompile(`^(\d{1,2})([./])(\d{1,2})([./])(\d{4})$`)
	isoRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	dmRe  = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})$`)
)

// Date parses dd/mm/yyyy (Марафонец), dd.mm.yyyy, yyyy-mm-dd and
// year-less dd.mm (ПроБЕГ). A dd.mm date is placed in the window
// [today−1 month, today+11 months), today taken in now's location: a race
// that ended last week stays in the past instead of moving a year ahead.
// Result is midnight UTC; impossible dates (31.02) are rejected.
func Date(s string, now time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	var y, m, d int
	if p := dmyRe.FindStringSubmatch(s); p != nil && p[2] == p[4] {
		d, m, y = atoi(p[1]), atoi(p[3]), atoi(p[5])
	} else if p := isoRe.FindStringSubmatch(s); p != nil {
		y, m, d = atoi(p[1]), atoi(p[2]), atoi(p[3])
	} else if p := dmRe.FindStringSubmatch(s); p != nil {
		d, m = atoi(p[1]), atoi(p[2])
		y = yearFor(d, time.Month(m), now)
	} else {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || t.Month() != time.Month(m) || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

// yearFor picks the year that puts dd.mm into [today−1 month, today+11 months).
func yearFor(d int, m time.Month, now time.Time) int {
	y, nm, nd := now.Date()
	today := time.Date(y, nm, nd, 0, 0, 0, 0, time.UTC)
	switch t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC); {
	case t.Before(today.AddDate(0, -1, 0)):
		return y + 1
	case !t.Before(today.AddDate(0, 11, 0)):
		return y - 1
	}
	return y
}

var timeRe = regexp.MustCompile(`^(\d{1,2})[:.](\d{2})$`)

// Time parses H:MM, HH:MM, H.MM or HH.MM into "HH:MM".
func Time(s string) (string, bool) {
	p := timeRe.FindStringSubmatch(strings.TrimSpace(s))
	if p == nil {
		return "", false
	}
	h, m := atoi(p[1]), atoi(p[2])
	if h > 23 || m > 59 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d", h, m), true
}

// atoi is for regexp-validated digit groups only.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
