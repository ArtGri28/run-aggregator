-- +goose Up
-- Вид спорта события. Существующие события — бег: все источники до этой
-- миграции отдавали только его. Допустимые коды — в справочнике кода;
-- здесь проверяется только формат.
ALTER TABLE events ADD COLUMN sport TEXT NOT NULL DEFAULT 'running'
    CHECK (sport ~ '^[a-z][a-z_]{1,31}$');

-- +goose Down
ALTER TABLE events DROP COLUMN sport;
