package bot

import (
	"context"
	"strings"
	"testing"
)

// TestStats — /stats: администратор видит счётчики анкеты, группы от 1 до 4
// скрыты как «меньше 5»; остальным — ответ неизвестной команды.
func TestStats(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	dbExec(t, `INSERT INTO users (tg_id) SELECT generate_series(100, 109)`)
	dbExec(t, `INSERT INTO user_profiles (tg_id, status, gender, age_group, residence_country_code, residence_city, sports)
		SELECT g, 'completed', 'm', 2, 'RU', 'Секретоград', '{run,trail}' FROM generate_series(100, 105) g`)
	dbExec(t, `INSERT INTO user_profiles (tg_id, status, gender) SELECT g, 'in_progress', 'f' FROM generate_series(106, 107) g`)
	dbExec(t, `INSERT INTO user_profiles (tg_id, status) VALUES (108, 'skipped')`)

	e.b.api.ProcessUpdate(ctx, commandUpdate(2, "/stats"))
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["text"] != "Нажмите /start, чтобы найти старт" {
		t.Fatalf("/stats не-админу: %+v", sends)
	}

	e.api.reset()
	e.b.api.ProcessUpdate(ctx, commandUpdate(1, "/stats"))
	sends = e.api.of("sendMessage")
	if len(sends) != 1 || e.api.total() != 1 {
		t.Fatalf("/stats: ждали одно сообщение, вызовы %+v", e.api.calls)
	}
	noParseMode(t, sends[0])
	matchSnapshot(t, "stats", renderCall(t, sends[0]))
	for _, secret := range []string{"Секретоград", "100", "108"} {
		if strings.Contains(sends[0].Form["text"], secret) {
			t.Errorf("сводка содержит %q", secret)
		}
	}
}
