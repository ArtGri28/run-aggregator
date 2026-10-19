package webapp

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

// Плавание и велоспорт в Mini App: справочник даёт их корзины и дисциплины,
// счётчик и карточку; дисциплина «другое» приходит кодом, подпись карточки
// без неё строит event.js.
func TestMetaSwimCycle(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	storetest.EnableSport(t, event.SportSwimming)
	storetest.EnableSport(t, event.SportCycling)
	e := &event.Event{Title: "Велогонка", TitleNorm: "велогонка", StartDate: fixtureToday.AddDate(0, 0, 5), CountryCode: "RU",
		Type: event.TypeOther, Sport: event.SportCycling, Status: event.StatusScheduled, Distances: []event.Distance{{Meters: 50000}}}
	if _, err := s.SaveEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, s)
	var m metaJSON
	r.getJSON(2, "/api/v1/meta", http.StatusOK, &m)
	counts := map[event.Sport]int{}
	for _, sp := range m.Sports {
		counts[sp.Code] = sp.Count
	}
	if c, ok := counts[event.SportSwimming]; !ok || c != 0 || counts[event.SportCycling] != 1 {
		t.Fatalf("sports = %+v", m.Sports)
	}
	if b := m.Buckets["swimming"]; len(b) != 4 || b[0].Label != "До 1 км" || b[0].Hi != 999 {
		t.Fatalf("buckets.swimming = %+v", b)
	}
	if b := m.Buckets["cycling"]; len(b) != 4 || b[1].Label != "50–100 км" || b[1].Lo != 49999 {
		t.Fatalf("buckets.cycling = %+v", b)
	}
	if w, c := m.Types["swimming"], m.Types["cycling"]; len(w) != 2 || w[0] != (typeJSON{"open_water", "Открытая вода"}) ||
		len(c) != 3 || c[0] != (typeJSON{"road_bike", "Шоссе"}) || c[1] != (typeJSON{"mtb", "МТБ"}) {
		t.Fatalf("types = %+v", m.Types)
	}
	var card cardJSON
	r.getJSON(2, "/api/v1/events/"+strconv.FormatInt(e.ID, 10), http.StatusOK, &card)
	if card.Sport != event.SportCycling || card.Type != event.TypeOther {
		t.Fatalf("card = %+v", card)
	}
}
