package bot

import (
	"testing"
)

// «Мои настройки» и сброс — контракт экранов Phase 8, сценарии S-12, S-13.

// TestS12Settings — S-12: просмотр настроек и переходы.
func TestS12Settings(t *testing.T) {
	s, api, b := countryFixture(t)
	startSnap(t, b, api, "s12_menu")

	// По умолчанию: все пять строк, страна показана всегда.
	def := pressSnap(t, b, api, "s:o", "s12_default")
	setUserFilters(t, s, `{"country":"*","date":"30","dist":[1,2],"type":"trail"}`)
	pressSnap(t, b, api, "s:o", "s12_all_countries")
	setUserFilters(t, s, `{"city":"moscow","date":"m202610","type":"road"}`)
	set := pressSnap(t, b, api, "s:o", "s12_filtered")

	// Экран только показывает: фильтры и версия не меняются.
	f1, v1 := filtersV(t, b)
	pressSnap(t, b, api, set.button(t, "✏️ Изменить фильтры"), "s12_to_search")
	pressSnap(t, b, api, def.button(t, backLabel), "s12_back_to_menu")
	if f2, v2 := filtersV(t, b); v2 != v1 || f2.City != f1.City || f2.Date != f1.Date || f2.Type != f1.Type {
		t.Errorf("настройки изменили фильтры: %+v v%d → %+v v%d", f1, v1, f2, v2)
	}
}

// TestS13Reset — S-13: сброс только после подтверждения.
func TestS13Reset(t *testing.T) {
	s, api, b := countryFixture(t)
	setUserFilters(t, s, `{"city":"moscow","date":"30","dist":[1],"type":"road"}`)
	_, v := filtersV(t, b)

	// Из настроек: вопрос, отмена — без изменений.
	ask := pressSnap(t, b, api, "s:r:o", "s13_ask_settings")
	pressSnap(t, b, api, ask.button(t, backLabel), "s13_cancel_settings")
	if f, v2 := filtersV(t, b); v2 != v || f.City != "moscow" || f.Date != "30" || f.Type != "road" || len(f.Dist) != 1 {
		t.Fatalf("отмена сброса изменила фильтры: %+v v%d", f, v2)
	}

	// Подтверждение: значения по умолчанию, версия +1, уведомление.
	c := press(t, b, api, ask.button(t, "✅ Да, сбросить"))
	matchSnapshot(t, "s13_done_settings", renderCall(t, c)+renderCall(t, api.of("answerCallbackQuery")[0]))
	if f, v2 := filtersV(t, b); v2 != v+1 || f.City != "" || f.Date != "" || f.Type != "" || len(f.Dist) != 0 || f.Country != "" {
		t.Fatalf("после сброса: %+v v%d, want по умолчанию v%d", f, v2, v+1)
	}

	// Уже по умолчанию: то же уведомление, версия не растёт.
	c = press(t, b, api, "s:y:o")
	if toast(api) != resetNotice {
		t.Errorf("повторный сброс: уведомление %q", toast(api))
	}
	if _, v3 := filtersV(t, b); v3 != v+1 {
		t.Errorf("повторный сброс изменил версию: %d", v3)
	}

	// Из поиска: «Сбросить» → вопрос → отмена возвращает в поиск без изменений.
	setUserFilters(t, s, `{"type":"trail"}`)
	_, v = filtersV(t, b)
	sum := press(t, b, api, "f:s")
	ask = pressSnap(t, b, api, sum.button(t, "🔄 Сбросить"), "s13_ask_search")
	pressSnap(t, b, api, ask.button(t, backLabel), "s13_cancel_search")
	if f, v2 := filtersV(t, b); v2 != v || f.Type != "trail" {
		t.Fatalf("отмена сброса из поиска изменила фильтры: %+v v%d", f, v2)
	}
	c = press(t, b, api, ask.button(t, "✅ Да, сбросить"))
	matchSnapshot(t, "s13_done_search", renderCall(t, c)+renderCall(t, api.of("answerCallbackQuery")[0]))
	if f, v2 := filtersV(t, b); v2 != v+1 || f.Type != "" {
		t.Fatalf("после сброса из поиска: %+v v%d", f, v2)
	}
	if !t.Failed() {
		t.Log("TRACER settings=reset users=default")
	}
}
