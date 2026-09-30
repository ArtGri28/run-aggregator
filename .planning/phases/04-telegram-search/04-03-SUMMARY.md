---
phase: 04-telegram-search
plan: 03
subsystem: bot
tags: [go-telegram-bot, pagination, event-card, error-handling, rate-limit, pgx, postgres, httptest]

requires:
  - phase: 04-telegram-search
    provides: "04-01/04-02: кодек callback e:/p:, listWhere, ListEvents/CountEvents, GetEvent, фильтры, фиктивный Bot API"
provides:
  - "Список с листанием вперёд и назад, зажимом страницы, кнопками карточек 1-5 и пометками [ОТМЕНЁН]/[ПЕРЕНЕСЁН] (BOT-04, BOT-06)"
  - "Карточка события с датами, местом, типом, дистанциями, описанием, кнопкой [Регистрация] и источниками (BOT-05)"
  - "validURL: только http/https с хостом, до 2048 байт, без пробелов и управляющих символов"
  - "classify + повтор 429 + запасные пути отправки и правки (BOT-09)"
  - "store.EventSources, storetest.HideEvent/MergeEvent/SetStatus"
affects: [04-04, 04-05, 04-06]

actuals:
  tokens: 12000
  tasks: 2
  commits: 0

plan_head_before: 79fdc94149b8ab8804b08c9ad5f9e49376e98a95
commits: "uncommitted — owner commits manually"

tech-stack:
  added: []
  patterns:
    - "Один разбор ошибок Bot API (classify); attempt = повтор после 429 + повтор без URL-кнопок при 400"
    - "Фиктивный Bot API со сценарием ответов: script(call, n) по порядковому номеру вызова метода"
    - "Карточка и список: cut до n рун, затем html.EscapeString; ссылки только через validURL"

key-files:
  created:
    - internal/bot/list_test.go
    - internal/bot/tg_test.go
  modified:
    - internal/bot/list.go
    - internal/bot/tg.go
    - internal/bot/bot.go
    - internal/bot/bot_test.go
    - internal/store/listing.go
    - internal/store/listing_test.go
    - internal/store/storetest/storetest.go

key-decisions:
  - "attempt(ctx, sc, call) общий для send и show: одна точка, где 429 повторяется один раз, а 400 на экране с URL-кнопками повторяется без них; повтор не рекурсивный, поэтому число вызовов ограничено"
  - "show: 400 при правке (любое описание, A4) - новый sendMessage в тот же чат; перед этим правка сама повторяется без URL-кнопок, так что испорченная ссылка не порождает лишнее сообщение"
  - "Лимиты длины в cardScreen (название 200, город 100, адрес 200, подпись дистанции 40 рун): схема их не ограничивает, а текст обязан уложиться в 4096"
  - "fakeTelegram(t, scripts...) - variadic: существующие вызовы fakeTelegram(t) не менялись"
  - "answer при ошибке пишет только Debug (устаревший callback не шум для Warn)"

requirements-completed: [BOT-04, BOT-05, BOT-06, BOT-09]

coverage:
  - id: D1
    description: "Листание вперёд и назад без потери фильтров, нет ← на первой и → на последней, строки без пропусков и повторов, кнопки карточек 1-5"
    requirement: BOT-04
    verification:
      - kind: integration
        ref: "internal/bot/list_test.go#TestListPaging, TestListScreenShape"
        status: pass
    human_judgment: false
  - id: D2
    description: "Устаревшая версия фильтров даёт страницу 1 актуальной выдачи с пояснением; страница за пределами зажимается до последней"
    requirement: BOT-04
    verification:
      - kind: integration
        ref: "internal/bot/list_test.go#TestListStaleAndClamp"
        status: pass
    human_judgment: false
  - id: D3
    description: "Карточка: название, дата на русском, место, тип, дистанции, источники со ссылками, [Регистрация] с проверенным URL, [К списку]; пустые поля без заглушек"
    requirement: BOT-05
    verification:
      - kind: integration
        ref: "internal/bot/list_test.go#TestCard"
        status: pass
    human_judgment: false
  - id: D4
    description: "Скрытые и слитые события не в списке и не в счётчике, старая кнопка - «Событие больше не доступно»; cancelled/postponed видны с пометкой в списке и карточке"
    requirement: BOT-06
    verification:
      - kind: integration
        ref: "internal/bot/list_test.go#TestHiddenAndMerged, TestStatusMarks"
        status: pass
    human_judgment: false
  - id: D5
    description: "Ссылки только http/https с хостом; HTML-экранирование названия, описания, подписей; пределы длины строки списка и карточки"
    requirement: BOT-05
    verification:
      - kind: unit
        ref: "internal/bot/list_test.go#TestValidURL, TestLongTexts"
        status: pass
      - kind: integration
        ref: "internal/bot/list_test.go#TestCardEscapingAndURLs"
        status: pass
    human_judgment: false
  - id: D6
    description: "Ошибки Bot API: классификация, повтор 429 с пределом и отменой по ctx, not modified - успех, запасная отправка, повтор без URL-кнопок, 403 без повтора и без текста экрана в логе"
    requirement: BOT-09
    verification:
      - kind: unit
        ref: "internal/bot/tg_test.go#TestClassify, TestSendBlocked, TestRateLimitRetry, TestEditNotModified, TestEditFallbackToSend, TestURLButtonFallback"
        status: pass
    human_judgment: false
  - id: D7
    description: "Сообщение без Message (InaccessibleMessage) правится по chat_id и message_id; ровно один ответ на каждый callback, в том числе при ошибке ответа 400"
    requirement: BOT-09
    verification:
      - kind: integration
        ref: "internal/bot/list_test.go#TestInaccessibleMessage, TestAnswerOncePerCallback"
        status: pass
    human_judgment: false
  - id: D8
    description: "SQL источников события: без state = gone и match_state = out_of_scope, по коду"
    requirement: BOT-05
    verification:
      - kind: integration
        ref: "internal/store/listing_test.go#TestEventSources"
        status: pass
    human_judgment: false

duration: 45min
completed: 2026-09-30
status: complete
---

# Phase 4 Plan 03: Список, карточка и ошибки Telegram Summary

**Список листается в обе стороны с зажимом страницы и пометками статуса, карточка события показывает всё по BOT-05 с проверенной кнопкой регистрации и источниками, а единый `classify` с одним повтором после 429 и запасными путями (правка -> новое сообщение, URL-кнопки -> без них) не даёт ошибкам Bot API уронить бота.**

## Performance

- **Duration:** около 45 мин
- **Completed:** 2026-09-30
- **Tasks:** 2 из 2
- **Files:** 2 новых, 7 изменённых

## Accomplishments

- Путь `/start -> ... -> список -> карточка -> регистрация` замкнут: строки страницы `N. ДД.ММ · Город · Название · км`, ряд кнопок `1..5` (`e:<id>:<стр>:v<N>`), ряд `[←] [стр. i/M] [→]`, `[Фильтры]`. Страница за пределами выдачи зажимается до последней, кнопка со старой версией фильтров ведёт на страницу 1 с пояснением.
- Карточка: жирное название с пометкой статуса, дата с русским месяцем (диапазон при `EndDate`), время только при непустом `StartTime`, «Город, адрес», тип, дистанции с подписями, описание, «Источники:» со ссылками. Кнопки `[Регистрация]` (только для `validURL`) и `[К списку]`.
- Скрытые и слитые события по старой кнопке дают «Событие больше не доступно» и `[К списку]`; `cancelled` и `postponed` не скрываются и получают `[ОТМЕНЁН]` / `[ПЕРЕНЕСЁН]` через константы `event.Status*`.
- `classify` - единственное место разбора ошибок Bot API (429 через `errors.As`, 403/400 через `errors.Is`, «message is not modified» отдельным видом). `attempt` даёт один повтор после 429 (ожидание не дольше `maxRetryWait` = 30 с и прерывается отменой ctx) и один повтор без URL-кнопок при 400.
- Проверено на полных данных: 133 события Марафонца, у 105 есть ссылка регистрации; тесты берут события из реальной фикстуры, а не из синтетики.

## Task Commits

Коммитов нет: по правилам владельца коммиты делает он вручную. Файлы по задачам:

**Задача 1 (список, карточка, пометки, источники)** - предлагаемый тип коммита `feat`:
- `internal/store/listing.go`, `internal/store/listing_test.go` (`EventSource`, `EventSources`, `TestEventSources`)
- `internal/store/storetest/storetest.go` (`HideEvent`, `MergeEvent`, `SetStatus`, `exec`)
- `internal/bot/list.go` (`onList` с зажимом, `listScreen`, `onCard`, `goneScreen`, `cardScreen`, `validURL`, `sourceNames`, `km`, `cut`, `statusMark`, `ruDate`)
- `internal/bot/bot.go` (маршрут `e:`)
- `internal/bot/list_test.go`: `TestListPaging`, `TestListStaleAndClamp`, `TestCard`, `TestHiddenAndMerged`, `TestStatusMarks`, `TestCardEscapingAndURLs`, `TestValidURL`, `TestLongTexts`, `TestListScreenShape`
- `internal/bot/bot_test.go` (тип `script`, `apiErr`, `fakeTelegram(t, scripts...)`; `TestTracerSearch` приведён к нумерованным строкам списка)

**Задача 2 (ошибки Telegram API)** - предлагаемый тип коммита `feat`:
- `internal/bot/tg.go` (`errKind`, `classify`, `maxRetryWait`, `withRetry`, `dropURLButtons`, `attempt`, новые тела `send`, `show`, `answer`)
- `internal/bot/tg_test.go`: `TestClassify`, `TestSendBlocked`, `TestRateLimitRetry`, `TestEditNotModified`, `TestEditFallbackToSend`, `TestURLButtonFallback`
- `internal/bot/list_test.go`: `TestInaccessibleMessage`, `TestAnswerOncePerCallback`

`list_test.go` и `bot_test.go` общие для двух задач; при желании развести коммиты проще положить их целиком в задачу 1.

## Files Created/Modified

- `internal/store/listing.go` - `EventSource`, `EventSources` (SQL константой с `$1`)
- `internal/store/storetest/storetest.go` - `HideEvent`, `MergeEvent`, `SetStatus`; отдельное соединение с проверкой имени базы на `_test`
- `internal/bot/list.go` - список, карточка, проверка ссылок, имена источников
- `internal/bot/tg.go` - классификация ошибок, повторы, запасные пути
- `internal/bot/bot.go` - `WithCallbackQueryDataHandler("e:", ...)`
- `internal/bot/{list,tg}_test.go` (новые), `bot_test.go`, `internal/store/listing_test.go` - тесты

## Decisions Made

- `send` и `show` делят один `attempt`, а не каждый свои ветки: число вызовов Bot API ограничено (send: до 4, show: до 2 правок + до 4 отправок в худшем сочетании), рекурсии нет.
- При `400` на правке карточки сначала повторяется правка без URL-кнопок (сообщение остаётся тем же), и только затем идёт новая отправка.
- Пределы длины в `cardScreen` введены потому, что схема не ограничивает название, город, адрес и подпись дистанции, а лимит Telegram - 4096; описание уже ≤ 1000 по CHECK.
- Несуществующий id карточки показывает то же «Событие больше не доступно», что и скрытое событие, - без различия для пользователя.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - блокирующее расхождение] `fakeTelegram(t, script)` в `<interfaces>` отсутствовал**
- **Found during:** Задача 2 (подготовка тестов)
- **Issue:** Раздел `<interfaces>` называет `fakeTelegram(t, script)` результатом 04-01/04-02, но в коде была только `fakeTelegram(t)`.
- **Fix:** добавлен variadic-параметр `scripts ...script`, где `script = func(c call, n int) string` получает вызов и порядковый номер среди вызовов того же метода; старые вызовы не менялись. Добавлен `apiErr` для тела ошибки.
- **Files modified:** `internal/bot/bot_test.go`
- **Commit:** не закоммичено (владелец)

**2. [Rule 1 - следствие плана] `TestTracerSearch` ожидал строки без номера**
- **Found during:** Задача 1
- **Issue:** формат строки списка по D-11 стал «N. ДД.ММ · …»; регулярное выражение и проверка префикса в tracer-тесте 04-01 больше не подходили.
- **Fix:** обновлены `listLineRE` и проверка префикса на «i+1. ДД.ММ · ».
- **Files modified:** `internal/bot/bot_test.go`
- **Commit:** не закоммичено (владелец)

**3. [Rule 2 - недостающая защита] Пределы длины полей карточки**
- **Found during:** Задача 1
- **Issue:** название, город, адрес и подпись дистанции в схеме не ограничены, длинное значение вывело бы карточку за 4096 символов.
- **Fix:** `cut` до 200 / 100 / 200 / 40 рун перед экранированием; `TestLongTexts` проверяет итог ≤ 4096.
- **Files modified:** `internal/bot/list.go`, `internal/bot/list_test.go`
- **Commit:** не закоммичено (владелец)

**Итого:** 3 отклонения (1 блокирующее расхождение, 1 следствие плана, 1 защитное); на цели плана не влияют.

## Known Stubs

Нет. Все заглушки 04-01 на этот план (зажим страницы, кнопки карточек, пометки статуса, тела `send`/`show`) закрыты. `sourceNames` содержит только `marathonec`: остальные источники подключаются в следующих фазах, неизвестный код показывается как есть.

## Issues Encountered

- Первый вызов `Edit` по `list.go` отклонён (файл только что переписан скриптом): файл прочитан заново, правка применена. На код это не повлияло.
- Прочих проблем нет. Правки владельца в `internal/ingest/config*.go` не трогались, dev-БД (порт 5434), сеть и `.env` не использовались.

## Проверка

- `TG ERRORS OK`: шесть тестов задачи 2 без БД (`env -u TEST_DATABASE_URL`) пройдены; в `tg.go` есть `TooManyRequestsError`, библиотечной проверки 429 нет.
- `LIST CARD OK` и `BOT09 OK`: `go test -p 1 ./internal/bot/ ./internal/store/` на тестовой БД (порт 5433) - все перечисленные в плане тесты PASS, SKIP нет, SQL вне `internal/store` нет, `go vet ./...` и `gofmt -l cmd internal migrations` чисто.
- `env -u TEST_DATABASE_URL go test -count=1 ./...` проходит (БД-тесты пропускаются).
- Полный прогон `TEST_DATABASE_URL=...5433/aggregator_test go test -p 1 -count=1 -timeout 5m -v ./...`: код 0, все пакеты `ok`, 188 строк `--- PASS` (тесты и подтесты), 0 `FAIL`, 0 `SKIP`.

## Threat Flags

Новой поверхности сверх `<threat_model>` плана нет. Митигации: T-04-13 (`validURL`, `TestValidURL`, `TestCardEscapingAndURLs`), T-04-14 (`html.EscapeString` для всех динамических значений карточки и списка), T-04-15 (`classify`, `attempt`, `TestRateLimitRetry`, `TestURLButtonFallback`, `TestSendBlocked`), T-04-16 (`onCard` проверяет `IsListed` и `MergedIntoID`, `TestHiddenAndMerged`), T-04-17 (`TestAnswerOncePerCallback`).

## Next Phase Readiness

- 04-04 добавляет `source_alerts` в список очистки `storetest.Open` (файл общий с этим планом; хелперы `HideEvent`, `MergeEvent`, `SetStatus` и `exec` его не трогают).
- 04-05 может использовать `script`, `apiErr` и `only` из тестов бота для проверки админских ответов и `send` с плоским текстом.
- Блокеров нет.

## Self-Check: PASSED

- Созданные файлы существуют: `internal/bot/list_test.go`, `internal/bot/tg_test.go`; изменены `list.go`, `tg.go`, `bot.go`, `bot_test.go`, `internal/store/listing.go`, `listing_test.go`, `storetest/storetest.go`.
- Коммитов нет по условию запуска; `plan_head_before` = 79fdc94.
- Автоматические проверки плана (`TG ERRORS OK`, `LIST CARD OK`, `BOT09 OK`, полный прогон) выполнены и прошли.

---
*Phase: 04-telegram-search*
*Completed: 2026-09-30*
