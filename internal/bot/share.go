package bot

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)

// «Поделиться стартом» и метки входа (Phase 20, 20-UX-CONTRACT.md N-1, N-2,
// N-8): ссылка https://t.me/<бот>?start=e_<id> открывает карточку старта,
// ?start=src_<метка> — главное меню; вход считается счётчиком метки без
// привязки к пользователю.

const (
	shareLabel = "📤 Поделиться"
	// shareSource — метка входа по ссылке на старт.
	shareSource = "share"
)

var sourceRE = regexp.MustCompile(`^[a-z0-9_]{1,20}$`)

// shareURL — окно Telegram «Переслать» со ссылкой на старт id и его названием.
func shareURL(botName string, id int64, title string) string {
	link := "https://t.me/" + botName + "?start=e_" + strconv.FormatInt(id, 10)
	return "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape(title)
}

// startArg — параметр /start: событие из e_<id> либо метка из src_<метка>;
// иначе нули — обычный /start.
func startArg(text string) (id int64, source string) {
	f := strings.Fields(text)
	if len(f) != 2 {
		return 0, ""
	}
	if s, ok := strings.CutPrefix(f[1], "e_"); ok {
		if n, err := num(s, 1, 1<<62); err == nil {
			return n, ""
		}
	}
	if s, ok := strings.CutPrefix(f[1], "src_"); ok && sourceRE.MatchString(s) {
		return 0, s
	}
	return 0, ""
}

// startCard — /start e_<id>: карточка новым сообщением с возвратом в меню.
// Пользователь создаётся без строки профиля: анкету предложит следующий
// обычный /start.
func (b *Bot) startCard(ctx context.Context, m *models.Message, id int64) {
	b.count(ctx, store.MetricSourcePrefix+shareSource)
	if _, _, err := b.Store.Filters(ctx, m.From.ID); err != nil {
		b.logger().Error("start", "err", err)
		return
	}
	sc, _, ok := b.buildCard(ctx, m.From.ID, callback{Kind: cbCard, ID: id, Legacy: true})
	if !ok {
		return
	}
	if err := b.send(ctx, m.Chat.ID, sc); err != nil {
		b.logger().Warn("send card", "err", err)
	}
	b.count(ctx, store.MetricCardBot)
}
