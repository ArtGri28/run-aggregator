package bot

import (
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Сценарии контракта Phase 21, часть 1 (21-UX-CONTRACT.md, раздел 6): «Мой
// календарь» в боте, файлы .ics и ссылка подписного календаря. Каталог —
// Марафонец, «сегодня» 2026-09-29.

// calStarts — избранное S-67: два старта октября и первый старт ноября.
func calStarts(t *testing.T) int64 {
	t.Helper()
	nov, err := strconv.ParseInt(dbScalar(t, `SELECT id::text FROM events WHERE start_date >= '2026-11-01' AND title NOT ILIKE '%уточняется%' ORDER BY start_date, id LIMIT 1`), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	favClear(t)
	favPut(t, 110, 52, nov)
	return nov
}

func calTap(t *testing.T, b *Bot, api *fakeAPI, data string) []call {
	t.Helper()
	return raw(b, api, 42, data)
}

// TestS67MyCalendar — S-67: кнопка в профиле, календарь по месяцам, пустой.
func TestS67MyCalendar(t *testing.T) {
	_, api, b := seeded(t)
	matchSnapshot(t, "s67_profile", renderCall(t, profileCmd(t, b, api, 42)))
	calStarts(t)
	pressSnap(t, b, api, "k:o", "s67_calendar")
	favClear(t)
	pressSnap(t, b, api, "k:o", "s67_empty")
}

var vevent = regexp.MustCompile(`BEGIN:VEVENT`)

// TestS68Ics — S-68: файл всех стартов, карточка с «📅 В календарь», файл
// одного старта, только даты-заглушки.
func TestS68Ics(t *testing.T) {
	s, api, b := seeded(t)
	setUserFilters(t, s, `{}`)
	calStarts(t)
	calls := calTap(t, b, api, "k:f")
	if methods(calls) != "sendDocument answerCallbackQuery" {
		t.Fatalf("k:f: вызовы %q", methods(calls))
	}
	matchSnapshot(t, "s68_file_all", renderCall(t, calls[0]))
	file := calls[0].Form["document"]
	if n := len(vevent.FindAllString(file, -1)); n != 3 || !strings.Contains(file, "UID:start-110@test_bot") ||
		!strings.Contains(file, "DTSTART;VALUE=DATE:20261002") || !strings.Contains(file, "URL:https://t.me/test_bot?start=e_110") {
		t.Errorf("файл всех стартов: %d событий\n%s", n, file)
	}
	if strings.Contains(file, "REFRESH-INTERVAL") {
		t.Errorf("файл: подписка в разовом файле\n%s", file)
	}

	pressSnap(t, b, api, at(t, "e:110:1"), "s68_card")
	calls = calTap(t, b, api, "k:e:110")
	if methods(calls) != "sendDocument answerCallbackQuery" {
		t.Fatalf("k:e: вызовы %q", methods(calls))
	}
	matchSnapshot(t, "s68_file_one", renderCall(t, calls[0]))
	if n := len(vevent.FindAllString(calls[0].Form["document"], -1)); n != 1 {
		t.Errorf("файл одного старта: %d событий", n)
	}

	dbExec(t, `UPDATE events SET title = title || ' (дата уточняется)' WHERE id IN (110, 52)`)
	favClear(t)
	favPut(t, 110, 52)
	if c := onlyToast(t, "k:f", calTap(t, b, api, "k:f")); c.Form["text"] != calNoDates {
		t.Errorf("только заглушки: %q", c.Form["text"])
	}
	if c := onlyToast(t, "k:e:110", calTap(t, b, api, "k:e:110")); c.Form["text"] != goneNotice {
		t.Errorf("файл заглушки: %q", c.Form["text"])
	}
}

var feedLinkRE = regexp.MustCompile(`/cal/([A-Za-z0-9_-]{43})\.ics`)

// linkToken — токен из сообщения K-5; снимок — с {token} вместо токена.
func linkToken(t *testing.T, c call, snap string) string {
	t.Helper()
	m := feedLinkRE.FindStringSubmatch(c.Form["text"])
	if m == nil {
		t.Fatalf("нет ссылки в %q", c.Form["text"])
	}
	matchSnapshot(t, snap, feedLinkRE.ReplaceAllString(renderCall(t, c), "/cal/{token}.ics"))
	return m[1]
}

func feedHash(t *testing.T) []byte {
	t.Helper()
	h := dbScalar(t, `SELECT COALESCE((SELECT encode(token_hash, 'hex') FROM calendar_feeds WHERE tg_id = 42), '')`)
	return []byte(h)
}

// TestS69CalendarLink — S-69: ссылка, повтор, новая ссылка, отключение; в
// базе только хеш; без домена кнопки нет.
func TestS69CalendarLink(t *testing.T) {
	_, api, b := seeded(t)
	calStarts(t)
	if c := press(t, b, api, "k:o"); strings.Contains(kbLines(t, c), "k:l") {
		t.Errorf("без домена есть кнопка ссылки")
	}
	b.domain = "app.test"
	if c := press(t, b, api, "k:o"); !strings.Contains(kbLines(t, c), "k:l") {
		t.Errorf("с доменом нет кнопки ссылки")
	}

	calls := calTap(t, b, api, "k:l")
	if methods(calls) != "sendMessage answerCallbackQuery" {
		t.Fatalf("k:l: вызовы %q", methods(calls))
	}
	tok := linkToken(t, calls[0], "s69_link")
	if got, want := string(feedHash(t)), hexOf(FeedHash(tok)); got != want {
		t.Errorf("в базе хеш %s, want %s", got, want)
	}
	if strings.Contains(dbScalar(t, `SELECT row_to_json(f)::text FROM calendar_feeds f`), tok) {
		t.Error("токен лежит в базе открытым")
	}

	calls = calTap(t, b, api, "k:l")
	matchSnapshot(t, "s69_link_again", renderCall(t, calls[0]))

	calls = calTap(t, b, api, "k:r")
	if methods(calls) != "sendMessage answerCallbackQuery" || calls[1].Form["text"] != calNewNotice {
		t.Fatalf("k:r: вызовы %q, %q", methods(calls), calls[1].Form["text"])
	}
	tok2 := feedLinkRE.FindStringSubmatch(calls[0].Form["text"])[1]
	if tok2 == tok || string(feedHash(t)) != hexOf(FeedHash(tok2)) {
		t.Errorf("новая ссылка не заменила старую")
	}

	calls = calTap(t, b, api, "k:x")
	if methods(calls) != "editMessageReplyMarkup answerCallbackQuery" || calls[1].Form["text"] != calOffNotice || len(feedHash(t)) != 0 {
		t.Errorf("k:x: вызовы %q, хеш %q", methods(calls), feedHash(t))
	}
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }
