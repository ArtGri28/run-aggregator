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
	"strings"
	"testing"
	"time"

	"aggregator/internal/dedup"
	"aggregator/internal/ingest"
	hlsrc "aggregator/internal/ingest/sources/heroleague"
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
	// ПроБЕГ сменил вёрстку 2026-11; записи прежнего снимка заморожены в JSON.
	{pbsrc.Code, "testdata/probeg-2026-09-30.records.json", pbsrc.CalendarURL,
		time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), parseRecords, pbsrc.New(nil).Policy()},
	{hlsrc.Code, "../ingest/sources/heroleague/testdata/event_list.json", hlsrc.ListURL,
		time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), hlsrc.Parse, hlsrc.New(nil).Policy()},
}

// parseRecords читает записи источника, сохранённые в JSON.
func parseRecords(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	var recs []ingest.SourceEvent
	return recs, json.Unmarshal(doc.Body, &recs)
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

// calibSides — стороны каталога для dedup.WindowPairs и dedup.Pairs (та же
// логика пар, что в dedup.Pass, M2); индекс стороны = индекс события.
func calibSides(evs []calibEvent) []dedup.Side {
	out := make([]dedup.Side, len(evs))
	for i := range evs {
		out[i] = evs[i].side
	}
	return out
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

// unlabeledAuto — пары окна каталога с WouldAuto (как их видит dedup.Pass),
// которых нет в наборе с подтверждённой меткой (proposed=false, не
// синтетические) — D-12: такая пара — повод проверить метку до включения
// автослияния. Результат — ключи сторон, схожесть и вердикт.
func unlabeledAuto(evs []calibEvent, pairs []calibPair, c dedup.Config) []string {
	labeled := map[string]bool{}
	for _, p := range pairs {
		if p.Proposed || p.Synthetic {
			continue
		}
		labeled[pairKey(p.A.key(), p.B.key())] = true
	}
	found, _ := dedup.Pairs(calibSides(evs), c, nil)
	var out []string
	for _, p := range found {
		a, b := &evs[p.A], &evs[p.B]
		if p.Reasons.WouldAuto && !labeled[pairKey(a.key, b.key)] {
			out = append(out, fmt.Sprintf("%s | %s (sim=%.2f verdict=%s)", a.key, b.key, p.Reasons.NameSim, p.Verdict))
		}
	}
	return out
}

func pairKey(x, y string) string {
	if x > y {
		x, y = y, x
	}
	return x + "|" + y
}

// thirdSourceReport — пары окна «Лиги Героев» с другими источниками и
// межисточниковые пары Беларуси (D-13): число пар окна, со схожестью не ниже
// Low, с WouldAuto, максимальная схожесть; BY-события по источникам.
func thirdSourceReport(t *testing.T, evs []calibEvent, c dedup.Config) {
	t.Helper()
	found, _ := dedup.Pairs(calibSides(evs), c, nil)
	auto := 0
	for _, p := range found {
		if p.Reasons.WouldAuto && (evs[p.A].side.Sources[0].Code == hlsrc.Code || evs[p.B].side.Sources[0].Code == hlsrc.Code) {
			auto++
		}
	}
	all, low, by := 0, 0, 0
	maxSim := 0.0
	dedup.WindowPairs(calibSides(evs), func(i, j int) {
		a, b := &evs[i], &evs[j]
		ca, cb := a.side.Sources[0].Code, b.side.Sources[0].Code
		if ca == cb {
			return
		}
		if a.side.CountryCode == "BY" {
			by++
		}
		if ca != hlsrc.Code && cb != hlsrc.Code {
			return
		}
		all++
		s := dedup.NameSim(a.side.Title, b.side.Title)
		maxSim = max(maxSim, s)
		if s >= c.Low {
			low++
		}
	})
	byEvents := map[string]int{}
	for _, e := range evs {
		if e.side.CountryCode == "BY" {
			byEvents[e.side.Sources[0].Code]++
		}
	}
	t.Logf("HEROLEAGUE pairs_window=%d auto=%d window_all=%d max_sim=%.2f", low, auto, all, maxSim)
	t.Logf("BY cross_source_pairs=%d events marathonec=%d probeg=%d heroleague=%d", by, byEvents[mcsrc.Code], byEvents[pbsrc.Code], byEvents[hlsrc.Code])
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
	_, partners := dedup.Pairs(calibSides(evs), c, nil)
	if *calibDump {
		dump(t, evs, partners, c)
	}

	pairs := loadPairs(t)
	// D-12: каждая потенциальная AUTO-пара окна фикстур размечена владельцем.
	for _, u := range unlabeledAuto(evs, pairs, c) {
		t.Errorf("AUTO-пара окна без подтверждённой метки: %s", u)
	}
	thirdSourceReport(t, evs, c)
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
	dedup.WindowPairs(calibSides(evs), func(i, j int) {
		a, b := &evs[i], &evs[j]
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
	dedup.WindowPairs(calibSides(evs), func(i, j int) {
		a, b := &evs[i], &evs[j]
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

// TestCalibrationUnlabeledAuto — проверка D-12 сама ловит неразмеченную и
// неподтверждённую AUTO-пару (синтетический каталог, не фикстуры).
func TestCalibrationUnlabeledAuto(t *testing.T) {
	day := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	ev := func(id int64, code, ext, title string) calibEvent {
		return calibEvent{key: code + "/" + ext, side: dedup.Side{EventID: id, Sources: []dedup.SourceRef{{Code: code}},
			Title: title, Start: day, CountryCode: "RU", CityKey: "ryazan"}}
	}
	evs := []calibEvent{
		ev(1, mcsrc.Code, "1", "Окская тропа"),
		ev(2, pbsrc.Code, "2", "Окская тропа"),
		ev(3, hlsrc.Code, "3", "Гонка Героев"),
	}
	c := dedup.DefaultConfig()
	label := func(proposed bool) []calibPair {
		return []calibPair{{ID: "c01", A: calibRef{pbsrc.Code, "2"}, B: calibRef{mcsrc.Code, "1"}, Label: "same", Kind: "dup", Proposed: proposed}}
	}
	if got := unlabeledAuto(evs, nil, c); len(got) != 1 || !strings.Contains(got[0], "marathonec/1") || !strings.Contains(got[0], "probeg/2") {
		t.Fatalf("без метки: %v, want одна пара marathonec/1–probeg/2", got)
	}
	if got := unlabeledAuto(evs, label(true), c); len(got) != 1 {
		t.Fatalf("метка proposed: %v, want пара не подтверждена", got)
	}
	if got := unlabeledAuto(evs, label(false), c); len(got) != 0 {
		t.Fatalf("подтверждённая метка (стороны в обратном порядке): %v, want пусто", got)
	}
}
