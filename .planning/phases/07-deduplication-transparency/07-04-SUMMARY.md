---
phase: 07-deduplication-transparency
plan: 04
subsystem: store
tags: [go, postgres, migration, unmerge, journal]
requires: [07-03]
provides:
  - "миграция 00005: event_merges, event_merge_records, event_merge_chain — журнал слияний"
  - "store.UndoMerge (по журналу; без журнала — только класс A), ErrNotUndoable, inventoryQuery"
  - "подкоманда aggregator unmerge [-apply -by <tg_id>] <id>"
affects: [07-05]
plan_head_before: 75cfcfa
actuals:
  tasks: 4
  commits: 0
tech-stack:
  added: []
  patterns:
    - "журнал пишется в транзакции MergeEvents; отмена — одна транзакция под LockDedup; apply=false — только проверки"
key-files:
  created:
    - migrations/00005_merge_log.sql
    - internal/store/unmerge.go
    - internal/store/unmerge_test.go
    - .planning/phases/07-deduplication-transparency/07-UNMERGE.md
  modified:
    - internal/store/dedup.go
    - cmd/aggregator/main.go
    - cmd/aggregator/main_test.go
key-decisions:
  - "D-14 = b+c (решение владельца 2026-10-03): служебная отмена в Go + журнал слияний"
  - "Отмена отклоняет пару (dedup_candidates rejected) — проход её больше не предложит"
  - "Цепочка отменяется с последнего слияния; иначе отказ без изменений"
requirements-completed: []
completed: 2026-10-03
duration: около 2 часов
status: complete
---

# Phase 7 Plan 04: отмена слияния — инвентаризация, журнал, служебная отмена

**Статус: complete (локально).** Инвентаризация обратимости, решение владельца D-14 = b+c, журнал слияний и отмена — реализованы и проверены только на тестовой базе. Production-база не читалась, инвентаризация на production не запускалась, реальных отмен нет, миграция `00005` на production не применялась, deploy нет, `DEDUP_AUTO_MERGE` не менялся. Коммит делает владелец.

DDP-14 выполнен локально (статус в REQUIREMENTS.md — в 07-05).

## Инвентаризация (задача 1)

`07-UNMERGE.md`: слияние не хранит исходную принадлежность записей и цепочку; связь восстанавливается по кандидату `/dupes` (первая запись проигравшего), `created_at` события = `first_seen_at` записи (одна транзакция прогона) и полям записи. Классы: A — точно, B — частично (поглощения, неоднозначность), C — нет связи. На тестовой базе: `INVENTORY A=2 B=1 C=1`, снимок базы до и после запроса равен.

Production (только локальный лог `p6-deploy-verify.log`, 2026-10-01): 86 слияний, 86 подтверждённых кандидатов, автослияние выключено. **Распределение 86 слияний по классам неизвестно** — ручной характер слияний не делает их классом A; для распределения нужен отдельный разрешённый read-only запрос.

## Решение владельца (задача 2)

Ответ: D-14 = b+c, с границами: только локальная реализация и тесты; без чтения production, без реальных отмен, без миграции на production, без deploy, без изменения `DEDUP_AUTO_MERGE`, без commit/push без согласования; «не следует считать все 86 слияний классом A только потому, что они ручные».

## Реализация (задача 3)

- `migrations/00005_merge_log.sql`: `event_merges` (winner, loser, match_state, decided_by, merged_at, undone_at, undone_by), `event_merge_records` (source_event_id, prev_match_state), `event_merge_chain` (event_id); FK `ON DELETE RESTRICT`; Down удаляет три таблицы.
- `internal/store/dedup.go` `MergeEvents`: перенос возвращает прежний `match_state`, перевешивание цепочки — id событий; журнал пишется в той же транзакции.
- `internal/store/unmerge.go`: `UndoMerge` — по журналу (последнее действующее слияние проигравшего; отказ, если более позднее действующее слияние перенесло те же записи или события цепочки, или они уже не у текущего корня) либо без журнала только класс A (`inventoryQuery`); возврат записей и цепочки, снятие `merged_into_id`, пересборка полей обоих событий через `event.Fold`, `is_listed`, отклонение пары; `apply=false` — только проверки.
- `cmd/aggregator`: подкоманда `unmerge` (без `-apply` — проверка, с `-apply -by <tg_id>` — отмена), строка `UNMERGE …` без названий; `unmergeArgs` с тестом.

## Проверки

| Проверка | Результат |
|---|---|
| verify задач плана | `INVENTORY OK`, `D14 DECIDED`, `UNMERGE OK`, `PLAN 07-04 OK` |
| `TestMergeJournal` | записи с прежним `match_state` и цепочка в журнале |
| `TestUnmergeRoundTrip` | по журналу — каталог равен исходному (в т. ч. дистанции корня); проверка без применения ничего не меняет; цепочка — LIFO, ранняя отмена до поздней — отказ; без журнала класс A — точно |
| `TestUnmergeRefuses` | B, C без журнала, не слитое, отсутствующее — отказ, данные не меняются |
| `TestUnmergeAtomic` | сбой посередине — полный откат, журнал не отмечен |
| `TestMigration00005`, `TestMigration00004` | Down/Up |
| `TestUnmergeArgs` | разбор аргументов подкоманды |
| Полный подробный прогон | 469 PASS, 0 SKIP, 0 FAIL; `gofmt`, `go vet` чисто |
| Мутации (копия вне репозитория) | без прежнего `match_state`; без проверки позднего слияния; цепочка не возвращается; без журнала любой класс; корень не пересобирается — каждый раз падают профильные тесты |

## Deviations from Plan

1. Список файлов задачи 3 уточнён после решения D-14 (план это предусматривал): миграция `00005`, `internal/store/{dedup,unmerge}.go`, `cmd/aggregator/main{,_test}.go`; `files_modified` плана обновлён.
2. Отмена отклоняет пару (`rejected`) — иначе проход сразу предложил бы её снова; описано в 07-UNMERGE.md.
3. Первый вариант кругового теста не ловил мутацию «корень не пересобирается» (у `sampleEvent` уже была дистанция 5000); проверочная дистанция заменена на 7777 — мутация ловится.
4. 07-02 и 07-03 на момент 07-04 не закоммичены; файлы 07-04 с ними не пересекаются, кроме `.planning/ROADMAP.md`, `.planning/STATE.md`, `internal/store/dedup.go` (07-03 — L5, 07-04 — журнал в `MergeEvents`).
5. Коммиты делает владелец; исполнитель их не выполнял.
