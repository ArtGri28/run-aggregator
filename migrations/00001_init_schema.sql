-- +goose Up

CREATE TABLE sources (
    id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code TEXT NOT NULL UNIQUE
);

CREATE TABLE ingest_runs (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id    BIGINT NOT NULL REFERENCES sources(id) ON DELETE RESTRICT,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ,
    status       TEXT NOT NULL CHECK (status IN ('running', 'ok', 'suspect', 'failed')),
    fetched      INTEGER NOT NULL DEFAULT 0,
    parsed       INTEGER NOT NULL DEFAULT 0,
    rejected     INTEGER NOT NULL DEFAULT 0,
    out_of_scope INTEGER NOT NULL DEFAULT 0,
    upserted     INTEGER NOT NULL DEFAULT 0,
    error        TEXT
);
CREATE INDEX idx_ingest_runs_source_started ON ingest_runs (source_id, started_at DESC);

CREATE TABLE events (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title            TEXT NOT NULL,
    title_norm       TEXT NOT NULL,
    start_date       DATE NOT NULL,
    end_date         DATE,
    start_time       TIME,
    timezone         TEXT,
    country_code     TEXT NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    region           TEXT,
    city             TEXT,
    city_key         TEXT CHECK (city_key IS NULL OR (char_length(city_key) <= 64 AND city_key ~ '^[a-z0-9-]+$')),
    address          TEXT,
    event_type       TEXT NOT NULL CHECK (event_type IN ('road', 'trail', 'cross', 'track', 'obstacle', 'other')),
    status           TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'cancelled', 'postponed')),
    description      TEXT CHECK (description IS NULL OR char_length(description) <= 1000),
    registration_url TEXT,
    is_listed        BOOLEAN NOT NULL DEFAULT true,
    merged_into_id   BIGINT REFERENCES events(id) ON DELETE RESTRICT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_listed_start_date_id ON events (start_date, id)
    WHERE is_listed AND merged_into_id IS NULL;
CREATE INDEX idx_events_city_key_start_date_id ON events (city_key, start_date, id)
    WHERE is_listed AND merged_into_id IS NULL;
CREATE INDEX idx_events_country_start_date ON events (country_code, start_date);

CREATE TABLE source_events (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id      BIGINT NOT NULL REFERENCES sources(id) ON DELETE RESTRICT,
    external_id    TEXT NOT NULL,
    url            TEXT NOT NULL,
    payload        JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_hash   TEXT NOT NULL,
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    state          TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'missing', 'gone')),
    missed_runs    INTEGER NOT NULL DEFAULT 0,
    event_id       BIGINT REFERENCES events(id) ON DELETE RESTRICT,
    match_state    TEXT NOT NULL DEFAULT 'new' CHECK (match_state IN ('new', 'auto', 'manual', 'candidate', 'out_of_scope')),
    last_run_id    BIGINT REFERENCES ingest_runs(id) ON DELETE RESTRICT,
    UNIQUE (source_id, external_id)
);
CREATE INDEX idx_source_events_event_id ON source_events (event_id);
CREATE INDEX idx_source_events_state_last_seen ON source_events (state, last_seen_at);

CREATE TABLE event_distances (
    event_id   BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    distance_m INTEGER NOT NULL CHECK (distance_m > 0),
    label      TEXT,
    PRIMARY KEY (event_id, distance_m)
);
CREATE INDEX idx_event_distances_distance_event ON event_distances (distance_m, event_id);

CREATE TABLE dedup_candidates (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_event_id BIGINT NOT NULL REFERENCES source_events(id) ON DELETE CASCADE,
    event_id        BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    score           NUMERIC NOT NULL,
    reasons         JSONB NOT NULL DEFAULT '{}'::jsonb,
    state           TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'confirmed', 'rejected')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_event_id, event_id)
);

CREATE TABLE users (
    tg_id         BIGINT PRIMARY KEY,
    filters       JSONB NOT NULL DEFAULT '{}'::jsonb,
    filters_v     INTEGER NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE heartbeats (
    component TEXT PRIMARY KEY,
    beat_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE heartbeats;
DROP TABLE users;
DROP TABLE dedup_candidates;
DROP TABLE event_distances;
DROP TABLE source_events;
DROP TABLE events;
DROP TABLE ingest_runs;
DROP TABLE sources;
