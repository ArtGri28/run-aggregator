package bot

import (
	"math"
	"strings"
	"testing"

	"aggregator/internal/store"
)

func TestCallbackRoundTrip(t *testing.T) {
	for _, s := range []string{
		"f:s", "f:d", "f:d:a", "f:d:30", "f:d:cm", "f:d:nm", "f:d:m202611",
		"f:c", "f:c:-", "f:c:nizhniy-novgorod",
		"f:b", "f:b:-", "f:b:0", "f:b:4",
		"f:t", "f:t:-", "f:t:trail",
		"f:k", "f:k:-", "f:k:*", "f:k:BY", "f:k:RU", "f:k:PL",
		"f:x", "f:x:d", "f:x:t", "f:x:k",
		"p:1:v1", "p:10000:v2147483647", "e:9223372036854775807:3:v12",
		"r:a:5", "r:y:5", "r:n:5",
		"d:o", "d:m:5", "d:y:5", "d:n:5", "d:x:5", "d:s:9223372036854775807",
	} {
		c, err := parseCallback(s)
		if err != nil {
			t.Fatalf("parseCallback(%q): %v", s, err)
		}
		got, err := c.encode()
		if err != nil || got != s {
			t.Fatalf("encode(parse(%q)) = %q, %v; want the same", s, got, err)
		}
	}
}

func TestCallbackReject(t *testing.T) {
	for _, s := range []string{
		"", "x", "f:", "f:q", "f:s:1", "f:d:31", "f:d:m202613", "f:d:m199912",
		"f:c:Moscow", "f:c:a--b", "f:c:" + strings.Repeat("a", 61),
		"f:b:5", "f:b:01", "f:t:ultra", "f:x:z",
		// страна: только "-", "*" и два заглавных латинских символа
		"f:k:", "f:k:by", "f:k:By", "f:k:B", "f:k:BYY", "f:k:XYZ", "f:k:B1", "f:k:ВУ", "f:k:**", "f:k:*:1", "f:k:BY:1", "f:k: BY", "f:x:K", "f:x:kk",
		"p:0:v1", "p:01:v1", "p:1", "p:1:1", "p:1:v0", "p:+1:v1", "p:10001:v1",
		"e:0:1:v1", "e:1:1", "r:q:5", "r:a:0", "r:a:5:1",
		// сверх списка плана: лишние части, регистр, пробелы, переполнение
		"f:s:", "f:d:", "F:s", "f:d:A", " f:s", "p:1:v1:", "p:1:v2147483648", "p:99999999999999999999:v1",
		"e:9223372036854775808:1:v1", "r:a:-1",
		"d:", "d:o:1", "d:m", "d:m:0", "d:m:01", "d:q:5", "d:m:-1", "d:m:5:1", "d:x:9223372036854775808",
	} {
		if c, err := parseCallback(s); err == nil {
			t.Errorf("parseCallback(%q) = %+v, want error", s, c)
		}
	}
}

func TestCbPrefix(t *testing.T) {
	for data, want := range map[string]string{"d:x:1": "d", "r:y:1": "r", "z:1": "?", "d": "?"} {
		if got := cbPrefix(data); got != want {
			t.Errorf("cbPrefix(%q) = %q, want %q", data, got, want)
		}
	}
}

func TestCallbackLength(t *testing.T) {
	key60 := strings.Repeat("a", 60)
	if got, err := (callback{Kind: cbCity, Value: key60}).encode(); err != nil || len(got) != 64 {
		t.Fatalf("encode(city 60) = %q (%d), %v; want 64 bytes", got, len(got), err)
	}
	key61 := key60 + "a"
	if _, err := parseCallback("f:c:" + key61); err == nil {
		t.Fatal("parseCallback(city 61) = nil error")
	}
	if got, err := (callback{Kind: cbCity, Value: key61}).encode(); err == nil {
		t.Fatalf("encode(city 61) = %q, want error", got)
	}

	// Все кнопки экранов на крайних значениях укладываются в 64 байта.
	f := userFilters{City: key60, Dist: []int{0, 1, 2, 3, 4}, Type: "obstacle"}
	items := []store.ListItem{{ID: math.MaxInt64, Title: "t"}}
	for name, sc := range map[string]screen{
		"summary": summaryScreen(f, "Город", math.MaxInt32, math.MaxInt32, true),
		"list":    listScreen(items, 50000, 10000, math.MaxInt32, true),
		"list1":   listScreen(items, 1, 1, 1, false),
		"empty":   emptyScreen(f, false),
	} {
		if sc.kb == nil || len(sc.kb.InlineKeyboard) == 0 {
			t.Errorf("%s: нет клавиатуры", name)
		}
		for _, r := range sc.kb.InlineKeyboard {
			for _, b := range r {
				if len(b.CallbackData) == 0 || len(b.CallbackData) > 64 {
					t.Errorf("%s: callback_data %q — %d байт", name, b.CallbackData, len(b.CallbackData))
				}
				if _, err := parseCallback(b.CallbackData); err != nil {
					t.Errorf("%s: callback_data %q не разбирается: %v", name, b.CallbackData, err)
				}
			}
		}
	}
	// Карточка (04-03): предельные id, страница и версия.
	if got, err := (callback{Kind: cbCard, ID: math.MaxInt64, Page: 10000, V: math.MaxInt32}).encode(); err != nil || len(got) > 64 {
		t.Errorf("encode(card max) = %q, %v", got, err)
	}
}

func TestCountryCallbackLength(t *testing.T) {
	for _, v := range []string{"", "-", "*", "BY", "RU"} {
		got, err := (callback{Kind: cbCountry, Value: v}).encode()
		if err != nil || len(got) > 7 {
			t.Errorf("encode(country %q) = %q (%d), %v; want ≤ 7 bytes", v, got, len(got), err)
		}
	}
	if got, err := (callback{Kind: cbReset, Value: "k"}).encode(); err != nil || got != "f:x:k" {
		t.Errorf("encode(reset k) = %q, %v; want f:x:k", got, err)
	}
	for _, v := range []string{"by", "XYZ", "B", "ВУ"} {
		if got, err := (callback{Kind: cbCountry, Value: v}).encode(); err == nil {
			t.Errorf("encode(country %q) = %q, want error", v, got)
		}
	}
}
