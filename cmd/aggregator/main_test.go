package main

import (
	"context"
	"errors"
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
	for _, c := range []string{"marathonec", "probeg", "heroleague", "tristyle"} {
		if _, ok := codes[c]; !ok {
			t.Fatalf("источник %s не зарегистрирован", c)
		}
	}
	// klbamatar и runin исключены из активного сбора решением владельца.
	if len(codes) != 4 {
		t.Fatalf("зарегистрировано источников: %d, want 4", len(codes))
	}
	if p := codes["probeg"]; p.CountryFallback != normalize.NoFallback {
		t.Fatalf("probeg: Policy = %+v, want NoFallback (D-01)", p)
	}
}

func TestUnmergeArgs(t *testing.T) {
	for _, c := range []struct {
		args  []string
		ok    bool
		apply bool
	}{
		{[]string{"12"}, true, false},
		{[]string{"-apply", "-by", "7", "12"}, true, true},
		{[]string{"-apply", "12"}, false, false},    // без tg_id администратора
		{[]string{}, false, false},                  // нет события
		{[]string{"12", "13"}, false, false},        // два события
		{[]string{"0"}, false, false},               // id не положительный
		{[]string{"x"}, false, false},               // не число
		{[]string{"-by", "-1", "12"}, false, false}, // отрицательный tg_id
		{[]string{"-unknown", "12"}, false, false},  // неизвестный флаг
	} {
		id, apply, _, err := unmergeArgs(c.args)
		if c.ok != (err == nil) || (c.ok && (id != 12 || apply != c.apply)) {
			t.Errorf("%v: id=%d apply=%v err=%v", c.args, id, apply, err)
		}
	}
}

// MON-03: healthcheck компоненты — отметка есть и не старше предела.
func TestHeartbeatFresh(t *testing.T) {
	ages := map[string]time.Duration{"bot": 5 * time.Minute, "worker": 5*time.Minute + time.Second}
	if err := heartbeatFresh(ages, "bot", healthMaxAge); err != nil {
		t.Fatalf("ровно 5 минут: %v, want nil", err)
	}
	if err := heartbeatFresh(ages, "worker", healthMaxAge); err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("старше 5 минут: %v, want ошибка с именем компоненты", err)
	}
	if err := heartbeatFresh(map[string]time.Duration{"bot": time.Second}, "worker", healthMaxAge); err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("строки нет: %v, want ошибка с именем компоненты", err)
	}
	// Неверный аргумент — usage до обращения к БД.
	t.Setenv("DATABASE_URL", "")
	for _, args := range [][]string{{"postgres"}, {"bot", "worker"}} {
		if err := runHealthcheck(context.Background(), args); !errors.Is(err, errUsage) {
			t.Fatalf("healthcheck %v: %v, want errUsage", args, err)
		}
	}
	if err := runHealthcheck(context.Background(), []string{"bot"}); err == nil || errors.Is(err, errUsage) {
		t.Fatalf("healthcheck bot без DATABASE_URL: %v, want ошибка окружения", err)
	}
}
