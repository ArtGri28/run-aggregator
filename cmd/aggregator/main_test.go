package main

import (
	"testing"
	"time"

	"aggregator/internal/ingest"
)

// Pitfall 11: worker и bot получают параметры наблюдения из одной конфигурации.
func TestNewRunner(t *testing.T) {
	cfg := ingest.Config{RawDir: "x", RawTTL: time.Hour, GoneAfterRuns: 5, GoneAfter: 10 * time.Hour, RunTimeout: 7 * time.Minute}
	cfg.Thresholds.MinRuns = 9
	r := newRunner(nil, cfg)
	if r.GoneAfterRuns != 5 || r.GoneAfter != 10*time.Hour || r.RunTimeout != 7*time.Minute || r.Thresholds.MinRuns != 9 {
		t.Fatalf("Runner = %+v, параметры не совпали с конфигурацией", r)
	}
	if r.Raw == nil || r.Raw.Dir != "x" || r.Raw.TTL != time.Hour {
		t.Fatalf("Raw = %+v", r.Raw)
	}
	if r.Now == nil || r.Log == nil {
		t.Fatal("Now или Log не заданы")
	}
}

// Боту сетевой загрузчик не нужен: источники строятся с nil и Policy не ходит в сеть.
func TestNewSources(t *testing.T) {
	for _, s := range newSources(nil) {
		if s.Code() == "marathonec" {
			_ = s.Policy()
			return
		}
	}
	t.Fatal("источник marathonec не зарегистрирован")
}
