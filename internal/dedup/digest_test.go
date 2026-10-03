package dedup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"aggregator/internal/event"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

func TestDigestDue(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, msk) }
	tests := []struct {
		name      string
		now, last time.Time
		at        string
		want      bool
	}{
		{"до слота", at(5, 9, 59), time.Time{}, "10:00", false},
		{"ровно в слот", at(5, 10, 0), time.Time{}, "10:00", true},
		{"вечером", at(5, 23, 59), time.Time{}, "10:00", true},
		{"уже сегодня", at(5, 10, 30), at(5, 10, 1), "10:00", false},
		{"последняя вчера", at(5, 10, 30), at(4, 10, 0), "10:00", true},
		{"утро до слота", at(6, 9, 0), at(5, 10, 0), "10:00", false},
		{"now в UTC", time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC), time.Time{}, "10:00", true},
		{"другое время", at(5, 9, 30), at(4, 9, 30), "09:30", true},
	}
	for _, tc := range tests {
		got, err := digestDue(tc.now, tc.last, tc.at, msk)
		if err != nil || got != tc.want {
			t.Errorf("%s: digestDue = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	if _, err := digestDue(at(5, 10, 0), time.Time{}, "25:00", msk); err == nil {
		t.Error("At 25:00: нет ошибки")
	}
}

type digestSent struct {
	chat int64
	text string
}

// digestRecorder — фиктивная Send; fail(chat) решает, вернуть ли ошибку.
type digestRecorder struct {
	mu    sync.Mutex
	calls []digestSent
	fail  func(chat int64) bool
}

func (r *digestRecorder) send(_ context.Context, chat int64, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, digestSent{chat, text})
	if r.fail != nil && r.fail(chat) {
		return errors.New("telegram недоступен")
	}
	return nil
}

func (r *digestRecorder) take() []digestSent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

var digestSeq int

// digestCandidate — два события (Марафонец и ПроБЕГ) и pending-кандидат
// между ними.
func digestCandidate(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	var ids [2]int64
	for i, code := range []string{"marathonec", "probeg"} {
		src, err := s.EnsureSource(ctx, code)
		if err != nil {
			t.Fatal(err)
		}
		run, err := s.StartRun(ctx, src, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FinishRun(ctx, run, store.RunFinish{Status: "ok"}); err != nil {
			t.Fatal(err)
		}
		digestSeq++
		row, _, err := s.UpsertSourceEvent(ctx, src, run, fmt.Sprint(digestSeq), fmt.Sprintf("https://example.org/%s/%d", code, digestSeq), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		e := event.Event{Title: "Забег", TitleNorm: "zabeg", StartDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			CountryCode: "RU", Type: event.TypeRoad, Status: event.StatusScheduled}
		if _, _, err := s.SaveEventFor(ctx, row, &e, event.FoldConfig{}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			ids[0] = e.ID
		} else {
			ids[1] = row
		}
	}
	if cid, err := s.UpsertCandidate(ctx, ids[1], ids[0], 0.6, []byte(`{}`)); err != nil || cid == 0 {
		t.Fatalf("UpsertCandidate = %d, %v", cid, err)
	}
}

func TestDigestTick(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	// notified_at ставят часы БД: база — реальное время, сдвиг — смена дня.
	base, shift := time.Now(), time.Duration(0)
	rec := &digestRecorder{}
	l := &DigestLoop{Store: s, Admins: []int64{101, 102}, Send: rec.send, Loc: time.UTC, At: "00:00",
		Now: func() time.Time { return base.Add(shift) }}
	tick := func(what string, wantSends int, parts ...string) {
		t.Helper()
		if err := l.Tick(ctx); err != nil {
			t.Fatalf("%s: Tick: %v", what, err)
		}
		calls := rec.take()
		if len(calls) != wantSends {
			t.Fatalf("%s: отправок %d, want %d: %+v", what, len(calls), wantSends, calls)
		}
		for _, c := range calls {
			for _, p := range parts {
				if !strings.Contains(c.text, p) {
					t.Fatalf("%s: в тексте нет %q: %q", what, p, c.text)
				}
			}
		}
	}
	fresh := func(want int) {
		t.Helper()
		if _, f, err := s.CandidateCounts(ctx); err != nil || f != want {
			t.Fatalf("fresh = %d, %v; want %d", f, err, want)
		}
	}

	tick("без кандидатов", 0)
	digestCandidate(t, s)
	digestCandidate(t, s)
	tick("два новых", 2, "ожидают решения 2", "новых 2")
	if p, f, _ := s.CandidateCounts(ctx); p != 2 || f != 0 {
		t.Fatalf("после сводки: pending %d, fresh %d", p, f)
	}

	digestCandidate(t, s)
	tick("тот же день", 0)
	// Перезапуск: новый цикл без памяти о прошлой отправке.
	l2 := *l
	l2.sent = time.Time{}
	if err := l2.Tick(ctx); err != nil || len(rec.take()) != 0 {
		t.Fatalf("перезапуск в тот же день: %v", err)
	}

	shift = 24 * time.Hour
	tick("следующий день", 2, "ожидают решения 3", "новых 1")
	shift = 48 * time.Hour
	tick("без новых", 0)

	digestCandidate(t, s)
	shift = 72 * time.Hour
	rec.fail = func(int64) bool { return true }
	tick("Telegram недоступен", 2)
	fresh(1)
	rec.fail = func(chat int64) bool { return chat == 101 }
	tick("частичный успех", 2)
	fresh(0)
	tick("после частичного успеха", 0)

	// Проход удалил всех отмеченных pending: LastDigestAt откатился, но
	// процесс помнит сегодняшнюю отправку.
	if _, err := s.PrunePendingCandidates(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if last, err := s.LastDigestAt(ctx); err != nil || !last.IsZero() {
		t.Fatalf("LastDigestAt после чистки = %v, %v", last, err)
	}
	digestCandidate(t, s)
	tick("после чистки очереди", 0)
}

func TestDigestNoAdmins(t *testing.T) {
	rec := &digestRecorder{}
	l := &DigestLoop{Send: rec.send} // Store nil: чтение БД упало бы
	if err := l.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	l.Run(context.Background()) // возврат сразу
	if calls := rec.take(); len(calls) != 0 {
		t.Fatalf("вызовы без администраторов: %+v", calls)
	}
}

// TestDigestMarksOnlyCounted — L2: кандидат, созданный между подсчётом и
// отметкой сводки, остаётся новым до следующей сводки.
func TestDigestMarksOnlyCounted(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()
	digestCandidate(t, s)
	added := false
	l := &DigestLoop{Store: s, Admins: []int64{101}, Loc: time.UTC, At: "00:00", Now: time.Now,
		Send: func(context.Context, int64, string) error {
			if !added {
				added = true
				digestCandidate(t, s) // появился во время отправки
			}
			return nil
		}}
	if err := l.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if p, f, err := s.CandidateCounts(ctx); err != nil || p != 2 || f != 1 {
		t.Fatalf("после сводки pending %d, fresh %d, %v; want 2, 1 — новый кандидат не входил в сводку", p, f, err)
	}
}
