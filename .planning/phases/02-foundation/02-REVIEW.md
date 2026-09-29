---
phase: 02-foundation
reviewed: 2026-09-29T12:40:00Z
depth: deep
files_reviewed: 17
files_reviewed_list:
  - cmd/aggregator/main.go
  - migrations/00001_init_schema.sql
  - migrations/embed.go
  - internal/event/event.go
  - internal/store/store.go
  - internal/store/events.go
  - internal/store/store_test.go
  - internal/normalize/normalize.go
  - internal/normalize/normalize_test.go
  - internal/normalize/place.go
  - internal/normalize/place_test.go
  - internal/normalize/distance.go
  - internal/normalize/distance_test.go
  - docker-compose.test.yml
  - .env.example
  - go.mod
  - go.sum
findings:
  critical: 0
  high: 1
  medium: 5
  low: 11
  info: 5
  warning: 16
  total: 22
status: partially_fixed
---

# Phase 2: Code Review Report

**Reviewed:** 2026-09-29T12:40:00Z
**Depth:** deep (cross-file: normalize -> event -> store -> schema)
**Files Reviewed:** 17
**Status:** issues_found

## Summary

Scope: `git diff --name-only b82e945 HEAD -- ':!.planning'` (commits 31152a0..8db6ff4).
Severity scale: critical / high / medium / low / info. `warning` in the frontmatter = medium + low.

Every finding marked "confirmed" was reproduced with a throwaway test against the code
(normalize: pure functions; store: the `docker-compose.test.yml` database via
`TEST_DATABASE_URL`). The scratch test files were deleted afterwards and the working tree
is unchanged.

Checked and clean:
- **DSN/password leakage.** A binary built with `DATABASE_URL=postgres://u:SECRETPW@127.0.0.1:1/db`
  was run as `migrate`, `healthcheck` and `worker`. None of them printed the password.
  `migrate` wraps the pgconn `ConnectError`, which shows only `user=... database=...` and the host.
  A DSN that fails to parse returns the fixed message.
- **SQL injection.** Every query uses `$N` placeholders, and no SQL is built from strings.
- **UTF-8 in `Description`.** It cuts on runes, and `char_length` in PostgreSQL counts the same
  units. `CityKey` truncates only an ASCII string, so byte slicing there is safe.
- **`TitleNorm` / `Description` idempotency.** 40 s of `go test -fuzz` found no counterexample.
- **Overflow.** `Distances` caps values at `MaxInt32`.

The main problems are in `normalize.Distances`. It misreads standard Russian number formatting
and stores wrong values without any warning. In `store.SaveEvent`, change detection breaks
(`updated_at` advances on every save) unless the caller passes values that are already in the
exact form the database reads back.

## High

### HI-01: `Distances` misparses Russian number formatting: thousands separators and NBSP give wrong or missing distances (confirmed)

**File:** `internal/normalize/distance.go:16` (also 43-56)
**Issue:** `distanceRe` is `(\d+(?:[.,]\d+)?)\s*(км|km|м|m)`. Two things go wrong:
1. The space used as a thousands separator is not part of the number. MVP.md §4.3 itself writes
   "42 195 м". Results:
   - `Distances("42 195 м")` returns `[195]`: the marathon is stored as 195 m.
   - `Distances("10 000 м")` and `Distances("1 000 м")` return `[]`: the last group parses as `000`, which becomes 0 and is dropped.

   Track races ("стадион": "5 000 м", "10 000 м") lose their distance entirely, and a road marathon becomes a 195 m event in distance filters.
2. In RE2, `\s` matches ASCII only. HTML sources decode `&nbsp;` to U+00A0, and Russian typography
   puts an NBSP between a number and its unit. Results: `Distances("5 км")` → `[]`,
   `Distances("21,1 км")` → `[]`, `Distances("42 195 м")` → `[]`.
   `TitleNorm` handles NBSP only by accident: its fixpoint loop rejoins the tokens with ASCII spaces. `Distances` has no such loop.

None of this produces an error. Wrong values go into `event_distances`, and the ultra/marathon filters then give wrong results.
**Fix:** Turn all Unicode spaces into ASCII spaces first. Then accept 3-digit groups and remove the separators before parsing:
```go
var distanceRe = regexp.MustCompile(`(?i)(\d{1,3}(?: \d{3})+|\d+)((?:[.,]\d+)?)\s*(км|km|м|m)`)

func Distances(s string) []int {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) { return ' ' }
		return r
	}, s)
	// num := strings.ReplaceAll(s[m[2]:m[3]], " ", "") + strings.Replace(s[m[4]:m[5]], ",", ".", 1)
	...
}
```
Apply the same space mapping in `removeDistances`/`TitleNorm`. Add test cases for "42 195 м", "10 000 м" and "5 км".

## Medium

### ME-01: `SaveEvent` reports a change on every save unless the caller passes already-canonical values; `updated_at` advances each run (confirmed)

**File:** `internal/store/events.go:208-210` (compare), `:246-251` (args)
**Issue:** The old row is scanned back from the database and compared to the caller's struct using `Equal` or exact string comparison:
- `StartDate`/`EndDate`: DATE comes back as midnight UTC. pgx encodes a `time.Time` using its wall-clock date in the value's own location. So a caller that builds
  `time.Date(2026,10,17,0,0,0,0, Europe/Moscow)` gets the correct date stored, but on every
  later `SaveEvent` the comparison is MSK midnight vs UTC midnight. Result: `changed=true` and a bumped `updated_at`
  on every ingest run. This was reproduced: repeated saves of the same MSK date returned `true, true`. A `time.Time` with a
  non-zero hour behaves the same way (for example, one parsed from an ISO datetime).
- `StartTime`: the value is read back with `to_char(start_time,'HH24:MI')`. `"9:05"` is stored as 09:05 and
  read back as `"09:05"`, so it never compares equal (reproduced: `changed=true` on every save). `"10:00:30"` is
  stored with its seconds, read back without them, and also never compares equal.

This defeats ING-05 ("updated_at only on a real change"), and it will break the Phase 3 `upserted` counts and any future
`event_changes`. `normalize.Date`/`normalize.Time` happen to return canonical forms, but `SaveEvent` does not
enforce them.
**Fix:** Canonicalize inside `SaveEvent` before comparing and writing. Alternatively, move the comparison into SQL:
```go
func dateOnly(t time.Time) time.Time { y, m, d := t.Date(); return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
// e.StartDate = dateOnly(e.StartDate); if e.EndDate != nil { v := dateOnly(*e.EndDate); e.EndDate = &v }
// reject StartTime unless it matches ^\d{2}:\d{2}$ (or normalize via normalize.Time)
```
The SQL alternative is `UPDATE events SET ... WHERE id=$1 AND (title, ..., start_time, ...) IS DISTINCT FROM ($2, ..., NULLIF($6,'')::time, ...) RETURNING updated_at`.

### ME-02: `Date` with a year-less "dd.mm" puts last month's events into next year at every month boundary

**File:** `internal/normalize/normalize.go:119-123`
**Issue:** The rule is "month < now.Month() → next year". Consequences:
- On 2026-10-01, ПроБЕГ still lists "30.09" (yesterday's race). The rule gives **2027-09-30**. The MVP §5 Validate window is `[today−1, today+3y]`, which accepts that date, so a finished race appears as an event one year ahead.
- On 1 January, "31.12" becomes the next 31 December.
- `now.Month()` is taken in `now`'s location, while the result is UTC. With `time.Now().UTC()`, the boundary falls
  3 hours off Moscow time, which makes the mistake more likely around midnight.

**Fix:** Choose the year relative to a grace window instead of comparing months:
```go
today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
t := time.Date(now.Year(), time.Month(m), d, 0, 0, 0, 0, time.UTC)
if t.Before(today.AddDate(0, -1, 0)) { y++ } // older than a month → next year
```
Re-run the existing Y/M/D round-trip check, which rejects 29.02. Add test cases for "30.09" on 01.10 and "31.12" on 01.01.

### ME-03: The word "марафон" in an event name adds a phantom 42 195 m distance (new aspect of the known double count) (confirmed)

**File:** `internal/normalize/distance.go:57-61`
**Issue:** The known issue is the double count `[21098 21100]`, which merge tolerance absorbs. The same code also fires when "марафон"
is part of the event's *name* rather than its distance:
- `Distances("Зелёный марафон 4,2 км")` returns `[4200 42195]`. "Зелёный марафон" is a real 4.2 km charity run.
- "Марафон «Белые ночи»: 10 км" returns `[10000 42195]`.

The gap here is 38 km, far beyond any merge tolerance. These events show up under marathon filters, and under ultra ("max distance") queries they count as marathon-length.
**Fix:** Use named distances only as a fallback when no numeric distance is found:
```go
if len(out) == 0 {
	for _, w := range strings.FieldsFunc(...) { if d, ok := namedDistances[w]; ok { out = append(out, d) } }
}
```
This also fixes the known `[21098 21100]` case.

### ME-04: `Country` treats an unrecognised explicit country as foreign, so Russian events go to `out_of_scope` without warning even under `RUOnlyFallback` (confirmed)

**File:** `internal/normalize/place.go:163-167`, dictionary `:123-129`
**Issue:** When `explicit` is non-empty but `CountryCode` does not recognise it, the result is `("", false)`. Confirmed with
`Country("Russian Federation", RUOnlyFallback)` → `"", false` and with `"Rossiya"` → `"", false`. The same
happens for "Россия (RU)" and similar spellings.

MVP §6 rule 3 applies only when the data *explicitly indicates another
country*. An unrecognised string means "cannot determine", which is rule 2 or rule 4 depending on the source's policy. As written, a source that changes its spelling of Russia
moves its whole feed to `out_of_scope`. The ingest-run `suspect` check (§5.4) looks at `parsed`/`rejected`, not
`out_of_scope`, so nothing flags it.
**Fix:**
- Add `"russian federation"` (and similar spellings) to the dictionary.
- Separate "known foreign" from "unknown":
  ```go
  if strings.TrimSpace(explicit) != "" {
  	if code = CountryCode(explicit); code != "" { return code, code == "RU" }
  	// unknown spelling: fall through to policy (or return a distinct "unknown" so the caller counts it as rejected)
  }
  ```

### ME-05: "K" distance notation ("5K", "10K") is not recognised, so distances are missing and `title_norm` differs between sources (confirmed)

**File:** `internal/normalize/distance.go:16`
**Issue:** "10K" is common in race names:
- `Distances("Забег 10K")` returns `[]`.
- `TitleNorm("Забег 10K")` returns `"10k"`, while `TitleNorm("Забег 10 км")` returns `""`.

So the same event under two spellings gets no distance, and its `title_norm` does not match. MVP §6 asks for "21,1 км", "42.2 km" "и подобного".
**Fix:** Accept `k`/`к` only when it is attached to the number, because a spaced "10 к старту" is a preposition. Keep the existing letter-after-unit guard:
`(?i)(\d+(?:[.,]\d+)?)(?:\s*(км|km|м|m)|([kк]))`. Treat group 3 as kilometres.

## Low

### LO-01: `UpsertSourceEvent` ignores a URL change when the payload is unchanged (confirmed)

**File:** `internal/store/events.go:69`
**Issue:** `url` is updated only when the payload hash differs, but `url` is not part of the hash. Reproduced: the first
upsert used `http://old` and the second used `https://new` with the same payload. The row kept `http://old` and returned `changed=false`. If a source moves from http to https or changes domain, the stored URL stays stale permanently.
**Fix:** Set `url = EXCLUDED.url` unconditionally, or include the URL in the hashed payload.

### LO-02: `UpsertSourceEvent` hashes raw bytes and races under concurrent writers

**File:** `internal/store/events.go:54-56`, `:59-75`
**Issue:**
- The hash is taken over the raw bytes, not over canonical JSON. `{"a":1}` followed by `{"a": 1}` returns `changed=true`
  (reproduced), even though jsonb stores both identically. If Phase 3 ever builds the payload with a different encoder or whitespace, every record will show up as "changed".
- `old` reads the statement snapshot, but `ON CONFLICT DO UPDATE` acts on the latest committed row version. With two
  concurrent writers for the same key, `changed` can be computed against a stale hash. Both writers can also report
  `changed=true` for a brand-new key.

**Fix:** Hash `payload::jsonb::text` in SQL, or canonicalize in Go. For the race, either document "one writer per source" as an
invariant or use `SELECT ... FOR UPDATE` in a transaction.

### LO-03: Schema constraints are missing relative to MVP §4 (confirmed)

**File:** `migrations/00001_init_schema.sql:28,34,41,62-63,14-18`
**Issue:** The following were all accepted by the database in the test:
- `end_date` earlier than `start_date`.
- `UPDATE events SET merged_into_id = id`, a self-merge. A self-loop or cycle would make "цепочки слияний схлопываются до корня" (§4.5) loop forever in Phase 5.
- `SetMatch(..., nil, auto)` and `SetMatch(..., &id, out_of_scope)`.
- `city_key = '--'`. The CHECK `^[a-z0-9-]+$` is weaker than the `CityKey` contract `^[a-z0-9]+(-[a-z0-9]+)*$`.
- Negative values in the `ingest_runs` counters.

**Fix:** Since 00001 is not yet deployed, amend it (or add 00002) with these checks:
- `CHECK (end_date IS NULL OR end_date >= start_date)`
- `CHECK (merged_into_id IS NULL OR merged_into_id <> id)`
- `CHECK (merged_into_id IS NULL OR NOT is_listed)`
- `CHECK ((match_state IN ('auto','manual')) = (event_id IS NOT NULL) OR match_state = 'candidate')`
- the tighter city_key regex
- `CHECK (fetched >= 0 AND ...)`

### LO-04: `CityKey` can produce "--", which violates its documented contract (confirmed)

**File:** `internal/normalize/place.go:84-95`
**Issue:** A word that transliterates to nothing leaves a double hyphen:
- `CityKey("Москва ъ Сити")` returns `"moskva--siti"`.
- `CityKey("Ab 東 Cd")` returns `"ab--cd"`.

The doc comment promises `^[a-z0-9]+(-[a-z0-9]+)*$`, and the DB CHECK (LO-03) does not catch it. A Phase 4 callback codec that relies on that shape would break.
**Fix:** Transliterate word by word, drop empty results, and join with `"-"`.

### LO-05: `TitleNorm` leaves year-suffix leftovers and collapses whole titles to "" or to a shared prefix (confirmed)

**File:** `internal/normalize/normalize.go:22-24`, `:48`
**Issue:**
- A year with a suffix is not removed: "Забег «Белые ночи» 2026г" → `"белые ночи 2026г"`, and "Забег 2026 г." → `"г"`. The same race gets a different `title_norm` than it does in a source that writes "2026".
- Stop-word-only titles collapse to `""`: "Марафон 2026" and "Полумарафон 2025" both give `""`.
- "Московский марафон" and "Московский полумарафон" both give `"московский"`.

If Phase 5 treats equal `title_norm` as strong evidence, empty or very short keys will cause false matches.
**Fix:**
- Extend `yearRe` to `^(19|20)\d\d(г|года?)?$` and drop a standalone `г`/`год` token.
- In Phase 5, never score an empty `title_norm` as a title match. Note this in the doc comment.

### LO-06: `Distances` misreads other uses of "м"/"m" as distances (confirmed)

**File:** `internal/normalize/distance.go:16`
**Issue:** "Набор высоты 1500 м" returns `[1500]` (elevation gain). "3M Half" returns `[3]`. "4х400 м" returns `[400]` (relay). The result
depends on which free-text fields Phase 3 passes to `Distances`. Descriptions of trail races routinely give elevation in metres.
**Fix:** Document that only title and distance fields may be passed, not description, and ignore matches preceded by "высот"/"D+"/"набор". At a minimum, add these as negative tests so Phase 3 knows the limitation.

### LO-07: `City` prefix stripping handles "г.о." incorrectly and misses settlement prefixes (confirmed)

**File:** `internal/normalize/place.go:14`, `:18-27`
**Issue:**
- "г.о. Химки" (городской округ) matches the "г." prefix and produces the displayed city `"о. Химки"` and the key `o-himki`.
- "п. Токсово", "пос.", "д.", "с." are not stripped, so the key is `p-toksovo` and the same village gets two keys across sources. Villages like this are common in trail races.

**Fix:** Check the longer prefixes first: `"г.о. "`, `"городской округ "`, `"пос. "`, `"п. "`, `"д. "`, `"с. "`. Require a space or the end of the token after the dot. Keep D-03's minimal dictionary for the synonyms.

### LO-08: `Healthcheck` hides the cause of the failure

**File:** `internal/store/store.go:50-51`; used by `cmd/aggregator/main.go:87,105`
**Issue:** Every failure is reported as `healthcheck: select 1 failed`. An operator cannot tell a refused connection from an auth failure, a timeout, or a missing database. Reproduced: `healthcheck` and `worker` both print only that message. `Migrate` does wrap the cause, and the run above showed that the pgconn error contains no password. So hiding the cause adds no safety and loses the diagnosis.
**Fix:** `return fmt.Errorf("healthcheck: %w", err)`. At minimum, include the `*pgconn.PgError` Code, whether it was `context.DeadlineExceeded`, or the `net.OpError` Op.

### LO-09: Signal handling cannot be interrupted a second time, and a SIGTERM during startup exits 1

**File:** `cmd/aggregator/main.go:27-28`, `:104-113`
**Issue:**
- `signal.NotifyContext` keeps catching SIGINT/SIGTERM until `stop()` runs, and `stop()` is only deferred. When Phase 3/4 add real loops, a hung shutdown cannot be interrupted with a second Ctrl-C.
- A SIGTERM that arrives during the startup healthcheck cancels `hcCtx`, and the process reports `command failed` with exit code 1 instead of 0.

**Fix:** Call `stop()` right after `<-ctx.Done()`, so the default signal handling is restored and a second signal kills the process. In `runProcess`, return `nil` when `ctx.Err() != nil` after a healthcheck failure.

### LO-10: `Migrate` takes no migration lock

**File:** `internal/store/store.go:63`
**Issue:** `goose.NewProvider` is called without `WithSessionLocker`. Two `migrate` runs at the same time can both try to apply 00001, and one fails partway through, for example a `compose run migrate` during a `compose up`, or a future bot/worker that migrates on start. Each goose migration runs in its own transaction, so no data is corrupted, but the run fails with an error that is hard to interpret.
**Fix:** Pass `goose.WithSessionLocker(lock.NewPostgresSessionLocker())` (`github.com/pressly/goose/v3/lock`, already a dependency through goose).

### LO-11: Integration tests TRUNCATE any database that `TEST_DATABASE_URL` points to

**File:** `internal/store/store_test.go:18-47`
**Issue:** The tests do not check which database they are connected to. Running `TEST_DATABASE_URL=$DATABASE_URL go test ./...` on the VPS would run `TRUNCATE ... RESTART IDENTITY CASCADE` on the production tables.
**Fix:** In `TestMain`, run `SELECT current_database()` and exit unless the name ends with `_test`.

## Info

### IN-01: A zero-value `Status`/`Type` fails the CHECK; the schema default `'scheduled'` can never apply

**File:** `internal/store/events.go:231,241,250`
`$13` is always passed explicitly, so `event.Event{}` with no `Status` fails with `events_status_check` (confirmed), and the column's `DEFAULT 'scheduled'` is never used.
**Fix:** Use `COALESCE(NULLIF($13,''),'scheduled')`, or validate in Go and return a clear error.

### IN-02: `Type` matches exact labels only

**File:** `internal/normalize/normalize.go:85-100`
"Шоссейный", "trail run" and "Кросс-кантри" all become `other`. This is expected until Phase 3 collects the real labels. Consider matching the first word or a prefix ("шоссе*", "трейл*", "кросс*").

### IN-03: `Time` rejects "HH:MM:SS"

**File:** `internal/normalize/normalize.go:134`
`Time("10:00:00")` returns `false`. JSON and ISO sources often include seconds. Allow an optional `(?::\d{2})?` and drop the seconds.

### IN-04: Invalid UTF-8 and NUL characters pass through `normalize` unchanged

**File:** `internal/normalize/normalize.go:67-72`, `place.go:18`
A short `Description` or `City` is returned as-is. Invalid UTF-8 (for example a windows-1251 page that was not decoded) or `\x00` then makes PostgreSQL reject the whole `SaveEvent`. jsonb also rejects `\u0000`, which fails `UpsertSourceEvent`, so the whole record is lost rather than one field.
**Fix:** Apply `strings.ToValidUTF8(s, "")` and strip `\x00` at the normalize boundary. Add a Phase 3 note on source charset decoding.

### IN-05: Redundant slice copy

**File:** `internal/store/events.go:267`
`append([]any{}, eventArgs(e)...)` copies a slice that `eventArgs` has just created. Pass `eventArgs(e)...` directly.

## Note on owner-known items

- **Roman-numeral stripping.** Besides the known "MIX", the same root cause also drops single uppercase Latin letters: "Run I Love" → `"run love"`, "Trail C 10 km" → `"trail"`. There is nothing else new here.
- **Named+numeric double count.** See ME-03. Here "марафон" is part of the event's *name*, which creates a 38 km phantom distance that merge tolerance cannot absorb. The fix proposed there also removes the known `[21098 21100]` case.

---

_Reviewed: 2026-09-29T12:40:00Z_
_Depth: deep_

## Fixes applied (2026-09-29)

| Finding | Fix | Regression tests |
|---------|-----|------------------|
| HI-01 | `distance.go`: Unicode spaces (NBSP, narrow NBSP) mapped to ASCII before matching; integer part accepts space-separated thousands (`\d{1,3}(?: \d{3})+`), separators removed before parsing. `removeDistances` (used by `TitleNorm`) uses the same mapping. | `TestDistances`: «42 195 м», «10 000 м», «1 000 м», NBSP and U+202F variants, «забег 2026 5 000 м», «5 км, 10 000 м» |
| ME-01 | `events.go`: `SaveEvent` canonicalizes input before insert/compare — dates become midnight UTC of their own calendar day, `StartTime` is parsed as `15:04` and written as `HH:MM`; anything else (e.g. `10:00:30`) is rejected with an error. | `TestSaveEventCanonicalRoundTrip` (MSK-midnight dates, `9:05`, two re-saves: `changed=false`, `updated_at` unchanged; `10:00:30` rejected) |
| ME-02 | `normalize.go`: year for `dd.mm` chosen so the date falls in `[today−1 month, today+11 months)`, today taken in `now`'s location. | `TestDate` (31.08/29.08/28.08 on 29.09), `TestDateAcrossBoundaries` (30.09 on 01.10, 31.12 on 01.01, 05.01 on 20.12, 00:30 MSK, 29.02) |
| ME-03 | `distance.go`: named distances («марафон», «полумарафон») only when no numeric distance was found. Side effect: «10 км и полумарафон» → [10000]. | `TestDistances`: «Зелёный марафон 4,2 км», «Марафон «Белые ночи»: 10 км», «полумарафон 21,1 км» |

All new cases were confirmed to fail on the pre-fix code (HEAD `8db6ff4`) and pass after the fix.

Deferred to Phase 3 by owner decision: ME-04 (unrecognised explicit country such as «Russian Federation»), ME-05 (`5K`/`10K`), all low and info findings.
