// Package storetest даёт тестам других пакетов чистую тестовую базу.
// Импортируется только из _test.go; тесты самого store его не используют
// (был бы цикл импортов).
package storetest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
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
		event_distances, dedup_candidates, user_profiles, users, heartbeats, source_alerts,
		support_messages, support_awaiting, error_reports, start_suggestions, suggest_awaiting,
		user_days, usage_counters, subscriptions, app_settings, calendar_feeds, broadcasts
		RESTART IDENTITY CASCADE`
	if _, err := conn.Exec(ctx, q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return s
}

// HideEvent снимает событие с публикации (is_listed = false).
func HideEvent(t testing.TB, id int64) {
	t.Helper()
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", id)
}

// MergeEvent помечает событие id слитым в into.
func MergeEvent(t testing.TB, id, into int64) {
	t.Helper()
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", into, id)
}

// SetStatus меняет статус события.
func SetStatus(t testing.TB, id int64, status event.Status) {
	t.Helper()
	exec(t, "UPDATE events SET status = $1 WHERE id = $2", string(status), id)
}

// SetSport меняет вид спорта события.
func SetSport(t testing.TB, id int64, sport event.Sport) {
	t.Helper()
	exec(t, "UPDATE events SET sport = $1 WHERE id = $2", string(sport), id)
}

// EnableSport включает вид спорта в справочнике event.Sports на время теста
// и возвращает прежнее значение по его завершении. Справочник общий на
// процесс: тест с EnableSport не запускается параллельно с другими.
func EnableSport(t testing.TB, code event.Sport) {
	t.Helper()
	setSportEnabled(t, code, true)
}

// DisableSport выключает вид спорта в справочнике event.Sports на время теста
// и возвращает прежнее значение по его завершении — как EnableSport.
func DisableSport(t testing.TB, code event.Sport) {
	t.Helper()
	setSportEnabled(t, code, false)
}

func setSportEnabled(t testing.TB, code event.Sport, on bool) {
	t.Helper()
	for i := range event.Sports {
		if event.Sports[i].Code == code {
			was := event.Sports[i].Enabled
			event.Sports[i].Enabled = on
			t.Cleanup(func() { event.Sports[i].Enabled = was })
			return
		}
	}
	t.Fatalf("storetest: вида спорта %q нет в справочнике", code)
}

// AgeHeartbeat сдвигает отметку компоненты назад на age.
func AgeHeartbeat(t testing.TB, component string, age time.Duration) {
	t.Helper()
	exec(t, "UPDATE heartbeats SET beat_at = now() - make_interval(secs => $2) WHERE component = $1", component, age.Seconds())
}

// exec выполняет запрос отдельным соединением с той же проверкой базы,
// что и Open: правки идут только в базу с именем на _test.
func exec(t testing.TB, q string, args ...any) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("storetest: connect: %v", err)
	}
	defer conn.Close(ctx)
	var name string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil || !strings.HasSuffix(name, "_test") {
		t.Fatal("TEST_DATABASE_URL: имя базы должно оканчиваться на _test")
	}
	if _, err := conn.Exec(ctx, q, args...); err != nil {
		t.Fatalf("storetest: %v", err)
	}
}
