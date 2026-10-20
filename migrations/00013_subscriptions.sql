-- +goose Up
CREATE TABLE subscriptions (
    tg_id      BIGINT PRIMARY KEY CHECK (tg_id > 0),
    filters    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    sent_at    TIMESTAMPTZ
);

-- +goose Down
DROP TABLE subscriptions;
