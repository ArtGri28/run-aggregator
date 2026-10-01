package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/normalize"
)

// userFilters — фильтры пользователя (users.filters, jsonb). Значения
// абсолютные; пустой объект — «все будущие даты, вся Россия, любая
// дистанция, любой тип».
type userFilters struct {
	Date string `json:"date,omitempty"` // "" и "a" — все будущие
	City string `json:"city,omitempty"` // city_key
	Dist []int  `json:"dist,omitempty"` // индексы buckets
	Type string `json:"type,omitempty"`
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

// unset снимает один фильтр: 'd' дата, 'c' город, 'b' дистанция, 't' тип.
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
		return func(f userFilters) userFilters { return f.setCity(v) }
	case c.Kind == cbType:
		return func(f userFilters) userFilters { return f.setType(v) }
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

// query переводит фильтры пользователя в параметры выдачи.
func (f userFilters) query(today time.Time) event.Filter {
	q := event.Filter{CityKey: f.City, Type: event.Type(f.Type)}
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

// summaryLine — строка сводки фильтров (HTML); cityName — написание города.
func summaryLine(f userFilters, cityName string) string {
	city := "вся Россия"
	if f.City != "" {
		city = html.EscapeString(cityName)
	}
	return "Дата: " + dateLabel(f.Date) + " · Город: " + city + " · Дистанция: " + distLabel(f.Dist) + " · Тип: " + typeLabel(f.Type)
}

// btn — ряд из одной кнопки.
func btn(text string, c callback) row {
	var r row
	r.add(text, c)
	return r
}

var back = callback{Kind: cbSummary}

// summaryScreen — сводка фильтров (D-10) с кнопкой «Показать N».
func summaryScreen(f userFilters, cityName string, v, n int) screen {
	var r1, r2 row
	r1.add("Дата", callback{Kind: cbDate})
	r1.add("Город", callback{Kind: cbCity})
	r2.add("Дистанция", callback{Kind: cbBucket})
	r2.add("Тип", callback{Kind: cbType})
	return screen{
		text: "<b>Найти забег</b>\n\n" + summaryLine(f, cityName),
		kb: keyboard(r1, r2,
			btn("Показать "+strconv.Itoa(n), callback{Kind: cbPage, Page: 1, V: v}),
			btn("Сбросить", callback{Kind: cbReset})),
	}
}

// emptyScreen — пустая выдача (D-12): кнопки снимают только заданные фильтры.
func emptyScreen(f userFilters) screen {
	var rows []row
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
	rows = append(rows, btn("Фильтры", back))
	return screen{text: "Ничего не нашлось", kb: keyboard(rows...)}
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
	return screen{text: "Выберите даты", kb: keyboard(append(rows, btn("Назад", back))...)}
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
	rows = append(rows, btn("Вся Россия", callback{Kind: cbCity, Value: "-"}), btn("Назад", back))
	return screen{text: "Выберите город или напишите его название сообщением", kb: keyboard(rows...)}, nil
}

// typeMenu — только типы, которые есть среди будущих событий (D-09).
func (b *Bot) typeMenu(ctx context.Context) (screen, error) {
	types, err := b.Store.EventTypes(ctx, b.today())
	if err != nil {
		return screen{}, err
	}
	var rows []row
	for _, t := range types {
		rows = append(rows, btn(typeLabel(string(t)), callback{Kind: cbType, Value: string(t)}))
	}
	rows = append(rows, btn("Любой", callback{Kind: cbType, Value: "-"}), btn("Назад", back))
	return screen{text: "Выберите тип забега", kb: keyboard(rows...)}, nil
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
		nm, cnt, err := b.Store.CityName(ctx, f.City, today)
		if err != nil {
			return screen{}, err
		}
		if cnt > 0 {
			name = nm
		}
	}
	return summaryScreen(f, name, v, n), nil
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

// onStart: /start создаёт пользователя, но фильтры не сбрасывает (D-17).
func (b *Bot) onStart(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	if u.Message == nil || u.Message.From == nil {
		return
	}
	if _, _, err := b.Store.Filters(ctx, u.Message.From.ID); err != nil {
		b.logger().Error("start", "err", err)
		return
	}
	sc := screen{text: "Привет! Помогу найти забег в России по дате, городу, дистанции и типу.",
		kb: keyboard(btn("Найти забег", back))}
	if err := b.send(ctx, u.Message.Chat.ID, sc); err != nil {
		b.logger().Warn("send start", "err", err)
	}
}

const errNotice = "Что-то пошло не так, попробуйте ещё раз"

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
		f, v, err = b.update(ctx, q.From.ID, mut)
	} else {
		var raw []byte
		raw, v, err = b.Store.Filters(ctx, q.From.ID)
		f = loadFilters(raw, b.today())
	}
	if err != nil {
		b.logger().Error("filters", "err", err)
		return errNotice
	}
	// Подменю корзин остаётся на экране и после выбора; остальные значения
	// возвращают к сводке.
	var sc screen
	switch {
	case c.Kind == cbBucket:
		sc = bucketMenu(f)
	case mut != nil || c.Kind == cbSummary:
		sc, err = b.summary(ctx, f, v)
	case c.Kind == cbDate:
		sc = dateMenu(b.today())
	case c.Kind == cbCity:
		sc, err = b.cityMenu(ctx, f)
	case c.Kind == cbType:
		sc, err = b.typeMenu(ctx)
	}
	if err != nil {
		b.logger().Error("filter screen", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show filters", "err", err)
	}
	return ""
}

const maxCityInput = 64

// onText: любое текстовое сообщение не-команда в личном чате — запрос
// города (без состояния «жду ввод»). Текст в лог не пишется (BOT-08).
func (b *Bot) onText(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	say := func(sc screen) {
		if err := b.send(ctx, m.Chat.ID, sc); err != nil {
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
	_, n, err := b.Store.CityName(ctx, key, today)
	if err != nil {
		b.logger().Error("city text", "err", err)
		say(screen{text: errNotice, plain: true})
		return
	}
	b.logger().Info("city text", "found", n > 0)
	if n > 0 {
		f, v, err := b.update(ctx, m.From.ID, func(f userFilters) userFilters { return f.setCity(key) })
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
	similar, err := b.Store.SimilarCities(ctx, key, today, 5)
	if err != nil {
		b.logger().Error("similar cities", "err", err)
		say(screen{text: errNotice, plain: true})
		return
	}
	var rows []row
	for _, c := range similar {
		rows = append(rows, btn(fmt.Sprintf("%s (%d)", c.Name, c.N), callback{Kind: cbCity, Value: c.Key}))
	}
	rows = append(rows, btn("Назад", back))
	say(screen{text: "В каталоге нет забегов в «" + html.EscapeString(text) + "»", kb: keyboard(rows...)})
}
