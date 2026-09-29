package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrRunInProgress — по источнику уже есть прогон в running
// (уникальный индекс ux_ingest_runs_running).
var ErrRunInProgress = errors.New("по источнику уже идёт прогон")

// ErrNotAcceptable — прогон не последний, не suspect или без снимка (D-06).
var ErrNotAcceptable = errors.New("прогон нельзя принять")

// maxRunError — предел длины ingest_runs.error в символах; стек паники
// и подробности — только в лог.
const maxRunError = 1000

// Run — строка ingest_runs.
type Run struct {
	ID, SourceID int64
	Status       string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Parsed       int
	Upserted     int
	Error        string
	// Snapshot — разобранные записи прогона suspect (JSON), nil — нет.
	Snapshot   []byte
	AcceptedAt *time.Time
}

// RunFinish — итог прогона для FinishRun.
type RunFinish struct {
	Status                                          string
	Fetched, Parsed, Rejected, OutOfScope, Upserted int
	// Error — причина; "" хранится как NULL, длиннее 1000 символов обрезается.
	Error string
	// Metrics — JSON-объект (nil → {}); Snapshot — JSON или nil.
	Metrics, Snapshot []byte
}

// BaselineRun — прогон ok из окна базы.
type BaselineRun struct {
	Parsed  int
	Metrics []byte
}

// Health — состояние источника по последнему завершённому прогону (MON-01).
type Health struct {
	HasRuns                bool
	LastStatus, LastReason string
	LastStartedAt          time.Time
	// ConsecutiveFailures — failed подряд после последнего ok/suspect.
	ConsecutiveFailures int
}

// StartRun создаёт прогон в running. startedAt — часы прогона: по ним же
// стадии проверяют даты, и Accept повторяет Prepare с этим моментом.
// Прогоны источника в running, начатые раньше startedAt − staleAfter,
// сначала закрываются как failed (процесс упал, Pitfall 6). Второй живой
// прогон того же источника → ErrRunInProgress.
func (s *Store) StartRun(ctx context.Context, sourceID int64, startedAt time.Time, staleAfter time.Duration) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("start run: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const closeStale = `
		UPDATE ingest_runs
		SET status = 'failed', finished_at = now(), error = 'прерван: процесс остановился'
		WHERE source_id = $1 AND status = 'running' AND started_at < $2`
	if _, err := tx.Exec(ctx, closeStale, sourceID, startedAt.Add(-staleAfter)); err != nil {
		return 0, fmt.Errorf("start run: close stale: %w", err)
	}
	var id int64
	err = tx.QueryRow(ctx,
		"INSERT INTO ingest_runs (source_id, started_at, status) VALUES ($1, $2, 'running') RETURNING id",
		sourceID, startedAt).Scan(&id)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return 0, ErrRunInProgress
	}
	if err != nil {
		return 0, fmt.Errorf("start run: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("start run: commit: %w", err)
	}
	return id, nil
}

// FinishRun завершает прогон в running (иначе ErrNotFound) и обнуляет
// снимки прочих прогонов источника: снимок живёт до принятия или до
// следующего завершённого прогона (D-06).
func (s *Store) FinishRun(ctx context.Context, runID int64, f RunFinish) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finish run: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	metrics := f.Metrics
	if metrics == nil {
		metrics = []byte("{}")
	}
	const q = `
		UPDATE ingest_runs
		SET status = $2, finished_at = now(), fetched = $3, parsed = $4, rejected = $5,
		    out_of_scope = $6, upserted = $7, error = NULLIF($8, ''), metrics = $9, snapshot = $10
		WHERE id = $1 AND status = 'running'
		RETURNING source_id`
	var sourceID int64
	err = tx.QueryRow(ctx, q, runID, f.Status, f.Fetched, f.Parsed, f.Rejected, f.OutOfScope, f.Upserted,
		truncateRunes(f.Error, maxRunError), metrics, f.Snapshot).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("finish run %d: %w", runID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	const clear = "UPDATE ingest_runs SET snapshot = NULL WHERE source_id = $1 AND id <> $2 AND snapshot IS NOT NULL"
	if _, err := tx.Exec(ctx, clear, sourceID, runID); err != nil {
		return fmt.Errorf("finish run: clear snapshots: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finish run: commit: %w", err)
	}
	return nil
}

// LastRun — последний прогон источника (по id); нет прогонов → ErrNotFound.
func (s *Store) LastRun(ctx context.Context, sourceID int64) (Run, error) {
	const q = `
		SELECT id, source_id, status, started_at, finished_at, parsed, upserted,
		       COALESCE(error, ''), snapshot, accepted_at
		FROM ingest_runs WHERE source_id = $1 ORDER BY id DESC LIMIT 1`
	var r Run
	err := s.pool.QueryRow(ctx, q, sourceID).Scan(&r.ID, &r.SourceID, &r.Status, &r.StartedAt, &r.FinishedAt,
		&r.Parsed, &r.Upserted, &r.Error, &r.Snapshot, &r.AcceptedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, fmt.Errorf("last run of source %d: %w", sourceID, ErrNotFound)
	}
	if err != nil {
		return Run{}, fmt.Errorf("last run: %w", err)
	}
	return r, nil
}

// BaselineRuns — окно базы (research Pattern 3, Pitfall 11): прогоны ok
// начиная с последнего принятого вручную, новые первыми, не больше limit.
func (s *Store) BaselineRuns(ctx context.Context, sourceID int64, limit int) ([]BaselineRun, error) {
	const q = `
		SELECT parsed, metrics FROM ingest_runs
		WHERE source_id = $1 AND status = 'ok'
		  AND id >= COALESCE((SELECT max(id) FROM ingest_runs WHERE source_id = $1 AND accepted_at IS NOT NULL), 0)
		ORDER BY id DESC LIMIT $2`
	rows, err := s.pool.Query(ctx, q, sourceID, limit)
	if err != nil {
		return nil, fmt.Errorf("baseline runs: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BaselineRun, error) {
		var b BaselineRun
		err := row.Scan(&b.Parsed, &b.Metrics)
		return b, err
	})
	if err != nil {
		return nil, fmt.Errorf("baseline runs: %w", err)
	}
	return out, nil
}

// AcceptRun принимает последний прогон источника в suspect со снимком
// (D-06): ok, accepted_at, снимок обнуляется, upserted и note (в error)
// записываются. Любой другой прогон → ErrNotAcceptable.
func (s *Store) AcceptRun(ctx context.Context, runID int64, upserted int, note string) error {
	const q = `
		UPDATE ingest_runs r
		SET status = 'ok', accepted_at = now(), snapshot = NULL, upserted = $2, error = NULLIF($3, '')
		WHERE r.id = $1 AND r.status = 'suspect' AND r.snapshot IS NOT NULL
		  AND r.id = (SELECT max(id) FROM ingest_runs WHERE source_id = r.source_id)`
	cmd, err := s.pool.Exec(ctx, q, runID, upserted, truncateRunes(note, maxRunError))
	if err != nil {
		return fmt.Errorf("accept run: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("accept run %d: %w", runID, ErrNotAcceptable)
	}
	return nil
}

// SourceHealth — последний завершённый прогон источника и число failed
// подряд после последнего ok/suspect (MON-01).
func (s *Store) SourceHealth(ctx context.Context, sourceID int64) (Health, error) {
	const q = `
		SELECT status, COALESCE(error, ''), started_at,
		       (SELECT count(*) FROM ingest_runs f
		        WHERE f.source_id = $1 AND f.status = 'failed'
		          AND f.id > COALESCE((SELECT max(id) FROM ingest_runs
		                               WHERE source_id = $1 AND status IN ('ok', 'suspect')), 0))
		FROM ingest_runs WHERE source_id = $1 AND status <> 'running'
		ORDER BY id DESC LIMIT 1`
	var h Health
	err := s.pool.QueryRow(ctx, q, sourceID).Scan(&h.LastStatus, &h.LastReason, &h.LastStartedAt, &h.ConsecutiveFailures)
	if errors.Is(err, pgx.ErrNoRows) {
		return Health{}, nil
	}
	if err != nil {
		return Health{}, fmt.Errorf("source health: %w", err)
	}
	h.HasRuns = true
	return h, nil
}

// truncateRunes обрезает s до n символов (не байт).
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
