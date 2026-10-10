package dedup

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"aggregator/internal/store"
)

// Pass — идемпотентный проход дедупликации после применения прогона
// (Runner.PostApply): корневые события в выдаче со start_date не раньше
// вчерашнего дня (UTC) разбиваются на пары одной страны в окне дат, по
// каждой решает Decide. Пары, отклонённые администратором, сравниваются по
// корням и пропускаются (DDP-07). При c.AutoMerge пары с вердиктом
// AutoMerge сливаются (D-08); остальные пары не ниже Low — кандидаты
// /dupes, не больше c.MaxPerEvent на событие, с наибольшей оценкой;
// pending-кандидаты, которые больше не проходят, удаляются. Всё — одной
// транзакцией под LockDedup; ошибка откатывает только проход, каталог
// прогона уже записан — вызывающий пишет её в лог.
//
// Потенциальные автослияния (Reasons.WouldAuto, D-21 Phase 5) считаются при
// любом c.AutoMerge и в слиянии не участвуют: признак пишется в reasons
// кандидата и в лог. Счётчики лога: would_auto — пары прохода с признаком
// (отклонённые по корням не оцениваются); would_auto_shown — записаны
// кандидатом; would_auto_hidden — не прошли лимит MaxPerEvent;
// would_auto_decided — пара уже решена администратором (UpsertCandidate
// вернул 0); при c.AutoMerge ещё would_auto_merged и would_auto_deferred
// (сторона уже слита в этом проходе). Их сумма равна would_auto.
func Pass(ctx context.Context, s *store.Store, c Config) error {
	err := s.InTx(ctx, func(tx *store.Store) error {
		if err := tx.LockDedup(ctx); err != nil {
			return err
		}
		return pass(ctx, tx, c)
	})
	if err != nil {
		return fmt.Errorf("dedup pass: %w", err)
	}
	return nil
}

// scored — пара после Decide; a.ID < b.ID.
type scored struct {
	a, b    *store.DedupEvent
	verdict Verdict
	reasons Reasons
}

func pass(ctx context.Context, tx *store.Store, c Config) error {
	y, m, d := time.Now().UTC().Date()
	from := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	all, err := tx.DedupEvents(ctx, from)
	if err != nil {
		return err
	}
	// Событие с записью без fields ещё не прошло текущую версию записи —
	// Fold его полей неполон.
	evs := slices.DeleteFunc(all, func(e store.DedupEvent) bool { return !e.Complete })

	rejected, err := rejectedRoots(ctx, tx)
	if err != nil {
		return err
	}

	// Пары окна и UniquePartner — общей функцией с калибровкой (M2);
	// отклонённые по корням пропускаются до Decide.
	sides := make([]Side, len(evs))
	for i := range evs {
		sides[i] = side(&evs[i])
	}
	skipped := 0
	found, _ := Pairs(sides, c, func(a, b int) bool {
		if rejected[[2]int64{evs[a].ID, evs[b].ID}] {
			skipped++
			return true
		}
		return false
	})
	pairs := make([]scored, 0, len(found))
	for _, p := range found {
		pairs = append(pairs, scored{a: &evs[p.A], b: &evs[p.B], verdict: p.Verdict, reasons: p.Reasons})
	}
	slices.SortStableFunc(pairs, func(x, y scored) int {
		return cmp.Or(cmp.Compare(y.reasons.Score, x.reasons.Score), cmp.Compare(x.a.ID, y.a.ID), cmp.Compare(x.b.ID, y.b.ID))
	})

	var w wouldAuto
	for _, p := range pairs {
		if p.reasons.WouldAuto {
			w.total++
		}
	}

	merged := map[int64]bool{}
	nMerged := 0
	if c.AutoMerge {
		for _, p := range pairs {
			if p.verdict != AutoMerge {
				continue
			}
			if merged[p.a.ID] || merged[p.b.ID] {
				w.deferred++
				continue
			}
			if err := tx.MergeEvents(ctx, p.b.ID, p.a.ID, "auto", nil, c.Fold()); err != nil {
				return err
			}
			merged[p.a.ID], merged[p.b.ID] = true, true
			nMerged++
			w.merged++
		}
	}

	var keep []int64
	perEvent := map[int64]int{}
	for _, p := range pairs {
		// AutoMerge бывает только при c.AutoMerge: пара слита или ждёт
		// следующего прохода, если сторона уже слита с другой.
		if p.verdict == AutoMerge || merged[p.a.ID] || merged[p.b.ID] {
			continue
		}
		if perEvent[p.a.ID] >= c.MaxPerEvent || perEvent[p.b.ID] >= c.MaxPerEvent {
			if p.reasons.WouldAuto {
				w.hidden++
			}
			continue
		}
		reasons, err := json.Marshal(p.reasons)
		if err != nil {
			return err
		}
		id, err := tx.UpsertCandidate(ctx, p.b.FirstSourceEventID, p.a.ID, p.reasons.Score, reasons)
		if err != nil {
			return err
		}
		if id != 0 {
			keep = append(keep, id)
			perEvent[p.a.ID]++
			perEvent[p.b.ID]++
		}
		if p.reasons.WouldAuto {
			if id != 0 {
				w.shown++
			} else {
				w.decided++
			}
		}
	}
	if _, err := tx.PrunePendingCandidates(ctx, keep); err != nil {
		return err
	}
	attrs := []any{"events", len(evs), "pairs", len(pairs), "candidates", len(keep),
		"merged", nMerged, "rejected_skipped", skipped,
		"would_auto", w.total, "would_auto_shown", w.shown, "would_auto_hidden", w.hidden, "would_auto_decided", w.decided}
	if c.AutoMerge {
		attrs = append(attrs, "would_auto_merged", w.merged, "would_auto_deferred", w.deferred)
	}
	slog.Info("dedup pass", attrs...)
	return nil
}

// wouldAuto — счётчики потенциальных автослияний прохода (см. Pass).
type wouldAuto struct {
	total, shown, hidden, decided, merged, deferred int
}

// rejectedRoots — отклонённые пары как пары корней (меньший id первым).
func rejectedRoots(ctx context.Context, tx *store.Store) (map[[2]int64]bool, error) {
	raw, err := tx.RejectedPairs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]int64]bool, len(raw))
	for _, p := range raw {
		a, err := tx.RootEventID(ctx, p[0])
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		b, err := tx.RootEventID(ctx, p[1])
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[[2]int64{min(a, b), max(a, b)}] = true
	}
	return out, nil
}

func side(e *store.DedupEvent) Side {
	s := Side{
		EventID: e.ID, Title: e.Title, Start: e.Start,
		CountryCode: e.CountryCode, CityKey: e.CityKey, RegURL: e.RegURL, Sport: e.Sport,
	}
	if e.End != nil {
		s.End = *e.End
	}
	for _, src := range e.Sources {
		s.Sources = append(s.Sources, SourceRef{Code: src.Code, GroupKey: src.GroupKey})
	}
	return s
}
