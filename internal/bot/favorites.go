package bot

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

// Избранное (Phase 14, 14-UX-CONTRACT.md): кнопки w:, раздел и строка
// избранного в карточке. Кнопки w: версии фильтров не несут и задают
// состояние, а не переключают его: повторное нажатие и кнопка из старого
// сообщения ничего не ломают. Тексты — дословно из контракта.
const (
	favLimit = 50 // предел будущих избранных стартов; число есть и в favLimitNotice

	favAddLabel = "⭐ В избранное"
	favDelLabel = "✖️ Убрать из избранного"

	favAbout     = "Сообщу, если у старта появится ссылка или изменится дата, если старт пропадёт из календаря, и напомню за 7 дней до старта."
	favEmptyText = "⭐ <b>Избранное</b>\n\nЗдесь пока пусто. Откройте карточку старта и нажмите «⭐ В избранное» — сообщу, если у старта появится ссылка или изменится дата, если старт пропадёт из календаря, и напомню за 7 дней до старта."

	favAddedNotice   = "Добавлено в избранное. Сообщу об изменениях и напомню за 7 дней до старта"
	favAlreadyNotice = "Уже в избранном"
	favRemovedNotice = "Убрано из избранного"
	favLimitNotice   = "В избранном уже 50 стартов — это предел. Уберите лишний, чтобы добавить новый"
	favPastNotice    = "Старт уже прошёл"
)

// favAdded — исход добавления: слово для лога и уведомление.
var favAdded = map[store.FavOutcome]struct{ outcome, notice string }{
	store.FavAdded:   {"added", favAddedNotice},
	store.FavAlready: {"already", favAlreadyNotice},
	store.FavLimit:   {"limit", favLimitNotice},
	store.FavPast:    {"past", favPastNotice},
	store.FavGone:    {"gone", goneNotice},
}

// onFav обрабатывает w:*: раздел, карточку из раздела и из сообщения бота,
// добавление и удаление.
func (b *Bot) onFav(ctx context.Context, q *models.CallbackQuery, c callback) string {
	switch c.Kind {
	case cbFavOpen:
		return b.favSection(ctx, q, 1)
	case cbFavPage:
		return b.favSection(ctx, q, c.Page)
	case cbFavCard, cbFavShow:
		return b.favCard(ctx, q, c)
	case cbFavAdd, cbFavDel:
		return b.favSet(ctx, q, c)
	}
	return staleNotice
}

// favSection показывает страницу раздела правкой сообщения; страница за
// пределами зажимается до последней, как в списке. В базу не пишет.
func (b *Bot) favSection(ctx context.Context, q *models.CallbackQuery, page int) string {
	today := b.today()
	items, total, err := b.Store.ListFavorites(ctx, q.From.ID, today, pageSize, (page-1)*pageSize)
	if err == nil && len(items) == 0 && total > 0 {
		page = (total + pageSize - 1) / pageSize
		items, total, err = b.Store.ListFavorites(ctx, q.From.ID, today, pageSize, (page-1)*pageSize)
	}
	if err != nil {
		b.logger().Error("favorite", "action", "list", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, favScreen(items, total, page, today.Year())); err != nil {
		b.logger().Warn("show favorites", "err", err)
	}
	return ""
}

// favScreen — раздел «Избранное»: старты кнопками, как в списке (флаг страны
// перед городом всегда, у небегового старта значок вида спорта, как в
// результатах запроса), навигация и возврат в главное меню; total = 0 — пустой
// раздел с подсказкой.
func favScreen(items []store.ListItem, total, page, year int) screen {
	toMenu := btn(backLabel, callback{Kind: cbMenu})
	if total == 0 {
		return screen{text: favEmptyText, kb: keyboard(toMenu)}
	}
	pages := (total + pageSize - 1) / pageSize
	rows := make([]row, 0, len(items)+2)
	for _, it := range items {
		if sp, ok := event.SportByCode(it.Sport); ok && sp.Code != event.SportRunning {
			it.Title = sp.Emoji + " " + it.Title
		}
		rows = append(rows, eventBtn(it, callback{Kind: cbFavCard, ID: it.ID, Page: page}, true, year))
	}
	var nav row
	if page > 1 {
		nav.add("←", callback{Kind: cbFavPage, Page: page - 1})
	}
	nav.add(fmt.Sprintf("%d/%d", page, pages), callback{Kind: cbFavPage, Page: page})
	if page < pages {
		nav.add("→", callback{Kind: cbFavPage, Page: page + 1})
	}
	return screen{text: fmt.Sprintf("⭐ <b>Избранное: %d</b>\n\n%s", total, favAbout), kb: keyboard(append(rows, nav, toMenu)...)}
}

// favCard показывает карточку старта с возвратом в раздел: w:e — правкой
// сообщения, на страницу кнопки; w:n — новым сообщением (сообщение с кнопкой
// не правится), на первую страницу. Недоступный старт — «Событие больше не
// доступно» с тем же возвратом. Корень слитого события находит buildCard
// (Store.RootEventID). В базу не пишет: Legacy — версия фильтров только
// читается.
func (b *Bot) favCard(ctx context.Context, q *models.CallbackQuery, c callback) string {
	sc, _, ok := b.buildCard(ctx, q.From.ID, callback{Kind: cbCard, ID: c.ID, Page: 1, Legacy: true})
	if !ok {
		return errNotice
	}
	page := 1
	if c.Kind == cbFavCard {
		page = c.Page
	}
	rows := sc.kb.InlineKeyboard
	rows[len(rows)-1] = btn(backLabel, callback{Kind: cbFavPage, Page: page})
	var err error
	if c.Kind == cbFavShow {
		err = b.send(ctx, q.From.ID, sc)
	} else {
		err = b.show(ctx, q, sc)
	}
	if err != nil {
		b.logger().Warn("show card", "err", err)
	}
	return ""
}

// favSet добавляет старт в избранное (w:a) либо убирает (w:x), отвечает
// уведомлением и правит клавиатуру сообщения нажатия (favSwap); текст
// сообщения не трогает. Отказ в добавлении клавиатуру не правит. Убрать можно
// и скрытый, и прошедший старт. В лог идут только действие и исход — без
// пользователя, события и названия.
func (b *Bot) favSet(ctx context.Context, q *models.CallbackQuery, c callback) string {
	add := c.Kind == cbFavAdd
	action, outcome, notice := "remove", "removed", favRemovedNotice
	if add {
		action = "add"
		res, err := b.Store.AddFavorite(ctx, q.From.ID, c.ID, b.today(), b.now(), favLimit)
		if err != nil {
			b.logger().Error("favorite", "action", action, "err", err)
			return errNotice
		}
		outcome, notice = favAdded[res].outcome, favAdded[res].notice
		if res == store.FavAdded {
			b.count(ctx, store.MetricFavBot)
		}
		if res != store.FavAdded && res != store.FavAlready {
			b.logger().Info("favorite", "action", action, "outcome", outcome)
			return notice
		}
	} else if err := b.Store.RemoveFavorite(ctx, q.From.ID, c.ID); err != nil {
		b.logger().Error("favorite", "action", action, "err", err)
		return errNotice
	}
	b.logger().Info("favorite", "action", action, "outcome", outcome)
	if kb := favSwap(q, c.ID, add); kb != nil {
		if err := b.showKeyboard(ctx, q.Message.Message.Chat.ID, q.Message.Message.ID, kb); err != nil {
			b.logger().Warn("show keyboard", "err", err)
		}
	}
	return notice
}

// favBtn — кнопка избранного события id: «убрать», если старт в избранном,
// иначе «добавить». Подписи одни и те же в карточке и в сообщении бота.
func favBtn(id int64, fav bool) models.InlineKeyboardButton {
	text, c := favAddLabel, callback{Kind: cbFavAdd, ID: id}
	if fav {
		text, c = favDelLabel, callback{Kind: cbFavDel, ID: id}
	}
	data, _ := c.encode() // w:<a|x>:<id> — не длиннее 23 байт
	return models.InlineKeyboardButton{Text: text, CallbackData: data}
}

// favSwap — клавиатура сообщения нажатия, в которой кнопка избранного события
// id (w:a либо w:x, где бы она ни стояла) заменена кнопкой состояния fav;
// остальные кнопки и их порядок прежние, кнопка избранного другого события не
// трогается. nil — править нечего: сообщение недоступно, такой кнопки нет либо
// клавиатура уже такая.
func favSwap(q *models.CallbackQuery, id int64, fav bool) *models.InlineKeyboardMarkup {
	m := q.Message.Message
	if m == nil || m.ReplyMarkup == nil {
		return nil
	}
	was, want := favBtn(id, !fav), favBtn(id, fav)
	changed := false
	rows := make([][]models.InlineKeyboardButton, len(m.ReplyMarkup.InlineKeyboard))
	for i, r := range m.ReplyMarkup.InlineKeyboard {
		rows[i] = slices.Clone(r)
		for j, bt := range r {
			if bt.CallbackData == was.CallbackData || (bt.CallbackData == want.CallbackData && bt.Text != want.Text) {
				rows[i][j], changed = want, true
			}
		}
	}
	if !changed {
		return nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
