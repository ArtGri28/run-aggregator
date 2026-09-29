package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

// Runner выполняет прогон источника: Fetch → Parse → Prepare → запись.
type Runner struct {
	Store *store.Store
	// Now — часы прогона; nil — time.Now.
	Now func() time.Time
	// Log — nil означает slog.Default().
	Log *slog.Logger
}

// Result — счётчики одного прогона.
type Result struct {
	Fetched, Parsed, Rejected, Expired, OutOfScope int
	Upserted, Created, Changed, Unchanged          int
}

// Run выполняет один прогон src. Ошибка Parse (в том числе
// ErrStructureNotFound, проверяется errors.Is) возвращается до любой
// записи в БД.
func (r *Runner) Run(ctx context.Context, src Source) (Result, error) {
	var res Result
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	log := r.Log
	if log == nil {
		log = slog.Default()
	}

	sourceID, err := r.Store.EnsureSource(ctx, src.Code())
	if err != nil {
		return res, err
	}
	docs, err := src.Fetch(ctx)
	if err != nil {
		return res, fmt.Errorf("%s: fetch: %w", src.Code(), err)
	}
	var recs []SourceEvent
	for _, d := range docs {
		evs, err := src.Parse(d)
		if err != nil {
			return res, fmt.Errorf("%s: parse %s: %w", src.Code(), d.URL, err)
		}
		recs = append(recs, evs...)
	}
	res.Fetched, res.Parsed = len(recs), len(recs)

	prepared, st := Prepare(recs, now(), src.Policy())
	res.Rejected, res.Expired, res.OutOfScope = st.Rejected, st.Expired, st.OutOfScope

	if err := r.apply(ctx, sourceID, prepared, &res); err != nil {
		return res, fmt.Errorf("%s: %w", src.Code(), err)
	}
	log.Info("run finished",
		"source", src.Code(),
		"fetched", res.Fetched, "parsed", res.Parsed,
		"rejected", res.Rejected, "expired", res.Expired, "out_of_scope", res.OutOfScope,
		"upserted", res.Upserted, "created", res.Created, "changed", res.Changed, "unchanged", res.Unchanged)
	return res, nil
}

// apply записывает КАЖДУЮ валидную запись прогона, а не только
// изменённые: иначе событие, не созданное из-за сбоя в прошлом прогоне,
// не появится никогда (Pitfall 9).
func (r *Runner) apply(ctx context.Context, sourceID int64, prepared []Prepared, res *Result) error {
	for i := range prepared {
		p := &prepared[i]
		id, _, err := r.Store.UpsertSourceEvent(ctx, sourceID, p.Source.ExternalID, p.Source.URL, p.Payload)
		if err != nil {
			return err
		}
		res.Upserted++
		if !p.InScope {
			if err := r.Store.SetMatch(ctx, id, nil, event.MatchOutOfScope); err != nil {
				return err
			}
			continue
		}
		created, changed, err := r.Store.SaveEventFor(ctx, id, &p.Event)
		if err != nil {
			return fmt.Errorf("external_id %s: %w", p.Source.ExternalID, err)
		}
		switch {
		case created:
			res.Created++
		case changed:
			res.Changed++
		default:
			res.Unchanged++
		}
	}
	return nil
}
