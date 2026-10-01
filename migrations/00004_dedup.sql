-- +goose Up
-- Нормализованные поля записи источника — вход свёртки полей события (D-07);
-- NULL — запись ещё не записывалась текущей версией.
ALTER TABLE source_events ADD COLUMN fields JSONB;

ALTER TABLE dedup_candidates
    ADD COLUMN decided_at  TIMESTAMPTZ,  -- когда кандидат слит или отклонён
    ADD COLUMN decided_by  BIGINT,       -- tg_id администратора; NULL — решено автоматически
    ADD COLUMN notified_at TIMESTAMPTZ;  -- когда кандидат вошёл в ежедневную сводку

-- Очередь /dupes и счётчики сводки: кандидаты по состоянию в порядке id.
CREATE INDEX idx_dedup_candidates_state ON dedup_candidates (state, id);

-- +goose Down
DROP INDEX idx_dedup_candidates_state;
ALTER TABLE dedup_candidates
    DROP COLUMN notified_at,
    DROP COLUMN decided_by,
    DROP COLUMN decided_at;
ALTER TABLE source_events DROP COLUMN fields;
