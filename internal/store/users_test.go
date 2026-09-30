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
