package ingest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"aggregator/internal/normalize"
)

func TestPrepareValidate(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	rec := func(id, title, date string) SourceEvent {
		return SourceEvent{ExternalID: id, Title: title, Date: date, Distances: "10 км", City: "Сочи", TypeLabel: "shosse"}
	}
	recs := []SourceEvent{
		rec("1", "Забег", "15/11/2026"),
		rec("1", "Забег-дубль", "15/11/2026"),
		rec("", "Без id", "15/11/2026"),
		rec("2", " ", "15/11/2026"),
		rec("3", "Плохая дата", "31/02/2027"),
		rec("4", "Через три года", "29/09/2029"),
		rec("5", "Позже трёх лет", "30/09/2029"),
		rec("6", "Вчера", "28/09/2026"),
		rec("7", "Позавчера", "27/09/2026"),
	}
	out, st := Prepare(recs, now, Policy{CountryFallback: normalize.RUOnlyFallback})

	var ids []string
	for _, p := range out {
		ids = append(ids, p.Source.ExternalID)
	}
	if got := len(ids); got != 3 || ids[0] != "1" || ids[1] != "4" || ids[2] != "6" {
		t.Fatalf("valid ids = %v, want [1 4 6]", ids)
	}
	if st.Parsed != 9 || st.Valid != 3 || st.Rejected != 5 || st.Expired != 1 || st.DateOutOfRange != 1 || st.OutOfScope != 0 {
		t.Fatalf("Stats = %+v", st)
	}
	for reason, want := range map[string]int{
		reasonDuplicateID: 1, reasonNoExternalID: 1, reasonNoTitle: 1, reasonBadDate: 1, reasonDateRange: 1,
	} {
		if got := st.Rejects[reason].Count; got != want {
			t.Errorf("Rejects[%q] = %d, want %d", reason, got, want)
		}
	}
	if ex := st.Rejects[reasonDuplicateID].Examples; len(ex) != 1 || ex[0] != "1" {
		t.Errorf("Rejects[дубликат].Examples = %v, want [1]", ex)
	}
	if ex := st.Rejects[reasonNoExternalID].Examples; len(ex) != 0 {
		t.Errorf("Rejects[нет external_id].Examples = %v, want []", ex)
	}
	if ex := st.Rejects[reasonBadDate].Examples; len(ex) != 1 || ex[0] != "3" {
		t.Errorf("Rejects[дата не разобрана].Examples = %v, want [3]", ex)
	}
	if p := out[0]; p.Event.CountryCode != "RU" || !p.InScope || len(p.Event.Distances) != 1 || p.Event.Distances[0].Meters != 10000 {
		t.Errorf("event = %+v, InScope %v", p.Event, p.InScope)
	}

	_, st = Prepare(recs[:1], now, Policy{CountryFallback: normalize.NoFallback})
	if st.OutOfScope != 1 {
		t.Errorf("NoFallback: OutOfScope = %d, want 1", st.OutOfScope)
	}
}

var (
	ruOnly     = Policy{CountryFallback: normalize.RUOnlyFallback}
	noFallback = Policy{CountryFallback: normalize.NoFallback}
)

func rec(id, date string) SourceEvent {
	return SourceEvent{ExternalID: id, Title: "Забег " + id, Date: date, Distances: "10 км", City: "Сочи", Region: "Краснодарский край", TypeLabel: "shosse"}
}

func TestPrepareExpired(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	out, st := Prepare([]SourceEvent{
		rec("1", "29/09/2026"), rec("2", "30/09/2026"), rec("3", "01/10/2029"), rec("4", "02/10/2029"),
	}, now, ruOnly)
	if len(out) != 2 || out[0].Source.ExternalID != "2" || out[1].Source.ExternalID != "3" {
		t.Fatalf("valid = %+v, want ids 2, 3", out)
	}
	if st.Expired != 1 || st.Rejected != 1 || st.DateOutOfRange != 1 || st.Valid != 2 {
		t.Errorf("Stats = %+v", st)
	}
}

func TestPrepareRegions(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	withRegion := func(id, region, city string) SourceEvent {
		r := rec(id, "15/11/2026")
		r.Region, r.City = region, city
		return r
	}
	recs := []SourceEvent{
		withRegion("1", "Московская обл.", "Химки"),
		withRegion("2", "Беларусь", "Минск"),
		withRegion("3", "Минская область", "Минск"),
		withRegion("4", "", "Сочи"),
		withRegion("5", "Москва и Московская область", "Москва"),
	}
	multi := withRegion("6", "", "Майкоп, Солохаул")
	multi.RegionSlugs, multi.Regions = []string{"krasnodarskij-kraj", "respublika-adygeya"}, []string{"Краснодарский край", "Республика Адыгея"}
	recs = append(recs, multi)

	out, st := Prepare(recs, now, ruOnly)
	if len(out) != 6 {
		t.Fatalf("valid = %d, want 6", len(out))
	}
	byID := map[string]Prepared{}
	for _, p := range out {
		byID[p.Source.ExternalID] = p
	}
	if p := byID["1"]; !p.InScope || p.Event.Region != "Московская область" || p.Event.CountryCode != "RU" {
		t.Errorf("Московская обл.: InScope %v, Region %q, CountryCode %q", p.InScope, p.Event.Region, p.Event.CountryCode)
	}
	if p := byID["2"]; p.InScope || p.Event.CountryCode != "" {
		t.Errorf("Беларусь: InScope %v, CountryCode %q", p.InScope, p.Event.CountryCode)
	}
	if p := byID["3"]; !p.InScope || p.Event.Region != "Минская область" {
		t.Errorf("Минская область: InScope %v, Region %q", p.InScope, p.Event.Region)
	}
	if p := byID["5"]; p.Event.Region != "Москва" || p.Event.Timezone != "Europe/Moscow" {
		t.Errorf("Москва и Московская область + Москва: Region %q, Timezone %q", p.Event.Region, p.Event.Timezone)
	}
	if p := byID["6"]; !p.InScope || p.Event.Region != "" {
		t.Errorf("две области: InScope %v, Region %q", p.InScope, p.Event.Region)
	}
	if st.OutOfScope != 1 || st.UnrecognizedRegion != 1 || st.MultiRegion != 1 || st.Empty["region"] != 1 {
		t.Errorf("Stats = %+v", st)
	}
	if want := map[string]int{"Минская область — Минск": 1}; len(st.Unrecognized) != 1 || st.Unrecognized["Минская область — Минск"] != 1 {
		t.Errorf("Unrecognized = %v, want %v", st.Unrecognized, want)
	}

	// Иностранная область — вне каталога при любой политике (D-09).
	if _, st := Prepare(recs[1:2], now, noFallback); st.OutOfScope != 1 {
		t.Errorf("Беларусь, NoFallback: OutOfScope = %d, want 1", st.OutOfScope)
	}
	// Нераспознанная область считается и при NoFallback.
	if _, st := Prepare(recs[2:3], now, noFallback); st.UnrecognizedRegion != 1 {
		t.Errorf("Минская область, NoFallback: UnrecognizedRegion = %d, want 1", st.UnrecognizedRegion)
	}
}

func TestPrepareNoFallback(t *testing.T) {
	r := rec("1", "15/11/2026")
	r.Region = "Московская область"
	out, st := Prepare([]SourceEvent{r}, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), noFallback)
	if len(out) != 1 || out[0].InScope || out[0].Event.CountryCode != "" || st.OutOfScope != 1 {
		t.Errorf("D-07: out = %+v, OutOfScope = %d", out, st.OutOfScope)
	}
}

func TestPrepareDistancePlausibility(t *testing.T) {
	r := rec("1", "15/11/2026")
	r.Distances = "5 км, 2000 км"
	out, st := Prepare([]SourceEvent{r}, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), ruOnly)
	if len(out) != 1 || len(out[0].Event.Distances) != 1 || out[0].Event.Distances[0].Meters != 5000 {
		t.Fatalf("out = %+v", out)
	}
	if st.DroppedDistances != 1 || st.Empty["distances"] != 0 {
		t.Errorf("DroppedDistances = %d, Empty = %v", st.DroppedDistances, st.Empty)
	}
}

func TestPrepareLongDescription(t *testing.T) {
	r := rec("1", "15/11/2026")
	r.Description = strings.Repeat("пробег ", 215) // 1505 рун
	out, _ := Prepare([]SourceEvent{r}, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), ruOnly)
	d := out[0].Event.Description
	if n := utf8.RuneCountInString(d); n > normalize.MaxDescriptionRunes || n == 0 || !utf8.ValidString(d) {
		t.Errorf("Description: %d рун, valid UTF-8 %v", n, utf8.ValidString(d))
	}
	var payload SourceEvent
	if err := json.Unmarshal(out[0].Payload, &payload); err != nil || payload.Description != r.Description {
		t.Errorf("payload description изменён (err %v)", err)
	}
}

func TestPrepareDeterministic(t *testing.T) {
	r := rec("1", "15/11/2026")
	r.URL = "https://marathonec.ru/trail-burya/"
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	a, _ := Prepare([]SourceEvent{r}, now, ruOnly)
	b, _ := Prepare([]SourceEvent{r}, now, ruOnly)
	if !bytes.Equal(a[0].Payload, b[0].Payload) || !bytes.Contains(a[0].Payload, []byte(r.URL)) {
		t.Errorf("payload %s / %s", a[0].Payload, b[0].Payload)
	}
}

func TestStatsMetrics(t *testing.T) {
	st := Stats{Valid: 3, Expired: 2, DateOutOfRange: 1, OutOfScope: 1, UnrecognizedRegion: 1, MultiRegion: 1, DroppedDistances: 4,
		Empty: map[string]int{"city": 1, "distances": 2}}
	want := `{"valid":3,"expired":2,"date_out_of_range":1,"out_of_scope":1,"unrecognized_region":1,"multi_region":1,"dropped_distances":4,"empty":{"city":0.3333,"region":0,"distances":0.6667}}`
	if got := string(st.Metrics()); got != want {
		t.Errorf("Metrics() =\n%s\nwant\n%s", got, want)
	}
	if got := string((Stats{}).Metrics()); !strings.Contains(got, `"empty":{"city":0,"region":0,"distances":0}`) {
		t.Errorf("Metrics() при Valid = 0: %s", got)
	}
}
