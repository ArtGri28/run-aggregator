package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/normalize"
	"aggregator/internal/store"
)

// defaultCountry — страна пользователя, не выбравшего её (пустое Country, в
// том числе во всех фильтрах, сохранённых до появления страны). Это значение
// бота: store и event.Filter страны по умолчанию не знают.
const defaultCountry = "RU"

// userFilters — фильтры пользователя (users.filters, jsonb). Значения
// абсолютные; пустой объект — «все будущие даты, вся Россия, любая
// дистанция, любой тип».
type userFilters struct {
	Date string `json:"date,omitempty"` // "" и "a" — все будущие
	City string `json:"city,omitempty"` // city_key страны фильтра
	Dist []int  `json:"dist,omitempty"` // индексы buckets
	Type string `json:"type,omitempty"`
	// Country: "" — страна по умолчанию, "*" — все страны, иначе ISO
	// alpha-2. Старые фильтры без поля читаются как "".
	Country string `json:"country,omitempty"`
}

// buckets — корзины дистанций (D-08): сплошные полуинтервалы (lo, hi] в метрах.
var buckets = [...]struct {
	label  string
	lo, hi int
}{
	{"до 5 км", 0, 5500},
	{"5–15 км", 5500, 15000},
	{"15–30 км, полумарафон", 15000, 30000},
	{"30–43 км, марафон", 30000, 43000},
	{"ультра > 43 км", 43000, 2147483647},
}

var typeLabels = map[event.Type]string{
	event.TypeRoad:     "Шоссе",
	event.TypeTrail:    "Трейл",
	event.TypeCross:    "Кросс",
	event.TypeTrack:    "Стадион",
	event.TypeObstacle: "С препятствиями",
	event.TypeOther:    "Другое",
}

var monthNames = [...]string{"январь", "февраль", "март", "апрель", "май", "июнь",
	"июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}

// loadFilters терпима к испорченным данным: ошибка разбора — значения по
// умолчанию, лишние ключи игнорируются, значения вне грамматики кодека
// отбрасываются. today — «сегодня» для сброса прошедшего месяца.
func loadFilters(raw []byte, today time.Time) userFilters {
	var f userFilters
	if json.Unmarshal(raw, &f) != nil {
		return userFilters{}
	}
	if f.Date != "" && !validDate(f.Date) {
		f.Date = ""
	}
	// Прошедший месяц из сохранённых фильтров — «все будущие» (LO-05);
	// значение уходит из БД при следующем изменении фильтров.
	if _, to := dateRange(f.Date, today); monthRE.MatchString(f.Date) && to.Before(today) {
		f.Date = ""
	}
	if f.City != "" && !validCityKey(f.City) {
		f.City = ""
	}
	if !validCountry(f.Country) {
		f.Country = ""
	}
	// Город принадлежит стране: при «всех странах» ключ города неоднозначен.
	if f.Country == allCountries {
		f.City = ""
	}
	if f.Type != "" && !validType(f.Type) {
		f.Type = ""
	}
	var dist []int
	for _, i := range f.Dist {
		if i >= 0 && i < len(buckets) && !slices.Contains(dist, i) {
			dist = append(dist, i)
		}
	}
	slices.Sort(dist)
	f.Dist = dist
	return f
}

// Изменения фильтров — чистые методы, возвращают новое значение. Дата, город
// и тип задаются абсолютно, корзина переключается относительно текущего
// состояния: update применяет изменение под блокировкой строки, поэтому
// двойное нажатие безопасно (даёт «выкл→вкл»).

func (f userFilters) setDate(v string) userFilters {
	if v == "a" {
		v = ""
	}
	f.Date = v
	return f
}

func (f userFilters) setCity(key string) userFilters {
	if key == "-" {
		key = ""
	}
	f.City = key
	return f
}

// setCountry задаёт страну: "-" и defaultCountry хранятся как "" (по
// умолчанию), "*" — все страны. Город принадлежит стране фильтра (его ключ
// осмыслен только вместе с ней), поэтому при смене эффективной страны он
// сбрасывается, а при той же стране сохраняется.
func (f userFilters) setCountry(c string) userFilters {
	if c == "-" || c == defaultCountry {
		c = ""
	}
	next := userFilters{Country: c}
	if next.effectiveCountry() != f.effectiveCountry() {
		f.City = ""
	}
	f.Country = c
	return f
}

func (f userFilters) setType(t string) userFilters {
	if t == "-" {
		t = ""
	}
	f.Type = t
	return f
}

// toggleBucket добавляет или убирает корзину относительно текущего состояния
// (под блокировкой строки в update); список отсортирован.
func (f userFilters) toggleBucket(i int) userFilters {
	if j := slices.Index(f.Dist, i); j >= 0 {
		f.Dist = slices.Delete(slices.Clone(f.Dist), j, j+1)
		return f
	}
	f.Dist = append(slices.Clone(f.Dist), i)
	slices.Sort(f.Dist)
	return f
}

func (f userFilters) clearBuckets() userFilters {
	f.Dist = nil
	return f
}

// unset снимает один фильтр: 'd' дата, 'c' город, 'b' дистанция, 't' тип,
// 'k' страна (возврат к стране по умолчанию; город сбрасывается).
func (f userFilters) unset(field byte) userFilters {
	switch field {
	case 'd':
		f.Date = ""
	case 'c':
		f.City = ""
	case 'b':
		f.Dist = nil
	case 't':
		f.Type = ""
	case 'k':
		f = f.setCountry("")
	}
	return f
}

func (userFilters) reset() userFilters { return userFilters{} }

// mutation — изменение фильтров, которое делает кнопка; nil — кнопка
// только показывает экран (сводку или подменю).
func mutation(c callback) func(userFilters) userFilters {
	v := c.Value
	switch {
	case c.Kind == cbReset && v == "":
		return userFilters.reset
	case c.Kind == cbReset:
		return func(f userFilters) userFilters { return f.unset(v[0]) }
	case v == "":
		return nil
	case c.Kind == cbDate:
		return func(f userFilters) userFilters { return f.setDate(v) }
	case c.Kind == cbCity:
		return func(f userFilters) userFilters {
			if f.Country == allCountries {
				return f
			}
			return f.setCity(v)
		}
	case c.Kind == cbType:
		return func(f userFilters) userFilters { return f.setType(v) }
	case c.Kind == cbCountry:
		return func(f userFilters) userFilters { return f.setCountry(v) }
	case c.Kind == cbBucket && v == "-":
		return userFilters.clearBuckets
	case c.Kind == cbBucket:
		return func(f userFilters) userFilters { return f.toggleBucket(int(v[0] - '0')) }
	}
	return nil
}

// monthEnd — последний день месяца, в котором лежит t (полночь UTC).
func monthEnd(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
}

// dateRange — диапазон дат пресета (включительно; нулевой to — без верхней
// границы). Чистая: «сегодня» приходит параметром. Значение вне грамматики —
// как по умолчанию.
func dateRange(preset string, today time.Time) (from, to time.Time) {
	from = today
	switch preset {
	case "30":
		to = today.AddDate(0, 0, 30)
	case "cm":
		to = monthEnd(today)
	case "nm":
		from = time.Date(today.Year(), today.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		to = monthEnd(from)
	default:
		if m := monthRE.FindStringSubmatch(preset); m != nil {
			y, _ := strconv.Atoi(m[1])
			mo, _ := strconv.Atoi(m[2])
			first := time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, time.UTC)
			if first.After(today) {
				from = first
			}
			to = monthEnd(first)
		}
	}
	return from, to
}

type option struct{ Value, Label string }

// monthOptions — текущий месяц и 11 следующих.
func monthOptions(today time.Time) []option {
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := make([]option, 12)
	for i := range out {
		m := first.AddDate(0, i, 0)
		out[i] = option{"m" + m.Format("200601"), monthNames[m.Month()-1] + " " + strconv.Itoa(m.Year())}
	}
	return out
}

// effectiveCountry — страна фильтра: "" — defaultCountry, "*" — все страны,
// иначе код как есть.
func (f userFilters) effectiveCountry() string {
	if f.Country == "" {
		return defaultCountry
	}
	return f.Country
}

// countryCode — страна для store и event.Filter: "*" (все страны) — пустая
// строка, то есть без ограничения по стране; иначе effectiveCountry.
func (f userFilters) countryCode() string {
	if c := f.effectiveCountry(); c != "*" {
		return c
	}
	return ""
}

// query переводит фильтры пользователя в параметры выдачи.
func (f userFilters) query(today time.Time) event.Filter {
	q := event.Filter{CountryCode: f.countryCode(), CityKey: f.City, Type: event.Type(f.Type)}
	q.From, q.To = dateRange(f.Date, today)
	for _, i := range f.Dist {
		q.Distances = append(q.Distances, event.DistanceRange{Lo: buckets[i].lo, Hi: buckets[i].hi})
	}
	return q
}

func dateLabel(d string) string {
	switch d {
	case "30":
		return "30 дней"
	case "cm":
		return "этот месяц"
	case "nm":
		return "следующий месяц"
	}
	if m := monthRE.FindStringSubmatch(d); m != nil {
		mo, _ := strconv.Atoi(m[2])
		return monthNames[mo-1] + " " + m[1]
	}
	return "все будущие"
}

func distLabel(dist []int) string {
	if len(dist) == 0 {
		return "любая"
	}
	l := make([]string, len(dist))
	for i, b := range dist {
		l[i] = buckets[b].label
	}
	return strings.Join(l, ", ")
}

func typeLabel(t string) string {
	if t == "" {
		return "любой"
	}
	if l, ok := typeLabels[event.Type(t)]; ok {
		return l
	}
	return html.EscapeString(t)
}

// upperFirst — первая буква заглавная (значения фильтров в сводке).
func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// summaryLine — строки сводки фильтров (HTML) по одной на фильтр; cityName —
// написание города. showCountry: первой строкой страна (в каталоге доступен
// выбор страны или страна выбрана явно).
func summaryLine(f userFilters, cityName string, showCountry bool) string {
	city := "Все города"
	if f.City != "" {
		city = html.EscapeString(cityName)
	}
	lines := []string{
		"📍 Город: " + city,
		"📅 Дата: " + upperFirst(dateLabel(f.Date)),
		"📏 Дистанция: " + upperFirst(distLabel(f.Dist)),
		"🏃 Тип: " + upperFirst(typeLabel(f.Type)),
	}
	if showCountry {
		lines = append([]string{"🌍 Страна: " + countryLabel(f.effectiveCountry())}, lines...)
	}
	return strings.Join(lines, "\n")
}

// btn — ряд из одной кнопки.
func btn(text string, c callback) row {
	var r row
	r.add(text, c)
	return r
}

var back = callback{Kind: cbSummary}

// summaryScreen — экран поиска (D-10): значения фильтров в тексте, в кнопках
// только названия; «Показать N»; showCountry добавляет страну над остальными.
func summaryScreen(f userFilters, cityName string, v, n int, showCountry bool) screen {
	var r1, r2 row
	r1.add("📍 Город", callback{Kind: cbCity})
	r1.add("📅 Дата", callback{Kind: cbDate})
	r2.add("📏 Дистанция", callback{Kind: cbBucket})
	r2.add("🏃 Тип", callback{Kind: cbType})
	var rows []row
	if showCountry {
		rows = append(rows, btn("🌍 Страна", callback{Kind: cbCountry}))
	}
	rows = append(rows, r1, r2,
		btn("🔎 Показать "+strconv.Itoa(n), callback{Kind: cbPage, Page: 1, V: v}),
		btn("🔄 Сбросить", callback{Kind: cbReset}))
	return screen{
		text: "🔎 <b>ПОИСК ЗАБЕГА</b>\n\n" + summaryLine(f, cityName, showCountry),
		kb:   keyboard(rows...),
	}
}

// emptyScreen — пустая выдача (D-12): кнопки снимают только заданные фильтры;
// при заданных фильтрах текст подсказывает, что их можно снять. pickCountry —
// страна не выбрана явно, но выбор доступен: кнопка «Выбрать страну».
func emptyScreen(f userFilters, pickCountry bool) screen {
	var rows []row
	if f.Country != "" {
		rows = append(rows, btn("Снять страну", callback{Kind: cbReset, Value: "k"}))
	}
	if f.Date != "" && f.Date != "a" {
		rows = append(rows, btn("Снять дату", callback{Kind: cbReset, Value: "d"}))
	}
	if f.City != "" {
		rows = append(rows, btn("Снять город", callback{Kind: cbReset, Value: "c"}))
	}
	if len(f.Dist) > 0 {
		rows = append(rows, btn("Снять дистанцию", callback{Kind: cbReset, Value: "b"}))
	}
	if f.Type != "" {
		rows = append(rows, btn("Снять тип", callback{Kind: cbReset, Value: "t"}))
	}
	text := "Ничего не нашлось"
	if len(rows) > 0 {
		text += "\n\nПопробуйте снять ограничение:"
	}
	if pickCountry {
		rows = append(rows, btn("🌍 Выбрать страну", callback{Kind: cbCountry}))
	}
	rows = append(rows, btn("⬅️ К фильтрам", back))
	return screen{text: text, kb: keyboard(rows...)}
}

// dateMenu — пресеты и 12 месяцев.
func dateMenu(today time.Time) screen {
	var r1, r2 row
	r1.add("30 дней", callback{Kind: cbDate, Value: "30"})
	r1.add("Этот месяц", callback{Kind: cbDate, Value: "cm"})
	r2.add("Следующий месяц", callback{Kind: cbDate, Value: "nm"})
	r2.add("Все будущие", callback{Kind: cbDate, Value: "a"})
	rows := []row{r1, r2}
	var mr row
	for i, o := range monthOptions(today) {
		mr.add(o.Label, callback{Kind: cbDate, Value: o.Value})
		if i%3 == 2 {
			rows, mr = append(rows, mr), nil
		}
	}
	return screen{text: "Выберите даты", kb: keyboard(append(rows, btn("⬅️ Назад", back))...)}
}

// bucketMenu — корзины дистанций; выбранные отмечены «✓».
func bucketMenu(f userFilters) screen {
	var rows []row
	for i, bk := range buckets {
		label := bk.label
		if slices.Contains(f.Dist, i) {
			label = "✓ " + label
		}
		rows = append(rows, btn(label, callback{Kind: cbBucket, Value: strconv.Itoa(i)}))
	}
	rows = append(rows, btn("Любая", callback{Kind: cbBucket, Value: "-"}), btn("Готово", back))
	return screen{text: "Выберите дистанции (можно несколько)", kb: keyboard(rows...)}
}

// cityMenu — топ-10 городов по остальным фильтрам; город можно и написать.
func (b *Bot) cityMenu(ctx context.Context, f userFilters) (screen, error) {
	cities, err := b.Store.TopCities(ctx, f.query(b.today()), 10)
	if err != nil {
		return screen{}, err
	}
	var rows []row
	for _, c := range cities {
		rows = append(rows, btn(fmt.Sprintf("%s (%d)", c.Name, c.N), callback{Kind: cbCity, Value: c.Key}))
	}
	rows = append(rows, btn("Все города", callback{Kind: cbCity, Value: "-"}), btn("⬅️ Назад", back))
	return screen{text: "Выберите город или напишите его название сообщением", kb: keyboard(rows...)}, nil
}

// typeMenu — только типы, которые есть среди будущих событий (D-09).
func (b *Bot) typeMenu(ctx context.Context, f userFilters) (screen, error) {
	types, err := b.Store.EventTypes(ctx, f.countryCode(), b.today())
	if err != nil {
		return screen{}, err
	}
	var rows []row
	for _, t := range types {
		rows = append(rows, btn(typeLabel(string(t)), callback{Kind: cbType, Value: string(t)}))
	}
	rows = append(rows, btn("Любой", callback{Kind: cbType, Value: "-"}), btn("⬅️ Назад", back))
	return screen{text: "Выберите тип забега", kb: keyboard(rows...)}, nil
}

// countryAvailable — кнопка страны нужна: в будущем каталоге минимум две
// страны либо одна, но не совпадающая со страной фильтра (иначе пользователь
// с умолчанием «Россия» не увидел бы каталог одной Беларуси). Одна страна,
// равная эффективной, — прежний компактный интерфейс.
func (b *Bot) countryAvailable(ctx context.Context, f userFilters) (bool, error) {
	cs, err := b.Store.Countries(ctx, event.Filter{From: b.today()})
	switch {
	case err != nil:
		return false, err
	case len(cs) >= 2:
		return true, nil
	case len(cs) == 1:
		return cs[0].Code != f.effectiveCountry(), nil
	}
	return false, nil
}

// showCountry — показывать ли страну в сводке фильтров: страна выбрана явно
// или кнопка страны доступна.
func (b *Bot) showCountry(ctx context.Context, f userFilters) (bool, error) {
	if f.Country != "" {
		return true, nil
	}
	return b.countryAvailable(ctx, f)
}

// summary строит сводку: число событий и написание выбранного города.
func (b *Bot) summary(ctx context.Context, f userFilters, v int) (screen, error) {
	today := b.today()
	n, err := b.Store.CountEvents(ctx, f.query(today))
	if err != nil {
		return screen{}, err
	}
	name := f.City
	if f.City != "" {
		nm, cnt, err := b.Store.CityName(ctx, f.countryCode(), f.City, today)
		if err != nil {
			return screen{}, err
		}
		if cnt > 0 {
			name = nm
		}
	}
	show, err := b.showCountry(ctx, f)
	if err != nil {
		return screen{}, err
	}
	return summaryScreen(f, name, v, n, show), nil
}

// countryMenu — все страны будущего каталога (без пользовательских фильтров:
// иначе страну с нулём событий при текущих фильтрах не выбрать) с числом
// событий при остальных фильтрах, в том числе (0), и «Все страны». Город в
// счёт не входит: он принадлежит стране и сбрасывается при её смене. Порядок
// не зависит от фильтров; текущая страна остаётся в списке, даже если её нет
// в каталоге.
func (b *Bot) countryMenu(ctx context.Context, f userFilters) (screen, error) {
	catalog, err := b.Store.Countries(ctx, event.Filter{From: b.today()})
	if err != nil {
		return screen{}, err
	}
	q := f.query(b.today())
	q.CityKey = ""
	filtered, err := b.Store.Countries(ctx, q)
	if err != nil {
		return screen{}, err
	}
	n := map[string]int{}
	total := 0
	for _, c := range filtered {
		n[c.Code] = c.N
		total += c.N
	}
	cs := slices.Clone(catalog)
	cur := f.effectiveCountry()
	if cur != allCountries && !slices.ContainsFunc(cs, func(c store.CountryCount) bool { return c.Code == cur }) {
		cs = append(cs, store.CountryCount{Code: cur})
	}
	for i := range cs {
		cs[i].N = n[cs[i].Code]
	}
	label := func(code string, n int) string {
		l := fmt.Sprintf("%s (%d)", countryLabel(code), n)
		if code == cur {
			l = "✓ " + l
		}
		return l
	}
	var rows []row
	for _, c := range cs {
		rows = append(rows, btn(label(c.Code, c.N), callback{Kind: cbCountry, Value: c.Code}))
	}
	rows = append(rows, btn(label(allCountries, total), callback{Kind: cbCountry, Value: allCountries}), btn("⬅️ Назад", back))
	return screen{text: "🌍 Страна", kb: keyboard(rows...)}, nil
}

// update меняет фильтры одной UpdateFilters: fn получает актуальные
// фильтры под блокировкой строки и возвращает новое значение.
func (b *Bot) update(ctx context.Context, tgID int64, fn func(userFilters) userFilters) (userFilters, int, error) {
	today := b.today()
	raw, v, err := b.Store.UpdateFilters(ctx, tgID, func(cur []byte) ([]byte, error) {
		return json.Marshal(fn(loadFilters(cur, today)))
	})
	return loadFilters(raw, today), v, err
}

// updateAt — изменение фильтров кнопкой c: применяется только при версии
// кнопки, равной текущей. Кнопка без версии или с другой версией —
// store.ErrStaleFilters, в базу не пишется ничего.
func (b *Bot) updateAt(ctx context.Context, tgID int64, c callback, fn func(userFilters) userFilters) (userFilters, int, error) {
	if c.Legacy {
		return userFilters{}, 0, store.ErrStaleFilters
	}
	today := b.today()
	raw, v, err := b.Store.UpdateFiltersAt(ctx, tgID, c.V, func(cur []byte) ([]byte, error) {
		return json.Marshal(fn(loadFilters(cur, today)))
	})
	return loadFilters(raw, today), v, err
}

// cbState — фильтры и версия для кнопки навигации. Актуальная кнопка
// (версия совпала) отмечает активность пользователя; устаревшая — без
// версии, с другой версией или от пользователя, которого нет в users, —
// читает без единой записи: stale = true, для отсутствующего пользователя
// фильтры по умолчанию и версия 0.
func (b *Bot) cbState(ctx context.Context, tgID int64, c callback) (raw []byte, v int, stale bool, err error) {
	raw, v, ok, err := b.Store.PeekFilters(ctx, tgID)
	if err != nil || !ok || c.Legacy || c.V != v {
		return raw, v, true, err
	}
	raw, v, err = b.Store.Filters(ctx, tgID)
	return raw, v, false, err
}

// stamp проставляет версию фильтров v во все кнопки f: экрана: экраны
// строят их без версии. Кнопка, не влезающая с версией в 64 байта,
// пропускается, как в row.add.
func stamp(sc screen, v int) screen {
	if sc.kb == nil {
		return sc
	}
	var rows []row
	for _, r := range sc.kb.InlineKeyboard {
		var out row
		for _, bt := range r {
			if c, err := parseCallback(bt.CallbackData); err == nil && strings.HasPrefix(bt.CallbackData, "f:") {
				c.V, c.Legacy = v, false
				data, err := c.encode()
				if err != nil {
					continue
				}
				bt.CallbackData = data
			}
			out = append(out, bt)
		}
		rows = append(rows, out)
	}
	sc.kb = keyboard(rows...)
	return sc
}

// onStart: /start создаёт пользователя, но фильтры не сбрасывает (D-17).
func (b *Bot) onStart(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	if u.Message == nil || u.Message.From == nil {
		return
	}
	_, v, err := b.Store.Filters(ctx, u.Message.From.ID)
	if err != nil {
		b.logger().Error("start", "err", err)
		return
	}
	sc := screen{text: "🏃 <b>КАТАЛОГ ЗАБЕГОВ</b>\n\nНайди свой следующий старт!", kb: keyboard(btn("🔎 Найти забег", back))}
	if err := b.send(ctx, u.Message.Chat.ID, stamp(sc, v)); err != nil {
		b.logger().Warn("send start", "err", err)
	}
}

const (
	errNotice        = "Что-то пошло не так, попробуйте ещё раз"
	cityNeedsCountry = "Сначала выберите страну"
)

// onFilter обрабатывает f:*: изменяет фильтры (если кнопка их меняет) и
// показывает сводку либо подменю.
func (b *Bot) onFilter(ctx context.Context, q *models.CallbackQuery, c callback) string {
	var (
		f   userFilters
		v   int
		err error
	)
	mut := mutation(c)
	if mut != nil {
		f, v, err = b.updateAt(ctx, q.From.ID, c, mut)
		if errors.Is(err, store.ErrStaleFilters) {
			return staleNotice
		}
	} else {
		var raw []byte
		raw, v, _, err = b.cbState(ctx, q.From.ID, c)
		f = loadFilters(raw, b.today())
	}
	if err != nil {
		b.logger().Error("filters", "err", err)
		return errNotice
	}
	// Подменю корзин остаётся на экране и после выбора; остальные значения
	// возвращают к сводке. Город при «всех странах» недоступен: вместо него —
	// выбор страны.
	var (
		sc     screen
		notice string
	)
	switch {
	case c.Kind == cbCity && f.Country == allCountries:
		sc, err = b.countryMenu(ctx, f)
		notice = cityNeedsCountry
	case c.Kind == cbBucket:
		sc = bucketMenu(f)
	case mut != nil || c.Kind == cbSummary:
		sc, err = b.summary(ctx, f, v)
	case c.Kind == cbDate:
		sc = dateMenu(b.today())
	case c.Kind == cbCity:
		sc, err = b.cityMenu(ctx, f)
	case c.Kind == cbType:
		sc, err = b.typeMenu(ctx, f)
	case c.Kind == cbCountry:
		sc, err = b.countryMenu(ctx, f)
	}
	if err != nil {
		b.logger().Error("filter screen", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, stamp(sc, v)); err != nil {
		b.logger().Warn("show filters", "err", err)
	}
	return notice
}

const maxCityInput = 64

// onText: любое текстовое сообщение не-команда в личном чате — запрос
// города (без состояния «жду ввод»). Текст в лог не пишется (BOT-08).
func (b *Bot) onText(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	v := 0 // версия фильтров для кнопок ответа; известна после чтения фильтров
	say := func(sc screen) {
		if err := b.send(ctx, m.Chat.ID, stamp(sc, v)); err != nil {
			b.logger().Warn("send text reply", "err", err)
		}
	}
	text := strings.TrimSpace(m.Text)
	if text == "" || utf8.RuneCountInString(text) > maxCityInput || strings.HasPrefix(text, "/") {
		say(screen{text: "Нажмите /start, чтобы найти забег", plain: true})
		return
	}
	key := normalize.CityKey(text)
	if key == "" || !validCityKey(key) {
		say(screen{text: "Не понял название города — попробуйте иначе или выберите из списка", plain: true})
		return
	}
	today := b.today()
	// Город ищется в стране фильтров пользователя: одинаковый ключ в двух
	// странах — разные города.
	raw, v, err := b.Store.Filters(ctx, m.From.ID)
	if err != nil {
		b.logger().Error("city text", "err", err)
		say(screen{text: errNotice, plain: true})
		return
	}
	uf := loadFilters(raw, today)
	if uf.Country == allCountries {
		say(screen{text: cityNeedsCountry, plain: true, kb: keyboard(btn("🌍 Страна", callback{Kind: cbCountry}))})
		return
	}
	country := uf.countryCode()
	_, n, err := b.Store.CityName(ctx, country, key, today)
	if err != nil {
		b.logger().Error("city text", "err", err)
		say(screen{text: errNotice, plain: true})
		return
	}
	b.logger().Info("city text", "found", n > 0)
	if n > 0 {
		var f userFilters
		f, v, err = b.update(ctx, m.From.ID, func(f userFilters) userFilters { return f.setCity(key) })
		var sc screen
		if err == nil {
			sc, err = b.summary(ctx, f, v)
		}
		if err != nil {
			b.logger().Error("city text", "err", err)
			say(screen{text: errNotice, plain: true})
			return
		}
		say(sc)
		return
	}
	similar, err := b.Store.SimilarCities(ctx, country, key, today, 5)
	if err != nil {
		b.logger().Error("similar cities", "err", err)
		say(screen{text: errNotice, plain: true})
		return
	}
	var rows []row
	for _, c := range similar {
		rows = append(rows, btn(fmt.Sprintf("%s (%d)", c.Name, c.N), callback{Kind: cbCity, Value: c.Key}))
	}
	rows = append(rows, btn("⬅️ Назад", back))
	say(screen{text: "В каталоге нет забегов в «" + html.EscapeString(text) + "»", kb: keyboard(rows...)})
}
