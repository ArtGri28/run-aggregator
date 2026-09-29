---
phase: 02-foundation
verified: 2026-09-29T09:59:53Z
status: human_needed
score: 5/5 roadmap success criteria verified (15/15 plan truths verified; 4/4 review fixes confirmed)
covered_files:
  - ".env.example"
  - ".planning/REQUIREMENTS.md"
  - ".planning/phases/02-foundation/02-01-PLAN.md"
  - ".planning/phases/02-foundation/02-01-SUMMARY.md"
  - ".planning/phases/02-foundation/02-02-PLAN.md"
  - ".planning/phases/02-foundation/02-02-SUMMARY.md"
  - ".planning/phases/02-foundation/02-REVIEW.md"
  - "cmd/aggregator/main.go"
  - "docker-compose.test.yml"
  - "go.mod"
  - "go.sum"
  - "internal/event/event.go"
  - "internal/normalize/distance.go"
  - "internal/normalize/distance_test.go"
  - "internal/normalize/normalize.go"
  - "internal/normalize/normalize_test.go"
  - "internal/normalize/place.go"
  - "internal/normalize/place_test.go"
  - "internal/store/events.go"
  - "internal/store/store.go"
  - "internal/store/store_test.go"
  - "migrations/00001_init_schema.sql"
  - "migrations/embed.go"
covered_digest: "v1:sha256:fc78e9eba5bebab32999d5b652b388cf23a2740240ab322516b5d918efdf0fca"
behavior_unverified: 0
overrides_applied: 0
re_verification:
  previous_status: human_needed
  previous_score: 5/5 roadmap criteria, 15/15 plan truths
  previous_verified_at: 2026-09-29T09:32:42Z (HEAD 8db6ff4)
  reason: "uncommitted fixes for code-review findings HI-01, ME-01, ME-02, ME-03 (02-REVIEW.md, section 'Fixes applied')"
  gaps_closed:
    - "HI-01: Distances handles space thousands separators and NBSP/U+202F"
    - "ME-01: SaveEvent canonicalizes dates and StartTime; re-save of equal non-canonical input reports changed=false"
    - "ME-02: year-less dd.mm no longer moves last month's race a year ahead"
    - "ME-03: a named distance inside an event name no longer adds a phantom 42195/21098"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "Judgment-tier prohibition 02-01: MUST NOT обращаться к сайтам-источникам (marathonec.ru, probeg.org) ни из кода, ни из тестов"
    expected: "Owner confirms. Non-authoritative verifier verdict: HOLDS — no net/http import, no http.Get, no source hostnames in any .go file (re-checked after the fixes); the strings \"marathonec\"/\"probeg\" appear only as sources.code values in store_test.go"
    why_human: "verification: judgment — prohibition sign-off is a human decision; grep evidence is supportive, not authoritative"
  - test: "Judgment-tier prohibition 02-02: MUST NOT расширять словарь городов общероссийским списком (D-03)"
    expected: "Owner confirms. Non-authoritative verifier verdict: HOLDS — place.go is unchanged since 8db6ff4; citySynonyms holds only Москва and Санкт-Петербург; everything else is transliterated"
    why_human: "verification: judgment — whether the dictionary matches 'cities of the two approved sources plus obvious synonyms' is a scope judgment"
---

# Phase 2: Foundation Verification Report

**Phase Goal:** Есть каркас проекта и схема данных, на которую можно сажать сбор данных и поиск
**Verified:** 2026-09-29T09:59:53Z (HEAD 8db6ff4 plus the uncommitted review fixes in `internal/normalize/{distance,normalize}.go`, `internal/store/events.go` and their tests)
**Status:** human_needed. All roadmap criteria, plan truths and the four review fixes are verified. Two judgment-tier prohibitions still need owner sign-off; the evidence says both hold.
**Re-verification:** Yes. It follows the fixes for code-review findings HI-01, ME-01, ME-02 and ME-03 (see `02-REVIEW.md`, "Fixes applied").

## Scope of the re-verification

The working-tree diff against 8db6ff4 touches only these files: `internal/normalize/distance.go`, `distance_test.go`, `normalize.go`, `normalize_test.go`, `internal/store/events.go`, `store_test.go`, and one-line doc edits in `02-01-SUMMARY.md` and `02-02-SUMMARY.md`. `go.mod`, `go.sum`, `cmd/`, `migrations/`, `internal/event/` and `internal/normalize/place.go` are byte-identical to 8db6ff4 (`git diff --quiet HEAD`). Everything else was re-run anyway: static checks, both test modes, the binary, and a migration from scratch.

## Review Fix Confirmation

Each input from the review was run against a scratch copy of HEAD 8db6ff4 and a scratch copy of the current tree, using the exported API only. The repository was not touched.

| Finding | Review input | HEAD 8db6ff4 | Current tree | Verdict |
|---------|--------------|--------------|--------------|---------|
| HI-01 | `Distances("42 195 м")` | `[195]` | `[42195]` | ✓ FIXED |
| HI-01 | `Distances("10 000 м")` / `("1 000 м")` | `[]` / `[]` | `[10000]` / `[1000]` | ✓ FIXED |
| HI-01 | `Distances("5 км")` / `("21,1 км")` / `("42 195 м")` | `[]` / `[]` / `[]` | `[5000]` / `[21100]` / `[42195]` | ✓ FIXED |
| HI-01 | `TitleNorm` with NBSP: `"Забег «Белые ночи» 21,1 км"` | `"белые ночи 21"` | `"белые ночи"` | ✓ FIXED (same `removeDistances` path) |
| ME-01 | `TestSaveEventCanonicalRoundTrip` (MSK-midnight dates, `"9:05"`, two re-saves) | FAIL: `re-save 0: changed = true` | PASS | ✓ FIXED |
| ME-02 | `Date("30.09", 2026-10-01 10:00 MSK)` | 2027-09-30 | 2026-09-30 | ✓ FIXED |
| ME-02 | `Date("31.12", 2027-01-01 10:00 MSK)` | 2027-12-31 | 2026-12-31 | ✓ FIXED |
| ME-02 | `Date("30.09", 2026-10-01 00:30 MSK)` (still 30.09 in UTC) | 2027-09-30 | 2026-09-30 | ✓ FIXED |
| ME-03 | `Distances("Зелёный марафон 4,2 км")` | `[4200 42195]` | `[4200]` | ✓ FIXED |
| ME-03 | `Distances("Марафон «Белые ночи»: 10 км")` | `[10000 42195]` | `[10000]` | ✓ FIXED |
| ME-03 | `Distances("полумарафон 21,1 км")` (the known double count) | `[21098 21100]` | `[21100]` | ✓ FIXED |

## Goal Achievement

### Roadmap Success Criteria

| # | Criterion | Status | Evidence (re-run on the current tree) |
|---|-----------|--------|---------------------------------------|
| 1 | Один бинарник собирается и запускает подкоманды `bot`, `worker`, `migrate`, `healthcheck` | ✓ VERIFIED | `go build ./...` OK. Binary built to scratch dir. `migrate` rc 0, `healthcheck` rc 0 on live DB, no args → rc 2. `cmd/aggregator/main.go` is unchanged since the initial run, which exercised `bot`/`worker` start and SIGTERM → rc 0 |
| 2 | Подкоманда `migrate` создаёт всю схему MVP в PostgreSQL 17 с нуля | ✓ VERIFIED | New empty database `reverify_fresh` on the test PostgreSQL 17: `migrate` rc 0, second run rc 0, goose_db_version = `0,1`. 9 tables (8 + goose_db_version), 7 non-constraint indexes (2 partial), 0 ENUM types. Scratch DB dropped afterwards |
| 3 | `normalize`: СПб/Питер/Санкт-Петербург → один `city_key`; Россия/Russia → `RU`; «21,1 км» и «полумарафон» → метры; описание ≤ 1000 символов (тестами) | ✓ VERIFIED | `TestCityKey`, `TestCountryCode`, `TestDistances`, `TestDescription` PASS. Criterion example still holds: `Distances("полумарафон")` = `[21098]` (also `"Полумарафон"` → `[21098]`, `"Марафон"` → `[42195]`), because named distances are used whenever no numeric distance is present. The `{"полумарафон", [21098]}` row of `TestDistances` is unchanged. `"21,1 км"` → `[21100]` |
| 4 | Интеграционный тест на реальной PostgreSQL проходит upsert `source_events` и `events` | ✓ VERIFIED | With `TEST_DATABASE_URL=postgres://aggregator@127.0.0.1:5433/aggregator_test`: TestEnsureSource, TestUpsertSourceEvent, TestSaveEvent, TestSetMatch, TestEventConstraints and the new TestSaveEventCanonicalRoundTrip all PASS |
| 5 | Секреты берутся из окружения и не попадают в репозиторий | ✓ VERIFIED | No change to `cmd/`, `.env.example`, `.gitignore` or `docker-compose.test.yml`. Bad-credential DSN `postgres://u:S3cretPW@…`: `healthcheck` rc 1 and `migrate` rc 1, and neither prints the password (0 occurrences) |

### Plan Truths (02-01, 02-02 must_haves)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | FND-01: one binary, dispatch by os.Args[1], usage + rc 2 on none/unknown | ✓ VERIFIED | main.go unchanged; no-args rc 2 re-checked |
| 2 | FND-02: 8 tables + goose, 7 indexes (2 partial), no ENUM, re-run migrate rc 0 no-op | ✓ VERIFIED | Fresh-DB run above |
| 3 | D-02: healthcheck SELECT 1, rc 0 live; rc 1 on failure; DSN never printed | ✓ VERIFIED | rc 0 live; rc 1 on closed port, password not echoed. store.go/main.go unchanged, so the initial run's blackhole and malformed-DSN checks still apply |
| 4 | bot / worker connect, slog line, SIGTERM → rc 0 | ✓ VERIFIED | Code unchanged since the initial behavioral run |
| 5 | UpsertSourceEvent idempotent on (source_id, external_id) | ✓ VERIFIED | TestUpsertSourceEvent PASS (function not touched by the fixes) |
| 6 | SaveEvent/GetEvent round-trip; no-op re-save changed=false, updated_at unchanged; real change → changed=true | ✓ VERIFIED | TestSaveEvent PASS and TestSaveEventCanonicalRoundTrip PASS. A scratch probe also confirmed that real changes are still detected (`09:05` → `9:06` changed=true, date +1 day changed=true) |
| 7 | CHECK constraints reject bad values | ✓ VERIFIED | TestEventConstraints, TestSetMatch PASS |
| 8 | FND-03: SQL only in internal/store, parameterized | ✓ VERIFIED | The fix adds no SQL; `canonicalize` is pure Go |
| 9 | FND-06: DATABASE_URL env-only, no credentials tracked | ✓ VERIFIED | Unchanged files; runtime leak check above |
| 10 | CityKey synonyms/transliteration + invariant | ✓ VERIFIED | TestCityKey PASS; place.go unchanged |
| 11 | CountryCode(Россия) = CountryCode(Russia) = RU | ✓ VERIFIED | TestCountryCode PASS |
| 12 | Country: four branches | ✓ VERIFIED | TestCountry PASS |
| 13 | Distances: 21,1 км → 21100, полумарафон → 21098, 42.2 km → 42200; «1 час», «марафонец» → none | ✓ VERIFIED | TestDistances PASS (35 cases, including the new HI-01/ME-03 rows). The one changed expectation, «10 км и полумарафон» → `[10000]`, is the documented ME-03 trade-off and does not contradict any plan truth |
| 14 | Description ≤ 1000 runes on word boundary | ✓ VERIFIED | TestDescription PASS |
| 15 | TitleNorm rules + idempotence; Date / Time / Timezone / Type; normalize is pure | ✓ VERIFIED | TestTitleNorm, TestDate, TestDateAcrossBoundaries, TestTime, TestTimezone, TestType PASS. A 25 s `go test -fuzz` of `TitleNorm(TitleNorm(x)) == TitleNorm(x)` plus `Distances` panic-freedom ran 423k execs with no failure. normalize still imports only stdlib + `internal/event` |

**Score:** 5/5 roadmap criteria, 15/15 plan truths verified (0 present-but-behavior-unverified)

### Required Artifacts

| Artifact | Status | Details |
|----------|--------|---------|
| `internal/normalize/distance.go` | ✓ VERIFIED | `asciiSpaces` is used by both `Distances` and `removeDistances`; the thousands-group regex is in place; the named-distance fallback runs only when `len(out) == 0` |
| `internal/normalize/normalize.go` | ✓ VERIFIED | `yearFor` is wired into `Date` for the dd.mm branch |
| `internal/store/events.go` | ✓ VERIFIED | `canonicalize` is called in `SaveEvent` before both the insert and update/compare paths |
| Tests (`distance_test.go`, `normalize_test.go`, `store_test.go`) | ✓ VERIFIED | New cases present and passing; the ME-01 test fails on HEAD code |
| All other phase artifacts | ✓ VERIFIED | Unchanged since the initial verification; re-checked by build, tests and fresh migrate |

### Key Link Verification

| From | To | Via | Status |
|------|----|-----|--------|
| `Distances` / `removeDistances` | `asciiSpaces` → `distanceMatches` → `distanceRe` | direct calls | ✓ WIRED |
| `TitleNorm` | `removeDistances` | `titleNormStep` | ✓ WIRED (NBSP titles now lose their distance) |
| `Date` | `yearFor` | dd.mm branch | ✓ WIRED |
| `SaveEvent` | `canonicalize` → `dateOnly` | before `eventArgs` and `canonicalFieldsEqual` | ✓ WIRED |
| All links from the initial verification | — | — | ✓ WIRED (files unchanged) |

### Data-Flow Trace (Level 4)

Not applicable, because this phase renders no dynamic output. The integration tests prove store round-trips against real PostgreSQL.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Static checks | `go vet ./...`, `gofmt -l .`, `go build ./...` | clean / empty / OK | ✓ PASS |
| Tests without DB | `go test -count=1 ./...` | normalize ok, store ok (integration tests skip) | ✓ PASS |
| Tests with DB | `TEST_DATABASE_URL=… go test -count=1 -v ./...` | normalize 14/14 PASS, store 6/6 PASS | ✓ PASS |
| Migrate from scratch + idempotent | `DATABASE_URL=…/reverify_fresh aggregator migrate` ×2 | rc 0, rc 0; 00001 applied once | ✓ PASS |
| Healthcheck | live / closed port with password in DSN | rc 0 / rc 1, password not printed | ✓ PASS |
| Migrate with bad DSN | closed port with password in DSN | rc 1, password not printed | ✓ PASS |
| No subcommand | `aggregator` | rc 2 | ✓ PASS |
| Fix reproduction | review inputs on HEAD copy vs current copy | see "Review Fix Confirmation" | ✓ PASS |
| SaveEvent valid inputs | scratch probe: `""`, `00:00`, `23:59`, `09:05`, `9:05`; dates at 23:30 UTC, 00:30 +14, 23:30 −10 | all saved; re-save changed=false; stored day = wall-clock day | ✓ PASS |
| SaveEvent rejects | `24:00`, `10:00:30`, `9.05`, `" 09:05"`, `09:5` | error `start time …: want HH:MM` | ✓ PASS (intended; `normalize.Time` never produces these) |

### Probe Execution

The phase declares no probes, and the repository has no `scripts/*/tests/probe-*.sh`. SKIPPED.

### Requirements Coverage

| Requirement | Source Plan | Status | Evidence |
|-------------|-------------|--------|----------|
| FND-01 | 02-01 | ✓ SATISFIED | Criterion 1 |
| FND-02 | 02-01 | ✓ SATISFIED | Criterion 2 |
| FND-03 | 02-01 | ✓ SATISFIED | SQL only in internal/store; the fixes add none |
| FND-04 | 02-02 | ✓ SATISFIED | Criterion 3; distances/dates are more correct after HI-01/ME-02/ME-03 |
| FND-05 | 02-01 | ✓ SATISFIED | Criterion 4 |
| FND-06 | 02-01 | ✓ SATISFIED | Criterion 5 |

No requirements are orphaned.

### Prohibitions

| Prohibition | Tier | Result |
|-------------|------|--------|
| No new direct deps | test | ✓ go.mod/go.sum unchanged |
| No SQL outside internal/store | test | ✓ |
| No network / DB in normalize and its tests | test | ✓ imports: stdlib + internal/event |
| DATABASE_URL never logged / in errors | test | ✓ re-checked at runtime |
| No access to source sites from code or tests | judgment | FLAGGED: unverified prohibition, human review recommended (verifier verdict: holds) |
| City dictionary not expanded to an all-Russia list (D-03) | judgment | FLAGGED: unverified prohibition, human review recommended (verifier verdict: holds; place.go unchanged) |

All other prohibitions from the initial verification concern files the fixes did not touch. They still hold.

### Anti-Patterns Found

The files changed by the fixes contain no TBD/FIXME/XXX/TODO/HACK markers.

The HI-01 and ME-03 fixes introduce some new behavior. None of it breaks a roadmap criterion or plan truth. All of it is recorded for Phase 3, where `Validate` and real source data are handled:

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| `internal/normalize/distance.go:17` | Space-separated number lists are read as one thousands-grouped number: `"100 200 400 м"` → `[100200400]` (≈100 000 km), `"60 100 200 м"` → `[60100200]`, `"эстафета 4 400 м"` → `[4400]`, `"5 100 м"` → `[5100]`. Before the fix these gave `[400]`, `[200]`, `[400]`, `[100]` (also wrong, but small). Values stay ≤ MaxInt32, so nothing rejects them | ⚠️ Warning | This ambiguity is inherent to Russian digit grouping. Separating list items with commas avoids it (`"5 км, 10 000 м"` → `[5000 10000]`). Phase 3 should add a plausibility cap in Validate (e.g. reject distances over a few hundred km) or test comma-less track lists from the real «стадион» data |
| `internal/normalize/distance.go:17` | The regex has no left boundary, so a thousands group can start inside a longer digit run: `"12345 678 м"` → `[345678]`; `TitleNorm("Старт 1 2026 000 м")` → `"старт 1 2"` | ℹ️ Info | Contrived inputs. TitleNorm stays idempotent (fuzzed) |
| `internal/normalize/distance.go:72` | ME-03 trade-off: when there is any numeric distance, a named one is dropped: `"10 км и полумарафон"` → `[10000]`, and likewise «марафон и 10 км» loses the marathon | ℹ️ Info | Documented in 02-REVIEW.md "Fixes applied". Multi-distance events should come from a distance field, not the title |
| `internal/normalize/normalize.go:138` | `today.AddDate(0, -1, 0)` normalizes at month ends. On 31.03 the lower bound becomes 03.03, so `"01.03"` / `"02.03"` (29–30 days ago) go to next year (2027-03-01), while `"03.03"` stays 2026 | ℹ️ Info | The window shrinks to about 28 days on some month-end days. The ME-02 review scenario itself (race from yesterday or last week) is fixed |
| `internal/store/events.go:257` | `canonicalize` rewrites the caller's `StartDate`/`EndDate` before it validates `StartTime`, so a rejected save still leaves the dates canonicalized in the caller's struct | ℹ️ Info | Harmless: the canonical form is an equal date |
| (carried over) `store_test.go` skip without DB; weak last_seen_at assertion; untested `source_events.state` CHECK; roman-numeral all-caps words | ℹ️ Info | Unchanged from the initial verification |

The initial report's Info item "«10 000 м» yields no distance" is resolved by HI-01. Its "stale Commits section in 02-01-SUMMARY" item is resolved by the SUMMARY edit.

### Human Verification Required

### 1. Judgment-tier prohibition: no access to source sites

**Test:** Confirm that no code or test in this phase contacts marathonec.ru or probeg.org.
**Expected:** The prohibition holds. Evidence: after the fixes, no `.go` file contains a `net/http` import, `http.Get`, or a `marathonec.ru`/`probeg.org` hostname. "marathonec"/"probeg" appear only as `sources.code` literals in `internal/store/store_test.go`.
**Why human:** The prohibition is declared `verification: judgment`, so sign-off belongs to the owner.

### 2. Judgment-tier prohibition: minimal city dictionary (D-03)

**Test:** Review `citySynonyms` and `cityZones` in `internal/normalize/place.go`.
**Expected:** The prohibition holds. The file is unchanged since 8db6ff4 and lists only Москва and Санкт-Петербург with synonyms.
**Why human:** The prohibition is declared `verification: judgment`. Whether the dictionary matches "cities of the two approved sources plus obvious synonyms" is a scope decision.

### Gaps Summary

There are no gaps. The four review fixes each resolve their finding: the review's failing inputs give wrong results on HEAD 8db6ff4 and correct results now, and the new ME-01 test fails on HEAD code and passes now. None of the five roadmap criteria regressed. Criterion 3's «полумарафон» → 21098 still holds because named distances are used when no numeric distance is present. SaveEvent accepts every form `normalize.Time`/`normalize.Date` produce, and still detects real changes.

The fixes do add one new risk: a comma-less, space-separated list of distances is now read as a single thousands-grouped number. This is a Warning for Phase 3 Validate, not a Phase 2 gap. The status is human_needed only because of the two judgment-tier prohibitions.

---

_Verified: 2026-09-29T09:59:53Z_
_Verifier: gsd-verifier_
