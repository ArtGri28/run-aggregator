// Package myrace — источник MyRace (myrace.info) по партнёрскому API:
// GET /api/1.0/calendar с токеном владельца. Сайт не читается: robots.txt
// его закрывает, разрешение дано только на API (Phase 17, письмо техлида
// 2026-10-13). Одна запись на событие; дистанции — из названий активностей.
package myrace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
)

// maxURLLen — предел длины ссылки из источника.
const maxURLLen = 2048

// runSports — виды спорта MyRace, которые идут в каталог (D-02), и подпись
// типа для normalize.Type (D-03): "" — тип other.
var runSports = map[string]string{
	"running":                "",
	"trail-running":          "trail",
	"sky-running":            "trail",
	"skysnow":                "trail",
	"running-with-obstacles": "obstacle",
}

// cyclingCode — код велоспорта в API.
const cyclingCode = "cycling"

type page struct {
	Events *[]calEvent `json:"events"`
	Next   string      `json:"next_page_token"`
}

type sport struct {
	Code string `json:"code"`
}

type calEvent struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Sports    []sport `json:"sports"`
	StartDate string  `json:"start_date"`
	EndDate   string  `json:"end_date"`
	Location  *struct {
		Country *string `json:"country"`
		City    *string `json:"city"`
	} `json:"location"`
	URL        string `json:"url"`
	Activities []struct {
		Name  string `json:"name"`
		Sport *sport `json:"sport"`
	} `json:"activities"`
}

// Parse разбирает одну страницу /calendar. Чистая функция: без сети, файлов и
// часов. Событие с беговым видом спорта — беговое (D-02 фазы 17); событие
// только велоспорта — велоспорт: дистанции из его активностей, подпись
// дисциплины — название события и активностей (normalize.SportType). Прочие
// события (плавание, мультиспорт и т. п.) пропускаются — так же, как фильтр
// «Бег» в адресе списка других источников. Ответ без массива events —
// ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	p, err := decode(doc.Body)
	if err != nil {
		return nil, err
	}
	var out []ingest.SourceEvent
	for _, e := range *p.Events {
		label, ok := typeLabel(e.Sports)
		cycle := !ok && onlyCycling(e.Sports)
		if !ok && !cycle {
			continue
		}
		var dists []string
		for _, a := range e.Activities {
			if _, run := runSports[code(a.Sport)]; run || a.Sport == nil || (cycle && code(a.Sport) == cyclingCode) {
				dists = append(dists, strings.Join(strings.Fields(a.Name), " "))
			}
		}
		rec := ingest.SourceEvent{
			ExternalID:      e.ID,
			URL:             httpURL(e.URL),
			Title:           strings.Join(strings.Fields(e.Name), " "),
			Date:            e.StartDate,
			Distances:       strings.Join(dists, ", "),
			TypeLabel:       label,
			RegistrationURL: httpURL(e.URL),
		}
		if cycle {
			rec.Sport = string(event.SportCycling)
			rec.TypeLabel = rec.Title + ", " + rec.Distances
		}
		if e.EndDate != e.StartDate {
			rec.EndDate = e.EndDate
		}
		if l := e.Location; l != nil {
			rec.City = strings.TrimSpace(deref(l.City))
			rec.Country = strings.ToUpper(strings.TrimSpace(deref(l.Country)))
		}
		out = append(out, rec)
	}
	return out, nil
}

// nextToken — курсор следующей страницы; "" — страница последняя.
func nextToken(body []byte) (string, error) {
	p, err := decode(body)
	if err != nil {
		return "", err
	}
	return p.Next, nil
}

func decode(body []byte) (page, error) {
	var p page
	if err := json.Unmarshal(body, &p); err != nil {
		return p, fmt.Errorf("разбор JSON: %w", err)
	}
	if p.Events == nil {
		return p, fmt.Errorf("%w: в ответе нет массива events", ingest.ErrStructureNotFound)
	}
	return p, nil
}

// typeLabel — подпись типа по видам спорта события: obstacle важнее trail,
// trail важнее running. ok == false — бегового вида нет.
func typeLabel(sports []sport) (string, bool) {
	label, ok := "", false
	for _, s := range sports {
		l, run := runSports[s.Code]
		if !run {
			continue
		}
		ok = true
		if l == "obstacle" || (l == "trail" && label == "") {
			label = l
		}
	}
	return label, ok
}

// onlyCycling — у события есть виды спорта, и все они — велоспорт.
func onlyCycling(sports []sport) bool {
	for _, s := range sports {
		if s.Code != cyclingCode {
			return false
		}
	}
	return len(sports) > 0
}

func code(s *sport) string {
	if s == nil {
		return ""
	}
	return s.Code
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// httpURL — ссылка из API, если это корректный абсолютный http(s)-адрес.
func httpURL(s string) string {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if s == "" || len(s) > maxURLLen || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return s
}
