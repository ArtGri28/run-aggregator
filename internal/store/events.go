package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aggregator/internal/event"
)

// ErrNotFound is returned when a lookup or update targets a row that
// does not exist.
var ErrNotFound = errors.New("not found")

// queryRower is satisfied by both *pgxpool.Pool and pgx.Tx, so read
// helpers work whether or not they run inside a transaction.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// execer is satisfied by both *pgxpool.Pool and pgx.Tx.
type execer interface {
	queryRower
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// EnsureSource inserts sources.code if absent, otherwise returns the
// existing id — a single parameterized upsert (MVP.md §4.1 UNIQUE code).
func (s *Store) EnsureSource(ctx context.Context, code string) (int64, error) {
	const q = `
		INSERT INTO sources (code) VALUES ($1)
		ON CONFLICT (code) DO UPDATE SET code = sources.code
		RETURNING id`
	var id int64
	if err := s.db.QueryRow(ctx, q, code).Scan(&id); err != nil {
		return 0, fmt.Errorf("ensure source: %w", err)
	}
	return id, nil
}

// UpsertSourceEvent is idempotent on (source_id, external_id): an
// unchanged payload only bumps last_seen_at, a changed payload replaces
// url/payload/content_hash and bumps last_seen_at. content_hash is
// computed here (hex SHA-256 of payload) so callers can't desync it.
// last_run_id = runID on insert and on every repeat: the row was seen by
// that run, which is how Observe tells found rows from missing ones.
// state, missed_runs, event_id, match_state are left untouched — that's
// the observation logic (Observe, ING-08).
func (s *Store) UpsertSourceEvent(ctx context.Context, sourceID, runID int64, externalID, url string, payload []byte) (int64, bool, error) {
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	const q = `
		WITH old AS (
			SELECT content_hash FROM source_events
			WHERE source_id = $1 AND external_id = $2
		),
		upsert AS (
			INSERT INTO source_events
				(source_id, external_id, url, payload, content_hash, first_seen_at, last_seen_at, state, missed_runs, match_state, last_run_id)
			VALUES ($1, $2, $3, $4, $5, now(), now(), 'active', 0, 'new', $6)
			ON CONFLICT (source_id, external_id) DO UPDATE
			SET last_seen_at = now(),
			    last_run_id  = EXCLUDED.last_run_id,
			    url          = CASE WHEN source_events.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.url ELSE source_events.url END,
			    payload      = CASE WHEN source_events.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.payload ELSE source_events.payload END,
			    content_hash = CASE WHEN source_events.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.content_hash ELSE source_events.content_hash END
			RETURNING id
		)
		SELECT upsert.id, (old.content_hash IS NULL OR old.content_hash <> $5)
		FROM upsert LEFT JOIN old ON true`

	var id int64
	var changed bool
	if err := s.db.QueryRow(ctx, q, sourceID, externalID, url, payload, hash, runID).Scan(&id, &changed); err != nil {
		return 0, false, fmt.Errorf("upsert source event: %w", err)
	}
	return id, changed, nil
}

const selectEventCols = `
	id, title, title_norm, start_date, end_date,
	COALESCE(to_char(start_time, 'HH24:MI'), ''),
	COALESCE(timezone, ''), country_code, COALESCE(region, ''), COALESCE(city, ''),
	COALESCE(city_key, ''), COALESCE(address, ''), event_type, status,
	COALESCE(description, ''), COALESCE(registration_url, ''),
	is_listed, merged_into_id, created_at, updated_at`

func scanEventRow(row pgx.Row) (event.Event, error) {
	var e event.Event
	var eventType, status string
	var mergedIntoID *int64
	err := row.Scan(
		&e.ID, &e.Title, &e.TitleNorm, &e.StartDate, &e.EndDate,
		&e.StartTime, &e.Timezone, &e.CountryCode, &e.Region, &e.City,
		&e.CityKey, &e.Address, &eventType, &status,
		&e.Description, &e.RegistrationURL,
		&e.IsListed, &mergedIntoID, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return event.Event{}, err
	}
	e.Type = event.Type(eventType)
	e.Status = event.Status(status)
	e.MergedIntoID = mergedIntoID
	return e, nil
}

func fetchDistances(ctx context.Context, q queryRowerMulti, eventID int64) ([]event.Distance, error) {
	rows, err := q.Query(ctx, "SELECT distance_m, COALESCE(label, '') FROM event_distances WHERE event_id = $1 ORDER BY distance_m", eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []event.Distance
	for rows.Next() {
		var d event.Distance
		if err := rows.Scan(&d.Meters, &d.Label); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// queryRowerMulti additionally exposes Query, needed to stream distance rows.
type queryRowerMulti interface {
	execer
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// GetEvent returns the canonical event and its distances (sorted by
// meters). A missing id is reported as ErrNotFound.
func (s *Store) GetEvent(ctx context.Context, id int64) (event.Event, error) {
	row := s.db.QueryRow(ctx, "SELECT"+selectEventCols+" FROM events WHERE id = $1", id)
	e, err := scanEventRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return event.Event{}, fmt.Errorf("get event %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return event.Event{}, fmt.Errorf("get event: %w", err)
	}
	e.Distances, err = fetchDistances(ctx, s.db, id)
	if err != nil {
		return event.Event{}, fmt.Errorf("get event: distances: %w", err)
	}
	return e, nil
}

// normalizeDistances sorts by meters and, when the same meters value
// appears more than once, keeps the label from the first occurrence
// (MVP.md §4.3 — distances are recomputed as a set, not accumulated).
func normalizeDistances(in []event.Distance) []event.Distance {
	seen := make(map[int]bool, len(in))
	out := make([]event.Distance, 0, len(in))
	for _, d := range in {
		if seen[d.Meters] {
			continue
		}
		seen[d.Meters] = true
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meters < out[j].Meters })
	return out
}

func sameDistances(a, b []event.Distance) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Meters != b[i].Meters || a[i].Label != b[i].Label {
			return false
		}
	}
	return true
}

func replaceDistances(ctx context.Context, tx execer, eventID int64, distances []event.Distance) error {
	if _, err := tx.Exec(ctx, "DELETE FROM event_distances WHERE event_id = $1", eventID); err != nil {
		return fmt.Errorf("replace distances: delete: %w", err)
	}
	for _, d := range distances {
		if _, err := tx.Exec(ctx,
			"INSERT INTO event_distances (event_id, distance_m, label) VALUES ($1, $2, NULLIF($3, ''))",
			eventID, d.Meters, d.Label,
		); err != nil {
			return fmt.Errorf("replace distances: insert: %w", err)
		}
	}
	return nil
}

func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func canonicalFieldsEqual(old, cur event.Event) bool {
	return old.Title == cur.Title &&
		old.TitleNorm == cur.TitleNorm &&
		old.StartDate.Equal(cur.StartDate) &&
		sameOptionalTime(old.EndDate, cur.EndDate) &&
		old.StartTime == cur.StartTime &&
		old.Timezone == cur.Timezone &&
		old.CountryCode == cur.CountryCode &&
		old.Region == cur.Region &&
		old.City == cur.City &&
		old.CityKey == cur.CityKey &&
		old.Address == cur.Address &&
		old.Type == cur.Type &&
		old.Status == cur.Status &&
		old.Description == cur.Description &&
		old.RegistrationURL == cur.RegistrationURL
}

const insertEventSQL = `
	INSERT INTO events (
		title, title_norm, start_date, end_date, start_time, timezone,
		country_code, region, city, city_key, address,
		event_type, status, description, registration_url
	) VALUES (
		$1, $2, $3, $4, NULLIF($5, '')::time, NULLIF($6, ''),
		$7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''),
		$12, $13, NULLIF($14, ''), NULLIF($15, '')
	)
	RETURNING id, created_at, updated_at`

const updateEventSQL = `
	UPDATE events SET
		title = $2, title_norm = $3, start_date = $4, end_date = $5,
		start_time = NULLIF($6, '')::time, timezone = NULLIF($7, ''),
		country_code = $8, region = NULLIF($9, ''), city = NULLIF($10, ''),
		city_key = NULLIF($11, ''), address = NULLIF($12, ''),
		event_type = $13, status = $14, description = NULLIF($15, ''),
		registration_url = NULLIF($16, ''), updated_at = now()
	WHERE id = $1
	RETURNING updated_at`

func eventArgs(e *event.Event) []any {
	return []any{
		e.Title, e.TitleNorm, e.StartDate, e.EndDate, e.StartTime, e.Timezone,
		e.CountryCode, e.Region, e.City, e.CityKey, e.Address,
		string(e.Type), string(e.Status), e.Description, e.RegistrationURL,
	}
}

// canonicalize brings e to the form the database returns, so a re-save of
// the same data compares equal (ING-05): dates become midnight UTC of their
// own calendar day (the day pgx stores), StartTime becomes "HH:MM".
func canonicalize(e *event.Event) error {
	e.StartDate = dateOnly(e.StartDate)
	if e.EndDate != nil {
		d := dateOnly(*e.EndDate)
		e.EndDate = &d
	}
	if e.StartTime != "" {
		t, err := time.Parse("15:04", e.StartTime)
		if err != nil {
			return fmt.Errorf("start time %q: want HH:MM", e.StartTime)
		}
		e.StartTime = t.Format("15:04")
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// SaveEvent inserts (e.ID == 0) or updates (e.ID != 0) the canonical
// event in one transaction. On update, updated_at only advances if a
// canonical field or the distance set actually changed (ING-05);
// is_listed/merged_into_id are never touched here (Phase 3/5 own those).
func (s *Store) SaveEvent(ctx context.Context, e *event.Event) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("save event: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changed, err := saveEventTx(ctx, tx, e)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("save event: commit: %w", err)
	}
	return changed, nil
}

// saveEventTx is SaveEvent's body inside the caller's transaction.
func saveEventTx(ctx context.Context, tx pgx.Tx, e *event.Event) (bool, error) {
	if err := canonicalize(e); err != nil {
		return false, fmt.Errorf("save event: %w", err)
	}
	distances := normalizeDistances(e.Distances)

	if e.ID == 0 {
		args := append([]any{}, eventArgs(e)...)
		row := tx.QueryRow(ctx, insertEventSQL, args...)
		if err := row.Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return false, fmt.Errorf("save event: insert: %w", err)
		}
		if err := replaceDistances(ctx, tx, e.ID, distances); err != nil {
			return false, err
		}
		e.IsListed = true
		e.Distances = distances
		return true, nil
	}

	oldRow := tx.QueryRow(ctx, "SELECT"+selectEventCols+" FROM events WHERE id = $1 FOR UPDATE", e.ID)
	old, err := scanEventRow(oldRow)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("save event %d: %w", e.ID, ErrNotFound)
	}
	if err != nil {
		return false, fmt.Errorf("save event: select: %w", err)
	}

	oldDistances, err := fetchDistances(ctx, tx, e.ID)
	if err != nil {
		return false, fmt.Errorf("save event: distances: %w", err)
	}

	fieldsChanged := !canonicalFieldsEqual(old, *e)
	distancesChanged := !sameDistances(oldDistances, distances)
	changed := fieldsChanged || distancesChanged

	switch {
	case fieldsChanged:
		args := append([]any{e.ID}, eventArgs(e)...)
		if err := tx.QueryRow(ctx, updateEventSQL, args...).Scan(&e.UpdatedAt); err != nil {
			return false, fmt.Errorf("save event: update: %w", err)
		}
	case changed:
		// Only the distance set changed: still bump updated_at (ING-05).
		if err := tx.QueryRow(ctx, "UPDATE events SET updated_at = now() WHERE id = $1 RETURNING updated_at", e.ID).Scan(&e.UpdatedAt); err != nil {
			return false, fmt.Errorf("save event: touch updated_at: %w", err)
		}
	default:
		e.UpdatedAt = old.UpdatedAt
	}

	if distancesChanged {
		if err := replaceDistances(ctx, tx, e.ID, distances); err != nil {
			return false, err
		}
	}

	e.CreatedAt = old.CreatedAt
	e.IsListed = old.IsListed
	e.MergedIntoID = old.MergedIntoID
	e.Distances = distances
	return changed, nil
}

// sourceFields — нормализованные поля одной записи источника в
// source_events.fields (D-19): свои snake_case-ключи, а не имена полей Go,
// поэтому переименование в event.Event не обнуляет старые строки; status,
// id и служебные поля не пишутся. Неизвестные ключи при чтении игнорируются.
type sourceFields struct {
	Title           string          `json:"title"`
	TitleNorm       string          `json:"title_norm"`
	StartDate       string          `json:"start_date"`
	EndDate         string          `json:"end_date,omitempty"`
	StartTime       string          `json:"start_time,omitempty"`
	Timezone        string          `json:"timezone,omitempty"`
	CountryCode     string          `json:"country_code"`
	Region          string          `json:"region,omitempty"`
	City            string          `json:"city,omitempty"`
	CityKey         string          `json:"city_key,omitempty"`
	Address         string          `json:"address,omitempty"`
	EventType       string          `json:"event_type"`
	Description     string          `json:"description,omitempty"`
	RegistrationURL string          `json:"registration_url,omitempty"`
	Distances       []fieldDistance `json:"distances"`
}

type fieldDistance struct {
	M     int    `json:"m"`
	Label string `json:"label,omitempty"`
}

const fieldsDate = "2006-01-02"

// marshalFields — JSON полей записи; e уже прошёл canonicalize.
func marshalFields(e *event.Event) ([]byte, error) {
	f := sourceFields{
		Title: e.Title, TitleNorm: e.TitleNorm, StartDate: e.StartDate.Format(fieldsDate),
		StartTime: e.StartTime, Timezone: e.Timezone, CountryCode: e.CountryCode,
		Region: e.Region, City: e.City, CityKey: e.CityKey, Address: e.Address,
		EventType: string(e.Type), Description: e.Description, RegistrationURL: e.RegistrationURL,
		Distances: []fieldDistance{},
	}
	if e.EndDate != nil {
		f.EndDate = e.EndDate.Format(fieldsDate)
	}
	for _, d := range normalizeDistances(e.Distances) {
		f.Distances = append(f.Distances, fieldDistance{M: d.Meters, Label: d.Label})
	}
	return json.Marshal(f)
}

// unmarshalFields — обратное marshalFields.
func unmarshalFields(raw []byte) (event.Event, error) {
	var f sourceFields
	if err := json.Unmarshal(raw, &f); err != nil {
		return event.Event{}, err
	}
	start, err := time.Parse(fieldsDate, f.StartDate)
	if err != nil {
		return event.Event{}, fmt.Errorf("start_date: %w", err)
	}
	e := event.Event{
		Title: f.Title, TitleNorm: f.TitleNorm, StartDate: start,
		StartTime: f.StartTime, Timezone: f.Timezone, CountryCode: f.CountryCode,
		Region: f.Region, City: f.City, CityKey: f.CityKey, Address: f.Address,
		Type: event.Type(f.EventType), Description: f.Description, RegistrationURL: f.RegistrationURL,
	}
	if f.EndDate != "" {
		end, err := time.Parse(fieldsDate, f.EndDate)
		if err != nil {
			return event.Event{}, fmt.Errorf("end_date: %w", err)
		}
		e.EndDate = &end
	}
	for _, d := range f.Distances {
		e.Distances = append(e.Distances, event.Distance{Meters: d.M, Label: d.Label})
	}
	return e, nil
}

// foldPartsTx — части для event.Fold: код источника и fields строк события
// eventID с непустым fields, вне gone и out_of_scope (как listEventSQL);
// строка ownID входит всегда — запись, которая пишется сейчас. Порядок по
// id строки; приоритет источников применяет Fold.
func foldPartsTx(ctx context.Context, tx queryRowerMulti, eventID, ownID int64) ([]event.Part, error) {
	const q = `
		SELECT s.code, se.fields FROM source_events se JOIN sources s ON s.id = se.source_id
		WHERE se.event_id = $1 AND se.fields IS NOT NULL
		  AND ((se.state <> 'gone' AND se.match_state <> 'out_of_scope') OR se.id = $2)
		ORDER BY se.id`
	rows, err := tx.Query(ctx, q, eventID, ownID)
	if err != nil {
		return nil, fmt.Errorf("fold parts: %w", err)
	}
	type row struct {
		code   string
		fields []byte
	}
	raw, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.code, &x.fields)
	})
	if err != nil {
		return nil, fmt.Errorf("fold parts: %w", err)
	}
	parts := make([]event.Part, 0, len(raw))
	for _, x := range raw {
		e, err := unmarshalFields(x.fields)
		if err != nil {
			return nil, fmt.Errorf("fold parts: event %d: %w", eventID, err)
		}
		parts = append(parts, event.Part{Source: x.code, Event: e})
	}
	return parts, nil
}

// SaveEventFor — единственная точка записи полей события из записи
// источника (D-17.1): всё идёт через saveEventTx в одной транзакции, чтобы
// сбой не оставил событие без связи (T-03-10). Поля записи сохраняются в
// source_events.fields. Несвязанная строка (event_id NULL) получает новое
// событие и match_state = 'auto'. Связанная обновляет своё событие: если у
// него несколько записей с fields, поля события — event.Fold всех записей
// по fold (D-07), иначе — поля этой записи; status берётся из БД, потому
// что источник без явного статуса не должен сбрасывать cancelled/postponed.
// Строка, вернувшаяся из out_of_scope, переиспользует событие:
// match_state = 'auto', is_listed пересчитывается (LO-12).
// Чужие строки читаются без блокировки: запись прогонов сериализует
// LockDedup. Нет sourceEventID — ErrNotFound.
func (s *Store) SaveEventFor(ctx context.Context, sourceEventID int64, e *event.Event, fold event.FoldConfig) (created, changed bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, false, fmt.Errorf("save event for: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID *int64
	var matchState string
	err = tx.QueryRow(ctx, "SELECT event_id, match_state FROM source_events WHERE id = $1 FOR UPDATE", sourceEventID).Scan(&eventID, &matchState)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, fmt.Errorf("save event for %d: %w", sourceEventID, ErrNotFound)
	}
	if err != nil {
		return false, false, fmt.Errorf("save event for: select source event: %w", err)
	}

	if err := canonicalize(e); err != nil {
		return false, false, fmt.Errorf("save event for: %w", err)
	}
	fields, err := marshalFields(e)
	if err != nil {
		return false, false, fmt.Errorf("save event for: fields: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE source_events SET fields = $2 WHERE id = $1", sourceEventID, fields); err != nil {
		return false, false, fmt.Errorf("save event for: fields: %w", err)
	}

	if eventID == nil {
		e.ID = 0
		if _, err := saveEventTx(ctx, tx, e); err != nil {
			return false, false, err
		}
		const link = "UPDATE source_events SET event_id = $2, match_state = 'auto' WHERE id = $1"
		if _, err := tx.Exec(ctx, link, sourceEventID, e.ID); err != nil {
			return false, false, fmt.Errorf("save event for: link: %w", err)
		}
		created = true
	} else {
		id := *eventID
		var status string
		if err := tx.QueryRow(ctx, "SELECT status FROM events WHERE id = $1 FOR UPDATE", id).Scan(&status); err != nil {
			return false, false, fmt.Errorf("save event for: select status: %w", err)
		}
		back := matchState == string(event.MatchOutOfScope)
		if back {
			if _, err := tx.Exec(ctx, "UPDATE source_events SET match_state = 'auto' WHERE id = $1", sourceEventID); err != nil {
				return false, false, fmt.Errorf("save event for: relink: %w", err)
			}
		}
		parts, err := foldPartsTx(ctx, tx, id, sourceEventID)
		if err != nil {
			return false, false, fmt.Errorf("save event for: %w", err)
		}
		if len(parts) > 1 {
			*e = event.Fold(parts, fold)
		}
		e.ID, e.Status = id, event.Status(status)
		if changed, err = saveEventTx(ctx, tx, e); err != nil {
			return false, false, err
		}
		if back {
			if _, err := tx.Exec(ctx, listEventSQL, e.ID); err != nil {
				return false, false, fmt.Errorf("save event for: is_listed: %w", err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("save event for: commit: %w", err)
	}
	return created, changed, nil
}

// SetMatch updates a single source_events row's event_id/match_state
// (the source_event → event link, or the out-of-scope path when eventID
// is nil). A missing sourceEventID is reported as ErrNotFound.
func (s *Store) SetMatch(ctx context.Context, sourceEventID int64, eventID *int64, state event.MatchState) error {
	cmd, err := s.db.Exec(ctx, "UPDATE source_events SET event_id = $2, match_state = $3 WHERE id = $1", sourceEventID, eventID, string(state))
	if err != nil {
		return fmt.Errorf("set match: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("set match %d: %w", sourceEventID, ErrNotFound)
	}
	return nil
}

// listEventSQL recomputes one event's is_listed: listed while a linked
// source_events row is neither gone nor out_of_scope (as Observe does);
// status and updated_at are never changed.
const listEventSQL = `
	UPDATE events SET is_listed = EXISTS (
		SELECT 1 FROM source_events WHERE event_id = $1 AND state <> 'gone' AND match_state <> 'out_of_scope')
	WHERE id = $1 AND merged_into_id IS NULL`

// SetOutOfScope marks a source_events row out_of_scope in one
// transaction, keeping its event_id so a row back in scope reuses the
// event (LO-12). If no other linked row outside gone and out_of_scope
// remains, the event gets is_listed = false; the event's status is never
// changed. A missing id is ErrNotFound.
func (s *Store) SetOutOfScope(ctx context.Context, sourceEventID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set out of scope: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID *int64
	err = tx.QueryRow(ctx, "SELECT event_id FROM source_events WHERE id = $1 FOR UPDATE", sourceEventID).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("set out of scope %d: %w", sourceEventID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("set out of scope: select: %w", err)
	}
	const mark = "UPDATE source_events SET match_state = 'out_of_scope' WHERE id = $1"
	if _, err := tx.Exec(ctx, mark, sourceEventID); err != nil {
		return fmt.Errorf("set out of scope: mark: %w", err)
	}
	if eventID != nil {
		if _, err := tx.Exec(ctx, listEventSQL, *eventID); err != nil {
			return fmt.Errorf("set out of scope: hide event: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set out of scope: commit: %w", err)
	}
	return nil
}
