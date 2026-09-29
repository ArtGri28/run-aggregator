# Phase 3: First Source - Pattern Map

**Mapped:** 2026-09-29
**Files analyzed:** 24 (new/modified)
**Analogs found:** 17 / 24 (все аналоги проверены как git-tracked: `git ls-files` даёт весь `internal/`, `cmd/`, `migrations/`, `docker-compose.test.yml`, `.env.example`, `.gitignore`)

Кодовая база после Phase 2 маленькая (19 tracked-файлов). Готовые кирпичи: `internal/store` (`events.go`, `store.go`), `internal/normalize` (`normalize.go`, `place.go`, `distance.go`), `internal/event/event.go`, `cmd/aggregator/main.go`, `migrations/00001_init_schema.sql`. Файлов `internal/ingest/**` пока нет, для них аналог берётся из соседних пакетов по стилю (комментарии, ошибки, константы SQL), а логика — из RESEARCH.md (Pattern N / Code Examples N).

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `migrations/00002_ingest_runs_quality.sql` (new) | migration | DDL | `migrations/00001_init_schema.sql` | exact |
| `internal/store/runs.go` (new: StartRun, FinishRun, Baseline, SourceHealth, Save/LoadSnapshot) | model/store | CRUD | `internal/store/events.go` (`EnsureSource`, `SetMatch`) | role-match |
| `internal/store/observe.go` (new: Observe) | model/store | batch (set-based SQL, tx) | `internal/store/events.go` (`SaveEvent` tx-блок) | role-match |
| `internal/store/events.go` (modify: `UpsertSourceEvent` + runID, `SaveEventFor`) | model/store | CRUD | сам файл: `UpsertSourceEvent`, `SaveEvent` | exact |
| `internal/store/store_test.go` (modify: `_test` guard, новые тесты) | test | CRUD | сам файл | exact |
| `internal/normalize/regions.go` (new: `ClassifyRegion`, 89 субъектов) | utility | transform | `internal/normalize/place.go` (`citySynonyms`, `cityLookupKey`, `Country`) | exact |
| `internal/normalize/normalize.go` (modify: `romanRe`), `distance.go` (modify: `5K`, склейка), `place.go` (modify: префиксы, `Country`/ME-04) | utility | transform | сами файлы | exact |
| `internal/normalize/*_test.go` (modify/new `regions_test.go`) | test | transform | `internal/normalize/place_test.go` | exact |
| `internal/ingest/source.go` (new: `SourceEvent`, `RawDoc`, `Policy`, `Source`) | model | transform | `internal/event/event.go` | role-match |
| `internal/ingest/config.go` (new: Config из env) | config | request-response | `cmd/aggregator/main.go` (`databaseURL`) | role-match |
| `internal/ingest/fetch.go` (new: Fetcher) | service | request-response | нет в репо; RESEARCH Code Examples 1, Pattern 8 | no analog |
| `internal/ingest/rawstore.go` (new) | service | file-I/O | нет; RESEARCH Pattern 10 | no analog |
| `internal/ingest/prepare.go` (new: Validate+Normalize, Stats) | service | transform | `internal/normalize/normalize.go` (чистые функции, `now` параметром) | role-match |
| `internal/ingest/quality.go` (new: `Evaluate`, `median`) | utility | transform | `internal/normalize/distance.go` (чистая функция + табличный тест) | role-match |
| `internal/ingest/runner.go` (new: Run, apply, финализация) | service | batch | `internal/store/events.go` (`SaveEvent`: tx, `defer Rollback`, `%w`) | partial |
| `internal/ingest/scheduler.go` (new) | service | event-driven (timer loop) | `cmd/aggregator/main.go` (`runProcess`: ctx.Done) | partial |
| `internal/ingest/report.go` (new: SanityReport) | utility | transform | нет; RESEARCH "Формат санити-отчёта" | no analog |
| `internal/ingest/sources/marathonec/marathonec.go` (new) | service | request-response | нет; RESEARCH Pattern 1 | no analog |
| `internal/ingest/sources/marathonec/parse.go` (new) | utility | transform | нет; RESEARCH Code Examples 2 | no analog |
| `internal/ingest/sources/marathonec/parse_test.go` + `testdata/` (new) | test | file-I/O (golden) | RESEARCH Code Examples 3 (табличный стиль — `place_test.go`) | partial |
| `internal/ingest/*_test.go` (new: fetch/quality/prepare) | test | transform | `internal/normalize/place_test.go` | role-match |
| `cmd/aggregator/main.go` (modify: под-подкоманды worker, slog, `stop()`) | controller/CLI | request-response | сам файл | exact |
| `docker-compose.dev.yml` (new) | config | — | `docker-compose.test.yml` | exact |
| `.env.example`, `.gitignore` (modify) | config | — | сами файлы | exact |
| `go.mod`/`go.sum` (modify: `golang.org/x/net v0.59.0`) | config | — | — | n/a (`go get golang.org/x/net@v0.59.0 && go mod tidy`) |

## Pattern Assignments

### `migrations/00002_ingest_runs_quality.sql` (migration)

**Analog:** `migrations/00001_init_schema.sql` (goose-формат, `-- +goose Up/Down`, индексы `idx_<table>_<cols>`, Down в обратном порядке).

Маркеры и стиль (строки 1, 8-21, 102-110):
```sql
-- +goose Up
CREATE TABLE ingest_runs (... status TEXT NOT NULL CHECK (status IN ('running','ok','suspect','failed')), ... error TEXT);
CREATE INDEX idx_ingest_runs_source_started ON ingest_runs (source_id, started_at DESC);
...
-- +goose Down
DROP TABLE ...;
```
Содержимое 00002 — RESEARCH Pattern 7 дословно (3 колонки `accepted_at`, `metrics JSONB NOT NULL DEFAULT '{}'`, `snapshot JSONB`, частичный `UNIQUE INDEX ux_ingest_runs_running ... WHERE status='running'`; Down: `DROP INDEX`, `DROP COLUMN`). Файл подхватывается `//go:embed *.sql` в `migrations/embed.go` автоматически; ничего регистрировать не надо.

---

### `internal/store/runs.go`, `internal/store/observe.go` (store, CRUD/batch)

**Analog:** `internal/store/events.go`

**Package/imports** (events.go:1-16):
```go
package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aggregator/internal/event"
)
```

**Простая CRUD-функция, SQL константой, `$N`, ошибка с префиксом** (events.go:36-46, копировать для `StartRun`/`FinishRun`/`SourceHealth`):
```go
func (s *Store) EnsureSource(ctx context.Context, code string) (int64, error) {
	const q = `
		INSERT INTO sources (code) VALUES ($1)
		ON CONFLICT (code) DO UPDATE SET code = sources.code
		RETURNING id`
	var id int64
	if err := s.pool.QueryRow(ctx, q, code).Scan(&id); err != nil {
		return 0, fmt.Errorf("ensure source: %w", err)
	}
	return id, nil
}
```

**Not-found через `ErrNotFound` и `RowsAffected`** (events.go:364-373, для `accept-baseline`, `FinishRun`):
```go
cmd, err := s.pool.Exec(ctx, "UPDATE ... WHERE id = $1", ...)
if err != nil { return fmt.Errorf("set match: %w", err) }
if cmd.RowsAffected() == 0 { return fmt.Errorf("set match %d: %w", sourceEventID, ErrNotFound) }
```

**Транзакция** (events.go:283-287; шаблон для `Observe` — три UPDATE из RESEARCH Pattern 5 внутри одной tx; для `StartRun`: сначала закрыть протухшие `running`, затем INSERT, `unique_violation` = «уже идёт прогон»):
```go
tx, err := s.pool.Begin(ctx)
if err != nil { return false, fmt.Errorf("save event: begin: %w", err) }
defer func() { _ = tx.Rollback(ctx) }()
...
if err := tx.Commit(ctx); err != nil { return false, fmt.Errorf("save event: commit: %w", err) }
```
Хелперы `execer`/`queryRower`/`queryRowerMulti` (events.go:24-32, 131-134) принимают и пул, и `pgx.Tx`; новые внутренние функции писать против них.

**Правило:** весь SQL только в `internal/store`, константы с `$N`, никакой конкатенации (`store.go:1-4`). Запрос базы (`Baseline`) и `Observe` SQL берутся из RESEARCH Pattern 3 / Pattern 5 без изменений.

---

### `internal/store/events.go` (modify: `UpsertSourceEvent`, новый `SaveEventFor`)

**Analog:** сам файл.

`UpsertSourceEvent` сейчас (events.go:54-83) возвращает `(id, changed, err)`, комментарий прямо называет ING-08 работой Phase 3. Менять: добавить параметр `runID` (`last_run_id = $6` в INSERT и в `DO UPDATE SET`), вернуть также `event_id`/`match_state` (для идемпотентного apply). Сохранить: CTE `old`/`upsert`, хеш SHA-256 считается внутри, `first_seen_at` не трогается.

`SaveEventFor(ctx, sourceEventID, *Event)` (Pattern 6): одна транзакция; внутри `SELECT ... FOR UPDATE` по `source_events`; если `event_id IS NULL`, взять логику вставки из `SaveEvent` (events.go:294-309: `insertEventSQL` + `replaceDistances`) и `UPDATE source_events SET event_id, match_state='auto'`; иначе взять логику обновления (events.go:311-358: `canonicalFieldsEqual`, `sameDistances`, bump `updated_at` только при изменении). Переиспользовать `canonicalize`, `normalizeDistances`, `replaceDistances`, не дублировать; лучше вынести общий приватный `saveEventTx(ctx, tx, e)` и вызывать его из `SaveEvent` и `SaveEventFor`.

Статус не перезаписывать (Pitfall 10): для существующего события брать `Status` из `GetEvent` (events.go:138-152).

Тесты Phase 2 (`store_test.go:80-…`) вызывают `UpsertSourceEvent` со старой сигнатурой, их надо обновить в том же плане.

---

### `internal/store/store_test.go` (modify)

**Analog:** сам файл.

**TestMain/testStore** (store_test.go:16-56): `TEST_DATABASE_URL` пуст → `os.Exit(m.Run())`; иначе `Open`+`Migrate`; `testStore(t)` делает `t.Skip` и `TRUNCATE ... RESTART IDENTITY CASCADE`. Добавить в `TestMain` после `Open` guard (LO-11), до `Migrate`:
```go
var db string
if err := s.pool.QueryRow(ctx, "SELECT current_database()").Scan(&db); err != nil || !strings.HasSuffix(db, "_test") {
	fmt.Fprintln(os.Stderr, "TEST_DATABASE_URL: имя БД должно оканчиваться на _test"); os.Exit(1)
}
```
Стиль новых интеграционных тестов (идемпотентность, наблюдение, RESEARCH Code Examples 5-6): `s := testStore(t)`, прямые проверки через `testStorePkg.pool.QueryRow(...)`, `t.Fatalf("… = %v, want …")`, без testify.

---

### `internal/normalize/regions.go` (utility, transform)

**Analog:** `internal/normalize/place.go` + `normalize.go`

Стиль справочника: `map`/slice-литерал на уровне пакета с комментарием-ссылкой на решение, ключ поиска приводится общей функцией (place.go:45-60, 102-106):
```go
var citySynonyms = map[string]string{ "москва": "moscow", ... }
func cityLookupKey(s string) string {
	return strings.Join(strings.FieldsFunc(yoToE(strings.ToLower(City(s))), notLetterOrDigit), " ")
}
```
Переиспользовать `yoToE`, `notLetterOrDigit` (normalize.go:14-18) и `collapseSpaces` (place.go:174). `regionKey`/`ClassifyRegion`/`rfSubjects` (89 записей) и `foreignNames` берутся из RESEARCH «Справочник субъектов РФ» и Code Examples 4. Возврат: `(canonical string, kind RegionKind)` с константами `RegionRF|RegionForeign|RegionUnknown` (стиль констант с doc-комментарием — place.go:143-156). Пустая область не `Unknown` (отдельный случай в вызывающем коде).

Тест: табличный, как `place_test.go` (`TestCountry` строка 138), плюс проверка «ровно 89 записей, коллизий ключей нет».

---

### `normalize.go` / `distance.go` / `place.go` (modify, отложенные находки Phase 2)

**Analog:** сами файлы. Точечные правки, решения в RESEARCH «Отложенные находки»:

- `romanRe` (normalize.go:21): сейчас `^M{0,3}(CM|CD|D?C{0,3})(XC|XL|L?X{0,3})(IX|IV|V?I{0,3})$` съедает «MIX». Заменить на ограниченный ≤299 без `M`/`D`; `isEdition` (normalize.go:58-60) не менять по форме.
- `distanceRe` (distance.go:17) и `removeDistances` (distance.go:84): добавить `k/к` только слитно с числом; склейку `100 200 400 м` решать по значениям фикстуры (группа `\d{1,3}(?: \d{3})+`). Верхний предел `MaxPlausibleDistanceM` живёт в `Prepare`, не в `Distances`.
- `cityPrefixes` (place.go:14): порядок `"г.о. "`, `"городской округ "`, `"пос. "`, `"п. "`, `"д. "`, `"с. "` ставить ПЕРЕД `"г."`.
- `Country` (place.go:163-172) и `countryNames` (place.go:123-129): неизвестная явная строка идёт по политике источника, известная иностранная (`foreignNames`) даёт out_of_scope; добавить `russian federation`, `rossiya`. Сохранить сигнатуру `(code string, inScope bool)` или расширить осознанно, потому что её вызывают тесты `TestCountry`.

---

### `internal/ingest/source.go` (model)

**Analog:** `internal/event/event.go` (доменные типы с doc-комментарием на каждый тип, типизированные строковые константы; см. event.go:1-50). `SourceEvent` со строковыми полями и `json`-тегами, `Source` интерфейс, `Policy{CountryFallback normalize.CountryPolicy; CrawlDelay time.Duration}`, `var ErrStructureNotFound = errors.New("структура не найдена")`: RESEARCH Pattern 1 дословно. Порядок полей `SourceEvent` фиксирован (детерминированный `content_hash`).

Импорты: `aggregator/internal/normalize`. Пакет `ingest` не импортирует `store` внутри `source.go`; источник (`sources/marathonec`) импортирует `ingest`, но не наоборот, регистрация в `cmd/aggregator`.

---

### `internal/ingest/config.go` (config)

**Analog:** `cmd/aggregator/main.go:50-59`
```go
func databaseURL() (string, error) {
	dsn, ok := os.LookupEnv("DATABASE_URL")
	if !ok || dsn == "" {
		return "", errors.New("DATABASE_URL не задан")
	}
	return dsn, nil
}
```
Копировать: `os.LookupEnv`, фиксированное русское сообщение, значения секретов не попадают в ошибки. `OWNER_CONTACT` обязателен для `worker`/`run-once`, никогда не логируется. Остальные параметры (`INGEST_INTERVAL`, `INGEST_JITTER`, `INGEST_RUN_TIMEOUT`, `RAW_DIR`, `RAW_TTL`, пороги) имеют значения по умолчанию в коде, парсятся `time.ParseDuration`/`strconv`.

---

### `internal/ingest/prepare.go`, `quality.go` (pure functions)

**Analog:** `internal/normalize/normalize.go` — чистые функции без `time.Now()`, время параметром (`Date(s, now)` normalize.go:113), таблично тестируемые; `internal/normalize/distance.go`. Вызывать существующие `normalize.Date`, `TitleNorm`, `City`, `CityKey`, `Region`, `Type`, `Distances`, `Description`, `Timezone`, `Country`. `Prepare(events, now, pol)` и `Evaluate(Stats, Baseline, Thresholds)`: RESEARCH Pattern 2/3. `Distances` вызывать только по заголовку и столбцу дистанций (LO-06), не по описанию.

---

### `internal/ingest/runner.go`, `scheduler.go`

**Analog:** `cmd/aggregator/main.go:93-115` (`runProcess`): подключение, healthcheck с `context.WithTimeout(ctx, 5*time.Second)`, `slog.Info("started"...)`, `<-ctx.Done()`, `slog.Info("stopping"...)`. Планировщик встаёт на место `<-ctx.Done()`.

Стиль ошибок и tx: `store/events.go` (`fmt.Errorf("<op>: %w", err)`, `defer func(){ _ = tx.Rollback(ctx) }()`). Логика — RESEARCH Pattern 2, 4, 6, 9: чистые стадии целиком → `Evaluate` → запись только при `ok`; apply идемпотентен по всем валидным; финализация под `context.WithoutCancel(ctx)` + свой таймаут; таймер `time.NewTimer` + `select` с `ctx.Done()`; `recover()` в Parse → `failed`.

Логи: `slog` со словарём полей из Pattern 11 (уже используется `slog.Info("migration applied", "version", ..., "duration", ...)` в store.go:73 и `slog.Error("command failed", "subcommand", ..., "error", err)` в main.go:45).

---

### `cmd/aggregator/main.go` (modify)

**Analog:** сам файл.

Текущее: `const usage`, `switch subcommand`, `case "bot", "worker": err = runProcess(ctx, subcommand)` (main.go:19, 32-42), `defer stop()` (28). Менять:
1. До `switch`: `slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))`, `LOG_FORMAT=text` → `NewTextHandler`.
2. `worker` вынести из общего `case` в `runWorker(ctx, os.Args[2:])` со `flag.FlagSet` на под-подкоманды `run-once`, `accept-baseline`, `sanity-report` (Pattern 12); `bot` остаётся на `runProcess`.
3. Убрать зависание на втором Ctrl-C: `stop()` вызвать сразу после `<-ctx.Done()` (LO-09), дождаться `wg.Wait()`.
4. Регистрация источников: `[]ingest.Source{marathonec.New(cfg)}` тут, не в `ingest` (иначе цикл импортов).
5. Обновить `usage`.

Соблюдать паттерн: `databaseURL()` → `store.Open(ctx, dsn)` → `defer s.Close()` → `Healthcheck` (main.go:61-88), ошибки наружу возвращаются из `runX`, а `slog.Error` + `os.Exit(1)` делает только `main`.

---

### `docker-compose.dev.yml` (config)

**Analog:** `docker-compose.test.yml` (комментарий-шапка о назначении, `name:`, `postgres:17`, порт только на `127.0.0.1`, healthcheck `pg_isready`). Отличия (Pattern 13): `name: aggregator-dev`, порт `127.0.0.1:5434:5432`, именованный volume вместо `tmpfs`, `POSTGRES_DB: aggregator_dev`, пароль из окружения `${DEV_POSTGRES_PASSWORD:?задайте в .env}` вместо `POSTGRES_HOST_AUTH_METHOD: trust`.

### `.env.example`, `.gitignore` (modify)

**Analog:** сами файлы. `.env.example` (комментарий про `.env` вне репозитория, пустые значения) дополнить пустыми `OWNER_CONTACT=`, `RAW_DIR=`, `DEV_POSTGRES_PASSWORD=`; шаблон `TEST_DATABASE_URL` оставить. `.gitignore`: добавить строку `/data/` (до первого прогона; Pitfall 13), в стиле существующих блоков с русским комментарием.

---

### `internal/ingest/sources/marathonec/*` и `internal/ingest/fetch.go`, `rawstore.go`, `report.go`

**Аналога в репо нет.** Использовать RESEARCH.md: Pattern 8 + Code Examples 1 (Fetcher/hostGate/retryAfter), Code Examples 2 (обход `x/net/html`: `doc.Descendants()`, `atom.Tr`, префикс `post-row-`, функция `text`), Code Examples 3 (golden-тест с флагом `-update`), Pattern 10 (rawstore: temp + `os.Rename`), «Формат санити-отчёта». Разметка столбцов неизвестна до фикстуры (D-01, задача 1), парсер привязывать к заголовкам/классам, нет ожидаемого столбца → `ErrStructureNotFound`. В `Parse` санитизировать каждую строку: `strings.ToValidUTF8(s, "")` и удаление `\x00` (Pitfall 8).

## Shared Patterns

### SQL только в store, константы с `$N`
**Source:** `internal/store/store.go:1-4`, `internal/store/events.go:37-41, 58-75, 223-244`
**Apply to:** `runs.go`, `observe.go`, `events.go`. Ни `ingest`, ни `cmd` не содержат SQL.

### Ошибки: префикс операции + `%w`, `ErrNotFound`
**Source:** `internal/store/events.go:20, 42-44, 369-371`
```go
return fmt.Errorf("<operation>: %w", err)
return fmt.Errorf("set match %d: %w", id, ErrNotFound)
```
**Apply to:** весь новый код `store` и `ingest`. Проверка `errors.Is(err, pgx.ErrNoRows)` → `ErrNotFound` (events.go:313-315).

### Секреты не в ошибках и логах
**Source:** `internal/store/store.go:24-38` (`Open` возвращает фиксированное сообщение без DSN), `cmd/aggregator/main.go:50-59`
**Apply to:** `config.go`, `fetch.go` (не логировать `OWNER_CONTACT`, User-Agent, тела ответов), `cmd`.

### Чистые функции с `now` параметром
**Source:** `internal/normalize/normalize.go:113` (`Date(s string, now time.Time)`)
**Apply to:** `prepare.go`, `quality.go`, Parse: `time.Now()` только в runner/CLI, стадии получают время аргументом.

### Тесты: таблицы, skip без БД
**Source:** `internal/normalize/place_test.go` (табличные `TestCity`, `TestCountry`), `internal/store/store_test.go:16-56` (`TEST_DATABASE_URL`, `testStore(t)` со `t.Skip` и `TRUNCATE`)
**Apply to:** все новые `_test.go`. Интеграционные тесты `store` пропускаются без `TEST_DATABASE_URL`; добавить `_test`-guard (LO-11).

### Структурные логи `log/slog`
**Source:** `internal/store/store.go:73`, `cmd/aggregator/main.go:45,111,113`
**Apply to:** runner, scheduler, все подкоманды. Единый словарь полей RESEARCH Pattern 11 (`source`, `run_id`, `status`, `reason`, `fetched`, `parsed`, `rejected`, ...). Уровни: `ok` → INFO, `suspect` → WARN, `failed` → ERROR.

## No Analog Found

| File | Role | Data Flow | Reason |
|---|---|---|---|
| `internal/ingest/fetch.go` | service | request-response (HTTP) | В репо нет сетевого кода; брать RESEARCH Code Examples 1 |
| `internal/ingest/rawstore.go` | service | file-I/O | Нет работы с файлами; RESEARCH Pattern 10 |
| `internal/ingest/report.go` | utility | transform (текстовый отчёт) | Нет; RESEARCH «Формат санити-отчёта» |
| `internal/ingest/sources/marathonec/{marathonec,parse}.go` | service/utility | HTML-парсинг | Нет; RESEARCH Pattern 1, Code Examples 2; разметку определяет фикстура (D-01) |
| `internal/ingest/scheduler.go` (цикл, jitter, backoff) | service | timer loop | Только скелет `runProcess`; логика по RESEARCH Pattern 9 |

## Metadata

**Analog search scope:** весь tracked-код репозитория (`cmd/`, `internal/`, `migrations/`, корневые конфиги), 19 файлов.
**Files scanned:** 12 прочитано полностью или в нужных диапазонах (`store/events.go`, `store/store.go`, `store/store_test.go` частично, `normalize/{normalize,place,distance}.go`, `event/event.go`, `cmd/aggregator/main.go`, `migrations/00001_init_schema.sql`, `docker-compose.test.yml`, `.env.example`, `.gitignore`).
**Pattern extraction date:** 2026-09-29
