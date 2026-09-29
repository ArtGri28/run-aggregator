// Command aggregator is the single binary for this project (MVP.md §3):
// subcommands bot, worker, migrate, healthcheck dispatch from one process,
// one Docker image, different `command`. No CLI framework (MVP.md §3.1).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aggregator/internal/store"
)

const usage = "usage: aggregator <bot|worker|migrate|healthcheck>"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	subcommand := os.Args[1]
	var err error
	switch subcommand {
	case "migrate":
		err = runMigrate(ctx)
	case "healthcheck":
		err = runHealthcheck(ctx)
	case "bot", "worker":
		err = runProcess(ctx, subcommand)
	default:
		fmt.Fprintln(os.Stderr, usage)
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

// runProcess is the shared skeleton for bot and worker in this phase:
// connect, healthcheck, log, wait for the signal-derived context to be
// cancelled, log, close (A5 — Phase 3/4 add their own loops inside it).
func runProcess(ctx context.Context, subcommand string) error {
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()

	hcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.Healthcheck(hcCtx)
	cancel()
	if err != nil {
		return err
	}

	slog.Info("started", "subcommand", subcommand)
	<-ctx.Done()
	slog.Info("stopping", "subcommand", subcommand)
	return nil
}
