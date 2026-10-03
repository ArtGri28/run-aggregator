package dedup_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"slices"
	"testing"
	"time"

	"aggregator/internal/dedup"
	"aggregator/internal/ingest"
	mcsrc "aggregator/internal/ingest/sources/marathonec"
	pbsrc "aggregator/internal/ingest/sources/probeg"
)

// calibDump печатает пары окна для подбора калибровочного набора.
var calibDump = flag.Bool("calib-dump", false, "TestCalibration: печатать пары окна фикстур (DUMP)")

const calibFile = "testdata/calibration.json"

// Фикстуры источников и моменты их загрузки (testdata/README.md).
var calibSources = []struct {
	code, path, url string
	now             time.Time
	parse           func(ingest.RawDoc) ([]ingest.SourceEvent, error)
	policy          ingest.Policy
}{
	{mcsrc.Code, "../ingest/sources/marathonec/testdata/calendar-beg.html", mcsrc.CalendarURL,
		time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), mcsrc.Parse, mcsrc.New(nil).Policy()},
	{pbsrc.Code, "../ingest/sources/probeg/testdata/year_ahead-2026-09-30.html", pbsrc.CalendarURL,
		time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), pbsrc.Parse, pbsrc.New(nil).Policy()},
}

type calibRef struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
}

func (r calibRef) key() string { return r.Source + "/" + r.ExternalID }

// calibPair — запись calibration.json (формат — testdata/README.md).
type calibPair struct {
	ID        string   `json:"id"`
	A         calibRef `json:"a"`
	B         calibRef `json:"b"`
	Label     string   `json:"label"`
	Kind      string   `json:"kind"`
	Synthetic bool     `json:"synthetic"`
	Proposed  bool     `json:"proposed"`
	Note      string   `json:"note"`
	Reason    string   `json:"reason"`
}

var (
	calibLabels = []string{"same", "different"}
	calibKinds  = []string{"dup", "dup-other-city", "same-name-different", "all-russia", "edge", "same-source", "other-year"}
	calibIDRe   = regexp.MustCompile(`^[ca][0-9]{2}$`) // a — AUTO-пары окна фикстур, проверенные владельцем
)

// calibEvent — событие каталога из фикстуры.
type calibEvent struct {
	key  string
	city string
	// info — дистанции и ссылка регистрации, только для -calib-dump.
	info string
	side dedup.Side
}

// loadCatalog — события в каталоге из обеих фикстур (Parse + Prepare, только
// InScope); Side — те же поля, что берёт pass.go из события.
func loadCatalog(t *testing.T) []calibEvent {
	t.Helper()
	var out []calibEvent
	for _, src := range calibSources {
		body, err := os.ReadFile(src.path)
		if err != nil {
			t.Fatalf("%s: %v", src.code, err)
		}
		recs, err := src.parse(ingest.RawDoc{URL: src.url, Body: body, FetchedAt: src.now})
		if err != nil {
			t.Fatalf("%s: Parse: %v", src.code, err)
		}
		prep, _ := ingest.Prepare(recs, src.now, src.policy)
		for _, p := range prep {
			if !p.InScope {
				continue
			}
			e := p.Event
			s := dedup.Side{
				EventID: int64(len(out) + 1),
				Sources: []dedup.SourceRef{{Code: src.code, GroupKey: p.Source.GroupKey}},
				Title:   e.Title, Start: e.StartDate,
				CountryCode: e.CountryCode, CityKey: e.CityKey, RegURL: e.RegistrationURL,
			}
			if e.EndDate != nil {
				s.End = *e.EndDate
			}
			info := fmt.Sprint("end=", s.End.Format(time.DateOnly), " reg=", e.RegistrationURL, " km=")
			for _, d := range e.Distances {
				info += fmt.Sprintf("%g,", float64(d.Meters)/1000)
			}
			out = append(out, calibEvent{key: src.code + "/" + p.Source.ExternalID, city: e.City, info: info, side: s})
		}
	}
	return out
}

// windowPairs — пары окна, как в dedup.Pass: одна страна, начало второго не
// позже конца первого + 2 дня; события отсортированы по (Start, EventID).
func windowPairs(evs []calibEvent, fn func(a, b *calibEvent)) {
	sorted := make([]*calibEvent, len(evs))
	for i := range evs {
		sorted[i] = &evs[i]
	}
	slices.SortFunc(sorted, func(x, y *calibEvent) int {
		return cmp.Or(x.side.Start.Compare(y.side.Start), cmp.Compare(x.side.EventID, y.side.EventID))
	})
	for i, a := range sorted {
		end := a.side.Start
		if a.side.End.After(end) {
			end = a.side.End
		}
		limit := end.AddDate(0, 0, 2)
		for _, b := range sorted[i+1:] {
			if b.side.Start.After(limit) {
				break
			}
			if a.side.CountryCode == b.side.CountryCode {
				fn(a, b)
			}
		}
	}
}

// countPartners — число партнёров с NameSim не ниже High у каждого события
// (как dedup.Pass: только пары с вердиктом не Separate, D-20).
func countPartners(evs []calibEvent, c dedup.Config) map[int64]int {
	partners := map[int64]int{}
	windowPairs(evs, func(a, b *calibEvent) {
		v, r := dedup.Decide(dedup.Pair{A: a.side, B: b.side}, c)
		if v != dedup.Separate && r.NameSim >= c.High {
			partners[a.side.EventID]++
			partners[b.side.EventID]++
		}
	})
	return partners
}

// judge — правило вердикта по метке. fail — нарушение (тест падает),
// mismatch — расхождение с предложенной, ещё не проверенной меткой.
// Пара одного источника и синтетическая «другой год» — всегда Separate
// (DDP-05, ворота дат), независимо от метки и proposed.
func judge(label, kind string, synthetic, proposed bool, v dedup.Verdict, r dedup.Reasons) (fail, mismatch string) {
	switch {
	case kind == "same-source":
		if v != dedup.Separate || r.Gate != "source" {
			return fmt.Sprintf("same-source: вердикт %s gate=%q, нужен separate gate=source", v, r.Gate), ""
		}
		return "", ""
	case synthetic:
		if v != dedup.Separate {
			return fmt.Sprintf("synthetic: вердикт %s, нужен separate", v), ""
		}
		return "", ""
	}
	var bad string
	switch {
	case label == "different" && v == dedup.AutoMerge:
		bad = "false_auto"
	case label == "same" && v == dedup.Separate:
		bad = "missed"
	}
	if bad == "" {
		return "", ""
	}
	if proposed {
		return "", bad
	}
	return bad, ""
}

func loadPairs(t *testing.T) []calibPair {
	t.Helper()
	data, err := os.ReadFile(calibFile)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var pairs []calibPair
	if err := dec.Decode(&pairs); err != nil {
		t.Fatalf("%s: %v", calibFile, err)
	}
	ids := map[string]bool{}
	seen := map[string]string{}
	for _, p := range pairs {
		if !calibIDRe.MatchString(p.ID) || ids[p.ID] {
			t.Errorf("%s: id неверный или повторяется", p.ID)
		}
		ids[p.ID] = true
		if !slices.Contains(calibLabels, p.Label) {
			t.Errorf("%s: label %q", p.ID, p.Label)
		}
		if !slices.Contains(calibKinds, p.Kind) {
			t.Errorf("%s: kind %q", p.ID, p.Kind)
		}
		if p.Synthetic != (p.Kind == "other-year") {
			t.Errorf("%s: synthetic=%v при kind %q", p.ID, p.Synthetic, p.Kind)
		}
		if p.Note == "" || p.Reason == "" {
			t.Errorf("%s: пустой note или reason", p.ID)
		}
		x, y := p.A.key(), p.B.key()
		if x > y {
			x, y = y, x
		}
		k := fmt.Sprintf("%s|%s|%v", x, y, p.Synthetic)
		if prev, ok := seen[k]; ok {
			t.Errorf("%s: та же пара, что %s", p.ID, prev)
		}
		seen[k] = p.ID
	}

	// Состав набора (D-12): не меньше 30 пар (+ проверенные AUTO-пары a01–a32, не больше 100), обе метки, все виды, 4–6 синтетических.
	kinds, labels := map[string]int{}, map[string]int{}
	for _, p := range pairs {
		kinds[p.Kind]++
		labels[p.Label]++
	}
	if n := len(pairs); n < 30 || n > 100 {
		t.Errorf("пар %d, нужно от 30 до 100", n)
	}
	if labels["same"] < 10 || labels["different"] < 10 {
		t.Errorf("same %d, different %d — нужно не меньше 10 каждой", labels["same"], labels["different"])
	}
	for _, k := range calibKinds {
		if kinds[k] == 0 {
			t.Errorf("нет пар вида %s", k)
		}
	}
	if n := kinds["other-year"]; n < 4 || n > 6 {
		t.Errorf("синтетических пар %d, нужно от 4 до 6", n)
	}
	return pairs
}

// TestCalibration — ворота DDP-09 на фикстурах: вердикт Decide для каждой
// размеченной пары при конфиге LoadConfig() с включённым автослиянием (D-08).
func TestCalibration(t *testing.T) {
	c, err := dedup.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c.AutoMerge = true
	off := c
	off.AutoMerge = false

	evs := loadCatalog(t)
	byKey := make(map[string]*calibEvent, len(evs))
	for i := range evs {
		byKey[evs[i].key] = &evs[i]
	}
	partners := countPartners(evs, c)
	if *calibDump {
		dump(t, evs, partners, c)
	}

	pairs := loadPairs(t)
	matrix := map[string]map[dedup.Verdict]int{}
	var confirmed, proposed, falseAuto, missed, pFalseAuto, pMissed int
	for _, p := range pairs {
		ea, eb := byKey[p.A.key()], byKey[p.B.key()]
		if ea == nil || eb == nil {
			t.Errorf("%s: сторона не найдена в каталоге (%s, %s): out_of_scope или неверный external_id", p.ID, p.A.key(), p.B.key())
			continue
		}
		pair := dedup.Pair{A: ea.side, B: eb.side,
			UniquePartner: partners[ea.side.EventID] <= 1 && partners[eb.side.EventID] <= 1}
		if p.Synthetic {
			// Худший случай: единственный партнёр, сторона b на год позже.
			pair.UniquePartner = true
			pair.B.Start = pair.B.Start.AddDate(1, 0, 0)
			if !pair.B.End.IsZero() {
				pair.B.End = pair.B.End.AddDate(1, 0, 0)
			}
		}
		v, r := dedup.Decide(pair, c)
		vOff, rOff := dedup.Decide(pair, off)
		// D-10, слой 2 на реальных фикстурах: при выключенном флаге AutoMerge
		// нет, признак и reasons те же, признак ⇔ AutoMerge при включённом.
		if vOff == dedup.AutoMerge || rOff != r || r.WouldAuto != (v == dedup.AutoMerge) {
			t.Errorf("%s: off=%s would_auto=%v verdict=%s; reasons off/on differ: %v", p.ID, vOff, r.WouldAuto, v, rOff != r)
		}
		// sim — схожесть названий всегда: Decide не считает её, если пару закрыли ворота раньше.
		t.Logf("PAIR %s kind=%s label=%s proposed=%v sim=%.2f score=%.2f verdict=%s gate=%s off=%s overlap=%v same_city=%v link=%v up=%v",
			p.ID, p.Kind, p.Label, p.Proposed, dedup.NameSim(pair.A.Title, pair.B.Title), r.Score, v, r.Gate, vOff, r.DatesOverlap, r.SameCity, r.Link, pair.UniquePartner)

		if matrix[p.Label] == nil {
			matrix[p.Label] = map[dedup.Verdict]int{}
		}
		matrix[p.Label][v]++
		if p.Proposed {
			proposed++
		} else {
			confirmed++
		}
		fail, mismatch := judge(p.Label, p.Kind, p.Synthetic, p.Proposed, v, r)
		switch {
		case fail != "":
			t.Errorf("%s: %s (label=%s verdict=%s sim=%.2f): %s", p.ID, fail, p.Label, v, r.NameSim, p.Note)
		case mismatch != "":
			t.Logf("MISMATCH (proposed) %s %s: label=%s verdict=%s sim=%.2f — %s", p.ID, mismatch, p.Label, v, r.NameSim, p.Note)
		}
		switch {
		case fail == "false_auto":
			falseAuto++
		case fail == "missed":
			missed++
		case mismatch == "false_auto":
			pFalseAuto++
		case mismatch == "missed":
			pMissed++
		}
	}
	for _, l := range calibLabels {
		m := matrix[l]
		t.Logf("MATRIX %s: separate=%d candidate=%d auto=%d", l, m[dedup.Separate], m[dedup.Candidate], m[dedup.AutoMerge])
	}
	t.Logf("CALIBRATION pairs=%d confirmed=%d proposed=%d false_auto=%d missed=%d high=%.2f low=%.2f link_bonus=%.2f proposed_false_auto=%d proposed_missed=%d",
		len(pairs), confirmed, proposed, falseAuto, missed, c.High, c.Low, c.LinkBonus, pFalseAuto, pMissed)
}

// dump печатает пары окна разных источников с NameSim не ниже 0,3 и пары
// одного источника не ниже 0,9 — по убыванию схожести.
func dump(t *testing.T, evs []calibEvent, partners map[int64]int, c dedup.Config) {
	type row struct {
		sim  float64
		line string
	}
	var rows []row
	desc := func(e *calibEvent) string {
		return fmt.Sprintf("%s %s %s/%s «%s» [%s]", e.key, e.side.Start.Format(time.DateOnly), e.side.CityKey, e.city, e.side.Title, e.info)
	}
	windowPairs(evs, func(a, b *calibEvent) {
		if a.side.Sources[0].Code == b.side.Sources[0].Code {
			return
		}
		s := dedup.NameSim(a.side.Title, b.side.Title)
		if s < 0.3 {
			return
		}
		if a.side.Sources[0].Code != mcsrc.Code {
			a, b = b, a
		}
		up := partners[a.side.EventID] <= 1 && partners[b.side.EventID] <= 1
		v, r := dedup.Decide(dedup.Pair{A: a.side, B: b.side, UniquePartner: up}, c)
		rows = append(rows, row{s, fmt.Sprintf("DUMP sim=%.2f score=%.2f verdict=%s up=%v gate=%s | %s | %s",
			s, r.Score, v, up, r.Gate, desc(a), desc(b))})
	})
	windowPairs(evs, func(a, b *calibEvent) {
		if a.side.Sources[0].Code != b.side.Sources[0].Code {
			return
		}
		s := dedup.NameSim(a.side.Title, b.side.Title)
		if s < 0.9 {
			return
		}
		v, r := dedup.Decide(dedup.Pair{A: a.side, B: b.side}, c)
		rows = append(rows, row{s, fmt.Sprintf("DUMP same-source sim=%.2f verdict=%s gate=%s | %s | %s", s, v, r.Gate, desc(a), desc(b))})
	})
	slices.SortStableFunc(rows, func(x, y row) int { return cmp.Compare(y.sim, x.sim) })
	for _, r := range rows {
		t.Log(r.line)
	}
}

// TestCalibrationCheck — правила judge.
func TestCalibrationCheck(t *testing.T) {
	src := dedup.Reasons{Gate: "source"}
	none := dedup.Reasons{}
	cases := []struct {
		name               string
		label, kind        string
		synthetic, propose bool
		v                  dedup.Verdict
		r                  dedup.Reasons
		fail, mismatch     bool
	}{
		{"same-source separate/source", "same", "same-source", false, false, dedup.Separate, src, false, false},
		{"same-source candidate", "same", "same-source", false, true, dedup.Candidate, none, true, false},
		{"same-source separate/dates", "same", "same-source", false, true, dedup.Separate, dedup.Reasons{Gate: "dates"}, true, false},
		{"synthetic separate", "different", "other-year", true, true, dedup.Separate, dedup.Reasons{Gate: "dates"}, false, false},
		{"synthetic candidate", "different", "other-year", true, true, dedup.Candidate, none, true, false},
		{"different auto confirmed", "different", "same-name-different", false, false, dedup.AutoMerge, none, true, false},
		{"different auto proposed", "different", "same-name-different", false, true, dedup.AutoMerge, none, false, true},
		{"same separate confirmed", "same", "dup", false, false, dedup.Separate, dedup.Reasons{Gate: "name"}, true, false},
		{"same separate proposed", "same", "dup", false, true, dedup.Separate, dedup.Reasons{Gate: "name"}, false, true},
		{"same candidate", "same", "dup", false, false, dedup.Candidate, none, false, false},
		{"different candidate", "different", "edge", false, false, dedup.Candidate, none, false, false},
		{"same auto", "same", "dup", false, false, dedup.AutoMerge, none, false, false},
		{"different separate", "different", "all-russia", false, false, dedup.Separate, dedup.Reasons{Gate: "name"}, false, false},
	}
	for _, tc := range cases {
		fail, mismatch := judge(tc.label, tc.kind, tc.synthetic, tc.propose, tc.v, tc.r)
		if (fail != "") != tc.fail || (mismatch != "") != tc.mismatch {
			t.Errorf("%s: fail=%q mismatch=%q", tc.name, fail, mismatch)
		}
	}
}
