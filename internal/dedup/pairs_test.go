package dedup

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestWindowPairs(t *testing.T) {
	side := func(id int64, country string, start, end time.Time) Side {
		return Side{EventID: id, CountryCode: country, Start: start, End: end, Title: "Окская тропа",
			Sources: []SourceRef{{Code: fmt.Sprint("s", id)}}, CityKey: "ryazan"}
	}
	sides := []Side{
		side(5, "RU", oct(10), time.Time{}),
		side(3, "RU", oct(10), time.Time{}), // тот же день, меньший EventID — раньше в порядке
		side(7, "BY", oct(10), time.Time{}), // другая страна — без пар
		side(4, "RU", oct(12), time.Time{}), // ровно +2 дня от 10.10 — в окне
		side(6, "RU", oct(13), time.Time{}), // +3 дня от 10.10 — вне окна; +1 от 12.10
		side(2, "RU", oct(1), oct(11)),      // многодневное: окно до 13.10
	}
	var got []string
	WindowPairs(sides, func(i, j int) {
		got = append(got, fmt.Sprintf("%d-%d", sides[i].EventID, sides[j].EventID))
	})
	want := []string{"2-3", "2-5", "2-4", "2-6", "3-5", "3-4", "5-4", "4-6"}
	if !slices.Equal(got, want) {
		t.Fatalf("пары окна = %v, want %v", got, want)
	}

	// Pairs: стороны ориентированы по EventID; партнёры — по NameSim ≥ High;
	// у события с двумя партнёрами UniquePartner = false (Candidate, не auto).
	c := autoOn()
	trio := []Side{side(1, "RU", oct(10), time.Time{}), side(2, "RU", oct(10), time.Time{}), side(3, "RU", oct(20), time.Time{})}
	pairs, partners := Pairs(trio, c, nil)
	if len(pairs) != 1 || pairs[0].A != 0 || pairs[0].B != 1 || pairs[0].Verdict != AutoMerge || partners[1] != 1 || partners[2] != 1 {
		t.Fatalf("Pairs(две стороны) = %+v, partners %v", pairs, partners)
	}
	trio[2].Start = oct(10)
	pairs, partners = Pairs(trio, c, nil)
	if len(pairs) != 3 || partners[1] != 2 || partners[2] != 2 || partners[3] != 2 {
		t.Fatalf("Pairs(три стороны) = %+v, partners %v", pairs, partners)
	}
	for _, p := range pairs {
		if p.Verdict != Candidate || p.Reasons.WouldAuto || trio[p.A].EventID > trio[p.B].EventID {
			t.Fatalf("пара %+v: want candidate без would_auto, A с меньшим EventID", p)
		}
	}
	// skip исключает пару до Decide; партнёры считаются без неё.
	pairs, partners = Pairs(trio, c, func(a, b int) bool { return trio[a].EventID == 1 && trio[b].EventID == 2 })
	if len(pairs) != 2 || partners[1] != 1 || partners[2] != 1 || partners[3] != 2 {
		t.Fatalf("Pairs(skip 1-2) = %+v, partners %v", pairs, partners)
	}
}
