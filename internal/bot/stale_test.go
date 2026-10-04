package bot

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store/storetest"
)

// Устаревшие и неизвестные кнопки, внутренняя ошибка — контракт экранов
// Phase 8 (08-UX-CONTRACT.md, раздел 5, сценарии S-14…S-17).

func dbScalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var out string
	if err := conn.QueryRow(ctx, q, args...).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// dbState — таблицы users и events целиком (все колонки, включая
// last_seen_at): любое изменение меняет строку.
func dbState(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT jsonb_build_object(
		'users', (SELECT jsonb_agg(to_jsonb(u) ORDER BY u.tg_id) FROM users u),
		'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM events e))::text`)
}

// raw нажимает кнопку как есть (без подстановки версии) и возвращает все
// вызовы Bot API.
func raw(b *Bot, api *fakeAPI, userID int64, data string) []call {
	api.reset()
	b.api.ProcessUpdate(context.Background(), cbUpdate(userID, 7, data))
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]call(nil), api.calls...)
}

// onlyToast: единственный вызов — ответ на callback; сообщение не правится.
func onlyToast(t *testing.T, data string, calls []call) call {
	t.Helper()
	if len(calls) != 1 || calls[0].Method != "answerCallbackQuery" {
		t.Fatalf("%q: ждали только answerCallbackQuery, вызовы %+v", data, calls)
	}
	return calls[0]
}

// edited: одна правка сообщения и один ответ; возвращает их снимок.
func edited(t *testing.T, data string, calls []call) string {
	t.Helper()
	var edit, answer *call
	for i := range calls {
		switch calls[i].Method {
		case "editMessageText":
			edit = &calls[i]
		case "answerCallbackQuery":
			answer = &calls[i]
		}
	}
	if len(calls) != 2 || edit == nil || answer == nil {
		t.Fatalf("%q: ждали правку и ответ, вызовы %+v", data, calls)
	}
	checkCallbackLengths(t, *edit)
	return renderCall(t, *edit) + renderCall(t, *answer)
}

// TestS14StaleFilter — S-14 (P1): кнопка изменения фильтра из старого
// сообщения ничего не меняет и не пишет.
func TestS14StaleFilter(t *testing.T) {
	_, api, b := countryFixture(t)
	logs := captureLogs(t)
	_, v := filtersV(t, b)
	old := ":v" + strconv.Itoa(v)
	press(t, b, api, "f:d:30") // пользователь сменил фильтр: версия выросла
	if _, v2 := filtersV(t, b); v2 != v+1 {
		t.Fatalf("версия %d → %d, want +1", v, v2)
	}

	before := dbState(t)
	for _, data := range []string{
		"f:t:road" + old, "f:c:moscow" + old, "f:b:1" + old, "f:k:BY" + old, "f:d:a" + old, "f:x:d" + old, "f:x" + old, // чужая версия
		"f:t:road", "f:b:1", "f:d:a", "f:x", "f:x:d", // формат до Phase 8, без версии
		"f:t:road:v0", "f:t:road:v99",
		"s:y:o" + old, "s:y:f" + old, "s:y:o:v0", "s:y:f:v99", // устаревшее «Да, сбросить»
	} {
		a := onlyToast(t, data, raw(b, api, 42, data))
		matchSnapshot(t, "s14_stale", renderCall(t, a))
		if after := dbState(t); after != before {
			t.Fatalf("%q изменил данные:\n%s\n%s", data, before, after)
		}
	}
	if f, _ := filtersV(t, b); f.Date != "30" || f.Type != "" || len(f.Dist) != 0 {
		t.Errorf("фильтры изменились: %+v", f)
	}
	if !strings.Contains(logs.String(), `msg="callback stale" prefix=f`) {
		t.Errorf("нет строки лога callback stale:\n%s", logs)
	}

	// Актуальная кнопка того же вида применяется: проверка чувствительна.
	press(t, b, api, "f:t:road")
	if f, _ := filtersV(t, b); f.Type != "road" {
		t.Errorf("актуальная кнопка не применилась: %+v", f)
	}
	if !t.Failed() {
		t.Log("TRACER stale=f users=unchanged events=unchanged")
	}
}

// TestS15StaleNavigation — S-15 (P2): кнопки навигации из старых сообщений
// работают по текущему состоянию и ничего не пишут.
func TestS15StaleNavigation(t *testing.T) {
	_, api, b := countryFixture(t)
	press(t, b, api, "f:t:road") // версия 2; кнопки v1 и без версии — старые
	before := dbState(t)
	for _, c := range []struct{ data, snap string }{
		{"f:s", "s15_summary"},        // формат до Phase 8
		{"f:s:v1", "s15_summary"},     // чужая версия
		{"f:t", "s15_type_menu"},      // открыть подменю
		{"p:2:v1", "s15_list"},        // 1-я страница и уведомление
		{"e:1:1:v1", "s15_card"},      // карточка, «назад» с текущей версией
		{"e:99999:1:v1", "s15_gone"},  // события нет
		{"s:o:v1", "s15_settings"},    // мои настройки
		{"s:r:o:v1", "s15_reset_ask"}, // вопрос о сбросе — ещё не изменение
	} {
		matchSnapshot(t, c.snap, edited(t, c.data, raw(b, api, 42, c.data)))
		if after := dbState(t); after != before {
			t.Fatalf("%q записал в базу:\n%s\n%s", c.data, before, after)
		}
	}

	// Пользователя нет в users: экран по умолчанию, строка не создаётся;
	// изменение — «устарела».
	for _, c := range []struct{ data, snap string }{{"f:s", "s15_no_user"}, {"f:s:v1", "s15_no_user"}, {"p:1:v1", "s15_no_user_list"}, {"s:o:v1", "s15_no_user_settings"}} {
		matchSnapshot(t, c.snap, edited(t, c.data, raw(b, api, 77, c.data)))
	}
	for _, data := range []string{"f:d:30:v0", "f:d:30", "f:d:30:v1", "s:y:o:v0", "s:y:f:v1"} {
		matchSnapshot(t, "s14_stale", renderCall(t, onlyToast(t, data, raw(b, api, 77, data))))
	}
	if after := dbState(t); after != before {
		t.Fatalf("кнопки пользователя без строки users записали в базу:\n%s\n%s", before, after)
	}

	// Актуальная кнопка отмечает активность: сравнение чувствительно к last_seen_at.
	press(t, b, api, "f:s")
	if dbState(t) == before {
		t.Error("актуальная кнопка не обновила last_seen_at")
	}
}

// TestS16Unknown — S-16: неизвестные и неразборчивые кнопки.
func TestS16Unknown(t *testing.T) {
	_, api, b := countryFixture(t)
	filtersV(t, b)
	before := dbState(t)
	for _, data := range []string{
		"zzz", "", "f:", "f:z", "f:z:v1", "f:s:1", "f:d:31:v1", "p:0:v1", "p:1", "e:0:1:v1", "e:1:1",
		"q:1", "s:o", "s:x:v1", "s:y:z:v1", "f:x", "f:x:v1", "m:v1", "mm", strings.Repeat("f", 65), "f:c:" + strings.Repeat("a", 61) + ":v1", "\xff\xfe", "f:s:v1:v2",
	} {
		a := onlyToast(t, data, raw(b, api, 42, data))
		matchSnapshot(t, "s16_unknown", renderCall(t, a))
		if after := dbState(t); after != before {
			t.Fatalf("%q изменил данные", data)
		}
	}
}

// TestS17InternalError — S-17 (P3): внутренняя ошибка отвечает иначе, чем
// устаревшая кнопка, и пишет свою строку лога.
func TestS17InternalError(t *testing.T) {
	s, api, b := countryFixture(t)
	_, v := filtersV(t, b)
	logs := captureLogs(t)

	// Паника обработчика: ответ с текстом ошибки, процесс жив.
	api.reset()
	h := b.cb(func(context.Context, *models.CallbackQuery, callback) string { panic("boom secret-data") })
	h(context.Background(), b.api, cbUpdate(42, 7, "f:s:v"+strconv.Itoa(v)))
	a := onlyToast(t, "panic", append([]call(nil), api.calls...))
	matchSnapshot(t, "s17_error", renderCall(t, a))
	if !strings.Contains(logs.String(), `msg="callback internal error" prefix=f`) {
		t.Errorf("нет строки лога callback internal error:\n%s", logs)
	}
	if strings.Contains(logs.String(), "callback stale") {
		t.Errorf("паника записана как устаревшая кнопка:\n%s", logs)
	}

	// Устаревшая кнопка — другой текст и другая строка лога.
	logs.Reset()
	stale := onlyToast(t, "f:d:30", raw(b, api, 42, "f:d:30"))
	matchSnapshot(t, "s14_stale", renderCall(t, stale))
	if !strings.Contains(logs.String(), `msg="callback stale" prefix=f`) || strings.Contains(logs.String(), "internal error") {
		t.Errorf("лог устаревшей кнопки:\n%s", logs)
	}

	// Ошибка БД (пул закрыт): тот же текст внутренней ошибки для f:, p:, e:.
	s.Close()
	for _, data := range []string{"f:s:v" + strconv.Itoa(v), "f:d:30:v" + strconv.Itoa(v), "p:1:v" + strconv.Itoa(v), "e:1:1:v" + strconv.Itoa(v)} {
		logs.Reset()
		a := onlyToast(t, data, raw(b, api, 42, data))
		matchSnapshot(t, "s17_error", renderCall(t, a))
		if !strings.Contains(logs.String(), `msg="callback internal error" prefix=`+data[:1]) || strings.Contains(logs.String(), "callback stale") {
			t.Errorf("%q: лог:\n%s", data, logs)
		}
	}
}

var fButtonRE = regexp.MustCompile(`→ ([fs]:[^ |\n]*)`)

// TestSnapshotsVersioned: в каждом эталоне экрана все кнопки f: и s: несут версию
// (контракт, раздел 3) — экран, показанный без stamp, сюда не пройдёт.
func TestSnapshotsVersioned(t *testing.T) {
	files, err := os.ReadDir(screensDir)
	if err != nil || len(files) == 0 {
		t.Fatalf("эталоны: %v", err)
	}
	for _, f := range files {
		body, err := os.ReadFile(screensDir + "/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		noUser := strings.HasPrefix(f.Name(), "s15_no_user")
		for _, m := range fButtonRE.FindAllStringSubmatch(string(body), -1) {
			c, err := parseCallback(m[1])
			if err != nil || c.Legacy || (c.V == 0) != noUser || !versionRE.MatchString(m[1]) {
				t.Errorf("%s: кнопка %q — версия %d, legacy %v, %v", f.Name(), m[1], c.V, c.Legacy, err)
			}
		}
	}
}

// TestCityKeysFit: ключи городов фикстуры помещаются в кнопку города с
// версией до 99999 (контракт, раздел 3).
func TestCityKeysFit(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	keys := strings.Split(dbScalar(t, `SELECT string_agg(DISTINCT city_key, ',') FROM events WHERE city_key IS NOT NULL`), ",")
	if len(keys) < 50 {
		t.Fatalf("ключей городов %d, want ≥ 50", len(keys))
	}
	for _, k := range keys {
		if _, err := (callback{Kind: cbCity, Value: k, V: 99999}).encode(); err != nil {
			t.Errorf("ключ %q не помещается в кнопку: %v", k, err)
		}
	}
}
