package probeg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// headerRow — заголовок таблицы как в фикстуре (6 <th>).
const headerRow = `<tr class="info"><th class="text-right">№</th><th class="text-right">Дата</th><th>Название</th>` +
	`<th>Город</th><th class="text-right" style="max-width:200px;">Дистанции</th><th class="text-right">Ссылки</th></tr>`

// row27 — строка 27 фикстуры (событие 83327) дословно; дата заменена
// маркером DATE.
const row27 = `<tr style="font-size: 11px;"><td class="text-right">27</td><td class="text-right">DATE</a></td><td><a href="/event/83327/">Трейл «Гора Липовая — Уральский вулкан»</a><br/>
						Планируют участвовать
						<a href="/event/83327/">
							2
							человека
						</a><span class="pull-right"></span></td><td><a href="/races/city/7761/">Свердловская область, Пригородный район, пос.&nbsp;Черноисточинск</a></td><td class="text-right" style="max-width:250px;">
			
				
					
					
					20 км
					<br/>
				
					
					
					10 км
					<br/>
				
					
					
					2 км
					<br/>
				
					
					
					1 км
					
				
			
		</td><td class="text-right"><a href="https://myrace.info/events/2080">Регистрация</a><br/><span class="nobr">
					
						&nbsp;<a href="https://vk.ru/uralvulkan"><img src="/static2/images/icons/vk_small_square.png" alt="Ссылка на страницу ВКонтакте"/></a></span><br/><a href="https://disk.yandex.ru/i/4kBjwyX0q73jHQ">Положение <span class="glyphicon glyphicon-new-window" aria-hidden="true"></span></a><br/></td></tr>`

// page — страница с таблицей календаря из строк rows.
func page(rows ...string) []byte {
	return []byte(`<html><body><table class="table">` + headerRow + strings.Join(rows, "") + `</table></body></html>`)
}

func testFetcher() *ingest.Fetcher {
	cfg := ingest.DefaultFetchConfig()
	cfg.UserAgent = "RunAggregator/0.5 (+contact: test@example.invalid)"
	return ingest.NewFetcher(cfg)
}

func TestSourcePolicy(t *testing.T) {
	s := New(nil)
	if s.Code() != "probeg" {
		t.Fatalf("Code = %q", s.Code())
	}
	want := ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: 30 * time.Second, OutOfScopeMax: 0.40}
	if p := s.Policy(); p != want {
		t.Fatalf("Policy = %+v, want %+v", p, want)
	}
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	doc := ingest.RawDoc{URL: CalendarURL, Body: body, FetchedAt: fixtureFetched}
	got, err1 := s.Parse(doc)
	want2, err2 := Parse(doc)
	if err1 != nil || err2 != nil || !reflect.DeepEqual(got, want2) {
		t.Fatalf("method Parse differs from Parse: %v, %v", err1, err2)
	}
}

// D-13: ровно один GET календаря за прогон.
func TestSourceFetch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("<html>calendar</html>"))
	}))
	defer srv.Close()
	s := New(testFetcher())
	s.url = srv.URL + "/races/year_ahead/"
	docs, err := s.Fetch(context.Background())
	if err != nil || len(docs) != 1 || string(docs[0].Body) != "<html>calendar</html>" || docs[0].URL != s.url || docs[0].FetchedAt.IsZero() {
		t.Fatalf("Fetch = %+v, %v", docs, err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("запросов к серверу: %d, want 1", n)
	}
}

// Tracer (ING-12, D-04): httptest → Fetch → Parse → Prepare → Run
// (suspect) → Accept → событие ПроБЕГа в каталоге.
func TestTracerRun(t *testing.T) {
	st := storetest.Open(t)
	date := time.Now().AddDate(0, 0, 20).Format("02.01")
	body := page(strings.Replace(row27, "DATE", date, 1))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	src := New(testFetcher())
	src.url = srv.URL + "/races/year_ahead/"

	ctx := context.Background()
	r := &ingest.Runner{Store: st, Now: time.Now}
	res, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != ingest.StatusSuspect || !slices.Contains(res.Reasons, "первый прогон источника — требуется принятие") {
		t.Fatalf("первый прогон = %s %v, want suspect с причиной первого прогона", res.Status, res.Reasons)
	}
	acc, err := r.Accept(ctx, src, res.RunID)
	if err != nil || acc.Created != 1 {
		t.Fatalf("Accept = %+v, %v; want Created 1", acc, err)
	}

	items, err := st.ListEvents(ctx, event.Filter{CityKey: normalize.CityKey("Черноисточинск")}, 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListEvents = %+v, %v; want 1 событие", items, err)
	}
	it := items[0]
	if it.Title != "Трейл «Гора Липовая — Уральский вулкан»" || it.City != "Черноисточинск" || !slices.Equal(it.Distances, []int{1000, 2000, 10000, 20000}) {
		t.Fatalf("событие = %+v", it)
	}
	srcs, err := st.EventSources(ctx, it.ID)
	if err != nil || !slices.Equal(srcs, []store.EventSource{{Code: "probeg", URL: "https://probeg.org/event/83327/"}}) {
		t.Fatalf("EventSources = %+v, %v", srcs, err)
	}
	t.Logf("TRACER probeg first=%s created=%d distances=%v source=%s", res.Status, acc.Created, it.Distances, srcs[0].URL)
}

// Беларусь проходит конвейер до каталога: Fetch → Parse → Prepare → Run →
// Accept → events с country_code BY, областью и районом; страна не из
// списка разрешённых остаётся вне каталога, Россия не меняется.
func TestRunBelarus(t *testing.T) {
	st := storetest.Open(t)
	date := time.Now().AddDate(0, 0, 20).Format("02.01")
	mk := func(id, title, place string) string {
		return strings.NewReplacer("83327", id, "Трейл «Гора Липовая — Уральский вулкан»", title,
			"Свердловская область, Пригородный район, пос.&nbsp;Черноисточинск", place, "DATE", date).Replace(row27)
	}
	body := page(
		mk("84200", "Bison Trail", "Беларусь, Минская область, Минский район, д.&nbsp;Раубичи"),
		mk("77919", "Минский пробег", "Беларусь, Минск"),
		mk("76135", "Любань трейл", "Беларусь, Минская область, Любань"),
		mk("90001", "Любанский забег", "Ленинградская область, Любань"),
		mk("90002", "Алматинский забег", "Казахстан, Алматы"),
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	src := New(testFetcher())
	src.url = srv.URL + "/races/year_ahead/"

	ctx := context.Background()
	r := &ingest.Runner{Store: st, Now: time.Now}
	res, err := r.Run(ctx, src)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	acc, err := r.Accept(ctx, src, res.RunID)
	if err != nil || acc.Created != 4 || acc.OutOfScope != 1 {
		t.Fatalf("Accept = %+v, %v; want Created 4 (RU 1, BY 3), OutOfScope 1 (KZ)", acc, err)
	}

	items, err := st.ListEvents(ctx, event.Filter{}, 20, 0)
	if err != nil || len(items) != 4 {
		t.Fatalf("ListEvents = %+v, %v; want 4 событий", items, err)
	}
	type want struct{ country, region, address, cityKey string }
	wants := map[string]want{
		"Bison Trail":     {"BY", "Минская область", "Минский район", "raubichi"},
		"Минский пробег":  {"BY", "", "", "minsk"},
		"Любань трейл":    {"BY", "Минская область", "", "lyuban"},
		"Любанский забег": {"RU", "Ленинградская область", "", "lyuban"},
	}
	for _, it := range items {
		e, err := st.GetEvent(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		w, ok := wants[e.Title]
		if !ok || e.CountryCode != w.country || e.Region != w.region || e.Address != w.address || e.CityKey != w.cityKey {
			t.Errorf("%q: %q/%q/%q/%q; want %+v", e.Title, e.CountryCode, e.Region, e.Address, e.CityKey, w)
		}
		delete(wants, e.Title)
	}
	if len(wants) != 0 {
		t.Errorf("нет событий: %v", wants)
	}
	t.Logf("TRACER belarus created=%d out_of_scope=%d", acc.Created, acc.OutOfScope)
}

// Строка места → каталог (политика ПроБЕГа): страна определяется по первому
// компоненту места, в каталог входят только разрешённые страны (RU, BY);
// остальные остаются вне каталога с кодом страны; город сохраняется всегда.
func TestPlaceCountryMatrix(t *testing.T) {
	tests := []struct {
		place                       string
		inScope                     bool
		country, region, city, addr string
	}{
		// A. Россия
		{"Московская область, Химки", true, "RU", "Московская область", "Химки", ""},
		{"Ростов-на-Дону", true, "RU", "", "Ростов-на-Дону", ""},
		// B. Беларусь (в том числе «Республика Беларусь») — в каталоге с кодом BY
		{"Беларусь, Минск", true, "BY", "", "Минск", ""},
		{"Беларусь, Минская область, Любань", true, "BY", "Минская область", "Любань", ""},
		{"Республика Беларусь, Брестская область, Малорита", true, "BY", "Брестская область", "Малорита", ""},
		// D. район и населённый пункт без префикса
		{"Беларусь, Брестская область, Барановичский район, д. Колбовичи", true, "BY", "Брестская область", "Колбовичи", "Барановичский район"},
		// C. другие страны — вне каталога
		{"Польша, Краков", false, "PL", "", "Краков", ""},
		{"Казахстан, Алматы", false, "KZ", "", "Алматы", ""},
		{"Украина, Киев", false, "UA", "", "Киев", ""},
		{"Абхазия, Сухум", false, "", "Абхазия", "Сухум", ""}, // у страны нет ISO-кода
		{"Атлантида, Центр", false, "", "Атлантида", "Центр", ""},
	}
	pol := New(nil).Policy()
	for i, tt := range tests {
		id := strconv.Itoa(90000 + i)
		row := strings.NewReplacer("83327", id, "Свердловская область, Пригородный район, пос.&nbsp;Черноисточинск", tt.place, "DATE", "15.11").Replace(row27)
		recs, err := Parse(ingest.RawDoc{URL: CalendarURL, Body: page(row), FetchedAt: fixtureFetched})
		if err != nil || len(recs) != 1 {
			t.Fatalf("%q: Parse = %d записей, %v", tt.place, len(recs), err)
		}
		out, _ := ingest.Prepare(recs, fixtureNow, pol)
		e := out[0].Event
		if out[0].InScope != tt.inScope || e.CountryCode != tt.country || e.Region != tt.region || e.City != tt.city || e.Address != tt.addr {
			t.Errorf("%q: in_scope=%v %q/%q/%q/%q; want %v %q/%q/%q/%q", tt.place,
				out[0].InScope, e.CountryCode, e.Region, e.City, e.Address, tt.inScope, tt.country, tt.region, tt.city, tt.addr)
		}
		if tt.inScope && e.CityKey == "" {
			t.Errorf("%q: пустой city_key", tt.place)
		}
	}
}
