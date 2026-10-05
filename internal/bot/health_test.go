package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"

	"aggregator/internal/store/storetest"
)

const meOK = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"t","username":"t_bot"}}`

// LO-06: бот «здоров», только пока getMe отвечает и библиотека не сообщала
// об ошибках за последние две минуты.
func TestHealthy(t *testing.T) {
	logs := captureLogs(t)
	meFails := false
	api := fakeTelegram(t, func(c call, _ int) string {
		switch {
		case c.Method == "getMe" && meFails:
			return apiErr(401, "Unauthorized", 0)
		case c.Method == "getMe":
			return meOK
		case c.Method == "getUpdates":
			return apiErr(401, "Unauthorized", 0)
		}
		return ""
	})
	b, err := New(Config{Token: "123:TEST"}, nil, tgbot.WithServerURL(api.srv.URL), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	now := fixtureNow
	b.Now = func() time.Time { return now }
	ctx := context.Background()

	if !b.Healthy(ctx) {
		t.Fatal("getMe отвечает, ошибок не было: Healthy = false")
	}
	meFails = true
	if b.Healthy(ctx) {
		t.Fatal("getMe отвечает ошибкой: Healthy = true")
	}
	meFails = false

	// Библиотека вызывает обработчик ошибок на ошибке getUpdates.
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); b.Start(sctx) }()
	for deadline := time.Now().Add(10 * time.Second); b.lastErr.Load() == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("обработчик ошибок библиотеки не вызван на ошибке getUpdates")
		}
	}
	cancel()
	<-done
	now = time.Unix(0, b.lastErr.Load()).Add(errQuiet - time.Second)
	if b.Healthy(ctx) {
		t.Fatal("ошибка getUpdates меньше двух минут назад: Healthy = true")
	}
	now = now.Add(time.Second)
	if !b.Healthy(ctx) {
		t.Fatal("две минуты без ошибок: Healthy = false")
	}
	if strings.Contains(logs.String(), "123:TEST") {
		t.Fatalf("токен в логе:\n%s", logs)
	}
}

// IN-07: при остановке бот ждёт выполняющийся обработчик, а тот успевает
// отправить ответ, хотя ctx уже отменён.
func TestWaitHandlers(t *testing.T) {
	s := storetest.Open(t)
	logs := captureLogs(t)
	release := make(chan struct{})
	api := fakeTelegram(t, func(c call, _ int) string {
		if c.Method == "sendMessage" {
			<-release
		}
		return ""
	})
	b, err := New(Config{Token: "123:TEST"}, s, tgbot.WithServerURL(api.srv.URL), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	if !b.Wait(time.Second) {
		t.Fatal("обработчиков нет: Wait = false")
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.api.ProcessUpdate(ctx, startUpdate(42)) // обработчик — в горутине библиотеки
	for deadline := time.Now().Add(5 * time.Second); len(api.of("sendMessage")) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("обработчик не дошёл до sendMessage")
		}
	}
	cancel() // SIGTERM

	// Обработчик дольше предела — Wait возвращает false не позже предела.
	began := time.Now()
	if b.Wait(100 * time.Millisecond) {
		t.Fatal("обработчик ещё выполняется: Wait = true")
	}
	if d := time.Since(began); d > time.Second {
		t.Fatalf("Wait с пределом 100 мс вернулся через %v", d)
	}

	waited := make(chan bool, 1)
	go func() { waited <- b.Wait(5 * time.Second) }()
	select {
	case <-waited:
		t.Fatal("Wait вернулся до завершения обработчика")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if !<-waited {
		t.Fatal("обработчик завершился: Wait = false")
	}
	// Ответ ушёл без ошибки: ctx обработчика не отменён вместе с остановкой.
	if strings.Contains(logs.String(), "send start") || strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("обработчик не смог ответить после отмены ctx:\n%s", logs)
	}
}
