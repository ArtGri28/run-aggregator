package ingest

import (
	"testing"
	"time"

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
	if p := out[0]; p.Event.CountryCode != "RU" || !p.InScope || len(p.Event.Distances) != 1 || p.Event.Distances[0].Meters != 10000 {
		t.Errorf("event = %+v, InScope %v", p.Event, p.InScope)
	}

	_, st = Prepare(recs[:1], now, Policy{CountryFallback: normalize.NoFallback})
	if st.OutOfScope != 1 {
		t.Errorf("NoFallback: OutOfScope = %d, want 1", st.OutOfScope)
	}
}
