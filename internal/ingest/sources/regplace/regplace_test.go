package regplace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
)

// fixtureNow — день загрузки образцов (testdata/README.md).
var fixtureNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// fixtureEvents — страницы событий образца с фрагментом, который ставит
// Fetch по карточке списка.
var fixtureEvents = []string{"spaskamenka2026#running+reg", "ultra-trail-2026#running+reg", "krylo-2027#swimming+reg", "odintsovomtb2026#cycling+reg", "bezpredela#swimming+reg"}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestListFixture(t *testing.T) {
	for _, c := range []struct {
		file, next string
		want       []card
	}{
		{"events-p1.html", "/events?page=2", []card{
			{"spaskamenka2026", event.SportRunning, true}, {"trejl-kutuzov-2026", event.SportRunning, false},
			{"wpybz4ku", event.SportRunning, true}, {"osenniy-flow-2026", event.SportCycling, true},
			{"autumn-exacerbation", event.SportRunning, true}, {"velogonka-mtb-potertoe-sedlo-copy", event.SportCycling, true},
			{"ultra-trail-2026", event.SportRunning, true}, {"odintsovomtb2026", event.SportCycling, true},
			{"owpyupaw", event.SportRunning, true},
		}},
		{"events-p5.html", "", []card{
			{"swimcup-onego-2027", event.SportSwimming, true}, {"swimcup-pskov-2027", event.SportSwimming, true},
			{"fvvsr-2027-3", event.SportSwimming, false}, {"altai27", event.SportSwimming, true},
			{"ruza2027", event.SportSwimming, true}, {"str-27", event.SportSwimming, true},
			{"swimcup-sulak-2027", event.SportSwimming, true},
		}},
	} {
		got, next, err := listCards(read(t, c.file))
		if err != nil || next != c.next || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, next %q, %v", c.file, got, next, err)
		}
	}
}

func TestEventFixture(t *testing.T) {
	var recs []ingest.SourceEvent
	for _, e := range fixtureEvents {
		slug, _, _ := strings.Cut(e, "#")
		r, err := Parse(ingest.RawDoc{URL: Site + "/events/" + e, Body: read(t, "event-"+slug+".html"), FetchedAt: fixtureNow})
		if err != nil || len(r) != 1 {
			t.Fatalf("%s: %d записей, %v", e, len(r), err)
		}
		recs = append(recs, r...)
	}
	var want []ingest.SourceEvent
	if err := json.Unmarshal(read(t, "events.golden.json"), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recs, want) {
		g, _ := json.MarshalIndent(recs, "", "  ")
		t.Fatalf("записи не совпадают с эталоном:\n%s", g)
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil).Policy())
	if st.Rejected != 0 || len(prep) != len(recs) || st.UnrecognizedRegion != 0 {
		t.Fatalf("Prepare: %d из %d, отбраковано %d, нераспознанных областей %d (%v)", len(prep), len(recs), st.Rejected, st.UnrecognizedRegion, st.Unrecognized)
	}
	types := []event.Type{event.TypeTrail, event.TypeTrail, event.TypeOpenWater, event.TypeMTB, event.TypeOther}
	for i, p := range prep {
		if p.Event.Type != types[i] {
			t.Errorf("%s: тип %s, want %s", p.Source.ExternalID, p.Event.Type, types[i])
		}
	}
}

func cardHTML(slug, state, title, meta string) string {
	return `<article class="b-event-card b-event-card--registration-` + state + `"><h3><a class="b-event-card__title-link" href="/events/` + slug +
		`?rp_c=x">` + title + `</a></h3><p class="b-event-card__meta">` + meta + `</p></article>`
}

func eventHTML(date, place string, races ...string) string {
	b := `<div class="event-page__date">` + date + `</div><h1>Старт</h1><p class="event-page__location-text">` + place + `</p><div class="event-page__races">`
	for _, r := range races {
		b += `<article class="race-card"><div class="race-card__meta-item"><span class="race-card__meta-label">Дистанция</span><span>` + r + `</span></div></article>`
	}
	return b + `</div>`
}

func TestParseEdge(t *testing.T) {
	cs, _, err := listCards([]byte(cardHTML("a", "open", "Лыжная гонка", "<strong>1 марта</strong> • Тула • Лыжи") +
		cardHTML("camp", "open", "Кэмп", "<strong>2 – 9 января</strong> • Самара • Лыжи, Лыжероллеры, Кемп") +
		cardHTML("zw", "open", "Онлайн-гонка", "<strong>1 марта</strong> • Велогонка") +
		cardHTML("tri", "closed", "Триатлон", "<strong>1 июня</strong> • Казань • Триатлон, Бег") +
		cardHTML("chess", "open", "Шахматы", "<strong>1 марта</strong> • Тула • Другой вид")))
	want := []card{{"a", event.SportSkiing, true}, {"tri", event.SportMultisport, false}}
	if err != nil || !reflect.DeepEqual(cs, want) {
		t.Errorf("отбор карточек: %+v, %v", cs, err)
	}
	if _, _, err := listCards([]byte(`<p>нет</p>`)); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Errorf("пустой список: %v", err)
	}

	parse := func(frag, body string) ingest.SourceEvent {
		t.Helper()
		r, err := Parse(ingest.RawDoc{URL: Site + "/events/x#" + frag, Body: []byte(body)})
		if err != nil || len(r) != 1 {
			t.Fatalf("%s: %v %v", frag, r, err)
		}
		return r[0]
	}
	if r := parse("skiing", eventHTML("31.12–02.01.2027", "Минск. Минская область, Беларусь", "1,0 м", "100,0 м", "10,0 км")); r.Date != "31.12.2026" || r.EndDate != "02.01.2027" ||
		r.Country != "BY" || r.Distances != "100 м, 10 км" || r.RegistrationURL != "" || r.TypeLabel != "Лыжные гонки" || r.Sport != "skiing" {
		t.Errorf("стык лет и Беларусь: %+v", r)
	}
	if r := parse("swimming+reg", eventHTML("08.11.2026", "Москва", "50,0 м")); r.TypeLabel != "Старт" || r.Distances != "50 м" || r.RegistrationURL != Site+"/events/x/registration" {
		t.Errorf("заплыв: %+v", r)
	}
	if r := parse("swimming", strings.Replace(eventHTML("08.11.2026", "Москва"), "Старт", "Swimcup Индор Москва", 1)); r.TypeLabel != "бассейн" || r.City != "Москва" || r.Address != "" {
		t.Errorf("бассейн: %+v", r)
	}
	for in, want := range map[string][2]string{
		"24.10.2026":            {"24.10.2026", ""},
		"30.10–01.11.2026":      {"30.10.2026", "01.11.2026"},
		"21–23.02.2027":         {"21.02.2027", "23.02.2027"},
		"30.12.2026–02.01.2027": {"30.12.2026", "02.01.2027"},
		"21.02":                 {"", ""},
		"скоро":                 {"", ""},
	} {
		if s, e := pageDates(in); s != want[0] || e != want[1] {
			t.Errorf("pageDates(%q) = %q, %q; want %q", in, s, e, want)
		}
	}
	// Незнакомая дата не роняет прогон: запись с датой как есть Prepare
	// отбраковывает.
	odd := parse("running", eventHTML("весна 2027", "Тула", "5,0 км"))
	if odd.Date != "весна 2027" {
		t.Errorf("незнакомая дата: %+v", odd)
	}
	if prep, st := ingest.Prepare([]ingest.SourceEvent{odd}, fixtureNow, New(nil).Policy()); len(prep) != 0 || st.Rejected != 1 {
		t.Errorf("незнакомая дата: Prepare %d, отбраковано %d", len(prep), st.Rejected)
	}
	if _, err := Parse(ingest.RawDoc{URL: Site + "/events/x#running", Body: []byte(`<h1>Старт</h1>`)}); !errors.Is(err, ingest.ErrStructureNotFound) {
		t.Errorf("без даты: %v", err)
	}
	if _, err := Parse(ingest.RawDoc{URL: Site + "/events/x#chess", Body: []byte(eventHTML("08.11.2026", "Москва"))}); err == nil {
		t.Error("неизвестный вид спорта принят")
	}
}

// TestFetch — страницы списка по ссылке «следующая», затем страницы
// отобранных событий; ошибка страницы события — ошибка прогона; больше
// maxPages страниц — ошибка.
func TestFetch(t *testing.T) {
	var hits atomic.Int32
	fail, endless := "", false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case r.URL.Path == "/events" && (r.URL.Query().Get("page") == "" || endless):
			w.Write([]byte(cardHTML("a", "open", "Забег", "<strong>1 марта</strong> • Тула • Бег") + `<a class="page-link" rel="next" href="/events?page=2">2</a>`))
		case r.URL.Path == "/events":
			w.Write([]byte(cardHTML("b", "closed", "Заплыв", "<strong>2 марта</strong> • Тула • Плавание") + cardHTML("c", "open", "Борьба", "<strong>2 марта</strong> • Тула • Спортивная борьба")))
		case r.URL.Path == fail:
			http.Error(w, "нет", http.StatusNotFound)
		default:
			w.Write([]byte(eventHTML("01.03.2027", "Тула", "5,0 км")))
		}
	}))
	defer srv.Close()
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent, cfg.Attempts = "test", 1
	s := New(ingest.NewFetcher(cfg))
	s.site, s.delay = srv.URL, time.Millisecond

	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 4 || hits.Load() != 4 {
		t.Fatalf("прогон: %d документов, %d запросов, %v", len(docs), hits.Load(), err)
	}
	var recs []ingest.SourceEvent
	for _, d := range docs {
		r, err := s.Parse(d)
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r...)
	}
	if len(recs) != 2 || recs[0].ExternalID != "a" || recs[0].RegistrationURL == "" || recs[1].Sport != "swimming" || recs[1].RegistrationURL != "" {
		t.Errorf("записи: %+v", recs)
	}

	fail = "/events/b"
	if _, err := s.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "событие b") {
		t.Errorf("ошибка страницы события: %v", err)
	}
	fail, endless = "", true
	if _, err := s.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "страниц") {
		t.Errorf("бесконечный список: %v", err)
	}
}
