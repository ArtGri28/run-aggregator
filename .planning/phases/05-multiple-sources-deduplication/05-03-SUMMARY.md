---
phase: 05-multiple-sources-deduplication
plan: 03
subsystem: bot, store
status: complete
tags: [dedup, bot, telegram, tech-debt]
requires: []
provides:
  - "(*Store).RootEventID(ctx, id) (int64, error), maxMergeHops = 32"
  - "sourceNames[\"probeg\"] = \"ПроБЕГ\""
affects: [05-04, 05-05, 05-06]
plan_head_before: 86bdc42
tech-stack:
  added: []
  patterns: ["WITH RECURSIVE с пределом глубины для цепочки merged_into_id", "предел ожидания 429 через ключ ctx"]
key-files:
  created: []
  modified:
    - internal/store/listing.go
    - internal/store/listing_test.go
    - internal/bot/list.go
    - internal/bot/list_test.go
    - internal/bot/filters.go
    - internal/bot/filters_test.go
    - internal/bot/callback_test.go
    - internal/bot/tg.go
    - internal/bot/tg_test.go
    - internal/bot/bot.go
    - internal/bot/bot_test.go
decisions:
  - "RootEventID: один запрос WITH RECURSIVE, глубина ≤ maxMergeHops; последняя строка не корень → ошибка «longer than 32 or cyclic», не ErrNotFound"
  - "onCard: RootEventID → GetEvent(root) → прежний switch; проверка MergedIntoID у корня оставлена как страховка от гонки"
  - "LO-05: сброс прошедшего mYYYYMM при чтении через существующий dateRange (to < today)"
  - "LO-03: retry_after > предела — сразу ошибка; предел maxRetryWait (30 с) или retryWaitKey из ctx; cb кладёт callbackRetryWait (5 с)"
  - "IN-04: privateOnlyMW стал методом Bot, callback из группы → b.answer(ctx, q, \"\")"
metrics:
  completed: 2026-10-01
actuals:
  tokens: 9000
  tasks: 3
  commits: 0
requirements-completed: [DDP-06]
---

# Phase 5 Plan 03: резолвер корня, карточка слитого события, долг Phase 4 в боте

Общий резолвер `store.RootEventID` (рекурсивный CTE с пределом 32 шага, цикл даёт ошибку, а не зависание). Старая кнопка слитого события теперь открывает карточку корня. У карточки появилась подпись «ПроБЕГ». Закрыты LO-03, LO-05, IN-01, IN-02 и IN-04.

## Задачи

1. **Tracer D-16/D-17.3.** `RootEventID` и `maxMergeHops` в `internal/store/listing.go`, только чтение. `onCard` получает корень через `RootEventID`: ErrNotFound → «Событие больше не доступно», другая ошибка → `errNotice`. Затем вызывается `GetEvent(root)` и выполняется прежний switch. В `sourceNames` добавлен `probeg`. Тесты: TestRootEventID (корень, 1 шаг, цепочка c→b→a, 999999, цикл d↔e), TestMergedCardOpensRoot (2 шага, [К списку] сохраняет страницу и версию, скрытый корень даёт gone, строка TRACER), TestCardSourceNames, правка TestHiddenAndMerged (слитое событие открывает корень).
2. **LO-05/IN-01/IN-02.** Новая сигнатура `loadFilters(raw, today)`: если прошедший `mYYYYMM` указывает на месяц, у которого `monthEnd < today`, `Date` сбрасывается в "". `update` вычисляет `today` один раз, `onFilter` и `onList` используют `b.today()`. Сигнатура `emptyScreen(f)` теперь без `v`. Комментарии к блоку изменений и к `toggleBucket` теперь описывают фактическое поведение. Тесты: расширен TestLoadFilters, добавлен TestStaleMonthFilter.
3. **LO-03/IN-04.** Добавлены `callbackRetryWait = 5 * time.Second` и `retryWaitKey`. `withRetry` больше не ждёт и не повторяет, если ожидание больше предела. `cb` передаёт в `fn` ctx с пределом, а `answer` вызывается с исходным ctx. `(*Bot).privateOnlyMW` отвечает пустым answerCallbackQuery на callback из группы. Тесты: TestCallbackRetryLimit, правки TestRateLimitRetry и TestPrivateOnly (обычное сообщение, сообщение из группы, InaccessibleMessage из supergroup).

## Проверка

- `go vet ./internal/store/... ./internal/bot/` чисто; `gofmt -l internal/store internal/bot` пусто.
- Гейт SQL вне internal/store не нашёл ничего; в listing.go нет UPDATE/DELETE/INSERT.
- `TEST_DATABASE_URL=…/agg_w1c_test go test -p 1 -count=1 -timeout 5m -v ./internal/store/... ./internal/bot/`: ok/ok, 125 PASS, без FAIL и SKIP. Обе строки TRACER (`TRACER merged-card hops=2 root=ok hidden-root=gone`) на месте. Прошли все тесты, перечисленные в verify трёх задач.

## Отклонения от плана

1. **[Rule 3] `shortRetry` в tg_test.go: предел 10 мс → 2 с.** Старый хелпер рассчитывал на то, что ожидание обрезается до предела. При новой семантике retry_after 1 с больше 10 мс, поэтому повтор не делался, и подтесты «один повтор» и «два 429 подряд» (по плану «без изменений») падали. Тела подтестов не менялись. Каждый из них теперь реально ждёт 1 с.
2. **Тестовая БД — `agg_w1c_test`** по указанию оркестратора, а не `aggregator_test` из plan verify. `docker compose up` не запускался: контейнер уже работал.
3. **Коммитов нет** (`commits: 0`, все изменения в рабочем дереве). Так требует правило владельца: коммиты он делает вручную.
4. Статусы в 04-REVIEW.md не обновлялись: план этого не требует.

## Known Stubs

Нет.

## Self-Check: PASSED

- Все 11 файлов из files_modified изменены, других production-файлов план не трогал.
- Acceptance-grep: `RootEventID`-сигнатура — 1, `Store.RootEventID(` в list.go — 1, `"probeg": "ПроБЕГ"` — 1, `emptyScreen(f, v)` / «с абсолютным результатом» / `min(wait, maxRetryWait)` / `func privateOnlyMW` — пусто, `callbackRetryWait = 5 * time.Second` — 1, `retryWaitKey{}` в bot.go — 1.
