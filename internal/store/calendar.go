package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Настройки приложения (app_settings) и ссылки подписного календаря (Phase 21,
// 21-CONTEXT.md D-11, D-12): у ссылки хранится только SHA-256 токена.

// SettingBotUsername — имя бота: его пишет бот при запуске, читает webapp.
const SettingBotUsername = "bot_username"

// SetSetting записывает значение настройки key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	if err != nil {
		return fmt.Errorf("set setting: %w", err)
	}
	return nil
}

// Setting — значение настройки key; нет — "".
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("setting: %w", err)
	}
	return v, nil
}

// SetFeed выдаёт пользователю ссылку календаря с хешем hash; прежняя ссылка
// перестаёт работать.
func (s *Store) SetFeed(ctx context.Context, tgID int64, hash []byte, now time.Time) error {
	_, err := s.db.Exec(ctx, `INSERT INTO calendar_feeds (tg_id, token_hash, created_at) VALUES ($1, $2, $3)
		ON CONFLICT (tg_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = EXCLUDED.created_at`, tgID, hash, now)
	if err != nil {
		return fmt.Errorf("set feed: %w", err)
	}
	return nil
}

// HasFeed — у пользователя есть ссылка календаря.
func (s *Store) HasFeed(ctx context.Context, tgID int64) (bool, error) {
	var ok bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM calendar_feeds WHERE tg_id = $1)`, tgID).Scan(&ok); err != nil {
		return false, fmt.Errorf("has feed: %w", err)
	}
	return ok, nil
}

// DeleteFeed отключает ссылку календаря; её не было — без ошибки.
func (s *Store) DeleteFeed(ctx context.Context, tgID int64) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM calendar_feeds WHERE tg_id = $1`, tgID); err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}
	return nil
}

// FeedOwner — пользователь ссылки календаря с хешем hash; ok = false — такой нет.
func (s *Store) FeedOwner(ctx context.Context, hash []byte) (tgID int64, ok bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT tg_id FROM calendar_feeds WHERE token_hash = $1`, hash).Scan(&tgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("feed owner: %w", err)
	}
	return tgID, true, nil
}
