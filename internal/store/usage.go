package store

import (
	"context"
	"fmt"
	"time"
)

// Активность (Phase 19, 19-CONTEXT.md): дни, когда пользователь открывал бота
// или Mini App, и счётчики действий за день без Telegram ID. day — дата по
// Москве как полночь UTC.

// Счётчики действий (D-03).
const (
	MetricSearchBot = "search_bot"
	MetricSearchApp = "search_app"
	MetricCardBot   = "card_bot"
	MetricCardApp   = "card_app"
	MetricFavBot    = "fav_bot"
	MetricFavApp    = "fav_app"
	// MetricSourcePrefix — начало счётчика входа по метке ссылки: src_<метка>.
	MetricSourcePrefix = "src_"
)

// MarkDay отмечает, что пользователь был сегодня в боте (app = false) или в
// Mini App. Повторная отметка того же дня строку не переписывает.
func (s *Store) MarkDay(ctx context.Context, tgID int64, day time.Time, app bool) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_days (day, tg_id, bot, app) VALUES ($1, $2, NOT $3, $3)
		ON CONFLICT (day, tg_id) DO UPDATE
		SET bot = user_days.bot OR EXCLUDED.bot, app = user_days.app OR EXCLUDED.app
		WHERE (EXCLUDED.bot AND NOT user_days.bot) OR (EXCLUDED.app AND NOT user_days.app)`,
		day, tgID, app)
	if err != nil {
		return fmt.Errorf("mark day: %w", err)
	}
	return nil
}

// CountAction прибавляет единицу к счётчику metric за день.
func (s *Store) CountAction(ctx context.Context, day time.Time, metric string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO usage_counters (day, metric, n) VALUES ($1, $2, 1)
		ON CONFLICT (day, metric) DO UPDATE SET n = usage_counters.n + 1`, day, metric)
	if err != nil {
		return fmt.Errorf("count action: %w", err)
	}
	return nil
}

// PruneUserDays удаляет отметки дней раньше before (D-02).
func (s *Store) PruneUserDays(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM user_days WHERE day < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune user days: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Activity — сводка /activity. Окна «сегодня», 7 и 30 дней считаются от
// today включительно.
type Activity struct {
	Since     time.Time // первая отметка; нулевое — отметок нет
	Users     int
	New7      int    // пришли за 7 дней
	Active    [3]int // заходили: сегодня, 7 дней, 30 дней
	App       [3]int // из них в Mini App
	Cohort    int    // пришли 8–30 дней назад
	Returned  int    // из них заходили за 7 дней
	Freq      [4]int // дней за 30 дней: 1, 2–3, 4–9, 10+
	Actions   map[string]int
	Reminders int // напоминаний отправлено за 7 дней
	Subs      int // подписаны на новые старты
}

// Activity считает сводку на день today и момент now.
func (s *Store) Activity(ctx context.Context, today, now time.Time) (Activity, error) {
	a := Activity{Actions: map[string]int{}}
	var since *time.Time
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT min(day) FROM user_days),
		       (SELECT count(*) FROM users),
		       (SELECT count(*) FROM users WHERE created_at >= $2::timestamptz - interval '7 days'),
		       count(DISTINCT tg_id) FILTER (WHERE day = $1),
		       count(DISTINCT tg_id) FILTER (WHERE day > $1::date - 7),
		       count(DISTINCT tg_id) FILTER (WHERE day > $1::date - 30),
		       count(DISTINCT tg_id) FILTER (WHERE app AND day = $1),
		       count(DISTINCT tg_id) FILTER (WHERE app AND day > $1::date - 7),
		       count(DISTINCT tg_id) FILTER (WHERE app AND day > $1::date - 30),
		       (SELECT count(*) FROM users WHERE created_at >= $2::timestamptz - interval '30 days'
		            AND created_at < $2::timestamptz - interval '7 days'),
		       (SELECT count(*) FROM users u WHERE created_at >= $2::timestamptz - interval '30 days'
		            AND created_at < $2::timestamptz - interval '7 days'
		            AND EXISTS (SELECT 1 FROM user_days d WHERE d.tg_id = u.tg_id AND d.day > $1::date - 7)),
		       (SELECT count(*) FROM outbox WHERE kind = $3 AND sent_at >= $2::timestamptz - interval '7 days'),
		       (SELECT count(*) FROM subscriptions)
		FROM user_days`, today, now, OutboxReminder).Scan(&since, &a.Users, &a.New7,
		&a.Active[0], &a.Active[1], &a.Active[2], &a.App[0], &a.App[1], &a.App[2],
		&a.Cohort, &a.Returned, &a.Reminders, &a.Subs)
	if err != nil {
		return a, fmt.Errorf("activity: %w", err)
	}
	if since != nil {
		a.Since = *since
	}
	rows, err := s.db.Query(ctx, `
		SELECT CASE WHEN n = 1 THEN 0 WHEN n <= 3 THEN 1 WHEN n <= 9 THEN 2 ELSE 3 END, count(*)
		FROM (SELECT count(*) AS n FROM user_days WHERE day > $1::date - 30 GROUP BY tg_id) t
		GROUP BY 1`, today)
	if err != nil {
		return a, fmt.Errorf("activity freq: %w", err)
	}
	for rows.Next() {
		var i, n int
		if err := rows.Scan(&i, &n); err != nil {
			rows.Close()
			return a, fmt.Errorf("activity freq: %w", err)
		}
		a.Freq[i] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, fmt.Errorf("activity freq: %w", err)
	}
	rows, err = s.db.Query(ctx, `
		SELECT metric, sum(n)::int FROM usage_counters WHERE day > $1::date - 7 GROUP BY metric`, today)
	if err != nil {
		return a, fmt.Errorf("activity actions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			return a, fmt.Errorf("activity actions: %w", err)
		}
		a.Actions[m] = n
	}
	return a, rows.Err()
}
