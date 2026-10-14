package bot

import (
	"context"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

// Сводка качества каталога (/quality, экран A-1) — только для администраторов.
// Только счётчики: без идентификаторов событий и пользователей (D-01). Метрики
// событий точные; «меньше 5» — только в разбивке заявок по причинам (Q-6).

// qualityWindow — окно свежести прогонов, заявок и предложений.
const qualityWindow = 30 * 24 * time.Hour

// share — доля n от total в целых процентах с округлением вниз.
func share(n, total int) string {
	return strconv.Itoa(n*100/total) + "%"
}

// runAge — возраст прогона: меньше часа — минуты, меньше 48 часов — часы,
// иначе — дни; с округлением вниз.
func runAge(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " мин"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " ч"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + " дн"
}

// qualityScreen — текст A-1 по сводке store.Quality на дату today и момент now.
func qualityScreen(q store.Quality, today, now time.Time) screen {
	var sb strings.Builder
	sb.WriteString("📈 Качество каталога — будущие старты с " + today.Format("02.01.2006") +
		"\n\nВсего в выдаче: " + strconv.Itoa(q.Total))
	for _, r := range q.Rows {
		sp, _ := event.SportByCode(r.Sport)
		sb.WriteString("\n\n" + r.Source + " · " + sp.Emoji + " " + sp.Title +
			"\nСтартов " + strconv.Itoa(r.Events) + " · уникальных " + strconv.Itoa(r.Unique) + " (" + share(r.Unique, r.Events) + ")" +
			"\nПолных " + strconv.Itoa(r.Complete) + " (" + share(r.Complete, r.Events) + "): дата " + strconv.Itoa(r.Dated) +
			" · город " + strconv.Itoa(r.WithCity) + " · дистанция " + strconv.Itoa(r.WithDistance) +
			" · ссылка " + strconv.Itoa(r.WithLink))
	}
	sb.WriteString("\n\n🕒 Прогоны")
	for _, s := range q.Sources {
		if !s.HasRun {
			sb.WriteString("\n" + s.Code + " — прогонов нет")
			continue
		}
		sb.WriteString("\n" + s.Code + " — " + s.Status + ", " + runAge(now.Sub(s.FinishedAt)) + " назад · за 30 дней создано " +
			strconv.Itoa(s.Created) + ", изменено " + strconv.Itoa(s.Changed))
	}
	total := 0
	for _, n := range q.Reports {
		total += n
	}
	sb.WriteString("\n\n📮 Заявки за 30 дней\nОшибки в карточках: " + strconv.Itoa(total))
	for _, r := range reportReasons {
		sb.WriteString("\n" + r.word + " — " + statsNum(q.Reports[r.code]))
	}
	sb.WriteString("\nПредложения стартов: " + strconv.Itoa(q.Suggestions))
	return screen{text: cut(sb.String(), maxAdminText), plain: true}
}

// onQuality — /quality: сводка качества каталога. Не администратор получает тот
// же ответ, что на любую неизвестную команду, и ничего не читается.
func (b *Bot) onQuality(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if !b.isAdmin(m.From.ID) {
		b.onText(ctx, api, u)
		return
	}
	now := b.now()
	sc := screen{text: errNotice, plain: true}
	if q, err := b.Store.Quality(ctx, b.today(), now.Add(-qualityWindow)); err != nil {
		b.logger().Error("quality", "err", err)
	} else {
		sc = qualityScreen(q, b.today(), now)
	}
	if err := b.send(ctx, m.Chat.ID, sc); err != nil {
		b.logger().Warn("send quality", "err", err)
	}
}
