package webapp

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/store/storetest"
)

// TestSubscriptionAPI — подписка из Mini App: без вида спорта — все виды
// спорта; совпадение с текущими фильтрами; общая с ботом копия; отписка;
// неверные фильтры — 400.
func TestSubscriptionAPI(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	r := newRig(t, s)
	const user = 42
	dbExec(t, `INSERT INTO users (tg_id) VALUES (42) ON CONFLICT DO NOTHING`)
	var sub subJSON
	r.getJSON(user, "/api/v1/subscription?country=RU", http.StatusOK, &sub)
	if sub.On {
		t.Fatalf("до подписки: %+v", sub)
	}
	if resp, _ := r.do(http.MethodPut, "/api/v1/subscription?country=RU", r.auth(user), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT: %d", resp.StatusCode)
	}
	if got := dbScalar(t, `SELECT filters::text FROM subscriptions WHERE tg_id = 42`); got != `{"all": true, "country": "RU"}` {
		t.Errorf("копия «все виды спорта»: %s", got)
	}
	r.getJSON(user, "/api/v1/subscription?country=RU", http.StatusOK, &sub)
	if !sub.On || !sub.Same || sub.Filters == nil || sub.Filters.Sport != "" || sub.Filters.Country != "RU" {
		t.Errorf("подписка: %+v %+v", sub, sub.Filters)
	}
	sub = subJSON{} // поля с omitempty не перезаписываются при разборе
	r.getJSON(user, "/api/v1/subscription?country=RU&sport=running&dist=1", http.StatusOK, &sub)
	if sub.Same {
		t.Error("другие фильтры — same")
	}
	r.do(http.MethodPut, "/api/v1/subscription?country=RU&sport=running&dist=1&type=trail", r.auth(user), "")
	if got := dbScalar(t, `SELECT filters::text FROM subscriptions WHERE tg_id = 42`); got != `{"type": "trail", "dist2": [1], "country": "RU"}` {
		t.Errorf("копия бега: %s", got)
	}
	for _, bad := range []string{"sport=chess", "dist=1", "sport=running&dist=99", "country=*&city=moskva", "type=trail"} {
		if resp, _ := r.do(http.MethodPut, "/api/v1/subscription?"+bad, r.auth(user), ""); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, resp.StatusCode)
		}
	}
	if resp, _ := r.do(http.MethodDelete, "/api/v1/subscription", r.auth(user), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: %d", resp.StatusCode)
	}
	if n := dbCount(t, `SELECT count(*) FROM subscriptions`); n != 0 {
		t.Errorf("после отписки строк %d", n)
	}
}

func dbScalar(t *testing.T, q string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var v string
	if err := conn.QueryRow(ctx, q).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
