package bot

import (
	"cmp"
	"context"
	"fmt"
	"html"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/normalize"
	"aggregator/internal/store"
)

const (
	maxSearchWords = 6 // слов запроса, которые участвуют в поиске
	// ponytail: поиск читает весь будущий каталог страны и сравнивает слова в
	// памяти; при каталоге больше этого предела — индекс в базе (pg_trgm).
	maxSearchCandidates = 5000
	queryPrefix         = "Запрос «"
)

// suffixes — окончания, которые отбрасывает stem; длинные раньше коротких.
var suffixes = []string{"иями", "ями", "ами", "ого", "его", "ому", "ему", "ыми", "ими", "ией",
	"ий", "ый", "ой", "ая", "яя", "ое", "ее", "ые", "ие", "ых", "их", "ов", "ев", "ей", "ом", "ем",
	"ам", "ям", "ах", "ях", "ую", "юю", "ья", "ье", "ьи", "ью",
	"а", "я", "о", "е", "ы", "и", "у", "ю", "ь", "й"}

// stem — основа слова для сравнения: без окончания (до двух отбрасываний:
// «казачьей» → «казачь» → «казач»), не короче трёх букв. Слово — строчными,
// «ё» уже заменена на «е». Это не словарь: разные формы слова дают одну основу,
// но не всегда.
func stem(w string) string {
	for range 2 {
		n := utf8.RuneCountInString(w)
		i := slices.IndexFunc(suffixes, func(s string) bool {
			return strings.HasSuffix(w, s) && n-utf8.RuneCountInString(s) >= 3
		})
		if i < 0 {
			break
		}
		w = strings.TrimSuffix(w, suffixes[i])
	}
	return w
}

// stems — основы слов текста: строчными, «ё» как «е», без знаков, без
// повторов; слово короче двух символов не участвует.
func stems(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(text), "ё", "е"), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if w = stem(w); utf8.RuneCountInString(w) >= 2 && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// searchWords — основы слов запроса, не больше maxSearchWords.
func searchWords(text string) []string {
	w := stems(text)
	return w[:min(len(w), maxSearchWords)]
}

// editDistance — расстояние Дамерау — Левенштейна между a и b: вставка,
// удаление, замена и перестановка соседних букв стоят 1.
func editDistance(a, b []rune) int {
	prev2, prev, cur := make([]int, len(b)+1), make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}

// wordScore — насколько слово запроса q подходит слову события w (основы):
// 3 — совпадает, 2 — начало слова (от 5 букв — любая часть), 1 — отличается опечаткой (одна ошибка
// при основе от 6 букв, две — от 10; сравнивается и с началом более длинного
// слова), 0 — не подходит. В коротких основах опечатка не прощается: иначе
// «казач» совпадало бы с «казан».
func wordScore(q, w string) int {
	qr, wr := []rune(q), []rune(w)
	switch {
	case q == w:
		return 3
	// Короткая основа — только начало слова: иначе «слав» находило бы «Переславль».
	case strings.HasPrefix(w, q), len(qr) >= 5 && strings.Contains(w, q):
		return 2
	}
	typos := 0
	switch {
	case len(qr) >= 10:
		typos = 2
	case len(qr) >= 6:
		typos = 1
	}
	if typos == 0 {
		return 0
	}
	if editDistance(qr, wr) <= typos || (len(wr) > len(qr) && editDistance(qr, wr[:len(qr)]) <= typos) {
		return 1
	}
	return 0
}

// matchEvents — события из items, подходящие запросу words, лучшие первыми:
// сначала те, где нашлись все слова; если таких нет — где нашлось хотя бы
// одно (больше слов — выше). При равенстве — точные совпадения выше опечаток,
// затем прежний порядок items (дата, id). Слова события — из названия и города.
func matchEvents(items []store.ListItem, words []string) []store.ListItem {
	type hit struct {
		it             store.ListItem
		matched, score int
	}
	var hits []hit
	full := false
	for _, it := range items {
		h := hit{it: it}
		ews := stems(it.Title + " " + it.City)
		for _, q := range words {
			best := 0
			for _, w := range ews {
				best = max(best, wordScore(q, w))
			}
			if best > 0 {
				h.matched++
				h.score += best
			}
		}
		if h.matched > 0 {
			hits = append(hits, h)
			full = full || h.matched == len(words)
		}
	}
	if full {
		hits = slices.DeleteFunc(hits, func(h hit) bool { return h.matched < len(words) })
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(b.matched, a.matched), cmp.Compare(b.score, a.score))
	})
	out := make([]store.ListItem, len(hits))
	for i, h := range hits {
		out[i] = h.it
	}
	return out
}

// searchResults — ответ на текстовый запрос, не совпавший с названием города:
// события по словам запроса (matchEvents) и подсказки городов. Поиск идёт по
// всем будущим событиям страны фильтров (при «всех странах» — по всем),
// остальные фильтры не учитывает и ничего не меняет.
func (b *Bot) searchResults(ctx context.Context, uf userFilters, text string, v int) (screen, error) {
	all := uf.Country == allCountries
	// Кнопка выбора города; при «всех странах» город недоступен — выбор страны.
	pick := btn("📍 Выбрать из списка", callback{Kind: cbCity})
	if all {
		pick = btn("🌍 Страна", callback{Kind: cbCountry})
	}
	words := searchWords(text)
	if len(words) == 0 {
		return screen{text: "Не понял запрос — напишите название забега или города", plain: true, kb: keyboard(pick, btn(backLabel, back))}, nil
	}
	today, country := b.today(), uf.countryCode()
	items, err := b.Store.SearchCandidates(ctx, country, today, maxSearchCandidates)
	if err != nil {
		return screen{}, err
	}
	found := matchEvents(items, words)
	var cities []store.CityCount
	if !all {
		key := normalize.CityKey(text)
		if !validCityKey(key) {
			key = ""
		}
		if cities, err = b.Store.SimilarCities(ctx, country, key, text, today, 5); err != nil {
			return screen{}, err
		}
	}
	b.logger().Info("text", "city", false, "events", len(found), "cities", len(cities))
	return searchScreen(text, found[:min(len(found), pageSize)], len(found), cities, v, all, today.Year(), pick), nil
}

// searchScreen — результаты запроса: подсказки городов (кнопка выбирает город
// фильтром) и найденные события (кнопка открывает карточку с возвратом к этим
// результатам); ничего не найдено — предложение открыть каталог. Первая
// строка «Запрос «…»» — по ней карточка узнаёт запрос (queryOf). pick —
// кнопка выбора города или страны; v — версия фильтров для кнопок.
func searchScreen(query string, items []store.ListItem, total int, cities []store.CityCount, v int, flags bool, year int, pick row) screen {
	q := "«" + html.EscapeString(query) + "»"
	if total == 0 && len(cities) == 0 {
		return screen{text: "По запросу " + q + " ничего не нашлось.\nПопробуйте другое слово или откройте каталог.",
			kb: keyboard(btn("📋 Открыть каталог", callback{Kind: cbPage, Page: 1, V: v}), pick, btn(backLabel, back))}
	}
	var lines []string
	var rows []row
	if len(cities) > 0 {
		lines = append(lines, fmt.Sprintf("📍 Города по запросу: %d", len(cities)))
		for _, c := range cities {
			rows = append(rows, btn(fmt.Sprintf("📍 %s (%d)", c.Name, c.N), callback{Kind: cbCity, Value: c.Key}))
		}
	}
	if total > 0 {
		found := fmt.Sprintf("🔎 <b>Забеги по запросу: %d</b>", total)
		if total > len(items) {
			found += fmt.Sprintf("\nПоказаны первые %d — уточните запрос", len(items))
		}
		lines = append(lines, found)
		for _, it := range items {
			rows = append(rows, eventBtn(it, callback{Kind: cbSearchCard, ID: it.ID, V: v}, flags, year))
		}
	}
	return screen{text: "Запрос " + q + "\n\n" + strings.Join(lines, "\n"), kb: keyboard(append(rows, pick, btn(backLabel, back))...)}
}

// queryOf — запрос из строки сообщения бота вида «…Запрос «текст»»; "" —
// строка не такая. Запрос нигде не хранится: его несёт само сообщение —
// первая строка результатов поиска и последняя строка карточки из поиска.
func queryOf(line string) string {
	_, q, ok := strings.Cut(line, queryPrefix)
	if !ok || !strings.HasSuffix(q, "»") {
		return ""
	}
	q = strings.TrimSpace(strings.TrimSuffix(q, "»"))
	if utf8.RuneCountInString(q) > maxCityInput {
		return ""
	}
	return q
}

// messageLines — первая и последняя строки текста сообщения с кнопкой; для
// недоступного (старого) сообщения — пустые.
func messageLines(q *models.CallbackQuery) (first, last string) {
	if q.Message.Message == nil {
		return "", ""
	}
	lines := strings.Split(q.Message.Message.Text, "\n")
	return lines[0], lines[len(lines)-1]
}

// onSearch обрабатывает g:*: карточку события из результатов поиска и возврат
// к этим результатам. Запрос берётся из текста сообщения; если его там нет
// (сообщение недоступно) — карточка возвращает в каталог, а возврат открывает
// первую страницу каталога.
func (b *Bot) onSearch(ctx context.Context, q *models.CallbackQuery, c callback) string {
	first, last := messageLines(q)
	if c.Kind == cbSearchCard {
		sc, v, ok := b.buildCard(ctx, q.From.ID, callback{Kind: cbCard, ID: c.ID, Page: 1, V: c.V})
		if !ok {
			return errNotice
		}
		if query := queryOf(first); query != "" {
			sc.text += "\n\n🔎 " + queryPrefix + html.EscapeString(query) + "»"
			rows := sc.kb.InlineKeyboard
			rows[len(rows)-1] = btn(backLabel, callback{Kind: cbSearchBack, V: v})
		}
		if err := b.show(ctx, q, sc); err != nil {
			b.logger().Warn("show card", "err", err)
		}
		return ""
	}
	query := queryOf(last)
	if c.Kind != cbSearchBack || query == "" {
		return b.onList(ctx, q, callback{Kind: cbPage, Page: 1, V: c.V})
	}
	raw, v, _, err := b.cbState(ctx, q.From.ID, c)
	if err != nil {
		b.logger().Error("filters", "err", err)
		return errNotice
	}
	sc, err := b.searchResults(ctx, loadFilters(raw, b.today()), query, v)
	if err != nil {
		b.logger().Error("search", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, stamp(sc, v)); err != nil {
		b.logger().Warn("show search", "err", err)
	}
	return ""
}
