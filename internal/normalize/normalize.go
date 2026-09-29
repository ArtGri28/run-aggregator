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
	romanRe = regexp.MustCompile(`^M{0,3}(CM|CD|D?C{0,3})(XC|XL|L?X{0,3})(IX|IV|V?I{0,3})$`)
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
	for _, w := range strings.FieldsFunc(removeDistances(yoToE(s)), notLetterOrDigit) {
		if isEdition(w) {
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

// isEdition reports an upper-case roman numeral ("XV"; Cyrillic Х counts
// as X). "Mix" is not one.
func isEdition(w string) bool {
	return w != "" && romanRe.MatchString(strings.ReplaceAll(w, "Х", "X"))
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

var (
	dmyRe = regexp.MustCompile(`^(\d{1,2})([./])(\d{1,2})([./])(\d{4})$`)
	isoRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	dmRe  = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})$`)
)

// Date parses dd/mm/yyyy (Марафонец), dd.mm.yyyy, yyyy-mm-dd and
// year-less dd.mm (ПроБЕГ). For dd.mm the year is now's, or the next one
// if the month is before now's month. Result is midnight UTC; impossible
// dates (31.02) are rejected.
func Date(s string, now time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	var y, m, d int
	if p := dmyRe.FindStringSubmatch(s); p != nil && p[2] == p[4] {
		d, m, y = atoi(p[1]), atoi(p[3]), atoi(p[5])
	} else if p := isoRe.FindStringSubmatch(s); p != nil {
		y, m, d = atoi(p[1]), atoi(p[2]), atoi(p[3])
	} else if p := dmRe.FindStringSubmatch(s); p != nil {
		d, m, y = atoi(p[1]), atoi(p[2]), now.Year()
		if time.Month(m) < now.Month() {
			y++
		}
	} else {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || t.Month() != time.Month(m) || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
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
