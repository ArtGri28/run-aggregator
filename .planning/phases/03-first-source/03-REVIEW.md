---
phase: 03-first-source
reviewed: 2026-09-29T20:00:00Z
depth: deep
files_reviewed: 22
files_reviewed_list:
  - cmd/aggregator/main.go
  - internal/ingest/config.go
  - internal/ingest/fetch.go
  - internal/ingest/prepare.go
  - internal/ingest/quality.go
  - internal/ingest/rawstore.go
  - internal/ingest/report.go
  - internal/ingest/runner.go
  - internal/ingest/scheduler.go
  - internal/ingest/source.go
  - internal/ingest/sources/marathonec/marathonec.go
  - internal/ingest/sources/marathonec/parse.go
  - internal/normalize/distance.go
  - internal/normalize/normalize.go
  - internal/normalize/place.go
  - internal/normalize/regions.go
  - internal/store/events.go
  - internal/store/observe.go
  - internal/store/runs.go
  - migrations/00002_ingest_runs_quality.sql
  - docker-compose.dev.yml
  - .env.example
findings:
  critical: 0
  high: 0
  medium: 3
  low: 12
  info: 5
  warning: 3
  total: 20
status: fixed_with_deferrals
fixed: [ME-01, ME-02, ME-03, LO-01, LO-02, LO-04, LO-08, LO-12, W-1, W-3]
deferred: [LO-03, LO-05, LO-06, LO-07, LO-09, LO-10, LO-11, IN-01, IN-02, IN-03, IN-04, IN-05, W-2]
---

# Phase 3: Code Review Report

**Reviewed:** 2026-09-29
**Depth:** deep (цепочки Runner → Store, SQL наблюдения, Fetcher, Parse → Prepare → normalize)
**Files Reviewed:** 22 (плюс .gitignore и go.mod — замечаний нет)
**Status:** issues_found

## Summary

Проверен код Phase 3: конвейер `Fetch → Parse → Prepare → Evaluate → apply → Observe`, `accept-baseline`, планировщик, Fetcher, хранилище сырых ответов, парсер Марафонца, изменения `normalize` и SQL в `internal/store`.

Блокеров уровня critical/high нет. По безопасности (ASVS L1) замечаний нет: весь SQL параметризован (`$N`, конкатенации нет); `OWNER_CONTACT` попадает только в заголовок `User-Agent` и не встречается ни в логах, ни в ошибках, ни в файлах; DSN и `PGPASSWORD` в ошибках не повторяются; сырые ответы пишутся с правами 0600 в каталог 0700, а имя каталога ограничено `^[a-z0-9-]+$`; `registration_url` принимает только http/https; dev-PostgreSQL слушает только 127.0.0.1.

Основные проблемы касаются инварианта MVP §4.4/§5.4: «suspect и failed ничего не применяют; наблюдение меняется только по найденным записям». Три сценария его нарушают: гонка `accept-baseline` с демоном (ME-01), неатомарная запись прогона (ME-03) и отбракованная, но присутствующая на странице строка, которая уходит в `gone` (ME-02). ME-01, ME-02, LO-01 и LO-02 подтверждены одноразовыми тестами на тестовой БД. После проверки тесты удалены, исходный код не изменялся.

## Narrative Findings (AI reviewer)

## Warnings

### ME-01 (medium): `accept-baseline` применяет снимок и наблюдение до проверки, что прогон всё ещё можно принять

**File:** `internal/ingest/runner.go:258-277` (в связке с `internal/store/runs.go:181-195`)
**Issue:** `Accept` проверяет `LastRun` (строки 245-252), затем без блокировки выполняет `apply` (строка 268) и `Observe` (строка 271). Только после этого `AcceptRun` проверяет, что прогон последний (`r.id = max(id)`). Если за это время демон `worker` выполнил `StartRun`, `AcceptRun` возвращает `ErrNotAcceptable`, но каталог уже изменён, `missed_runs++` у ненайденных строк записан, а прогон остаётся `suspect`.
Комментарий `ponytail:` на строках 263-265 предполагает, что следующий прогон ok всё исправит. Для первого принятия это неверно: окно базы пусто, поэтому по D-05 следующий прогон тоже получит `suspect` и ничего не исправит. Кроме того, повторное наблюдение не откатывает увеличенный `missed_runs`, так что преждевременный `gone` возможен.
**Подтверждение:** одноразовый тест вызвал `StartRun` в момент между проверкой и `AcceptRun`. Результат: `Accept err = accept run 6: прогон нельзя принять`, `created=133`, в БД 133 связанных события с `is_listed = true`, у прогона 6 статус `suspect`. D-03 прямо допускает одновременную работу демона и ручных CLI-команд на Mac владельца.
**Fix:** сначала занять прогон, потом писать. Уникальный индекс `ux_ingest_runs_running` уже есть, поэтому достаточно перевести принимаемый прогон в `running`, и демон получит `ErrRunInProgress`:
```go
// store: ClaimForAccept — одна строка, атомарно
UPDATE ingest_runs r SET status = 'running'
WHERE r.id = $1 AND r.status = 'suspect' AND r.snapshot IS NOT NULL
  AND r.id = (SELECT max(id) FROM ingest_runs WHERE source_id = r.source_id)
// 0 строк → ErrNotAcceptable; 23505 → ErrRunInProgress
```
Дальше `apply` и `observe`, затем `AcceptRun` с условием `status = 'running'`. При ошибке прогон нужно вернуть в `suspect`. Другой вариант — `pg_advisory_lock(source_id)` в `Run` (от `StartRun` до `FinishRun`) и в `Accept`.

### ME-02 (medium): строка, которая есть на странице, но отбракована Validate, уходит в `missing` и затем в `gone`, а событие скрывается

**File:** `internal/ingest/prepare.go:96-113`, `internal/ingest/runner.go:288-296`, `internal/store/observe.go:27-35`
**Issue:** `apply` вызывает `UpsertSourceEvent` (и тем самым выставляет `last_run_id`) только для валидных записей. У отбракованной строки (`дата не разобрана`, `дата вне диапазона`) и у ставшей `expired` строки `last_run_id` остаётся старым. Для `observeMissingSQL` она не отличается от исчезнувшей: `missed_runs++`, после 3 прогонов ok и 72 ч строка получает `gone`, а событие — `is_listed = false`. Прогон при этом остаётся `ok`, потому что порог отбраковки 20-30 % позволяет так скрыть до пятой части каталога. MVP §4.4 переводит в `missing` только запись, которая «не найдена в здоровом прогоне», а эта запись найдена.
**Подтверждение:** одноразовый тест заменил дату строки `post-row-60817` на «уточняется». Три прогона получили `ok` (`rejected=1`), затем `missing=1`, `missing=1`, `gone=1`. Итог в БД: `state=gone`, `is_listed=false`, хотя строка по-прежнему в таблице источника. Типичный сценарий: организатор временно заменяет дату текстом, и забег пропадает из поиска.
**Fix:** отмечать как «увиденные» все строки с непустым `external_id`, в том числе отбракованные и `expired`, но payload не менять:
```go
// Prepare: st.SeenIDs = все непустые external_id (включая reject/expired)
// store:
UPDATE source_events SET last_run_id = $2
WHERE source_id = $1 AND external_id = ANY($3)
```
Этот вызов нужно выполнить в `apply` до `Observe`.

### ME-03 (medium): запись прогона неатомарна, поэтому `failed` может оставить каталог применённым наполовину

**File:** `internal/ingest/runner.go:166-173` (`apply` на строках 288-316)
**Issue:** `apply` выполняет от двух до трёх отдельных транзакций на каждую запись (`UpsertSourceEvent`, затем `SetOutOfScope` или `SaveEventFor`). Если на записи N из 133 пропадёт соединение, истечёт `FinalizeTimeout` (5 мин) или случится ошибка БД, прогон будет помечен `failed ("запись: …")`, хотя первые N записей уже созданы или изменены, а их `last_run_id` указывает на `failed`-прогон. То же происходит при сбое `observe` после успешного `apply` (строки 170-173). Это нарушает MVP §5.4: failed-прогон не должен применять ничего. Следующий прогон ok идемпотентно исправит состояние, но если следующие прогоны будут `suspect`, частичное состояние останется. Комментарий в `finalizeCtx` (строки 91-93) обещает, что каталог не окажется «применённым наполовину», но защищает только от отмены контекста, а не от ошибки посередине записи.
**Fix:** выполнять `apply` и `Observe` в одной транзакции `pgx.Tx`. В store `saveEventTx` уже принимает `tx`, поэтому нужен один метод `store.ApplyRun(ctx, sourceID, runID, items, observeParams)`, который открывает транзакцию, вызывает для каждой записи upsert и затем save/out-of-scope в этой транзакции, после этого выполняет наблюдение и `Commit`. При ошибке — `Rollback` и `failed` с чистым каталогом.

## Info

Ниже — находки уровня low и info (формат шаблона: Warnings = high/medium, Info = low/info).

### LO-01 (low): `RawStore.Prune` удаляет файлы текущего прогона, если id прогонов пошли заново

**File:** `internal/ingest/rawstore.go:81-88`
**Issue:** `Prune` оставляет два наибольших `run_id` среди файлов. После пересоздания dev-БД (`docker compose down -v`, D-03) или при общем `RAW_DIR` у двух баз новые id оказываются меньше старых. В результате сразу после `Save` удаляется только что записанный ответ, а старые файлы живут ещё 14 дней по TTL. Требование ING-10 («последний ответ плюс один предыдущий») тогда не выполняется.
**Подтверждение:** одноразовый тест с `Save(56)`, `Save(57)`, `Save(1)` показал, что `1-0.html` не существует.
**Fix:** всегда сохранять runID, переданный в `Save`, например `keep = append(keep, runID)`, или упорядочивать по `ModTime`/`fetched_at` из `.meta.json`, а не по id.

### LO-02 (low): переполнение при разборе `Retry-After` отменяет просьбу сервера подождать

**File:** `internal/ingest/fetch.go:149-150`
**Issue:** `time.Duration(s) * time.Second` переполняется при `s > ~9.2e9`. Одноразовый тест: `Retry-After: 10000000000` → `-2346317h47m…`. Затем `max(backoff, отрицательное)` даёт обычный backoff, и повтор уходит вместо остановки по `MaxRetryWait`.
**Fix:** `if s > int(math.MaxInt64/int64(time.Second)) { return math.MaxInt64, true }`, либо сравнивать `s` с `MaxRetryWait` до умножения.

### LO-03 (low): `Accept` берёт окно дат от момента старта прогона, поэтому при позднем принятии в каталог попадают прошедшие забеги

**File:** `internal/ingest/runner.go:260`
**Issue:** `Prepare(recs, last.StartedAt, …)` при принятии через N дней пропускает строки с датой в `[StartedAt−1д, now−1д)` как валидные и создаёт по ним события с `is_listed = true`. `Observe` их не трогает, потому что у них `start_date < today`. Это расходится с D-11 (прошедшие забеги не попадают в каталог).
**Fix:** передавать в `Prepare` `r.now()`. Если воспроизводимость отчёта важна, отказывать в принятии снимка старше, например, `Interval`.

### LO-04 (low): у принятого прогона в `ingest_runs` остаются счётчики suspect-оценки

**File:** `internal/store/runs.go:181-186`
**Issue:** `AcceptRun` обновляет только `upserted`, `status`, `error`. Значения `rejected`, `out_of_scope` и `metrics` остаются от Prepare по политике на момент suspect. После переключения политики (D-15 → `RUOnlyFallback`) в строке может остаться, например, `out_of_scope = 133`, хотя применено 0. Мониторинг MON-01 обычным SQL покажет неверные цифры, а `metrics` принятого прогона станут базой (`baselineOf`).
**Fix:** передавать в `AcceptRun` `RunFinish` из Prepare при принятии и записывать `rejected`/`out_of_scope`/`metrics` так же, как в `FinishRun`.

### LO-05 (low): индексы столбцов берутся из всех `<th>` таблицы, включая `tfoot` и вложенные таблицы

**File:** `internal/ingest/sources/marathonec/parse.go:76-83`
**Issue:** `findTable` нумерует все потомки `<th>` одним счётчиком. Если Posts Table Pro включит футер с повтором заголовков (опция плагина) или в строках появятся `<th scope=row>`, `cols[*]` укажут на индексы 9-17. Тогда `cell()` вернёт `nil`, каждая строка получит пустое название, и прогон станет `suspect` («доля отбракованных 100 %»), а не `failed` «структура не найдена» (D-02). Каталог не пострадает, но причина будет ложной, а каждый такой прогон сбрасывает ожидающий снимок (`FinishRun` → `clear`).
**Fix:** брать `data-name` только из первой `<tr>` внутри `<thead>` таблицы; если ни одна строка не дала ячейку названия, возвращать `ErrStructureNotFound`.

### LO-06 (low): редиректы выполняются мимо затвора хоста и Crawl-delay

**File:** `internal/ingest/fetch.go:94-99`
**Issue:** `CheckRedirect` разрешает до трёх переходов на тот же хост. `http.Client` выполняет их сразу, без паузы `CrawlDelay` (40 с). Например, `/calendar-beg/` → `/calendar-beg` → … даёт несколько запросов за доли секунды, что противоречит `Crawl-delay: 40` из robots.txt.
**Fix:** `return http.ErrUseLastResponse` и считать 3xx ошибкой без повтора («адрес страницы изменился»), так как URL единственный и фиксированный.

### LO-07 (low): `worker run-once` не соблюдает расписание и Crawl-delay между процессами

**File:** `cmd/aggregator/main.go:219-228`
**Issue:** затвор хоста живёт внутри процесса, а `StartRun` запрещает только одновременные прогоны. Если запустить `run-once` сразу после прогона демона, к Марафонцу уйдут два GET за секунды.
**Fix:** в `run-once` перед `Run` читать `SourceHealth` и отказывать (или ждать), если `LastStartedAt + CrawlDelay > now`.

### LO-08 (low): SIGTERM или Ctrl-C во время загрузки записывается как сбой источника и удваивает интервал

**File:** `internal/ingest/runner.go:118-143`, `internal/ingest/scheduler.go:80-92`
**Issue:** отмена `ctx` во время `Fetch` даёт причину `загрузка: fetch: context canceled` и прогон `failed`. Он попадает в `ConsecutiveFailures`, поэтому после перезапуска следующий прогон будет через 12 ч вместо 6. В Phase 4 такой прогон вызовет ложный алерт «источник упал».
**Fix:** если `ctx.Err() != nil` (отменён родительский контекст, а не `runTimeout`), закрывать прогон отдельной причиной «прерван: остановка процесса» и не считать его в `ConsecutiveFailures`, например по отдельному коду в `error` или по `metrics`.

### LO-09 (low): эвристика разбиения списка ломает длинные дистанции

**File:** `internal/normalize/distance.go:90-96`
**Issue:** проверено: `"1 200 км"` → `[1000 200000]`, `"2 000 км"` → `[2000]` (2 км вместо 2000 км). Любое число с разделителем тысяч больше `MaxPlausibleDistanceM` разбивается на группы, хотя разбивать нужно только списки из нескольких чисел с общей единицей.
**Fix:** разбивать только при единице «м» (случай «100 200 400 м»); для «км» и «миль» число больше предела отбрасывать целиком, `Prepare` посчитает его в `DroppedDistances`.

### LO-10 (low): присоединённая единица «k/к» даёт ложные дистанции

**File:** `internal/normalize/distance.go:20`
**Issue:** проверено: `"Night Run 2K26"` → `[2000]`, `"10к участников"` → `[10000]`. Кроме того, `TitleNorm("Night Run 2K26")` = `"night run 26"`. Затрагивает фолбэк на название при пустом столбце дистанций (`prepare.go:143-146`) и `title_norm`.
**Fix:** после `k/к` требовать не букву и не цифру (сейчас проверяется только отсутствие буквы, `distanceMatches:41`).

### LO-11 (low): ячейка с двумя городами становится одним несуществующим городом

**File:** `internal/ingest/prepare.go:115-116`, `internal/ingest/sources/marathonec/parse.go:133`
**Issue:** в фикстуре две строки: `"Майкоп, Солохаул"` → `city_key = maykop-solohaul`, `"Красная Поляна, Псебай"` → `krasnaya-polyana-psebay`. Фильтр по городу в Phase 4 такие события не найдёт ни по одному из городов.
**Fix:** брать первый город (`strings.Cut(city, ",")`), а весь список сохранять в `payload`; либо считать такие строки отдельным счётчиком, как `MultiRegion`.

### LO-12 (low): каждый переход out_of_scope → в каталоге создаёт новое событие, а старое остаётся скрытым сиротой

**File:** `internal/store/events.go:393-402`, `internal/store/events.go:454-465`
**Issue:** `SetOutOfScope` обнуляет `event_id` и скрывает событие. Если строка затем снова попадает в каталог, `SaveEventFor` создаёт новое событие. Каждое колебание классификации области или политики оставляет в `events` скрытую копию, и Phase 5 придётся учитывать такие дубли.
**Fix:** в `SetOutOfScope` сохранять `event_id` и скрывать событие только через `is_listed`; при возврате в каталог переиспользовать его и заново вычислять `is_listed`.

### IN-01 (info): правило римских цифр удаляет двухбуквенные заглавные слова

**File:** `internal/normalize/normalize.go:22`, `:62-68`
**Issue:** проверено: `"XL Трейл 2026"` → `"трейл"`, `"CI Run Москва"` → `"run москва"`, `"LIV марафон"` → `""` (пустой `title_norm`). Влияет только на дедуп в Phase 5.
**Fix:** для слов из двух и более букв без кириллицы в названии требовать соседства с кириллицей или ограничиться I-XXX.

### IN-02 (info): сдвиг в `backoff` может переполниться при большом `FETCH_ATTEMPTS`

**File:** `internal/ingest/fetch.go:133`
**Issue:** `BackoffBase << (retry-1)` при `retry ≳ 34` может дать малое положительное значение в обход потолка. Возможно только при неудачной настройке.
**Fix:** ограничить сдвиг: `if retry > 30 { ceil = BackoffMax }`.

### IN-03 (info): после падения процесса планировщик до 45 минут ежеминутно пишет ERROR

**File:** `internal/ingest/scheduler.go:51-58`
**Issue:** если осталась строка `running` моложе `StaleAfter`, каждый цикл получает `ErrRunInProgress`, делает паузу `retryPause` (1 мин) и повторяет. К сайту запросов нет, но в логе копится около 45 строк ERROR.
**Fix:** при `errors.Is(err, store.ErrRunInProgress)` ждать до `started_at + StaleAfter` и логировать на уровне INFO.

### IN-04 (info): `FETCH_MAX_BYTES = MaxInt64` приводит к переполнению `MaxBytes+1`

**File:** `internal/ingest/fetch.go:236`, `internal/ingest/config.go:64`
**Issue:** `LimitReader` с отрицательным N читает 0 байт, поэтому тело пустое и прогон получает `failed` «структура не найдена» с ложной причиной.
**Fix:** в `LoadConfig` ограничить `FETCH_MAX_BYTES` разумным максимумом (например, 100 МБ).

### IN-05 (info): `RAW_DIR` по умолчанию относительный

**File:** `internal/ingest/config.go:38`
**Issue:** `data/raw` зависит от текущего каталога. `.gitignore` закрывает только `/data/` в корне репозитория, и запуск бинарника из подкаталога репозитория положит сырой HTML в неигнорируемый путь.
**Fix:** требовать абсолютный `RAW_DIR` или приводить путь через `filepath.Abs` и логировать итоговый каталог при старте.

## Fixes applied

Исправлено по списку владельца (2026-09-29). W-1 и W-3 — из `03-VERIFICATION.md`. Для каждого исправления есть регрессионный тест. До исправления тест падал (RED), после — проходит (GREEN). Прогон на тестовой БД: `go test -p 1 -count=1 ./...`, 85 тестов верхнего уровня. Без `TEST_DATABASE_URL`: 54 PASS, 31 SKIP. Миграция не понадобилась. Фикстура не менялась, golden-файлы тоже.

| Находка | Исправление | Тест |
|---|---|---|
| ME-01 | `Accept` выполняется одной транзакцией (`Store.InTx`). Первым шагом `ClaimRun` переводит прогон в `running` (условия прежние: `suspect`, есть снимок, прогон последний). Затем идут запись и наблюдение, в конце — `FinishRun` с `Accepted`. Если демон начал прогон раньше, возвращается `ErrNotAcceptable` и ничего не записывается. `StartRun`, начатый позже, ждёт конца транзакции на `ux_ingest_runs_running`. `AcceptRun` удалён, комментарий `ponytail:` о гонке тоже | `TestAcceptRefusedWritesNothing`: `StartRun` вызывается между проверкой и записью. RED: `Created 0, Unchanged 10`, то есть отказ записал каталог. GREEN: `Created 10` |
| ME-02 | Новый метод `Store.MarkSeen` выставляет `last_run_id` всем строкам прогона с непустым `external_id`, включая отбракованные и `expired`; payload не меняется. Вызывается в `Runner.write` до `Observe` | `TestRunRejectedRowStaysSeen`. RED: `Gone:2` (строка с датой «уточняется» и строка с прошедшей датой). GREEN: `Missing 0, Gone 0` |
| ME-03 | `Runner.write` (apply → MarkSeen → Observe) выполняется в одной транзакции `Store.InTx`. `Store` получил поле `db`: это либо пул, либо `pgx.Tx`. Внутренние `Begin` методов внутри `InTx` становятся точками сохранения. При ошибке всё откатывается, счётчики записи обнуляются, прогон получает `failed` | `TestRunWriteFailureAppliesNothing`: NUL в payload последней записи. RED: у failed-прогона `upserted 10`, названия изменены. GREEN: `upserted 0`, следующий прогон — `Changed 0, Unchanged 10` |
| LO-12 | `SetOutOfScope` сохраняет `event_id` и скрывает событие через `is_listed` (`listEventSQL`). `SaveEventFor` для строки, вернувшейся из `out_of_scope`, использует прежнее событие, ставит `match_state = 'auto'` и пересчитывает `is_listed`. `Observe` не переводит `out_of_scope` в `missing` и не показывает их события | `TestOutOfScopeReturnReusesEvent` (store). RED: `created true, id 2`. GREEN: прежнее событие, `true:auto`, событий 1. `TestSetOutOfScope` обновлён: теперь `event_id` сохраняется (`out_of_scope:false`) |
| W-1 / LO-04 | При принятии `FinishRun` записывает `fetched`/`parsed`/`rejected`/`out_of_scope`/`upserted` и `metrics` (с `created`/`changed`/`missing`/…) по политике на момент принятия. `store.Run` дополнен полями `Rejected`, `OutOfScope`, `Metrics` | `TestAcceptAfterPolicySwitch` (расширен). RED: `out_of_scope 133`, в `metrics` нет `created`. GREEN: 0 и `created 133` |
| W-3 | Защита в `internal/ingest` (`openTestStore`) проверяет `Store.DatabaseName()`, то есть `current_database()`, а не путь URL | `TestTestDBGuard`: `…/aggregator_test?dbname=postgres`. RED: `err = nil`. GREEN: отказ «_test». Запуск пакета с таким URL завершается до миграции |
| LO-02 | `retryAfter` ограничивает секунды `math.MaxInt64/time.Second`, значение не бывает отрицательным | `TestRetryAfter`, случай `10000000000`. RED: `-2346317h47m…`. GREEN: предел |
| LO-01 | `RawStore.Prune(source, current, now)` не удаляет файлы прогона `current`, их передаёт `Save` | `TestRawStoreKeepsCurrentRun` (`Save` 56, 57, 1). RED: `1-0.html` нет. GREEN: файл есть |
| LO-08 | Если отменён родительский `ctx` (не `runTimeout`), прогон закрывается с причиной `store.RunInterrupted` («прерван: остановка процесса»). `SourceHealth` не считает такие прогоны в `ConsecutiveFailures` | `TestRunCancelledIsNotFailure`. RED: `failed: загрузка: fetch: context canceled`, `ConsecutiveFailures 1`. GREEN: 0 |

Ограничения:
- Прогон, закрытый `StartRun` как брошенный («прерван: процесс остановился»), по-прежнему считается сбоем. LO-08 касается только отмены контекста.
- Скрытые копии событий, которые могли остаться в dev-БД от прежних переходов `out_of_scope` → в каталог, не связываются заново: `event_id` у таких строк уже потерян. Dev-БД не проверялась.

## Deferred to Phase 5

Не исправлялось по решению владельца:
- LO-03 — окно дат при позднем `accept-baseline`.
- LO-05 — индексы столбцов из всех `<th>`.
- LO-06 — редиректы мимо затвора хоста.
- LO-07 — Crawl-delay между процессами (`run-once` рядом с демоном).
- LO-09 — «1 200 км» / «2 000 км».
- LO-10 — «2K26», «10к участников».
- LO-11 — ключ из двух городов.
- IN-01 — римские цифры.
- IN-02 — сдвиг в `backoff`.
- IN-03 — ERROR каждую минуту при брошенном `running`.
- IN-04 — `FETCH_MAX_BYTES = MaxInt64`.
- IN-05 — относительный `RAW_DIR`.
- W-2 (из `03-VERIFICATION.md`) — нет проверки объёма в окне из 1–2 прогонов. Это решение D-05, изменений нет.

---

_Reviewed: 2026-09-29_
_Depth: deep_
