package bot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

// Сценарии контракта Phase 20 (20-UX-CONTRACT.md, раздел 7): подписка на
// новые старты, сообщение подписки, ссылка на старт и метки входа. Каталог —
// Марафонец, «сегодня» 2026-09-29.

func subRow(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT COALESCE((SELECT filters::text FROM subscriptions WHERE tg_id = 42), 'none')`)
}

// TestS62Subscribe — S-62: подписка в «Мои настройки», смена фильтров,
// отписка, устаревшая кнопка.
func TestS62Subscribe(t *testing.T) {
	s, api, b := seeded(t)
	setUserFilters(t, s, `{"dates":["cm"]}`)

	pressSnap(t, b, api, "s:o", "s62_settings")
	pressSnap(t, b, api, "s:b", "s62_subscribed")
	if got := subRow(t); got != `{}` {
		t.Errorf("шаг 2: копия фильтров %s, want {} (без дат)", got)
	}
	if n := api.of("answerCallbackQuery"); len(n) != 1 || n[0].Form["text"] != subOnNotice {
		t.Errorf("шаг 2: уведомление %+v", n)
	}

	setUserFilters(t, s, `{"country":"*"}`)
	pressSnap(t, b, api, "s:o", "s62_changed")

	calls := raw(b, api, 42, "s:u:o")
	if subRow(t) != "none" || len(calls) != 2 || calls[0].Method != "editMessageText" || calls[1].Form["text"] != subOffNotice {
		t.Fatalf("шаг 4: подписка %s, вызовы %q", subRow(t), methods(calls))
	}
	if !strings.Contains(kbLines(t, calls[0]), "🔔 Подписаться на новые старты по этим фильтрам") {
		t.Errorf("шаг 4: после отписки нет кнопки подписки: %s", kbLines(t, calls[0]))
	}

	if c := onlyToast(t, "s:b:v1", raw(b, api, 42, "s:b:v1")); c.Form["text"] != staleNotice || subRow(t) != "none" {
		t.Errorf("шаг 5: устаревшая кнопка: %q, подписка %s", c.Form["text"], subRow(t))
	}
}

// digestRig — сценарии сообщения подписки: подписка пользователя 42 на бег в
// России с 21 сентября, n стартов каталога «появились» 22 сентября.
func digestRig(t *testing.T, n int) *notifyRig {
	t.Helper()
	r := newNotifyRig(t)
	dbExec(t, `DELETE FROM favorites`)
	dbExec(t, `DELETE FROM subscriptions`)
	dbExec(t, `UPDATE events SET created_at = '2026-09-01T00:00:00Z'`)
	dbExec(t, `INSERT INTO subscriptions (tg_id, filters, created_at) VALUES (42, '{}', '2026-09-21T00:00:00Z')`)
	if n > 0 {
		dbExec(t, `UPDATE events SET created_at = '2026-09-22T00:00:00Z' WHERE id IN (
			SELECT id FROM events WHERE sport = 'running' AND start_date >= '2026-09-29' ORDER BY start_date, id LIMIT $1)`, n)
	}
	return r
}

func digests(t *testing.T) string {
	t.Helper()
	return dbScalar(t, `SELECT count(*)::text FROM outbox WHERE kind = 'digest'`)
}

// TestS63Digest — S-63: нет новых — нет сообщения; три новых; двенадцать
// новых; повтор в ту же неделю; 403.
func TestS63Digest(t *testing.T) {
	mon := func(r *notifyRig, h, m int) time.Time { return r.msk(time.September, 28, h, m) }

	r := digestRig(t, 0)
	r.at(mon(r, 9, 59))
	r.silent(t, "до 10:00")
	r.at(mon(r, 10, 0))
	r.silent(t, "нет новых")
	if got := rowState(t, lastRow(t, "digest")); got != "closed:stale" {
		t.Errorf("нет новых: строка %s, want closed:stale", got)
	}

	r = digestRig(t, 3)
	r.at(mon(r, 10, 0))
	msg := r.sent(t, "три новых")
	matchSnapshot(t, "s63_digest", renderCall(t, msg))
	plain(t, msg)
	checkCallbackLengths(t, msg)
	if got := dbScalar(t, `SELECT sent_at::text FROM subscriptions WHERE tg_id = 42`); !strings.HasPrefix(got, "2026-09-28 07:00:00") {
		t.Errorf("время сообщения в подписке %s", got)
	}
	for _, at := range []time.Time{mon(r, 10, 5), r.msk(time.October, 3, 12, 0)} {
		r.at(at)
		r.silent(t, "повтор в ту же неделю "+at.Format("02.01"))
	}
	if digests(t) != "1" {
		t.Errorf("строк подписки %s, want 1", digests(t))
	}

	r = digestRig(t, 12)
	r.at(mon(r, 10, 0))
	msg = r.sent(t, "двенадцать новых")
	matchSnapshot(t, "s63_digest_more", renderCall(t, msg))

	r = digestRig(t, 3)
	r.reply = func(c call, _ int) string { return errBlocked }
	r.at(mon(r, 10, 0))
	r.step()
	if subRow(t) != "none" || rowState(t, lastRow(t, "digest")) != "closed:blocked" {
		t.Errorf("403: подписка %s, строка %s", subRow(t), rowState(t, lastRow(t, "digest")))
	}
}

// digestMsg — нажатие кнопки в сообщении подписки (у сообщения — клавиатура
// msg); возвращает вызовы.
func digestMsg(t *testing.T, r *notifyRig, msg call, data string) []call {
	t.Helper()
	u := cbUpdate(42, 9, data)
	var kb models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(msg.Form["reply_markup"]), &kb); err != nil {
		t.Fatal(err)
	}
	u.CallbackQuery.Message.Message.ReplyMarkup = &kb
	r.api.reset()
	r.b.api.ProcessUpdate(context.Background(), u)
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	return append([]call(nil), r.api.calls...)
}

// TestS64DigestActions — S-64: старт из сообщения — карточка новым
// сообщением; «Показать все в поиске» — список новым сообщением; «Отписаться».
func TestS64DigestActions(t *testing.T) {
	r := digestRig(t, 12)
	r.at(r.msk(time.September, 28, 10, 0))
	msg := r.sent(t, "сообщение")
	r.at(fixtureNow)

	first := datas(t, msg)[0]
	calls := digestMsg(t, r, msg, first)
	if methods(calls) != "sendMessage answerCallbackQuery" {
		t.Fatalf("карточка: вызовы %q", methods(calls))
	}
	matchSnapshot(t, "s64_card", renderCall(t, calls[0]))

	setUserFilters(t, r.s, `{"country":"*","dates":["cm"]}`)
	calls = digestMsg(t, r, msg, "s:l")
	if methods(calls) != "sendMessage answerCallbackQuery" {
		t.Fatalf("список: вызовы %q", methods(calls))
	}
	matchSnapshot(t, "s64_list", renderCall(t, calls[0]))
	wantStored(t, "список", `{}`, 3)

	calls = digestMsg(t, r, msg, "s:u:d")
	if methods(calls) != "editMessageReplyMarkup answerCallbackQuery" || calls[1].Form["text"] != subOffNotice || subRow(t) != "none" {
		t.Fatalf("отписка: вызовы %q, подписка %s", methods(calls), subRow(t))
	}
	matchSnapshot(t, "s64_unsubscribed", renderCall(t, calls[0]))
	if calls = digestMsg(t, r, msg, "s:u:d"); calls[len(calls)-1].Form["text"] != subOffNotice {
		t.Errorf("повторная отписка: %q", calls[len(calls)-1].Form["text"])
	}
	if c := onlyToast(t, "s:l", digestMsg(t, r, msg, "s:l")); c.Form["text"] != staleNotice {
		t.Errorf("«Показать все» без подписки: %q", c.Form["text"])
	}
}

func startWith(t *testing.T, b *Bot, api *fakeAPI, userID int64, arg string) []call {
	t.Helper()
	u := startUpdate(userID)
	u.Message.Text = "/start " + arg
	api.reset()
	b.api.ProcessUpdate(context.Background(), u)
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]call(nil), api.calls...)
}

// TestS65ShareLink — S-65: «Поделиться» в карточке; /start e_<id> новым
// пользователем без предложения анкеты; слитое и скрытое событие.
func TestS65ShareLink(t *testing.T) {
	_, api, b := seeded(t)
	setUserFilters(t, b.Store, `{}`)
	pressSnap(t, b, api, at(t, "e:110:1"), "s65_card_share")

	calls := startWith(t, b, api, 77, "e_110")
	if len(calls) != 1 || calls[0].Method != "sendMessage" {
		t.Fatalf("ссылка: вызовы %q", methods(calls))
	}
	matchSnapshot(t, "s65_link_new", renderCall(t, calls[0]))
	if dbScalar(t, `SELECT count(*)::text FROM users WHERE tg_id = 77`) != "1" ||
		dbScalar(t, `SELECT count(*)::text FROM user_profiles WHERE tg_id = 77`) != "0" {
		t.Errorf("ссылка: пользователь создан без строки профиля — нет")
	}
	if c := startWith(t, b, api, 77, ""); len(c) != 1 || !strings.Contains(c[0].Form["text"], "Анкета") {
		t.Errorf("следующий /start: анкета не предложена: %q", c[0].Form["text"])
	}

	dbExec(t, `UPDATE events SET merged_into_id = 52 WHERE id = 110`)
	if c := startWith(t, b, api, 77, "e_110"); !strings.Contains(c[0].Form["text"], "Шарташ") {
		t.Errorf("слитое: %q", c[0].Form["text"])
	}
	dbExec(t, `UPDATE events SET is_listed = false WHERE id = 52`)
	calls = startWith(t, b, api, 77, "e_110")
	matchSnapshot(t, "s65_link_gone", renderCall(t, calls[0]))
	if n := dbScalar(t, `SELECT n::text FROM usage_counters WHERE metric = 'src_share'`); n != "3" {
		t.Errorf("счётчик src_share = %s, want 3", n)
	}
}

// TestS66Sources — S-66: метка входа — главное меню и счётчик; плохая метка —
// обычный /start; /activity с блоком меток.
func TestS66Sources(t *testing.T) {
	e := newAdminEnv(t)
	dbExec(t, `INSERT INTO users (tg_id) VALUES (42), (1)`)
	dbExec(t, `INSERT INTO user_profiles (tg_id, status) VALUES (42, 'skipped'), (1, 'skipped')`)
	for _, arg := range []string{"src_vk", "src_vk", "src_tg_1", "src_Bad", "src_" + strings.Repeat("a", 21), "e_0", "x"} {
		calls := startWith(t, e.b, e.api, 42, arg)
		if len(calls) != 1 || !strings.Contains(calls[0].Form["text"], "Где старт?") {
			t.Fatalf("%s: вызовы %+v, want главное меню", arg, calls)
		}
	}
	if got := dbScalar(t, `SELECT string_agg(metric || '=' || n, ',' ORDER BY metric) FROM usage_counters WHERE metric LIKE 'src_%'`); got != "src_tg_1=1,src_vk=2" {
		t.Errorf("счётчики меток %q", got)
	}
	e.api.reset()
	e.b.api.ProcessUpdate(context.Background(), commandUpdate(1, "/activity"))
	matchSnapshot(t, "s66_activity", renderCall(t, e.api.of("sendMessage")[0]))
}
