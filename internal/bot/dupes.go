package bot

// Очередь кандидатов в дубли (/dupes, D-09, D-10) — только для
// администратора. Каталог меняется только через store: после [Да, слить]
// (ConfirmCandidate) или [Разные] (RejectCandidate). Стороны кандидата —
// уже корни (store, D-17.3); бот по цепочке слияний сам не ходит.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/dedup"
	"aggregator/internal/store"
)

const mergeFailed = "Не удалось слить, подробности в логе"

// onDupes — /dupes: первый ожидающий кандидат. Не администратор получает
// тот же ответ, что на любую неизвестную команду, и ничего не читается.
func (b *Bot) onDupes(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if !b.isAdmin(m.From.ID) {
		b.onText(ctx, api, u)
		return
	}
	if err := b.send(ctx, m.Chat.ID, b.dupeNext(ctx, 0, "")); err != nil {
		b.logger().Warn("send dupes", "err", err)
	}
}

// onDupe обрабатывает d:*. Администратор проверяется на каждом нажатии до
// любого чтения store: кнопку мог прислать кто угодно. Каждый ответ —
// экран в том же сообщении.
func (b *Bot) onDupe(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if !b.isAdmin(q.From.ID) {
		b.logger().Warn("admin callback from non-admin")
		return ""
	}
	var sc screen
	switch c.Kind {
	case cbDupeOpen:
		sc = b.dupeNext(ctx, 0, "")
	case cbDupeSkip:
		sc = b.dupeNext(ctx, c.ID, "")
	case cbDupeAsk, cbDupeNo:
		// Только экраны: каталог не меняется.
		sc = b.dupeOne(ctx, c.ID, c.Kind == cbDupeAsk)
	case cbDupeRej:
		sc = b.dupeDecide(ctx, c.ID, "reject", fmt.Sprintf("Кандидат #%d: разные.", c.ID), errNotice,
			func() error { return b.Store.RejectCandidate(ctx, c.ID, q.From.ID) })
	case cbDupeYes:
		if b.Runner == nil {
			return "Слияние недоступно"
		}
		cand, err := b.Store.CandidateByID(ctx, c.ID)
		if errors.Is(err, store.ErrNotFound) {
			sc = b.dupeNext(ctx, c.ID, done(c.ID))
			break
		}
		if err != nil {
			b.logger().Error("dupe candidate", "candidate", c.ID, "err", err)
			sc = screen{text: mergeFailed, plain: true}
			break
		}
		lo, hi := min(cand.EventA, cand.EventB), max(cand.EventA, cand.EventB)
		sc = b.dupeDecide(ctx, c.ID, "merge", fmt.Sprintf("Слито: #%d → #%d.", hi, lo), mergeFailed,
			func() error { return b.Store.ConfirmCandidate(ctx, c.ID, q.From.ID, b.Runner.Fold) })
	}
	if err := b.show(ctx, q, sc); err != nil {
		b.logger().Warn("show dupe", "err", err)
	}
	return ""
}

func done(cid int64) string {
	return fmt.Sprintf("Кандидат #%d уже обработан.", cid)
}

// dupeDecide выполняет решение и возвращает экран следующего кандидата с
// итогом. Уже решённый или исчезнувший кандидат — «уже обработан», без
// ошибки. В лог — id кандидата и действие, без tg id и названий.
func (b *Bot) dupeDecide(ctx context.Context, cid int64, action, ok, failed string, do func() error) screen {
	err := do()
	switch {
	case err == nil:
		b.logger().Info("dupe decided", "candidate", cid, "action", action)
		return b.dupeNext(ctx, cid, ok)
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrNotMergeable):
		return b.dupeNext(ctx, cid, done(cid))
	}
	b.logger().Error("dupe decide", "candidate", cid, "action", action, "err", err)
	return screen{text: failed, plain: true}
}

// dupeOne — экран кандидата cid (ask — с подтверждением слияния).
func (b *Bot) dupeOne(ctx context.Context, cid int64, ask bool) screen {
	c, err := b.Store.CandidateByID(ctx, cid)
	if errors.Is(err, store.ErrNotFound) {
		return b.dupeNext(ctx, cid, done(cid))
	}
	if err == nil {
		var sc screen
		if sc, err = b.dupeScreen(ctx, c, "", ask); err == nil {
			return sc
		}
	}
	b.logger().Error("dupe screen", "candidate", cid, "err", err)
	return screen{text: errNotice, plain: true}
}

// dupeNext — первый ожидающий кандидат с id > afterID; prefix — итог
// предыдущего действия над экраном. Конец очереди — число ожидающих и
// [С начала].
func (b *Bot) dupeNext(ctx context.Context, afterID int64, prefix string) screen {
	c, err := b.Store.NextCandidate(ctx, afterID)
	if err == nil {
		var sc screen
		if sc, err = b.dupeScreen(ctx, c, prefix, false); err == nil {
			return sc
		}
	} else if errors.Is(err, store.ErrNotFound) {
		var pending int
		if pending, _, err = b.Store.CandidateCounts(ctx); err == nil {
			if pending == 0 {
				return screen{text: withPrefix(prefix, "Кандидатов нет"), plain: true}
			}
			return screen{text: withPrefix(prefix, fmt.Sprintf("Кандидатов дальше нет. Ожидают: %d", pending)), plain: true,
				kb: keyboard(btn("С начала", callback{Kind: cbDupeOpen}))}
		}
	}
	b.logger().Error("dupe next", "after", afterID, "err", err)
	return screen{text: errNotice, plain: true}
}

func withPrefix(prefix, text string) string {
	if prefix == "" {
		return text
	}
	return prefix + "\n\n" + text
}

// dupeScreen — кандидат обычным текстом (названия бывают с «<» и «&»):
// оценка, сигналы, стороны А и Б; ask — вопрос и [Да, слить] / [Отмена]
// вместо кнопок решения.
func (b *Bot) dupeScreen(ctx context.Context, c store.Candidate, prefix string, ask bool) (screen, error) {
	pending, _, err := b.Store.CandidateCounts(ctx)
	if err != nil {
		return screen{}, err
	}
	var t strings.Builder
	fmt.Fprintf(&t, "Кандидат #%d · оценка %s · ожидают %d", c.ID, comma(c.Score, 2), pending)
	if s := signals(c.Reasons); s != "" {
		t.WriteString("\n" + s)
	}
	for _, side := range []struct {
		label string
		id    int64
	}{{"А", c.EventA}, {"Б", c.EventB}} {
		block, err := b.dupeSide(ctx, side.id)
		if err != nil {
			return screen{}, err
		}
		fmt.Fprintf(&t, "\n\n%s · #%d\n%s", side.label, side.id, block)
	}
	lo, hi := min(c.EventA, c.EventB), max(c.EventA, c.EventB)
	var kb *models.InlineKeyboardMarkup
	if ask {
		fmt.Fprintf(&t, "\n\nСлить? Останется #%d, #%d скроется из выдачи, его источники перейдут к оставшемуся. Отменить из бота нельзя.", lo, hi)
		var yn row
		yn.add("Да, слить", callback{Kind: cbDupeYes, ID: c.ID})
		yn.add("Отмена", callback{Kind: cbDupeNo, ID: c.ID})
		kb = keyboard(yn)
	} else {
		var decide row
		decide.add("Слить", callback{Kind: cbDupeAsk, ID: c.ID})
		decide.add("Разные", callback{Kind: cbDupeRej, ID: c.ID})
		kb = keyboard(decide, btn("Пропустить", callback{Kind: cbDupeSkip, ID: c.ID}))
	}
	return screen{text: cut(withPrefix(prefix, t.String()), maxAdminText), kb: kb, plain: true}, nil
}

// dupeSide — дата, город, название, дистанции и источники события.
func (b *Bot) dupeSide(ctx context.Context, id int64) (string, error) {
	e, err := b.Store.GetEvent(ctx, id)
	if err != nil {
		return "", err
	}
	srcs, err := b.Store.EventSources(ctx, id)
	if err != nil {
		return "", err
	}
	date := e.StartDate.Format("02.01.2006")
	if e.EndDate != nil && !e.EndDate.Equal(e.StartDate) {
		date += "–" + e.EndDate.Format("02.01.2006")
	}
	city := "город не указан"
	if e.City != "" {
		city = cut(e.City, 100)
	}
	lines := []string{date + " · " + city, cut(e.Title, 200)}
	meters := make([]int, len(e.Distances))
	for i, d := range e.Distances {
		meters[i] = d.Meters
	}
	if km := kmList(meters); km != "" {
		lines = append(lines, km)
	}
	for _, s := range srcs {
		line := sourceName(s.Code)
		if validURL(s.URL) {
			line += ": " + s.URL
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

// signals — сработавшие сигналы из reasons и строка потенциального
// автослияния (would_auto); не разобрались — "".
func signals(raw json.RawMessage) string {
	var r dedup.Reasons
	if err := json.Unmarshal(raw, &r); err != nil {
		return ""
	}
	parts := []string{"название " + comma(r.NameSim, 2)}
	if r.DatesOverlap {
		parts = append(parts, "даты пересекаются")
	} else {
		parts = append(parts, "даты рядом (±1 день)")
	}
	if r.SameCity {
		parts = append(parts, "город совпал")
	} else {
		parts = append(parts, "город разный")
	}
	if r.Link {
		parts = append(parts, "ссылка регистрации совпала")
	}
	if r.GroupKey {
		parts = append(parts, "серия")
	}
	out := "Сигналы: " + strings.Join(parts, " · ")
	// При DEDUP_AUTO_MERGE=true такая пара сливается проходом и кандидатом
	// не становится; ключа нет в старых reasons — строки нет.
	if r.WouldAuto {
		out += "\nАвтослияние слило бы эту пару (сейчас выключено)"
	}
	return out
}

// comma — число с n знаками после запятой.
func comma(x float64, n int) string {
	return strings.Replace(strconv.FormatFloat(x, 'f', n, 64), ".", ",", 1)
}

// SendDupesDigest отправляет сводку дублей администратору обычным текстом с
// кнопкой [Открыть /dupes]. Сигнатура совпадает с dedup.DigestLoop.Send (D-11).
func (b *Bot) SendDupesDigest(ctx context.Context, chatID int64, text string) error {
	if !b.isAdmin(chatID) {
		return errNotAdmin
	}
	return b.send(ctx, chatID, screen{text: cut(text, maxAdminText), plain: true,
		kb: keyboard(btn("Открыть /dupes", callback{Kind: cbDupeOpen}))})
}
