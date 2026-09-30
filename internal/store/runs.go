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

// RunInterrupted — причина failed прогона, прерванного остановкой
// процесса (SIGTERM, Ctrl-C): это не сбой источника, в
// ConsecutiveFailures он не считается.
const RunInterrupted = "прерван: остановка процесса"

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
	Rejected     int
	OutOfScope   int
	Upserted     int
	Error        string
	Metrics      []byte
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
	// Accepted — прогон принят вручную (ClaimRun): accepted_at = now().
	Accepted bool
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
	tx, err := s.db.Begin(ctx)
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

// FinishRun завершает прогон в running — начатый StartRun или занятый
// ClaimRun (иначе ErrNotFound) — и обнуляет снимки прочих прогонов
// источника: снимок живёт до принятия или до следующего завершённого
// прогона (D-06).
func (s *Store) FinishRun(ctx context.Context, runID int64, f RunFinish) error {
	tx, err := s.db.Begin(ctx)
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
		    out_of_scope = $6, upserted = $7, error = NULLIF($8, ''), metrics = $9, snapshot = $10,
		    accepted_at = CASE WHEN $11 THEN now() ELSE accepted_at END
		WHERE id = $1 AND status = 'running'
		RETURNING source_id`
	var sourceID int64
	err = tx.QueryRow(ctx, q, runID, f.Status, f.Fetched, f.Parsed, f.Rejected, f.OutOfScope, f.Upserted,
		truncateRunes(f.Error, maxRunError), metrics, f.Snapshot, f.Accepted).Scan(&sourceID)
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
		SELECT id, source_id, status, started_at, finished_at, parsed, rejected, out_of_scope, upserted,
		       COALESCE(error, ''), metrics, snapshot, accepted_at
		FROM ingest_runs WHERE source_id = $1 ORDER BY id DESC LIMIT 1`
	var r Run
	err := s.db.QueryRow(ctx, q, sourceID).Scan(&r.ID, &r.SourceID, &r.Status, &r.StartedAt, &r.FinishedAt,
		&r.Parsed, &r.Rejected, &r.OutOfScope, &r.Upserted, &r.Error, &r.Metrics, &r.Snapshot, &r.AcceptedAt)
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
	rows, err := s.db.Query(ctx, q, sourceID, limit)
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

// ClaimRun занимает для принятия (D-06) последний прогон источника в
// suspect со снимком: переводит его в running до FinishRun с Accepted.
// Вызывается в InTx до записи: прогон, начатый раньше, делает принятие
// невозможным (ErrNotAcceptable, ничего не записано), а StartRun,
// начатый позже, ждёт конца транзакции (ux_ingest_runs_running). При
// откате прогон остаётся suspect. Любой другой прогон → ErrNotAcceptable;
// чужой незавершённый StartRun → ErrRunInProgress.
func (s *Store) ClaimRun(ctx context.Context, runID int64) error {
	const q = `
		UPDATE ingest_runs r SET status = 'running'
		WHERE r.id = $1 AND r.status = 'suspect' AND r.snapshot IS NOT NULL
		  AND r.id = (SELECT max(id) FROM ingest_runs WHERE source_id = r.source_id)`
	cmd, err := s.db.Exec(ctx, q, runID)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return ErrRunInProgress
	}
	if err != nil {
		return fmt.Errorf("claim run: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("claim run %d: %w", runID, ErrNotAcceptable)
	}
	return nil
}

// SourceHealth — последний завершённый прогон источника и число failed
// подряд после последнего ok/suspect, кроме прерванных (MON-01).
func (s *Store) SourceHealth(ctx context.Context, sourceID int64) (Health, error) {
	const q = `
		SELECT status, COALESCE(error, ''), started_at,
		       (SELECT count(*) FROM ingest_runs f
		        WHERE f.source_id = $1 AND f.status = 'failed' AND f.error IS DISTINCT FROM $2
		          AND f.id > COALESCE((SELECT max(id) FROM ingest_runs
		                               WHERE source_id = $1 AND status IN ('ok', 'suspect')), 0))
		FROM ingest_runs WHERE source_id = $1 AND status <> 'running'
		ORDER BY id DESC LIMIT 1`
	var h Health
	err := s.db.QueryRow(ctx, q, sourceID, RunInterrupted).Scan(&h.LastStatus, &h.LastReason, &h.LastStartedAt, &h.ConsecutiveFailures)
	if errors.Is(err, pgx.ErrNoRows) {
		return Health{}, nil
	}
	if err != nil {
		return Health{}, fmt.Errorf("source health: %w", err)
	}
	h.HasRuns = true
	return h, nil
}

// FinishedRun — последний завершённый прогон источника для цикла алертов.
type FinishedRun struct {
	SourceID   int64
	Code       string
	RunID      int64
	Status     string
	FinishedAt time.Time
	Error      string
	// Unrecognized — metrics.unrecognized_region.
	Parsed, Rejected, OutOfScope, Unrecognized int
	// HasSnapshot — есть снимок для принятия.
	HasSnapshot bool
}

// LatestFinishedRuns — по одному последнему завершённому (не running)
// прогону на источник; источники без завершённых прогонов не попадают.
func (s *Store) LatestFinishedRuns(ctx context.Context) ([]FinishedRun, error) {
	const q = `
		SELECT DISTINCT ON (r.source_id) r.source_id, s.code, r.id, r.status, r.finished_at, COALESCE(r.error, ''),
		       r.parsed, r.rejected, r.out_of_scope, COALESCE((r.metrics->>'unrecognized_region')::int, 0),
		       (r.snapshot IS NOT NULL)
		FROM ingest_runs r JOIN sources s ON s.id = r.source_id
		WHERE r.status <> 'running' AND r.finished_at IS NOT NULL
		ORDER BY r.source_id, r.id DESC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("latest finished runs: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (FinishedRun, error) {
		var f FinishedRun
		err := row.Scan(&f.SourceID, &f.Code, &f.RunID, &f.Status, &f.FinishedAt, &f.Error,
			&f.Parsed, &f.Rejected, &f.OutOfScope, &f.Unrecognized, &f.HasSnapshot)
		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("latest finished runs: %w", err)
	}
	return out, nil
}

// RecentRun — строка истории прогонов для /runs.
type RecentRun struct {
	Code       string
	ID         int64
	Status     string
	StartedAt  time.Time
	FinishedAt *time.Time
	Parsed     int
	Rejected   int
	OutOfScope int
	Upserted   int
	Error      string
	// HasSnapshot — есть снимок для принятия.
	HasSnapshot bool
}

// RecentRuns — последние perSource прогонов каждого источника (в том числе
// running): по коду источника, внутри источника новые первыми.
func (s *Store) RecentRuns(ctx context.Context, perSource int) ([]RecentRun, error) {
	const q = `
		SELECT code, id, status, started_at, finished_at, parsed, rejected, out_of_scope, upserted, err, has_snapshot
		FROM (
		    SELECT s.code, r.id, r.status, r.started_at, r.finished_at, r.parsed, r.rejected, r.out_of_scope,
		           r.upserted, COALESCE(r.error, '') AS err, (r.snapshot IS NOT NULL) AS has_snapshot,
		           row_number() OVER (PARTITION BY r.source_id ORDER BY r.id DESC) AS rn
		    FROM ingest_runs r JOIN sources s ON s.id = r.source_id) t
		WHERE rn <= $1 ORDER BY code, id DESC`
	rows, err := s.db.Query(ctx, q, perSource)
	if err != nil {
		return nil, fmt.Errorf("recent runs: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (RecentRun, error) {
		var r RecentRun
		err := row.Scan(&r.Code, &r.ID, &r.Status, &r.StartedAt, &r.FinishedAt, &r.Parsed, &r.Rejected,
			&r.OutOfScope, &r.Upserted, &r.Error, &r.HasSnapshot)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("recent runs: %w", err)
	}
	return out, nil
}

// RunSource — код источника прогона; нет прогона → ErrNotFound.
func (s *Store) RunSource(ctx context.Context, runID int64) (string, error) {
	const q = "SELECT s.code FROM ingest_runs r JOIN sources s ON s.id = r.source_id WHERE r.id = $1"
	var code string
	err := s.db.QueryRow(ctx, q, runID).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("run %d source: %w", runID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("run source: %w", err)
	}
	return code, nil
}

// truncateRunes обрезает s до n символов (не байт).
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
