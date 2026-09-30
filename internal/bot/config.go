// Package bot — Telegram-бот: сборка на go-telegram/bot, кодек callback_data,
// экраны и обработчики. SQL здесь нет: всё через *store.Store.
package bot

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Config — настройки бота из окружения.
type Config struct {
	Token  string
	Admins []int64
}

var tokenRE = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

// LoadConfig читает TELEGRAM_BOT_TOKEN (обязателен) и ADMIN_TG_IDS
// (необязателен). Ошибка называет переменную, но не повторяет её значение.
func LoadConfig() (Config, error) {
	tok := os.Getenv("TELEGRAM_BOT_TOKEN")
	if strings.TrimSpace(tok) == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN не задан")
	}
	if !tokenRE.MatchString(tok) {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN: неверный формат")
	}
	c := Config{Token: tok}

	if ids := strings.TrimSpace(os.Getenv("ADMIN_TG_IDS")); ids != "" {
		for _, s := range strings.Split(ids, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err != nil || id <= 0 {
				return Config{}, fmt.Errorf("ADMIN_TG_IDS: неверное значение")
			}
			c.Admins = append(c.Admins, id)
		}
	}
	return c, nil
}
