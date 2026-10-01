package heroleague

import (
	"context"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "heroleague"
	// ListURL — единственный запрос прогона: открытый JSON-API сайта,
	// тот же, что использует его страница календаря.
	ListURL = "https://heroleague.ru/api/event/list"
	// CrawlDelay — robots.txt сайта не задаёт Crawl-delay; запрос один за прогон.
	CrawlDelay = 10 * time.Second
)

// Source — источник «Лига Героев»: один GET ListURL за прогон.
type Source struct {
	f   *ingest.Fetcher
	url string
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: ListURL} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — все подключённые события российские и страну несут явно, fallback
// RU нужен только как страховка для записи без страны.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.RUOnlyFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает список событий.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает ответ API (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
