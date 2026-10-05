package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// profileRow — строка user_profiles целиком (все колонки) для сравнения до/после.
func profileRow(t *testing.T, tgID int64) string {
	t.Helper()
	return scalar[string](t, `SELECT COALESCE((SELECT to_jsonb(p)::text FROM user_profiles p WHERE tg_id = $1), '')`, tgID)
}

func countRows(t *testing.T, table string, tgID int64) int {
	t.Helper()
	return scalar[int](t, `SELECT count(*) FROM `+table+` WHERE tg_id = $1`, tgID)
}

// PRF-01, D-06: /start создаёт строку профиля в той же транзакции, что и
// пользователя; offered = true только у вызова, который создал строку.
func TestProfileStart(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Нет пользователя: чтение не создаёт ни users, ни user_profiles.
	p, v, ok, err := s.PeekProfile(ctx, 10)
	if err != nil || ok || v != 0 || p.AgeGroup != -1 {
		t.Fatalf("PeekProfile (нет строки) = %+v, %d, %v, %v", p, v, ok, err)
	}
	if countRows(t, "users", 10) != 0 || countRows(t, "user_profiles", 10) != 0 {
		t.Fatal("PeekProfile создал строку")
	}

	// Первый /start: пользователь и пустой профиль.
	fv, pv, offered, err := s.StartUser(ctx, 10)
	if err != nil || fv != 1 || pv != 1 || !offered {
		t.Fatalf("StartUser (первый) = %d, %d, %v, %v; want 1, 1, true", fv, pv, offered, err)
	}
	empty := scalar[bool](t, `SELECT status = 'not_started' AND gender IS NULL AND age_group IS NULL
		AND residence_country_code IS NULL AND residence_city IS NULL AND residence_city_key IS NULL
		AND sports = '{}' AND awaiting_city_until IS NULL AND awaiting_city_mode IS NULL
		AND started_at IS NULL AND completed_at IS NULL FROM user_profiles WHERE tg_id = 10`)
	if !empty {
		t.Fatalf("профиль не пуст: %s", profileRow(t, 10))
	}
	p, v, ok, err = s.PeekProfile(ctx, 10)
	if err != nil || !ok || v != 1 || p.Status != ProfileNotStarted || p.AgeGroup != -1 || len(p.Sports) != 0 {
		t.Fatalf("PeekProfile = %+v, %d, %v, %v", p, v, ok, err)
	}
	t.Log("TRACER start=offered profile=not_started")

	// Повторный /start: строка одна, версия прежняя, отметка ожидания снята.
	seen := scalar[time.Time](t, `SELECT last_seen_at FROM users WHERE tg_id = 10`)
	exec(t, `UPDATE user_profiles SET awaiting_city_until = now() + interval '1 minute', awaiting_city_mode = 'w' WHERE tg_id = 10`)
	fv, pv, offered, err = s.StartUser(ctx, 10)
	if err != nil || fv != 1 || pv != 1 || offered {
		t.Fatalf("StartUser (повторный) = %d, %d, %v, %v; want 1, 1, false", fv, pv, offered, err)
	}
	if n := countRows(t, "user_profiles", 10); n != 1 {
		t.Fatalf("строк профиля %d, want 1", n)
	}
	if !scalar[bool](t, `SELECT awaiting_city_until IS NULL AND awaiting_city_mode IS NULL FROM user_profiles WHERE tg_id = 10`) {
		t.Fatal("отметка ожидания города не снята")
	}
	if now := scalar[time.Time](t, `SELECT last_seen_at FROM users WHERE tg_id = 10`); !now.After(seen) {
		t.Fatalf("last_seen_at не обновлён: %v -> %v", seen, now)
	}

	// Пользователь без строки профиля (создан Filters): профиль предлагается,
	// фильтры и filters_v не меняются.
	if _, _, err := s.UpdateFilters(ctx, 11, withKey("type", "road", 0)); err != nil {
		t.Fatal(err)
	}
	rawBefore, vBefore, _, _ := s.PeekFilters(ctx, 11)
	fv, pv, offered, err = s.StartUser(ctx, 11)
	if err != nil || fv != vBefore || pv != 1 || !offered {
		t.Fatalf("StartUser (есть users) = %d, %d, %v, %v; want %d, 1, true", fv, pv, offered, err, vBefore)
	}
	if raw, v, _, _ := s.PeekFilters(ctx, 11); string(raw) != string(rawBefore) || v != vBefore {
		t.Fatalf("StartUser изменил фильтры: %s v=%d -> %s v=%d", rawBefore, vBefore, raw, v)
	}
}

// BOT-08 в новой редакции: список колонок user_profiles равен утверждённой схеме.
func TestUserProfilesColumns(t *testing.T) {
	testStore(t)
	rows, err := testStorePkg.pool.Query(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'user_profiles' ORDER BY ordinal_position`)
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
	want := []string{"tg_id", "status", "gender", "age_group", "residence_country_code", "residence_city",
		"residence_city_key", "sports", "profile_v", "awaiting_city_until", "awaiting_city_mode",
		"offered_at", "started_at", "completed_at", "updated_at"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("user_profiles columns = %v, want %v", got, want)
	}
}

// Удаление пользователя удаляет его профиль.
func TestProfileCascade(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, _, _, err := s.StartUser(ctx, 20); err != nil {
		t.Fatal(err)
	}
	exec(t, `DELETE FROM users WHERE tg_id = 20`)
	if n := countRows(t, "user_profiles", 20); n != 0 {
		t.Fatalf("после удаления users строк профиля %d, want 0", n)
	}
}

// setProfile возвращает fn, заменяющую профиль целиком (с паузой внутри, если pause > 0).
func setProfile(next Profile, pause time.Duration) func(Profile) (Profile, error) {
	return func(Profile) (Profile, error) {
		time.Sleep(pause)
		return next, nil
	}
}

// PRF-05, PRF-06: ответы сохраняются как есть; версия и updated_at растут
// только при реальном изменении ответов или статуса; users не затрагивается.
func TestProfileUpdateAt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const id = 30
	t1 := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	if _, _, _, err := s.StartUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	usersBefore := userRow(t, id)

	// Первые ответы: status in_progress, started_at = t1, версия 2.
	want := Profile{Status: ProfileInProgress, Gender: "m", AgeGroup: 2, Country: "RU",
		City: "Москва", CityKey: "moskva", Sports: []string{"trail", "run"}}
	got, v, err := s.UpdateProfileAt(ctx, id, 1, t1, setProfile(want, 0))
	if err != nil || v != 2 || !reflect.DeepEqual(got, want) {
		t.Fatalf("UpdateProfileAt = %+v, %d, %v; want %+v, 2", got, v, err, want)
	}
	if p, pv, ok, err := s.PeekProfile(ctx, id); err != nil || !ok || pv != 2 || !reflect.DeepEqual(p, want) {
		t.Fatalf("PeekProfile = %+v, %d, %v, %v; want %+v", p, pv, ok, err, want)
	}
	stamps := `SELECT started_at = $2 AND completed_at IS NULL AND updated_at = $2 FROM user_profiles WHERE tg_id = $1`
	if !scalar[bool](t, stamps, int64(id), t1) {
		t.Fatalf("метки времени после первого изменения: %s", profileRow(t, id))
	}

	// Тот же профиль: ни версии, ни updated_at, ни строки.
	same := profileRow(t, id)
	if _, v, err := s.UpdateProfileAt(ctx, id, 2, t2, setProfile(want, 0)); err != nil || v != 2 {
		t.Fatalf("UpdateProfileAt (без изменений) = %d, %v", v, err)
	}
	if row := profileRow(t, id); row != same {
		t.Fatalf("неизменённый профиль записан:\n%s\n%s", same, row)
	}

	// Только отметка ожидания (поставить и снять): строка меняется, версия и updated_at нет.
	wait := want
	wait.AwaitingCityUntil, wait.AwaitingCityMode = t2.Add(time.Minute), "e"
	if _, v, err := s.UpdateProfileAt(ctx, id, 2, t2, setProfile(wait, 0)); err != nil || v != 2 {
		t.Fatalf("UpdateProfileAt (ожидание) = %d, %v", v, err)
	}
	if row := profileRow(t, id); row == same {
		t.Fatal("отметка ожидания не записана")
	}
	if !scalar[bool](t, stamps, int64(id), t1) {
		t.Fatalf("отметка ожидания изменила updated_at: %s", profileRow(t, id))
	}
	if _, v, err := s.UpdateProfileAt(ctx, id, 2, t2, setProfile(want, 0)); err != nil || v != 2 {
		t.Fatalf("UpdateProfileAt (снятие ожидания) = %d, %v", v, err)
	}
	if row := profileRow(t, id); row != same {
		t.Fatalf("после снятия ожидания строка другая:\n%s\n%s", same, row)
	}

	// Город без ключа и другой порядок видов спорта: реальное изменение, версия 3.
	next := want
	next.City, next.CityKey, next.Sports = "Тула", "", []string{"bike", "swim", "run"}
	got, v, err = s.UpdateProfileAt(ctx, id, 2, t2, setProfile(next, 0))
	if err != nil || v != 3 || !reflect.DeepEqual(got, next) {
		t.Fatalf("UpdateProfileAt = %+v, %d, %v; want %+v, 3", got, v, err, next)
	}
	if p, _, _, _ := s.PeekProfile(ctx, id); !reflect.DeepEqual(p, next) {
		t.Fatalf("PeekProfile = %+v, want %+v", p, next)
	}
	if !scalar[bool](t, `SELECT residence_city_key IS NULL AND updated_at = $2 FROM user_profiles WHERE tg_id = $1`, int64(id), t2) {
		t.Fatalf("город без ключа: %s", profileRow(t, id))
	}

	// Завершение: started_at прежний, completed_at = t3; пустые виды спорта — '{}'.
	done := next
	done.Status, done.Sports = ProfileCompleted, nil
	if _, v, err = s.UpdateProfileAt(ctx, id, 3, t3, setProfile(done, 0)); err != nil || v != 4 {
		t.Fatalf("UpdateProfileAt (завершение) = %d, %v", v, err)
	}
	if !scalar[bool](t, `SELECT started_at = $2 AND completed_at = $3 AND updated_at = $3 AND sports = '{}' FROM user_profiles WHERE tg_id = $1`,
		int64(id), t1, t3) {
		t.Fatalf("метки времени после завершения: %s", profileRow(t, id))
	}

	// Сброс ответов: AgeGroup -1 и пустые строки пишутся как NULL; completed_at сохраняется.
	reset := Profile{Status: ProfileCompleted, AgeGroup: -1}
	if _, v, err = s.UpdateProfileAt(ctx, id, 4, t3.Add(time.Hour), setProfile(reset, 0)); err != nil || v != 5 {
		t.Fatalf("UpdateProfileAt (сброс) = %d, %v", v, err)
	}
	if !scalar[bool](t, `SELECT gender IS NULL AND age_group IS NULL AND residence_country_code IS NULL AND residence_city IS NULL
		AND completed_at = $2 FROM user_profiles WHERE tg_id = $1`, int64(id), t3) {
		t.Fatalf("сброс ответов: %s", profileRow(t, id))
	}

	if row := userRow(t, id); row != usersBefore {
		t.Fatalf("UpdateProfileAt изменил users:\n%s\n%s", usersBefore, row)
	}
}

// PRF-07: чужая версия, нет строки или ошибка fn — ни одной записи.
func TestProfileStaleNoWrite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	t1 := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	called := false
	probe := func(p Profile) (Profile, error) {
		called = true
		p.Gender = "f"
		return p, nil
	}

	// Нет строки профиля: ни users, ни user_profiles не создаются.
	if _, _, err := s.UpdateProfileAt(ctx, 40, 1, t1, probe); !errors.Is(err, ErrStaleProfile) {
		t.Fatalf("UpdateProfileAt (нет строки) = %v, want ErrStaleProfile", err)
	}
	if countRows(t, "users", 40) != 0 || countRows(t, "user_profiles", 40) != 0 || called {
		t.Fatal("устаревшее изменение без строки создало строку либо вызвало fn")
	}

	// Пользователь есть, профиля нет — то же самое.
	if _, _, err := s.Filters(ctx, 41); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateProfileAt(ctx, 41, 1, t1, probe); !errors.Is(err, ErrStaleProfile) {
		t.Fatalf("UpdateProfileAt (нет профиля) = %v, want ErrStaleProfile", err)
	}
	if countRows(t, "user_profiles", 41) != 0 {
		t.Fatal("устаревшее изменение создало профиль")
	}

	// Чужая версия: строка профиля побайтно прежняя.
	if _, _, _, err := s.StartUser(ctx, 42); err != nil {
		t.Fatal(err)
	}
	before := profileRow(t, 42)
	if _, _, err := s.UpdateProfileAt(ctx, 42, 7, t1, probe); !errors.Is(err, ErrStaleProfile) {
		t.Fatalf("UpdateProfileAt (чужая версия) = %v, want ErrStaleProfile", err)
	}
	if row := profileRow(t, 42); row != before || called {
		t.Fatalf("устаревшее изменение записало в профиль или вызвало fn:\n%s\n%s", before, row)
	}

	// Ошибка fn возвращается как есть и ничего не пишет.
	boom := errors.New("boom")
	_, _, err := s.UpdateProfileAt(ctx, 42, 1, t1, func(p Profile) (Profile, error) {
		p.Gender = "m"
		return p, boom
	})
	if err != boom {
		t.Fatalf("UpdateProfileAt (ошибка fn) = %v, want boom", err)
	}
	if row := profileRow(t, 42); row != before {
		t.Fatalf("ошибка fn оставила запись:\n%s\n%s", before, row)
	}
}

// PRF-07: два изменения с одной версией — применяется ровно одно.
func TestProfileRace(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, _, _, err := s.StartUser(ctx, 50); err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, g := range []string{"m", "f"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = s.UpdateProfileAt(ctx, 50, 1, t1,
				setProfile(Profile{Status: ProfileInProgress, Gender: g, AgeGroup: -1}, 50*time.Millisecond))
		}()
	}
	wg.Wait()
	stale := 0
	for _, e := range errs {
		if errors.Is(e, ErrStaleProfile) {
			stale++
		} else if e != nil {
			t.Fatal(e)
		}
	}
	if _, v, _, _ := s.PeekProfile(ctx, 50); stale != 1 || v != 2 {
		t.Fatalf("гонка: устаревших %d (want 1), версия %d (want 2)", stale, v)
	}
}

// PRF-04: значения вне D-04 база отвергает; ошибка не ErrStaleProfile,
// строка профиля не меняется.
func TestProfileChecks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	t1 := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	if _, _, _, err := s.StartUser(ctx, 60); err != nil {
		t.Fatal(err)
	}
	base := Profile{Status: ProfileInProgress, Gender: "m", AgeGroup: 1, Country: "RU",
		City: "Москва", CityKey: "moskva", Sports: []string{"run"}}
	if _, _, err := s.UpdateProfileAt(ctx, 60, 1, t1, setProfile(base, 0)); err != nil {
		t.Fatal(err)
	}
	before := profileRow(t, 60)

	cases := []struct {
		name string
		mod  func(*Profile)
	}{
		{"пол x", func(p *Profile) { p.Gender = "x" }},
		{"возраст 6", func(p *Profile) { p.AgeGroup = 6 }},
		{"вид спорта other", func(p *Profile) { p.Sports = []string{"run", "other"} }},
		{"город из 65 символов", func(p *Profile) { p.City = strings.Repeat("я", 65) }},
		{"страна строчными", func(p *Profile) { p.Country = "ru" }},
		{"ключ с заглавной", func(p *Profile) { p.CityKey = "Moskva" }},
		{"город без страны", func(p *Profile) { p.Country = "" }},
		{"режим ожидания без времени", func(p *Profile) { p.AwaitingCityMode = "w" }},
		{"статус x", func(p *Profile) { p.Status = "x" }},
	}
	for _, c := range cases {
		next := base
		c.mod(&next)
		_, _, err := s.UpdateProfileAt(ctx, 60, 2, t1, setProfile(next, 0))
		if err == nil || errors.Is(err, ErrStaleProfile) {
			t.Errorf("%s: err = %v, want ошибка ограничения", c.name, err)
		}
		if row := profileRow(t, 60); row != before {
			t.Errorf("%s: строка профиля изменилась:\n%s\n%s", c.name, before, row)
		}
	}
}
