package store

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// supportRows — содержимое обеих таблиц поддержки одной строкой (для
// сравнения до/после и для проверки «ничего не записано»).
func supportRows(t *testing.T) string {
	t.Helper()
	return scalar[string](t, `SELECT
		COALESCE((SELECT jsonb_agg(to_jsonb(m) ORDER BY chat_id, message_id)::text FROM support_messages m), '[]') || '|' ||
		COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY tg_id)::text FROM support_awaiting a), '[]')`)
}

// tableColumns — «имя тип» колонок таблицы по порядку.
func tableColumns(t *testing.T, table string) []string {
	t.Helper()
	rows, err := testStorePkg.pool.Query(context.Background(),
		`SELECT column_name || ' ' || data_type FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`, table)
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
	return got
}

// SUP-11, D-13: отметка ставится, потребляется один раз, связка записывается
// и находится по паре «группа, сообщение»; пользователь без users и профиля.
func TestSupportFlow(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const id, chat, msg = 4001, int64(-1001), 77
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	until := now.Add(5 * time.Minute)

	if _, ok, err := s.SupportAwaiting(ctx, id); err != nil || ok {
		t.Fatalf("SupportAwaiting (нет отметки) = %v, %v", ok, err)
	}
	if err := s.SupportBegin(ctx, id, until); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.SupportAwaiting(ctx, id)
	if err != nil || !ok || !got.Equal(until) {
		t.Fatalf("SupportAwaiting = %v, %v, %v; want %v, true", got, ok, err, until)
	}
	out, err := s.SupportTake(ctx, id, now, now.Add(-time.Hour), 5)
	if err != nil || out != SupportTaken {
		t.Fatalf("SupportTake = %v, %v; want SupportTaken", out, err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, id); ok {
		t.Fatal("отметка не удалена")
	}
	out, err = s.SupportTake(ctx, id, now, now.Add(-time.Hour), 5)
	if err != nil || out != SupportNotWaiting {
		t.Fatalf("повторный SupportTake = %v, %v; want SupportNotWaiting", out, err)
	}

	if err := s.SupportLink(ctx, chat, msg, id, now); err != nil {
		t.Fatal(err)
	}
	row := supportRows(t)
	if err := s.SupportLink(ctx, chat, msg, id, now.Add(time.Minute)); err != nil {
		t.Fatalf("повторный SupportLink: %v", err)
	}
	if after := supportRows(t); after != row {
		t.Fatalf("повторный SupportLink изменил строку:\n%s\n%s", row, after)
	}
	if a, ok, err := s.SupportAuthor(ctx, chat, msg); err != nil || !ok || a != id {
		t.Fatalf("SupportAuthor = %d, %v, %v; want %d, true", a, ok, err, id)
	}
	if _, ok, err := s.SupportAuthor(ctx, chat-1, msg); err != nil || ok {
		t.Fatalf("SupportAuthor (другая группа) ok = %v, %v", ok, err)
	}
	if _, ok, err := s.SupportAuthor(ctx, chat, msg+1); err != nil || ok {
		t.Fatalf("SupportAuthor (неизвестное сообщение) ok = %v, %v", ok, err)
	}
	if countRows(t, "users", id) != 0 || countRows(t, "user_profiles", id) != 0 {
		t.Fatal("поддержка создала строку users или user_profiles")
	}
	t.Log("TRACER support=taken link=stored author=found")
}

// T-11-05, D-13: в таблицах поддержки только идентификаторы и время —
// колонок для текста, вложений, имени и username нет.
func TestSupportColumns(t *testing.T) {
	testStore(t)
	wantMsg := []string{"chat_id bigint", "message_id integer", "tg_id bigint", "created_at timestamp with time zone"}
	wantAwait := []string{"tg_id bigint", "expires_at timestamp with time zone"}
	if got := tableColumns(t, "support_messages"); !reflect.DeepEqual(got, wantMsg) {
		t.Fatalf("support_messages columns = %v, want %v", got, wantMsg)
	}
	if got := tableColumns(t, "support_awaiting"); !reflect.DeepEqual(got, wantAwait) {
		t.Fatalf("support_awaiting columns = %v, want %v", got, wantAwait)
	}
}

// D-12, D-14: лимит считается по связкам автора за окно; чужие связки и связки
// старше окна в счёт не идут; отметка потребляется и при отказе.
func TestSupportLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const id, other, chat = 4010, 4011, int64(-1001)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for i, ago := range []time.Duration{59 * time.Minute, 40 * time.Minute, 30 * time.Minute, 20 * time.Minute, 10 * time.Minute} {
		if err := s.SupportLink(ctx, chat, 100+i, id, now.Add(-ago)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SupportLink(ctx, chat, 200, other, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.SupportLink(ctx, chat, 201, id, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.SupportBegin(ctx, id, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	out, err := s.SupportTake(ctx, id, now, now.Add(-time.Hour), 5)
	if err != nil || out != SupportLimited {
		t.Fatalf("SupportTake (5 связок) = %v, %v; want SupportLimited", out, err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, id); ok {
		t.Fatal("при отказе по лимиту отметка не удалена")
	}
	if n := countRows(t, "support_messages", id); n != 6 {
		t.Fatalf("связок автора %d, want 6 (пять в окне и одна старая)", n)
	}

	// Через две минуты самая старая связка выходит из окна.
	later := now.Add(2 * time.Minute)
	if err := s.SupportBegin(ctx, id, later.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	out, err = s.SupportTake(ctx, id, later, later.Add(-time.Hour), 5)
	if err != nil || out != SupportTaken {
		t.Fatalf("SupportTake (окно сдвинулось) = %v, %v; want SupportTaken", out, err)
	}
}

// D-08: отметка живёт до заданного момента; истёкшая не принимается и удаляется.
func TestSupportExpired(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const id = 4020
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	since := now.Add(-time.Hour)

	for _, at := range []time.Time{now, now.Add(time.Second)} {
		if err := s.SupportBegin(ctx, id, now); err != nil {
			t.Fatal(err)
		}
		out, err := s.SupportTake(ctx, id, at, since, 5)
		if err != nil || out != SupportNotWaiting {
			t.Fatalf("SupportTake (now = %v, отметка до %v) = %v, %v; want SupportNotWaiting", at, now, out, err)
		}
		if _, ok, _ := s.SupportAwaiting(ctx, id); ok {
			t.Fatal("истёкшая отметка не удалена")
		}
		if n := scalar[int](t, `SELECT count(*) FROM support_messages`); n != 0 {
			t.Fatalf("связок %d, want 0", n)
		}
	}

	// Повторный вход заменяет момент.
	first, second := now.Add(time.Minute), now.Add(3*time.Minute)
	if err := s.SupportBegin(ctx, id, first); err != nil {
		t.Fatal(err)
	}
	if err := s.SupportBegin(ctx, id, second); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.SupportAwaiting(ctx, id); err != nil || !ok || !got.Equal(second) {
		t.Fatalf("SupportAwaiting = %v, %v, %v; want %v", got, ok, err, second)
	}
	if n := scalar[int](t, `SELECT count(*) FROM support_awaiting`); n != 1 {
		t.Fatalf("отметок %d, want 1", n)
	}

	// Снятие без отметки — не ошибка.
	if err := s.SupportCancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.SupportCancel(ctx, id); err != nil {
		t.Fatalf("SupportCancel без отметки: %v", err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, id); ok {
		t.Fatal("отметка не снята")
	}
}

// D-09: из двух одновременных сообщений обращением становится ровно одно.
func TestSupportRace(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const id = 4030
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for round := 0; round < 10; round++ {
		if err := s.SupportBegin(ctx, id, now.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		outs := make([]SupportOutcome, 2)
		errs := make([]error, 2)
		for i := range outs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outs[i], errs[i] = s.SupportTake(ctx, id, now, now.Add(-time.Hour), 5)
			}()
		}
		wg.Wait()
		taken, none := 0, 0
		for i, o := range outs {
			if errs[i] != nil {
				t.Fatal(errs[i])
			}
			switch o {
			case SupportTaken:
				taken++
			case SupportNotWaiting:
				none++
			}
		}
		if taken != 1 || none != 1 {
			t.Fatalf("круг %d: принято %d (want 1), не ждали %d (want 1)", round, taken, none)
		}
	}
}

// profileRowNoCity — строка профиля без двух колонок отметки города.
func profileRowNoCity(t *testing.T, tgID int64) string {
	t.Helper()
	return scalar[string](t, `SELECT COALESCE((SELECT (to_jsonb(p) - 'awaiting_city_until' - 'awaiting_city_mode')::text
		FROM user_profiles p WHERE tg_id = $1), '')`, tgID)
}

// D-10: «жду город» и «жду обращение» не действуют одновременно.
func TestSupportExclusive(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	until := now.Add(5 * time.Minute)

	// (1) SupportBegin снимает отметку города; остальное в строке профиля прежнее.
	const a = 4040
	if _, _, _, err := s.StartUser(ctx, a); err != nil {
		t.Fatal(err)
	}
	exec(t, `UPDATE user_profiles SET awaiting_city_until = now() + interval '1 minute', awaiting_city_mode = 'e',
		profile_v = 3, updated_at = '2026-01-01T00:00:00Z' WHERE tg_id = $1`, a)
	before := profileRowNoCity(t, a)
	if err := s.SupportBegin(ctx, a, until); err != nil {
		t.Fatal(err)
	}
	if !scalar[bool](t, `SELECT awaiting_city_until IS NULL AND awaiting_city_mode IS NULL FROM user_profiles WHERE tg_id = $1`, a) {
		t.Fatalf("отметка города не снята: %s", profileRow(t, a))
	}
	if after := profileRowNoCity(t, a); after != before {
		t.Fatalf("SupportBegin изменил профиль:\n%s\n%s", before, after)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, a); !ok {
		t.Fatal("отметка поддержки не поставлена")
	}

	// (2) StartUser снимает отметку поддержки: у действующего и у нового пользователя.
	if _, _, _, err := s.StartUser(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, a); ok {
		t.Fatal("StartUser (действующий) не снял отметку поддержки")
	}
	const fresh = 4041
	if err := s.SupportBegin(ctx, fresh, until); err != nil {
		t.Fatal(err)
	}
	if _, _, offered, err := s.StartUser(ctx, fresh); err != nil || !offered {
		t.Fatalf("StartUser (новый) offered = %v, %v; want true", offered, err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, fresh); ok {
		t.Fatal("StartUser (новый) не снял отметку поддержки")
	}

	// (3) UpdateProfileAt: только ответы — отметка поддержки на месте; отметка города — снята.
	const b = 4042
	if _, _, _, err := s.StartUser(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SupportBegin(ctx, b, until); err != nil {
		t.Fatal(err)
	}
	answers := func(p Profile) (Profile, error) { p.Status, p.Gender = ProfileInProgress, "m"; return p, nil }
	if _, _, err := s.UpdateProfileAt(ctx, b, 1, now, answers); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, b); !ok {
		t.Fatal("UpdateProfileAt (только ответы) снял отметку поддержки")
	}
	city := func(p Profile) (Profile, error) { p.AwaitingCityUntil, p.AwaitingCityMode = until, "w"; return p, nil }
	if _, _, err := s.UpdateProfileAt(ctx, b, 2, now, city); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, b); ok {
		t.Fatal("UpdateProfileAt (отметка города) не снял отметку поддержки")
	}
	// Профиль уже несёт отметку города и не меняется — отметка поддержки всё равно снимается.
	exec(t, `INSERT INTO support_awaiting (tg_id, expires_at) VALUES ($1, $2)`, b, until)
	if _, _, err := s.UpdateProfileAt(ctx, b, 2, now, func(p Profile) (Profile, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, b); ok {
		t.Fatal("UpdateProfileAt без записи не снял отметку поддержки")
	}

	// (4) Устаревшая версия и ошибка fn отметку поддержки не трогают.
	const c = 4043
	if _, _, _, err := s.StartUser(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.SupportBegin(ctx, c, until); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateProfileAt(ctx, c, 9, now, city); !errors.Is(err, ErrStaleProfile) {
		t.Fatalf("устаревшая версия: err = %v, want ErrStaleProfile", err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, c); !ok {
		t.Fatal("устаревшая версия сняла отметку поддержки")
	}
	boom := errors.New("boom")
	if _, _, err := s.UpdateProfileAt(ctx, c, 1, now, func(p Profile) (Profile, error) {
		next, _ := city(p)
		return next, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("ошибка fn: err = %v, want boom", err)
	}
	if _, ok, _ := s.SupportAwaiting(ctx, c); !ok {
		t.Fatal("ошибка fn сняла отметку поддержки")
	}
}

// Поддержка не пишет в users и не создаёт строку профиля.
func TestSupportNoUsersWrite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	run := func(id int64) {
		t.Helper()
		if err := s.SupportBegin(ctx, id, now.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if out, err := s.SupportTake(ctx, id, now, now.Add(-time.Hour), 5); err != nil || out != SupportTaken {
			t.Fatalf("SupportTake = %v, %v", out, err)
		}
		if err := s.SupportLink(ctx, -1001, 1, id, now); err != nil {
			t.Fatal(err)
		}
		if err := s.SupportBegin(ctx, id, now.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.SupportCancel(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	run(4050)
	if u, p := scalar[int](t, `SELECT count(*) FROM users`), scalar[int](t, `SELECT count(*) FROM user_profiles`); u != 0 || p != 0 {
		t.Fatalf("для неизвестного пользователя users = %d, user_profiles = %d, want 0, 0", u, p)
	}

	const known = 4051
	if _, _, _, err := s.StartUser(ctx, known); err != nil {
		t.Fatal(err)
	}
	uBefore, pBefore := userRow(t, known), profileRow(t, known)
	run(known)
	if uAfter := userRow(t, known); uAfter != uBefore {
		t.Fatalf("строка users изменилась:\n%s\n%s", uBefore, uAfter)
	}
	if pAfter := profileRow(t, known); pAfter != pBefore {
		t.Fatalf("строка профиля изменилась:\n%s\n%s", pBefore, pAfter)
	}
}

// T-11-07: база отвергает идентификаторы вне допустимых; таблицы не меняются.
func TestSupportChecks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if err := s.SupportLink(ctx, -1001, 5, 4060, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SupportBegin(ctx, 4060, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	before := supportRows(t)

	cases := []struct {
		name string
		fn   func() error
	}{
		{"группа с нулевым идентификатором", func() error { return s.SupportLink(ctx, 0, 6, 4060, now) }},
		{"группа с положительным идентификатором", func() error { return s.SupportLink(ctx, 1001, 6, 4060, now) }},
		{"сообщение с нулевым номером", func() error { return s.SupportLink(ctx, -1001, 0, 4060, now) }},
		{"нулевой автор связки", func() error { return s.SupportLink(ctx, -1001, 6, 0, now) }},
		{"отрицательный автор связки", func() error { return s.SupportLink(ctx, -1001, 6, -4060, now) }},
		{"нулевой автор отметки", func() error { return s.SupportBegin(ctx, 0, now) }},
	}
	for _, c := range cases {
		if err := c.fn(); err == nil {
			t.Errorf("%s: ошибки нет", c.name)
		}
		if after := supportRows(t); after != before {
			t.Errorf("%s: таблицы изменились:\n%s\n%s", c.name, before, after)
		}
	}
}
