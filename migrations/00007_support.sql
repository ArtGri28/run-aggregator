-- +goose Up
-- Связка «сообщение бота в группе поддержки → автор обращения» (D-13
-- Phase 11): по ней ответ из группы доставляется пользователю и считается
-- лимит обращений (D-12, D-14). Текста обращения и ответа в таблице нет.
-- chat_id входит в ключ: номер сообщения уникален только внутри чата, а
-- идентификатор группы может смениться.
CREATE TABLE support_messages (
    chat_id    BIGINT NOT NULL CHECK (chat_id < 0),      -- группа на момент отправки
    message_id INTEGER NOT NULL CHECK (message_id > 0),  -- сообщение бота в этой группе
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),        -- автор обращения
    created_at TIMESTAMPTZ NOT NULL,                     -- время отправки по часам бота
    PRIMARY KEY (chat_id, message_id)
);
CREATE INDEX support_messages_user_created ON support_messages (tg_id, created_at);

-- Отметка «жду обращение» (D-08…D-10 Phase 11): до expires_at следующее
-- сообщение пользователя считается обращением в поддержку. Отдельная
-- таблица, а не колонка user_profiles: поддержка доступна и без строки
-- профиля, а откат образа не примет текст обращения за город проживания.
CREATE TABLE support_awaiting (
    tg_id      BIGINT PRIMARY KEY CHECK (tg_id > 0),
    expires_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE support_awaiting;
DROP TABLE support_messages;
