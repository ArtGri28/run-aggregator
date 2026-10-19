package tristyle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

// swimCycleNow — день загрузки фикстур плавания и велоспорта (testdata/README.md).
var swimCycleNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// swimCycleSource — источник со всеми списками на тестовом сервере.
func swimCycleSource(srvURL string) *Source {
	s := skiSource(srvURL)
	s.more = []sportList{
		{event.SportSwimming, strings.Replace(SwimListURL, "https://tristyle.club", srvURL, 1)},
		{event.SportCycling, strings.Replace(CycleListURL, "https://tristyle.club", srvURL, 1)},
	}
	return s
}

// listServer — списки по параметру specialization и страницы событий; hits —
// запросы по адресу целиком.
type listServer struct {
	mu    sync.Mutex
	lists map[string][]byte
	pages map[string][]byte
	hits  map[string]int
}

func (s *listServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[r.URL.RequestURI()]++
	body := s.pages[r.URL.Path]
	if r.URL.Path == "/competition/" {
		body = s.lists[r.URL.Query().Get("specialization")]
	}
	if body == nil {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(body)
}

func TestSwimCycleFetch(t *testing.T) {
	srv := &listServer{hits: map[string]int{}, pages: map[string][]byte{}, lists: map[string][]byte{
		"running":  listPage("5"),
		"skiing":   listPage("6"),
		"swimming": listPage("7", "5"),
		"cycling":  listPage("8", "7"),
	}}
	for _, id := range []string{"5", "6", "7", "8"} {
		srv.pages["/competition/"+id+"/"] = []byte("<html>" + id + "</html>")
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	docs, err := swimCycleSource(ts.URL).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range docs[4:] {
		got = append(got, strings.TrimPrefix(d.URL, ts.URL))
	}
	// Номер из нескольких списков — в первом из них; страница — один раз.
	want := []string{"/competition/5/", "/competition/6/#skiing", "/competition/7/#swimming", "/competition/8/#cycling"}
	if len(docs) != 8 || !slices.Equal(got, want) {
		t.Fatalf("Fetch: %d документов, события %v; want 8 и %v", len(docs), got, want)
	}
	for _, id := range []string{"5", "6", "7", "8"} {
		if n := srv.hits["/competition/"+id+"/"]; n != 1 {
			t.Errorf("страница %s запрошена %d раз", id, n)
		}
	}
	srv.lists["cycling"] = nil
	if docs, err := swimCycleSource(ts.URL).Fetch(context.Background()); err == nil || docs != nil {
		t.Fatalf("список велоспорта недоступен: Fetch = %d документов, %v; want ошибку", len(docs), err)
	}
}

func TestParseSwimCycleFixtures(t *testing.T) {
	for name, want := range map[string][]string{
		"list-swimming.html": {"1098", "1172", "1179", "1166", "1163", "1165"},
		"list-cycling.html":  {"1099", "1214", "673", "674"},
	} {
		if ids, err := listIDs(fixture(t, name)); err != nil || !slices.Equal(ids, want) {
			t.Fatalf("%s: listIDs = %v, %v; want %v", name, ids, err, want)
		}
	}
	pages := []struct {
		id    string
		sport event.Sport
	}{
		{"1098", event.SportSwimming}, {"1172", event.SportSwimming}, {"1179", event.SportSwimming}, {"1163", event.SportSwimming},
		{"1099", event.SportCycling}, {"1214", event.SportCycling}, {"673", event.SportCycling}, {"674", event.SportCycling},
	}
	var recs []ingest.SourceEvent
	for _, p := range pages {
		got, err := Parse(ingest.RawDoc{URL: eventBase + p.id + "/#" + string(p.sport), Body: fixture(t, "event-"+p.id+".html"), FetchedAt: swimCycleNow})
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if p.id == "1163" {
			if got != nil {
				t.Fatalf("1163 (Стамбул, Турция): %+v; want без записи", got)
			}
			continue
		}
		if len(got) != 1 {
			t.Fatalf("%s: %d записей", p.id, len(got))
		}
		r := got[0]
		r.TypeLabel = string(normalize.SportType(event.Sport(r.Sport), r.TypeLabel))
		recs = append(recs, r)
	}
	var want []ingest.SourceEvent
	if err := json.Unmarshal(fixture(t, "swimcycle.golden.json"), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recs, want) {
		g, _ := json.MarshalIndent(recs, "", "  ")
		t.Fatalf("записи не совпадают с эталоном:\n%s", g)
	}

	// Все семь записей проходят Prepare с видом спорта и дисциплиной.
	prep, st := ingest.Prepare(recs, swimCycleNow, New(nil).Policy())
	if st.Rejected != 0 || len(prep) != 7 {
		t.Fatalf("Prepare: %d записей, отбраковано %d (%v)", len(prep), st.Rejected, st.Rejects)
	}
}

func TestSwimCycleRules(t *testing.T) {
	for loc, want := range map[string]bool{
		"Стамбул, Турция": true, "Марсель, Франция ": true, "Сочи, Россия": true,
		"Минск": false, "г. Брест, ул. Московская 147": false, "Минск, Беларусь": false, "": false,
	} {
		if got := isAbroad(loc); got != want {
			t.Errorf("isAbroad(%q) = %v, want %v", loc, got, want)
		}
	}
	// Пометка о дате в названии уже есть — вторая не ставится; без неё —
	// ставится, как у лыж. Бегового документа правила не касаются.
	for _, c := range []struct{ url, title, want string }{
		{"/#swimming", "Заплыв (предполагаемая дата)", "Заплыв (предполагаемая дата)"},
		{"/#cycling", "Гонка (дата уточняется)", "Гонка (дата уточняется)"},
		{"/#cycling", "Гонка", "(дата уточняется) Гонка"},
		{"/", "Забег", "Забег"},
	} {
		recs, err := Parse(ingest.RawDoc{URL: eventBase + "9" + c.url, Body: eventPage(c.title, "2027-03-01T12:00:00", "Минск", "<p>март 2027 г.</p>")})
		if err != nil || len(recs) != 1 || recs[0].Title != c.want {
			t.Errorf("%s %q: %+v, %v; want название %q", c.url, c.title, recs, err, c.want)
		}
	}
	// Зарубежный беговой и лыжный старт остаются, как раньше.
	for _, u := range []string{"/", "/#skiing"} {
		if recs, err := Parse(ingest.RawDoc{URL: eventBase + "9" + u, Body: eventPage("Старт", "2027-03-05T12:00:00", "Сочи, Россия", "")}); err != nil || len(recs) != 1 {
			t.Errorf("%s: зарубежный старт: %+v, %v; want запись", u, recs, err)
		}
	}
}
