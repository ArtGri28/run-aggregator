---
phase: 04-telegram-search
plan: 01
subsystem: bot
tags: [go-telegram-bot, long-polling, callback-codec, pgx, postgres, httptest]

requires:
  - phase: 03-first-source
    provides: "события Марафонца в БД (ingest.Runner Run/Accept), store.SaveEvent/GetEvent, InTx"
provides:
  - "Tracer /start -> Найти забег -> сводка -> Показать N -> страница 1 списка на реальных событиях (BOT-01)"
  - "internal/bot: New/Start, recoverMW, privateOnlyMW, свой default-handler, cb с единственным ответом на callback"
  - "Кодек callback_data f:/p:/e:/r: (строгий разбор, <= 64 байт) как контракт для 04-02..04-05"
  - "store.ListEvents/CountEvents по event.Filter; store.Filters/UpdateFilters (filters_v растёт только при изменении)"
  - "storetest.Open: чистая тестовая БД для пакетов вне store"
  - "Подкоманда bot: runBot (LoadConfig до БД и сети)"
affects: [04-02, 04-03, 04-04, 04-05, 04-06]

actuals:
  tokens: 26000
  tasks: 2
  commits: 0

plan_head_before: b534b9eccbb53eaba77b7af1728fbc882ff654d5
commits: "uncommitted — owner commits manually"

tech-stack:
  added: ["github.com/go-telegram/bot v1.27.0 (D-16)"]
  patterns:
    - "Фиктивный Bot API на httptest (multipart, запись вызовов) + реальный PostgreSQL 17"
    - "Один answerCallbackQuery в defer обёртки b.cb; recover-middleware первым"
    - "SQL выдачи: общий WHERE-константа, COALESCE(cardinality(...), 0) и []int32{} против ловушки nil-среза pgx"

key-files:
  created:
    - internal/event/filter.go
    - internal/store/listing.go
    - internal/store/users.go
    - internal/store/storetest/storetest.go
    - internal/store/listing_test.go
    - internal/store/users_test.go
    - internal/bot/config.go
    - internal/bot/callback.go
    - internal/bot/bot.go
    - internal/bot/tg.go
    - internal/bot/filters.go
    - internal/bot/list.go
    - internal/bot/bot_test.go
    - internal/bot/callback_test.go
    - internal/bot/config_test.go
  modified:
    - go.mod
    - go.sum
    - cmd/aggregator/main.go

key-decisions:
  - "recoverMW только логирует стек и не отвечает на callback: ответ уходит из defer в b.cb, иначе на панику пришло бы два ответа"
  - "encode() проверяет результат обратным parseCallback: кнопка, не проходящая грамматику или 64 байта, пропускается, а не уходит в Telegram"
  - "UpdateFilters блокирует строку самим upsert (INSERT ... ON CONFLICT DO UPDATE), отдельный SELECT FOR UPDATE не нужен"
  - "В логах бота вид callback — только буква f/p/e/r либо ?, чтобы произвольная строка data не попадала в лог"

requirements-completed: [BOT-01, BOT-03, BOT-04, BOT-08]

coverage:
  - id: D1
    description: "Tracer: /start, [Найти забег], сводка, [Показать N], первая страница из 5 строк на 133 реальных событиях Марафонца"
    requirement: BOT-01
    verification:
      - kind: integration
        ref: "internal/bot/bot_test.go#TestTracerSearch"
        status: pass
    human_judgment: false
  - id: D2
    description: "Кодек callback: строгий разбор, круговая проверка, предел 64 байта; устаревшие кнопки получают мягкий ответ"
    requirement: BOT-04
    verification:
      - kind: unit
        ref: "internal/bot/callback_test.go#TestCallbackRoundTrip, TestCallbackReject, TestCallbackLength"
        status: pass
      - kind: integration
        ref: "internal/bot/bot_test.go#TestCallbackPanicAnswered, TestNoPersonalDataInLogs"
        status: pass
    human_judgment: false
  - id: D3
    description: "Фильтры пользователя в users.filters с filters_v только при изменении; гонка быстрых нажатий; /start фильтры не сбрасывает"
    requirement: BOT-03
    verification:
      - kind: integration
        ref: "internal/store/users_test.go#TestUserFilters, TestUserFiltersConcurrent"
        status: pass
      - kind: unit
        ref: "internal/bot/bot_test.go#TestLoadFilters"
        status: pass
    human_judgment: false
  - id: D4
    description: "Приватность: в users только tg_id, настройки и метки времени; в логах нет текстов и имён; группы игнорируются; токен и ADMIN_TG_IDS не попадают в ошибки"
    requirement: BOT-08
    verification:
      - kind: integration
        ref: "internal/store/users_test.go#TestUsersColumns; internal/bot/bot_test.go#TestNoPersonalDataInLogs, TestPrivateOnly"
        status: pass
      - kind: unit
        ref: "internal/bot/config_test.go#TestLoadConfig"
        status: pass
      - kind: other
        ref: "команда BOT CLI OK из 04-01-PLAN (бинарник bot без токена / с неверным ADMIN_TG_IDS завершается кодом 1)"
        status: pass
    human_judgment: false
  - id: D5
    description: "SQL выдачи: видимость, границы корзин (lo, hi], пустые корзины не обнуляют выдачу, LIMIT/OFFSET, город и тип"
    verification:
      - kind: integration
        ref: "internal/store/listing_test.go#TestListEvents"
        status: pass
    human_judgment: false

duration: 65min
completed: 2026-09-30
status: complete
---

# Phase 4 Plan 01: Tracer Telegram-поиска Summary

**Путь /start -> [Найти забег] -> сводка -> [Показать N] -> страница 1 из 5 строк проходит на 133 реальных событиях Марафонца через go-telegram/bot v1.27.0, кодек callback_data, store и PostgreSQL 17 против фиктивного Bot API; подкоманда `bot` запускает long polling.**

## Performance

- **Duration:** около 65 мин
- **Started:** 2026-09-30T06:45:00Z (приблизительно)
- **Completed:** 2026-09-30T07:50:38Z
- **Tasks:** 2 из 2
- **Files:** 15 новых, 3 изменённых

## Accomplishments

- Tracer работает насквозь: `TRACER start=ok summary=133 list=5` (N = `CountEvents` по умолчанию, строки списка совпадают с `ListEvents(..., 5, 0)`, на каждый callback ровно один `answerCallbackQuery`, правка того же сообщения).
- Кодек callback_data закреплён как контракт для планов 04-02..04-05: строгий белый список форм, числа без знака и ведущих нулей, страницы 1..10000, версия 1..MaxInt32, `encode` отвергает > 64 байт и всё, что не разбирается обратно.
- Защита процесса и приватности: recover-middleware первым, `privateOnlyMW` (группы игнорируются без ответа), свой default-handler вместо библиотечного (тот печатает апдейт целиком), в логах только вид апдейта и буква префикса.
- Выдача: общий WHERE-фрагмент как константа с `$N`, `COALESCE(cardinality($5::int[]), 0)` плюс `[]int32{}` в Go (nil-срез pgx стал бы NULL и обнулил выдачу); проверено тестами на nil, пустых и заданных корзинах.
- `UpdateFilters` в транзакции: параллельные обновления не затирают друг друга, `filters_v` растёт только при реальном изменении (равный jsonb в другой записи версию не меняет).

## Task Commits

Коммитов нет: по правилам владельца сделаны только правки рабочего дерева, коммиты выполняет владелец вручную. Файлы по задачам:

**Задача 1 (tracer)** - предлагаемый тип коммита `feat`:
- `go.mod`, `go.sum`
- `internal/event/filter.go`
- `internal/store/users.go`, `internal/store/listing.go`
- `internal/store/storetest/storetest.go`
- `internal/bot/config.go`, `callback.go`, `bot.go`, `tg.go`, `filters.go`, `list.go`
- `internal/bot/bot_test.go` (в состоянии после задачи 1 содержал только `TestTracerSearch` и хелперы; итоговый файл включает и тесты задачи 2)
- `cmd/aggregator/main.go`

**Задача 2 (тесты)** - предлагаемый тип коммита `test`:
- `internal/bot/callback_test.go`, `internal/bot/config_test.go`
- `internal/bot/bot_test.go` (добавлены `TestLoadFilters`, `TestNoPersonalDataInLogs`, `TestPrivateOnly`, `TestCallbackPanicAnswered`)
- `internal/store/users_test.go`, `internal/store/listing_test.go`

Поскольку `bot_test.go` один на обе задачи, при желании развести коммиты по задачам его проще положить в задачу 1 целиком.

## Files Created/Modified

- `go.mod`, `go.sum` - `github.com/go-telegram/bot v1.27.0`; две строки go.sum совпали с хешами из 04-RESEARCH, косвенных зависимостей не добавилось
- `internal/event/filter.go` - `Filter`, `DistanceRange` (полуинтервал (Lo, Hi] в метрах)
- `internal/store/listing.go` - `ListItem`, `ListEvents`, `CountEvents`, `filterArgs`
- `internal/store/users.go` - `Filters`, `UpdateFilters`
- `internal/store/storetest/storetest.go` - `Open(t)`: skip без `TEST_DATABASE_URL`, fatal при имени базы не на `_test`, Migrate, TRUNCATE отдельным соединением
- `internal/bot/config.go` - `Config`, `LoadConfig` (ошибки без значений)
- `internal/bot/callback.go` - `callback`, `parseCallback`, `encode`
- `internal/bot/bot.go` - `Bot`, `New`, `Start`, `recoverMW`, `privateOnlyMW`, `cb`, `onDefault`, `today`
- `internal/bot/tg.go` - `screen`, `row`, `keyboard`, `send`, `show`, `answer`, `msgRef`
- `internal/bot/filters.go` - `userFilters`, `buckets`, `loadFilters`, `dateRange`, `query`, `summaryScreen`, `emptyScreen`, `onStart`, `onFilter`, `onText`
- `internal/bot/list.go` - `onList`, `listScreen`, `kmList`
- `cmd/aggregator/main.go` - `runBot` вместо `runProcess`, импорт `time/tzdata`
- тесты: `internal/bot/{bot,callback,config}_test.go`, `internal/store/{users,listing}_test.go`

## Decisions Made

- `recoverMW` не отвечает на callback сам (в отличие от эскиза в 04-RESEARCH): ответ даёт `defer` в `b.cb`, иначе паника породила бы два ответа на один callback.
- `callback.encode()` подтверждает результат обратным разбором. Это единственная точка, где гарантируется «то, что бот отправил, бот же и разберёт».
- Вид callback в логах сведён к одной букве (`f`, `p`, `e`, `r`) либо `?`: префикс до первого «:» из чужих данных мог бы нести пользовательский текст.
- Отдельный `SELECT ... FOR UPDATE` не понадобился: `INSERT ... ON CONFLICT DO UPDATE` блокирует строку до конца транзакции и возвращает актуальные `filters`, что подтверждает `TestUserFiltersConcurrent` (окно гонки 100 мс).

## Deviations from Plan

None - plan executed exactly as written.

Уточнения внутри плана (не отклонения):
- `go get` записал зависимость как `// indirect` (кода с импортом ещё не было); после кода `go mod tidy` перенёс её в прямые, итог соответствует проверке STATIC OK.
- Тесты сверх списка плана: `TestUserFiltersConcurrent` вынесен отдельно от `TestUserFilters`, `TestUsersColumns` проверяет BOT-08 по `information_schema`; в `TestCallbackReject` добавлены дополнительные отвергаемые формы (регистр, переполнение int32/int64, хвостовой «:»).

## Known Stubs

Намеренные, по плану закрываются в 04-02 / 04-03 (кнопок для установки этих значений в 04-01 нет, поэтому в реальной работе бота они недостижимы):

| Файл | Что | Закроет |
|------|-----|---------|
| `internal/bot/filters.go` `dateRange` | поддерживает только значение по умолчанию (`from = today`, без верхней границы); пресеты `30`, `cm`, `nm`, `mYYYYMM` игнорируются | 04-02 |
| `internal/bot/filters.go` `summaryScreen` | подпись даты всегда «все будущие»; название города = ключ, если он задан | 04-02 |
| `internal/bot/filters.go` `emptyScreen` | параметры `f`, `v` пока не используются, кнопок «снять фильтр» нет | 04-02 |
| `internal/bot/filters.go` `onFilter`, `onText` | из видов `f:` обрабатывается только `f:s`; любой текст получает подсказку «Нажмите /start» | 04-02 |
| `internal/bot/list.go` `onList`, `listScreen` | нет зажима страницы, кнопок карточек и пометок статуса | 04-03 |

## Issues Encountered

- `gofmt` (doc-комментарий) превратил пару одинарных кавычек `''` в тексте комментария `listWhere` в типографскую `”`; комментарий переписан словами «пустая строка».
- Прочих проблем нет; `internal/ingest/config.go` и `config_test.go` (возможные правки владельца) не тронуты, по `git status` они без изменений.

## Проверка

- `go mod verify`: all modules verified; `go vet ./...` и `gofmt -l cmd internal migrations` чисто.
- STATIC OK: ровно четыре прямые зависимости с нужными версиями, обе строки go.sum, список косвенных зависимостей не изменился, SQL вне `internal/store` не найден, нет `WithDebug` и адреса `api.telegram.org`, нет импорта `log`, `go test ./...` без `TEST_DATABASE_URL` проходит.
- TRACER OK: `TRACER start=ok summary=133 list=5`.
- BOT CORE OK: все 13 перечисленных в плане тестов пройдены, без SKIP.
- BOT CLI OK: `bot` без токена и с `ADMIN_TG_IDS=12,bad7value` завершается кодом 1, имена переменных в выводе есть, значений нет; запуск без подкоманды даёт код 2.
- Полный прогон `TEST_DATABASE_URL=...5433/aggregator_test go test -p 1 -count=1 -timeout 5m ./...`: все пакеты `ok`; 139 строк `--- PASS` (тесты и подтесты), 0 `FAIL`, 0 `SKIP`.
- Тестовая БД: только контейнер `aggregator-test-postgres-1` (порт 5433); dev-БД и сеть Telegram не использовались, `.env` не открывался.

## Threat Flags

Новой поверхности сверх `<threat_model>` плана нет. Сетевых слушателей бот не открывает, исходящее соединение одно: long polling к Bot API (в тестах заменён на httptest).

## User Setup Required

Для запуска в работе: `TELEGRAM_BOT_TOKEN` (обязательно) и `ADMIN_TG_IDS` (необязательно) в окружении процесса `bot`. Локально с настоящим токеном не запускать (D-02, Pitfall 7); переменные заводит 04-06/04-07.

## Next Phase Readiness

- 04-02 расширяет `dateRange`, `summaryScreen`, `emptyScreen`, `onFilter`, `onText` и добавляет `store/facets.go`; сигнатуры и грамматика callback готовы.
- 04-03 меняет только тела `send`/`show` (классификация ошибок Telegram, повтор 429) и добавляет карточку, зажим страницы и пометки статуса.
- 04-04 и 04-05 добавляют `source_alerts` в список очистки `storetest.Open` и админ-обработчики (`Bot.Runner`, `Bot.Sources`).
- Блокеров нет.

## Self-Check: PASSED

- Все 15 созданных и 3 изменённых файла существуют.
- Коммитов нет по условию запуска (владелец коммитит вручную); `plan_head_before` = b534b9e.
- Команды проверки плана (STATIC OK, TRACER OK, BOT CORE OK, BOT CLI OK, полный прогон) выполнены и прошли.

---
*Phase: 04-telegram-search*
*Completed: 2026-09-30*
