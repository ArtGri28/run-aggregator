package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/event"
	"aggregator/migrations"
)

// TestSubscriptions — подписка: включение и замена копии, граница «новых»
// (после since, не позже until), одна строка очереди на неделю, отписка; Down
// удаляет только таблицу подписок.
func TestSubscriptions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	at := func(day int) time.Time { return time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO users (tg_id) VALUES (42)`); err != nil {
		t.Fatal(err)
	}

	if _, _, ok, err := s.Subscription(ctx, 42); ok || err != nil {
		t.Fatalf("до подписки: ok=%v err=%v", ok, err)
	}
	must(s.Subscribe(ctx, 42, []byte(`{"city":"tula"}`), at(1)))
	must(s.MarkDigestSent(ctx, 42, at(5)))
	must(s.Subscribe(ctx, 42, []byte(`{}`), at(10)))
	raw, since, ok, err := s.Subscription(ctx, 42)
	if err != nil || !ok || string(raw) != `{}` || !since.Equal(at(10)) {
		t.Fatalf("замена копии: %s %v %v %v", raw, since, ok, err)
	}

	e := sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	f := event.Filter{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	for _, c := range []struct {
		created time.Time
		want    int
	}{{at(10), 0}, {at(11), 1}, {at(12), 1}, {at(13), 0}} {
		if _, err := s.pool.Exec(ctx, `UPDATE events SET created_at = $1`, c.created); err != nil {
			t.Fatal(err)
		}
		items, total, err := s.NewEvents(ctx, f, at(10), at(12), 10)
		if err != nil || total != c.want || len(items) != c.want {
			t.Errorf("создано %v: %d/%d, %v; want %d", c.created, len(items), total, err, c.want)
		}
	}

	for range 2 {
		if _, err := s.EnqueueDigests(ctx, "2026-10-19", at(19), at(19)); err != nil {
			t.Fatal(err)
		}
	}
	if n := scalar[int64](t, `SELECT count(*) FROM outbox WHERE kind = 'digest' AND dedup_key = 'digest:2026-10-19'`); n != 1 {
		t.Errorf("строк недели %d, want 1", n)
	}
	if n, _ := s.EnqueueDigests(ctx, "2026-10-12", at(9), at(19)); n != 0 {
		t.Errorf("подписка позже момента недели получила строку: %d", n)
	}

	must(s.Unsubscribe(ctx, 42))
	must(s.Unsubscribe(ctx, 42))
	if _, _, ok, _ := s.Subscription(ctx, 42); ok {
		t.Error("после отписки подписка есть")
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
	_, err = p.DownTo(ctx, 12)
	must(err)
	if n := scalar[int64](t, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename IN ('subscriptions', 'user_days')`); n != 1 {
		t.Errorf("после Down таблиц %d, want 1 (user_days остаётся)", n)
	}
}
