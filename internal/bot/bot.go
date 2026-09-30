package bot

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/ingest"
	"aggregator/internal/store"
)

const staleNotice = "Кнопка устарела — нажмите /start"

// Bot — Telegram-бот. Now nil — time.Now. Runner и Sources нужны только
// для принятия прогона кнопкой (nil — принятие недоступно).
type Bot struct {
	Store   *store.Store
	Now     func() time.Time
	Runner  *ingest.Runner
	Sources []ingest.Source

	api    *tgbot.Bot
	admins map[int64]bool
	loc    *time.Location
	log    *slog.Logger
	token  string // только для маскирования в логах (redactHandler)
}

// New собирает бота. Дополнительные opts добавляются последними (тесты
// подменяют адрес API). Отладочный режим библиотеки не включается: он
// печатает содержимое запросов. Значение токена в ошибку не попадает.
func New(cfg Config, s *store.Store, opts ...tgbot.Option) (*Bot, error) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return nil, fmt.Errorf("часовой пояс Europe/Moscow: %w", err)
	}
	b := &Bot{Store: s, admins: map[int64]bool{}, loc: loc, token: cfg.Token}
	for _, id := range cfg.Admins {
		b.admins[id] = true
	}

	all := []tgbot.Option{
		tgbot.WithCheckInitTimeout(15 * time.Second),
		tgbot.WithErrorsHandler(func(err error) { b.logger().Error("telegram", "err", err) }),
		tgbot.WithDefaultHandler(b.onDefault),
		tgbot.WithMiddlewares(recoverMW, privateOnlyMW),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "callback_query"}),
		tgbot.WithMessageTextHandler("start", tgbot.MatchTypeCommandStartOnly, b.onStart),
		tgbot.WithMessageTextHandler("runs", tgbot.MatchTypeCommandStartOnly, b.onRuns),
		tgbot.WithCallbackQueryDataHandler("f:", tgbot.MatchTypePrefix, b.cb(b.onFilter)),
		tgbot.WithCallbackQueryDataHandler("p:", tgbot.MatchTypePrefix, b.cb(b.onList)),
		tgbot.WithCallbackQueryDataHandler("e:", tgbot.MatchTypePrefix, b.cb(b.onCard)),
		tgbot.WithCallbackQueryDataHandler("r:", tgbot.MatchTypePrefix, b.cb(b.onRun)),
	}
	api, err := tgbot.New(cfg.Token, append(all, opts...)...)
	if err != nil {
		// Библиотека маскирует токен в URL сетевой ошибки; на всякий случай
		// значение не выводится и здесь.
		return nil, fmt.Errorf("создание бота: %s", strings.ReplaceAll(err.Error(), cfg.Token, "***"))
	}
	b.api = api
	return b, nil
}

// Start принимает апдейты (long polling) до отмены ctx.
func (b *Bot) Start(ctx context.Context) { b.api.Start(ctx) }

func (b *Bot) isAdmin(id int64) bool { return b.admins[id] }

// logger — логгер бота с маскированием токена. Библиотека сама заменяет токен
// в URL сетевой ошибки; здесь — вторая линия для любых других текстов.
func (b *Bot) logger() *slog.Logger {
	l := b.log
	if l == nil {
		l = slog.Default()
	}
	if b.token == "" {
		return l
	}
	return slog.New(redactHandler{Handler: l.Handler(), secret: b.token})
}

// redactHandler заменяет secret на *** в сообщении и во всех значениях записи
// (включая группы и атрибуты из With).
type redactHandler struct {
	slog.Handler
	secret string
}

func (h redactHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, h.mask(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(h.attr(a))
		return true
	})
	return h.Handler.Handle(ctx, nr)
}

func (h redactHandler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = h.attr(a)
	}
	return redactHandler{Handler: h.Handler.WithAttrs(out), secret: h.secret}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{Handler: h.Handler.WithGroup(name), secret: h.secret}
}

func (h redactHandler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		g := v.Group()
		out := make([]any, len(g))
		for i, x := range g {
			out[i] = h.attr(x)
		}
		return slog.Group(a.Key, out...)
	}
	if s := v.String(); strings.Contains(s, h.secret) {
		return slog.String(a.Key, h.mask(s))
	}
	return slog.Attr{Key: a.Key, Value: v}
}

func (h redactHandler) mask(s string) string { return strings.ReplaceAll(s, h.secret, "***") }

func (b *Bot) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// today — сегодняшняя календарная дата по Москве как полночь UTC (так
// хранятся start_date); даты событий в другой пояс не переводятся.
func (b *Bot) today() time.Time {
	y, m, d := b.now().In(b.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// recoverMW ловит панику обработчика (библиотека запускает его в горутине
// без recover — панику не поймать иначе, процесс упал бы). В лог идёт
// только стек, без данных апдейта.
func recoverMW(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, api *tgbot.Bot, u *models.Update) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("handler panic", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			}
		}()
		next(ctx, api, u)
	}
}

// privateOnlyMW пропускает только апдейты из личных чатов; остальные
// молча отбрасываются, без ответа и без записи в users.
func privateOnlyMW(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, api *tgbot.Bot, u *models.Update) {
		if !isPrivate(u) {
			return
		}
		next(ctx, api, u)
	}
}

func isPrivate(u *models.Update) bool {
	switch {
	case u.Message != nil:
		return u.Message.Chat.Type == models.ChatTypePrivate
	case u.CallbackQuery != nil:
		m := u.CallbackQuery.Message
		if m.Message != nil {
			return m.Message.Chat.Type == models.ChatTypePrivate
		}
		if m.InaccessibleMessage != nil {
			return m.InaccessibleMessage.Chat.Type == models.ChatTypePrivate
		}
	}
	return false
}

// cb оборачивает обработчик callback: разбор data (ошибка — «кнопка
// устарела» без вызова fn) и ровно один ответ в defer — в том числе при
// панике fn. fn возвращает текст уведомления ("" — без уведомления).
func (b *Bot) cb(fn func(ctx context.Context, q *models.CallbackQuery, c callback) string) tgbot.HandlerFunc {
	return func(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
		q := u.CallbackQuery
		if q == nil {
			return
		}
		notice := staleNotice
		defer func() { b.answer(ctx, q, notice) }()
		c, err := parseCallback(q.Data)
		if err != nil {
			b.logger().Info("callback rejected", "prefix", cbPrefix(q.Data))
			return
		}
		notice = "" // при панике fn ответ уйдёт без текста
		notice = fn(ctx, q, c)
	}
}

// cbPrefix — вид callback для логов: только буква до «:» из известных.
func cbPrefix(data string) string {
	if len(data) > 1 && data[1] == ':' && (data[0] == 'f' || data[0] == 'p' || data[0] == 'e' || data[0] == 'r') {
		return data[:1]
	}
	return "?"
}

// onDefault заменяет обработчик библиотеки, который печатает апдейт целиком.
func (b *Bot) onDefault(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		b.logger().Info("callback unknown", "prefix", cbPrefix(u.CallbackQuery.Data))
		b.answer(ctx, u.CallbackQuery, staleNotice)
	case u.Message != nil && u.Message.Text != "":
		b.onText(ctx, api, u)
	}
}
