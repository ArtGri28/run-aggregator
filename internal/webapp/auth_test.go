package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
)

// testToken — выдуманный токен в формате Telegram; настоящим не является.
const testToken = "123456:TEST-token_for-webapp-tests"

var testNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// sign собирает initData так, как это делает Telegram: пары, отсортированные
// по ключу, через \n, HMAC производным ключом, hash — hex.
func sign(t *testing.T, token string, fields map[string]string) string {
	t.Helper()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var check []string
	for _, k := range keys {
		check = append(check, k+"="+fields[k])
	}
	m := hmac.New(sha256.New, SecretFromToken(token))
	m.Write([]byte(strings.Join(check, "\n")))
	v := url.Values{}
	for k, s := range fields {
		v.Set(k, s)
	}
	v.Set("hash", hex.EncodeToString(m.Sum(nil)))
	return v.Encode()
}

func fields(authAt time.Time, userJSON string) map[string]string {
	return map[string]string{
		"auth_date": strconv.FormatInt(authAt.Unix(), 10),
		"query_id":  "AAHdF6IQAAAAAN0XohDhrOrc",
		"user":      userJSON,
	}
}

const userJSON = `{"id":42,"first_name":"Имя","language_code":"ru","allows_write_to_pm":true}`

func TestVerify(t *testing.T) {
	secret := SecretFromToken(testToken)
	good := sign(t, testToken, fields(testNow.Add(-time.Hour), userJSON))

	id, err := Verify(good, secret, testNow)
	if err != nil || id != 42 {
		t.Fatalf("годная строка: id=%d err=%v", id, err)
	}

	// Сверка с библиотечной проверкой на том же векторе: обе принимают.
	vals, _ := url.ParseQuery(good)
	if u, ok := tgbot.ValidateWebappRequest(vals, testToken); !ok || u.ID != 42 {
		t.Fatalf("библиотечная проверка не приняла вектор теста: ok=%v", ok)
	}

	cases := map[string]struct {
		data string
		code string
	}{
		"пусто":          {"", "length"},
		"слишком длинно": {good + "&x=" + strings.Repeat("a", maxInitData), "length"},
		"изменено поле":  {strings.Replace(good, "auth_date=", "auth_date=1", 1), "hmac"},
		"чужой ключ":     {sign(t, "999:other", fields(testNow, userJSON)), "hmac"},
		"повтор ключа":   {good + "&query_id=again", "duplicate"},
		"нет hash":       {"auth_date=1&user=%7B%7D", "hash"},
		"короткий hash":  {"auth_date=1&hash=abcd", "hash"},
		"просрочено":     {sign(t, testToken, fields(testNow.Add(-authWindow-time.Second), userJSON)), "expired"},
		"из будущего":    {sign(t, testToken, fields(testNow.Add(authSkew+time.Second), userJSON)), "expired"},
		"нет auth_date":  {sign(t, testToken, map[string]string{"user": userJSON}), "auth_date"},
		"id нуль":        {sign(t, testToken, fields(testNow, `{"id":0}`)), "user"},
		"id минус":       {sign(t, testToken, fields(testNow, `{"id":-5}`)), "user"},
		"user не JSON":   {sign(t, testToken, fields(testNow, `{"id":`)), "user"},
		"нет user":       {sign(t, testToken, map[string]string{"auth_date": strconv.FormatInt(testNow.Unix(), 10)}), "user"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			id, err := Verify(c.data, secret, testNow)
			var ae *authError
			if !errors.As(err, &ae) || !errors.Is(err, ErrAuth) {
				t.Fatalf("ждали отказ, получили id=%d err=%v", id, err)
			}
			if ae.code != c.code {
				t.Fatalf("код %q, ждали %q", ae.code, c.code)
			}
			if id != 0 {
				t.Fatalf("при отказе id должен быть 0, получили %d", id)
			}
		})
	}

	// Границы окна: ровно на краю — принимается.
	for _, at := range []time.Time{testNow.Add(-authWindow), testNow.Add(authSkew)} {
		if _, err := Verify(sign(t, testToken, fields(at, userJSON)), secret, testNow); err != nil {
			t.Fatalf("граница %v: %v", at, err)
		}
	}
}

func TestMiddleware(t *testing.T) {
	secret := SecretFromToken(testToken)
	good := sign(t, testToken, fields(testNow, userJSON))
	var codes []string
	h := Middleware(secret, func() time.Time { return testNow }, func(c string) { codes = append(codes, c) })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := TgID(r.Context())
			if !ok {
				t.Error("в контексте нет tg_id")
			}
			w.Write([]byte(strconv.FormatInt(id, 10)))
		}))

	call := func(auth string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := call("tma " + good); rec.Code != http.StatusOK || rec.Body.String() != "42" {
		t.Fatalf("годный заголовок: code=%d body=%q", rec.Code, rec.Body.String())
	}
	for _, auth := range []string{"", "Bearer " + good, "tma", "tma " + good + "&z=1"} {
		rec := call(auth)
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != `{"error":"unauthorized"}` {
			t.Fatalf("%q: code=%d body=%q", auth, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "hmac") || strings.Contains(rec.Body.String(), "scheme") {
			t.Fatalf("причина отказа ушла клиенту: %q", rec.Body.String())
		}
	}
	// Пустой заголовок и чужая схема — "scheme"; пустые данные — "length"; подмена — "hmac".
	want := []string{"scheme", "scheme", "length", "hmac"}
	if strings.Join(codes, ",") != strings.Join(want, ",") {
		t.Fatalf("коды для лога %v, ждали %v", codes, want)
	}
	if _, ok := TgID(httptest.NewRequest(http.MethodGet, "/", nil).Context()); ok {
		t.Fatal("tg_id не должен быть в контексте без middleware")
	}
}
