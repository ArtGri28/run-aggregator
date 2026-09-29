package normalize

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// distanceRe matches "number + optional spaces + unit" with a decimal point
// or comma. Go's \b is ASCII-only, so the "no letter after the unit" check
// lives in distanceMatches.
var distanceRe = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(км|km|м|m)`)

// distanceMatches returns submatch indexes of distanceRe whose unit is not
// followed by a letter ("10 мин" is not 10 m).
func distanceMatches(s string) [][]int {
	var out [][]int
	for _, m := range distanceRe.FindAllStringSubmatchIndex(s, -1) {
		if r, _ := utf8.DecodeRuneInString(s[m[1]:]); unicode.IsLetter(r) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// namedDistances are whole words only: "марафонец" is not a distance.
// Half marathon is 21097.5 m rounded.
var namedDistances = map[string]int{
	"полумарафон": 21098,
	"марафон":     42195,
}

// Distances extracts distances in whole meters from free text: "21,1 км",
// "42.2 km", "500 м", "полумарафон". Result is sorted and unique, nil if
// none; durations like "1 час" are not distances (MVP.md §16).
func Distances(s string) []int {
	var out []int
	for _, m := range distanceMatches(s) {
		num := strings.Replace(s[m[2]:m[3]], ",", ".", 1)
		if unit := strings.ToLower(s[m[4]:m[5]]); unit == "км" || unit == "km" {
			num += "e3" // exact decimal shift: 21.0975 km -> 21097.5 m
		}
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			continue
		}
		// event_distances.distance_m is a positive INTEGER.
		if meters := math.Round(v); meters > 0 && meters <= math.MaxInt32 {
			out = append(out, int(meters))
		}
	}
	for _, w := range strings.FieldsFunc(yoToE(strings.ToLower(s)), notLetter) {
		if d, ok := namedDistances[w]; ok {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// removeDistances blanks every distance mention matched by distanceRe.
func removeDistances(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range distanceMatches(s) {
		b.WriteString(s[last:m[0]])
		b.WriteByte(' ')
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func notLetter(r rune) bool { return !unicode.IsLetter(r) }
