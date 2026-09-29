package store

import (
	"context"
	"fmt"
	"time"
)

// ObserveStats — число строк каждого перехода одного вызова Observe.
type ObserveStats struct {
	// Seen — вернулись в active (были missing/gone или с пропусками).
	Seen int64
	// Missing — не найдены прогоном и остались missing.
	Missing int64
	// Gone — перешли в gone в этом вызове.
	Gone int64
	// ListedChanged — событий с изменённым is_listed.
	ListedChanged int64
}

const (
	observeSeenSQL = `
		UPDATE source_events SET state = 'active', missed_runs = 0
		WHERE source_id = $1 AND last_run_id = $2 AND (state <> 'active' OR missed_runs <> 0)`

	// Только записи с событием и start_date >= today; уже gone не трогаются.
	observeMissingSQL = `
		UPDATE source_events se
		SET missed_runs = se.missed_runs + 1,
		    state = CASE WHEN se.missed_runs + 1 >= $4 AND now() - se.last_seen_at >= $5
		                 THEN 'gone' ELSE 'missing' END
		FROM events e
		WHERE se.source_id = $1 AND se.last_run_id IS DISTINCT FROM $2 AND se.state <> 'gone'
		  AND se.event_id = e.id AND e.start_date >= $3
		RETURNING se.state`

	// is_listed = не все связанные source_events в gone; updated_at не
	// трогается (ING-05), status не меняется никогда (MVP.md §4.4).
	observeListedSQL = `
		UPDATE events e SET is_listed = x.listed
		FROM (SELECT event_id, bool_or(state <> 'gone') AS listed FROM source_events
		      WHERE event_id IN (SELECT event_id FROM source_events WHERE source_id = $1 AND event_id IS NOT NULL)
		      GROUP BY event_id) x
		WHERE e.id = x.event_id AND e.merged_into_id IS NULL AND e.is_listed <> x.listed`
)

// Observe ведёт состояние наблюдения source_events источника после
// здорового прогона runID (ING-08, MVP.md §4.4) одной транзакцией:
// найденные прогоном (last_run_id = runID) → active; ненайденные с
// событием в будущем → missing, а при missed_runs >= goneAfterRuns и
// goneAfter с last_seen_at → gone; затем is_listed событий источника.
// Вызывается только для прогона ok и при принятии прогона.
func (s *Store) Observe(ctx context.Context, sourceID, runID int64, today time.Time, goneAfterRuns int, goneAfter time.Duration) (ObserveStats, error) {
	var st ObserveStats
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return st, fmt.Errorf("observe: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cmd, err := tx.Exec(ctx, observeSeenSQL, sourceID, runID)
	if err != nil {
		return st, fmt.Errorf("observe: seen: %w", err)
	}
	st.Seen = cmd.RowsAffected()

	rows, err := tx.Query(ctx, observeMissingSQL, sourceID, runID, dateOnly(today), goneAfterRuns, goneAfter)
	if err != nil {
		return st, fmt.Errorf("observe: missing: %w", err)
	}
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			rows.Close()
			return st, fmt.Errorf("observe: missing: %w", err)
		}
		if state == "gone" {
			st.Gone++
		} else {
			st.Missing++
		}
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("observe: missing: %w", err)
	}

	if cmd, err = tx.Exec(ctx, observeListedSQL, sourceID); err != nil {
		return st, fmt.Errorf("observe: is_listed: %w", err)
	}
	st.ListedChanged = cmd.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		return st, fmt.Errorf("observe: commit: %w", err)
	}
	return st, nil
}
