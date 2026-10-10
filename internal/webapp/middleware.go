package webapp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

type ctxKey struct{}

// TgID — Telegram ID из контекста запроса, который прошёл Middleware.
func TgID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKey{}).(int64)
	return id, ok
}

// Middleware требует заголовок «Authorization: tma <initData>», проверяет его
// функцией Verify и кладёт Telegram ID в контекст. Любой отказ — 401 без
// подробностей; onReject получает код причины для лога (может быть nil).
func Middleware(secret []byte, now func() time.Time, onReject func(code string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme, data, _ := strings.Cut(r.Header.Get("Authorization"), " ")
			var id int64
			err := error(&authError{"scheme"})
			if scheme == authScheme {
				id, err = Verify(data, secret, now())
			}
			if err != nil {
				var ae *authError
				if onReject != nil && errors.As(err, &ae) {
					onReject(ae.code)
				}
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}
