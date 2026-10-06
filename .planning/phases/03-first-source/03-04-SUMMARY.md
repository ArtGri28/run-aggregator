---
phase: 03-first-source
plan: 04
subsystem: store (прогоны, наблюдение) + ingest (Runner.Run с оценкой качества, Accept)
tags: [ingest-runs, quality, observe, accept-baseline, lo-11, d-03, d-05, d-06, d-09, ing-06, ing-07, ing-08, ing-13, mon-01]
status: complete

requires:
  - "03-02: Runner, SaveEventFor, SourceEvent (RegionSlugs/Regions), фикстура"
  - "03-03: Prepare/Stats (MultiRegion, Unrecognized, Metrics()), Evaluate, DefaultThresholds, median"
  - "Phase 2: схема 00001 (ingest_runs, source_events.last_run_id/state/missed_runs, events.is_listed)"
provides:
  - "migrations/00002_ingest_runs_quality.sql — accepted_at, metrics, snapshot, ux_ingest_runs_running"
  - "internal/store/runs.go — StartRun, FinishRun, LastRun, BaselineRuns, AcceptRun, SourceHealth, ErrRunInProgress, ErrNotAcceptable"
  - "internal/store/observe.go — Observe, ObserveStats (active/missing/gone, is_listed)"
  - "internal/store/events.go — UpsertSourceEvent(+runID → last_run_id), SetOutOfScope"
  - "internal/ingest/runner.go — Run: чистые стадии → Evaluate → запись только при ok; Accept (D-06); логи run started/finished/accepted, record rejected, unrecognized region"
  - "guard LO-11 в TestMain internal/store: current_database() должна оканчиваться на _test"
affects: [03-05-fetch-cli, 03-06-first-run, 03-07-decision, phase-4-bot-alerts]

actuals:
  tokens: 17900   # chars/4: новые файлы 13533 Б + git diff 58158 Б
  tasks: 2
  commits: 0      # коммиты делает владелец вручную
  plan_head_before: 06f2785f098f97db20d4df540befae741e701ceb

tech-stack:
  added: []
  patterns:
    - "Прогон: StartRun → Fetch/Parse под RunTimeout → Prepare → BaselineRuns → Evaluate → финализация под context.WithoutCancel + FinalizeTimeout"
    - "suspect хранит все записи Parse в ingest_runs.snapshot; следующий завершённый прогон или AcceptRun его обнуляет"
    - "Окно базы — прогоны ok с id ≥ последнего принятого вручную, не больше 7"
    - "Наблюдение — три UPDATE в одной транзакции по last_run_id; events.status не трогается"

key-files:
  created:
    - migrations/00002_ingest_runs_quality.sql
    - internal/store/runs.go
    - internal/store/observe.go
  modified:
    - internal/store/events.go
    - internal/store/store_test.go
    - internal/ingest/runner.go
    - internal/ingest/runner_test.go
    - internal/ingest/quality.go

key-decisions:
  - "StartRun получил параметр startedAt: started_at = часы Runner.Now, те же, что у Prepare; Accept повторяет Prepare с этим моментом точно, а тесты с фиксированными часами не зависят от реальной даты"
  - "ObserveStats.Missing — не найденные прогоном и оставшиеся missing (включая повторные пропуски); Gone — перешедшие в gone в этом вызове; записи в gone наблюдение больше не трогает"
  - "SourceHealth смотрит последний завершённый прогон (running пропускается); ConsecutiveFailures — failed после последнего ok/suspect"
  - "median стала обобщённой (int | float64): база долей пустых полей — медиана по metrics.empty прогонов окна"
  - "Бизнес-исходы ok/suspect/failed возвращаются с err == nil; ошибка — ErrRunInProgress, сбой БД при чтении базы, записи или наблюдении"

metrics:
  started: 2026-09-29T16:30:00Z
  completed: 2026-09-29T16:43:00Z
  duration: "~13 мин"
---

# Phase 3 Plan 04: ingest_runs, наблюдение и Runner с оценкой качества — итоги

Каждый прогон теперь пишется в `ingest_runs` со статусом, счётчиками, причиной и метриками. `suspect` и `failed` ничего не применяют: `suspect` сохраняет снимок записей, `Runner.Accept` применяет его без обращения к сайту. Здоровый прогон ведёт наблюдение `active/missing/gone`: исчезнувшее событие скрывается через `is_listed`, его `status` не меняется. Тесты `internal/store` отказываются работать с базой, имя которой не оканчивается на `_test`.

## Выполнено

| Задача | Что | Коммит |
|---|---|---|
| 1 (tdd) | миграция 00002, `runs.go`, `observe.go`, `UpsertSourceEvent(+runID)`, `SetOutOfScope`, guard LO-11, 7 новых тестов store; Runner пишет каждый прогон — `STORE LIFECYCLE OK`, `GUARD OK` | не закоммичено: коммиты делает владелец |
| 2 (tdd) | окончательный `Runner.Run` (Evaluate до записи, snapshot, Observe, финализация под WithoutCancel), `Runner.Accept`, логи MON-01/02; 8 новых тестов, 2 обновлены — `RUNNER OK`, `RUNNER STATIC OK` | не закоммичено |

## Результаты проверок

- `STORE LIFECYCLE OK`, `GUARD OK`, `RUNNER OK`, `RUNNER STATIC OK` — все четыре команды `<verify>` зелёные (задача 1 перепроверена после задачи 2).
- Guard LO-11: `TEST_DATABASE_URL=…/postgres` → `TEST_DATABASE_URL: имя базы должно оканчиваться на _test`, `FAIL`, код ≠ 0; в базе `postgres` после этого 0 таблиц в `public` (миграции не применялись).
- Миграция на отдельной базе `migcheck_test`: `migrate` дважды — второй раз ничего не применяет (версии 0, 1, 2); `DownTo(1)` через goose Provider убирает 3 колонки и индекс; повторный `migrate` возвращает 00002. База удалена, временная программа проверки удалена.
- `go test -p 1 -count=1 ./...` с `TEST_DATABASE_URL`: 57 PASS, 0 SKIP, 0 FAIL (тесты верхнего уровня; было 42, +15 новых). Без неё: 34 PASS, 23 SKIP (тесты с БД), 0 FAIL.
- `go vet ./...`, `gofmt -l .` (пусто), `git diff --check` — чисто. SQL только в `internal/store`, `Sprintf` в SQL store нет, слова-индикатора антибота в Go нет, колонок изображений в схеме нет.
- SHA-256 фикстуры `dd4a58d3…3ddf` не изменился, `git status` по `testdata/` пуст.
- Tracer на фикстуре: первый Run — `suspect` («первый прогон источника — требуется принятие»), 0 записано; Accept — `created=133`; второй Run — `ok`, `created=0 changed=0 unchanged=133` (ROADMAP критерий 2).

## TDD

- Задача 1: сначала тесты (ошибка компиляции), затем заглушки API, после этого RED был поведенческим: падали `TestUpsertSourceEvent` (last_run_id), `TestSetOutOfScope`, `TestRunLifecycle`, `TestStartRunExclusive`, `TestBaselineRuns`, `TestSourceHealth`, `TestObserve`. **Слабый RED:** `TestSaveEventFor` прошёл сразу — `SaveEventFor` сделан в 03-02, тест лишь закрепляет его поведение.
- Задача 2: после добавления полей `Result` и заглушки `Accept` падали все 10 тестов runner (статус не выставлялся, `Accept` не реализован, failed возвращался ошибкой).
- Мутации (исходники восстановлены, тесты снова зелёные): события в прошлом не исключаются из missing — падает `TestObserve`; окно базы без принятого прогона — `TestBaselineRuns`, `TestRunVolumeSuspect`; снимок предыдущего прогона не очищается — `TestRunLifecycle`; suspect применяется как ok — 8 тестов runner; `is_listed` не обновляется — `TestObserve`; `last_run_id` только при вставке — `TestUpsertSourceEvent`, `TestObserve`, `TestRunVolumeSuspect`, `TestRunObservation`; AcceptRun принимает не последний прогон — `TestRunLifecycle`.

## Deviations from Plan

**1. [Rule 1] `StartRun(ctx, sourceID, startedAt, staleAfter)` вместо `StartRun(ctx, sourceID, staleAfter)`.**
- Проблема: по плану `started_at` = `now()` БД, а Accept делает Prepare с `now = started_at`. Run же делает Prepare с `Runner.Now`. В тестах с фиксированными часами (`fixtureNow`) Accept проверял бы даты по реальному времени. Даты фикстуры начинаются 2026-09-29, поэтому уже через несколько дней часть записей стала бы `expired` только в Accept и `TestRunFixture` начал бы падать. В проде это давало бы расхождение Run и Accept на миллисекунды.
- Сделано: `started_at` берётся из часов прогона (`Runner.Now`, в проде `time.Now`), и брошенный `running` тоже определяется от этого момента. Остальные сигнатуры совпадают с таблицей Artifacts. 03-05 вызывает только `LastRun`/`SourceHealth` и вставляет прогон SQL-запросом с `now()`, поэтому правка его не затрагивает. Строку `StartRun` в таблицах Artifacts планов 03-05…03-07 стоит читать с этим параметром.

**2. `median` обобщена до `int | float64`** (`quality.go`, одна строка). Нужно для `Baseline.EmptyShare` — медианы долей пустых полей по `metrics` окна базы. `TestMedian` не менялся и проходит.

**3. `Runner.StaleAfter` по умолчанию** = `RunTimeout + 15 мин` (45 мин при умолчаниях), как в задаче 2. В задаче 1 временно стояла константа 45 мин.

**4. Дополнительные проверки в тестах** (сверх `<behavior>`): `SetOutOfScope` не скрывает событие, у которого осталась другая живая связь; `SourceHealth` сбрасывает счётчик после `suspect`; `TestObserve` проверяет, что без 72 ч запись с 3 пропусками остаётся `missing`; `TestAcceptBaseline` проверяет, что `Fetch` при Accept не вызывается.

**5. Причина `ErrStructureNotFound`** пишется как текст ошибки `Parse` («структура не найдена: нет таблицы…»), без второго префикса.

## Known Stubs

Нет. `Runner.Run`/`Accept` ещё не вызываются подкомандами — это делает 03-05.

## Известный предел

- `Accept` проверяет, что последний прогон не `running`, но между проверкой и `AcceptRun` запись не заблокирована (комментарий `Упрощение:` в `runner.go`). Если в это окно стартует демон, `AcceptRun` вернёт `ErrNotAcceptable`, а уже записанные строки исправит его же прогон `ok`: запись идемпотентна. Если это окажется проблемой, нужно занимать слот `running` на время Accept.

## Threat Flags

Новой поверхности сверх threat_model нет. T-03-17 закрыт тестами `TestRunFirstRunSuspect`, `TestRunVolumeSuspect`, `TestRunObservation`, `TestRunFailures`; T-03-18/19 — `TestObserve`, `TestSetOutOfScope`, `TestSaveEventFor`; T-03-20 — guard и отказ на базе `postgres`; T-03-21 — `TestStartRunExclusive`; T-03-22 — `context.WithoutCancel` + `FinalizeTimeout`; T-03-23 — `TestAcceptRefusals` и условие в SQL `AcceptRun`; T-03-24 — стек паники только в лог ERROR, в `ingest_runs.error` — «паника парсера».

## Для следующих планов

- 03-05: `Runner.Thresholds`, `RunTimeout`, `GoneAfterRuns`, `GoneAfter` — поля для значений из окружения; `Runner.Raw` ещё не добавлен. `store.ErrRunInProgress` приходит из `Run` как ошибка (`errors.Is`).
- `ingest_runs.metrics` у прогона ok содержит `created/changed/unchanged/missing/gone` (нужны проверке `PHASE DATA OK` в 03-07); у принятого прогона остаются метрики Prepare на момент suspect.

## Self-Check: PASSED

- Созданы `migrations/00002_ingest_runs_quality.sql`, `internal/store/runs.go`, `internal/store/observe.go`; изменены 5 файлов (`git status --short`).
- Коммитов нет по правилу проекта: `commits: 0` при `plan_head_before` = HEAD `06f2785`; изменения ждут коммита владельца.
