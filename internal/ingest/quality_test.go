package ingest

import (
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	def := DefaultThresholds()
	if def.UnrecognizedMax != 0.10 || def.OutOfScopeMax != 0.20 || def.MinRuns != 3 || def.RejectedAbs != 0.30 || def.RejectedMax != 0.20 {
		t.Fatalf("DefaultThresholds() = %+v", def)
	}
	// good — 100 строк, все валидны и в каталоге, пустых полей нет.
	good := func() Stats { return Stats{Parsed: 100, Valid: 100} }
	with := func(f func(*Stats)) Stats { s := good(); f(&s); return s }
	young := Baseline{OKRuns: 1}
	mature := Baseline{OKRuns: 3, Parsed: []int{90, 100, 110}, EmptyShare: map[string]float64{"city": 0.02}}

	tests := []struct {
		name   string
		s      Stats
		b      Baseline
		status RunStatus
		reason string // подстрока причины при suspect
	}{
		{"первый прогон", good(), Baseline{}, StatusSuspect, "первый прогон источника — требуется принятие"},
		{"абсолютная: rejected 31 %", with(func(s *Stats) { s.Rejected, s.Valid = 31, 69 }), young, StatusSuspect, "31.0 %"},
		{"абсолютная: rejected 30 %", with(func(s *Stats) { s.Rejected, s.Valid = 30, 70 }), young, StatusOK, ""},
		{"медиана: 49", with(func(s *Stats) { s.Parsed, s.Valid = 49, 49 }), mature, StatusSuspect, "49"},
		{"медиана: 50", with(func(s *Stats) { s.Parsed, s.Valid = 50, 50 }), mature, StatusOK, ""},
		{"медиана: 500", with(func(s *Stats) { s.Parsed, s.Valid = 500, 500 }), mature, StatusOK, ""},
		{"медиана: 501", with(func(s *Stats) { s.Parsed, s.Valid = 501, 501 }), mature, StatusSuspect, "501"},
		{"база: rejected 21 %", with(func(s *Stats) { s.Rejected, s.Valid = 21, 79 }), mature, StatusSuspect, "21.0 %"},
		{"база: rejected 20 %", with(func(s *Stats) { s.Rejected, s.Valid = 20, 80 }), mature, StatusOK, ""},
		// D-11: expired не входит в знаменатель — 10 из 50, а не из 100.
		{"expired вне знаменателя", with(func(s *Stats) { s.Expired, s.Rejected, s.Valid = 50, 10, 40 }), mature, StatusOK, ""},
		{"expired вне знаменателя, 11 из 50", with(func(s *Stats) { s.Expired, s.Rejected, s.Valid = 50, 11, 39 }), mature, StatusSuspect, "22.0 %"},
		{"даты вне диапазона 6 %", with(func(s *Stats) { s.DateOutOfRange, s.Rejected, s.Valid = 6, 6, 94 }), mature, StatusSuspect, "6.0 %"},
		{"даты вне диапазона 5 %", with(func(s *Stats) { s.DateOutOfRange, s.Rejected, s.Valid = 5, 5, 95 }), mature, StatusOK, ""},
		{"out_of_scope 21 %", with(func(s *Stats) { s.OutOfScope = 21 }), mature, StatusSuspect, "21.0 %"},
		{"out_of_scope 20 %", with(func(s *Stats) { s.OutOfScope = 20 }), mature, StatusOK, ""},
		{"нераспознанные 11 %", with(func(s *Stats) { s.UnrecognizedRegion = 11 }), mature, StatusSuspect, "11.0 %"},
		{"нераспознанные 10 %", with(func(s *Stats) { s.UnrecognizedRegion = 10 }), mature, StatusOK, ""},
		{"нераспознанные при первом прогоне тоже считаются", with(func(s *Stats) { s.UnrecognizedRegion = 11 }), young, StatusSuspect, "11.0 %"},
		{"пустые city 0.23 при базе 0.02", with(func(s *Stats) { s.Empty = map[string]int{"city": 23} }), mature, StatusSuspect, "city"},
		{"пустые city 0.21 при базе 0.02", with(func(s *Stats) { s.Empty = map[string]int{"city": 21} }), mature, StatusOK, ""},
		{"рост пустых полей до базы не проверяется", with(func(s *Stats) { s.Empty = map[string]int{"city": 50} }), young, StatusOK, ""},
	}
	for _, tt := range tests {
		st, why := Evaluate(tt.s, tt.b, def)
		if st != tt.status {
			t.Errorf("%s: status %s, want %s (причины %q)", tt.name, st, tt.status, why)
			continue
		}
		if tt.status == StatusOK && len(why) != 0 {
			t.Errorf("%s: ok с причинами %q", tt.name, why)
		}
		if tt.reason != "" && !strings.Contains(strings.Join(why, "; "), tt.reason) {
			t.Errorf("%s: причины %q не содержат %q", tt.name, why, tt.reason)
		}
	}

	// Причины строятся от значений порогов, а не от зашитых процентов.
	strict := def
	strict.RejectedAbs = 0.05
	if _, why := Evaluate(with(func(s *Stats) { s.Rejected, s.Valid = 6, 94 }), young, strict); len(why) != 1 || !strings.Contains(why[0], "5.0 %") {
		t.Errorf("порог 5 %%: причины %q", why)
	}
}

func TestMedian(t *testing.T) {
	tests := []struct {
		in   []int
		want float64
	}{
		{nil, 0},
		{[]int{3, 1, 2}, 2},
		{[]int{1, 2, 3, 4}, 2.5},
	}
	for _, tt := range tests {
		in := append([]int(nil), tt.in...)
		if got := median(tt.in); got != tt.want {
			t.Errorf("median(%v) = %v, want %v", tt.in, got, tt.want)
		}
		for i := range in {
			if in[i] != tt.in[i] {
				t.Errorf("median изменила вход: %v", tt.in)
			}
		}
	}
}
