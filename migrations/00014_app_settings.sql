-- +goose Up
CREATE TABLE app_settings (
    key   TEXT PRIMARY KEY CHECK (key ~ '^[a-z][a-z_]{1,31}$'),
    value TEXT NOT NULL CHECK (char_length(value) <= 200)
);

CREATE TABLE calendar_feeds (
    tg_id      BIGINT PRIMARY KEY CHECK (tg_id > 0),
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE calendar_feeds;
DROP TABLE app_settings;
