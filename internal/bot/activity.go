package bot

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

// Сводка активности (/activity, Phase 19) — только для администраторов: числа
// без tg_id. Группы частоты от 1 до 4 человек — «меньше 5», как в /stats.

// dayKeep — срок хранения отметок «был сегодня», дней (D-02).
const dayKeep = 90

// activityScreen — текст сводки по store.Activity.
func activityScreen(a store.Activity) screen {
	act := a.Actions
	var sb strings.Builder
	sb.WriteString("📈 Активность (с " + a.Since.Format("02.01.2006") + ")" +
		"\n\nПользователей: " + strconv.Itoa(a.Users) +
		"\nНовых за 7 дней: " + strconv.Itoa(a.New7) +
		"\n🔔 Подписаны на новые старты: " + strconv.Itoa(a.Subs) +
		"\n\n👥 Заходили в бот или Mini App")
	for i, label := range []string{"Сегодня", "За 7 дней", "За 30 дней"} {
		sb.WriteString("\n" + label + " — " + strconv.Itoa(a.Active[i]) + " (из них в Mini App — " + strconv.Itoa(a.App[i]) + ")")
	}
	sb.WriteString("\n\n🔁 Вернулись — " + strconv.Itoa(a.Returned) + " из " + strconv.Itoa(a.Cohort) +
		"\nИз пришедших 8–30 дней назад заходили за последние 7 дней" +
		"\n\nСколько дней заходили за 30 дней")
	for i, label := range []string{"1 день", "2–3 дня", "4–9 дней", "10 и больше"} {
		sb.WriteString("\n" + label + " — " + statsNum(a.Freq[i]))
	}
	sb.WriteString("\n\nДействия за 7 дней" +
		"\n🔎 Поиск — " + strconv.Itoa(act[store.MetricSearchBot]+act[store.MetricSearchApp]) +
		"\n📄 Открыли карточку — " + strconv.Itoa(act[store.MetricCardBot]+act[store.MetricCardApp]) +
		" (Mini App — " + strconv.Itoa(act[store.MetricCardApp]) + ")" +
		"\n⭐ В избранное — " + strconv.Itoa(act[store.MetricFavBot]+act[store.MetricFavApp]) +
		"\n🔔 Напоминаний отправлено — " + strconv.Itoa(a.Reminders))
	// Входы по меткам ссылок (N-8): блок есть, только если они были.
	type src struct {
		label string
		n     int
	}
	var srcs []src
	for m, n := range act {
		if l, ok := strings.CutPrefix(m, store.MetricSourcePrefix); ok && n > 0 {
			srcs = append(srcs, src{l, n})
		}
	}
	slices.SortFunc(srcs, func(x, y src) int { return cmp.Or(y.n-x.n, strings.Compare(x.label, y.label)) })
	if len(srcs) > 0 {
		sb.WriteString("\n\n🔗 Пришли по ссылкам за 7 дней")
		for _, s := range srcs[:min(len(srcs), 10)] {
			sb.WriteString("\n" + s.label + " — " + strconv.Itoa(s.n))
		}
	}
	return screen{text: sb.String(), plain: true}
}

// onActivity — /activity. Не администратор получает тот же ответ, что на
// любую неизвестную команду, и ничего не читается.
func (b *Bot) onActivity(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if !b.isAdmin(m.From.ID) {
		b.onText(ctx, api, u)
		return
	}
	sc := screen{text: errNotice, plain: true}
	today := b.today()
	if a, err := b.Store.Activity(ctx, today, b.now()); err != nil {
		b.logger().Error("activity", "err", err)
	} else {
		if a.Since.IsZero() {
			a.Since = today
		}
		sc = activityScreen(a)
	}
	if err := b.send(ctx, m.Chat.ID, sc); err != nil {
		b.logger().Warn("send activity", "err", err)
	}
}
