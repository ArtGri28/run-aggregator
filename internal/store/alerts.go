package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AlertState — строка source_alerts: о каком плохом состоянии источника
// администраторы уже оповещены (MON-05, D-13).
type AlertState struct {
	SourceID int64
	// State — suspect или failed.
	State string
	// RunID — прогон, по которому отправлено последнее сообщение.
	RunID int64
	// NotifiedAt — когда состояние объявлено впервые; RemindedAt — время
	// последнего сообщения (первого или напоминания).
	NotifiedAt, RemindedAt time.Time
}

// AlertStates возвращает все строки source_alerts по source_id.
func (s *Store) AlertStates(ctx context.Context) (map[int64]AlertState, error) {
	const q = "SELECT source_id, state, run_id, notified_at, reminded_at FROM source_alerts"
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("alert states: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (AlertState, error) {
		var a AlertState
		err := row.Scan(&a.SourceID, &a.State, &a.RunID, &a.NotifiedAt, &a.RemindedAt)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("alert states: %w", err)
	}
	out := make(map[int64]AlertState, len(list))
	for _, a := range list {
		out[a.SourceID] = a
	}
	return out, nil
}

// SaveAlertState создаёт или обновляет строку источника целиком; времена
// задаёт вызывающий (часы цикла алертов), а не now() БД.
func (s *Store) SaveAlertState(ctx context.Context, st AlertState) error {
	const q = `
		INSERT INTO source_alerts (source_id, state, run_id, notified_at, reminded_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (source_id) DO UPDATE
		SET state = EXCLUDED.state, run_id = EXCLUDED.run_id,
		    notified_at = EXCLUDED.notified_at, reminded_at = EXCLUDED.reminded_at`
	if _, err := s.db.Exec(ctx, q, st.SourceID, st.State, st.RunID, st.NotifiedAt, st.RemindedAt); err != nil {
		return fmt.Errorf("save alert state: %w", err)
	}
	return nil
}

// ClearAlertState удаляет строку источника; отсутствующая — не ошибка.
func (s *Store) ClearAlertState(ctx context.Context, sourceID int64) error {
	if _, err := s.db.Exec(ctx, "DELETE FROM source_alerts WHERE source_id = $1", sourceID); err != nil {
		return fmt.Errorf("clear alert state: %w", err)
	}
	return nil
}
