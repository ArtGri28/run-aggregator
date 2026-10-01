// Package ingest собирает данные источников по конвейеру MVP.md §5:
// Fetch → Parse → Validate → Normalize → store. Код, зависящий от
// источника, — только Fetch и Parse (internal/ingest/sources/<name>);
// нормализация — в internal/normalize.
package ingest

import (
	"context"
	"errors"
	"time"

	"aggregator/internal/normalize"
)

// ErrStructureNotFound — страница не содержит ожидаемой структуры
// (таблицы, строк, столбца). Решение принимается только по структуре,
// а не по словам на странице (D-02); прогон получает статус failed.
var ErrStructureNotFound = errors.New("структура не найдена")

// SourceEvent — одна запись источника в «сыром» виде: строки как на
// странице, без нормализации. Payload в source_events = json.Marshal
// этой структуры; порядок полей фиксирован, поэтому content_hash
// детерминирован. URL входит в payload: смена URL меняет hash (LO-01).
type SourceEvent struct {
	ExternalID string `json:"external_id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	// Date — дата как в источнике (у Марафонца dd/mm/yyyy).
	Date      string `json:"date"`
	Distances string `json:"distances"`
	City      string `json:"city"`
	// Region — название области; "", если у записи несколько областей
	// (D-14: такая запись считается отдельно).
	Region string `json:"region"`
	// RegionSlugs и Regions — все области записи в порядке источника:
	// слаги и их названия (название = слаг, если источник его не дал).
	// Больше одного элемента — запись с несколькими областями (D-14).
	RegionSlugs     []string `json:"region_slugs,omitempty"`
	Regions         []string `json:"regions,omitempty"`
	Country         string   `json:"country,omitempty"`
	TypeLabel       string   `json:"type"`
	RegistrationURL string   `json:"registration_url,omitempty"`
	Description     string   `json:"description,omitempty"`
	// EndDate — дата окончания в формате, понятном normalize.Date;
	// "" — однодневное событие. Новые поля стоят в конце и с omitempty:
	// payload записей без них (и content_hash) не меняется.
	EndDate string `json:"end_date,omitempty"`
	// GroupKey — ключ серии: записи с равным непустым ключом в одном
	// прогоне сворачиваются в Prepare до одной — единственная разрешённая
	// склейка внутри источника (DDP-05).
	GroupKey string `json:"group_key,omitempty"`
	// Address — уточнение места сверх города и области (район и т. п.);
	// попадает в events.address как есть.
	Address string `json:"address,omitempty"`
	// Timezone — IANA-зона площадки, если источник её сообщает; используется,
	// когда зону не определяет справочник городов.
	Timezone string `json:"timezone,omitempty"`
}

// RawDoc — один загруженный документ источника.
type RawDoc struct {
	URL       string
	Body      []byte
	FetchedAt time.Time
}

// Policy — правила источника, не зависящие от конкретной записи.
type Policy struct {
	// CountryFallback — страна для записей без явной страны (MVP.md §6).
	CountryFallback normalize.CountryPolicy
	// CrawlDelay — минимальный интервал между запросами к хосту.
	CrawlDelay time.Duration
	// OutOfScopeMax — доля out_of_scope для Evaluate этого источника;
	// 0 — общий порог Thresholds.
	OutOfScopeMax float64
}

// Source — контракт источника (MVP.md §5). Fetcher источник получает
// в конструкторе, поэтому контракт от него не зависит, а тесты
// реализуют Source без сети.
type Source interface {
	Code() string
	Policy() Policy
	Fetch(ctx context.Context) ([]RawDoc, error)
	// Parse — чистая функция: без сети, файлов и часов. Каждая найденная
	// строка источника даёт ровно одну запись; отбраковка — в Prepare.
	Parse(doc RawDoc) ([]SourceEvent, error)
}
