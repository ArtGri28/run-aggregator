package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrStaleProfile — версия профиля не совпала с ожидаемой либо строки
// профиля нет: кнопка анкеты или профиля, которая просила изменение, устарела.
// Версия профиля (profile_v) отдельна от filters_v (D-08).
var ErrStaleProfile = errors.New("profile: устаревшая версия")

// Статусы анкеты (user_profiles.status).
const (
	ProfileNotStarted = "not_started"
	ProfileInProgress = "in_progress"
	ProfileCompleted  = "completed"
	ProfileSkipped    = "skipped"
)

// Profile — ответы добровольной анкеты. NULL в базе — нулевое значение поля:
// пустая строка, AgeGroup = -1, нулевое время.
type Profile struct {
	Status            string
	Gender            string // "m", "f" или пусто
	AgeGroup          int    // 0..5 или -1
	Country           string // двухбуквенный код, ZZ — другая страна
	City              string
	CityKey           string
	Sports            []string
	AwaitingCityUntil time.Time // нулевое время — текст города не ждём
	AwaitingCityMode  string    // "w" (анкета), "e" (редактирование) или пусто
}

// profileSelect читает строку профиля; NULL приводится к нулевым значениям Profile.
const profileSelect = `
	SELECT status, COALESCE(gender, ''), COALESCE(age_group, -1),
	       COALESCE(residence_country_code, ''), COALESCE(residence_city, ''),
	       COALESCE(residence_city_key, ''), sports,
	       awaiting_city_until, COALESCE(awaiting_city_mode, ''), profile_v
	FROM user_profiles WHERE tg_id = $1`

func scanProfile(row pgx.Row) (p Profile, v int, err error) {
	var until *time.Time
	var age int16
	err = row.Scan(&p.Status, &p.Gender, &age, &p.Country, &p.City, &p.CityKey,
		&p.Sports, &until, &p.AwaitingCityMode, &v)
	if err != nil {
		return Profile{}, 0, err
	}
	p.AgeGroup = int(age)
	if until != nil {
		p.AwaitingCityUntil = *until
	}
	return p, v, nil
}

// StartUser — транзакция /start: пользователь создаётся (или обновляется
// last_seen_at), затем создаётся строка профиля. Строка профиля появляется
// здесь, до отправки экрана, чтобы «предложить анкету» случилось ровно один
// раз на пользователя: offered = true только у вызова, который создал строку
// (D-06). Повторный /start снимает отметку ожидания города (D-10).
// Это единственное место, где одна транзакция касается и users, и
// user_profiles; filters_v возвращается для главного меню, фильтры не меняются.
func (s *Store) StartUser(ctx context.Context, tgID int64) (filtersV, profileV int, offered bool, err error) {
	err = s.InTx(ctx, func(tx *Store) error {
		var raw []byte
		if err := tx.db.QueryRow(ctx, touchUser, tgID).Scan(&raw, &filtersV); err != nil {
			return fmt.Errorf("start user: touch: %w", err)
		}
		const ins = `
			INSERT INTO user_profiles (tg_id) VALUES ($1)
			ON CONFLICT (tg_id) DO NOTHING RETURNING profile_v`
		err := tx.db.QueryRow(ctx, ins, tgID).Scan(&profileV)
		if err == nil {
			offered = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("start user: create profile: %w", err)
		}
		const clear = `
			UPDATE user_profiles SET awaiting_city_until = NULL, awaiting_city_mode = NULL
			WHERE tg_id = $1 RETURNING profile_v`
		if err := tx.db.QueryRow(ctx, clear, tgID).Scan(&profileV); err != nil {
			return fmt.Errorf("start user: clear awaiting: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, 0, false, err
	}
	return filtersV, profileV, offered, nil
}

// PeekProfile читает профиль и его версию без единой записи: ни users, ни
// user_profiles не получают строк. Нет строки — ok = false, пустой профиль
// (AgeGroup = -1) и версия 0.
func (s *Store) PeekProfile(ctx context.Context, tgID int64) (p Profile, v int, ok bool, err error) {
	p, v, err = scanProfile(s.db.QueryRow(ctx, profileSelect, tgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{AgeGroup: -1}, 0, false, nil
	}
	if err != nil {
		return Profile{}, 0, false, fmt.Errorf("peek profile: %w", err)
	}
	return p, v, true, nil
}

// UpdateProfileAt заменяет профиль результатом fn(текущий) при версии wantV.
// Версия проверяется под блокировкой строки в той же транзакции, что и запись:
// между проверкой и изменением нет окна. Другая версия или нет строки профиля —
// ErrStaleProfile, и в базу не пишется ничего; строка users не затрагивается
// (PRF-06). Ошибка fn отменяет изменение и возвращается как есть.
// profile_v и updated_at растут только при изменении ответов или статуса:
// отметка ожидания города — служебная, её постановка и снятие версию не
// меняют, иначе кнопка, показанная до ввода города, устарела бы от самого
// ввода. started_at и completed_at ставятся один раз по метке now.
// Переходы статусов хранилище не решает — их задаёт бот через fn.
func (s *Store) UpdateProfileAt(ctx context.Context, tgID int64, wantV int, now time.Time, fn func(Profile) (Profile, error)) (Profile, int, error) {
	var out Profile
	var v int
	err := s.InTx(ctx, func(tx *Store) error {
		cur, curV, err := scanProfile(tx.db.QueryRow(ctx, profileSelect+` FOR UPDATE`, tgID))
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && curV != wantV) {
			return ErrStaleProfile
		}
		if err != nil {
			return fmt.Errorf("update profile: lock: %w", err)
		}
		next, err := fn(cur)
		if err != nil {
			return err
		}
		out, v = cur, curV
		answers := cur.Status != next.Status || cur.Gender != next.Gender || cur.AgeGroup != next.AgeGroup ||
			cur.Country != next.Country || cur.City != next.City || cur.CityKey != next.CityKey ||
			!slices.Equal(cur.Sports, next.Sports)
		if !answers && cur.AwaitingCityUntil.Equal(next.AwaitingCityUntil) && cur.AwaitingCityMode == next.AwaitingCityMode {
			return nil
		}
		if next.Sports == nil {
			next.Sports = []string{}
		}
		var until *time.Time
		if !next.AwaitingCityUntil.IsZero() {
			until = &next.AwaitingCityUntil
		}
		const q = `
			UPDATE user_profiles
			SET status = $2, gender = NULLIF($3, ''), age_group = NULLIF($4, -1),
			    residence_country_code = NULLIF($5, ''), residence_city = NULLIF($6, ''),
			    residence_city_key = NULLIF($7, ''), sports = $8,
			    awaiting_city_until = $9, awaiting_city_mode = NULLIF($10, ''),
			    profile_v = profile_v + CASE WHEN $11::boolean THEN 1 ELSE 0 END,
			    updated_at = CASE WHEN $11::boolean THEN $12::timestamptz ELSE updated_at END,
			    started_at = COALESCE(started_at,
			        CASE WHEN $11::boolean AND $2::text IN ('in_progress', 'completed') THEN $12::timestamptz END),
			    completed_at = COALESCE(completed_at,
			        CASE WHEN $11::boolean AND $2::text = 'completed' THEN $12::timestamptz END)
			WHERE tg_id = $1
			RETURNING profile_v`
		if err := tx.db.QueryRow(ctx, q, tgID, next.Status, next.Gender, next.AgeGroup, next.Country,
			next.City, next.CityKey, next.Sports, until, next.AwaitingCityMode, answers, now).Scan(&v); err != nil {
			return fmt.Errorf("update profile: %w", err)
		}
		out = next
		return nil
	})
	if err != nil {
		return Profile{}, 0, err
	}
	return out, v, nil
}
