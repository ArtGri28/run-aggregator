package ingest

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"aggregator/internal/event"
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

// TestPrepareCountries — явная страна записи: разрешённая (RU, BY) проходит
// в каталог с country_code и областью как есть, неразрешённая остаётся вне
// каталога; перечень субъектов РФ к области другой страны не применяется.
func TestPrepareCountries(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	mk := func(id, country, region, city, address string) SourceEvent {
		r := rec(id, "15/11/2026")
		r.Country, r.Region, r.City, r.Address = country, region, city, address
		return r
	}
	recs := []SourceEvent{
		mk("1", "Беларусь", "Минская область", "Минск", ""),
		mk("2", "Республика Беларусь", "Брестская область", "д. Колбовичи", "Барановичский район"),
		mk("3", "Беларусь", "", "Гомель", ""),
		mk("4", "Казахстан", "Алматинская область", "Алматы", ""),
		mk("5", "Украина", "", "Киев", ""),
		mk("6", "Россия", "Московская область", "Химки", ""),
		mk("7", "Россия", "Беларусь", "Минск", ""), // область-страна: вне каталога, как раньше
		mk("8", "Беларусь", "Минская область", "Любань", ""),
		mk("9", "Россия", "Ленинградская область", "Любань", ""),
	}
	for _, pol := range []Policy{ruOnly, noFallback} {
		out, st := Prepare(recs, now, pol)
		if len(out) != len(recs) {
			t.Fatalf("valid = %d, want %d", len(out), len(recs))
		}
		by := map[string]Prepared{}
		for _, p := range out {
			by[p.Source.ExternalID] = p
		}
		type want struct {
			in           bool
			code, region string
		}
		for id, w := range map[string]want{
			"1": {true, "BY", "Минская область"},
			"2": {true, "BY", "Брестская область"},
			"3": {true, "BY", ""},
			"4": {false, "KZ", "Алматинская область"},
			"5": {false, "UA", ""},
			"6": {true, "RU", "Московская область"},
			"7": {false, "", "Беларусь"},
			"8": {true, "BY", "Минская область"},
			"9": {true, "RU", "Ленинградская область"},
		} {
			p := by[id]
			if p.InScope != w.in || p.Event.CountryCode != w.code || p.Event.Region != w.region {
				t.Errorf("%s: InScope %v, %q, %q; want %v, %q, %q", id, p.InScope, p.Event.CountryCode, p.Event.Region, w.in, w.code, w.region)
			}
		}
		if p := by["2"]; p.Event.City != "Колбовичи" || p.Event.Address != "Барановичский район" {
			t.Errorf("2: City %q, Address %q", p.Event.City, p.Event.Address)
		}
		// Область другой страны не считается нераспознанной областью РФ.
		if st.UnrecognizedRegion != 0 || st.OutOfScope != 3 || st.Empty["region"] != 2 {
			t.Errorf("Stats = %+v, want UnrecognizedRegion 0, OutOfScope 3, Empty[region] 2", st)
		}
		// Одинаковое название города в разных странах: city_key один,
		// идентичность города — пара (country_code, city_key).
		if a, b := by["8"].Event, by["9"].Event; a.CityKey != "lyuban" || a.CityKey != b.CityKey || a.CountryCode == b.CountryCode {
			t.Errorf("Любань: %q/%q и %q/%q", a.CountryCode, a.CityKey, b.CountryCode, b.CityKey)
		}
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
	if got := string((Stats{Valid: 1, Collapsed: 2}).Metrics()); !strings.Contains(got, `"collapsed":2`) {
		t.Errorf("Metrics() c Collapsed: %s", got)
	}
}

func TestPayloadStable(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	r := SourceEvent{ExternalID: "1", URL: "u", Title: "Забег", Date: "15/11/2026", Distances: "10 км", City: "Сочи", TypeLabel: "shosse"}
	const base = `{"external_id":"1","url":"u","title":"Забег","date":"15/11/2026","distances":"10 км","city":"Сочи","region":"","type":"shosse"`
	out, _ := Prepare([]SourceEvent{r}, now, ruOnly)
	if got := string(out[0].Payload); got != base+`}` {
		t.Errorf("payload без новых полей =\n%s\nwant\n%s}", got, base)
	}
	r.EndDate, r.GroupKey = "16/11/2026", "series:x"
	out, _ = Prepare([]SourceEvent{r}, now, ruOnly)
	if got, want := string(out[0].Payload), base+`,"end_date":"16/11/2026","group_key":"series:x"}`; got != want {
		t.Errorf("payload с новыми полями =\n%s\nwant\n%s", got, want)
	}
	// Вид спорта — последний ключ, после всех прежних (D-06).
	r.Address, r.Timezone, r.Sport = "парк", "Europe/Moscow", "skiing"
	out, _ = Prepare([]SourceEvent{r}, now, ruOnly)
	if got, want := string(out[0].Payload), base+`,"end_date":"16/11/2026","group_key":"series:x","address":"парк","timezone":"Europe/Moscow","sport":"skiing"}`; got != want {
		t.Errorf("payload с видом спорта =\n%s\nwant\n%s", got, want)
	}
}

// TestPrepareSport — вид спорта записи (D-05): пусто — бег, код из
// справочника проходит, код вне справочника отбраковывается и не становится
// победителем серии.
func TestPrepareSport(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	sport := func(r SourceEvent, code string) SourceEvent { r.Sport = code; return r }
	out, st := Prepare([]SourceEvent{
		rec("1", "15/11/2026"), sport(rec("2", "15/11/2026"), "skiing"), sport(rec("3", "15/11/2026"), "chess"),
	}, now, ruOnly)
	if len(out) != 2 || out[0].Event.Sport != event.SportRunning || out[1].Event.Sport != event.SportSkiing {
		t.Fatalf("выход = %+v, want бег и лыжи", out)
	}
	if r := st.Rejects[reasonUnknownSport]; st.Rejected != 1 || r.Count != 1 || len(r.Examples) != 1 || r.Examples[0] != "3" {
		t.Errorf("Rejected %d, Rejects = %+v", st.Rejected, st.Rejects)
	}

	// Серия: ближайшая запись с неизвестным кодом не победитель.
	series := func(id, date, code string) SourceEvent {
		r := sport(rec(id, date), code)
		r.GroupKey = "S"
		return r
	}
	out, st = Prepare([]SourceEvent{series("a", "06/10/2026", "chess"), series("b", "13/10/2026", "")}, now, ruOnly)
	if len(out) != 1 || out[0].Source.ExternalID != "b" || st.Collapsed != 0 || st.Rejected != 1 {
		t.Errorf("серия: out %+v, Stats %+v; want запись b, Collapsed 0, Rejected 1", out, st)
	}
}

func TestPrepareEndDate(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		end  string
		want string // "" — EndDate nil
	}{
		{"17/11/2026", "2026-11-17"},
		{"", ""},
		{"15/11/2026", ""},
		{"когда-нибудь", ""},
		{"14/11/2026", ""},
	} {
		r := rec("1", "15/11/2026")
		r.EndDate = tc.end
		out, st := Prepare([]SourceEvent{r}, now, ruOnly)
		if len(out) != 1 || st.Rejected != 0 {
			t.Fatalf("EndDate %q: out %d, Rejected %d", tc.end, len(out), st.Rejected)
		}
		got := ""
		if e := out[0].Event.EndDate; e != nil {
			got = e.Format(time.DateOnly)
		}
		if got != tc.want {
			t.Errorf("EndDate %q → %q, want %q", tc.end, got, tc.want)
		}
	}
}

func TestPrepareGroupKey(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	key := func(r SourceEvent, k string) SourceEvent { r.GroupKey = k; return r }
	same := func(id string) SourceEvent { r := rec(id, "10/10/2026"); r.Title = "Паркран"; return r }
	recs := []SourceEvent{
		key(rec("a1", "13/10/2026"), "A"),
		same("n1"),
		key(rec("a2", "06/10/2026"), "A"),
		key(rec("b1", "05/10/2026"), "B"),
		key(rec("a3", "20/10/2026"), "A"),
		key(rec("a4", "20/09/2026"), "A"),
		key(rec("b2", "28/09/2026"), "B"),
		key(rec("c1", "01/11/2026"), "C"),
		key(rec("c2", "01/11/2026"), "C"),
		same("n2"),
	}
	out, st := Prepare(recs, now, ruOnly)
	var ids []string
	for _, p := range out {
		ids = append(ids, p.Source.ExternalID)
	}
	if got, want := strings.Join(ids, " "), "n1 a2 b2 c1 n2"; got != want {
		t.Errorf("выход = %s, want %s", got, want)
	}
	if st.Collapsed != 4 || st.Expired != 1 || st.Valid != 5 {
		t.Errorf("Stats = %+v, want Collapsed 4, Expired 1, Valid 5", st)
	}
	if st.Parsed != st.Valid+st.Rejected+st.Expired+st.Collapsed {
		t.Errorf("Parsed %d != Valid+Rejected+Expired+Collapsed (%+v)", st.Parsed, st)
	}

	// Только ключ A: остаётся 06.10, Collapsed 2, Expired 1.
	out, st = Prepare([]SourceEvent{recs[0], recs[2], recs[4], recs[5]}, now, ruOnly)
	if len(out) != 1 || out[0].Source.ExternalID != "a2" || st.Collapsed != 2 || st.Expired != 1 {
		t.Errorf("ключ A: out %v, Stats %+v", out, st)
	}

	// NoFallback: счётчики — только по оставшейся записи серии.
	var series []SourceEvent
	for i, d := range []string{"13/10/2026", "06/10/2026", "20/10/2026"} {
		r := key(rec(strconv.Itoa(i), d), "S")
		r.Region = ""
		if i == 0 {
			r.City = ""
		}
		series = append(series, r)
	}
	out, st = Prepare(series, now, noFallback)
	if len(out) != 1 || st.OutOfScope != 1 || st.Collapsed != 2 || st.Empty["city"] != 0 {
		t.Errorf("NoFallback: out %d, Stats %+v", len(out), st)
	}
}

func TestPrepareSourceTimezone(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rec := func(city, tz string) SourceEvent {
		return SourceEvent{ExternalID: city, Title: "Старт", Date: "2026-10-17", City: city, Country: "RU", Timezone: tz}
	}
	out, _ := Prepare([]SourceEvent{rec("Волгоград", "Europe/Volgograd"), rec("Москва", "Asia/Tokyo"), rec("Тула", "Nope/Zone")}, now, Policy{})
	got := []string{out[0].Event.Timezone, out[1].Event.Timezone, out[2].Event.Timezone}
	// справочник городов важнее источника; неизвестная зона отбрасывается
	if want := []string{"Europe/Volgograd", "Europe/Moscow", ""}; !slices.Equal(got, want) {
		t.Fatalf("timezone = %v, want %v", got, want)
	}
}

// TestPrepareSki — дисциплина и дистанции лыжной записи (D-11): дисциплина —
// только из перечня своего вида спорта; «марафон» и «полумарафон» дистанцию
// дают только беговой записи.
func TestPrepareSki(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	r := func(id, sport, title, label, distances string) SourceEvent {
		return SourceEvent{ExternalID: id, Title: title, Date: "15/11/2026", Distances: distances, City: "Сочи", TypeLabel: label, Sport: sport}
	}
	out, st := Prepare([]SourceEvent{
		r("1", "skiing", "Гонка", "Лыжные гонки", "10 км"),
		r("2", "skiing", "Гонка", "Лыжероллеры", "10 км"),
		r("3", "skiing", "Гонка", "trail", "10 км"),
		r("4", "skiing", "Гонка", "", "10 км"),
		r("5", "", "Забег", "Лыжные гонки", "10 км"),
		r("6", "", "Забег", "trail", "10 км"),
		r("7", "skiing", "Лыжный марафон", "Лыжные гонки", ""),
		r("8", "skiing", "Лыжный полумарафон", "Лыжные гонки", "классический стиль"),
		r("9", "skiing", "Лыжная гонка 30 км", "Лыжные гонки", ""),
		r("10", "skiing", "Лыжный марафон", "Лыжные гонки", "15 км, 30 км и 45 км."),
		r("11", "", "Марафон", "shosse", ""),
		r("12", "", "Забег", "shosse", "полумарафон"),
	}, now, ruOnly)
	want := []struct {
		typ    event.Type
		meters []int
	}{
		{event.TypeSki, []int{10000}}, {event.TypeRollerski, []int{10000}}, {event.TypeOther, []int{10000}}, {event.TypeOther, []int{10000}},
		{event.TypeOther, []int{10000}}, {event.TypeTrail, []int{10000}},
		{event.TypeSki, nil}, {event.TypeSki, nil}, {event.TypeSki, []int{30000}}, {event.TypeSki, []int{15000, 30000, 45000}},
		{event.TypeRoad, []int{42195}}, {event.TypeRoad, []int{21098}},
	}
	if len(out) != len(want) || st.Rejected != 0 || st.Empty["distances"] != 2 {
		t.Fatalf("выход %d записей, Stats %+v; want %d записей, 2 без дистанций", len(out), st, len(want))
	}
	for i, p := range out {
		var meters []int
		for _, d := range p.Event.Distances {
			meters = append(meters, d.Meters)
		}
		if p.Event.Type != want[i].typ || !slices.Equal(meters, want[i].meters) {
			t.Errorf("запись %s: тип %q, дистанции %v; want %q, %v", p.Source.ExternalID, p.Event.Type, meters, want[i].typ, want[i].meters)
		}
		if got := event.TypeFor(p.Event.Sport, p.Event.Type); got != p.Event.Type {
			t.Errorf("запись %s: пара %s — %s вне перечня справочника", p.Source.ExternalID, p.Event.Sport, p.Event.Type)
		}
	}
}

// Время старта переносится в событие; неразборчивое отбрасывается, запись
// остаётся; payload записи без времени не содержит ключа start_time.
func TestPrepareStartTime(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a, b, c := rec("1", "10/10/2026"), rec("2", "10/10/2026"), rec("3", "10/10/2026")
	a.StartTime, b.StartTime = "9:05", "25:00"
	out, st := Prepare([]SourceEvent{a, b, c}, now, ruOnly)
	if len(out) != 3 || st.Rejected != 0 {
		t.Fatalf("valid %d, rejected %d", len(out), st.Rejected)
	}
	if out[0].Event.StartTime != "09:05" || out[1].Event.StartTime != "" || out[2].Event.StartTime != "" {
		t.Errorf("время: %q %q %q", out[0].Event.StartTime, out[1].Event.StartTime, out[2].Event.StartTime)
	}
	if strings.Contains(string(out[2].Payload), "start_time") {
		t.Errorf("payload без времени содержит start_time: %s", out[2].Payload)
	}
}
