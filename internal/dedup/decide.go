package dedup

import (
	"time"

	"aggregator/internal/event"
)

// SourceRef — запись источника, вошедшая в событие.
type SourceRef struct {
	Code     string
	GroupKey string
}

// Side — сторона пары: корневое событие и его записи. Дистанций нет —
// в решении о паре они не участвуют (DDP-05).
type Side struct {
	EventID     int64
	Sources     []SourceRef
	Title       string
	Start, End  time.Time
	CountryCode string
	CityKey     string
	RegURL      string
	// Sport — код вида спорта; пустой означает бег.
	Sport event.Sport
}

// Pair — пара событий. UniquePartner считает Pass (05-05): у каждой
// стороны не больше одного партнёра со схожестью не ниже High.
type Pair struct {
	A, B          Side
	UniquePartner bool
}

// Verdict — решение по паре.
type Verdict int

const (
	Separate Verdict = iota
	Candidate
	AutoMerge
)

func (v Verdict) String() string {
	switch v {
	case Candidate:
		return "candidate"
	case AutoMerge:
		return "auto"
	}
	return "separate"
}

// Reasons — почему принято решение; пишется в dedup_candidates.reasons и
// показывается в /dupes.
type Reasons struct {
	NameSim float64 `json:"name_sim"`
	// Score — NameSim плюс бонус ссылки; влияет на порядок, не на автослияние (D-20).
	Score        float64 `json:"score"`
	Link         bool    `json:"link,omitempty"`
	DatesOverlap bool    `json:"dates_overlap"`
	SameCity     bool    `json:"same_city"`
	// GroupKey — пара записей одного источника допущена по равному group_key.
	GroupKey bool `json:"group_key,omitempty"`
	// Gate — почему Separate: country, sport, source, dates, name.
	Gate string `json:"gate,omitempty"`
	// WouldAuto — пара выполняет все условия автослияния (NameSim не ниже
	// High без бонуса ссылки, пересечение дат, равный непустой CityKey,
	// UniquePartner) независимо от DEDUP_AUTO_MERGE (D-21 Phase 5). Сам
	// признак ничего не сливает. Отсутствие ключа в старых reasons — false.
	WouldAuto bool `json:"would_auto,omitempty"`
}

// Decide решает судьбу пары. Ворота, любое закрытое → Separate:
//   - country: равный непустой CountryCode (DDP-01);
//   - sport: один вид спорта; события разных видов спорта — отдельные
//     события, пары нет и кандидатом она не становится. Пустой код — бег;
//   - source: записи одного источника по разные стороны — только с равным
//     непустым GroupKey (DDP-05);
//   - dates: календарные диапазоны [Start, End или Start] отстоят не больше
//     чем на 1 день (D-05, DDP-01);
//   - name: NameSim не ниже Low (DDP-02, DDP-03).
//
// Общая ссылка регистрации прибавляет LinkBonus к Score только при NameSim
// не ниже Low (DDP-04). Reasons.WouldAuto — NameSim не ниже High без бонуса
// ссылки (D-20), пересечение дат (D-05), равный непустой CityKey (D-06) и
// UniquePartner (DDP-03) — считается при любом c.AutoMerge. AutoMerge —
// только при c.AutoMerge и WouldAuto (D-08); иначе Candidate.
func Decide(p Pair, c Config) (Verdict, Reasons) {
	var r Reasons
	a, b := p.A, p.B
	if a.CountryCode == "" || a.CountryCode != b.CountryCode {
		r.Gate = "country"
		return Separate, r
	}
	if event.SportOf(string(a.Sport)) != event.SportOf(string(b.Sport)) {
		r.Gate = "sport"
		return Separate, r
	}
	ok, byGroup := sourcesAllowed(a.Sources, b.Sources)
	if !ok {
		r.Gate = "source"
		return Separate, r
	}
	r.GroupKey = byGroup
	loA, hiA, okA := days(a)
	loB, hiB, okB := days(b)
	gap := max(loA, loB) - min(hiA, hiB)
	if !okA || !okB || gap > 1 {
		r.Gate = "dates"
		return Separate, r
	}
	r.DatesOverlap = gap <= 0
	r.SameCity = a.CityKey != "" && a.CityKey == b.CityKey

	s := NameSim(a.Title, b.Title)
	r.NameSim, r.Score = s, s
	if k := LinkKey(a.RegURL); s >= c.Low && k != "" && k == LinkKey(b.RegURL) {
		r.Link = true
		r.Score = min(1, s+c.LinkBonus)
	}
	if s < c.Low {
		r.Gate = "name"
		return Separate, r
	}
	r.WouldAuto = s >= c.High && r.DatesOverlap && r.SameCity && p.UniquePartner
	if c.AutoMerge && r.WouldAuto {
		return AutoMerge, r
	}
	return Candidate, r
}

// sourcesAllowed: каждая пара записей с равным кодом должна иметь равный
// непустой GroupKey; byGroup — такая пара была.
func sourcesAllowed(a, b []SourceRef) (ok, byGroup bool) {
	for _, x := range a {
		for _, y := range b {
			if x.Code != y.Code {
				continue
			}
			if x.GroupKey == "" || x.GroupKey != y.GroupKey {
				return false, false
			}
			byGroup = true
		}
	}
	return true, byGroup
}

// days — календарный диапазон стороны в днях от эпохи; End нулевой или
// раньше Start — однодневное событие; ok false при нулевом Start.
func days(s Side) (lo, hi int64, ok bool) {
	if s.Start.IsZero() {
		return 0, 0, false
	}
	lo = dayNum(s.Start)
	hi = lo
	if !s.End.IsZero() {
		hi = max(lo, dayNum(s.End))
	}
	return lo, hi, true
}

// dayNum — номер календарного дня (Y/M/D, без перевода часовых поясов:
// дата события — местная, MVP.md §4.2).
func dayNum(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}
