package webapp

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

// TestUsage — запрос с проверенным initData отмечает день в Mini App;
// карточка, поиск и добавление в избранное прибавляют счётчики; запрос без
// initData ничего не пишет.
func TestUsage(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	dbExec(t, `DELETE FROM favorites`)
	r := newRig(t, s)
	items, err := s.ListEvents(context.Background(), event.Filter{From: fixtureToday}, 1, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListEvents: %v", err)
	}
	id := strconv.FormatInt(items[0].ID, 10)

	r.get("/api/v1/meta", "")
	if n := dbCount(t, `SELECT count(*) FROM user_days`); n != 0 {
		t.Errorf("без initData отметок %d, want 0", n)
	}
	for range 2 {
		if resp, _ := r.get("/api/v1/events/"+id, r.auth(7)); resp.StatusCode != http.StatusOK {
			t.Fatalf("карточка: %d", resp.StatusCode)
		}
	}
	r.get("/api/v1/search?q=%D0%BC%D0%B0%D1%80%D0%B0%D1%84%D0%BE%D0%BD", r.auth(7))
	r.put(7, id)
	r.put(7, id)
	if n := dbCount(t, `SELECT count(*) FROM user_days WHERE tg_id = 7 AND app AND NOT bot AND day = '2026-09-29'`); n != 1 {
		t.Errorf("отметок Mini App %d, want 1", n)
	}
	for metric, want := range map[string]int{"card_app": 2, "search_app": 1, "fav_app": 1} {
		if n := dbCount(t, `SELECT COALESCE(sum(n), 0) FROM usage_counters WHERE metric = '`+metric+`'`); n != want {
			t.Errorf("%s = %d, want %d", metric, n, want)
		}
	}
}
