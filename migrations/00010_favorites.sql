-- +goose Up
-- Избранное пользователя (Phase 14, D-01): пара «пользователь — старт».
-- Добавление в избранное включает сообщения об этом старте; отдельного
-- признака согласия нет (D-14). Записывается событие, корневое на момент
-- добавления; более позднее слияние разрешается при чтении (D-04).
-- Запись о прошедшем старте остаётся (D-03).
CREATE TABLE favorites (
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),
    event_id   BIGINT NOT NULL REFERENCES events(id),
    created_at TIMESTAMPTZ NOT NULL,  -- по часам бота: от него считается напоминание (D-09)
    PRIMARY KEY (tg_id, event_id)
);
CREATE INDEX favorites_event ON favorites (event_id);

-- Очередь исходящих сообщений пользователям. Строки создают применение
-- прогона источника (изменения избранных стартов) и бот (напоминания);
-- отправляет только бот. Текста сообщения в таблице нет: payload — что
-- изменилось у старта, текст собирает бот при отправке.
-- UNIQUE (tg_id, dedup_key): одно сообщение на старт за прогон, одно
-- напоминание на дату старта.
CREATE TABLE outbox (
    id         BIGSERIAL PRIMARY KEY,
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),
    kind       TEXT NOT NULL CHECK (kind ~ '^[a-z][a-z_]{1,31}$'),
    event_id   BIGINT REFERENCES events(id),
    dedup_key  TEXT NOT NULL CHECK (char_length(dedup_key) BETWEEN 1 AND 100),
    payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    claimed_at TIMESTAMPTZ,  -- бот взял сообщение в отправку; без sent_at и done_at исход неизвестен, повторно не шлётся
    sent_at    TIMESTAMPTZ,  -- Telegram принял сообщение
    done_at    TIMESTAMPTZ,  -- закрыто без доставки, причина — в outcome
    outcome    TEXT CHECK (outcome ~ '^[a-z][a-z_]{1,31}$'),
    UNIQUE (tg_id, dedup_key),
    CHECK (sent_at IS NULL OR done_at IS NULL),
    CHECK ((done_at IS NULL) = (outcome IS NULL))
);
CREATE INDEX outbox_pending ON outbox (id) WHERE claimed_at IS NULL AND done_at IS NULL;

-- +goose Down
DROP TABLE outbox;
DROP TABLE favorites;
