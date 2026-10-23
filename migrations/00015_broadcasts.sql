-- +goose Up
ALTER TABLE user_profiles
    ADD COLUMN notify_opt_in       BOOLEAN NOT NULL DEFAULT false,  -- приглашения: согласие, выключено по умолчанию
    ADD COLUMN notify_changed_at   TIMESTAMPTZ,
    ADD COLUMN announce_opt_out    BOOLEAN NOT NULL DEFAULT false,  -- объявления: отказ
    ADD COLUMN announce_changed_at TIMESTAMPTZ;

CREATE TABLE broadcasts (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL CHECK (kind IN ('announce', 'invite')),
    admin_tg_id  BIGINT NOT NULL CHECK (admin_tg_id > 0),
    created_at   TIMESTAMPTZ NOT NULL,
    filter       JSONB NOT NULL DEFAULT '{}'::jsonb,  -- у приглашения: {"country":"RU","city_key":"tula","sports":["run"]}
    text         TEXT CHECK (char_length(text) BETWEEN 1 AND 4096),  -- NULL после рассылки (Q-2 а)
    recipients   INTEGER NOT NULL CHECK (recipients >= 0),
    finished_at  TIMESTAMPTZ,
    CHECK (kind = 'invite' OR filter = '{}'::jsonb)
);

-- +goose Down
DROP TABLE broadcasts;
ALTER TABLE user_profiles
    DROP COLUMN announce_changed_at, DROP COLUMN announce_opt_out,
    DROP COLUMN notify_changed_at, DROP COLUMN notify_opt_in;
