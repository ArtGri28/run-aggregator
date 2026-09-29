package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// sourceCodeRe — допустимый код источника: он же имя каталога, поэтому
// выйти за пределы Dir через код нельзя (T-03-30).
var sourceCodeRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// RawStore хранит сырые ответы источников (ING-10, MVP.md §5.1):
// Dir/<источник>/<run_id>-<n>.html и .meta.json. Остаются файлы Keep
// последних прогонов (0 → 2), не старше TTL (0 → 14 суток). Имена файлов —
// только из run_id и номера документа, не из данных сайта.
type RawStore struct {
	Dir  string
	TTL  time.Duration
	Keep int
}

type rawMeta struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	Bytes     int       `json:"bytes"`
	SHA256    string    `json:"sha256"`
	RunID     int64     `json:"run_id"`
}

// Save записывает документы прогона и удаляет устаревшие файлы источника.
// Каждый файл пишется во временный и переименовывается: читатель не
// видит половину файла.
func (s *RawStore) Save(source string, runID int64, docs []RawDoc) error {
	dir, err := s.dir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("raw: %w", err)
	}
	for n, d := range docs {
		sum := sha256.Sum256(d.Body)
		meta, err := json.MarshalIndent(rawMeta{d.URL, d.FetchedAt.UTC(), len(d.Body), hex.EncodeToString(sum[:]), runID}, "", "  ")
		if err != nil {
			return fmt.Errorf("raw: %w", err)
		}
		base := filepath.Join(dir, fmt.Sprintf("%d-%d", runID, n))
		if err := writeFileAtomic(base+".html", d.Body); err != nil {
			return err
		}
		if err := writeFileAtomic(base+".meta.json", append(meta, '\n')); err != nil {
			return err
		}
	}
	return s.Prune(source, time.Now())
}

// Prune удаляет файлы прогонов вне Keep последних и любые файлы старше TTL.
func (s *RawStore) Prune(source string, now time.Time) error {
	dir, err := s.dir(source)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("raw: %w", err)
	}
	var runs []int64
	for _, e := range entries {
		if id, ok := runOf(e.Name()); ok && !slices.Contains(runs, id) {
			runs = append(runs, id)
		}
	}
	slices.Sort(runs)
	keep := runs[max(0, len(runs)-orDefault(s.Keep, 2)):]
	ttl := orDefault(s.TTL, 14*24*time.Hour)

	var errs []error
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		id, ok := runOf(e.Name())
		old := false
		if fi, err := e.Info(); err == nil {
			old = now.Sub(fi.ModTime()) > ttl
		}
		if old || (ok && !slices.Contains(keep, id)) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *RawStore) dir(source string) (string, error) {
	if !sourceCodeRe.MatchString(source) {
		return "", errors.New("raw: недопустимый код источника")
	}
	return filepath.Join(s.Dir, source), nil
}

// runOf — run_id из имени «<run_id>-<n>.…».
func runOf(name string) (int64, bool) {
	head, _, ok := strings.Cut(name, "-")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(head, 10, 64)
	return id, err == nil
}

// writeFileAtomic пишет data во временный файл (0600) в каталоге path и
// переименовывает его в path.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("raw: %w", err)
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("raw: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("raw: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("raw: %w", err)
	}
	return nil
}
