// Package alert — цикл оповещений администраторов о прогонах failed и
// suspect (MON-05, D-13). Пакет не знает про Telegram: отправка идёт
// через Loop.Send, SQL — только через internal/store.
package alert

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"aggregator/internal/store"
)

// remindEvery — как часто напоминать, пока плохое состояние держится.
const remindEvery = 24 * time.Hour

// staleAfter — плохой прогон старше этого срока не оповещается: источник
// выключен в worker (например, без токена) и новых прогонов не будет. Самая
// долгая пауза расписания после сбоев — четыре интервала (сутки при
// интервале 6 ч), поэтому у работающего источника прогоны свежее.
const staleAfter = 72 * time.Hour

// Loop опрашивает последние завершённые прогоны источников и шлёт
// администраторам сообщение при переходе в failed или suspect.
type Loop struct {
	Store  *store.Store
	Admins []int64
	// Send отправляет обычный текст одному чату; acceptRunID > 0 — приложить
	// кнопку принятия этого прогона.
	Send func(ctx context.Context, chatID int64, text string, acceptRunID int64) error
	// Now — часы (nil → time.Now); Log (nil → slog.Default()); Every — период
	// опроса (0 → минута).
	Now   func() time.Time
	Log   *slog.Logger
	Every time.Duration
	// Loc — часовой пояс времени в тексте; бот передаёт свой (Europe/Moscow),
	// nil — UTC.
	Loc *time.Location
}

type action int

const (
	none   action = iota
	send          // новое плохое состояние
	remind        // состояние держится ≥ 24 ч
	reset         // источник вернулся в ok: снять состояние без сообщения
)

// decide — что делать с последним завершённым прогоном источника; st —
// строка source_alerts (nil — источник ещё не оповещён).
func decide(run store.FinishedRun, st *store.AlertState, now time.Time) action {
	switch {
	case run.Status == "ok":
		if st != nil {
			return reset
		}
		return none
	case run.Status == "failed" && run.Error == store.RunInterrupted:
		return none // остановка процесса — не сбой источника
	case now.Sub(run.FinishedAt) >= staleAfter:
		return none // источник выключен: прогонов больше нет
	case st == nil, st.State != run.Status:
		return send
	case run.Status == "suspect" && st.RunID != run.RunID:
		// Новый suspect-прогон — новая кнопка принятия; кнопка прежнего алерта
		// уже устарела. Для failed — прежний ритм (раз в сутки), без алерта на
		// каждый повтор расписания.
		return send
	case now.Sub(st.RemindedAt) >= remindEvery:
		return remind
	}
	return none
}

// message — обычный текст без разметки (в причинах бывают «<» и «>»).
// failures — число сбоев подряд (для failed). acceptRunID != 0 только у
// suspect со снимком (D-15).
func message(run store.FinishedRun, failures int, reminder bool, loc *time.Location) (text string, acceptRunID int64) {
	var b strings.Builder
	if reminder {
		b.WriteString("Напоминание: ")
	}
	fmt.Fprintf(&b, "Источник %s: %s (прогон %d), завершён %s МСК\n",
		run.Code, run.Status, run.RunID, run.FinishedAt.In(loc).Format("02.01 15:04"))
	reason := run.Error
	if reason == "" {
		reason = "не указана"
	}
	fmt.Fprintf(&b, "Причина: %s\n", reason)
	if run.Status == "suspect" {
		fmt.Fprintf(&b, "parsed %d · rejected %d · out_of_scope %d · unrecognized_region %d\n",
			run.Parsed, run.Rejected, run.OutOfScope, run.Unrecognized)
		fmt.Fprintf(&b, "Ничего не применено. Принять как базу — кнопкой ниже (если есть) или командой worker accept-baseline --source %s", run.Code)
		if run.HasSnapshot {
			acceptRunID = run.RunID
		}
	} else {
		fmt.Fprintf(&b, "Сбоев подряд: %d", failures)
	}
	return b.String(), acceptRunID
}

func (l *Loop) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Loop) loc() *time.Location {
	if l.Loc != nil {
		return l.Loc
	}
	return time.UTC
}

func (l *Loop) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// Tick один раз сверяет прогоны с source_alerts. Состояние пишется только
// после успешной отправки хотя бы одному администратору (Pitfall 14):
// иначе следующий тик повторяет попытку. Ошибка по одному источнику не
// останавливает обход остальных (IN-03): все ошибки возвращаются вместе.
func (l *Loop) Tick(ctx context.Context) error {
	if len(l.Admins) == 0 {
		return nil
	}
	runs, err := l.Store.LatestFinishedRuns(ctx)
	if err != nil {
		return err
	}
	states, err := l.Store.AlertStates(ctx)
	if err != nil {
		return err
	}
	now := l.now()
	var errs []error
	for _, run := range runs {
		var st *store.AlertState
		if cur, ok := states[run.SourceID]; ok {
			st = &cur
		}
		act := decide(run, st, now)
		switch act {
		case reset:
			if err := l.Store.ClearAlertState(ctx, run.SourceID); err != nil {
				errs = append(errs, err)
			}
		case send, remind:
			failures := 0
			if run.Status == "failed" {
				h, err := l.Store.SourceHealth(ctx, run.SourceID)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				failures = h.ConsecutiveFailures
			}
			text, acceptRunID := message(run, failures, act == remind, l.loc())
			if !l.sendAll(ctx, run.Code, text, acceptRunID) {
				continue
			}
			next := store.AlertState{SourceID: run.SourceID, State: run.Status, RunID: run.RunID, NotifiedAt: now, RemindedAt: now}
			if act == remind {
				next.NotifiedAt = st.NotifiedAt
			}
			if err := l.Store.SaveAlertState(ctx, next); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// sendAll шлёт сообщение каждому администратору; true, если дошло хотя бы
// до одного. В лог — код источника и тип ошибки, без её текста.
func (l *Loop) sendAll(ctx context.Context, code, text string, acceptRunID int64) bool {
	ok := false
	for _, id := range l.Admins {
		if err := l.Send(ctx, id, text, acceptRunID); err != nil {
			l.log().Warn("alert send failed", "source", code, "err_type", fmt.Sprintf("%T", err))
			continue
		}
		ok = true
	}
	return ok
}

// Run делает первый Tick сразу и затем по одному на Every до отмены ctx.
func (l *Loop) Run(ctx context.Context) {
	if len(l.Admins) == 0 {
		l.log().Warn("ADMIN_TG_IDS пуст: алерты отключены")
		return
	}
	every := l.Every
	if every <= 0 {
		every = time.Minute
	}
	tick := func() {
		if err := l.Tick(ctx); err != nil && ctx.Err() == nil {
			l.log().Warn("alert tick", "err", err)
		}
	}
	tick()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
