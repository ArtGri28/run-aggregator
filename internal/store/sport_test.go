package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"aggregator/internal/event"
	"aggregator/migrations"
)

// sampleFields — fields записи sampleEvent(), как их писал код до появления
// вида спорта: строка получена с прежнего marshalFields и не меняется (D-06).
const sampleFields = `{"title":"Санкт-Петербургский марафон","title_norm":"peterburgskiy marafon","start_date":"2026-10-17","end_date":"2026-10-18","start_time":"10:00","timezone":"Europe/Moscow","country_code":"RU","region":"Северо-Запад","city":"Санкт-Петербург","city_key":"saint-petersburg","address":"Дворцовая площадь","event_type":"road","description":"Ежегодный осенний марафон по центру города","registration_url":"https://example.test/register","distances":[{"m":5000},{"m":21100,"label":"21,1 км"}]}`

func skiing() *event.Event {
	e := sampleEvent()
	e.Sport = event.SportSkiing
	return e
}

// TestFieldsSport — у беговой записи ключа sport в fields нет, прежние
// байты те же; у остальных ключ стоит последним (D-06).
func TestFieldsSport(t *testing.T) {
	fields := func(e *event.Event) string {
		t.Helper()
		if err := canonicalize(e); err != nil {
			t.Fatal(err)
		}
		b, err := marshalFields(e)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := fields(sampleEvent()); got != sampleFields {
		t.Fatalf("fields беговой записи =\n%s\nwant\n%s", got, sampleFields)
	}
	withKey := strings.TrimSuffix(sampleFields, "}") + `,"sport":"skiing"}`
	if got := fields(skiing()); got != withKey {
		t.Fatalf("fields записи с видом спорта =\n%s\nwant\n%s", got, withKey)
	}
	for raw, want := range map[string]event.Sport{sampleFields: event.SportRunning, withKey: event.SportSkiing} {
		e, err := unmarshalFields([]byte(raw))
		if err != nil || e.Sport != want {
			t.Errorf("unmarshalFields: Sport = %q, %v; want %q", e.Sport, err, want)
		}
	}
}

// TestEventSport — запись, чтение и сравнение вида спорта события (D-04).
func TestEventSport(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sport := func(id int64) string { return scalar[string](t, "SELECT sport FROM events WHERE id = $1", id) }
	row := func(id int64) string {
		return scalar[string](t, "SELECT to_jsonb(e)::text FROM events e WHERE id = $1", id)
	}

	e := sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEvent(ctx, e.ID)
	if err != nil || sport(e.ID) != "running" || e.Sport != event.SportRunning || got.Sport != event.SportRunning {
		t.Fatalf("без вида спорта: колонка %q, e.Sport %q, GetEvent %q, %v; want running", sport(e.ID), e.Sport, got.Sport, err)
	}
	again := sampleEvent()
	again.ID = e.ID
	if changed, err := s.SaveEvent(ctx, again); err != nil || changed || !again.UpdatedAt.Equal(e.UpdatedAt) {
		t.Fatalf("повторная запись: changed %v, updated_at %v → %v, %v", changed, e.UpdatedAt, again.UpdatedAt, err)
	}
	again.Sport = event.SportSkiing
	if changed, err := s.SaveEvent(ctx, again); err != nil || !changed || sport(e.ID) != "skiing" {
		t.Fatalf("смена вида спорта: changed %v, колонка %q, %v", changed, sport(e.ID), err)
	}

	ski := skiing()
	if _, err := s.SaveEvent(ctx, ski); err != nil || sport(ski.ID) != "skiing" {
		t.Fatalf("запись skiing: колонка %q, %v", sport(ski.ID), err)
	}

	n := scalar[int64](t, "SELECT count(*) FROM events")
	bad := sampleEvent()
	bad.Sport = "Ski"
	if _, err := s.SaveEvent(ctx, bad); err == nil {
		t.Fatal("код вне формата колонки записан")
	}
	if got := scalar[int64](t, "SELECT count(*) FROM events"); got != n {
		t.Fatalf("строк events %d, want %d", got, n)
	}

	// Строка, как её писал код до появления колонки: sport не указан.
	old := sampleEvent()
	old.ID = scalar[int64](t, `
		INSERT INTO events (title, title_norm, start_date, end_date, start_time, timezone,
			country_code, region, city, city_key, address, event_type, status, description, registration_url)
		VALUES ($1, $2, $3, $4, $5::time, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id`,
		old.Title, old.TitleNorm, old.StartDate, old.EndDate, old.StartTime, old.Timezone,
		old.CountryCode, old.Region, old.City, old.CityKey, old.Address, string(old.Type), string(old.Status),
		old.Description, old.RegistrationURL)
	exec(t, "INSERT INTO event_distances (event_id, distance_m, label) VALUES ($1, 5000, NULL), ($1, 21100, '21,1 км')", old.ID)
	if got, err := s.GetEvent(ctx, old.ID); err != nil || got.Sport != event.SportRunning {
		t.Fatalf("прежняя строка: Sport = %q, %v; want running", got.Sport, err)
	}
	before := row(old.ID)
	if changed, err := s.SaveEvent(ctx, old); err != nil || changed {
		t.Fatalf("запись прежней строки: changed %v, %v; want без изменений", changed, err)
	}
	if after := row(old.ID); after != before {
		t.Fatalf("прежняя строка изменилась:\nдо    %s\nпосле %s", before, after)
	}
}

func TestMigration00008(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	col := func() string {
		return scalar[string](t, `SELECT COALESCE(string_agg(data_type || ' ' || is_nullable || ' ' || column_default, ''), '')
			FROM information_schema.columns WHERE table_name = 'events' AND column_name = 'sport'`)
	}
	if got := col(); got != "text NO 'running'::text" {
		t.Fatalf("колонка sport = %q", got)
	}
	e := sampleEvent()
	if _, err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveEvent(ctx, sampleEvent()); err != nil {
		t.Fatal(err)
	}
	const set = "UPDATE events SET sport = $2 WHERE id = $1"
	for _, bad := range []string{"SKIING", "cross country", "a"} {
		if _, err := s.pool.Exec(ctx, set, e.ID, bad); err == nil {
			t.Errorf("sport = %q принят базой", bad)
		}
	}
	exec(t, set, e.ID, "cross_country")

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
	if _, err := p.DownTo(ctx, 7); err != nil {
		t.Fatalf("DownTo(7): %v", err)
	}
	if got := col(); got != "" {
		t.Fatalf("после Down колонка sport = %q, want нет", got)
	}
	if got := scalar[int64](t, "SELECT count(*) FROM events"); got != 2 {
		t.Fatalf("после Down строк events %d, want 2", got)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := scalar[string](t, "SELECT string_agg(sport, ',') FROM events"); got != "running,running" {
		t.Fatalf("после Up sport = %q, want running у обеих строк", got)
	}
}

// TestMergeSport — вид спорта слитого события берётся из записи главного
// по приоритету источника; запись без ключа в fields — бег (D-07).
func TestMergeSport(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fields := func(rows ...int64) string {
		return scalar[string](t, "SELECT string_agg(fields::text, ' ' ORDER BY id) FROM source_events WHERE id = ANY($1)", rows)
	}
	for i, tc := range []struct {
		name          string
		winner, loser string // коды источников; запись проигравшего — skiing
		want          string
	}{
		{"победитель — главный источник", "marathonec", "probeg", "running"},
		{"проигравший — главный источник", "probeg", "marathonec", "skiing"},
	} {
		w, rowW := linked(t, s, tc.winner, itoa(int64(i)), sampleEvent())
		l, rowL := linked(t, s, tc.loser, itoa(int64(i)), skiing())
		before := fields(rowW, rowL)
		if got := scalar[string](t, "SELECT string_agg((fields ? 'sport')::text, ',' ORDER BY id) FROM source_events WHERE id = ANY($1)", []int64{rowW, rowL}); got != "false,true" {
			t.Fatalf("%s: ключ sport в fields = %s; want только у записи проигравшего", tc.name, got)
		}
		if err := s.MergeEvents(ctx, l, w, "auto", nil, testFold); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := scalar[string](t, "SELECT sport FROM events WHERE id = $1", w); got != tc.want {
			t.Errorf("%s: sport победителя = %q, want %q", tc.name, got, tc.want)
		}
		if after := fields(rowW, rowL); after != before {
			t.Errorf("%s: слияние изменило fields:\nдо    %s\nпосле %s", tc.name, before, after)
		}
	}
}
