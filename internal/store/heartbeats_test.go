package store

import (
	"context"
	"testing"
	"time"
)

func TestBeat(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for range 2 {
		if err := s.Beat(ctx, "worker"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM heartbeats WHERE component = 'worker'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("строк worker: %d, %v; want 1", n, err)
	}
	// Повторная отметка обновляет beat_at.
	if _, err := s.pool.Exec(ctx, "UPDATE heartbeats SET beat_at = now() - interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Beat(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	ages, err := s.HeartbeatAges(ctx)
	if err != nil || ages["worker"] > 5*time.Second {
		t.Fatalf("после повторной отметки возраст %v, %v", ages["worker"], err)
	}
}

func TestHeartbeatAges(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if ages, err := s.HeartbeatAges(ctx); err != nil || len(ages) != 0 {
		t.Fatalf("пустая таблица: %v, %v", ages, err)
	}
	if err := s.Beat(ctx, "bot"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "INSERT INTO heartbeats (component, beat_at) VALUES ('backup', now() - interval '27 hours')"); err != nil {
		t.Fatal(err)
	}
	ages, err := s.HeartbeatAges(ctx)
	if err != nil || len(ages) != 2 {
		t.Fatalf("HeartbeatAges = %v, %v", ages, err)
	}
	if a := ages["bot"]; a < 0 || a > 5*time.Second {
		t.Errorf("bot: %v, want меньше 5 с", a)
	}
	if a := ages["backup"]; a < 27*time.Hour || a > 27*time.Hour+time.Minute {
		t.Errorf("backup: %v, want около 27 ч", a)
	}
	if _, ok := ages["worker"]; ok {
		t.Error("компонента без строки есть в карте")
	}
}
