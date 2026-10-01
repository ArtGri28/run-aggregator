// Command aggregator is the single binary for this project (MVP.md §3):
// subcommands bot, worker, migrate, healthcheck dispatch from one process,
// one Docker image, different `command`. No CLI framework (MVP.md §3.1).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // Europe/Moscow не зависит от tzdata образа

	"aggregator/internal/alert"
	"aggregator/internal/bot"
	"aggregator/internal/dedup"
	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/heroleague"
	"aggregator/internal/ingest/sources/marathonec"
	"aggregator/internal/ingest/sources/probeg"
	"aggregator/internal/store"
)

const (
	usage       = "usage: aggregator <bot|worker|migrate|healthcheck>\n       " + workerUsage
	workerUsage = "usage: aggregator worker [run-once|accept-baseline|sanity-report] --source <код> [--run <id>]"
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
		err = runHealthcheck(ctx)
	case "bot":
		err = runBot(ctx)
	case "worker":
		err = runWorker(ctx, os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	if errors.Is(err, errUsage) {
		fmt.Fprintln(os.Stderr, workerUsage)
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

func runHealthcheck(ctx context.Context) error {
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
	return s.Healthcheck(ctx)
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
func newSources(f *ingest.Fetcher) []ingest.Source {
	return []ingest.Source{marathonec.New(f), probeg.New(f), heroleague.New(f)}
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

	slog.Info("started", "subcommand", "bot", "admins", len(cfg.Admins), "dedup_auto_merge", dcfg.AutoMerge)
	if len(cfg.Admins) == 0 {
		slog.Warn("ADMIN_TG_IDS пуст: админ-функции, алерты и сводка дублей отключены")
	}
	loop := &alert.Loop{Store: s, Admins: cfg.Admins, Send: b.SendAlert, Loc: b.Loc(), Log: slog.Default()}
	digest := &dedup.DigestLoop{Store: s, Admins: cfg.Admins, Send: b.SendDupesDigest, Loc: b.Loc(), At: dcfg.DigestAt, Log: slog.Default()}
	var wg sync.WaitGroup
	wg.Go(func() { loop.Run(ctx) })
	wg.Go(func() { digest.Run(ctx) })
	b.Start(ctx)
	slog.Info("stopping", "subcommand", "bot")
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
		runner.Loop(ctx, sources, ingest.Schedule{Interval: cfg.Interval, Jitter: cfg.Jitter})
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
