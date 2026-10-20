package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
)

var testStorePkg *Store

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open test store:", err)
		os.Exit(1)
	}
	// TRUNCATE в тестах уничтожил бы данные dev-БД (LO-11, D-03):
	// работаем только с базой, имя которой оканчивается на _test.
	var dbName string
	if err := s.pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		fmt.Fprintln(os.Stderr, "test store: current_database:", err)
		os.Exit(1)
	}
	if !strings.HasSuffix(dbName, "_test") {
		fmt.Fprintln(os.Stderr, "TEST_DATABASE_URL: имя базы должно оканчиваться на _test")
		os.Exit(1)
	}
	if err := s.Migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "migrate test store:", err)
		os.Exit(1)
	}
	testStorePkg = s
	code := m.Run()
	s.Close()
	os.Exit(code)
}

// testStore skips the calling test when TEST_DATABASE_URL is not set, and
// otherwise truncates every table (Pitfall 6) so each test starts clean.
func testStore(t *testing.T) *Store {
	t.Helper()
	if testStorePkg == nil {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	const q = `TRUNCATE TABLE sources, ingest_runs, source_events, events,
		event_distances, dedup_candidates, user_profiles, users, heartbeats, source_alerts,
		support_messages, support_awaiting, error_reports, start_suggestions, suggest_awaiting,
		user_days, usage_counters, subscriptions
		RESTART IDENTITY CASCADE`
	if _, err := testStorePkg.pool.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return testStorePkg
}

func TestEnsureSource(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	id1, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatalf("EnsureSource: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("EnsureSource id = %d, want > 0", id1)
	}
	id1Again, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatalf("EnsureSource (repeat): %v", err)
	}
	if id1Again != id1 {
		t.Fatalf("EnsureSource repeat id = %d, want %d", id1Again, id1)
	}

	id2, err := s.EnsureSource(ctx, "probeg")
	if err != nil {
		t.Fatalf("EnsureSource (probeg): %v", err)
	}
	if id2 == id1 {
		t.Fatalf("EnsureSource(probeg) id = %d, want different from marathonec id %d", id2, id1)
	}
}

func TestUpsertSourceEvent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	sourceID, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatalf("EnsureSource: %v", err)
	}
	run1 := newRun(t, s, sourceID)
	run2 := newRun(t, s, sourceID)

	id, changed, err := s.UpsertSourceEvent(ctx, sourceID, run1, "60817", "https://example.test/60817", []byte(`{"title":"A"}`))
	if err != nil {
		t.Fatalf("UpsertSourceEvent (insert): %v", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d, want > 0", id)
	}
	if !changed {
		t.Fatalf("changed = false on first insert, want true")
	}

	var firstSeen1, lastSeen1 time.Time
	var hash1 string
	row := testStorePkg.pool.QueryRow(ctx, "SELECT first_seen_at, last_seen_at, content_hash FROM source_events WHERE id = $1", id)
	if err := row.Scan(&firstSeen1, &lastSeen1, &hash1); err != nil {
		t.Fatalf("scan after insert: %v", err)
	}
	if got := lastRunID(t, id); got != run1 {
		t.Fatalf("last_run_id after insert = %d, want %d", got, run1)
	}

	time.Sleep(10 * time.Millisecond)

	idAgain, changed, err := s.UpsertSourceEvent(ctx, sourceID, run2, "60817", "https://example.test/60817", []byte(`{"title":"A"}`))
	if err != nil {
		t.Fatalf("UpsertSourceEvent (same payload): %v", err)
	}
	if idAgain != id {
		t.Fatalf("id on repeat = %d, want %d", idAgain, id)
	}
	if changed {
		t.Fatalf("changed = true on unchanged payload, want false")
	}

	var firstSeen2, lastSeen2 time.Time
	var hash2 string
	row = testStorePkg.pool.QueryRow(ctx, "SELECT first_seen_at, last_seen_at, content_hash FROM source_events WHERE id = $1", id)
	if err := row.Scan(&firstSeen2, &lastSeen2, &hash2); err != nil {
		t.Fatalf("scan after repeat: %v", err)
	}
	if !firstSeen2.Equal(firstSeen1) {
		t.Fatalf("first_seen_at changed on unchanged payload: %v -> %v", firstSeen1, firstSeen2)
	}
	if hash2 != hash1 {
		t.Fatalf("content_hash changed on unchanged payload: %v -> %v", hash1, hash2)
	}
	if lastSeen2.Before(lastSeen1) {
		t.Fatalf("last_seen_at went backwards: %v -> %v", lastSeen1, lastSeen2)
	}
	if got := lastRunID(t, id); got != run2 {
		t.Fatalf("last_run_id after unchanged repeat = %d, want %d", got, run2)
	}

	idChanged, changed, err := s.UpsertSourceEvent(ctx, sourceID, run2, "60817", "https://example.test/60817", []byte(`{"title":"B"}`))
	if err != nil {
		t.Fatalf("UpsertSourceEvent (changed payload): %v", err)
	}
	if idChanged != id {
		t.Fatalf("id on change = %d, want %d", idChanged, id)
	}
	if !changed {
		t.Fatalf("changed = false on modified payload, want true")
	}

	var hash3, payload3 string
	row = testStorePkg.pool.QueryRow(ctx, "SELECT content_hash, payload::text FROM source_events WHERE id = $1", id)
	if err := row.Scan(&hash3, &payload3); err != nil {
		t.Fatalf("scan after change: %v", err)
	}
	if hash3 == hash1 {
		t.Fatalf("content_hash unchanged after payload change")
	}
	if !strings.Contains(payload3, "B") {
		t.Fatalf("payload = %q, want it to contain the new title", payload3)
	}

	otherID, changed, err := s.UpsertSourceEvent(ctx, sourceID, run2, "60818", "https://example.test/60818", []byte(`{"title":"C"}`))
	if err != nil {
		t.Fatalf("UpsertSourceEvent (other external_id): %v", err)
	}
	if otherID == id {
		t.Fatalf("other external_id got the same id %d", otherID)
	}
	if !changed {
		t.Fatalf("changed = false for new external_id, want true")
	}

	var count int
	if err := testStorePkg.pool.QueryRow(ctx, "SELECT count(*) FROM source_events").Scan(&count); err != nil {
		t.Fatalf("count source_events: %v", err)
	}
	if count != 2 {
		t.Fatalf("source_events count = %d, want 2", count)
	}

	var state, matchState string
	row = testStorePkg.pool.QueryRow(ctx, "SELECT state, match_state FROM source_events WHERE id = $1", id)
	if err := row.Scan(&state, &matchState); err != nil {
		t.Fatalf("scan state: %v", err)
	}
	if state != "active" {
		t.Fatalf("state = %q, want active", state)
	}
	if matchState != "new" {
		t.Fatalf("match_state = %q, want new", matchState)
	}
}

func sampleEvent() *event.Event {
	end := mustDateFixed("2026-10-18")
	return &event.Event{
		Title:           "Санкт-Петербургский марафон",
		TitleNorm:       "peterburgskiy marafon",
		StartDate:       mustDateFixed("2026-10-17"),
		EndDate:         &end,
		StartTime:       "10:00",
		Timezone:        "Europe/Moscow",
		CountryCode:     "RU",
		Region:          "Северо-Запад",
		City:            "Санкт-Петербург",
		CityKey:         "saint-petersburg",
		Address:         "Дворцовая площадь",
		Type:            event.TypeRoad,
		Status:          event.StatusScheduled,
		Description:     "Ежегодный осенний марафон по центру города",
		RegistrationURL: "https://example.test/register",
		Distances: []event.Distance{
			{Meters: 5000, Label: ""},
			{Meters: 21100, Label: "21,1 км"},
		},
	}
}

func mustDateFixed(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

func TestSaveEvent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	e := sampleEvent()
	changed, err := s.SaveEvent(ctx, e)
	if err != nil {
		t.Fatalf("SaveEvent (insert): %v", err)
	}
	if !changed {
		t.Fatalf("changed = false on insert, want true")
	}
	if e.ID <= 0 {
		t.Fatalf("e.ID = %d, want > 0", e.ID)
	}

	got, err := s.GetEvent(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if got.Title != e.Title || got.TitleNorm != e.TitleNorm {
		t.Fatalf("title mismatch: got %+v", got)
	}
	if !got.StartDate.Equal(e.StartDate) {
		t.Fatalf("StartDate = %v, want %v", got.StartDate, e.StartDate)
	}
	if got.EndDate == nil || !got.EndDate.Equal(*e.EndDate) {
		t.Fatalf("EndDate = %v, want %v", got.EndDate, e.EndDate)
	}
	if got.StartTime != e.StartTime {
		t.Fatalf("StartTime = %q, want %q", got.StartTime, e.StartTime)
	}
	if got.Timezone != e.Timezone {
		t.Fatalf("Timezone = %q, want %q", got.Timezone, e.Timezone)
	}
	if got.CountryCode != e.CountryCode || got.Region != e.Region || got.City != e.City || got.CityKey != e.CityKey || got.Address != e.Address {
		t.Fatalf("place mismatch: got %+v", got)
	}
	if got.Type != e.Type || got.Status != e.Status {
		t.Fatalf("type/status mismatch: got %+v", got)
	}
	if got.Description != e.Description || got.RegistrationURL != e.RegistrationURL {
		t.Fatalf("description/url mismatch: got %+v", got)
	}
	if !got.IsListed {
		t.Fatalf("IsListed = false, want true")
	}
	if got.MergedIntoID != nil {
		t.Fatalf("MergedIntoID = %v, want nil", got.MergedIntoID)
	}
	if len(got.Distances) != 2 {
		t.Fatalf("Distances = %+v, want 2 entries", got.Distances)
	}
	if got.Distances[0].Meters != 5000 || got.Distances[1].Meters != 21100 {
		t.Fatalf("Distances not sorted by meters: %+v", got.Distances)
	}
	if got.Distances[1].Label != "21,1 км" {
		t.Fatalf("Distances[1].Label = %q, want %q", got.Distances[1].Label, "21,1 км")
	}

	// Unchanged re-save: changed=false, UpdatedAt unchanged.
	unchanged := got
	changed, err = s.SaveEvent(ctx, &unchanged)
	if err != nil {
		t.Fatalf("SaveEvent (unchanged): %v", err)
	}
	if changed {
		t.Fatalf("changed = true on unchanged re-save, want false")
	}
	reGot, err := s.GetEvent(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEvent (after unchanged): %v", err)
	}
	if !reGot.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("UpdatedAt changed on unchanged re-save: %v -> %v", got.UpdatedAt, reGot.UpdatedAt)
	}

	// Change Title: changed=true, UpdatedAt grows.
	titleChanged := got
	titleChanged.Title = "Санкт-Петербургский марафон 2026"
	time.Sleep(10 * time.Millisecond)
	changed, err = s.SaveEvent(ctx, &titleChanged)
	if err != nil {
		t.Fatalf("SaveEvent (title change): %v", err)
	}
	if !changed {
		t.Fatalf("changed = false on title change, want true")
	}
	afterTitle, err := s.GetEvent(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEvent (after title change): %v", err)
	}
	if !afterTitle.UpdatedAt.After(got.UpdatedAt) {
		t.Fatalf("UpdatedAt did not grow after title change: %v -> %v", got.UpdatedAt, afterTitle.UpdatedAt)
	}

	// Change only distances: changed=true, GetEvent reflects new set.
	distChanged := afterTitle
	distChanged.Distances = []event.Distance{{Meters: 10000, Label: "10 км"}}
	changed, err = s.SaveEvent(ctx, &distChanged)
	if err != nil {
		t.Fatalf("SaveEvent (distance change): %v", err)
	}
	if !changed {
		t.Fatalf("changed = false on distance-only change, want true")
	}
	afterDist, err := s.GetEvent(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEvent (after distance change): %v", err)
	}
	if len(afterDist.Distances) != 1 || afterDist.Distances[0].Meters != 10000 {
		t.Fatalf("Distances after change = %+v, want [{10000 10 км}]", afterDist.Distances)
	}

	// Event with empty optional fields round-trips as empty/nil.
	minimal := &event.Event{
		Title:       "Забег выходного дня",
		TitleNorm:   "zabeg vyhodnogo dnya",
		StartDate:   mustDateFixed("2026-11-01"),
		CountryCode: "RU",
		Type:        event.TypeRoad,
		Status:      event.StatusScheduled,
	}
	changed, err = s.SaveEvent(ctx, minimal)
	if err != nil {
		t.Fatalf("SaveEvent (minimal): %v", err)
	}
	if !changed {
		t.Fatalf("changed = false on minimal insert, want true")
	}
	gotMinimal, err := s.GetEvent(ctx, minimal.ID)
	if err != nil {
		t.Fatalf("GetEvent (minimal): %v", err)
	}
	if gotMinimal.EndDate != nil {
		t.Fatalf("EndDate = %v, want nil", gotMinimal.EndDate)
	}
	if gotMinimal.StartTime != "" || gotMinimal.Timezone != "" || gotMinimal.Region != "" || gotMinimal.Address != "" || gotMinimal.Description != "" || gotMinimal.RegistrationURL != "" {
		t.Fatalf("optional fields not empty: %+v", gotMinimal)
	}
	if len(gotMinimal.Distances) != 0 {
		t.Fatalf("Distances = %+v, want empty", gotMinimal.Distances)
	}

	// Nonexistent ID.
	_, err = s.GetEvent(ctx, 999999999)
	if err == nil {
		t.Fatalf("GetEvent(nonexistent) err = nil, want ErrNotFound")
	}
	if !isNotFound(err) {
		t.Fatalf("GetEvent(nonexistent) err = %v, want errors.Is ErrNotFound", err)
	}

	missing := &event.Event{
		ID:          999999999,
		Title:       "x",
		TitleNorm:   "x",
		StartDate:   mustDateFixed("2026-11-01"),
		CountryCode: "RU",
		Type:        event.TypeRoad,
		Status:      event.StatusScheduled,
	}
	_, err = s.SaveEvent(ctx, missing)
	if err == nil {
		t.Fatalf("SaveEvent(nonexistent ID) err = nil, want ErrNotFound")
	}
	if !isNotFound(err) {
		t.Fatalf("SaveEvent(nonexistent ID) err = %v, want errors.Is ErrNotFound", err)
	}
}

func TestSetMatch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	sourceID, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatalf("EnsureSource: %v", err)
	}
	runID := newRun(t, s, sourceID)
	se1, _, err := s.UpsertSourceEvent(ctx, sourceID, runID, "1", "https://example.test/1", []byte(`{}`))
	if err != nil {
		t.Fatalf("UpsertSourceEvent 1: %v", err)
	}
	se2, _, err := s.UpsertSourceEvent(ctx, sourceID, runID, "2", "https://example.test/2", []byte(`{}`))
	if err != nil {
		t.Fatalf("UpsertSourceEvent 2: %v", err)
	}

	e := sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}

	if err := s.SetMatch(ctx, se1, &e.ID, event.MatchAuto); err != nil {
		t.Fatalf("SetMatch (auto): %v", err)
	}
	var eventID *int64
	var matchState string
	row := testStorePkg.pool.QueryRow(ctx, "SELECT event_id, match_state FROM source_events WHERE id = $1", se1)
	if err := row.Scan(&eventID, &matchState); err != nil {
		t.Fatalf("scan after SetMatch: %v", err)
	}
	if eventID == nil || *eventID != e.ID {
		t.Fatalf("event_id = %v, want %d", eventID, e.ID)
	}
	if matchState != "auto" {
		t.Fatalf("match_state = %q, want auto", matchState)
	}

	var eventsBefore int
	if err := testStorePkg.pool.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&eventsBefore); err != nil {
		t.Fatalf("count events: %v", err)
	}

	if err := s.SetMatch(ctx, se2, nil, event.MatchOutOfScope); err != nil {
		t.Fatalf("SetMatch (out_of_scope): %v", err)
	}
	row = testStorePkg.pool.QueryRow(ctx, "SELECT event_id, match_state FROM source_events WHERE id = $1", se2)
	if err := row.Scan(&eventID, &matchState); err != nil {
		t.Fatalf("scan after SetMatch out_of_scope: %v", err)
	}
	if eventID != nil {
		t.Fatalf("event_id = %v, want nil", eventID)
	}
	if matchState != "out_of_scope" {
		t.Fatalf("match_state = %q, want out_of_scope", matchState)
	}

	var eventsAfter int
	if err := testStorePkg.pool.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&eventsAfter); err != nil {
		t.Fatalf("count events (after): %v", err)
	}
	if eventsAfter != eventsBefore {
		t.Fatalf("events count changed: %d -> %d", eventsBefore, eventsAfter)
	}

	if err := s.SetMatch(ctx, 999999999, &e.ID, event.MatchAuto); err == nil || !isNotFound(err) {
		t.Fatalf("SetMatch(nonexistent) err = %v, want ErrNotFound", err)
	}

	if err := s.SetMatch(ctx, se1, &e.ID, event.MatchState("bogus")); err == nil {
		t.Fatalf("SetMatch(bogus state) err = nil, want CHECK violation")
	}
}

func TestEventConstraints(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	longDescription := strings.Repeat("а", 1001)

	tests := []struct {
		name string
		mod  func(e *event.Event)
	}{
		{"description too long", func(e *event.Event) { e.Description = longDescription }},
		{"bad country_code", func(e *event.Event) { e.CountryCode = "RUS" }},
		{"bad city_key", func(e *event.Event) { e.CityKey = "Moscow" }},
		{"bad status", func(e *event.Event) { e.Status = event.Status("unknown") }},
		{"bad type", func(e *event.Event) { e.Type = event.Type("Ultra") }}, // схема проверяет формат (миграция 00009)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before int
			if err := testStorePkg.pool.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&before); err != nil {
				t.Fatalf("count before: %v", err)
			}

			e := sampleEvent()
			tt.mod(e)
			_, err := s.SaveEvent(ctx, e)
			if err == nil {
				t.Fatalf("SaveEvent(%s) err = nil, want constraint violation", tt.name)
			}

			var after int
			if err := testStorePkg.pool.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&after); err != nil {
				t.Fatalf("count after: %v", err)
			}
			if after != before {
				t.Fatalf("events count changed on rejected insert (%s): %d -> %d", tt.name, before, after)
			}
		})
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// A caller passing non-canonical but equal values (MSK-midnight date,
// "9:05") must not see changed=true or a moving updated_at on re-save.
func TestSaveEventCanonicalRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	msk := time.FixedZone("MSK", 3*3600)

	build := func() *event.Event {
		e := sampleEvent()
		e.StartDate = time.Date(2026, 10, 17, 0, 0, 0, 0, msk)
		end := time.Date(2026, 10, 18, 9, 30, 0, 0, msk)
		e.EndDate = &end
		e.StartTime = "9:05"
		return e
	}

	e := build()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatalf("SaveEvent (insert): %v", err)
	}
	got, err := s.GetEvent(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if !got.StartDate.Equal(mustDateFixed("2026-10-17")) || !got.EndDate.Equal(mustDateFixed("2026-10-18")) || got.StartTime != "09:05" {
		t.Fatalf("stored %v / %v / %q, want 2026-10-17 / 2026-10-18 / 09:05", got.StartDate, *got.EndDate, got.StartTime)
	}

	for i := 0; i < 2; i++ {
		again := build()
		again.ID = e.ID
		changed, err := s.SaveEvent(ctx, again)
		if err != nil {
			t.Fatalf("SaveEvent (re-save %d): %v", i, err)
		}
		if changed {
			t.Fatalf("re-save %d: changed = true, want false", i)
		}
		if !again.UpdatedAt.Equal(got.UpdatedAt) {
			t.Fatalf("re-save %d: UpdatedAt %v -> %v", i, got.UpdatedAt, again.UpdatedAt)
		}
	}

	bad := build()
	bad.StartTime = "10:00:30"
	if _, err := s.SaveEvent(ctx, bad); err == nil {
		t.Fatalf("SaveEvent with StartTime %q: err = nil, want error", bad.StartTime)
	}
}

// scalar читает одно значение прямым запросом (подготовка и проверки
// тестов; SQL допустим только в internal/store).
func scalar[T any](t *testing.T, q string, args ...any) T {
	t.Helper()
	var v T
	if err := testStorePkg.pool.QueryRow(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := testStorePkg.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// newRun создаёт завершённый прогон ok — на него ссылается last_run_id.
func newRun(t *testing.T, s *Store, sourceID int64) int64 {
	t.Helper()
	return finishedRun(t, s, sourceID, RunFinish{Status: "ok"})
}

func finishedRun(t *testing.T, s *Store, sourceID int64, f RunFinish) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := s.StartRun(ctx, sourceID, time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := s.FinishRun(ctx, id, f); err != nil {
		t.Fatalf("FinishRun(%s): %v", f.Status, err)
	}
	return id
}

func lastRunID(t *testing.T, sourceEventID int64) int64 {
	t.Helper()
	return scalar[int64](t, "SELECT last_run_id FROM source_events WHERE id = $1", sourceEventID)
}

func TestSaveEventFor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	run := newRun(t, s, src)
	upsert := func(ext string) int64 {
		t.Helper()
		id, _, err := s.UpsertSourceEvent(ctx, src, run, ext, "https://example.test/"+ext, []byte(`{}`))
		if err != nil {
			t.Fatalf("UpsertSourceEvent %s: %v", ext, err)
		}
		return id
	}

	se := upsert("1")
	e := sampleEvent()
	created, changed, err := s.SaveEventFor(ctx, se, e, event.FoldConfig{})
	if err != nil || !created || changed {
		t.Fatalf("SaveEventFor (new) = %v, %v, %v; want created", created, changed, err)
	}
	if got := scalar[int64](t, "SELECT event_id FROM source_events WHERE id = $1", se); got != e.ID {
		t.Fatalf("event_id = %d, want %d", got, e.ID)
	}
	if got := scalar[string](t, "SELECT match_state FROM source_events WHERE id = $1", se); got != "auto" {
		t.Fatalf("match_state = %q, want auto", got)
	}
	before, err := s.GetEvent(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}

	again := sampleEvent()
	created, changed, err = s.SaveEventFor(ctx, se, again, event.FoldConfig{})
	if err != nil || created || changed || again.ID != e.ID {
		t.Fatalf("SaveEventFor (same) = %v, %v, %v, id %d; want unchanged event %d", created, changed, err, again.ID, e.ID)
	}
	after, err := s.GetEvent(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("updated_at moved on unchanged save: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}

	// Источник без явного статуса не сбрасывает cancelled (Pitfall 10).
	exec(t, "UPDATE events SET status = 'cancelled' WHERE id = $1", e.ID)
	if _, _, err := s.SaveEventFor(ctx, se, sampleEvent(), event.FoldConfig{}); err != nil {
		t.Fatalf("SaveEventFor (after cancel): %v", err)
	}
	if got := scalar[string](t, "SELECT status FROM events WHERE id = $1", e.ID); got != "cancelled" {
		t.Fatalf("status = %q, want cancelled", got)
	}

	// Бывшая out_of_scope запись получает событие.
	se2 := upsert("2")
	if err := s.SetOutOfScope(ctx, se2); err != nil {
		t.Fatal(err)
	}
	e2 := sampleEvent()
	if created, _, err := s.SaveEventFor(ctx, se2, e2, event.FoldConfig{}); err != nil || !created {
		t.Fatalf("SaveEventFor (out_of_scope) = %v, %v; want created", created, err)
	}
	if got := scalar[string](t, "SELECT match_state FROM source_events WHERE id = $1", se2); got != "auto" {
		t.Fatalf("match_state after out_of_scope = %q, want auto", got)
	}

	// Ошибка внутри транзакции откатывает и событие, и связь.
	se3 := upsert("3")
	events := scalar[int64](t, "SELECT count(*) FROM events")
	bad := sampleEvent()
	bad.Description = strings.Repeat("а", 1001)
	if _, _, err := s.SaveEventFor(ctx, se3, bad, event.FoldConfig{}); err == nil {
		t.Fatal("SaveEventFor (1001 chars) err = nil, want CHECK violation")
	}
	if got := scalar[int64](t, "SELECT count(*) FROM events"); got != events {
		t.Fatalf("events count %d -> %d after failed save", events, got)
	}
	if got := scalar[string](t, "SELECT match_state || ':' || (event_id IS NULL) FROM source_events WHERE id = $1", se3); got != "new:true" {
		t.Fatalf("link after failed save = %q, want new:true", got)
	}

	if _, _, err := s.SaveEventFor(ctx, 999999999, sampleEvent(), event.FoldConfig{}); !isNotFound(err) {
		t.Fatalf("SaveEventFor(nonexistent) err = %v, want ErrNotFound", err)
	}
}

func TestSetOutOfScope(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	run := newRun(t, s, src)
	linked := func(ext string) (int64, int64) {
		t.Helper()
		se, _, err := s.UpsertSourceEvent(ctx, src, run, ext, "https://example.test/"+ext, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		e := sampleEvent()
		if _, _, err := s.SaveEventFor(ctx, se, e, event.FoldConfig{}); err != nil {
			t.Fatal(err)
		}
		return se, e.ID
	}

	se1, ev1 := linked("1")
	exec(t, "UPDATE events SET status = 'postponed' WHERE id = $1", ev1)
	if err := s.SetOutOfScope(ctx, se1); err != nil {
		t.Fatalf("SetOutOfScope: %v", err)
	}
	if got := scalar[string](t, "SELECT match_state || ':' || (event_id IS NULL) FROM source_events WHERE id = $1", se1); got != "out_of_scope:false" {
		t.Fatalf("source event = %q, want out_of_scope:false (event_id kept, LO-12)", got)
	}
	if got := scalar[string](t, "SELECT is_listed || ':' || status FROM events WHERE id = $1", ev1); got != "false:postponed" {
		t.Fatalf("event = %q, want false:postponed", got)
	}

	// Событие с другой живой связью остаётся видимым.
	se2, ev2 := linked("2")
	se3, _, err := s.UpsertSourceEvent(ctx, src, run, "3", "https://example.test/3", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMatch(ctx, se3, &ev2, event.MatchAuto); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOutOfScope(ctx, se2); err != nil {
		t.Fatal(err)
	}
	if !scalar[bool](t, "SELECT is_listed FROM events WHERE id = $1", ev2) {
		t.Fatal("event with another linked source event got is_listed = false")
	}

	if err := s.SetOutOfScope(ctx, 999999999); !isNotFound(err) {
		t.Fatalf("SetOutOfScope(nonexistent) err = %v, want ErrNotFound", err)
	}
}

func TestRunLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LastRun(ctx, src); !isNotFound(err) {
		t.Fatalf("LastRun without runs err = %v, want ErrNotFound", err)
	}

	id, err := s.StartRun(ctx, src, time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	run, err := s.LastRun(ctx, src)
	if err != nil || run.ID != id || run.Status != "running" || run.FinishedAt != nil {
		t.Fatalf("LastRun after start = %+v, %v; want running %d", run, err, id)
	}

	snap := []byte(`[{"external_id":"1"}]`)
	f := RunFinish{Status: "suspect", Fetched: 10, Parsed: 10, Rejected: 1, OutOfScope: 2,
		Error: "первый прогон источника — требуется принятие", Metrics: []byte(`{"valid":9}`), Snapshot: snap}
	if err := s.FinishRun(ctx, id, f); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	run, err = s.LastRun(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "suspect" || run.FinishedAt == nil || run.Parsed != 10 || run.Upserted != 0 ||
		run.Error != f.Error || !strings.Contains(string(run.Snapshot), `"external_id"`) || run.AcceptedAt != nil {
		t.Fatalf("LastRun after finish = %+v", run)
	}
	if got := scalar[string](t, "SELECT fetched || '/' || rejected || '/' || out_of_scope || '/' || (metrics->>'valid') FROM ingest_runs WHERE id = $1", id); got != "10/1/2/9" {
		t.Fatalf("counters/metrics = %q, want 10/1/2/9", got)
	}
	if err := s.FinishRun(ctx, id, RunFinish{Status: "ok"}); !isNotFound(err) {
		t.Fatalf("FinishRun (finished run) err = %v, want ErrNotFound", err)
	}

	// Следующий завершённый прогон очищает снимок предыдущего (D-06).
	okID := finishedRun(t, s, src, RunFinish{Status: "ok", Snapshot: snap})
	if !scalar[bool](t, "SELECT snapshot IS NULL FROM ingest_runs WHERE id = $1", id) {
		t.Fatal("snapshot of the previous run was not cleared")
	}
	if got := scalar[string](t, "SELECT metrics::text FROM ingest_runs WHERE id = $1", okID); got != "{}" {
		t.Fatalf("nil metrics stored as %q, want {}", got)
	}
	if err := s.ClaimRun(ctx, id); !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("ClaimRun (old run) err = %v, want ErrNotAcceptable", err)
	}
	if err := s.ClaimRun(ctx, okID); !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("ClaimRun (ok run) err = %v, want ErrNotAcceptable", err)
	}
	noSnap := finishedRun(t, s, src, RunFinish{Status: "suspect"})
	if err := s.ClaimRun(ctx, noSnap); !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("ClaimRun (no snapshot) err = %v, want ErrNotAcceptable", err)
	}

	withSnap := finishedRun(t, s, src, RunFinish{Status: "suspect", Snapshot: snap})
	running, err := s.StartRun(ctx, src, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimRun(ctx, withSnap); !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("ClaimRun (not last) err = %v, want ErrNotAcceptable", err)
	}
	if err := s.FinishRun(ctx, running, RunFinish{Status: "suspect", Snapshot: snap, Error: strings.Repeat("я", 1500)}); err != nil {
		t.Fatal(err)
	}
	if got := scalar[int](t, "SELECT char_length(error) FROM ingest_runs WHERE id = $1", running); got != 1000 {
		t.Fatalf("error length = %d, want 1000", got)
	}

	note := "принят вручную; исходная причина: x"
	acceptRun(t, s, running, RunFinish{Status: "ok", Upserted: 7, Error: note})
	run, err = s.LastRun(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "ok" || run.AcceptedAt == nil || run.Snapshot != nil || run.Upserted != 7 || run.Error != note {
		t.Fatalf("LastRun after accept = %+v", run)
	}
	if err := s.ClaimRun(ctx, running); !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("ClaimRun (twice) err = %v, want ErrNotAcceptable", err)
	}
}

// acceptRun принимает прогон, как Runner.Accept: ClaimRun и FinishRun с
// Accepted одной транзакцией.
func acceptRun(t *testing.T, s *Store, runID int64, f RunFinish) {
	t.Helper()
	f.Accepted = true
	err := s.InTx(context.Background(), func(tx *Store) error {
		if err := tx.ClaimRun(context.Background(), runID); err != nil {
			return err
		}
		return tx.FinishRun(context.Background(), runID, f)
	})
	if err != nil {
		t.Fatalf("accept run %d: %v", runID, err)
	}
}

func TestStartRunExclusive(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.EnsureSource(ctx, "probeg")
	if err != nil {
		t.Fatal(err)
	}
	stale := 45 * time.Minute

	first, err := s.StartRun(ctx, src, time.Now(), stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRun(ctx, src, time.Now(), stale); !errors.Is(err, ErrRunInProgress) {
		t.Fatalf("second StartRun err = %v, want ErrRunInProgress", err)
	}
	if _, err := s.StartRun(ctx, other, time.Now(), stale); err != nil {
		t.Fatalf("StartRun (other source): %v", err)
	}

	exec(t, "UPDATE ingest_runs SET started_at = now() - interval '2 hours' WHERE id = $1", first)
	next, err := s.StartRun(ctx, src, time.Now(), stale)
	if err != nil || next == first {
		t.Fatalf("StartRun after stale = %d, %v; want a new run", next, err)
	}
	got := scalar[string](t, "SELECT status || '|' || error || '|' || (finished_at IS NOT NULL) FROM ingest_runs WHERE id = $1", first)
	if got != "failed|прерван: процесс остановился|true" {
		t.Fatalf("stale run = %q", got)
	}
}

func TestBaselineRuns(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	okRun := func(parsed int) {
		finishedRun(t, s, src, RunFinish{Status: "ok", Parsed: parsed, Metrics: []byte(fmt.Sprintf(`{"valid":%d}`, parsed))})
	}
	parsedOf := func(rows []BaselineRun) []int {
		var out []int
		for _, r := range rows {
			out = append(out, r.Parsed)
		}
		return out
	}

	if rows, err := s.BaselineRuns(ctx, src, 7); err != nil || len(rows) != 0 {
		t.Fatalf("BaselineRuns (no runs) = %v, %v", rows, err)
	}
	for i := 1; i <= 9; i++ {
		okRun(i)
	}
	finishedRun(t, s, src, RunFinish{Status: "failed", Parsed: 50})
	rows, err := s.BaselineRuns(ctx, src, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(parsedOf(rows)); got != "[9 8 7 6 5 4 3]" {
		t.Fatalf("baseline without accepted = %s, want [9 8 7 6 5 4 3]", got)
	}
	var m struct{ Valid int }
	if err := json.Unmarshal(rows[0].Metrics, &m); err != nil || m.Valid != 9 {
		t.Fatalf("metrics = %s, %v; want valid 9", rows[0].Metrics, err)
	}

	accepted := finishedRun(t, s, src, RunFinish{Status: "suspect", Parsed: 100, Snapshot: []byte(`[]`)})
	acceptRun(t, s, accepted, RunFinish{Status: "ok", Parsed: 100, Upserted: 100, Error: "принят вручную"})
	okRun(101)
	okRun(102)
	finishedRun(t, s, src, RunFinish{Status: "suspect", Parsed: 5})

	rows, err = s.BaselineRuns(ctx, src, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(parsedOf(rows)); got != "[102 101 100]" {
		t.Fatalf("baseline after accept = %s, want [102 101 100]", got)
	}
	rows, err = s.BaselineRuns(ctx, src, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(parsedOf(rows)); got != "[102 101]" {
		t.Fatalf("baseline limit 2 = %s, want [102 101]", got)
	}
}

func TestSourceHealth(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.SourceHealth(ctx, src)
	if err != nil || h.HasRuns {
		t.Fatalf("SourceHealth (no runs) = %+v, %v", h, err)
	}

	finishedRun(t, s, src, RunFinish{Status: "ok"})
	finishedRun(t, s, src, RunFinish{Status: "failed", Error: "загрузка: timeout"})
	finishedRun(t, s, src, RunFinish{Status: "failed", Error: "структура не найдена"})
	h, err = s.SourceHealth(ctx, src)
	if err != nil || !h.HasRuns || h.LastStatus != "failed" || h.LastReason != "структура не найдена" ||
		h.ConsecutiveFailures != 2 || h.LastStartedAt.IsZero() {
		t.Fatalf("SourceHealth = %+v, %v; want failed, 2 in a row", h, err)
	}

	finishedRun(t, s, src, RunFinish{Status: "suspect", Error: "первый прогон"})
	finishedRun(t, s, src, RunFinish{Status: "failed", Error: "parsed = 0"})
	if h, err = s.SourceHealth(ctx, src); err != nil || h.ConsecutiveFailures != 1 {
		t.Fatalf("SourceHealth after suspect = %+v, %v; want 1", h, err)
	}

	finishedRun(t, s, src, RunFinish{Status: "ok"})
	if h, err = s.SourceHealth(ctx, src); err != nil || h.LastStatus != "ok" || h.LastReason != "" || h.ConsecutiveFailures != 0 {
		t.Fatalf("SourceHealth after ok = %+v, %v", h, err)
	}
}

func TestObserve(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	today := dateOnly(time.Now())
	future, past := today.AddDate(0, 1, 0), today.AddDate(0, 0, -10)
	const goneRuns, goneAfter = 3, 72 * time.Hour

	upsert := func(run int64, ext string) int64 {
		t.Helper()
		id, _, err := s.UpsertSourceEvent(ctx, src, run, ext, "https://example.test/"+ext, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	run1 := newRun(t, s, src)
	ids, events := map[string]int64{}, map[string]int64{}
	for ext, date := range map[string]time.Time{"x": future, "z": future, "y": future, "p": past} {
		ids[ext] = upsert(run1, ext)
		e := sampleEvent()
		e.StartDate, e.EndDate = date, nil
		if _, _, err := s.SaveEventFor(ctx, ids[ext], e, event.FoldConfig{}); err != nil {
			t.Fatal(err)
		}
		events[ext] = e.ID
	}
	ids["o"] = upsert(run1, "o")
	if err := s.SetOutOfScope(ctx, ids["o"]); err != nil {
		t.Fatal(err)
	}
	exec(t, "UPDATE events SET status = 'cancelled' WHERE id = $1", events["x"])

	observe := func(seen ...string) ObserveStats {
		t.Helper()
		run := newRun(t, s, src)
		for _, ext := range seen {
			upsert(run, ext)
		}
		st, err := s.Observe(ctx, src, run, today, goneRuns, goneAfter)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		return st
	}
	state := func(ext string) string {
		t.Helper()
		return scalar[string](t, "SELECT state || ':' || missed_runs FROM source_events WHERE id = $1", ids[ext])
	}
	listed := func(ext string) string {
		t.Helper()
		return scalar[string](t, "SELECT is_listed || ':' || status FROM events WHERE id = $1", events[ext])
	}
	check := func(what string, got, want any) {
		t.Helper()
		if got != want {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}

	st := observe("y")
	check("stats run 2", st, ObserveStats{Missing: 2})
	check("x after run 2", state("x"), "missing:1")
	check("x event after run 2", listed("x"), "true:cancelled")

	observe("y")
	check("x after run 3", state("x"), "missing:2")
	exec(t, "UPDATE source_events SET last_seen_at = now() - interval '73 hours' WHERE id = $1", ids["x"])

	st = observe("y")
	check("stats run 4", st, ObserveStats{Missing: 1, Gone: 1, ListedChanged: 1})
	check("x after run 4", state("x"), "gone:3")
	check("x event after run 4", listed("x"), "false:cancelled")
	// 3 пропуска, но last_seen_at свежий — ещё не gone.
	check("z after run 4", state("z"), "missing:3")
	check("z event after run 4", listed("z"), "true:scheduled")
	// Прошедшее событие и запись без события наблюдение не трогает.
	check("p after run 4", state("p"), "active:0")
	check("o after run 4", state("o"), "active:0")

	st = observe("y", "x")
	check("stats run 5", st, ObserveStats{Seen: 1, Missing: 1, ListedChanged: 1})
	check("x after return", state("x"), "active:0")
	check("x event after return", listed("x"), "true:cancelled")
}

// LO-12: запись, ушедшая из каталога (out_of_scope) и вернувшаяся,
// получает своё прежнее событие, а не новое; пока она вне каталога,
// наблюдение событие не показывает и в missing её не переводит.
func TestOutOfScopeReturnReusesEvent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	today := dateOnly(time.Now())
	upsert := func(run int64, ext string) int64 {
		t.Helper()
		id, _, err := s.UpsertSourceEvent(ctx, src, run, ext, "https://example.test/"+ext, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	observe := func(seen ...string) {
		t.Helper()
		run := newRun(t, s, src)
		for _, ext := range seen {
			upsert(run, ext)
		}
		if _, err := s.Observe(ctx, src, run, today, 3, 72*time.Hour); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	se := upsert(newRun(t, s, src), "1")
	e := sampleEvent()
	e.StartDate, e.EndDate = today.AddDate(0, 1, 0), nil
	if _, _, err := s.SaveEventFor(ctx, se, e, event.FoldConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOutOfScope(ctx, se); err != nil {
		t.Fatal(err)
	}
	observe("1")
	if scalar[bool](t, "SELECT is_listed FROM events WHERE id = $1", e.ID) {
		t.Fatal("observe listed the event of an out_of_scope row")
	}
	observe()
	if got := scalar[string](t, "SELECT state || ':' || missed_runs FROM source_events WHERE id = $1", se); got != "active:0" {
		t.Fatalf("out_of_scope row after a run without it = %q, want active:0", got)
	}

	back := sampleEvent()
	back.StartDate, back.EndDate = e.StartDate, nil
	created, _, err := s.SaveEventFor(ctx, se, back, event.FoldConfig{})
	if err != nil || created || back.ID != e.ID {
		t.Fatalf("SaveEventFor (back in scope) = created %v, id %d, %v; want the old event %d", created, back.ID, err, e.ID)
	}
	if got := scalar[int64](t, "SELECT count(*) FROM events"); got != 1 {
		t.Fatalf("events = %d, want 1", got)
	}
	if got := scalar[string](t, "SELECT is_listed || ':' || se.match_state FROM events e JOIN source_events se ON se.event_id = e.id WHERE se.id = $1", se); got != "true:auto" {
		t.Fatalf("event after return = %q, want true:auto", got)
	}
}
