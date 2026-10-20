package webapp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"aggregator/internal/bot"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// TestCalendarFeed — подписной календарь: без initData; известная ссылка —
// календарь избранных будущих стартов; неизвестная и битая — 404; предел
// запросов на ссылку; токена нет в логе.
func TestCalendarFeed(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	ctx := context.Background()
	const token = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	dbExec(t, `DELETE FROM favorites`)
	dbExec(t, `INSERT INTO users (tg_id) VALUES (42) ON CONFLICT DO NOTHING`)
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 110, now()), (42, 52, now())`)
	if err := s.SetFeed(ctx, 42, bot.FeedHash(token), fixtureNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, store.SettingBotUsername, "test_bot"); err != nil {
		t.Fatal(err)
	}

	resp, body := r.get("/cal/"+token+".ics", "")
	cal := string(body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/calendar; charset=utf-8" || resp.Header.Get("Cache-Control") != feedCache {
		t.Fatalf("календарь: %d %v", resp.StatusCode, resp.Header)
	}
	if strings.Count(cal, "BEGIN:VEVENT") != 2 || !strings.Contains(cal, "UID:start-110@test_bot") || !strings.Contains(cal, "REFRESH-INTERVAL") {
		t.Errorf("календарь:\n%s", cal)
	}
	for _, path := range []string{"/cal/" + strings.Repeat("B", 43) + ".ics", "/cal/short.ics", "/cal/" + token} {
		if resp, _ := r.get(path, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, resp.StatusCode)
		}
	}
	for i := 1; i < feedLimit; i++ {
		r.get("/cal/"+token+".ics", "")
	}
	if resp, _ := r.get("/cal/"+token+".ics", ""); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("сверх предела: %d, want 429", resp.StatusCode)
	}
	if logs := r.logs.String(); strings.Contains(logs, token) || !strings.Contains(logs, "route=\"GET /cal/{file}\"") {
		t.Errorf("лог:\n%s", logs)
	}
}
