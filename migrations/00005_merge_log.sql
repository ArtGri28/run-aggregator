-- +goose Up
-- Журнал слияний (D-14 Phase 7, вариант c): что именно перенесло каждое
-- слияние, чтобы его можно было отменить точно. Пишет MergeEvents в той же
-- транзакции; слияния до этой миграции журнала не имеют.
CREATE TABLE event_merges (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    winner_id   BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    loser_id    BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    match_state TEXT NOT NULL CHECK (match_state IN ('auto', 'manual')),
    decided_by  BIGINT,                              -- tg_id администратора; NULL — автоматически
    merged_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    undone_at   TIMESTAMPTZ,                         -- когда отменено; NULL — действует
    undone_by   BIGINT
);
CREATE INDEX idx_event_merges_loser ON event_merges (loser_id, id);

-- Записи источников, перенесённые слиянием, и их match_state до слияния.
CREATE TABLE event_merge_records (
    merge_id         BIGINT NOT NULL REFERENCES event_merges(id) ON DELETE RESTRICT,
    source_event_id  BIGINT NOT NULL REFERENCES source_events(id) ON DELETE RESTRICT,
    prev_match_state TEXT NOT NULL,
    PRIMARY KEY (merge_id, source_event_id)
);
CREATE INDEX idx_event_merge_records_source ON event_merge_records (source_event_id);

-- События, раньше слитые в проигравшего и перевешенные на победителя
-- (цепочка в один шаг).
CREATE TABLE event_merge_chain (
    merge_id BIGINT NOT NULL REFERENCES event_merges(id) ON DELETE RESTRICT,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    PRIMARY KEY (merge_id, event_id)
);

-- +goose Down
DROP TABLE event_merge_chain;
DROP TABLE event_merge_records;
DROP TABLE event_merges;
