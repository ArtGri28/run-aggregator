package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// ErrNotUndoable — слияние нельзя отменить точно: нет журнала и класс не A,
// данные после слияния изменились или его записи перенесло более позднее
// действующее слияние (отменять — с последнего). Данные не меняются.
var ErrNotUndoable = errors.New("merge cannot be undone exactly")

// inventoryQuery — классы обратимости слияний без журнала (07-UNMERGE.md,
// тот же текст; TestUnmergeInventory сверяет): A — ручное слияние через
// /dupes, проигравший найден однозначно и сам ничего не поглощал; B — связь
// есть, но не однозначна или проигравший поглощал другие; C — связи нет.
const inventoryQuery = `WITH manual AS (
    SELECT dc.id AS cid, dc.source_event_id AS r, dc.event_id AS w
    FROM dedup_candidates dc
    WHERE dc.state = 'confirmed' AND dc.decided_by IS NOT NULL
),
link AS (
    SELECT m.id AS l, manual.cid, manual.r
    FROM events m
    JOIN source_events se ON se.first_seen_at = m.created_at
    JOIN manual ON manual.r = se.id
    WHERE m.merged_into_id IS NOT NULL
      AND se.fields->>'title_norm' = m.title_norm
      AND se.fields->>'start_date' = to_char(m.start_date, 'YYYY-MM-DD')
)
SELECT m.id AS event_id,
       m.merged_into_id AS root_id,
       CASE
           WHEN NOT EXISTS (SELECT 1 FROM link WHERE link.l = m.id) THEN 'C'
           WHEN (SELECT count(*) FROM link WHERE link.l = m.id) = 1
                AND (SELECT count(*) FROM link x WHERE x.r = (SELECT r FROM link WHERE link.l = m.id)) = 1
                AND NOT EXISTS (SELECT 1 FROM manual WHERE manual.w = m.id) THEN 'A'
           ELSE 'B'
       END AS class,
       (SELECT min(r) FROM link WHERE link.l = m.id) AS record_id
FROM events m
WHERE m.merged_into_id IS NOT NULL
ORDER BY m.id`

// UndoResult — что отменяется (или отменено) для проигравшего.
type UndoResult struct {
	// Mode: journal — по журналу event_merges; legacy — слияние до журнала,
	// класс A по inventoryQuery.
	Mode    string
	Class   string // legacy: класс по inventoryQuery
	MergeID int64  // journal: строка event_merges
	Root    int64  // текущий корень, у которого записи проигравшего
	Records []int64
	Chain   []int64 // события, возвращаемые под проигравшего
}

// UndoMerge отменяет слияние проигравшего loserID (D-14 Phase 7, вариант b+c)
// одной транзакцией под LockDedup. По журналу — последнее действующее слияние
// с этим проигравшим: записи и события цепочки возвращаются, match_state —
// прежний (out_of_scope, выставленный после слияния, сохраняется). Без
// журнала — только класс A (inventoryQuery): одна запись возвращается с
// match_state auto. Затем у проигравшего снимается merged_into_id, поля
// проигравшего и корня пересобираются event.Fold их записей, is_listed
// пересчитывается, пара отклоняется (dedup_candidates rejected с
// decided_by = adminID) — проход больше её не предложит. apply = false —
// только проверки и результат, без изменений. Не слито — ErrNotMergeable;
// нет события — ErrNotFound; точная отмена невозможна — ErrNotUndoable.
func (s *Store) UndoMerge(ctx context.Context, loserID, adminID int64, fold event.FoldConfig, apply bool) (UndoResult, error) {
	var res UndoResult
	err := s.InTx(ctx, func(tx *Store) error {
		if err := tx.LockDedup(ctx); err != nil {
			return err
		}
		var root *int64
		err := tx.db.QueryRow(ctx, "SELECT merged_into_id FROM events WHERE id = $1 FOR UPDATE", loserID).Scan(&root)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if root == nil {
			return fmt.Errorf("event is not merged: %w", ErrNotMergeable)
		}
		res.Root = *root

		var winner int64
		err = tx.db.QueryRow(ctx, `SELECT id, winner_id FROM event_merges
			WHERE loser_id = $1 AND undone_at IS NULL ORDER BY id DESC LIMIT 1`, loserID).Scan(&res.MergeID, &winner)
		switch {
		case err == nil:
			res.Mode = "journal"
			if err := tx.journalPlan(ctx, &res); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
			res.Mode = "legacy"
			if winner, err = tx.legacyPlan(ctx, loserID, &res); err != nil {
				return err
			}
		default:
			return err
		}
		if !apply {
			return nil
		}
		return tx.undo(ctx, loserID, winner, adminID, fold, &res)
	})
	if err != nil {
		return UndoResult{}, fmt.Errorf("undo merge %d: %w", loserID, err)
	}
	return res, nil
}

// journalPlan — записи и цепочка слияния из журнала и проверки: всё ещё у
// текущего корня, нет более позднего действующего слияния с ними.
func (tx *Store) journalPlan(ctx context.Context, res *UndoResult) error {
	rows, err := tx.db.Query(ctx, "SELECT source_event_id FROM event_merge_records WHERE merge_id = $1 ORDER BY source_event_id", res.MergeID)
	if err != nil {
		return err
	}
	if res.Records, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
		return err
	}
	rows, err = tx.db.Query(ctx, "SELECT event_id FROM event_merge_chain WHERE merge_id = $1 ORDER BY event_id", res.MergeID)
	if err != nil {
		return err
	}
	if res.Chain, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
		return err
	}
	var later *int64
	err = tx.db.QueryRow(ctx, `SELECT min(m.id) FROM event_merges m
		WHERE m.id > $1 AND m.undone_at IS NULL AND (
			EXISTS (SELECT 1 FROM event_merge_records r WHERE r.merge_id = m.id AND r.source_event_id = ANY($2))
			OR EXISTS (SELECT 1 FROM event_merge_chain c WHERE c.merge_id = m.id AND c.event_id = ANY($3)))`,
		res.MergeID, res.Records, res.Chain).Scan(&later)
	if err != nil {
		return err
	}
	if later != nil {
		return fmt.Errorf("later merge %d moved the same records, undo it first: %w", *later, ErrNotUndoable)
	}
	return tx.checkUnderRoot(ctx, res)
}

// legacyPlan — слияние без журнала: только класс A; возвращает победителя
// пары (событие подтверждённого кандидата) для отклонения.
func (tx *Store) legacyPlan(ctx context.Context, loserID int64, res *UndoResult) (int64, error) {
	var record *int64
	err := tx.db.QueryRow(ctx, "SELECT class, record_id FROM ("+inventoryQuery+") inv WHERE event_id = $1", loserID).Scan(&res.Class, &record)
	if err != nil {
		return 0, err
	}
	if res.Class != "A" || record == nil {
		return 0, fmt.Errorf("no merge journal, class %s: %w", res.Class, ErrNotUndoable)
	}
	res.Records = []int64{*record}
	var later *int64
	if err := tx.db.QueryRow(ctx, `SELECT min(r.merge_id) FROM event_merge_records r
		JOIN event_merges m ON m.id = r.merge_id
		WHERE r.source_event_id = $1 AND m.undone_at IS NULL`, *record).Scan(&later); err != nil {
		return 0, err
	}
	if later != nil {
		return 0, fmt.Errorf("later merge %d moved the record, undo it first: %w", *later, ErrNotUndoable)
	}
	if err := tx.checkUnderRoot(ctx, res); err != nil {
		return 0, err
	}
	var winner int64
	err = tx.db.QueryRow(ctx, `SELECT event_id FROM dedup_candidates
		WHERE source_event_id = $1 AND state = 'confirmed' AND decided_by IS NOT NULL ORDER BY id LIMIT 1`, *record).Scan(&winner)
	return winner, err
}

// checkUnderRoot — записи и события цепочки по-прежнему у текущего корня.
func (tx *Store) checkUnderRoot(ctx context.Context, res *UndoResult) error {
	var away int64
	err := tx.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM source_events WHERE id = ANY($1) AND event_id IS DISTINCT FROM $3) +
		(SELECT count(*) FROM events WHERE id = ANY($2) AND merged_into_id IS DISTINCT FROM $3)`,
		res.Records, res.Chain, res.Root).Scan(&away)
	if err != nil {
		return err
	}
	if away != 0 || len(res.Records) == 0 {
		return fmt.Errorf("records or chain are no longer under root %d: %w", res.Root, ErrNotUndoable)
	}
	return nil
}

// undo применяет отмену по плану res.
func (tx *Store) undo(ctx context.Context, loserID, winnerID, adminID int64, fold event.FoldConfig, res *UndoResult) error {
	if res.Mode == "journal" {
		const back = `
			UPDATE source_events se
			SET event_id = $1, match_state = CASE WHEN se.match_state = 'out_of_scope' THEN se.match_state ELSE r.prev_match_state END
			FROM event_merge_records r
			WHERE r.merge_id = $2 AND se.id = r.source_event_id`
		if _, err := tx.db.Exec(ctx, back, loserID, res.MergeID); err != nil {
			return fmt.Errorf("records back: %w", err)
		}
		if _, err := tx.db.Exec(ctx, "UPDATE events SET merged_into_id = $1 WHERE id = ANY($2)", loserID, res.Chain); err != nil {
			return fmt.Errorf("chain back: %w", err)
		}
		if _, err := tx.db.Exec(ctx, "UPDATE event_merges SET undone_at = now(), undone_by = $2 WHERE id = $1", res.MergeID, adminID); err != nil {
			return fmt.Errorf("journal: %w", err)
		}
	} else {
		const back = `
			UPDATE source_events
			SET event_id = $1, match_state = CASE WHEN match_state = 'out_of_scope' THEN match_state ELSE 'auto' END
			WHERE id = ANY($2)`
		if _, err := tx.db.Exec(ctx, back, loserID, res.Records); err != nil {
			return fmt.Errorf("records back: %w", err)
		}
	}
	if _, err := tx.db.Exec(ctx, "UPDATE events SET merged_into_id = NULL WHERE id = $1", loserID); err != nil {
		return fmt.Errorf("unmark loser: %w", err)
	}
	for _, id := range []int64{loserID, res.Root} {
		if err := tx.refold(ctx, id, fold); err != nil {
			return err
		}
	}
	// Пара отклонена: проход больше не предложит и не сольёт её (DDP-07).
	const reject = `
		INSERT INTO dedup_candidates (source_event_id, event_id, score, reasons, state, decided_at, decided_by)
		VALUES ($1, $2, 0, '{}', 'rejected', now(), $3)
		ON CONFLICT (source_event_id, event_id) DO UPDATE
		SET state = 'rejected', decided_at = now(), decided_by = $3`
	if _, err := tx.db.Exec(ctx, reject, res.Records[0], winnerID, adminID); err != nil {
		return fmt.Errorf("reject pair: %w", err)
	}
	return nil
}

// refold — поля события пересобираются event.Fold его записей (как в
// MergeEvents), статус не меняется; is_listed пересчитывается.
func (tx *Store) refold(ctx context.Context, id int64, fold event.FoldConfig) error {
	var status string
	if err := tx.db.QueryRow(ctx, "SELECT status FROM events WHERE id = $1 FOR UPDATE", id).Scan(&status); err != nil {
		return fmt.Errorf("refold %d: %w", id, err)
	}
	parts, err := foldPartsTx(ctx, tx.db, id, 0)
	if err != nil {
		return fmt.Errorf("refold %d: %w", id, err)
	}
	if len(parts) > 0 {
		e := event.Fold(parts, fold)
		e.ID, e.Status = id, event.Status(status)
		ptx, ok := tx.db.(pgx.Tx)
		if !ok {
			return fmt.Errorf("refold %d: not in a transaction", id)
		}
		if _, err := saveEventTx(ctx, ptx, &e); err != nil {
			return fmt.Errorf("refold %d: %w", id, err)
		}
	}
	if _, err := tx.db.Exec(ctx, listEventSQL, id); err != nil {
		return fmt.Errorf("refold %d: is_listed: %w", id, err)
	}
	return nil
}
