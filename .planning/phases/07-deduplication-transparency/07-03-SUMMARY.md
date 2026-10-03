---
phase: 07-deduplication-transparency
plan: 03
subsystem: dedup
tags: [go, dedup, review, digest, runner]
requires: [07-02]
provides:
  - "store.DigestCandidates; MarkCandidatesNotified(ctx, ids) — сводка отмечает только посчитанных (L2)"
  - "Runner.PostApplyTimeout и поле лога post_apply_failed_total (L3)"
  - "MergeEvents не понижает match_state manual до auto (L5)"
  - "регрессионные тесты L1 и воспроизведение L4"
affects: [07-04]
plan_head_before: 75cfcfa
actuals:
  tasks: 4
  commits: 0
tech-stack:
  added: []
  patterns:
    - "воспроизведение тестом до исправления; тесты компилируются на прежнем API"
key-files:
  created: []
  modified:
    - internal/store/dedup.go
    - internal/store/dedup_test.go
    - internal/dedup/digest.go
    - internal/dedup/digest_test.go
    - internal/ingest/runner.go
    - internal/ingest/runner_test.go
    - internal/ingest/sources/probeg/parse_test.go
    - internal/bot/dupes_test.go
key-decisions:
  - "L1 — решение владельца l1-not-reproduced: сценарий ревью не воспроизводится, код не меняется, тест остаётся регрессионным"
  - "L4 — решение владельца defer-l4: ключ серии ПроБЕГа не меняется, риск отложен"
  - "Счётчик ошибок прохода называется post_apply_failed_total: Runner — общий код ingest"
requirements-completed: []
completed: 2026-10-03
duration: около 1,5 часа
status: complete
---

# Phase 7 Plan 03: замечания L1–L5 ревью Phase 5

**Статус: complete (локально).** Все пять замечаний воспроизведены тестами (или записано, почему не воспроизводятся); L2, L3, L5 исправлены с тестами; по L1 и L4 — решения владельца. Миграций нет; `deploy/`, `docker-compose.yml`, `DEDUP_AUTO_MERGE` не менялись. Коммит делает владелец.

DDP-15 выполнен локально (статус в REQUIREMENTS.md — в 07-05).

## Воспроизведение (до исправлений)

Тесты написаны с правильным ожиданием и запущены на коде HEAD `75cfcfa` (+ незакоммиченные правки 07-02, не затрагивающие эти места):

```
--- PASS: TestConfirmShownRoots                         (L1 не воспроизвёлся, см. ниже)
digest_test.go: после сводки pending 2, fresh 0; want 2, 1 — новый кандидат не входил в сводку
--- FAIL: TestDigestMarksOnlyCounted
runner_test.go: тайм-аут PostApply = 9h59m59.97s, want свой (не больше 10 мин), не FinalizeTimeout
runner_test.go: в логе нет счётчика ошибок прохода
--- FAIL: TestPostApplyOwnTimeout
L4 REPRO same_key=true cities=Москва,Пенза kept_park_a=1 collapsed=1
--- PASS: TestSeriesGroupKeyPlace                       (печать факта)
dedup_test.go: запись ручного слияния после автоматического = 1:auto, want 1:manual
--- FAIL: TestMergeKeepsManual
L5 STATUS winner=cancelled loser=scheduled result=cancelled
L5 STATUS winner=scheduled loser=cancelled result=scheduled
```

## Итог по замечаниям

| № | Воспроизведение | Результат |
|---|---|---|
| L1 | `TestConfirmShownRoots` (бот): корень стороны меняется только `MergeEvents`, а он удаляет pending-кандидатов проигравшего — «Да, слить» отвечает «уже обработан», слияния нет (`root_change=already_processed`); сторона осталась корнем, но поглотила третье событие — подтверждение сливает группу (`absorbed_side=merged_with_absorbed`) | Не воспроизведено. Решение владельца `l1-not-reproduced`: код не меняется, тест остаётся регрессионным |
| L2 | `TestDigestMarksOnlyCounted`: кандидат, созданный во время отправки сводки, отмечался вошедшим | Исправлено: `store.DigestCandidates` (число pending и id новых одним запросом), `MarkCandidatesNotified(ctx, ids)` отмечает только их; тесты `TestDigestMarksOnlyCounted`, `TestCandidateDigest`, `TestDigestTick` |
| L3 | `TestPostApplyOwnTimeout`: проход получал остаток контекста финализации; ошибки не считались | Исправлено: `Runner.PostApplyTimeout` (0 → 5 мин), контекст без отмены вместе с прогоном; `post-apply failed` с полем `post_apply_failed_total`; поведение «каталог прогона записан, статус не меняется» сохранено (`TestPostApply`) |
| L4 | `TestSeriesGroupKeyPlace`: одноимённые серии в разных городах — один `GroupKey`, `Prepare` оставляет одну (Пенза пропадает). На фикстуре: 943 записи с ключом, 191 ключ, ключей с несколькими местами — 0; исправление изменило бы все 191 ключ (разовый всплеск changed после выката) | L4 decision: решение владельца `defer-l4` — исправление отложено, тест-воспроизведение остаётся |
| L5 | `TestMergeKeepsManual`: автоматическое слияние понижало `manual` до `auto` | Исправлено: `CASE WHEN match_state IN ('out_of_scope', 'manual') THEN match_state ELSE $3 END`. Статус: при слиянии берётся статус победителя, статус проигравшего теряется (печать выше); источники статус не выставляют (D-15 Phase 5) — логика статуса не менялась |

## Решения владельца

Ответ дословно: «l1-not-reproduced, defer-l4».

## Проверки

- После исправлений: тесты L1–L5 и прежние `TestPostApply`, `TestCandidateDigest`, `TestDigestTick` — PASS.
- Подробный полный прогон `go test -p 1 -count=1 -v ./...` на локальной PostgreSQL 17: 462 PASS, 0 SKIP, 0 FAIL; `gofmt` пуст, `go vet ./...` чисто.
- Миграции, `deploy/`, `docker-compose.yml`, `go.mod`, `go.sum`, `internal/dedup/config.go` не менялись.

## Deviations from Plan

1. **L1 не воспроизвёлся** — по плану стоп и вопрос владельцу; исправление L1 не выполнялось (решение `l1-not-reproduced`). Файлы `internal/bot/dupes.go` и `internal/bot/callback.go` из списка плана не менялись.
2. **Счётчик ошибок прохода** назван `post_apply_failed_total`, а не `dedup_pass_failed_total`: `Runner` в пакете ingest не знает про дедупликацию.
3. **L3** исправлен в `internal/ingest/runner.go`; `internal/dedup/pass.go` и `pass_test.go` в 07-03 не менялись.
4. **Статус при слиянии (L5)**: только воспроизведение и запись; изменение не требовалось планом без подтверждённой ошибки в данных.
5. 07-02 на момент 07-03 не был закоммичен; изменения планов в разных файлах, кроме `.planning/ROADMAP.md` и `.planning/STATE.md`.
6. Коммиты делает владелец; исполнитель их не выполнял.
