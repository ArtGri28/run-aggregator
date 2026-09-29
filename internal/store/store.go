// Package store is the sole owner of SQL in this project (MVP.md §3):
// every query against PostgreSQL lives here, parameterized with $N
// placeholders, never built by string formatting or concatenation.
package store

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/migrations"
)

// Store wraps a pgxpool connection pool: the single entry point Phase 3+
// use to read and write PostgreSQL (02-CONTEXT.md Integration Points).
type Store struct {
	pool *pgxpool.Pool
}

// Open parses dsn and opens a connection pool. On a parse error it returns
// a fixed message referencing DATABASE_URL WITHOUT wrapping the original
// error, because pgxpool's parse error can echo the connection string
// (including the password) back verbatim (T-02-01).
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: не удалось разобрать строку подключения")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: не удалось открыть пул соединений")
	}
	return &Store{pool: pool}, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() {
	s.pool.Close()
}

// Healthcheck performs a real round-trip to PostgreSQL (D-02): SELECT 1
// through the pool. A non-nil error means the database is not reachable
// or not answering queries.
func (s *Store) Healthcheck(ctx context.Context) error {
	var one int
	if err := s.pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("healthcheck: select 1 failed")
	}
	return nil
}

// Migrate applies every embedded migration in migrations.FS via goose's
// Provider API (in-binary, no separate goose CLI — 02-RESEARCH Pattern 2).
// It is idempotent: migrations already applied are skipped.
func (s *Store) Migrate(ctx context.Context) error {
	db := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("new goose provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		slog.Info("migration applied", "version", r.Source.Path, "duration", r.Duration)
	}
	return nil
}
