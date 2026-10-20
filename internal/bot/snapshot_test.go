package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

// screensDir — эталоны экранов. Тесты их только читают: эталон создаётся и
// меняется правкой файла вместе с контрактом экранов, автообновления нет.
const screensDir = "testdata/screens"

// renderCall — снимок вызова Bot API: метод, parse_mode, текст и клавиатура
// построчно «[подпись] → callback_data» (или «→ url:<адрес>»). Для ответа на
// callback — текст уведомления и show_alert.
func renderCall(t *testing.T, c call) string {
	t.Helper()
	var sb strings.Builder
	fmt.Fprintf(&sb, "method: %s\n", c.Method)
	if c.Method == "answerCallbackQuery" {
		fmt.Fprintf(&sb, "text: %s\nshow_alert: %s\n", c.Form["text"], c.Form["show_alert"])
		return sb.String()
	}
	if c.Method == "sendDocument" {
		fmt.Fprintf(&sb, "document: %s\ncaption: %s\n", c.Form["document_name"], c.Form["caption"])
		return sb.String()
	}
	fmt.Fprintf(&sb, "parse_mode: %s\n---\n%s\n---\n", c.Form["parse_mode"], c.Form["text"])
	if raw := c.Form["reply_markup"]; raw != "" {
		var kb models.InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(raw), &kb); err != nil {
			t.Fatalf("reply_markup: %v", err)
		}
		for _, r := range kb.InlineKeyboard {
			var cells []string
			for _, b := range r {
				target := b.CallbackData
				if b.URL != "" {
					target = "url:" + b.URL
				}
				cells = append(cells, "["+b.Text+"] → "+target)
			}
			sb.WriteString(strings.Join(cells, " | ") + "\n")
		}
	}
	return sb.String()
}

// matchSnapshot сверяет снимок с эталоном testdata/screens/<name>.txt.
func matchSnapshot(t *testing.T, name, got string) {
	t.Helper()
	if err := matchSnapshotIn(screensDir, name, got); err != nil {
		t.Errorf("%v\n--- получено (эталон правится вручную) ---\n%s", err, got)
	}
}

func matchSnapshotIn(dir, name, got string) error {
	want, err := os.ReadFile(filepath.Join(dir, name+".txt"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("снимок %s: нет эталона", name)
	}
	if err != nil {
		return err
	}
	if string(want) != got {
		return fmt.Errorf("снимок %s: не совпадает с эталоном", name)
	}
	return nil
}

// TestSnapshotSelf: сверка ловит изменение в один символ и отсутствие эталона
// (на эталоне start, без записи файлов).
func TestSnapshotSelf(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(screensDir, "start.txt"))
	if err != nil || len(want) == 0 {
		t.Fatalf("эталон start: %v", err)
	}
	if err := matchSnapshotIn(screensDir, "start", string(want)); err != nil {
		t.Errorf("совпадающий снимок: %v", err)
	}
	changed := []byte(string(want))
	changed[len(changed)-2] ^= 1
	if err := matchSnapshotIn(screensDir, "start", string(changed)); err == nil {
		t.Error("изменённый на один символ снимок не обнаружен")
	}
	if err := matchSnapshotIn(screensDir, "no-such-screen", string(want)); err == nil {
		t.Error("отсутствующий эталон не обнаружен")
	}
}
