package dedup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"aggregator/internal/store"
)

// DigestLoop раз в день после At шлёт администраторам сводку кандидатов в
// дубли, если с прошлой сводки появились новые (D-11, DDP-08). Пакет не
// знает про Telegram: отправка идёт через Send.
type DigestLoop struct {
	Store  *store.Store
	Admins []int64
	// Send отправляет обычный текст одному чату (бот прикладывает кнопку
	// [Открыть /dupes]).
	Send func(ctx context.Context, chatID int64, text string) error
	// Now — часы (nil → time.Now).
	Now func() time.Time
	// Loc — часовой пояс At (nil → UTC); бот передаёт Europe/Moscow.
	Loc *time.Location
	// At — время сводки «ЧЧ:ММ» (DEDUP_DIGEST_AT).
	At string
	// Every — период проверки (0 → минута); Log (nil → slog.Default()).
	Every time.Duration
	Log   *slog.Logger

	// sent — время последней отправки этим процессом: страховка, если
	// проход удалил всех отмеченных pending и LastDigestAt откатился.
	sent time.Time
}

func (l *DigestLoop) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *DigestLoop) loc() *time.Location {
	if l.Loc != nil {
		return l.Loc
	}
	return time.UTC
}

func (l *DigestLoop) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// digestDue — пора ли слать: сегодняшний слот at (в loc) уже наступил, а
// последняя сводка была раньше него.
func digestDue(now, last time.Time, at string, loc *time.Location) (bool, error) {
	t, err := time.Parse("15:04", at)
	if err != nil {
		return false, fmt.Errorf("время сводки: %w", err)
	}
	n := now.In(loc)
	slot := time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	return !now.Before(slot) && last.Before(slot), nil
}

// Tick один раз проверяет, пора ли слать сводку. Состояния между
// перезапусками в памяти нет: время последней сводки — notified_at в БД,
// поэтому перезапуск в тот же день сводку не повторяет. Отметка ставится
// только после успешной отправки хотя бы одному администратору (Pitfall 14):
// иначе следующий тик повторяет попытку.
func (l *DigestLoop) Tick(ctx context.Context) error {
	if len(l.Admins) == 0 {
		return nil
	}
	last, err := l.Store.LastDigestAt(ctx)
	if err != nil {
		return err
	}
	if l.sent.After(last) {
		last = l.sent
	}
	due, err := digestDue(l.now(), last, l.At, l.loc())
	if err != nil || !due {
		return err
	}
	pending, freshIDs, err := l.Store.DigestCandidates(ctx)
	if err != nil || len(freshIDs) == 0 {
		return err
	}
	fresh := len(freshIDs)
	text := fmt.Sprintf("Сводка дублей: ожидают решения %d, из них новых %d. Открыть очередь — кнопкой ниже или командой /dupes", pending, fresh)
	ok := false
	for _, id := range l.Admins {
		if err := l.Send(ctx, id, text); err != nil {
			l.log().Warn("digest send failed", "err_type", fmt.Sprintf("%T", err))
			continue
		}
		ok = true
	}
	if !ok {
		return nil
	}
	if err := l.Store.MarkCandidatesNotified(ctx, freshIDs); err != nil {
		return err
	}
	l.sent = l.now()
	l.log().Info("digest sent", "pending", pending, "fresh", fresh)
	return nil
}

// Run делает первый Tick сразу и затем по одному на Every до отмены ctx.
func (l *DigestLoop) Run(ctx context.Context) {
	if len(l.Admins) == 0 {
		l.log().Warn("ADMIN_TG_IDS пуст: сводка дублей отключена")
		return
	}
	every := l.Every
	if every <= 0 {
		every = time.Minute
	}
	tick := func() {
		if err := l.Tick(ctx); err != nil && ctx.Err() == nil {
			l.log().Warn("digest tick", "err", err)
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
