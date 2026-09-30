package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestRawStoreSave(t *testing.T) {
	s := &RawStore{Dir: t.TempDir()}
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for run := int64(1); run <= 3; run++ {
		doc := RawDoc{URL: "https://marathonec.ru/calendar-beg/", Body: []byte("<html>run</html>"), FetchedAt: at}
		if err := s.Save("marathonec", run, []RawDoc{doc}); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(s.Dir, "marathonec")
	want := []string{"2-0.html", "2-0.meta.json", "3-0.html", "3-0.meta.json"}
	if got := dirNames(t, dir); !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
	for _, n := range want {
		if fi, _ := os.Stat(filepath.Join(dir, n)); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", n, fi.Mode().Perm())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "3-0.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"url", "fetched_at", "bytes", "sha256", "run_id"} {
		if _, ok := meta[k]; !ok {
			t.Errorf("meta.json has no %q: %s", k, raw)
		}
	}
	if meta["run_id"] != float64(3) || meta["bytes"] != float64(len("<html>run</html>")) {
		t.Errorf("meta.json = %s", raw)
	}
	if strings.Contains(string(raw), "RunAggregator") || strings.Contains(strings.ToLower(string(raw)), "user-agent") {
		t.Fatalf("meta.json mentions User-Agent: %s", raw)
	}
}

func TestRawStorePruneTTL(t *testing.T) {
	s := &RawStore{Dir: t.TempDir(), TTL: 24 * time.Hour}
	now := time.Now()
	docs := []RawDoc{{URL: "https://example.org/", Body: []byte("x"), FetchedAt: now}}
	for run := int64(1); run <= 2; run++ {
		if err := s.Save("src", run, docs); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(s.Dir, "src")
	old := now.Add(-48 * time.Hour)
	for _, n := range []string{"1-0.html", "1-0.meta.json"} {
		if err := os.Chtimes(filepath.Join(dir, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Prune("src", 2, now); err != nil {
		t.Fatal(err)
	}
	if got, want := dirNames(t, dir), []string{"2-0.html", "2-0.meta.json"}; !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestRawStoreBadSource(t *testing.T) {
	root := t.TempDir()
	s := &RawStore{Dir: filepath.Join(root, "raw")}
	docs := []RawDoc{{URL: "https://example.org/", Body: []byte("x")}}
	for _, code := range []string{"../x", "a/b", "", "UPPER", "."} {
		if err := s.Save(code, 1, docs); err == nil {
			t.Errorf("Save(%q) = nil, want error", code)
		}
	}
	if got := dirNames(t, root); len(got) != 0 {
		t.Fatalf("files created: %v", got)
	}
}

// LO-01: id прогонов пошли заново (пересозданная БД) — файлы только что
// сохранённого прогона остаются.
func TestRawStoreKeepsCurrentRun(t *testing.T) {
	s := &RawStore{Dir: t.TempDir()}
	docs := []RawDoc{{URL: "https://example.org/", Body: []byte("x")}}
	for _, run := range []int64{56, 57, 1} {
		if err := s.Save("src", run, docs); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "src", "1-0.html")); err != nil {
		t.Fatalf("file of the saved run 1: %v; files %v", err, dirNames(t, filepath.Join(s.Dir, "src")))
	}
}
