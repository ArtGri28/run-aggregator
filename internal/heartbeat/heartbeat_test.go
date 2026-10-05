package heartbeat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor ждёт, пока n достигнет want.
func waitFor(t *testing.T, n *atomic.Int32, want int32) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); n.Load() < want; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("отметок %d, want не меньше %d", n.Load(), want)
		}
	}
}

func TestHeartbeatRun(t *testing.T) {
	// Отметка сразу при старте и на каждом тике; ошибка цикл не останавливает.
	var beats atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, func(context.Context) error {
			if beats.Add(1) == 2 {
				return errors.New("база недоступна")
			}
			return nil
		}, 5*time.Millisecond, nil)
	}()
	waitFor(t, &beats, 4)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены ctx")
	}

	// Сразу при старте: период час, отметка одна.
	beats.Store(0)
	ctx, cancel = context.WithCancel(context.Background())
	go Run(ctx, func(context.Context) error { beats.Add(1); return nil }, time.Hour, nil)
	waitFor(t, &beats, 1)
	cancel()
	if n := beats.Load(); n != 1 {
		t.Fatalf("отметок при старте: %d, want 1", n)
	}

	// Гейт false — отметка не ставится; гейт true — ставится.
	beats.Store(0)
	var open atomic.Bool
	var asked atomic.Int32
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, func(context.Context) error { beats.Add(1); return nil }, 5*time.Millisecond,
		func() bool { asked.Add(1); return open.Load() })
	waitFor(t, &asked, 3)
	if n := beats.Load(); n != 0 {
		t.Fatalf("при закрытом гейте отметок %d, want 0", n)
	}
	open.Store(true)
	waitFor(t, &beats, 1)
}
