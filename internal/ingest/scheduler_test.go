package ingest

import (
	"testing"
	"time"
)

func TestNextDelay(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	fixed := func(int64) int64 { return int64(10 * time.Minute) }
	for _, tc := range []struct {
		name     string
		last     time.Time
		hasRuns  bool
		jitter   time.Duration
		failures int
		want     time.Duration
	}{
		{"no runs", time.Time{}, false, 0, 0, 0},
		{"1h ago", now.Add(-time.Hour), true, 0, 0, 5 * time.Hour},
		{"1 failed", now.Add(-time.Hour), true, 0, 1, 11 * time.Hour},
		{"5 failed capped 4x", now.Add(-time.Hour), true, 0, 5, 23 * time.Hour},
		{"jitter", now.Add(-time.Hour), true, 30 * time.Minute, 0, 5*time.Hour + 10*time.Minute},
		{"10 days ago", now.Add(-240 * time.Hour), true, 0, 0, 0},
		{"many failures no overflow", now.Add(-time.Hour), true, 0, 100, 23 * time.Hour},
	} {
		if got := nextDelay(tc.last, tc.hasRuns, now, 6*time.Hour, tc.jitter, tc.failures, fixed); got != tc.want {
			t.Errorf("%s: nextDelay = %v, want %v", tc.name, got, tc.want)
		}
	}
}
