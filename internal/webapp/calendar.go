package webapp

import (
	"encoding/binary"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"aggregator/internal/bot"
	"aggregator/internal/event"
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

// calendarFile — POST /calendar/file[?event=<id>]: запрос файла .ics, его
// пришлёт бот (A-2, A-4). Старт недоступен или дата-заглушка — 404 gone; нет
// стартов с точной датой в избранном — 409 empty.
func (s *server) calendarFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", noStore)
	tgID, _ := TgID(ctx)
	var id int64
	if v := r.URL.Query().Get("event"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			fail(w, http.StatusBadRequest, "bad_request")
			return
		}
		root, err := s.store.RootEventID(ctx, n)
		var e event.Event
		if err == nil {
			e, err = s.store.GetEvent(ctx, root)
		}
		sp, _ := event.SportByCode(e.Sport)
		switch {
		case errors.Is(err, store.ErrNotFound):
			fail(w, http.StatusNotFound, "gone")
			return
		case err != nil:
			s.internal(w, r, err)
			return
		case !e.IsListed || e.MergedIntoID != nil || !sp.Enabled || event.DateTBD(e.Title):
			fail(w, http.StatusNotFound, "gone")
			return
		}
		id = e.ID
	} else {
		items, _, err := s.store.ListFavorites(ctx, tgID, s.today(), s.dict.FavLimit, 0)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if !slices.ContainsFunc(items, func(it store.ListItem) bool { return !event.DateTBD(it.Title) }) {
			fail(w, http.StatusConflict, "empty")
			return
		}
	}
	s.queued(w, r, tgID, store.OutboxICS, id)
}

// calendarLink — POST /calendar/link: запрос ссылки календаря, её пришлёт бот.
func (s *server) calendarLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", noStore)
	tgID, _ := TgID(r.Context())
	s.queued(w, r, tgID, store.OutboxCalLink, 0)
}

// queued ставит запрос в очередь бота: 202, в том числе если такой же запрос
// уже ждёт отправки.
func (s *server) queued(w http.ResponseWriter, r *http.Request, tgID int64, kind string, eventID int64) {
	if _, err := s.store.EnqueueAppRequest(r.Context(), tgID, kind, eventID, s.now()); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"outcome": "queued"})
}
