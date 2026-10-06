package bot

import (
	"reflect"
	"strings"
	"testing"
)

var configVars = []string{"TELEGRAM_BOT_TOKEN", "ADMIN_TG_IDS", "SUPPORT_CHAT_ID"}

// setEnv очищает переменные бота и выставляет заданные.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range configVars {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadConfig(t *testing.T) {
	setEnv(t, nil)
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Fatalf("без токена: err = %v, want упоминание TELEGRAM_BOT_TOKEN", err)
	}

	setEnv(t, map[string]string{"TELEGRAM_BOT_TOKEN": "123456:ABC-def_1", "ADMIN_TG_IDS": " 11, 22 "})
	cfg, err := LoadConfig()
	if err != nil || cfg.Token != "123456:ABC-def_1" || !reflect.DeepEqual(cfg.Admins, []int64{11, 22}) {
		t.Fatalf("LoadConfig = %+v, %v; want token and admins [11 22]", cfg, err)
	}

	setEnv(t, map[string]string{"TELEGRAM_BOT_TOKEN": "123456:ABC-def_1", "ADMIN_TG_IDS": ""})
	if cfg, err = LoadConfig(); err != nil || len(cfg.Admins) != 0 {
		t.Fatalf("пустой ADMIN_TG_IDS: %+v, %v; want пустой список без ошибки", cfg, err)
	}

	// SUPPORT_CHAT_ID необязательна: пусто или не задана — поддержка выключена (0).
	for _, tc := range []struct {
		val  string
		want int64
	}{{"", 0}, {"   ", 0}, {"-1001234567890", -1001234567890}, {"  -1001234567890\t", -1001234567890}, {"-5", -5}} {
		setEnv(t, map[string]string{"TELEGRAM_BOT_TOKEN": "123456:ABC-def_1", "SUPPORT_CHAT_ID": tc.val})
		if cfg, err = LoadConfig(); err != nil || cfg.SupportChatID != tc.want {
			t.Fatalf("SUPPORT_CHAT_ID=%q: %+v, %v; want %d", tc.val, cfg, err, tc.want)
		}
	}
	setEnv(t, map[string]string{"TELEGRAM_BOT_TOKEN": "123456:ABC-def_1"})
	if cfg, err = LoadConfig(); err != nil || cfg.SupportChatID != 0 {
		t.Fatalf("без SUPPORT_CHAT_ID: %+v, %v; want 0 без ошибки", cfg, err)
	}
	// Ноль и положительное число (идентификатор человека — обращения ушли бы в
	// личный чат), не число и дробь — ошибка старта без повтора значения.
	for _, v := range []string{"0", "-0", "424242", "+424242", "bad7value", "-100.5", "-", "1e3", "--5", "- 5", "-1001234567890123456789"} {
		setEnv(t, map[string]string{"TELEGRAM_BOT_TOKEN": "123456:ABC-def_1", "SUPPORT_CHAT_ID": v})
		_, err := LoadConfig()
		if err == nil || !strings.Contains(err.Error(), "SUPPORT_CHAT_ID") {
			t.Fatalf("SUPPORT_CHAT_ID=%q: err = %v, want упоминание SUPPORT_CHAT_ID", v, err)
		}
		if len(v) > 2 && strings.Contains(err.Error(), v) {
			t.Fatalf("SUPPORT_CHAT_ID=%q: err = %q повторяет значение", v, err)
		}
	}

	bad := []struct {
		name, token, admins, mention, leak string
	}{
		{"токен с пробелом", "123:LEAK7 TOKEN", "", "TELEGRAM_BOT_TOKEN", "LEAK7"},
		{"токен без двоеточия", "LEAK7token", "", "TELEGRAM_BOT_TOKEN", "LEAK7"},
		{"токен с управляющим символом", "123:LEAK7\ttoken", "", "TELEGRAM_BOT_TOKEN", "LEAK7"},
		{"нечисловой админ", "123:ABC", "12,bad7value", "ADMIN_TG_IDS", "bad7value"},
		{"отрицательный админ", "123:ABC", "-5", "ADMIN_TG_IDS", "-5"},
		{"нулевой админ", "123:ABC", "0", "ADMIN_TG_IDS", ""},
		{"пустой элемент", "123:ABC", "1,,2", "ADMIN_TG_IDS", ""},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, map[string]string{"TELEGRAM_BOT_TOKEN": tc.token, "ADMIN_TG_IDS": tc.admins})
			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), tc.mention) {
				t.Fatalf("err = %v, want упоминание %s", err, tc.mention)
			}
			if tc.leak != "" && strings.Contains(err.Error(), tc.leak) {
				t.Fatalf("err = %q повторяет значение %q", err, tc.leak)
			}
		})
	}
}
