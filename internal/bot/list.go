package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

const (
	pageSize   = 8
	maxListBtn = 64 // рун в кнопке события: клиент всё равно обрезает по ширине экрана
	goneNotice = "Событие больше не доступно"
)

// onList показывает страницу списка (p:<стр>:v<N>). Версия фильтров
// устарела — вместо страницы кнопки открывается первая страница
// актуальной выдачи; страница за пределами выдачи зажимается до последней.
func (b *Bot) onList(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind != cbPage {
		return staleNotice
	}
	raw, v, _, err := b.cbState(ctx, q.From.ID, c)
	if err != nil {
		b.logger().Error("filters", "err", err)
		return errNotice
	}
	today := b.today()
	f := loadFilters(raw, today)
	page, notice := c.Page, ""
	if c.V != v {
		page = 1
		// v = 0 — пользователя нет в users: менять ему было нечего.
		if v != 0 {
			notice = "Фильтры изменились — показываю актуальную выдачу"
		}
	}
	sc, err := b.listPage(ctx, f, v, page)
	if err != nil {
		b.logger().Error("list", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, stamp(sc, v)); err != nil {
		b.logger().Warn("show list", "err", err)
	}
	return notice
}

// listPage — экран страницы page выдачи по фильтрам f версии v (без отметки
// версии в кнопках); пустая выдача — экран пустой выдачи. Страница за
// пределами выдачи зажимается до последней.
func (b *Bot) listPage(ctx context.Context, f userFilters, v, page int) (screen, error) {
	today := b.today()
	flt := f.query(today)
	total, err := b.Store.CountEvents(ctx, flt)
	if err != nil {
		return screen{}, err
	}
	if total == 0 {
		var pick bool
		if f.Country == "" {
			if pick, err = b.countryAvailable(ctx, f); err != nil {
				b.logger().Warn("countries", "err", err)
			}
		}
		return emptyScreen(f, pick, b.suggestRow()), nil
	}
	page = min(page, (total+pageSize-1)/pageSize)
	items, err := b.Store.ListEvents(ctx, flt, pageSize, (page-1)*pageSize)
	if err != nil {
		return screen{}, err
	}
	name, err := b.cityName(ctx, f)
	if err != nil {
		return screen{}, err
	}
	sc := listScreen(items, total, page, v, f.Country == allCountries, today.Year(), activeLine(f, name))
	// Небеговой вид спорта назван в заголовке списка; у бега заголовок прежний.
	if sp := f.sport(); !f.running() {
		_, rest, _ := strings.Cut(sc.text, "\n")
		sc.text = fmt.Sprintf("%s <b>%s — найдено: %d</b>\n%s", sp.Emoji, sp.Title, total, rest)
	}
	return sc, nil
}

// statusIcon — короткая пометка статуса в кнопке списка.
func statusIcon(s event.Status) string {
	switch s {
	case event.StatusCancelled:
		return "❌ "
	case event.StatusPostponed:
		return "⏸ "
	}
	return ""
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

// maxBtnCity — рун города в кнопке события: длинный город не вытесняет название.
const maxBtnCity = 16

// eventBtn — кнопка события «дата · город · название»; c — что она открывает
// (карточку из списка или из результатов поиска).
// Кнопка однострочная (перенос строки Telegram в подписи не показывает),
// поэтому она не длиннее maxListBtn, а город — не длиннее maxBtnCity. flags —
// перед городом флаг страны события; область и район не показываются никогда.
func eventBtn(it store.ListItem, c callback, flags bool, year int) row {
	parts := []string{listDate(it.StartDate, year)}
	if it.City != "" {
		city := cut(it.City, maxBtnCity)
		if f := countryFlag(it.CountryCode); flags && f != "" {
			city = f + " " + city
		}
		parts = append(parts, city)
	}
	parts = append(parts, statusIcon(it.Status)+it.Title)
	return btn(cut(strings.Join(parts, " · "), maxListBtn), c)
}

// listScreen — страница списка: каждое событие — кнопка (eventBtn); под ними
// навигация «← стр/всего →» и возврат к поиску. В тексте — число найденных
// жирным и отдельной строкой заданные фильтры (filters, готовый HTML; "" — не
// заданы). year — текущий год (см. listDate). flags — выдача «все страны».
// Дистанции — в карточке.
func listScreen(items []store.ListItem, total, page, v int, flags bool, year int, filters string) screen {
	pages := (total + pageSize - 1) / pageSize
	head := "⚙️ Фильтры не заданы"
	if filters != "" {
		head = "⚙️ Фильтры: " + filters
	}
	rows := make([]row, 0, len(items)+2)
	for _, it := range items {
		rows = append(rows, eventBtn(it, callback{Kind: cbCard, ID: it.ID, Page: page, V: v}, flags, year))
	}
	var nav row
	if page > 1 {
		nav.add("←", callback{Kind: cbPage, Page: page - 1, V: v})
	}
	nav.add(fmt.Sprintf("%d/%d", page, pages), callback{Kind: cbPage, Page: page, V: v})
	if page < pages {
		nav.add("→", callback{Kind: cbPage, Page: page + 1, V: v})
	}
	return screen{text: fmt.Sprintf("🔎 <b>Найдено: %d</b>\n\n%s", total, head), kb: keyboard(append(rows, nav, btn(backLabel, back))...)}
}

// listDate — дата в строке списка: «ДД.ММ», а для события не текущего года
// year — «ДД.ММ.ГГГГ», чтобы забег следующего года не выглядел прошедшим.
func listDate(d time.Time, year int) string {
	if d.Year() != year {
		return d.Format("02.01.2006")
	}
	return d.Format("02.01")
}

// onCard показывает карточку события (e:<id>:<стр>:v<N>). Слитое событие
// открывает карточку корня (D-16, корень — только через store.RootEventID);
// скрытое и несуществующее недоступно (BOT-06), событие выключенного вида
// спорта — тоже (D-08); отменённое и перенесённое показывается с пометкой.
func (b *Bot) onCard(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind != cbCard {
		return staleNotice
	}
	sc, _, ok := b.buildCard(ctx, q.From.ID, c)
	if !ok {
		return errNotice
	}
	// Карточка из сообщения без списка приходит новым сообщением: то не правится.
	if c.Page == 0 {
		err := b.send(ctx, q.From.ID, sc)
		if err != nil {
			b.logger().Warn("send card", "err", err)
		}
	} else if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show card", "err", err)
	}
	b.count(ctx, store.MetricCardBot)
	return ""
}

// buildCard — экран карточки события c.ID (либо «Событие больше не доступно»)
// с возвратом на страницу c.Page и версия фильтров для его кнопок; ok = false —
// внутренняя ошибка (уже в логе).
func (b *Bot) buildCard(ctx context.Context, tgID int64, c callback) (sc screen, v int, ok bool) {
	// Кнопки карточки — с текущей версией фильтров: карточка из старого
	// сообщения открывается, а «назад» ведёт в актуальную выдачу.
	raw, v, _, err := b.cbState(ctx, tgID, c)
	if err != nil {
		b.logger().Error("filters", "err", err)
		return screen{}, 0, false
	}
	root, err := b.Store.RootEventID(ctx, c.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		b.logger().Error("root event", "err", err)
		return screen{}, 0, false
	}
	var e event.Event
	if err == nil {
		e, err = b.Store.GetEvent(ctx, root)
	}
	sport, _ := event.SportByCode(e.Sport) // нет в справочнике — как выключен
	switch {
	case errors.Is(err, store.ErrNotFound):
		return goneScreen(c.Page, v), v, true
	case err != nil:
		b.logger().Error("get event", "err", err)
		return screen{}, 0, false
	// Слитый корень — страховка от гонки со слиянием между запросами.
	case !e.IsListed || e.MergedIntoID != nil || !sport.Enabled:
		return goneScreen(c.Page, v), v, true
	}
	srcs, err := b.Store.EventSources(ctx, e.ID)
	if err != nil {
		b.logger().Error("event sources", "err", err)
		return screen{}, 0, false
	}
	// Место: «📍 город», а при «всех странах» ещё и «🌍 страна».
	style := placePlain
	if loadFilters(raw, b.today()).Country == allCountries {
		style = placeWithCountry
	}
	sc = cardScreen(e, srcs, c.Page, v, style)
	// Строка избранного — перед строкой возврата (она всегда последняя).
	fav, err := b.Store.IsFavorite(ctx, tgID, e.ID)
	if err != nil {
		b.logger().Error("is favorite", "err", err)
		return screen{}, 0, false
	}
	rows := sc.kb.InlineKeyboard
	favRow := []models.InlineKeyboardButton{favBtn(e.ID, fav)}
	if b.name != "" {
		favRow = append(favRow, models.InlineKeyboardButton{Text: shareLabel, URL: shareURL(b.name, e.ID, e.Title)})
	}
	add := [][]models.InlineKeyboardButton{favRow}
	if !event.DateTBD(e.Title) {
		add = append(add, btn(calFileLabel, callback{Kind: cbCalFile, ID: e.ID}))
	}
	if b.reportsOn() {
		add = append(add, btn(reportLabel, callback{Kind: cbReportOpen, ID: e.ID}))
	}
	sc.kb.InlineKeyboard = slices.Insert(rows, len(rows)-1, add...)
	return sc, v, true
}

func goneScreen(page, v int) screen {
	return screen{text: goneNotice, kb: keyboard(cardBack(page, v))}
}

// menuLabel — возврат из карточки, открытой не из списка (страница 0: по
// ссылке или из сообщения подписки).
const menuLabel = "⬅️ В меню"

// cardBack — последний ряд карточки: на страницу списка либо, для страницы 0,
// в главное меню.
func cardBack(page, v int) row {
	if page == 0 {
		return btn(menuLabel, callback{Kind: cbMenu})
	}
	return btn(backLabel, callback{Kind: cbPage, Page: page, V: v})
}

// monthsGen — месяцы в родительном падеже.
var monthsGen = [...]string{"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

// sourceNames — подпись источника по коду; неизвестный код показывается как есть.
var sourceNames = map[string]string{"marathonec": "Марафонец", "probeg": "ПроБЕГ", "heroleague": "Лига Героев", "tristyle": "Tristyle.Клуб", "klbamatar": "КЛБ «Аматар»", "runin": "RunIn", "myrace": "MyRace",
	"swimrocket": "SwimRocket", "swimcup": "Swimcup", "cyclingrace": "CyclingRace", "otime": "O-time", "russialoppet": "Russialoppet", "regplace": "Reg.place"}

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
	placePlain       placeStyle = iota // «📍 Минск»
	placeWithCountry                   // «🇧🇾 Беларусь» и «📍 Минск» — все страны
)

// cardScreen — карточка события (BOT-05, HTML): всё динамическое
// экранировано, ссылки только из validURL. Пустые поля строк не дают. Вид
// карточки определяет событие: у небегового — значок вида спорта перед
// названием и строка «вид спорта · дисциплина» вместо строки типа; дисциплина
// «другое» не называется.
func cardScreen(e event.Event, srcs []store.EventSource, page, v int, style placeStyle) screen {
	esc := func(s string, n int) string { return html.EscapeString(cut(s, n)) }
	icon, kind := typeEmoji(e.Type), typeEmoji(e.Type)+" "+typeLabel(string(e.Type))
	if sp, ok := event.SportByCode(event.SportOf(string(e.Sport))); ok && sp.Code != event.SportRunning {
		icon, kind = sp.Emoji, sp.Emoji+" "+sp.Title
		if e.Type != event.TypeOther {
			kind += " · " + typeLabel(string(e.Type))
		}
	}
	var sb strings.Builder
	sb.WriteString(icon + " <b>" + statusMark(e.Status) + esc(e.Title, 200) + "</b>\n\n")
	sb.WriteString("📅 " + ruDate(e.StartDate))
	if e.EndDate != nil && !e.EndDate.Equal(e.StartDate) {
		sb.WriteString(" – " + ruDate(*e.EndDate))
	}
	sb.WriteString("\n")
	if e.StartTime != "" {
		sb.WriteString("🕒 Старт в " + esc(e.StartTime, 5) + " (местное время)\n")
	}
	place := "город не указан"
	if e.City != "" {
		place = esc(e.City, 100)
	}
	if a := VisibleAddress(e); a != "" {
		place += ", " + esc(a, 200)
	}
	sb.WriteString("📍 " + place + "\n")
	if style == placeWithCountry {
		sb.WriteString("🌍 " + esc(countryName(e.CountryCode), 60) + "\n")
	}
	// Дистанции одной группой «3 · 10 · 21 км»; если у какой-то есть подпись —
	// единица у каждой: «5 км (детский) · 10 км».
	if len(e.Distances) > 0 {
		labelled := slices.ContainsFunc(e.Distances, func(d event.Distance) bool { return d.Label != "" })
		parts := make([]string, len(e.Distances))
		for i, d := range e.Distances {
			parts[i] = km(d.Meters)
			if labelled {
				parts[i] += " км"
			}
			if d.Label != "" {
				parts[i] += " (" + esc(d.Label, 40) + ")"
			}
		}
		line := strings.Join(parts, " · ")
		if !labelled {
			line += " км"
		}
		sb.WriteString("📏 " + line + "\n")
	}
	sb.WriteString(kind + "\n")
	if e.Description != "" {
		sb.WriteString("\n" + esc(e.Description, 1000) + "\n")
	}
	if len(srcs) > 0 {
		sb.WriteString("\n" + sourcesLine(srcs))
	}
	var reg row
	if validURL(e.RegistrationURL) {
		reg = row{{Text: "🔗 Регистрация", URL: e.RegistrationURL}}
	}
	return screen{text: strings.TrimRight(sb.String(), "\n"),
		kb: keyboard(reg, cardBack(page, v))}
}

// sourcesLine — строка источников карточки: «🔗 Источник: …», ссылка — только
// годная; srcs не пуст.
func sourcesLine(srcs []store.EventSource) string {
	names := make([]string, len(srcs))
	for i, s := range srcs {
		names[i] = html.EscapeString(sourceName(s.Code))
		if validURL(s.URL) {
			names[i] = `<a href="` + html.EscapeString(s.URL) + `">` + names[i] + "</a>"
		}
	}
	return "🔗 " + plural(len(srcs), "Источник", "Источники", "Источники") + ": " + strings.Join(names, ", ")
}

// validURL — ссылка, которую можно показать (event.ValidURL): та же проверка,
// по которой прогон решает, что у старта появилась ссылка.
func validURL(s string) bool { return event.ValidURL(s) }

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
