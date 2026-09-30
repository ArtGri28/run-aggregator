package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// screen — готовое сообщение: текст (HTML, если не plain) и клавиатура.
type screen struct {
	text  string
	kb    *models.InlineKeyboardMarkup
	plain bool // без parse_mode: админские сообщения
}

// row — ряд inline-кнопок.
type row []models.InlineKeyboardButton

// add добавляет кнопку с callback_data; не влезающая в 64 байта пропускается.
func (r *row) add(text string, c callback) {
	data, err := c.encode()
	if err != nil {
		return
	}
	*r = append(*r, models.InlineKeyboardButton{Text: text, CallbackData: data})
}

// keyboard собирает клавиатуру из непустых рядов.
func keyboard(rows ...row) *models.InlineKeyboardMarkup {
	kb := &models.InlineKeyboardMarkup{}
	for _, r := range rows {
		if len(r) > 0 {
			kb.InlineKeyboard = append(kb.InlineKeyboard, r)
		}
	}
	return kb
}

func linkPreviewOff() *models.LinkPreviewOptions {
	return &models.LinkPreviewOptions{IsDisabled: tgbot.True()}
}

// errKind — вид ошибки Bot API; разбирает её только classify.
type errKind int

const (
	kindNone errKind = iota
	kindNotModified
	kindBlocked
	kindRateLimited
	kindBadRequest
	kindOther
)

// maxRetryWait — предел ожидания retry_after перед единственным повтором.
var maxRetryWait = 30 * time.Second

// classify — единственное место разбора ошибок Bot API. Для 429 возвращает
// retry_after. Библиотечная проверка 429 не используется: она приводит тип
// напрямую и не разворачивает обёрнутые ошибки.
func classify(err error) (errKind, time.Duration) {
	var tm *tgbot.TooManyRequestsError
	switch {
	case err == nil:
		return kindNone, 0
	case errors.As(err, &tm):
		return kindRateLimited, time.Duration(tm.RetryAfter) * time.Second
	case errors.Is(err, tgbot.ErrorForbidden):
		return kindBlocked, 0
	case errors.Is(err, tgbot.ErrorBadRequest) && strings.Contains(err.Error(), "message is not modified"):
		return kindNotModified, 0
	case errors.Is(err, tgbot.ErrorBadRequest):
		return kindBadRequest, 0
	}
	return kindOther, 0
}

// withRetry вызывает fn; при 429 ждёт retry_after (не дольше maxRetryWait,
// с учётом ctx) и вызывает ещё раз. Повтор один.
func withRetry(ctx context.Context, fn func() error) error {
	err := fn()
	kind, wait := classify(err)
	if kind != kindRateLimited {
		return err
	}
	t := time.NewTimer(min(wait, maxRetryWait))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return err
	case <-t.C:
		return fn()
	}
}

// dropURLButtons возвращает экран без URL-кнопок; false — таких кнопок не было.
func dropURLButtons(sc screen) (screen, bool) {
	if sc.kb == nil {
		return sc, false
	}
	var rows []row
	found := false
	for _, r := range sc.kb.InlineKeyboard {
		var keep row
		for _, bt := range r {
			if bt.URL != "" {
				found = true
			} else {
				keep = append(keep, bt)
			}
		}
		rows = append(rows, keep)
	}
	if !found {
		return sc, false
	}
	sc.kb = nil
	if kb := keyboard(rows...); len(kb.InlineKeyboard) > 0 {
		sc.kb = kb
	}
	return sc, true
}

// attempt выполняет вызов Bot API с повтором после 429 и, если Telegram
// отверг экран с URL-кнопками (400), повтором без них.
func attempt(ctx context.Context, sc screen, call func(screen) error) error {
	err := withRetry(ctx, func() error { return call(sc) })
	if kind, _ := classify(err); kind == kindBadRequest {
		if plain, ok := dropURLButtons(sc); ok {
			return withRetry(ctx, func() error { return call(plain) })
		}
	}
	return err
}

// send отправляет экран новым сообщением. Заблокировавший бота пользователь
// (403) — запись в лог, без повтора.
func (b *Bot) send(ctx context.Context, chatID int64, sc screen) error {
	err := attempt(ctx, sc, func(sc screen) error {
		p := &tgbot.SendMessageParams{ChatID: chatID, Text: sc.text, LinkPreviewOptions: linkPreviewOff()}
		if !sc.plain {
			p.ParseMode = models.ParseModeHTML
		}
		if sc.kb != nil {
			p.ReplyMarkup = sc.kb
		}
		_, err := b.api.SendMessage(ctx, p)
		return err
	})
	b.logKind(err, "sendMessage")
	return err
}

// logKind пишет в лог вид ошибки и метод; текст экрана и данные пользователя
// не логируются.
func (b *Bot) logKind(err error, method string) {
	if kind, _ := classify(err); kind == kindBlocked {
		b.logger().Info("bot blocked by user", "method", method)
	}
}

// msgRef возвращает чат и id сообщения, к которому привязан callback
// (у старых сообщений Message == nil, есть только InaccessibleMessage).
func msgRef(q *models.CallbackQuery) (chatID int64, msgID int, ok bool) {
	if m := q.Message.Message; m != nil {
		return m.Chat.ID, m.ID, true
	}
	if m := q.Message.InaccessibleMessage; m != nil {
		return m.Chat.ID, m.MessageID, true
	}
	return 0, 0, false
}

// show правит сообщение, к которому привязан callback; без него — новое
// сообщение пользователю. «message is not modified» — успех; если сообщение
// править нельзя (любая ошибка 400), экран уходит новым сообщением.
func (b *Bot) show(ctx context.Context, q *models.CallbackQuery, sc screen) error {
	chatID, msgID, ok := msgRef(q)
	if !ok {
		return b.send(ctx, q.From.ID, sc)
	}
	err := attempt(ctx, sc, func(sc screen) error {
		p := &tgbot.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: sc.text, LinkPreviewOptions: linkPreviewOff()}
		if !sc.plain {
			p.ParseMode = models.ParseModeHTML
		}
		if sc.kb != nil {
			p.ReplyMarkup = sc.kb
		}
		_, err := b.api.EditMessageText(ctx, p)
		return err
	})
	switch kind, _ := classify(err); kind {
	case kindNone, kindNotModified:
		return nil
	case kindBadRequest:
		return b.send(ctx, chatID, sc)
	}
	b.logKind(err, "editMessageText")
	return fmt.Errorf("edit message: %w", err)
}

// answer отвечает на callback (текст — всплывающее уведомление); ошибка
// (например, устаревший callback) только в лог уровня Debug.
func (b *Bot) answer(ctx context.Context, q *models.CallbackQuery, notice string) {
	_, err := b.api.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: notice})
	if err != nil {
		b.logger().Debug("answer callback", "err", err)
	}
}
