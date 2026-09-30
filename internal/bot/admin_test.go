package bot

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/alert"
	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// adminEnv — бот с администратором 1, Runner и источником-фикстурой.
type adminEnv struct {
	s   *store.Store
	api *fakeAPI
	b   *Bot
	r   *ingest.Runner
	src fixtureSource
}

func newAdminEnv(t *testing.T) adminEnv {
	t.Helper()
	s := storetest.Open(t)
	body, err := os.ReadFile(calendarFixture)
	if err != nil {
		t.Fatal(err)
	}
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	b.admins[1] = true
	r := &ingest.Runner{Store: s, Now: func() time.Time { return fixtureNow }}
	src := fixtureSource{body: body}
	b.Runner, b.Sources = r, []ingest.Source{src}
	return adminEnv{s: s, api: api, b: b, r: r, src: src}
}

// firstRun делает первый прогон источника: suspect со снимком.
func (e adminEnv) firstRun(t *testing.T) int64 {
	t.Helper()
	res, err := e.r.Run(context.Background(), e.src)
	if err != nil || res.Status != ingest.StatusSuspect {
		t.Fatalf("first Run: %+v, %v; want suspect", res, err)
	}
	return res.RunID
}

func (e adminEnv) catalog(t *testing.T) int {
	t.Helper()
	n, err := e.s.CountEvents(context.Background(), event.Filter{From: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e adminEnv) press(userID int64, data string) {
	e.b.api.ProcessUpdate(context.Background(), cbUpdate(userID, 7, data))
}

func commandUpdate(userID int64, cmd string) *models.Update {
	u := startUpdate(userID)
	u.Message.Text = cmd
	u.Message.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: len(cmd)}}
	return u
}

func run(id int64) string { return strconv.FormatInt(id, 10) }

func TestRunsNonAdmin(t *testing.T) {
	e := newAdminEnv(t)
	e.firstRun(t)
	ctx := context.Background()

	e.b.api.ProcessUpdate(ctx, commandUpdate(2, "/foo"))
	foo := e.api.of("sendMessage")
	e.api.reset()
	e.b.api.ProcessUpdate(ctx, commandUpdate(2, "/runs"))
	runs := e.api.of("sendMessage")
	if len(foo) != 1 || len(runs) != 1 || !reflect.DeepEqual(foo[0].Form, runs[0].Form) {
		t.Fatalf("/runs для не-админа: %+v, /foo: %+v; ответы должны совпасть", runs, foo)
	}
	for _, c := range e.api.calls {
		for _, v := range c.Form {
			for _, leak := range []string{"marathonec", "suspect", "прогон"} {
				if strings.Contains(strings.ToLower(v), leak) {
					t.Fatalf("ответ не-админу содержит %q: %q", leak, v)
				}
			}
		}
	}
}

func TestRunsAdmin(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()

	e.b.api.ProcessUpdate(ctx, commandUpdate(1, "/runs"))
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || !strings.Contains(sends[0].Form["text"], "Прогонов пока нет") {
		t.Fatalf("без прогонов: %+v", sends)
	}

	id := e.firstRun(t)
	all, err := e.s.RecentRuns(ctx, 3)
	if err != nil || len(all) != 1 || all[0].Error == "" {
		t.Fatalf("RecentRuns = %+v, %v", all, err)
	}
	e.api.reset()
	e.b.api.ProcessUpdate(ctx, commandUpdate(1, "/runs"))
	sends = e.api.of("sendMessage")
	if len(sends) != 1 {
		t.Fatalf("sendMessage = %+v, want one", sends)
	}
	f := sends[0].Form
	if _, ok := f["parse_mode"]; ok {
		t.Fatalf("/runs отправлен с parse_mode %q", f["parse_mode"])
	}
	for _, part := range []string{"marathonec", "#" + run(id) + " suspect", "parsed", cut(all[0].Error, maxRunReason), "МСК"} {
		if !strings.Contains(f["text"], part) {
			t.Fatalf("/runs %q не содержит %q", f["text"], part)
		}
	}
	if got := sends[0].button(t, "Принять прогон "+run(id)); got != "r:a:"+run(id) {
		t.Fatalf("кнопка принятия = %q, want r:a:%d", got, id)
	}
}

func TestAcceptFlow(t *testing.T) {
	e := newAdminEnv(t)
	id := e.firstRun(t)

	e.press(1, "r:a:"+run(id))
	edits := e.api.of("editMessageText")
	if len(edits) != 1 || !strings.Contains(edits[0].Form["text"], "Принять прогон "+run(id)+" (marathonec) как базу?") {
		t.Fatalf("r:a: %+v", edits)
	}
	if edits[0].button(t, "Да") != "r:y:"+run(id) || edits[0].button(t, "Отмена") != "r:n:"+run(id) {
		t.Fatalf("r:a: кнопки %+v", edits[0].buttons(t))
	}
	if _, ok := edits[0].Form["parse_mode"]; ok {
		t.Fatal("r:a отправлен с parse_mode")
	}
	if n := len(e.api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("r:a: answerCallbackQuery = %d, want 1", n)
	}
	if n := e.catalog(t); n != 0 {
		t.Fatalf("до подтверждения в каталоге %d событий", n)
	}

	e.api.reset()
	e.press(1, "r:y:"+run(id))
	edits = e.api.of("editMessageText")
	if len(edits) != 1 || !strings.HasPrefix(edits[0].Form["text"], "Принято: прогон "+run(id)+" (marathonec)") || len(edits[0].buttons(t)) != 0 {
		t.Fatalf("r:y: %+v", edits)
	}
	if n := len(e.api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("r:y: answerCallbackQuery = %d, want 1", n)
	}
	if n := e.catalog(t); n <= 100 {
		t.Fatalf("после принятия в каталоге %d событий, want > 100", n)
	}

	e.api.reset()
	e.press(1, "r:y:"+run(id))
	edits = e.api.of("editMessageText")
	if len(edits) != 1 || !strings.Contains(edits[0].Form["text"], "Уже нельзя принять") {
		t.Fatalf("повторный r:y: %+v", edits)
	}
	if n := len(e.api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("повторный r:y: answerCallbackQuery = %d, want 1", n)
	}
}

// Принятие не последнего прогона и прогон, идущий сейчас, дают понятные отказы.
func TestAcceptRefusals(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	old := e.firstRun(t)
	if _, err := e.r.Run(ctx, e.src); err != nil { // второй прогон: тот же suspect, старый уже не последний
		t.Fatal(err)
	}

	e.press(1, "r:y:"+run(old))
	edits := e.api.of("editMessageText")
	if len(edits) != 1 || !strings.Contains(edits[0].Form["text"], "Уже нельзя принять") || e.catalog(t) != 0 {
		t.Fatalf("не последний прогон: %+v", edits)
	}

	id, err := e.s.EnsureSource(ctx, "marathonec")
	if err != nil {
		t.Fatal(err)
	}
	running, err := e.s.StartRun(ctx, id, fixtureNow.Add(time.Minute), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e.api.reset()
	e.press(1, "r:y:"+run(running))
	edits = e.api.of("editMessageText")
	if len(edits) != 1 || !strings.Contains(edits[0].Form["text"], "повторите позже") || e.catalog(t) != 0 {
		t.Fatalf("идущий прогон: %+v", edits)
	}

	e.api.reset()
	e.press(1, "r:y:999999")
	if n := len(e.api.of("editMessageText")); n != 0 {
		t.Fatalf("несуществующий прогон правит сообщение: %d", n)
	}
	if a := e.api.of("answerCallbackQuery"); len(a) != 1 || a[0].Form["text"] != "Прогон не найден" {
		t.Fatalf("несуществующий прогон: %+v", a)
	}
}

func TestAcceptCancel(t *testing.T) {
	e := newAdminEnv(t)
	id := e.firstRun(t)

	e.press(1, "r:n:"+run(id))
	edits := e.api.of("editMessageText")
	if len(edits) != 1 || !strings.Contains(edits[0].Form["text"], "Отменено") || len(edits[0].buttons(t)) != 0 {
		t.Fatalf("r:n: %+v", edits)
	}
	if n := e.catalog(t); n != 0 {
		t.Fatalf("после отмены в каталоге %d событий", n)
	}
	e.api.reset()
	e.b.api.ProcessUpdate(context.Background(), commandUpdate(1, "/runs"))
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].button(t, "Принять прогон") != "r:a:"+run(id) {
		t.Fatalf("после отмены /runs: %+v", sends)
	}
}

func TestAcceptNonAdmin(t *testing.T) {
	e := newAdminEnv(t)
	id := e.firstRun(t)
	logs := captureLogs(t)

	e.press(2, "r:y:"+run(id))
	e.press(2, "r:a:"+run(id))
	e.press(2, "r:n:"+run(id))
	if n := e.catalog(t); n != 0 {
		t.Fatalf("не-админ изменил каталог: %d событий", n)
	}
	if n := len(e.api.of("editMessageText")); n != 0 {
		t.Fatalf("editMessageText вызван %d раз", n)
	}
	if n := len(e.api.of("answerCallbackQuery")); n != 3 {
		t.Fatalf("answerCallbackQuery = %d, want 3", n)
	}
	last, err := e.s.RunSource(context.Background(), id)
	if err != nil || last != "marathonec" {
		t.Fatalf("RunSource = %q, %v", last, err)
	}
	if !strings.Contains(logs.String(), "admin callback from non-admin") || strings.Contains(logs.String(), "r:y") {
		t.Fatalf("лог: %s", logs)
	}
	if all, _ := e.s.RecentRuns(context.Background(), 1); len(all) != 1 || all[0].Status != "suspect" {
		t.Fatalf("прогон изменился: %+v", all)
	}
}

// Без Runner принятие недоступно и ничего не меняет.
func TestAcceptWithoutRunner(t *testing.T) {
	e := newAdminEnv(t)
	id := e.firstRun(t)
	e.b.Runner = nil
	e.press(1, "r:y:"+run(id))
	if a := e.api.of("answerCallbackQuery"); len(a) != 1 || a[0].Form["text"] != "Принятие недоступно" || e.catalog(t) != 0 {
		t.Fatalf("без Runner: %+v", a)
	}
}

func TestSendAlert(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()

	if err := e.b.SendAlert(ctx, 1, "текст <b>", 7); err != nil {
		t.Fatal(err)
	}
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["chat_id"] != "1" || sends[0].Form["text"] != "текст <b>" {
		t.Fatalf("SendAlert: %+v", sends)
	}
	if _, ok := sends[0].Form["parse_mode"]; ok {
		t.Fatalf("parse_mode = %q, want отсутствует", sends[0].Form["parse_mode"])
	}
	if got := sends[0].button(t, "Принять как базу"); got != "r:a:7" {
		t.Fatalf("кнопка = %q, want r:a:7", got)
	}

	e.api.reset()
	if err := e.b.SendAlert(ctx, 1, "без кнопки", 0); err != nil {
		t.Fatal(err)
	}
	if sends := e.api.of("sendMessage"); len(sends) != 1 || sends[0].Form["reply_markup"] != "" {
		t.Fatalf("acceptRunID 0: %+v", sends)
	}

	e.api.reset()
	if err := e.b.SendAlert(ctx, 2, "чужому", 7); err == nil || e.api.total() != 0 {
		t.Fatalf("не-админ: err=%v, вызовов API %d", err, e.api.total())
	}
}

// Связка D-13/D-15/D-18: алерт с кнопкой → принятие → следующий тик молчит.
func TestAlertToAccept(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	id := e.firstRun(t)
	loop := &alert.Loop{Store: e.s, Admins: []int64{1}, Send: e.b.SendAlert, Now: func() time.Time { return fixtureNow }}

	if err := loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["chat_id"] != "1" || sends[0].button(t, "Принять") != "r:a:"+run(id) {
		t.Fatalf("алерт: %+v", sends)
	}

	e.press(1, "r:a:"+run(id))
	e.press(1, "r:y:"+run(id))
	if n := e.catalog(t); n <= 100 {
		t.Fatalf("после принятия в каталоге %d событий", n)
	}

	e.api.reset()
	if err := loop.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.api.of("sendMessage")); n != 0 {
		t.Fatalf("после принятия тик отправил %d сообщений", n)
	}
	if st, err := e.s.AlertStates(ctx); err != nil || len(st) != 0 {
		t.Fatalf("состояние не снято: %+v, %v", st, err)
	}
}
