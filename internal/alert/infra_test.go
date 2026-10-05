package alert

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"aggregator/internal/heartbeat"
	"aggregator/internal/store/storetest"
)

// infraFix — цикл на фейковых часах и отметках из карты.
type infraFix struct {
	l    *InfraLoop
	rec  *recorder
	now  time.Time
	ages map[string]time.Duration
	err  error
	logs *bytes.Buffer
}

func newInfra(watch []Watch, dbKey string) *infraFix {
	f := &infraFix{rec: &recorder{}, now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), logs: &bytes.Buffer{}}
	f.ages = map[string]time.Duration{"worker": time.Second, "backup": time.Hour, "backup_copy": time.Hour, "bot": time.Second}
	f.l = &InfraLoop{
		Ages:  func(context.Context) (map[string]time.Duration, error) { return f.ages, f.err },
		Watch: watch, DBKey: dbKey, Admins: []int64{101, 102}, Send: f.rec.send,
		Now: func() time.Time { return f.now },
		Log: slog.New(slog.NewTextHandler(f.logs, nil)),
	}
	return f
}

// tick двигает часы на d, делает тик и возвращает отправленные тексты.
func (f *infraFix) tick(d time.Duration) []string {
	f.now = f.now.Add(d)
	f.l.Tick(context.Background())
	var out []string
	for _, c := range f.rec.take() {
		out = append(out, c.text)
	}
	return out
}

// one требует ровно по одному сообщению каждому из двух администраторов с
// подстроками want.
func one(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != 2 || got[0] != got[1] {
		t.Fatalf("сообщения = %q, want одно каждому из двух администраторов", got)
	}
	for _, w := range want {
		if !strings.Contains(got[0], w) {
			t.Fatalf("в сообщении %q нет %q", got[0], w)
		}
	}
}

func silent(t *testing.T, got []string, what string) {
	t.Helper()
	if len(got) != 0 {
		t.Fatalf("%s: отправлено %q, want ничего", what, got)
	}
}

func TestInfraDecide(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		bad  bool
		st   *infraState
		want action
	}{
		{"свежая без состояния", false, nil, none},
		{"просрочена без состояния", true, nil, send},
		{"просрочена, ещё не доставлено", true, &infraState{}, send},
		{"оповещён, 23:59", true, &infraState{notified: true, remindedAt: now.Add(-24*time.Hour + time.Minute)}, none},
		{"оповещён, 24 ч", true, &infraState{notified: true, remindedAt: now.Add(-24 * time.Hour)}, remind},
		{"снова свежая и оповещён", false, &infraState{notified: true, remindedAt: now}, reset},
		{"до времени следующей попытки", true, &infraState{fails: 1, nextTry: now.Add(time.Second)}, none},
		{"восстановление до времени следующей попытки", false, &infraState{notified: true, fails: 1, nextTry: now.Add(time.Second)}, none},
		{"время следующей попытки настало", true, &infraState{fails: 1, nextTry: now}, send},
	} {
		if got := infraDecide(c.bad, c.st, now); got != c.want {
			t.Errorf("%s: infraDecide = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestInfraBackoff(t *testing.T) {
	for fails, want := range map[int]time.Duration{0: 0, 1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute,
		4: 8 * time.Minute, 5: 15 * time.Minute, 6: 15 * time.Minute, 100: 15 * time.Minute} {
		if got := backoff(fails); got != want {
			t.Errorf("backoff(%d) = %v, want %v", fails, got, want)
		}
	}
}

func TestInfraLoop(t *testing.T) {
	f := newInfra(BotWatch(), KeyPostgresDown)
	silent(t, f.tick(time.Minute), "всё свежее")

	// Ровно на пороге — не просрочка.
	f.ages["backup"], f.ages["worker"] = 26*time.Hour, 10*time.Minute
	silent(t, f.tick(time.Minute), "ровно 26 ч и ровно 10 мин")

	// Локальная и внешняя копии — два независимых алерта (D-12).
	f.ages["backup"] = 27 * time.Hour
	one(t, f.tick(time.Minute), "backup_stale", "27 ч", "порог 26 ч")
	silent(t, f.tick(time.Minute), "тот же алерт на следующем тике")
	f.ages["backup_copy"] = 27 * time.Hour
	got := f.tick(time.Minute)
	one(t, got, "backup_copy_stale")
	if strings.Contains(got[0], "(backup_stale)") {
		t.Fatalf("алерт внешней копии повторил локальную: %q", got[0])
	}

	// Строки нет — просрочка.
	delete(f.ages, "worker")
	one(t, f.tick(time.Minute), "worker_stale", "отметки нет")

	// Напоминание раз в 24 часа, по каждому ключу своё.
	silent(t, f.tick(23*time.Hour), "до суток")
	got = f.tick(time.Hour)
	if len(got) != 6 {
		t.Fatalf("напоминаний %d, want 6 (три ключа, два администратора): %q", len(got), got)
	}
	for _, text := range got {
		if !strings.HasPrefix(text, "Напоминание: ") {
			t.Fatalf("нет префикса напоминания: %q", text)
		}
	}
	silent(t, f.tick(time.Hour), "после напоминания")

	// Возврат — одно «Восстановлено» с ключом.
	f.ages["worker"] = time.Second
	got = f.tick(time.Minute)
	one(t, got, "Восстановлено: worker_stale")
	silent(t, f.tick(time.Minute), "после восстановления")
}

func TestInfraLoopBackoff(t *testing.T) {
	f := newInfra([]Watch{{KeyWorkerStale, "worker", serviceMaxAge}}, "")
	f.ages["worker"] = 11 * time.Minute
	failing := true
	f.rec.fail = func(int64) bool { return failing }

	// Отправка не удалась обоим: повторы строго по ряду 1, 2, 4, 8, 15, 15.
	if got := f.tick(time.Minute); len(got) != 2 {
		t.Fatalf("первая попытка: %d вызовов, want 2", len(got))
	}
	for _, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute} {
		silent(t, f.tick(wait-time.Second), "до срока "+wait.String())
		if got := f.tick(time.Second); len(got) != 2 {
			t.Fatalf("срок %v: %d вызовов, want 2", wait, len(got))
		}
	}
	if n := strings.Count(f.logs.String(), "infra alert send failed"); n != 1 {
		t.Fatalf("записей WARN о неудаче: %d, want 1\n%s", n, f.logs)
	}
	if strings.Contains(f.logs.String(), "telegram недоступен") {
		t.Fatalf("в логе текст ошибки отправки:\n%s", f.logs)
	}

	// Дошло одному из двух — доставлено, повторов нет.
	f.rec.fail = func(chat int64) bool { return chat == 101 }
	if got := f.tick(15 * time.Minute); len(got) != 2 {
		t.Fatalf("после срока: %d вызовов, want 2", len(got))
	}
	if !strings.Contains(f.logs.String(), "infra alert delivered") {
		t.Fatalf("нет записи о доставке:\n%s", f.logs)
	}
	silent(t, f.tick(time.Hour), "после доставки одному")

	// Отметка вернулась до первой доставки — состояние снято без сообщения.
	f = newInfra([]Watch{{KeyWorkerStale, "worker", serviceMaxAge}}, "")
	f.ages["worker"] = 11 * time.Minute
	f.rec.fail = func(int64) bool { return true }
	f.tick(time.Minute)
	f.ages["worker"], f.rec.fail = time.Second, nil
	silent(t, f.tick(time.Hour), "возврат до первой доставки")
	silent(t, f.tick(time.Hour), "после возврата")
}

func TestInfraPostgresDown(t *testing.T) {
	f := newInfra(BotWatch(), KeyPostgresDown)
	delete(f.ages, "worker")
	one(t, f.tick(time.Minute), "worker_stale")

	f.err = errors.New("pq: пароль не подошёл к 10.0.0.1")
	f.ages["worker"] = time.Second // пока БД недоступна, это не должно быть замечено
	silent(t, f.tick(time.Minute), "первая ошибка опроса")
	silent(t, f.tick(time.Minute), "вторая ошибка опроса")
	got := f.tick(time.Minute)
	one(t, got, "postgres_down")
	if strings.Contains(got[0], "pq:") || strings.Contains(got[0], "10.0.0.1") {
		t.Fatalf("в алерте текст ошибки БД: %q", got[0])
	}
	silent(t, f.tick(time.Minute), "БД всё ещё недоступна")

	// БД отвечает: «Восстановлено» и обычное вычисление остальных ключей.
	f.err = nil
	got = f.tick(time.Minute)
	if len(got) != 4 || !strings.Contains(strings.Join(got, "|"), "Восстановлено: postgres_down") ||
		!strings.Contains(strings.Join(got, "|"), "Восстановлено: worker_stale") {
		t.Fatalf("после возврата БД: %q", got)
	}

	// Две ошибки и ответ — счётчик сброшен, алерта нет.
	f.err = errors.New("x")
	f.tick(time.Minute)
	f.tick(time.Minute)
	f.err = nil
	silent(t, f.tick(time.Minute), "ответ после двух ошибок")
	f.err = errors.New("x")
	silent(t, f.tick(time.Minute), "снова первая ошибка")
}

func TestInfraLogOnly(t *testing.T) {
	for name, set := range map[string]func(l *InfraLoop){
		"Send не задан": func(l *InfraLoop) { l.Send = nil },
		"Admins пуст":   func(l *InfraLoop) { l.Admins = nil },
	} {
		f := newInfra(WorkerWatch(), "")
		set(f.l)
		f.ages["bot"] = 11 * time.Minute
		silent(t, f.tick(time.Minute), name)
		silent(t, f.tick(time.Minute), name)
		if n := strings.Count(f.logs.String(), "key=bot_stale"); n != 1 {
			t.Fatalf("%s: записей с ключом bot_stale %d, want 1 (переход — одна запись)\n%s", name, n, f.logs)
		}
		f.ages["bot"] = time.Second
		f.tick(time.Minute)
		if !strings.Contains(f.logs.String(), "Восстановлено: bot_stale") {
			t.Fatalf("%s: нет записи о восстановлении\n%s", name, f.logs)
		}
		// Пустой DBKey: недоступность БД только подавляет остальные ключи.
		f.err, f.ages["bot"] = errors.New("x"), time.Hour
		before := f.logs.Len()
		for range 5 {
			f.tick(time.Minute)
		}
		if f.logs.Len() != before {
			t.Fatalf("%s: при недоступной БД и пустом DBKey появились записи\n%s", name, f.logs)
		}
	}
}

// Tracer (MON-03): heartbeat.Run → store.Beat → строка heartbeats →
// store.HeartbeatAges → InfraLoop → сообщение worker_stale.
func TestInfraTracer(t *testing.T) {
	s := storetest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go heartbeat.Run(ctx, func(ctx context.Context) error { return s.Beat(ctx, "worker") }, time.Hour, nil)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if ages, err := s.HeartbeatAges(ctx); err == nil && len(ages) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("отметка worker не появилась")
		}
	}

	rec := &recorder{}
	l := &InfraLoop{Ages: s.HeartbeatAges, Watch: []Watch{{KeyWorkerStale, "worker", serviceMaxAge}}, Admins: []int64{101, 102}, Send: rec.send}
	l.Tick(ctx)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("при свежей отметке отправлено %+v", got)
	}
	storetest.AgeHeartbeat(t, "worker", 11*time.Minute)
	l.Tick(ctx)
	got := rec.take()
	if len(got) != 2 || got[0].chat != 101 || got[1].chat != 102 || !strings.Contains(got[0].text, "worker_stale") || got[0].accept != 0 {
		t.Fatalf("после 11 минут: %+v", got)
	}
	l.Tick(ctx)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("повторный тик отправил %+v", got)
	}
	t.Logf("TRACER infra: %s", got[0].text)
}
