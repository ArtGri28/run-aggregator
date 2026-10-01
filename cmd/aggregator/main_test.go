package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"aggregator/internal/dedup"
	"aggregator/internal/ingest"
	"aggregator/internal/normalize"
)

// Pitfall 11: worker и bot получают параметры наблюдения из одной конфигурации.
func TestNewRunner(t *testing.T) {
	cfg := ingest.Config{RawDir: "x", RawTTL: time.Hour, GoneAfterRuns: 5, GoneAfter: 10 * time.Hour, RunTimeout: 7 * time.Minute}
	cfg.Thresholds.MinRuns = 9
	dcfg := dedup.DefaultConfig()
	dcfg.DistanceTolM, dcfg.SourcePriority = 50, []string{"probeg", "marathonec"}
	r := newRunner(nil, cfg, dcfg)
	if r.GoneAfterRuns != 5 || r.GoneAfter != 10*time.Hour || r.RunTimeout != 7*time.Minute || r.Thresholds.MinRuns != 9 {
		t.Fatalf("Runner = %+v, параметры не совпали с конфигурацией", r)
	}
	if r.Raw == nil || r.Raw.Dir != "x" || r.Raw.TTL != time.Hour {
		t.Fatalf("Raw = %+v", r.Raw)
	}
	if r.Now == nil || r.Log == nil {
		t.Fatal("Now или Log не заданы")
	}
	// Проход дедупликации и свёртка полей — из той же конфигурации (D-17).
	if r.PostApply == nil || r.Fold.DistanceTolM != 50 || !slices.Equal(r.Fold.Priority, []string{"probeg", "marathonec"}) {
		t.Fatalf("PostApply задан: %v, Fold = %+v", r.PostApply != nil, r.Fold)
	}
}

// DEDUP_* проверяются до подключения к БД; ошибка не повторяет значение.
func TestWorkerDedupConfigFirst(t *testing.T) {
	t.Setenv("DEDUP_HIGH", "x9q")
	t.Setenv("DATABASE_URL", "")
	err := runWorker(context.Background(), []string{"sanity-report", "--source", "marathonec"})
	if err == nil || !strings.Contains(err.Error(), "DEDUP_HIGH") || strings.Contains(err.Error(), "x9q") || strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("runWorker = %v; want ошибка DEDUP_HIGH без значения и до БД", err)
	}
}

// Боту сетевой загрузчик не нужен: источники строятся с nil и Policy не ходит в сеть.
func TestNewSources(t *testing.T) {
	codes := map[string]ingest.Policy{}
	for _, s := range newSources(nil) {
		codes[s.Code()] = s.Policy()
	}
	for _, c := range []string{"marathonec", "probeg", "heroleague"} {
		if _, ok := codes[c]; !ok {
			t.Fatalf("источник %s не зарегистрирован", c)
		}
	}
	if p := codes["probeg"]; p.CountryFallback != normalize.NoFallback {
		t.Fatalf("probeg: Policy = %+v, want NoFallback (D-01)", p)
	}
}
