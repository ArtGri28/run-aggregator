package alert

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

func TestDecide(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	state := func(s string, ago time.Duration) *store.AlertState {
		return &store.AlertState{State: s, RemindedAt: now.Add(-ago)}
	}
	run := func(status, errText string) store.FinishedRun {
		return store.FinishedRun{Status: status, Error: errText, FinishedAt: now.Add(-time.Hour)}
	}
	tests := []struct {
		name string
		run  store.FinishedRun
		st   *store.AlertState
		want action
	}{
		{"ok без состояния", run("ok", ""), nil, none},
		{"ok с состоянием", run("ok", ""), state("failed", time.Hour), reset},
		{"прерванный без состояния", run("failed", store.RunInterrupted), nil, none},
		{"прерванный при состоянии", run("failed", store.RunInterrupted), state("failed", 48*time.Hour), none},
		{"suspect впервые", run("suspect", "x"), nil, send},
		{"failed впервые", run("failed", "x"), nil, send},
		{"failed после suspect", run("failed", "x"), state("suspect", time.Minute), send},
		{"suspect после failed", run("suspect", "x"), state("failed", time.Minute), send},
		{"suspect держится 23:59", run("suspect", "x"), state("suspect", 24*time.Hour-time.Minute), none},
		{"suspect держится 24 ч", run("suspect", "x"), state("suspect", 24*time.Hour), remind},
		{"failed держится 30 ч", run("failed", "x"), state("failed", 30*time.Hour), remind},
		{"новый suspect-прогон", store.FinishedRun{Status: "suspect", RunID: 2, FinishedAt: now}, &store.AlertState{State: "suspect", RunID: 1, RemindedAt: now}, send},
		{"тот же suspect-прогон", store.FinishedRun{Status: "suspect", RunID: 1, FinishedAt: now}, &store.AlertState{State: "suspect", RunID: 1, RemindedAt: now}, none},
		{"выключенный источник: failed старше 72 ч", store.FinishedRun{Status: "failed", Error: "x", FinishedAt: now.Add(-72 * time.Hour)}, state("failed", 30*time.Hour), none},
		{"выключенный источник без состояния", store.FinishedRun{Status: "failed", Error: "x", FinishedAt: now.Add(-100 * time.Hour)}, nil, none},
		{"failed 71 ч назад ещё напоминается", store.FinishedRun{Status: "failed", Error: "x", FinishedAt: now.Add(-71 * time.Hour)}, state("failed", 30*time.Hour), remind},
		{"новый failed-прогон до суток", store.FinishedRun{Status: "failed", RunID: 2, Error: "x", FinishedAt: now}, &store.AlertState{State: "failed", RunID: 1, RemindedAt: now.Add(-time.Hour)}, none},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(tc.run, tc.st, now); got != tc.want {
				t.Errorf("decide = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMessage(t *testing.T) {
	fin := time.Date(2026, 9, 29, 21, 30, 0, 0, time.UTC) // 00:30 МСК следующего дня
	suspect := store.FinishedRun{
		Code: "marathonec", RunID: 7, Status: "suspect", FinishedAt: fin, Error: "первый прогон <источника>",
		Parsed: 133, Rejected: 1, OutOfScope: 0, Unrecognized: 2, HasSnapshot: true,
	}
	text, accept := message(suspect, 0, false, moscow(t))
	for _, want := range []string{
		"marathonec", "прогон 7", "suspect", "первый прогон <источника>",
		"parsed 133 · rejected 1 · out_of_scope 0 · unrecognized_region 2",
		"30.09 00:30 МСК", "worker accept-baseline --source marathonec", "кнопкой",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в тексте нет %q:\n%s", want, text)
		}
	}
	if accept != 7 {
		t.Errorf("acceptRunID = %d, want 7", accept)
	}
	if strings.HasPrefix(text, "Напоминание:") {
		t.Errorf("обычное сообщение начинается с напоминания:\n%s", text)
	}

	noSnap := suspect
	noSnap.HasSnapshot = false
	if _, accept := message(noSnap, 0, false, time.UTC); accept != 0 {
		t.Errorf("suspect без снимка: acceptRunID = %d, want 0", accept)
	}

	failed := store.FinishedRun{Code: "marathonec", RunID: 9, Status: "failed", FinishedAt: fin, Error: "загрузка: timeout", HasSnapshot: true}
	text, accept = message(failed, 3, false, time.UTC)
	if accept != 0 || !strings.Contains(text, "Сбоев подряд: 3") || !strings.Contains(text, "загрузка: timeout") ||
		strings.Contains(text, "parsed") {
		t.Errorf("failed: accept = %d, текст:\n%s", accept, text)
	}

	text, _ = message(failed, 3, true, time.UTC)
	if !strings.HasPrefix(text, "Напоминание: ") {
		t.Errorf("напоминание без префикса:\n%s", text)
	}
}

type sent struct {
	chat   int64
	text   string
	accept int64
}

// recorder — фиктивная Send: пишет вызовы; fail(chat) решает, вернуть ли ошибку.
type recorder struct {
	mu    sync.Mutex
	calls []sent
	fail  func(chat int64) bool
}

func (r *recorder) send(_ context.Context, chat int64, text string, accept int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sent{chat, text, accept})
	if r.fail != nil && r.fail(chat) {
		return errors.New("telegram недоступен")
	}
	return nil
}

func (r *recorder) take() []sent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

func finish(t *testing.T, s *store.Store, sourceID int64, f store.RunFinish) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := s.StartRun(ctx, sourceID, time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := s.FinishRun(ctx, id, f); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	return id
}

// moscow — часовой пояс, который бот передаёт в Loop.Loc.
func moscow(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func admins(t *testing.T, rec *recorder, s *store.Store, now *time.Time) *Loop {
	t.Helper()
	return &Loop{
		Store: s, Admins: []int64{101, 102}, Send: rec.send,
		Now: func() time.Time { return *now }, Loc: moscow(t),
	}
}

func alertState(t *testing.T, s *store.Store, src int64) (store.AlertState, bool) {
	t.Helper()
	m, err := s.AlertStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, ok := m[src]
	return st, ok
}

func TestTick(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	rec := &recorder{}
	l := admins(t, rec, s, &now)
	tick := func() []sent {
		t.Helper()
		if err := l.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return rec.take()
	}

	if calls := tick(); len(calls) != 0 {
		t.Fatalf("пустая БД: вызовы %+v", calls)
	}

	// suspect со снимком: сообщение каждому администратору с кнопкой.
	run := finish(t, s, src, store.RunFinish{Status: "suspect", Parsed: 133, Error: "первый прогон", Snapshot: []byte(`[]`)})
	calls := tick()
	if len(calls) != 2 || calls[0].chat != 101 || calls[1].chat != 102 {
		t.Fatalf("suspect: вызовы %+v", calls)
	}
	for _, c := range calls {
		if c.accept != run || !strings.Contains(c.text, "suspect") || strings.HasPrefix(c.text, "Напоминание:") {
			t.Errorf("suspect: %+v", c)
		}
	}
	if st, ok := alertState(t, s, src); !ok || st.State != "suspect" || st.RunID != run || !st.NotifiedAt.Equal(now) || !st.RemindedAt.Equal(now) {
		t.Fatalf("source_alerts после suspect: %+v, %v", st, ok)
	}

	// Повторный тик — тишина.
	if calls := tick(); len(calls) != 0 {
		t.Fatalf("повторный тик: %+v", calls)
	}

	// Через сутки — напоминание обоим; NotifiedAt прежний.
	first := now
	now = now.Add(24 * time.Hour)
	calls = tick()
	if len(calls) != 2 {
		t.Fatalf("напоминание: %+v", calls)
	}
	for _, c := range calls {
		if !strings.HasPrefix(c.text, "Напоминание: ") || c.accept != run {
			t.Errorf("напоминание: %+v", c)
		}
	}
	if st, _ := alertState(t, s, src); !st.NotifiedAt.Equal(first) || !st.RemindedAt.Equal(now) {
		t.Errorf("времена после напоминания: %+v", st)
	}

	// Новый прогон failed — смена состояния.
	failed := finish(t, s, src, store.RunFinish{Status: "failed", Error: "загрузка: timeout"})
	calls = tick()
	if len(calls) != 2 || !strings.Contains(calls[0].text, "Сбоев подряд: 1") || calls[0].accept != 0 {
		t.Fatalf("failed: %+v", calls)
	}
	if st, _ := alertState(t, s, src); st.State != "failed" || st.RunID != failed {
		t.Errorf("source_alerts после failed: %+v", st)
	}

	// Прерванный остановкой прогон — тишина, состояние на месте.
	finish(t, s, src, store.RunFinish{Status: "failed", Error: store.RunInterrupted})
	if calls := tick(); len(calls) != 0 {
		t.Fatalf("прерванный прогон: %+v", calls)
	}
	if _, ok := alertState(t, s, src); !ok {
		t.Error("прерванный прогон снял состояние")
	}

	// Возврат в ok — состояние снято без сообщения.
	finish(t, s, src, store.RunFinish{Status: "ok", Parsed: 133})
	if calls := tick(); len(calls) != 0 {
		t.Fatalf("возврат в ok: %+v", calls)
	}
	if _, ok := alertState(t, s, src); ok {
		t.Error("после ok строка source_alerts осталась")
	}
}

// Регрессия LO-02: второй suspect-прогон подряд даёт новый алерт с кнопкой
// нового прогона, а не ждёт суточного напоминания; повтор того же — тишина.
func TestTickNewSuspectRun(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	rec := &recorder{}
	l := admins(t, rec, s, &now)
	tick := func() []sent {
		t.Helper()
		if err := l.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return rec.take()
	}

	first := finish(t, s, src, store.RunFinish{Status: "suspect", Parsed: 134, Error: "первый прогон", Snapshot: []byte(`[]`)})
	if calls := tick(); len(calls) != 2 || calls[0].accept != first {
		t.Fatalf("первый suspect: %+v", calls)
	}
	now = now.Add(6 * time.Hour)
	second := finish(t, s, src, store.RunFinish{Status: "suspect", Parsed: 135, Error: "первый прогон", Snapshot: []byte(`[]`)})
	calls := tick()
	if len(calls) != 2 {
		t.Fatalf("второй suspect-прогон: вызовы %+v, want 2", calls)
	}
	for _, c := range calls {
		if c.accept != second || strings.HasPrefix(c.text, "Напоминание:") || !strings.Contains(c.text, "(прогон "+strconv.FormatInt(second, 10)+")") {
			t.Errorf("второй suspect-прогон: %+v", c)
		}
	}
	if st, _ := alertState(t, s, src); st.RunID != second || !st.NotifiedAt.Equal(now) {
		t.Errorf("source_alerts после второго suspect: %+v", st)
	}
	if calls := tick(); len(calls) != 0 {
		t.Fatalf("повторный тик по тому же прогону: %+v", calls)
	}
}

func TestTickInterruptedFirst(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	src, _ := s.EnsureSource(ctx, "marathonec")
	finish(t, s, src, store.RunFinish{Status: "failed", Error: store.RunInterrupted})
	now := time.Now()
	rec := &recorder{}
	if err := admins(t, rec, s, &now).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := rec.take(); len(calls) != 0 {
		t.Fatalf("прерванный прогон без состояния: %+v", calls)
	}
}

func TestTickSendFailure(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	src, _ := s.EnsureSource(ctx, "marathonec")
	finish(t, s, src, store.RunFinish{Status: "failed", Error: "сбой"})
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	// Telegram недоступен всем: состояние не записано, следующий тик повторяет.
	rec := &recorder{fail: func(int64) bool { return true }}
	l := admins(t, rec, s, &now)
	for i := 1; i <= 2; i++ {
		if err := l.Tick(ctx); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		if calls := rec.take(); len(calls) != 2 {
			t.Fatalf("тик %d: попыток %d, want 2", i, len(calls))
		}
		if _, ok := alertState(t, s, src); ok {
			t.Fatalf("тик %d: состояние записано без успешной отправки", i)
		}
	}

	// Один администратор недоступен, другой получил: состояние записано.
	rec.fail = func(chat int64) bool { return chat == 101 }
	if err := l.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := rec.take(); len(calls) != 2 {
		t.Fatalf("частичный успех: %+v", calls)
	}
	if st, ok := alertState(t, s, src); !ok || st.State != "failed" {
		t.Fatalf("после частичного успеха: %+v, %v", st, ok)
	}
	if err := l.Tick(ctx); err != nil || len(rec.take()) != 0 {
		t.Fatal("после записи состояния тик должен молчать")
	}
}

// IN-03: ошибка записи состояния по одному источнику не останавливает обход.
func TestTickContinuesAfterError(t *testing.T) {
	s := storetest.Open(t)
	bg := context.Background()
	for _, code := range []string{"marathonec", "probeg"} {
		src, err := s.EnsureSource(bg, code)
		if err != nil {
			t.Fatal(err)
		}
		finish(t, s, src, store.RunFinish{Status: "suspect", Error: "сбой " + code})
	}
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	var mu sync.Mutex
	var texts []string
	l := &Loop{Store: s, Admins: []int64{101}, Now: time.Now,
		Send: func(_ context.Context, _ int64, text string, _ int64) error {
			mu.Lock()
			defer mu.Unlock()
			texts = append(texts, text)
			cancel() // запись состояния первого источника упадёт
			return nil
		}}
	err := l.Tick(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Tick = %v, want context.Canceled", err)
	}
	all := strings.Join(texts, "\n")
	if len(texts) != 2 || !strings.Contains(all, "сбой marathonec") || !strings.Contains(all, "сбой probeg") {
		t.Fatalf("отправки: %q; want по одной на каждый источник", texts)
	}
}

func TestNoAdmins(t *testing.T) {
	rec := &recorder{}
	l := &Loop{Admins: nil, Send: rec.send} // Store nil: чтение БД упало бы
	if err := l.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	l.Run(context.Background()) // возврат сразу
	if calls := rec.take(); len(calls) != 0 {
		t.Fatalf("вызовы без администраторов: %+v", calls)
	}
}

func TestRunStops(t *testing.T) {
	s := storetest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, _ := s.EnsureSource(ctx, "marathonec")
	finish(t, s, src, store.RunFinish{Status: "failed", Error: "сбой"})

	got := make(chan struct{}, 4)
	l := &Loop{
		Store: s, Admins: []int64{101}, Every: time.Hour, // повторных тиков в тесте нет: первый — сразу
		Send: func(context.Context, int64, string, int64) error { got <- struct{}{}; return nil },
	}
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()

	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("первый тик не выполнен сразу")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run не завершился после отмены ctx")
	}
}
