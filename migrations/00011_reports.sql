-- +goose Up
-- Заявки «Сообщить об ошибке» в карточке старта (Phase 16, D-06…D-08):
-- кто, о каком старте, какая причина и когда. Текста заявки нет: причина
-- выбирается кнопкой. Записывается событие, корневое на момент заявки.
-- Одна заявка на «пользователь — событие — причина»; предел заявок в сутки
-- считается по created_at. Заявка не связывается с сообщением в группе
-- поддержки: ответ на него пользователю не доставляется (D-07).
-- Допустимые причины — в справочнике кода; здесь проверяется только формат.
CREATE TABLE error_reports (
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),
    event_id   BIGINT NOT NULL REFERENCES events(id),
    reason     TEXT NOT NULL CHECK (reason ~ '^[a-z][a-z_]{1,31}$'),
    created_at TIMESTAMPTZ NOT NULL,  -- по часам бота
    PRIMARY KEY (tg_id, event_id, reason)
);
CREATE INDEX error_reports_user_created ON error_reports (tg_id, created_at);

-- Предложения стартов (D-10…D-12): только кто и когда — для предела
-- 3 в сутки и счётчика /quality. Сама ссылка в базе не хранится: она есть
-- только в сообщении бота в группе поддержки или администраторам.
-- Строка пишется до отправки и удаляется, если отправка не удалась.
CREATE TABLE start_suggestions (
    id         BIGSERIAL PRIMARY KEY,
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),
    created_at TIMESTAMPTZ NOT NULL  -- по часам бота
);
CREATE INDEX start_suggestions_user_created ON start_suggestions (tg_id, created_at);

-- Отметка «жду ссылку»: до expires_at следующее сообщение пользователя
-- считается предложением старта. Отдельная таблица, а не вид отметки в
-- support_awaiting: откат образа не примет ссылку за обращение в поддержку.
-- Режимы «жду ссылку», «жду обращение» и «жду город» не действуют
-- одновременно — это держит код.
CREATE TABLE suggest_awaiting (
    tg_id      BIGINT PRIMARY KEY CHECK (tg_id > 0),
    expires_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE suggest_awaiting;
DROP TABLE start_suggestions;
DROP TABLE error_reports;
