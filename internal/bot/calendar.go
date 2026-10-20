package bot

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/ics"
	"aggregator/internal/store"
)

// «Мой календарь» (Phase 21, 21-UX-CONTRACT.md K-1…K-5): избранные будущие
// старты по месяцам, файл .ics одного старта и всех, ссылка подписного
// календаря. У ссылки в базе только SHA-256 токена: показать её второй раз
// нельзя.

const (
	calLabel     = "📅 Мой календарь"
	calFileLabel = "📅 В календарь"
	calHead      = "📅 <b>Мой календарь</b>"
	calEmpty     = "Здесь пока пусто. Добавьте старты в избранное — они появятся здесь по датам."
	calNoDates   = "Нет стартов с точной датой"
	calOneCap    = "📅 Откройте файл и нажмите «Добавить»."
	calNewNotice = "Старая ссылка больше не работает"
	calOffNotice = "🚫 Ссылка отключена"
	calLinkHead  = "🔗 <b>Календарь телефона</b>"
	calLinkHelp  = "iPhone: Настройки → Календарь → Учётные записи → Добавить учётную запись → Другое → Подписной календарь.\n" +
		"Android: Google Календарь на компьютере → «Другие календари» → «+» → «Добавить по URL».\n\n" +
		"Кто получит эту ссылку, увидит ваши избранные старты. Если ссылка попала не туда — нажмите «Новая ссылка»."
	calLinkAgain = "Ссылка уже выдана — она в одном из сообщений выше. Если не найти — нажмите «Новая ссылка»."
	// CalendarName — название календаря в телефоне.
	CalendarName = "Где старт? — избранное"
)

// FeedHash — SHA-256 токена ссылки календаря: так ссылка хранится в базе.
func FeedHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ICSEvent — старт в календаре телефона: название с пометкой статуса, место,
// ссылки на карточку (если известно имя бота) и на источник. Данных
// пользователя нет.
func ICSEvent(e event.Event, srcs []store.EventSource, botName string) ics.Event {
	id := strconv.FormatInt(e.ID, 10)
	ev := ics.Event{UID: "start-" + id, Title: statusMark(e.Status) + e.Title, Start: e.StartDate}
	if e.EndDate != nil {
		ev.End = *e.EndDate
	}
	ev.Location = strings.Join(slicesNonEmpty(e.City, VisibleAddress(e)), ", ")
	var desc []string
	if botName != "" {
		ev.UID += "@" + botName
		ev.URL = "https://t.me/" + botName + "?start=e_" + id
		desc = append(desc, "Карточка: "+ev.URL)
	}
	for _, s := range srcs {
		if validURL(s.URL) {
			desc = append(desc, "Источник: "+s.URL)
			break
		}
	}
	ev.Description = strings.Join(desc, "\n")
	return ev
}

func slicesNonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// FavoriteICS — события календаря из избранных будущих стартов пользователя;
// старты с датой-заглушкой не попадают. Общая для файла и ссылки календаря.
func FavoriteICS(ctx context.Context, s *store.Store, botName string, list []store.ListItem) ([]ics.Event, error) {
	var out []ics.Event
	for _, it := range list {
		if event.DateTBD(it.Title) {
			continue
		}
		e, err := s.GetEvent(ctx, it.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		srcs, err := s.EventSources(ctx, e.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ICSEvent(e, srcs, botName))
	}
	return out, nil
}

// onCalendar обрабатывает k:*.
func (b *Bot) onCalendar(ctx context.Context, q *models.CallbackQuery, c callback) string {
	switch c.Kind {
	case cbCalOpen:
		return b.calOpen(ctx, q)
	case cbCalAll:
		return b.calAll(ctx, q)
	case cbCalFile:
		return b.calOne(ctx, q, c.ID)
	case cbCalLink:
		return b.calLink(ctx, q, false)
	case cbCalNew:
		return b.calLink(ctx, q, true)
	case cbCalOff:
		if err := b.Store.DeleteFeed(ctx, q.From.ID); err != nil {
			b.logger().Error("calendar", "action", "off", "err", err)
			return errNotice
		}
		b.logger().Info("calendar", "action", "off")
		if m := q.Message.Message; m != nil {
			if err := b.showKeyboard(ctx, m.Chat.ID, m.ID, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}); err != nil {
				b.logger().Warn("edit calendar link", "err", err)
			}
		}
		return calOffNotice
	}
	return staleNotice
}

// favorites — избранные будущие старты (не больше предела избранного).
func (b *Bot) favorites(ctx context.Context, tgID int64) ([]store.ListItem, error) {
	items, _, err := b.Store.ListFavorites(ctx, tgID, b.today(), favLimit, 0)
	return items, err
}

// calOpen — K-2.
func (b *Bot) calOpen(ctx context.Context, q *models.CallbackQuery) string {
	items, err := b.favorites(ctx, q.From.ID)
	if err != nil {
		b.logger().Error("calendar", "action", "open", "err", err)
		return errNotice
	}
	if err := b.show(ctx, q, calendarScreen(items, b.today().Year(), b.domain != "")); err != nil {
		b.logger().Warn("show calendar", "err", err)
	}
	return ""
}

// calendarScreen — K-2: старты по месяцам строками как в списке.
func calendarScreen(items []store.ListItem, year int, link bool) screen {
	fav, back := btn("⭐ Избранное", callback{Kind: cbFavOpen}), btn(backLabel, callback{Kind: cbProfile})
	if len(items) == 0 {
		return screen{text: calHead + "\n\n" + calEmpty, kb: keyboard(fav, back)}
	}
	var sb strings.Builder
	sb.WriteString(calHead)
	month := ""
	for _, it := range items {
		if m := upperFirst(monthNames[it.StartDate.Month()-1]) + " " + strconv.Itoa(it.StartDate.Year()); m != month {
			month = m
			sb.WriteString("\n\n<b>" + m + "</b>")
		}
		if sp, ok := event.SportByCode(it.Sport); ok && sp.Code != event.SportRunning {
			it.Title = sp.Emoji + " " + it.Title
		}
		sb.WriteString("\n" + html.EscapeString(eventBtn(it, callback{Kind: cbMenu}, false, year)[0].Text))
	}
	rows := []row{btn("📅 Добавить все в календарь телефона", callback{Kind: cbCalAll})}
	if link {
		rows = append(rows, btn("🔗 Подключить к календарю телефона", callback{Kind: cbCalLink}))
	}
	return screen{text: sb.String(), kb: keyboard(append(rows, fav, back)...)}
}

// calAll — K-3: файл всех стартов.
func (b *Bot) calAll(ctx context.Context, q *models.CallbackQuery) string {
	items, err := b.favorites(ctx, q.From.ID)
	if err == nil {
		var evs []ics.Event
		if evs, err = FavoriteICS(ctx, b.Store, b.name, items); err == nil {
			if len(evs) == 0 {
				return calNoDates
			}
			caption := "📅 Избранные старты: " + strconv.Itoa(len(evs)) + ". Откройте файл и нажмите «Добавить»."
			b.sendFile(ctx, q.From.ID, "starts.ics", ics.Calendar(CalendarName, b.now(), false, evs), caption)
			return ""
		}
	}
	b.logger().Error("calendar", "action", "file", "err", err)
	return errNotice
}

// calOne — K-4: файл одного старта (корень слияния).
func (b *Bot) calOne(ctx context.Context, q *models.CallbackQuery, id int64) string {
	root, err := b.Store.RootEventID(ctx, id)
	var e event.Event
	if err == nil {
		e, err = b.Store.GetEvent(ctx, root)
	}
	if errors.Is(err, store.ErrNotFound) {
		return goneNotice
	}
	if err != nil {
		b.logger().Error("calendar", "action", "file", "err", err)
		return errNotice
	}
	sport, _ := event.SportByCode(e.Sport)
	if !e.IsListed || e.MergedIntoID != nil || !sport.Enabled || event.DateTBD(e.Title) {
		return goneNotice
	}
	srcs, err := b.Store.EventSources(ctx, e.ID)
	if err != nil {
		b.logger().Error("calendar", "action", "file", "err", err)
		return errNotice
	}
	name := "start-" + strconv.FormatInt(e.ID, 10) + ".ics"
	b.sendFile(ctx, q.From.ID, name, ics.Calendar(CalendarName, b.now(), false, []ics.Event{ICSEvent(e, srcs, b.name)}), calOneCap)
	return ""
}

// sendFile отправляет документ; в лог — только исход.
func (b *Bot) sendFile(ctx context.Context, chatID int64, name string, data []byte, caption string) {
	err := withRetry(ctx, func() error {
		_, err := b.api.SendDocument(ctx, &tgbot.SendDocumentParams{ChatID: chatID, Caption: caption,
			Document: &models.InputFileUpload{Filename: name, Data: bytes.NewReader(data)}})
		return err
	})
	b.logKind(err, "sendDocument")
	if err != nil {
		b.logger().Warn("send calendar file", "err", err)
	}
}

// calLink — K-5: renew = false — выдать ссылку, если её ещё нет; true — новая
// ссылка вместо прежней.
func (b *Bot) calLink(ctx context.Context, q *models.CallbackQuery, renew bool) string {
	if b.domain == "" {
		return staleNotice
	}
	kb := keyboard(btn("🔄 Новая ссылка", callback{Kind: cbCalNew}), btn("🚫 Отключить", callback{Kind: cbCalOff}))
	if !renew {
		has, err := b.Store.HasFeed(ctx, q.From.ID)
		if err != nil {
			b.logger().Error("calendar", "action", "link", "err", err)
			return errNotice
		}
		if has {
			if err := b.send(ctx, q.From.ID, screen{text: calLinkHead + "\n\n" + calLinkAgain, kb: kb}); err != nil {
				b.logger().Warn("send calendar link", "err", err)
			}
			return ""
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		b.logger().Error("calendar", "action", "link", "err", err)
		return errNotice
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := b.Store.SetFeed(ctx, q.From.ID, FeedHash(token), b.now()); err != nil {
		b.logger().Error("calendar", "action", "link", "err", err)
		return errNotice
	}
	b.logger().Info("calendar", "action", "link")
	text := calLinkHead + "\n\nПодключите эту ссылку в календаре телефона — он сам будет подтягивать старты из избранного:\n" +
		"https://" + b.domain + "/cal/" + token + ".ics\n\n" + calLinkHelp
	if err := b.send(ctx, q.From.ID, screen{text: text, kb: kb}); err != nil {
		b.logger().Warn("send calendar link", "err", err)
	}
	if renew {
		return calNewNotice
	}
	return ""
}
