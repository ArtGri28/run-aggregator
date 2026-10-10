-- +goose Up
-- Дисциплина события хранится в event_type; у каждого вида спорта свои
-- значения. Допустимые значения — в справочнике кода; здесь проверяется
-- только формат, как у events.sport.
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check
    CHECK (event_type ~ '^[a-z][a-z_]{1,31}$');

-- +goose Down
-- Прежний закрытый список. NOT VALID: строки с дисциплинами других видов
-- спорта остаются на месте, проверяются только новые и изменяемые строки.
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check
    CHECK (event_type IN ('road', 'trail', 'cross', 'track', 'obstacle', 'other')) NOT VALID;
