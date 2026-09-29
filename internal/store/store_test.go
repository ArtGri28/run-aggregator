package store

import (
	"context"
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
		event_distances, dedup_candidates, users, heartbeats RESTART IDENTITY CASCADE`
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

	id, changed, err := s.UpsertSourceEvent(ctx, sourceID, "60817", "https://example.test/60817", []byte(`{"title":"A"}`))
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

	time.Sleep(10 * time.Millisecond)

	idAgain, changed, err := s.UpsertSourceEvent(ctx, sourceID, "60817", "https://example.test/60817", []byte(`{"title":"A"}`))
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

	idChanged, changed, err := s.UpsertSourceEvent(ctx, sourceID, "60817", "https://example.test/60817", []byte(`{"title":"B"}`))
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

	otherID, changed, err := s.UpsertSourceEvent(ctx, sourceID, "60818", "https://example.test/60818", []byte(`{"title":"C"}`))
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
	se1, _, err := s.UpsertSourceEvent(ctx, sourceID, "1", "https://example.test/1", []byte(`{}`))
	if err != nil {
		t.Fatalf("UpsertSourceEvent 1: %v", err)
	}
	se2, _, err := s.UpsertSourceEvent(ctx, sourceID, "2", "https://example.test/2", []byte(`{}`))
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
		{"bad type", func(e *event.Event) { e.Type = event.Type("ultra") }},
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
