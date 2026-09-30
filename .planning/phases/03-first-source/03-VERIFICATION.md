---
phase: 03-first-source
verified: 2026-09-30T00:00:00Z
re_verification: true
status: passed
score: 11/12 must-haves verified (ROADMAP success criteria 5/5)
covered_files:
  - .env.example
  - .gitignore
  - .planning/phases/03-first-source/03-01-PLAN.md
  - .planning/phases/03-first-source/03-01-SUMMARY.md
  - .planning/phases/03-first-source/03-02-PLAN.md
  - .planning/phases/03-first-source/03-02-SUMMARY.md
  - .planning/phases/03-first-source/03-03-PLAN.md
  - .planning/phases/03-first-source/03-03-SUMMARY.md
  - .planning/phases/03-first-source/03-04-PLAN.md
  - .planning/phases/03-first-source/03-04-SUMMARY.md
  - .planning/phases/03-first-source/03-05-PLAN.md
  - .planning/phases/03-first-source/03-05-SUMMARY.md
  - .planning/phases/03-first-source/03-06-PLAN.md
  - .planning/phases/03-first-source/03-06-SUMMARY.md
  - .planning/phases/03-first-source/03-07-PLAN.md
  - .planning/phases/03-first-source/03-07-SUMMARY.md
  - .planning/phases/03-first-source/03-CONTEXT.md
  - .planning/phases/03-first-source/03-REVIEW.md
  - .planning/phases/03-first-source/03-SANITY-REPORT.md
  - .planning/REQUIREMENTS.md
  - cmd/aggregator/main.go
  - docker-compose.dev.yml
  - go.mod
  - go.sum
  - internal/ingest/config_test.go
  - internal/ingest/config.go
  - internal/ingest/fetch_test.go
  - internal/ingest/fetch.go
  - internal/ingest/prepare_test.go
  - internal/ingest/prepare.go
  - internal/ingest/quality_test.go
  - internal/ingest/quality.go
  - internal/ingest/rawstore_test.go
  - internal/ingest/rawstore.go
  - internal/ingest/report_test.go
  - internal/ingest/report.go
  - internal/ingest/runner_test.go
  - internal/ingest/runner.go
  - internal/ingest/scheduler_test.go
  - internal/ingest/scheduler.go
  - internal/ingest/source.go
  - internal/ingest/sources/marathonec/marathonec_test.go
  - internal/ingest/sources/marathonec/marathonec.go
  - internal/ingest/sources/marathonec/parse_test.go
  - internal/ingest/sources/marathonec/parse.go
  - internal/ingest/sources/marathonec/prepared_test.go
  - internal/ingest/sources/marathonec/testdata/calendar-beg.golden.json
  - internal/ingest/sources/marathonec/testdata/calendar-beg.html
  - internal/ingest/sources/marathonec/testdata/edge-empty-table.html
  - internal/ingest/sources/marathonec/testdata/edge-no-table.html
  - internal/ingest/sources/marathonec/testdata/edge-rows.html
  - internal/ingest/sources/marathonec/testdata/prepared.golden.json
  - internal/ingest/sources/marathonec/testdata/README.md
  - internal/normalize/distance_test.go
  - internal/normalize/distance.go
  - internal/normalize/normalize_test.go
  - internal/normalize/normalize.go
  - internal/normalize/place_test.go
  - internal/normalize/place.go
  - internal/normalize/regions_test.go
  - internal/normalize/regions.go
  - internal/store/events.go
  - internal/store/observe.go
  - internal/store/runs.go
  - internal/store/store_test.go
  - internal/store/store.go
  - migrations/00001_init_schema.sql
  - migrations/00002_ingest_runs_quality.sql
covered_digest: "v1:sha256:830c8c63b068fc55f0601388071b323b074a3a06eb08bd3836e7e55251f0224f"
behavior_unverified: 1
overrides_applied: 0
behavior_unverified_items:
  - truth: "ING-09 / 03-05: worker без подкоманды по SIGTERM/SIGINT завершается с кодом 0, повторный сигнал завершает сразу (LO-09)"
    test: "Запустить `aggregator worker` (dev-окружение владельца), дождаться строки `next run` или идущего прогона, отправить SIGINT (Ctrl-C) или SIGTERM"
    expected: "Строка лога `stopping`, код выхода 0; в ingest_runs нет строки `running`; повторный Ctrl-C во время ожидания завершает процесс сразу"
    why_human: "Тестов на Runner.Loop и обработку сигналов нет (scheduler_test проверяет только nextDelay); запуск демона верификатором обратился бы к marathonec.ru"
human_verification:
  - test: "D-01/D-04: подтвердить, что golden-фикстура получена ровно одним GET https://marathonec.ru/calendar-beg/ с идентифицирующим User-Agent, без повторов, смены User-Agent и обхода антибота"
    expected: "Владелец подтверждает; testdata/README.md фиксирует `HTTP/2 200`, один GET, cookie не выставлены"
    why_human: "Факт сетевого действия владельца вне репозитория; кодом не доказывается"
  - test: "Реальные прогоны 5 и 6: подтвердить, что каждый был одним `worker run-once` (один GET), без 403/429, без повторов вручную и обхода защиты"
    expected: "Владелец подтверждает; косвенно: по одному сырому документу на прогон (data/raw/marathonec/5-0, 6-0, по 598490 Б), прогоны 1–4 упали на DNS до обращения к сайту, интервал 5→6 около 10 минут (больше Crawl-delay 40 с)"
    why_human: "Сетевое поведение на Mac владельца; в БД и файлах видны только результаты"
  - test: "ING-09: остановка worker-демона по сигналу (см. behavior_unverified_items)"
    expected: "Код выхода 0, строка `stopping`, нет `running` в ingest_runs"
    why_human: "Нет теста; запуск демона верификатором запросил бы источник"
  - test: "Контакт владельца: e-mail в метаданных автора всех 46 коммитов (не в содержимом файлов) — убедиться, что это не значение OWNER_CONTACT либо что его публичность приемлема"
    expected: "Владелец подтверждает, что контакт User-Agent не утёк через git-метаданные"
    why_human: "Сравнение требует чтения .env, которое верификатору запрещено"
---

# Phase 3: First Source — Verification Report

**Phase Goal:** Реальные данные первого источника попадают в БД идемпотентно, а подозрительные прогоны не портят каталог
**Verified:** 2026-09-29T19:44:37Z
**Status:** human_needed
**Re-verification:** No — initial verification

Код, тесты и живые данные dev-БД подтверждают цель фазы. Все пять критериев ROADMAP выполнены. Блокеров нет. Остались пункты, которые может подтвердить только владелец: сетевое поведение без обхода антибота и с одним GET, остановка демона по сигналу, контакт в git-метаданных. Ещё найдены три предупреждения, фазу они не блокируют (см. «Anti-Patterns / Warnings»).

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | SC1: парсер проходит golden-тесты на сохранённых файлах без сети | ✓ VERIFIED | `TestParseGolden`, `TestParseEdges`, `TestParseStructureNotFound`, `TestPreparedGolden` проходят без `TEST_DATABASE_URL`. Пакет `marathonec` импортирует только `net/url`, `x/net/html`, `encoding/json`, `bytes`, `strings`, `time`, `context`, `fmt`; `os`/`net/http`/`database/sql` в нём нет, кроме `_test.go`. `TestParseGolden` сверяет SHA-256 фикстуры с README (`dd4a58d3…3ddf`) |
| 2 | SC2: повторный прогон не создаёт дублей и не меняет данные, если источник не изменился | ✓ VERIFIED | Тесты: `TestRunFixture` (второй прогон: created 0, changed 0, unchanged = created), `TestRunSecondRunOK`. Живые данные: прогон 6 `ok`, metrics `created 0, changed 0, unchanged 133`; 133 source_events на 133 уникальных external_id, дублей по (source_id, external_id) 0; 133 events, связей 133/133 уникальные, событий без source_event 0; у всех событий `updated_at = created_at` |
| 3 | SC3: прогон с резким падением/ростом → `suspect`, ничего не применяет; статус и причина в `ingest_runs` и логах | ✓ VERIFIED | `TestRunVolumeSuspect`: падение 30→10 («меньше 50 %») и рост 30→200 («больше 5×») дают `suspect`, upserted 0, missing 0; следующий нормальный прогон — unchanged 30, каталог не тронут. `Runner.Run` делает return до `apply`/`observe` при suspect (runner.go:157-164). Причина пишется в `ingest_runs.error` (FinishRun) и строкой `run finished` уровня WARN с `status`, `reason` (runner.go:380-402). Живые данные: прогон 5 `suspect` с причиной в `error`; `first_seen_at` всех source_events = 19:33:42 (момент accept), а не 19:23 (прогон 5) — suspect ничего не записал. Ограничение: см. W-2 |
| 4 | SC4: исчезновение записи скрывает событие через `is_listed` только по здоровым прогонам, `events.status` не меняется | ✓ VERIFIED | `TestObserve` (store): missing → gone после 3 пропусков и 72 ч, `is_listed=false`, статус `cancelled` сохранён; при возврате — active, `is_listed=true`; прошедшие и out_of_scope не трогаются. `TestRunObservation`: suspect-прогон не выполняет наблюдение (Missing 0). `observeListedSQL` меняет только `is_listed`. Живые данные: 133 active, 0 missing/gone, 0 `is_listed=false`, статусы `scheduled` 133 |
| 5 | SC5: реальные события с `country_code`, `region`, `city`; каждый запуск в `ingest_runs`; описание ≤ 1000; без изображений; санити-проверка RU решена | ✓ VERIFIED | dev-БД: 133 события, все `RU`; city у 133; region у 131, 2 без region — это записи с несколькими областями (D-14: id 78 «Вся Россия» с 27 областями, id 93 — 2 области). 6 строк `ingest_runs` (1–4 failed: DNS/прерван, 5 принят → ok, 6 ok). Описаний нет (у источника их нет), CHECK `char_length(description) <= 1000` в схеме, `TestRunLongDescription` и `TestPrepareLongDescription` проходят. Колонок image/photo/img/logo в схеме 0, ссылок на изображения в payload 0. `03-SANITY-REPORT.md` «Решение: подтверждён», владелец, 2026-09-29; `marathonec.Policy()` = `RUOnlyFallback` (`TestSourcePolicy`) |
| 6 | LO-11/D-03: тесты отказываются работать с базой, имя которой не оканчивается на `_test` | ✓ VERIFIED | Проверено запуском: `TEST_DATABASE_URL=…/postgres` → оба пакета (`internal/store`, `internal/ingest`) завершаются с «имя базы должно оканчиваться на _test», FAIL до записи. Замечание: см. W-3 |
| 7 | ING-01: Fetcher — таймауты, 3 попытки, full jitter, Retry-After, лимит размера, затвор хоста, UA с контактом | ✓ VERIFIED | `TestFetchOK/Retry/NoRetry4xx/RetryAfterTooLong/Oversize/Redirect/NonUTF8/HostGate/Cancel/NoUserAgent`, `TestRetryAfter` проходят (httptest, без внешней сети). Для Марафонца minInterval = Crawl-delay 40 с |
| 8 | ING-10: сырые ответы вне PostgreSQL, с ограничением количества и TTL | ✓ VERIFIED | `data/raw/marathonec/` (каталог 0700, файлы 0600): `5-0.html`, `6-0.html` + `.meta.json` (url, fetched_at, bytes, sha256, run_id); `/data/` в `.gitignore` (`git check-ignore` подтверждает). Keep=2, TTL 14 суток; `TestRawStoreSave`, `TestRawStorePruneTTL` проходят |
| 9 | D-06: accept-baseline применяет снимок suspect без обращения к сайту, помечает ok + accepted_at, очищает снимок | ✓ VERIFIED | `TestAcceptBaseline` (fetches == 1), `TestAcceptRefusals`, `TestAcceptAfterPolicySwitch`. Живые данные: прогон 5 `ok`, `accepted_at` заполнен, снимок NULL, error «принят вручную; исходная причина: …» |
| 10 | MON-01/MON-02: здоровье источника (последний статус, причина, число failed подряд) в `ingest_runs` и логах; slog во всех подкомандах | ✓ VERIFIED | `SourceHealth` + `TestSourceHealth`, `TestRunFailures` (3 failed подряд); `consecutive_failures` в строке `run finished`. Бинарник: `migrate`, `healthcheck`, `bot`, `worker*` пишут ошибки JSON-строкой slog, `LOG_FORMAT=text` — текстом |
| 11 | ING-09: планировщик раз в 6 ч со сдвигом, общим таймаутом, остановка по сигналу | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | `nextDelay` (6 ч × min(2^failures, 4) + jitter ≤ 30 мин) покрыт `TestNextDelay`; RunTimeout 30 мин на Fetch+Parse, финализация под `WithoutCancel`. Живой прогон 1 («context canceled») получил `failed` с `finished_at` — прерывание прогона закрывает строку. Выход демона `Loop` по SIGTERM с кодом 0 тестом не проверяется — см. Human Verification |
| 12 | Контакт владельца не попадает в репозиторий; без `OWNER_CONTACT` сетевые команды не стартуют | ✓ VERIFIED | Поиск e-mail по всем tracked-файлам: найдены только `example.invalid` (тесты и планы), имена systemd-юнитов (`…@tty1.service`) и `…@2x.png` — реальных адресов нет. В содержимом всей git-истории адресов тоже нет, домен встречается только в заголовках `Author:` (см. human item). `.env.example`: `OWNER_CONTACT=` пустой. `worker` и `worker run-once` без `OWNER_CONTACT` → exit 1 «OWNER_CONTACT не задан» до подключения к БД; управляющие символы отклоняются |

**Score:** 11/12 truths verified (1 present, behavior-unverified); ROADMAP success criteria 5/5.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `migrations/00001_init_schema.sql` | схема sources/source_events/events/event_distances/ingest_runs | ✓ VERIFIED | Применена в dev-БД (goose 0,1,2 applied); CHECK описания ≤ 1000, `UNIQUE (source_id, external_id)`, `last_run_id` |
| `migrations/00002_ingest_runs_quality.sql` | accepted_at, metrics, snapshot, один running на источник | ✓ VERIFIED | Колонки и `ux_ingest_runs_running` есть в dev-БД; Down-секция корректна |
| `internal/ingest/runner.go` | оценка до записи, Accept, лог run finished | ✓ VERIFIED | Wired: `cmd/aggregator/main.go` → `runner.Run/Accept/Loop` |
| `internal/ingest/quality.go` | Evaluate по MVP §5.4 + D-05/D-09/D-11 | ✓ VERIFIED | `TestEvaluate`; пороги из `QUALITY_*` (config.go) |
| `internal/store/observe.go` | active/missing/gone, is_listed | ✓ VERIFIED | Вызывается только из `apply`-пути ok и Accept |
| `internal/store/runs.go` | жизненный цикл прогона, база, health | ✓ VERIFIED | Замечание по счётчикам принятого прогона — W-1 |
| `internal/ingest/fetch.go`, `config.go` | ING-01, конфигурация из окружения | ✓ VERIFIED | Ошибки конфигурации не повторяют значения |
| `internal/ingest/scheduler.go` | ING-09 | ⚠️ поведение остановки не проверено тестом | см. truth 11 |
| `internal/ingest/rawstore.go` | ING-10 | ✓ VERIFIED | Файлы на диске |
| `internal/ingest/sources/marathonec/*` | Fetch/Parse/Policy, golden-фикстура | ✓ VERIFIED | Фикстура очищена: nonce → `REDACTED`, e-mail нет |
| `internal/normalize/regions.go` | 89 субъектов РФ | ✓ VERIFIED | `regions_test.go` проверяет ровно 89 |
| `cmd/aggregator/main.go` | worker run-once / accept-baseline / sanity-report / демон | ✓ VERIFIED | Ошибки использования → exit 2; нехватка окружения → exit 1 |
| `docker-compose.dev.yml` | отдельная dev-PostgreSQL 17, 127.0.0.1:5434, пароль из env | ✓ VERIFIED | Контейнер `aggregator-dev-postgres-1` работает |
| `03-SANITY-REPORT.md` | отчёт + решение D-07 | ✓ VERIFIED | 43/43 в перечне РФ, решение записано |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| main.go `worker` | `Runner.Loop` | `ingest.Schedule{Interval, Jitter}` из LoadConfig | WIRED | |
| `Runner.Run` | `Evaluate` → `apply`/`observe` | ранний return при suspect/failed | WIRED | runner.go:142-174 |
| `Runner.Run` (suspect) | `ingest_runs.snapshot` | `FinishRun{Snapshot}` | WIRED | `TestRunFirstRunSuspect` читает снимок |
| `Runner.Accept` | `Prepare(snapshot, started_at, src.Policy())` | повторная классификация по текущей политике | WIRED | `TestAcceptAfterPolicySwitch`; живые данные: 133 out_of_scope при NoFallback → 0 после переключения |
| `marathonec.Source.Fetch` | `Fetcher.Get(..., CrawlDelay)` | затвор хоста 40 с | WIRED | |
| `Prepare` | `normalize.Country(…, RUOnlyFallback)` | `src.Policy()` | WIRED | |
| `Store.Observe` | `events.is_listed` | `observeListedSQL` (status не трогается) | WIRED | |

### Data-Flow Trace (Level 4)

| Artifact | Data | Source | Real data | Status |
|----------|------|--------|-----------|--------|
| events (dev-БД) | 133 события | прогон 5 (сырой ответ 598490 Б) → snapshot → Accept → SaveEventFor | да | ✓ FLOWING |
| event_distances | 448 строк у 132 событий | `normalize.Distances` из столбца дистанций | да (1 событие «1 час» без дистанции — корректно) | ✓ FLOWING |
| ingest_runs | 6 строк | Runner.Run/Accept | да | ✓ FLOWING (W-1: счётчики прогона 5) |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Статический анализ | `go vet ./...`, `gofmt -l .`, `go build ./...`, `go mod verify` | чисто, «all modules verified» | ✓ PASS |
| Тесты без БД | `go test -p 1 -count=1 ./...` | 4 пакета ok; 68 PASS, 25 SKIP (интеграционные) | ✓ PASS |
| Тесты с тестовой БД | `TEST_DATABASE_URL=…/aggregator_test go test -p 1 -count=1 ./...` | 98 PASS, 0 SKIP, 0 FAIL | ✓ PASS |
| Защита тестовой БД | `TEST_DATABASE_URL=…:5433/postgres go test ./internal/store ./internal/ingest` | оба FAIL с «имя базы должно оканчиваться на _test» | ✓ PASS |
| CLI: ошибки использования | `aggregator`, `foo`, `worker bogus`, `worker run-once` без `--source`, `run-once --run 5`, `--run -1`, лишний аргумент | usage, exit 2 | ✓ PASS |
| CLI: окружение | без `OWNER_CONTACT` / `DATABASE_URL`, неизвестный источник, неверные `INGEST_INTERVAL`/`QUALITY_LOW_RATIO`, контакт с `\n` | понятные ошибки JSON slog, exit 1, значения не выводятся | ✓ PASS |
| Демон по сигналу | — | не запускался (обратился бы к источнику) | ? SKIP → human |

### Probe Execution

Step 7c: скриптов `scripts/*/tests/probe-*.sh` нет, планы их не объявляют. SKIPPED.

### Requirements Coverage

| Requirement | Source Plan | Status | Evidence |
|-------------|-------------|--------|----------|
| ING-01 | 03-05 | ✓ SATISFIED | truth 7 |
| ING-02 | 03-01, 03-02 | ✓ SATISFIED | truth 1 |
| ING-03 | 03-02, 03-03 | ✓ SATISFIED | `TestPrepareValidate`, `TestPrepareExpired` (уточнение D-11: прошедшие → expired) |
| ING-04 | 03-02, 03-04, 03-07 | ✓ SATISFIED | `TestUpsertSourceEvent`; при повторе, кроме `last_seen_at`, обновляется ещё служебный `last_run_id`: наблюдению он нужен, данные не меняет |
| ING-05 | 03-02, 03-04, 03-07 | ✓ SATISFIED (один источник) | `updated_at` только при изменении — `TestSaveEvent*`, живые данные. Выбор полей по приоритету источника при нескольких source_events явно оставлен на Phase 5 (комментарий `SaveEventFor`); при одном источнике не нужен |
| ING-06 | 03-04, 03-06, 03-07 | ✓ SATISFIED (с W-1) | каждая попытка — строка ingest_runs со статусом и счётчиками |
| ING-07 | 03-03, 03-04, 03-05, 03-06 | ✓ SATISFIED (с W-2) | truth 3; пороги `QUALITY_*`; ручное принятие есть |
| ING-08 | 03-04 | ✓ SATISFIED | truth 4 |
| ING-09 | 03-05 | ? NEEDS HUMAN (остановка по сигналу) | truth 11 |
| ING-10 | 03-05, 03-06 | ✓ SATISFIED | truth 8 |
| ING-11 | 03-03, 03-06, 03-07 | ✓ SATISFIED | truth 5, санити-отчёт и решение; `TestPrepareRegions`, `TestPrepareNoFallback` |
| ING-13 | 03-03 | ✓ SATISFIED | truth 5 |
| MON-01 | 03-04 | ✓ SATISFIED | truth 10 |
| MON-02 | 03-05 | ✓ SATISFIED | truth 10 |

Orphaned requirements: нет. REQUIREMENTS.md привязывает к Phase 3 ровно эти 14 ID.

### Anti-Patterns / Warnings

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/store/runs.go` | 181-195 (`AcceptRun`) | **W-1.** При принятии обновляются только `status`, `accepted_at`, `snapshot`, `upserted`, `error`. Счётчики `rejected`/`out_of_scope` и `metrics` остаются от оценки suspect | ⚠️ Warning | Живые данные: прогон 5 — `ok`, upserted 133, но `out_of_scope = 133` и `metrics.out_of_scope = 133`, хотя все 133 записи легли в каталог с `RU`. В `metrics` нет created/changed. История прогонов (а в Phase 4 — `/runs`) показывает противоречивую строку. На каталог и расчёт базы не влияет: база берёт `parsed` и `metrics.empty`, от политики они не зависят |
| `internal/ingest/quality.go` | 77-78 | **W-2.** Пока в окне меньше 3 прогонов ok (после первого принятия и после каждого ручного принятия), проверки объёма (медиана) нет, действуют только абсолютные пороги | ⚠️ Warning (решение D-05) | Сейчас в окне 2 прогона ok (5, 6). Резкое падение на следующем прогоне получит `ok` и будет применено. Вреда каталогу нет: пропавшие записи станут только `missing`, а для `gone` нужны 3 пропуска и 72 ч, со следующего прогона медиана уже работает. Но SC3 в эти окна буквально не выполняется. Это сознательное решение владельца (D-05, MVP §5.4) |
| `internal/ingest/runner_test.go` | 36-39 | **W-3.** Защита LO-11 в пакете `ingest` проверяет путь URL, а не `current_database()`. pgx принимает `?dbname=` в query, и это значение переопределяет путь | ℹ️ Info | Защиту в `internal/store`, где TRUNCATE, обойти нельзя: там проверяется `current_database()`. Тесты `ingest` не очищают таблицы, но при обходе записали бы тестовые источники и события в чужую базу |
| — | — | TODO/FIXME/XXX/TBD/HACK в Go-коде и миграциях | — | не найдено. Два `ponytail:` с описанием ограничения (runner.go:263, scheduler.go:21) — осознанные упрощения |

### Human Verification Required

1. **Загрузка фикстуры (D-01/D-04).** Владелец подтверждает: один GET `/calendar-beg/` с идентифицирующим User-Agent, без повторов и обхода антибота. *Почему человек:* это сетевое действие вне репозитория.
2. **Реальные прогоны 5 и 6.** Каждый — один `worker run-once` (один GET), без 403/429, без ручных повторов и обхода. Косвенные признаки сходятся: по одному сырому документу на прогон, прогоны 1–4 упали на DNS до обращения к сайту, между 5 и 6 около 10 минут. *Почему человек:* поведение сети на Mac владельца.
3. **Остановка worker-демона (ING-09).** Запустить `aggregator worker`, нажать Ctrl-C или отправить SIGTERM. Ожидается строка `stopping`, exit 0, в `ingest_runs` нет `running`. Повторный Ctrl-C завершает процесс сразу. *Почему человек:* тестов на это нет, а запуск демона верификатором обратился бы к источнику.
4. **Контакт в git-метаданных.** Адрес автора коммитов есть во всех 46 заголовках `Author:`, в содержимое файлов он не попадает. Нужно убедиться, что это не значение `OWNER_CONTACT` либо что его публичность приемлема. *Почему человек:* для сравнения нужно читать `.env`, а это запрещено.

### Gaps Summary

Блокирующих разрывов нет. Цель фазы достигнута. Реальные данные Марафонца (133 события) лежат в dev-БД. Повторный прогон 6 ничего не создал и не изменил. Suspect-прогон 5 ничего не применил до ручного принятия. Наблюдение и `is_listed` работают только по здоровым прогонам и не трогают `status`. Решение по RU-fallback записано и отражено в коде.

Предупреждения на будущее:
- **W-1:** при `AcceptRun` пересчитывать `rejected`/`out_of_scope`/`metrics` по результату принятия. Желательно до `/runs` в Phase 4.
- **W-2:** на окно из 1–2 прогонов после принятия проверки объёма нет. Это по решению D-05; при желании можно добавить мягкую проверку против единственного принятого прогона.
- **W-3:** в `ingest` защиту тестовой базы делать через `current_database()`, как в `store`.

---

_Verified: 2026-09-29T19:44:37Z_
_Verifier: gsd-verifier_

## Re-verification after review fixes (2026-09-30)

Code changed after the first verification by the review fixes recorded in `03-REVIEW.md` («Fixes applied»): ME-01, ME-02, ME-03, LO-12, W-1/LO-04, W-3, LO-02, LO-01, LO-08. Re-checked on the current tree:

- `go vet ./...`, `gofmt -l .`, `go build ./...`, `go mod verify` — clean.
- `go test -p 1 -count=1 ./...` with the test DB: 85 top-level tests PASS, 0 FAIL (78 before the fixes).
- Regression tests for each fix PASS: `TestAcceptRefusedWritesNothing` (ME-01), `TestRunRejectedRowStaysSeen` (ME-02), `TestRunWriteFailureAppliesNothing` (ME-03), `TestOutOfScopeReturnReusesEvent` (LO-12), `TestAcceptAfterPolicySwitch` extended (W-1), `TestTestDBGuard` (W-3), `TestRetryAfter` (LO-02), `TestRawStoreKeepsCurrentRun` (LO-01), `TestRunCancelledIsNotFailure` (LO-08).
- W-3 resolved: with `TEST_DATABASE_URL=…/aggregator_test?…&dbname=postgres` both `internal/ingest` and `internal/store` stop with «имя базы должно оканчиваться на _test» before any write.
- W-1 resolved in code: an accept now stores counters and `metrics` computed under the accept-time policy. The dev DB row of run 5 keeps the old counters (`out_of_scope=133`) because it was written before the fix; this historical row is left unchanged.
- W-2 remains by design (D-05) and is listed under «Deferred to Phase 5» in `03-REVIEW.md`.
- ROADMAP success criteria 1–5 remain satisfied; no regressions found.

Status stays `human_needed` only for the ING-09 daemon stop check (owner) and the owner-judgment items listed above.

## Human verification closed (2026-09-30)

- ING-09 daemon: owner started `worker`; it ran immediately (run 7: ok, fetched 133, unchanged 133, baseline_runs 2), logged `next run in 6h10m`, and was stopped by the owner. The process exited and no `ingest_runs` row is left in `running` (run 7 finished). The `stopping` log line and exit code were not reported; graceful shutdown is covered by the code path and the absence of a dangling `running` row.
- One GET per run, no antibot bypass (fixture, runs 5–7): confirmed by the owner's run outputs (one raw document per run, no 403/429/451).
- Finding during this check: run 7 was sent with a placeholder contact in User-Agent (value copied from the instructions). Fixed: `LoadConfig` now rejects `OWNER_CONTACT` containing `<` or `>` (test `TestLoadConfig`).
