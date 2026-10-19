package myrace

import (
	"testing"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

// Событие только велоспорта — запись cycling с дистанциями велоактивностей;
// плавание и мультиспорт пропускаются; смешанное с бегом — беговое (TestParse).
func TestParseCycling(t *testing.T) {
	recs, err := Parse(ingest.RawDoc{Body: readFixture(t, "testdata/calendar-cycling.json"), FetchedAt: fixtureNow})
	if err != nil || len(recs) != 1 {
		t.Fatalf("Parse = %+v, %v; want 1 запись", recs, err)
	}
	r := recs[0]
	if r.Sport != "cycling" || r.Distances != "Fat bike - 50 км, Вело - 28 км" || r.City != "Фролово" || r.Country != "RU" {
		t.Fatalf("запись: %+v", r)
	}
	if got := normalize.SportType(event.SportCycling, r.TypeLabel); got != event.TypeOther {
		t.Errorf("дисциплина %q, want other", got)
	}
	prep, st := ingest.Prepare(recs, fixtureNow, New(nil, "").Policy())
	if st.Rejected != 0 || len(prep) != 1 {
		t.Fatalf("Prepare: %d записей, %v", len(prep), st.Rejects)
	}

	for _, sports := range []string{`[{"code":"swimming"}]`, `[{"code":"cycling"},{"code":"swimming"}]`, `[]`} {
		body := []byte(`{"events":[{"id":"1","name":"Старт","sports":` + sports + `,"start_date":"2026-11-01","end_date":"2026-11-01",` +
			`"location":{"country":"RU","city":"Москва"},"url":"https://myrace.info/events/1","activities":[{"name":"10 км","sport":{"code":"cycling"}}]}]}`)
		if recs, err := Parse(ingest.RawDoc{Body: body}); err != nil || len(recs) != 0 {
			t.Errorf("виды %s: %+v, %v; want без записи", sports, recs, err)
		}
	}
}
