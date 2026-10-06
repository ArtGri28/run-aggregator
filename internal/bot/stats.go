package bot

import (
	"context"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

// Сводка анкеты (/stats) — только для администраторов (ADMIN_TG_IDS). Сводка —
// это счётчики по одному признаку: без tg_id, без городов и без сочетаний
// признаков.

// statsMin — группа меньше этого числа показывается как «меньше 5».
const statsMin = 5

// statsNum — число для разбивки: от 1 до statsMin-1 скрывается.
// ponytail: каждая группа скрывается отдельно, поэтому единственную скрытую
// группу разбивки можно вычислить вычитанием из «Предложена». Скрывать вторую
// группу — когда сводку увидит кто-то, у кого нет доступа к базе.
func statsNum(n int) string {
	if n > 0 && n < statsMin {
		return "меньше " + strconv.Itoa(statsMin)
	}
	return strconv.Itoa(n)
}

// statsScreen — текст сводки по счётчикам store.ProfileCounts. Итоги по
// статусам точные, разбивки — через statsNum.
func statsScreen(c map[string]map[string]int) screen {
	st := c["status"]
	offered := 0
	for _, n := range st {
		offered += n
	}
	var sb strings.Builder
	sb.WriteString("📊 Анкета\n\nПользователей: " + strconv.Itoa(c["users"][""]) +
		"\nПредложена: " + strconv.Itoa(offered) +
		"\n✅ Заполнили: " + strconv.Itoa(st[store.ProfileCompleted]) +
		"\n📝 Начали, не закончили: " + strconv.Itoa(st[store.ProfileInProgress]) +
		"\n⏭ Пропустили: " + strconv.Itoa(st[store.ProfileSkipped]) +
		"\n— Не ответили: " + strconv.Itoa(st[store.ProfileNotStarted]))
	section := func(title, dim, none string, opts []profileOption, trim bool) {
		sb.WriteString("\n\n" + title)
		for _, o := range opts {
			label := o.Label
			if trim {
				_, label, _ = strings.Cut(label, " ") // подпись без эмодзи
			}
			sb.WriteString("\n" + label + " — " + statsNum(c[dim][o.Code]))
		}
		sb.WriteString("\n" + none + " — " + statsNum(c[dim][""]))
	}
	section("🚻 Пол", "gender", "не указан", profileGenders, false)
	section("🎂 Возраст", "age", "не указан", profileAges, false)
	section("🏠 Страна", "country", "не указана", profileCountries, false)
	section("🏅 Спорт (можно несколько)", "sport", "не отмечен", profileSports, true)
	return screen{text: sb.String(), plain: true}
}

// onStats — /stats: сводка анкеты. Не администратор получает тот же ответ,
// что на любую неизвестную команду, и ничего не читается.
func (b *Bot) onStats(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil {
		return
	}
	if !b.isAdmin(m.From.ID) {
		b.onText(ctx, api, u)
		return
	}
	sc := screen{text: errNotice, plain: true}
	if c, err := b.Store.ProfileCounts(ctx); err != nil {
		b.logger().Error("stats", "err", err)
	} else {
		sc = statsScreen(c)
	}
	if err := b.send(ctx, m.Chat.ID, sc); err != nil {
		b.logger().Warn("send stats", "err", err)
	}
}
