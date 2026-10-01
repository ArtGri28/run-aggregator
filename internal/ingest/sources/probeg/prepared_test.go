package probeg

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const goldenPrepared = "testdata/prepared.golden.json"

// fixtureNow — момент прогона по фикстуре: время её загрузки.
var fixtureNow = fixtureFetched

var cityKeyRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// preparedRecord — то, что попадёт в каталог из одной записи.
type preparedRecord struct {
	ExternalID      string `json:"external_id"`
	InScope         bool   `json:"in_scope"`
	Title           string `json:"title"`
	TitleNorm       string `json:"title_norm"`
	StartDate       string `json:"start_date"`
	EndDate         string `json:"end_date,omitempty"`
	CountryCode     string `json:"country_code"`
	Region          string `json:"region"`
	City            string `json:"city"`
	CityKey         string `json:"city_key"`
	Timezone        string `json:"timezone"`
	Type            string `json:"type"`
	Distances       []int  `json:"distances"`
	RegistrationURL string `json:"registration_url,omitempty"`
	GroupKey        string `json:"group_key,omitempty"`
}

func prepareFixture(t *testing.T) ([]ingest.Prepared, ingest.Stats) {
	t.Helper()
	out, st := ingest.Prepare(parseFixture(t), fixtureNow, New(nil).Policy())
	return out, st
}

func TestPreparedGolden(t *testing.T) {
	out, st := prepareFixture(t)
	var recs []preparedRecord
	for _, p := range out {
		e := p.Event
		r := preparedRecord{
			ExternalID: p.Source.ExternalID, InScope: p.InScope, Title: e.Title, TitleNorm: e.TitleNorm,
			StartDate: e.StartDate.Format(time.DateOnly), CountryCode: e.CountryCode, Region: e.Region,
			City: e.City, CityKey: e.CityKey, Timezone: e.Timezone, Type: string(e.Type),
			RegistrationURL: e.RegistrationURL, GroupKey: p.Source.GroupKey, Distances: []int{},
		}
		if e.EndDate != nil {
			r.EndDate = e.EndDate.Format(time.DateOnly)
		}
		for _, d := range e.Distances {
			r.Distances = append(r.Distances, d.Meters)
		}
		if p.InScope && (len(r.CityKey) > 64 || !cityKeyRe.MatchString(r.CityKey)) {
			t.Errorf("%s: CityKey %q", r.ExternalID, r.CityKey)
		}
		recs = append(recs, r)
	}
	t.Logf("parsed %d, valid %d, collapsed %d, out of scope %d, expired %d, rejected %d, unrecognized %d, empty %v",
		st.Parsed, st.Valid, st.Collapsed, st.OutOfScope, st.Expired, st.Rejected, st.UnrecognizedRegion, st.Empty)

	buf, err := json.MarshalIndent(struct {
		Stats   ingest.Stats     `json:"stats"`
		Records []preparedRecord `json:"records"`
	}{st, recs}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	buf = append(buf, '\n')

	if *update {
		if err := os.WriteFile(goldenPrepared, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPrepared)
	if err != nil {
		t.Fatalf("%v (сгенерируйте: go test -run TestPreparedGolden -update)", err)
	}
	if !bytes.Equal(want, buf) {
		t.Errorf("расхождение с %s; запустите go test -update и просмотрите diff", goldenPrepared)
	}
}

func TestPreparedStats(t *testing.T) {
	out, st := prepareFixture(t)
	if st.Expired != 0 || st.DateOutOfRange != 0 || st.Rejected != 0 || st.UnrecognizedRegion != 0 {
		t.Errorf("Stats = %+v, want Expired/DateOutOfRange/Rejected/UnrecognizedRegion = 0", st)
	}
	if st.Collapsed < 700 || st.Parsed != len(parseFixture(t)) || st.Parsed != st.Valid+st.Rejected+st.Expired+st.Collapsed {
		t.Errorf("Parsed %d, Valid %d, Collapsed %d", st.Parsed, st.Valid, st.Collapsed)
	}
	byID := map[string]ingest.Prepared{}
	series := map[string][]ingest.Prepared{}
	for _, p := range out {
		byID[p.Source.ExternalID] = p
		if p.InScope && p.Event.CountryCode != "RU" {
			t.Errorf("%s: в каталоге с CountryCode %q", p.Source.ExternalID, p.Event.CountryCode)
		}
		if k := p.Source.GroupKey; k != "" {
			series[k] = append(series[k], p)
		}
	}
	for _, id := range []string{"83368", "84016"} { // Беларусь; серия без города
		if p, ok := byID[id]; !ok || p.InScope {
			t.Errorf("%s: есть %v, InScope %v; want вне каталога", id, ok, p.InScope)
		}
	}
	if s := series["series:"+normalize.TitleNorm("5 вёрст Алёшкинский лес")]; len(s) != 1 || s[0].Event.StartDate.Format(time.DateOnly) != "2026-10-03" {
		t.Errorf("серия 84009: %d записей", len(s))
	}
	if p, ok := byID["83327"]; !ok || !p.InScope || p.Event.CityKey != normalize.CityKey("Черноисточинск") || p.Event.Region != "Свердловская область" {
		t.Errorf("83327: %+v", p.Event)
	}
}

// TestPolicyGate — порог out_of_scope источника (Pitfall 5, A3): с ним
// первый прогон suspect только по причине первого прогона, дальше ok;
// общий порог 0,20 держал бы источник в suspect навсегда.
func TestPolicyGate(t *testing.T) {
	_, st := prepareFixture(t)
	pol := New(nil).Policy()
	share := float64(st.OutOfScope) / float64(st.Valid)
	t.Logf("out_of_scope %d из %d valid = %.4f, OutOfScopeMax %.2f", st.OutOfScope, st.Valid, share, pol.OutOfScopeMax)
	if share+0.05 > pol.OutOfScopeMax || pol.OutOfScopeMax > 0.50 {
		t.Fatalf("OutOfScopeMax %.2f вне [%.4f; 0.50]", pol.OutOfScopeMax, share+0.05)
	}
	th := ingest.DefaultThresholds()
	th.OutOfScopeMax = pol.OutOfScopeMax

	if status, why := ingest.Evaluate(st, ingest.Baseline{}, th); status != ingest.StatusSuspect ||
		!slices.Equal(why, []string{"первый прогон источника — требуется принятие"}) {
		t.Errorf("первый прогон: %s %v", status, why)
	}
	empty := map[string]float64{}
	for _, f := range []string{"city", "region", "distances"} {
		empty[f] = float64(st.Empty[f]) / float64(st.Valid)
	}
	base := ingest.Baseline{OKRuns: 3, Parsed: []int{st.Parsed, st.Parsed, st.Parsed}, EmptyShare: empty}
	if status, why := ingest.Evaluate(st, base, th); status != ingest.StatusOK {
		t.Errorf("устоявшаяся база: %s %v", status, why)
	}
	if status, why := ingest.Evaluate(st, base, ingest.DefaultThresholds()); status != ingest.StatusSuspect ||
		!slices.ContainsFunc(why, func(s string) bool { return strings.Contains(s, "доля вне каталога (out_of_scope)") }) {
		t.Errorf("общий порог: %s %v, want suspect по out_of_scope", status, why)
	}
}

func TestSanityReportFixture(t *testing.T) {
	var b strings.Builder
	if err := ingest.SanityReport(&b, Code, 1, fixtureNow, parseFixture(t)); err != nil {
		t.Fatal(err)
	}
	rep := b.String()
	m := regexp.MustCompile(`Серии \(group_key\): ключей — (\d+) \| записей — (\d+) \| ключей без города — (\d+)`).FindStringSubmatch(rep)
	f := regexp.MustCompile(`иностранная — (\d+)`).FindStringSubmatch(rep)
	if !strings.Contains(rep, "## Страна и серии") || m == nil || f == nil {
		t.Fatalf("нет раздела «Страна и серии»:\n%s", rep)
	}
	keys, noCity, foreign := atoi(m[1]), atoi(m[3]), atoi(f[1])
	if keys < 180 || keys > 191 || noCity < 135 || noCity > 145 || foreign < 8 {
		t.Errorf("ключей %d (180–191), без города %d (135–145), иностранных %d (≥ 8)", keys, noCity, foreign)
	}
	t.Logf("%s; %s", m[0], f[0])
}
