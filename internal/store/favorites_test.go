package store

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/event"
	"aggregator/migrations"
)

// TestMigration00010 — таблицы избранного и очереди исходящих сообщений
// (14-SCHEMA.md): ключи и проверки на месте; раздел Down удаляет обе таблицы и
// не трогает остальные; после теста схема возвращена на последнюю версию.
func TestMigration00010(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	constraint := func(name string) string {
		return scalar[string](t, `SELECT COALESCE((SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1), '')`, name)
	}
	if got := constraint("favorites_pkey"); got != "PRIMARY KEY (tg_id, event_id)" {
		t.Errorf("favorites_pkey = %q", got)
	}
	if got := constraint("outbox_tg_id_dedup_key_key"); got != "UNIQUE (tg_id, dedup_key)" {
		t.Errorf("outbox_tg_id_dedup_key_key = %q", got)
	}

	e := sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	const fav = `INSERT INTO favorites (tg_id, event_id, created_at) VALUES ($1, $2, now())`
	if _, err := s.pool.Exec(ctx, fav, 42, e.ID); err != nil {
		t.Fatalf("строка избранного отвергнута: %v", err)
	}
	for _, bad := range [][2]int64{{0, e.ID}, {-1, e.ID}, {42, e.ID + 1000}, {42, e.ID}} {
		if _, err := s.pool.Exec(ctx, fav, bad[0], bad[1]); err == nil {
			t.Errorf("строка избранного (%d, %d) принята базой", bad[0], bad[1])
		}
	}
	const out = `INSERT INTO outbox (tg_id, kind, event_id, dedup_key, created_at) VALUES ($1, $2, $3, $4, now())`
	if _, err := s.pool.Exec(ctx, out, 42, "change", e.ID, "change:1:1"); err != nil {
		t.Fatalf("строка очереди отвергнута: %v", err)
	}
	if _, err := s.pool.Exec(ctx, out, 42, "fuse", nil, "fuse:1"); err != nil {
		t.Errorf("строка очереди без события отвергнута: %v", err)
	}
	for _, bad := range []struct {
		tg        int64
		kind, key string
	}{
		{42, "change", "change:1:1"}, // пара «пользователь — ключ» повторяется
		{0, "change", "k"}, {42, "", "k"}, {42, "Change", "k"}, {42, "c", "k"}, {42, "change 1", "k"}, {42, "change", ""},
	} {
		if _, err := s.pool.Exec(ctx, out, bad.tg, bad.kind, e.ID, bad.key); err == nil {
			t.Errorf("строка очереди %+v принята базой", bad)
		}
	}

	const tables = `SELECT string_agg(tablename::text, ',' ORDER BY tablename) FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename NOT IN ('favorites', 'outbox', 'error_reports', 'start_suggestions', 'suggest_awaiting')`
	const added = `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename IN ('favorites', 'outbox')`
	others := scalar[string](t, tables)
	if n := scalar[int64](t, added); n != 2 {
		t.Fatalf("таблиц favorites и outbox — %d, want 2", n)
	}

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
	if _, err := p.DownTo(ctx, 9); err != nil {
		t.Fatalf("DownTo(9): %v", err)
	}
	if n := scalar[int64](t, added); n != 0 {
		t.Errorf("после Down таблиц favorites и outbox — %d, want 0", n)
	}
	if got := scalar[string](t, tables); got != others {
		t.Errorf("после Down остальные таблицы %s, want %s", got, others)
	}
	if n := scalar[int64](t, "SELECT count(*) FROM events"); n != 1 {
		t.Errorf("после Down событий %d, want 1", n)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if n := scalar[int64](t, added); n != 2 {
		t.Errorf("после Up таблиц favorites и outbox — %d, want 2", n)
	}
}

// TestFavorites — хранилище избранного: корень слитого события, отбор
// «будущий, видимый, включённый вид спорта», предел по показываемым стартам.
func TestFavorites(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enableSports(t, event.SportRunning, event.SportSkiing)
	today := mustDateFixed("2026-10-01")
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	const far = 100 // предел, до которого тест не доходит

	add := func(tg, id int64, day time.Time, limit int) FavOutcome {
		t.Helper()
		out, err := s.AddFavorite(ctx, tg, id, day, at, limit)
		if err != nil {
			t.Fatalf("AddFavorite(%d, %d): %v", tg, id, err)
		}
		return out
	}
	remove := func(tg, id int64) {
		t.Helper()
		if err := s.RemoveFavorite(ctx, tg, id); err != nil {
			t.Fatalf("RemoveFavorite(%d, %d): %v", tg, id, err)
		}
	}
	is := func(tg, id int64) bool {
		t.Helper()
		ok, err := s.IsFavorite(ctx, tg, id)
		if err != nil {
			t.Fatalf("IsFavorite(%d, %d): %v", tg, id, err)
		}
		return ok
	}
	page := func(tg int64, day time.Time, limit, offset int) ([]int64, int) {
		t.Helper()
		items, total, err := s.ListFavorites(ctx, tg, day, limit, offset)
		if err != nil {
			t.Fatalf("ListFavorites(%d): %v", tg, err)
		}
		return listIDs(items), total
	}
	// rows — записи пользователя как есть: идентификаторы событий по возрастанию.
	rows := func(tg int64) string {
		t.Helper()
		return scalar[string](t, `SELECT COALESCE(string_agg(event_id::text, ',' ORDER BY event_id), '') FROM favorites WHERE tg_id = $1`, tg)
	}
	ids := func(xs ...int64) string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = strconv.FormatInt(x, 10)
		}
		return strings.Join(out, ",")
	}
	wantPage := func(step string, tg int64, day time.Time, want []int64) {
		t.Helper()
		if got, total := page(tg, day, 50, 0); !reflect.DeepEqual(got, want) || total != len(want) {
			t.Errorf("%s: страница %v, всего %d; want %v", step, got, total, want)
		}
	}

	late := listEvent(t, s, "2026-10-05", "Москва", "moscow", event.TypeRoad)
	b := listEvent(t, s, "2026-10-02", "Казань", "kazan", event.TypeRoad)
	c := listEvent(t, s, "2026-10-02", "Тверь", "tver", event.TypeRoad)
	past := listEvent(t, s, "2026-09-30", "Тула", "tula", event.TypeRoad)
	old := listEvent(t, s, "2026-10-03", "Псков", "pskov", event.TypeRoad) // будет слито в late

	// Добавление дважды: одна строка, время первого добавления.
	if got := add(42, late, today, far); got != FavAdded {
		t.Fatalf("первое добавление: %v, want FavAdded", got)
	}
	at = at.Add(time.Hour)
	if got := add(42, late, today, far); got != FavAlready {
		t.Errorf("повторное добавление: %v, want FavAlready", got)
	}
	if n := scalar[int64](t, "SELECT count(*) FROM favorites WHERE tg_id = 42 AND event_id = $1 AND created_at = $2", late, at.Add(-time.Hour)); n != 1 || rows(42) != ids(late) {
		t.Errorf("после повторного добавления: строк с первым временем %d, записи %q", n, rows(42))
	}

	// Несуществующее и прошедшее событие.
	if got := add(42, late+1000, today, far); got != FavGone {
		t.Errorf("несуществующее событие: %v, want FavGone", got)
	}
	if got := add(42, past, today, far); got != FavPast {
		t.Errorf("прошедшее событие: %v, want FavPast", got)
	}
	if rows(42) != ids(late) {
		t.Errorf("отказ создал строку: %q", rows(42))
	}

	// Запись на событие, слитое позже: корень — «уже есть», страница без
	// повторов, проверка по корню верна.
	if got := add(42, old, today, far); got != FavAdded {
		t.Fatalf("добавление old: %v", got)
	}
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", late, old)
	if got := add(42, late, today, far); got != FavAlready {
		t.Errorf("корень при записи на слитое: %v, want FavAlready", got)
	}
	if got := add(42, old, today, far); got != FavAlready {
		t.Errorf("слитое при записи на слитое: %v, want FavAlready", got)
	}
	if rows(42) != ids(late, old) {
		t.Errorf("записи %q, want %q", rows(42), ids(late, old))
	}
	wantPage("слитое и корень", 42, today, []int64{late})
	if !is(42, late) || is(42, b) || is(43, late) {
		t.Errorf("IsFavorite: корень %v, чужое событие %v, чужой пользователь %v", is(42, late), is(42, b), is(43, late))
	}

	// Удаление убирает записи и на корень, и на слитое — по любому из двух id.
	remove(42, late)
	if rows(42) != "" || is(42, late) {
		t.Errorf("после удаления по корню записи %q", rows(42))
	}
	exec(t, "INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, $1, now()), (42, $2, now())", late, old)
	remove(42, old)
	if rows(42) != "" {
		t.Errorf("после удаления по слитому записи %q", rows(42))
	}
	// Запись только на слитое событие: показан корень, проверка по корню верна.
	exec(t, "INSERT INTO favorites (tg_id, event_id, created_at) VALUES (42, $1, now())", old)
	wantPage("только слитое", 42, today, []int64{late})
	if !is(42, late) {
		t.Error("IsFavorite(корень) при записи только на слитое событие — false")
	}
	remove(42, late)
	if rows(42) != "" {
		t.Errorf("после удаления по корню запись на слитое осталась: %q", rows(42))
	}
	remove(42, late)        // отсутствующая запись
	remove(42, late+100000) // несуществующее событие

	// Добавление слитого события записывает корень.
	if got := add(42, old, today, far); got != FavAdded || rows(42) != ids(late) {
		t.Errorf("добавление слитого: %v, записи %q; want FavAdded и %q", got, rows(42), ids(late))
	}

	// Порядок — дата, затем id; страницы; другой пользователь независим.
	add(42, c, today, far)
	add(42, b, today, far)
	wantPage("порядок", 42, today, []int64{b, c, late})
	if got, total := page(42, today, 2, 2); !reflect.DeepEqual(got, []int64{late}) || total != 3 {
		t.Errorf("вторая страница %v, всего %d; want [%d] и 3", got, total, late)
	}
	wantPage("другой пользователь", 43, today, []int64{})
	if got := add(43, b, today, far); got != FavAdded {
		t.Errorf("тот же старт у другого пользователя: %v", got)
	}
	remove(43, c)
	wantPage("чужое удаление", 42, today, []int64{b, c, late})

	// Прошедший старт: запись остаётся, страница и предел его не считают.
	wantPage("сегодня 3 октября", 42, mustDateFixed("2026-10-03"), []int64{late})
	if rows(42) != ids(late, b, c) {
		t.Errorf("прошедшие записи исчезли: %q", rows(42))
	}

	// Скрытый старт: не показан и в предел не идёт, запись на месте, удаление и
	// проверка работают; вернулся в выдачу — снова показан.
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", b)
	wantPage("скрытый", 42, today, []int64{c, late})
	if !is(42, b) || rows(42) != ids(late, b, c) {
		t.Errorf("скрытый старт: IsFavorite %v, записи %q", is(42, b), rows(42))
	}
	if got := add(43, c, today, far); got != FavAdded {
		t.Errorf("добавление видимого: %v", got)
	}
	if got := add(44, b, today, far); got != FavGone {
		t.Errorf("добавление скрытого: %v, want FavGone", got)
	}
	exec(t, "UPDATE events SET is_listed = true WHERE id = $1", b)
	wantPage("вернулся в выдачу", 42, today, []int64{b, c, late})
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", b)
	remove(42, b)
	if is(42, b) || rows(42) != ids(late, c) {
		t.Errorf("удаление скрытого: записи %q", rows(42))
	}
	exec(t, "UPDATE events SET is_listed = true WHERE id = $1", b)

	// Выключенный вид спорта: как скрытый.
	ski := sampleEvent()
	ski.Title, ski.TitleNorm, ski.Sport, ski.Type = "Лыжная гонка", "lyzhnaya gonka", event.SportSkiing, event.TypeSki
	ski.StartDate, ski.EndDate = mustDateFixed("2026-12-19"), nil
	if _, err := s.SaveEvent(ctx, ski); err != nil {
		t.Fatal(err)
	}
	if got := add(42, ski.ID, today, far); got != FavAdded {
		t.Fatalf("лыжный старт: %v", got)
	}
	wantPage("лыжи включены", 42, today, []int64{c, late, ski.ID})
	enableSports(t, event.SportRunning)
	wantPage("лыжи выключены", 42, today, []int64{c, late})
	if got := add(43, ski.ID, today, far); got != FavGone {
		t.Errorf("старт выключенного вида спорта: %v, want FavGone", got)
	}
	if !is(42, ski.ID) {
		t.Error("запись о старте выключенного вида спорта исчезла")
	}

	// Предел — по показываемым стартам: у 42 их два (c, late), лыжный не в счёт.
	if got := add(42, b, today, 2); got != FavLimit || rows(42) != ids(late, c, ski.ID) {
		t.Errorf("сверх предела: %v, записи %q; want FavLimit без новой строки", got, rows(42))
	}
	if got := add(42, late, today, 2); got != FavAlready {
		t.Errorf("повторное добавление при пределе: %v, want FavAlready", got)
	}
	// 3 октября c прошёл и предел не занимает: добавление проходит.
	four := listEvent(t, s, "2026-10-04", "Орёл", "orel", event.TypeRoad)
	if got := add(42, four, mustDateFixed("2026-10-03"), 2); got != FavAdded {
		t.Errorf("прошедшая запись заняла предел: %v, want FavAdded", got)
	}
	if got := add(43, late, today, 2); got != FavLimit {
		t.Errorf("предел другого пользователя (b, c): %v, want FavLimit", got)
	}
}
