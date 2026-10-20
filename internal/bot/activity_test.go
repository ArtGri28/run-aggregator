package bot

import (
	"context"
	"strings"
	"testing"
)

// TestActivity — /activity: администратор видит сводку активности, группа
// частоты от 1 до 4 — «меньше 5»; не администратору — ответ неизвестной
// команды; любой апдейт в личном чате отмечает день.
func TestActivity(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	dbExec(t, `INSERT INTO users (tg_id, created_at) SELECT g, $1::timestamptz - interval '20 days' FROM generate_series(100, 109) g`, fixtureNow)
	dbExec(t, `INSERT INTO user_days (day, tg_id, bot) SELECT $1::date - 1, g, true FROM generate_series(100, 105) g`, fixtureNow)
	dbExec(t, `INSERT INTO user_days (day, tg_id, bot) VALUES ($1::date - 2, 106, true), ($1::date - 3, 106, true)`, fixtureNow)
	dbExec(t, `INSERT INTO usage_counters (day, metric, n) VALUES ($1::date, 'card_bot', 5), ($1::date, 'card_app', 2),
		($1::date, 'search_bot', 3), ($1::date, 'fav_app', 1)`, fixtureNow)

	e.b.api.ProcessUpdate(ctx, commandUpdate(2, "/activity"))
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["text"] != "Нажмите /start, чтобы найти старт" {
		t.Fatalf("/activity не-админу: %+v", sends)
	}

	e.api.reset()
	e.b.api.ProcessUpdate(ctx, commandUpdate(1, "/activity"))
	sends = e.api.of("sendMessage")
	if len(sends) != 1 || e.api.total() != 1 {
		t.Fatalf("/activity: ждали одно сообщение, вызовы %+v", e.api.calls)
	}
	noParseMode(t, sends[0])
	matchSnapshot(t, "activity", renderCall(t, sends[0]))
	if strings.Contains(sends[0].Form["text"], "100") {
		t.Errorf("сводка содержит tg_id")
	}
	if got := dbScalar(t, `SELECT string_agg(tg_id::text, ',' ORDER BY tg_id) FROM user_days WHERE day = $1::date`, fixtureNow); got != "1,2" {
		t.Errorf("отмечены сегодня %q, want 1,2", got)
	}
}
