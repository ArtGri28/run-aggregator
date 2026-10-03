---
phase: 07-deduplication-transparency
plan: 01
subsystem: dedup
tags: [go, dedup, would_auto, dupes, invariant]
requires: []
provides:
  - "dedup.Reasons.WouldAuto (JSON would_auto, omitempty) — признак потенциального автослияния при любом DEDUP_AUTO_MERGE"
  - "счётчики прохода would_auto, would_auto_shown, would_auto_hidden, would_auto_decided (+ would_auto_merged, would_auto_deferred при включённом флаге)"
  - "строка «Автослияние слило бы эту пару (сейчас выключено)» в карточке /dupes"
affects: [07-02, 07-05]
plan_head_before: 1f8b6d1
actuals:
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "одна точка правды: вердикт AutoMerge = c.AutoMerge && WouldAuto"
    - "снимки таблиц через jsonb_agg(to_jsonb(...)) в тестах; статическая проверка вызовов через go/ast"
key-files:
  created:
    - internal/dedup/wouldauto_test.go
    - internal/dedup/invariant_test.go
    - internal/bot/wouldauto_test.go
  modified:
    - internal/dedup/decide.go
    - internal/dedup/decide_test.go
    - internal/dedup/pass.go
    - internal/dedup/pass_test.go
    - internal/dedup/calibration_test.go
    - internal/bot/dupes.go
key-decisions:
  - "D-05/D-06/D-07: признак считается в памяти прохода и пишется в существующий JSON reasons; схема БД не менялась"
  - "D-09: лимит MaxPerEvent сохранён; скрытые лимитом потенциальные AUTO-пары — would_auto_hidden"
  - "match_state = 'auto' не признак слияния (так ingest помечает привязку записи к своему событию) — критерий D-10 исправлен"
requirements-completed: []
completed: 2026-10-03
duration: около 1,5 часа
status: complete
---

# Phase 7 Plan 01: would_auto без миграции и ноль фактических слияний

**Статус: complete (локально).** Признак потенциального автослияния считается при выключенном `DEDUP_AUTO_MERGE`, виден в логе прохода и в `/dupes`; фактических слияний нет. Production, VPS, `deploy/`, `docker-compose.yml`, миграции и `DEDUP_AUTO_MERGE` не менялись. Коммит делает владелец.

DDP-11 выполнен локально; DDP-12 — в части лога и `/dupes` (`agg verify` — план 07-05). Статусы в REQUIREMENTS.md меняются в 07-05 (задача 3) по договорённости плана.

## Что сделано

- `internal/dedup/decide.go`: `Reasons.WouldAuto` (`json:"would_auto,omitempty"`) — NameSim ≥ High без бонуса ссылки, пересечение дат, равный непустой CityKey, UniquePartner — при любом флаге; `if c.AutoMerge && r.WouldAuto { return AutoMerge, r }`. Отдельной копии правил нет.
- `internal/dedup/pass.go`: счётчики `would_auto`, `would_auto_shown`, `would_auto_hidden`, `would_auto_decided` в строке лога `dedup pass` (при включённом флаге ещё `would_auto_merged`, `would_auto_deferred`); признак уходит в reasons кандидата тем же `UpsertCandidate`. Логика слияния и лимита не менялась.
- `internal/bot/dupes.go`: в `signals` — строка «Автослияние слило бы эту пару (сейчас выключено)» при `WouldAuto`; старые reasons без ключа — без строки, без ошибок.

## Семантика и подсчёт

- `would_auto` относится к последнему проходу; pending-кандидаты получают актуальный признак первым же проходом (reasons перезаписываются, остальные pending удаляются). Confirmed и rejected reasons не обновляются — в них ключа может не быть, это false.
- Подсчёт: `would_auto` — пары прохода с признаком (отклонённые по корням не оцениваются); `shown` — `UpsertCandidate` вернул id; `hidden` — лимит `MaxPerEvent`; `decided` — `UpsertCandidate` вернул 0; `merged`/`deferred` — только при флаге. Инвариант суммы проверяется в каждом тесте, читающем лог.

## Гарантия нуля слияний (D-10) — тесты

| Слой | Тест | Что доказывает |
|---|---|---|
| 1, 4 | `TestAutoMergeCallSites` (go/ast) | вызов `MergeEvents(…"auto"…)` в непроверочном коде один — `internal/dedup/pass.go` внутри `if c.AutoMerge`; `WouldAuto` не в условиях на пути к нему |
| 2 | `TestDecideWouldAuto`, `TestDecideOffNeverAuto` (1296 вариантов, 32 с признаком) | при выключенном флаге `AutoMerge` нет; reasons при обоих значениях флага равны; признак ⇔ AutoMerge при включённом |
| 2 | `TestCalibration` (81 пара фикстур) | то же на реальных фикстурах; `CALIBRATION pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0 high=0.70 low=0.30` — без изменений |
| 3 | `TestPassOffCatalogUnchanged` (PostgreSQL 17) | 3 пары с признаком, 1 кандидат без признака, 1 раздельная пара: снимки `events` (все колонки), `source_events` (event_id, match_state, fields, state), `event_distances` после двух проходов равны исходному; кандидаты после второго прохода равны первому; слитых событий 0; событий с записями двух источников 0; при включённом флаге тот же каталог даёт `merged=3`, `would_auto_merged=3` |
| 3 | `TestPassWouldAutoOff`, `TestPassWouldAutoHidden` | признак в reasons; лимит `MaxPerEvent=1` скрывает пару — `would_auto_hidden=1` |
| сквозной | `TestDupesWouldAuto` (бот, настоящий `Pass`) | `TRACER would_auto=1 merged=0 catalog=unchanged dupes_line=yes`; старый кандидат без ключа — без строки |

Мутационная проверка на копии кода (вне репозитория): Decide без флага; снятая ветка `if c.AutoMerge`; слияние по признаку при выключенном флаге; нет строки в `/dupes`; не считается `hidden`; признак без UniquePartner — каждый раз падают профильные тесты.

## Проверки

- verify задач плана: `TRACER OK`, `ZERO MERGE OK`, `PLAN 07-01 OK`.
- Подробный полный прогон `go test -p 1 -count=1 -v ./...` на локальной PostgreSQL 17 (`docker-compose.test.yml`): 455 PASS, 0 SKIP, 0 FAIL (verify задачи 3 без `-v` строки SKIP не видит — поэтому отдельный подробный прогон).
- `gofmt -l` пуст, `go vet ./...` чисто; `go.mod`, `go.sum`, `internal/dedup/config.go` не менялись; `deploy/`, `docker-compose.yml`, `migrations/` — без изменений.

## Deviations from Plan

1. **Критерий D-10 «строк `match_state = 'auto'` — 0» был неверным.** `match_state = 'auto'` ставит ingest при привязке записи к своему событию (`internal/store/events.go:527, 540`), это не признак слияния. Тест упал на исходном коде без всяких слияний. Проверка заменена на «ни у одного события нет записей двух источников»; неизменность `match_state` доказывает снимок `source_events`. Исправлены формулировки в 07-CONTEXT.md (D-10) и 07-01-PLAN.md.
2. **Файлы тестов.** Новые тесты прохода — в `internal/dedup/wouldauto_test.go` (а не в `pass_test.go`, где добавлен только помощник `addEventMod`); тест бота — в `internal/bot/wouldauto_test.go` (а не в `dupes_test.go`): сквозной tracer с настоящим `Pass` и датами от текущего дня (помощник `dupeEvent` даёт даты от фиксированного 2026-09-29).
3. **Строка TRACER** печатается тестом бота (сквозной путь Pass → reasons → `/dupes`), а не тестом пакета dedup.
4. **verify задачи 3** не видит `--- SKIP` без `-v`; добавлен подробный прогон (результат выше). Сам план не менялся в этой части.
5. Коммиты делает владелец; исполнитель их не выполнял.
