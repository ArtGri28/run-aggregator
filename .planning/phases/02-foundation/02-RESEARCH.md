# Phase 2: Foundation - Research

**Researched:** 2026-09-29
**Domain:** Go single-binary CLI + PostgreSQL 17 schema/migrations + normalization logic, greenfield (no existing Go code in repo)
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**D-01 (тестовая инфраструктура, FND-05):** Интеграционные тесты получают реальную PostgreSQL 17 через отдельный `docker-compose.test.yml` (только сервис `postgres:17`, без `bot`/`worker`/`migrate`). Отдельный файл от production-состава Docker Compose из `MVP.md` §10.2 — тот появится в Phase 6.

**D-02 (подкоманда healthcheck, FND-01):** `healthcheck` в Phase 2 делает реальную проверку — подключается к PostgreSQL через `pgxpool` и выполняет `SELECT 1`; код выхода 0 при успехе, 1 при ошибке. Пригодно для `HEALTHCHECK` в Docker Compose и для ручной проверки. Heartbeat-таблица и мониторинг/алерты из `MVP.md` §11 — Phase 6, здесь не реализуются.

**D-03 (начальный словарь городов, `city_key`, MVP §6):** Словарь синонимов в Phase 2 минимальный — только города/регионы, реально встречающиеся у двух утверждённых источников Phase 1 (Марафонец, ПроБЕГ), плюс очевидные синонимы (СПб/Питер/Санкт-Петербург и подобные). Расширяется по мере подключения новых источников в следующих фазах, а не зашивается заранее общероссийским списком. Неизвестный город получает `city_key` транслитерацией — ничего не ломается.

### Claude's Discretion

Точная структура каталогов внутри `internal/` (кроме уже зафиксированной в `MVP.md` §3.2), формат имён файлов миграций `goose`, конкретный список переменных окружения (кроме того, что секреты не коммитятся) — оставлено на усмотрение планировщика/исполнителя.

### Deferred Ideas (OUT OF SCOPE)

- Production Docker Compose (postgres/migrate/bot/worker, §10.2) — Phase 6.
- Heartbeat-таблица, мониторинг, алерты (§11) — Phase 6.
- Расширение словаря городов за пределы двух утверждённых источников — по мере подключения новых источников (Phase 3+).
- Сетевая логика (`Fetch`/`Parse`), Telegram-бот, дедуп — следующие фазы (Phase 3–5).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| FND-01 | Go-модуль (Go 1.27.1), один бинарник, подкоманды `bot`, `worker`, `migrate`, `healthcheck` | §Architecture Patterns — stdlib `os.Args` dispatch pattern; §Code Examples — `cmd/aggregator/main.go` skeleton; `bot`/`worker` are stub subcommands this phase (no `go-telegram/bot` dependency needed yet) |
| FND-02 | Схема PostgreSQL 17 из `MVP.md` §4 миграциями `goose`, встроенными в бинарник | §Standard Stack (goose Provider API); §Common Pitfalls (embed.FS directory constraint, enum-in-transaction pitfall); §Code Examples (full DDL, migrate subcommand) |
| FND-03 | Доменные типы `event` и слой `store` на `pgx/v5`+`pgxpool`; весь SQL только в `internal/store` | §Standard Stack (pgxpool); §Architecture Patterns (store package shape, querier interface for tests); §Code Examples (pgxpool setup, upsert pattern) |
| FND-04 | Пакет `normalize`: `title_norm`, `city_key`, даты/часовой пояс, дистанции в метрах, тип, `country_code`, `region`/`city`, обрезка `description` до 1000 символов | §Common Pitfalls (rune-vs-byte truncation, comma decimal separator, named distances); §Code Examples (`truncateDescription`, `ParseDistance`, transliteration table); §Package Legitimacy Audit (no transliteration library — hand-rolled table) |
| FND-05 | Тестовая инфраструктура на реальной PostgreSQL 17: юнит-тесты `normalize`, интеграционный upsert-тест | §Code Examples (`docker-compose.test.yml`, `TestMain`, TRUNCATE-based isolation, table-driven test pattern) |
| FND-06 | Конфигурация через переменные окружения; секреты не в репозитории | §Architecture Patterns (minimal `Config` struct, `os.LookupEnv`); §Security Domain |
</phase_requirements>

## Summary

This is the first Go code in the repository — there is nothing to reuse, only `MVP.md` decisions to implement. The phase has two independent halves that don't need each other to be "done" to be individually correct: (1) a **process skeleton** — one binary, `os.Args`-based subcommand dispatch, no CLI framework, matching MVP's explicit "no new dependency without approval" rule — and (2) a **data layer** — a `goose`-driven PostgreSQL 17 schema plus a `pgx/v5`/`pgxpool` `store` package and a pure `normalize` package. The riskiest technical fact discovered this session is a **hard Go language constraint**: `//go:embed` patterns cannot contain `..` and cannot reach outside the directory of the `.go` file that declares them ([CITED: pkg.go.dev/embed]). MVP.md's project-structure diagram places `migrations/` as a *sibling* of `cmd/` and `internal/`, which cannot be embedded directly from an `internal/store/*.go` file. This must be resolved by placing the `//go:embed` directive in a small file that lives inside `migrations/` itself (a tiny `package migrations` at repo root, imported by `store`/`cmd`), not by moving the directory MVP.md fixed in its diagram.

The two Phase-1-approved sources (Марафонец, ПроБЕГ) directly shape two `normalize` decisions this phase must get right: Marathonec's HTML gives a `dd/mm/yyyy` date and free-text distances like "21,1 км" (comma decimal separator, not dot); ProBeg gives `dd.mm` (no year) and "Область, Город" city strings. Neither report enumerates the *exact* list of city names seen — only region counts and format — so the Phase 2 city-dictionary seed is necessarily a judgment call, not a verified list (flagged in Assumptions Log).

For the schema, `MVP.md` §4.1 is prescriptive about columns and states but silent on SQL types (TEXT vs VARCHAR, ENUM vs CHECK, SERIAL vs IDENTITY, TIMESTAMP vs TIMESTAMPTZ). Current PostgreSQL community guidance is unambiguous on all four: prefer `TEXT`, prefer `TEXT + CHECK` over native `ENUM` (enum value removal requires a full type rewrite; CHECK constraints are a plain constraint swap — [CITED: crunchydata.com/blog/enums-vs-check-constraints-in-postgres]), prefer `GENERATED ALWAYS AS IDENTITY` over `SERIAL` ([CITED: wiki.postgresql.org/wiki/Don't_Do_This]), and prefer `TIMESTAMPTZ` for true instants — but **not** for `events.start_date`/`start_time`, which MVP.md §4.2 explicitly stores as local wall-clock values with a separate IANA `timezone` string, precisely to avoid the instant-vs-local-time conversion `TIMESTAMPTZ` would silently perform.

**Primary recommendation:** stdlib-only `os.Args` subcommand dispatch; `pgx/v5` + `pgxpool` for the pool, `pressly/goose/v3`'s `Provider` API (not the separate `goose` CLI) with `database/sql` via `pgx/v5/stdlib` for migrations; `TEXT + CHECK` for every enum-shaped column; `GENERATED ALWAYS AS IDENTITY` for every surrogate key; a hand-rolled Cyrillic→ASCII transliteration table in `normalize` (no dependency — the mapping is small, fixed, and MVP.md already mandates the fallback behavior); TRUNCATE-based test isolation against a single `docker-compose.test.yml` Postgres 17 service.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| CLI subcommand dispatch (`bot`/`worker`/`migrate`/`healthcheck`) | API / Backend | — | Process entry point; no browser/frontend tier exists in this project |
| Schema definition (tables, constraints, indexes) | Database / Storage | API / Backend | Schema is owned by PostgreSQL; the `goose` SQL files that define it live in the binary (API/Backend) but their effect is Database/Storage state |
| Migration execution (`migrate` subcommand) | API / Backend | Database / Storage | Orchestration code runs in the binary; it mutates Database/Storage schema |
| `store` package (all SQL) | API / Backend | Database / Storage | MVP.md §3 mandates all SQL live in `internal/store`; it is the sole boundary to Database/Storage |
| `normalize` package (title_norm, city_key, dates, distances, country_code) | API / Backend | — | Pure in-process functions, no I/O; operates on data already in memory |
| `healthcheck` subcommand | API / Backend | Database / Storage | Process-level check that round-trips to Database/Storage (`SELECT 1`) and reports via exit code |
| Config from environment | API / Backend | — | Read once at process startup; no persistence, no external service |

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|---------------|
| `github.com/jackc/pgx/v5` | v5.11.0 [VERIFIED: proxy.golang.org, 2026-09-07] | PostgreSQL driver, `pgxpool` connection pool | Already locked in `MVP.md` §3.1; de facto standard Go PostgreSQL driver (14.3k GitHub stars, active, [VERIFIED: api.github.com/repos/jackc/pgx]) |
| `github.com/jackc/pgx/v5/stdlib` | (part of pgx/v5) | `database/sql` compatibility shim so `goose`'s `Provider` (which requires `*sql.DB`) can run against the same PostgreSQL dialect | [CITED: github.com/jackc/pgx/stdlib package doc, fetched via Context7] — `sql.Open("pgx", dsn)` or `stdlib.OpenDBFromPool(pool)` |
| `github.com/pressly/goose/v3` | v3.28.0 [VERIFIED: proxy.golang.org, 2026-09-02] | SQL migrations, run as a subcommand of the binary via the `Provider` API, `embed.FS` | Already locked in `MVP.md` §3.1; long-standing, active (11.5k GitHub stars, [VERIFIED: api.github.com/repos/pressly/goose]) |
| `log/slog`, `flag`, `os`, `context`, `database/sql`, `embed`, `crypto/sha256`, `net/http` | Go 1.27.1 stdlib | CLI dispatch, config, migrations glue, content hashing, (future) HTTP client | `MVP.md` §3.1 explicitly forbids new deps for scheduler/rate-limiter/config; stdlib covers everything Phase 2 needs |

### Supporting (not installed this phase)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/go-telegram/bot` | v1.27.0 [VERIFIED: proxy.golang.org, 2026-09-11] | Telegram long polling | Locked in `MVP.md` §3.1 for Phase 4 (`bot` subcommand's real logic). **Do not add this dependency in Phase 2** — the phase boundary explicitly excludes Telegram bot logic, and `go.mod` should only list what's actually imported. |
| `golang.org/x/net/html` or `github.com/PuerkitoBio/goquery` | — | HTML parsing for Phase 3 source parsers | `MVP.md` §3.1: decided "when the first source requires it," needs separate owner approval. Not needed in Phase 2 (no `Fetch`/`Parse` this phase). |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| stdlib `os.Args` switch dispatch | `spf13/cobra`, `urfave/cli` | Both are mature, but `MVP.md` §3.1 explicitly bars new dependencies without approval, and a 4-subcommand dispatcher needs none of cobra's help-generation/completion machinery |
| `goose` `Provider` API (library) | `goose` CLI binary invoked via `exec.Command`, or golang-migrate | Contradicts "миграции... встроенные в бинарник" (FND-02); shelling out to a separate `goose` binary would require it to be present in the runtime image, defeating the single-binary goal |
| Native PostgreSQL `ENUM` types | `TEXT` + `CHECK (col IN (...))` | ENUM is slightly more compact and self-documenting in `\d`, but adding a value inside a transaction can't be used in the same transaction ([CITED: postgresql.org/docs/17/sql-altertype.html]), and there is no `DROP VALUE` at all — removing a value requires a full type rewrite. Given the project is 5 phases from stable and status/state vocabularies may still shift, `CHECK` is the safer default ([CITED: crunchydata.com/blog/enums-vs-check-constraints-in-postgres]) |
| Hand-rolled Cyrillic transliteration table | A transliteration library (e.g. `essentialkaos/translit`) | A library adds a dependency for ~35 fixed rune mappings that MVP.md already specifies the *behavior* for (fallback slug for unknown cities); the mapping is small, deterministic, and easy to unit-test in place — squarely inside the "no new dependency without approval" constraint |

**Installation:**
```bash
go mod init <module-path>   # module path TBD — see Open Questions
go get github.com/jackc/pgx/v5@v5.11.0
go get github.com/pressly/goose/v3@v3.28.0
```

**Version verification:** confirmed against the Go module proxy (`proxy.golang.org`), which is the authoritative Go package registry (equivalent role to npm's registry for Node) — not training-data recall. `go.mod` minimums: `pgx/v5` requires Go ≥1.25.0, `goose/v3` requires Go ≥1.26.0 [VERIFIED: raw go.mod files at the tagged versions above] — both compatible with the pinned Go 1.27.1.

## Package Legitimacy Audit

> The `package-legitimacy check` seam only supports `npm|pypi|crates` ecosystems; Go modules were verified manually against the Go module proxy and GitHub.

| Package | Registry | Age | Stars | Source Repo | Verdict | Disposition |
|---------|----------|-----|-------|--------------|---------|-------------|
| `github.com/jackc/pgx/v5` | Go module proxy | created 2013 (13 yrs) | 14,277 | github.com/jackc/pgx | OK | Approved — install this phase |
| `github.com/pressly/goose/v3` | Go module proxy | created 2016 (10 yrs) | 11,511 | github.com/pressly/goose | OK | Approved — install this phase |
| `github.com/go-telegram/bot` | Go module proxy | created 2022 (4 yrs) | 1,845 | github.com/go-telegram/bot | OK | Not installed this phase (Phase 4) — no action needed now |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** none. All three packages are established, actively maintained, non-archived repositories with plausible star counts and matching org/repo names — no slopsquatting signal.

## Architecture Patterns

### System Architecture Diagram

```
                         ┌───────────────────────────┐
                         │   cmd/aggregator/main.go   │
                         │  os.Args[1] switch:        │
                         │  bot | worker | migrate |  │
                         │  healthcheck               │
                         └─────────────┬──────────────┘
                                       │ each branch:
                                       │ 1. load Config (env)
                                       │ 2. pgxpool.New(ctx, dsn)
                                       ▼
        ┌─────────────────────────────────────────────────────────┐
        │                                                         │
        ▼                                                         ▼
┌───────────────┐                                     ┌────────────────────┐
│ migrate        │  sql.Open("pgx", dsn) (database/sql)│ bot / worker / hc  │
│ goose.Provider │─────────────────────────────────────▶ *pgxpool.Pool      │
│ (embed.FS SQL) │  separate connection, same DSN       │ (app queries)      │
└───────┬───────┘                                     └─────────┬──────────┘
        │ applies migrations/*.sql                               │ calls
        ▼                                                        ▼
┌───────────────────────────────┐                     ┌────────────────────┐
│ PostgreSQL 17 schema           │◀────────────────────│ internal/store      │
│ sources, ingest_runs,          │   all SQL lives here │ (Upsert*, Get*)     │
│ source_events, events,         │                      └─────────┬──────────┘
│ event_distances,               │                                │ uses domain types
│ dedup_candidates, users,       │                                ▼
│ heartbeats                     │                      ┌────────────────────┐
└───────────────────────────────┘                      │ internal/event      │
                                                         │ (Event, Distance,   │
                                                         │  Filter, Status)    │
                                                         └─────────┬──────────┘
                                                                   │ populated from
                                                                   ▼
                                                         ┌────────────────────┐
                                                         │ internal/normalize  │
                                                         │ title_norm,city_key,│
                                                         │ distances, country  │
                                                         │ (pure, no I/O)      │
                                                         └────────────────────┘
```

A reader trace for the primary use case this phase proves (`healthcheck`, and the integration test's upsert path): `main.go` dispatches → loads `DATABASE_URL` from env → opens a `pgxpool.Pool` → either runs `SELECT 1` (healthcheck) or calls into `internal/store` which runs parameterized SQL against the schema `migrate` created.

### Recommended Project Structure

```
go.mod
cmd/
  aggregator/
    main.go              # os.Args dispatch only — no business logic
migrations/
  embed.go               # package migrations; //go:embed *.sql; var FS embed.FS
  00001_init_schema.sql  # -- +goose Up / -- +goose Down
internal/
  config/
    config.go            # Config struct, LoadFromEnv()
  event/
    event.go             # Event, Distance, Filter, Status, Type domain types
  store/
    store.go             # Store struct wrapping *pgxpool.Pool (or a small querier interface)
    source_events.go     # UpsertSourceEvent
    events.go            # UpsertEvent / event_distances helpers
  normalize/
    title.go             # TitleNorm
    city.go              # CityKey, synonym dictionary, transliteration fallback
    distance.go          # ParseDistance
    description.go       # TruncateDescription
    country.go           # normalize country name -> ISO code (fallback logic itself is Phase 3/ING-11)
  bot/                    # empty/stub package this phase — real logic Phase 4
  worker/                 # empty/stub package this phase — real logic Phase 3
docker-compose.test.yml
```

`migrations/` stays a **sibling** of `cmd/` and `internal/`, matching `MVP.md` §3.2's diagram — but note it now contains a tiny `embed.go` (see Pitfall 1) instead of the embed directive living in `internal/store`.

### Pattern 1: stdlib-only subcommand dispatch

**What:** `main()` reads `os.Args[1]`, switches into one function per subcommand; each subcommand owns its own `flag.NewFlagSet` if it needs flags.
**When to use:** Always for this binary — `MVP.md` §3.1 forbids a CLI framework dependency for this.
**Example:**
```go
// Source: Go stdlib flag package pattern (https://pkg.go.dev/flag#example-package-Getopt),
// adapted for subcommands — this shape has no single canonical doc page but is the
// standard idiom for "no framework" Go CLIs.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aggregator <bot|worker|migrate|healthcheck> [flags]")
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "bot":
		err = runBot(ctx, os.Args[2:])
	case "worker":
		err = runWorker(ctx, os.Args[2:])
	case "migrate":
		err = runMigrate(ctx, os.Args[2:])
	case "healthcheck":
		err = runHealthcheck(ctx, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		slog.Error("command failed", "subcommand", os.Args[1], "error", err)
		os.Exit(1)
	}
}
```

### Pattern 2: `goose.Provider` + `embed.FS`, run as a library not a CLI

**What:** `goose.NewProvider(goose.DialectPostgres, db, fsys)` where `db` is a `*sql.DB` opened with the `pgx` stdlib driver, and `fsys` is an `embed.FS`.
**When to use:** Inside `runMigrate` only. [VERIFIED: pressly/goose v3.28.0 source, `provider.go` — `NewProvider` exists and is documented for exactly this use case: "The caller is responsible for matching the database dialect with the database/sql driver... the database dialect is 'postgres', the database/sql driver could be ... github.com/jackc/pgx"]
**Example:**
```go
// Source: Context7 /pressly/goose (README.md, quick-reference.md), adapted
package main

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"example.com/aggregator/migrations"
)

func runMigrate(ctx context.Context, args []string) error {
	dsn, ok := os.LookupEnv("DATABASE_URL")
	if !ok || dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("new goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		fmt.Println(r.String())
	}
	return nil
}
```

### Pattern 3: `store` package around `*pgxpool.Pool`, parameterized SQL only

**What:** A `Store` struct wraps `*pgxpool.Pool`; every query uses `$1, $2, ...` placeholders, never string concatenation.
**When to use:** Every read/write to PostgreSQL.
**Example:**
```go
// Source: Context7 /jackc/pgx (Getting-started-with-pgx wiki page), adapted for upsert
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// UpsertSourceEvent is idempotent on (source_id, external_id) per the UNIQUE
// constraint in MVP.md §4.1. Unchanged rows only bump last_seen_at (ING-04,
// implemented fully in Phase 3 — Phase 2 needs the upsert primitive to exist
// and be exercised by the integration test).
func (s *Store) UpsertSourceEvent(ctx context.Context, sourceID int64, externalID, url string, payload []byte, contentHash string) (int64, error) {
	const q = `
		INSERT INTO source_events (source_id, external_id, url, payload, content_hash, first_seen_at, last_seen_at, state, missed_runs, match_state)
		VALUES ($1, $2, $3, $4, $5, now(), now(), 'active', 0, 'new')
		ON CONFLICT (source_id, external_id) DO UPDATE
		SET last_seen_at = now(),
		    payload = CASE WHEN source_events.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.payload ELSE source_events.payload END,
		    content_hash = CASE WHEN source_events.content_hash <> EXCLUDED.content_hash THEN EXCLUDED.content_hash ELSE source_events.content_hash END
		RETURNING id`
	var id int64
	err := s.pool.QueryRow(ctx, q, sourceID, externalID, url, payload, contentHash).Scan(&id)
	return id, err
}
```

### Pattern 4: Minimal env-based config

**What:** A `Config` struct populated by `os.LookupEnv`, validated once at startup, no config library.
**When to use:** Phase 2 only needs `DATABASE_URL` — resist adding fields for env vars no Phase 2 code reads yet (YAGNI; `MVP.md` §3.1 stdlib-only applies here too).
**Example:**
```go
// Source: idiomatic Go env-config pattern (stdlib os.LookupEnv), no external source needed
package config

import (
	"fmt"
	"os"
)

type Config struct {
	DatabaseURL string
}

func LoadFromEnv() (Config, error) {
	dsn, ok := os.LookupEnv("DATABASE_URL")
	if !ok || dsn == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return Config{DatabaseURL: dsn}, nil
}
```

### Anti-Patterns to Avoid

- **Shelling out to the `goose` CLI binary via `exec.Command`:** defeats the single-binary requirement (FND-01/FND-02) and requires the `goose` binary to exist in the deployment image.
- **String-concatenated SQL anywhere ("just this once"):** `internal/store` is the only place SQL exists precisely so every query can be grepped and reviewed for parameterization; concatenating even a "safe" internal value (e.g. a table name from a constant) sets a bad precedent for the next contributor who concatenates a *user*-derived value.
- **Native PostgreSQL `ENUM` for `status`/`state`/`match_state`:** see Alternatives Considered — removing or renaming a value later requires a type rewrite, and MVP's own iterative philosophy ("расширяется по мере...", §3) argues against locking these vocabularies into a type.
- **Byte-based string truncation for `description`:** `len(s)` and `s[:1000]` operate on UTF-8 bytes, not characters — see Pitfall 1.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|--------------|-----|
| PostgreSQL migrations / schema versioning | A custom `schema_migrations` table + manual SQL runner | `pressly/goose`'s `Provider` API | Already locked in MVP.md §3.1; goose handles version tracking, up/down, transaction wrapping, and the `NO TRANSACTION` escape hatch correctly — a hand-rolled runner would need to reinvent all of this |
| PostgreSQL connection pooling / retry-on-idle-connection-death | A custom `sync.Pool` of `*pgx.Conn` | `pgxpool.Pool` | `pgxpool` already implements idle health checks, connection lifetime limits, and lifecycle hooks (`BeforeConnect`, `AfterConnect`) — reinventing this is exactly the kind of "deceptively complex" problem the Don't-Hand-Roll principle exists for |
| Content hashing for change detection (`source_events.content_hash`) | A custom hash/checksum | stdlib `crypto/sha256` (or `crypto/fnv` if collision resistance is not a concern) over the canonicalized `payload` bytes | Stdlib already solves this; "don't hand-roll" doesn't mean "avoid stdlib," only avoid re-implementing what a library (here, the standard library itself) already does correctly |

**Key insight:** In this phase, the *dependencies to avoid* (a CLI framework, a config library, a transliteration library) are not violations of "don't hand-roll" — MVP.md already made those calls (§3.1) and this research doesn't second-guess locked decisions. The genuine "don't hand-roll" risks this phase are migrations and connection pooling, both already covered by the locked stack.

## Common Pitfalls

### Pitfall 1: `//go:embed` cannot reach outside its own directory

**What goes wrong:** `internal/store/embed.go` with `//go:embed ../../migrations/*.sql` fails to compile.
**Why it happens:** Go's `embed` package spec: "Patterns may not contain '.' or '..' or empty path elements, nor may they begin or end with a slash" [CITED: pkg.go.dev/embed]. `MVP.md` §3.2's diagram shows `migrations/` as a *sibling* of `internal/`, which is not embeddable from a file living inside `internal/store/`.
**How to avoid:** Put the `//go:embed *.sql` directive in a tiny file that lives *inside* `migrations/` itself (e.g. `migrations/embed.go`, `package migrations`, `var FS embed.FS`), and import that package from `cmd/aggregator` (or from `internal/store` if the migrate logic is placed there). This keeps `migrations/` at the top level exactly as `MVP.md` §3.2 draws it, satisfies the language constraint, and doesn't violate "all SQL lives in `internal/store`" — migration DDL is schema management, not query logic, and precedent elsewhere in Go projects treats it as a separate concern from the query layer.
**Warning signs:** `go build` errors like `pattern ../../migrations/*.sql: invalid pattern syntax` or `patterns must not contain '..'`.

### Pitfall 2: byte truncation vs. rune truncation for `description`

**What goes wrong:** `if len(s) > 1000 { s = s[:1000] }` cuts a UTF-8 multi-byte Cyrillic string mid-character (each Cyrillic letter is 2 bytes in UTF-8), corrupting the last character and truncating at roughly **half** the intended 1000-character limit for all-Cyrillic text.
**Why it happens:** Go's `len(string)` and slice indexing on a `string` operate on bytes, not on Unicode code points ("символы" in MVP.md's requirement means characters, not bytes).
**How to avoid:** Convert to `[]rune` first, truncate the rune slice, then convert back and trim to the last word boundary:
```go
// Source: standard Go idiom for rune-safe truncation (no single canonical doc page;
// this is the widely-documented fix for the len()/byte-slice pitfall on non-ASCII text)
package normalize

import (
	"strings"
	"unicode"
)

func TruncateDescription(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRightFunc(cut, unicode.IsSpace)
}
```
**Warning signs:** A golden-file test with a long Cyrillic description that doesn't visibly break on ASCII fixtures but corrupts on real Marathonec/ProBeg descriptions — write the FND-04 test fixture with **Cyrillic** text specifically, not Latin placeholder text, or this bug passes silently.

### Pitfall 3: comma as decimal separator in source distance strings

**What goes wrong:** `strconv.ParseFloat("21,1", 64)` returns an error — Go's float parser expects `.` as the decimal separator, but both approved sources write distances with a comma (Marathonec: "21,1 км"; per Phase 1 report §2.1 item 10).
**Why it happens:** Russian typographic convention uses `,` for decimals; Go's numeric parsing is locale-independent and only accepts `.`.
**How to avoid:** `strings.ReplaceAll(match, ",", ".")` before `strconv.ParseFloat`, inside the distance-parsing regex match, not globally on the whole input string (a global replace could corrupt unrelated commas, e.g. in "Область, Город" city strings that also appear in the same source event).
**Warning signs:** `ParseDistance` silently returning `ok=false` for every fractional-km distance (half marathons, "21,1 км") while whole-km distances ("10 км") parse fine — a test suite that only covers whole-number distances won't catch this.

### Pitfall 4: named distances without a number ("полумарафон", "марафон")

**What goes wrong:** A regex that only matches `<number> (км|м)` misses bare words like "полумарафон", which the phase's own success criteria explicitly require normalizing to meters.
**How to avoid:** Check a small named-distance lookup table *before* falling back to the numeric regex: `{"марафон": 42195, "полумарафон": 21098}` (official IAAF distances, rounded to the nearest meter — 21097.5m → 21098m). This exact rounding is an engineering judgment call, not sourced from `MVP.md` or the Phase 1 report — flagged in Assumptions Log (A1). `MVP.md` §4.3's merge tolerance absorbs a few meters of difference either way, so the precise rounding choice is low-risk.
**Warning signs:** none of this phase's fixtures include a bare-word distance — write at least one test case for "полумарафон" specifically, since it's named in the phase's own success criteria (not just MVP.md).

### Pitfall 5: enum-shaped column, ENUM type, and transactional migrations don't mix

**What goes wrong:** If a native `ENUM` type is used for `status`/`state`/`match_state` and a later migration both adds a new enum value *and* references it in the same migration file, it fails: "If `ALTER TYPE ... ADD VALUE` ... is executed inside a transaction block, the new value cannot be used until after the transaction has been committed" [CITED: postgresql.org/docs/17/sql-altertype.html]. `goose` wraps each migration file in a transaction by default.
**Why it happens:** PostgreSQL's enum catalog changes are not fully transactional in the same way ordinary DML is.
**How to avoid:** This phase avoids the problem entirely by using `TEXT + CHECK (col IN (...))` for every enum-shaped column instead of native `ENUM` (see Standard Stack → Alternatives Considered). Widening the set of allowed values later is then just `ALTER TABLE ... DROP CONSTRAINT ...; ALTER TABLE ... ADD CONSTRAINT ... CHECK (...)`, ordinary transactional DDL.
**Warning signs:** N/A this phase (informs the type choice, not something that can go wrong later if `TEXT + CHECK` is followed).

### Pitfall 6: `TRUNCATE` order and identity-column resets between integration tests

**What goes wrong:** Truncating only `events` between tests leaves orphaned rows in `event_distances`/`source_events`/`dedup_candidates` if their foreign keys aren't `ON DELETE CASCADE`, or the truncate itself fails with a foreign-key violation.
**How to avoid:** One `TRUNCATE TABLE sources, ingest_runs, source_events, events, event_distances, dedup_candidates, users, heartbeats RESTART IDENTITY CASCADE;` statement naming every table — `CASCADE` here means "also truncate tables with FKs pointing at any listed table," which is safe because every table with such an FK is already in the list; `RESTART IDENTITY` resets `GENERATED ALWAYS AS IDENTITY` sequences so test assertions on IDs are deterministic across runs.
**Warning signs:** Flaky integration tests where ID-based assertions pass in isolation but fail when run after another test in the same suite.

## Code Examples

### PostgreSQL 17 DDL — `migrations/00001_init_schema.sql`

Derived from `MVP.md` §4.1 (table/column list, `[VERIFIED: MVP.md:111-143]`, quoted below) and §4.6 (index list, `[VERIFIED: MVP.md:192-199]`, quoted below), with SQL types filled in per the Standard Stack recommendations (TEXT+CHECK, GENERATED ALWAYS AS IDENTITY, TIMESTAMPTZ for instants / DATE+TIME for wall-clock local values).

MVP.md's verbatim table list (§4.1, lines 111-143):
```
sources(id, code UNIQUE)              приоритеты и интервалы источников — в коде

ingest_runs(id, source_id, started_at, finished_at,
            status: running|ok|suspect|failed,
            fetched, parsed, rejected, out_of_scope, upserted, error)

source_events(id, source_id, external_id, url, payload jsonb, content_hash,
              first_seen_at, last_seen_at,
              state: active|missing|gone, missed_runs,
              event_id NULL, match_state: new|auto|manual|candidate|out_of_scope,
              last_run_id,
              UNIQUE(source_id, external_id))

events(id, title, title_norm,
       start_date NOT NULL, end_date NULL, start_time NULL, timezone NULL,
       country_code, region NULL, city NULL, city_key NULL, address NULL,
       event_type, status: scheduled|cancelled|postponed,
       description NULL (≤1000 символов), registration_url NULL,
       is_listed, merged_into_id NULL, created_at, updated_at)

event_distances(event_id, distance_m INTEGER, label NULL,
                PK(event_id, distance_m))

dedup_candidates(id, source_event_id, event_id, score, reasons jsonb,
                 state: pending|confirmed|rejected, created_at,
                 UNIQUE(source_event_id, event_id))

users(tg_id PK, filters jsonb, filters_v, created_at, last_seen_at)

heartbeats(component PK, beat_at)
```

MVP.md's verbatim index list (§4.6, lines 192-199):
```
- `events(start_date, id)` и `events(city_key, start_date, id)`, частичные: `WHERE is_listed AND merged_into_id IS NULL`;
- `events(country_code, start_date)` для кандидатов дедупа;
- `event_distances(distance_m, event_id)`;
- `source_events(event_id)`, `source_events(state, last_seen_at)`;
- `ingest_runs(source_id, started_at DESC)`.
```

```sql
-- +goose Up

CREATE TABLE sources (
    id   INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code TEXT NOT NULL UNIQUE
);

CREATE TABLE ingest_runs (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id    INTEGER NOT NULL REFERENCES sources(id) ON DELETE RESTRICT,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ,
    status       TEXT NOT NULL CHECK (status IN ('running', 'ok', 'suspect', 'failed')),
    fetched      INTEGER NOT NULL DEFAULT 0,
    parsed       INTEGER NOT NULL DEFAULT 0,
    rejected     INTEGER NOT NULL DEFAULT 0,
    out_of_scope INTEGER NOT NULL DEFAULT 0,
    upserted     INTEGER NOT NULL DEFAULT 0,
    error        TEXT
);
CREATE INDEX idx_ingest_runs_source_started ON ingest_runs (source_id, started_at DESC);

CREATE TABLE events (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title            TEXT NOT NULL,
    title_norm       TEXT NOT NULL,
    start_date       DATE NOT NULL,
    end_date         DATE,
    start_time       TIME,
    timezone         TEXT,
    country_code     TEXT NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    region           TEXT,
    city             TEXT,
    city_key         TEXT CHECK (city_key IS NULL OR (char_length(city_key) <= 64 AND city_key ~ '^[a-z0-9-]+$')),
    address          TEXT,
    event_type       TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'cancelled', 'postponed')),
    description      TEXT CHECK (description IS NULL OR char_length(description) <= 1000),
    registration_url TEXT,
    is_listed        BOOLEAN NOT NULL DEFAULT true,
    merged_into_id   BIGINT REFERENCES events(id) ON DELETE RESTRICT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_start_date_id ON events (start_date, id);
CREATE INDEX idx_events_city_key_start_date_id ON events (city_key, start_date, id)
    WHERE is_listed AND merged_into_id IS NULL;
CREATE INDEX idx_events_listed_start_date_id ON events (start_date, id)
    WHERE is_listed AND merged_into_id IS NULL;
CREATE INDEX idx_events_country_start_date ON events (country_code, start_date);

CREATE TABLE source_events (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id      INTEGER NOT NULL REFERENCES sources(id) ON DELETE RESTRICT,
    external_id    TEXT NOT NULL,
    url            TEXT NOT NULL,
    payload        JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_hash   TEXT NOT NULL,
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    state          TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'missing', 'gone')),
    missed_runs    INTEGER NOT NULL DEFAULT 0,
    event_id       BIGINT REFERENCES events(id) ON DELETE RESTRICT,
    match_state    TEXT NOT NULL DEFAULT 'new' CHECK (match_state IN ('new', 'auto', 'manual', 'candidate', 'out_of_scope')),
    last_run_id    BIGINT REFERENCES ingest_runs(id) ON DELETE RESTRICT,
    UNIQUE (source_id, external_id)
);
CREATE INDEX idx_source_events_event_id ON source_events (event_id);
CREATE INDEX idx_source_events_state_last_seen ON source_events (state, last_seen_at);

CREATE TABLE event_distances (
    event_id   BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    distance_m INTEGER NOT NULL CHECK (distance_m > 0),
    label      TEXT,
    PRIMARY KEY (event_id, distance_m)
);
CREATE INDEX idx_event_distances_distance_event ON event_distances (distance_m, event_id);

CREATE TABLE dedup_candidates (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_event_id BIGINT NOT NULL REFERENCES source_events(id) ON DELETE CASCADE,
    event_id        BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    score           NUMERIC NOT NULL,
    reasons         JSONB NOT NULL DEFAULT '{}'::jsonb,
    state           TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'confirmed', 'rejected')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_event_id, event_id)
);

CREATE TABLE users (
    tg_id         BIGINT PRIMARY KEY,
    filters       JSONB NOT NULL DEFAULT '{}'::jsonb,
    filters_v     INTEGER NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE heartbeats (
    component TEXT PRIMARY KEY,
    beat_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE heartbeats;
DROP TABLE users;
DROP TABLE dedup_candidates;
DROP TABLE event_distances;
DROP TABLE source_events;
DROP TABLE events;
DROP TABLE ingest_runs;
DROP TABLE sources;
```

Notes on judgment calls the planner should confirm (none of these are in MVP.md verbatim, all `[ASSUMED]`, see Assumptions Log):
- `sources.id INTEGER`, all other surrogate PKs `BIGINT` — sources is a tiny, hand-maintained table (2-3 rows for the whole project); everything else can grow.
- `country_code ~ '^[A-Z]{2}$'` enforces ISO 3166-1 alpha-2 shape at the DB level as defense-in-depth; the actual normalization logic (MVP §6) lives in Go.
- `city_key` CHECK mirrors the 64-byte `callback_data` budget MVP §6 states in prose; enforcing it at the DB level catches a `normalize` bug before it reaches a Telegram `callback_data` overflow in Phase 4.
- `description` CHECK duplicates the Go-side truncation (Pitfall 2) as defense-in-depth, not a replacement for it — `normalize` must still truncate on a word boundary; the DB constraint alone would reject correctly-truncated-later data if a bug re-inserts pre-truncation text and merely bounds the damage.
- `merged_into_id`/FK `ON DELETE RESTRICT` choices: MVP.md states `source_events`/`events` are "never deleted" (§4.5); `RESTRICT` documents that expectation at the schema level rather than allowing an accidental `DELETE` to cascade silently. `event_distances`/`dedup_candidates` use `CASCADE` because MVP.md explicitly says distances are recomputed from scratch (§4.6) and dedup candidates are a working queue tied to specific rows, not durable history.

### Cyrillic → ASCII transliteration fallback

```go
// Source: common Russian-web URL-slug transliteration convention (not a single
// canonical spec — GOST 7.79 System B and most "translit for URLs" implementations
// converge on this table); [ASSUMED], not verified against an authoritative standard
// this session — flagged in Assumptions Log (A2).
package normalize

var cyrillicToLatin = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "c", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

func transliterate(s string) string {
	var b []byte
	for _, r := range []rune(s) {
		if repl, ok := cyrillicToLatin[unicode.ToLower(r)]; ok {
			b = append(b, repl...)
			continue
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b = append(b, byte(r))
		} else if r == ' ' {
			b = append(b, '-')
		}
		// else: drop the rune (punctuation, etc.)
	}
	return string(b)
}
```

### Table-driven test pattern for `normalize`

```go
// Source: standard Go table-driven test idiom (go.dev/wiki/TableDrivenTests)
package normalize

import "testing"

func TestCityKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"moscow plain", "Москва", "moskva"}, // adjust to dictionary-based key, not transliteration, if Moscow is in the synonym dict
		{"spb full", "Санкт-Петербург", "saint-petersburg"},
		{"spb short", "СПб", "saint-petersburg"},
		{"spb colloquial", "Питер", "saint-petersburg"},
		{"unknown city falls back to transliteration", "Урюпинск", "uryupinsk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CityKey(tt.in)
			if got != tt.want {
				t.Errorf("CityKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
```

### `docker-compose.test.yml` and integration test bootstrap

```yaml
# Source: standard docker compose healthcheck pattern (docs.docker.com/reference/compose-file/services)
services:
  postgres:
    image: postgres:17
    environment:
      POSTGRES_USER: aggregator_test
      POSTGRES_PASSWORD: aggregator_test
      POSTGRES_DB: aggregator_test
    ports:
      - "5433:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U aggregator_test -d aggregator_test"]
      interval: 2s
      timeout: 2s
      retries: 30
```

```bash
# CI / local invocation — `--wait` blocks until the healthcheck reports healthy
# [CITED: docs.docker.com/compose/how-tos/startup-order — depends_on/condition:
# service_healthy semantics; `--wait` is the single-service equivalent for a
# compose file with no dependent services]
docker compose -f docker-compose.test.yml up -d --wait
TEST_DATABASE_URL="postgres://aggregator_test:aggregator_test@localhost:5433/aggregator_test?sslmode=disable" go test ./... -tags=integration
docker compose -f docker-compose.test.yml down -v
```

```go
// Source: idiomatic Go integration-test bootstrap (TestMain + build tag), no
// single canonical doc page — standard community pattern for opt-in DB tests
//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Stderr.WriteString("TEST_DATABASE_URL not set, skipping integration tests\n")
		os.Exit(0)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	defer pool.Close()
	// Apply migrations once for the whole test binary run (reuse the same
	// goose.Provider path as runMigrate, against migrations.FS).
	testPool = pool
	code := m.Run()
	os.Exit(code)
}

func truncateAll(t *testing.T) {
	t.Helper()
	const q = `TRUNCATE TABLE sources, ingest_runs, source_events, events,
		event_distances, dedup_candidates, users, heartbeats RESTART IDENTITY CASCADE`
	if _, err := testPool.Exec(context.Background(), q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
```

### `healthcheck` subcommand

```go
// Source: pgxpool.Pool.Ping / QueryRow pattern per Context7 /jackc/pgx (Getting-started-with-pgx)
func runHealthcheck(ctx context.Context, args []string) error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("select 1: %w", err)
	}
	return nil
}
// main() already maps a non-nil error to os.Exit(1) and no error to falling
// through to a normal (0) exit — see Pattern 1.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|-------------------|---------------|--------|
| `SERIAL` / `BIGSERIAL` primary keys | `GENERATED ALWAYS AS IDENTITY` | PostgreSQL 10 (2017), community consensus solidified since | Avoids `SERIAL`'s "weird [ownership/permission] behaviors" [CITED: wiki.postgresql.org/wiki/Don't_Do_This]; no functional difference for a project starting fresh on PG17 |
| Separate `goose` CLI binary + `goose.SetDialect`/`goose.Up(db, dir)` global-function API | `goose.NewProvider(...)` instance-based `Provider` API | goose v3.x (current: v3.28.0) | Instance-based API is what supports embedding a filesystem cleanly and is the API this phase should use for in-binary migrations; the old global-function style still works but is documented as legacy in goose's own docs |

**Deprecated/outdated:** none directly relevant beyond the two rows above — this is a greenfield phase with no legacy code to migrate away from.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|-----------------|
| A1 | Named distances map to official/rounded meters: марафон=42195, полумарафон=21098 | Common Pitfalls #4, Code Examples (DDL notes) | Low — MVP §4.3's merge tolerance absorbs a few meters either way; wrong only if MVP intends the literal "21,1 км" rounding (21100) to be used consistently instead of the IAAF figure |
| A2 | Cyrillic→Latin transliteration table (GOST-System-B-style) is an acceptable fallback convention | Code Examples (transliteration) | Low-medium — `city_key` fallback slugs are only used for cities *not* in the synonym dictionary (D-03); a different letter mapping wouldn't break functionality, only produce a different (still valid) ASCII slug. Confirm the mapping once real unmapped city names surface in Phase 3 |
| A3 | The exact set of city names appearing in Марафонец/ПроБЕГ data (beyond "49 российских областей" and the "Область, Город" format) is not enumerated anywhere in the Phase 1 report | D-03 (initial city dictionary), Summary | Medium — the Phase 2 dictionary seed can only include well-known Russian city synonyms (Moscow, Saint Petersburg, and similar) reasoned from general knowledge, not a verified list from Phase 1 data. Real gaps will surface once Phase 3 actually parses live pages |
| A4 | `sources.id` as `INTEGER` (not `BIGINT`) and per-column `ON DELETE` choices (`RESTRICT` vs `CASCADE`) in the DDL | Code Examples (DDL) | Low — these are engineering defaults consistent with MVP's stated deletion/recompute semantics, not verbatim MVP requirements; easy to adjust in a follow-up migration before the schema has real data |
| A5 | `bot`/`worker` subcommands should be minimal stubs this phase (open pool, log, exit) rather than blocking process skeletons | Architecture Patterns, Phase Requirements table | Low — either shape satisfies FND-01's "binary runs the subcommand" criterion; a blocking skeleton better rehearses Phase 3/4's eventual shape but isn't required this phase |
| A6 | Go module path for `go mod init` (no remote configured yet per STATE.md) | Open Questions | Low — module path is purely internal until the module needs to be `go get`-able externally; renaming later via `go mod edit -module` is a one-line fix |

## Open Questions

1. **Exact Go module path for `go mod init`**
   - What we know: `git remote` is not yet configured (STATE.md: "remote ещё не настроен, push невозможен до настройки владельцем"), so there's no canonical import path yet.
   - What's unclear: whether to use a placeholder (e.g. `aggregator`) now and rename once the remote exists, or wait for the remote.
   - Recommendation: use a placeholder module path now (e.g. `aggregator` or the eventual repo name if already known to the owner); `go mod edit -module` is a trivial one-command fix once the real path is known, and blocking Phase 2 on remote configuration would stall unrelated work.

2. **Migration file naming: sequential vs. timestamp**
   - What we know: `MVP.md` leaves this to the planner (CONTEXT.md discretion). No `goose` CLI binary is installed (migrations run only via the `Provider` API in-binary), so `goose create` isn't necessarily part of the workflow — migration files can be authored by hand.
   - What's unclear: whether the executor will ever want `goose create`'s convenience.
   - Recommendation: sequential, zero-padded, hand-named files (`00001_init_schema.sql`, `00002_...sql`) — this is a single-owner project (no concurrent-branch collision risk that motivates goose's timestamp default), and the numbering is easier to read in a short migration history.

3. **Exact list of city names / synonyms for the D-03 seed dictionary**
   - What we know: Marathonec's snapshot has "49 российских областей" values (region-level, not necessarily city-level); ProBeg's rows are "Область, Город" or just "Город," specific names not enumerated in the Phase 1 report.
   - What's unclear: the literal strings that will need dictionary entries.
   - Recommendation: seed with the well-known top-tier synonym cases MVP.md itself names as examples (§6: "СПб", "Питер", "Санкт-Петербург") plus Moscow's common forms, and treat everything else as relying on the transliteration fallback until Phase 3 actually parses live pages and surfaces real gaps — this matches D-03's own instruction not to pre-build a national list.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|--------------|-----------|---------|----------|
| Go toolchain | Building/testing the binary | ✓ | go1.27.1 darwin/arm64 [VERIFIED: `go version`] | — |
| Docker | `docker-compose.test.yml` (D-01) | ✓ | 29.4.0 [VERIFIED: `docker --version`] | — |
| Docker Compose | `docker-compose.test.yml` (D-01) | ✓ | v5.1.2 [VERIFIED: `docker compose version`] | — |
| `psql` CLI | Manual schema inspection during development | ✗ | — | Not required — `migrate`/tests use `pgx`/`goose` programmatically; a developer can add `psql` locally or exec into the test container (`docker compose exec postgres psql ...`) if manual inspection is wanted |
| PostgreSQL 17 server (standalone, non-Docker) | N/A this phase | not probed | — | Not needed — D-01 locks integration tests to the Docker Compose service, not a bare-metal install |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** `psql` (optional developer convenience, not required for any Phase 2 success criterion).

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|-----------------|---------|--------------------|
| V2 Authentication | No | No authentication surface exists in this phase (no exposed network service; `bot`/`worker` are stubs) |
| V3 Session Management | No | Same as above |
| V4 Access Control | No | Same as above |
| V5 Input Validation | Yes | `normalize` package is the input-validation boundary for everything eventually written to PostgreSQL: `description` length (Pitfall 2), `country_code` shape, `city_key` charset/length. Enforced twice — in Go (source of truth, unit-tested) and as DB `CHECK` constraints (defense-in-depth) |
| V6 Cryptography | No hand-rolled crypto | `content_hash` uses stdlib `crypto/sha256` (a hash, not encryption — no cryptographic secrecy claim is being made about it); no secrets are encrypted at rest this phase (out of scope — Phase 6 backup encryption, PRD-03) |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|------------------------|
| SQL injection via unparameterized queries | Tampering | All `internal/store` queries use `pgx` placeholder args (`$1, $2, ...`); never `fmt.Sprintf` SQL with untrusted input. Enforced by convention + code review, since Go has no compiler-level guard here |
| Secret leakage into logs or error messages | Information Disclosure | `DATABASE_URL` (contains the DB password) must never be logged directly; `config.LoadFromEnv()` should not implement a `String()`/`GoString()` method that prints the DSN verbatim, and any `slog` call logging config should log individual non-secret fields, not the whole struct |
| Secrets committed to the repository | Information Disclosure | `.env`/`.env.*` already `.gitignore`d (verified: repo's existing `.gitignore`); `.env.example` should list variable *names* only, never real values — MVP.md §12.1 |
| Migration SQL executed with elevated/unexpected privileges | Elevation of Privilege | Out of scope for Phase 2 — the same `DATABASE_URL` is used for both `migrate` and app queries in this phase; a privilege-separated migration user is a Phase 6 production-hardening concern (not blocking Phase 2's local/test schema creation) |

## Sources

### Primary (HIGH confidence)
- Go module proxy (`proxy.golang.org`) — version/publish-date verification for `pgx/v5`, `goose/v3`, `go-telegram/bot`
- GitHub API (`api.github.com/repos/...`) — star count, creation date, archived status for legitimacy audit
- `pkg.go.dev/embed` — `//go:embed` pattern restrictions (fetched via WebFetch, quoted verbatim)
- `postgresql.org/docs/17/sql-altertype.html` — `ALTER TYPE ... ADD VALUE` transaction restriction (fetched via WebFetch, quoted verbatim)
- Project files read this session: `MVP.md` (§3, §4, §5.1-5.2, §6, §12.1), `.planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md` (§1, §2.1, §2.4, §4.2, §5), `.planning/REQUIREMENTS.md`, `.planning/STATE.md`, `.planning/phases/02-foundation/02-CONTEXT.md`

### Secondary (MEDIUM confidence)
- Context7 `/pressly/goose` — `Provider`/`NewProvider` API, `embed.FS` usage, `NO TRANSACTION` annotation
- Context7 `/jackc/pgx` — `pgxpool.New`, `QueryRow`/`Exec`/`Begin` method surface, `stdlib` compatibility shim
- `wiki.postgresql.org/wiki/Don't_Do_This` — `IDENTITY` vs `SERIAL`, `timestamptz` vs `timestamp`, `text` vs `varchar(n)`/`char(n)` (fetched via WebFetch, quoted)
- `crunchydata.com/blog/enums-vs-check-constraints-in-postgres` (via WebSearch) — ENUM vs CHECK tradeoffs
- `docs.docker.com/reference/compose-file/services`, `docs.docker.com/compose/how-tos/startup-order` (via WebSearch) — healthcheck/`depends_on`/`--wait` semantics

### Tertiary (LOW confidence)
- General Go community idiom for table-driven tests, rune-safe string truncation, and stdlib-only subcommand dispatch — well-established patterns without one single canonical doc page; flagged as such inline rather than over-cited
- Cyrillic transliteration table (Assumptions Log A2) — common convention, not verified against a single authoritative standard this session

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — pgx/goose versions verified against the Go module proxy and cross-checked with official docs via Context7; both already locked by MVP.md
- Architecture: HIGH — project structure fixed by MVP.md §3.2; the one genuine open technical question (embed.FS placement) is resolved against the Go language spec itself
- Pitfalls: HIGH — all six pitfalls are either verified against official documentation (embed, ALTER TYPE) or are well-known, easily-demonstrated Go behaviors (byte vs rune, comma decimals matched against Phase 1's actual source data)

**Research date:** 2026-09-29
**Valid until:** 2026-10-29 (30 days — stable stack: Go 1.27.1, PostgreSQL 17, and pinned library versions are not expected to require re-verification before Phase 2 executes)
