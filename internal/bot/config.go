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

// Config — настройки бота из окружения. SupportChatID — идентификатор группы
// поддержки; 0 — поддержка выключена целиком (D-03).
type Config struct {
	Token         string
	Admins        []int64
	SupportChatID int64
	// Domain — домен Mini App (WEBAPP_DOMAIN) для ссылки подписного календаря;
	// "" — ссылки нет.
	Domain string
}

var domainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

var tokenRE = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

// LoadConfig читает TELEGRAM_BOT_TOKEN (обязателен), ADMIN_TG_IDS и
// SUPPORT_CHAT_ID (необязательны). SUPPORT_CHAT_ID — целое строго меньше нуля
// (идентификатор группы); пусто — поддержка выключена. Положительное число —
// идентификатор человека: обращения ушли бы в его личный чат, поэтому это
// ошибка старта. Ошибка называет переменную, но не повторяет её значение.
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
	if v := strings.TrimSpace(os.Getenv("SUPPORT_CHAT_ID")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id >= 0 {
			return Config{}, errors.New("SUPPORT_CHAT_ID: неверное значение")
		}
		c.SupportChatID = id
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("WEBAPP_DOMAIN"))); v != "" {
		if !domainRE.MatchString(v) {
			return Config{}, errors.New("WEBAPP_DOMAIN: неверное значение")
		}
		c.Domain = v
	}
	return c, nil
}
