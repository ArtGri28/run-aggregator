package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

const (
	pageSize     = 5
	maxListTitle = 60   // рун в строке списка
	maxURLLen    = 2048 // предел Telegram для URL-кнопки
	goneNotice   = "Событие больше не доступно"
)

// onList показывает страницу списка (p:<стр>:v<N>). Версия фильтров
// устарела — вместо страницы кнопки открывается первая страница
// актуальной выдачи; страница за пределами выдачи зажимается до последней.
func (b *Bot) onList(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind != cbPage {
		return staleNotice
	}
	raw, v, err := b.Store.Filters(ctx, q.From.ID)
	if err != nil {
		b.logger().Error("filters", "err", err)
		return errNotice
	}
	today := b.today()
	f := loadFilters(raw, today)
	page, notice := c.Page, ""
	if c.V != v {
		page, notice = 1, "Фильтры изменились — показываю актуальную выдачу"
	}
	flt := f.query(today)
	total, err := b.Store.CountEvents(ctx, flt)
	if err != nil {
		b.logger().Error("count", "err", err)
		return errNotice
	}
	sc := emptyScreen(f)
	if total > 0 {
		page = min(page, (total+pageSize-1)/pageSize)
		items, err := b.Store.ListEvents(ctx, flt, pageSize, (page-1)*pageSize)
		if err != nil {
			b.logger().Error("list", "err", err)
			return errNotice
		}
		sc = listScreen(items, total, page, v, f.Country == allCountries)
	}
	if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show list", "err", err)
	}
	return notice
}

// statusMark — пометка статуса в начале названия.
func statusMark(s event.Status) string {
	switch s {
	case event.StatusCancelled:
		return "[ОТМЕНЁН] "
	case event.StatusPostponed:
		return "[ПЕРЕНЕСЁН] "
	}
	return ""
}

// cut обрезает строку до n рун, последняя — «…».
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// listScreen — страница списка (D-11): пронумерованные строки, кнопки
// карточек 1…5, навигация и «Фильтры». flags — перед городом флаг страны
// события (выдача «все страны»); область и район не показываются никогда.
func listScreen(items []store.ListItem, total, page, v int, flags bool) screen {
	pages := (total + pageSize - 1) / pageSize
	var sb strings.Builder
	fmt.Fprintf(&sb, "Найдено %d · страница %d/%d\n", total, page, pages)
	var cards row
	for i, it := range items {
		city := it.City
		if city == "" {
			city = "—"
		}
		if f := countryFlag(it.CountryCode); flags && f != "" {
			city = f + " " + city
		}
		fmt.Fprintf(&sb, "\n%d. %s · %s · %s%s", i+1, it.StartDate.Format("02.01"), html.EscapeString(city),
			statusMark(it.Status), html.EscapeString(cut(it.Title, maxListTitle)))
		if km := kmList(it.Distances); km != "" {
			sb.WriteString(" · " + km)
		}
		cards.add(strconv.Itoa(i+1), callback{Kind: cbCard, ID: it.ID, Page: page, V: v})
	}
	var nav row
	if page > 1 {
		nav.add("←", callback{Kind: cbPage, Page: page - 1, V: v})
	}
	nav.add(fmt.Sprintf("стр. %d/%d", page, pages), callback{Kind: cbPage, Page: page, V: v})
	if page < pages {
		nav.add("→", callback{Kind: cbPage, Page: page + 1, V: v})
	}
	return screen{text: sb.String(), kb: keyboard(cards, nav, btn("Фильтры", back))}
}

// onCard показывает карточку события (e:<id>:<стр>:v<N>). Слитое событие
// открывает карточку корня (D-16, корень — только через store.RootEventID);
// скрытое и несуществующее недоступно (BOT-06); отменённое и перенесённое
// показывается с пометкой.
func (b *Bot) onCard(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind != cbCard {
		return staleNotice
	}
	root, err := b.Store.RootEventID(ctx, c.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		b.logger().Error("root event", "err", err)
		return errNotice
	}
	var e event.Event
	if err == nil {
		e, err = b.Store.GetEvent(ctx, root)
	}
	var sc screen
	switch {
	case errors.Is(err, store.ErrNotFound):
		sc = goneScreen(c.Page, c.V)
	case err != nil:
		b.logger().Error("get event", "err", err)
		return errNotice
	// Слитый корень — страховка от гонки со слиянием между запросами.
	case !e.IsListed || e.MergedIntoID != nil:
		sc = goneScreen(c.Page, c.V)
	default:
		srcs, err := b.Store.EventSources(ctx, e.ID)
		if err != nil {
			b.logger().Error("event sources", "err", err)
			return errNotice
		}
		style, err := b.cardPlaceStyle(ctx, q.From.ID)
		if err != nil {
			b.logger().Error("card place", "err", err)
			return errNotice
		}
		sc = cardScreen(e, srcs, c.Page, c.V, style)
	}
	if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show card", "err", err)
	}
	return ""
}

// cardPlaceStyle — как показать место в карточке по фильтрам пользователя:
// прежний текст (одна страна), «📍 город» (страна выбрана) или «🌍 страна» и
// «📍 город» (все страны).
func (b *Bot) cardPlaceStyle(ctx context.Context, tgID int64) (placeStyle, error) {
	raw, _, err := b.Store.Filters(ctx, tgID)
	if err != nil {
		return placePlain, err
	}
	f := loadFilters(raw, b.today())
	if f.Country == allCountries {
		return placeWithCountry, nil
	}
	show, err := b.showCountry(ctx, f)
	if show {
		return placePinned, err
	}
	return placePlain, err
}

func goneScreen(page, v int) screen {
	return screen{text: goneNotice, kb: keyboard(btn("К списку", callback{Kind: cbPage, Page: page, V: v}))}
}

// monthsGen — месяцы в родительном падеже.
var monthsGen = [...]string{"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

// sourceNames — подпись источника по коду; неизвестный код показывается как есть.
var sourceNames = map[string]string{"marathonec": "Марафонец", "probeg": "ПроБЕГ", "heroleague": "Лига Героев"}

func sourceName(code string) string {
	if n, ok := sourceNames[code]; ok {
		return n
	}
	return code
}

func ruDate(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), monthsGen[t.Month()-1], t.Year())
}

// placeStyle — вид строки места в карточке.
type placeStyle int

const (
	placePlain       placeStyle = iota // «Минск» — одна страна в каталоге, прежний текст
	placePinned                        // «📍 Минск» — страна выбрана
	placeWithCountry                   // «🇧🇾 Беларусь» и «📍 Минск» — все страны
)

// cardScreen — карточка события (BOT-05, HTML): всё динамическое
// экранировано, ссылки только из validURL. Пустые поля строк не дают.
func cardScreen(e event.Event, srcs []store.EventSource, page, v int, style placeStyle) screen {
	esc := func(s string, n int) string { return html.EscapeString(cut(s, n)) }
	var sb strings.Builder
	sb.WriteString("<b>" + statusMark(e.Status) + esc(e.Title, 200) + "</b>\n")
	sb.WriteString(ruDate(e.StartDate))
	if e.EndDate != nil && !e.EndDate.Equal(e.StartDate) {
		sb.WriteString(" – " + ruDate(*e.EndDate))
	}
	sb.WriteString("\n")
	if e.StartTime != "" {
		sb.WriteString("Старт в " + esc(e.StartTime, 5) + " (местное время)\n")
	}
	place := "город не указан"
	if e.City != "" {
		place = esc(e.City, 100)
	}
	if a := visibleAddress(e); a != "" {
		place += ", " + esc(a, 200)
	}
	if style == placeWithCountry {
		sb.WriteString("🌍 " + esc(countryName(e.CountryCode), 60) + "\n")
	}
	if style != placePlain {
		place = "📍 " + place
	}
	sb.WriteString(place + "\n" + typeLabel(string(e.Type)) + "\n")
	if len(e.Distances) > 0 {
		parts := make([]string, len(e.Distances))
		for i, d := range e.Distances {
			parts[i] = km(d.Meters) + " км"
			if d.Label != "" {
				parts[i] += " (" + esc(d.Label, 40) + ")"
			}
		}
		sb.WriteString("Дистанции: " + strings.Join(parts, " · ") + "\n")
	}
	if e.Description != "" {
		sb.WriteString("\n" + esc(e.Description, 1000) + "\n")
	}
	if len(srcs) > 0 {
		names := make([]string, len(srcs))
		for i, s := range srcs {
			names[i] = html.EscapeString(sourceName(s.Code))
			if validURL(s.URL) {
				names[i] = `<a href="` + html.EscapeString(s.URL) + `">` + names[i] + "</a>"
			}
		}
		sb.WriteString("\nИсточники: " + strings.Join(names, ", "))
	}
	var reg row
	if validURL(e.RegistrationURL) {
		reg = row{{Text: "Регистрация", URL: e.RegistrationURL}}
	}
	return screen{text: strings.TrimRight(sb.String(), "\n"),
		kb: keyboard(reg, btn("К списку", callback{Kind: cbPage, Page: page, V: v}))}
}

// validURL — ссылка, которую можно показать: http/https с хостом, без
// пробелов и управляющих символов, не длиннее maxURLLen (Pitfall 5).
func validURL(s string) bool {
	if s == "" || len(s) > maxURLLen || strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

// km — километры: целые без дроби, иначе одна цифра после запятой.
func km(m int) string {
	if m%1000 == 0 {
		return strconv.Itoa(m / 1000)
	}
	return strings.Replace(strconv.FormatFloat(float64(m)/1000, 'f', 1, 64), ".", ",", 1)
}

// kmList — «5/10/21 км»; "" без дистанций.
func kmList(meters []int) string {
	if len(meters) == 0 {
		return ""
	}
	parts := make([]string, len(meters))
	for i, m := range meters {
		parts[i] = km(m)
	}
	return strings.Join(parts, "/") + " км"
}
