package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// Заявки об ошибках, предложения стартов и сводка /quality (Phase 16,
// 16-SCHEMA.md). Хранилище знает только идентификаторы, причину и время:
// предложенная ссылка ему не передаётся. Пределы, окна и сроки — значения
// бота, время — по его часам.

// ReportOutcome — исход заявки об ошибке.
type ReportOutcome int

const (
	// ReportAdded — заявка записана.
	ReportAdded ReportOutcome = iota
	// ReportRepeat — та же пара «событие — причина» от пользователя уже есть
	// (R-3): строка не меняется.
	ReportRepeat
	// ReportLimited — предел заявок за окно исчерпан (R-4): строки нет.
	ReportLimited
)

// AddReport принимает заявку пользователя о событии eventID с причиной
// reason: записывается корень слияний (RootEventID), он же возвращается.
// Повтор проверяется раньше предела; предел — число заявок пользователя с
// created_at позже since. Видимость и дату события проверяет вызывающий.
//
// Упрощение: две одновременные заявки разных пар у пользователя на пределе
// могут обе пройти проверку — перебор на одну заявку; чинить — блокировкой по
// пользователю, если перебор станет заметен. Вставка берёт по строке события
// FOR KEY SHARE и ждёт конца записи прогона того же события — как у избранного.
func (s *Store) AddReport(ctx context.Context, tgID, eventID int64, reason string, now, since time.Time, limit int) (root int64, out ReportOutcome, err error) {
	err = s.InTx(ctx, func(tx *Store) error {
		r, err := tx.RootEventID(ctx, eventID)
		if err != nil {
			return fmt.Errorf("add report: %w", err)
		}
		root = r
		var had bool
		const dup = `SELECT EXISTS (SELECT 1 FROM error_reports WHERE tg_id = $1 AND event_id = $2 AND reason = $3)`
		if err := tx.db.QueryRow(ctx, dup, tgID, root, reason).Scan(&had); err != nil {
			return fmt.Errorf("add report: repeat: %w", err)
		}
		if had {
			out = ReportRepeat
			return nil
		}
		var n int
		if err := tx.db.QueryRow(ctx,
			`SELECT count(*) FROM error_reports WHERE tg_id = $1 AND created_at > $2`, tgID, since).Scan(&n); err != nil {
			return fmt.Errorf("add report: limit: %w", err)
		}
		if n >= limit {
			out = ReportLimited
			return nil
		}
		const ins = `
			INSERT INTO error_reports (tg_id, event_id, reason, created_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`
		cmd, err := tx.db.Exec(ctx, ins, tgID, root, reason, now)
		if err != nil {
			return fmt.Errorf("add report: %w", err)
		}
		// Одновременная заявка той же пары успела раньше — это повтор.
		out = ReportAdded
		if cmd.RowsAffected() == 0 {
			out = ReportRepeat
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return root, out, nil
}

// DeleteReport удаляет заявку, которую не удалось передать; eventID — корень,
// который вернул AddReport. Нет строки — не ошибка.
func (s *Store) DeleteReport(ctx context.Context, tgID, eventID int64, reason string) error {
	const q = `DELETE FROM error_reports WHERE tg_id = $1 AND event_id = $2 AND reason = $3`
	if _, err := s.db.Exec(ctx, q, tgID, eventID, reason); err != nil {
		return fmt.Errorf("delete report: %w", err)
	}
	return nil
}

// SuggestOutcome — исход попытки принять сообщение как предложение старта.
type SuggestOutcome int

const (
	// SuggestTaken — отметка действовала и потреблена, предел не исчерпан:
	// строка предложения записана заранее, ссылка пересылается.
	SuggestTaken SuggestOutcome = iota
	// SuggestNotWaiting — отметки нет либо она истекла: сообщение не предложение.
	SuggestNotWaiting
	// SuggestLimited — отметка потреблена, но предел предложений исчерпан (P-4).
	SuggestLimited
)

// SuggestBegin ставит отметку «жду ссылку» до expiresAt (повторный вызов
// заменяет момент) и снимает отметки ожидания города и обращения в поддержку:
// режимы ввода не действуют одновременно (Q-7). Порядок блокировок — профиль,
// отметка поддержки, отметка ссылки — как у StartUser.
func (s *Store) SuggestBegin(ctx context.Context, tgID int64, expiresAt time.Time) error {
	return s.InTx(ctx, func(tx *Store) error { return tx.suggestBegin(ctx, tgID, expiresAt, "suggest begin") })
}

func (s *Store) suggestBegin(ctx context.Context, tgID int64, expiresAt time.Time, op string) error {
	const clearCity = `
		UPDATE user_profiles SET awaiting_city_until = NULL, awaiting_city_mode = NULL
		WHERE tg_id = $1 AND awaiting_city_until IS NOT NULL`
	if _, err := s.db.Exec(ctx, clearCity, tgID); err != nil {
		return fmt.Errorf("%s: clear city: %w", op, err)
	}
	if err := s.dropSupportAwaiting(ctx, tgID, op); err != nil {
		return err
	}
	const upsert = `
		INSERT INTO suggest_awaiting (tg_id, expires_at) VALUES ($1, $2)
		ON CONFLICT (tg_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`
	if _, err := s.db.Exec(ctx, upsert, tgID, expiresAt); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// SuggestCancel снимает отметку «жду ссылку»; отсутствие отметки не ошибка.
func (s *Store) SuggestCancel(ctx context.Context, tgID int64) error {
	return s.dropSuggestAwaiting(ctx, tgID, "suggest cancel")
}

// SuggestAwaiting читает отметку без записи. Истёкшая отметка возвращается
// как есть (ok = true): сравнить момент с часами — дело вызывающего, решает
// же SuggestTake. Нужна, чтобы отказать в формате (P-3), не трогая отметку.
func (s *Store) SuggestAwaiting(ctx context.Context, tgID int64) (expiresAt time.Time, ok bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT expires_at FROM suggest_awaiting WHERE tg_id = $1`, tgID).Scan(&expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("suggest awaiting: %w", err)
	}
	return expiresAt, true, nil
}

// SuggestTake принимает годную ссылку как предложение: отметка читается под
// блокировкой строки и потребляется, предел — число предложений пользователя с
// created_at позже since, строка предложения пишется до отправки и её id
// возвращается (id = 0 при любом исходе, кроме SuggestTaken). Из двух
// одновременных сообщений предложением становится ровно одно. Истёкшая отметка
// удаляется — SuggestNotWaiting.
func (s *Store) SuggestTake(ctx context.Context, tgID int64, now, since time.Time, limit int) (id int64, out SuggestOutcome, err error) {
	out = SuggestNotWaiting
	err = s.InTx(ctx, func(tx *Store) error {
		var until time.Time
		err := tx.db.QueryRow(ctx, `SELECT expires_at FROM suggest_awaiting WHERE tg_id = $1 FOR UPDATE`, tgID).Scan(&until)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("suggest take: lock: %w", err)
		}
		if err := tx.dropSuggestAwaiting(ctx, tgID, "suggest take"); err != nil {
			return err
		}
		if !until.After(now) {
			return nil
		}
		var n int
		if err := tx.db.QueryRow(ctx,
			`SELECT count(*) FROM start_suggestions WHERE tg_id = $1 AND created_at > $2`, tgID, since).Scan(&n); err != nil {
			return fmt.Errorf("suggest take: limit: %w", err)
		}
		if n >= limit {
			out = SuggestLimited
			return nil
		}
		if err := tx.db.QueryRow(ctx,
			`INSERT INTO start_suggestions (tg_id, created_at) VALUES ($1, $2) RETURNING id`, tgID, now).Scan(&id); err != nil {
			return fmt.Errorf("suggest take: %w", err)
		}
		out = SuggestTaken
		return nil
	})
	if err != nil {
		return 0, SuggestNotWaiting, err
	}
	return id, out, nil
}

// SuggestRelease откатывает предложение, которое не удалось передать (P-5):
// удаляет строку id и снова ставит отметку до expiresAt, как SuggestBegin.
func (s *Store) SuggestRelease(ctx context.Context, tgID, id int64, expiresAt time.Time) error {
	return s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.db.Exec(ctx, `DELETE FROM start_suggestions WHERE id = $1`, id); err != nil {
			return fmt.Errorf("suggest release: %w", err)
		}
		return tx.suggestBegin(ctx, tgID, expiresAt, "suggest release")
	})
}

// dropSuggestAwaiting снимает отметку «жду ссылку»; нет отметки — не ошибка.
func (s *Store) dropSuggestAwaiting(ctx context.Context, tgID int64, op string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM suggest_awaiting WHERE tg_id = $1`, tgID); err != nil {
		return fmt.Errorf("%s: clear suggest awaiting: %w", op, err)
	}
	return nil
}

// PruneReports удаляет заявки и предложения, созданные раньше before (Q-4).
// Возвращает число удалённых строк обеих таблиц.
func (s *Store) PruneReports(ctx context.Context, before time.Time) (int, error) {
	var n int
	err := s.InTx(ctx, func(tx *Store) error {
		for _, q := range []string{
			`DELETE FROM error_reports WHERE created_at < $1`,
			`DELETE FROM start_suggestions WHERE created_at < $1`,
		} {
			cmd, err := tx.db.Exec(ctx, q, before)
			if err != nil {
				return fmt.Errorf("prune reports: %w", err)
			}
			n += int(cmd.RowsAffected())
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// QualityRow — строка сводки /quality «источник · вид спорта»: видимые
// будущие события, у которых есть запись источника не в gone и не
// out_of_scope. Unique — нет записей других источников в любом состоянии
// (Q-5); Complete — все четыре признака D-03, остальные — по одному признаку.
type QualityRow struct {
	Source                                  string
	Sport                                   event.Sport
	Events, Unique, Complete                int
	Dated, WithCity, WithDistance, WithLink int
}

// QualitySource — свежесть источника (D-04): последний завершённый прогон
// (HasRun = false — прогонов нет) и суммы created и changed прогонов ok за
// окно.
type QualitySource struct {
	Code             string
	HasRun           bool
	Status           string
	FinishedAt       time.Time
	Created, Changed int
}

// Quality — сводка /quality: Total — различные видимые будущие события; Rows —
// по коду источника, внутри — по порядку event.Sports, пары без событий не
// попадают; Sources — все источники по коду; Reports — заявки об ошибках по
// причине и Suggestions — предложения за окно.
type Quality struct {
	Total       int
	Rows        []QualityRow
	Sources     []QualitySource
	Reports     map[string]int
	Suggestions int
}

// Quality читает сводку качества каталога: события — будущие на today, как
// выдача (futureListed); прогоны, заявки и предложения — позже since.
// Признаки даты и ссылки считаются здесь функциями event.DateTBD и
// event.ValidURL: второй реализации в SQL нет (D-03).
func (s *Store) Quality(ctx context.Context, today, since time.Time) (Quality, error) {
	q := Quality{Reports: map[string]int{}}
	const events = `
		SELECT DISTINCT e.id, e.sport, s.code, e.title,
		       COALESCE(e.city, '') <> '',
		       EXISTS (SELECT 1 FROM event_distances d WHERE d.event_id = e.id),
		       COALESCE(e.registration_url, ''),
		       (SELECT count(DISTINCT x.source_id) FROM source_events x WHERE x.event_id = e.id) = 1
		FROM events e
		JOIN source_events se ON se.event_id = e.id
		                     AND se.state <> 'gone' AND se.match_state <> 'out_of_scope'
		JOIN sources s ON s.id = se.source_id`
	rows, err := s.db.Query(ctx, events+futureListed(), today)
	if err != nil {
		return Quality{}, fmt.Errorf("quality: events: %w", err)
	}
	type key struct {
		src   string
		sport event.Sport
	}
	byKey := map[key]*QualityRow{}
	ids := map[int64]bool{}
	var id int64
	var sport, code, title, url string
	var hasCity, hasDist, unique bool
	_, err = pgx.ForEachRow(rows, []any{&id, &sport, &code, &title, &hasCity, &hasDist, &url, &unique}, func() error {
		ids[id] = true
		k := key{code, event.Sport(sport)}
		r := byKey[k]
		if r == nil {
			r = &QualityRow{Source: code, Sport: k.sport}
			byKey[k] = r
		}
		dated, link := !event.DateTBD(title), event.ValidURL(url)
		r.Events++
		r.Unique += b2i(unique)
		r.Dated += b2i(dated)
		r.WithCity += b2i(hasCity)
		r.WithDistance += b2i(hasDist)
		r.WithLink += b2i(link)
		r.Complete += b2i(dated && hasCity && hasDist && link)
		return nil
	})
	if err != nil {
		return Quality{}, fmt.Errorf("quality: events: %w", err)
	}
	q.Total = len(ids)
	sportOrder := func(sp event.Sport) int {
		return slices.IndexFunc(event.Sports[:], func(i event.SportInfo) bool { return i.Code == sp })
	}
	for _, r := range byKey {
		q.Rows = append(q.Rows, *r)
	}
	slices.SortFunc(q.Rows, func(a, b QualityRow) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(sportOrder(a.Sport), sportOrder(b.Sport)))
	})

	runs, err := s.LatestFinishedRuns(ctx)
	if err != nil {
		return Quality{}, fmt.Errorf("quality: %w", err)
	}
	const fresh = `
		SELECT s.code, COALESCE(c.created, 0), COALESCE(c.changed, 0)
		FROM sources s
		LEFT JOIN (SELECT r.source_id,
		                  sum((r.metrics->>'created')::int) AS created,
		                  sum((r.metrics->>'changed')::int) AS changed
		           FROM ingest_runs r
		           WHERE r.status = 'ok' AND r.finished_at >= $1
		           GROUP BY r.source_id) c ON c.source_id = s.id
		ORDER BY s.code`
	rows, err = s.db.Query(ctx, fresh, since)
	if err != nil {
		return Quality{}, fmt.Errorf("quality: runs: %w", err)
	}
	q.Sources, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (QualitySource, error) {
		var f QualitySource
		err := row.Scan(&f.Code, &f.Created, &f.Changed)
		return f, err
	})
	if err != nil {
		return Quality{}, fmt.Errorf("quality: runs: %w", err)
	}
	for i := range q.Sources {
		for _, r := range runs {
			if r.Code == q.Sources[i].Code {
				q.Sources[i].HasRun, q.Sources[i].Status, q.Sources[i].FinishedAt = true, r.Status, r.FinishedAt
			}
		}
	}

	rows, err = s.db.Query(ctx, `SELECT reason, count(*) FROM error_reports WHERE created_at >= $1 GROUP BY reason`, since)
	if err != nil {
		return Quality{}, fmt.Errorf("quality: reports: %w", err)
	}
	var reason string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&reason, &n}, func() error { q.Reports[reason] = n; return nil }); err != nil {
		return Quality{}, fmt.Errorf("quality: reports: %w", err)
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM start_suggestions WHERE created_at >= $1`, since).Scan(&q.Suggestions); err != nil {
		return Quality{}, fmt.Errorf("quality: suggestions: %w", err)
	}
	return q, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
