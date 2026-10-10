package webapp

import (
	"time"

	"aggregator/internal/bot"
	"aggregator/internal/event"
	"aggregator/internal/store"
)

// JSON-представление справочников GET /api/v1/meta. Источник значений —
// bot.Catalog() и event.Sports: своих копий подписей и границ здесь нет.

type sportJSON struct {
	Code  event.Sport `json:"code"`
	Title string      `json:"title"`
	Emoji string      `json:"emoji"`
	Count int         `json:"count"`
}

type typeJSON struct {
	Code  event.Type `json:"code"`
	Label string     `json:"label"`
}

type bucketJSON struct {
	I     int    `json:"i"`
	Label string `json:"label"`
	Lo    int    `json:"lo"`
	Hi    int    `json:"hi"`
}

type countryJSON struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type optionJSON struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type limitsJSON struct {
	PageSize  int `json:"page_size"`
	Favorites int `json:"favorites"`
}

type metaJSON struct {
	Today     string                       `json:"today"`
	Sports    []sportJSON                  `json:"sports"`
	Types     map[event.Sport][]typeJSON   `json:"types"`
	Buckets   map[event.Sport][]bucketJSON `json:"buckets"`
	Countries []countryJSON                `json:"countries"`
	Months    []optionJSON                 `json:"months"`
	Limits    limitsJSON                   `json:"limits"`
}

// enabledSports — включённые виды спорта в порядке справочника.
func enabledSports() []event.SportInfo {
	var out []event.SportInfo
	for _, sp := range event.Sports {
		if sp.Enabled {
			out = append(out, sp)
		}
	}
	return out
}

// dictJSON — справочники без счётчиков: sports (без count) и countries
// заполняет обработчик по данным базы.
func dictJSON(d bot.Dictionary, today time.Time) metaJSON {
	m := metaJSON{
		Today:   today.Format(time.DateOnly),
		Types:   map[event.Sport][]typeJSON{},
		Buckets: map[event.Sport][]bucketJSON{},
		Limits:  limitsJSON{PageSize: defaultLimit, Favorites: d.FavLimit},
	}
	for _, sp := range enabledSports() {
		m.Sports = append(m.Sports, sportJSON{Code: sp.Code, Title: sp.Title, Emoji: sp.Emoji})
		types := []typeJSON{}
		for _, t := range d.Types[sp.Code] {
			types = append(types, typeJSON{t, typeLabel(d, t)})
		}
		m.Types[sp.Code] = types
		buckets := []bucketJSON{}
		for i, b := range d.Buckets[sp.Code] {
			buckets = append(buckets, bucketJSON{i, b.Label, b.Lo, b.Hi})
		}
		m.Buckets[sp.Code] = buckets
	}
	for _, o := range d.Months(today) {
		m.Months = append(m.Months, optionJSON{o.Code, o.Label})
	}
	return m
}

// typeLabel — подпись дисциплины; неизвестный код — он сам.
func typeLabel(d bot.Dictionary, t event.Type) string {
	if l, ok := d.TypeLabels[t]; ok {
		return l
	}
	return string(t)
}

// name — значение из справочника названий; неизвестный код — он сам.
func name(names map[string]string, code string) string {
	if n, ok := names[code]; ok {
		return n
	}
	return code
}

type cityJSON struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func citiesJSON(in []store.CityCount) []cityJSON {
	out := make([]cityJSON, 0, len(in))
	for _, c := range in {
		out = append(out, cityJSON{c.Key, c.Name, c.N})
	}
	return out
}

type listItemJSON struct {
	ID          int64        `json:"id"`
	Title       string       `json:"title"`
	StartDate   string       `json:"start_date"`
	CountryCode string       `json:"country_code"`
	City        string       `json:"city"`
	Status      event.Status `json:"status"`
	Sport       event.Sport  `json:"sport"`
	Distances   []int        `json:"distances"`
}

type listJSON struct {
	Total int            `json:"total"`
	Page  int            `json:"page"`
	Pages int            `json:"pages"`
	Items []listItemJSON `json:"items"`
}

func itemsJSON(in []store.ListItem) []listItemJSON {
	out := make([]listItemJSON, 0, len(in))
	for _, it := range in {
		dist := it.Distances
		if dist == nil {
			dist = []int{}
		}
		out = append(out, listItemJSON{it.ID, it.Title, it.StartDate.Format(time.DateOnly), it.CountryCode, it.City, it.Status, it.Sport, dist})
	}
	return out
}

type distanceJSON struct {
	M     int    `json:"m"`
	Label string `json:"label,omitempty"`
}

type sourceJSON struct {
	Code string `json:"code"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type cardJSON struct {
	ID              int64          `json:"id"`
	Title           string         `json:"title"`
	Status          event.Status   `json:"status"`
	Sport           event.Sport    `json:"sport"`
	Type            event.Type     `json:"type"`
	TypeLabel       string         `json:"type_label"`
	StartDate       string         `json:"start_date"`
	EndDate         string         `json:"end_date,omitempty"`
	StartTime       string         `json:"start_time"`
	Timezone        string         `json:"timezone"`
	CountryCode     string         `json:"country_code"`
	CountryName     string         `json:"country_name"`
	City            string         `json:"city"`
	Address         string         `json:"address"`
	Description     string         `json:"description"`
	RegistrationURL string         `json:"registration_url,omitempty"`
	Distances       []distanceJSON `json:"distances"`
	Sources         []sourceJSON   `json:"sources"`
	IsFavorite      bool           `json:"is_favorite"`
}

// cardJSONOf — карточка события: те же поля, что у карточки бота
// (cardScreen); ссылки — только годные по event.ValidURL.
func cardJSONOf(d bot.Dictionary, e event.Event, srcs []store.EventSource, fav bool) cardJSON {
	c := cardJSON{
		ID: e.ID, Title: e.Title, Status: e.Status, Sport: event.SportOf(string(e.Sport)), Type: e.Type,
		TypeLabel: typeLabel(d, e.Type), StartDate: e.StartDate.Format(time.DateOnly),
		StartTime: e.StartTime, Timezone: e.Timezone, CountryCode: e.CountryCode,
		CountryName: name(d.CountryNames, e.CountryCode), City: e.City, Address: bot.VisibleAddress(e),
		Description: e.Description, Distances: []distanceJSON{}, Sources: []sourceJSON{}, IsFavorite: fav,
	}
	if e.EndDate != nil {
		c.EndDate = e.EndDate.Format(time.DateOnly)
	}
	if event.ValidURL(e.RegistrationURL) {
		c.RegistrationURL = e.RegistrationURL
	}
	for _, x := range e.Distances {
		c.Distances = append(c.Distances, distanceJSON{x.Meters, x.Label})
	}
	for _, s := range srcs {
		src := sourceJSON{Code: s.Code, Name: name(d.SourceNames, s.Code)}
		if event.ValidURL(s.URL) {
			src.URL = s.URL
		}
		c.Sources = append(c.Sources, src)
	}
	return c
}
