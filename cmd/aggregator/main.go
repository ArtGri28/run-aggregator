// Command aggregator is the single binary for this project (MVP.md §3):
// subcommands bot, worker, webapp, migrate, healthcheck dispatch from one
// process, one Docker image, different `command`. No CLI framework (MVP.md §3.1).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // Europe/Moscow не зависит от tzdata образа

	"aggregator/internal/alert"
	"aggregator/internal/bot"
	"aggregator/internal/dedup"
	"aggregator/internal/heartbeat"
	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/heroleague"
	"aggregator/internal/ingest/sources/marathonec"
	"aggregator/internal/ingest/sources/myrace"
	"aggregator/internal/ingest/sources/probeg"
	"aggregator/internal/ingest/sources/tristyle"
	"aggregator/internal/store"
	"aggregator/internal/webapp"
)

const (
	usage        = "usage: aggregator <bot|worker|webapp|webapp-secret|migrate|healthcheck [bot|worker|webapp]|unmerge>\n       " + workerUsage + "\n       " + unmergeUsage
	workerUsage  = "usage: aggregator worker [run-once|accept-baseline|sanity-report] --source <код> [--run <id>]"
	unmergeUsage = "usage: aggregator unmerge [-apply -by <tg_id>] <id слитого события>"
)

const (
	// beatEvery — период отметки bot и worker в heartbeats (MON-03).
	beatEvery = time.Minute
	// healthMaxAge — отметка компоненты старше — healthcheck не проходит.
	healthMaxAge = 5 * time.Minute
	// handlersWait — сколько bot ждёт выполняющиеся обработчики при остановке
	// (меньше stop_grace_period в docker-compose.yml).
	handlersWait = 15 * time.Second
)

// errUsage — неверные аргументы: код выхода 2.
var errUsage = errors.New("usage")

func main() {
	// MON-02: структурные логи во всех подкомандах; LOG_FORMAT=text — для чтения глазами.
	var h slog.Handler = slog.NewJSONHandler(os.Stderr, nil)
	if os.Getenv("LOG_FORMAT") == "text" {
		h = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(h))

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// LO-09: после первого сигнала обработка сигналов возвращается к
	// стандартной — повторный Ctrl-C завершает процесс сразу.
	go func() {
		<-ctx.Done()
		stop()
	}()

	subcommand := os.Args[1]
	var err error
	switch subcommand {
	case "migrate":
		err = runMigrate(ctx)
	case "healthcheck":
		err = runHealthcheck(ctx, os.Args[2:])
	case "bot":
		err = runBot(ctx)
	case "worker":
		err = runWorker(ctx, os.Args[2:])
	case "unmerge":
		err = runUnmerge(ctx, os.Args[2:])
	case "webapp":
		err = runWebapp(ctx)
	case "webapp-secret":
		err = runWebappSecret()
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	if errors.Is(err, errUsage) {
		switch subcommand {
		case "unmerge":
			fmt.Fprintln(os.Stderr, unmergeUsage)
		case "healthcheck":
			fmt.Fprintln(os.Stderr, usage)
		default:
			fmt.Fprintln(os.Stderr, workerUsage)
		}
		os.Exit(2)
	}
	if err != nil {
		slog.Error("command failed", "subcommand", subcommand, "error", err)
		os.Exit(1)
	}
}

// databaseURL reads DATABASE_URL from the environment. Its value is never
// logged or included in returned errors (T-02-01) — callers report only
// this fixed message on failure.
func databaseURL() (string, error) {
	dsn, ok := os.LookupEnv("DATABASE_URL")
	if !ok || dsn == "" {
		return "", errors.New("DATABASE_URL не задан")
	}
	return dsn, nil
}

func runMigrate(ctx context.Context) error {
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Migrate(ctx)
}

// runHealthcheck: без аргумента — только БД; bot, worker или webapp — БД и
// отметка компоненты не старше healthMaxAge (docker healthcheck сервиса).
func runHealthcheck(ctx context.Context, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "bot" && args[0] != "worker" && args[0] != "webapp") {
		return errUsage
	}
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	s, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Healthcheck(ctx); err != nil || len(args) == 0 {
		return err
	}
	ages, err := s.HeartbeatAges(ctx)
	if err != nil {
		return err
	}
	return heartbeatFresh(ages, args[0], healthMaxAge)
}

// heartbeatFresh — отметка component есть и не старше max.
func heartbeatFresh(ages map[string]time.Duration, component string, max time.Duration) error {
	age, ok := ages[component]
	switch {
	case !ok:
		return fmt.Errorf("healthcheck: нет отметки %s", component)
	case age > max:
		return fmt.Errorf("healthcheck: отметка %s старше %s", component, max)
	}
	return nil
}

// openStore подключается к БД и проверяет её за 5 с.
func openStore(ctx context.Context) (*store.Store, error) {
	dsn, err := databaseURL()
	if err != nil {
		return nil, err
	}
	s, err := store.Open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	hcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.Healthcheck(hcCtx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// newSources — единственное место регистрации источников: пакет ingest их
// не импортирует. f — сетевой загрузчик (боту не нужен: nil).
// Адаптеры klbamatar и runin (internal/ingest/sources) написаны, но не
// зарегистрированы: на 2026-10-04 они дают только дубли ПроБЕГа.
// MyRace подключается, только если задан MYRACE_TOKEN (Phase 17, D-07): токен
// партнёра уходит только в заголовок запроса.
func newSources(f *ingest.Fetcher) []ingest.Source {
	src := []ingest.Source{marathonec.New(f), probeg.New(f), heroleague.New(f), tristyle.New(f)}
	if token := strings.TrimSpace(os.Getenv("MYRACE_TOKEN")); token != "" {
		src = append(src, myrace.New(f, token))
	}
	return src
}

// newRunner собирает Runner из настроек наблюдения и дедупликации. Worker и
// bot строят его одной функцией: принятие из бота должно наблюдать с теми же
// GoneAfterRuns/GoneAfter, что и worker (Pitfall 11), и запускать тот же
// проход дедупликации с той же конфигурацией (принятие кнопкой — тоже).
func newRunner(s *store.Store, cfg ingest.Config, dcfg dedup.Config) *ingest.Runner {
	return &ingest.Runner{
		Store:         s,
		Raw:           &ingest.RawStore{Dir: cfg.RawDir, TTL: cfg.RawTTL, Keep: 2},
		Thresholds:    cfg.Thresholds,
		RunTimeout:    cfg.RunTimeout,
		GoneAfterRuns: cfg.GoneAfterRuns,
		GoneAfter:     cfg.GoneAfter,
		Now:           time.Now,
		Log:           slog.Default(),
		Fold:          dcfg.Fold(),
		PostApply:     func(ctx context.Context) error { return dedup.Pass(ctx, s, dcfg) },
	}
}

// runBot запускает Telegram-бота (long polling), цикл алертов и сводку
// дублей до отмены ctx. Конфигурация читается первой: ошибки токена,
// ADMIN_TG_IDS, настроек наблюдения и DEDUP_* видны без БД и сети. OWNER_CONTACT боту не нужен: к сайтам он
// не ходит, принятие прогона использует сохранённый снимок.
func runBot(ctx context.Context) error {
	cfg, err := bot.LoadConfig()
	if err != nil {
		return err
	}
	icfg, err := ingest.LoadConfig(false)
	if err != nil {
		return err
	}
	dcfg, err := dedup.LoadConfig()
	if err != nil {
		return err
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	b, err := bot.New(cfg, s)
	if err != nil {
		return err
	}
	b.Runner, b.Sources = newRunner(s, icfg, dcfg), newSources(nil)

	slog.Info("started", "subcommand", "bot", "admins", len(cfg.Admins), "support", cfg.SupportChatID != 0, "dedup_auto_merge", dcfg.AutoMerge)
	if len(cfg.Admins) == 0 {
		slog.Warn("ADMIN_TG_IDS пуст: админ-функции, алерты и сводка дублей отключены")
	}
	loop := &alert.Loop{Store: s, Admins: cfg.Admins, Send: b.SendAlert, Loc: b.Loc(), Log: slog.Default()}
	digest := &dedup.DigestLoop{Store: s, Admins: cfg.Admins, Send: b.SendDupesDigest, Loc: b.Loc(), At: dcfg.DigestAt, Log: slog.Default()}
	// MON-03, MON-04: отметка бота (только пока он работает с Telegram) и
	// инфраструктурные алерты — worker, резервные копии, БД.
	infra := &alert.InfraLoop{Ages: s.HeartbeatAges, Watch: alert.BotWatch(), DBKey: alert.KeyPostgresDown, Admins: cfg.Admins, Send: b.SendAlert}
	// Phase 14: сообщения пользователям из очереди шлёт только бот.
	notify := bot.NewNotifier(b)
	var wg sync.WaitGroup
	wg.Go(func() { loop.Run(ctx) })
	wg.Go(func() { digest.Run(ctx) })
	wg.Go(func() { infra.Run(ctx) })
	wg.Go(func() { notify.Run(ctx) })
	wg.Go(func() {
		heartbeat.Run(ctx, func(ctx context.Context) error { return s.Beat(ctx, "bot") }, beatEvery, func() bool { return b.Healthy(ctx) })
	})
	b.Start(ctx)
	slog.Info("stopping", "subcommand", "bot")
	if !b.Wait(handlersWait) {
		slog.Warn("обработчики не завершились за отведённое время", "wait", handlersWait.String())
	}
	wg.Wait() // store закрывается только после циклов алертов и сводки
	return nil
}

// runWorker: без аргументов — демон-планировщик всех источников; иначе
// run-once (один прогон сейчас), accept-baseline (D-06) или
// sanity-report (D-07) для источника --source.
func runWorker(ctx context.Context, args []string) error {
	var cmd, code string
	var runID int64
	if len(args) > 0 {
		cmd = args[0]
		switch cmd {
		case "run-once", "accept-baseline", "sanity-report":
		default:
			return errUsage
		}
		fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.StringVar(&code, "source", "", "код источника")
		fs.Int64Var(&runID, "run", 0, "id прогона (0 — последний)")
		if err := fs.Parse(args[1:]); err != nil || code == "" || fs.NArg() > 0 || runID < 0 || (cmd == "run-once" && runID != 0) {
			return errUsage
		}
	}

	// Контакт нужен только командам, которые обращаются к сайту.
	cfg, err := ingest.LoadConfig(cmd == "" || cmd == "run-once")
	if err != nil {
		return err
	}
	dcfg, err := dedup.LoadConfig()
	if err != nil {
		return err
	}
	fetcher := ingest.NewFetcher(cfg.Fetch)
	sources := newSources(fetcher)
	var codes []string
	var src ingest.Source
	for _, s := range sources {
		codes = append(codes, s.Code())
		if s.Code() == code {
			src = s
		}
	}
	if cmd != "" && src == nil {
		return fmt.Errorf("неизвестный источник; известные: %s", strings.Join(codes, ", "))
	}

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	runner := newRunner(s, cfg, dcfg)

	switch cmd {
	case "":
		slog.Info("started", "subcommand", "worker", "sources", codes, "dedup_auto_merge", dcfg.AutoMerge)
		// MON-03: отметка worker — отдельной горутиной, прогон её не
		// задерживает; за ботом worker следит без токена Telegram: bot_stale
		// только в лог (решение владельца log-only).
		infra := &alert.InfraLoop{Ages: s.HeartbeatAges, Watch: alert.WorkerWatch()}
		var wg sync.WaitGroup
		wg.Go(func() { infra.Run(ctx) })
		wg.Go(func() {
			heartbeat.Run(ctx, func(ctx context.Context) error { return s.Beat(ctx, "worker") }, beatEvery, nil)
		})
		runner.Loop(ctx, sources, ingest.Schedule{Interval: cfg.Interval, Jitter: cfg.Jitter})
		wg.Wait() // store закрывается только после отметок и слежения
		slog.Info("stopping", "subcommand", "worker")
		return nil
	case "run-once":
		res, err := runner.Run(ctx, src)
		if err != nil {
			return err
		}
		printResult(res)
		if res.Status == ingest.StatusFailed {
			return fmt.Errorf("прогон failed: %s", strings.Join(res.Reasons, "; "))
		}
		return nil
	case "accept-baseline":
		res, err := runner.Accept(ctx, src, runID)
		if err != nil {
			return err
		}
		printResult(res)
		return nil
	default: // sanity-report
		return sanityReport(ctx, s, src.Code(), runID)
	}
}

func printResult(r ingest.Result) {
	fmt.Printf("run %d: %s (%s) fetched=%d parsed=%d rejected=%d expired=%d out_of_scope=%d upserted=%d created=%d changed=%d unchanged=%d missing=%d gone=%d\n",
		r.RunID, r.Status, strings.Join(r.Reasons, "; "), r.Fetched, r.Parsed, r.Rejected, r.Expired, r.OutOfScope,
		r.Upserted, r.Created, r.Changed, r.Unchanged, r.Missing, r.Gone)
}

// sanityReport печатает отчёт D-07 по снимку последнего прогона: снимок
// есть только у последнего прогона suspect до его принятия.
func sanityReport(ctx context.Context, s *store.Store, code string, runID int64) error {
	sourceID, err := s.EnsureSource(ctx, code)
	if err != nil {
		return err
	}
	last, err := s.LastRun(ctx, sourceID)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%s: нет прогонов", code)
	}
	if err != nil {
		return err
	}
	if runID != 0 && runID != last.ID {
		return fmt.Errorf("%s: отчёт строится только по последнему прогону (последний: %d)", code, last.ID)
	}
	if last.Snapshot == nil {
		return fmt.Errorf("%s: прогон %d (%s) — нет снимка: отчёт строится по последнему прогону suspect до принятия", code, last.ID, last.Status)
	}
	var recs []ingest.SourceEvent
	if err := json.Unmarshal(last.Snapshot, &recs); err != nil {
		return fmt.Errorf("%s: снимок прогона %d: %w", code, last.ID, err)
	}
	return ingest.SanityReport(os.Stdout, code, last.ID, last.StartedAt, recs)
}

// webappConfig читает WEBAPP_ADDR (host:port прослушивания во внутренней
// сети) и WEBAPP_SECRET (производный ключ проверки initData, hex 64 символа;
// печатает `aggregator webapp-secret`). Ошибка называет переменную, но не
// повторяет её значение.
func webappConfig() (addr string, secret []byte, err error) {
	addr = os.Getenv("WEBAPP_ADDR")
	if addr == "" {
		return "", nil, errors.New("WEBAPP_ADDR не задан")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", nil, errors.New("WEBAPP_ADDR: ожидается host:port")
	}
	hexKey := os.Getenv("WEBAPP_SECRET")
	if hexKey == "" {
		return "", nil, errors.New("WEBAPP_SECRET не задан")
	}
	secret, err = hex.DecodeString(hexKey)
	if err != nil || len(secret) != sha256.Size {
		return "", nil, errors.New("WEBAPP_SECRET: ожидается hex из 64 символов")
	}
	return addr, secret, nil
}

// runWebapp — HTTP API Mini App (Phase 15, этап 2) до отмены ctx: отметка
// webapp в heartbeats, как у bot и worker; остановка ждёт текущие запросы.
// Токен бота процессу не нужен (D-03). Упрощение: пользователь базы тот же,
// что у bot и worker (D-04); «webapp пишет только в favorites» держит код,
// отдельная роль только для чтения — позже отдельным решением.
func runWebapp(ctx context.Context) error {
	addr, secret, err := webappConfig()
	if err != nil {
		return err
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	srv := webapp.NewServer(addr, webapp.New(s, secret, time.Now, slog.Default()))

	// Отказ прослушивания завершает отметки и ожидание сигнала.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		heartbeat.Run(ctx, func(ctx context.Context) error { return s.Beat(ctx, "webapp") }, beatEvery, nil)
	})
	wg.Go(func() {
		<-ctx.Done()
		if err := webapp.Shutdown(srv); err != nil {
			slog.Warn("webapp shutdown", "err", err)
		}
	})
	slog.Info("started", "subcommand", "webapp")
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	cancel()
	wg.Wait() // store закрывается после отметок и остановки сервера
	slog.Info("stopping", "subcommand", "webapp")
	return err
}

// runWebappSecret печатает производный ключ проверки initData из
// TELEGRAM_BOT_TOKEN одной строкой hex в stdout — значение для WEBAPP_SECRET.
// Формат токена проверяется, как при старте бота; в лог ничего не пишется.
func runWebappSecret() error {
	cfg, err := bot.LoadConfig()
	if err != nil {
		return err
	}
	fmt.Println(hex.EncodeToString(webapp.SecretFromToken(cfg.Token)))
	return nil
}

// unmergeArgs — id слитого события, -apply и -by (tg_id администратора,
// обязателен с -apply); иначе errUsage.
func unmergeArgs(args []string) (id int64, apply bool, by int64, err error) {
	fs := flag.NewFlagSet("unmerge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&apply, "apply", false, "применить отмену; без флага — только проверка")
	fs.Int64Var(&by, "by", 0, "tg_id администратора, отменяющего слияние")
	if fs.Parse(args) != nil || fs.NArg() != 1 {
		return 0, false, 0, errUsage
	}
	id, perr := strconv.ParseInt(fs.Arg(0), 10, 64)
	if perr != nil || id <= 0 || by < 0 || (apply && by == 0) {
		return 0, false, 0, errUsage
	}
	return id, apply, by, nil
}

// runUnmerge — отмена слияния события (D-14 Phase 7): без -apply только
// проверка (режим журнала или класс без журнала, что вернётся), с -apply —
// store.UndoMerge одной транзакцией. Строка UNMERGE — без названий.
func runUnmerge(ctx context.Context, args []string) error {
	id, apply, by, err := unmergeArgs(args)
	if err != nil {
		return err
	}
	dcfg, err := dedup.LoadConfig()
	if err != nil {
		return err
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	r, err := s.UndoMerge(ctx, id, by, dcfg.Fold(), apply)
	if err != nil {
		return err
	}
	fmt.Printf("UNMERGE event=%d mode=%s class=%s merge=%d root=%d records=%d chain=%d applied=%t\n",
		id, r.Mode, r.Class, r.MergeID, r.Root, len(r.Records), len(r.Chain), apply)
	return nil
}
