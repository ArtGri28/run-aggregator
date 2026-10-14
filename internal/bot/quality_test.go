package bot

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// Сводка /quality (16-UX-CONTRACT.md, сценарий S-56). Каталог и дата — как в
// Phase 14 (reportBot); администратор 1. Эталоны s56_* написаны вручную по
// контракту.

// qualityStep — /quality от пользователя from: ровно одно сообщение обычным текстом.
func qualityStep(t *testing.T, b *Bot, api *fakeAPI, from int64) call {
	t.Helper()
	calls := feed(b, api, commandUpdate(from, "/quality"))
	if methods(calls) != "sendMessage" {
		t.Fatalf("/quality от %d: вызовы %q", from, methods(calls))
	}
	noParseMode(t, calls[0])
	return calls[0]
}

// qualityRow — строка сводки по источнику и виду спорта (нулевая — строки нет).
func qualityRow(q store.Quality, src, sport string) store.QualityRow {
	for _, r := range q.Rows {
		if r.Source == src && string(r.Sport) == sport {
			return r
		}
	}
	return store.QualityRow{}
}

// TestS56Quality — S-56: сводка качества каталога.
func TestS56Quality(t *testing.T) {
	s, api, b := reportBot(t, Config{Token: "123:TEST", Admins: []int64{1}})
	ctx := context.Background()
	b.Now = func() time.Time { return fixtureNow.Add(3 * time.Hour) }

	// 1. Две заявки и одно предложение в окне; заявка и предложение старше 30 дней
	// в счёт не идут.
	dbExec(t, `INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES
		(900001, 110, 'date', $1), (900002, 110, 'date', $1), (900003, 110, 'link', $2)`,
		fixtureNow, fixtureNow.Add(-31*24*time.Hour))
	dbExec(t, `INSERT INTO start_suggestions (tg_id, created_at) VALUES (900001, $1), (900002, $2)`,
		fixtureNow, fixtureNow.Add(-31*24*time.Hour))
	// Прогоны: у marathonec два ok (суммы за окно), последний у tristyle — failed
	// (счётчиков нет), у probeg прогонов нет.
	dbExec(t, `UPDATE ingest_runs SET started_at = $1, finished_at = $1`, fixtureNow)
	dbExec(t, `INSERT INTO ingest_runs (source_id, started_at, finished_at, status, metrics)
		SELECT id, $1, $1, 'ok', '{"created": 2, "changed": 4}' FROM sources WHERE code = 'marathonec'`, fixtureNow.Add(time.Hour))
	dbExec(t, `INSERT INTO ingest_runs (source_id, started_at, finished_at, status, error)
		SELECT id, $1, $1, 'failed', 'сбой' FROM sources WHERE code = 'tristyle'`, fixtureNow.Add(170*time.Minute))
	dbExec(t, `INSERT INTO sources (code) VALUES ('probeg')`)
	msg := qualityStep(t, b, api, 1)
	matchSnapshot(t, "s56_quality", renderCall(t, msg))
	text := msg.Form["text"]
	for _, id := range []string{"110", "900001", "900002"} {
		if strings.Contains(text, id) {
			t.Errorf("шаг 1: в сводке %q", id)
		}
	}

	// 3. Не администратор — ответ неизвестной команды.
	matchSnapshot(t, "s05_command", renderCall(t, qualityStep(t, b, api, 42)))

	// 4. Слитое событие двух источников: одно в «Всего», по одному в строке каждого
	// источника, уникальным не считается; запись в gone не делает событие
	// событием источника.
	before, err := s.Quality(ctx, b.today(), b.now().Add(-qualityWindow))
	if err != nil {
		t.Fatal(err)
	}
	const ski = 135
	dbExec(t, `UPDATE source_events SET event_id = 110 WHERE event_id = $1`, ski)
	storetest.MergeEvent(t, ski, 110)
	after, err := s.Quality(ctx, b.today(), b.now().Add(-qualityWindow))
	if err != nil {
		t.Fatal(err)
	}
	mBefore, mAfter := qualityRow(before, "marathonec", "running"), qualityRow(after, "marathonec", "running")
	if after.Total != before.Total-1 || mAfter.Events != mBefore.Events || mAfter.Unique != mBefore.Unique-1 ||
		qualityRow(after, "tristyle", "skiing").Events != qualityRow(before, "tristyle", "skiing").Events-1 {
		t.Errorf("шаг 4: до %+v, после %+v", before, after)
	}
	if r := qualityRow(after, "tristyle", "running"); r.Events != 1 || r.Unique != 0 {
		t.Errorf("шаг 4: tristyle · бег %+v", r)
	}
	dbExec(t, `UPDATE source_events SET state = 'gone' WHERE event_id = 110 AND source_id = (SELECT id FROM sources WHERE code = 'tristyle')`)
	gone, err := s.Quality(ctx, b.today(), b.now().Add(-qualityWindow))
	if err != nil {
		t.Fatal(err)
	}
	if r := qualityRow(gone, "tristyle", "running"); r.Events != 0 || gone.Total != after.Total ||
		qualityRow(gone, "marathonec", "running").Unique != mAfter.Unique {
		t.Errorf("шаг 4: после gone %+v", gone)
	}

	// 2. Пустой каталог, прогонов нет, заявок нет.
	dbExec(t, `TRUNCATE sources, ingest_runs, source_events, events, event_distances, dedup_candidates,
		error_reports, start_suggestions RESTART IDENTITY CASCADE`)
	dbExec(t, `INSERT INTO sources (code) VALUES ('marathonec'), ('tristyle')`)
	matchSnapshot(t, "s56_empty", renderCall(t, qualityStep(t, b, api, 1)))

	// Возраст прогона и доли.
	for d, want := range map[time.Duration]string{-time.Minute: "0 мин", 59 * time.Minute: "59 мин", time.Hour: "1 ч",
		47*time.Hour + 59*time.Minute: "47 ч", 48 * time.Hour: "2 дн", 100 * time.Hour: "4 дн"} {
		if got := runAge(d); got != want {
			t.Errorf("runAge(%v) = %q, want %q", d, got, want)
		}
	}
	for _, c := range [][3]int{{0, 7, 0}, {1, 3, 33}, {2, 3, 66}, {99, 100, 99}, {7, 7, 100}} {
		if got := share(c[0], c[1]); got != strconv.Itoa(c[2])+"%" {
			t.Errorf("share(%d, %d) = %q", c[0], c[1], got)
		}
	}
}
