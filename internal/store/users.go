package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrStaleFilters — версия фильтров не совпала с ожидаемой либо пользователя
// нет: кнопка, которая просила изменение, устарела.
var ErrStaleFilters = errors.New("filters: устаревшая версия")

// touchUser создаёт пользователя при первом обращении и обновляет
// last_seen_at; вернувшаяся строка заблокирована до конца транзакции.
// BOT-08: о пользователе хранятся только tg_id, filters, filters_v и метки времени.
const touchUser = `
	INSERT INTO users (tg_id) VALUES ($1)
	ON CONFLICT (tg_id) DO UPDATE SET last_seen_at = now()
	RETURNING filters, filters_v`

// Filters возвращает фильтры пользователя (jsonb) и их версию; первое
// обращение создаёт пользователя с фильтрами по умолчанию.
func (s *Store) Filters(ctx context.Context, tgID int64) (raw []byte, v int, err error) {
	if err := s.db.QueryRow(ctx, touchUser, tgID).Scan(&raw, &v); err != nil {
		return nil, 0, fmt.Errorf("filters: %w", err)
	}
	return raw, v, nil
}

// PeekFilters читает фильтры и версию без единой записи: пользователь не
// создаётся, last_seen_at не меняется. Нет пользователя — ok = false,
// фильтры по умолчанию и версия 0.
func (s *Store) PeekFilters(ctx context.Context, tgID int64) (raw []byte, v int, ok bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT filters, filters_v FROM users WHERE tg_id = $1`, tgID).Scan(&raw, &v)
	if errors.Is(err, pgx.ErrNoRows) {
		return []byte(`{}`), 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("peek filters: %w", err)
	}
	return raw, v, true, nil
}

// UpdateFilters заменяет фильтры результатом fn(текущие). Строка
// пользователя заблокирована на время вызова, поэтому быстрые нажатия
// не затирают друг друга; filters_v растёт только при реальном изменении
// значения. Ошибка fn отменяет изменение.
func (s *Store) UpdateFilters(ctx context.Context, tgID int64, fn func(cur []byte) ([]byte, error)) (raw []byte, v int, err error) {
	return s.updateFilters(ctx, tgID, -1, fn)
}

// UpdateFiltersAt — UpdateFilters только при версии фильтров wantV. Версия
// проверяется под блокировкой строки в той же транзакции, что и запись:
// между проверкой и изменением нет окна. Другая версия или нет пользователя —
// ErrStaleFilters, и в базу не пишется ничего (в том числе last_seen_at).
func (s *Store) UpdateFiltersAt(ctx context.Context, tgID int64, wantV int, fn func(cur []byte) ([]byte, error)) (raw []byte, v int, err error) {
	return s.updateFilters(ctx, tgID, wantV, fn)
}

// updateFilters: wantV < 0 — без проверки версии, пользователь создаётся.
func (s *Store) updateFilters(ctx context.Context, tgID int64, wantV int, fn func(cur []byte) ([]byte, error)) (raw []byte, v int, err error) {
	err = s.InTx(ctx, func(tx *Store) error {
		var cur []byte
		if wantV < 0 {
			if err := tx.db.QueryRow(ctx, touchUser, tgID).Scan(&cur, &v); err != nil {
				return fmt.Errorf("update filters: lock: %w", err)
			}
		} else {
			err := tx.db.QueryRow(ctx, `SELECT filters, filters_v FROM users WHERE tg_id = $1 FOR UPDATE`, tgID).Scan(&cur, &v)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && v != wantV) {
				return ErrStaleFilters
			}
			if err != nil {
				return fmt.Errorf("update filters: lock: %w", err)
			}
		}
		next, err := fn(cur)
		if err != nil {
			return err
		}
		const q = `
			UPDATE users
			SET filters = $2,
			    filters_v = filters_v + CASE WHEN filters IS DISTINCT FROM $2::jsonb THEN 1 ELSE 0 END,
			    last_seen_at = now()
			WHERE tg_id = $1
			RETURNING filters, filters_v`
		if err := tx.db.QueryRow(ctx, q, tgID, next).Scan(&raw, &v); err != nil {
			return fmt.Errorf("update filters: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return raw, v, nil
}
