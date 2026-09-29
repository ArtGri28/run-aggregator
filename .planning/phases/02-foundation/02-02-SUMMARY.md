---
phase: 02-foundation
plan: 02
subsystem: normalize
tags: [go, normalize, stdlib, table-driven-tests]

requires:
  - "02-01: internal/event (event.Type constants), schema CHECK constraints"
provides:
  - "internal/normalize: City, Region, SplitPlace, CityKey, Timezone, CountryCode, CountryPolicy (NoFallback, RUOnlyFallback), Country"
  - "internal/normalize: Distances, TitleNorm, MaxDescriptionRunes, Description, Type, Date, Time"
affects: [phase-3-ingest, phase-4-bot, phase-5-match]

actuals:
  tokens: 5600
  tasks: 2
  commits: 0
  plan_head_before: 0ab9a7bbc5556857697a710fff8e95e303c8a234

tech-stack:
  added: []
  patterns:
    - "Pure stdlib package: regexp (RE2), strings, unicode, strconv, time; only project import is aggregator/internal/event"
    - "Dictionaries as Go map literals (city synonyms, zones, countries, types)"
    - "One distance regexp shared by Distances and TitleNorm, with an explicit Unicode letter-boundary check after the unit"

key-files:
  created:
    - internal/normalize/place.go
    - internal/normalize/place_test.go
    - internal/normalize/distance.go
    - internal/normalize/distance_test.go
    - internal/normalize/normalize.go
    - internal/normalize/normalize_test.go
  modified: []

key-decisions:
  - "TitleNorm repeats its step until a fixpoint: dropping a word can make a new distance mention adjacent ('10 - км забег' -> '10 км' -> ''), a single pass would not be idempotent"
  - "Distances drops values above math.MaxInt32: event_distances.distance_m is INTEGER, an absurd '99999999999 км' would fail the insert"
  - "Kilometre values are parsed as '<number>e3' (exact decimal shift) instead of float*1000"

requirements-completed: [FND-04, FND-05]

coverage:
  - id: D1
    description: "CityKey: СПб/Питер/Санкт-Петербург -> saint-petersburg, Москва -> moscow, unknown city transliterated; every result matches ^[a-z0-9]+(-[a-z0-9]+)*$ and <= 64 bytes"
    requirement: "FND-04"
    verification:
      - kind: unit
        ref: "internal/normalize/place_test.go TestCityKey (PLACE OK)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Country: four MVP §6 branches; RUOnlyFallback documented as preliminary until ING-11"
    requirement: "FND-04"
    verification:
      - kind: unit
        ref: "TestCountryCode, TestCountry; PLACE PURE OK grep for 'предварительн' and 'ING-11'"
        status: pass
    human_judgment: false
  - id: D3
    description: "Distances: '21,1 км' -> 21100, 'полумарафон' -> 21098, '42.2 km' -> 42200; '1 час', 'марафонец' -> none"
    requirement: "FND-04"
    verification:
      - kind: unit
        ref: "internal/normalize/distance_test.go TestDistances (NORMALIZE OK)"
        status: pass
    human_judgment: false
  - id: D4
    description: "Description: <= 1000 runes on a word boundary, valid UTF-8, prefix of input, tested on Cyrillic text"
    requirement: "FND-04"
    verification:
      - kind: unit
        ref: "internal/normalize/normalize_test.go TestDescription"
        status: pass
    human_judgment: false
  - id: D5
    description: "TitleNorm (idempotent), Type, Date (dd.mm year rollover, 31.02 rejected), Time"
    requirement: "FND-04"
    verification:
      - kind: unit
        ref: "TestTitleNorm, TestType, TestDate, TestTime"
        status: pass
    human_judgment: false
  - id: D6
    description: "normalize unit tests run in go test ./... with and without TEST_DATABASE_URL; package imports no net/database/os/third-party"
    requirement: "FND-05"
    verification:
      - kind: unit
        ref: "REPO OK (env -u TEST_DATABASE_URL go test ./...), full run with TEST_DATABASE_URL"
        status: pass
    human_judgment: false

duration: ~25min
completed: 2026-09-29
status: complete
---

# Phase 2 Plan 2: normalize Summary

**Pure-stdlib `internal/normalize`: city/region/city_key (Moscow + Saint Petersburg synonyms, GOST-B-style transliteration otherwise, 64-byte ASCII slug), IANA zone, country_code with the four MVP §6 branches and the preliminary Марафонец RU fallback, distances in meters (decimal comma, «полумарафон»/«марафон»), idempotent title_norm, rune-safe 1000-character description, type, date and time — 13 table-driven tests, all green. Code is NOT committed (owner commits manually).**

## Performance

- **Duration:** ~25 min
- **Completed:** 2026-09-29
- **Tasks:** 2/2
- **Files created:** 6

## Accomplishments

- `place.go`: `City` (drops parentheses and «г.»/«г »/«город » prefixes, keeps «Гатчина»), `Region`, `SplitPlace` (last comma, ПроБЕГ «Область, Город»), `CityKey` (synonym map with exactly two canonical keys `moscow` and `saint-petersburg`; transliteration fallback; trim/cap 64 bytes), `Timezone` (city_key, then region as city), `CountryCode`, `CountryPolicy` (`NoFallback`, `RUOnlyFallback` with the preliminary-status doc comment referencing ING-11), `Country`.
- `distance.go`: `distanceRe` + `distanceMatches` (no letter after the unit, Unicode-aware: «10 мин» is not 10 m), `Distances` (sorted unique meters, nil if none), `removeDistances` reused by `TitleNorm`.
- `normalize.go`: `TitleNorm`, `MaxDescriptionRunes`, `Description`, `Type` (returns `event.Type`), `Date`, `Time`.

## Task Commits

None — commits are made by the owner. Suggested split:

1. `feat(02-02): add place and country normalization` — `internal/normalize/place.go`, `internal/normalize/place_test.go`
2. `feat(02-02): add distance, title, description, type, date and time normalization` — `internal/normalize/distance.go`, `distance_test.go`, `normalize.go`, `normalize_test.go`
3. `docs(02-02): complete normalize plan` — this SUMMARY, `.planning/STATE.md`, `.planning/ROADMAP.md`, `.planning/REQUIREMENTS.md`

## TDD Notes

- Both tasks: tests written first; RED was a compile failure (`undefined: City`, `undefined: Distances`, …), not a behavioural failure — there were no stubs. After implementation every test passed on the first run; no test passed before its implementation existed.
- Load-bearing cases checked: «10 - км забег» fails the idempotency assertion without the fixpoint loop in `TitleNorm`. The `e3` parsing of kilometres is not caught by the current table (`21.0975*1000` happens to round to 21098 as well); it is kept as the exact form, not proven necessary by a test.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] TitleNorm idempotency with separated distance mentions**
- **Found during:** Task 2
- **Issue:** a single pass leaves «10 км» after «10 - км забег» (the «-» blocks the first match, dropping «забег» joins the words), so `TitleNorm(TitleNorm(x)) != TitleNorm(x)`.
- **Fix:** `TitleNorm` repeats the step until the output stops changing (terminates: after the first pass each step only removes text). Test case added.
- **Files:** `internal/normalize/normalize.go`, `normalize_test.go`

**2. [Rule 2 - Missing critical] Upper bound for distances**
- **Found during:** Task 2
- **Issue:** `event_distances.distance_m` is `INTEGER`; the plan only dropped `<= 0`.
- **Fix:** values above `math.MaxInt32` are dropped; test case «99999999999 км» → none.
- **Files:** `internal/normalize/distance.go`, `distance_test.go`

Extra test cases beyond `<behavior>` (no behaviour change): «Г. Москва», «Щёлково» in `TestCity`; «Московская область, Химки, 10 км» and the MaxInt32 case in `TestDistances`; «5.1» in `TestDate`; double space in «гонка  с препятствиями» in `TestType`.

## Known Limitations (by plan design)

- All-caps Latin titles: a word that is a valid roman numeral is dropped as an edition number («MIX RUN» → «run»). The plan's rule («целиком из заглавных римских цифр») is followed literally; only mixed case «Mix» is protected.
- Named distance and numeric distance in one string both count («полумарафон 21,1 км» → [21098 21100]); merge tolerance (MVP §4.3) absorbs it.
- City dictionary is only Moscow and Saint Petersburg (D-03, A3); Phase 3 extends it from real data.

## Verification

- `PLACE OK`, `PLACE PURE OK`, `NORMALIZE OK`, `REPO OK` — all four plan `<verify>` commands pass.
- `go vet ./...`, `gofmt -l .` (empty), `go build ./...`, `git diff --check` — clean.
- `go test ./...` without `TEST_DATABASE_URL`: 13 PASS (normalize), 5 SKIP (store integration).
- With `TEST_DATABASE_URL` (PostgreSQL 17, `aggregator-test`): 18 PASS, 0 SKIP.
- No `internal/{ingest,match,bot,alert,worker,config}`; `git tag -l` empty.

## Next Phase Readiness

- Phase 3 calls `SplitPlace`/`CityKey`/`Country`/`Distances`/… over `payload` and saves through `store`. `inScope == false` from `Country` → `store.SetMatch(…, nil, event.MatchOutOfScope)`.
- The test database container is still running; stop it with `docker compose -f docker-compose.test.yml down` when the phase is closed.

---
*Phase: 02-foundation*
*Completed: 2026-09-29*

## Self-Check: PASSED

All 6 created files verified on disk. No commit hashes to verify (0 commits — owner commits manually).
