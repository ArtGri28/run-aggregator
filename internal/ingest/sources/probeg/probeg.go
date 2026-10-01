package probeg

import (
	"context"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "probeg"
	// CalendarURL — общий календарь на год вперёд: единственная страница
	// прогона (D-13, горизонт D-02).
	CalendarURL = "https://probeg.org/races/year_ahead/"
	// CrawlDelay — из robots.txt сайта (Crawl-delay: 30).
	CrawlDelay = 30 * time.Second
	// outOfScopeMax — порог доли out_of_scope этого источника: серии без
	// города (D-14) и иностранные строки дают вне каталога по построению.
	// На фикстуре 2026-09-30 доля 28,0 % (155 из 553 valid), с запасом
	// 0,05 — 0,33, поэтому 0,40; TestPolicyGate держит значение в
	// [доля + 0,05; 0,50]. 05-08 сверяет с первым реальным прогоном.
	outOfScopeMax = 0.40
)

// Source — источник ПроБЕГ: один GET CalendarURL за прогон.
type Source struct {
	f   *ingest.Fetcher
	url string
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: CalendarURL} }

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — правила источника. Страну ставит Parse по строке места и
// справочнику городов РФ (D-01); без неё запись вне каталога (NoFallback).
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay, OutOfScopeMax: outOfScopeMax}
}

// Fetch загружает календарь на год вперёд одним GET с соблюдением
// Crawl-delay; страницы событий не запрашиваются (D-01, D-13).
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil {
		return nil, err
	}
	return []ingest.RawDoc{doc}, nil
}

// Parse разбирает страницу календаря (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
