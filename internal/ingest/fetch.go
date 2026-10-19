package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// FetchConfig — параметры надёжности загрузки (MVP.md §5.3); значения
// по умолчанию — DefaultFetchConfig, переопределяются окружением.
type FetchConfig struct {
	// Timeout — весь запрос, включая чтение тела; ConnectTimeout — подключение.
	Timeout, ConnectTimeout time.Duration
	// MaxBytes — предел тела; больше — ошибка, а не усечение.
	MaxBytes int64
	// Attempts — всего попыток (повторы — только сеть, 5xx и 429).
	Attempts int
	// BackoffBase, BackoffMax — full jitter перед повтором.
	BackoffBase, BackoffMax time.Duration
	// MaxRetryWait — если сервер просит ждать дольше, загрузка прекращается.
	MaxRetryWait time.Duration
	// HostMinInterval — пауза между запросами к хосту, если источник не
	// задал свою (Crawl-delay).
	HostMinInterval time.Duration
	// UserAgent содержит контакт владельца: не логируется и не
	// попадает в тексты ошибок.
	UserAgent string
}

// DefaultFetchConfig — стартовые значения MVP.md §5.3.
func DefaultFetchConfig() FetchConfig {
	return FetchConfig{
		Timeout: 30 * time.Second, ConnectTimeout: 10 * time.Second, MaxBytes: 10 << 20,
		Attempts: 3, BackoffBase: 2 * time.Second, BackoffMax: 30 * time.Second,
		MaxRetryWait: 2 * time.Minute, HostMinInterval: time.Second,
	}
}

// maxRedirects — предел переходов; переход — только на ту же схему и хост,
// иначе редирект обходил бы затвор хоста (T-03-25).
const maxRedirects = 3

var errRedirect = errors.New("редирект запрещён")

// Fetcher загружает страницы источников: повторы, лимиты и затвор хоста —
// не больше одного запроса к хосту одновременно и пауза между концом
// предыдущего запроса и началом следующего, повторы включительно.
type Fetcher struct {
	cfg    FetchConfig
	client *http.Client
	// now и sleep подменяются в тестах.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	mu    sync.Mutex
	gates map[string]*hostGate
}

type hostGate struct {
	mu   sync.Mutex
	last time.Time // конец предыдущего запроса к хосту
}

// retryErr — ошибка, после которой допустим повтор; wait — Retry-After.
type retryErr struct {
	err  error
	wait time.Duration
}

func (e *retryErr) Error() string { return e.err.Error() }
func (e *retryErr) Unwrap() error { return e.err }

// NewFetcher создаёт загрузчик. Прокси из окружения не используются:
// Transport без Proxy. Сжатие HTTP-клиент запрашивает и распаковывает сам.
func NewFetcher(cfg FetchConfig) *Fetcher {
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: cfg.ConnectTimeout}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			first := via[0].URL
			if len(via) > maxRedirects || req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
				return errRedirect
			}
			return nil
		},
	}
	return &Fetcher{cfg: cfg, client: client, now: time.Now, sleep: sleepCtx, gates: map[string]*hostGate{}}
}

// sleepCtx ждёт d или отмену ctx (SIGTERM не ждёт конца паузы).
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (f *Fetcher) gate(host string) *hostGate {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gates[host]
	if g == nil {
		g = &hostGate{}
		f.gates[host] = g
	}
	return g
}

// backoff — full jitter: случайное из [0, min(BackoffMax, BackoffBase·2^(retry−1))].
func (f *Fetcher) backoff(retry int) time.Duration {
	ceil := f.cfg.BackoffBase << (retry - 1)
	if ceil <= 0 || ceil > f.cfg.BackoffMax {
		ceil = f.cfg.BackoffMax
	}
	if ceil <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1))
}

// retryAfter разбирает Retry-After: секунды или HTTP-дата.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		// Без предела большое число секунд переполняет Duration в минус.
		return min(time.Duration(s), math.MaxInt64/time.Second) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, t.Sub(now)), true
	}
	return 0, false
}

// Get загружает rawURL: до Attempts попыток, каждая — после паузы
// minInterval (0 → HostMinInterval) от конца предыдущего запроса к хосту.
// RawDoc.URL — запрошенный адрес (не адрес после редиректа).
func (f *Fetcher) Get(ctx context.Context, rawURL string, minInterval time.Duration) (RawDoc, error) {
	return f.GetAuth(ctx, rawURL, minInterval, "")
}

// GetAuth — Get с заголовком Authorization (непустой auth) и Accept JSON:
// для API, которые выдают ключ. Значение auth не попадает ни в RawDoc, ни в
// тексты ошибок.
func (f *Fetcher) GetAuth(ctx context.Context, rawURL string, minInterval time.Duration, auth string) (RawDoc, error) {
	if f.cfg.UserAgent == "" {
		return RawDoc{}, errors.New("fetch: User-Agent не задан (нужен OWNER_CONTACT)")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return RawDoc{}, errors.New("fetch: неверный адрес")
	}
	if minInterval <= 0 {
		minInterval = f.cfg.HostMinInterval
	}
	var last error
	for attempt := 1; attempt <= f.cfg.Attempts; attempt++ {
		doc, err := f.once(ctx, u, minInterval, auth)
		if err == nil {
			return doc, nil
		}
		last = err
		re, ok := errors.AsType[*retryErr](err)
		if !ok || attempt == f.cfg.Attempts || ctx.Err() != nil {
			break
		}
		wait := max(f.backoff(attempt), re.wait)
		if wait > f.cfg.MaxRetryWait {
			return RawDoc{}, fmt.Errorf("fetch: сервер просит ждать %s — больше предела %s: %w", wait, f.cfg.MaxRetryWait, err)
		}
		if err := f.sleep(ctx, wait); err != nil {
			return RawDoc{}, err
		}
	}
	return RawDoc{}, fmt.Errorf("fetch: %w", last)
}

// once — одна попытка под затвором хоста.
func (f *Fetcher) once(ctx context.Context, u *url.URL, minInterval time.Duration, auth string) (RawDoc, error) {
	g := f.gate(u.Host)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.last.IsZero() {
		if err := f.sleep(ctx, g.last.Add(minInterval).Sub(f.now())); err != nil {
			return RawDoc{}, err
		}
	}
	defer func() { g.last = f.now() }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return RawDoc{}, err
	}
	req.Header.Set("User-Agent", f.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	if auth != "" {
		req.Header.Set("Authorization", auth)
		req.Header.Set("Accept", "application/json")
	}
	resp, err := f.client.Do(req)
	switch {
	case errors.Is(err, errRedirect):
		return RawDoc{}, fmt.Errorf("редирект на другой хост или больше %d переходов", maxRedirects)
	case err != nil && ctx.Err() != nil:
		return RawDoc{}, ctx.Err()
	case err != nil:
		return RawDoc{}, &retryErr{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	const botNote = " — возможна защита от ботов, не обходить"
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		w, _ := retryAfter(resp.Header, f.now())
		return RawDoc{}, &retryErr{err: fmt.Errorf("HTTP %d%s", resp.StatusCode, botNote), wait: w}
	case resp.StatusCode >= 500:
		w, _ := retryAfter(resp.Header, f.now())
		return RawDoc{}, &retryErr{err: fmt.Errorf("HTTP %d", resp.StatusCode), wait: w}
	case resp.StatusCode == http.StatusForbidden:
		return RawDoc{}, fmt.Errorf("HTTP %d%s", resp.StatusCode, botNote)
	case resp.StatusCode != http.StatusOK:
		return RawDoc{}, fmt.Errorf("HTTP %d (без повтора)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
	if err != nil {
		return RawDoc{}, &retryErr{err: err}
	}
	if int64(len(body)) > f.cfg.MaxBytes {
		return RawDoc{}, fmt.Errorf("ответ больше %d байт", f.cfg.MaxBytes)
	}
	if !utf8.Valid(body) {
		if !windows1251(resp.Header.Get("Content-Type")) {
			return RawDoc{}, errors.New("тело не UTF-8")
		}
		if body, err = charmap.Windows1251.NewDecoder().Bytes(body); err != nil {
			return RawDoc{}, fmt.Errorf("перекодирование windows-1251: %w", err)
		}
	}
	return RawDoc{URL: u.String(), Body: body, FetchedAt: f.now()}, nil
}

// windows1251 — сервер объявил кодировку windows-1251 в Content-Type (O-time:
// «charset=cp-1251»). Тело в ней перекодируется в UTF-8; другие кодировки,
// кроме UTF-8, — ошибка, как раньше.
func windows1251(contentType string) bool {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch strings.ToLower(params["charset"]) {
	case "windows-1251", "cp-1251", "cp1251":
		return true
	}
	return false
}
