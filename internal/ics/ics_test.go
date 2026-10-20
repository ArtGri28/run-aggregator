package ics

import (
	"strings"
	"testing"
	"time"
)

func TestCalendar(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	long := strings.Repeat("Длинное название; с, запятой ", 5)
	got := string(Calendar("Где старт? — избранное", day(1), true, []Event{
		{UID: "start-1@test_bot", Title: long, Start: day(3), Location: "Москва, парк", URL: "https://t.me/test_bot?start=e_1", Description: "Карточка\nИсточник"},
		{UID: "start-2@test_bot", Title: "Двухдневный", Start: day(5), End: day(6)},
	}))
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\n", "REFRESH-INTERVAL;VALUE=DURATION:PT6H\r\n",
		"DTSTART;VALUE=DATE:20261003\r\nDTEND;VALUE=DATE:20261004\r\n",
		"DTSTART;VALUE=DATE:20261005\r\nDTEND;VALUE=DATE:20261007\r\n",
		`LOCATION:Москва\, парк`, `DESCRIPTION:Карточка\nИсточник`, "DTSTAMP:20261001T000000Z", "END:VCALENDAR\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q в\n%s", want, got)
		}
	}
	for _, l := range strings.Split(got, "\r\n") {
		if len(l) > 75 {
			t.Errorf("строка длиннее 75 байт: %q", l)
		}
	}
	unfolded := strings.ReplaceAll(got, "\r\n ", "")
	if !strings.Contains(unfolded, "SUMMARY:"+strings.ReplaceAll(strings.ReplaceAll(long, ";", `\;`), ",", `\,`)+"\r\n") {
		t.Errorf("название после склейки строк искажено:\n%s", unfolded)
	}
	if strings.Contains(string(Calendar("x", day(1), false, nil)), "REFRESH") {
		t.Error("файл без подписки содержит REFRESH-INTERVAL")
	}
}
