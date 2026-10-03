package bot

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"aggregator/internal/dedup"
	"aggregator/internal/event"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
)

const wouldLine = "Автослияние слило бы эту пару (сейчас выключено)"

// passEvent — запись источника code со своим событием title через 10 дней
// от сегодня (окно прохода дедупликации считается от текущей даты).
func passEvent(t *testing.T, s *store.Store, code, title string) int64 {
	t.Helper()
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := s.StartRun(ctx, src, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, runID, store.RunFinish{Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	dupeSeq++
	rowID, _, err := s.UpsertSourceEvent(ctx, src, runID, fmt.Sprint(dupeSeq), fmt.Sprintf("https://example.org/%s/%d", code, dupeSeq), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	y, m, d := time.Now().UTC().AddDate(0, 0, 10).Date()
	e := event.Event{
		Title: title, TitleNorm: normalize.TitleNorm(title),
		StartDate:   time.Date(y, m, d, 0, 0, 0, 0, time.UTC),
		CountryCode: "RU", City: "Казань", CityKey: "kazan",
		Type: event.TypeRoad, Status: event.StatusScheduled,
		Distances: []event.Distance{{Meters: 10000}},
	}
	if _, _, err := s.SaveEventFor(ctx, rowID, &e, event.FoldConfig{}); err != nil {
		t.Fatal(err)
	}
	return e.ID
}

// TestDupesWouldAuto — tracer 07-01: пара, которую слило бы автослияние, при
// DEDUP_AUTO_MERGE=false проходом становится кандидатом с would_auto, каталог
// не меняется, /dupes показывает строку; старые reasons без ключа — без строки.
func TestDupesWouldAuto(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	a := passEvent(t, e.s, "marathonec", "Казанский осенний полумарафон")
	b := passEvent(t, e.s, "probeg", "Казанский осенний полумарафон")
	get := func(id int64) event.Event {
		ev, err := e.s.GetEvent(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	beforeA, beforeB := get(a), get(b)

	if err := dedup.Pass(ctx, e.s, dedup.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	catalog := "unchanged"
	if !reflect.DeepEqual(get(a), beforeA) || !reflect.DeepEqual(get(b), beforeB) {
		catalog = "changed"
		t.Fatalf("проход изменил события:\n%+v\n%+v", get(a), get(b))
	}
	listedRoots(t, e.s, a, b)
	c1, err := e.s.NextCandidate(ctx, 0)
	if err != nil || !strings.Contains(string(c1.Reasons), `"would_auto": true`) {
		t.Fatalf("кандидат = %+v, %v; want would_auto", c1, err)
	}
	// Старый кандидат: reasons без ключа would_auto (записаны до 07-01).
	cOld, _, _ := seedCandidate(t, e.s, "Кремлёвский забег")

	e.api.reset()
	e.b.api.ProcessUpdate(ctx, commandUpdate(1, "/dupes"))
	sends := e.api.of("sendMessage")
	if len(sends) != 1 {
		t.Fatalf("/dupes: %+v", sends)
	}
	text := sends[0].Form["text"]
	line := "no"
	if strings.Contains(text, "Кандидат #"+run(c1.ID)) && strings.Contains(text, wouldLine) && strings.Contains(text, "название 1,00") {
		line = "yes"
	}
	if line != "yes" {
		t.Fatalf("экран кандидата с would_auto:\n%s", text)
	}
	edit := e.oneEdit(t, "d:s:"+run(c1.ID))
	old := edit.Form["text"]
	if !strings.Contains(old, "Кандидат #"+run(cOld)) || strings.Contains(old, wouldLine) || !strings.Contains(old, "название 0,89") {
		t.Fatalf("экран старого кандидата:\n%s", old)
	}
	t.Logf("TRACER would_auto=1 merged=0 catalog=%s dupes_line=%s", catalog, line)
}
