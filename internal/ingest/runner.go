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
	"sync/atomic"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

// baselineLimit — сколько прогонов ok берётся в базу (MVP.md §5.4).
const baselineLimit = 7

// watchFuse — предохранитель шага сообщений: больше стольких стартов с
// изменением или скрытием за один прогон (считаются вместе, по всем будущим
// стартам каталога, а не только по избранным) — строки очереди пользователям
// не создаются вовсе.
//
// Упрощение: два потолка. Законное массовое изменение или исчезновение
// (источник перенёс серию стартов) пользователям не сообщается. Частичный сбой
// источника, скрывший не больше порога стартов, даст подписчикам сообщение
// «старт пропал из календаря».
const watchFuse = 20

// errParserPanic — причина failed при панике внутри Parse; стек — в лог.
var errParserPanic = errors.New("паника парсера")

// Runner выполняет прогон источника: чистые стадии Fetch → Parse →
// Prepare целиком, затем Evaluate, и только при ok — запись в каталог
// и наблюдение (research Pattern 2). Нулевые поля — значения по умолчанию.
type Runner struct {
	Store *store.Store
	// Raw — хранилище сырых ответов (ING-10); nil — не сохранять.
	Raw *RawStore
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
	// PostApply — шаг после применения прогона к каталогу (дедупликация):
	// вызывается после успешного Run (ok) и после Accept; nil — шага нет.
	// Ошибка только в лог, статус прогона не меняется. PostApplyTimeout —
	// его собственный предел (0 → 5 мин), не остаток FinalizeTimeout (L3).
	PostApply        func(ctx context.Context) error
	PostApplyTimeout time.Duration
	// postApplyFailed — ошибки PostApply с запуска процесса (поле лога
	// post_apply_failed_total).
	postApplyFailed atomic.Int64
	// Fold — приоритет источников и допуск дистанций для полей события с
	// несколькими записями (D-07); нулевое — порядок записей и точные
	// метры. Заполняет newRunner из настроек дедупликации.
	Fold event.FoldConfig
}

// Result — итог и счётчики одного прогона или принятия.
type Result struct {
	RunID   int64
	Status  RunStatus
	Reasons []string

	Fetched, Parsed, Rejected, Expired, OutOfScope int
	Upserted, Created, Changed, Unchanged          int
	Missing, Gone                                  int
	// Шаг сообщений (избранное): WatchChanged — стартов с изменением ссылки,
	// даты или статуса; WatchGone — скрытых стартов; WatchQueued — созданных
	// строк очереди; WatchHeld — сумма изменений и скрытий, когда сработал
	// предохранитель (строк тогда нет), иначе 0.
	WatchChanged, WatchGone, WatchQueued, WatchHeld int
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
	recs, reason := r.collect(runCtx, src, res.RunID, log)
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
		if ctx.Err() != nil {
			// Остановка процесса, а не сбой источника (runTimeout отменяет
			// только runCtx).
			reason = store.RunInterrupted
		}
		return fail(reason)
	}
	pol := src.Policy()
	prepared, st := Prepare(recs, now, pol)
	res.Rejected, res.Expired, res.OutOfScope = st.Rejected, st.Expired, st.OutOfScope
	logRecords(log, st)

	rows, err := r.Store.BaselineRuns(fctx, sourceID, baselineLimit)
	if err != nil {
		res, _ = fail("база: " + err.Error())
		return res, fmt.Errorf("%s: %w", code, err)
	}
	base = baselineOf(rows)
	t := r.thresholds()
	if pol.OutOfScopeMax > 0 {
		// Порог источника — только для его прогонов (Pitfall 5).
		t.OutOfScopeMax = pol.OutOfScopeMax
	}
	res.Status, res.Reasons = Evaluate(st, base, t)

	if res.Status == StatusSuspect {
		// Каталог не трогается; записи сохраняются для принятия (D-06).
		snapshot, err := json.Marshal(recs)
		if err != nil {
			return fail("снимок: " + err.Error())
		}
		return res, finish(store.RunFinish{Metrics: st.Metrics(), Snapshot: snapshot})
	}

	if err := r.Store.InTx(fctx, func(tx *store.Store) error {
		return r.write(fctx, tx, sourceID, res.RunID, recs, prepared, now, &res)
	}); err != nil {
		// Транзакция откатилась: каталог не тронут.
		res.Upserted, res.Created, res.Changed, res.Unchanged, res.Missing, res.Gone = 0, 0, 0, 0, 0, 0
		res.WatchChanged, res.WatchGone, res.WatchQueued, res.WatchHeld = 0, 0, 0, 0
		res, _ = fail(err.Error())
		return res, fmt.Errorf("%s: %w", code, err)
	}
	if err := finish(store.RunFinish{Metrics: withCounts(st.Metrics(), res)}); err != nil {
		return res, err
	}
	r.postApply(fctx, log)
	return res, nil
}

// postApply выполняет PostApply, если он задан, со своим тайм-аутом и без
// отмены вместе с ctx (каталог прогона уже записан); ошибка — только в лог
// со счётчиком ошибок с запуска процесса.
func (r *Runner) postApply(ctx context.Context, log *slog.Logger) {
	if r.PostApply == nil {
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), orDefault(r.PostApplyTimeout, 5*time.Minute))
	defer cancel()
	if err := r.PostApply(pctx); err != nil {
		log.Warn("post-apply failed", "err", err, "post_apply_failed_total", r.postApplyFailed.Add(1))
	}
}

// collect выполняет Fetch, сохраняет сырые ответы (до Parse — чтобы
// упавший разбор можно было повторить по файлу) и разбирает все
// документы. Непустая причина — прогон failed; записи тогда не нужны.
func (r *Runner) collect(ctx context.Context, src Source, runID int64, log *slog.Logger) ([]SourceEvent, string) {
	docs, err := src.Fetch(ctx)
	if err != nil {
		return nil, "загрузка: " + err.Error()
	}
	size := 0
	for _, d := range docs {
		size += len(d.Body)
	}
	log.Info("fetch done", "docs", len(docs), "bytes", size)
	if r.Raw != nil {
		if err := r.Raw.Save(src.Code(), runID, docs); err != nil {
			log.Warn("raw save failed", "err", err)
		}
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
// ok с accepted_at, счётчиками и metrics этого применения; он становится
// началом окна базы (Pitfall 11). Всё — одной транзакцией, начатой с
// ClaimRun: отказ или сбой ничего не записывает.
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
		return res, fmt.Errorf("%s: принимать можно только последний прогон (последний: %d): %w", code, last.ID, store.ErrNotAcceptable)
	case last.Status == "running":
		return res, fmt.Errorf("%s: идёт прогон %d: %w", code, last.ID, store.ErrRunInProgress)
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

	fctx, cancel := r.finalizeCtx(ctx)
	defer cancel()
	err = r.Store.InTx(fctx, func(tx *store.Store) error {
		// Прогон, начатый демоном после проверки выше, делает принятие
		// невозможным до записи; начатый позже ждёт конца транзакции.
		if err := tx.ClaimRun(fctx, last.ID); err != nil {
			return err
		}
		if err := r.write(fctx, tx, sourceID, last.ID, recs, prepared, r.now(), &res); err != nil {
			return err
		}
		return tx.FinishRun(fctx, last.ID, store.RunFinish{
			Status: string(StatusOK), Accepted: true,
			Fetched: res.Fetched, Parsed: res.Parsed, Rejected: res.Rejected, OutOfScope: res.OutOfScope, Upserted: res.Upserted,
			Error:   "принят вручную; исходная причина: " + last.Error,
			Metrics: withCounts(st.Metrics(), res),
		})
	})
	if err != nil {
		return Result{}, fmt.Errorf("%s: прогон %d: %w", code, last.ID, err)
	}
	res.Status = StatusOK
	log := r.logger().With("source", code, "run_id", last.ID)
	log.Info("run accepted", "reason", last.Error,
		"upserted", res.Upserted, "created", res.Created, "changed", res.Changed, "unchanged", res.Unchanged,
		"out_of_scope", res.OutOfScope, "missing", res.Missing, "gone", res.Gone)
	r.postApply(fctx, log)
	return res, nil
}

// write применяет здоровый прогон в транзакции tx: записи, отметка
// найденных строк, наблюдение. Ошибка откатывает всё: failed-прогон не
// применяет ничего (MVP.md §5.4). Вокруг — шаг сообщений (watchBefore, watch):
// он только читает итог записи и наблюдения и пишет строки очереди, а его
// ошибка из write не возвращается.
func (r *Runner) write(ctx context.Context, tx *store.Store, sourceID, runID int64, recs []SourceEvent, prepared []Prepared, now time.Time, res *Result) error {
	// Запись прогонов всех источников, проход дедупликации и слияния идут
	// под одной блокировкой: без тупиков и гонки за поля общего события.
	// Упрощение: источники пишутся по очереди (секунды на прогон);
	// блокировки по событиям — если это станет узким местом.
	if err := tx.LockDedup(ctx); err != nil {
		return fmt.Errorf("запись: %w", err)
	}
	before := r.watchBefore(ctx, tx, runID, now)
	if err := r.apply(ctx, tx, sourceID, runID, prepared, res); err != nil {
		return fmt.Errorf("запись: %w", err)
	}
	// Отбракованная или прошедшая строка на странице — найдена: без этого
	// наблюдение увело бы её в missing и gone.
	var ids []string
	for _, rec := range recs {
		if rec.ExternalID != "" {
			ids = append(ids, rec.ExternalID)
		}
	}
	if err := tx.MarkSeen(ctx, sourceID, runID, ids); err != nil {
		return fmt.Errorf("запись: %w", err)
	}
	if err := r.observe(ctx, tx, sourceID, runID, now, res); err != nil {
		return fmt.Errorf("наблюдение: %w", err)
	}
	r.watch(ctx, tx, runID, now, before, res)
	return nil
}

// watchBefore — снимок состояния будущих видимых стартов до применения
// прогона; nil — снимок не прочитан, шага сообщений в этом прогоне нет.
// Дата «сегодня» — UTC-дата now, как у наблюдения.
func (r *Runner) watchBefore(ctx context.Context, tx *store.Store, runID int64, now time.Time) map[int64]event.WatchState {
	var before map[int64]event.WatchState
	err := tx.InTx(ctx, func(sp *store.Store) (err error) {
		before, err = sp.WatchStates(ctx, now.UTC())
		return err
	})
	if err != nil {
		r.logger().Warn("watch failed", "run_id", runID, "err", err)
		return nil
	}
	return before
}

// watch — шаг сообщений после наблюдения: снимок «после», перечень стартов
// снимка «до», скрытых этим прогоном (D-17), сравнение, предохранитель и
// строки очереди подписчикам — в транзакции прогона.
//
// Упрощение: шаг идёт в точке сохранения и прогону не мешает — его ошибка
// откатывает только шаг и пишется в лог, каталог применяется. Потолок:
// сообщения этого прогона теряются — следующий прогон этих изменений и
// скрытий уже не увидит.
//
// В лог идут только счётчики: ни названий стартов, ни идентификаторов событий
// и пользователей.
func (r *Runner) watch(ctx context.Context, tx *store.Store, runID int64, now time.Time, before map[int64]event.WatchState, res *Result) {
	if before == nil {
		return
	}
	var changed, gone, queued, held int
	err := tx.InTx(ctx, func(sp *store.Store) error {
		after, err := sp.WatchStates(ctx, now.UTC())
		if err != nil {
			return err
		}
		var lost []int64
		for id := range before {
			if _, ok := after[id]; !ok {
				lost = append(lost, id)
			}
		}
		hidden, err := sp.HiddenEvents(ctx, lost)
		if err != nil {
			return err
		}
		changes := event.WatchDiff(before, after, hidden)
		gone = len(hidden)
		changed = len(changes) - gone
		if len(changes) > watchFuse {
			held = len(changes)
			return nil
		}
		// Не больше watchFuse запросов на прогон.
		for _, c := range changes {
			n, err := sp.EnqueueChange(ctx, runID, c, now)
			if err != nil {
				return err
			}
			queued += n
		}
		return nil
	})
	log := r.logger().With("run_id", runID)
	switch {
	case err != nil:
		log.Warn("watch failed", "err", err)
		return
	case held > 0:
		log.Warn("watch held", "changed", changed, "gone", gone, "queued", 0, "threshold", watchFuse)
	case changed+gone > 0:
		log.Info("watch", "changed", changed, "gone", gone, "queued", queued)
	}
	res.WatchChanged, res.WatchGone, res.WatchQueued, res.WatchHeld = changed, gone, queued, held
}

// apply записывает КАЖДУЮ валидную запись прогона, а не только
// изменённые: иначе событие, не созданное из-за сбоя в прошлом прогоне
// или до смены политики страны, не появится никогда (Pitfall 9).
func (r *Runner) apply(ctx context.Context, tx *store.Store, sourceID, runID int64, prepared []Prepared, res *Result) error {
	for i := range prepared {
		p := &prepared[i]
		id, _, err := tx.UpsertSourceEvent(ctx, sourceID, runID, p.Source.ExternalID, p.Source.URL, p.Payload)
		if err != nil {
			return err
		}
		res.Upserted++
		if !p.InScope {
			if err := tx.SetOutOfScope(ctx, id); err != nil {
				return err
			}
			continue
		}
		created, changed, err := tx.SaveEventFor(ctx, id, &p.Event, r.Fold)
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
func (r *Runner) observe(ctx context.Context, tx *store.Store, sourceID, runID int64, now time.Time, res *Result) error {
	obs, err := tx.Observe(ctx, sourceID, runID, now.UTC(), orDefault(r.GoneAfterRuns, 3), orDefault(r.GoneAfter, 72*time.Hour))
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
	// Ключ есть только у прогона со сработавшим предохранителем: по нему бот
	// сообщает администратору; метрики остальных прогонов прежние.
	if res.WatchHeld > 0 {
		m["watch_held"] = res.WatchHeld
	}
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
		"date_out_of_range", st.DateOutOfRange, "collapsed", st.Collapsed, "unrecognized_region", st.UnrecognizedRegion,
		"baseline_runs", base.OKRuns, "baseline_median", median(base.Parsed),
		"duration_ms", time.Since(began).Milliseconds(),
		"consecutive_failures", h.ConsecutiveFailures)
}
