package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"maps"
	"slices"
	"strings"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

// Отправка сообщений из очереди outbox (Phase 14, 14-UX-CONTRACT.md, разделы
// «Отправка» и «Напоминание»): сообщение об изменении избранного старта (N-1),
// напоминание за семь дней (N-2), сообщение администратору о предохранителе
// (N-3) и о старте, пропавшем из календаря (N-4). Строки об изменениях пишет
// применение прогона источника; напоминания и сообщения о предохранителе
// создаёт служебная часть шага — они зависят от времени; отправляет только
// бот — цикл Notifier.Run запускается в процессе бота, у воркера Telegram
// нет. Текст сообщения в очереди не хранится: он собирается при отправке из
// строки события и содержимого строки очереди.
const (
	// notifyEvery — период шага: один шаг — не больше одного сообщения, то есть не
	// больше одного сообщения в секунду на весь бот (потолок 3600 в час).
	//
	// Упрощение: один общий потолок на всех пользователей; ускорять — когда
	// очередь станет расти быстрее, чем уходит.
	notifyEvery = time.Second
	// notifyDBPause — пауза отправки после сбоя базы до вызова Bot API: без неё
	// сбой давал бы попытку и строку лога каждую секунду.
	notifyDBPause = time.Minute
	// notifyStepMax — предел времени одного шага: меньше stop_grace_period
	// контейнера бота (20 с). Начатый при остановке шаг доводится до отметки исхода.
	notifyStepMax = 15 * time.Second
	// notifyMaxAge — срок годности сообщения об изменении и о пропаже: позже
	// строка закрывается как просроченная.
	notifyMaxAge = 72 * time.Hour
	// notifyKeep — срок хранения строк очереди: старше — удаляются.
	notifyKeep = 30 * 24 * time.Hour
	// choresEvery — как часто шаг выполняет служебную часть (срок годности и
	// очистка).
	choresEvery = time.Minute

	// remindHour — час напоминания по Москве: напоминание создаётся в день «за
	// remindDays дней до старта» с этого часа и до конца суток (D-09).
	remindHour = 10
	remindDays = 7
	// fuseWindow — за какой срок ищутся прогоны со сработавшим предохранителем.
	fuseWindow = 24 * time.Hour
	// fuseThreshold — порог предохранителя шага сообщений прогона, как он назван
	// в тексте N-3.
	//
	// Упрощение: число продублировано — watchFuse в internal/ingest не
	// экспортируется, а запись прогона по условиям фазы не меняется; при смене
	// порога править оба места.
	fuseThreshold = 20

	// Ночь по Москве: с nightFrom часов (включительно) до nightTo часов (не
	// включительно) сообщения уходят сразу, но без звука (D-13).
	nightFrom = 23
	nightTo   = 8

	notifyChangeHead = "🔔 <b>Изменения у старта из избранного</b>"
	notifyGoneHead   = "⚠️ <b>Старт из избранного пропал из календаря</b>"
	notifyGoneText   = "Старт больше не виден в календаре. Возможно, он отменён или перенесён — уточните на сайте организатора."
	notifyOpenLabel  = "📋 Открыть старт"
	notifyTBDMark    = " (дата уточняется)"
	notifyLinkLine   = "🔗 Ссылка: не было → появилась"
	notifyRemindHead = "⏰ <b>Через 7 дней старт</b>"
)

// statusLabels — подписи статусов в строке «было → стало».
var statusLabels = map[event.Status]string{
	event.StatusScheduled: "состоится",
	event.StatusCancelled: "отменён",
	event.StatusPostponed: "перенесён",
}

// noWait — предел ожидания 429 внутри вызова: отрицательный, чтобы не ждать и
// не повторять вызов даже при retry_after = 0. Паузу ведёт сам цикл.
const noWait = time.Duration(-1)

// Notifier — цикл отправки сообщений из очереди. Состояние (пауза и время
// служебной части) живёт в процессе: Step и Run вызываются из одной горутины.
type Notifier struct {
	b          *Bot
	pauseUntil time.Time // отправка стоит до этого момента по часам бота
	choresAt   time.Time // когда служебная часть выполнялась в последний раз
}

// NewNotifier — цикл отправки бота b.
func NewNotifier(b *Bot) *Notifier { return &Notifier{b: b} }

// Run выполняет шаг раз в секунду до отмены ctx. Начатый шаг доводится до
// отметки исхода с контекстом без отмены и пределом notifyStepMax; после
// отмены новые шаги не начинаются.
func (n *Notifier) Run(ctx context.Context) {
	t := time.NewTicker(notifyEvery)
	defer t.Stop()
	for ctx.Err() == nil {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyStepMax)
		n.Step(sctx)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Step — один шаг цикла. Порядок: служебная часть (не чаще раза в минуту);
// пауза; взять самую старую ожидающую строку отдельной зафиксированной
// записью; подготовить сообщение (перечитать состояние, собрать экран);
// вызвать Bot API; отметить исход. Не больше одного сообщения за шаг.
//
// Гарантия «не более одного раза». Отметка взятия ставится до вызова Bot API,
// и взятая строка повторно не берётся никогда — ни следующим шагом, ни после
// перезапуска. Граница: сообщение, отправка которого оборвалась между взятием
// и отметкой (остановка бота, обрыв связи или тайм-аут во время вызова),
// повторно не шлётся и может не дойти; такая строка остаётся с отметкой взятия
// без отметок отправки и закрытия — «исход неизвестен». Повтор возможен только
// тогда, когда сообщение заведомо не отправлено: после ответа 429 и после
// сбоя до вызова Bot API (участок «подготовка»). Вторая граница: если при
// таком сбое не удалось и снять отметку взятия (база недоступна), строка
// остаётся взятой и сообщение не уходит никогда.
func (n *Notifier) Step(ctx context.Context) {
	b := n.b
	now := b.now()
	if !now.Before(n.choresAt.Add(choresEvery)) {
		n.choresAt = now
		n.chores(ctx, now)
	}
	if now.Before(n.pauseUntil) {
		return
	}
	row, ok, err := b.Store.ClaimOutbox(ctx, now)
	if err != nil {
		n.pauseUntil = now.Add(notifyDBPause)
		b.logger().Warn("notify", "outcome", "claim_failed")
		return
	}
	if !ok {
		return
	}

	// Участок «подготовка»: вызов Bot API не начат, сообщение не отправлялось.
	p, err := n.prepare(ctx, row, now)
	if err == nil && p.close != "" {
		if err = b.Store.CloseOutbox(ctx, row.ID, p.close, now); err == nil {
			b.logger().Info("notify", "kind", row.Kind, "outcome", p.close)
			return
		}
	}
	if err != nil {
		// Сбой базы: сообщение не уходило, отметка снимается, отправка стоит минуту.
		n.pauseUntil = now.Add(notifyDBPause)
		if b.Store.ReleaseOutbox(ctx, row.ID) != nil {
			b.logger().Warn("notify", "kind", row.Kind, "outcome", "release_failed")
			return
		}
		b.logger().Warn("notify", "kind", row.Kind, "outcome", "released")
		return
	}

	// Участок «вызов»: с этого места отметка взятия не снимается — кроме 429.
	p.sc.quiet = quietAt(now, b.loc)
	n.settle(ctx, row, b.send(context.WithValue(ctx, retryWaitKey{}, noWait), row.TgID, p.sc), now)
}

// settle отмечает исход вызова Bot API.
//
// Упрощение: после 403 признака «заблокировал» в схеме нет — следующая строка
// этому пользователю создаётся и пробуется как обычно, то есть одна лишняя
// попытка на каждое новое сообщение заблокировавшему пользователю; потолок —
// признак в схеме, если таких пользователей станет много.
//
// Сообщение теряется в двух случаях: вызов оборвался после взятия (обрыв
// связи, тайм-аут, остановка — строка остаётся взятой, «исход неизвестен»,
// повтора нет) и сбой базы до вызова, при котором не удалось снять отметку
// (строка остаётся взятой, сообщение не уходит никогда).
func (n *Notifier) settle(ctx context.Context, row store.OutboxRow, err error, now time.Time) {
	b := n.b
	kind, wait := classify(err)
	switch kind {
	case kindNone:
		if b.Store.MarkOutboxSent(ctx, row.ID, now) != nil {
			b.logger().Warn("notify", "kind", row.Kind, "outcome", "sent_unmarked")
			return
		}
		b.logger().Info("notify", "kind", row.Kind, "outcome", "sent")
	case kindBlocked:
		// 403: строка и остальные ожидающие строки пользователя закрываются одной
		// записью; избранное остаётся.
		closed, cerr := b.Store.CloseUserOutbox(ctx, row.TgID, row.ID, store.OutboxBlocked, now)
		if cerr != nil {
			b.logger().Warn("notify", "kind", row.Kind, "outcome", "close_failed")
			return
		}
		b.logger().Info("notify", "kind", row.Kind, "outcome", store.OutboxBlocked, "closed", closed)
	case kindRateLimited:
		// 429: Telegram сообщение не принял — строка снова ожидает, отправка всем
		// стоит до названного срока.
		n.pauseUntil = now.Add(wait)
		if b.Store.ReleaseOutbox(ctx, row.ID) != nil {
			b.logger().Warn("notify", "kind", row.Kind, "outcome", "release_failed")
			return
		}
		b.logger().Info("notify", "kind", row.Kind, "outcome", "rate_limited")
	case kindBadRequest:
		if b.Store.CloseOutbox(ctx, row.ID, store.OutboxRejected, now) != nil {
			b.logger().Warn("notify", "kind", row.Kind, "outcome", "close_failed")
			return
		}
		b.logger().Info("notify", "kind", row.Kind, "outcome", store.OutboxRejected)
	default:
		// Любая другая ошибка вызова, в том числе тайм-аут и обрыв связи: исход
		// неизвестен, строка остаётся взятой и больше не отправляется.
		b.logger().Warn("notify", "kind", row.Kind, "outcome", "unknown", "cause", kindName(err))
	}
}

// chores — служебная часть шага: закрывает просроченные сообщения об
// изменениях и о пропавших стартах, удаляет строки очереди, заявок и
// предложений старше срока хранения, создаёт напоминания и сообщения
// администраторам о предохранителе. Ошибка
// пишется в лог и отправку не останавливает; в лог идут только счётчики и
// названия шагов, без текста ошибки: она может содержать значения из базы.
func (n *Notifier) chores(ctx context.Context, now time.Time) {
	b := n.b
	var failed []string
	do := func(step string, f func() (int, error)) int {
		c, err := f()
		if err != nil {
			failed = append(failed, step)
		}
		return c
	}
	expired := do("expire", func() (int, error) {
		return b.Store.ExpireOutbox(ctx, []string{event.KindChange, event.KindGone}, now.Add(-notifyMaxAge), now)
	})
	pruned := do("prune", func() (int, error) { return b.Store.PruneOutbox(ctx, now.Add(-notifyKeep)) })
	reminders := do("remind", func() (int, error) { return n.remind(ctx, now) })
	fuse := do("fuse", func() (int, error) { return n.fuse(ctx, now) })
	reports := do("reports", func() (int, error) { return b.Store.PruneReports(ctx, now.Add(-reportKeep)) })
	days := do("days", func() (int, error) { return b.Store.PruneUserDays(ctx, n.dayOf(now).AddDate(0, 0, -dayKeep)) })
	if len(failed) > 0 {
		b.logger().Warn("notify chores failed", "steps", strings.Join(failed, ","))
	}
	if expired > 0 || pruned > 0 || reminders > 0 || fuse > 0 || reports > 0 || days > 0 {
		b.logger().Info("notify chores", "expired", expired, "pruned", pruned, "reminders", reminders, "fuse", fuse, "reports", reports, "days", days)
	}
}

// dayOf — календарная дата момента по Москве как полночь UTC (как Bot.today,
// но от переданного момента: шаг работает с одним значением часов).
func (n *Notifier) dayOf(now time.Time) time.Time {
	y, m, d := now.In(n.b.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// remind создаёт напоминания (D-09…D-11). Условие по часам бота: по Москве
// уже наступил remindHour — до конца суток вызов повторяется каждую служебную
// минуту, повтор безвреден (строка на пару «пользователь — ключ» одна), а
// пользователь, добавивший старт позже этого часа, под условие не попадает.
// Бот, начавший работать позже этого часа, создаёт напоминания при первом же
// шаге; бот, не работавший весь день, — нет. Кандидатов отбирает база, заглушки
// отсеивает event.DateTBD — единственная реализация признака.
//
// Упрощение: ключ напоминания — корневое событие и дата старта. Слияние
// избранного старта с другим после отправки напоминания в тот же день меняет
// корень и даёт второе напоминание о том же старте; потолок — одно лишнее
// напоминание на такое слияние, автослияние выключено. Убирается учётом
// напоминаний по всей группе слитых событий.
//
// Упрощение: вставка строк со ссылкой на событие при схеме с внешними ключами
// ждёт конца записи прогона, держащей это событие; потолок — секунды на запись
// одного прогона, шаг отправки на это время стоит.
func (n *Notifier) remind(ctx context.Context, now time.Time) (int, error) {
	local := now.In(n.b.loc)
	moment := time.Date(local.Year(), local.Month(), local.Day(), remindHour, 0, 0, 0, n.b.loc)
	if now.Before(moment) {
		return 0, nil
	}
	cands, err := n.b.Store.ReminderCandidates(ctx, n.dayOf(now), remindDays, moment)
	if err != nil {
		return 0, err
	}
	keep := cands[:0]
	for _, c := range cands {
		if !event.DateTBD(c.Title) {
			keep = append(keep, c)
		}
	}
	return n.b.Store.EnqueueReminders(ctx, keep, now)
}

// fuse создаёт каждому администратору из перечня бота сообщение о прогоне,
// завершённом за последние fuseWindow, у которого сработал предохранитель.
// Пользователям сообщений нет; без администраторов в конфигурации — тоже.
func (n *Notifier) fuse(ctx context.Context, now time.Time) (int, error) {
	admins := slices.Sorted(maps.Keys(n.b.admins))
	return n.b.Store.EnqueueFuseNotices(ctx, admins, now.Add(-fuseWindow), now)
}

// prepared — итог подготовки: экран либо исход, с которым строка закрывается
// без отправки.
type prepared struct {
	sc    screen
	close string
}

// prepare перечитывает состояние и собирает экран. Ошибка — только сбой базы
// (повторится позже). Строка, которую нельзя прочитать при любой попытке
// (неизвестный вид, нечитаемое содержимое), и строка, у которой условие
// сообщения больше не выполняется, закрываются с исходом.
//
// Перед отправкой условия проверяются заново: сообщение об изменении — старт
// в избранном, виден, не прошёл; сообщение о пропавшем старте — старт в
// избранном, всё ещё скрыт и не прошёл. Корень старта берётся заново, поэтому
// слитое после записи в очередь событие даёт сообщение о корневом.
func (n *Notifier) prepare(ctx context.Context, row store.OutboxRow, now time.Time) (prepared, error) {
	b := n.b
	switch row.Kind {
	case store.OutboxReminder:
		return n.prepareReminder(ctx, row, now)
	case store.OutboxFuse:
		return prepareFuse(row), nil
	}
	if (row.Kind != event.KindChange && row.Kind != event.KindGone) || row.EventID == 0 {
		return prepared{close: store.OutboxRejected}, nil
	}
	if now.Sub(row.CreatedAt) > notifyMaxAge {
		return prepared{close: store.OutboxExpired}, nil
	}
	ch, err := event.ParseChange(row.Kind, row.EventID, row.Payload)
	if err != nil {
		return prepared{close: store.OutboxRejected}, nil
	}
	var lines []string
	if !ch.Gone {
		var ok bool
		if lines, ok = changeLines(ch); !ok {
			return prepared{close: store.OutboxRejected}, nil
		}
	}
	// Исход «условие не выполняется»: у изменения старт недоступен, у пропажи —
	// вернулся в выдачу либо прошёл.
	miss := store.OutboxUnavailable
	if ch.Gone {
		miss = store.OutboxStale
	}

	root, err := b.Store.RootEventID(ctx, row.EventID)
	if errors.Is(err, store.ErrNotFound) {
		return prepared{close: miss}, nil
	}
	if err != nil {
		return prepared{}, err
	}
	fav, err := b.Store.IsFavorite(ctx, row.TgID, root)
	if err != nil {
		return prepared{}, err
	}
	if !fav {
		return prepared{close: store.OutboxUnfollowed}, nil
	}
	e, err := b.Store.GetEvent(ctx, root)
	if errors.Is(err, store.ErrNotFound) {
		return prepared{close: miss}, nil
	}
	if err != nil {
		return prepared{}, err
	}
	sport, _ := event.SportByCode(e.Sport)
	visible := e.IsListed && e.MergedIntoID == nil && sport.Enabled
	past := e.StartDate.Before(b.today())
	if ch.Gone {
		if e.IsListed || e.MergedIntoID != nil || past {
			return prepared{close: miss}, nil
		}
		return prepared{sc: goneMessage(e)}, nil
	}
	if !visible || past {
		return prepared{close: miss}, nil
	}
	return prepared{sc: changeMessage(e, lines)}, nil
}

// prepareReminder — подготовка напоминания: условия проверяются заново по
// текущему состоянию. Старт в избранном пользователя, виден, не прошёл, вид
// спорта включён — иначе строка закрывается как «убран» либо «недоступен»;
// статус «состоится», дата не заглушка и до старта по-прежнему ровно remindDays
// дней — иначе «условие больше не выполняется». Строка, созданная перед
// полуночью и не успевшая уйти, наутро закрывается: до старта уже шесть дней.
// Срок годности 72 часа на напоминание не распространяется.
func (n *Notifier) prepareReminder(ctx context.Context, row store.OutboxRow, now time.Time) (prepared, error) {
	b := n.b
	if row.EventID == 0 {
		return prepared{close: store.OutboxRejected}, nil
	}
	root, err := b.Store.RootEventID(ctx, row.EventID)
	if errors.Is(err, store.ErrNotFound) {
		return prepared{close: store.OutboxUnavailable}, nil
	}
	if err != nil {
		return prepared{}, err
	}
	fav, err := b.Store.IsFavorite(ctx, row.TgID, root)
	if err != nil {
		return prepared{}, err
	}
	if !fav {
		return prepared{close: store.OutboxUnfollowed}, nil
	}
	e, err := b.Store.GetEvent(ctx, root)
	if errors.Is(err, store.ErrNotFound) {
		return prepared{close: store.OutboxUnavailable}, nil
	}
	if err != nil {
		return prepared{}, err
	}
	today := n.dayOf(now)
	sport, _ := event.SportByCode(e.Sport)
	if !e.IsListed || e.MergedIntoID != nil || !sport.Enabled || e.StartDate.Before(today) {
		return prepared{close: store.OutboxUnavailable}, nil
	}
	if e.Status != event.StatusScheduled || event.DateTBD(e.Title) || !e.StartDate.Equal(today.AddDate(0, 0, remindDays)) {
		return prepared{close: store.OutboxStale}, nil
	}
	return prepared{sc: reminderMessage(e)}, nil
}

// prepareFuse — подготовка сообщения администратору о предохранителе: у строки
// нет события и нет условий, которые проверяются заново; нечитаемое содержимое
// закрывает строку.
func prepareFuse(row store.OutboxRow) prepared {
	var p struct {
		Source string `json:"source"`
		Run    int64  `json:"run"`
		Held   int    `json:"held"`
	}
	if json.Unmarshal(row.Payload, &p) != nil || p.Source == "" || p.Run <= 0 || p.Held <= 0 {
		return prepared{close: store.OutboxRejected}
	}
	return prepared{sc: fuseMessage(p.Source, p.Run, p.Held)}
}

// changeLines — строки «было → стало» сообщения об изменении в постоянном
// порядке: ссылка, дата, статус. false — сказать нечего либо статус неизвестен:
// строку не прочитать.
func changeLines(c event.Change) ([]string, bool) {
	var lines []string
	if c.Link {
		lines = append(lines, notifyLinkLine)
	}
	if d := c.Date; d != nil {
		lines = append(lines, "📅 Дата: "+notifyDate(d.From, d.FromTBD)+" → "+notifyDate(d.To, d.ToTBD))
	}
	if s := c.Status; s != nil {
		from, ok1 := statusLabels[s.From]
		to, ok2 := statusLabels[s.To]
		if !ok1 || !ok2 {
			return nil, false
		}
		lines = append(lines, "📌 Статус: "+from+" → "+to)
	}
	return lines, len(lines) > 0
}

// notifyDate — дата в строке «было → стало»; у даты-заглушки — пометка.
func notifyDate(d time.Time, tbd bool) string {
	if tbd {
		return ruDate(d) + notifyTBDMark
	}
	return ruDate(d)
}

// startHead — шапка сообщения, как первые строки карточки: значок типа либо
// вида спорта, название (у сообщения об изменении — с пометкой статуса) и
// строка даты. Название экранируется так же, как в карточке.
func startHead(e event.Event, withStatus bool) string {
	icon := typeEmoji(e.Type)
	if sp, ok := event.SportByCode(event.SportOf(string(e.Sport))); ok && sp.Code != event.SportRunning {
		icon = sp.Emoji
	}
	mark := ""
	if withStatus {
		mark = statusMark(e.Status)
	}
	date := "📅 " + ruDate(e.StartDate)
	if e.EndDate != nil && !e.EndDate.Equal(e.StartDate) {
		date += " – " + ruDate(*e.EndDate)
	}
	return icon + " <b>" + mark + html.EscapeString(cut(e.Title, 200)) + "</b>\n" + date
}

// changeMessage — сообщение N-1: заголовок, шапка старта, строки «было → стало»
// и две кнопки — «Открыть старт» и «Убрать из избранного».
func changeMessage(e event.Event, lines []string) screen {
	return screen{
		text: notifyChangeHead + "\n\n" + startHead(e, true) + "\n\n" + strings.Join(lines, "\n"),
		kb:   keyboard(btn(notifyOpenLabel, callback{Kind: cbFavShow, ID: e.ID}), row{favBtn(e.ID, true)}),
	}
}

// reminderMessage — сообщение N-2: заголовок, шапка старта (название и дата из
// текущего состояния корневого события) и те же две кнопки, что у N-1.
func reminderMessage(e event.Event) screen {
	return screen{
		text: notifyRemindHead + "\n\n" + startHead(e, false),
		kb:   keyboard(btn(notifyOpenLabel, callback{Kind: cbFavShow, ID: e.ID}), row{favBtn(e.ID, true)}),
	}
}

// fuseMessage — сообщение N-3 администратору о сработавшем предохранителе:
// обычный текст без клавиатуры, получателя не называет.
func fuseMessage(source string, run int64, held int) screen {
	return screen{
		text:  fmt.Sprintf("Источник %s, прогон %d: стартов с изменением или скрытием — %d, порог — %d. Сообщения пользователям не созданы.", source, run, held, fuseThreshold),
		plain: true,
	}
}

// goneMessage — сообщение N-4 о старте, пропавшем из календаря: нейтральный
// текст без утверждения об отмене и причины; одна кнопка «Убрать из
// избранного» — кнопки «Открыть старт» нет: карточка скрытого старта
// недоступна. Статус события в текст не входит.
func goneMessage(e event.Event) screen {
	return screen{
		text: notifyGoneHead + "\n\n" + startHead(e, false) + "\n\n" + notifyGoneText,
		kb:   keyboard(row{favBtn(e.ID, true)}),
	}
}

// quietAt — момент приходится на ночь по часовому поясу бота (с 23:00 до
// 08:00): сообщение уходит без звука. Границы: 22:59 — со звуком, 23:00 — без
// звука, 07:59 — без звука, 08:00 — со звуком.
func quietAt(t time.Time, loc *time.Location) bool {
	h := t.In(loc).Hour()
	return h >= nightFrom || h < nightTo
}
