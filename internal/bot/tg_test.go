package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	tgbot "github.com/go-telegram/bot"
)

// shortRetry уменьшает предел ожидания 429 до 2 с до конца теста:
// retry_after 1 (минимальный) ещё повторяется, 120 — уже нет.
func shortRetry(t *testing.T) {
	t.Helper()
	old := maxRetryWait
	maxRetryWait = 2 * time.Second
	t.Cleanup(func() { maxRetryWait = old })
}

// only отвечает на вызовы одного метода по номеру вызова; остальное — успех.
func only(method string, reply func(n int) string) script {
	return func(c call, n int) string {
		if c.Method != method {
			return ""
		}
		return reply(n)
	}
}

var (
	errNotModified = apiErr(400, "Bad Request: message is not modified: specified new message content and reply markup are exactly the same", 0)
	errCantEdit    = apiErr(400, "Bad Request: message can't be edited", 0)
	errBadURL      = apiErr(400, "Bad Request: wrong HTTP URL", 0)
	errBlocked     = apiErr(403, "Forbidden: bot was blocked by the user", 0)
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		kind  errKind
		retry time.Duration
	}{
		{"not modified", errNotModified, kindNotModified, 0},
		{"edit not found", apiErr(400, "Bad Request: message to edit not found", 0), kindBadRequest, 0},
		{"blocked", errBlocked, kindBlocked, 0},
		{"429", apiErr(429, "Too Many Requests: retry after 7", 7), kindRateLimited, 7 * time.Second},
		{"500", apiErr(500, "Internal Server Error", 0), kindOther, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := fakeTelegram(t, func(call, int) string { return tc.body })
			b := newTestBot(t, api, nil)
			_, err := b.api.SendMessage(context.Background(), &tgbot.SendMessageParams{ChatID: 1, Text: "x"})
			if err == nil {
				t.Fatal("SendMessage: want error")
			}
			for _, e := range []error{err, fmt.Errorf("обёрнута: %w", err)} {
				kind, retry := classify(e)
				if kind != tc.kind || retry != tc.retry {
					t.Fatalf("classify(%v) = %v, %v; want %v, %v", e, kind, retry, tc.kind, tc.retry)
				}
			}
		})
	}
	if kind, _ := classify(nil); kind != kindNone {
		t.Fatalf("classify(nil) = %v, want none", kind)
	}
}

func TestSendBlocked(t *testing.T) {
	api := fakeTelegram(t, only("sendMessage", func(int) string { return errBlocked }))
	b := newTestBot(t, api, nil)
	logs := captureLogs(t)

	if err := b.send(context.Background(), 42, screen{text: "секретный текст экрана"}); err == nil {
		t.Fatal("send: want error for blocked bot")
	}
	if n := len(api.of("sendMessage")); n != 1 {
		t.Fatalf("sendMessage = %d, want 1 (no retry)", n)
	}
	if !strings.Contains(logs.String(), "blocked") {
		t.Fatalf("log lacks error kind: %s", logs)
	}
	if strings.Contains(logs.String(), "секретный") {
		t.Fatalf("log has screen text: %s", logs)
	}
}

func TestRateLimitRetry(t *testing.T) {
	shortRetry(t)
	q := cbUpdate(42, 7, "f:s").CallbackQuery
	sc := screen{text: "x"}

	t.Run("один повтор", func(t *testing.T) {
		api := fakeTelegram(t, only("editMessageText", func(n int) string {
			if n == 1 {
				return apiErr(429, "Too Many Requests: retry after 1", 1)
			}
			return ""
		}))
		b := newTestBot(t, api, nil)
		if err := b.show(context.Background(), q, sc); err != nil {
			t.Fatalf("show: %v", err)
		}
		if n := len(api.of("editMessageText")); n != 2 {
			t.Fatalf("editMessageText = %d, want 2", n)
		}
	})

	t.Run("retry_after больше предела — без ожидания и повтора", func(t *testing.T) {
		api := fakeTelegram(t, only("editMessageText", func(n int) string {
			if n == 1 {
				return apiErr(429, "Too Many Requests: retry after 120", 120)
			}
			return ""
		}))
		b := newTestBot(t, api, nil)
		start := time.Now()
		if err := b.show(context.Background(), q, sc); err == nil {
			t.Fatal("show: want 429 error")
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("show waited %v with maxRetryWait %v", d, maxRetryWait)
		}
		if e, m := len(api.of("editMessageText")), len(api.of("sendMessage")); e != 1 || m != 0 {
			t.Fatalf("editMessageText = %d, sendMessage = %d; want 1 and 0", e, m)
		}
	})

	t.Run("два 429 подряд", func(t *testing.T) {
		api := fakeTelegram(t, only("editMessageText", func(int) string {
			return apiErr(429, "Too Many Requests: retry after 1", 1)
		}))
		b := newTestBot(t, api, nil)
		if err := b.show(context.Background(), q, sc); err == nil {
			t.Fatal("show: want error after second 429")
		}
		if n := len(api.of("editMessageText")); n != 2 {
			t.Fatalf("editMessageText = %d, want 2 (no third)", n)
		}
		if n := len(api.of("sendMessage")); n != 0 {
			t.Fatalf("sendMessage = %d, want 0", n)
		}
	})

	t.Run("отмена контекста прерывает ожидание", func(t *testing.T) {
		maxRetryWait = time.Minute
		api := fakeTelegram(t, only("editMessageText", func(int) string {
			return apiErr(429, "Too Many Requests: retry after 60", 60)
		}))
		b := newTestBot(t, api, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err := b.show(ctx, q, sc); err == nil {
			t.Fatal("show: want error")
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("show waited %v despite cancelled ctx", d)
		}
	})
}

// TestCallbackRetryLimit — LO-03: внутри b.cb предел ожидания 429 —
// callbackRetryWait, ответ на callback не ждёт долгий retry_after.
func TestCallbackRetryLimit(t *testing.T) {
	h := func(b *Bot) tgbot.HandlerFunc {
		return b.cb(func(ctx context.Context, q *models.CallbackQuery, _ callback) string {
			_ = b.show(ctx, q, screen{text: "x"})
			return "n"
		})
	}
	cases := []struct {
		name       string
		retryAfter int
		edits      int
		maxDur     time.Duration
	}{
		{"долгий 429 — без ожидания", 10, 1, 2 * time.Second},
		{"короткий 429 — повтор", 1, 2, 5 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := fakeTelegram(t, only("editMessageText", func(n int) string {
				if n == 1 {
					return apiErr(429, fmt.Sprintf("Too Many Requests: retry after %d", c.retryAfter), c.retryAfter)
				}
				return ""
			}))
			b := newTestBot(t, api, nil)
			start := time.Now()
			h(b)(context.Background(), b.api, cbUpdate(42, 7, "p:1:v1"))
			if d := time.Since(start); d > c.maxDur {
				t.Fatalf("callback took %v, want < %v", d, c.maxDur)
			}
			if n := len(api.of("editMessageText")); n != c.edits {
				t.Fatalf("editMessageText = %d, want %d", n, c.edits)
			}
			ans := api.of("answerCallbackQuery")
			if len(ans) != 1 || ans[0].Form["text"] != "n" {
				t.Fatalf("answerCallbackQuery = %+v, want one with text «n»", ans)
			}
		})
	}
}

func TestEditNotModified(t *testing.T) {
	api := fakeTelegram(t, only("editMessageText", func(int) string { return errNotModified }))
	b := newTestBot(t, api, nil)
	if err := b.show(context.Background(), cbUpdate(42, 7, "f:s").CallbackQuery, screen{text: "x"}); err != nil {
		t.Fatalf("show: %v", err)
	}
	if n := len(api.of("sendMessage")); n != 0 {
		t.Fatalf("sendMessage = %d, want 0", n)
	}
}

func TestEditFallbackToSend(t *testing.T) {
	api := fakeTelegram(t, only("editMessageText", func(int) string { return errCantEdit }))
	b := newTestBot(t, api, nil)
	if err := b.show(context.Background(), cbUpdate(42, 7, "f:s").CallbackQuery, screen{text: "текст экрана"}); err != nil {
		t.Fatalf("show: %v", err)
	}
	sends := api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["chat_id"] != "42" || sends[0].Form["text"] != "текст экрана" {
		t.Fatalf("sendMessage = %+v, want one to chat 42 with the same text", sends)
	}
}

// urlScreen — экран с URL-кнопкой и кнопкой callback.
func urlScreen() screen {
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "Регистрация", URL: "https://example.test/reg"}},
		{{Text: "К списку", CallbackData: "p:1:v1"}},
	}}
	return screen{text: "карточка", kb: kb}
}

func hasURLButton(t *testing.T, c call) bool {
	t.Helper()
	for _, bt := range c.buttons(t) {
		if bt.URL != "" {
			return true
		}
	}
	return false
}

func TestURLButtonFallback(t *testing.T) {
	q := cbUpdate(42, 7, "e:1:1:v1").CallbackQuery

	t.Run("send: повтор без URL-кнопок", func(t *testing.T) {
		api := fakeTelegram(t, only("sendMessage", func(n int) string {
			if n == 1 {
				return errBadURL
			}
			return ""
		}))
		b := newTestBot(t, api, nil)
		if err := b.send(context.Background(), 42, urlScreen()); err != nil {
			t.Fatalf("send: %v", err)
		}
		sends := api.of("sendMessage")
		if len(sends) != 2 || !hasURLButton(t, sends[0]) || hasURLButton(t, sends[1]) || sends[1].button(t, "К списку") != "p:1:v1" {
			t.Fatalf("sendMessage = %+v, want URL button dropped on the second call, callback kept", sends)
		}
	})

	t.Run("send: повтор тоже 400", func(t *testing.T) {
		api := fakeTelegram(t, only("sendMessage", func(int) string { return errBadURL }))
		b := newTestBot(t, api, nil)
		if err := b.send(context.Background(), 42, urlScreen()); err == nil {
			t.Fatal("send: want error")
		}
		if n := len(api.of("sendMessage")); n != 2 {
			t.Fatalf("sendMessage = %d, want 2 (no third)", n)
		}
	})

	t.Run("show: правка без URL-кнопок", func(t *testing.T) {
		api := fakeTelegram(t, only("editMessageText", func(n int) string {
			if n == 1 {
				return errBadURL
			}
			return ""
		}))
		b := newTestBot(t, api, nil)
		if err := b.show(context.Background(), q, urlScreen()); err != nil {
			t.Fatalf("show: %v", err)
		}
		edits := api.of("editMessageText")
		if len(edits) != 2 || hasURLButton(t, edits[1]) || edits[1].button(t, "К списку") != "p:1:v1" || len(api.of("sendMessage")) != 0 {
			t.Fatalf("calls = %+v, want two edits, URL button dropped in the second", api.calls)
		}
	})
}
