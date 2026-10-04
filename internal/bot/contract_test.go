package bot

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

const uxContract = "../../.planning/phases/08-telegram-bot-ux/08-UX-CONTRACT.md"

var (
	contractScenarioRE = regexp.MustCompile("(?m)^### S-([0-9]{2}) .*тест `(TestS[0-9]{2}[A-Za-z0-9]*)`")
	scenarioTestRE     = regexp.MustCompile(`(?m)^func (TestS([0-9]{2})[A-Za-z0-9]*)\(t \*testing\.T\)`)
	scenarioSnapshotRE = regexp.MustCompile(`^s([0-9]{2})_.+\.txt$`)
)

// TestContractCoverage: каждый сценарий S-xx контракта экранов имеет свой
// тест TestSxx… и хотя бы один эталон sxx_*; тестов и эталонов сценариев,
// которых нет в контракте, тоже нет.
func TestContractCoverage(t *testing.T) {
	doc, err := os.ReadFile(uxContract)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{} // номер сценария → имя теста по контракту
	for _, m := range contractScenarioRE.FindAllStringSubmatch(string(doc), -1) {
		if m[2][5:7] != m[1] {
			t.Errorf("S-%s: тест %s с другим номером", m[1], m[2])
		}
		want[m[1]] = m[2]
	}
	if len(want) < 17 {
		t.Fatalf("в контракте найдено сценариев: %d, want ≥ 17", len(want))
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("тесты сценариев расходятся с контрактом:\nтесты    %v\nконтракт %v", sorted(got), sorted(want))
	}

	snaps := map[string]bool{}
	entries, err := os.ReadDir(screensDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if m := scenarioSnapshotRE.FindStringSubmatch(e.Name()); m != nil {
			snaps[m[1]] = true
			if _, ok := want[m[1]]; !ok {
				t.Errorf("эталон %s: сценария S-%s нет в контракте", e.Name(), m[1])
			}
		}
	}
	for n := range want {
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
