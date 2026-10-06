package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// dedupLockKey — ключ pg_advisory_xact_lock, которым сериализуются запись
// прогонов, проход дедупликации, слияния и решения администратора
// (Pitfall 10). Значение произвольное, но постоянное: менять его можно
// только при остановленных worker и bot.
const dedupLockKey int64 = 0x5d_ed_0b_10_c0

// ErrNotMergeable — пару нельзя слить или кандидат уже обработан: событие
// не корень, стороны совпадают, кандидат не в pending.
var ErrNotMergeable = errors.New("not mergeable")

// LockDedup берёт advisory-блокировку дедупликации до конца транзакции.
// Вызывать только на Store из InTx: на пуле блокировка снялась бы сразу.
// Повторный вызов в той же транзакции не ждёт.
func (s *Store) LockDedup(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", dedupLockKey); err != nil {
		return fmt.Errorf("lock dedup: %w", err)
	}
	return nil
}

// DedupSource — запись источника корневого события для правила одного
// источника (DDP-05): код и group_key из payload.
type DedupSource struct {
	Code, GroupKey string
}

// DedupEvent — корневое событие для прохода дедупликации.
type DedupEvent struct {
	ID          int64
	Title       string
	Start       time.Time
	End         *time.Time
	CountryCode string
	CityKey     string
	RegURL      string
	Sources     []DedupSource
	// FirstSourceEventID — минимальный id записи: сторона A в строке кандидата.
	FirstSourceEventID int64
	// Complete — у всех записей есть fields (прошли текущую версию записи).
	Complete bool
}

// DedupEvents — корневые события в выдаче со start_date не раньше from и
// их записи вне gone и out_of_scope, по возрастанию id. Событие без таких
// записей не возвращается.
func (s *Store) DedupEvents(ctx context.Context, from time.Time) ([]DedupEvent, error) {
	const q = `
		SELECT e.id, e.title, e.start_date, e.end_date, e.country_code,
		       COALESCE(e.city_key, ''), COALESCE(e.registration_url, ''),
		       s.code, COALESCE(se.payload->>'group_key', ''), se.id, se.fields IS NOT NULL
		FROM events e
		JOIN source_events se ON se.event_id = e.id AND se.state <> 'gone' AND se.match_state <> 'out_of_scope'
		JOIN sources s ON s.id = se.source_id
		WHERE e.merged_into_id IS NULL AND e.is_listed AND e.start_date >= $1
		ORDER BY e.id, se.id`
	rows, err := s.db.Query(ctx, q, from)
	if err != nil {
		return nil, fmt.Errorf("dedup events: %w", err)
	}
	defer rows.Close()
	var out []DedupEvent
	for rows.Next() {
		var e DedupEvent
		var src DedupSource
		var seID int64
		var hasFields bool
		if err := rows.Scan(&e.ID, &e.Title, &e.Start, &e.End, &e.CountryCode, &e.CityKey, &e.RegURL,
			&src.Code, &src.GroupKey, &seID, &hasFields); err != nil {
			return nil, fmt.Errorf("dedup events: scan: %w", err)
		}
		if n := len(out); n == 0 || out[n-1].ID != e.ID {
			e.FirstSourceEventID, e.Complete = seID, true
			out = append(out, e)
		}
		last := &out[len(out)-1]
		last.Sources = append(last.Sources, src)
		last.Complete = last.Complete && hasFields
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dedup events: %w", err)
	}
	return out, nil
}

// RejectedPairs — сырые пары отклонённых кандидатов: событие записи
// источника и event_id. Корни считает вызывающий через RootEventID.
func (s *Store) RejectedPairs(ctx context.Context) ([][2]int64, error) {
	const q = `
		SELECT se.event_id, dc.event_id FROM dedup_candidates dc
		JOIN source_events se ON se.id = dc.source_event_id
		WHERE dc.state = 'rejected' AND se.event_id IS NOT NULL`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("rejected pairs: %w", err)
	}
	pairs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]int64, error) {
		var p [2]int64
		return p, r.Scan(&p[0], &p[1])
	})
	if err != nil {
		return nil, fmt.Errorf("rejected pairs: %w", err)
	}
	return pairs, nil
}

// UpsertCandidate вставляет кандидата или обновляет score и reasons у
// pending-строки той же пары. Пара уже решена (confirmed, rejected) —
// 0, nil, строка не меняется.
func (s *Store) UpsertCandidate(ctx context.Context, sourceEventID, eventID int64, score float64, reasons []byte) (int64, error) {
	const q = `
		INSERT INTO dedup_candidates (source_event_id, event_id, score, reasons)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (source_event_id, event_id) DO UPDATE
		SET score = EXCLUDED.score, reasons = EXCLUDED.reasons
		WHERE dedup_candidates.state = 'pending'
		RETURNING id`
	var id int64
	err := s.db.QueryRow(ctx, q, sourceEventID, eventID, score, reasons).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("upsert candidate: %w", err)
	}
	return id, nil
}

// PrunePendingCandidates удаляет pending-кандидатов, чьих id нет в keep
// (пара больше не проходит); rejected и confirmed не трогаются никогда.
func (s *Store) PrunePendingCandidates(ctx context.Context, keep []int64) (int64, error) {
	if keep == nil {
		keep = []int64{} // nil ушёл бы как NULL, и не удалилось бы ничего
	}
	cmd, err := s.db.Exec(ctx, "DELETE FROM dedup_candidates WHERE state = 'pending' AND NOT (id = ANY($1))", keep)
	if err != nil {
		return 0, fmt.Errorf("prune candidates: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// MergeEvents сливает корень loserID в корень winnerID одной транзакцией
// (DDP-06): записи проигравшего переходят к победителю с matchState
// (auto или manual; out_of_scope остаётся, manual не понижается до auto —
// L5), проигравший получает
// merged_into_id и is_listed = false, события, слитые в проигравшего,
// перевешиваются на победителя (цепочка в один шаг). Поля победителя
// пересобираются event.Fold записей через saveEventTx (D-17.1). Pending-
// кандидаты пары подтверждаются с decidedBy (nil — автоматически), прочие
// pending с участием проигравшего удаляются. События и записи источников
// не удаляются (D-17.2). Слияние пишется в журнал (event_merges: записи с
// прежним match_state и события цепочки) — для точной отмены (UndoMerge,
// D-14 Phase 7). Победитель — корень с меньшим id. Блокировки:
// LockDedup, записи обоих событий, затем события, по возрастанию id.
// Нет события — ErrNotFound; не корень или одно событие — ErrNotMergeable.
func (s *Store) MergeEvents(ctx context.Context, loserID, winnerID int64, matchState string, decidedBy *int64, fold event.FoldConfig) error {
	switch {
	case matchState != string(event.MatchAuto) && matchState != string(event.MatchManual):
		return fmt.Errorf("merge events: match state %q: want auto or manual", matchState)
	case loserID == winnerID:
		return fmt.Errorf("merge events %d: same event: %w", loserID, ErrNotMergeable)
	case winnerID > loserID:
		return fmt.Errorf("merge events %d into %d: winner must have the smaller id", loserID, winnerID)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("merge events: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", dedupLockKey); err != nil {
		return fmt.Errorf("merge events: lock: %w", err)
	}

	if _, err := tx.Exec(ctx, "SELECT id FROM source_events WHERE event_id IN ($1, $2) ORDER BY id FOR UPDATE", loserID, winnerID); err != nil {
		return fmt.Errorf("merge events: lock source events: %w", err)
	}
	rows, err := tx.Query(ctx, "SELECT merged_into_id IS NULL, status FROM events WHERE id IN ($1, $2) ORDER BY id FOR UPDATE", loserID, winnerID)
	if err != nil {
		return fmt.Errorf("merge events: lock events: %w", err)
	}
	type evRow struct {
		root   bool
		status string
	}
	evs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (evRow, error) {
		var x evRow
		return x, r.Scan(&x.root, &x.status)
	})
	if err != nil {
		return fmt.Errorf("merge events: lock events: %w", err)
	}
	if len(evs) != 2 {
		return fmt.Errorf("merge events %d into %d: %w", loserID, winnerID, ErrNotFound)
	}
	if !evs[0].root || !evs[1].root {
		return fmt.Errorf("merge events %d into %d: not a root: %w", loserID, winnerID, ErrNotMergeable)
	}
	winnerStatus := evs[0].status // ORDER BY id: победитель первый

	// Прежний match_state каждой записи — в журнал слияния (отмена, D-14).
	const move = `
		UPDATE source_events se
		SET event_id = $2, match_state = CASE WHEN se.match_state IN ('out_of_scope', 'manual') THEN se.match_state ELSE $3 END
		FROM (SELECT id, match_state FROM source_events WHERE event_id = $1) old
		WHERE se.id = old.id
		RETURNING se.id, old.match_state`
	moved, err := tx.Query(ctx, move, loserID, winnerID, matchState)
	if err != nil {
		return fmt.Errorf("merge events: move records: %w", err)
	}
	type movedRow struct {
		ID    int64
		State string
	}
	movedRows, err := pgx.CollectRows(moved, pgx.RowToStructByPos[movedRow])
	if err != nil {
		return fmt.Errorf("merge events: move records: %w", err)
	}
	movedIDs := make([]int64, len(movedRows))
	movedStates := make([]string, len(movedRows))
	for i, r := range movedRows {
		movedIDs[i], movedStates[i] = r.ID, r.State
	}
	if _, err := tx.Exec(ctx, "UPDATE events SET merged_into_id = $2, is_listed = false WHERE id = $1", loserID, winnerID); err != nil {
		return fmt.Errorf("merge events: mark loser: %w", err)
	}
	chained, err := tx.Query(ctx, "UPDATE events SET merged_into_id = $2 WHERE merged_into_id = $1 RETURNING id", loserID, winnerID)
	if err != nil {
		return fmt.Errorf("merge events: collapse chain: %w", err)
	}
	chainIDs, err := pgx.CollectRows(chained, pgx.RowTo[int64])
	if err != nil {
		return fmt.Errorf("merge events: collapse chain: %w", err)
	}
	const journal = `
		WITH m AS (
			INSERT INTO event_merges (winner_id, loser_id, match_state, decided_by)
			VALUES ($1, $2, $3, $4) RETURNING id
		), r AS (
			INSERT INTO event_merge_records (merge_id, source_event_id, prev_match_state)
			SELECT m.id, x.id, x.state FROM m, unnest($5::bigint[], $6::text[]) AS x(id, state)
		)
		INSERT INTO event_merge_chain (merge_id, event_id)
		SELECT m.id, c FROM m, unnest($7::bigint[]) AS c`
	if _, err := tx.Exec(ctx, journal, winnerID, loserID, matchState, decidedBy, movedIDs, movedStates, chainIDs); err != nil {
		return fmt.Errorf("merge events: journal: %w", err)
	}

	parts, err := foldPartsTx(ctx, tx, winnerID, 0)
	if err != nil {
		return fmt.Errorf("merge events: %w", err)
	}
	if len(parts) > 0 {
		e := event.Fold(parts, fold)
		e.ID, e.Status = winnerID, event.Status(winnerStatus)
		if _, err := saveEventTx(ctx, tx, &e); err != nil {
			return fmt.Errorf("merge events: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, listEventSQL, winnerID); err != nil {
		return fmt.Errorf("merge events: is_listed: %w", err)
	}

	const confirm = `
		UPDATE dedup_candidates dc SET state = 'confirmed', decided_at = now(), decided_by = $3
		FROM source_events se
		WHERE se.id = dc.source_event_id AND dc.state = 'pending'
		  AND se.event_id = $1 AND dc.event_id IN ($1, $2)`
	if _, err := tx.Exec(ctx, confirm, winnerID, loserID, decidedBy); err != nil {
		return fmt.Errorf("merge events: confirm candidates: %w", err)
	}
	const prune = "DELETE FROM dedup_candidates WHERE state = 'pending' AND (event_id = $1 OR source_event_id = ANY($2))"
	if _, err := tx.Exec(ctx, prune, loserID, movedIDs); err != nil {
		return fmt.Errorf("merge events: prune candidates: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("merge events: commit: %w", err)
	}
	return nil
}

// Candidate — кандидат очереди /dupes: EventA — корень события записи
// источника, EventB — корень event_id.
type Candidate struct {
	ID      int64
	Score   float64
	Reasons json.RawMessage
	EventA  int64
	EventB  int64
}

type candidateRow struct {
	Candidate
	sourceEvent *int64 // событие записи источника; NULL — запись без события
	state       string
}

// candidateRows — кандидаты по условию where (параметры args) по
// возрастанию id, с сырыми id событий в EventA/EventB.
func (s *Store) candidateRows(ctx context.Context, where string, args ...any) ([]candidateRow, error) {
	q := `
		SELECT dc.id, dc.score::float8, dc.reasons, se.event_id, dc.event_id, dc.state
		FROM dedup_candidates dc JOIN source_events se ON se.id = dc.source_event_id
		WHERE ` + where + ` ORDER BY dc.id`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (candidateRow, error) {
		var c candidateRow
		return c, r.Scan(&c.ID, &c.Score, &c.Reasons, &c.sourceEvent, &c.EventB, &c.state)
	})
}

// resolve заменяет стороны кандидата корнями (D-17.3); ok false — запись
// без события или стороны уже в одном корне.
func (s *Store) resolve(ctx context.Context, c *candidateRow) (bool, error) {
	if c.sourceEvent == nil {
		return false, nil
	}
	a, err := s.RootEventID(ctx, *c.sourceEvent)
	if err != nil {
		return false, err
	}
	b, err := s.RootEventID(ctx, c.EventB)
	if err != nil {
		return false, err
	}
	c.EventA, c.EventB = a, b
	return a != b, nil
}

// NextCandidate — первый pending-кандидат с id больше afterID, стороны —
// корни; кандидаты, чьи стороны уже в одном корне, пропускаются. Очередь
// пуста — ErrNotFound.
func (s *Store) NextCandidate(ctx context.Context, afterID int64) (Candidate, error) {
	// Упрощение: читает весь хвост очереди; LIMIT с дочиткой — если очередь вырастет до тысяч
	list, err := s.candidateRows(ctx, "dc.state = 'pending' AND dc.id > $1", afterID)
	if err != nil {
		return Candidate{}, fmt.Errorf("next candidate: %w", err)
	}
	for i := range list {
		ok, err := s.resolve(ctx, &list[i])
		if err != nil {
			return Candidate{}, fmt.Errorf("next candidate: %w", err)
		}
		if ok {
			return list[i].Candidate, nil
		}
	}
	return Candidate{}, fmt.Errorf("next candidate after %d: %w", afterID, ErrNotFound)
}

// CandidateByID — pending-кандидат id со сторонами-корнями; обработанный,
// несуществующий или уже слитый в один корень — ErrNotFound (бот: «Кандидат
// уже обработан»).
func (s *Store) CandidateByID(ctx context.Context, id int64) (Candidate, error) {
	list, err := s.candidateRows(ctx, "dc.id = $1 AND dc.state = 'pending'", id)
	if err != nil {
		return Candidate{}, fmt.Errorf("candidate %d: %w", id, err)
	}
	if len(list) == 0 {
		return Candidate{}, fmt.Errorf("candidate %d: %w", id, ErrNotFound)
	}
	ok, err := s.resolve(ctx, &list[0])
	if err != nil {
		return Candidate{}, fmt.Errorf("candidate %d: %w", id, err)
	}
	if !ok {
		return Candidate{}, fmt.Errorf("candidate %d: %w", id, ErrNotFound)
	}
	return list[0].Candidate, nil
}

// ConfirmCandidate — решение администратора [Да, слить]: корни сторон
// сливаются (победитель — меньший id, match_state manual), кандидат —
// confirmed с decided_by = adminID. Нет кандидата — ErrNotFound; не
// pending или стороны уже в одном корне — ErrNotMergeable.
func (s *Store) ConfirmCandidate(ctx context.Context, id, adminID int64, fold event.FoldConfig) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.LockDedup(ctx); err != nil {
			return fmt.Errorf("confirm candidate: %w", err)
		}
		list, err := tx.candidateRows(ctx, "dc.id = $1", id)
		if err != nil {
			return fmt.Errorf("confirm candidate %d: %w", id, err)
		}
		if len(list) == 0 {
			return fmt.Errorf("confirm candidate %d: %w", id, ErrNotFound)
		}
		c := list[0]
		if c.state != "pending" {
			return fmt.Errorf("confirm candidate %d: %s: %w", id, c.state, ErrNotMergeable)
		}
		ok, err := tx.resolve(ctx, &c)
		if err != nil {
			return fmt.Errorf("confirm candidate %d: %w", id, err)
		}
		if !ok {
			return fmt.Errorf("confirm candidate %d: %w", id, ErrNotMergeable)
		}
		// Сначала решение: слияние удаляет pending-строки с участием проигравшего.
		if err := tx.decide(ctx, id, "confirmed", adminID); err != nil {
			return fmt.Errorf("confirm candidate %d: %w", id, err)
		}
		return tx.MergeEvents(ctx, max(c.EventA, c.EventB), min(c.EventA, c.EventB), string(event.MatchManual), &adminID, fold)
	})
}

// RejectCandidate — решение администратора [Разные]: кандидат rejected,
// пара по корням больше не предлагается (DDP-07). Нет кандидата —
// ErrNotFound; не pending — ErrNotMergeable.
func (s *Store) RejectCandidate(ctx context.Context, id, adminID int64) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.LockDedup(ctx); err != nil {
			return fmt.Errorf("reject candidate: %w", err)
		}
		var state string
		err := tx.db.QueryRow(ctx, "SELECT state FROM dedup_candidates WHERE id = $1 FOR UPDATE", id).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("reject candidate %d: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("reject candidate %d: %w", id, err)
		}
		if state != "pending" {
			return fmt.Errorf("reject candidate %d: %s: %w", id, state, ErrNotMergeable)
		}
		if err := tx.decide(ctx, id, "rejected", adminID); err != nil {
			return fmt.Errorf("reject candidate %d: %w", id, err)
		}
		return nil
	})
}

func (s *Store) decide(ctx context.Context, id int64, state string, adminID int64) error {
	_, err := s.db.Exec(ctx, "UPDATE dedup_candidates SET state = $2, decided_at = now(), decided_by = $3 WHERE id = $1", id, state, adminID)
	return err
}

// CandidateCounts — ожидающие кандидаты и из них новые (ещё не в сводке).
func (s *Store) CandidateCounts(ctx context.Context) (pending, fresh int, err error) {
	const q = `
		SELECT count(*) FILTER (WHERE state = 'pending'),
		       count(*) FILTER (WHERE state = 'pending' AND notified_at IS NULL)
		FROM dedup_candidates`
	if err := s.db.QueryRow(ctx, q).Scan(&pending, &fresh); err != nil {
		return 0, 0, fmt.Errorf("candidate counts: %w", err)
	}
	return pending, fresh, nil
}

// LastDigestAt — время последней сводки (максимум notified_at); сводок
// не было — нулевое время.
func (s *Store) LastDigestAt(ctx context.Context) (time.Time, error) {
	var at *time.Time
	if err := s.db.QueryRow(ctx, "SELECT max(notified_at) FROM dedup_candidates").Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("last digest: %w", err)
	}
	if at == nil {
		return time.Time{}, nil
	}
	return *at, nil
}

// DigestCandidates — ожидающие кандидаты и id новых (ещё не в сводке) одним
// запросом: сводка считает и отмечает ровно один набор (L2).
func (s *Store) DigestCandidates(ctx context.Context) (pending int, fresh []int64, err error) {
	const q = `
		SELECT count(*) FILTER (WHERE state = 'pending'),
		       coalesce(array_agg(id ORDER BY id) FILTER (WHERE state = 'pending' AND notified_at IS NULL), '{}')
		FROM dedup_candidates`
	if err := s.db.QueryRow(ctx, q).Scan(&pending, &fresh); err != nil {
		return 0, nil, fmt.Errorf("digest candidates: %w", err)
	}
	return pending, fresh, nil
}

// MarkCandidatesNotified отмечает вошедшими в сводку новых pending-кандидатов
// из ids — тех, что сводка посчитала (L2); созданные позже остаются новыми.
// Вызывать после успешной отправки (Pitfall 11).
func (s *Store) MarkCandidatesNotified(ctx context.Context, ids []int64) error {
	const q = "UPDATE dedup_candidates SET notified_at = now() WHERE id = ANY($1) AND state = 'pending' AND notified_at IS NULL"
	if _, err := s.db.Exec(ctx, q, ids); err != nil {
		return fmt.Errorf("mark candidates notified: %w", err)
	}
	return nil
}
