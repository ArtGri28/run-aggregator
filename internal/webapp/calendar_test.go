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

// TestCalendarRequests — POST /calendar/file и /calendar/link: строка очереди
// для бота; повтор до отправки строки не добавляет; недоступный старт и
// дата-заглушка — 404; нет стартов с точной датой — 409; имя бота в meta;
// признак даты-заглушки в карточке.
func TestCalendarRequests(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	ctx := context.Background()
	const user = 42
	dbExec(t, `DELETE FROM favorites`)
	rows := func(kind string) int { return dbCount(t, `SELECT count(*) FROM outbox WHERE kind = '`+kind+`'`) }
	code := func(path string) (int, string) {
		resp, body := r.do(http.MethodPost, path, r.auth(user), "")
		return resp.StatusCode, string(body)
	}

	if c, b := code("/api/v1/calendar/file"); c != http.StatusConflict || !strings.Contains(b, "empty") {
		t.Errorf("пустое избранное: %d %s", c, b)
	}
	dbExec(t, `INSERT INTO users (tg_id) VALUES (42) ON CONFLICT DO NOTHING`)
	dbExec(t, `INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, 110, now())`)
	for range 2 {
		if c, _ := code("/api/v1/calendar/file"); c != http.StatusAccepted {
			t.Fatalf("файл всех: %d", c)
		}
	}
	if c, _ := code("/api/v1/calendar/file?event=110"); c != http.StatusAccepted || rows("ics") != 2 {
		t.Errorf("файл старта: %d, строк %d, want 2 (повтор всех не добавил)", c, rows("ics"))
	}
	if c, _ := code("/api/v1/calendar/file?event=999999"); c != http.StatusNotFound {
		t.Errorf("нет старта: %d", c)
	}
	if c, _ := code("/api/v1/calendar/file?event=x"); c != http.StatusBadRequest {
		t.Errorf("плохой id: %d", c)
	}
	for range 2 {
		code("/api/v1/calendar/link")
	}
	if rows("cal_link") != 1 {
		t.Errorf("строк ссылки %d, want 1", rows("cal_link"))
	}

	dbExec(t, `UPDATE events SET title = title || ' (дата уточняется)' WHERE id = 110`)
	if c, _ := code("/api/v1/calendar/file?event=110"); c != http.StatusNotFound {
		t.Errorf("заглушка: %d", c)
	}
	var card cardJSON
	r.getJSON(user, "/api/v1/events/110", http.StatusOK, &card)
	if !card.DateTBD {
		t.Error("карточка: нет признака даты-заглушки")
	}
	if c, b := code("/api/v1/calendar/file"); c != http.StatusConflict {
		t.Errorf("только заглушки: %d %s", c, b)
	}

	var m metaJSON
	r.getJSON(user, "/api/v1/meta", http.StatusOK, &m)
	if m.Bot != "" {
		t.Errorf("имя бота до записи: %q", m.Bot)
	}
	if err := s.SetSetting(ctx, store.SettingBotUsername, "test_bot"); err != nil {
		t.Fatal(err)
	}
	r.getJSON(user, "/api/v1/meta", http.StatusOK, &m)
	if m.Bot != "test_bot" {
		t.Errorf("имя бота: %q", m.Bot)
	}
}
