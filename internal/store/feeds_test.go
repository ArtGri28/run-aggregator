package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/migrations"
)

// TestCalendarStore — настройки и ссылки календаря: замена ссылки, поиск по
// хешу, отключение; проверки схемы; Down удаляет только две таблицы Phase 21.
func TestCalendarStore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if v, err := s.Setting(ctx, SettingBotUsername); v != "" || err != nil {
		t.Fatalf("пустая настройка: %q, %v", v, err)
	}
	must(s.SetSetting(ctx, SettingBotUsername, "a_bot"))
	must(s.SetSetting(ctx, SettingBotUsername, "b_bot"))
	if v, _ := s.Setting(ctx, SettingBotUsername); v != "b_bot" {
		t.Errorf("настройка %q, want b_bot", v)
	}
	if s.SetSetting(ctx, "Bad-Key", "x") == nil {
		t.Error("недопустимый ключ принят")
	}

	h1, h2 := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	if s.SetFeed(ctx, 42, []byte{1, 2}, now) == nil {
		t.Error("хеш не той длины принят")
	}
	must(s.SetFeed(ctx, 42, h1, now))
	if has, _ := s.HasFeed(ctx, 42); !has {
		t.Error("ссылки нет после выдачи")
	}
	must(s.SetFeed(ctx, 42, h2, now))
	if _, ok, _ := s.FeedOwner(ctx, h1); ok {
		t.Error("старая ссылка работает после замены")
	}
	if id, ok, err := s.FeedOwner(ctx, h2); !ok || id != 42 || err != nil {
		t.Errorf("новая ссылка: %d %v %v", id, ok, err)
	}
	must(s.DeleteFeed(ctx, 42))
	must(s.DeleteFeed(ctx, 42))
	if _, ok, _ := s.FeedOwner(ctx, h2); ok {
		t.Error("ссылка работает после отключения")
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
	_, err = p.DownTo(ctx, 13)
	must(err)
	if n := scalar[int64](t, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename IN ('app_settings', 'calendar_feeds', 'subscriptions')`); n != 1 {
		t.Errorf("после Down таблиц %d, want 1 (subscriptions остаётся)", n)
	}
}
