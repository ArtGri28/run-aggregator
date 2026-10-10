package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// Очередь исходящих сообщений (Phase 14, таблица outbox). Строки об
// изменениях и о пропавших стартах пишет применение прогона источника в своей
// транзакции; отправляет только бот. Текста сообщения в строке нет — только
// что изменилось у старта (event.Change).

// WatchStates — состояние всех будущих видимых корневых стартов включённых
// видов спорта на дату today (futureListed): снимок, который прогон берёт до
// применения и после наблюдения. Только читает.
//
// Упрощение: один запрос по всем будущим стартам каталога (сейчас сотни
// строк), а не по событиям прогона — так предохранитель видит весь каталог.
// При десятках тысяч стартов — сравнивать только события, затронутые прогоном.
func (s *Store) WatchStates(ctx context.Context, today time.Time) (map[int64]event.WatchState, error) {
	q := `
		SELECT e.id, e.title, e.start_date, e.status, COALESCE(e.registration_url, '')
		FROM events e` + futureListed()
	rows, err := s.db.Query(ctx, q, dateOnly(today))
	if err != nil {
		return nil, fmt.Errorf("watch states: %w", err)
	}
	out := map[int64]event.WatchState{}
	var id int64
	var st event.WatchState
	var status string
	_, err = pgx.ForEachRow(rows, []any{&id, &st.Title, &st.StartDate, &status, &st.URL}, func() error {
		st.Status = event.Status(status)
		out[id] = st
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("watch states: %w", err)
	}
	return out, nil
}

// HiddenEvents — какие из событий ids скрыты из выдачи и не слиты
// (is_listed = false без merged_into_id), по возрастанию id. Слитое событие
// тоже не показывается, но скрытым не считается: оно ведёт к корню. Только
// читает; пустой ids — пустой ответ без запроса.
func (s *Store) HiddenEvents(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const q = `SELECT id FROM events WHERE id = ANY($1) AND NOT is_listed AND merged_into_id IS NULL ORDER BY id`
	rows, err := s.db.Query(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("hidden events: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("hidden events: %w", err)
	}
	return out, nil
}

// EnqueueChange записывает изменение c корневого старта, найденное прогоном
// runID, в очередь: по одной строке каждому пользователю, чьё избранное ведёт
// к этому старту — в том числе через слитое событие (favRoot); пользователь с
// двумя такими записями получает одну строку. Вид строки — c.Kind(), ключ
// повтора — «вид:прогон:событие», содержимое — c.Payload(), время — now.
// Повтор той же пары «пользователь — ключ» строки не создаёт. Возвращает
// число созданных строк.
//
// Упрощение: получатели определяются в момент записи. Пользователь,
// добавивший старт в избранное между этой записью и фиксацией транзакции
// прогона, сообщения об этом изменении не получит; потолок — проверка при
// добавлении в избранное.
func (s *Store) EnqueueChange(ctx context.Context, runID int64, c event.Change, now time.Time) (int, error) {
	const q = `
		INSERT INTO outbox (tg_id, kind, event_id, dedup_key, payload, created_at)
		SELECT DISTINCT f.tg_id, $1::text, $2::bigint, $3::text, $4::jsonb, $5::timestamptz
		FROM favorites f JOIN events fe ON fe.id = f.event_id
		WHERE ` + favRoot + ` = $2::bigint
		ON CONFLICT (tg_id, dedup_key) DO NOTHING`
	kind := c.Kind()
	key := fmt.Sprintf("%s:%d:%d", kind, runID, c.EventID)
	cmd, err := s.db.Exec(ctx, q, kind, c.EventID, key, c.Payload(), now)
	if err != nil {
		return 0, fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return int(cmd.RowsAffected()), nil
}
