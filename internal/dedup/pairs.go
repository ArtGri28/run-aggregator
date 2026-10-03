package dedup

import (
	"cmp"
	"slices"
)

// WindowPairs вызывает fn для каждой пары окна: стороны одной страны, начало
// второй не позже конца первой + 2 дня (окно шире ворот Decide ±1 день).
// Стороны перебираются в порядке (Start, EventID); i и j — индексы в sides,
// у sides[i] начало не позже, чем у sides[j]. Вход не меняется.
func WindowPairs(sides []Side, fn func(i, j int)) {
	order := make([]int, len(sides))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(x, y int) int {
		return cmp.Or(sides[x].Start.Compare(sides[y].Start), cmp.Compare(sides[x].EventID, sides[y].EventID))
	})
	for n, i := range order {
		end := sides[i].Start
		if sides[i].End.After(end) {
			end = sides[i].End
		}
		limit := end.AddDate(0, 0, 2)
		for _, j := range order[n+1:] {
			if sides[j].Start.After(limit) {
				break
			}
			if sides[i].CountryCode == sides[j].CountryCode {
				fn(i, j)
			}
		}
	}
}

// ScoredPair — пара после Decide; A и B — индексы в sides, у A меньший EventID.
type ScoredPair struct {
	A, B    int
	Verdict Verdict
	Reasons Reasons
}

// Pairs — пары окна с вердиктом не Separate, как их видит Pass: Decide без
// UniquePartner, затем партнёры — пары с NameSim не ниже High (по чистой
// схожести, как порог автослияния, D-20; Pitfall 9), затем Decide с
// UniquePartner. skip (может быть nil) исключает пару до Decide —
// отклонённые администратором пары по корням (DDP-07). partners — число
// таких партнёров у каждого EventID; калибровка берёт его для размеченных
// пар.
func Pairs(sides []Side, c Config, skip func(a, b int) bool) (pairs []ScoredPair, partners map[int64]int) {
	WindowPairs(sides, func(i, j int) {
		a, b := i, j
		if sides[a].EventID > sides[b].EventID {
			a, b = b, a
		}
		if skip != nil && skip(a, b) {
			return
		}
		if v, r := Decide(Pair{A: sides[a], B: sides[b]}, c); v != Separate {
			pairs = append(pairs, ScoredPair{A: a, B: b, Verdict: v, Reasons: r})
		}
	})
	partners = map[int64]int{}
	for _, p := range pairs {
		if p.Reasons.NameSim >= c.High {
			partners[sides[p.A].EventID]++
			partners[sides[p.B].EventID]++
		}
	}
	for i := range pairs {
		p := &pairs[i]
		a, b := sides[p.A], sides[p.B]
		p.Verdict, p.Reasons = Decide(Pair{A: a, B: b, UniquePartner: partners[a.EventID] <= 1 && partners[b.EventID] <= 1}, c)
	}
	return pairs, partners
}
