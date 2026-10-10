package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/event"
	"aggregator/migrations"
)

// TestMigration00009 — закрытый список event_type заменён проверкой формата
// (D-11); раздел Down возвращает список как NOT VALID и строк не удаляет
// (13-SCHEMA.md, раздел «Откат»).
func TestMigration00009(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	check := func() string {
		return scalar[string](t, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conrelid = 'events'::regclass AND conname = 'events_event_type_check'`)
	}
	if got := check(); got != `CHECK ((event_type ~ '^[a-z][a-z_]{1,31}$'::text))` {
		t.Fatalf("events_event_type_check = %s", got)
	}
	e, ski := sampleEvent(), sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	ski.Sport, ski.Type = event.SportSkiing, event.TypeSki
	if _, err := s.SaveEvent(ctx, ski); err != nil {
		t.Fatalf("запись дисциплины ski: %v", err)
	}
	const set = "UPDATE events SET event_type = $2 WHERE id = $1"
	for _, typ := range []string{"road", "trail", "cross", "track", "obstacle", "other", "ski", "rollerski"} {
		if _, err := s.pool.Exec(ctx, set, e.ID, typ); err != nil {
			t.Errorf("event_type = %q отвергнут: %v", typ, err)
		}
	}
	for _, bad := range []string{"", "Ski", "ROAD", "s", "roller ski", "ski2"} {
		if _, err := s.pool.Exec(ctx, set, e.ID, bad); err == nil {
			t.Errorf("event_type = %q принят базой", bad)
		}
	}
	exec(t, set, e.ID, "road")

	db := stdlib.OpenDB(*s.pool.Config().ConnConfig)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := p.Up(context.Background()); err != nil {
			t.Errorf("restore Up: %v", err)
		}
	}()
	const types = "SELECT string_agg(event_type, ',' ORDER BY id) FROM events"
	if _, err := p.DownTo(ctx, 8); err != nil {
		t.Fatalf("DownTo(8): %v", err)
	}
	// Down: прежний список, NOT VALID; лыжная строка на месте.
	if got := check(); !strings.Contains(got, "'obstacle'") || !strings.HasSuffix(got, "NOT VALID") {
		t.Fatalf("после Down events_event_type_check = %s", got)
	}
	if got := scalar[string](t, types); got != "road,ski" {
		t.Fatalf("после Down event_type = %q, want road,ski", got)
	}
	// Список проверяется у новых и изменяемых строк: лыжную строку нельзя
	// изменить, не сменив ей дисциплину.
	if _, err := s.pool.Exec(ctx, set, e.ID, "ski"); err == nil {
		t.Error("после Down значение ski принято у изменяемой строки")
	}
	if _, err := s.pool.Exec(ctx, "UPDATE events SET title = title || '!' WHERE id = $1", ski.ID); err == nil {
		t.Error("после Down лыжная строка изменена без смены дисциплины")
	}
	exec(t, set, e.ID, "trail")

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up при лыжной строке: %v", err)
	}
	if got := scalar[string](t, types); got != "trail,ski" {
		t.Fatalf("после Up event_type = %q, want trail,ski", got)
	}
	if got := check(); strings.Contains(got, "NOT VALID") || !strings.Contains(got, "event_type ~") {
		t.Fatalf("после Up events_event_type_check = %s", got)
	}
}
