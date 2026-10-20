-- +goose Up
CREATE TABLE user_days (
    day   DATE    NOT NULL,
    tg_id BIGINT  NOT NULL,
    bot   BOOLEAN NOT NULL DEFAULT false,
    app   BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (day, tg_id)
);

CREATE TABLE usage_counters (
    day    DATE    NOT NULL,
    metric TEXT    NOT NULL,
    n      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, metric)
);

-- +goose Down
DROP TABLE usage_counters;
DROP TABLE user_days;
