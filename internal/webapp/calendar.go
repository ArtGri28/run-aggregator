package webapp

import (
	"encoding/binary"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"aggregator/internal/bot"
	"aggregator/internal/ics"
	"aggregator/internal/store"
)

// Подписной календарь (Phase 21, 21-UX-CONTRACT.md раздел 4): GET
// /cal/<токен>.ics без initData — календарь телефона заголовков не передаёт.
// Токен ищется по SHA-256; неизвестный — 404 без подробностей. В лог идёт
// только шаблон маршрута (logged), токена там нет.

const (
	// feedLimit — запросов в окне rateWindow на одну ссылку.
	feedLimit = 30
	feedCache = "private, max-age=3600"
)

var feedFileRE = regexp.MustCompile(`^([A-Za-z0-9_-]{43})\.ics$`)

func (s *server) calFeed(w http.ResponseWriter, r *http.Request) {
	m := feedFileRE.FindStringSubmatch(r.PathValue("file"))
	if m == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	hash := bot.FeedHash(m[1])
	if retry := s.feeds.take(int64(binary.BigEndian.Uint64(hash))); retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	ctx := r.Context()
	tgID, ok, err := s.store.FeedOwner(ctx, hash)
	if err != nil {
		s.log.Error("store", "route", r.Pattern, "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, err := s.feedBody(r, tgID)
	if err != nil {
		s.log.Error("store", "route", r.Pattern, "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/calendar; charset=utf-8")
	h.Set("Cache-Control", feedCache)
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// feedBody — календарь избранных будущих стартов пользователя.
func (s *server) feedBody(r *http.Request, tgID int64) ([]byte, error) {
	ctx := r.Context()
	name, err := s.store.Setting(ctx, store.SettingBotUsername)
	if err != nil {
		return nil, err
	}
	items, _, err := s.store.ListFavorites(ctx, tgID, s.today(), s.dict.FavLimit, 0)
	if err != nil {
		return nil, err
	}
	evs, err := bot.FavoriteICS(ctx, s.store, strings.TrimSpace(name), items)
	if err != nil {
		return nil, err
	}
	return ics.Calendar(bot.CalendarName, s.now(), true, evs), nil
}
