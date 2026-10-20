package webapp

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"aggregator/internal/bot"
	"aggregator/internal/event"
)

// Подписка на новые старты из Mini App (Phase 21, 21-UX-CONTRACT.md A-3):
// одна на пользователя, общая с ботом. Фильтры каталога — параметрами
// адреса, как у GET /events: sport ("" — все виды спорта), country, city,
// type и dist (только с видом спорта); даты не учитываются.

type subJSON struct {
	On      bool         `json:"on"`
	Same    bool         `json:"same,omitempty"`
	Filters *subFiltJSON `json:"filters,omitempty"`
}

type subFiltJSON struct {
	Sport    string `json:"sport"`
	Country  string `json:"country"`
	City     string `json:"city"`
	CityName string `json:"city_name"`
	Type     string `json:"type"`
	Dist     []int  `json:"dist"`
}

// subFilters разбирает фильтры каталога в фильтры подписки.
func (s *server) subFilters(q url.Values) (bot.AppFilters, error) {
	var a bot.AppFilters
	if sp := q.Get("sport"); sp != "" {
		info, ok := event.SportByCode(event.Sport(sp))
		if !ok || !info.Enabled {
			return a, errBadRequest
		}
		a.Sport = sp
	}
	code, all, err := s.countryParam(q)
	if err != nil {
		return a, err
	}
	a.Country = code
	if all {
		a.Country = "*"
	}
	if city := q.Get("city"); city != "" {
		if all || len(city) > 60 || !cityKeyRE.MatchString(city) {
			return a, errBadRequest
		}
		a.City = city
	}
	if a.Sport == "" {
		if q.Get("type") != "" || q.Get("dist") != "" {
			return a, errBadRequest
		}
		return a, nil
	}
	if t := q.Get("type"); t != "" {
		if !typeAllowed(event.Sport(a.Sport), event.Type(t)) {
			return a, errBadRequest
		}
		a.Type = t
	}
	if dist := q.Get("dist"); dist != "" {
		bs := s.dict.Buckets[event.Sport(a.Sport)]
		for _, p := range strings.Split(dist, ",") {
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(bs) {
				return a, errBadRequest
			}
			a.Dist = append(a.Dist, i)
		}
	}
	return a, nil
}

// subscription — GET /subscription?<фильтры каталога>: включена ли подписка,
// совпадает ли с текущими фильтрами и её фильтры.
func (s *server) subscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", noStore)
	tgID, _ := TgID(ctx)
	cur, err := s.subFilters(r.URL.Query())
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	raw, _, ok, err := s.store.Subscription(ctx, tgID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, subJSON{})
		return
	}
	out := subJSON{On: true}
	if want, err := bot.AppSubscription(cur); err == nil {
		out.Same = bot.SameSubscription(raw, want)
	}
	if v, ok := bot.ViewSubscription(raw); ok {
		f := &subFiltJSON{Sport: v.Sport, Country: v.Country, City: v.City, Type: v.Type, Dist: v.Dist}
		if f.Dist == nil {
			f.Dist = []int{}
		}
		if f.City != "" && f.Country != "" && f.Country != "*" {
			if f.CityName, _, err = s.store.CityName(ctx, f.Country, f.City, s.today()); err != nil {
				s.internal(w, r, err)
				return
			}
		}
		out.Filters = f
	}
	writeJSON(w, http.StatusOK, out)
}

// subscribe — PUT /subscription?<фильтры каталога>: включить или заменить.
func (s *server) subscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tgID, _ := TgID(ctx)
	a, err := s.subFilters(r.URL.Query())
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	raw, err := bot.AppSubscription(a)
	if err == nil {
		err = s.store.Subscribe(ctx, tgID, raw, s.now())
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unsubscribe — DELETE /subscription.
func (s *server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	tgID, _ := TgID(r.Context())
	if err := s.store.Unsubscribe(r.Context(), tgID); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
