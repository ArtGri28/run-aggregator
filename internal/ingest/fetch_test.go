package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testUA = "RunAggregator/0.3 (+contact: test@example.invalid)"

// fakeClock — часы и сон без реальных ожиданий: сон записывает
// длительность и сдвигает часы.
type fakeClock struct {
	t     time.Time
	waits []time.Duration
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if d > 0 {
		c.waits = append(c.waits, d)
		c.t = c.t.Add(d)
	}
	return ctx.Err()
}

func testFetcher(cfg FetchConfig) (*Fetcher, *fakeClock) {
	if cfg.UserAgent == "" {
		cfg.UserAgent = testUA
	}
	f := NewFetcher(cfg)
	c := &fakeClock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	f.now, f.sleep = c.now, c.sleep
	return f, c
}

// server считает запросы и отвечает по номеру запроса (с 1).
func server(t *testing.T, h func(n int, w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(int(n.Add(1)), w, r)
	}))
	t.Cleanup(s.Close)
	return s, &n
}

func TestFetchOK(t *testing.T) {
	var gotUA, gotAE string
	s, _ := server(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		gotUA, gotAE = r.Header.Get("User-Agent"), r.Header.Get("Accept-Encoding")
		if !strings.Contains(gotAE, "gzip") {
			_, _ = w.Write([]byte("plain"))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte("<html>привет</html>"))
		_ = zw.Close()
	})
	f, c := testFetcher(DefaultFetchConfig())
	doc, err := f.Get(context.Background(), s.URL+"/calendar-beg/", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(doc.Body) != "<html>привет</html>" {
		t.Fatalf("body = %q (Accept-Encoding %q), want decompressed", doc.Body, gotAE)
	}
	if gotUA != testUA {
		t.Fatalf("User-Agent = %q", gotUA)
	}
	if doc.URL != s.URL+"/calendar-beg/" || !doc.FetchedAt.Equal(c.t) {
		t.Fatalf("doc = %q %v", doc.URL, doc.FetchedAt)
	}
}

func hasWait(waits []time.Duration, pred func(time.Duration) bool) bool {
	for _, w := range waits {
		if pred(w) {
			return true
		}
	}
	return false
}

func TestFetchRetry(t *testing.T) {
	t.Run("503 Retry-After then 200", func(t *testing.T) {
		s, n := server(t, func(i int, w http.ResponseWriter, _ *http.Request) {
			if i == 1 {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("ok"))
		})
		f, c := testFetcher(DefaultFetchConfig())
		doc, err := f.Get(context.Background(), s.URL, 40*time.Second)
		if err != nil || string(doc.Body) != "ok" {
			t.Fatalf("Get = %q, %v", doc.Body, err)
		}
		if n.Load() != 2 {
			t.Fatalf("requests = %d, want 2", n.Load())
		}
		// Повтор ждёт Retry-After, затем затвор хоста добирает до 40 с.
		if !hasWait(c.waits, func(d time.Duration) bool { return d >= 7*time.Second && d < 8*time.Second }) ||
			!hasWait(c.waits, func(d time.Duration) bool { return d > 30*time.Second && d <= 33*time.Second }) {
			t.Fatalf("waits = %v, want Retry-After ≥ 7s and host gate ≈ 33s", c.waits)
		}
	})
	t.Run("429 retried", func(t *testing.T) {
		s, n := server(t, func(i int, w http.ResponseWriter, _ *http.Request) {
			if i == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte("ok"))
		})
		f, _ := testFetcher(DefaultFetchConfig())
		if _, err := f.Get(context.Background(), s.URL, 0); err != nil || n.Load() != 2 {
			t.Fatalf("Get err = %v, requests = %d; want nil, 2", err, n.Load())
		}
	})
	t.Run("three 503", func(t *testing.T) {
		s, n := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		f, _ := testFetcher(DefaultFetchConfig())
		_, err := f.Get(context.Background(), s.URL, 0)
		if err == nil || !strings.Contains(err.Error(), "HTTP 503") || n.Load() != 3 {
			t.Fatalf("err = %v, requests = %d; want HTTP 503, 3", err, n.Load())
		}
	})
}

func TestFetchNoRetry4xx(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
		s, n := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
		f, _ := testFetcher(DefaultFetchConfig())
		_, err := f.Get(context.Background(), s.URL, 0)
		want := fmt.Sprintf("HTTP %d", code)
		if err == nil || !strings.Contains(err.Error(), want) || n.Load() != 1 {
			t.Fatalf("%d: err = %v, requests = %d; want %q, 1 request", code, err, n.Load(), want)
		}
		if code == http.StatusForbidden && !strings.Contains(err.Error(), "не обходить") {
			t.Fatalf("403 err = %v, want bot-protection note", err)
		}
		if strings.Contains(err.Error(), testUA) {
			t.Fatalf("error leaks User-Agent: %v", err)
		}
	}
}

// GetAuth: заголовок доходит до сервера, Accept — JSON; 401 без повтора, и
// значение заголовка не попадает ни в ошибку, ни в RawDoc.
func TestFetchAuth(t *testing.T) {
	const auth = "Bearer secret-test-value"
	var gotAuth, gotAccept string
	s, n := server(t, func(i int, w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		if i > 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	f, _ := testFetcher(DefaultFetchConfig())
	doc, err := f.GetAuth(context.Background(), s.URL, 0, auth)
	if err != nil || gotAuth != auth || gotAccept != "application/json" || strings.Contains(doc.URL, "secret") {
		t.Fatalf("doc=%+v err=%v auth=%q accept=%q", doc, err, gotAuth, gotAccept)
	}
	_, err = f.GetAuth(context.Background(), s.URL, 0, auth)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "secret") || n.Load() != 2 {
		t.Fatalf("401: err = %v, requests = %d", err, n.Load())
	}
	if _, err := f.Get(context.Background(), s.URL, 0); gotAuth != "" || err == nil {
		t.Fatalf("Get без ключа отправил Authorization %q", gotAuth)
	}
}

func TestFetchRetryAfterTooLong(t *testing.T) {
	s, n := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	f, c := testFetcher(DefaultFetchConfig())
	_, err := f.Get(context.Background(), s.URL, 0)
	if err == nil || n.Load() != 1 {
		t.Fatalf("err = %v, requests = %d; want error after 1 request", err, n.Load())
	}
	if hasWait(c.waits, func(d time.Duration) bool { return d >= 2*time.Minute }) {
		t.Fatalf("waits = %v, must not sleep for Retry-After above MaxRetryWait", c.waits)
	}
}

func TestFetchOversize(t *testing.T) {
	cfg := DefaultFetchConfig()
	cfg.MaxBytes = 10
	s, _ := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 11))
	})
	f, _ := testFetcher(cfg)
	doc, err := f.Get(context.Background(), s.URL, 0)
	if err == nil || doc.Body != nil {
		t.Fatalf("Get = %d bytes, %v; want error and no body", len(doc.Body), err)
	}
}

func TestFetchRedirect(t *testing.T) {
	other, otherN := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("other")) })
	s, _ := server(t, func(_ int, w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/away":
			http.Redirect(w, r, other.URL+"/x", http.StatusFound)
		case r.URL.Path == "/moved":
			http.Redirect(w, r, "/final", http.StatusFound)
		case r.URL.Path == "/final":
			_, _ = w.Write([]byte("final"))
		case strings.HasPrefix(r.URL.Path, "/loop"):
			http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
		}
	})
	f, _ := testFetcher(DefaultFetchConfig())
	ctx := context.Background()
	if _, err := f.Get(ctx, s.URL+"/away", 0); err == nil || otherN.Load() != 0 {
		t.Fatalf("redirect to other host: err = %v, other requests = %d; want error, 0", err, otherN.Load())
	}
	if doc, err := f.Get(ctx, s.URL+"/moved", 0); err != nil || string(doc.Body) != "final" {
		t.Fatalf("same-host redirect = %q, %v", doc.Body, err)
	}
	if _, err := f.Get(ctx, s.URL+"/loop", 0); err == nil {
		t.Fatal("4 redirects: want error")
	}
}

func TestFetchNonUTF8(t *testing.T) {
	s, _ := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte{'a', 0xff}) })
	f, _ := testFetcher(DefaultFetchConfig())
	if _, err := f.Get(context.Background(), s.URL, 0); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("err = %v, want UTF-8 error", err)
	}
}

// Тело в windows-1251 с этой кодировкой в Content-Type перекодируется в UTF-8;
// с другой кодировкой или без неё — ошибка, как раньше.
func TestFetchWindows1251(t *testing.T) {
	ct := "text/html; charset=cp-1251"
	s, _ := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write([]byte{0xcc, 0xee, 0xf1, 0xea, 0xe2, 0xe0}) // «Москва»
	})
	f, _ := testFetcher(DefaultFetchConfig())
	doc, err := f.Get(context.Background(), s.URL, 0)
	if err != nil || string(doc.Body) != "Москва" {
		t.Fatalf("Get = %q, %v; want «Москва»", doc.Body, err)
	}
	for _, ct = range []string{"text/html; charset=koi8-r", "text/html", "не тип"} {
		if _, err := f.Get(context.Background(), s.URL, 0); err == nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Errorf("%q: err = %v, want UTF-8 error", ct, err)
		}
	}
}

func TestFetchHostGate(t *testing.T) {
	s, _ := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	f, c := testFetcher(DefaultFetchConfig())
	ctx := context.Background()
	if _, err := f.Get(ctx, s.URL+"/a", 40*time.Second); err != nil {
		t.Fatal(err)
	}
	if len(c.waits) != 0 {
		t.Fatalf("first request waited %v", c.waits)
	}
	if _, err := f.Get(ctx, s.URL+"/b", 40*time.Second); err != nil {
		t.Fatal(err)
	}
	if len(c.waits) != 1 || c.waits[0] != 40*time.Second {
		t.Fatalf("waits = %v, want [40s]", c.waits)
	}
}

func TestFetchCancel(t *testing.T) {
	s, n := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	cfg := DefaultFetchConfig()
	cfg.UserAgent = testUA
	f := NewFetcher(cfg) // настоящие часы и сон
	if _, err := f.Get(context.Background(), s.URL, 40*time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	began := time.Now()
	_, err := f.Get(ctx, s.URL, 40*time.Second)
	if !errors.Is(err, context.Canceled) || time.Since(began) > 5*time.Second || n.Load() != 1 {
		t.Fatalf("err = %v after %v, requests = %d; want context.Canceled at once, 1 request", err, time.Since(began), n.Load())
	}
}

func TestFetchNoUserAgent(t *testing.T) {
	s, n := server(t, func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	f := NewFetcher(DefaultFetchConfig())
	if _, err := f.Get(context.Background(), s.URL, 0); err == nil || n.Load() != 0 {
		t.Fatalf("err = %v, requests = %d; want error before any request", err, n.Load())
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		v    string
		want time.Duration
		ok   bool
	}{
		{"7", 7 * time.Second, true},
		{now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second, true},
		{"abc", 0, false},
		// LO-02: переполнение секунд → предел, не отрицательное значение.
		{"10000000000", time.Duration(math.MaxInt64).Truncate(time.Second), true},
		{"", 0, false},
	} {
		h := http.Header{}
		if tc.v != "" {
			h.Set("Retry-After", tc.v)
		}
		got, ok := retryAfter(h, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("retryAfter(%q) = %v, %t; want %v, %t", tc.v, got, ok, tc.want, tc.ok)
		}
	}
}
