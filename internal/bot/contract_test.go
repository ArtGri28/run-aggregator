package bot

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

// uxContracts — контракты экранов: Phase 8 и Phase 9 (D-16). Сценарии обоих
// сливаются в одно множество; номера не повторяются.
var uxContracts = []string{
	"../../.planning/phases/08-telegram-bot-ux/08-UX-CONTRACT.md",
	"../../.planning/phases/09-onboarding-user-profile/09-UX-CONTRACT.md",
}

var (
	contractScenarioRE = regexp.MustCompile("(?m)^### S-([0-9]{2}) .*тест `(TestS[0-9]{2}[A-Za-z0-9]*)`")
	scenarioTestRE     = regexp.MustCompile(`(?m)^func (TestS([0-9]{2})[A-Za-z0-9]*)\(t \*testing\.T\)`)
	scenarioSnapshotRE = regexp.MustCompile(`^s([0-9]{2})_.+\.txt$`)
)

// TestContractCoverage: множество сценариев S-xx двух контрактов экранов (Phase 8
// и Phase 9) равно множеству тестов TestSxx…; у каждого сценария есть хотя бы один
// эталон sxx_*, а тестов и эталонов сценариев, которых нет ни в одном контракте,
// нет. Лишние и отсутствующие сценарии перечисляются в ошибке.
func TestContractCoverage(t *testing.T) {
	all := map[string]string{} // номер сценария → имя теста по контрактам
	nPhase8, nPhase9 := 0, 0
	for i, path := range uxContracts {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range contractScenarioRE.FindAllStringSubmatch(string(doc), -1) {
			if m[2][5:7] != m[1] {
				t.Errorf("S-%s: тест %s с другим номером", m[1], m[2])
			}
			if prev, dup := all[m[1]]; dup {
				t.Errorf("S-%s: сценарий есть в двух контрактах или дважды — %s и %s", m[1], prev, m[2])
			}
			all[m[1]] = m[2]
			if i == 0 {
				nPhase8++
			} else {
				nPhase9++
			}
		}
	}
	if nPhase8 < 17 || nPhase9 < 8 {
		t.Fatalf("найдено сценариев: Phase 8 — %d (want ≥ 17), Phase 9 — %d (want ≥ 8)", nPhase8, nPhase9)
	}

	got := map[string]string{}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range scenarioTestRE.FindAllStringSubmatch(string(src), -1) {
			if prev, dup := got[m[2]]; dup {
				t.Errorf("S-%s: два теста — %s и %s", m[2], prev, m[1])
			}
			got[m[2]] = m[1]
		}
	}
	if !reflect.DeepEqual(got, all) {
		t.Errorf("тесты сценариев расходятся с контрактами:\nтесты    %v\nконтракты %v", sorted(got), sorted(all))
	}

	snaps := map[string]bool{}
	entries, err := os.ReadDir(screensDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if m := scenarioSnapshotRE.FindStringSubmatch(e.Name()); m != nil {
			snaps[m[1]] = true
			if _, ok := all[m[1]]; !ok {
				t.Errorf("эталон %s: сценария S-%s нет в контракте", e.Name(), m[1])
			}
		}
	}
	for n := range all {
		if !snaps[n] {
			t.Errorf("S-%s: нет эталона s%s_*", n, n)
		}
	}
}

func sorted(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, "S-"+k+"="+v)
	}
	sort.Strings(out)
	return out
}
