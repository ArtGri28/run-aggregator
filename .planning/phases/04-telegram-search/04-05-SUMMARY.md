---
phase: 04-telegram-search
plan: 05
subsystem: bot-admin
tags: [telegram, admin, alerts, accept-baseline, wiring]

requires:
  - phase: 04-telegram-search
    provides: "04-01..04-03: Bot, callback-кодек r:, show/send/answer; 04-04: alert.Loop, RecentRuns, RunSource"
provides:
  - "/runs: последние 3 прогона по источнику, только ADMIN_TG_IDS (BOT-07)"
  - "Принятие suspect-прогона кнопкой с подтверждением через Runner.Accept (D-15)"
  - "Bot.SendAlert (обычный текст, только администраторам) как alert.Loop.Send (MON-05)"
  - "runBot: Runner и источники как у worker, цикл алертов раз в минуту; newRunner и newSources общие"
affects: [04-06, 04-07, 04-08]

actuals:
  tokens: 11000
  tasks: 2
  commits: 0

plan_head_before: 5f876dee8092f73403ee18c29215388934208c05
commits: "uncommitted — owner commits manually"

tech-stack:
  added: []
  patterns:
    - "Не-админ по /runs получает ответ неизвестной команды (вызов onText с тем же апдейтом), по r:* — пустой notice без обращения к store"
    - "Проверка администратора в самом обработчике на каждом callback, а не по факту показа кнопки"
    - "Один newRunner из ingest.LoadConfig для worker и bot (Pitfall 11)"

key-files:
  created:
    - internal/bot/admin.go
    - internal/bot/admin_test.go
    - cmd/aggregator/main_test.go
  modified:
    - internal/bot/bot.go
    - cmd/aggregator/main.go
    - internal/ingest/runner.go

key-decisions:
  - "Ошибки принятия показываются правкой сообщения (кнопки исчезают), а не всплывающим уведомлением: отказ виден в чате; «Прогон не найден» и «Принятие недоступно» - уведомлением без правки"
  - "«скрыто N» в сообщении об успехе - Result.Gone"
  - "Текст /runs и алерта обрезается до 4000 рун (запас до лимита Telegram 4096)"

requirements-completed: [BOT-07, MON-05, BOT-01]

coverage:
  - id: D1
    description: "/runs только для администраторов: не-админ получает ответ, побайтно равный ответу на /foo, без данных о прогонах; админ видит прогоны обычным текстом с кнопкой принятия"
    requirement: BOT-07
    verification:
      - kind: integration
        ref: "internal/bot/admin_test.go#TestRunsNonAdmin, TestRunsAdmin"
        status: pass
    human_judgment: false
  - id: D2
    description: "Принятие: вопрос -> подтверждение -> Runner.Accept; повторное и не последнее -> «Уже нельзя принять»; идущий прогон -> «повторите позже»; отмена ничего не меняет; не-админ ничего не меняет; без Runner принятие недоступно"
    requirement: BOT-07
    verification:
      - kind: integration
        ref: "internal/bot/admin_test.go#TestAcceptFlow, TestAcceptRefusals, TestAcceptCancel, TestAcceptNonAdmin, TestAcceptWithoutRunner"
        status: pass
    human_judgment: false
  - id: D3
    description: "SendAlert: без parse_mode, кнопка r:a:<id> только при acceptRunID > 0, не-админ - ошибка без вызова API; связка алерт -> кнопка -> принятие -> следующий тик молчит"
    requirement: MON-05
    verification:
      - kind: integration
        ref: "internal/bot/admin_test.go#TestSendAlert, TestAlertToAccept"
        status: pass
    human_judgment: false
  - id: D4
    description: "newRunner переносит параметры наблюдения из конфигурации; newSources(nil) регистрирует marathonec без сети"
    requirement: BOT-01
    verification:
      - kind: unit
        ref: "cmd/aggregator/main_test.go#TestNewRunner, TestNewSources"
        status: pass
    human_judgment: false
  - id: D5
    description: "Бинарник bot: ошибки конфигурации (токен, OBSERVE_GONE_RUNS, DATABASE_URL) - код 1, имя переменной без значения, до сети; worker bogus - код 2"
    requirement: BOT-01
    verification:
      - kind: e2e
        ref: "команда verify задачи 2 (BOT WIRING OK)"
        status: pass
    human_judgment: false

duration: 30min
completed: 2026-09-30
status: complete
---

# Phase 4 Plan 05: Админ-функции и сборка процесса bot Summary

**Администратор видит `/runs` и принимает suspect-прогон кнопкой с подтверждением через тот же `Runner.Accept`, что и CLI; `runBot` строит Runner той же функцией, что worker, и запускает цикл алертов, отправляющий сообщения через `Bot.SendAlert`.**

## Accomplishments

- `/runs`: заголовок, по источнику до трёх строк `#<id> <status> ДД.ММ ЧЧ:ММ МСК · parsed · rej · oos · upserted · причина (до 200)`, обычный текст. Кнопка «Принять прогон <id> (<код>)» у источника, чей последний прогон suspect со снимком. Пусто - «Прогонов пока нет».
- `r:a` -> вопрос с `[Да, принять]` / `[Отмена]`; `r:y` -> `Runner.Accept`; `r:n` -> «Отменено». Отказы: `ErrNotAcceptable` - «Уже нельзя принять…», `ErrRunInProgress` - «повторите позже», прочее - «Не удалось принять…» и `slog.Error`.
- Доступ: `isAdmin(From.ID)` в начале каждого `onRun` и в `onRuns`. Не-админ по `/runs` - вызов `onText` с исходным апдейтом (ответ неизвестной команде), по `r:*` - пустой notice, `slog.Warn` без id и текста, store не трогается.
- `SendAlert(ctx, chatID, text, acceptRunID)`: chat вне администраторов - ошибка без вызова API; текст без parse_mode; кнопка `r:a:<id>` при `acceptRunID > 0`; отправка через общий `send` (повтор 429 и прочее из BOT-09).
- `cmd/aggregator/main.go`: `newSources(f)` и `newRunner(s, cfg)` общие для `runWorker` и `runBot`. `runBot`: `bot.LoadConfig` -> `ingest.LoadConfig(false)` -> `openStore` -> `bot.New` -> `Runner`/`Sources` (`newSources(nil)`) -> `alert.Loop` в горутине (`sync.WaitGroup`) -> `b.Start` -> ожидание цикла -> закрытие store. Ошибки конфигурации по-прежнему видны до БД и сети.

## Task Commits

Коммитов нет: по правилам владельца коммиты делает он вручную ("uncommitted — owner commits manually"). Файлы по задачам, предлагаемый тип `feat`:

**Задача 1 (`/runs`, принятие, SendAlert):**
- `internal/bot/admin.go` (новый: `onRuns`, `runsScreen`, `onRun`, `SendAlert`)
- `internal/bot/admin_test.go` (новый: `TestRunsNonAdmin`, `TestRunsAdmin`, `TestAcceptFlow`, `TestAcceptRefusals`, `TestAcceptCancel`, `TestAcceptNonAdmin`, `TestAcceptWithoutRunner`, `TestSendAlert`, `TestAlertToAccept`)
- `internal/bot/bot.go` (поля `Runner`, `Sources`; маршруты `runs` и `r:`; `isAdmin`)
- `internal/ingest/runner.go` (два `%w` в `Accept`, см. отклонение 1)

**Задача 2 (`runBot`, общие `newRunner`/`newSources`):**
- `cmd/aggregator/main.go` (`newSources`, `newRunner`, `runBot`, `runWorker` переведён на общие функции)
- `cmd/aggregator/main_test.go` (новый: `TestNewRunner`, `TestNewSources`)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `Runner.Accept` не оборачивал два отказа sentinel-ошибками**
- **Found during:** Задача 1 (тест `TestAcceptRefusals`, ожидание из `must_haves`)
- **Issue:** «идёт прогон» и «принимать можно только последний прогон» возвращались обычными `fmt.Errorf` без `%w`, поэтому бот не мог отличить их от сбоя и показал бы «Не удалось принять» вместо «повторите позже» / «Уже нельзя принять».
- **Fix:** в `internal/ingest/runner.go` добавлены `%w` с `store.ErrRunInProgress` и `store.ErrNotAcceptable`; тексты ошибок для CLI дополнены хвостом от `%w`, поведение и коды выхода не менялись. Файл не входил в `files_modified` плана.
- **Files modified:** `internal/ingest/runner.go`
- **Commit:** не закоммичено (владелец)

**2. [Уточнение плана] Ветка `ErrRunInProgress` в тесте**
- Кнопка старого suspect-прогона при уже идущем новом прогоне даёт «Уже нельзя принять» (прогон перестал быть последним), а не «повторите позже»: `Accept` сравнивает id раньше статуса. «Повторите позже» достигается, когда нажат id самого идущего прогона (или гонка с `ClaimRun`). `TestAcceptRefusals` проверяет оба случая.

**Итого:** 1 исправление, 1 уточнение; цели плана не затронуты.

## Known Stubs

Нет.

## Issues Encountered

Нет. Правки владельца, dev-БД (порт 5434), `.env` и сеть не использовались; `docker compose up` не вызывался (тестовая БД на порту 5433 уже работала), остальные команды `<verify>` выполнены как в плане.

## Проверка

- `ADMIN OK`: `go test -p 1 ./internal/bot/` (все перечисленные тесты PASS, SKIP нет), `go vet ./...`, `gofmt -l cmd internal migrations` чисто.
- `BOT WIRING OK`: `TestNewRunner`, `TestNewSources`; `bot` без токена, с неверным `OBSERVE_GONE_RUNS` и без `DATABASE_URL` завершается кодом 1 с именем переменной, значение токена в выводе отсутствует; `worker bogus` - код 2. Бинарник `bot` с токеном и доступной БД не запускался.
- `PHASE CODE OK`: `go test -p 1 -count=1 -timeout 5m ./...` на тестовой БД - все пакеты `ok`; `-v` показывает 222 строки `--- PASS`, 0 `FAIL`, 0 `SKIP`; `go mod verify` чисто, прямые зависимости ровно четыре, SQL вне `internal/store` нет, `WithDebug` и `api.telegram.org` в коде нет, стандартный `log` не подключён.
- `go.mod`, `go.sum` не менялись.

## Threat Flags

Новой поверхности сверх `<threat_model>` нет. Митигации: T-04-22 (`TestRunsNonAdmin`, `TestAcceptNonAdmin`), T-04-23 (`TestRunsNonAdmin`, `TestSendAlert`), T-04-24 (только `Runner.Accept`; `TestAcceptFlow`, `TestAcceptRefusals`), T-04-25 (`TestNewRunner`), T-04-26 (BOT WIRING OK).

## Next Phase Readiness

- Бинарник содержит всё для первого включения (D-07): worker делает первый прогон suspect, бот шлёт алерт с кнопкой, принятие наполняет каталог. Дальше упаковка и доставка: 04-06..04-08.
- Владельцу: миграция `00003` применяется сервисом `migrate` (на dev-БД - вручную по желанию). Боту в compose нужны `TELEGRAM_BOT_TOKEN`, `ADMIN_TG_IDS`, `DATABASE_URL` и опционально те же `OBSERVE_*`/`QUALITY_*`, что у worker; `OWNER_CONTACT` не нужен.
- Блокеров нет.

## Self-Check: PASSED

- Файлы существуют: `internal/bot/admin.go`, `internal/bot/admin_test.go`, `cmd/aggregator/main_test.go`; изменены `internal/bot/bot.go`, `cmd/aggregator/main.go`, `internal/ingest/runner.go`.
- Коммитов нет по условию запуска; `plan_head_before` = 5f876de.
- Проверки `ADMIN OK`, `BOT WIRING OK`, `PHASE CODE OK` выполнены и прошли.

---
*Phase: 04-telegram-search*
*Completed: 2026-09-30*
