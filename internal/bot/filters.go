package bot

import (
	"context"
	"encoding/json"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
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
	event.TypeRoad:     "шоссе",
	event.TypeTrail:    "трейл",
	event.TypeCross:    "кросс",
	event.TypeTrack:    "стадион",
	event.TypeObstacle: "с препятствиями",
	event.TypeOther:    "другое",
}

// loadFilters терпима к испорченным данным: ошибка разбора — значения по
// умолчанию, лишние ключи игнорируются, значения вне грамматики кодека
// отбрасываются.
func loadFilters(raw []byte) userFilters {
	var f userFilters
	if json.Unmarshal(raw, &f) != nil {
		return userFilters{}
	}
	if f.Date != "" && !validDate(f.Date) {
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

// dateRange — диапазон дат пресета. Пока только значение по умолчанию
// (все будущие); остальные пресеты добавляет план 04-02.
func dateRange(preset string, today time.Time) (from, to time.Time) {
	return today, time.Time{}
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

// summaryScreen — сводка фильтров с кнопкой «Показать N». Подменю
// добавляет 04-02. cityName — подпись выбранного города.
func summaryScreen(f userFilters, cityName string, v, n int) screen {
	date := "все будущие" // подписи остальных пресетов — 04-02
	city := "вся Россия"
	if f.City != "" {
		city = html.EscapeString(cityName)
	}
	dist := "любая"
	if len(f.Dist) > 0 {
		var l []string
		for _, i := range f.Dist {
			l = append(l, buckets[i].label)
		}
		dist = strings.Join(l, ", ")
	}
	typ := "любой"
	if f.Type != "" {
		typ = typeLabels[event.Type(f.Type)]
	}
	var r row
	r.add("Показать "+strconv.Itoa(n), callback{Kind: cbPage, Page: 1, V: v})
	return screen{
		text: "<b>Найти забег</b>\n\nДата: " + date + " · Город: " + city + " · Дистанция: " + dist + " · Тип: " + typ,
		kb:   keyboard(r),
	}
}

// emptyScreen — пустая выдача. Кнопки «снять фильтр» добавит 04-02.
func emptyScreen(f userFilters, v int) screen {
	var r row
	r.add("Фильтры", callback{Kind: cbSummary})
	return screen{text: "Ничего не нашлось", kb: keyboard(r)}
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
	var r row
	r.add("Найти забег", callback{Kind: cbSummary})
	sc := screen{text: "Привет! Помогу найти забег в России по дате, городу, дистанции и типу.", kb: keyboard(r)}
	if err := b.send(ctx, u.Message.Chat.ID, sc); err != nil {
		b.logger().Warn("send start", "err", err)
	}
}

const errNotice = "Что-то пошло не так, попробуйте ещё раз"

// onFilter обрабатывает f:*. В этом плане — только сводка (f:s).
func (b *Bot) onFilter(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind != cbSummary {
		return staleNotice // остальные экраны фильтров — 04-02
	}
	raw, v, err := b.Store.Filters(ctx, q.From.ID)
	if err != nil {
		b.logger().Error("filters", "err", err)
		return errNotice
	}
	f := loadFilters(raw)
	n, err := b.Store.CountEvents(ctx, f.query(b.today()))
	if err != nil {
		b.logger().Error("count", "err", err)
		return errNotice
	}
	// Название города по ключу добавит 04-02; выбрать город здесь пока нельзя.
	if err := b.show(ctx, q, summaryScreen(f, f.City, v, n)); err != nil {
		b.logger().Warn("show summary", "err", err)
	}
	return ""
}

// onText: текстовый ввод города добавит 04-02.
func (b *Bot) onText(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	if err := b.send(ctx, u.Message.Chat.ID, screen{text: "Нажмите /start, чтобы найти забег", plain: true}); err != nil {
		b.logger().Warn("send hint", "err", err)
	}
}
