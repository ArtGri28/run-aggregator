---
phase: 07-deduplication-transparency
verified: 2026-10-03
status: human_needed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
gaps: []
human_verification:
  - test: "После RESTORE GATE Phase 6 и выката Phase 7 (по отдельному разрешению) выполнить agg verify"
    expected: "Строки dedup_would_auto=pending:<n> и dedup_pass_last=would_auto:…|hidden:…|shown:…|decided:… (после первого прохода worker), dedup_auto_merge_bot=false, dedup_auto_merge_worker=false, events_merged не вырос сам по себе"
    why_human: "Phase 7 на production не выкачена; пакет agg на VPS устарел (SHA256SUMS 4c2f486f…f183 локально), нужен ship.sh по разрешению владельца"
  - test: "От имени администратора открыть /dupes на production после выката"
    expected: "У пар с would_auto строка «Автослияние слило бы эту пару (сейчас выключено)»; слияний без подтверждения нет"
    why_human: "Экран проверен только автотестами с фейковым Bot API"
  - test: "Read-only инвентаризация 86 прошлых production-слияний запросом из 07-UNMERGE.md (отдельным разрешением)"
    expected: "Распределение по классам A/B/C известно; ручное слияние не считается классом A"
    why_human: "Production-база не читалась; без этого неизвестно, какие из прошлых слияний отменяемы"
  - test: "Владелец принимает результат L1–L5: L1 — l1-not-reproduced, L4 — defer-l4"
    expected: "Явное принятие при закрытии фазы; L4 остаётся в блокерах STATE до отдельного плана"
    why_human: "Решения владельца записаны в 07-03-SUMMARY.md; закрытие фазы — только командой владельца"
---

# Phase 7: Deduplication Transparency — Verification Report

**Phase Goal:** Владелец видит, какие пары слило бы автослияние, и может отменить ошибочное слияние; автослияние остаётся выключенным.
**Verified:** 2026-10-03
**Status:** human_needed
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
