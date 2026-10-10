package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

// Откуда пришли на подтверждение сброса: туда же возвращают «Назад» и
// результат сброса.
const (
	fromSettings = "o"
	fromSearch   = "f"
)

const resetNotice = "Настройки сброшены"

// settingsScreen — «Мои настройки»: сохранённые параметры поиска, только
// существующие данные users.filters. Страна показывается всегда. При выбранном
// небеговом виде спорта — строка вида спорта, его фильтры и нейтральные слова.
func settingsScreen(f userFilters, cityName string) screen {
	active, sport := "🔎 Фильтры не заданы — показываются все будущие забеги", ""
	if !f.running() {
		sp := f.sport()
		active, sport = "🔎 Фильтры не заданы — показываются все будущие старты", sp.Emoji+" Вид спорта: "+sp.Title+"\n"
	}
	if n := f.activeCount(); n > 0 {
		active = fmt.Sprintf("🔎 Активно фильтров: %d из 5", n)
	}
	return screen{
		text: "⚙️ <b>Мои настройки</b>\n\n" + sport + summaryLine(f, cityName, true) + "\n\n" + active,
		kb: keyboard(
			btn("✏️ Изменить фильтры", back),
			btn("🔄 Сбросить настройки", callback{Kind: cbResetAsk, Value: fromSettings}),
			btn(backLabel, callback{Kind: cbMenu})),
	}
}

// resetConfirmScreen — подтверждение сброса; «Назад» отменяет и возвращает
// туда, откуда пришли. running — выбран бег: текст прежний.
func resetConfirmScreen(from string, running bool) screen {
	cancel := callback{Kind: cbSettings}
	if from == fromSearch {
		cancel = back
	}
	kind := "любой тип"
	if !running {
		kind = "любая дисциплина"
	}
	return screen{
		text: "Сбросить настройки поиска?\n\nСтанет: страна по умолчанию, все города, все будущие даты, любая дистанция, " + kind + ".",
		kb:   keyboard(btn("✅ Да, сбросить", callback{Kind: cbResetYes, Value: from}), btn(backLabel, cancel)),
	}
}

// onSettings обрабатывает s:*: экран настроек, подтверждение сброса и сам
// сброс. Сброс — изменение данных: только актуальной кнопкой.
func (b *Bot) onSettings(ctx context.Context, q *models.CallbackQuery, c callback) string {
	var (
		f      userFilters
		v      int
		err    error
		notice string
	)
	if c.Kind == cbResetYes {
		f, v, err = b.updateAt(ctx, q.From.ID, c, userFilters.reset)
		if errors.Is(err, store.ErrStaleFilters) {
			return staleNotice
		}
		notice = resetNotice
	} else {
		var raw []byte
		raw, v, _, err = b.cbState(ctx, q.From.ID, c)
		f = loadFilters(raw, b.today())
	}
	if err != nil {
		b.logger().Error("settings", "err", err)
		return errNotice
	}
	var sc screen
	switch {
	case c.Kind == cbResetAsk:
		sc = resetConfirmScreen(c.Value, f.running())
	case c.Kind == cbResetYes && c.Value == fromSearch:
		sc, err = b.summary(ctx, f, v)
	default:
		var name string
		if name, err = b.cityName(ctx, f); err == nil {
			sc = settingsScreen(f, name)
		}
	}
	if err != nil {
		b.logger().Error("settings screen", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, stamp(sc, v)); err != nil {
		b.logger().Warn("show settings", "err", err)
	}
	return notice
}
