---
phase: 03-first-source
plan: 05
subsystem: ingest (Fetcher, конфигурация, сырые ответы, планировщик, санити-отчёт) + cmd (worker) + dev-окружение
tags: [fetch, crawl-delay, config, raw-store, scheduler, sanity-report, cli, slog, dev-db, ing-01, ing-07, ing-09, ing-10, ing-11, mon-02, d-03, d-06, d-07, lo-09]
status: complete

requires:
  - "03-02: SourceEvent, RawDoc, Policy, Source, marathonec.Parse"
  - "03-03: normalize.ClassifyRegion/Region/Distances/Type, Thresholds, DefaultThresholds"
  - "03-04: Runner.Run/Accept, store.LastRun/SourceHealth/EnsureSource, snapshot у suspect"
provides:
  - "internal/ingest/fetch.go — FetchConfig, DefaultFetchConfig, Fetcher, NewFetcher, Get (повторы, Retry-After, лимиты, редиректы, затвор хоста)"
  - "internal/ingest/config.go — Config, LoadConfig(needContact): INGEST_*, RAW_*, OBSERVE_*, QUALITY_*, FETCH_*, OWNER_CONTACT"
  - "internal/ingest/sources/marathonec/marathonec.go — Code, CalendarURL, CrawlDelay, Source, New; политика NoFallback (D-07)"
  - "internal/ingest/rawstore.go — RawStore{Dir, TTL, Keep}: Save, Prune"
  - "internal/ingest/scheduler.go — Schedule, Runner.Loop, nextDelay"
  - "internal/ingest/report.go — SanityReport"
  - "internal/ingest/runner.go — поле Raw; сохранение сырых ответов после Fetch до Parse; лог fetch done"
  - "cmd/aggregator/main.go — slog во всех подкомандах, LO-09, worker [run-once|accept-baseline|sanity-report]"
  - "docker-compose.dev.yml — dev-PostgreSQL 17 (aggregator-dev, 127.0.0.1:5434, именованный том)"
affects: [03-06-first-run, 03-07-decision, phase-4-bot-alerts, phase-6-deploy]

actuals:
  tokens: 16400   # chars/4: новые файлы 51072 Б + git diff 14487 Б
  tasks: 3
  commits: 0      # коммиты делает владелец вручную
  plan_head_before: 75bb78b

tech-stack:
  added: []
  patterns:
    - "Затвор хоста: мьютекс на хост + время конца предыдущего запроса; пауза перед каждой попыткой, повторы включительно"
    - "Ожидания через time.NewTimer + select с ctx.Done(); часы и сон Fetcher подменяются в тестах"
    - "Задержка планировщика считается от последнего started_at в БД — перезапуск не запрашивает сайт повторно"
    - "Сырые файлы: temp + os.Rename, 0700/0600, имена только из run_id и номера"
    - "Ошибки конфигурации называют переменную, но не повторяют значение"

key-files:
  created:
    - internal/ingest/fetch.go
    - internal/ingest/fetch_test.go
    - internal/ingest/config.go
    - internal/ingest/config_test.go
    - internal/ingest/rawstore.go
    - internal/ingest/rawstore_test.go
    - internal/ingest/scheduler.go
    - internal/ingest/scheduler_test.go
    - internal/ingest/report.go
    - internal/ingest/report_test.go
    - internal/ingest/sources/marathonec/marathonec.go
    - internal/ingest/sources/marathonec/marathonec_test.go
    - docker-compose.dev.yml
  modified:
    - internal/ingest/runner.go
    - internal/ingest/runner_test.go
    - cmd/aggregator/main.go
    - .env.example
    - .gitignore

key-decisions:
  - "RawDoc.URL — запрошенный адрес, а не адрес после редиректа: у внешних ссылок URL события = CalendarURL (D-13), и payload не меняется от редиректа на тот же хост; Parse по-прежнему берёт doc.URL"
  - "Параметры Fetcher тоже читаются из окружения (FETCH_*), как требует must-have ING-01"
  - "Санити-отчёт показывает записи с несколькими областями отдельной строкой «несколько областей», а не как «область пуста»"
  - "После ошибки Run (например, ErrRunInProgress) планировщик ждёт 1 мин, прежде чем снова считать задержку"
  - "worker run-once не принимает --run; значения <0 и лишние аргументы — usage, код 2"

metrics:
  started: 2026-09-29T17:30:00Z
  completed: 2026-09-29T18:05:00Z
  duration: "~35 мин"
---

# Phase 3 Plan 05: Fetcher, планировщик, сырые ответы, санити-отчёт и подкоманды worker — итоги

Теперь worker может ходить в сеть. `Fetcher` выполняет все правила MVP §5.3 и соблюдает Crawl-delay 40 с через затвор хоста, повторы включительно. Марафонец получил сетевой `Fetch` и политику `NoFallback`, то есть RU не подставляется, пока владелец не решит (D-07). Конфигурация читается из окружения, пороги ING-07 тоже. Сырые ответы пишутся на диск, при этом хранятся только два последних прогона, а TTL 14 суток. Планировщик ведёт отсчёт от `started_at` в БД. Санити-отчёт выдаётся в Markdown. У `worker` появились подкоманды `run-once`, `accept-baseline` и `sanity-report`. Для реальных данных поднимается отдельная dev-PostgreSQL. К marathonec.ru ни тесты, ни проверки не обращались.

## Выполнено

| Задача | Что | Коммит |
|---|---|---|
| 1 (tdd) | `fetch.go`, `config.go`, `marathonec.go` + 14 тестов — `FETCH OK` | не закоммичено: коммиты делает владелец |
| 2 (tdd) | `rawstore.go`, `scheduler.go`, `report.go`, `Runner.Raw` + 6 тестов — `SCHEDULE RAW REPORT OK` | не закоммичено |
| 3 | `main.go` (slog, LO-09, подкоманды worker), `docker-compose.dev.yml`, `.env.example`, `.gitignore` — `DEV ENV OK`, `CLI OK` | не закоммичено |

## Результаты проверок

- `FETCH OK`, `SCHEDULE RAW REPORT OK`, `DEV ENV OK`, `CLI OK`. В `DEV ENV OK` не выполнялся один подпункт, `git check-ignore -q .env`: локальный guard секретов блокирует любую команду с этим именем файла. Правило `.env` в `.gitignore` было и раньше, план его не менял. Остальные подпункты команды прошли.
- `CLI OK`: команда пересоздаёт тестовую БД. Коды выхода: usage — 2; без `OWNER_CONTACT` — 1 с упоминанием переменной; при неизвестном источнике — 1 со списком `marathonec`; `sanity-report` без прогонов — 1. По вставленному снимку `sanity-report` выводит «Минская область — НЕТ в перечне» и «## Решение». `accept-baseline` переводит прогон в `ok:true:true`. Демон пишет `next run … in 6h19m9s`, по SIGTERM завершается с кодом 0, а новых прогонов не появляется (`count = 1`). Маркер контакта и строка подключения в выводе не встречаются.
- `go test -p 1 -count=1 ./...` с `TEST_DATABASE_URL`: 77 PASS, 0 SKIP, 0 FAIL (тесты верхнего уровня; было 57, добавлено 20). Без неё: 53 PASS, 24 SKIP (тесты с БД), 0 FAIL.
- `go vet ./...` без замечаний, `gofmt -l .` пусто, `git diff --check` чисто, включая новые файлы, проверенные через `git add -N`. Прямых зависимостей по-прежнему три. SQL есть только в `internal/store`. Dockerfile и `docker-compose.yml` отсутствуют.
- SHA-256 фикстуры `dd4a58d3…3ddf` не изменился.
- `TestLoadConfig/defaults` очищает все переменные LoadConfig. Проверено с `RAW_DIR`, `QUALITY_MIN_RUNS` и `OWNER_CONTACT` в окружении: тест проходит.

## TDD

- Задачи 1 и 2: RED был ошибкой компиляции, потому что тесты написаны до API. **Это слабый RED.** Поэтому поведение проверено мутациями; исходник каждый раз восстанавливался, после чего тесты снова зелёные. Результаты:
  - Retry-After игнорируется — падают `TestFetchRetry` и `TestFetchRetryAfterTooLong`.
  - Затвор хоста выключен — `TestFetchRetry`, `TestFetchHostGate`, `TestFetchCancel`.
  - Редирект на другой хост разрешён — `TestFetchRedirect`.
  - Нет проверки размера — `TestFetchOversize`.
  - 4xx повторяются — `TestFetchNoRetry4xx`.
  - Нет проверки UTF-8 — `TestFetchNonUTF8`.
  - Нет предела MaxRetryWait — `TestFetchRetryAfterTooLong`.
- Первые два мутанта, «Retry-After игнорируется» и «нет UTF-8», сначала не компилировались из-за неиспользуемой переменной и импорта. Их переписали так, чтобы код собирался, и только после этого засчитали.
- Мутации для `rawstore`, `scheduler` и `report` не делались. Их тесты проверяют конкретные значения: имена файлов, задержки, строки отчёта.

## Deviations from Plan

**1. [Rule 2] Переменные `FETCH_*`.** В must-have ING-01 сказано: «все значения — в FetchConfig и переменных окружения». При этом в списке переменных действия 2 их нет. Добавлены `FETCH_TIMEOUT`, `FETCH_CONNECT_TIMEOUT`, `FETCH_MAX_BYTES`, `FETCH_ATTEMPTS`, `FETCH_BACKOFF_BASE`, `FETCH_BACKOFF_MAX`, `FETCH_MAX_RETRY_WAIT`, `FETCH_HOST_MIN_INTERVAL`, они перечислены и в `.env.example`. Отрицательные значения отклоняются. Нулевые отклоняются там, где ноль ломает работу: интервал, число попыток, размер, таймауты.

**2. [Rule 1] Ошибка редиректа не повторяется.** `http.Client` возвращает ошибку `CheckRedirect` как `*url.Error`. Прототип из research принял бы её за сетевую ошибку и повторил бы запрос. Теперь такая ошибка определяется через `errors.Is(err, errRedirect)` и не повторяется. То же касается отмены контекста: `ctx.Err()` возвращается без повтора.

**3. Строка «несколько областей» в санити-отчёте** (по заметке 03-03). Без неё такие записи попали бы в «область пуста». В сводке добавлены поля «область пуста: N» и «несколько областей: N» (оба — число строк). В таблице такие записи стоят после подозрительных и перед «в перечне РФ».

**4. `collect` стал методом `Runner`** и получает `runID`, потому что сохранять сырые ответы нужно между Fetch и Parse. Кроме того, добавлен лог `fetch done` (docs, bytes) из словаря research Pattern 11.

**5. Пауза 1 мин после ошибки `Run`** в планировщике. Без неё `ErrRunInProgress` (например, пока владелец вручную выполняет `run-once`) приводил бы к циклу без пауз с запросами к БД. К сайту эта ошибка не обращается.

**6. `sanity-report` по прогону без снимка** сообщает в ошибке номер и статус последнего прогона. Этот случай включает и уже принятый прогон. Research предлагал строить отчёт после принятия по `payload` активных `source_events`, но план этого не требует, и это не сделано.

## Known Stubs

Нет.

## Известные пределы

- Редирект на тот же хост выполняется внутри одного `client.Do`, без паузы Crawl-delay. Всего таких переходов не больше 3. Для `/calendar-beg/` редиректа не ожидается.
- Общего предела параллельных хостов нет: по горутине на источник (комментарий `ponytail:` в `scheduler.go`). Семафор понадобится, когда источников станет больше четырёх.
- Затвор хоста работает внутри одного процесса. Если одновременно работают демон и ручной `run-once`, затвор между ними не действует. Демон отсчитывает интервал от последнего `started_at`, поэтому после ручного прогона он ждёт около 6 ч. Уникальный индекс `running` не даёт двум прогонам источника идти одновременно.
- LO-09 (второй сигнал сразу завершает процесс) реализован, но автотестом не покрыт.

## Threat Flags

Новой поверхности сверх threat_model нет. Покрытие угроз:
- T-03-25: `TestFetchRedirect`, Transport без Proxy.
- T-03-26: `TestFetchOversize`, таймауты.
- T-03-27: `TestFetchNoRetry4xx`, `TestFetchHostGate`, `TestNextDelay`, демон в `CLI OK` не создал прогон.
- T-03-28: `TestRawStoreSave` (в meta.json нет UA), `TestFetchNoRetry4xx` (в ошибке нет UA), маркер контакта в `CLI OK`.
- T-03-29: скан URL с паролем, compose без значения пароля, порт 127.0.0.1.
- T-03-30: `TestRawStoreBadSource`, права 0700/0600, `/data/` в `.gitignore`.
- T-03-32: ожидания через таймер и ctx, LO-09.

## Для следующих планов (03-06)

- Окружение: `DATABASE_URL=postgres://aggregator@127.0.0.1:5434/aggregator_dev?sslmode=disable`, `PGPASSWORD` (совпадает с `DEV_POSTGRES_PASSWORD`), `OWNER_CONTACT`, при желании `LOG_FORMAT=text` и `RAW_DIR`. Бинарник `.env` не читает, переменные нужно экспортировать в shell.
- Команды: `docker compose -f docker-compose.dev.yml up -d --wait` → `aggregator migrate` → `aggregator worker run-once --source marathonec` (ожидается `suspect`, D-05) → `aggregator worker sanity-report --source marathonec > .planning/phases/03-first-source/03-SANITY-REPORT.md` → решение владельца → `aggregator worker accept-baseline --source marathonec`.
- Сырой ответ первого прогона: `data/raw/marathonec/<run_id>-0.html` и `.meta.json`.

## Self-Check: PASSED

- Все 13 созданных файлов на месте, 5 изменённых видны в `git status --short`.
- Коммитов нет по правилу проекта: `commits: 0` при `plan_head_before` = HEAD `75bb78b`. Изменения ждут коммита владельца.
