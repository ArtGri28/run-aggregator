package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/migrations"
)

// TestBroadcasts — рассылки: получатели объявления и приглашения, отбор по
// городу и видам спорта, журнал и строки очереди одной транзакцией, условие
// перед отправкой, итог и стирание текста, переключатели без profile_v; Down
// до 14 убирает таблицу и колонки.
func TestBroadcasts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	exec(t, `INSERT INTO users (tg_id) VALUES (1), (42), (43), (44), (45)`)
	exec(t, `INSERT INTO user_profiles (tg_id, status, residence_country_code, residence_city, residence_city_key, sports) VALUES
		(42, 'completed', 'RU', 'Тула', 'tula', '{run}'),
		(43, 'completed', 'RU', 'Тула', 'tula', '{swim}'),
		(44, 'in_progress', 'RU', 'Тула', 'tula', '{run}'),
		(1, 'completed', NULL, NULL, NULL, '{}')`)
	count := func(kind string, f BroadcastFilter) int {
		t.Helper()
		n, err := s.CountRecipients(ctx, kind, f)
		must(err)
		return n
	}
	run := BroadcastFilter{Sports: []string{"run"}}
	if n := count(BroadcastAnnounce, BroadcastFilter{}); n != 5 {
		t.Errorf("объявление: %d, want 5 (все, включая пользователя без профиля)", n)
	}
	if n := count(BroadcastInvite, run); n != 0 {
		t.Errorf("приглашение без согласий: %d", n)
	}

	// Согласие: включить нельзя без города и видов спорта (1) и без завершённой анкеты (44).
	for _, c := range []struct {
		id   int64
		want bool
	}{{42, true}, {43, true}, {44, false}, {1, false}, {45, false}} {
		ok, err := s.SetInvite(ctx, c.id, true, now)
		if must(err); ok != c.want {
			t.Errorf("SetInvite(%d) = %v, want %v", c.id, ok, c.want)
		}
	}
	if v := scalar[int32](t, `SELECT profile_v FROM user_profiles WHERE tg_id = 42`); v != 1 {
		t.Errorf("согласие изменило profile_v: %d", v)
	}
	if p, _ := s.NotifyPrefs(ctx, 42); !p.Invite || p.AnnounceOff || !p.Eligible {
		t.Errorf("NotifyPrefs(42) = %+v", p)
	}
	if p, _ := s.NotifyPrefs(ctx, 45); p != (NotifyPrefs{}) {
		t.Errorf("NotifyPrefs без профиля = %+v", p)
	}
	for _, c := range []struct {
		f    BroadcastFilter
		want int
	}{
		{run, 1},
		{BroadcastFilter{Sports: []string{"run", "swim"}}, 2},
		{BroadcastFilter{Country: "RU", CityKey: "tula"}, 2},
		{BroadcastFilter{Country: "BY", CityKey: "tula"}, 0},
		{BroadcastFilter{Country: "RU", CityKey: "tula", Sports: []string{"swim"}}, 1},
	} {
		if n := count(BroadcastInvite, c.f); n != c.want {
			t.Errorf("приглашение %+v: %d, want %d", c.f, n, c.want)
		}
	}

	// Отказ от объявлений — и у пользователя без строки профиля.
	must(s.SetAnnounceOff(ctx, 43, true, now))
	must(s.SetAnnounceOff(ctx, 45, true, now))
	if n := count(BroadcastAnnounce, BroadcastFilter{}); n != 3 {
		t.Errorf("объявление после отказов: %d, want 3", n)
	}
	if n := count(BroadcastInvite, BroadcastFilter{Sports: []string{"swim"}}); n != 1 {
		t.Errorf("отказ от объявлений выключил приглашения: %d", n)
	}

	// Рассылка: журнал и строки очереди.
	if _, ok, _ := s.LastAnnounce(ctx); ok {
		t.Error("объявлений ещё не было")
	}
	id, n, err := s.CreateBroadcast(ctx, BroadcastAnnounce, 1, BroadcastFilter{}, "<b>Новое</b>", now)
	if must(err); id == 0 || n != 3 {
		t.Fatalf("CreateBroadcast: id %d, n %d", id, n)
	}
	if at, ok, _ := s.LastAnnounce(ctx); !ok || !at.Equal(now) {
		t.Errorf("LastAnnounce = %v %v", at, ok)
	}
	if got := scalar[string](t, `SELECT filter::text || '|' || recipients::text FROM broadcasts WHERE id = $1`, id); got != `{}|3` {
		t.Errorf("журнал: %s", got)
	}
	if none, nn, err := s.CreateBroadcast(ctx, BroadcastInvite, 1, BroadcastFilter{Sports: []string{"row"}}, "x", now); err != nil || none != 0 || nn != 0 ||
		scalar[int64](t, `SELECT count(*) FROM broadcasts`) != 1 {
		t.Errorf("без получателей: id %d n %d %v", none, nn, err)
	}

	if kind, text, ok, _ := s.BroadcastSend(ctx, id, 42); kind != BroadcastAnnounce || text != "<b>Новое</b>" || !ok {
		t.Errorf("BroadcastSend(42) = %q %q %v", kind, text, ok)
	}
	must(s.SetAnnounceOff(ctx, 42, true, now))
	if _, _, ok, _ := s.BroadcastSend(ctx, id, 42); ok {
		t.Error("отказ после записи в очередь: условие выполняется")
	}

	// Итог: пока есть ожидающие строки — не завершена.
	if n, _ := s.FinishBroadcasts(ctx, now); n != 0 {
		t.Errorf("завершена при ожидающих строках: %d", n)
	}
	exec(t, `UPDATE outbox SET claimed_at = $1, sent_at = $1 WHERE kind = 'broadcast' AND tg_id = 1`, now)
	exec(t, `UPDATE outbox SET claimed_at = $1, done_at = $1, outcome = 'stale' WHERE kind = 'broadcast' AND tg_id = 42`, now)
	exec(t, `UPDATE outbox SET claimed_at = $1 WHERE kind = 'broadcast' AND tg_id = 44`, now) // исход неизвестен
	if n, err := s.FinishBroadcasts(ctx, now); err != nil || n != 1 {
		t.Fatalf("FinishBroadcasts = %d, %v", n, err)
	}
	if got := scalar[string](t, `SELECT tg_id::text || ' ' || payload::text FROM outbox WHERE kind = 'broadcast_done'`); got != `1 {"b": 1, "kind": "announce", "sent": 1, "failed": 2}` {
		t.Errorf("итог: %s", got)
	}
	if got := scalar[string](t, `SELECT (text IS NULL)::text || (finished_at IS NOT NULL)::text FROM broadcasts WHERE id = $1`, id); got != "truetrue" {
		t.Errorf("текст не стёрт: %s", got)
	}
	if n, _ := s.FinishBroadcasts(ctx, now); n != 0 {
		t.Errorf("повторное завершение: %d", n)
	}
	if _, text, _, _ := s.BroadcastSend(ctx, id, 44); text != "" {
		t.Errorf("текст завершённой рассылки: %q", text)
	}

	// Ответ в конце анкеты: версия и статус.
	if err := s.AnswerInviteAt(ctx, 43, 2, false, now); err != ErrStaleProfile {
		t.Errorf("чужая версия: %v", err)
	}
	if err := s.AnswerInviteAt(ctx, 44, 1, true, now); err != ErrStaleProfile {
		t.Errorf("незавершённая анкета: %v", err)
	}
	must(s.AnswerInviteAt(ctx, 43, 1, false, now))
	if p, _ := s.NotifyPrefs(ctx, 43); p.Invite || !p.AnnounceOff {
		t.Errorf("после ответа «Нет»: %+v", p)
	}

	// Режимы ввода.
	exec(t, `UPDATE user_profiles SET awaiting_city_until = $2, awaiting_city_mode = 'e' WHERE tg_id = $1`, 42, now.Add(time.Minute))
	must(s.SupportBegin(ctx, 1, now.Add(time.Minute)))
	for _, id := range []int64{42, 1} {
		if ok, _ := s.Awaiting(ctx, id, now); !ok {
			t.Errorf("Awaiting(%d) = false", id)
		}
		must(s.DropAwaiting(ctx, id))
		if ok, _ := s.Awaiting(ctx, id, now); ok {
			t.Errorf("после DropAwaiting(%d) отметка осталась", id)
		}
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
	_, err = p.DownTo(ctx, 14)
	must(err)
	if n := scalar[int64](t, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename = 'broadcasts'`); n != 0 {
		t.Error("после Down таблица broadcasts осталась")
	}
	if n := scalar[int64](t, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'user_profiles' AND column_name LIKE '%opt_%'`); n != 0 {
		t.Errorf("после Down колонок согласия: %d", n)
	}
}
