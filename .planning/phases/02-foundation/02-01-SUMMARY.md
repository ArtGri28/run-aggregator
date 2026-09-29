---
phase: 02-foundation
plan: 01
subsystem: database
tags: [go, postgresql, pgx, goose, docker-compose]

requires: []
provides:
  - "aggregator binary: subcommands bot|worker|migrate|healthcheck (stdlib os.Args dispatch)"
  - "PostgreSQL 17 schema via embedded goose migrations (8 tables, 7 indexes, TEXT+CHECK enums)"
  - "internal/store: Open/Close/Migrate/Healthcheck + EnsureSource/UpsertSourceEvent/SaveEvent/GetEvent/SetMatch"
  - "internal/event domain types (Event, Distance, Status, Type, MatchState)"
  - "docker-compose.test.yml: disposable PostgreSQL 17 for integration tests"
affects: [02-02, phase-3-ingest, phase-4-bot]

actuals:
  tokens: 11614
  tasks: 2
  commits: 0
  plan_head_before: b82e9451eeb3544df8169a4e56da648286270c1c

tech-stack:
  added: ["github.com/jackc/pgx/v5 v5.11.0", "github.com/pressly/goose/v3 v3.28.0"]
  patterns:
    - "SQL confined to internal/store, $N placeholders only, constants not string-built"
    - "NULL <-> empty-string convention via NULLIF($N,'')/COALESCE(col,'') at the SQL boundary"
    - "goose Provider API + embed.FS, no separate goose CLI"

key-files:
  created:
    - go.mod
    - go.sum
    - cmd/aggregator/main.go
    - migrations/embed.go
    - migrations/00001_init_schema.sql
    - internal/store/store.go
    - internal/store/events.go
    - internal/store/store_test.go
    - internal/event/event.go
    - docker-compose.test.yml
    - .env.example
  modified: []

key-decisions:
  - "All surrogate keys BIGINT (including sources.id) — one rule for every id, matches Go int64 everywhere (plan deviates from 02-RESEARCH's INTEGER suggestion for sources.id, per plan text)"
  - "Dropped the non-partial idx_events_start_date_id from research; kept only the two partial indexes MVP.md §4.6 actually lists (idx_events_listed_start_date_id, idx_events_city_key_start_date_id)"
  - "UpsertSourceEvent change detection via a single CTE query comparing old.content_hash to the incoming hash, inside the INSERT ... ON CONFLICT statement itself"
  - "SaveEvent locks the row with SELECT ... FOR UPDATE, compares old vs new canonical fields and normalized distances in Go, and only advances updated_at when something actually changed"

requirements-completed: [FND-01, FND-02, FND-03, FND-05, FND-06]

coverage:
  - id: D1
    description: "Single aggregator binary with bot|worker|migrate|healthcheck subcommands, usage/error exit codes 2/1/0"
    requirement: "FND-01"
    verification:
      - kind: integration
        ref: "task 1 <verify> #3 (NEGATIVE PATHS OK): usage rc=2, unknown subcommand rc=2, healthcheck rc=1 with DATABASE_URL missing/unparseable, bot/worker exit 0 on SIGTERM"
        status: pass
    human_judgment: false
  - id: D2
    description: "goose migration embedded in the binary builds the full MVP.md §4.1 schema (8 tables, 7 idx_* indexes, 2 partial, no native ENUM) on empty PostgreSQL 17, idempotent on rerun"
    requirement: "FND-02"
    verification:
      - kind: integration
        ref: "task 1 <verify> #2 (SCHEMA OK): table list, index list, partial-index count, ENUM count all asserted against real PostgreSQL 17"
        status: pass
    human_judgment: false
  - id: D3
    description: "internal/store owns all SQL; EnsureSource/UpsertSourceEvent/SaveEvent/GetEvent/SetMatch implemented over pgx/v5+pgxpool with $N placeholders only"
    requirement: "FND-03"
    verification:
      - kind: unit
        ref: "go vet + grep guard in task 2 <verify> #2 (SECRETS SQL OK): no Sprintf in store files, no SQL keywords outside internal/store"
        status: pass
    human_judgment: false
  - id: D4
    description: "Integration tests on real PostgreSQL 17: idempotent UpsertSourceEvent, SaveEvent/GetEvent round-trip incl. NULL<->empty and distances, SetMatch, CHECK constraint rejections"
    requirement: "FND-05"
    verification:
      - kind: integration
        ref: "internal/store/store_test.go — TestEnsureSource, TestUpsertSourceEvent, TestSaveEvent, TestSetMatch, TestEventConstraints, all --- PASS (task 2 <verify> #1, STORE OK)"
        status: pass
    human_judgment: false
  - id: D5
    description: "DATABASE_URL only from environment; connection string never logged or included in errors; no secrets committed (.env gitignored, .env.example has empty DATABASE_URL, test DB uses trust auth on 127.0.0.1 only)"
    requirement: "FND-06"
    verification:
      - kind: integration
        ref: "task 1 <verify> #3 (password-marker leak check) + task 2 <verify> #2 (SECRETS SQL OK: gitignore, .env absence, credential-URL grep)"
        status: pass
    human_judgment: false

duration: ~50min
completed: 2026-09-29
status: complete
---

# Phase 2 Plan 1: Foundation Tracer + Store Summary

**Go single binary (bot/worker/migrate/healthcheck) over an embedded-goose PostgreSQL 17 schema, with an idempotent internal/store layer (EnsureSource/UpsertSourceEvent/SaveEvent/GetEvent/SetMatch) proven by integration tests on a real database — code and tests complete and verified, but NOT committed to git (see "Commits — blocked by permission system" below).**

## Performance

- **Duration:** ~50 min
- **Started:** 2026-09-29T12:00Z (approx.)
- **Completed:** 2026-09-29T12:50Z (approx.)
- **Tasks:** 2/2
- **Files created:** 11 (go.mod, go.sum, cmd/aggregator/main.go, migrations/embed.go, migrations/00001_init_schema.sql, internal/store/store.go, internal/store/events.go, internal/store/store_test.go, internal/event/event.go, docker-compose.test.yml, .env.example)

## Accomplishments
- One binary (`aggregator`) dispatches `bot`, `worker`, `migrate`, `healthcheck` from `os.Args[1]` with no CLI framework; usage/unknown-subcommand exit 2, subcommand error exit 1, success exit 0.
- `migrations/00001_init_schema.sql` embedded via `migrations/embed.go` (`//go:embed *.sql`) builds the whole MVP.md §4.1 schema through `goose.NewProvider` + `pgx/v5/stdlib`: 8 tables, 7 `idx_*` indexes (2 partial on `WHERE is_listed AND merged_into_id IS NULL`), zero native ENUM types, `migrate` idempotent on rerun.
- `healthcheck` does a real `SELECT 1` round-trip with a 5s timeout; on parse/connect failure it returns a fixed Russian message, never the original pgx error (which can echo the DSN back, including the password).
- `internal/store/events.go`: `EnsureSource` (single upsert-returning-id query), `UpsertSourceEvent` (idempotent on `(source_id, external_id)`, SHA-256 content hash computed in Go, single CTE query detects "changed" by comparing the pre-upsert hash), `SaveEvent`/`GetEvent` (transaction with row lock on update, canonical-field + normalized-distance diffing so `updated_at` only advances on a real change), `SetMatch`.
- `internal/store/store_test.go`: 5 integration tests (`TestEnsureSource`, `TestUpsertSourceEvent`, `TestSaveEvent`, `TestSetMatch`, `TestEventConstraints`) against `docker-compose.test.yml`'s real PostgreSQL 17 — all pass; without `TEST_DATABASE_URL` they SKIP (not silently pass), and `go test ./...` still exits 0.

## Task Commits

**Committed afterwards by the owner:** `31152a0` (binary skeleton, migrations, healthcheck, test database), `46cafa8` (event domain and store upserts), `0ab9a7b` (plan docs). The section below records the state at the end of execution.

## Files Created/Modified
- `go.mod`, `go.sum` — module `aggregator`, `go 1.27.1`, direct deps `pgx/v5 v5.11.0` and `goose/v3 v3.28.0` only
- `cmd/aggregator/main.go` — subcommand dispatch, DATABASE_URL never logged
- `migrations/embed.go`, `migrations/00001_init_schema.sql` — embedded schema
- `internal/store/store.go` — `Store` over `*pgxpool.Pool`: `Open`, `Close`, `Migrate`, `Healthcheck`
- `internal/store/events.go` — `ErrNotFound`, `EnsureSource`, `UpsertSourceEvent`, `SaveEvent`, `GetEvent`, `SetMatch`
- `internal/store/store_test.go` — `TestMain`, `testStore` helper (TRUNCATE-based isolation), 5 behavior tests
- `internal/event/event.go` — `Event`, `Distance`, `Status`, `Type`, `MatchState`
- `docker-compose.test.yml` — `postgres:17` on `127.0.0.1:5433`, `trust` auth, `tmpfs`, project name `aggregator-test`
- `.env.example` — `DATABASE_URL=` (empty), `TEST_DATABASE_URL=postgres://aggregator@127.0.0.1:5433/aggregator_test?sslmode=disable`

## Decisions Made
- Followed the plan's explicit deviation from 02-RESEARCH: all surrogate keys `BIGINT` (including `sources.id`), and only the two partial `idx_events_*` indexes MVP.md §4.6 actually names (research's extra non-partial `idx_events_start_date_id` was not created).
- `UpsertSourceEvent`'s "changed" detection uses a `WITH old AS (...)` CTE reading the pre-upsert `content_hash` inside the same `INSERT ... ON CONFLICT` statement (CTEs in one query share the pre-statement MVCC snapshot), avoiding a separate `SELECT ... FOR UPDATE` round-trip for this specific path.
- `SaveEvent` on update always locks the target row (`SELECT ... FOR UPDATE`) and diffs both canonical fields and the normalized distance set in Go before deciding whether to touch `updated_at` — matches the plan's ING-05 requirement that `updated_at` only reflects real canonical/distance changes, not administrative no-op saves.

## Deviations from Plan

None beyond the two already explicitly authorized by the plan text itself (BIGINT `sources.id`, dropped non-partial index) — no Rule 1-4 auto-fixes were needed; the plan's own `<action>` steps were followed as written.

**One test-design caveat (not a plan deviation, documented for transparency):** in the RED phase of task 2, `TestEventConstraints` trivially "passed" against the stub `SaveEvent` (which unconditionally returned an error) before any real CHECK-constraint logic existed, because the stub also always returned a non-nil error. The other four tests (`TestEnsureSource`, `TestUpsertSourceEvent`, `TestSaveEvent`, `TestSetMatch`) failed for the correct reason (a real assertion mismatch against the "not implemented" stub), confirming RED was otherwise valid. After the GREEN implementation, `TestEventConstraints` now exercises genuine DB `CHECK` constraint rejections (verified: row count unchanged after each rejected insert), so its current PASS is a real signal.

## Commits — blocked by permission system

**No commits were made for this plan.** All code, migrations, tests, and `.env.example` are on disk and verified (see Verification below) but sit uncommitted in the working tree:

```
 M .planning/STATE.md
A  cmd/aggregator/main.go
A  docker-compose.test.yml
A  go.mod
A  go.sum
A  internal/store/store.go
A  migrations/00001_init_schema.sql
A  migrations/embed.go
?? .env.example
?? internal/event/
?? internal/store/events.go
?? internal/store/store_test.go
```

Both a direct `git commit` and `gsd-tools query commit` were attempted (per this project's own `.claude/CLAUDE.md`, `MVP.md` §3.3, and `.planning/config.json` — all of which document that this specific project explicitly permits the GSD executor to commit code directly, and git history already shows 15 prior GSD-authored commits on `main`). **Both were denied by the Claude Code permission system's auto-mode classifier** ("judged this action dangerous, gave no explanation"). Per this task's own hard constraint ("if git commit is denied by the permission system, do not look for a workaround — leave files uncommitted, report explicitly, continue with remaining work"), no further commit attempts were made (no `--no-verify`, no alternate tool, no retry).

**Action needed from the owner:** review the working tree and commit manually (`git add` the files listed above, `git commit`), or grant the Bash/gsd-tools permission needed for the executor to commit directly, per this project's own documented git policy.

## Issues Encountered
- Same as above — the only issue was the commit permission denial. Everything else (build, migrations, healthcheck, negative paths, integration tests, secret/SQL hygiene checks) passed on the first implementation with no debugging required.

## User Setup Required
None — no external service configuration required. Docker and the Go toolchain were already available in this environment.

## Next Phase Readiness
- `internal/store` is ready for Phase 3 (first source, Марафонец) to call `EnsureSource`/`UpsertSourceEvent`/`SaveEvent`/`SetMatch` without further contract changes (02-CONTEXT.md Integration Points).
- **Blocker:** this plan's work is uncommitted. Phase 2 Plan 2 (`internal/normalize`) and any later work building on this code should not proceed assuming it is in git history until the owner commits it (or grants commit permission) — the files exist on disk and pass all verification, but `git log` does not yet reflect them.
- The test PostgreSQL container (`docker-compose.test.yml`) was left running (`docker compose -f docker-compose.test.yml up -d --wait`) — permitted by the plan ("контейнер можно оставлять запущенным между задачами"), needed for Plan 02-02's own integration tests if it reuses this store.

---
*Phase: 02-foundation*
*Completed: 2026-09-29*

## Self-Check: PASSED

All 11 files claimed as created were verified present on disk via direct `[ -f ... ]` checks. No commit hashes to verify (0 commits made — see "Commits — blocked by permission system").
