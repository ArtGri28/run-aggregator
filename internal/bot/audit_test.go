package bot

import (
	"context"
	"strconv"
	"testing"

	"aggregator/internal/event"
	"aggregator/internal/store/storetest"
)

// Аудит интерфейса Phase 8 (08-AUDIT.md): снимки пользовательских экранов
// исходной реализации. Эталоны — testdata/screens, правятся только вручную.

// TestAuditStart — главное меню /start.
func TestAuditStart(t *testing.T) {
	_, api, b := countryFixture(t)
	b.api.ProcessUpdate(context.Background(), startUpdate(42))
	s := api.of("sendMessage")
	if len(s) != 1 {
		t.Fatalf("/start: sendMessage = %d, want 1", len(s))
	}
	matchSnapshot(t, "start", renderCall(t, s[0]))
	if !t.Failed() {
		t.Log("TRACER snapshot=start match=yes")
	}
}

// pressSnap нажимает кнопку и сверяет правку сообщения со снимком.
func pressSnap(t *testing.T, b *Bot, api *fakeAPI, data, name string) call {
	t.Helper()
	c := press(t, b, api, data)
	matchSnapshot(t, name, renderCall(t, c))
	return c
}

// textSnap отправляет текст и сверяет единственный ответ со снимком.
func textSnap(t *testing.T, b *Bot, api *fakeAPI, text, name string) {
	t.Helper()
	api.reset()
	b.api.ProcessUpdate(context.Background(), textUpdate(42, text))
	s := api.of("sendMessage")
	if len(s) != 1 {
		t.Fatalf("%q: sendMessage = %d, want 1", text, len(s))
	}
	checkCallbackLengths(t, s[0])
	matchSnapshot(t, name, renderCall(t, s[0]))
}

// TestAuditFilterScreens — сводка, подменю и текстовый ввод города.
func TestAuditFilterScreens(t *testing.T) {
	s, api, b := countryFixture(t)
	filtersV(t, b) // пользователь создан, как после /start
	pressSnap(t, b, api, "f:s", "summary_default")
	pressSnap(t, b, api, "f:d", "date_menu")
	pressSnap(t, b, api, "f:c", "city_menu")
	pressSnap(t, b, api, "f:t", "type_menu")
	pressSnap(t, b, api, "f:k", "country_menu")
	setUserFilters(t, s, `{"date":"30","city":"moscow","dist":[1],"type":"road"}`)
	pressSnap(t, b, api, "f:s", "summary_filtered")
	pressSnap(t, b, api, "f:b", "bucket_menu")

	setUserFilters(t, s, `{}`)
	textSnap(t, b, api, "Москва", "city_text_found")
	textSnap(t, b, api, "Моск", "city_text_not_found")
	textSnap(t, b, api, "/foo", "city_text_command")
}

// TestAuditListAndCard — список, листание, карточка, статус, недоступное
// событие, смена фильтров во время листания.
func TestAuditListAndCard(t *testing.T) {
	s := storetest.Open(t)
	seedMarathonec(t, s)
	api := fakeTelegram(t)
	b := newTestBot(t, api, s)
	_, v := filtersV(t, b)
	pv := ":v" + strconv.Itoa(v)

	list := pressSnap(t, b, api, "p:1"+pv, "list_page1")
	pressSnap(t, b, api, "p:"+strconv.Itoa(maxPage)+pv, "list_last_page")

	card := list.button(t, "1")
	if card == "" {
		t.Fatalf("нет кнопки карточки: %v", datas(t, list))
	}
	pressSnap(t, b, api, card, "card")
	id := cardID(t, card)
	storetest.SetStatus(t, id, event.StatusCancelled)
	pressSnap(t, b, api, card, "card_cancelled")
	storetest.HideEvent(t, id)
	pressSnap(t, b, api, card, "gone")

	setUserFilters(t, s, `{"type":"trail"}`)
	c := press(t, b, api, "p:2"+pv)
	matchSnapshot(t, "list_filters_changed", renderCall(t, c)+renderCall(t, api.of("answerCallbackQuery")[0]))
}

func cardID(t *testing.T, data string) int64 {
	t.Helper()
	c, err := parseCallback(data)
	if err != nil || c.Kind != cbCard {
		t.Fatalf("callback карточки %q: %v", data, err)
	}
	return c.ID
}

// TestAuditEmptyScreens — пустая выдача с подсказками и с выбором страны.
func TestAuditEmptyScreens(t *testing.T) {
	s, api, b := countryFixture(t)
	setUserFilters(t, s, `{"date":"m202701"}`)
	_, v := filtersV(t, b)
	pressSnap(t, b, api, "p:1:v"+strconv.Itoa(v), "empty_hints")

	s2 := storetest.Open(t)
	seedCountryEvent(t, s2, "BY", "2026-10-12", "Минск", "minsk", event.TypeRoad, "Минский забег")
	api2 := fakeTelegram(t)
	b2 := newTestBot(t, api2, s2)
	_, v = filtersV(t, b2)
	pressSnap(t, b2, api2, "p:1:v"+strconv.Itoa(v), "empty_pick_country")
}

// TestAuditStaleCallback — ответ на неизвестный и неразборчивый callback.
func TestAuditStaleCallback(t *testing.T) {
	_, api, b := countryFixture(t)
	for _, data := range []string{"zzz", "f:z"} {
		api.reset()
		b.api.ProcessUpdate(context.Background(), cbUpdate(42, 7, data))
		a := api.of("answerCallbackQuery")
		if len(a) != 1 || api.total() != 1 {
			t.Fatalf("%s: ждали только один answerCallbackQuery, вызовы %+v", data, api.calls)
		}
		matchSnapshot(t, "stale_callback", renderCall(t, a[0]))
	}
}
