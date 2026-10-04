package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// withKey — fn для UpdateFilters: добавляет ключ в объект фильтров.
func withKey(key, val string, pause time.Duration) func([]byte) ([]byte, error) {
	return func(cur []byte) ([]byte, error) {
		m := map[string]any{}
		if err := json.Unmarshal(cur, &m); err != nil {
			return nil, err
		}
		time.Sleep(pause) // окно гонки: без блокировки строки оба читают старое значение
		m[key] = val
		return json.Marshal(m)
	}
}

func TestUserFilters(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	raw, v, err := s.Filters(ctx, 42)
	if err != nil || string(raw) != "{}" || v != 1 {
		t.Fatalf("Filters (first) = %s, %d, %v; want {}, 1", raw, v, err)
	}
	if _, v, _ = s.Filters(ctx, 42); v != 1 {
		t.Fatalf("Filters (repeat) v = %d, want 1", v)
	}

	raw, v, err = s.UpdateFilters(ctx, 42, withKey("type", "road", 0))
	if err != nil || v != 2 || string(raw) != `{"type": "road"}` {
		t.Fatalf("UpdateFilters (change) = %s, %d, %v; want {\"type\": \"road\"}, 2", raw, v, err)
	}
	// То же значение (и другая запись того же JSON) версию не меняет.
	same := func([]byte) ([]byte, error) { return []byte(`{ "type":"road" }`), nil }
	if _, v, err = s.UpdateFilters(ctx, 42, same); err != nil || v != 2 {
		t.Fatalf("UpdateFilters (same) v = %d, %v; want 2", v, err)
	}

	// Ошибка fn отменяет изменение.
	boom := errors.New("boom")
	_, _, err = s.UpdateFilters(ctx, 42, func([]byte) ([]byte, error) { return []byte(`{"type":"trail"}`), boom })
	if !errors.Is(err, boom) {
		t.Fatalf("UpdateFilters (fn error) err = %v, want boom", err)
	}
	if raw, v, _ = s.Filters(ctx, 42); v != 2 || string(raw) != `{"type": "road"}` {
		t.Fatalf("after failed update: %s, %d; want unchanged", raw, v)
	}

	// Новый пользователь создаётся и через UpdateFilters.
	if _, v, err = s.UpdateFilters(ctx, 43, withKey("city", "moscow", 0)); err != nil || v != 2 {
		t.Fatalf("UpdateFilters (new user) v = %d, %v; want 2", v, err)
	}
}

func TestUserFiltersConcurrent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, _, err := s.Filters(ctx, 7); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, kv := range [][2]string{{"city", "moscow"}, {"type", "road"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.UpdateFilters(ctx, 7, withKey(kv[0], kv[1], 100*time.Millisecond)); err != nil {
				t.Errorf("UpdateFilters(%s): %v", kv[0], err)
			}
		}()
	}
	wg.Wait()

	raw, v, err := s.Filters(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"city": "moscow", "type": "road"}; !reflect.DeepEqual(got, want) || v != 3 {
		t.Fatalf("after concurrent updates: %v, v=%d; want %v, v=3", got, v, want)
	}
}

// BOT-08: о пользователе хранятся только tg_id, настройки и метки времени.
func TestUsersColumns(t *testing.T) {
	testStore(t)
	rows, err := testStorePkg.pool.Query(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'users' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	want := []string{"tg_id", "filters", "filters_v", "created_at", "last_seen_at"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users columns = %v, want %v", got, want)
	}
}

// userRow — строка users целиком (все колонки) для сравнения до/после.
func userRow(t *testing.T, tgID int64) string {
	t.Helper()
	return scalar[string](t, `SELECT COALESCE((SELECT to_jsonb(u)::text FROM users u WHERE tg_id = $1), '')`, tgID)
}

// TestUpdateFiltersAt — Phase 8, контракт (раздел 5): изменение применяется
// только при совпавшей версии; иначе ErrStaleFilters и ни одной записи.
func TestUpdateFiltersAt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Нет пользователя: не создаётся ни чтением, ни устаревшим изменением.
	raw, v, ok, err := s.PeekFilters(ctx, 42)
	if err != nil || ok || v != 0 || string(raw) != "{}" {
		t.Fatalf("PeekFilters (нет пользователя) = %s, %d, %v, %v", raw, v, ok, err)
	}
	if _, _, err := s.UpdateFiltersAt(ctx, 42, 1, withKey("type", "road", 0)); !errors.Is(err, ErrStaleFilters) {
		t.Fatalf("UpdateFiltersAt (нет пользователя) = %v, want ErrStaleFilters", err)
	}
	if row := userRow(t, 42); row != "" {
		t.Fatalf("пользователь создан: %s", row)
	}

	if _, _, err := s.Filters(ctx, 42); err != nil {
		t.Fatal(err)
	}
	before := userRow(t, 42)

	// Чтение без записи и чужая версия: строка не меняется вовсе.
	if _, v, ok, err = s.PeekFilters(ctx, 42); err != nil || !ok || v != 1 {
		t.Fatalf("PeekFilters = %d, %v, %v; want 1, true", v, ok, err)
	}
	if _, _, err := s.UpdateFiltersAt(ctx, 42, 7, withKey("type", "road", 0)); !errors.Is(err, ErrStaleFilters) {
		t.Fatalf("UpdateFiltersAt (чужая версия) = %v, want ErrStaleFilters", err)
	}
	if row := userRow(t, 42); row != before {
		t.Fatalf("устаревшее изменение записало в users:\n%s\n%s", before, row)
	}

	// Совпавшая версия: применяется, версия растёт.
	raw, v, err = s.UpdateFiltersAt(ctx, 42, 1, withKey("type", "road", 0))
	if err != nil || v != 2 || string(raw) != `{"type": "road"}` {
		t.Fatalf("UpdateFiltersAt (актуальная) = %s, %d, %v", raw, v, err)
	}

	// Два изменения с одной версией: применяется ровно одно.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = s.UpdateFiltersAt(ctx, 42, 2, withKey("k"+string(rune('a'+i)), "x", 50*time.Millisecond))
		}()
	}
	wg.Wait()
	stale := 0
	for _, e := range errs {
		if errors.Is(e, ErrStaleFilters) {
			stale++
		} else if e != nil {
			t.Fatal(e)
		}
	}
	if _, v, _, _ = s.PeekFilters(ctx, 42); stale != 1 || v != 3 {
		t.Fatalf("гонка: устаревших %d (want 1), версия %d (want 3)", stale, v)
	}
}
