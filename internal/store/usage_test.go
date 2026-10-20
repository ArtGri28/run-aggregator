package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/migrations"
)

// TestUsage — отметки дней, счётчики, окна сводки, очистка; Down удаляет
// только две таблицы Phase 19.
func TestUsage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	today := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	day := func(ago int) time.Time { return today.AddDate(0, 0, -ago) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// 1 — пришёл 20 дней назад, сегодня в боте и в Mini App, вчера в боте;
	// 2 — пришёл 10 дней назад, был 9 дней назад; 3 — новый, сегодня в Mini App;
	// 4 — 12 дней в окне 30 дней.
	for id, ago := range map[int64]int{1: 20, 2: 10, 3: 0, 4: 40} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO users (tg_id, created_at) VALUES ($1, $2)`, id, now.AddDate(0, 0, -ago)); err != nil {
			t.Fatal(err)
		}
	}
	must(s.MarkDay(ctx, 1, day(0), false))
	must(s.MarkDay(ctx, 1, day(0), true))
	must(s.MarkDay(ctx, 1, day(0), false))
	must(s.MarkDay(ctx, 1, day(1), false))
	must(s.MarkDay(ctx, 2, day(9), false))
	must(s.MarkDay(ctx, 3, day(0), true))
	for i := 2; i < 14; i++ {
		must(s.MarkDay(ctx, 4, day(i), false))
	}
	must(s.MarkDay(ctx, 4, day(100), false))
	if got := scalar[string](t, `SELECT bot::text || app::text FROM user_days WHERE tg_id = 1 AND day = $1`, day(0)); got != "truetrue" {
		t.Errorf("отметка бот+Mini App = %q", got)
	}
	for range 3 {
		must(s.CountAction(ctx, day(0), MetricCardBot))
	}
	must(s.CountAction(ctx, day(6), MetricCardApp))
	must(s.CountAction(ctx, day(7), MetricCardApp))

	a, err := s.Activity(ctx, today, now)
	must(err)
	if !a.Since.Equal(day(100)) || a.Users != 4 || a.New7 != 1 {
		t.Errorf("since/users/new7 = %v %d %d", a.Since, a.Users, a.New7)
	}
	if a.Active != [3]int{2, 3, 4} || a.App != [3]int{2, 2, 2} {
		t.Errorf("active %v app %v", a.Active, a.App)
	}
	if a.Cohort != 2 || a.Returned != 1 {
		t.Errorf("cohort %d returned %d, want 2 и 1", a.Cohort, a.Returned)
	}
	if a.Freq != [4]int{2, 1, 0, 1} {
		t.Errorf("freq %v", a.Freq)
	}
	if a.Actions[MetricCardBot] != 3 || a.Actions[MetricCardApp] != 1 {
		t.Errorf("actions %v", a.Actions)
	}

	n, err := s.PruneUserDays(ctx, day(90))
	if err != nil || n != 1 {
		t.Errorf("prune = %d, %v; want 1", n, err)
	}

	db := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	must(err)
	defer func() {
		if _, err := p.Up(context.Background()); err != nil {
			t.Errorf("restore Up: %v", err)
		}
	}()
	const added = `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename IN ('user_days', 'usage_counters')`
	_, err = p.DownTo(ctx, 11)
	must(err)
	if n := scalar[int64](t, added); n != 0 {
		t.Errorf("после Down таблиц %d, want 0", n)
	}
	if n := scalar[int64](t, `SELECT count(*) FROM users`); n != 4 {
		t.Errorf("после Down пользователей %d, want 4", n)
	}
}
