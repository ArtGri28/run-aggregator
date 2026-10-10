package event

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestEventValidURL — та же таблица случаев, что у TestValidURL пакета бота:
// карточка и обнаружение изменений используют одну проверку.
func TestEventValidURL(t *testing.T) {
	long := "https://example.test/" + strings.Repeat("a", 2048)
	cases := map[string]bool{
		"https://example.test/reg?a=1&b=2": true,
		"http://example.test":              true,
		"HTTPS://Example.test/x":           true,
		"javascript:alert(1)":              false,
		"data:text/html,x":                 false,
		"ftp://example.test/x":             false,
		"https://":                         false,
		"https:///path":                    false,
		"//example.test/x":                 false,
		"example.test/x":                   false,
		"https://example.test/a b":         false,
		"https://example.test/a\tb":        false,
		"https://example.test/a\x00b":      false,
		"https://example.test/a b":         false,
		"":                                 false,
		long:                               false,
		long[:2048]:                        true,
	}
	for in, want := range cases {
		if got := ValidURL(in); got != want {
			t.Errorf("ValidURL(%.60q) = %v, want %v", in, got, want)
		}
	}
}

func TestDateTBD(t *testing.T) {
	cases := map[string]bool{
		"(дата уточняется) VI этап Кубка «Виктория»": true,
		"Лыжный марафон (ДАТА УТОЧНЯЕТСЯ)":           true,
		"Кубок, Дата Уточняется, Минск":              true,
		"V этап Кубка «Виктория» — лыжные гонки":     false,
		"Дата и место уточняются":                    false,
		"": false,
	}
	for in, want := range cases {
		if got := DateTBD(in); got != want {
			t.Errorf("DateTBD(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWatchDiff(t *testing.T) {
	const (
		url1 = "https://example.test/reg"
		url2 = "https://example.test/other"
		tbd  = "(дата уточняется) Кубок"
	)
	base := WatchState{Title: "Кубок", StartDate: day(10), Status: StatusScheduled}
	with := func(f func(*WatchState)) WatchState {
		s := base
		f(&s)
		return s
	}
	linked := with(func(s *WatchState) { s.URL = url1 })
	date := func(from, to int, fromTBD, toTBD bool) *DateChange {
		return &DateChange{From: day(from), To: day(to), FromTBD: fromTBD, ToTBD: toTBD}
	}

	cases := []struct {
		name          string
		before, after WatchState
		want          *Change // nil — изменений нет; EventID подставляет тест
	}{
		{"нет ссылки → годная", base, linked, &Change{Link: true}},
		{"негодная ссылка → годная", with(func(s *WatchState) { s.URL = "javascript:alert(1)" }), linked, &Change{Link: true}},
		{"годная → другая годная", linked, with(func(s *WatchState) { s.URL = url2 }), nil},
		{"годная → пусто", linked, base, nil},
		{"годная → негодная", linked, with(func(s *WatchState) { s.URL = "example.test" }), nil},
		{"дата изменилась", base, with(func(s *WatchState) { s.StartDate = day(3) }), &Change{Date: date(10, 3, false, false)}},
		{"заглушка на первое число → точная дата первого числа",
			WatchState{Title: tbd, StartDate: day(1), Status: StatusScheduled},
			WatchState{Title: "Кубок", StartDate: day(1), Status: StatusScheduled},
			&Change{Date: date(1, 1, true, false)}},
		{"заглушка → точная дата другого числа",
			WatchState{Title: tbd, StartDate: day(1), Status: StatusScheduled},
			WatchState{Title: "Кубок", StartDate: day(19), Status: StatusScheduled},
			&Change{Date: date(1, 19, true, false)}},
		{"точная дата → заглушка", base, WatchState{Title: tbd, StartDate: day(1), Status: StatusScheduled},
			&Change{Date: date(10, 1, false, true)}},
		{"название изменилось без смены признака", base, with(func(s *WatchState) { s.Title = "Кубок города" }), nil},
		{"название заглушки изменилось, признак прежний",
			WatchState{Title: tbd, StartDate: day(1)}, WatchState{Title: "(Дата уточняется) Кубок города", StartDate: day(1)}, nil},
		{"состоится → отменён", base, with(func(s *WatchState) { s.Status = StatusCancelled }),
			&Change{Status: &StatusChange{From: StatusScheduled, To: StatusCancelled}}},
		{"отменён → состоится", with(func(s *WatchState) { s.Status = StatusCancelled }), base,
			&Change{Status: &StatusChange{From: StatusCancelled, To: StatusScheduled}}},
		{"состоится → перенесён", base, with(func(s *WatchState) { s.Status = StatusPostponed }),
			&Change{Status: &StatusChange{From: StatusScheduled, To: StatusPostponed}}},
		{"ссылка, дата и статус вместе", base,
			WatchState{Title: "Кубок", StartDate: day(3), Status: StatusCancelled, URL: url1},
			&Change{Link: true, Date: date(10, 3, false, false), Status: &StatusChange{From: StatusScheduled, To: StatusCancelled}}},
		{"без изменений", linked, linked, nil},
	}
	for _, c := range cases {
		got := WatchDiff(map[int64]WatchState{7: c.before}, map[int64]WatchState{7: c.after}, nil)
		var want []Change
		if c.want != nil {
			w := *c.want
			w.EventID = 7
			want = []Change{w}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: WatchDiff = %s, want %s", c.name, show(got), show(want))
		}
		// Содержимое строки очереди читается обратно в то же изменение.
		for _, ch := range got {
			back, err := ParseChange(ch.Kind(), ch.EventID, ch.Payload())
			if err != nil || !reflect.DeepEqual(back, ch) {
				t.Errorf("%s: ParseChange(%s) = %s, %v; want %s", c.name, ch.Payload(), show([]Change{back}), err, show([]Change{ch}))
			}
		}
	}

	one := map[int64]WatchState{7: base}
	none := map[int64]WatchState{}
	sets := []struct {
		name          string
		before, after map[int64]WatchState
		hidden        []int64
		want          []Change
	}{
		{"событие только в снимке «после»", none, one, nil, nil},
		{"только в снимке «до», в перечне скрытых", one, none, []int64{7}, []Change{{EventID: 7, Gone: true}}},
		{"только в снимке «до», не в перечне скрытых", one, none, nil, nil},
		{"в перечне скрытых, но не было в снимке «до»", none, none, []int64{7}, nil},
		{"в перечне скрытых, но есть в обоих снимках", one, one, []int64{7}, nil},
		{"одинаковые снимки, пустой перечень", one, one, nil, nil},
		{"пустые снимки", nil, nil, nil, nil},
		{"порядок по идентификатору",
			map[int64]WatchState{30: base, 4: base, 12: base, 9: base, 100: base},
			map[int64]WatchState{30: linked, 4: linked, 9: base, 100: linked},
			[]int64{12},
			[]Change{{EventID: 4, Link: true}, {EventID: 12, Gone: true}, {EventID: 30, Link: true}, {EventID: 100, Link: true}}},
	}
	for _, c := range sets {
		if got := WatchDiff(c.before, c.after, c.hidden); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: WatchDiff = %s, want %s", c.name, show(got), show(c.want))
		}
	}

	// Вид и содержимое строки: «пропал» — без частей о полях; ключи свои и
	// короткие; незнакомые ключи при чтении не мешают.
	gone := Change{EventID: 7, Gone: true, Link: true}
	if gone.Kind() != KindGone || string(gone.Payload()) != "{}" {
		t.Errorf("«пропал»: вид %q, содержимое %s; want gone, {}", gone.Kind(), gone.Payload())
	}
	full := Change{EventID: 7, Link: true, Date: date(1, 19, true, false), Status: &StatusChange{From: StatusScheduled, To: StatusCancelled}}
	const wantPayload = `{"link":true,"date":{"from":"2026-10-01","to":"2026-10-19","from_tbd":true},"status":{"from":"scheduled","to":"cancelled"}}`
	if full.Kind() != KindChange || string(full.Payload()) != wantPayload {
		t.Errorf("изменение: вид %q, содержимое %s; want change, %s", full.Kind(), full.Payload(), wantPayload)
	}
	back, err := ParseChange(KindChange, 7, []byte(`{"link":true,"new_key":[1,2],"date":{"from":"2026-10-01","to":"2026-10-19","from_tbd":true,"x":1},"status":{"from":"scheduled","to":"cancelled"}}`))
	if err != nil || !reflect.DeepEqual(back, full) {
		t.Errorf("ParseChange с незнакомыми ключами = %s, %v; want %s", show([]Change{back}), err, show([]Change{full}))
	}
	if back, err := ParseChange(KindGone, 7, []byte(`{}`)); err != nil || !reflect.DeepEqual(back, Change{EventID: 7, Gone: true}) {
		t.Errorf("ParseChange(gone) = %s, %v", show([]Change{back}), err)
	}
	for _, bad := range [][2]string{{"reminder", `{}`}, {KindChange, `{`}, {KindChange, `{"date":{"from":"вчера","to":"2026-10-19"}}`}} {
		if _, err := ParseChange(bad[0], 7, []byte(bad[1])); err == nil {
			t.Errorf("ParseChange(%s, %s): err = nil", bad[0], bad[1])
		}
	}
}

// show — изменения одной строкой: вид, событие и содержимое.
func show(cs []Change) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = fmt.Sprintf("%s:%d%s", c.Kind(), c.EventID, c.Payload())
	}
	return "[" + strings.Join(out, " ") + "]"
}
