package heroleague

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const fixture = "testdata/event_list.json"

// fixtureNow — день загрузки фикстуры.
var fixtureNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func fixtureDoc(t *testing.T) ingest.RawDoc {
	t.Helper()
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return ingest.RawDoc{URL: ListURL, Body: body}
}

func parseFixture(t *testing.T) map[string]ingest.SourceEvent {
	t.Helper()
	recs, err := Parse(fixtureDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[string]ingest.SourceEvent, len(recs))
	for _, r := range recs {
		if _, dup := m[r.ExternalID]; dup {
			t.Fatalf("external_id %q повторён", r.ExternalID)
		}
		m[r.ExternalID] = r
	}
	return m
}

func byProject(recs map[string]ingest.SourceEvent, prefix string) []ingest.SourceEvent {
	var out []ingest.SourceEvent
	for id, r := range recs {
		if strings.HasPrefix(id, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// A, B, C, F, G: три OCR-проекта → obstacle, external_id = event_city.public_id.
func TestParseOCRProjects(t *testing.T) {
	recs := parseFixture(t)
	// 20 запусков Гонки Героев − 2 корпоративных, 2 городских; Mud Run
	// (public=false) исключён: до фильтра было бы 21.
	if len(recs) != 20 {
		t.Fatalf("записей %d, want 20", len(recs))
	}
	for prefix, n := range map[string]int{"gonka2026_": 18, "urban2026_": 2, "mudrun2026_": 0} {
		got := byProject(recs, prefix)
		if len(got) != n {
			t.Fatalf("%s: %d записей, want %d", prefix, len(got), n)
		}
		for _, r := range got {
			if r.TypeLabel != "obstacle" || normalize.Type(r.TypeLabel) != event.TypeObstacle {
				t.Fatalf("%s: type %q → %q, want obstacle", r.ExternalID, r.TypeLabel, normalize.Type(r.TypeLabel))
			}
			if r.Country != "RU" {
				t.Fatalf("%s: country %q", r.ExternalID, r.Country)
			}
			if !strings.HasPrefix(r.URL, "https://heroleague.ru/event/") {
				t.Fatalf("%s: url %q", r.ExternalID, r.URL)
			}
		}
	}
	for _, id := range []string{"gonka2026_472757_2609", "urban2026_msk", "urban2026_spb"} {
		if r, ok := recs[id]; !ok || r.ExternalID != id {
			t.Fatalf("нет записи %s", id)
		}
	}
}

// Скрытый из публичного календаря запуск (public=false) не импортируется;
// явное public=true и отсутствие поля — импортируются. Реальный Mud Run из
// фикстуры — public=false.
func TestParsePublicFlag(t *testing.T) {
	if _, ok := parseFixture(t)["mudrun2026_1"]; ok {
		t.Fatal("Mud Run (public=false) попал в записи")
	}
	body := `{"values":[{"public_id":"mudrun2027","title":"Mud Run","event_type":{"public_id":"mudrun"},
	 "event_city":[{"public_id":"hidden","public":false,"start_time":"2027-08-07T06:00:00","timezone":"Europe/Moscow","city":{"name_ru":"Истра"}},
	 {"public_id":"shown","public":true,"start_time":"2027-08-07T06:00:00","timezone":"Europe/Moscow","city":{"name_ru":"Истра"}},
	 {"public_id":"unset","start_time":"2027-08-07T06:00:00","timezone":"Europe/Moscow","city":{"name_ru":"Истра"}}]}]}`
	recs, err := Parse(ingest.RawDoc{Body: []byte(body)})
	if err != nil || len(recs) != 2 || recs[0].ExternalID != "shown" || recs[1].ExternalID != "unset" {
		t.Fatalf("%+v, %v", recs, err)
	}
	if recs[0].TypeLabel != "obstacle" { // тип mudrun → obstacle (C)
		t.Fatalf("type %q", recs[0].TypeLabel)
	}
}

// D, E: обычный бег и лагеря в записи не попадают; корпоративные тоже.
func TestParseSkipsOtherProjects(t *testing.T) {
	recs := parseFixture(t)
	for id := range recs {
		for _, bad := range []string{"zabeg2027", "m52_2026", "doroga2027", "ligaputeshestviy"} {
			if strings.Contains(id, bad) {
				t.Fatalf("%s не должен попасть в источник", id)
			}
		}
	}
	for _, id := range []string{"gonka2026_524901_1306", "gonka2026_498817_2006"} { // kind=corp
		if _, ok := recs[id]; ok {
			t.Fatalf("корпоративный старт %s попал в записи", id)
		}
	}
}

// H: start_time в API — UTC; дата берётся по зоне площадки.
func TestParseLocalDate(t *testing.T) {
	recs := parseFixture(t)
	for id, want := range map[string][2]string{
		"gonka2026_524901_1804":  {"2026-04-18", "Europe/Moscow"},
		"gonka2026_2013348_1707": {"2026-07-18", "Asia/Vladivostok"}, // 17.07 23:00 UTC
		"gonka2026_1486209_1306": {"2026-06-13", "Asia/Yekaterinburg"},
		"urban2026_msk":          {"2026-10-17", "Europe/Moscow"},
	} {
		r := recs[id]
		if r.Date != want[0] || r.Timezone != want[1] {
			t.Fatalf("%s: date %q tz %q, want %v", id, r.Date, r.Timezone, want)
		}
	}
}

// Время старта — по зоне площадки (22-CONTEXT.md, D-02); скрытое
// организатором (hide_start_time) — заглушка, времени нет.
func TestParseStartTime(t *testing.T) {
	recs := parseFixture(t)
	for id, want := range map[string]string{
		"urban2026_msk":          "10:00", // 07:00 UTC
		"gonka2026_2013348_1707": "09:00", // 17.07 23:00 UTC, Владивосток
		"gonka2026_1486209_1306": "09:00", // 04:00 UTC, Екатеринбург
	} {
		if got := recs[id].StartTime; got != want {
			t.Errorf("%s: время %q, want %q", id, got, want)
		}
	}
	body := `{"values":[{"public_id":"p","title":"Гонка","event_type":{"public_id":"gonka"},
	 "event_city":[{"public_id":"hid","hide_start_time":true,"start_time":"2027-08-07T06:00:00","timezone":"Europe/Moscow","city":{"name_ru":"Истра"}},
	 {"public_id":"bad","start_time":"2027-08-07","timezone":"Europe/Moscow","city":{"name_ru":"Истра"}}]}]}`
	r, err := Parse(ingest.RawDoc{Body: []byte(body)})
	if err != nil || len(r) != 2 || r[0].StartTime != "" || r[0].Date != "2027-08-07" || r[1].StartTime != "" {
		t.Fatalf("скрытое и неразборчивое время: %+v, %v", r, err)
	}
}

func TestParseFields(t *testing.T) {
	recs := parseFixture(t)
	r := recs["urban2026_msk"]
	want := ingest.SourceEvent{
		ExternalID: "urban2026_msk", URL: "https://heroleague.ru/event/urban2026",
		Title: "Городские препятствия", Date: "2026-10-17", City: "Москва", Country: "RU",
		TypeLabel: "obstacle", Address: "Парк победы на Поклонной горе", Timezone: "Europe/Moscow",
		StartTime: "10:00",
	}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("urban2026_msk = %+v\nwant %+v", r, want)
	}
	if a := recs["gonka2026_472757_2609"].Address; a != "" { // адрес = город
		t.Fatalf("адрес-дубль города: %q", a)
	}
	// ссылка только там, где API её дал
	if u := recs["gonka2026_527579_2507"].RegistrationURL; !strings.HasPrefix(u, "https://heroleague.ru/") {
		t.Fatalf("registration_url = %q", u)
	}
	if u := recs["urban2026_msk"].RegistrationURL; u != "" {
		t.Fatalf("registration_url выдуман: %q", u)
	}
}

// I + весь путь: Prepare не придумывает дистанцию, страна RU, зона из источника.
func TestPrepare(t *testing.T) {
	var recs []ingest.SourceEvent
	for _, r := range parseFixture(t) {
		recs = append(recs, r)
	}
	slices.SortFunc(recs, func(a, b ingest.SourceEvent) int { return strings.Compare(a.ExternalID, b.ExternalID) })
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Rejected != 0 || st.OutOfScope != 0 {
		t.Fatalf("Rejected=%d OutOfScope=%d: %+v", st.Rejected, st.OutOfScope, st.Rejects)
	}
	// на 2026-10-01 будущие: Волгоград 10.10, Москва 17.10, СПб 03.10.
	if st.Valid != 3 || st.Expired != 17 {
		t.Fatalf("Valid=%d Expired=%d, want 4 и 17", st.Valid, st.Expired)
	}
	got := map[string]event.Event{}
	for _, p := range prep {
		if !p.InScope {
			t.Fatalf("%s вне каталога", p.Source.ExternalID)
		}
		e := p.Event
		if e.Type != event.TypeObstacle || e.CountryCode != "RU" || e.Status != event.StatusScheduled {
			t.Fatalf("%s: %+v", p.Source.ExternalID, e)
		}
		if len(e.Distances) != 0 {
			t.Fatalf("%s: выдуманная дистанция %v", p.Source.ExternalID, e.Distances)
		}
		got[p.Source.ExternalID] = e
	}
	if st.Empty["distances"] != 3 {
		t.Fatalf("Empty[distances] = %d", st.Empty["distances"])
	}
	v := got["gonka2026_472757_2609"]
	if v.StartDate.Format("2006-01-02") != "2026-10-10" || v.City != "Волгоград" || v.Timezone != "Europe/Volgograd" {
		t.Fatalf("Волгоград: %+v", v)
	}
	if m := got["urban2026_msk"]; m.Timezone != "Europe/Moscow" || m.Address != "Парк победы на Поклонной горе" {
		t.Fatalf("Москва: %+v", m)
	}
}

func TestParseEdges(t *testing.T) {
	if _, err := Parse(ingest.RawDoc{Body: []byte(`{"items": []}`)}); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Fatalf("без values: %v", err)
	}
	if _, err := Parse(ingest.RawDoc{Body: []byte(`<html>`)}); err == nil {
		t.Fatal("не JSON принят")
	}
	recs, err := Parse(ingest.RawDoc{Body: []byte(`{"values": []}`)})
	if err != nil || len(recs) != 0 {
		t.Fatalf("пустой список: %v, %v", recs, err)
	}
	// неизвестная зона или время — пустая дата, а не угаданная
	body := `{"values":[{"public_id":"gonka2027","title":"Гонка Героев","event_type":{"public_id":"gonka"},
	 "event_city":[{"public_id":"a","start_time":"2027-01-01T06:00:00","timezone":"Nope/Zone","city":{"name_ru":"Москва"}},
	 {"public_id":"b","start_time":"вчера","timezone":"Europe/Moscow","city":{"name_ru":"Москва"}},
	 {"public_id":"c","start_time":"2027-01-01T06:00:00","city":{"name_ru":"Москва"}}]}]}`
	recs, err = Parse(ingest.RawDoc{Body: []byte(body)})
	if err != nil || len(recs) != 3 {
		t.Fatalf("%v, %v", recs, err)
	}
	for i, tz := range []string{"", "Europe/Moscow", ""} {
		if r := recs[i]; r.Date != "" || r.Timezone != tz {
			t.Fatalf("%s: date %q tz %q", r.ExternalID, r.Date, r.Timezone)
		}
	}
	// новый год проекта с тем же event_type подхватывается без правок кода
	if r, _ := Parse(ingest.RawDoc{Body: []byte(strings.Replace(body, `"Nope/Zone"`, `"Europe/Moscow"`, 1))}); r[0].Date != "2027-01-01" {
		t.Fatalf("gonka2027: %+v", r[0])
	}
}

func TestSource(t *testing.T) {
	s := New(nil)
	if s.Code() != "heroleague" {
		t.Fatalf("Code = %q", s.Code())
	}
	if p := s.Policy(); p.CountryFallback != normalize.RUOnlyFallback || p.CrawlDelay != CrawlDelay {
		t.Fatalf("Policy = %+v", p)
	}
	doc := fixtureDoc(t)
	got, err1 := s.Parse(doc)
	want, err2 := Parse(doc)
	if err1 != nil || err2 != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("method Parse differs: %v %v", err1, err2)
	}
}

func TestSourceFetch(t *testing.T) {
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		_, _ = w.Write([]byte(`{"values":[]}`))
	}))
	defer srv.Close()
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent = "RunAggregator/0.3 (+contact: test@example.invalid)"
	s := New(ingest.NewFetcher(cfg))
	s.url = srv.URL + "/api/event/list"
	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 1 || gets != 1 || docs[0].URL != s.url {
		t.Fatalf("Fetch = %+v, %v, gets=%d", docs, err, gets)
	}
}
