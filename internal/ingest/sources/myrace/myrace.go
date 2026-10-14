package myrace

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "myrace"
	// ListURL — общий календарь платформы. Фильтр country API не работает
	// (на 2026-10-14 отдаёт пустой список), страну отбирает каталог (D-01).
	ListURL = "https://myrace.info/api/1.0/calendar?limit=100"
	// CrawlDelay — пауза между страницами (D-08).
	CrawlDelay = time.Second
	// maxPages — предел страниц за прогон. Больше — ошибка, а не усечение:
	// обрезанный список пометил бы остальные события пропавшими.
	maxPages = 50
)

// Source — источник MyRace: страницы /calendar по курсору next_page_token.
type Source struct {
	f     *ingest.Fetcher
	url   string
	token string
	delay time.Duration
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f и токеном партнёра (MYRACE_TOKEN).
func New(f *ingest.Fetcher, token string) *Source {
	return &Source{f: f, url: ListURL, token: token, delay: CrawlDelay}
}

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страна только из API: событие без страны вне каталога (D-01).
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает все страницы календаря. Ошибка любой страницы или больше
// maxPages страниц — ошибка прогона: неполный набор не возвращается.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	var docs []ingest.RawDoc
	u := s.url
	for {
		if len(docs) == maxPages {
			return nil, fmt.Errorf("в календаре больше %d страниц", maxPages)
		}
		doc, err := s.f.GetAuth(ctx, u, s.delay, "Bearer "+s.token)
		if err != nil {
			return nil, fmt.Errorf("страница %d: %w", len(docs)+1, err)
		}
		docs = append(docs, doc)
		next, err := nextToken(doc.Body)
		if err != nil {
			return nil, fmt.Errorf("страница %d: %w", len(docs), err)
		}
		if next == "" {
			return docs, nil
		}
		u = s.url + "&page_token=" + url.QueryEscape(next)
	}
}

// Parse разбирает страницу календаря (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
