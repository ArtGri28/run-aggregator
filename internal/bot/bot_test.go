package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"aggregator/internal/event"
	"aggregator/internal/ingest"
	"aggregator/internal/ingest/sources/marathonec"
	"aggregator/internal/store"
	"aggregator/internal/store/storetest"
)

// fixtureNow — дата загрузки фикстуры Марафонца (как в тестах ingest), 12:00 UTC.
var fixtureNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

const calendarFixture = "../ingest/sources/marathonec/testdata/calendar-beg.html"

// call — один вызов фиктивного Bot API: метод и поля формы.
type call struct {
	Method string
	Form   map[string]string
}

// fakeAPI изображает Bot API на httptest и записывает вызовы.
type fakeAPI struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []call
}

// script подменяет ответ фиктивного API: получает вызов и его порядковый
// номер среди вызовов того же метода (с 1); пустая строка — ответ по
// умолчанию (успех).
type script func(c call, n int) string

// apiErr — тело ответа Bot API с ошибкой.
func apiErr(code int, desc string, retryAfter int) string {
	return `{"ok":false,"error_code":` + strconv.Itoa(code) + `,"description":` + strconv.Quote(desc) +
		`,"parameters":{"retry_after":` + strconv.Itoa(retryAfter) + `}}`
}

func fakeTelegram(t *testing.T, scripts ...script) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20) // библиотека шлёт multipart, не JSON
		c := call{Method: path.Base(r.URL.Path), Form: map[string]string{}}
		if r.MultipartForm != nil {
			for k, v := range r.MultipartForm.Value {
				c.Form[k] = v[0]
			}
			// Файлы (sendDocument): содержимое — Form[k], имя — Form[k+"_name"].
			for k, fhs := range r.MultipartForm.File {
				if f, err := fhs[0].Open(); err == nil {
					body, _ := io.ReadAll(f)
					f.Close()
					c.Form[k], c.Form[k+"_name"] = string(body), fhs[0].Filename
				}
			}
		}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		n := 0
		for _, x := range f.calls {
			if x.Method == c.Method {
				n++
			}
		}
		f.mu.Unlock()
		for _, sc := range scripts {
			if body := sc(c, n); body != "" {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		result := `{"message_id":5,"date":1,"chat":{"id":1,"type":"private"}}`
		if c.Method == "answerCallbackQuery" {
			result = `true`
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":` + result + `}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// of возвращает вызовы метода в порядке поступления.
func (f *fakeAPI) of(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeAPI) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeAPI) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

// buttons разбирает reply_markup вызова в плоский список кнопок.
func (c call) buttons(t *testing.T) []models.InlineKeyboardButton {
	t.Helper()
	raw := c.Form["reply_markup"]
	if raw == "" {
		return nil
	}
	var kb models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(raw), &kb); err != nil {
		t.Fatalf("reply_markup: %v", err)
	}
	var out []models.InlineKeyboardButton
	for _, r := range kb.InlineKeyboard {
		out = append(out, r...)
	}
	return out
}

// button ищет кнопку по подписи (префиксу) и возвращает её callback_data.
func (c call) button(t *testing.T, textPrefix string) string {
	t.Helper()
	for _, b := range c.buttons(t) {
		if strings.HasPrefix(b.Text, textPrefix) {
			return noV(b.CallbackData)
		}
	}
	return ""
}

// newTestBot — бот без группы поддержки: так собираются все тесты, кроме
// сценариев поддержки (newSupportBot), и этим доказывается «выключено = как раньше».
// Администратор 1 (как в тестах админских команд) включает заявки об ошибке: строка
// «📝 Сообщить об ошибке» в карточке есть, как в эталонах Phase 16.
func newTestBot(t *testing.T, api *fakeAPI, s *store.Store) *Bot {
	t.Helper()
	return newBotWith(t, api, s, Config{Token: "123:TEST", Admins: []int64{1}})
}

func newBotWith(t *testing.T, api *fakeAPI, s *store.Store, cfg Config) *Bot {
	t.Helper()
	opts := []tgbot.Option{tgbot.WithServerURL(api.srv.URL), tgbot.WithSkipGetMe(), tgbot.WithNotAsyncHandlers()}
	if forcedProfileStatus != "" { // непусто только внутри TestProfileNoLimits
		opts = append(opts, tgbot.WithMiddlewares(forceProfileStatusMW(forcedProfileStatus)))
	}
	b, err := New(cfg, s, opts...)
	if err != nil {
		t.Fatal(err)
	}
	b.Now = func() time.Time { return fixtureNow }
	b.name = "test_bot"
	return b
}

// startUpdate — «/start» в личном чате; без сущности bot_command
// MatchTypeCommandStartOnly не сработает.
func startUpdate(userID int64) *models.Update {
	return &models.Update{ID: 1, Message: &models.Message{
		ID: 1, From: &models.User{ID: userID}, Chat: models.Chat{ID: userID, Type: models.ChatTypePrivate},
		Text: "/start", Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 6}},
	}}
}

func cbUpdate(userID int64, msgID int, data string) *models.Update {
	return &models.Update{ID: 2, CallbackQuery: &models.CallbackQuery{
		ID: "cq" + strconv.Itoa(msgID), From: models.User{ID: userID}, Data: data,
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: msgID, Chat: models.Chat{ID: userID, Type: models.ChatTypePrivate}},
		},
	}}
}

// fixtureSource отдаёт календарь Марафонца из файла без сети.
type fixtureSource struct{ body []byte }

func (s fixtureSource) Code() string          { return marathonec.Code }
func (s fixtureSource) Policy() ingest.Policy { return marathonec.New(nil).Policy() }
func (s fixtureSource) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) {
	return marathonec.Parse(doc)
}
func (s fixtureSource) Fetch(context.Context) ([]ingest.RawDoc, error) {
	return []ingest.RawDoc{{URL: marathonec.CalendarURL, Body: s.body, FetchedAt: fixtureNow}}, nil
}

// seedMarathonec загружает реальные события Марафонца путём Phase 3:
// первый прогон (suspect) и его принятие. Возвращает число созданных событий.
func seedMarathonec(t *testing.T, s *store.Store) int {
	t.Helper()
	body, err := os.ReadFile(calendarFixture)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	src := fixtureSource{body: body}
	r := &ingest.Runner{Store: s, Now: func() time.Time { return fixtureNow }}
	if _, err := r.Run(ctx, src); err != nil {
		t.Fatalf("Run: %v", err)
	}
	res, err := r.Accept(ctx, src, 0)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	return res.Created
}

var listLineRE = regexp.MustCompile(`^\d{2}\.\d{2} · .+`)

func TestTracerSearch(t *testing.T) {
	s := storetest.Open(t)
	if n := seedMarathonec(t, s); n <= 100 {
		t.Fatalf("seedMarathonec created %d events, want > 100", n)
	}
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	ctx := context.Background()

	// Первый /start — предложение анкеты; приветствие и кнопка «Бег» —
	// на втором.
	b.api.ProcessUpdate(ctx, startUpdate(42))
	api.reset()
	b.api.ProcessUpdate(ctx, startUpdate(42))
	sends := api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["chat_id"] != "42" {
		t.Fatalf("/start: sendMessage = %+v, want one to chat 42", sends)
	}
	if got := sends[0].button(t, "🏃 Бег"); got != "f:a:running" {
		t.Fatalf("/start: button callback = %q, want f:a:running", got)
	}
	if _, v, err := s.Filters(ctx, 42); err != nil || v != 1 {
		t.Fatalf("Filters after /start: v=%d err=%v, want v=1", v, err)
	}

	// Кнопка поиска из прежнего сообщения (f:s): сводка.
	want, err := s.CountEvents(ctx, event.Filter{From: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)})
	if err != nil || want <= 100 {
		t.Fatalf("CountEvents = %d, %v; want > 100", want, err)
	}
	api.reset()
	b.api.ProcessUpdate(ctx, cbUpdate(42, 7, "f:s"))
	edits := api.of("editMessageText")
	if len(edits) != 1 || edits[0].Form["message_id"] != "7" {
		t.Fatalf("f:s: editMessageText = %+v, want one for message 7", edits)
	}
	for _, part := range []string{"Дата: Все будущие", "Город: Все города"} {
		if !strings.Contains(edits[0].Form["text"], part) {
			t.Fatalf("summary text %q lacks %q", edits[0].Form["text"], part)
		}
	}
	if got := edits[0].button(t, "🔎 Показать "+strconv.Itoa(want)+" "); got != "p:1:v1" {
		t.Fatalf("summary: «Показать %d» callback = %q, want p:1:v1", want, got)
	}
	if n := len(api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("f:s: answerCallbackQuery = %d, want 1", n)
	}
	checkCallbackLengths(t, edits[0])

	// [Показать N]: первая страница списка.
	api.reset()
	b.api.ProcessUpdate(ctx, cbUpdate(42, 7, "p:1:v1"))
	edits = api.of("editMessageText")
	if len(edits) != 1 || edits[0].Form["message_id"] != "7" {
		t.Fatalf("p:1:v1: editMessageText = %+v, want one for message 7", edits)
	}
	pages := (want + pageSize - 1) / pageSize
	if text := edits[0].Form["text"]; text != "🔎 <b>Найдено: "+strconv.Itoa(want)+"</b>\n\n⚙️ Фильтры не заданы" || edits[0].button(t, "1/"+strconv.Itoa(pages)) != "p:1:v1" {
		t.Fatalf("list: text %q buttons %+v", text, edits[0].buttons(t))
	}
	items, err := s.ListEvents(ctx, event.Filter{From: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)}, pageSize, 0)
	if err != nil || len(items) != pageSize {
		t.Fatalf("ListEvents = %d, %v; want %d", len(items), err, pageSize)
	}
	rows := listRows(t, edits[0])
	if len(rows) != pageSize {
		t.Fatalf("list rows = %d (%q), want %d", len(rows), rows, pageSize)
	}
	for i, l := range rows {
		if !listLineRE.MatchString(l) {
			t.Fatalf("row %d = %q, want ДД.ММ · Город · Название", i, l)
		}
		if !strings.HasPrefix(l, items[i].StartDate.Format("02.01")+" · ") || edits[0].button(t, l) != "e:"+strconv.FormatInt(items[i].ID, 10)+":1:v1" {
			t.Fatalf("row %d = %q, want event %d (%s)", i, l, items[i].ID, items[i].Title)
		}
	}
	if edits[0].button(t, "→") != "p:2:v1" {
		t.Fatalf("list: no →  p:2:v1 button: %+v", edits[0].buttons(t))
	}
	for _, bt := range edits[0].buttons(t) {
		if strings.HasPrefix(bt.CallbackData, "p:0:") {
			t.Fatalf("list: button %q leads to page 0", bt.CallbackData)
		}
	}
	if n := len(api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("p:1:v1: answerCallbackQuery = %d, want 1", n)
	}
	checkCallbackLengths(t, edits[0])

	// Повторный /start фильтры не сбрасывает (D-17).
	b.api.ProcessUpdate(ctx, startUpdate(42))
	if _, v, err := s.Filters(ctx, 42); err != nil || v != 1 {
		t.Fatalf("Filters after second /start: v=%d err=%v, want v=1", v, err)
	}
	t.Logf("TRACER start=ok summary=%d list=%d", want, len(rows))
}

func checkCallbackLengths(t *testing.T, c call) {
	t.Helper()
	for _, bt := range c.buttons(t) {
		if len(bt.CallbackData) > maxCallbackBytes {
			t.Fatalf("callback_data %q longer than %d bytes", bt.CallbackData, maxCallbackBytes)
		}
	}
}

func TestLoadFilters(t *testing.T) {
	today := day(2026, 9, 29)
	if got := loadFilters([]byte(`{}`), today); !reflect.DeepEqual(got, userFilters{}) {
		t.Fatalf(`loadFilters({}) = %+v, want zero`, got)
	}
	if got := loadFilters([]byte(`not json`), today); !reflect.DeepEqual(got, userFilters{}) {
		t.Fatalf("loadFilters(garbage) = %+v, want zero", got)
	}
	got := loadFilters([]byte(`{"date":"x","city":"Moscow","dist2":[1,9],"type":"ultra","extra":1}`), today)
	if want := (userFilters{Dist: []int{1}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFilters(invalid values) = %+v, want %+v", got, want)
	}
	valid := `{"date":"m202611","city":"nizhniy-novgorod","dist2":[3,2,2],"type":"trail"}`
	if got, want := loadFilters([]byte(valid), today), (userFilters{Dates: []string{"m202611"}, City: "nizhniy-novgorod", Dist: []int{2, 3}, Type: "trail"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFilters(valid) = %+v, want %+v", got, want)
	}
	// Прошедший месяц сбрасывается (LO-05); текущий, будущий и пресеты — нет.
	for date, want := range map[string]string{
		"m202608": "", "m202609": "m202609", "m202610": "m202610",
		"30": "30", "cm": "cm", "nm": "nm", "a": "a",
	} {
		if got := loadFilters([]byte(`{"date":"`+date+`"}`), today); date1(got) != want {
			t.Fatalf("loadFilters(date %s) = %q, want %q", date, date1(got), want)
		}
	}
	if got, want := loadFilters([]byte(`{"date":"m202512","city":"moscow"}`), today), (userFilters{City: "moscow"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("loadFilters(past month) = %+v, want %+v", got, want)
	}
}

// captureLogs подменяет slog.Default на запись в буфер до конца теста.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// Токен бота не попадает в лог ни из текста ошибки, ни из групп и атрибутов With,
// в том числе при настоящей сетевой ошибке запроса к Bot API.
func TestLogRedactsToken(t *testing.T) {
	const tok = "123456:SECRETtokenValue_abcdefghijklmnopq"
	logs := captureLogs(t)
	b, err := New(Config{Token: tok}, nil,
		tgbot.WithServerURL("http://127.0.0.1:1"), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}

	raw := errors.New(`Post "https://api.telegram.org/bot` + tok + `/getUpdates": dial tcp: refused`)
	b.logger().Error("telegram "+tok, "err", raw, slog.Group("g", "url", "bot"+tok))
	b.logger().With("tok", tok).WithGroup("w").Warn("with", "x", tok)
	_, sendErr := b.api.SendMessage(context.Background(), &tgbot.SendMessageParams{ChatID: 1, Text: "x"})
	if sendErr == nil {
		t.Fatal("sendMessage на закрытый порт: нет ошибки")
	}
	b.logger().Warn("send runs", "err", sendErr)

	out := logs.String()
	if strings.Contains(out, tok) || strings.Contains(out, "SECRETtoken") {
		t.Fatalf("токен в логе:\n%s", out)
	}
	if strings.Count(out, "***") < 5 || !strings.Contains(out, "getUpdates") {
		t.Fatalf("маскирование не видно или потерян текст:\n%s", out)
	}
}

// BOT-08: в логах нет текста сообщений, имён и username.
func TestNoPersonalDataInLogs(t *testing.T) {
	s := storetest.Open(t)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	logs := captureLogs(t)
	ctx := context.Background()

	from := &models.User{ID: 42, FirstName: "Секретимя", Username: "secret_user7"}
	text := startUpdate(42)
	text.Message.From, text.Message.Text, text.Message.Entities = from, "мой адрес 42", nil
	b.api.ProcessUpdate(ctx, text)
	start := startUpdate(42)
	start.Message.From = from
	b.api.ProcessUpdate(ctx, start)

	api.reset()
	stale := cbUpdate(42, 7, "zzz:secret")
	stale.CallbackQuery.From = *from
	b.api.ProcessUpdate(ctx, stale)
	answers := api.of("answerCallbackQuery")
	if len(answers) != 1 || !strings.Contains(answers[0].Form["text"], "устарела") {
		t.Fatalf("неизвестный callback: answerCallbackQuery = %+v, want one «устарела»", answers)
	}

	for _, leak := range []string{"Секретимя", "secret_user7", "мой адрес", "secret"} {
		if strings.Contains(logs.String(), leak) {
			t.Fatalf("в логах есть %q:\n%s", leak, logs)
		}
	}
}

// T-04-07: группы и каналы игнорируются без ответа.
func TestPrivateOnly(t *testing.T) {
	s := storetest.Open(t)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	ctx := context.Background()

	group := startUpdate(42)
	group.Message.Chat.Type = models.ChatTypeGroup
	b.api.ProcessUpdate(ctx, group)
	if n := api.total(); n != 0 {
		t.Fatalf("сообщение из группы: %d вызовов API, want 0", n)
	}

	// Callback из группы — ровно один пустой ответ, обработчик не вызывается (IN-04).
	inGroup := cbUpdate(42, 7, "f:s")
	inGroup.CallbackQuery.Message.Message.Chat.Type = models.ChatTypeGroup
	inaccessible := &models.Update{ID: 3, CallbackQuery: &models.CallbackQuery{
		ID: "cqx", From: models.User{ID: 42}, Data: "f:s",
		Message: models.MaybeInaccessibleMessage{
			Type: models.MaybeInaccessibleMessageTypeInaccessibleMessage,
			InaccessibleMessage: &models.InaccessibleMessage{
				Chat: models.Chat{ID: -100, Type: models.ChatTypeSupergroup}, MessageID: 9},
		},
	}}
	for _, u := range []*models.Update{inGroup, inaccessible} {
		api.reset()
		b.api.ProcessUpdate(ctx, u)
		ans := api.of("answerCallbackQuery")
		if api.total() != 1 || len(ans) != 1 || ans[0].Form["text"] != "" {
			t.Fatalf("callback из группы: вызовы %+v, want один пустой answerCallbackQuery", api.calls)
		}
	}

	api.reset()
	b.api.ProcessUpdate(ctx, startUpdate(42))
	b.api.ProcessUpdate(ctx, cbUpdate(42, 7, "f:s"))
	if len(api.of("sendMessage")) != 1 || len(api.of("editMessageText")) != 1 || len(api.of("answerCallbackQuery")) != 1 {
		t.Fatalf("апдейты из личного чата: вызовы %+v", api.calls)
	}
}

// Pitfall 1 и 6: паника обработчика не роняет процесс, callback получает один ответ.
func TestCallbackPanicAnswered(t *testing.T) {
	s := storetest.Open(t)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	captureLogs(t)

	h := b.recoverMW(b.cb(func(context.Context, *models.CallbackQuery, callback) string { panic("boom") }))
	h(context.Background(), b.api, cbUpdate(42, 7, "p:1:v1"))
	if n := len(api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("answerCallbackQuery = %d, want 1", n)
	}
}
