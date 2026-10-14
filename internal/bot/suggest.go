package bot

// Предложение старта (Phase 16, 16-UX-CONTRACT.md): кнопка q:s на экранах
// «ничего не найдено» и /suggest, экраны P-1…P-5 и сообщение G-2. Следующее
// сообщение в окне отметки — ссылка; бот её не открывает, только проверяет
// формат и пересылает людям (deliver). Ссылка не пишется ни в базу, ни в лог:
// в базе только Telegram ID и время, в лог — вид события, исход и вид ошибки.

import (
	"context"
	"html"
	"strings"
	"time"
	"unicode/utf8"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

const (
	// awaitSuggestFor — окно приёма ссылки после приглашения (Q-7).
	awaitSuggestFor = 10 * time.Minute
	// maxSuggestURL — предел ссылки в символах.
	maxSuggestURL = 500
	// suggestLimit предложений за suggestWindow от одного пользователя (D-12).
	suggestLimit  = 3
	suggestWindow = 24 * time.Hour
)

// Тексты экранов предложения — дословно из контракта; ничего не обещают.
const (
	suggestLabel     = "💡 Предложить старт"
	suggestInvite    = "💡 <b>Предложить старт</b>\n\nНе нашли старт в каталоге? Пришлите ссылку на его страницу одним сообщением — ссылка должна начинаться с http:// или https://. Следующее сообщение в течение 10 минут будет принято как ссылка."
	suggestRefused   = "Нужна одна ссылка на страницу старта: начинается с http:// или https://, не длиннее 500 символов. Пришлите её ещё раз."
	suggestLimited   = "Можно предложить не больше 3 стартов в сутки. Попробуйте позже."
	suggestGroupHead = "💡 <b>Предложение старта</b>"
)

// suggestRow — строка «💡 Предложить старт» экранов «ничего не найдено» (E-1);
// предложению некуда уйти без группы и без администраторов — строки нет.
func (b *Bot) suggestRow() row {
	if !b.reportsOn() {
		return nil
	}
	return btn(suggestLabel, callback{Kind: cbSuggest})
}

// suggestPrompt — P-1, приглашение прислать ссылку.
func suggestPrompt() screen {
	return screen{text: suggestInvite, kb: keyboard(btn("Отмена", callback{Kind: cbSuggestCancel}))}
}

// onSuggest обрабатывает q:s и q:c, обе отвечают пустым уведомлением:
//   - q:s — отметка «жду ссылку» (снимает отметки поддержки и города) и P-1
//     новым сообщением: экран «ничего не найдено» остаётся на месте;
//   - q:c — отметка снимается, сообщение правится на главное меню; кнопка
//     работает и на старом приглашении без отметки.
func (b *Bot) onSuggest(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind == cbSuggestCancel {
		if err := b.Store.SuggestCancel(ctx, q.From.ID); err != nil {
			b.logger().Error("suggest cancel", "err", err)
			return errNotice
		}
		return b.onMenu(ctx, q, c)
	}
	if err := b.Store.SuggestBegin(ctx, q.From.ID, b.now().Add(awaitSuggestFor)); err != nil {
		b.logger().Error("suggest begin", "err", err)
		return errNotice
	}
	if err := b.send(ctx, q.From.ID, suggestPrompt()); err != nil {
		b.logger().Warn("send suggest prompt", "kind", kindName(err))
	}
	return ""
}

// onSuggestCmd — /suggest: отметка на окно и P-1 новым сообщением. Регистрируется
// только при группе или администраторах, иначе команда неизвестна (onText).
func (b *Bot) onSuggestCmd(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if err := b.Store.SuggestBegin(ctx, m.From.ID, b.now().Add(awaitSuggestFor)); err != nil {
		b.logger().Error("suggest begin", "err", err)
		b.supportSay(ctx, m, errNotice)
		return
	}
	if err := b.send(ctx, m.Chat.ID, suggestPrompt()); err != nil {
		b.logger().Warn("send suggest prompt", "kind", kindName(err))
	}
}

// suggestText принимает текст как предложение, если стоит действующая отметка;
// true — сообщение обработано, в город и в поиск оно не идёт. Проверяется в
// onText сразу после поддержки — раньше предела 64 символа. Нет отметки, она
// истекла либо текст начинается с «/» — false без записи. Формат проверяется
// до записи: отказ P-3 оставляет отметку и окно прежними. Отметку потребляет
// SuggestTake под блокировкой, строка предложения пишется до отправки; сбой
// отправки — строка удаляется, отметка возвращается на окно (P-5).
func (b *Bot) suggestText(ctx context.Context, m *models.Message) bool {
	if !b.reportsOn() {
		return false
	}
	until, ok, err := b.Store.SuggestAwaiting(ctx, m.From.ID)
	if err != nil {
		b.logger().Error("suggest awaiting", "err", err)
		b.supportSay(ctx, m, errNotice)
		return true
	}
	if !ok || !until.After(b.now()) {
		return false
	}
	link := strings.TrimSpace(m.Text)
	if strings.HasPrefix(link, "/") {
		return false
	}
	// В ссылке нет пробелов: слова вокруг неё и две ссылки ValidURL не пропускает.
	if !validURL(link) || utf8.RuneCountInString(link) > maxSuggestURL {
		b.logger().Info("suggest", "outcome", "refused")
		b.supportSay(ctx, m, suggestRefused)
		return true
	}
	now := b.now()
	id, out, err := b.Store.SuggestTake(ctx, m.From.ID, now, now.Add(-suggestWindow), suggestLimit)
	if err != nil {
		b.logger().Error("suggest take", "err", err)
		b.supportSay(ctx, m, errNotice)
		return true
	}
	switch out {
	case store.SuggestNotWaiting: // отметку сняли, пока ждали блокировку
		return false
	case store.SuggestLimited: // отметка потреблена
		b.logger().Info("suggest", "outcome", "limited")
		b.supportSay(ctx, m, suggestLimited)
		return true
	}
	if !b.deliver(ctx, "suggest", suggestGroupHead+"\n"+html.EscapeString(link)) {
		if err := b.Store.SuggestRelease(ctx, m.From.ID, id, b.now().Add(awaitSuggestFor)); err != nil {
			b.logger().Error("suggest release", "err", err)
		}
		b.supportSay(ctx, m, supportSendError)
		return true
	}
	b.logger().Info("suggest", "outcome", "sent")
	b.supportSay(ctx, m, reportSent)
	return true
}

// suggestNonText — сообщение без текста при действующей отметке: отказ P-3,
// отметка и окно не меняются. Вне режима — без единого вызова Bot API.
func (b *Bot) suggestNonText(ctx context.Context, m *models.Message) {
	if !b.reportsOn() || m.From == nil {
		return
	}
	until, ok, err := b.Store.SuggestAwaiting(ctx, m.From.ID)
	if err != nil {
		b.logger().Error("suggest awaiting", "err", err)
		return
	}
	if ok && until.After(b.now()) {
		b.supportSay(ctx, m, suggestRefused)
	}
}
