package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

const verifyScript = "../../deploy/vps/deploy-verify.sh"

// verifySQL — запрос строки <prefix> из deploy-verify.sh: тест выполняет ровно
// тот текст, что выполняет agg verify.
func verifySQL(t *testing.T, prefix string) string {
	t.Helper()
	src, err := os.ReadFile(verifyScript)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(src), "\n") {
		if !strings.Contains(l, "'"+prefix) {
			continue
		}
		_, rest, ok := strings.Cut(l, `q "`)
		q, _, ok2 := strings.Cut(rest, `" ||`)
		if ok && ok2 {
			return q
		}
	}
	t.Fatalf("%s: нет запроса %s", verifyScript, prefix)
	return ""
}

// TestVerifyWouldAuto — 07-05, DDP-12: dedup_would_auto считает только
// pending-кандидатов с would_auto = true.
func TestVerifyWouldAuto(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, _ := ingested(t, s, "marathonec", "p", "Марафон А")
	cand := func(ext, reasons string) int64 {
		_, row := ingested(t, s, "probeg", ext, "Марафон А")
		id, err := s.UpsertCandidate(ctx, row, p, 0.9, []byte(reasons))
		if err != nil || id == 0 {
			t.Fatalf("UpsertCandidate = %d, %v", id, err)
		}
		return id
	}
	cand("t1", `{"would_auto":true}`)
	cand("t2", `{"would_auto":true,"same_city":true}`)
	cand("f", `{"would_auto":false}`)
	cand("none", `{}`)
	rej := cand("rej", `{"would_auto":true}`)
	if _, err := s.pool.Exec(ctx, `UPDATE dedup_candidates SET state = 'rejected' WHERE id = $1`, rej); err != nil {
		t.Fatal(err)
	}

	if got := scalar[string](t, verifySQL(t, "dedup_would_auto=")); got != "dedup_would_auto=pending:2" {
		t.Fatalf("got %q, want dedup_would_auto=pending:2", got)
	}
}
