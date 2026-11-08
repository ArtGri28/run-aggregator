package probeg

import (
	"context"
	"fmt"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "probeg"
	// CalendarURL — календарь на год вперёд, первая страница; следующие —
	// по ссылке «Следующая страница» (?page=N).
	CalendarURL = "https://probeg.org/races/year_ahead/"
	// maxPages — предел страниц за прогон (в ноябре 2026 их 6).
	maxPages = 12
	// CrawlDelay — из robots.txt сайта (Crawl-delay: 30).
	CrawlDelay = 30 * time.Second
	// outOfScopeMax — порог доли out_of_scope этого источника: старты в
	// странах вне каталога. С ноября 2026 паркраны без города записей не
	// дают, и на образцах доля 0; порог прежний, с запасом.
	outOfScopeMax = 0.40
)

// Source — источник ПроБЕГ: страницы календаря по ссылке «Следующая страница».
type Source struct {
	f     *ingest.Fetcher
	url   string
	delay time.Duration // пауза между страницами; в тестах короче CrawlDelay
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: CalendarURL, delay: CrawlDelay} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — правила источника. Страну ставит Parse по строке места и
// справочнику городов РФ (D-01); без неё запись вне каталога (NoFallback).
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay, OutOfScopeMax: outOfScopeMax}
}

// Fetch загружает страницы календаря по ссылке «Следующая страница» с
// соблюдением Crawl-delay; страницы событий не запрашиваются. Больше
// maxPages страниц или ошибка любой страницы — ошибка прогона: неполный
// список пометил бы старты пропавшими.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	var docs []ingest.RawDoc
	for next := s.url; next != ""; {
		if len(docs) == maxPages {
			return nil, fmt.Errorf("в календаре больше %d страниц", maxPages)
		}
		doc, err := s.f.Get(ctx, next, s.delay)
		if err != nil {
			return nil, fmt.Errorf("страница %d: %w", len(docs)+1, err)
		}
		docs = append(docs, doc)
		if next, err = nextPage(doc.Body, doc.URL); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

// Parse разбирает страницу календаря (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
