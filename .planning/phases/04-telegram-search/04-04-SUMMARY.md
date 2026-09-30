---
phase: 04-telegram-search
plan: 04
subsystem: alerts
tags: [alerts, postgres, migration, goose, pgx, admin]

requires:
  - phase: 04-telegram-search
    provides: "04-01: storetest.Open; Phase 3: ingest_runs, RunInterrupted, SourceHealth"
provides:
  - "Миграция 00003: таблица source_alerts (одна строка на источник в плохом состоянии)"
  - "store: AlertStates, SaveAlertState, ClearAlertState; LatestFinishedRuns, RecentRuns, RunSource"
  - "internal/alert: decide, message, Loop.Tick, Loop.Run (отправка через переданную функцию, без Telegram)"
affects: [04-05, 04-06]

actuals:
  tokens: 9000
  tasks: 2
  commits: 0

plan_head_before: e2fbb453fbdd323d4279a064cc51e8a55ae7fc92
commits: "uncommitted — owner commits manually"

tech-stack:
  added: []
  patterns:
    - "Решение по состоянию, а не по событию: decide(последний завершённый прогон, строка source_alerts, now)"
    - "Отправить, потом записать: source_alerts меняется только после успешной отправки хотя бы одному администратору"
    - "В лог при сбое отправки — код источника и тип ошибки (%T), без текста ошибки"

key-files:
  created:
    - migrations/00003_source_alerts.sql
    - internal/store/alerts.go
    - internal/store/alerts_test.go
    - internal/alert/alert.go
    - internal/alert/alert_test.go
  modified:
    - internal/store/runs.go
    - internal/store/store_test.go
    - internal/store/storetest/storetest.go

key-decisions:
  - "Действие «снять состояние» названо reset, а не clear: clear затенил бы встроенную функцию"
  - "Сообщение и сбой отправки: в лог идёт тип ошибки (%T), а не её текст, чтобы адрес запроса к Bot API не попал в логи"
  - "Если reason пуст, в сообщении «Причина: не указана»: строка есть всегда, разбор сообщения одинаков"

requirements-completed: [MON-05]

coverage:
  - id: D1
    description: "Миграция 00003: таблица, CHECK на state, внешние ключи, Down"
    requirement: MON-05
    verification:
      - kind: integration
        ref: "internal/store/alerts_test.go#TestMigration00003"
        status: pass
    human_judgment: false
  - id: D2
    description: "Последние завершённые прогоны источников, состояния алертов (upsert, чтение, снятие), история /runs, источник прогона"
    requirement: MON-05
    verification:
      - kind: integration
        ref: "internal/store/alerts_test.go#TestLatestFinishedRuns, TestAlertStateCRUD, TestRecentRuns, TestRunSource"
        status: pass
    human_judgment: false
  - id: D3
    description: "Чистое решение: один алерт на смену состояния, напоминание с 24 ч, ok снимает состояние, прерванный остановкой прогон молчит"
    requirement: MON-05
    verification:
      - kind: unit
        ref: "internal/alert/alert_test.go#TestDecide"
        status: pass
    human_judgment: false
  - id: D4
    description: "Текст алерта: код, номер прогона, причина, счётчики suspect, число сбоев failed, время МСК, префикс напоминания, кнопка принятия только у suspect со снимком"
    requirement: MON-05
    verification:
      - kind: unit
        ref: "internal/alert/alert_test.go#TestMessage"
        status: pass
    human_judgment: false
  - id: D5
    description: "Цикл: адресаты только из Admins, без повторов, напоминание через 24 ч, тишина при ok и при прерванном прогоне, состояние переживает тик, Telegram недоступен - повтор на следующем тике"
    requirement: MON-05
    verification:
      - kind: integration
        ref: "internal/alert/alert_test.go#TestTick, TestTickInterruptedFirst, TestTickSendFailure, TestNoAdmins, TestRunStops"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-09-30
status: complete
---

# Phase 4 Plan 04: Алерты администраторам о failed и suspect Summary

**Цикл раз в минуту читает последний завершённый прогон каждого источника и шлёт администраторам один алерт на смену состояния (failed/suspect) и напоминание раз в 24 часа; «уже оповещено» хранится в `source_alerts`, запись идёт только после успешной отправки, а прерванный остановкой процесса прогон молчит.**

## Performance

- **Duration:** около 25 мин
- **Completed:** 2026-09-30
- **Tasks:** 2 из 2
- **Files:** 5 новых, 3 изменённых

## Accomplishments

- Миграция `00003_source_alerts`: `source_alerts(source_id PK, state suspect|failed, run_id, notified_at, reminded_at)`, внешние ключи `ON DELETE RESTRICT`, Down = `DROP TABLE`. Аддитивная, откат образа безопасен.
- `store`: `LatestFinishedRuns` (`DISTINCT ON`, без running, счётчик `unrecognized_region` из metrics, признак снимка), `RecentRuns` (последние N на источник для `/runs`), `RunSource` (код источника по номеру прогона, `ErrNotFound`), `AlertStates`/`SaveAlertState`/`ClearAlertState` (времена задаёт вызывающий).
- `internal/alert`: `decide` (чистая), `message` (обычный текст без разметки, время в фиксированной зоне UTC+3), `Loop.Tick` и `Loop.Run`. Пакет не импортирует библиотеку Telegram и не содержит SQL; отправка - поле `Send`, которое подключит 04-05.
- Данные для кнопки (D-15): `acceptRunID` отдаётся только у suspect со снимком; в напоминании кнопка сохраняется.
- Первый запуск на пустой БД: первый прогон источника - suspect, значит алерт уйдёт сразу после запуска бота (шаг D-07).

## Task Commits

Коммитов нет: по правилам владельца коммиты делает он вручную. Файлы по задачам:

**Задача 1 (миграция и store)** - предлагаемый тип коммита `feat`:
- `migrations/00003_source_alerts.sql`
- `internal/store/alerts.go`
- `internal/store/runs.go` (`FinishedRun`, `LatestFinishedRuns`, `RecentRun`, `RecentRuns`, `RunSource`)
- `internal/store/alerts_test.go` (`TestMigration00003`, `TestLatestFinishedRuns`, `TestAlertStateCRUD`, `TestRecentRuns`, `TestRunSource`)
- `internal/store/store_test.go`, `internal/store/storetest/storetest.go` (`source_alerts` в списке `TRUNCATE`)

**Задача 2 (пакет alert)** - предлагаемый тип коммита `feat`:
- `internal/alert/alert.go` (`Loop`, `decide`, `message`, `Tick`, `Run`)
- `internal/alert/alert_test.go` (`TestDecide`, `TestMessage`, `TestTick`, `TestTickInterruptedFirst`, `TestTickSendFailure`, `TestNoAdmins`, `TestRunStops`)

## Decisions Made

- Решение принимается по состоянию (последний завершённый прогон против строки `source_alerts`), поэтому алерт не теряется, если прогон закончился при выключенном боте.
- При напоминании `notified_at` остаётся прежним, `run_id` и `reminded_at` обновляются. При смене failed <-> suspect строка перезаписывается целиком (`notified_at` = сейчас).
- Ошибка чтения или записи БД прерывает `Tick` и возвращается; `Run` пишет `Warn` и продолжает со следующего тика. Сбой отправки ошибкой `Tick` не считается.
- Повторный failed после failed (та же ситуация, другой прогон) тишину не нарушает до истечения 24 ч.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - стиль] Действие `clear` переименовано в `reset`**
- **Found during:** Задача 2
- **Issue:** Константа `clear` из текста плана затеняет встроенную функцию `clear`.
- **Fix:** `none, send, remind, reset`; поведение не менялось.
- **Files modified:** `internal/alert/alert.go`, `internal/alert/alert_test.go`
- **Commit:** не закоммичено (владелец)

**2. [Дополнение к плану] Тест `TestTickInterruptedFirst`**
- **Issue:** `TestTick` проверяет прерванный прогон только при существующем состоянии; случай «прерванный прогон и состояния нет» (типичный после деплоя) добавлен отдельным тестом.
- **Files modified:** `internal/alert/alert_test.go`

**Итого:** 2 отклонения, на цели плана не влияют.

## Known Stubs

Нет. `Loop.Send` намеренно остаётся внешней зависимостью: `bot.SendAlert` и запуск `alert.Loop` в `runBot` - задача 04-05.

## Issues Encountered

Первая запись `alert.go` не выполнилась (каталога `internal/alert` не было); каталог создан, файл записан заново. На код не повлияло.

## Проверка

- `go vet ./...` и `gofmt -l cmd internal migrations`: чисто.
- `go test -p 1 -count=1 -timeout 5m ./internal/alert/ ./internal/store/ ./internal/ingest/` на тестовой БД (порт 5433): все `ok`; все перечисленные в плане тесты PASS (в том числе `TestRunLifecycle`, `TestSourceHealth`, `TestAcceptBaseline`, `TestRunFixture`), SKIP нет.
- В тестовой БД у `source_alerts` ровно пять нужных колонок; в миграции есть раздел Down; `source_alerts` есть в обоих списках очистки.
- `go list -deps ./internal/alert` не содержит `go-telegram`; `store.RunInterrupted` используется в `alert.go`; SQL вне `internal/store` (кроме тестов) нет; `Sprintf` в `alerts.go` и `runs.go` нет; `go.mod` и `go.sum` не менялись.
- Полный прогон `TEST_DATABASE_URL=...5433/aggregator_test go test -p 1 -count=1 -timeout 5m -v ./...`: все пакеты `ok`, 211 строк `--- PASS` (тесты и подтесты), 0 `FAIL`, 0 `SKIP`.
- `env -u TEST_DATABASE_URL go test -count=1 ./...` проходит (БД-тесты пропускаются).
- Dev-БД (порт 5434) не использовалась и миграция на неё не применялась: владелец применит `00003` сам. Сеть, `.env` и `internal/ingest/config*.go` не затрагивались.

## Threat Flags

Новой поверхности сверх `<threat_model>` нет. Митигации: T-04-18 (получатели только из `Loop.Admins`; `TestNoAdmins`, `TestTick` проверяет чаты 101 и 102), T-04-19 (`TestDecide`, `TestTick`: один алерт на смену состояния, напоминание с 24 ч, прерванный прогон молчит), T-04-20 (`TestTickSendFailure`), T-04-21 (текст без разметки, `message` не экранирует, `<` и `>` из причины проходят как есть; 04-05 обязан отправлять без parse_mode).

## Next Phase Readiness

- 04-05 подключает `bot.SendAlert(ctx, chatID, text, acceptRunID)` как `Loop.Send` (без parse_mode), кнопку `r:a:<runID>` при `acceptRunID > 0`, запуск `alert.Loop.Run` в `runBot` и использует `RecentRuns` и `RunSource` для `/runs` и принятия.
- Владельцу перед деплоем: миграция `00003` применяется автоматически `migrate`-сервисом; на dev-БД (порт 5434) - вручную по желанию.
- Блокеров нет.

## Self-Check: PASSED

- Файлы существуют: `migrations/00003_source_alerts.sql`, `internal/store/alerts.go`, `internal/store/alerts_test.go`, `internal/alert/alert.go`, `internal/alert/alert_test.go`; изменены `internal/store/runs.go`, `store_test.go`, `storetest/storetest.go`.
- Коммитов нет по условию запуска; `plan_head_before` = e2fbb45.
- Проверки плана (`ALERT STORE OK`, `ALERT LOOP OK` по составляющим) и полный прогон выполнены и прошли.

---
*Phase: 04-telegram-search*
*Completed: 2026-09-30*
