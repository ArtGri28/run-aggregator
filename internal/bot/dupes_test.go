package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"aggregator/internal/event"
	"aggregator/internal/store"
)

var dupeSeq int

// dupeEvent — запись источника code со своим событием title (без прогона
// ingest: прогон открывается и сразу закрывается); возвращает id события и
// записи.
func dupeEvent(t *testing.T, s *store.Store, code, title string) (eventID, rowID int64) {
	t.Helper()
	ctx := context.Background()
	src, err := s.EnsureSource(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := s.StartRun(ctx, src, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, runID, store.RunFinish{Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	dupeSeq++
	rowID, _, err = s.UpsertSourceEvent(ctx, src, runID, fmt.Sprint(dupeSeq), fmt.Sprintf("https://example.org/%s/%d", code, dupeSeq), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	e := event.Event{
		Title: title, TitleNorm: strings.ToLower(title),
		StartDate:   fixtureToday.AddDate(0, 0, 10),
		CountryCode: "RU", City: "Казань", CityKey: "kazan",
		Type: event.TypeRoad, Status: event.StatusScheduled,
		Distances: []event.Distance{{Meters: 10000}, {Meters: 21100}},
	}
	if _, _, err := s.SaveEventFor(ctx, rowID, &e, event.FoldConfig{}); err != nil {
		t.Fatal(err)
	}
	return e.ID, rowID
}

// seedCandidate — два события одного забега от Марафонца (a) и ПроБЕГа (b)
// и кандидат между ними.
func seedCandidate(t *testing.T, s *store.Store, title string) (cid, a, b int64) {
	t.Helper()
	a, _ = dupeEvent(t, s, "marathonec", title)
	b, row := dupeEvent(t, s, "probeg", title+" (ПроБЕГ)")
	cid, err := s.UpsertCandidate(context.Background(), row, a, 0.62,
		[]byte(`{"name_sim":0.89,"score":0.62,"dates_overlap":true,"same_city":true}`))
	if err != nil || cid == 0 {
		t.Fatalf("UpsertCandidate = %d, %v", cid, err)
	}
	return cid, a, b
}

func counts(t *testing.T, s *store.Store) (pending, fresh int) {
	t.Helper()
	p, f, err := s.CandidateCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p, f
}

// listedRoots проверяет, что события в выдаче и не слиты.
func listedRoots(t *testing.T, s *store.Store, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		e, err := s.GetEvent(context.Background(), id)
		if err != nil || !e.IsListed || e.MergedIntoID != nil {
			t.Fatalf("событие %d: listed %v, merged %v, %v; want listed root", id, e.IsListed, e.MergedIntoID, err)
		}
	}
}

func noParseMode(t *testing.T, c call) {
	t.Helper()
	if _, ok := c.Form["parse_mode"]; ok {
		t.Fatalf("%s с parse_mode %q", c.Method, c.Form["parse_mode"])
	}
}

// oneEdit — единственная правка сообщения и единственный ответ на callback.
func (e adminEnv) oneEdit(t *testing.T, data string) call {
	t.Helper()
	e.api.reset()
	e.press(1, data)
	edits := e.api.of("editMessageText")
	if len(edits) != 1 {
		t.Fatalf("%s: editMessageText = %+v, want one", data, edits)
	}
	if n := len(e.api.of("answerCallbackQuery")); n != 1 {
		t.Fatalf("%s: answerCallbackQuery = %d, want 1", data, n)
	}
	noParseMode(t, edits[0])
	checkCallbackLengths(t, edits[0])
	return edits[0]
}

func wantDupeButtons(t *testing.T, c call, cid int64) {
	t.Helper()
	id := run(cid)
	if c.button(t, "Слить") != "d:m:"+id || c.button(t, "Разные") != "d:x:"+id || c.button(t, "Пропустить") != "d:s:"+id {
		t.Fatalf("кнопки кандидата %d: %+v", cid, c.buttons(t))
	}
}

func TestTracerDupes(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	cid1, a1, b1 := seedCandidate(t, e.s, "Казанский марафон <b>&")
	cid2, _, _ := seedCandidate(t, e.s, "Кремлёвский забег")
	if cid1 >= cid2 {
		t.Fatalf("cid1 = %d, cid2 = %d", cid1, cid2)
	}

	// Не-админ: ответ неизвестной команды, кандидаты не тронуты.
	e.b.api.ProcessUpdate(ctx, commandUpdate(2, "/dupes"))
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["text"] != "Нажмите /start, чтобы найти старт" || strings.Contains(sends[0].Form["reply_markup"], "d:") {
		t.Fatalf("/dupes не-админу: %+v", sends)
	}
	if p, f := counts(t, e.s); p != 2 || f != 2 {
		t.Fatalf("после не-админа: pending %d, fresh %d", p, f)
	}

	e.api.reset()
	e.b.api.ProcessUpdate(ctx, commandUpdate(1, "/dupes"))
	sends = e.api.of("sendMessage")
	if len(sends) != 1 {
		t.Fatalf("/dupes: %+v", sends)
	}
	noParseMode(t, sends[0])
	text := sends[0].Form["text"]
	for _, part := range []string{"Кандидат #" + run(cid1), "оценка 0,62", "Казанский марафон <b>&", "Казанский марафон <b>& (ПроБЕГ)",
		"Марафонец", "ПроБЕГ", "10/21,1 км", "название 0,89", "даты пересекаются", "город совпал", "https://example.org/probeg/"} {
		if !strings.Contains(text, part) {
			t.Fatalf("экран кандидата не содержит %q:\n%s", part, text)
		}
	}
	wantDupeButtons(t, sends[0], cid1)
	checkCallbackLengths(t, sends[0])

	edit := e.oneEdit(t, "d:x:"+run(cid1))
	if !strings.Contains(edit.Form["text"], "Кремлёвский забег") || !strings.Contains(edit.Form["text"], "Кандидат #"+run(cid2)) {
		t.Fatalf("после [Разные]: %q", edit.Form["text"])
	}
	wantDupeButtons(t, edit, cid2)
	if _, err := e.s.CandidateByID(ctx, cid1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CandidateByID(отклонённый) = %v, want ErrNotFound", err)
	}
	if p, _ := counts(t, e.s); p != 1 {
		t.Fatalf("pending = %d, want 1", p)
	}
	listedRoots(t, e.s, a1, b1)
	t.Log("TRACER dupes nonadmin=unknown_command first=shown reject=rejected next=shown pending=1")
}

func TestDupesMerge(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	logs := captureLogs(t)
	cid1, a, b := seedCandidate(t, e.s, "Осенний полумарафон")
	cid2, _, _ := seedCandidate(t, e.s, "Зимний забег")
	lo, hi := min(a, b), max(a, b)

	ask := e.oneEdit(t, "d:m:"+run(cid1))
	for _, part := range []string{"Слить?", "Останется #" + run(lo), "#" + run(hi) + " скроется"} {
		if !strings.Contains(ask.Form["text"], part) {
			t.Fatalf("подтверждение не содержит %q:\n%s", part, ask.Form["text"])
		}
	}
	if ask.button(t, "Да, слить") != "d:y:"+run(cid1) || ask.button(t, "Отмена") != "d:n:"+run(cid1) || len(ask.buttons(t)) != 2 {
		t.Fatalf("кнопки подтверждения: %+v", ask.buttons(t))
	}
	listedRoots(t, e.s, a, b)
	if p, _ := counts(t, e.s); p != 2 {
		t.Fatalf("после [Слить] pending = %d", p)
	}

	back := e.oneEdit(t, "d:n:"+run(cid1))
	if !strings.Contains(back.Form["text"], "Кандидат #"+run(cid1)) || strings.Contains(back.Form["text"], "Слить?") {
		t.Fatalf("после [Отмена]: %q", back.Form["text"])
	}
	wantDupeButtons(t, back, cid1)
	listedRoots(t, e.s, a, b)

	merged := e.oneEdit(t, "d:y:"+run(cid1))
	if !strings.Contains(merged.Form["text"], fmt.Sprintf("Слито: #%d → #%d.", hi, lo)) || !strings.Contains(merged.Form["text"], "Кандидат #"+run(cid2)) {
		t.Fatalf("после [Да, слить]: %q", merged.Form["text"])
	}
	wantDupeButtons(t, merged, cid2)
	loser, err := e.s.GetEvent(ctx, hi)
	if err != nil || loser.MergedIntoID == nil || *loser.MergedIntoID != lo {
		t.Fatalf("проигравший %d: merged %v, %v; want %d", hi, loser.MergedIntoID, err, lo)
	}
	srcs, err := e.s.EventSources(ctx, lo)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, s := range srcs {
		codes = append(codes, s.Code)
	}
	if got := strings.Join(codes, ","); !strings.Contains(got, "marathonec") || !strings.Contains(got, "probeg") || len(codes) != 2 {
		t.Fatalf("источники победителя: %s", got)
	}

	again := e.oneEdit(t, "d:y:"+run(cid1))
	if !strings.Contains(again.Form["text"], "Кандидат #"+run(cid1)+" уже обработан") {
		t.Fatalf("повторное [Да, слить]: %q", again.Form["text"])
	}
	if l2, _ := e.s.GetEvent(ctx, hi); l2.MergedIntoID == nil || *l2.MergedIntoID != lo || !l2.UpdatedAt.Equal(loser.UpdatedAt) {
		t.Fatalf("повторное слияние изменило событие: %+v", l2)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("ошибка в логе:\n%s", logs)
	}
	if !strings.Contains(logs.String(), "dupe decided") || strings.Contains(logs.String(), "Осенний") {
		t.Fatalf("лог решений:\n%s", logs)
	}
}

func TestDupesSkipAndOpen(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	cid1, _, _ := seedCandidate(t, e.s, "Пробег один")
	cid2, _, _ := seedCandidate(t, e.s, "Пробег два")

	if c := e.oneEdit(t, "d:s:"+run(cid1)); !strings.Contains(c.Form["text"], "Кандидат #"+run(cid2)) {
		t.Fatalf("[Пропустить] cid1: %q", c.Form["text"])
	}
	end := e.oneEdit(t, "d:s:"+run(cid2))
	if !strings.Contains(end.Form["text"], "Кандидатов дальше нет. Ожидают: 2") || end.button(t, "С начала") != "d:o" {
		t.Fatalf("конец очереди: %q %+v", end.Form["text"], end.buttons(t))
	}
	if c := e.oneEdit(t, "d:o"); !strings.Contains(c.Form["text"], "Кандидат #"+run(cid1)) {
		t.Fatalf("[С начала]: %q", c.Form["text"])
	}
	if p, _ := counts(t, e.s); p != 2 {
		t.Fatalf("пропуск изменил очередь: pending %d", p)
	}

	e.press(1, "d:x:"+run(cid1))
	e.press(1, "d:x:"+run(cid2))
	e.api.reset()
	e.b.api.ProcessUpdate(ctx, commandUpdate(1, "/dupes"))
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["text"] != "Кандидатов нет" {
		t.Fatalf("пустая очередь: %+v", sends)
	}
	if rm, ok := sends[0].Form["reply_markup"]; ok {
		t.Fatalf("пустая очередь: reply_markup %q", rm)
	}
}

func TestDupesNonAdmin(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	logs := captureLogs(t)
	cid, a, b := seedCandidate(t, e.s, "Чужой забег")
	for _, k := range []string{"d:o", "d:m:", "d:y:", "d:x:", "d:s:", "d:n:"} {
		data := k
		if k != "d:o" {
			data += run(cid)
		}
		e.press(2, data)
	}
	if n := len(e.api.of("answerCallbackQuery")); n != 6 {
		t.Fatalf("answerCallbackQuery = %d, want 6", n)
	}
	for _, a := range e.api.of("answerCallbackQuery") {
		if a.Form["text"] != "" {
			t.Fatalf("не-админу уведомление %q", a.Form["text"])
		}
	}
	if n := len(e.api.of("sendMessage")) + len(e.api.of("editMessageText")); n != 0 {
		t.Fatalf("не-админу отправлено %d экранов", n)
	}
	if _, err := e.s.CandidateByID(ctx, cid); err != nil {
		t.Fatalf("кандидат не pending: %v", err)
	}
	listedRoots(t, e.s, a, b)
	if strings.Count(logs.String(), "admin callback from non-admin") != 6 {
		t.Fatalf("лог:\n%s", logs)
	}
}

// Без Runner слияние недоступно, отклонение работает.
func TestDupesWithoutRunner(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	cid, a, b := seedCandidate(t, e.s, "Без раннера")
	e.b.Runner = nil

	e.press(1, "d:y:"+run(cid))
	if ans := e.api.of("answerCallbackQuery"); len(ans) != 1 || ans[0].Form["text"] != "Слияние недоступно" {
		t.Fatalf("d:y без Runner: %+v", ans)
	}
	if n := len(e.api.of("editMessageText")); n != 0 {
		t.Fatalf("d:y без Runner: правок %d", n)
	}
	if _, err := e.s.CandidateByID(ctx, cid); err != nil {
		t.Fatalf("кандидат не pending: %v", err)
	}
	listedRoots(t, e.s, a, b)

	if c := e.oneEdit(t, "d:x:"+run(cid)); c.Form["text"] != fmt.Sprintf("Кандидат #%d: разные.\n\nКандидатов нет", cid) {
		t.Fatalf("d:x без Runner: %q", c.Form["text"])
	}
	if _, err := e.s.CandidateByID(ctx, cid); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("после d:x: %v", err)
	}
}

func TestSendDupesDigest(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()

	if err := e.b.SendDupesDigest(ctx, 2, "чужому"); !errors.Is(err, errNotAdmin) || e.api.total() != 0 {
		t.Fatalf("не-админ: err=%v, вызовов API %d", err, e.api.total())
	}
	if err := e.b.SendDupesDigest(ctx, 1, "текст <b>"); err != nil {
		t.Fatal(err)
	}
	sends := e.api.of("sendMessage")
	if len(sends) != 1 || sends[0].Form["chat_id"] != "1" || sends[0].Form["text"] != "текст <b>" {
		t.Fatalf("SendDupesDigest: %+v", sends)
	}
	noParseMode(t, sends[0])
	if got := sends[0].button(t, "Открыть /dupes"); got != "d:o" {
		t.Fatalf("кнопка = %q, want d:o", got)
	}
}

// TestConfirmShownRoots — L1 (ревью Phase 5), воспроизведение: «Да, слить»
// после изменения сторон между экраном подтверждения и нажатием.
//   - корень стороны сменился (сторона слита в другое событие) — MergeEvents
//     удаляет pending-кандидатов проигравшего, кнопка отвечает «уже
//     обработан», ничего не сливается;
//   - сторона осталась корнем, но поглотила третье событие — кандидат жив,
//     подтверждение сливает группу целиком (печать для решения владельца).
func TestConfirmShownRoots(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	admin := int64(1)

	c, _ := dupeEvent(t, e.s, "third", "Казанский марафон") // меньший id: станет корнем b
	cid, a, b := seedCandidate(t, e.s, "Казанский марафон")
	yes := e.oneEdit(t, "d:m:"+run(cid)).button(t, "Да, слить")
	if err := e.s.MergeEvents(ctx, b, c, "manual", &admin, event.FoldConfig{}); err != nil {
		t.Fatal(err)
	}
	edit := e.oneEdit(t, yes)
	if ra, err := e.s.RootEventID(ctx, a); err != nil || ra != a {
		t.Fatalf("корень сменился: после «Да, слить» корень a = %d, %v; want %d", ra, err, a)
	}
	rootChange := "merged"
	if strings.Contains(edit.Form["text"], done(cid)) {
		rootChange = "already_processed"
	}

	cid2, a2, b2 := seedCandidate(t, e.s, "Кремлёвский забег")
	d, _ := dupeEvent(t, e.s, "third", "Кремлёвский забег")
	yes = e.oneEdit(t, "d:m:"+run(cid2)).button(t, "Да, слить")
	if err := e.s.MergeEvents(ctx, d, a2, "manual", &admin, event.FoldConfig{}); err != nil { // a2 поглощает d
		t.Fatal(err)
	}
	e.oneEdit(t, yes)
	rb, _ := e.s.RootEventID(ctx, b2)
	rd, _ := e.s.RootEventID(ctx, d)
	t.Logf("L1 REPRO root_change=%s absorbed_side=%s", rootChange, map[bool]string{true: "merged_with_absorbed", false: "not_merged"}[rb == rd])
	if rootChange != "already_processed" {
		t.Fatalf("смена корня стороны: %s", edit.Form["text"])
	}
}
