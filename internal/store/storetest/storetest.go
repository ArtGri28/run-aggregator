// Package storetest даёт тестам других пакетов чистую тестовую базу.
// Импортируется только из _test.go; тесты самого store его не используют
// (был бы цикл импортов).
package storetest

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/store"
)

// Open открывает базу из TEST_DATABASE_URL, применяет миграции и очищает
// все таблицы. Без переменной тест пропускается; база, имя которой не
// оканчивается на _test, — ошибка до любой очистки (защита dev-БД).
func Open(t testing.TB) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()

	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(s.Close)

	name, err := s.DatabaseName(ctx)
	if err != nil {
		t.Fatalf("test store: %v", err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("TEST_DATABASE_URL: имя базы должно оканчиваться на _test")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate test store: %v", err)
	}

	// Пул store неэкспортируемый: очищаем отдельным соединением.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("test store: connect: %v", err)
	}
	defer conn.Close(ctx)
	const q = `TRUNCATE TABLE sources, ingest_runs, source_events, events,
		event_distances, dedup_candidates, users, heartbeats RESTART IDENTITY CASCADE`
	if _, err := conn.Exec(ctx, q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return s
}
