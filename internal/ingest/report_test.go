package ingest

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSanityReport(t *testing.T) {
	recs := []SourceEvent{
		{ExternalID: "1", Title: "Химкинский полумарафон", Distances: "21,1 км", City: "Химки", Region: "Московская обл.", TypeLabel: "shosse"},
		{ExternalID: "2", Title: "Забег в Химках", Distances: "10 км", City: "Химки", Region: "Московская обл.", TypeLabel: "shosse"},
		{ExternalID: "3", Title: "Минский марафон", Distances: "42,2 км", City: "Минск", Region: "Минская область", TypeLabel: "shosse"},
		{ExternalID: "4", Title: "Гродно", Distances: "5 км", City: "Гродно", Region: "Беларусь", TypeLabel: "trejl"},
		{ExternalID: "5", Title: "Без области", Distances: "120 км", City: "Где-то", TypeLabel: "ультра"},
		{ExternalID: "6", Title: "Вся Россия бежит", Distances: "5 км", City: "Вся Россия",
			RegionSlugs: []string{"a", "b"}, Regions: []string{"Москва", "Тверская область"}, TypeLabel: "shosse"},
	}
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	var a, b bytes.Buffer
	if err := SanityReport(&a, "marathonec", 7, at, recs); err != nil {
		t.Fatal(err)
	}
	if err := SanityReport(&b, "marathonec", 7, at, recs); err != nil {
		t.Fatal(err)
	}
	out := a.String()
	t.Log("\n" + out)
	if out != b.String() {
		t.Fatal("report is not deterministic")
	}
	if !strings.HasPrefix(out, "# Санити-отчёт RU-fallback — marathonec, прогон 7") {
		t.Fatalf("header: %q", strings.SplitN(out, "\n", 2)[0])
	}
	for _, s := range []string{
		"Строк: 6 | уникальных областей: 3 | в перечне РФ: 1 | НЕТ в перечне: 1 | иностранных: 1 | область пуста: 1 | несколько областей: 1",
		"## Области (сначала подозрительные)",
		"| Область (как в источнике) | Статус | Строк | Города (первые 5) |",
		"| Московская обл. | в перечне РФ | 2 | Химки |",
		"| Минская область | НЕТ в перечне | 1 | Минск |",
		"| Беларусь | иностранная | 1 | Гродно |",
		"| Москва + Тверская область | несколько областей | 1 | Вся Россия |",
		"## Прочее для проверки глазами",
		"120 км",
		"ультра",
		"## Решение",
		"Заполняется по решению владельца (план 03-07): значение, дата, кто принял решение",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("report has no %q", s)
		}
	}
	rf := strings.Index(out, "| в перечне РФ |")
	for _, s := range []string{"| НЕТ в перечне |", "| иностранная |"} {
		if i := strings.Index(out, s); i < 0 || i > rf {
			t.Errorf("%q must come before rows «в перечне РФ»", s)
		}
	}
	if strings.Contains(out, "21,1 км") {
		t.Error("distances ≤ 100 km must not be listed")
	}
}

func TestSanityReportCountrySeries(t *testing.T) {
	recs := []SourceEvent{
		{ExternalID: "1", Country: "Россия", City: "Сочи"},
		{ExternalID: "2", Region: "Беларусь", City: "Гродно"},
		{ExternalID: "3", City: "Мариуполь"},
	}
	for i := range 3 {
		recs = append(recs, SourceEvent{ExternalID: fmt.Sprint("b", i), GroupKey: "series:5 верст бийск", City: "Бийск", Country: "Россия"})
	}
	recs = append(recs, SourceEvent{ExternalID: "p", GroupKey: "series:паркран"})
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	var a, b bytes.Buffer
	if err := SanityReport(&a, "probeg", 1, at, recs); err != nil {
		t.Fatal(err)
	}
	if err := SanityReport(&b, "probeg", 1, at, recs); err != nil {
		t.Fatal(err)
	}
	out := a.String()
	if out != b.String() {
		t.Fatal("report is not deterministic")
	}
	areas := strings.Index(out, "## Области")
	for _, s := range []string{
		"## Страна и серии",
		"Страна: RU — 4 | иностранная — 1 | не указана — 2",
		"Не указана, города (первые 10): Мариуполь\n",
		"Серии (group_key): ключей — 2 | записей — 4 | ключей без города — 1",
	} {
		if i := strings.Index(out, s); i < 0 || i > areas {
			t.Errorf("report has no %q before «## Области»:\n%s", s, out)
		}
	}
}

// Разрешённая страна не РФ (BY) считается отдельно от иностранных, а её
// область не попадает в «НЕТ в перечне» (перечень субъектов РФ не применяется).
func TestSanityReportAllowedCountry(t *testing.T) {
	recs := []SourceEvent{
		{ExternalID: "1", Country: "Россия", Region: "Московская область", City: "Химки"},
		{ExternalID: "2", Country: "Беларусь", Region: "Минская область", City: "Любань"},
		{ExternalID: "3", Country: "Республика Беларусь", Region: "Минская область", City: "Минск"},
		{ExternalID: "4", Country: "Беларусь", City: "Гомель"},
		{ExternalID: "5", Country: "Казахстан", Region: "Алматинская область", City: "Алматы"},
	}
	var b bytes.Buffer
	if err := SanityReport(&b, "probeg", 1, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), recs); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, s := range []string{
		"Страна: RU — 1 | BY — 3 | иностранная — 1 | не указана — 0",
		"| Минская область | страна BY | 2 |",
		"| (пусто) | страна BY | 1 | Гомель |",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("report has no %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "НЕТ в перечне | ") {
		t.Errorf("область другой страны помечена «НЕТ в перечне»:\n%s", out)
	}
}
