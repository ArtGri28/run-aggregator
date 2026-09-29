package ingest

import (
	"encoding/json"
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
	// DateOutOfRange — даты позже сегодня+3 года (входят в Rejected).
	DateOutOfRange int
	OutOfScope     int
	Rejects        map[string]ReasonStat
}

// Prepare выполняет Validate и Normalize (MVP.md §5.2, §6) над всеми
// записями прогона. Чистая функция: время — только из now (в прогоне —
// момент старта). Порядок выхода совпадает с порядком входа.
func Prepare(recs []SourceEvent, now time.Time, pol Policy) ([]Prepared, Stats) {
	st := Stats{Parsed: len(recs), Rejects: map[string]ReasonStat{}}
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

	seen := make(map[string]bool, len(recs))
	out := make([]Prepared, 0, len(recs))
	for _, rec := range recs {
		if rec.ExternalID != "" && seen[rec.ExternalID] {
			reject(rec, reasonDuplicateID)
			continue
		}
		seen[rec.ExternalID] = true
		title := strings.TrimSpace(rec.Title)
		date, dateOK := normalize.Date(rec.Date, now)
		switch {
		case rec.ExternalID == "":
			reject(rec, reasonNoExternalID)
			continue
		case title == "":
			reject(rec, reasonNoTitle)
			continue
		case !dateOK:
			reject(rec, reasonBadDate)
			continue
		case date.Before(minDate):
			st.Expired++
			continue
		case date.After(maxDate):
			st.DateOutOfRange++
			reject(rec, reasonDateRange)
			continue
		}

		city := normalize.City(rec.City)
		cityKey := normalize.CityKey(city)
		region := normalize.Region(rec.Region)
		// LO-06: дистанции только из столбца дистанций, при пустом — из
		// названия; описание не используется никогда.
		distSrc := rec.Distances
		if strings.TrimSpace(distSrc) == "" {
			distSrc = title
		}
		var dists []event.Distance
		for _, meters := range normalize.Distances(distSrc) {
			dists = append(dists, event.Distance{Meters: meters})
		}
		code, inScope := normalize.Country(rec.Country, pol.CountryFallback)
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
