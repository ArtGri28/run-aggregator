package marathonec

import (
	"context"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "marathonec"
	// CalendarURL — единственная страница прогона (URL без «?»,
	// 01-SOURCES-REPORT §2.1); она же URL события для внешних ссылок (D-13).
	CalendarURL = "https://marathonec.ru/calendar-beg/"
	// CrawlDelay — из robots.txt сайта (01-SOURCES-REPORT §2.1 п. 2).
	CrawlDelay = 40 * time.Second
)

// Source — источник Марафонец: один GET CalendarURL за прогон.
type Source struct {
	f   *ingest.Fetcher
	url string
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: CalendarURL} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — правила источника. D-07: fallback RU выключен (NoFallback) до
// решения владельца, записанного в
// .planning/phases/03-first-source/03-SANITY-REPORT.md; меняется только
// планом 03-07 по этому решению.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает страницу календаря с соблюдением Crawl-delay.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает страницу календаря (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
