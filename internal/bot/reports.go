package bot

// Заявки об ошибке в карточке (Phase 16, 16-UX-CONTRACT.md): кнопки q:, экраны
// R-1…R-8 и сообщение G-1. Заявка уходит людям одним сообщением бота — в группу
// поддержки, а без неё каждому администратору; автора в сообщении нет, связки
// «сообщение → автор» нет. В базе только Telegram ID, событие, причина и время.
// В лог идут вид события, исход и вид ошибки Bot API — без событий и пользователей.

import (
	"context"
	"errors"
	"html"
	"strconv"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

const (
	// reportLimit заявок за reportWindow от одного пользователя (Q-1).
	reportLimit  = 10
	reportWindow = 24 * time.Hour
	// reportKeep — срок хранения заявок и предложений (Q-4).
	reportKeep = 90 * 24 * time.Hour
)

// Тексты экранов заявки — дословно из контракта; ничего не обещают.
const (
	reportLabel     = "📝 Сообщить об ошибке"
	reportHead      = "📝 <b>Сообщить об ошибке</b>"
	reportAsk       = "Что не так в карточке?"
	reportSent      = "✅ Передал."
	reportRepeat    = "Эту заявку уже передал."
	reportLimited   = "Можно отправить не больше 10 заявок об ошибках в сутки. Попробуйте позже."
	reportCancelled = "Заявка не отправлена."
	reportFailed    = "Не удалось передать заявку. Попробуйте ещё раз позже."
	reportGroupHead = "📝 <b>Ошибка в карточке</b>"
)

// reportReason — причина заявки: буква в кнопке, подпись, код в базе и слово в
// строке «Причина: …».
type reportReason struct{ letter, label, code, word string }

// reportReasons — справочник причин в порядке кнопок (D-06); единственный
// источник допустимых букв q:r.
var reportReasons = []reportReason{
	{"d", "Дата", "date", "дата"},
	{"p", "Место", "place", "место"},
	{"k", "Дистанция", "distance", "дистанция"},
	{"u", "Ссылка", "link", "ссылка"},
	{"c", "Отменён или перенесён", "cancelled", "отмена или перенос"},
	{"m", "Дубль другого старта", "duplicate", "дубль"},
	{"o", "Другое", "other", "другое"},
}

func reasonByLetter(l string) (reportReason, bool) {
	for _, r := range reportReasons {
		if r.letter == l {
			return r, true
		}
	}
	return reportReason{}, false
}

// reportsOn — заявкам есть куда уйти: задана группа или есть администраторы.
func (b *Bot) reportsOn() bool { return b.supportChat != 0 || len(b.adminIDs) > 0 }

// onReport обрабатывает q:*. Состояние берётся из базы, а не из сообщения:
// кнопки R-1 из старой копии работают. Слитое событие — корень.
//   - q:o — R-1 новым сообщением; недоступное и прошедшее — уведомлением;
//   - q:r — заявка и правка R-1 на итог (R-2…R-6); сбой доставки — R-8
//     уведомлением, заявка удаляется, R-1 не правится;
//   - q:x — правка R-1 на R-7.
func (b *Bot) onReport(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if c.Kind == cbReportCancel {
		b.reportShow(ctx, q, screen{text: reportCancelled})
		return ""
	}
	e, state, err := b.reportEvent(ctx, c.ID)
	if err != nil {
		b.logger().Error("report", "action", "event", "err", err)
		return errNotice
	}
	if c.Kind == cbReportOpen {
		switch state {
		case reportGone:
			return goneNotice
		case reportPast:
			return favPastNotice
		}
		if err := b.send(ctx, q.From.ID, reasonsScreen(e)); err != nil {
			b.logger().Warn("send report reasons", "kind", kindName(err))
		}
		return ""
	}
	switch state {
	case reportGone:
		b.logger().Info("report", "outcome", "gone")
		b.reportShow(ctx, q, screen{text: goneNotice, plain: true})
		return ""
	case reportPast:
		b.logger().Info("report", "outcome", "past")
		b.reportShow(ctx, q, screen{text: favPastNotice})
		return ""
	}
	r, _ := reasonByLetter(c.Value) // буква проверена разбором
	now := b.now()
	root, out, err := b.Store.AddReport(ctx, q.From.ID, e.ID, r.code, now, now.Add(-reportWindow), reportLimit)
	if err != nil {
		b.logger().Error("report", "action", "add", "err", err)
		return errNotice
	}
	switch out {
	case store.ReportRepeat:
		b.logger().Info("report", "outcome", "repeat")
		b.reportShow(ctx, q, screen{text: reportText(e, "Причина: "+r.word+"\n"+reportRepeat)})
		return ""
	case store.ReportLimited:
		b.logger().Info("report", "outcome", "limited")
		b.reportShow(ctx, q, screen{text: reportText(e, reportLimited)})
		return ""
	}
	srcs, err := b.Store.EventSources(ctx, root)
	if err != nil {
		b.logger().Error("report", "action", "sources", "err", err)
		b.reportUndo(ctx, q.From.ID, root, r.code)
		return errNotice
	}
	e.ID = root
	if !b.deliver(ctx, "report", groupReport(e, srcs, r)) {
		b.reportUndo(ctx, q.From.ID, root, r.code)
		return reportFailed
	}
	b.logger().Info("report", "outcome", "sent")
	b.reportShow(ctx, q, screen{text: reportText(e, "Причина: "+r.word+"\n"+reportSent)})
	return ""
}

// reportUndo удаляет заявку, которую не удалось передать: можно нажать ещё раз.
func (b *Bot) reportUndo(ctx context.Context, tgID, root int64, code string) {
	if err := b.Store.DeleteReport(ctx, tgID, root, code); err != nil {
		b.logger().Error("report", "action", "delete", "err", err)
	}
}

// reportShow правит сообщение R-1 на итог без клавиатуры.
func (b *Bot) reportShow(ctx context.Context, q *models.CallbackQuery, sc screen) {
	if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show report", "kind", kindName(err))
	}
}

// Доступность события для заявки — по правилам выдачи (раздел 2 контракта).
const (
	reportOK = iota
	reportGone
	reportPast
)

// reportEvent — корневое событие id и его доступность: нет, скрыто, слито
// (гонка со слиянием), выключен вид спорта — reportGone; старт раньше «сегодня» —
// reportPast. Ошибка — только сбой базы.
func (b *Bot) reportEvent(ctx context.Context, id int64) (event.Event, int, error) {
	root, err := b.Store.RootEventID(ctx, id)
	var e event.Event
	if err == nil {
		e, err = b.Store.GetEvent(ctx, root)
	}
	if errors.Is(err, store.ErrNotFound) {
		return event.Event{}, reportGone, nil
	}
	if err != nil {
		return event.Event{}, 0, err
	}
	sport, _ := event.SportByCode(e.Sport)
	switch {
	case !e.IsListed || e.MergedIntoID != nil || !sport.Enabled:
		return e, reportGone, nil
	case e.StartDate.Before(b.today()):
		return e, reportPast, nil
	}
	return e, reportOK, nil
}

// reportText — R-1…R-4: шапка (как первые строки карточки) и последняя строка.
func reportText(e event.Event, last string) string {
	return reportHead + "\n\n" + startHead(e, true) + "\n\n" + last
}

// reasonsScreen — R-1: шапка, вопрос, причины по две в ряду и «Отмена».
func reasonsScreen(e event.Event) screen {
	rows := make([]row, 0, 6)
	var pair row
	for i, r := range reportReasons {
		bt := btn(r.label, callback{Kind: cbReport, ID: e.ID, Value: r.letter})
		switch {
		case i < 4: // короткие — по две в ряду
			pair = append(pair, bt...)
			if len(pair) == 2 {
				rows, pair = append(rows, pair), nil
			}
		default:
			rows = append(rows, bt)
		}
	}
	rows = append(rows, btn("Отмена", callback{Kind: cbReportCancel}))
	return screen{text: reportText(e, reportAsk), kb: keyboard(rows...)}
}

// groupReport — G-1: причина, шапка корневого события, его номер, источники и
// годная ссылка регистрации. Автора нет.
func groupReport(e event.Event, srcs []store.EventSource, r reportReason) string {
	s := reportGroupHead + "\nПричина: " + r.word + "\n\n" + startHead(e, true) +
		"\nСобытие: <code>" + strconv.FormatInt(e.ID, 10) + "</code>"
	if len(srcs) > 0 {
		s += "\n" + sourcesLine(srcs)
	}
	if validURL(e.RegistrationURL) {
		s += "\n🔗 Регистрация: " + html.EscapeString(e.RegistrationURL)
	}
	return s
}

// deliver отправляет сообщение людям (D-07): в группу поддержки, а без неё —
// каждому администратору по порядку ADMIN_TG_IDS. true — дошло хотя бы одно.
// Повтор при 429 — withRetry. В лог — вид сообщения (what) и вид ошибки; при
// переносе группы в супергруппу — новый идентификатор группы, как у поддержки.
func (b *Bot) deliver(ctx context.Context, what, text string) bool {
	to := b.adminIDs
	if b.supportChat != 0 {
		to = []int64{b.supportChat}
	}
	ok := false
	for _, chat := range to {
		err := withRetry(ctx, func() error {
			_, err := b.api.SendMessage(ctx, &tgbot.SendMessageParams{
				ChatID:             chat,
				Text:               text,
				ParseMode:          models.ParseModeHTML,
				LinkPreviewOptions: linkPreviewOff(),
			})
			return err
		})
		var mig *tgbot.MigrateError
		switch {
		case err == nil:
			ok = true
		case errors.As(err, &mig):
			b.logger().Error("support group migrated", "new_chat_id", mig.MigrateToChatID)
		default:
			b.logger().Warn("deliver failed", "what", what, "kind", kindName(err))
		}
	}
	return ok
}
