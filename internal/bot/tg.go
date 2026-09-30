package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// send отправляет экран новым сообщением.
func (b *Bot) send(ctx context.Context, chatID int64, sc screen) error {
	p := &tgbot.SendMessageParams{ChatID: chatID, Text: sc.text, LinkPreviewOptions: linkPreviewOff()}
	if !sc.plain {
		p.ParseMode = models.ParseModeHTML
	}
	if sc.kb != nil {
		p.ReplyMarkup = sc.kb
	}
	_, err := b.api.SendMessage(ctx, p)
	return err
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
// сообщение пользователю. «message is not modified» — успех.
func (b *Bot) show(ctx context.Context, q *models.CallbackQuery, sc screen) error {
	chatID, msgID, ok := msgRef(q)
	if !ok {
		return b.send(ctx, q.From.ID, sc)
	}
	p := &tgbot.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: sc.text, LinkPreviewOptions: linkPreviewOff()}
	if !sc.plain {
		p.ParseMode = models.ParseModeHTML
	}
	if sc.kb != nil {
		p.ReplyMarkup = sc.kb
	}
	_, err := b.api.EditMessageText(ctx, p)
	if errors.Is(err, tgbot.ErrorBadRequest) && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("edit message: %w", err)
	}
	return nil
}

// answer отвечает на callback (текст — всплывающее уведомление); ошибка
// только в лог.
func (b *Bot) answer(ctx context.Context, q *models.CallbackQuery, notice string) {
	_, err := b.api.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: notice})
	if err != nil {
		b.logger().Warn("answer callback", "err", err)
	}
}
