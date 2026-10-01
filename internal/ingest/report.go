package ingest

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/normalize"
)

// Статусы области в санити-отчёте (D-07).
const (
	regionNotRF   = "НЕТ в перечне"
	regionForeign = "иностранная"
	regionRF      = "в перечне РФ"
	regionEmpty   = "область пуста"
	regionMulti   = "несколько областей"
)

// regionRank — порядок в таблице: сначала подозрительные.
var regionRank = map[string]int{regionNotRF: 0, regionForeign: 0, regionMulti: 1, regionEmpty: 1, regionRF: 2}

// rank — место статуса в таблице; область другой страны («страна BY») — как
// область из перечня: перечень субъектов РФ к ней не применяется.
func rank(status string) int {
	if r, ok := regionRank[status]; ok {
		return r
	}
	return rankDefault
}

const rankDefault = 2

// longDistanceM — дистанции длиннее показываются для проверки склеек.
const longDistanceM = 100_000

// SanityReport пишет Markdown-отчёт по записям прогона для решения
// владельца о fallback RU (D-07, ING-11): области с отметкой «в перечне
// РФ» / «НЕТ в перечне» / «иностранная», число строк и первые 5 городов;
// дистанции больше 100 км и нераспознанные типы; страна записей и
// серии (D-04, D-14). Чистая функция, вывод детерминирован.
func SanityReport(w io.Writer, source string, runID int64, startedAt time.Time, recs []SourceEvent) error {
	type group struct {
		name, status string
		rows         int
		cities       map[string]int
	}
	groups := map[string]*group{}
	perStatus, rowsBy := map[string]int{}, map[string]int{}
	var long []string
	types := map[string]int{}

	for _, rec := range recs {
		region := normalize.Region(rec.Region)
		var name, status string
		switch {
		case len(rec.RegionSlugs) > 1:
			names := rec.Regions
			if len(names) == 0 {
				names = rec.RegionSlugs
			}
			name, status = strings.Join(names, " + "), regionMulti
		case otherCountry(rec.Country):
			name, status = cmp.Or(region, "(пусто)"), "страна "+normalize.CountryCode(rec.Country)
		case region == "":
			name, status = "(пусто)", regionEmpty
		default:
			name = region
			switch _, kind := normalize.ClassifyRegion(region); kind {
			case normalize.RegionRF:
				status = regionRF
			case normalize.RegionForeign:
				status = regionForeign
			default:
				status = regionNotRF
			}
		}
		key := status + "\x00" + name
		g := groups[key]
		if g == nil {
			g = &group{name: name, status: status, cities: map[string]int{}}
			groups[key] = g
			perStatus[status]++
		}
		g.rows++
		rowsBy[status]++
		if c := normalize.City(rec.City); c != "" {
			g.cities[c]++
		}

		for _, m := range normalize.Distances(rec.Distances) {
			if m > longDistanceM {
				long = append(long, fmt.Sprintf("«%s» (%s): «%s» → %d м", rec.Title, rec.ExternalID, rec.Distances, m))
				break
			}
		}
		if normalize.Type(rec.TypeLabel) == event.TypeOther {
			types[cmp.Or(strings.TrimSpace(rec.TypeLabel), "(пусто)")]++
		}
	}

	rows := slices.SortedFunc(maps.Values(groups), func(a, b *group) int {
		return cmp.Or(cmp.Compare(rank(a.status), rank(b.status)),
			cmp.Compare(b.rows, a.rows), strings.Compare(a.name, b.name), strings.Compare(a.status, b.status))
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Санити-отчёт RU-fallback — %s, прогон %d, %s UTC\n\n", source, runID, startedAt.UTC().Format("2006-01-02 15:04"))
	fmt.Fprintf(&sb, "Строк: %d | уникальных областей: %d | %s: %d | %s: %d | иностранных: %d | %s: %d | %s: %d\n\n",
		len(recs), perStatus[regionRF]+perStatus[regionNotRF]+perStatus[regionForeign],
		regionRF, perStatus[regionRF], regionNotRF, perStatus[regionNotRF], perStatus[regionForeign],
		regionEmpty, rowsBy[regionEmpty], regionMulti, rowsBy[regionMulti])
	writeCountrySeries(&sb, recs)
	sb.WriteString("## Области (сначала подозрительные)\n\n")
	sb.WriteString("| Область (как в источнике) | Статус | Строк | Города (первые 5) |\n|---|---|---|---|\n")
	for _, g := range rows {
		fmt.Fprintf(&sb, "| %s | %s | %d | %s |\n", mdCell(g.name), g.status, g.rows, mdCell(topCities(g.cities, 5)))
	}

	sb.WriteString("\n## Прочее для проверки глазами\n\n")
	sb.WriteString("Дистанции больше 100 км (возможные склейки):\n\n")
	writeList(&sb, long)
	sb.WriteString("\nТипы, не распознанные как известные (other):\n\n")
	var typeLines []string
	for _, l := range slices.SortedFunc(maps.Keys(types), func(a, b string) int {
		return cmp.Or(cmp.Compare(types[b], types[a]), strings.Compare(a, b))
	}) {
		typeLines = append(typeLines, fmt.Sprintf("«%s» — %d", l, types[l]))
	}
	writeList(&sb, typeLines)

	sb.WriteString("\n## Решение\n\nЗаполняется по решению владельца (план 03-07): значение, дата, кто принял решение.\n")
	_, err := io.WriteString(w, sb.String())
	return err
}

// writeCountrySeries пишет раздел «Страна и серии» (D-04, D-14). RU —
// явная страна РФ; иностранная — явная не-RU страна или иностранная
// страна/область; иначе «не указана» — судьбу решает политика источника
// (при no_fallback — вне каталога), её города показываются для проверки.
func writeCountrySeries(sb *strings.Builder, recs []SourceEvent) {
	var ru, foreign, unknown int
	other := map[string]int{} // разрешённые страны не РФ: код → записей
	unknownCities := map[string]int{}
	seriesRows := 0
	seriesCity := map[string]bool{} // ключ серии → есть запись с городом
	for _, rec := range recs {
		city := normalize.City(rec.City)
		_, regionKind := normalize.ClassifyRegion(normalize.Region(rec.Region))
		_, countryKind := normalize.ClassifyRegion(rec.Country)
		switch code := normalize.CountryCode(rec.Country); {
		case code == "RU":
			ru++
		case code != "" && normalize.IsAllowedCountry(code):
			other[code]++
		case code != "" || countryKind == normalize.RegionForeign || regionKind == normalize.RegionForeign:
			foreign++
		default:
			unknown++
			if city != "" {
				unknownCities[city]++
			}
		}
		if rec.GroupKey != "" {
			seriesRows++
			seriesCity[rec.GroupKey] = seriesCity[rec.GroupKey] || city != ""
		}
	}
	noCity := 0
	for _, has := range seriesCity {
		if !has {
			noCity++
		}
	}
	sb.WriteString("## Страна и серии\n\n")
	countries := fmt.Sprintf("RU — %d", ru)
	for _, code := range slices.Sorted(maps.Keys(other)) {
		countries += fmt.Sprintf(" | %s — %d", code, other[code])
	}
	fmt.Fprintf(sb, "Страна: %s | иностранная — %d | не указана — %d\n\n", countries, foreign, unknown)
	fmt.Fprintf(sb, "Не указана, города (первые 10): %s\n\n", topCities(unknownCities, 10))
	fmt.Fprintf(sb, "Серии (group_key): ключей — %d | записей — %d | ключей без города — %d\n\n", len(seriesCity), seriesRows, noCity)
}

// topCities — до n городов по убыванию числа строк, затем по имени.
func topCities(cities map[string]int, n int) string {
	names := slices.SortedFunc(maps.Keys(cities), func(a, b string) int {
		return cmp.Or(cmp.Compare(cities[b], cities[a]), strings.Compare(a, b))
	})
	if len(names) == 0 {
		return "—"
	}
	if len(names) > n {
		return strings.Join(names[:n], ", ") + ", …"
	}
	return strings.Join(names, ", ")
}

// mdCell экранирует символ, ломающий строку таблицы Markdown.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func writeList(sb *strings.Builder, lines []string) {
	if len(lines) == 0 {
		sb.WriteString("- нет\n")
	}
	for _, l := range lines {
		sb.WriteString("- " + l + "\n")
	}
}

// otherCountry — у записи явная страна с ISO-кодом не РФ.
func otherCountry(country string) bool {
	code := normalize.CountryCode(country)
	return code != "" && code != "RU"
}
