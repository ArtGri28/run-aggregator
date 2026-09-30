package bot

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

const pageSize = 5

// onList показывает страницу списка (p:<стр>:v<N>). Версия фильтров
// устарела — вместо страницы кнопки открывается первая страница
// актуальной выдачи.
func (b *Bot) onList(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind != cbPage {
		return staleNotice
	}
	raw, v, err := b.Store.Filters(ctx, q.From.ID)
	if err != nil {
		b.logger().Error("filters", "err", err)
		return errNotice
	}
	f := loadFilters(raw)
	page, notice := c.Page, ""
	if c.V != v {
		page, notice = 1, "Фильтры изменились — показываю актуальную выдачу"
	}
	flt := f.query(b.today())
	total, err := b.Store.CountEvents(ctx, flt)
	if err != nil {
		b.logger().Error("count", "err", err)
		return errNotice
	}
	sc := emptyScreen(f, v)
	if total > 0 {
		items, err := b.Store.ListEvents(ctx, flt, pageSize, (page-1)*pageSize)
		if err != nil {
			b.logger().Error("list", "err", err)
			return errNotice
		}
		sc = listScreen(items, total, page, v)
	}
	if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show list", "err", err)
	}
	return notice
}

// listScreen — страница списка: строка на событие и навигация. Кнопки
// карточек и пометки статуса добавит 04-03.
func listScreen(items []store.ListItem, total, page, v int) screen {
	pages := (total + pageSize - 1) / pageSize
	var sb strings.Builder
	fmt.Fprintf(&sb, "Найдено %d · страница %d/%d\n", total, page, pages)
	for _, it := range items {
		city := it.City
		if city == "" {
			city = "—"
		}
		fmt.Fprintf(&sb, "\n%s · %s · %s", it.StartDate.Format("02.01"), html.EscapeString(city), html.EscapeString(it.Title))
		if km := kmList(it.Distances); km != "" {
			sb.WriteString(" · " + km)
		}
	}
	var nav row
	if page > 1 {
		nav.add("←", callback{Kind: cbPage, Page: page - 1, V: v})
	}
	nav.add(fmt.Sprintf("стр. %d/%d", page, pages), callback{Kind: cbPage, Page: page, V: v})
	if page < pages {
		nav.add("→", callback{Kind: cbPage, Page: page + 1, V: v})
	}
	var filters row
	filters.add("Фильтры", callback{Kind: cbSummary})
	return screen{text: sb.String(), kb: keyboard(nav, filters)}
}

// kmList — «5/10/21 км»; "" без дистанций.
func kmList(meters []int) string {
	if len(meters) == 0 {
		return ""
	}
	parts := make([]string, len(meters))
	for i, m := range meters {
		if m%1000 == 0 {
			parts[i] = strconv.Itoa(m / 1000)
		} else {
			parts[i] = strings.Replace(strconv.FormatFloat(float64(m)/1000, 'f', 1, 64), ".", ",", 1)
		}
	}
	return strings.Join(parts, "/") + " км"
}
