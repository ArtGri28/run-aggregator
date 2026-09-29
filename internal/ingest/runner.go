package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"aggregator/internal/store"
)

// baselineLimit — сколько прогонов ok берётся в базу (MVP.md §5.4).
const baselineLimit = 7

// errParserPanic — причина failed при панике внутри Parse; стек — в лог.
var errParserPanic = errors.New("паника парсера")

// Runner выполняет прогон источника: чистые стадии Fetch → Parse →
// Prepare целиком, затем Evaluate, и только при ok — запись в каталог
// и наблюдение (research Pattern 2). Нулевые поля — значения по умолчанию.
type Runner struct {
	Store *store.Store
	// Thresholds — пороги Evaluate; нулевое значение — DefaultThresholds().
	Thresholds Thresholds
	// RunTimeout — предел Fetch и Parse (0 → 30 мин); FinalizeTimeout —
	// предел записи, наблюдения и FinishRun (0 → 5 мин); StaleAfter —
	// после скольких минут чужой running считается брошенным
	// (0 → RunTimeout + 15 мин).
	RunTimeout, FinalizeTimeout, StaleAfter time.Duration
	// GoneAfterRuns (0 → 3) и GoneAfter (0 → 72 ч) — когда missing
	// становится gone (MVP.md §4.4).
	GoneAfterRuns int
	GoneAfter     time.Duration
	// Now — часы прогона; nil — time.Now.
	Now func() time.Time
	// Log — nil означает slog.Default().
	Log *slog.Logger
}

// Result — итог и счётчики одного прогона или принятия.
type Result struct {
	RunID   int64
	Status  RunStatus
	Reasons []string

	Fetched, Parsed, Rejected, Expired, OutOfScope int
	Upserted, Created, Changed, Unchanged          int
	Missing, Gone                                  int
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

func (r *Runner) thresholds() Thresholds {
	return orDefault(r.Thresholds, DefaultThresholds())
}

func (r *Runner) runTimeout() time.Duration { return orDefault(r.RunTimeout, 30*time.Minute) }

func (r *Runner) staleAfter() time.Duration {
	return orDefault(r.StaleAfter, r.runTimeout()+15*time.Minute)
}

// finalizeCtx — контекст записи и FinishRun: не отменяется вместе с ctx
// (SIGTERM, таймаут прогона), чтобы строка не осталась в running, а
// каталог — применённым наполовину (Pitfall 7).
func (r *Runner) finalizeCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), orDefault(r.FinalizeTimeout, 5*time.Minute))
}

// Run выполняет один прогон src. Исходы ok/suspect/failed возвращаются
// с err == nil (статус и причины — в Result и ingest_runs); ошибка —
// только инфраструктурная: БД недоступна, по источнику уже идёт прогон
// (store.ErrRunInProgress), сбой записи.
func (r *Runner) Run(ctx context.Context, src Source) (Result, error) {
	var res Result
	code := src.Code()
	sourceID, err := r.Store.EnsureSource(ctx, code)
	if err != nil {
		return res, fmt.Errorf("%s: %w", code, err)
	}
	now := r.now()
	res.RunID, err = r.Store.StartRun(ctx, sourceID, now, r.staleAfter())
	if err != nil {
		return res, fmt.Errorf("%s: %w", code, err)
	}
	log := r.logger().With("source", code, "run_id", res.RunID)
	log.Info("run started")
	began := time.Now()

	runCtx, cancel := context.WithTimeout(ctx, r.runTimeout())
	recs, reason := collect(runCtx, src, log)
	cancel()
	res.Fetched, res.Parsed = len(recs), len(recs)

	fctx, cancelF := r.finalizeCtx(ctx)
	defer cancelF()
	var st Stats
	var base Baseline
	finish := func(f store.RunFinish) error {
		f.Status = string(res.Status)
		f.Fetched, f.Parsed, f.Rejected, f.OutOfScope, f.Upserted = res.Fetched, res.Parsed, res.Rejected, res.OutOfScope, res.Upserted
		f.Error = strings.Join(res.Reasons, "; ")
		if err := r.Store.FinishRun(fctx, res.RunID, f); err != nil {
			return fmt.Errorf("%s: %w", code, err)
		}
		r.logFinished(fctx, log, sourceID, res, st, base, began)
		return nil
	}
	fail := func(reason string) (Result, error) {
		res.Status, res.Reasons = StatusFailed, []string{reason}
		return res, finish(store.RunFinish{})
	}

	if reason != "" {
		return fail(reason)
	}
	prepared, st := Prepare(recs, now, src.Policy())
	res.Rejected, res.Expired, res.OutOfScope = st.Rejected, st.Expired, st.OutOfScope
	logRecords(log, st)

	rows, err := r.Store.BaselineRuns(fctx, sourceID, baselineLimit)
	if err != nil {
		res, _ = fail("база: " + err.Error())
		return res, fmt.Errorf("%s: %w", code, err)
	}
	base = baselineOf(rows)
	res.Status, res.Reasons = Evaluate(st, base, r.thresholds())

	if res.Status == StatusSuspect {
		// Каталог не трогается; записи сохраняются для принятия (D-06).
		snapshot, err := json.Marshal(recs)
		if err != nil {
			return fail("снимок: " + err.Error())
		}
		return res, finish(store.RunFinish{Metrics: st.Metrics(), Snapshot: snapshot})
	}

	if err := r.apply(fctx, sourceID, res.RunID, prepared, &res); err != nil {
		res, _ = fail("запись: " + err.Error())
		return res, fmt.Errorf("%s: %w", code, err)
	}
	if err := r.observe(fctx, sourceID, res.RunID, now, &res); err != nil {
		res, _ = fail("наблюдение: " + err.Error())
		return res, fmt.Errorf("%s: %w", code, err)
	}
	return res, finish(store.RunFinish{Metrics: withCounts(st.Metrics(), res)})
}

// collect выполняет Fetch и Parse всех документов. Непустая причина —
// прогон failed; записи в этом случае не нужны.
func collect(ctx context.Context, src Source, log *slog.Logger) ([]SourceEvent, string) {
	docs, err := src.Fetch(ctx)
	if err != nil {
		return nil, "загрузка: " + err.Error()
	}
	var recs []SourceEvent
	for _, d := range docs {
		evs, err := parseDoc(src, d, log)
		switch {
		case errors.Is(err, errParserPanic):
			return nil, err.Error()
		case errors.Is(err, ErrStructureNotFound):
			return nil, err.Error()
		case err != nil:
			return nil, "разбор: " + err.Error()
		}
		recs = append(recs, evs...)
	}
	if len(recs) == 0 {
		return nil, "parsed = 0"
	}
	return recs, ""
}

// parseDoc вызывает Parse и превращает панику в errParserPanic; стек —
// только в лог уровня ERROR.
func parseDoc(src Source, d RawDoc, log *slog.Logger) (evs []SourceEvent, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Error("parser panic", "url", d.URL, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			evs, err = nil, errParserPanic
		}
	}()
	return src.Parse(d)
}

// Accept принимает последний прогон src в suspect (D-06): применяет его
// снимок без обращения к сайту — Prepare с моментом старта прогона и
// текущей политикой источника, запись, наблюдение — и помечает прогон
// ok с accepted_at; он становится началом окна базы (Pitfall 11).
// runID = 0 — последний прогон; иначе он обязан быть последним.
func (r *Runner) Accept(ctx context.Context, src Source, runID int64) (Result, error) {
	var res Result
	code := src.Code()
	sourceID, err := r.Store.EnsureSource(ctx, code)
	if err != nil {
		return res, fmt.Errorf("%s: %w", code, err)
	}
	last, err := r.Store.LastRun(ctx, sourceID)
	if errors.Is(err, store.ErrNotFound) {
		return res, fmt.Errorf("%s: нет прогонов", code)
	}
	if err != nil {
		return res, fmt.Errorf("%s: %w", code, err)
	}
	switch {
	case runID != 0 && runID != last.ID:
		return res, fmt.Errorf("%s: принимать можно только последний прогон (последний: %d)", code, last.ID)
	case last.Status == "running":
		return res, fmt.Errorf("%s: идёт прогон %d", code, last.ID)
	case last.Status != string(StatusSuspect) || last.Snapshot == nil:
		return res, fmt.Errorf("%s: прогон %d (%s, снимок: %t): %w", code, last.ID, last.Status, last.Snapshot != nil, store.ErrNotAcceptable)
	}
	var recs []SourceEvent
	if err := json.Unmarshal(last.Snapshot, &recs); err != nil {
		return res, fmt.Errorf("%s: снимок прогона %d: %w", code, last.ID, err)
	}

	res.RunID = last.ID
	res.Fetched, res.Parsed = len(recs), len(recs)
	prepared, st := Prepare(recs, last.StartedAt, src.Policy())
	res.Rejected, res.Expired, res.OutOfScope = st.Rejected, st.Expired, st.OutOfScope

	// ponytail: гонка с демоном между проверкой и AcceptRun не закрыта
	// блокировкой; AcceptRun откажет, если за это время начался новый
	// прогон, а уже записанное идемпотентно исправит следующий прогон ok.
	fctx, cancel := r.finalizeCtx(ctx)
	defer cancel()
	if err := r.apply(fctx, sourceID, last.ID, prepared, &res); err != nil {
		return res, fmt.Errorf("%s: запись: %w", code, err)
	}
	if err := r.observe(fctx, sourceID, last.ID, r.now(), &res); err != nil {
		return res, fmt.Errorf("%s: наблюдение: %w", code, err)
	}
	note := "принят вручную; исходная причина: " + last.Error
	if err := r.Store.AcceptRun(fctx, last.ID, res.Upserted, note); err != nil {
		return res, fmt.Errorf("%s: %w", code, err)
	}
	res.Status = StatusOK
	r.logger().Info("run accepted", "source", code, "run_id", last.ID, "reason", last.Error,
		"upserted", res.Upserted, "created", res.Created, "changed", res.Changed, "unchanged", res.Unchanged,
		"out_of_scope", res.OutOfScope, "missing", res.Missing, "gone", res.Gone)
	return res, nil
}

// apply записывает КАЖДУЮ валидную запись прогона, а не только
// изменённые: иначе событие, не созданное из-за сбоя в прошлом прогоне
// или до смены политики страны, не появится никогда (Pitfall 9).
func (r *Runner) apply(ctx context.Context, sourceID, runID int64, prepared []Prepared, res *Result) error {
	for i := range prepared {
		p := &prepared[i]
		id, _, err := r.Store.UpsertSourceEvent(ctx, sourceID, runID, p.Source.ExternalID, p.Source.URL, p.Payload)
		if err != nil {
			return err
		}
		res.Upserted++
		if !p.InScope {
			if err := r.Store.SetOutOfScope(ctx, id); err != nil {
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

// observe — наблюдение после записи здорового прогона; today — UTC-дата now.
func (r *Runner) observe(ctx context.Context, sourceID, runID int64, now time.Time, res *Result) error {
	obs, err := r.Store.Observe(ctx, sourceID, runID, now.UTC(), orDefault(r.GoneAfterRuns, 3), orDefault(r.GoneAfter, 72*time.Hour))
	if err != nil {
		return err
	}
	res.Missing, res.Gone = int(obs.Missing), int(obs.Gone)
	return nil
}

// baselineOf собирает Baseline из прогонов окна: parsed и медианы долей
// пустых полей из metrics.
func baselineOf(rows []store.BaselineRun) Baseline {
	b := Baseline{OKRuns: len(rows), EmptyShare: map[string]float64{}}
	shares := map[string][]float64{}
	for _, row := range rows {
		b.Parsed = append(b.Parsed, row.Parsed)
		var m struct {
			Empty map[string]float64 `json:"empty"`
		}
		if json.Unmarshal(row.Metrics, &m) != nil {
			continue
		}
		for _, f := range emptyFields {
			if v, ok := m.Empty[f]; ok {
				shares[f] = append(shares[f], v)
			}
		}
	}
	for f, vs := range shares {
		b.EmptyShare[f] = median(vs)
	}
	return b
}

// withCounts дополняет метрики Prepare итогом записи и наблюдения.
func withCounts(metrics []byte, res Result) []byte {
	m := map[string]any{}
	_ = json.Unmarshal(metrics, &m)
	m["created"], m["changed"], m["unchanged"] = res.Created, res.Changed, res.Unchanged
	m["missing"], m["gone"] = res.Missing, res.Gone
	// Marshal карты из чисел и вложенных карт не возвращает ошибку.
	b, _ := json.Marshal(m)
	return b
}

// logRecords — отбраковка одной строкой на причину и нераспознанные
// области одной строкой на пару «область — город» (D-09), а не строкой
// на запись.
func logRecords(log *slog.Logger, st Stats) {
	for _, reason := range slices.Sorted(maps.Keys(st.Rejects)) {
		rs := st.Rejects[reason]
		log.Warn("record rejected", "reason", reason, "count", rs.Count, "examples", rs.Examples)
	}
	for _, place := range slices.Sorted(maps.Keys(st.Unrecognized)) {
		log.Warn("unrecognized region", "place", place, "count", st.Unrecognized[place])
	}
}

// logFinished пишет строку run finished (MON-01, MON-02): INFO для ok,
// WARN для suspect, ERROR для failed. Тела ответов, User-Agent и DSN не
// логируются.
func (r *Runner) logFinished(ctx context.Context, log *slog.Logger, sourceID int64, res Result, st Stats, base Baseline, began time.Time) {
	h, err := r.Store.SourceHealth(ctx, sourceID)
	if err != nil {
		log.Warn("source health", "err", err)
	}
	level := slog.LevelInfo
	switch res.Status {
	case StatusSuspect:
		level = slog.LevelWarn
	case StatusFailed:
		level = slog.LevelError
	}
	log.Log(ctx, level, "run finished",
		"status", string(res.Status), "reason", strings.Join(res.Reasons, "; "),
		"fetched", res.Fetched, "parsed", res.Parsed, "rejected", res.Rejected, "expired", res.Expired,
		"out_of_scope", res.OutOfScope, "upserted", res.Upserted,
		"created", res.Created, "changed", res.Changed, "unchanged", res.Unchanged,
		"missing", res.Missing, "gone", res.Gone,
		"date_out_of_range", st.DateOutOfRange, "unrecognized_region", st.UnrecognizedRegion,
		"baseline_runs", base.OKRuns, "baseline_median", median(base.Parsed),
		"duration_ms", time.Since(began).Milliseconds(),
		"consecutive_failures", h.ConsecutiveFailures)
}
