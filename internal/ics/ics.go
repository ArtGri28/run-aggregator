// Package ics собирает календарь iCalendar (RFC 5545) из стартов: событие на
// весь день, без времени и без данных пользователя (Phase 21, 21-UX-CONTRACT.md
// раздел 4).
package ics

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Event — старт в календаре. Start и End — первый и последний день старта
// (полночь UTC, как start_date); End нулевое — однодневный.
type Event struct {
	UID         string
	Title       string
	Start, End  time.Time
	Location    string
	URL         string
	Description string
}

// Calendar — календарь name из events; stamp — момент сборки (DTSTAMP);
// refresh — подписной календарь: телефону подсказывается перечитывать его
// раз в шесть часов.
func Calendar(name string, stamp time.Time, refresh bool, events []Event) []byte {
	var b strings.Builder
	line := func(s string) { b.WriteString(fold(s) + "\r\n") }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//gde-start//calendar//RU")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:" + escape(name))
	if refresh {
		line("REFRESH-INTERVAL;VALUE=DURATION:PT6H")
		line("X-PUBLISHED-TTL:PT6H")
	}
	for _, e := range events {
		end := e.End
		if end.IsZero() || end.Before(e.Start) {
			end = e.Start
		}
		line("BEGIN:VEVENT")
		line("UID:" + escape(e.UID))
		line("DTSTAMP:" + stamp.UTC().Format("20060102T150405Z"))
		line("DTSTART;VALUE=DATE:" + e.Start.Format("20060102"))
		line("DTEND;VALUE=DATE:" + end.AddDate(0, 0, 1).Format("20060102"))
		line("SUMMARY:" + escape(e.Title))
		if e.Location != "" {
			line("LOCATION:" + escape(e.Location))
		}
		if e.URL != "" {
			line("URL:" + e.URL)
		}
		if e.Description != "" {
			line("DESCRIPTION:" + escape(e.Description))
		}
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return []byte(b.String())
}

var escaper = strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)

func escape(s string) string { return escaper.Replace(s) }

// fold переносит строку длиннее 75 байт: продолжение — с новой строки и
// пробела; многобайтный символ не разрывается.
func fold(s string) string {
	if len(s) <= 75 {
		return s
	}
	var b strings.Builder
	n, limit := 0, 75
	for _, r := range s {
		w := utf8.RuneLen(r)
		if n+w > limit {
			b.WriteString("\r\n ")
			n, limit = 0, 74
		}
		b.WriteRune(r)
		n += w
	}
	return b.String()
}
