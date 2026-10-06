package ingest

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// retryPause — пауза после ошибки БД или прогона: сайт при ошибке
// расчёта задержки не запрашивается, а цикл не крутится вхолостую.
const retryPause = time.Minute

// Schedule — расписание прогонов (ING-09).
type Schedule struct {
	Interval, Jitter time.Duration
}

// Loop запускает прогоны всех источников по расписанию до отмены ctx:
// по горутине на источник, возврат после остановки всех.
// Упрощение: без общего предела параллельных хостов — по одному источнику
// на хост; семафор нужен, когда источников станет больше четырёх.
func (r *Runner) Loop(ctx context.Context, sources []Source, sc Schedule) {
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Go(func() { r.loop(ctx, src, sc) })
	}
	wg.Wait()
}

func (r *Runner) loop(ctx context.Context, src Source, sc Schedule) {
	log := r.logger().With("source", src.Code())
	rnd := func(n int64) int64 { return rand.Int64N(n) }
	for {
		d, err := r.delay(ctx, src, sc, rnd)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error("schedule", "err", err, "retry_in", retryPause.String())
			d = retryPause
		} else {
			log.Info("next run", "in", d.Round(time.Second).String(), "at", r.now().Add(d).UTC().Format(time.RFC3339))
		}
		if sleepCtx(ctx, d) != nil {
			return
		}
		if err != nil {
			continue
		}
		if _, err := r.Run(ctx, src); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("run error", "err", err)
			if sleepCtx(ctx, retryPause) != nil {
				return
			}
		}
	}
}

// delay — задержка до следующего прогона по состоянию источника в БД:
// перезапуск процесса не запрашивает сайт повторно (Pitfall 5).
func (r *Runner) delay(ctx context.Context, src Source, sc Schedule, rnd func(int64) int64) (time.Duration, error) {
	id, err := r.Store.EnsureSource(ctx, src.Code())
	if err != nil {
		return 0, err
	}
	h, err := r.Store.SourceHealth(ctx, id)
	if err != nil {
		return 0, err
	}
	return nextDelay(h.LastStartedAt, h.HasRuns, r.now(), sc.Interval, sc.Jitter, h.ConsecutiveFailures, rnd), nil
}

// nextDelay: нет прогонов — сразу; иначе последний started_at + интервал
// × min(2^failures, 4) (circuit breaker MVP.md §5.3) + сдвиг rnd(jitter);
// не меньше 0.
func nextDelay(last time.Time, hasRuns bool, now time.Time, interval, jitter time.Duration, failures int, rnd func(int64) int64) time.Duration {
	if !hasRuns {
		return 0
	}
	mult := time.Duration(1)
	for i := 0; i < failures && mult < 4; i++ {
		mult *= 2
	}
	d := last.Add(interval * mult).Sub(now)
	if jitter > 0 {
		d += time.Duration(rnd(int64(jitter)))
	}
	return max(d, 0)
}
