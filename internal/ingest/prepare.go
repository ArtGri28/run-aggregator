package ingest

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/normalize"
)

// Причины отбраковки записи (ING-03); ключи Stats.Rejects.
const (
	reasonDuplicateID  = "дубликат external_id"
	reasonNoExternalID = "нет external_id"
	reasonNoTitle      = "нет названия"
	reasonBadDate      = "дата не разобрана"
	reasonDateRange    = "дата вне диапазона"
)

// maxRejectExamples — сколько external_id хранить примером на причину.
const maxRejectExamples = 3

// Prepared — валидная нормализованная запись, готовая к записи в store.
type Prepared struct {
	Source SourceEvent
	// Payload = json.Marshal(Source) — содержимое source_events.payload.
	Payload []byte
	Event   event.Event
	// InScope == false: запись сохраняется как out_of_scope, события нет.
	InScope bool
}

// ReasonStat — число записей, отбракованных по одной причине, и до
// трёх примеров их external_id.
type ReasonStat struct {
	Count    int
	Examples []string
}

// Stats — счётчики одного прогона Prepare.
type Stats struct {
	Parsed   int
	Valid    int
	Rejected int
	// Expired — прошедшие даты: вне каталога, но не rejected (D-11).
	Expired int
	// Collapsed — записи серии (равный непустой GroupKey), не ближайшие
	// по дате: в выход не попадают (D-03, DDP-05).
	Collapsed int `json:",omitempty"`
	// DateOutOfRange — даты позже сегодня+3 года (входят в Rejected).
	DateOutOfRange int
	OutOfScope     int
	// UnrecognizedRegion — область не из перечня субъектов РФ и не
	// иностранная (D-09); пары «область — город» — в Unrecognized.
	UnrecognizedRegion int
	// MultiRegion — записи с несколькими областями (D-14): region пуст,
	// но это не пустое поле и не нераспознанная область.
	MultiRegion int
	// DroppedDistances — дистанции больше normalize.MaxPlausibleDistanceM,
	// отброшенные из событий.
	DroppedDistances int
	Rejects          map[string]ReasonStat
	// Empty — число валидных записей с пустым city, region, distances.
	Empty        map[string]int
	Unrecognized map[string]int
}

// Prepare выполняет Validate и Normalize (MVP.md §5.2, §6) над всеми
// записями прогона. Чистая функция: время — только из now (в прогоне —
// момент старта). Порядок выхода совпадает с порядком входа.
//
// Серии (D-03, DDP-05): из записей с равным непустым GroupKey остаётся
// одна — с ближайшей датой не раньше today−1, при равенстве первая по
// входу; остальные считаются в Collapsed. Записи без GroupKey не
// сворачиваются никогда. Parsed = Valid + Rejected + Expired + Collapsed.
func Prepare(recs []SourceEvent, now time.Time, pol Policy) ([]Prepared, Stats) {
	st := Stats{Parsed: len(recs), Rejects: map[string]ReasonStat{}, Empty: map[string]int{}, Unrecognized: map[string]int{}}
	y, m, d := now.UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	minDate, maxDate := today.AddDate(0, 0, -1), today.AddDate(3, 0, 0)

	reject := func(rec SourceEvent, reason string) {
		r := st.Rejects[reason]
		r.Count++
		if rec.ExternalID != "" && len(r.Examples) < maxRejectExamples {
			r.Examples = append(r.Examples, rec.ExternalID)
		}
		st.Rejects[reason] = r
		st.Rejected++
	}

	// Победитель серии: индекс записи с ближайшей датой среди записей,
	// которые пройдут проверки цикла ниже (те же check и дубль id).
	winner := map[string]int{}
	winDate := map[string]time.Time{}
	seen := make(map[string]bool, len(recs))
	for i, rec := range recs {
		dup := rec.ExternalID != "" && seen[rec.ExternalID]
		seen[rec.ExternalID] = true
		if dup || rec.GroupKey == "" {
			continue
		}
		date, reason, expired := check(rec, now, minDate, maxDate)
		if reason != "" || expired {
			continue
		}
		if d, ok := winDate[rec.GroupKey]; !ok || date.Before(d) {
			winner[rec.GroupKey], winDate[rec.GroupKey] = i, date
		}
	}

	clear(seen)
	out := make([]Prepared, 0, len(recs))
	for i, rec := range recs {
		if rec.ExternalID != "" && seen[rec.ExternalID] {
			reject(rec, reasonDuplicateID)
			continue
		}
		seen[rec.ExternalID] = true
		date, reason, expired := check(rec, now, minDate, maxDate)
		switch {
		case expired:
			st.Expired++
			continue
		case reason != "":
			if reason == reasonDateRange {
				st.DateOutOfRange++
			}
			reject(rec, reason)
			continue
		}
		// Не ближайшая запись серии — до подсчёта city/region/distances/
		// country, чтобы счётчики считали только оставшиеся записи.
		if rec.GroupKey != "" && winner[rec.GroupKey] != i {
			st.Collapsed++
			continue
		}
		title := strings.TrimSpace(rec.Title)
		// Дата окончания: неразборчивая или не позже старта — без неё,
		// запись не отбраковывается.
		var endDate *time.Time
		if end, ok := normalize.Date(rec.EndDate, now); ok && end.After(date) {
			endDate = &end
		}

		city := normalize.City(rec.City)
		cityKey := normalize.CityKey(city)
		if city == "" {
			st.Empty["city"]++
		}
		// Область (D-08, D-09, D-14). Иностранная — вне каталога при любой
		// политике; нераспознанная считается независимо от политики.
		region := normalize.Region(rec.Region)
		foreign := false
		switch {
		case len(rec.RegionSlugs) > 1:
			st.MultiRegion++
		case region == "":
			st.Empty["region"]++
		default:
			canonical, kind := normalize.ClassifyPlace(region, city)
			switch kind {
			case normalize.RegionRF:
				region = canonical
			case normalize.RegionForeign:
				foreign = true
			default:
				st.UnrecognizedRegion++
				st.Unrecognized[region+" — "+city]++
			}
		}
		// LO-06: дистанции только из столбца дистанций, при пустом — из
		// названия; описание не используется никогда.
		distSrc := rec.Distances
		if strings.TrimSpace(distSrc) == "" {
			distSrc = title
		}
		var dists []event.Distance
		for _, meters := range normalize.Distances(distSrc) {
			if meters > normalize.MaxPlausibleDistanceM {
				st.DroppedDistances++
				continue
			}
			dists = append(dists, event.Distance{Meters: meters})
		}
		if len(dists) == 0 {
			st.Empty["distances"]++
		}
		code, inScope := normalize.Country(rec.Country, pol.CountryFallback)
		if foreign {
			code, inScope = "", false
		}
		if !inScope {
			st.OutOfScope++
		}
		// Marshal структуры из строк и срезов строк не возвращает ошибку.
		payload, _ := json.Marshal(rec)

		out = append(out, Prepared{
			Source:  rec,
			Payload: payload,
			InScope: inScope,
			Event: event.Event{
				Title:           title,
				TitleNorm:       normalize.TitleNorm(title),
				StartDate:       date,
				EndDate:         endDate,
				CountryCode:     code,
				Region:          region,
				City:            city,
				CityKey:         cityKey,
				Timezone:        normalize.Timezone(cityKey, region),
				Type:            normalize.Type(rec.TypeLabel),
				Status:          event.StatusScheduled,
				Description:     normalize.Description(rec.Description),
				RegistrationURL: rec.RegistrationURL,
				Distances:       dists,
			},
		})
	}
	st.Valid = len(out)
	return out, st
}

// check — проверки записи в Prepare, кроме дубля external_id: дата и
// причина отбраковки ("" — запись валидна); expired — прошедшая дата
// (вне каталога, но не отбраковка).
func check(rec SourceEvent, now, minDate, maxDate time.Time) (date time.Time, reason string, expired bool) {
	date, dateOK := normalize.Date(rec.Date, now)
	switch {
	case rec.ExternalID == "":
		return date, reasonNoExternalID, false
	case strings.TrimSpace(rec.Title) == "":
		return date, reasonNoTitle, false
	case !dateOK:
		return date, reasonBadDate, false
	case date.Before(minDate):
		return date, "", true
	case date.After(maxDate):
		return date, reasonDateRange, false
	}
	return date, "", false
}

// Metrics — метрики прогона для ingest_runs.metrics: счётчики и доли
// пустых ключевых полей от valid (4 знака; при valid = 0 — нули). Доли
// нужны проверке роста пустых полей относительно базы (MVP.md §5.4).
func (s Stats) Metrics() []byte {
	type empty struct {
		City      float64 `json:"city"`
		Region    float64 `json:"region"`
		Distances float64 `json:"distances"`
	}
	m := struct {
		Valid              int   `json:"valid"`
		Expired            int   `json:"expired"`
		DateOutOfRange     int   `json:"date_out_of_range"`
		OutOfScope         int   `json:"out_of_scope"`
		UnrecognizedRegion int   `json:"unrecognized_region"`
		MultiRegion        int   `json:"multi_region"`
		DroppedDistances   int   `json:"dropped_distances"`
		Empty              empty `json:"empty"`
		Collapsed          int   `json:"collapsed,omitempty"`
	}{s.Valid, s.Expired, s.DateOutOfRange, s.OutOfScope, s.UnrecognizedRegion, s.MultiRegion, s.DroppedDistances,
		empty{s.emptyShare("city"), s.emptyShare("region"), s.emptyShare("distances")}, s.Collapsed}
	// Marshal структуры из чисел не возвращает ошибку.
	b, _ := json.Marshal(m)
	return b
}

// emptyShare — доля валидных записей с пустым полем, 4 знака.
func (s Stats) emptyShare(field string) float64 {
	if s.Valid == 0 {
		return 0
	}
	return math.Round(float64(s.Empty[field])/float64(s.Valid)*1e4) / 1e4
}
