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
	slices.SortFunc(evs, func(x, y store.DedupEvent) int {
		return cmp.Or(x.Start.Compare(y.Start), cmp.Compare(x.ID, y.ID))
	})

	rejected, err := rejectedRoots(ctx, tx)
	if err != nil {
		return err
	}

	// Окно шире ворот Decide (±1 день): конец первого + 2 дня.
	var pairs []scored
	skipped := 0
	for i := range evs {
		end := evs[i].Start
		if evs[i].End != nil && evs[i].End.After(end) {
			end = *evs[i].End
		}
		limit := end.AddDate(0, 0, 2)
		for j := i + 1; j < len(evs) && !evs[j].Start.After(limit); j++ {
			a, b := &evs[i], &evs[j]
			if a.CountryCode != b.CountryCode {
				continue
			}
			if a.ID > b.ID {
				a, b = b, a
			}
			if rejected[[2]int64{a.ID, b.ID}] {
				skipped++
				continue
			}
			v, r := Decide(Pair{A: side(a), B: side(b)}, c)
			if v != Separate {
				pairs = append(pairs, scored{a: a, b: b, verdict: v, reasons: r})
			}
		}
	}

	// UniquePartner (Pitfall 9): у каждой стороны не больше одного партнёра
	// с NameSim не ниже High — по чистой схожести, как порог автослияния (D-20).
	partners := map[int64]int{}
	for _, p := range pairs {
		if p.reasons.NameSim >= c.High {
			partners[p.a.ID]++
			partners[p.b.ID]++
		}
	}
	for i := range pairs {
		p := &pairs[i]
		p.verdict, p.reasons = Decide(Pair{A: side(p.a), B: side(p.b), UniquePartner: partners[p.a.ID] <= 1 && partners[p.b.ID] <= 1}, c)
	}
	slices.SortStableFunc(pairs, func(x, y scored) int {
		return cmp.Or(cmp.Compare(y.reasons.Score, x.reasons.Score), cmp.Compare(x.a.ID, y.a.ID), cmp.Compare(x.b.ID, y.b.ID))
	})

	merged := map[int64]bool{}
	nMerged := 0
	if c.AutoMerge {
		for _, p := range pairs {
			if p.verdict != AutoMerge || merged[p.a.ID] || merged[p.b.ID] {
				continue
			}
			if err := tx.MergeEvents(ctx, p.b.ID, p.a.ID, "auto", nil, c.Fold()); err != nil {
				return err
			}
			merged[p.a.ID], merged[p.b.ID] = true, true
			nMerged++
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
	}
	if _, err := tx.PrunePendingCandidates(ctx, keep); err != nil {
		return err
	}
	slog.Info("dedup pass", "events", len(evs), "pairs", len(pairs), "candidates", len(keep),
		"merged", nMerged, "rejected_skipped", skipped)
	return nil
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
		CountryCode: e.CountryCode, CityKey: e.CityKey, RegURL: e.RegURL,
	}
	if e.End != nil {
		s.End = *e.End
	}
	for _, src := range e.Sources {
		s.Sources = append(s.Sources, SourceRef{Code: src.Code, GroupKey: src.GroupKey})
	}
	return s
}
