package bot

// Поддержка (Phase 11, 11-UX-CONTRACT.md): пользователь пишет в бота, обращение
// уходит одним сообщением бота в закрытую группу (Config.SupportChatID).
// Только текст (D-11): код ниже не отправляет вложения и не пересылает и не
// копирует сообщения пользователя — в группу идёт новое текстовое сообщение с
// шапкой. В базе только связка «сообщение бота в группе → автор» и отметка
// ожидания (D-13); текст обращения, имя и username в хранилище не передаются и
// в лог не пишутся (D-15): в лог идут вид события и вид ошибки Bot API.

import (
	"context"
	"errors"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

const (
	// awaitSupportFor — окно приёма обращения после входа в поддержку (D-08).
	awaitSupportFor = 10 * time.Minute
	// maxSupportText — предел обращения в единицах UTF-16, как считает Telegram:
	// сообщение вместе с шапкой заведомо помещается в предел Bot API (4096).
	maxSupportText = 2000
	// supportLimit обращений за supportWindow от одного пользователя (D-12).
	supportLimit  = 5
	supportWindow = time.Hour
)

// Тексты экранов поддержки — дословно из контракта; ни один не говорит о
// данных пользователя и о сроке ответа (D-17, D-19).
const (
	supportInvite    = "💬 <b>Поддержка</b>\n\nНапишите вопрос или опишите проблему одним текстовым сообщением. Следующее сообщение в течение 10 минут уйдёт в поддержку."
	supportConfirm   = "✅ Сообщение получено. Ответ придёт в этот чат."
	supportTooLong   = "Сообщение длиннее 2000 символов. Сократите его и отправьте ещё раз."
	supportNotText   = "В поддержку можно отправить только текст. Напишите сообщение текстом."
	supportLimited   = "Можно отправить не больше 5 обращений в час. Попробуйте позже."
	supportSendError = "Не удалось отправить сообщение. Попробуйте ещё раз позже."
)

// supportPrompt — H-1, приглашение написать обращение.
func supportPrompt() screen {
	return screen{text: supportInvite, kb: keyboard(btn("Отмена", callback{Kind: cbSupportCancel}))}
}

// onSupport обрабатывает h:*. Все три кнопки отвечают пустым уведомлением и не
// пишут в users; идентификатор пользователя — только q.From.ID.
//   - h:o (из меню): отметка и приглашение правкой сообщения меню;
//   - h:r («Ответить»): отметка и приглашение новым сообщением — сообщение с
//     ответом поддержки остаётся на месте;
//   - h:x («Отмена»): отметка снимается, сообщение правится на главное меню; кнопка
//     работает и на старом приглашении, когда отметки уже нет.
func (b *Bot) onSupport(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind == cbSupportCancel {
		if err := b.Store.SupportCancel(ctx, q.From.ID); err != nil {
			b.logger().Error("support cancel", "err", err)
			return errNotice
		}
		return b.onMenu(ctx, q, c)
	}
	if err := b.Store.SupportBegin(ctx, q.From.ID, b.now().Add(awaitSupportFor)); err != nil {
		b.logger().Error("support begin", "err", err)
		return errNotice
	}
	var err error
	if c.Kind == cbSupportReply {
		err = b.send(ctx, q.From.ID, supportPrompt())
	} else {
		err = b.show(ctx, q, supportPrompt())
	}
	if err != nil {
		b.logger().Warn("show support prompt", "kind", kindName(err))
	}
	return ""
}

// onSupportCmd — /support: отметка ожидания на окно и приглашение новым
// сообщением. users и профиль не пишутся: поддержка доступна и тому, кто не
// нажимал /start.
func (b *Bot) onSupportCmd(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if err := b.Store.SupportBegin(ctx, m.From.ID, b.now().Add(awaitSupportFor)); err != nil {
		b.logger().Error("support begin", "err", err)
		b.supportSay(ctx, m, errNotice)
		return
	}
	if err := b.send(ctx, m.Chat.ID, supportPrompt()); err != nil {
		b.logger().Warn("send support prompt", "kind", kindName(err))
	}
}

// supportText принимает текст как обращение, если стоит действующая отметка
// ожидания; true — сообщение обработано (принято, отказано или ошибка), в город
// проживания и в поиск оно не идёт. Первая проверка onText: обращение длиннее
// 64 символов и с переводами строк не должно упереться в пределы поиска.
// Поддержка выключена, нет отметки или она истекла, текст пуст либо начинается с
// «/» — false без записи. Отметку читают без записи, а потребляет SupportTake
// под блокировкой строки: из двух одновременных сообщений обращением становится
// одно, а окно не может истечь между проверкой и записью (D-09).
func (b *Bot) supportText(ctx context.Context, m *models.Message) bool {
	if b.supportChat == 0 {
		return false
	}
	until, ok, err := b.Store.SupportAwaiting(ctx, m.From.ID)
	if err != nil {
		b.logger().Error("support awaiting", "err", err)
		b.supportSay(ctx, m, errNotice)
		return true
	}
	if !ok || !until.After(b.now()) {
		return false
	}
	text := strings.TrimSpace(m.Text) // переводы строк внутри сохраняются
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	if len(utf16.Encode([]rune(text))) > maxSupportText {
		b.supportSay(ctx, m, supportTooLong) // отметка остаётся: можно сразу сократить (Q-4)
		return true
	}
	now := b.now()
	out, err := b.Store.SupportTake(ctx, m.From.ID, now, now.Add(-supportWindow), supportLimit)
	if err != nil {
		b.logger().Error("support take", "err", err)
		b.supportSay(ctx, m, errNotice)
		return true
	}
	switch out {
	case store.SupportNotWaiting: // отметку сняли, пока ждали блокировку
		return false
	case store.SupportLimited: // отметка потреблена: после лимита режим снят (Q-4)
		b.logger().Info("support", "event", "limited")
		b.supportSay(ctx, m, supportLimited)
		return true
	}
	b.supportSend(ctx, m, text)
	return true
}

// supportNonText отвечает отказом R-2 на сообщение без текста (фото, видео,
// документ, голосовое, стикер, сообщение альбома — вид не разбирается) в режиме
// обращения (D-11). Вне режима — без единого вызова: нетекстовые сообщения бот не
// замечает, как раньше. В группу ничего не уходит; отметка не снимается и окно не
// продлевается (Q-4): можно сразу написать текстом. Содержимое сообщения (подпись,
// идентификатор файла) никуда не передаётся и не логируется; альбом приходит по
// одному сообщению и получает по отказу, память о предыдущих не ведётся.
func (b *Bot) supportNonText(ctx context.Context, m *models.Message) {
	if m.From == nil {
		return
	}
	until, ok, err := b.Store.SupportAwaiting(ctx, m.From.ID)
	if err != nil {
		b.logger().Error("support awaiting", "err", err)
		return
	}
	if !ok || !until.After(b.now()) {
		return
	}
	b.supportSay(ctx, m, supportNotText)
}

// supportSend отправляет принятое обращение в группу и отвечает пользователю.
// Связка записывается только после успешной отправки; сбой записи связки не
// отменяет подтверждение: обращение уже в группе (в лог — ошибка хранилища).
func (b *Bot) supportSend(ctx context.Context, m *models.Message, text string) {
	var sent *models.Message
	err := withRetry(ctx, func() error {
		var err error
		sent, err = b.api.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:             b.supportChat,
			Text:               supportHeader(m.From) + html.EscapeString(text),
			ParseMode:          models.ParseModeHTML,
			LinkPreviewOptions: linkPreviewOff(),
		})
		return err
	})
	if err != nil {
		b.supportFailed(ctx, m, err)
		return
	}
	if err := b.Store.SupportLink(ctx, b.supportChat, sent.ID, m.From.ID, b.now()); err != nil {
		b.logger().Error("support link", "err", err)
	}
	b.logger().Info("support", "event", "sent")
	b.supportSay(ctx, m, supportConfirm)
}

// supportFailed — сбой отправки в группу: отказ вместо подтверждения, связки
// нет, отметка возвращается на окно (Q-4). В лог — только вид ошибки; при
// переносе группы в супергруппу — ещё новый идентификатор группы (не данные
// пользователя): бот за ним не следует, переменную меняет владелец.
func (b *Bot) supportFailed(ctx context.Context, m *models.Message, err error) {
	var mig *tgbot.MigrateError
	if errors.As(err, &mig) {
		b.logger().Error("support group migrated", "new_chat_id", mig.MigrateToChatID)
	} else {
		b.logger().Warn("support send failed", "kind", kindName(err))
	}
	if err := b.Store.SupportBegin(ctx, m.From.ID, b.now().Add(awaitSupportFor)); err != nil {
		b.logger().Error("support begin", "err", err)
	}
	b.supportSay(ctx, m, supportSendError)
}

// supportSay отвечает пользователю обычным текстом без клавиатуры.
func (b *Bot) supportSay(ctx context.Context, m *models.Message, text string) {
	if err := b.send(ctx, m.Chat.ID, screen{text: text, plain: true}); err != nil {
		b.logger().Warn("send support reply", "kind", kindName(err))
	}
}

// supportHeader — шапка G-1: имя, фамилия, username (если есть) и числовой id
// автора из сообщения в момент отправки; нигде не сохраняется. Каждая часть
// экранируется — разметка только у самой шапки.
func supportHeader(u *models.User) string {
	var parts []string
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		parts = append(parts, html.EscapeString(name))
	}
	if u.Username != "" {
		parts = append(parts, "@"+html.EscapeString(u.Username))
	}
	parts = append(parts, "id <code>"+strconv.FormatInt(u.ID, 10)+"</code>")
	return "💬 <b>Обращение</b>\nОт: " + strings.Join(parts, " · ") + "\n\n"
}

// kindName — вид ошибки Bot API для лога: единственное, что о ней пишется
// (текст ошибки Telegram в лог не идёт, D-15).
func kindName(err error) string {
	switch kind, _ := classify(err); kind {
	case kindBlocked:
		return "blocked"
	case kindRateLimited:
		return "rate limited"
	case kindBadRequest:
		return "bad request"
	}
	return "other"
}
