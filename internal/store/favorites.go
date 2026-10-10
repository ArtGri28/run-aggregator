package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aggregator/internal/event"
)

// FavOutcome — исход добавления старта в избранное.
type FavOutcome int

const (
	// FavAdded — запись создана.
	FavAdded FavOutcome = iota
	// FavAlready — старт уже в избранном: запись и время добавления прежние.
	FavAlready
	// FavLimit — у пользователя уже предел будущих избранных стартов.
	FavLimit
	// FavPast — дата старта прошла.
	FavPast
	// FavGone — события нет, оно скрыто либо его вид спорта выключен.
	FavGone
)

// Избранное (Phase 14) — пары «пользователь — событие». Записывается событие,
// корневое на момент добавления; более позднее слияние разрешается при чтении
// (D-04). Строки users избранному не нужны: ни один метод ниже её не создаёт и
// не читает.
//
// Упрощение: корень записи читается одним шагом merged_into_id (favRoot) —
// этого достаточно, пока слияние схлопывает цепочки до одного шага. Если
// цепочки перестанут схлопываться, запросы избранного переходят на обход
// цепочки, как RootEventID.
//
// Упрощение: таблица ссылается на событие внешним ключом, поэтому добавление,
// попавшее на запись прогона того же события, ждёт конца её транзакции —
// секунды; предела ожидания нет (14-SCHEMA.md, раздел «Блокировки»). Чтение и
// удаление не ждут.

// favRoot — корень события записи избранного: fe — строка events записи.
const favRoot = `COALESCE(fe.merged_into_id, fe.id)`

// favVisible — отбор показываемых избранных стартов пользователя: корневые
// события его записей, будущие, видимые, включённого вида спорта — то же
// правило, что у выдачи (futureListed); корень двух записей отбирается один
// раз. Параметры: $1 — «сегодня», $2 — пользователь.
func favVisible() string {
	return `
		FROM events e` + futureListed() + `
		  AND EXISTS (SELECT 1 FROM favorites f JOIN events fe ON fe.id = f.event_id
		              WHERE f.tg_id = $2::bigint AND ` + favRoot + ` = e.id)`
}

// AddFavorite добавляет старт eventID в избранное пользователя: записывается
// корень события (тем же одним шагом, что при чтении) и время now. Повторное
// добавление записи не меняет. Старт добавляется, только если он виден в
// выдаче на дату today; limit — предел показываемых (будущих) избранных
// стартов, прошедшие и скрытые его не занимают.
//
// Упрощение: предел проверяется в том же запросе, что и вставка, без
// блокировки — два одновременных добавления одного пользователя могут
// превысить предел на единицу. Если это станет заметно — блокировка по
// пользователю.
func (s *Store) AddFavorite(ctx context.Context, tgID, eventID int64, today, now time.Time, limit int) (FavOutcome, error) {
	q := `
		WITH root AS (
			SELECT COALESCE(x.merged_into_id, x.id) AS id FROM events x WHERE x.id = $3::bigint
		), ev AS (
			SELECT e.id, e.start_date >= $1::date AS future
			FROM events e
			WHERE e.id = (SELECT id FROM root) AND e.is_listed AND e.merged_into_id IS NULL` + sportVisible() + `
		), had AS (
			SELECT EXISTS (SELECT 1 FROM favorites f JOIN events fe ON fe.id = f.event_id
			               WHERE f.tg_id = $2::bigint AND ` + favRoot + ` = (SELECT id FROM root)) AS yes
		), cnt AS (
			SELECT count(*) AS n` + favVisible() + `
		), ins AS (
			INSERT INTO favorites (tg_id, event_id, created_at)
			SELECT $2::bigint, ev.id, $4::timestamptz FROM ev, had, cnt
			WHERE ev.future AND NOT had.yes AND cnt.n < $5::int
			ON CONFLICT DO NOTHING
			RETURNING 1
		)
		SELECT EXISTS (SELECT 1 FROM ev), COALESCE((SELECT future FROM ev), false),
		       (SELECT yes FROM had), (SELECT n FROM cnt), EXISTS (SELECT 1 FROM ins)`
	var found, future, had, added bool
	var n int
	if err := s.db.QueryRow(ctx, q, today, tgID, eventID, now, limit).Scan(&found, &future, &had, &n, &added); err != nil {
		return 0, fmt.Errorf("add favorite: %w", err)
	}
	switch {
	case !found:
		return FavGone, nil
	case !future:
		return FavPast, nil
	case added:
		return FavAdded, nil
	case !had && n >= limit:
		return FavLimit, nil
	}
	return FavAlready, nil
}

// RemoveFavorite убирает старт из избранного: удаляются все записи
// пользователя, ведущие к тому же корню, что и eventID. Работает и для
// скрытого, и для прошедшего старта; нет записи или события — не ошибка.
func (s *Store) RemoveFavorite(ctx context.Context, tgID, eventID int64) error {
	const q = `
		DELETE FROM favorites f USING events fe
		WHERE f.tg_id = $1 AND fe.id = f.event_id
		  AND ` + favRoot + ` = (SELECT COALESCE(x.merged_into_id, x.id) FROM events x WHERE x.id = $2)`
	if _, err := s.db.Exec(ctx, q, tgID, eventID); err != nil {
		return fmt.Errorf("remove favorite: %w", err)
	}
	return nil
}

// IsFavorite — ведёт ли хотя бы одна запись пользователя к корневому событию
// rootID. Видимость и дата события не проверяются.
func (s *Store) IsFavorite(ctx context.Context, tgID, rootID int64) (bool, error) {
	const q = `
		SELECT EXISTS (SELECT 1 FROM favorites f JOIN events fe ON fe.id = f.event_id
		               WHERE f.tg_id = $1 AND ` + favRoot + ` = $2)`
	var ok bool
	if err := s.db.QueryRow(ctx, q, tgID, rootID).Scan(&ok); err != nil {
		return false, fmt.Errorf("is favorite: %w", err)
	}
	return ok, nil
}

// ListFavorites — страница показываемых избранных стартов пользователя
// (favVisible) в порядке start_date, id и их общее число. Дистанции не
// заполняются. Только читает.
func (s *Store) ListFavorites(ctx context.Context, tgID int64, today time.Time, limit, offset int) ([]ListItem, int, error) {
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*)`+favVisible(), today, tgID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count favorites: %w", err)
	}
	q := `
		SELECT e.id, e.title, e.start_date, e.country_code, COALESCE(e.city, ''), e.status, e.sport` + favVisible() + `
		ORDER BY e.start_date, e.id
		LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(ctx, q, today, tgID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list favorites: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ListItem, error) {
		var it ListItem
		var status string
		if err := row.Scan(&it.ID, &it.Title, &it.StartDate, &it.CountryCode, &it.City, &status, &it.Sport); err != nil {
			return ListItem{}, err
		}
		it.Status = event.Status(status)
		return it, nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list favorites: %w", err)
	}
	return items, total, nil
}
