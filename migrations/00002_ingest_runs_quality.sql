-- +goose Up
ALTER TABLE ingest_runs
    ADD COLUMN accepted_at TIMESTAMPTZ,                        -- ручное принятие (граница окна базы)
    ADD COLUMN metrics     JSONB NOT NULL DEFAULT '{}'::jsonb, -- доли пустых полей, нераспознанные области и т. п.
    ADD COLUMN snapshot    JSONB;                              -- разобранный результат suspect до принятия или следующего прогона
-- Не больше одного прогона в running на источник.
CREATE UNIQUE INDEX ux_ingest_runs_running ON ingest_runs (source_id) WHERE status = 'running';

-- +goose Down
DROP INDEX ux_ingest_runs_running;
ALTER TABLE ingest_runs DROP COLUMN snapshot, DROP COLUMN metrics, DROP COLUMN accepted_at;
