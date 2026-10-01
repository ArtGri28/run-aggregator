package marathonec

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const goldenPrepared = "testdata/prepared.golden.json"

// fixtureNow — дата загрузки фикстуры (testdata/README.md), 12:00 UTC;
// та же, что в internal/ingest/runner_test.go.
var fixtureNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

var cityKeyRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// preparedRecord — то, что попадёт в каталог из одной записи.
type preparedRecord struct {
	ExternalID      string `json:"external_id"`
	InScope         bool   `json:"in_scope"`
	Title           string `json:"title"`
	TitleNorm       string `json:"title_norm"`
	StartDate       string `json:"start_date"`
	CountryCode     string `json:"country_code"`
	Region          string `json:"region"`
	City            string `json:"city"`
	CityKey         string `json:"city_key"`
	Timezone        string `json:"timezone"`
	Type            string `json:"type"`
	Distances       []int  `json:"distances"`
	RegistrationURL string `json:"registration_url,omitempty"`
}

func prepareFile(t *testing.T, path string, pol normalize.CountryPolicy) ([]ingest.Prepared, ingest.Stats) {
	t.Helper()
	recs, err := parseFile(t, path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, st := ingest.Prepare(recs, fixtureNow, ingest.Policy{CountryFallback: pol})
	return out, st
}

func TestPreparedGolden(t *testing.T) {
	out, st := prepareFile(t, fixture, normalize.RUOnlyFallback)

	var recs []preparedRecord
	inScope := 0
	for _, p := range out {
		e := p.Event
		r := preparedRecord{
			ExternalID: p.Source.ExternalID, InScope: p.InScope, Title: e.Title, TitleNorm: e.TitleNorm,
			StartDate: e.StartDate.Format(time.DateOnly), CountryCode: e.CountryCode, Region: e.Region,
			City: e.City, CityKey: e.CityKey, Timezone: e.Timezone, Type: string(e.Type),
			RegistrationURL: e.RegistrationURL, Distances: []int{},
		}
		for _, d := range e.Distances {
			if d.Meters > normalize.MaxPlausibleDistanceM {
				t.Errorf("%s: дистанция %d м больше MaxPlausibleDistanceM", r.ExternalID, d.Meters)
			}
			r.Distances = append(r.Distances, d.Meters)
		}
		if p.InScope {
			inScope++
			if r.TitleNorm == "" || normalize.TitleNorm(r.TitleNorm) != r.TitleNorm {
				t.Errorf("%s: TitleNorm %q пуст или не идемпотентен", r.ExternalID, r.TitleNorm)
			}
			if len(r.CityKey) > 64 || !cityKeyRe.MatchString(r.CityKey) {
				t.Errorf("%s: CityKey %q", r.ExternalID, r.CityKey)
			}
		}
		recs = append(recs, r)
	}
	t.Logf("parsed %d, in scope %d, out of scope %d, expired %d, rejected %d, unrecognized %d %v, multi-region %d, empty %v",
		st.Parsed, inScope, st.OutOfScope, st.Expired, st.Rejected, st.UnrecognizedRegion, st.Unrecognized, st.MultiRegion, st.Empty)

	buf, err := json.MarshalIndent(struct {
		Stats   ingest.Stats     `json:"stats"`
		Records []preparedRecord `json:"records"`
	}{st, recs}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	buf = append(buf, '\n')

	// D-07: без записанного решения (NoFallback) записи Марафонца без
	// явной страны не попадают в каталог.
	noFB, stNo := prepareFile(t, fixture, normalize.NoFallback)
	for _, p := range noFB {
		if p.InScope {
			t.Errorf("NoFallback: %s в каталоге", p.Source.ExternalID)
		}
	}
	if stNo.OutOfScope != stNo.Valid {
		t.Errorf("NoFallback: OutOfScope %d, Valid %d", stNo.OutOfScope, stNo.Valid)
	}

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

func TestPreparedEdges(t *testing.T) {
	out, st := prepareFile(t, "testdata/edge-rows.html", normalize.RUOnlyFallback)
	byTitle := map[string]ingest.Prepared{}
	for _, p := range out {
		byTitle[p.Source.Title] = p
	}
	get := func(title string) ingest.Prepared {
		t.Helper()
		p, ok := byTitle[title]
		if !ok {
			t.Fatalf("нет валидной записи %q", title)
		}
		return p
	}

	if p := get("MIX RUN"); p.Event.TitleNorm != "mix run" || len(p.Event.Distances) != 2 ||
		p.Event.Distances[0].Meters != 5000 || p.Event.Distances[1].Meters != 10000 {
		t.Errorf("MIX RUN: TitleNorm %q, Distances %+v", p.Event.TitleNorm, p.Event.Distances)
	}
	if p := get("Стадионный забег"); len(p.Event.Distances) != 3 || p.Event.Distances[0].Meters != 100 ||
		p.Event.Distances[1].Meters != 200 || p.Event.Distances[2].Meters != 400 {
		t.Errorf("100 200 400 м: Distances %+v", p.Event.Distances)
	}
	if p := get("Минский забег"); p.InScope {
		t.Error("Беларусь: InScope = true")
	}
	if p := get("Забег Минской области"); !p.InScope || p.Event.Region != "Минская область" {
		t.Errorf("Минская область: InScope %v, Region %q", p.InScope, p.Event.Region)
	}
	if p := get("Две области"); p.Event.Region != "" || !p.InScope {
		t.Errorf("две области: Region %q, InScope %v", p.Event.Region, p.InScope)
	}
	for _, title := range []string{"Дубликат, вторая строка", "Через четыре года", "Прошедший забег"} {
		if _, ok := byTitle[title]; ok {
			t.Errorf("%q среди валидных", title)
		}
	}
	// Нераспознанные: «Минская область» и слаг без названия.
	if st.UnrecognizedRegion != 2 || st.Unrecognized["Минская область — Минск"] != 1 || st.Unrecognized["neizvestnaya-oblast — Красногорск"] != 1 {
		t.Errorf("UnrecognizedRegion %d, Unrecognized %v", st.UnrecognizedRegion, st.Unrecognized)
	}
	if st.MultiRegion != 1 || st.OutOfScope != 1 || st.Expired != 1 || st.DateOutOfRange != 1 {
		t.Errorf("Stats = %+v", st)
	}
	if r := st.Rejects["дубликат external_id"]; r.Count != 1 {
		t.Errorf("Rejects = %+v", st.Rejects)
	}
}

// E. Политика Марафонца не менялась: страны в записях нет, fallback — RU;
// область-страна («Беларусь») по-прежнему выводит запись из каталога, а
// Беларусь не становится fallback'ом этого источника.
func TestPolicyKeepsRUOnly(t *testing.T) {
	pol := New(nil).Policy()
	if pol.CountryFallback != normalize.RUOnlyFallback {
		t.Fatalf("CountryFallback = %v, want RUOnlyFallback", pol.CountryFallback)
	}
	for _, tt := range []struct {
		region, city string
		inScope      bool
		country      string
	}{
		{"Московская область", "Химки", true, "RU"},
		{"", "Сочи", true, "RU"},
		{"Беларусь", "Минск", false, ""},
		{"Республика Беларусь", "Гомель", false, ""},
	} {
		rec := ingest.SourceEvent{ExternalID: "1", Title: "Забег", Date: "15/11/2026", Distances: "10 км", Region: tt.region, City: tt.city}
		out, _ := ingest.Prepare([]ingest.SourceEvent{rec}, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), pol)
		if p := out[0]; p.InScope != tt.inScope || p.Event.CountryCode != tt.country {
			t.Errorf("%q/%q: in_scope=%v %q; want %v %q", tt.region, tt.city, p.InScope, p.Event.CountryCode, tt.inScope, tt.country)
		}
	}
}
