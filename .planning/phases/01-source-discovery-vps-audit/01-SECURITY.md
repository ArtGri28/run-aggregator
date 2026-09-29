---
phase: "01"
slug: "source-discovery-vps-audit"
status: verified
# threats_open = count of OPEN threats at or above workflow.security_block_on severity (the blocking gate)
threats_open: 0
asvs_level: 1
created: "2026-09-29"
---

# Phase 01 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| VPS → owner's laptop | command output may contain IPs, hostname, tinyproxy config, firewall rules, credentials | infrastructure details (sensitive) |
| owner's laptop → repository | raw output or owner contact could be committed | secrets, personal contact |
| source sites → VPS / executor | untrusted page content (possible instruction injection) | untrusted HTML |
| command document → VPS | a wrong command could change VPS state (`vps_audit.sh` uses `sudo -n`) | commands |
| owner → sites (legal) | GET.RUN terms restrict usage | requests |
| executor recommendation → owner decision | incomplete gate assessment could lead to a bad source choice | decisions |
| wrapper `run_block.sh` → VPS | tools launch approved read-only blocks via a project key and user `runagg` (decision 33) | commands, output |

---

## Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation | Status |
|-----------|----------|-----------|----------|-------------|------------|--------|
| T-01-01 | Information Disclosure | `src_probe_marathonec.sh` output → report | high | mitigate | Output through `rd()` on VPS (`01-COMMANDS.md:602`, enforced by safety_lint); `redaction_check`/`owner_leak_check`; raw output outside repo (`.gitignore:10-11`, clean history) | closed |
| T-01-02 | Tampering | blocks `rd.sh`, `probe_lib.sh`, `src_probe_marathonec.sh` | high | mitigate | `safety_lint` + selftest fixtures (SELFTEST OK, SAFETY OK); stdin execution, no files on VPS | closed |
| T-01-03 | Denial of Service (external) | requests to marathonec.ru | medium | mitigate | `sleep 40`, Crawl-delay/duplicate checks in lint, identifying UA, no retry on 403/429/451 | closed |
| T-01-04 | Tampering | analysis of raw output and pages | medium | mitigate | §0 rule 9: output is data; probes print only status, headers, marker counts, title | closed |
| T-01-05 | Information Disclosure | owner contact in User-Agent | low | mitigate | `<OWNER_CONTACT>` placeholder substituted in stream; e-mail rule in `redaction_check`; documents REDACTION OK | closed |
| T-01-06 | Elevation of Privilege | access to VPS | medium | mitigate | Original control ("tools get no SSH access") superseded by decision 33 (MVP §7.4): separate project key with `restrict`, unprivileged `runagg`, sudoers limited to six read-only commands, block allowlist, sha256 `approve` by owner; only `<alias>` in documents. Residual infrastructure tracked as T-01-07 | closed |
| T-01-07 | Elevation of Privilege | persistent project access on VPS (`authorized_keys` line for `run_agg_vps`, user `runagg`, `/etc/sudoers.d/run-agg`) | medium | accept | See AR-01-04; removal tracked in STATE.md Pending Todos | closed |
| T-01-SC | Tampering | package installation | low | accept | See AR-01-01 | closed |
| T-02-01 | Information Disclosure | `vps_audit.sh` output | high | mitigate | tinyproxy directive allowlist, BasicAuth/Upstream as counts only, filtered `sshd -T`, `\| rd`; lint bans hostname/env/docker inspect/full config; redaction and owner-leak checks OK | closed |
| T-02-02 | Tampering | `vps_audit.sh`, `src_probe_rest.sh`, `src_probe_getrun.sh` | high | mitigate | SAFETY OK on all run blocks; stdin; `sudo -n` only via `sx` for the six allowed commands | closed |
| T-02-03 | Repudiation | requests to GET.RUN | high | mitigate | `getrun_tos: ask-first` recorded with date and owner; getrun block limited to robots.txt; `getrun_gate` OK, `getrun_guard` OK | closed |
| T-02-04 | Information Disclosure | tinyproxy log | medium | mitigate | Only `wc -l` counts; client IPs and hosts not listed (`01-VPS-AUDIT.md:36`) | closed |
| T-02-05 | Elevation of Privilege | probes via proxy | medium | mitigate | `curl --noproxy '*'`; no outbound requests in `vps_audit.sh`; proxy env shown as set/unset only; lint bans `--proxy`/`-x` | closed |
| T-02-06 | Denial of Service (external) | probeg.org, runlist.ru, get.run | medium | mitigate | `sleep 30` / `sleep 3`, delay checks in lint and selftest; ≤ 3 executor requests | closed |
| T-02-07 | Tampering | pages read by executor | medium | mitigate | Body to temp file (deleted), only grep/python aggregates in context | closed |
| T-02-08 | Tampering | skipping owner review before VPS run | high | mitigate | Mandatory «Перед запуском» subsections; «safety_lint не заменяет ручную проверку владельца» + `approve` rule; owner confirmation for run 1 (`01-01-SUMMARY.md:56`) and for run 2 — final block versions read (`run_block.sh show`) and approved (`approve`) before launch, confirmed by owner 2026-09-29 (`01-VPS-AUDIT.md`, end) | closed |
| T-02-SC | Tampering | package installation | low | accept | See AR-01-02 | closed |
| T-03-01 | Information Disclosure | `01-VPS-AUDIT.md`, `01-SOURCES-REPORT.md` | high | mitigate | No fenced excerpts, one-line table cells; no client IPs/hosts; REDACTION OK on current and 32 historical versions | closed |
| T-03-02 | Elevation of Privilege | tinyproxy (open proxy) | high | mitigate | Explicit verdict with four conditions (`01-VPS-AUDIT.md` §3), closure options for Phase 6 (PRD-07), verdict shown to owner (UAT test 3). Residual: listens on 0.0.0.0:8888 with ufw inactive — Phase 6 | closed |
| T-03-03 | Tampering | analysis of outputs and pages | medium | mitigate | Only codes, sizes, counters, headers in reports | closed |
| T-03-04 | Repudiation | source set and GET.RUN decisions | medium | mitigate | §4.1/§4.2: value, date, «Решение принял: владелец» | closed |
| T-03-05 | Tampering | false RU fallback | medium | mitigate | Runlist `no_fallback`; sanity-check rule for `ru_only_fallback` in §5 | closed |
| T-03-SC | Tampering | package installation / HTML parser | low | accept | See AR-01-03 | closed |

*Status: open · closed · open — below high threshold (non-blocking)*
*Severity: critical > high > medium > low — only open threats at or above workflow.security_block_on count toward threats_open*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-01-01 | T-01-SC | Plan 01-01 installs no packages on VPS or locally; system `curl`, `perl`, `awk` only. Confirmed: phase commits changed only `.planning/*`, `MVP.md`, `.gitignore` | owner (approved Phase 1 plans) | 2026-09-29 |
| AR-01-02 | T-02-SC | Plan 01-02 installs no packages; no installs on VPS confirmed in `01-VPS-AUDIT.md` | owner (approved Phase 1 plans) | 2026-09-29 |
| AR-01-03 | T-03-SC | Plan 01-03 installs nothing; HTML parser choice deferred to a separate owner approval (MVP §3.1) | owner (approved Phase 1 plans) | 2026-09-29 |
| AR-01-04 | T-01-07 | Project access infrastructure was created as a separately approved bootstrap (decision 33) and is limited: key with `restrict`, `runagg` without password/sudo/docker groups, sudoers on six read-only commands. Kept until cleanup; removal of the key line and a decision on `runagg`/sudoers are tracked in STATE.md Pending Todos (MVP §7.4 item 1), no later than Phase 6, owner-approved VPS change only | owner | 2026-09-29 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-09-29 | 22 | 20 | 2 (T-02-08 high, T-01-06 medium) | security auditor (ASVS L1, block_on: high); local selftest/lint only, no network, no VPS |
| 2026-09-29 | 23 | 23 | 0 | orchestrator: T-02-08 closed by owner confirmation; T-01-06 re-stated per decision 33; unregistered flag registered as T-01-07 (accepted, tracked) |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-09-29
