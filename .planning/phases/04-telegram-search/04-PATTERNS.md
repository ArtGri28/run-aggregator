# Phase 4: Telegram Search - Pattern Map

**Mapped:** 2026-09-30
**Files analyzed:** 27 (новые/изменяемые)
**Analogs found:** 21 / 27 (все аналоги — tracked-файлы, проверено `git ls-files`)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `cmd/aggregator/main.go` (изм.: `runBot`, `newSources`, `healthcheck`) | entrypoint | event-driven | он же: `runProcess`, `runWorker` (строки 140-239) | exact |
| `internal/store/users.go` (`UpdateFilters`, `GetFilters`) | store | CRUD (tx, FOR UPDATE) | `internal/store/runs.go` (`StartRun`, `ClaimRun`) + `InTx` в `store.go` | role-match |
| `internal/store/listing.go` (`ListEvents`, `CountEvents`, `TopCities`, `SimilarCities`, `EventTypes`, `EventSources`) | store | request-response (read) | `internal/store/events.go` (`GetEvent`, `fetchDistances`, `selectEventCols`) | role-match |
| `internal/store/alerts.go` (`source_alerts` get/upsert/delete) | store | CRUD | `internal/store/runs.go` (`LastRun`, `FinishRun`) | role-match |
| `internal/store/runs.go` (изм.: `LatestFinishedRuns`, `RecentRuns`, `RunSource`) | store | request-response (read) | `SourceHealth`, `LastRun` в том же файле | exact |
| `internal/store/store_test.go` (изм.: `TRUNCATE` + новые тесты) | test | integration | он же: `testStore`, `TestMain` | exact |
| `migrations/00003_source_alerts.sql` | migration | DDL | `migrations/00002_ingest_runs_quality.sql` | exact |
| `internal/event/` (+ `Filter`) | model | transform | `internal/event/event.go` | role-match |
| `internal/bot/config.go` (`TELEGRAM_BOT_TOKEN`, `ADMIN_TG_IDS`) | config | env read | `internal/ingest/config.go` (`LoadConfig`, `setValue`) + `databaseURL` в `main.go` | exact |
| `internal/bot/config_test.go` | test | unit | `internal/ingest/config_test.go` | exact |
| `internal/bot/bot.go` (сборка, middleware, recover) | service | event-driven (long polling) | нет в репо; RESEARCH Pattern 1 | no analog |
| `internal/bot/callback.go` (кодек `f:/p:/e:/r:`) | utility | transform | нет; RESEARCH Pattern 2 | no analog |
| `internal/bot/filters.go`, `screens.go` (чистые функции экран -> текст+клавиатура) | utility | transform | `internal/ingest/report.go` (`SanityReport`, форматирование) | partial |
| `internal/bot/handlers.go`, `tg.go` | controller | request-response | нет; RESEARCH Pattern 5-7, 9 | no analog |
| `internal/bot/*_test.go` (кодек, экраны, обработчики на httptest) | test | unit | `internal/ingest/*_test.go` (табличные) | role-match |
| `internal/alert/decide.go`, `loop.go` | service | batch/periodic | `internal/ingest/runner.go` `Loop` + `scheduler.go` | role-match |
| `internal/alert/*_test.go` | test | unit (таблица) | `internal/ingest/scheduler_test.go` | role-match |
| `Dockerfile`, `.dockerignore` | config | build | нет в репо; RESEARCH Pattern 10 | no analog |
| `docker-compose.yml` (prod) | config | infra | `docker-compose.dev.yml` | role-match |
| `.env.example` (изм.) | config | docs | он же | exact |
| `.gitignore` (проверка) | config | - | он же (`.env`, `!.env.example`) | exact |
| `deploy/ship.sh`, `deploy/oneliner.sh` | script | file-I/O (Mac) | нет; RESEARCH Pattern 11 | no analog |
| `deploy/vps/host-preflight.sh`, `host-apply.sh`, `host-verify.sh`, `deploy-preflight.sh`, `deploy-apply.sh`, `deploy-verify.sh`, `rollback.sh` | script | request-response (PREFLIGHT/APPLY/VERIFY) | `01-COMMANDS.md` (`rd.sh`, `vps_audit.sh`), MVP §7.4 | role-match |
| `deploy/README.md` (SHA256-однострочники) | docs | - | `01-COMMANDS.md` §0 (правила) | role-match |

## Pattern Assignments

### `cmd/aggregator/main.go` (entrypoint, event-driven)

**Analog:** сам файл. Каркас `bot` уже есть: `case "bot": err = runProcess(ctx, subcommand)` (строки 62-63), `runProcess` (140-153) заменить на `runBot`, остальное не трогать.

**Открытие БД** (строки 121-138) — вызывать как есть:
```go
s, err := openStore(ctx)   // databaseURL() + store.Open + Healthcheck за 5 с
if err != nil { return err }
defer s.Close()
```

**Реестр источников** (строка 184) — вынести в `newSources(f *ingest.Fetcher) []ingest.Source`; бот вызывает `newSources(nil)`:
```go
sources := []ingest.Source{marathonec.New(fetcher)}
```

**Runner для `Accept` в боте** — скопировать сборку из строк 178-211, но `ingest.LoadConfig(false)` (без OWNER_CONTACT) и обязательно перенести `GoneAfterRuns`/`GoneAfter`:
```go
cfg, err := ingest.LoadConfig(false)
runner := &ingest.Runner{Store: s, Raw: &ingest.RawStore{Dir: cfg.RawDir, TTL: cfg.RawTTL, Keep: 2},
    Thresholds: cfg.Thresholds, RunTimeout: cfg.RunTimeout,
    GoneAfterRuns: cfg.GoneAfterRuns, GoneAfter: cfg.GoneAfter,
    Now: time.Now, Log: slog.Default()}
```

**Жизненный цикл / логи** (строки 46-52, 149-151): `ctx` из `signal.NotifyContext` уже приходит в `runX`; `slog.Info("started", "subcommand", ...)` / `"stopping"`. `b.Start(ctx)` блокируется до отмены ctx; `alert.Loop` — отдельная горутина с тем же ctx.

**Ошибки:** `main` печатает `slog.Error("command failed", ...)` и `os.Exit(1)`. Токен не должен попасть в `err` (см. Shared Patterns / Secrets).

**Импорты** (строки 6-23): stdlib, пустая строка, затем `"aggregator/internal/..."`. Модуль называется `aggregator`. Новый импорт `_ "time/tzdata"` добавить здесь (для `Europe/Moscow`).

---

### `internal/store/users.go` (store, CRUD с транзакцией)

**Analog:** `internal/store/runs.go` + `InTx` (`store.go` 53-66).

**Транзакционный паттерн** (`runs.go` 78-106, 112-147): `tx, err := s.db.Begin(ctx)` -> `defer func() { _ = tx.Rollback(ctx) }()` -> запросы через `tx.QueryRow/Exec` -> `tx.Commit(ctx)`; ошибки оборачиваются `fmt.Errorf("start run: insert: %w", err)`. Для `UpdateFilters` использовать `s.InTx(ctx, func(tx *Store) error {...})` — внутри `tx.db.QueryRow(...)`.

**Константа SQL с $N, не конкатенация** (`runs.go` 123-129):
```go
const q = `
    UPDATE ingest_runs
    SET status = $2, ...
    WHERE id = $1 AND status = 'running'
    RETURNING source_id`
err = tx.QueryRow(ctx, q, runID, ...).Scan(&sourceID)
```

**Not-found:** `errors.Is(err, pgx.ErrNoRows)` -> `fmt.Errorf("...: %w", ErrNotFound)` (`runs.go` 158-160; `ErrNotFound` объявлен в `events.go:20`).

**Схема:** `users(tg_id, filters jsonb, filters_v int, created_at, last_seen_at)` (`00001_init_schema.sql`, строки 90-96). Писать только эти колонки (BOT-08).

---

### `internal/store/listing.go` (store, request-response read)

**Analog:** `internal/store/events.go` — `GetEvent` (141-158), `fetchDistances` (116-132), `selectEventCols` (88-94).

**Чтение с Query/CollectRows** (`runs.go` 175-187):
```go
rows, err := s.db.Query(ctx, q, sourceID, limit)
if err != nil { return nil, fmt.Errorf("baseline runs: %w", err) }
out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (BaselineRun, error) {
    var b BaselineRun
    err := row.Scan(&b.Parsed, &b.Metrics)
    return b, err
})
```

**Общий WHERE** — Go-константа (компилируется склейкой констант; это допустимо, остаётся константой с `$N`). Готовый SQL — `04-RESEARCH.md` Pattern 4 (предикат `is_listed AND merged_into_id IS NULL` дословно как в частичных индексах). Ловушка: передавать `[]int32{}`, не `nil` (pgx кодирует nil как NULL).

**Карточка:** `GetEvent(ctx, id)` уже возвращает `event.Event` с `Distances` — использовать как есть, добавить проверку `IsListed && MergedIntoID == nil` в обработчике.

---

### `internal/store/alerts.go` (store, CRUD)

**Analog:** `internal/store/runs.go` (`LastRun` 150-165 — сканирование в struct и ErrNotFound; `FinishRun` — UPSERT-стиль запросов). Тип `State{SourceID, State, RunID, NotifiedAt, RemindedAt}`, доки на русском в стиле `runs.go`. Запрос "последний завершённый прогон на источник" и `/runs` — SQL в RESEARCH Pattern 8/9; добавить в `runs.go` рядом с `SourceHealth`.

Использовать константу `RunInterrupted` (`runs.go:23`) в `alert.decide`: прерванный прогон -> `None`.

---

### `migrations/00003_source_alerts.sql` (migration)

**Analog:** `migrations/00002_ingest_runs_quality.sql` (полностью):
```sql
-- +goose Up
ALTER TABLE ingest_runs
    ADD COLUMN accepted_at TIMESTAMPTZ,                        -- ручное принятие (граница окна базы)
    ...
-- +goose Down
DROP INDEX ux_ingest_runs_running;
ALTER TABLE ingest_runs DROP COLUMN snapshot, ...;
```
Формат: `-- +goose Up` / `-- +goose Down`, комментарии на русском у колонок, Down зеркальный. Текст таблицы — `04-RESEARCH.md` Pattern 8. Файл подхватывает `//go:embed *.sql` (`migrations/embed.go`) автоматически; править `embed.go` не нужно.

---

### `internal/store/store_test.go` (test, integration)

**Analog:** сам файл. `TestMain` (16-50) — защита `current_database()` с суффиксом `_test`, `Migrate`, skip без `TEST_DATABASE_URL`. `testStore(t)` (52-63) — добавить `source_alerts` в `TRUNCATE`:
```go
const q = `TRUNCATE TABLE sources, ingest_runs, source_events, events,
    event_distances, dedup_candidates, users, heartbeats RESTART IDENTITY CASCADE`
```
Новые тесты: `s := testStore(t); ctx := context.Background()` и т. д. как в `TestEnsureSource`. Запуск: `go test -p 1`.

---

### `internal/bot/config.go` + `config_test.go` (config)

**Analog:** `internal/ingest/config.go`.

**Правило ошибок без значений** (`config.go` 97-135, 71-79): `fmt.Errorf("%s: %w", v.name, err)` где `err` — фиксированный текст ("неверное значение"), значение не повторяется. Для `ADMIN_TG_IDS`: `strconv.ParseInt` по элементам, ошибка называет переменную (`"ADMIN_TG_IDS: неверное значение"`), не элемент.

**Отсутствие обязательного:** `errors.New("OWNER_CONTACT не задан (нужен для User-Agent)")` -> `errors.New("TELEGRAM_BOT_TOKEN не задан")`; `databaseURL()` в `main.go:84-90` — тот же шаблон `os.LookupEnv` + проверка пустоты.

**Тест** (`config_test.go` 1-25): список переменных `configVars` очищается `t.Setenv(k, "")` в начале, чтобы не зависеть от окружения; подтест "ошибка не содержит значение" — по образцу существующих проверок `strings.Contains(err.Error(), secret)`.

---

### `internal/event/` (+ `Filter`) (model, transform)

**Analog:** `internal/event/event.go` — типы `event.Type` (`TypeRoad`, `TypeTrail`, ...), `event.Status` (`StatusScheduled|StatusCancelled|StatusPostponed`). Использовать эти константы для подписей и пометок `[ОТМЕНЁН]`/`[ПЕРЕНЕСЁН]`, не строковые литералы. Модель фильтра/корзин/пресетов дат — RESEARCH Pattern 3; чистые функции с фиксированным `today` для тестов.

**Город:** приводить ввод через `normalize.CityKey(q)` (`internal/normalize/place.go:81`), `""` = не понял. Не дублировать словарь синонимов.

---

### `internal/alert/decide.go`, `loop.go` (service, periodic)

**Analog:** `internal/ingest/runner.go` `Loop` (вызывается `runner.Loop(ctx, sources, ingest.Schedule{...})`, main.go:216) и `internal/ingest/scheduler.go`. Копировать: цикл с `ctx.Done()`, логгер `slog.Default()`, заменяемые часы `Now func() time.Time` (как в `Runner.Now`) для тестов. Тикер — `time.NewTicker(time.Minute)` + `select`. Чистая функция `decide` — RESEARCH Pattern 8; табличный тест по образцу `scheduler_test.go`.

**Порядок:** отправить -> записать `source_alerts`; при ошибке Telegram только `slog.Warn` и повтор на следующем тике.

---

### `docker-compose.yml` (prod) (config, infra)

**Analog:** `docker-compose.dev.yml`.

**Что скопировать** (строки 6-26): `name:` проекта, сервис `postgres` на `postgres:17`, `POSTGRES_USER: aggregator`, пароль только из `${VAR:?сообщение}`, healthcheck через `pg_isready -h 127.0.0.1`, именованный том, русские комментарии в шапке о том, что и почему.
```yaml
POSTGRES_PASSWORD: ${DEV_POSTGRES_PASSWORD:?задайте DEV_POSTGRES_PASSWORD в .env}
healthcheck:
  test: ["CMD-SHELL", "pg_isready -h 127.0.0.1 -U aggregator -d aggregator_dev"]
```
**Отличия:** `name: aggregator` (не `-dev`), НЕТ `ports:` (в dev — `127.0.0.1:5434:5432`), БД `aggregator`, добавить `migrate`/`bot`/`worker` и `logging` 10m x 3. Шапку dev-файла ("Это не production-состав... появляется в Phase 6") обновить: production переехал в Phase 4. Полный текст файла — RESEARCH Pattern 10 (проверен локально).

`docker-compose.test.yml` не трогать.

---

### `.env.example` (config)

**Analog:** сам файл. Формат: русские комментарии над переменной, реальные значения не хранятся, необязательные закомментированы (`# INGEST_INTERVAL=`). Добавить `TELEGRAM_BOT_TOKEN`, `ADMIN_TG_IDS`, `POSTGRES_PASSWORD`, `IMAGE_TAG` без значений; `.gitignore` уже содержит `.env`, `.env.*`, `!.env.example` — менять не нужно. `.env` не читать.

---

### `deploy/vps/*.sh`, `deploy/oneliner.sh`, `deploy/README.md` (script, PREFLIGHT -> APPLY -> VERIFY)

**Analog (подход):** `.planning/phases/01-source-discovery-vps-audit/01-COMMANDS.md` и `MVP.md` §7.4 (решение 33). Сами скрипты Phase 1 (`run_block.sh`, `vps_audit.sh`) в репозитории не лежат как файлы: блоки живут внутри `01-COMMANDS.md` (`bash <имя>`), обёртка вне репо. Поэтому для Phase 4 это скорее "по образцу", чем копирование кода.

**Что перенять:**
- `set -u`, `export LC_ALL=C` в шапке (блок `rd.sh`, строки ~36-40 `01-COMMANDS.md`);
- вывод не должен содержать секреты и IP; значения `.env` не печатаются, только права/наличие ключей;
- PREFLIGHT только читает, изменения VPS только APPLY и только по отдельному разрешению владельца;
- проверка целостности по sha256 (в Phase 1 — `approve` по хешу; в Phase 4 — однострочник `sha256sum -c` + `STOP: HASH MISMATCH`, RESEARCH Pattern 11);
- правила п. 0 (`01-COMMANDS.md`): запускать в `bash`, скрипт как файл, вывод в `tee` вне репозитория, без параллельного запуска.

**Не копировать:** `blk`/`awk`-извлечение из markdown и allowlist-обёртку (в Phase 4 скрипты обычные файлы в `deploy/`). Проверка: `bash -n`, `shellcheck` при наличии.

---

## Shared Patterns

### Секреты только из окружения, ошибки без значений
**Source:** `cmd/aggregator/main.go:81-90` (`databaseURL`), `internal/store/store.go:38-48` (`Open`), `internal/ingest/config.go:97-135`
**Apply to:** `internal/bot/config.go`, `bot.go`, `alert`, все `deploy` скрипты
```go
// Its value is never logged or included in returned errors (T-02-01)
cfg, err := pgxpool.ParseConfig(dsn)
if err != nil {
    return nil, fmt.Errorf("DATABASE_URL: не удалось разобрать строку подключения")
}
```
Токен бота: не логировать `err` внешних HTTP-вызовов без просмотра; свой `WithDefaultHandler` и `WithErrorsHandler(slog)` (библиотека по умолчанию логирует весь апдейт, RESEARCH Pitfall 2).

### SQL только в `internal/store`, константы с `$N`
**Source:** `internal/store/store.go:1-5`, `runs.go:123-129`
**Apply to:** `users.go`, `listing.go`, `alerts.go`, `runs.go`. `internal/bot` и `internal/alert` SQL не содержат, работают через методы `*store.Store`.

### Обёртка ошибок и sentinel-ошибки
**Source:** `internal/store/runs.go:13-18`, `events.go:20`
```go
var ErrRunInProgress = errors.New("по источнику уже идёт прогон")
var ErrNotAcceptable = errors.New("прогон нельзя принять")
return fmt.Errorf("claim run %d: %w", runID, ErrNotAcceptable)
```
**Apply to:** обработчик `r:y:<id>` в боте: `errors.Is(err, store.ErrNotAcceptable)` / `store.ErrRunInProgress` -> человекочитаемое сообщение (RESEARCH Pattern 9). `errors.AsType[*pgconn.PgError]` (Go 1.27) уже используется в `runs.go:96,203`.

### Структурные логи `slog`
**Source:** `cmd/aggregator/main.go:34-39, 76`
**Apply to:** весь новый код. `slog.Default()` (или `slog.Info/Warn/Error`), без `log.Printf`; в логах только тип апдейта, префикс callback, код ошибки (BOT-08), не текст сообщений и не имя пользователя.

### Тесты на реальной PostgreSQL 17
**Source:** `internal/store/store_test.go:16-63`
**Apply to:** `internal/store` (новые методы), интеграционные тесты `alert`/`bot`, если они берут БД. Защита `_test`, `-p 1`, skip без `TEST_DATABASE_URL`.

### Язык и стиль
Комментарии и тексты ошибок на русском, doc-комментарии у экспортируемого начинаются с имени (`// Run — строка ingest_runs.`). Импорты: stdlib -> пустая строка -> сторонние -> `aggregator/...`.

## No Analog Found

Использовать RESEARCH.md (номера паттернов):

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/bot/bot.go`, `handlers.go`, `tg.go` | service/controller | long polling | нет Telegram-кода; Pattern 1, 5, 6, 7, 9 (включая `recoverMW`, `classify(err)`) |
| `internal/bot/callback.go` | utility | transform | нет кодека; Pattern 2 (строгий `Parse`, тест длины <= 64 байт) |
| `Dockerfile`, `.dockerignore` | config | build | нет Dockerfile в репо; Pattern 10 (узкий `COPY`, чтобы `.env` не попал в образ) |
| `deploy/ship.sh` | script | file-I/O | образа/доставки ещё нет; D-05, Pattern 11 |
| `httptest`-фиктивный Bot API для тестов | test | request-response | нет в репо; RESEARCH "Code Examples" |
| зависимость `go-telegram/bot` v1.27.0 | dep | - | `go get github.com/go-telegram/bot@v1.27.0 && go mod tidy`; `go.sum` получит 2 строки (хеши в RESEARCH Standard Stack) |

## Metadata

**Analog search scope:** `cmd/aggregator`, `internal/{store,ingest,normalize,event}`, `migrations`, `docker-compose.{dev,test}.yml`, `.env.example`, `.gitignore`, `.planning/phases/01-*/01-COMMANDS.md`, `MVP.md` §7.4
**Files scanned:** 42 tracked (`git ls-files`); прочитаны целиком: `main.go`, `store.go`, `runs.go`, `ingest/config.go`, `docker-compose.dev.yml`, `00002_*.sql`, `embed.go`; частично: `events.go`, `store_test.go`, `runner.go`, `config_test.go`, `01-COMMANDS.md`
**Проверка tracked-source:** все пути аналогов в `git ls-files`; зеркал `.gsd` нет
**Pattern extraction date:** 2026-09-30
