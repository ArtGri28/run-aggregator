package tristyle

import (
	"context"
	"fmt"
	"time"

	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "tristyle"
	// ListURL — календарь Tristyle.Клуба с фильтрами сайта «Бег» и «только
	// Беларусь»; на него же ссылается races.by. Страна и вид спорта записей
	// следуют из этих фильтров: страница события их не повторяет.
	ListURL = "https://tristyle.club/competition/?q=&specialization=running&from=&to=&only_belarus=on"
	// CrawlDelay — robots.txt сайта не задаёт Crawl-delay; страницы событий
	// запрашиваются по одной с этой паузой.
	CrawlDelay = 5 * time.Second
	// maxEvents — предел страниц событий за прогон. Больше — ошибка, а не
	// усечение: обрезанный список пометил бы остальные события пропавшими.
	maxEvents = 60
)

// Source — источник Tristyle.Клуб: GET ListURL и по одному GET на каждое
// событие списка (города и полной даты в списке нет).
type Source struct {
	f             *ingest.Fetcher
	listURL, base string
	delay         time.Duration
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source {
	return &Source{f: f, listURL: ListURL, base: eventBase, delay: CrawlDelay}
}

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse (список отфильтрован по Беларуси); запись
// без страны — вне каталога.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает список и страницы всех его событий. Ошибка любой
// страницы — ошибка прогона: неполный набор не возвращается.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	list, err := s.f.Get(ctx, s.listURL, s.delay)
	if err != nil {
		return nil, err
	}
	ids, err := listIDs(list.Body)
	if err != nil {
		return nil, err
	}
	if len(ids) > maxEvents {
		return nil, fmt.Errorf("в списке %d событий — больше предела %d", len(ids), maxEvents)
	}
	docs := []ingest.RawDoc{list}
	for _, id := range ids {
		doc, err := s.f.Get(ctx, s.base+id+"/", s.delay)
		if err != nil {
			return nil, fmt.Errorf("событие %s: %w", id, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// Parse разбирает страницу списка или события (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
