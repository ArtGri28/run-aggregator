package ingest

import (
	"strings"
	"testing"
	"time"
)

// configVars — все переменные LoadConfig: тест очищает их, чтобы не
// зависеть от окружения того, кто запускает тесты (пустое = не задано).
var configVars = strings.Fields(`OWNER_CONTACT INGEST_INTERVAL INGEST_JITTER INGEST_RUN_TIMEOUT RAW_DIR RAW_TTL
	OBSERVE_GONE_RUNS OBSERVE_GONE_AFTER QUALITY_MIN_RUNS QUALITY_LOW_RATIO QUALITY_HIGH_RATIO
	QUALITY_REJECTED_MAX QUALITY_REJECTED_ABS QUALITY_DATE_OOR_MAX QUALITY_OUT_OF_SCOPE_MAX
	QUALITY_UNRECOGNIZED_MAX QUALITY_EMPTY_GROWTH FETCH_TIMEOUT FETCH_CONNECT_TIMEOUT FETCH_MAX_BYTES
	FETCH_ATTEMPTS FETCH_BACKOFF_BASE FETCH_BACKOFF_MAX FETCH_MAX_RETRY_WAIT FETCH_HOST_MIN_INTERVAL`)

func TestLoadConfig(t *testing.T) {
	for _, k := range configVars {
		t.Setenv(k, "")
	}
	t.Run("defaults", func(t *testing.T) {
		c, err := LoadConfig(false)
		if err != nil {
			t.Fatal(err)
		}
		want := Config{
			Fetch: DefaultFetchConfig(), Thresholds: DefaultThresholds(),
			Interval: 6 * time.Hour, Jitter: 30 * time.Minute, RunTimeout: 30 * time.Minute,
			RawDir: "data/raw", RawTTL: 336 * time.Hour, GoneAfterRuns: 3, GoneAfter: 72 * time.Hour,
		}
		if c != want {
			t.Fatalf("LoadConfig = %+v\nwant %+v", c, want)
		}
		f := DefaultFetchConfig()
		if f.Timeout != 30*time.Second || f.ConnectTimeout != 10*time.Second || f.MaxBytes != 10<<20 ||
			f.Attempts != 3 || f.BackoffBase != 2*time.Second || f.BackoffMax != 30*time.Second ||
			f.MaxRetryWait != 2*time.Minute || f.HostMinInterval != time.Second {
			t.Fatalf("DefaultFetchConfig = %+v", f)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		for k, v := range map[string]string{
			"INGEST_INTERVAL": "1h", "INGEST_JITTER": "0s", "INGEST_RUN_TIMEOUT": "5m",
			"RAW_DIR": "/tmp/raw", "RAW_TTL": "48h",
			"OBSERVE_GONE_RUNS": "5", "OBSERVE_GONE_AFTER": "24h",
			"QUALITY_MIN_RUNS": "4", "QUALITY_LOW_RATIO": "0.4", "QUALITY_HIGH_RATIO": "6",
			"QUALITY_REJECTED_MAX": "0.1", "QUALITY_REJECTED_ABS": "0.25", "QUALITY_DATE_OOR_MAX": "0.02",
			"QUALITY_OUT_OF_SCOPE_MAX": "0.3", "QUALITY_UNRECOGNIZED_MAX": "0.15", "QUALITY_EMPTY_GROWTH": "0.1",
			"FETCH_TIMEOUT": "20s", "FETCH_ATTEMPTS": "2", "FETCH_MAX_BYTES": "1000",
			"FETCH_HOST_MIN_INTERVAL": "3s",
		} {
			t.Setenv(k, v)
		}
		c, err := LoadConfig(false)
		if err != nil {
			t.Fatal(err)
		}
		th := Thresholds{MinRuns: 4, LowRatio: 0.4, HighRatio: 6, RejectedMax: 0.1, RejectedAbs: 0.25,
			DateOORMax: 0.02, OutOfScopeMax: 0.3, UnrecognizedMax: 0.15, EmptyGrowth: 0.1}
		if c.Interval != time.Hour || c.Jitter != 0 || c.RunTimeout != 5*time.Minute || c.RawDir != "/tmp/raw" ||
			c.RawTTL != 48*time.Hour || c.GoneAfterRuns != 5 || c.GoneAfter != 24*time.Hour || c.Thresholds != th ||
			c.Fetch.Timeout != 20*time.Second || c.Fetch.Attempts != 2 || c.Fetch.MaxBytes != 1000 ||
			c.Fetch.HostMinInterval != 3*time.Second {
			t.Fatalf("LoadConfig = %+v", c)
		}
	})
	t.Run("bad value", func(t *testing.T) {
		for _, kv := range [][2]string{{"INGEST_INTERVAL", "SECRETVAL"}, {"QUALITY_LOW_RATIO", "SECRETVAL"},
			{"OBSERVE_GONE_RUNS", "-1"}, {"FETCH_ATTEMPTS", "0"}} {
			t.Run(kv[0], func(t *testing.T) {
				t.Setenv(kv[0], kv[1])
				_, err := LoadConfig(false)
				if err == nil || !strings.Contains(err.Error(), kv[0]) || strings.Contains(err.Error(), kv[1]) {
					t.Fatalf("err = %v, want name %s without the value", err, kv[0])
				}
			})
		}
	})
	t.Run("contact", func(t *testing.T) {
		t.Setenv("OWNER_CONTACT", "")
		if _, err := LoadConfig(true); err == nil || !strings.Contains(err.Error(), "OWNER_CONTACT") {
			t.Fatalf("err = %v, want OWNER_CONTACT", err)
		}
		t.Setenv("OWNER_CONTACT", "me@example.invalid")
		c, err := LoadConfig(true)
		if err != nil || c.Fetch.UserAgent != "RunAggregator/0.3 (+contact: me@example.invalid)" {
			t.Fatalf("UserAgent = %q, %v", c.Fetch.UserAgent, err)
		}
		t.Setenv("OWNER_CONTACT", "me@example.invalid\r\nX-Evil: 1")
		if _, err := LoadConfig(false); err == nil || strings.Contains(err.Error(), "me@example") {
			t.Fatalf("err = %v, want error without the value", err)
		}
		// Placeholder copied from instructions instead of a real contact.
		t.Setenv("OWNER_CONTACT", "<ваш контакт>")
		if _, err := LoadConfig(false); err == nil || strings.Contains(err.Error(), "ваш") {
			t.Fatalf("err = %v, want placeholder error without the value", err)
		}
	})
}
