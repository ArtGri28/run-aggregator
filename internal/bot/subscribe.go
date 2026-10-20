package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

// Подписка на новые старты (Phase 20, 20-UX-CONTRACT.md N-3…N-7): копия
// фильтров поиска без дат, одна на пользователя; раз в неделю сообщение о
// стартах, появившихся после прошлого сообщения.

const (
	subOnNotice  = "🔔 Подписка включена. Новые старты — по понедельникам"
	subOffNotice = "🔕 Подписка отключена"
	subLine      = "🔔 Подписка на новые старты: включена"
	subOldMark   = " (по прежним фильтрам)"
	// subAbout — как работает подписка: на экране без подписки и с ней.
	subOffAbout = "🔔 Подписка на новые старты: раз в неделю, по понедельникам, бот пришлёт старты, которые появились в каталоге по этим фильтрам. Если новых не будет — сообщения не будет."
	subOnAbout  = "\nСообщение приходит по понедельникам после 10:00 по Москве — только если появились новые старты по этим фильтрам. Отписаться можно здесь или кнопкой в самом сообщении."
	digestHead  = "🔔 <b>Новые старты по вашей подписке</b>"
	// digestMax — стартов в сообщении подписки; остальные — в поиске (N-7).
	digestMax = 10
	// digestHour — час по Москве в понедельник, с которого создаются сообщения
	// подписки этой недели.
	digestHour = 10
)

// subCopy — фильтры для подписки: без дат, новые старты всегда будущие.
func subCopy(f userFilters) userFilters {
	f.Date, f.Dates = "", nil
	return f
}

// sameSub — копия фильтров raw (как в базе) совпадает с фильтрами f без дат.
func sameSub(raw []byte, f userFilters) bool {
	var sub userFilters
	if json.Unmarshal(raw, &sub) != nil {
		return false
	}
	a, _ := json.Marshal(sub)
	b, _ := json.Marshal(subCopy(f))
	return string(a) == string(b)
}

// withSub дополняет экран «Мои настройки» строкой и кнопками подписки:
// ряды подписки — над «✏️ Изменить фильтры». raw — копия фильтров подписки,
// nil — подписки нет.
func withSub(sc screen, f userFilters, raw []byte) screen {
	var add [][]models.InlineKeyboardButton
	switch {
	case raw == nil:
		sc.text += "\n\n" + subOffAbout
		add = append(add, btn("🔔 Подписаться на новые старты по этим фильтрам", callback{Kind: cbSubscribe}))
	case sameSub(raw, f):
		sc.text += "\n\n" + subLine + subOnAbout
		add = append(add, btn("🔕 Отписаться", callback{Kind: cbUnsubscribe, Value: fromSettings}))
	default:
		sc.text += "\n\n" + subLine + subOldMark + subOnAbout
		add = append(add, btn("🔔 Подписаться по этим фильтрам", callback{Kind: cbSubscribe}),
			btn("🔕 Отписаться", callback{Kind: cbUnsubscribe, Value: fromSettings}))
	}
	sc.kb.InlineKeyboard = slices.Insert(sc.kb.InlineKeyboard, 0, add...)
	return sc
}

// subscribe — s:b: подписка текущими фильтрами; только актуальной кнопкой.
func (b *Bot) subscribe(ctx context.Context, tgID int64, c callback) (userFilters, int, string, error) {
	raw, v, stale, err := b.cbState(ctx, tgID, c)
	if err != nil || stale {
		return userFilters{}, v, staleNotice, err
	}
	f := loadFilters(raw, b.today())
	data, err := json.Marshal(subCopy(f))
	if err != nil {
		return f, v, "", err
	}
	if err := b.Store.Subscribe(ctx, tgID, data, b.now()); err != nil {
		return f, v, "", err
	}
	b.logger().Info("subscription", "action", "on")
	return f, v, subOnNotice, nil
}

// unsubscribeDigest — s:u:d: отписка из сообщения подписки; у сообщения
// пропадает ряд «🔕 Отписаться», текст прежний.
func (b *Bot) unsubscribeDigest(ctx context.Context, q *models.CallbackQuery) string {
	if err := b.Store.Unsubscribe(ctx, q.From.ID); err != nil {
		b.logger().Error("subscription", "action", "off", "err", err)
		return errNotice
	}
	b.logger().Info("subscription", "action", "off")
	if m := q.Message.Message; m != nil && m.ReplyMarkup != nil {
		rows := slices.DeleteFunc(slices.Clone(m.ReplyMarkup.InlineKeyboard), func(r []models.InlineKeyboardButton) bool {
			return slices.ContainsFunc(r, func(bt models.InlineKeyboardButton) bool { return strings.HasPrefix(bt.CallbackData, "s:u:") })
		})
		if err := b.showKeyboard(ctx, m.Chat.ID, m.ID, &models.InlineKeyboardMarkup{InlineKeyboard: rows}); err != nil {
			b.logger().Warn("edit digest", "err", err)
		}
	}
	return subOffNotice
}

// subList — s:l: фильтры поиска заменяются копией из подписки (даты
// сбрасываются), первая страница выдачи приходит новым сообщением.
func (b *Bot) subList(ctx context.Context, q *models.CallbackQuery) string {
	raw, _, ok, err := b.Store.Subscription(ctx, q.From.ID)
	if err != nil {
		b.logger().Error("subscription", "err", err)
		return errNotice
	}
	if !ok {
		return staleNotice
	}
	var sub userFilters
	if json.Unmarshal(raw, &sub) != nil {
		return staleNotice
	}
	_, v, err := b.Store.UpdateFilters(ctx, q.From.ID, func([]byte) ([]byte, error) { return json.Marshal(subCopy(sub)) })
	if err != nil {
		b.logger().Error("subscription", "err", err)
		return errNotice
	}
	sc, err := b.listPage(ctx, loadFilters(raw, b.today()), v, 1)
	if err != nil {
		b.logger().Error("list", "err", err)
		return errNotice
	}
	if err := b.send(ctx, q.From.ID, stamp(sc, v)); err != nil {
		b.logger().Warn("send list", "err", err)
	}
	return ""
}

// digestWeek — ключ недели сообщения подписки (дата понедельника по Москве) и
// момент, с которого его создают: понедельник, digestHour по Москве.
func digestWeek(now time.Time, loc *time.Location) (string, time.Time) {
	l := now.In(loc)
	mon := time.Date(l.Year(), l.Month(), l.Day(), digestHour, 0, 0, 0, loc).AddDate(0, 0, -((int(l.Weekday()) + 6) % 7))
	return mon.Format(time.DateOnly), mon
}

// digest создаёт сообщения подписки этой недели: с понедельника digestHour по
// Москве, по одному на подписку, включённую раньше этого момента. Бот,
// не работавший в понедельник, создаёт их при первом шаге позже на неделе.
func (n *Notifier) digest(ctx context.Context, now time.Time) (int, error) {
	week, moment := digestWeek(now, n.b.loc)
	if now.Before(moment) {
		return 0, nil
	}
	return n.b.Store.EnqueueDigests(ctx, week, moment, now)
}

// prepareDigest собирает сообщение подписки: старты, появившиеся в каталоге
// после прошлого сообщения (или включения) и не позже now. Подписки нет —
// строка закрывается «убран»; новых стартов нет — «условие не выполняется».
func (n *Notifier) prepareDigest(ctx context.Context, row store.OutboxRow, now time.Time) (prepared, error) {
	b := n.b
	raw, since, ok, err := b.Store.Subscription(ctx, row.TgID)
	if err != nil {
		return prepared{}, err
	}
	if !ok {
		return prepared{close: store.OutboxUnfollowed}, nil
	}
	today := n.dayOf(now)
	f := loadFilters(raw, today)
	items, total, err := b.Store.NewEvents(ctx, f.query(today), since, now, digestMax)
	if err != nil {
		return prepared{}, err
	}
	if total == 0 {
		return prepared{close: store.OutboxStale}, nil
	}
	name, err := b.cityName(ctx, f)
	if err != nil {
		return prepared{}, err
	}
	return prepared{sc: digestMessage(f, name, items, total, today.Year())}, nil
}

// digestMessage — N-5: вид спорта и фильтры подписки, до digestMax стартов
// кнопками на карточки (новым сообщением), «Показать все» при большем числе и
// «Отписаться».
func digestMessage(f userFilters, cityName string, items []store.ListItem, total, year int) screen {
	sp := f.sport()
	var sb strings.Builder
	sb.WriteString(digestHead + "\n\n" + sp.Emoji + " " + sp.Title)
	if line := activeLine(f, cityName); line != "" {
		sb.WriteString("\n⚙️ Фильтры: " + line)
	}
	if total > len(items) {
		sb.WriteString(fmt.Sprintf("\nПоказаны %d из %d", len(items), total))
	}
	rows := make([]row, 0, len(items)+2)
	for _, it := range items {
		rows = append(rows, eventBtn(it, callback{Kind: cbCard, ID: it.ID}, f.Country == allCountries, year))
	}
	if total > len(items) {
		rows = append(rows, btn("🔎 Показать все в поиске", callback{Kind: cbSubList}))
	}
	rows = append(rows, btn("🔕 Отписаться", callback{Kind: cbUnsubscribe, Value: fromDigest}))
	return screen{text: sb.String(), kb: keyboard(rows...)}
}
