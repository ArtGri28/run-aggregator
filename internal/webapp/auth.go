// Package webapp — Mini App «Где старт?»: проверка данных Telegram Web App и
// (позже) API для фронтенда. Этот файл — только проверка initData: без
// бота, базы и net/http; HTTP-обвязка — в middleware.go.
package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// maxInitData — предел длины строки initData до любого разбора.
	maxInitData = 4096
	// authWindow — срок годности auth_date: initData обновляется при каждом
	// открытии Mini App; окно уточняется решением владельца (этап 0).
	authWindow = 24 * time.Hour
	// authSkew — допустимое опережение auth_date (расхождение часов).
	authSkew = 5 * time.Minute
	// authScheme — схема заголовка Authorization.
	authScheme = "tma"
)

// ErrAuth — любая ошибка проверки. Причина наружу не различается: клиент
// получает один и тот же отказ, в лог идёт только код.
var ErrAuth = errors.New("webapp: initData не прошла проверку")

// authError — причина отказа для лога; текстом наружу не уходит.
type authError struct{ code string }

func (e *authError) Error() string { return ErrAuth.Error() + ": " + e.code }
func (e *authError) Unwrap() error { return ErrAuth }

// SecretFromToken — производный ключ HMAC_SHA256(key="WebAppData", msg=token).
// Контейнеру webapp достаточно этого ключа; сам токен бота ему не нужен.
func SecretFromToken(token string) []byte {
	m := hmac.New(sha256.New, []byte("WebAppData"))
	m.Write([]byte(token))
	return m.Sum(nil)
}

// Verify проверяет initData по документации Telegram «Validating data received
// via the Mini App» и возвращает Telegram ID пользователя. Порядок: длина →
// разбор → hash → HMAC в константное время → auth_date → user.id. Из user
// берётся только id; остальное не читается и не возвращается.
func Verify(initData string, secret []byte, now time.Time) (int64, error) {
	if len(initData) == 0 || len(initData) > maxInitData {
		return 0, &authError{"length"}
	}
	values, err := url.ParseQuery(initData)
	if err != nil {
		return 0, &authError{"parse"}
	}
	pairs := make([]string, 0, len(values))
	var gotHash string
	for k, v := range values {
		if len(v) != 1 {
			return 0, &authError{"duplicate"}
		}
		if k == "hash" {
			gotHash = v[0]
			continue
		}
		pairs = append(pairs, k+"="+v[0])
	}
	want, err := hex.DecodeString(gotHash)
	if err != nil || len(want) != sha256.Size {
		return 0, &authError{"hash"}
	}
	sort.Strings(pairs)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(strings.Join(pairs, "\n")))
	if subtle.ConstantTimeCompare(m.Sum(nil), want) != 1 {
		return 0, &authError{"hmac"}
	}
	sec, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || sec <= 0 {
		return 0, &authError{"auth_date"}
	}
	at := time.Unix(sec, 0)
	if at.After(now.Add(authSkew)) || now.Sub(at) > authWindow {
		return 0, &authError{"expired"}
	}
	var u struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &u); err != nil || u.ID <= 0 {
		return 0, &authError{"user"}
	}
	return u.ID, nil
}
