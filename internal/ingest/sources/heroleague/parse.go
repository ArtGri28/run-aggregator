// Package heroleague — источник «Лига Героев» (heroleague.ru), только гонки
// с препятствиями. Один GET /api/event/list: проекты (event) с запусками по
// городам (event_city). Форматы участия (массовый, команда, Pro) — варианты
// одного старта и в событиях не участвуют; дистанций API не даёт.
package heroleague

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // зона из API нужна Parse независимо от образа

	"aggregator/internal/ingest"
)

const (
	eventURLPrefix = "https://heroleague.ru/event/"
	// maxURLLen — предел длины ссылки из источника.
	maxURLLen = 2048
	// kindCorp — закрытый корпоративный старт: не для публики.
	kindCorp = "corp"
	typeOCR  = "obstacle"
)

// ocrTypes — event_type.public_id проектов, которые подключаются (все — гонки
// с препятствиями). Остальные проекты Лиги (забег, марафоны, лагеря) —
// обычный бег или не соревнования и в каталог не идут.
var ocrTypes = map[string]bool{"gonka": true, "urban": true, "mudrun": true}

type listResponse struct {
	Values *[]project `json:"values"`
}

type project struct {
	PublicID  string `json:"public_id"`
	Title     string `json:"title"`
	EventType struct {
		PublicID string `json:"public_id"`
	} `json:"event_type"`
	Cities []launch `json:"event_city"`
}

type launch struct {
	PublicID  string `json:"public_id"`
	Address   string `json:"address"`
	StartTime string `json:"start_time"`
	Timezone  string `json:"timezone"`
	Kind      string `json:"kind"`
	// Public == false — запуск скрыт из публичного календаря сайта (его
	// /api/event_city/filtered такие не отдаёт); отсутствие поля — не скрыт.
	Public      *bool  `json:"public"`
	ExternalURL string `json:"external_url"`
	City        struct {
		Name string `json:"name_ru"`
	} `json:"city"`
}

// Parse разбирает ответ /api/event/list. Чистая функция: без сети, файлов и
// часов. Каждый запуск event_city OCR-проекта даёт одну запись; external_id —
// event_city.public_id. Неразборчивое время или зона дают пустую дату (запись
// отбракует ingest.Prepare), а не выдуманную. Ответ без списка values —
// ingest.ErrStructureNotFound.
func Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	var r listResponse
	if err := json.Unmarshal(doc.Body, &r); err != nil {
		return nil, fmt.Errorf("разбор JSON: %w", err)
	}
	if r.Values == nil {
		return nil, fmt.Errorf("%w: в ответе нет списка values", ingest.ErrStructureNotFound)
	}
	var out []ingest.SourceEvent
	for _, p := range *r.Values {
		if !ocrTypes[p.EventType.PublicID] {
			continue
		}
		for _, c := range p.Cities {
			if c.Kind == kindCorp || (c.Public != nil && !*c.Public) {
				continue
			}
			city := strings.TrimSpace(c.City.Name)
			out = append(out, ingest.SourceEvent{
				ExternalID:      c.PublicID,
				URL:             eventURLPrefix + p.PublicID,
				Title:           strings.TrimSpace(p.Title),
				Date:            localDate(c.StartTime, c.Timezone),
				City:            city,
				Country:         "RU",
				TypeLabel:       typeOCR,
				RegistrationURL: httpURL(c.ExternalURL),
				Address:         address(c.Address, city),
				Timezone:        validZone(c.Timezone),
			})
		}
	}
	return out, nil
}

// localDate — дата старта в зоне площадки: start_time в API — UTC без
// суффикса, поэтому ранний старт на востоке попадает на следующие сутки.
// Пустая строка: время или зона не разобраны.
func localDate(start, zone string) string {
	loc, err := time.LoadLocation(zone)
	if zone == "" || err != nil {
		return ""
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(start), time.UTC)
	if err != nil {
		return ""
	}
	return t.In(loc).Format("2006-01-02")
}

func validZone(zone string) string {
	if _, err := time.LoadLocation(zone); err != nil || zone == "" {
		return ""
	}
	return zone
}

// address — место сверх города: адрес без ведущего «<город>, »; адрес,
// совпадающий с городом, — пустой.
func address(addr, city string) string {
	addr = strings.Join(strings.Fields(addr), " ")
	if strings.EqualFold(addr, city) {
		return ""
	}
	if len(addr) > len(city)+1 && strings.EqualFold(addr[:len(city)], city) && addr[len(city)] == ',' {
		return strings.TrimSpace(addr[len(city)+1:])
	}
	return addr
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
