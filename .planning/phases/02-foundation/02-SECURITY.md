---
phase: "02"
slug: "foundation"
status: verified
# threats_open = count of OPEN threats at or above workflow.security_block_on severity (the blocking gate)
threats_open: 0
asvs_level: 1
created: "2026-09-29"
---

# Phase 02 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| process environment → binary | `DATABASE_URL` carries the DB secret; may leak into logs and errors | DB credentials (secret) |
| working copy → repository | `.env`, credentials in compose or `.env.example` could be committed | secrets |
| local machine → test PostgreSQL | test DB port on the developer host | disposable test data |
| future source data (Phase 3) → store / normalize | untrusted strings reach SQL parameters and normalization | untrusted text |
| Go module proxy → go.sum | dependency supply chain | third-party code |

---

## Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation | Status |
|-----------|----------|-----------|----------|-------------|------------|--------|
| T-02-01 | Information Disclosure | `store.Open`, `main.go` (logs, errors) | high | mitigate | Fixed error text on DSN parse/connect failure (`internal/store/store.go:29-36`); DSN only via `os.LookupEnv`, never logged; 20 runtime cases with a password marker — marker never printed | closed |
| T-02-02 | Information Disclosure | `.env`, `.env.example`, `docker-compose.test.yml` | high | mitigate | `.gitignore:2-4`; no `.env` on disk; `.env.example` has empty `DATABASE_URL`; test DB uses trust auth; credential-URL scan clean | closed |
| T-02-03 | Tampering | SQL in `internal/store` | high | mitigate | All queries are constants with `$N` parameters; no string formatting in store; no SQL outside `internal/store`; CHECK constraints as second line (`TestEventConstraints`) | closed |
| T-02-04 | Information Disclosure | test PostgreSQL port | medium | mitigate | `127.0.0.1:5433` only, tmpfs, "tests only" header, no app services (`docker-compose.test.yml`) | closed |
| T-02-05 | Tampering | catalog integrity | medium | mitigate | Schema CHECK constraints (description ≤ 1000, country_code, city_key, type/status/state/match_state); `TestEventConstraints`, `TestSetMatch` | closed |
| T-02-06 | Denial of Service | `healthcheck` | low | mitigate | 5 s timeout over connect + `SELECT 1` (`main.go:79`); blackhole hosts exit rc=1 after ~5.1 s | closed |
| T-02-07 | Elevation of Privilege | `migrate` and app share one DB user | low | accept | See Accepted Risks Log AR-02-01 | closed |
| T-02-08 | Tampering | `normalize.Country` | medium | mitigate | Explicit non-RU country never in scope; RU fallback only for `RUOnlyFallback` (`place.go:163-172`); `TestCountry` + 200k-input probe | closed |
| T-02-09 | Tampering | `normalize.Description` | medium | mitigate | Rune-based truncation to 1000 (`normalize.go:67-83`); `TestDescription`; DB CHECK; 200k-input probe | closed |
| T-02-10 | Tampering | `normalize.CityKey` | medium | mitigate | Output limited to `[a-z0-9-]`, ≤ 64 bytes (`place.go:79-100`); `TestCityKey`; 200k-input probe satisfies DB CHECK | closed |
| T-02-11 | Denial of Service | normalize regexes on long input | low | accept | See Accepted Risks Log AR-02-02 | closed |
| T-02-12 | Information Disclosure | normalize | low | accept | See Accepted Risks Log AR-02-03 | closed |
| T-02-SC | Tampering | `go get` pgx/goose | medium | mitigate | Exact versions pinned (`go.mod:5-8`), two direct deps; `go mod verify` OK; go.sum hashes match sum.golang.org | closed |

*Status: open · closed · open — below high threshold (non-blocking)*
*Severity: critical > high > medium > low — only open threats at or above workflow.security_block_on count toward threats_open*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-02-01 | T-02-07 | Phase 2 uses only a local disposable test DB; separating migration and application DB roles is production hardening in Phase 6 (`02-01-PLAN.md` threat model) | owner (approved Phase 2 plans) | 2026-09-29 |
| AR-02-02 | T-02-11 | Only stdlib `regexp` (RE2, linear time) is used; input size is bounded by the 10 MB fetch limit introduced in Phase 3 (`02-02-PLAN.md` threat model) | owner (approved Phase 2 plans) | 2026-09-29 |
| AR-02-03 | T-02-12 | `normalize` is pure: no network, files, OS, database or logging imports, so it cannot disclose data (`02-02-PLAN.md` threat model) | owner (approved Phase 2 plans) | 2026-09-29 |

*Accepted risks do not resurface in future audit runs.*

### Known residual items (not open threats; tracked in 02-REVIEW.md, deferred to Phase 3+)

- `CityKey` can produce `--` (e.g. `moskva--siti`, LO-04): passes the DB CHECK but not the stricter doc-comment shape; Phase 4 callback encoding must not rely on it.
- Only `Migrate` wraps the underlying pgconn error; the auth-failure path could not be exercised against the trust-auth test DB.
- Short `Description` with invalid UTF-8 or NUL is returned unchanged and rejected by PostgreSQL on save (IN-04).
- `source_events.state` CHECK has no test.
- Tests TRUNCATE whatever database `TEST_DATABASE_URL` points to; no `_test` name guard (LO-11).

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-09-29 | 13 | 13 | 0 | security auditor (ASVS L1, block_on: high) at HEAD 2733004 |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-09-29
