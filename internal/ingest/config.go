package ingest

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Config — настройки worker; значения по умолчанию — в LoadConfig,
// переопределяются окружением.
type Config struct {
	Fetch      FetchConfig
	Thresholds Thresholds
	// Interval, Jitter — расписание прогонов (ING-09); RunTimeout — предел Fetch и Parse.
	Interval, Jitter, RunTimeout time.Duration
	// RawDir, RawTTL — сырые ответы (ING-10).
	RawDir string
	RawTTL time.Duration
	// GoneAfterRuns, GoneAfter — когда missing становится gone (MVP.md §4.4).
	GoneAfterRuns int
	GoneAfter     time.Duration
}

// userAgentPrefix — имя и версия в User-Agent (01-SOURCES-REPORT §5 п. 8).
const userAgentPrefix = "RunAggregator/0.3 (+contact: "

// LoadConfig читает настройки из окружения. needContact — команде нужен
// сайт: без OWNER_CONTACT она не запускается. Ошибка называет
// переменную, но не повторяет её значение.
func LoadConfig(needContact bool) (Config, error) {
	c := Config{
		Fetch: DefaultFetchConfig(), Thresholds: DefaultThresholds(),
		Interval: 6 * time.Hour, Jitter: 30 * time.Minute, RunTimeout: 30 * time.Minute,
		RawDir: "data/raw", RawTTL: 14 * 24 * time.Hour, GoneAfterRuns: 3, GoneAfter: 72 * time.Hour,
	}
	t, f := &c.Thresholds, &c.Fetch
	vars := []struct {
		name     string
		dst      any
		positive bool
	}{
		{"INGEST_INTERVAL", &c.Interval, true},
		{"INGEST_JITTER", &c.Jitter, false},
		{"INGEST_RUN_TIMEOUT", &c.RunTimeout, true},
		{"RAW_DIR", &c.RawDir, true},
		{"RAW_TTL", &c.RawTTL, true},
		{"OBSERVE_GONE_RUNS", &c.GoneAfterRuns, true},
		{"OBSERVE_GONE_AFTER", &c.GoneAfter, true},
		{"QUALITY_MIN_RUNS", &t.MinRuns, false},
		{"QUALITY_LOW_RATIO", &t.LowRatio, false},
		{"QUALITY_HIGH_RATIO", &t.HighRatio, true},
		{"QUALITY_REJECTED_MAX", &t.RejectedMax, false},
		{"QUALITY_REJECTED_ABS", &t.RejectedAbs, false},
		{"QUALITY_DATE_OOR_MAX", &t.DateOORMax, false},
		{"QUALITY_OUT_OF_SCOPE_MAX", &t.OutOfScopeMax, false},
		{"QUALITY_UNRECOGNIZED_MAX", &t.UnrecognizedMax, false},
		{"QUALITY_EMPTY_GROWTH", &t.EmptyGrowth, false},
		{"FETCH_TIMEOUT", &f.Timeout, true},
		{"FETCH_CONNECT_TIMEOUT", &f.ConnectTimeout, true},
		{"FETCH_MAX_BYTES", &f.MaxBytes, true},
		{"FETCH_ATTEMPTS", &f.Attempts, true},
		{"FETCH_BACKOFF_BASE", &f.BackoffBase, false},
		{"FETCH_BACKOFF_MAX", &f.BackoffMax, false},
		{"FETCH_MAX_RETRY_WAIT", &f.MaxRetryWait, false},
		{"FETCH_HOST_MIN_INTERVAL", &f.HostMinInterval, true},
	}
	for _, v := range vars {
		s, ok := os.LookupEnv(v.name)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		if err := setValue(strings.TrimSpace(s), v.dst, v.positive); err != nil {
			return Config{}, fmt.Errorf("%s: %w", v.name, err)
		}
	}

	contact := strings.TrimSpace(os.Getenv("OWNER_CONTACT"))
	if strings.ContainsFunc(contact, unicode.IsControl) {
		return Config{}, errors.New("OWNER_CONTACT содержит управляющие символы")
	}
	if contact == "" && needContact {
		return Config{}, errors.New("OWNER_CONTACT не задан (нужен для User-Agent)")
	}
	if contact != "" {
		c.Fetch.UserAgent = userAgentPrefix + contact + ")"
	}
	return c, nil
}

// setValue разбирает s в *dst; текст ошибки не содержит s.
func setValue(s string, dst any, positive bool) error {
	bad := errors.New("неверное значение")
	var neg, zero bool
	switch p := dst.(type) {
	case *string:
		*p = s
		return nil
	case *time.Duration:
		d, err := time.ParseDuration(s)
		if err != nil {
			return bad
		}
		*p, neg, zero = d, d < 0, d == 0
	case *int:
		n, err := strconv.Atoi(s)
		if err != nil {
			return bad
		}
		*p, neg, zero = n, n < 0, n == 0
	case *int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return bad
		}
		*p, neg, zero = n, n < 0, n == 0
	case *float64:
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return bad
		}
		*p, neg, zero = x, x < 0, x == 0
	default:
		panic(fmt.Sprintf("setValue: тип %T", dst))
	}
	if neg || (positive && zero) {
		return errors.New("значение должно быть больше нуля")
	}
	return nil
}
