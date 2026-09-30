-- +goose Up
-- Состояние «администратор уже оповещён»: одна строка на источник, пока он в плохом состоянии.
CREATE TABLE source_alerts (
    source_id   BIGINT PRIMARY KEY REFERENCES sources(id) ON DELETE RESTRICT,
    state       TEXT NOT NULL CHECK (state IN ('suspect', 'failed')),  -- о каком состоянии сообщили
    run_id      BIGINT NOT NULL REFERENCES ingest_runs(id) ON DELETE RESTRICT,  -- прогон, по которому сообщили
    notified_at TIMESTAMPTZ NOT NULL DEFAULT now(),  -- когда состояние объявлено впервые
    reminded_at TIMESTAMPTZ NOT NULL DEFAULT now()   -- последнее отправленное сообщение (первое или напоминание)
);

-- +goose Down
DROP TABLE source_alerts;
