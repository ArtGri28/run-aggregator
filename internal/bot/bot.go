package bot

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync/atomic"
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

	// lastErr — время последней ошибки библиотеки Telegram (UnixNano; 0 — не
	// было); active — число выполняющихся обработчиков.
	lastErr atomic.Int64
	active  atomic.Int64
}

const (
	// errQuiet — столько без ошибок библиотеки, чтобы бот считался здоровым.
	errQuiet = 2 * time.Minute
	// getMeTimeout — предел проверки getMe в Healthy.
	getMeTimeout = 10 * time.Second
)

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
		tgbot.WithErrorsHandler(func(err error) {
			b.lastErr.Store(b.now().UnixNano())
			b.logger().Error("telegram", "err", err)
		}),
		tgbot.WithDefaultHandler(b.onDefault),
		tgbot.WithMiddlewares(b.recoverMW, b.privateOnlyMW),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "callback_query"}),
		tgbot.WithMessageTextHandler("start", tgbot.MatchTypeCommandStartOnly, b.onStart),
		tgbot.WithMessageTextHandler("profile", tgbot.MatchTypeCommandStartOnly, b.onProfileCmd),
		tgbot.WithMessageTextHandler("runs", tgbot.MatchTypeCommandStartOnly, b.onRuns),
		tgbot.WithMessageTextHandler("dupes", tgbot.MatchTypeCommandStartOnly, b.onDupes),
		tgbot.WithCallbackQueryDataHandler("m", tgbot.MatchTypeExact, b.cb(b.onMenu)),
		tgbot.WithCallbackQueryDataHandler("f:", tgbot.MatchTypePrefix, b.cb(b.onFilter)),
		tgbot.WithCallbackQueryDataHandler("s:", tgbot.MatchTypePrefix, b.cb(b.onSettings)),
		tgbot.WithCallbackQueryDataHandler("u:", tgbot.MatchTypePrefix, b.cb(b.onProfile)),
		tgbot.WithCallbackQueryDataHandler("p:", tgbot.MatchTypePrefix, b.cb(b.onList)),
		tgbot.WithCallbackQueryDataHandler("e:", tgbot.MatchTypePrefix, b.cb(b.onCard)),
		tgbot.WithCallbackQueryDataHandler("g:", tgbot.MatchTypePrefix, b.cb(b.onSearch)),
		tgbot.WithCallbackQueryDataHandler("r:", tgbot.MatchTypePrefix, b.cb(b.onRun)),
		tgbot.WithCallbackQueryDataHandler("d:", tgbot.MatchTypePrefix, b.cb(b.onDupe)),
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

// Healthy — бот работает с Telegram (LO-06): за последние errQuiet не было
// ошибок библиотеки (её обработчик вызывается на каждой ошибке getUpdates) и
// getMe отвечает. По этому признаку бот ставит свою отметку heartbeats.
func (b *Bot) Healthy(ctx context.Context) bool {
	if last := b.lastErr.Load(); last != 0 && b.now().Sub(time.Unix(0, last)) < errQuiet {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, getMeTimeout)
	defer cancel()
	_, err := b.api.GetMe(ctx)
	return err == nil
}

// Wait ждёт завершения выполняющихся обработчиков не дольше timeout (IN-07);
// true — все завершились. Вызывается после Start при остановке процесса.
func (b *Bot) Wait(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for b.active.Load() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

func (b *Bot) isAdmin(id int64) bool { return b.admins[id] }

// Loc — часовой пояс бота (Europe/Moscow): единственный источник для /runs,
// алертов и сводки дублей (IN-08).
func (b *Bot) Loc() *time.Location { return b.loc }

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
//
// IN-07: обработчик считается выполняющимся (счётчик, а не WaitGroup:
// библиотека запускает обработчики в своих горутинах, и Add одновременно с
// Wait при нуле — ошибка использования) и получает ctx без отмены от SIGTERM:
// иначе начатый ответ ушёл бы с отменённым ctx. Остановку ограничивает Wait.
func (b *Bot) recoverMW(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, api *tgbot.Bot, u *models.Update) {
		b.active.Add(1)
		defer b.active.Add(-1)
		ctx = context.WithoutCancel(ctx)
		defer func() {
			if p := recover(); p != nil {
				slog.Error("handler panic", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			}
		}()
		next(ctx, api, u)
	}
}

// privateOnlyMW пропускает только апдейты из личных чатов. Callback из
// другого чата получает пустой answerCallbackQuery (IN-04), сообщения
// отбрасываются молча; обработчик не вызывается, users не пишется.
func (b *Bot) privateOnlyMW(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, api *tgbot.Bot, u *models.Update) {
		if !isPrivate(u) {
			if u.CallbackQuery != nil {
				b.answer(ctx, u.CallbackQuery, "")
			}
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
// устарела» без вызова fn) и ровно один ответ в defer. fn возвращает текст
// уведомления ("" — без уведомления) и получает ctx с коротким пределом
// ожидания 429 (callbackRetryWait, LO-03), чтобы ответ ушёл вовремя; сам
// ответ — с исходным ctx. Устаревшая кнопка и внутренняя ошибка (errNotice
// от fn либо паника fn) различаются и в ответе, и в логе; в лог идёт только
// вид кнопки, при панике — ещё стек, без данных апдейта.
func (b *Bot) cb(fn func(ctx context.Context, q *models.CallbackQuery, c callback) string) tgbot.HandlerFunc {
	return func(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
		q := u.CallbackQuery
		if q == nil {
			return
		}
		notice := staleNotice
		defer func() {
			if p := recover(); p != nil {
				notice = errNotice
				b.logger().Error("callback internal error", "prefix", cbPrefix(q.Data), "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			} else if notice == errNotice {
				b.logger().Error("callback internal error", "prefix", cbPrefix(q.Data))
			} else if notice == staleNotice {
				b.logger().Info("callback stale", "prefix", cbPrefix(q.Data))
			}
			b.answer(ctx, q, notice)
		}()
		c, err := parseCallback(q.Data)
		if err != nil {
			return
		}
		notice = fn(context.WithValue(ctx, retryWaitKey{}, callbackRetryWait), q, c)
	}
}

// cbPrefix — вид callback для логов: только буква до «:» из известных.
func cbPrefix(data string) string {
	if data == "m" {
		return "m"
	}
	if len(data) > 1 && data[1] == ':' && (data[0] == 'f' || data[0] == 's' || data[0] == 'p' || data[0] == 'e' || data[0] == 'g' || data[0] == 'r' || data[0] == 'd' || data[0] == 'u') {
		return data[:1]
	}
	return "?"
}

// onDefault заменяет обработчик библиотеки, который печатает апдейт целиком.
func (b *Bot) onDefault(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		b.logger().Info("callback stale", "prefix", cbPrefix(u.CallbackQuery.Data))
		b.answer(ctx, u.CallbackQuery, staleNotice)
	case u.Message != nil && u.Message.Text != "":
		b.onText(ctx, api, u)
	}
}
