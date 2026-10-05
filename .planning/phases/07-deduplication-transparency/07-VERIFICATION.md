---
phase: 07-deduplication-transparency
verified: 2026-10-05
status: passed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
gaps: []
re_verification: true
human_verification: []
---

# Phase 7: Deduplication Transparency — Verification Report

**Phase Goal:** Владелец видит, какие пары слило бы автослияние, и может отменить ошибочное слияние; автослияние остаётся выключенным.
**Verified:** 2026-10-03
**Status:** passed (повторная проверка 2026-10-05 — раздел «Повторная проверка 2026-10-05» в конце; текст ниже — отчёт прежней проверки, сохранён как история)
**HEAD при проверке:** `b165c10` (рабочее дерево чистое, совпадает с `origin/main`)

## Итог

Блокеров нет. Все пять критериев ROADMAP выполнены **локально** (код + автотесты на тестовой базе PostgreSQL 17). Статус `human_needed`: Phase 7 не выкачена на production (ждёт RESTORE GATE Phase 6 и разрешения владельца), поэтому наблюдений в production нет. Фаза **не закрыта**; `phase.complete` — только по команде владельца.

Проверки этого прогона: `gofmt -l .` — пусто; `go vet ./...` — чисто; `go test -p 1 -count=1 -v ./...` с `TEST_DATABASE_URL` — 470 PASS, 0 SKIP, 0 FAIL; `TestCalibration`: `CALIBRATION pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0 high=0.70 low=0.30 link_bonus=0.10`. `deploy/deploy-apply-test.sh` — `DEPLOY APPLY OK`, `deploy/backup-test.sh` — `BACKUP TESTS OK` (07-05, тот же код).

## Observable Truths (ROADMAP Success Criteria)

| # | Критерий | Статус | Доказательства |
|---|---|---|---|
| 1 | При `DEDUP_AUTO_MERGE=false` фактических автослияний 0; расчёт потенциальных не меняет каталог | VERIFIED (локально) | `TestPassWouldAutoOff`, `TestPassOffCatalogUnchanged` (снимок каталога до/после прохода), `TestAutoMergeCallSites` (go/ast: `MergeEvents` с `auto` только за `c.AutoMerge && WouldAuto`); `decide.go`: `AutoMerge` только при `c.AutoMerge && r.WouldAuto`; `docker-compose.yml`: `DEDUP_AUTO_MERGE: ${DEDUP_AUTO_MERGE:-}` (пусто → false) |
| 2 | Потенциальные автослияния видны в `/dupes`, логе прохода и `agg verify` | VERIFIED (локально) | лог: счётчики `would_auto`, `_shown`, `_hidden`, `_decided` (`TestPassWouldAutoHidden`); `/dupes`: `TestDupesWouldAuto`; `agg verify`: `dedup_would_auto`, `dedup_pass_last` (`TestVerifyWouldAuto` + раздел verify в `deploy-apply-test.sh`, мутации ловятся) |
| 3 | Калибровка падает на неразмеченной AUTO-паре; третий источник и BY размечены или отсутствие пересечений задокументировано | VERIFIED (локально) | `TestCalibrationUnlabeledAuto`; одна функция пар для прохода и калибровки (`TestWindowPairs`, M2); `internal/dedup/testdata/README.md` раздел «Третий источник и Беларусь» — пересечений нет (max_sim 0,13), подтверждено владельцем (07-02) |
| 4 | Процедура отмены слияния описана и проверена на тестовой базе | VERIFIED (локально) | `07-UNMERGE.md`, `deploy/RUNBOOK.md` «Отмена слияния событий»; журнал `00005`, `UndoMerge`, `aggregator unmerge`; `TestUnmergeRoundTrip`, `TestUnmergeRefuses`, `TestUnmergeAtomic`, `TestMergeJournal`, `TestUnmergeInventory`, `TestMigration00005`, `TestUnmergeArgs` |
| 5 | По L1–L5 есть результат; риск L1 не принят без владельца | VERIFIED (локально) | L1 — не воспроизведён (`TestConfirmShownRoots`), решение владельца `l1-not-reproduced`; L2 — исправлен (`TestDigestMarksOnlyCounted`, `TestCandidateDigest`); L3 — исправлен (`TestPostApplyOwnTimeout`, `post_apply_failed_total`); L4 — воспроизведён (`TestSeriesGroupKeyPlace`), решение владельца `defer-l4`; L5 — исправлен (`TestMergeKeepsManual`) |

## Requirements

| Требование | Статус | Основание |
|---|---|---|
| DDP-11 | SATISFIED (локально) | критерий 1, 07-01 |
| DDP-12 | SATISFIED (локально) | критерий 2, 07-01, 07-05 |
| DDP-13 | SATISFIED (локально) | критерий 3, 07-02 |
| DDP-14 | SATISFIED (локально) | критерий 4, 07-04, 07-05 |
| DDP-15 | SATISFIED (локально) | критерий 5, 07-03 |

## Ограничения базы доказательств

- Всё проверено только локально: production-база не читалась, VPS не затрагивался, миграция `00005` на production не применялась, `DEDUP_AUTO_MERGE` не менялся.
- Пакет `agg` изменён (07-05): локальный SHA256SUMS `4c2f486f…f183`, на VPS — пакет `2db41ff`; однострочники root-консоли из текущего репозитория дадут `STOP: HASH MISMATCH` до повторного `ship.sh`.
- 86 прошлых production-слияний журнала не имеют, их классы обратимости неизвестны.
- L4 отложен: исправление меняет ключи серий ПроБЕГа (разовый всплеск changed после выката).
- Калибровка — на закоммиченных снимках источников 2026-09-29…2026-10-01, не на production-данных.

## Anti-Patterns

Не найдено: в изменённых файлах Phase 7 нет TODO/заглушек; `gofmt`, `go vet` чисто.

## Повторная проверка 2026-10-05

**Status:** passed. **Score:** 5/5.

Проверки этого прогона (HEAD `2d2ebba`; код совпадает с `f1aa1c6` — после него менялись только документы): `gofmt -l .` — пусто; `go vet ./...` — чисто; `go test -p 1 -count=1 -v ./...` с `TEST_DATABASE_URL` — 530 PASS, 0 FAIL, 0 SKIP, все 14 пакетов `ok`. Production — только чтением, по разрешениям владельца: `agg verify` 2026-10-05 08:38 МСК (`run-agg-phase4/p14-verify.log`): `DEPLOY VERIFY OK`, образ `aggregator:636deea312f7`, `events_merged=96`, `dedup_candidates=pending:0|confirmed:96|rejected:8|fresh:0`, `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`, `dedup_would_auto=pending:0`.

Ответ владельца дословно (2026-10-05) на вопросы 1–5 и два пункта без наблюдения: «1–5 да, оба пункта снимаю, закрывай фазы».

Phase 7 выкачена на production 2026-10-04 (образ `fbdca0f5eecb`, миграция `00005` применена; далее `11d6d4bd8ee0`, `5d81e5a2ba00`, `f1aa1c6dfc2d`, `636deea312f7` — см. `08-08-SUMMARY.md`). Ключевые тесты на текущем коде: `TestMigration00005`, `TestUnmergeInventory`, `TestUnmergeRoundTrip`, `TestUnmergeRefuses`, `TestUnmergeAtomic`, `TestCalibration` — PASS.

| Пункт прежней проверки | Итог | Основание |
|---|---|---|
| `agg verify` после выката | VERIFIED с оговоркой | `dedup_would_auto=pending:0`, автослияние выключено у бота и воркера, `events_merged` вырос с 89 до 96 только ручными подтверждениями (проход 2026-10-04: `pairs 7, merged 0, would_auto 4`). Оговорка: строка `dedup_pass_last` во всех проверках 2026-10-04…05 — `unknown`; ожидаемые счётчики прохода в `agg verify` не наблюдались (проход 2026-10-04 шёл в боте при принятии базы Tristyle), причина не разбиралась |
| Строка «Автослияние слило бы эту пару (сейчас выключено)» в `/dupes` | VERIFIED | вопрос 3 — «да»: у 4 из 7 пар Tristyle (`would_auto_shown: 4` в логе бота) |
| Инвентаризация прошлых production-слияний по классам A/B/C | снят владельцем | production-база не читалась, распределение по классам обратимости неизвестно. Владелец: «оба пункта снимаю» |
| Принятие итога L1–L5 | VERIFIED решением владельца | вопрос 5 — «да»: L1 — не воспроизведён, L4 — отложен и остаётся в блокерах STATE |

Не проверено и остаётся известным ограничением: классы обратимости прошлых ручных слияний (на 2026-10-05 их 96) неизвестны; `dedup_pass_last` в `agg verify` не показал счётчики прохода.
