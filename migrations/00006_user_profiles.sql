-- +goose Up
-- Профиль пользователя (D-07 Phase 9, BOT-08 в новой редакции): ответы
-- добровольной анкеты отдельно от настроек поиска. Строка создаётся в
-- транзакции /start; таблица users не меняется. profile_v — своя версия,
-- не filters_v: устаревшая кнопка профиля ничего не пишет.
CREATE TABLE user_profiles (
    tg_id                  BIGINT PRIMARY KEY REFERENCES users(tg_id) ON DELETE CASCADE,
    status                 TEXT NOT NULL DEFAULT 'not_started'
                           CHECK (status IN ('not_started', 'in_progress', 'completed', 'skipped')),
    gender                 TEXT CHECK (gender IN ('m', 'f')),
    age_group              SMALLINT CHECK (age_group BETWEEN 0 AND 5),
    residence_country_code TEXT CHECK (residence_country_code ~ '^[A-Z]{2}$'),  -- ZZ — другая страна
    residence_city         TEXT CHECK (char_length(residence_city) BETWEEN 1 AND 64),
    residence_city_key     TEXT CHECK (char_length(residence_city_key) <= 64 AND residence_city_key ~ '^[a-z0-9-]+$'),
    sports                 TEXT[] NOT NULL DEFAULT '{}'
                           CHECK (sports <@ ARRAY['run', 'trail', 'ocr', 'bike', 'swim', 'ski', 'tri', 'row']::text[]),
    profile_v              INTEGER NOT NULL DEFAULT 1,
    awaiting_city_until    TIMESTAMPTZ,                  -- до какого момента текст считается городом; NULL — не ждём
    awaiting_city_mode     TEXT CHECK (awaiting_city_mode IN ('w', 'e')),  -- анкета или редактирование
    offered_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at             TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (residence_city IS NULL OR residence_country_code IS NOT NULL),
    CHECK (residence_city_key IS NULL OR residence_city IS NOT NULL),
    CHECK ((awaiting_city_until IS NULL) = (awaiting_city_mode IS NULL))
);

-- +goose Down
DROP TABLE user_profiles;
