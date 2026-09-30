---
phase: 04-telegram-search
plan: 02
subsystem: bot
tags: [go-telegram-bot, filters, callback-codec, pgx, postgres, httptest]

requires:
  - phase: 04-telegram-search
    provides: "04-01: кодек callback f:/p:, UpdateFilters, listWhere/filterArgs, ListEvents/CountEvents, фиктивный Bot API"
provides:
  - "Экран-сводка фильтров D-10 с подменю даты, города, дистанции и типа (BOT-02)"
  - "Пресеты дат (30 дней, месяц, следующий месяц, все будущие) и 12 месяцев; «сегодня» по Europe/Moscow"
  - "Текстовый ввод города через normalize.CityKey без состояния «жду ввод»; «город не найден» с похожими городами (D-12)"
  - "Пустая выдача «Ничего не нашлось» с кнопками снятия только заданных фильтров (D-12)"
  - "store.TopCities, SimilarCities, CityName, EventTypes"
affects: [04-03, 04-05]

actuals:
  tokens: 10800
  tasks: 2
  commits: 0

plan_head_before: 3467d5b452e544d4ffd1cc8bc3a556a4000b1351
commits: "uncommitted — owner commits manually"

tech-stack:
  added: []
  patterns:
    - "Кнопка -> mutation(callback) -> одна UpdateFilters с абсолютным результатом -> экран; кнопка без изменения только показывает экран"
    - "Фасеты городов и типов переиспользуют listWhere и filterArgs из listing.go (город обнуляется в фильтре, а не вырезается из SQL)"
    - "Ввод города: normalize.CityKey, затем CityName; иначе SimilarCities по началу слова"

key-files:
  created:
    - internal/store/facets.go
    - internal/store/facets_test.go
    - internal/bot/filters_test.go
  modified:
    - internal/bot/filters.go

key-decisions:
  - "Кнопка сводит своё действие к чистой mutation(callback): nil — только показать экран; иначе одна UpdateFilters под блокировкой строки (T-04-11, Pitfall 13)"
  - "TopCities не режет предикат города из SQL: обнуляет f.CityKey и берёт тот же listWhere, поэтому фасеты и выдача не расходятся"
  - "Текст города в лог не пишется; логируется только факт «city text» и found=true/false (BOT-08, T-04-12)"
  - "Ключ длиннее 60 байт (предел callback f:c:) в onText считается «не понял»: такой город нельзя ни выбрать кнопкой, ни сохранить (loadFilters его бы отбросил)"

requirements-completed: [BOT-02, BOT-03]

coverage:
  - id: D1
    description: "Модель фильтров: пресеты дат, месяцы, корзины D-08 без дыр и пересечений, подписи, экранирование, чистые изменения"
    requirement: BOT-02
    verification:
      - kind: unit
        ref: "internal/bot/filters_test.go#TestDateRange, TestBucketMembership, TestMonthOptions, TestFilterLabels, TestFilterMutations"
        status: pass
    human_judgment: false
  - id: D2
    description: "Сводка D-10 и подменю даты, города (топ-10), дистанции (несколько корзин) и типа (D-09) на 133 реальных событиях; повтор выбора filters_v не меняет"
    requirement: BOT-02
    verification:
      - kind: integration
        ref: "internal/bot/filters_test.go#TestFilterFlow"
        status: pass
    human_judgment: false
  - id: D3
    description: "Текстовый ввод города: синоним «Питер», «город не найден» с похожими, экранирование HTML, команды и длинный ввод дают подсказку и не меняют фильтры"
    requirement: BOT-02
    verification:
      - kind: integration
        ref: "internal/bot/filters_test.go#TestCityText"
        status: pass
    human_judgment: false
  - id: D4
    description: "Пустая выдача: кнопки снятия только заданных фильтров; сброс всех фильтров даёт {} и увеличивает filters_v на 1"
    requirement: BOT-03
    verification:
      - kind: integration
        ref: "internal/bot/filters_test.go#TestEmptyResult, TestResetFilters"
        status: pass
    human_judgment: false
  - id: D5
    description: "SQL фасетов: топ-города с учётом остальных фильтров, похожие города по началу слова с защитой алфавита, имя города, типы будущих событий"
    requirement: BOT-02
    verification:
      - kind: integration
        ref: "internal/store/facets_test.go#TestTopCities, TestSimilarCities, TestCityName, TestEventTypes"
        status: pass
    human_judgment: false

duration: 30min
completed: 2026-09-30
status: complete
---

# Phase 4 Plan 02: Фильтры поиска Summary

**Один экран-сводка D-10 с подменю даты (пресеты и 12 месяцев), города (топ-10 из БД и текстовый ввод через normalize.CityKey), дистанции (корзины D-08, несколько сразу) и типа (D-09); каждое нажатие — одна UpdateFilters с абсолютным значением; «город не найден» и «ничего не нашлось» по D-12.**

## Performance

- **Duration:** около 30 мин
- **Completed:** 2026-09-30
- **Tasks:** 2 из 2
- **Files:** 3 новых, 1 изменённый

## Accomplishments

- Модель фильтров целиком чистыми функциями: `dateRange` (пресеты `30`, `cm`, `nm`, `mYYYYMM`, значение вне грамматики — как по умолчанию), `monthOptions`, подписи, `mutation(callback)` и методы `setDate/setCity/setType/toggleBucket/clearBuckets/unset/reset`. Часы не читаются: `today` приходит параметром.
- Корзины D-08 проверены перебором 1..200000 м: у каждой дистанции ровно одна корзина.
- `onFilter` разобран на «изменение» и «экран»: кнопка без изменения (сводка, открытие подменю) читает `Filters`, остальные идут через `b.update` -> `UpdateFilters`. Подменю корзин остаётся на экране после выбора (отметка «✓»), прочие значения возвращают к сводке.
- Кнопки города строятся из `TopCities(query без города, 10)`: при выбранных дате, типе и дистанции они не ведут в пустую выдачу. Кнопка с неформатируемым callback пропускается самим `row.add` (T-04-10).
- `onText` без состояния ожидания: любой текст, не команда и не длиннее 64 рун, — запрос города. «Питер» даёт сводку новым сообщением; неизвестный город даёт «В каталоге нет забегов в «…»» с до 5 похожими (`Новгород` -> оба новгорода) и «Назад».
- Пустая выдача: кнопки «Снять дату/город/дистанцию/тип» только для реально заданных полей и «Фильтры».

## Task Commits

Коммитов нет: по правилам владельца коммиты делает он вручную. Файлы по задачам:

**Задача 1 (модель фильтров)** — предлагаемый тип коммита `feat`:
- `internal/bot/filters.go` (модель, `dateRange`, `monthOptions`, подписи, `summaryLine`, изменения)
- `internal/bot/filters_test.go`: `TestDateRange`, `TestBucketMembership`, `TestMonthOptions`, `TestFilterLabels`, `TestFilterMutations`

**Задача 2 (подменю, ввод города, фасеты)** — предлагаемый тип коммита `feat`:
- `internal/store/facets.go`, `internal/store/facets_test.go`
- `internal/bot/filters.go` (`summaryScreen`, `emptyScreen`, меню, `onFilter`, `onText`)
- `internal/bot/filters_test.go`: `TestFilterFlow`, `TestCityText`, `TestEmptyResult`, `TestResetFilters` и хелперы `press`, `textUpdate`

Оба файла `filters.go` и `filters_test.go` общие для двух задач; при желании развести коммиты проще положить их целиком в задачу 1, а задачей 2 закоммитить `facets*.go`.

## Files Created/Modified

- `internal/store/facets.go` — `CityCount`, `TopCities`, `SimilarCities`, `CityName`, `EventTypes`; SQL только константами с `$N`
- `internal/store/facets_test.go` — четыре теста на реальном PostgreSQL
- `internal/bot/filters.go` — модель и экраны фильтров, `onFilter`, `onText`
- `internal/bot/filters_test.go` — чистые тесты модели и сценарии фильтров на фиктивном Bot API с 133 событиями Марафонца

## Decisions Made

- Вместо длинного switch по видам callback в `onFilter` — чистая `mutation(c)`: она возвращает функцию изменения либо nil, и тем же значением решается, менять ли фильтры и какой экран показывать.
- `TopCities` использует общий `listWhere` с обнулённым `CityKey`, а не свою копию WHERE без предиката города: одна правда о видимости событий.
- `SimilarCities` проверяет алфавит и длину регуляркой `^[a-z0-9-]{2,64}$` до запроса; `_` и `%` отсекаются (тест `no_`, `a%b`).
- Подсказка «Нажмите /start» для команд и длинного ввода отправляется без parse_mode (plain), как и «Не понял…».

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - противоречие в плане] Подписи корзин в TestFilterLabels**
- **Found during:** Задача 1
- **Issue:** `<behavior>` ожидает «до 5 км, 30–43 км (марафон)», а `<action>` п.3, D-08 (04-CONTEXT) и таблица `buckets` из 04-01 дают «30–43 км, марафон».
- **Fix:** оставлены подписи D-08 и `buckets`; тест ожидает «до 5 км, 30–43 км, марафон».
- **Files modified:** `internal/bot/filters_test.go`
- **Commit:** не закоммичено (владелец)

**2. [Rule 1 - противоречие в плане] Кнопки подменю типа**
- **Found during:** Задача 2
- **Issue:** `<behavior>` требует «ровно кнопки Шоссе, Трейл, Любой», а `<action>` добавляет [Назад] `f:s`.
- **Fix:** [Назад] оставлена (иначе из подменю нельзя выйти, не меняя фильтр); тест проверяет, что кроме «Назад» кнопок ровно «Шоссе», «Трейл», «Любой».
- **Files modified:** `internal/bot/filters.go`, `internal/bot/filters_test.go`
- **Commit:** не закоммичено (владелец)

**3. [Rule 2 - недостающая проверка] Ключ города длиннее 60 байт в onText**
- **Found during:** Задача 2
- **Issue:** `normalize.CityKey` отдаёт до 64 байт, а кодек `f:c:<key>` принимает до 60; сохранённый ключ длиннее 60 `loadFilters` молча бы отбросил.
- **Fix:** `!validCityKey(key)` в `onText` — ответ «Не понял название города…».
- **Files modified:** `internal/bot/filters.go`
- **Commit:** не закоммичено (владелец)

**Итого:** 3 отклонения (2 противоречия внутри плана, 1 защитное); на цели плана не влияют.

## Known Stubs

Нет. Закрыты все заглушки 04-01, назначенные на 04-02: `dateRange`, подписи и название города в `summaryScreen`, кнопки `emptyScreen`, все виды `f:` в `onFilter`, текстовый ввод города в `onText`.

Остаётся на 04-03 (по плану): зажим страницы, кнопки карточек и пометки статуса в `list.go`, классификация ошибок Telegram и повтор 429 в `tg.go`.

## Issues Encountered

- Первый прогон `TestSimilarCities` упал из-за неверного ожидания порядка в самом тесте (сортировка «число событий, затем ключ»: `novosibirsk` идёт раньше `velikiy-novgorod`); код не менялся, исправлено ожидание.
- Прочих проблем нет. Правки владельца в `internal/ingest/config*.go` не трогались.

## Проверка

- `FILTER MODEL OK` и `FILTERS OK` — обе автоматические проверки плана напечатали маркеры (все перечисленные тесты PASS, SKIP нет, нет `time.Now` в `filters.go`, `normalize.CityKey` используется, SQL вне `internal/store` не найден, `Sprintf` в `facets.go` нет, `go vet` и `gofmt` чисто).
- Полный прогон `TEST_DATABASE_URL=...5433/aggregator_test go test -p 1 -count=1 -timeout 5m -v ./...`: код 0, все пакеты `ok`, 158 строк `--- PASS`, 0 `FAIL`, 0 `SKIP`.
- Тестовая БД: только контейнер `aggregator-test-postgres-1` (порт 5433); dev-БД, сеть Telegram и `.env` не использовались.

## Threat Flags

Новой поверхности сверх `<threat_model>` плана нет. Митигации: T-04-08 (`html.EscapeString` в `summaryLine`, `onText`; `TestCityText` с `<b>Тула&`, `TestFilterLabels`), T-04-09 (`similarRE`, параметры `$N`; `TestSimilarCities` с `a%b` и `no_`), T-04-10 (`row.add` пропускает неформатируемые callback), T-04-11 (одна `UpdateFilters` на нажатие), T-04-12 (ввод длиннее 64 рун — подсказка, текст в лог не пишется).

## Next Phase Readiness

- 04-03 может менять `tg.go`, `list.go` и добавлять `e:`-обработчик: сигнатуры `screen`, `row`, `btn`, `keyboard`, `emptyScreen(f, v)` не менялись; для кнопок карточки пригоден хелпер `btn`.
- Блокеров нет.

## Self-Check: PASSED

- Созданные файлы существуют: `internal/store/facets.go`, `internal/store/facets_test.go`, `internal/bot/filters_test.go`; изменён `internal/bot/filters.go`.
- Коммитов нет по условию запуска; `plan_head_before` = 3467d5b.
- Команды проверки плана (`FILTER MODEL OK`, `FILTERS OK`, полный прогон) выполнены и прошли.

---
*Phase: 04-telegram-search*
*Completed: 2026-09-30*
