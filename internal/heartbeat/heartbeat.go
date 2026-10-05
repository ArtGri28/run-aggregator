// Package heartbeat — периодическая отметка «процесс жив» (MON-03). Пакет
// не знает про хранилище: отметку ставит переданная функция.
package heartbeat

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// beatTimeout — предел одной отметки.
const beatTimeout = 10 * time.Second

// Run ставит отметку сразу и затем раз в every (0 и меньше — минута) до
// отмены ctx. gate != nil и вернул false — отметка пропускается. Ошибка
// beat цикл не останавливает; сбой и восстановление записи — по одной
// записи в лог на смену состояния, с типом ошибки без её текста.
func Run(ctx context.Context, beat func(context.Context) error, every time.Duration, gate func() bool) {
	if every <= 0 {
		every = time.Minute
	}
	failing := false
	once := func() {
		if gate != nil && !gate() {
			return
		}
		bctx, cancel := context.WithTimeout(ctx, beatTimeout)
		err := beat(bctx)
		cancel()
		switch {
		case err != nil && !failing && ctx.Err() == nil:
			failing = true
			slog.Default().Warn("heartbeat failed", "err_type", fmt.Sprintf("%T", err))
		case err == nil && failing:
			failing = false
			slog.Default().Info("heartbeat restored")
		}
	}
	once()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			once()
		}
	}
}
