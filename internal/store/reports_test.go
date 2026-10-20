package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/event"
	"aggregator/migrations"
)

// TestMigration00011 — таблицы заявок, предложений и отметки «жду ссылку»
// (16-SCHEMA.md): ключи и проверки на месте; раздел Down удаляет три таблицы и
// не трогает остальные; после теста схема возвращена на последнюю версию.
func TestMigration00011(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	constraint := func(name string) string {
		return scalar[string](t, `SELECT COALESCE((SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1), '')`, name)
	}
	if got := constraint("error_reports_pkey"); got != "PRIMARY KEY (tg_id, event_id, reason)" {
		t.Errorf("error_reports_pkey = %q", got)
	}
	if got := constraint("error_reports_event_id_fkey"); got != "FOREIGN KEY (event_id) REFERENCES events(id)" {
		t.Errorf("error_reports_event_id_fkey = %q", got)
	}

	e := sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	const rep = `INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES ($1, $2, $3, now())`
	if _, err := s.pool.Exec(ctx, rep, 42, e.ID, "date"); err != nil {
		t.Fatalf("строка заявки отвергнута: %v", err)
	}
	for _, bad := range []struct {
		tg, ev int64
		reason string
	}{
		{42, e.ID, "date"}, {0, e.ID, "place"}, {42, e.ID + 1000, "place"},
		{42, e.ID, ""}, {42, e.ID, "d"}, {42, e.ID, "Date"}, {42, e.ID, "date 1"}, {42, e.ID, "_date"},
	} {
		if _, err := s.pool.Exec(ctx, rep, bad.tg, bad.ev, bad.reason); err == nil {
			t.Errorf("строка заявки %+v принята базой", bad)
		}
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO start_suggestions (tg_id, created_at) VALUES (0, now())`); err == nil {
		t.Error("предложение с tg_id = 0 принято базой")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO suggest_awaiting (tg_id, expires_at) VALUES (-1, now())`); err == nil {
		t.Error("отметка с tg_id = -1 принята базой")
	}

	const added = `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'
		AND tablename IN ('error_reports', 'start_suggestions', 'suggest_awaiting')`
	const tables = `SELECT string_agg(tablename::text, ',' ORDER BY tablename) FROM pg_tables
		WHERE schemaname = 'public' AND tablename NOT IN ('error_reports', 'start_suggestions', 'suggest_awaiting', 'user_days', 'usage_counters', 'subscriptions', 'app_settings', 'calendar_feeds')`
	others := scalar[string](t, tables)
	if n := scalar[int64](t, added); n != 3 {
		t.Fatalf("новых таблиц %d, want 3", n)
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
	if _, err := p.DownTo(ctx, 10); err != nil {
		t.Fatalf("DownTo(10): %v", err)
	}
	if n := scalar[int64](t, added); n != 0 {
		t.Errorf("после Down новых таблиц %d, want 0", n)
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
	if n := scalar[int64](t, added); n != 3 {
		t.Errorf("после Up новых таблиц %d, want 3", n)
	}
}

// TestReports — заявки об ошибках: повтор раньше предела, предел по окну,
// корень слитого события, удаление при сбое отправки.
func TestReports(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	const limit = 3

	ids := make([]int64, 4)
	for i := range ids {
		e := sampleEvent()
		e.Title += string(rune('A' + i))
		if _, err := s.SaveEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
		ids[i] = e.ID
	}
	add := func(tg, id int64, reason string, at time.Time) (int64, ReportOutcome) {
		t.Helper()
		root, out, err := s.AddReport(ctx, tg, id, reason, at, at.Add(-24*time.Hour), limit)
		if err != nil {
			t.Fatalf("AddReport(%d, %d, %s): %v", tg, id, reason, err)
		}
		return root, out
	}
	rows := func(tg int64) int64 {
		t.Helper()
		return scalar[int64](t, "SELECT count(*) FROM error_reports WHERE tg_id = $1", tg)
	}

	// Старая заявка вне окна в предел не идёт.
	if _, out := add(42, ids[3], "other", since.Add(-time.Minute)); out != ReportAdded {
		t.Fatalf("старая заявка: %v", out)
	}
	if root, out := add(42, ids[0], "date", now); out != ReportAdded || root != ids[0] {
		t.Fatalf("первая заявка: %d, %v", root, out)
	}
	if _, out := add(42, ids[0], "date", now); out != ReportRepeat {
		t.Fatalf("повтор: %v", out)
	}
	if rows(42) != 2 {
		t.Fatalf("после повтора строк %d, want 2", rows(42))
	}
	if _, out := add(42, ids[0], "place", now); out != ReportAdded {
		t.Fatalf("другая причина: %v", out)
	}
	if _, out := add(42, ids[1], "link", now); out != ReportAdded {
		t.Fatalf("третья заявка: %v", out)
	}
	if _, out := add(42, ids[2], "link", now); out != ReportLimited {
		t.Fatalf("сверх предела: %v", out)
	}
	if rows(42) != 4 {
		t.Fatalf("после предела строк %d, want 4", rows(42))
	}
	// Повтор проверяется раньше предела.
	if _, out := add(42, ids[1], "link", now); out != ReportRepeat {
		t.Fatalf("повтор на пределе: %v", out)
	}
	// Предел — у каждого пользователя свой.
	if _, out := add(43, ids[2], "link", now); out != ReportAdded {
		t.Fatalf("другой пользователь: %v", out)
	}
	// Окно скользит: через сутки заявка снова проходит.
	if _, out := add(42, ids[2], "link", now.Add(24*time.Hour)); out != ReportAdded {
		t.Fatalf("через сутки: %v", out)
	}

	// Слитое событие: заявка — на корень, повтор считается по корню.
	exec(t, "UPDATE events SET merged_into_id = $1 WHERE id = $2", ids[1], ids[3])
	if root, out := add(44, ids[3], "duplicate", now); out != ReportAdded || root != ids[1] {
		t.Fatalf("слитое: %d, %v; want %d", root, out, ids[1])
	}
	if _, out := add(44, ids[1], "duplicate", now); out != ReportRepeat {
		t.Fatalf("повтор по корню: %v", out)
	}
	if got := scalar[int64](t, "SELECT event_id FROM error_reports WHERE tg_id = 44"); got != ids[1] {
		t.Fatalf("event_id заявки %d, want корень %d", got, ids[1])
	}

	if _, _, err := s.AddReport(ctx, 42, 99999, "date", now, since, limit); !isNotFound(err) {
		t.Fatalf("нет события: %v", err)
	}

	if err := s.DeleteReport(ctx, 44, ids[1], "duplicate"); err != nil {
		t.Fatal(err)
	}
	if rows(44) != 0 {
		t.Fatal("DeleteReport не удалил строку")
	}
	if err := s.DeleteReport(ctx, 44, ids[1], "duplicate"); err != nil {
		t.Fatalf("повторное удаление: %v", err)
	}
}

// TestSuggest — отметка «жду ссылку», приём под блокировкой, предел,
// откат при сбое отправки и взаимоисключение режимов ввода.
func TestSuggest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	until := now.Add(10 * time.Minute)
	const limit = 3
	const a = 5050

	take := func(at time.Time) (int64, SuggestOutcome) {
		t.Helper()
		id, out, err := s.SuggestTake(ctx, a, at, at.Add(-24*time.Hour), limit)
		if err != nil {
			t.Fatalf("SuggestTake: %v", err)
		}
		return id, out
	}
	waiting := func(tg int64) bool {
		t.Helper()
		_, ok, err := s.SuggestAwaiting(ctx, tg)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	begin := func() {
		t.Helper()
		if err := s.SuggestBegin(ctx, a, until); err != nil {
			t.Fatal(err)
		}
	}

	if _, out := take(now); out != SuggestNotWaiting {
		t.Fatalf("без отметки: %v", out)
	}
	// Истёкшая отметка не действует и удаляется.
	if err := s.SuggestBegin(ctx, a, now); err != nil {
		t.Fatal(err)
	}
	if _, out := take(now); out != SuggestNotWaiting || waiting(a) {
		t.Fatalf("истёкшая отметка: %v, отметка %v", out, waiting(a))
	}

	begin()
	if got, ok, _ := s.SuggestAwaiting(ctx, a); !ok || !got.Equal(until) {
		t.Fatalf("SuggestAwaiting = %v, %v", got, ok)
	}
	id, out := take(now)
	if out != SuggestTaken || id == 0 || waiting(a) {
		t.Fatalf("приём: %d, %v, отметка %v", id, out, waiting(a))
	}
	if got := scalar[int64](t, "SELECT tg_id FROM start_suggestions WHERE id = $1", id); got != a {
		t.Fatalf("строка предложения tg_id = %d", got)
	}
	// Повторное сообщение без новой отметки — не предложение.
	if _, out := take(now); out != SuggestNotWaiting {
		t.Fatalf("второе сообщение: %v", out)
	}

	// Сбой отправки: строка удалена, отметка возвращена.
	if err := s.SuggestRelease(ctx, a, id, until); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int64](t, "SELECT count(*) FROM start_suggestions"); n != 0 || !waiting(a) {
		t.Fatalf("после отката строк %d, отметка %v", n, waiting(a))
	}

	for i := range limit {
		if i > 0 {
			begin()
		}
		if _, out := take(now); out != SuggestTaken {
			t.Fatalf("предложение %d: %v", i+1, out)
		}
	}
	begin()
	if id, out := take(now); out != SuggestLimited || id != 0 || waiting(a) {
		t.Fatalf("сверх предела: %d, %v, отметка %v", id, out, waiting(a))
	}
	if n := scalar[int64](t, "SELECT count(*) FROM start_suggestions"); n != limit {
		t.Fatalf("строк %d, want %d", n, limit)
	}

	if err := s.SuggestCancel(ctx, a); err != nil {
		t.Fatal(err)
	}
	begin()
	if err := s.SuggestCancel(ctx, a); err != nil || waiting(a) {
		t.Fatalf("SuggestCancel: %v, отметка %v", err, waiting(a))
	}
}

// TestSuggestExclusive — режимы «жду ссылку», «жду обращение» и «жду город»
// взаимно снимают друг друга (Q-7).
func TestSuggestExclusive(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	until := now.Add(10 * time.Minute)
	const a = 6060
	suggest := func() bool {
		t.Helper()
		_, ok, err := s.SuggestAwaiting(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	support := func() bool {
		t.Helper()
		_, ok, err := s.SupportAwaiting(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	city := func() bool {
		t.Helper()
		return scalar[bool](t, "SELECT awaiting_city_until IS NOT NULL FROM user_profiles WHERE tg_id = $1", a)
	}
	if _, _, _, err := s.StartUser(ctx, a); err != nil {
		t.Fatal(err)
	}

	// Вход в предложение снимает поддержку и город.
	exec(t, "UPDATE user_profiles SET awaiting_city_until = now() + interval '1 minute', awaiting_city_mode = 'e' WHERE tg_id = $1", a)
	if err := s.SupportBegin(ctx, a, until); err != nil {
		t.Fatal(err)
	}
	exec(t, "UPDATE user_profiles SET awaiting_city_until = now() + interval '1 minute', awaiting_city_mode = 'e' WHERE tg_id = $1", a)
	if err := s.SuggestBegin(ctx, a, until); err != nil {
		t.Fatal(err)
	}
	if !suggest() || support() || city() {
		t.Fatalf("SuggestBegin: ссылка %v, поддержка %v, город %v", suggest(), support(), city())
	}

	// Вход в поддержку снимает ссылку.
	if err := s.SupportBegin(ctx, a, until); err != nil {
		t.Fatal(err)
	}
	if suggest() || !support() {
		t.Fatalf("SupportBegin: ссылка %v, поддержка %v", suggest(), support())
	}

	// /start снимает ссылку — у действующего и у нового пользователя.
	if err := s.SuggestBegin(ctx, a, until); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.StartUser(ctx, a); err != nil {
		t.Fatal(err)
	}
	if suggest() {
		t.Fatal("StartUser (действующий) не снял отметку ссылки")
	}
	const fresh = 6061
	if err := s.SuggestBegin(ctx, fresh, until); err != nil {
		t.Fatal(err)
	}
	if _, _, offered, err := s.StartUser(ctx, fresh); err != nil || !offered {
		t.Fatalf("StartUser (новый): %v, %v", offered, err)
	}
	if _, ok, _ := s.SuggestAwaiting(ctx, fresh); ok {
		t.Fatal("StartUser (новый) не снял отметку ссылки")
	}

	// Вопрос о городе снимает ссылку; изменение без отметки города — нет.
	if err := s.SuggestBegin(ctx, a, until); err != nil {
		t.Fatal(err)
	}
	_, v, _, err := s.PeekProfile(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	v = updateProfile(t, s, a, v, func(p Profile) Profile { p.Gender = "f"; return p })
	if !suggest() {
		t.Fatal("ответ без отметки города снял отметку ссылки")
	}
	updateProfile(t, s, a, v, func(p Profile) Profile { p.AwaitingCityUntil = until; p.AwaitingCityMode = "e"; return p })
	if suggest() || !city() {
		t.Fatalf("вопрос о городе: ссылка %v, город %v", suggest(), city())
	}
}

func updateProfile(t *testing.T, s *Store, tgID int64, v int, f func(Profile) Profile) int {
	t.Helper()
	_, nv, err := s.UpdateProfileAt(context.Background(), tgID, v, time.Now(), func(p Profile) (Profile, error) { return f(p), nil })
	if err != nil {
		t.Fatalf("UpdateProfileAt: %v", err)
	}
	return nv
}

// TestPruneReports — заявки и предложения старше срока удаляются, свежие
// и отметки ожидания остаются.
func TestPruneReports(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	before := now.Add(-90 * 24 * time.Hour)
	e := sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	const rep = `INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES ($1, $2, $3, $4)`
	exec(t, rep, 1, e.ID, "date", before.Add(-time.Second))
	exec(t, rep, 1, e.ID, "place", before)
	const sug = `INSERT INTO start_suggestions (tg_id, created_at) VALUES ($1, $2)`
	exec(t, sug, 1, before.Add(-time.Hour))
	exec(t, sug, 1, now)
	exec(t, `INSERT INTO suggest_awaiting (tg_id, expires_at) VALUES (1, $1)`, before.Add(-time.Hour))

	n, err := s.PruneReports(ctx, before)
	if err != nil || n != 2 {
		t.Fatalf("PruneReports = %d, %v; want 2", n, err)
	}
	if got := scalar[string](t, "SELECT string_agg(reason, ',') FROM error_reports"); got != "place" {
		t.Errorf("остались заявки %q, want place", got)
	}
	if got := scalar[int64](t, "SELECT count(*) FROM start_suggestions"); got != 1 {
		t.Errorf("осталось предложений %d, want 1", got)
	}
	if got := scalar[int64](t, "SELECT count(*) FROM suggest_awaiting"); got != 1 {
		t.Errorf("отметок %d, want 1", got)
	}
}

// TestQuality — сводка /quality: отбор будущих видимых событий, строки
// «источник · вид спорта», уникальность по Q-5, полнота по D-03, свежесть и
// заявки за окно.
func TestQuality(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	enableSports(t, event.SportRunning, event.SportSkiing)
	today := mustDateFixed("2026-10-01")
	since := time.Now().Add(-30 * 24 * time.Hour)

	ev := func(title string, f func(*event.Event)) *event.Event {
		e := sampleEvent()
		e.Title = title
		if f != nil {
			f(e)
		}
		return e
	}
	// Полное событие одного источника.
	full, _ := linked(t, s, "marathonec", "1", ev("Полный", nil))
	// Слитое из двух источников: в обеих строках, не уникальное, одно в Total.
	win, _ := linked(t, s, "marathonec", "2", ev("Победитель", func(e *event.Event) { e.RegistrationURL = "" }))
	lose, _ := linked(t, s, "probeg", "2", ev("Проигравший", nil))
	exec(t, "UPDATE source_events SET event_id = $1 WHERE event_id = $2", win, lose)
	exec(t, "UPDATE events SET merged_into_id = $1, is_listed = false WHERE id = $2", win, lose)
	// Запись marathonec в gone: событие только probeg, но не уникальное (Q-5).
	gone, _ := linked(t, s, "probeg", "3", ev("С пропавшей записью", func(e *event.Event) { e.City = "" }))
	_, goneRow := linked(t, s, "marathonec", "3", ev("Пропавшая запись", nil))
	exec(t, "UPDATE source_events SET event_id = $1, state = 'gone' WHERE id = $2", gone, goneRow)
	// Две записи одного источника — уникальное (Q-5).
	twin, _ := linked(t, s, "probeg", "4", ev("Две записи", func(e *event.Event) { e.Distances = nil }))
	_, twinRow := linked(t, s, "probeg", "5", ev("Вторая запись", nil))
	exec(t, "UPDATE source_events SET event_id = $1 WHERE id = $2", twin, twinRow)
	// Лыжи с датой-заглушкой, без ссылки и дистанций.
	linked(t, s, "tristyle", "1", ev("Гонка (дата уточняется)", func(e *event.Event) {
		e.Sport, e.Type, e.Distances, e.RegistrationURL = event.SportSkiing, event.TypeSki, nil, "ftp://x"
	}))
	// Не в сводке: прошедшее, скрытое, выключенный вид спорта.
	linked(t, s, "marathonec", "6", ev("Прошедшее", func(e *event.Event) { e.StartDate, e.EndDate = mustDateFixed("2026-09-30"), nil }))
	hidden, _ := linked(t, s, "marathonec", "7", ev("Скрытое", nil))
	exec(t, "UPDATE events SET is_listed = false WHERE id = $1", hidden)
	linked(t, s, "marathonec", "8", ev("Велогонка", func(e *event.Event) { e.Sport = event.SportCycling }))

	// Прогоны: у marathonec последний — failed, суммы — по ok за окно.
	src := func(code string) int64 {
		return scalar[int64](t, "SELECT id FROM sources WHERE code = $1", code)
	}
	finishedRun(t, s, src("marathonec"), RunFinish{Status: "ok", Metrics: []byte(`{"created":2,"changed":3}`)})
	old := finishedRun(t, s, src("marathonec"), RunFinish{Status: "ok", Metrics: []byte(`{"created":100,"changed":100}`)})
	exec(t, "UPDATE ingest_runs SET finished_at = now() - interval '31 days' WHERE id = $1", old)
	finishedRun(t, s, src("marathonec"), RunFinish{Status: "suspect", Metrics: []byte(`{"created":50}`)})
	finishedRun(t, s, src("marathonec"), RunFinish{Status: "failed"})
	if _, err := s.EnsureSource(ctx, "zeta"); err != nil {
		t.Fatal(err)
	}

	// Заявки и предложения: старые в окно не идут.
	exec(t, `INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES
		(1, $1, 'date', now()), (2, $1, 'date', now()), (1, $1, 'link', now()), (3, $1, 'place', now() - interval '31 days')`, full)
	exec(t, `INSERT INTO start_suggestions (tg_id, created_at) VALUES (1, now()), (1, now() - interval '31 days')`)

	q, err := s.Quality(ctx, today, since)
	if err != nil {
		t.Fatal(err)
	}
	if q.Total != 5 {
		t.Errorf("Total = %d, want 5", q.Total)
	}
	want := []QualityRow{
		{Source: "marathonec", Sport: event.SportRunning, Events: 2, Unique: 1, Complete: 1,
			Dated: 2, WithCity: 2, WithDistance: 2, WithLink: 1},
		{Source: "probeg", Sport: event.SportRunning, Events: 3, Unique: 1, Complete: 0,
			Dated: 3, WithCity: 2, WithDistance: 2, WithLink: 2},
		{Source: "tristyle", Sport: event.SportSkiing, Events: 1, Unique: 1, Complete: 0,
			Dated: 0, WithCity: 1, WithDistance: 0, WithLink: 0},
	}
	if !reflect.DeepEqual(q.Rows, want) {
		t.Errorf("Rows:\n%+v\nwant\n%+v", q.Rows, want)
	}

	if len(q.Sources) != 4 {
		t.Fatalf("Sources = %+v", q.Sources)
	}
	m := q.Sources[0]
	if m.Code != "marathonec" || !m.HasRun || m.Status != "failed" || m.FinishedAt.IsZero() || m.Created != 2 || m.Changed != 3 {
		t.Errorf("marathonec = %+v", m)
	}
	if z := q.Sources[3]; z.Code != "zeta" || z.HasRun || z.Created != 0 || z.Changed != 0 {
		t.Errorf("zeta = %+v", z)
	}
	if !reflect.DeepEqual(q.Reports, map[string]int{"date": 2, "link": 1}) {
		t.Errorf("Reports = %v", q.Reports)
	}
	if q.Suggestions != 1 {
		t.Errorf("Suggestions = %d, want 1", q.Suggestions)
	}

	// Пустой каталог: нули без ошибки.
	s = testStore(t)
	q, err = s.Quality(ctx, today, since)
	if err != nil || q.Total != 0 || len(q.Rows) != 0 || len(q.Sources) != 0 || len(q.Reports) != 0 || q.Suggestions != 0 {
		t.Fatalf("пустая сводка = %+v, %v", q, err)
	}
}
