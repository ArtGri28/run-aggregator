package tristyle

import (
	"context"
	"fmt"
	"slices"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

const (
	// Code — код источника в sources.code.
	Code = "tristyle"
	// ListURL — календарь Tristyle.Клуба с фильтрами сайта «Бег» и «только
	// Беларусь»; на него же ссылается races.by. Страна и вид спорта записей
	// следуют из этих фильтров: страница события их не повторяет. Вид спорта
	// записи — по списку, в котором событие найдено.
	ListURL = "https://tristyle.club/competition/?q=&specialization=running&from=&to=&only_belarus=on"
	// SkiListURL — тот же календарь с фильтром «Лыжи»: события этого списка —
	// лыжные (лыжные гонки и лыжероллеры).
	SkiListURL = "https://tristyle.club/competition/?q=&specialization=skiing&from=&to=&only_belarus=on"
	// SwimListURL и CycleListURL — тот же календарь с фильтрами «Плавание» и
	// «Велосипед».
	SwimListURL  = "https://tristyle.club/competition/?q=&specialization=swimming&from=&to=&only_belarus=on"
	CycleListURL = "https://tristyle.club/competition/?q=&specialization=cycling&from=&to=&only_belarus=on"
	// CrawlDelay — robots.txt сайта не задаёт Crawl-delay; страницы событий
	// запрашиваются по одной с этой паузой.
	CrawlDelay = 5 * time.Second
	// maxEvents — предел страниц событий за прогон, на все списки вместе.
	// Больше — ошибка, а не усечение: обрезанный список пометил бы остальные
	// события пропавшими.
	maxEvents = 60
	// skiMark — фрагмент адреса документа лыжного события. На сервер фрагмент
	// не отправляется, а RawDoc.URL его сохраняет: так вид спорта доходит от
	// списка до Parse без состояния между Fetch и Parse.
	skiMark = string(event.SportSkiing)
)

// sportList — список календаря с одним небеговым видом спорта.
type sportList struct {
	sport event.Sport
	url   string
}

// Source — источник Tristyle.Клуб: GET ListURL, GET списков других видов
// спорта и по одному GET на каждое событие списков (города и полной даты в
// списке нет).
type Source struct {
	f *ingest.Fetcher
	// skiListURL пустой — лыжного списка нет.
	listURL, skiListURL, base string
	// more — списки плавания и велоспорта, после лыжного.
	more  []sportList
	delay time.Duration
}

var _ ingest.Source = (*Source)(nil)

// New создаёт источник с загрузчиком f.
func New(f *ingest.Fetcher) *Source {
	return &Source{f: f, listURL: ListURL, skiListURL: SkiListURL, base: eventBase, delay: CrawlDelay,
		more: []sportList{{event.SportSwimming, SwimListURL}, {event.SportCycling, CycleListURL}}}
}

// Code — код источника.
func (s *Source) Code() string { return Code }

// Policy — страну ставит Parse (список отфильтрован по Беларуси); запись
// без страны — вне каталога.
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay}
}

// Fetch загружает все списки и страницы всех их событий: документы списков,
// затем события бегового списка, затем лыжного, плавания и велоспорта.
// Страница события запрашивается один раз: номер из нескольких списков
// остаётся в первом из них. Документ небегового события помечен фрагментом —
// кодом вида спорта. Ошибка любой страницы — ошибка прогона: неполный набор не
// возвращается.
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	list, err := s.f.Get(ctx, s.listURL, s.delay)
	if err != nil {
		return nil, err
	}
	ids, err := listIDs(list.Body)
	if err != nil {
		return nil, err
	}
	docs := []ingest.RawDoc{list}
	lists := s.more
	if s.skiListURL != "" {
		lists = slices.Concat([]sportList{{event.SportSkiing, s.skiListURL}}, s.more)
	}
	marks := make([]string, len(ids))
	for _, l := range lists {
		sl, err := s.f.Get(ctx, l.url, s.delay)
		if err != nil {
			return nil, fmt.Errorf("список %s: %w", l.sport, err)
		}
		more, err := listIDs(sl.Body)
		if err != nil {
			return nil, fmt.Errorf("список %s: %w", l.sport, err)
		}
		docs = append(docs, sl)
		for _, id := range more {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
				marks = append(marks, string(l.sport))
			}
		}
	}
	if n := len(ids); n > maxEvents {
		return nil, fmt.Errorf("в списке %d событий — больше предела %d", n, maxEvents)
	}
	for i, id := range ids {
		u := s.base + id + "/"
		if marks[i] != "" {
			u += "#" + marks[i]
		}
		doc, err := s.f.Get(ctx, u, s.delay)
		if err != nil {
			return nil, fmt.Errorf("событие %s: %w", id, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// Parse разбирает страницу списка или события (см. функцию Parse).
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
