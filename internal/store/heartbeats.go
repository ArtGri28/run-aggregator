package store

import (
	"context"
	"errors"
	"time"
)

// Beat ставит отметку компоненты в heartbeats: строка создаётся или её
// beat_at обновляется временем БД. Ошибка — без текста драйвера.
func (s *Store) Beat(ctx context.Context, component string) error {
	const q = `INSERT INTO heartbeats (component) VALUES ($1)
		ON CONFLICT (component) DO UPDATE SET beat_at = now()`
	if _, err := s.db.Exec(ctx, q, component); err != nil {
		return errors.New("heartbeat: отметка не записана")
	}
	return nil
}

// HeartbeatAges — возраст отметки каждой компоненты. Возраст считает БД
// (now() − beat_at), а не часы процесса; компоненты без строки в карте нет.
func (s *Store) HeartbeatAges(ctx context.Context) (map[string]time.Duration, error) {
	fail := errors.New("heartbeat: отметки не прочитаны")
	rows, err := s.db.Query(ctx, "SELECT component, EXTRACT(EPOCH FROM now() - beat_at)::float8 FROM heartbeats")
	if err != nil {
		return nil, fail
	}
	defer rows.Close()
	out := map[string]time.Duration{}
	for rows.Next() {
		var c string
		var sec float64
		if err := rows.Scan(&c, &sec); err != nil {
			return nil, fail
		}
		out[c] = time.Duration(sec * float64(time.Second))
	}
	if rows.Err() != nil {
		return nil, fail
	}
	return out, nil
}
