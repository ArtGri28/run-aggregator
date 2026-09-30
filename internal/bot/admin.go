package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

// msk — Москва без перехода на летнее время: фиксированная зона не зависит
// от tzdata.
var msk = time.FixedZone("MSK", 3*3600)

const (
	runsPerSource = 3
	maxRunReason  = 200
	maxAdminText  = 4000 // запас до лимита Telegram в 4096
)

var errNotAdmin = errors.New("получатель не администратор")

// onRuns — /runs (BOT-07): последние прогоны по источникам. Не администратор
// получает тот же ответ, что на любую неизвестную команду, и ничего не
// читается из БД.
func (b *Bot) onRuns(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if !b.isAdmin(m.From.ID) {
		b.onText(ctx, api, u)
		return
	}
	runs, err := b.Store.RecentRuns(ctx, runsPerSource)
	sc := screen{plain: true}
	if err != nil {
		b.logger().Error("recent runs", "err", err)
		sc.text = errNotice
	} else {
		sc = b.runsScreen(runs)
	}
	if err := b.send(ctx, m.Chat.ID, sc); err != nil {
		b.logger().Warn("send runs", "err", err)
	}
}

// runsScreen — обычный текст: источник заголовком, под ним его прогоны;
// кнопка принятия — у источника, чей последний прогон suspect со снимком.
func (b *Bot) runsScreen(runs []store.RecentRun) screen {
	if len(runs) == 0 {
		return screen{text: "Прогонов пока нет", plain: true}
	}
	var t strings.Builder
	var rows []row
	fmt.Fprintf(&t, "Прогоны — последние %d по источнику", runsPerSource)
	prev := ""
	for _, r := range runs {
		if r.Code != prev {
			// Внутри источника новые первыми: первый прогон группы — последний.
			prev = r.Code
			fmt.Fprintf(&t, "\n\n%s", r.Code)
			if r.Status == "suspect" && r.HasSnapshot {
				rows = append(rows, btn(fmt.Sprintf("Принять прогон %d (%s)", r.ID, r.Code), callback{Kind: cbRunAsk, ID: r.ID}))
			}
		}
		fmt.Fprintf(&t, "\n#%d %s %s МСК · parsed %d · rej %d · oos %d · upserted %d",
			r.ID, r.Status, r.StartedAt.In(msk).Format("02.01 15:04"), r.Parsed, r.Rejected, r.OutOfScope, r.Upserted)
		if r.Error != "" {
			t.WriteString(" · " + cut(r.Error, maxRunReason))
		}
	}
	return screen{text: cut(t.String(), maxAdminText), kb: keyboard(rows...), plain: true}
}

// onRun обрабатывает r:a / r:y / r:n. Администратор проверяется на каждом
// нажатии: кнопку мог прислать кто угодно, в том числе вручную собранным
// callback.
func (b *Bot) onRun(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if !b.isAdmin(q.From.ID) {
		b.logger().Warn("admin callback from non-admin")
		return ""
	}
	if b.Runner == nil {
		return "Принятие недоступно"
	}
	say := func(text string) string {
		if err := b.show(ctx, q, screen{text: text, plain: true}); err != nil {
			b.logger().Warn("show run", "err", err)
		}
		return ""
	}
	if c.Kind == cbRunNo {
		return say(fmt.Sprintf("Отменено. Прогон %d не принят.", c.ID))
	}
	code, err := b.Store.RunSource(ctx, c.ID)
	if errors.Is(err, store.ErrNotFound) {
		return "Прогон не найден"
	}
	if err != nil {
		b.logger().Error("run source", "err", err)
		return errNotice
	}
	if c.Kind == cbRunAsk {
		var yn row
		yn.add("Да, принять", callback{Kind: cbRunYes, ID: c.ID})
		yn.add("Отмена", callback{Kind: cbRunNo, ID: c.ID})
		sc := screen{plain: true, kb: keyboard(yn),
			text: fmt.Sprintf("Принять прогон %d (%s) как базу? Будет применён сохранённый снимок, к сайту запросов нет.", c.ID, code)}
		if err := b.show(ctx, q, sc); err != nil {
			b.logger().Warn("show run", "err", err)
		}
		return ""
	}

	for _, src := range b.Sources {
		if src.Code() != code {
			continue
		}
		res, err := b.Runner.Accept(ctx, src, c.ID)
		switch {
		case err == nil:
			return say(fmt.Sprintf("Принято: прогон %d (%s) — создано %d, изменено %d, без изменений %d, скрыто %d",
				c.ID, code, res.Created, res.Changed, res.Unchanged, res.Gone))
		case errors.Is(err, store.ErrNotAcceptable):
			return say("Уже нельзя принять: прогон не последний, не suspect или уже принят. Актуальное состояние — /runs")
		case errors.Is(err, store.ErrRunInProgress):
			return say("Сейчас идёт прогон источника — повторите позже")
		}
		b.logger().Error("accept run", "source", code, "run_id", c.ID, "err", err)
		return say("Не удалось принять прогон, подробности в логе")
	}
	return say(fmt.Sprintf("Источник %s не подключён к боту", code))
}

// SendAlert отправляет алерт администратору обычным текстом (в причинах
// бывают «<» и «>»); acceptRunID > 0 — с кнопкой принятия. Сигнатура
// совпадает с alert.Loop.Send.
func (b *Bot) SendAlert(ctx context.Context, chatID int64, text string, acceptRunID int64) error {
	if !b.isAdmin(chatID) {
		return errNotAdmin
	}
	sc := screen{text: cut(text, maxAdminText), plain: true}
	if acceptRunID > 0 {
		sc.kb = keyboard(btn("Принять как базу", callback{Kind: cbRunAsk, ID: acceptRunID}))
	}
	return b.send(ctx, chatID, sc)
}
