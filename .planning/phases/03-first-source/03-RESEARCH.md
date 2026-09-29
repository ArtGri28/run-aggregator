# Phase 3: First Source - Research

**Researched:** 2026-09-29
**Domain:** Go-ingestion (HTTP fetch + HTML-парсинг + контроль качества прогона + наблюдение за записями) поверх готовых `store`/`normalize`/PostgreSQL 17 из Phase 2; первый источник — Марафонец (`marathonec.ru/calendar-beg/`)
**Confidence:** HIGH по стеку, контракту, SQL и fetch (проверено кодом и запуском); MEDIUM по разметке самой страницы Марафонца (страница по ограничению задачи не запрашивалась, структура — по отчёту Phase 1; подтверждается первой задачей фазы, D-01)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Уже зафиксировано до обсуждения (не пересматривать)**
- Одна страница `/calendar-beg/` за прогон; `Crawl-delay: 40` соблюдается; URL с `?` не используются (01-SOURCES-REPORT §2.1).
- `external_id` = WP id из `<tr id="post-row-<id>">`; повторное использование поста на следующий год — это перенос даты того же события, а не новое событие.
- Дата — только из столбца даты `dd/mm/yyyy` (слаг месяца не использовать); `registration_url` — только из внешних ссылок строки, страницы событий не запрашиваются.
- Расписание раз в 6 часов со случайным сдвигом; пороги `suspect`/`failed` и параметры надёжности — стартовые значения MVP §5.3–§5.4.
- `User-Agent` с контактом проекта: контакт передаётся через переменную окружения (`OWNER_CONTACT`) и в репозиторий не попадает (решение Phase 1).
- HTML-парсер — новая зависимость, одобрение владельца получено (D-10).

**Проверка captcha**
- **D-01:** Первая задача фазы — скачать одну страницу `/calendar-beg/` как golden-фикстуру в `internal/ingest/sources/marathonec/testdata/` и по её телу установить контекст всех вхождений `captcha`. Это тот же запрос, что нужен для golden-тестов. Если на странице настоящий челлендж (нет таблицы с `post-row`), фаза останавливается, и владелец решает о замене источника на ПроБЕГ. Антибот не обходится ни при каком исходе.
- **D-02:** В обычной работе челлендж распознаётся по структуре, а не по словам: нет таблицы или 0 строк `post-row` → прогон `failed` с причиной «структура не найдена». Слово `captcha` само по себе ни на что не влияет; резкое падение числа строк ловит проверка `suspect`.

**Где работает worker в Phase 3**
- **D-03:** Worker и БД с реальными данными — локально на Mac владельца: отдельная dev-PostgreSQL в Docker (не тестовая БД из `docker-compose.test.yml`, у которой tmpfs и trust без пароля для тестов), запуск `go run`/бинарника вручную или циклом планировщика. Запросы к Марафонцу идут с IP владельца. VPS в Phase 3 не используется и не меняется. — **Reversibility:** reversible — перенос на VPS в Phase 6 не меняет код, только окружение.
- **D-04:** Golden-фикстура скачивается с Mac, одним GET с идентифицирующим `User-Agent` и с соблюдением `Crawl-delay`.

**Первый прогон и базовый уровень**
- **D-05:** Самый первый прогон источника (нет ни одного принятого прогона) всегда получает `suspect` и ничего не применяет, пока владелец не примет его вручную. Это уточняет MVP §5.4 («пока базы нет — абсолютные проверки»): абсолютные проверки (`parsed ≥ 1`, `rejected ≤ 30 %`) действуют со второго прогона и до накопления 3 прогонов `ok`, затем — медиана последних 7 `ok`. Первый прогон одновременно служит точкой ручной проверки санити-отчёта RU (D-07).
- **D-06:** Принятие прогона — подкоманда CLI (в MVP §5.4 обозначена как `worker accept-baseline --source X`; точное имя и аргументы определяет планировщик). Она применяет уже сохранённый разобранный результат указанного прогона `suspect` без повторного запроса к сайту и делает его точкой отсчёта для базы. Значит, разобранные данные прогона `suspect` должны сохраняться до принятия или до следующего прогона. В Phase 4 эту же операцию можно повесить на админ-команду бота.

**Санити-проверка RU (ING-11)**
- **D-07:** Окончательное подтверждение fallback `RU` = автоматический отчёт + решение владельца. На первом прогоне формируется отчёт: все значения области и города из данных, каждое помечено «есть в перечне субъектов РФ / нет». Владелец просматривает отчёт и записывает решение (значение, дата, «Решение принял: владелец») в отчёт фазы. До записанного решения fallback `RU` для Марафонца выключен: записи без явной страны не получают `RU` молча (в `normalize` для этого есть отдельная политика источника). — **Reversibility:** reversible — политика источника меняется одной настройкой, данные перенормализуются из `payload`.
- **D-08:** Для отчёта и последующих проверок нужен полный перечень субъектов РФ (около 89 названий с распространёнными вариантами написания). Это справочник регионов, а не словарь синонимов городов: решение D-03 из Phase 2 («минимальный словарь городов») к нему не относится.
- **D-09:** После подтверждения строка с явно иностранной областью или страной (например, «Беларусь») сохраняется в `source_events` с `match_state = out_of_scope` и события не создаёт. Строка с областью, которой нет в перечне и которая не распознана как иностранная, получает `RU` по подтверждённому fallback, но считается в прогоне и пишется в структурный лог. Если доля таких нераспознанных областей выше порога (конфигурируемого), прогон становится `suspect`. Остальные строки прогона применяются как обычно.

**HTML-парсер**
- **D-10:** Для разбора HTML используется `golang.org/x/net/html` — одобрено владельцем 2026-09-29 при планировании Phase 3 (MVP §3.1). `goquery` не добавляется. Версия фиксируется точно, легитимность пакета проверяется так же, как для pgx/goose в Phase 2. — **Reversibility:** reversible — замена парсера затрагивает только `internal/ingest/sources/marathonec/`.

### На усмотрение исполнителя и планировщика (Discretion)
- Хранение сырых ответов (ING-10): каталог, число документов и TTL — в рамках MVP §5.1 (достаточно последнего ответа и одного предыдущего).
- Как именно хранится разобранный результат прогона `suspect` для D-06 (таблица, `payload` в `source_events`, файл) — решает исследование и планировщик.
- Формат структурных логов и набор полей `slog` (MON-02); порог доли нераспознанных областей для D-09.
- Rate limit на хост 1 запрос/с из MVP §5.3 перекрывается для Марафонца `Crawl-delay: 40`; конкретный механизм — на усмотрение планировщика.
- Отложенные находки ревью Phase 2, которые касаются данных Марафонца и берутся в эту фазу: проверка правдоподобия дистанции в Validate (склейка «100 200 400 м»), правило римских цифр в `TitleNorm` («MIX RUN»), `5K`/`10K`, нераспознанное явное название страны («Russian Federation», ME-04). Решать по реальным названиям из фикстуры; low-находки 02-REVIEW.md — если затрагивают код, который фаза и так меняет.

### Deferred Ideas (OUT OF SCOPE)
- Перенос worker и БД на VPS, production Compose, бэкапы — Phase 6.
- Алерты админу в Telegram о `failed`/`suspect` и админ-команда принятия прогона — Phase 4.
- Уборка проектного доступа к VPS (ключ `run-agg-phase1`, `runagg`, sudoers) — отслеживается в STATE.md, не позже Phase 6.
- Закрытие пробелов карточки ПроБЕГ (sitemap/RSS, API, частота обновления) — при подключении ПроБЕГ в Phase 5.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| ING-01 | `Fetch`: таймауты, 3 попытки, backoff с jitter, `Retry-After`, лимит размера, rate limit на хост, UA с контактом; конфигурируемо | §Fetch (stdlib, проверенный прототип: 503+Retry-After → ретрай, 403 → без ретрая, переполнение → ошибка, а не усечение); §Code Examples 1 |
| ING-02 | `Parse` — чистая функция; фикстуры `testdata/` и golden-тесты | §Golden-тесты; `x/net/html` v0.59.0; §Code Examples 2, 5 |
| ING-03 | `Validate`: обязательные поля, дата в `[сегодня−1 день, сегодня+3 года]`, отклонённые считаются | §Pipeline и счётчики; §Отложенные находки Phase 2 (правдоподобие дистанции) |
| ING-04 | Идемпотентный upsert `source_events` | §Store: изменения; §Идемпотентность (apply обрабатывает ВСЕ валидные записи, а не только changed) |
| ING-05 | Канонические `events` + `event_distances`; `updated_at` только при изменении | §Store: `SaveEventFor` атомарно создаёт и связывает; статус не перезаписывается |
| ING-06 | Каждый запуск в `ingest_runs` со счётчиками | §Миграция 00002, §Семантика счётчиков |
| ING-07 | Авто-статус `ok/suspect/failed`, пороги конфигурируемы, ручное принятие | §Оценка качества (чистая `Evaluate`), §Snapshot и accept-baseline |
| ING-08 | `active/missing/gone` только по здоровым прогонам, только `start_date ≥ сегодня`; `gone` → только `is_listed` | §Наблюдение (SQL проверен на PostgreSQL 17) |
| ING-09 | Планировщик 6 ч + jitter, таймаут прогона, graceful stop | §Планировщик |
| ING-10 | Сырые ответы на диск: количество + TTL | §Сырые ответы |
| ING-11 | `country_code`: явная страна / подтверждённый RU-fallback / `out_of_scope` | §RU-fallback и санити-отчёт; §Справочник субъектов РФ |
| ING-13 | Описание ≤ 1000, изображения не сохраняются | §Тестирование: синтетический SourceEvent с длинным описанием + проверка схемы |
| MON-01 | Здоровье источника в `ingest_runs` и логах (статус, подряд сбоев, причина) | §Наблюдаемость: запрос `SourceHealth` поверх `ingest_runs`, без новой таблицы |
| MON-02 | Структурные логи `log/slog` во всех подкомандах | §Логи (словарь полей, настройка default-хендлера в `main`) |
</phase_requirements>

## Project Constraints (проектные правила)

Источник: проектный файл правил (глобальные пользовательские правила Git для этого проекта переопределены проектным исключением; источник истины — `MVP.md` §3.3).

- Разрешены `git add`, `git commit`, обычный `git push` в настроенный remote (remote пока не настроен); **запрещено без подтверждения владельца:** `push --force`, `--force-with-lease`, `reset --hard`, удаление веток/remote и прочие разрушительные операции. Теги не создавать.
- Сообщения коммитов не содержат упоминаний ИИ-инструментов, автоматизации планирования и `Co-Authored-By` с такими именами.
- Источник истины — `MVP.md`; планирование — `.planning/`.
- Наследуется из Phase 2 (`02-CONTEXT`, `02-SECURITY`): весь SQL только в `internal/store` константами с `$N`; секреты только из окружения; новая зависимость только с одобрением (для этой фазы одобрена одна — `golang.org/x/net`); интеграционные тесты пропускаются без `TEST_DATABASE_URL`.
- Для исследования (эта сессия): запросов к `marathonec.ru` не делалось (D-01 — первая задача фазы).

## Summary

Фаза строит поверх готовых `store` и `normalize` тонкий слой `internal/ingest`: `Fetcher` (stdlib), контракт источника, чистые стадии `Parse → Validate → Normalize`, чистая функция оценки качества `Evaluate`, применение (`apply`) и шаг наблюдения, планировщик в `worker`, три подкоманды CLI. Единственная новая зависимость — `golang.org/x/net` v0.59.0 (пакет `html` зависит только от stdlib и `html/atom`, транзитивного кода не тянет; `go mod tidy` добавит его в `require` и поднимет `x/text` до v0.42.0 и `x/sync` до v0.23.0 как indirect). Всё остальное — stdlib, как требует MVP §3.1.

Ключевое проектное решение фазы: **стадии до записи в БД чистые и выполняются для всего прогона целиком** (Parse → Validate → Normalize по всем строкам → статистика → `Evaluate`), и только при `ok` начинается запись. Отсюда три следствия. (1) «`suspect`/`failed` ничего не применяют» выполняется структурно, а не проверками внутри цикла записи. (2) Разобранный результат прогона `suspect` — просто `[]SourceEvent`, его можно положить в JSONB и позже применить без сети (D-06). (3) `apply` обрабатывает **все** валидные записи, а не только изменённые, поэтому смена политики RU-fallback после решения владельца (D-07) сама «лечит» ранее `out_of_scope` строки на следующем прогоне или при принятии — отдельная команда перенормализации не нужна.

Схема: одна миграция `00002` — три колонки в `ingest_runs` (`accepted_at`, `metrics JSONB`, `snapshot JSONB`) и частичный уникальный индекс «один `running` на источник». Отдельных таблиц для снимка и справочника регионов не нужно: снимок — JSONB в строке прогона, справочник субъектов РФ — Go-таблица в `normalize` (D-08, 89 записей, проверено компиляцией и тестом коллизий). Наблюдение `active/missing/gone` и `is_listed` — три set-based SQL-оператора в одной транзакции; сценарий (в том числе «`gone` не меняет `events.status`», «прошедшие события не трогаются», уникальный индекс `running`) прогнан на PostgreSQL 17 в одноразовом контейнере.

**Primary recommendation:** держать pipeline «чистые стадии целиком → `Evaluate` → запись только при `ok`»; добавить `golang.org/x/net` v0.59.0 как единственную зависимость; миграция `00002` (3 колонки + 1 индекс); `apply` идемпотентно проходит все валидные записи; первой задачей фазы выполнить D-01 и остановиться, если в теле страницы нет строк `post-row`.

## Architectural Responsibility Map

В проекте нет браузера/фронтенд-сервера; «ярусы» здесь — процессы и хранилища.

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| HTTP-загрузка, retry, rate limit, лимит размера | Worker (`internal/ingest/fetch`) | — | Бот не ходит на сайты (MVP §3) |
| Разбор HTML → `SourceEvent` | Worker (`sources/marathonec`, чистая функция) | — | Source-specific код, тестируется на файлах |
| Validate / Normalize | Worker (`ingest` + `normalize`, чистые) | — | Детерминированы, перезапускаются поверх `payload` |
| Оценка качества прогона (`Evaluate`) | Worker (чистая функция) | БД (медиана базы — запрос) | Решение принимается до записи |
| Запись `source_events` / `events` / `event_distances` | БД через `internal/store` | Worker (порядок вызовов) | Весь SQL только в `store` |
| Наблюдение `active/missing/gone`, `is_listed` | БД (set-based SQL в `store`) | Worker (вызов после `ok`) | Транзакционная согласованность |
| Сырые ответы (ING-10) | Файловая система (`RAW_DIR`) | — | Вне PostgreSQL по MVP §5.1 |
| Хранение снимка `suspect` (D-06) | БД (`ingest_runs.snapshot`) | — | Переживает перезапуск процесса, чистится автоматически |
| Расписание | Worker (цикл с таймером, stdlib) | БД (`ingest_runs.started_at` как «последняя попытка») | Защита от шторма запросов при рестартах |
| Принятие прогона / санити-отчёт | CLI (`worker <sub>`) | БД | Владелец, ручной шаг |
| Справочник субъектов РФ | `normalize` (Go-таблица) | — | Чистые данные, без БД |

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `golang.org/x/net` (пакет `html`) | **v0.59.0** [VERIFIED: proxy.golang.org, `Time` 2026-09-08T19:18:02Z] | Разбор HTML5 в дерево узлов (`html.Parse`, `Node.Descendants()`, `Node.ChildNodes()`) | Решение владельца D-10; официальный репозиторий Go; HTML5-совместимый парсер, вставляет `<tbody>` как браузер |
| stdlib: `net/http`, `io`, `context`, `time`, `math/rand/v2`, `encoding/json`, `os`, `flag`, `log/slog`, `sync`, `net/url`, `strconv`, `slices` | Go 1.27.1 | Fetch, retry, планировщик, CLI, логи | MVP §3.1: планировщик/limiter/конфиг — stdlib; новых зависимостей не добавлять |
| `github.com/jackc/pgx/v5` v5.11.0, `pressly/goose/v3` v3.28.0 | уже в `go.mod` | БД и миграции | Без изменений [VERIFIED: go.mod] |

`go.mod` требует `go 1.26.0` от `x/net` v0.59.0 — совместимо с Go 1.27.1 [VERIFIED: `https://proxy.golang.org/golang.org/x/net/@v/v0.59.0.mod` — «go 1.26.0», require: x/crypto v0.57.0, x/sys v0.48.0, x/term v0.46.0, x/text v0.42.0].

Эффект на `go.mod`, проверенный в копии модуля (`go get golang.org/x/net@v0.59.0` в scratch, проект не менялся): добавляется `golang.org/x/net v0.59.0`; `golang.org/x/text` v0.41.0 → v0.42.0; `golang.org/x/sync` v0.22.0 → v0.23.0. `go mod verify` — «all modules verified»; `go vet` — чисто. `go.sum` строки `golang.org/x/net v0.59.0 h1:5zfYln+w5XCxwrnMMJPufRgNoXEaGxl0wo5GqPXyues=` и `/go.mod h1:2DA/G1UfVbCpQPeWTmMPGY7Cs2PkBkwu743bVX5PIVg=` совпадают с ответом `sum.golang.org/lookup/golang.org/x/net@v0.59.0` [VERIFIED].

Зависимости пакета: `go list -deps golang.org/x/net/html` → только stdlib и `golang.org/x/net/html/atom`. **Пакет `html/charset` НЕ использовать**: он тянет `x/text/encoding/*` (кодировки) — лишний код; вместо этого проверять `utf8.Valid` и падать с внятной причиной (сайт на WordPress отдаёт UTF-8) [VERIFIED: `go list -deps` в scratch].

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `x/net/html` (D-10) | `goquery` | Отклонено владельцем (D-10): лишняя зависимость (cascadia) ради CSS-селекторов, а обход одной таблицы — ~40 строк |
| `Descendants()` итератор | ручная рекурсия | Итераторы `Descendants/ChildNodes/Ancestors` есть с v0.31.0 [CITED: pkg.go.dev/golang.org/x/net@v0.59.0/html]; код короче |
| `time.Ticker` | `robfig/cron` и т.п. | MVP §3.1 запрещает; интервал 6 ч + jitter проще таймером |

**Installation:**
```bash
go get golang.org/x/net@v0.59.0 && go mod tidy
```

**Version verification:** `proxy.golang.org/golang.org/x/net/@latest` → `v0.59.0`; `@v/list` заканчивается на `v0.59.0`; `vuln.go.dev` — последняя запись об уязвимости `golang.org/x/net` исправлена в 0.56.0 (`GO-2026-5942`), значит v0.59.0 их все включает [VERIFIED: vuln.go.dev/index/modules.json, 2026-09-29].

## Package Legitimacy Audit

Seam `package-legitimacy check` поддерживает только `npm|pypi|crates` (см. 02-RESEARCH); модуль Go проверен так же, как pgx/goose: прокси модулей + checksum-база + GitHub. Слово «зависимость» в MVP §3.1 — одна: `golang.org/x/net`.

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `golang.org/x/net` v0.59.0 | proxy.golang.org / sum.golang.org | репозиторий с 2014-12-04 (11+ лет); v0.59.0 от 2026-09-08 | n/a (Go proxy не публикует счётчики); один из базовых `x/` модулей Go-команды, уже в графе модулей проекта (v0.58.0 приходит транзитивно) | `go.googlesource.com/net`, зеркало github.com/golang/net (3044 звезды, не archived, `pushed_at` 2026-09-28) | OK (ручная проверка) | Approved (D-10) |

- Имя модуля получено из официальной документации (pkg.go.dev, решение владельца D-10), а не из поиска/памяти; хеш в `go.sum` сверен с `sum.golang.org` [VERIFIED].
- `go.sum` уже содержит `golang.org/x/net v0.58.0/go.mod`-запись через граф; после `go get` версия v0.59.0 фиксируется точно (D-10: «версия фиксируется точно»).
- Скриптов установки у Go-модулей нет (аналога `postinstall` не существует).

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

## Architecture Patterns

### System Architecture Diagram

```
                      ┌───────────── worker (loop, 6ч+jitter) ─────────────┐
 CLI: worker run-once │                                                      │
 CLI: worker          ▼                                                      │
        Scheduler ──► Runner.Run(ctx 30min, source)                          │
                        │ 1. store.StartRun ─► ingest_runs(status=running)   │
                        │ 2. src.Fetch ─► Fetcher.Get (gate 40с, retry) ─► RawDoc
                        │        └─► rawstore.Save (latest/previous, диск)   │
                        │ 3. src.Parse(RawDoc) ─► []SourceEvent  (чистая)    │
                        │ 4. Validate+Normalize по ВСЕМ строкам (чистые)     │
                        │        └─► []Prepared{SourceEvent, Event, verdict}, Stats
                        │ 5. store.Baseline ─► Evaluate(Stats, Baseline, Thresholds)
                        │            │
                        │   ┌────────┼───────────────┐
                        │   ▼        ▼               ▼
                        │ failed   suspect          ok
                        │ (ничего  (snapshot=JSONB  6. apply: для каждой валидной записи
                        │  не       в ingest_runs;     UpsertSourceEvent → (in scope?)
                        │  пишет)   ничего не          SaveEventFor / SetMatch(out_of_scope)
                        │           применяет)       7. store.Observe(run): seen→active,
                        │                               not seen→missing/gone, is_listed
                        │ 8. store.FinishRun(status, counters, error=причина, metrics)
                        └──────────────────────────────────────────────────────┘
 CLI: worker accept-baseline --source X ─► берёт snapshot последнего suspect ─► шаги 6-7 ─► status=ok, accepted_at
 CLI: worker sanity-report  --source X ─► snapshot/payload ─► таблица «область/город/в перечне РФ?»
```

### Recommended Project Structure

```
internal/
  ingest/
    source.go          # SourceEvent, RawDoc, Source (интерфейс контракта), Policy
    config.go          # Config из env: Fetch/Quality/Schedule (значения по умолчанию в коде)
    fetch.go           # Fetcher: Get, hostGate, backoff, retryAfter
    rawstore.go        # latest/previous на диск, TTL
    prepare.go         # Validate + Normalize: SourceEvent → Prepared, Stats
    quality.go         # Evaluate (чистая), median
    runner.go          # Run(ctx, src), apply, финализация (WithoutCancel)
    scheduler.go       # Loop(ctx, sources)
    report.go          # SanityReport(snapshot) → text
    sources/marathonec/
      marathonec.go    # New(), Code, Policy, Fetch (один GET)
      parse.go         # Parse(RawDoc) ([]SourceEvent, error)
      parse_test.go    # golden
      testdata/        # calendar-beg.html, calendar-beg.golden.json, edge-*.html
  normalize/
    regions.go         # ClassifyRegion + rfSubjects (89) + foreignNames   (новый)
  store/
    runs.go            # StartRun, FinishRun, Baseline, SourceHealth, SaveSnapshot, LoadSnapshot
    observe.go         # Observe (missing/gone/is_listed)
    events.go          # UpsertSourceEvent(+runID, +возврат event_id), SaveEventFor
migrations/00002_ingest_runs_quality.sql
cmd/aggregator/main.go # worker: под-подкоманды; slog default; stop() после ctx.Done
docker-compose.dev.yml # dev PostgreSQL (D-03), отдельно от docker-compose.test.yml
```

Интерфейсы — только там, где реально нужны: `Source` нужен планировщику (несколько источников, ING-12); всё остальное — структуры и функции.

### Pattern 1: контракт источника (ING-12 закладывается сейчас)

**What:** общий `SourceEvent` со строковыми «сырыми» полями; source-specific — только `Fetch` и `Parse`. Вся нормализация — в `normalize` по политике источника.
**Why:** формат даты (`dd/mm/yyyy` у Марафонца, `dd.mm` у ПроБЕГ) и «Область, Город» (ПроБЕГ) уже поддержаны `normalize.Date`/`SplitPlace` — добавление ПроБЕГа в Phase 5 не меняет ни интерфейс, ни runner.

```go
// internal/ingest/source.go
// SourceEvent — распарсенная запись «как есть». Марафонец: Country пуст.
// В payload (jsonb) попадает json.Marshal этой структуры: порядок полей фиксирован
// объявлением, значит байты, а с ними и content_hash, детерминированы.
type SourceEvent struct {
	ExternalID      string `json:"external_id"`
	URL             string `json:"url"` // в payload: смена URL меняет hash (LO-01 обходится без правки store)
	Title           string `json:"title"`
	Date            string `json:"date"`      // "17/10/2026"
	Distances       string `json:"distances"` // видимый текст столбца дистанций
	City            string `json:"city"`
	Region          string `json:"region"`
	Country         string `json:"country,omitempty"`
	TypeLabel       string `json:"type"` // "trejl" / "shosse"
	RegistrationURL string `json:"registration_url,omitempty"`
	Description     string `json:"description,omitempty"`
}

type RawDoc struct {
	URL       string
	Body      []byte
	FetchedAt time.Time
}

type Policy struct {
	CountryFallback normalize.CountryPolicy // Марафонец: NoFallback до записи решения D-07, затем RUOnlyFallback
	CrawlDelay      time.Duration           // Марафонец: 40*time.Second (robots.txt), иначе Config.HostMinInterval
}

type Source interface {
	Code() string
	Policy() Policy
	Fetch(ctx context.Context, f *Fetcher) ([]RawDoc, error) // Марафонец: один GET
	Parse(doc RawDoc) ([]SourceEvent, error)                 // чистая; ErrStructureNotFound → failed
}

var ErrStructureNotFound = errors.New("структура не найдена") // D-02
```

Регистрация («строка регистрации», MVP §5.2) — в `cmd/aggregator` (`[]ingest.Source{marathonec.New(cfg)}`): пакет источника импортирует `ingest`, обратный импорт дал бы цикл.

### Pattern 2: pipeline «чистые стадии целиком → решение → запись»

`Prepare(events []SourceEvent, now time.Time, pol Policy) (prepared []Prepared, st Stats)`:

1. **Дубликаты `external_id` внутри страницы** → вторая запись `rejected` («дубликат external_id»), иначе двойной upsert одной строки в одном прогоне.
2. **Validate** (ING-03): `ExternalID`, `Title` непусты; `normalize.Date(s, now)` ок; дата в `[today−1d, today+3y]`, `today` = UTC-дата `now` (сдвиг ±3 ч относительно МСК доброкачественен: окно `−1 день`). Дата вне диапазона → `rejected` **и** `DateOutOfRange++` (нужен для проверки «>5 %» MVP §5.4).
3. **Normalize**: `TitleNorm`, `City`/`CityKey`, `Region`, `Type(TypeLabel)`, `Distances(Distances)` (**только** заголовок и столбец дистанций, никогда описание: LO-06), `Description(…)`, `Timezone`, страна (§RU-fallback).
4. Плаузибельность дистанции (§Отложенные находки).
5. `Stats`: `Fetched` (число сырых записей, найденных в документе), `Parsed` (записи, превращённые Parse в `SourceEvent`), `Rejected`, `DateOutOfRange`, `OutOfScope`, `UnrecognizedRegion`, `InScope`, `Empty[поле]`.

**Семантика счётчиков ingest_runs (предложение, planner подтверждает):** `fetched` = строк-кандидатов в документе (`post-row`); `parsed` = строк, из которых Parse получил `SourceEvent`; `rejected` = не прошедшие Validate; `out_of_scope` = валидные, но вне РФ (сохранены в `source_events`); `upserted` = валидные записи, прошедшие через `UpsertSourceEvent` (включая неизменённые и `out_of_scope`); подробности `created/changed/unchanged` — в `slog`. Если Parse не пропускает ни одной строки-кандидата, `fetched == parsed`, а `rejected/fetched` из MVP §5.4 остаётся осмысленной долей.

### Pattern 3: оценка качества — чистая функция

Прототип (скомпилирован и проверен `go vet` в scratch; логика соответствует MVP §5.4 + D-05 + D-09):

```go
func Evaluate(s Stats, b Baseline, t Thresholds) (Status, []string) {
	var why []string
	if b.OKRuns == 0 { // D-05: нет ни одного принятого прогона → всегда suspect
		why = append(why, "первый прогон источника — требуется принятие")
	}
	if b.OKRuns < t.MinRuns { // 0..2 прогонов ok: только абсолютные проверки
		if r := share(s.Rejected, s.Fetched); r > t.RejectedAbs { /* 30 % */ }
	} else { // медиана последних ≤7 ok с момента последнего принятия
		m := median(b.Parsed)
		// parsed < 0.5*m ; parsed > 5*m ; rejected/fetched > 20 % ; рост доли пустых полей
	}
	// всегда: дат вне диапазона > 5 %; out_of_scope > 20 % валидных (ME-04-страховка);
	//         нераспознанных областей > порога от InScope (D-09)
	// failed выставляет ВЫЗЫВАЮЩИЙ: ошибка загрузки/структуры, parsed == 0, паника
	...
}
```

Пороги (стартовые, конфигурируемые через env, значения по умолчанию в коде): `MinRuns=3`, `LowRatio=0.5`, `HighRatio=5`, `RejectedMax=0.20` (есть база) / `RejectedAbs=0.30` (нет базы), `DateOOR=0.05`, `OutOfScopeMax=0.20`, `UnrecognizedMax=0.10` (D-09, моё предложение: единичные опечатки ≈1 % проходят, смена формата столбца — ловится), `NullGrow=0.20` (рост доли пустых ключевых полей относительно медианы базы, в п.п.).

Медиана чётного числа — среднее двух средних. Граничные случаи (`parsed == 0.5*m`, `== 5*m`) — в табличный тест.

**Что такое «база» после ручного принятия.** Окно базы = прогоны `ok` с `id ≥ id последнего принятого вручную прогона` (не более 7). Без этого легитимный скачок (150 → 800) после `accept-baseline` снова окажется `suspect` («800 > 5 × медиана 150»), потому что медиана из 7 значений почти не сдвинется. С этим окном после принятия `OKRuns = 1` → абсолютные проверки до накопления 3 прогонов (ровно D-05). Запрос проверен на PostgreSQL 17:

```sql
SELECT parsed FROM ingest_runs
WHERE source_id = $1 AND status = 'ok'
  AND id >= COALESCE((SELECT max(id) FROM ingest_runs WHERE source_id = $1 AND accepted_at IS NOT NULL), 0)
ORDER BY id DESC LIMIT 7
```

**Рост доли пустых полей** сравнивается с прошлыми прогонами, значит, доли нужно хранить: `metrics` (см. миграцию) содержит `{"empty":{"city":3,"region":5,"distances":2},"unrecognized_region":0,"date_out_of_range":0,"out_of_scope":0}`. Альтернатива без колонки — сравнивать с текущим содержимым `source_events.payload` (JSON-ключи в SQL, связывает `store` с формой payload) — не рекомендуется.

### Pattern 4: хранение результата `suspect` и `accept-baseline` (D-06)

**Решение: `ingest_runs.snapshot JSONB`** (полный `[]SourceEvent` прогона, ≈150 записей ≈ 100 КБ). Отвергнутые варианты:
- сырой HTML на диске и повторный Parse при принятии — работает без миграции, но ротация «последний + предыдущий» (ING-10) может вытеснить нужный файл, а результат Parse новой версии кода может отличаться от того, что видел владелец в отчёте;
- `payload` в `source_events` — нельзя: `suspect` ничего не применяет, а `source_events` — применённое состояние.

Жизненный цикл: `FinishRun(suspect, snapshot)` записывает JSONB; следующий завершившийся прогон того же источника обнуляет `snapshot` предыдущих (`UPDATE ingest_runs SET snapshot = NULL WHERE source_id=$1 AND snapshot IS NOT NULL AND id <> $2`) — «до принятия или до следующего прогона» (D-06).

`worker accept-baseline --source X [--run ID]`:
1. Берёт последний прогон источника; требует `status='suspect'` и `snapshot IS NOT NULL` (иначе внятная ошибка); `--run` должен совпасть.
2. Отказывает, если по источнику есть свежий `running` (гонка с демоном).
3. `now` для Validate/Normalize = `started_at` прогона (результат совпадает с тем, что видел владелец в отчёте), политика страны — **текущая** (после решения D-07).
4. Выполняет шаги 6–7 pipeline (apply + Observe) — прогон становится «здоровым» по решению владельца.
5. `status='ok'`, `accepted_at=now()`, `upserted=…`, `snapshot=NULL`, в `error` дописывается «принят вручную; исходная причина: …».

Порядок для первого источника: **сначала решение владельца по RU (D-07) и переключение политики, потом `accept-baseline`** — иначе весь снимок применится как `out_of_scope` (политика `NoFallback`), а события появятся только следующим прогоном (это безопасно благодаря идемпотентному `apply`, но лишний круг).

### Pattern 5: наблюдение `active/missing/gone` (ING-08) — SQL проверен на PostgreSQL 17

Выполняется в **одной транзакции**, только для прогона со статусом `ok` (или принимаемого вручную). `last_run_id` проставляет `UpsertSourceEvent` (новый параметр `runID`), «сегодня» = `current_date` на стороне приложения передаётся параметром `$3`.

```sql
-- seen: снова найденные → active, missed_runs = 0
UPDATE source_events SET state = 'active', missed_runs = 0
WHERE source_id = $1 AND last_run_id = $2 AND (state <> 'active' OR missed_runs <> 0);

-- missing: не найденные, только события с start_date >= сегодня
UPDATE source_events se
SET missed_runs = se.missed_runs + 1,
    state = CASE WHEN se.missed_runs + 1 >= $4 AND now() - se.last_seen_at >= $5::interval
                 THEN 'gone' ELSE 'missing' END
FROM events e
WHERE se.source_id = $1 AND se.last_run_id IS DISTINCT FROM $2
  AND se.event_id = e.id AND e.start_date >= $3;

-- is_listed = НЕ все связанные source_events в gone; updated_at не трогаем (ING-05)
UPDATE events e SET is_listed = x.listed
FROM (SELECT event_id, bool_or(state <> 'gone') AS listed FROM source_events
      WHERE event_id IN (SELECT event_id FROM source_events WHERE source_id = $1 AND event_id IS NOT NULL)
      GROUP BY event_id) x
WHERE e.id = x.event_id AND e.merged_into_id IS NULL AND e.is_listed <> x.listed;
```

Проверено на сценарии: запись 2 (`missing`, `missed_runs=2`, `last_seen_at` 80 ч назад, событие в будущем) → `gone`, `is_listed=false`, `events.status` остался `scheduled`; запись 3 (событие в прошлом) не тронута; запись 1 (найдена) осталась `active`. Значения 3 и 72 ч — конфигурируемые параметры `$4`, `$5` (MVP §4.4).

Строки без `event_id` (`out_of_scope`) наблюдением не затрагиваются — видимости у них нет. `gone`-запись, вернувшаяся в источник, `UpsertSourceEvent` + шаг 1 возвращают в `active`, а третий оператор возвращает `is_listed = true`.

### Pattern 6: идемпотентный `apply`

Для **каждой** валидной записи (не только `changed`):
1. `UpsertSourceEvent(sourceID, runID, externalID, url, payload)` → `{id, changed, eventID *int64, matchState}` (ING-04; хеш и `first_seen_at` не меняются, `last_seen_at`, `last_run_id` — обновляются).
2. Страна по политике (§RU-fallback). Вне РФ → `SetMatch(id, nil, MatchOutOfScope)`; событие не создаётся.
3. В РФ → `store.SaveEventFor(ctx, sourceEventID, *Event)`: **одна транзакция**, внутри `SELECT … FOR UPDATE` строки `source_events`; если `event_id IS NULL` — INSERT события + дистанций + `UPDATE source_events SET event_id, match_state='auto'`; иначе — обновление существующего (логика текущего `SaveEvent`: `updated_at` только при изменении).
   Зачем атомарность: сейчас `SaveEvent` и `SetMatch` — две транзакции; падение между ними оставляет событие-сироту, и следующий прогон создаст второе (дубль, который ING-04 запрещает).
4. **Статус не перезаписывать:** для существующего события `Status` берётся из БД (`GetEvent`), пока источник не сообщает явный статус (у Марафонца в списке его нет); иначе каждый прогон сбрасывал бы `cancelled`/`postponed` в `scheduled` (MVP §4.4). `IsListed`/`MergedIntoID` `SaveEvent` и так не трогает.

Пока у события один `source_event`, «приоритет источника» (ING-05) тривиален: поля берутся из него. Когда в Phase 5 появится второй `source_event` у одного события, обновление «из первого источника» перепишет поля — это работа Phase 5 (`match`), здесь оставить комментарий.

Падение процесса в середине `apply` безопасно: все операции идемпотентны, `Observe` выполняется только после полного прохода, а «висящий» `running` разбирается `StartRun` (см. ниже).

**Транзакция запуска:** `StartRun` сначала помечает `failed` (`error='прерван: процесс остановился'`) прогоны источника в `running` старше `run_timeout + 15 мин`, затем вставляет новую строку; уникальный частичный индекс отклоняет параллельный запуск (проверено: второй `running` для того же источника → `unique_violation`).

### Pattern 7: миграция `00002_ingest_runs_quality.sql`

```sql
-- +goose Up
ALTER TABLE ingest_runs
    ADD COLUMN accepted_at TIMESTAMPTZ,                       -- ручное принятие (граница базы)
    ADD COLUMN metrics     JSONB NOT NULL DEFAULT '{}'::jsonb, -- доли пустых полей, нераспознанные области и т.п.
    ADD COLUMN snapshot    JSONB;                              -- разобранный результат suspect до принятия/следующего прогона
CREATE UNIQUE INDEX ux_ingest_runs_running ON ingest_runs (source_id) WHERE status = 'running';

-- +goose Down
DROP INDEX ux_ingest_runs_running;
ALTER TABLE ingest_runs DROP COLUMN snapshot, DROP COLUMN metrics, DROP COLUMN accepted_at;
```

Причина `failed`/`suspect` пишется в существующую колонку `error` (единственная текстовая; в `slog` — поле `reason`). Колонок `reason`, отдельных таблиц для снимка, регионов, сырых ответов — не добавлять. Ограничить `error` 1000 символами (стек паники — только в лог). Существующее из 00001 — [VERIFIED: migrations/00001_init_schema.sql:8-21] `ingest_runs (… status TEXT NOT NULL CHECK (status IN ('running', 'ok', 'suspect', 'failed')), … error TEXT)`, `source_events … last_run_id BIGINT REFERENCES ingest_runs(id)` [VERIFIED: :64], `state … CHECK (state IN ('active', 'missing', 'gone'))` [VERIFIED: :60], `missed_runs INTEGER NOT NULL DEFAULT 0` [VERIFIED: :61].

Примечание: LO-03 (CHECK-и схемы) — необязательно, можно приложить `CHECK (end_date IS NULL OR end_date >= start_date)` и неотрицательные счётчики, если планировщик хочет; для Марафонца (`end_date` не бывает) не критично.

### Pattern 8: Fetch (ING-01) — stdlib, прототип проверен тестами

Полный прототип — §Code Examples 1. Свойства, которые обязан сохранить исполнитель:

- `http.Client{Timeout: 30s}` (покрывает и чтение тела) + `Transport{DialContext: (&net.Dialer{Timeout: 10s}).DialContext, TLSHandshakeTimeout: 10s}`; `CheckRedirect` — не более 3 переходов и только на тот же хост (иначе rate limit хоста обходится редиректом).
- `io.ReadAll(io.LimitReader(body, max+1))` и **ошибка при `len > max`** — не молчаливое усечение (усечённый HTML дал бы «нормальный» прогон с меньшим числом строк).
- 4xx (кроме 429) — без повтора; 403/429 логируются как «возможна антибот-защита — не обходить»; повторяются сетевые ошибки, 5xx, 429.
- `Attempts = 3` (всего); ожидание перед повтором `max(fullJitter(n), Retry-After)`, `fullJitter(n) = rand[0, min(30s, 2s<<(n−1))]`; `Retry-After` — секунды или HTTP-дата (`http.ParseTime`); если ждать дольше `MaxRetryWait` (2 мин по умолчанию) — сдаться с причиной, а не спать.
- **Затвор хоста:** один мьютекс на хост (1 одновременный запрос), время конца предыдущего запроса; перед КАЖДОЙ попыткой (в том числе повтором) ждать до `last + minInterval`. Для Марафонца `minInterval = 40 s`: ретрай не может нарушить `Crawl-delay`. Худший случай: 3 попытки ≈ 2×40 с + ожидания ≈ 2–3 мин ≪ 30 мин таймаута прогона.
- Ожидание — через `time.NewTimer` + `select` с `ctx.Done()` (не `time.Sleep`), иначе SIGTERM ждёт до 40 с. Функция сна и часы — поля `Fetcher` (в тестах подменяются, реальных ожиданий нет).
- **`Accept-Encoding` не выставлять** — Go сам запросит gzip и распакует; ручной заголовок отключает автораспаковку [ASSUMED: поведение `net/http` Transport, известное свойство stdlib].
- `User-Agent = "<имя>/<версия> (+" + OWNER_CONTACT + ")"`; `OWNER_CONTACT` обязателен для `worker`/`run-once` (пусто → ошибка старта); значение **не логировать и не писать в сырые ответы/метаданные**. Значение с `\r\n` `net/http` отвергнет ошибкой заголовка (не инъекция).
- Условный GET (`ETag`/`If-Modified-Since`) не реализовывать: у `/calendar-beg/` этих заголовков нет (`cache-control: max-age=0`) [CITED: 01-SOURCES-REPORT §2.1 «Остальные наблюдения прогона»].

### Pattern 9: планировщик (ING-09)

- По горутине на источник в `sync.WaitGroup`; цикл: `next = last_attempt + interval + rand[0, jitter)`, где `last_attempt = max(started_at)` из `ingest_runs` источника (первый запуск — сразу, если строк нет). Это исключает шторм запросов при рестартах/краш-лупе: перезапуск процесса не запрашивает сайт повторно, пока не прошёл интервал.
- После серии `failed` интервал растёт (`interval × min(2^n, 4)`, n — подряд идущих сбоев из `SourceHealth`) — это «circuit breaker» MVP §5.3 без библиотеки.
- Контекст прогона: `context.WithTimeout(ctx, RunTimeout=30m)` для Fetch/Parse/Evaluate. **Применение (`apply`+`Observe`) и `FinishRun` выполняются под `context.WithoutCancel(ctx)` + собственный таймаут (например 5 мин)**: SIGTERM или истёкший таймаут не должны оставить каталог наполовину применённым и строку прогона в `running`.
- Graceful stop: в `main` после `<-ctx.Done()` вызвать `stop()` (восстановить поведение сигналов — второй Ctrl-C убивает; LO-09), дождаться `wg.Wait()`. Паника внутри `Parse` — `defer recover()` → `failed` («паника парсера»), стек в лог.
- Интервал 6 ч и jitter (по умолчанию 30 мин) — env `INGEST_INTERVAL`, `INGEST_JITTER`; `INGEST_RUN_TIMEOUT=30m`.

### Pattern 10: сырые ответы (ING-10)

`RAW_DIR` (по умолчанию `data/raw`, каталог `0700`, файлы `0600`, **добавить `/data/` в `.gitignore`**): `<RAW_DIR>/<source>/latest.html`, `previous.html` (+ `latest.meta.json`: url, fetched_at, sha256, run_id, без контакта). Сохранение сразу после успешного Fetch (до Parse — чтобы упавший Parse был разбираем): запись во временный файл в том же каталоге → `os.Rename(latest, previous)` → `os.Rename(tmp, latest)`. TTL: при каждом прогоне удалять файлы старше `RAW_TTL` (14 суток). Количество — 2, как разрешено MVP §5.1.

### Pattern 11: логи (MON-01/MON-02)

В `main` (до диспетчера подкоманд): `slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))`, `LOG_FORMAT=text` для локального чтения. Runner получает `log := slog.With("source", code)` и дальше `log.With("run_id", id)`. Словарь полей (единый во всех подкомандах):

`source`, `run_id`, `status`, `reason`, `fetched`, `parsed`, `rejected`, `out_of_scope`, `upserted`, `created`, `changed`, `unchanged`, `date_out_of_range`, `unrecognized_region`, `baseline_runs`, `baseline_median`, `bytes`, `attempts`, `duration_ms`, `consecutive_failures`.

События: `run started`; `fetch done` (bytes, attempts, status); `run finished` (все счётчики) — уровень `INFO` для `ok`, `WARN` для `suspect` (с `reason`), `ERROR` для `failed`; `record rejected` — агрегированно (причина → число + до 3 примеров `external_id`), а не 150 строк; `unrecognized region` — по одной строке на уникальную пару «область — город» (D-09). Никогда не логировать: DSN, `OWNER_CONTACT`, User-Agent, тела ответов. MON-01: `store.SourceHealth(sourceID)` = последний статус + `reason` + число подряд идущих `failed`/`suspect` (запрос по `idx_ingest_runs_source_started`), пишется в `run finished` и служит входом backoff планировщика — новая таблица не нужна.

### Pattern 12: CLI

`main.go` сейчас: `case "bot", "worker": err = runProcess(ctx, subcommand)` и `usage = "usage: aggregator <bot|worker|migrate|healthcheck>"` [VERIFIED: cmd/aggregator/main.go:19,37-38]. `worker` получает под-подкоманды (stdlib `flag.FlagSet`, без фреймворка):

| Команда | Действие |
|---------|----------|
| `worker` | демон: планировщик всех зарегистрированных источников |
| `worker run-once --source X` | один прогон сейчас (первый прогон D-05, отладка) |
| `worker accept-baseline --source X [--run ID]` | D-06 |
| `worker sanity-report --source X` | D-07: отчёт из `snapshot` последнего `suspect` (до принятия) или из `payload` активных `source_events` (после) |

### Pattern 13: dev PostgreSQL (D-03)

Новый `docker-compose.dev.yml` (`name: aggregator-dev`, чтобы не пересекаться с `aggregator-test`): `postgres:17`, порт **`127.0.0.1:5434:5432`** (тестовая БД — 5433), **именованный том** (данные должны переживать `down`), пароль из окружения без значения в файле (`POSTGRES_PASSWORD: ${DEV_POSTGRES_PASSWORD:?задайте в .env}`), имя БД `aggregator_dev`. `DATABASE_URL` — в `.env` вне репозитория (шаблон `.env.example` дополнить пустыми `OWNER_CONTACT=`, `RAW_DIR=`, пороги — необязательно). `psql` на Mac нет [VERIFIED: `which psql` → not found]: SQL-запросы — `docker compose -f docker-compose.dev.yml exec postgres psql -U … -d aggregator_dev`.

**Обязательно вместе с этим — защита тестов (LO-11):** `TestMain` в `internal/store/store_test.go` делает `TRUNCATE … CASCADE` в любой БД из `TEST_DATABASE_URL`. При двух БД на одной машине одна опечатка в env уничтожит реальные данные Марафонца. Добавить в `TestMain` проверку `SELECT current_database()`: не оканчивается на `_test` → `os.Exit(1)`; тестовая БД уже называется `aggregator_test` [VERIFIED: docker-compose.test.yml: `POSTGRES_DB: aggregator_test`; .env.example: `.../aggregator_test`].

### RU-fallback и санити-отчёт (ING-11, D-07..D-09)

Состояние политики Марафонца — одно поле `Policy.CountryFallback` в регистрации источника:
- **До записанного решения** — `normalize.NoFallback`: записи без явной страны → `out_of_scope` (в `normalize.Country("", NoFallback)` это `("", false)` [VERIFIED: internal/normalize/place.go:163-172]).
- **После решения «подтверждаю»** — `RUOnlyFallback`; переключение — правка одной строки (задача плана после чекпойнта). «Отклоняю» — остаётся `NoFallback`, Марафонец не даёт событий (кроме случаев с явной страной), решение фиксируется в отчёте.
- Кто «страна»: у Марафонца страны в данных нет. Классификация по столбцу «область» (`normalize.ClassifyRegion`, §Справочник): `RegionRF` → страна не задана явно, действует политика; `RegionForeign` (Беларусь и т.п.) → `explicit` = название страны → `out_of_scope`; `RegionUnknown` (не в перечне и не иностранная) → по политике `RU`, но `UnrecognizedRegion++` и запись в лог (D-09).
- Пустая область — не `Unknown`, а отдельная категория «область пуста» (считается как пустое ключевое поле, не как нераспознанная).

**Формат санити-отчёта (D-07)** — текст/Markdown, кладётся в `.planning/phases/03-first-source/03-SANITY-REPORT.md` (публичные данные, секретов нет), владелец добавляет блок решения:

```
# Санити-отчёт RU-fallback — marathonec, прогон <id>, <дата UTC>
Строк: 148 | уникальных областей: 49 | в перечне РФ: 49 | НЕТ в перечне: 0 | иностранных: 0 | область пуста: 0

## Области (сначала подозрительные)
| Область (как в источнике) | Статус | Строк | Города (первые 5) |
|---|---|---|---|
| Минская область | НЕТ в перечне | 2 | Минск, ... |
| Московская область | в перечне РФ | 31 | Москва, Химки, ... |

## Прочее для проверки глазами
- Дистанции > 100 км (возможные склейки): <title> → 100200400 м ...
- Нераспознанные типы: ...

## Решение
Значение fallback RU для Марафонца: <подтверждён / отклонён>   Дата: <YYYY-MM-DD>   Решение принял: владелец
```

Пары «область — город» и число строк даёт один проход по `[]SourceEvent`; сортировка: `НЕТ` и `иностранные` первыми.

### Справочник субъектов РФ (D-08) — Go-таблица, проверена

Файл `internal/normalize/regions.go`. Сравнение идёт по `regionKey`: нижний регистр, `ё→е`, любая пунктуация → пробел, выбрасываются служебные слова («республика», «респ», «область», «обл», «край», «автономный/ая», «округ», «ао», «аобл», «город», «г»). Поэтому «Республика Татарстан», «Татарстан», «Татарстан (Республика)», «Респ. Татарстан» — один ключ; «Москва» ≠ «Московская»; «Республика Алтай» (`алтай`) ≠ «Алтайский край» (`алтайский`). Проверено scratch-тестом: **ровно 89 записей, коллизий ключей нет, ни одно иностранное имя не совпало с субъектом РФ**; 28 контрольных написаний классифицированы как ожидается (`Ямало-Ненецкий АО`, `Ярославская обл`, `Северная Осетия — Алания`, `Южная Осетия` → иностранная, `Беларусь` → иностранная, `Минская область` → неизвестная).

Состав: 24 республики (включая Крым, ДНР, ЛНР), 9 краёв, 48 областей (включая Запорожскую и Херсонскую), 3 города федерального значения, 1 автономная область, 4 автономных округа = 89. Точные написания в столбце «область» на сайте неизвестны до фикстуры: **если столбец содержит слаги таксономии WordPress (`moskovskaya-oblast`), а не кириллицу**, ключ слага сравнивается с `normalize.CityKey(canonical)` (для «Московская область» это `moskovskaya-oblast` [VERIFIED: запуск в scratch]); WP-плагины транслитерации могут отличаться для `х, ц, щ, ю` — решать по фикстуре.

```go
// internal/normalize/regions.go (фрагмент; полный список 89 — в Code Examples 4)
func regionKey(s string) string {
	var out []string
	for _, w := range strings.FieldsFunc(yoToE(strings.ToLower(s)), notLetterOrDigit) {
		if !regionGeneric[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

func ClassifyRegion(s string) (canonical string, kind RegionKind) // RegionRF | RegionForeign | RegionUnknown
```

Иностранные названия — небольшой список (CIS, Турция, Балканы, ОАЭ и др. — в Code Examples 4); для событий вне РФ код страны в БД не пишется (события нет), поэтому отдельные ISO-коды не нужны. **Иностранные области** (Минская, Брестская, Алматинская…) в список не включены: в снимке 2026-09-25 все 49 значений российские [CITED: 01-SOURCES-REPORT §2.1 п.16]; если фикстура покажет иное, отчёт вынесет их в «НЕТ в перечне», и их добавляют в `foreignNames`.

### Отложенные находки Phase 2: что брать и как

Проверено запуском текущего `normalize` в scratch-копии (проект не менялся):

| Находка | Факт сейчас | Значимость для Марафонца | Решение для фазы |
|---|---|---|---|
| Склейка «100 200 400 м» | `Distances("100 200 400 м") = [100200400]`, `Distances("200 400 м") = [200400]`; `Distances("100 м, 200 м, 400 м") = [100 200 400]` | Значение 100 200 400 м укладывается в `INT32` и **записалось бы в БД как дистанция 100 200 км**. Зависит от того, как столбец Марафонца разделяет значения | В `Prepare`: дистанция > `MaxPlausibleDistanceM` (предложение: 1 000 000 м = 1000 км) отбрасывается с `WARN` и попаданием в отчёт «дистанции > 100 км»; событие сохраняется без неё. Токены с ≥ 3 пробел-разделёнными трёхзначными группами («100 200 400») в `Distances` считать списком отдельных чисел с общей единицей. Финальное правило — по значениям столбца в фикстуре |
| Римские цифры («MIX RUN») | `TitleNorm("MIX RUN") = "run"`; `TitleNorm("Run I Love") = "run love"`; `TitleNorm("X Московский марафон") = "московский"` | Названия забегов на латинице с заглавными — обычны | Ограничить `romanRe` значениями ≤ 299 (без ведущей `M`, `D`): `^(C{0,2})(XC\|XL\|L?X{0,3})(IX\|IV\|V?I{0,3})$`, минимум 2 символа; однобуквенные `I/V/X` — только если в названии есть кириллица. Проверять по таблице всех заголовков фикстуры (golden `title_norm`) |
| `5K` / `10K` | `Distances("5K") = []`, `Distances("Забег 10K") = []` | Зависит от фикстуры (grep `\d[KkКк]\b`) | Добавить `k/к` только слитно с числом (`(\d+([.,]\d+)?)[kк]`), в `Distances` и `removeDistances`; если в фикстуре нет — всё равно дёшево (ME-05) |
| ME-04 «Russian Federation» | `Country("Russian Federation", RUOnlyFallback)` → `("", false)`; `Country("Беларусь", …)` → `("", false)` | Неузнанная явная страна молча выбрасывает запись | Различить «известная иностранная» и «неизвестная»: неизвестная явная строка падает в политику источника (и считается); словарь дополнить `russian federation`, `rossiya`; иностранные — по `foreignNames`. Добавлен предохранитель `out_of_scope > 20 %` → `suspect` |
| LO-07 префиксы `г.о.`, `п.`, `с.` | `City("г.о. Химки") = "о. Химки"`, `City("п. Токсово") = "п. Токсово"` | Вероятны в столбце «город» | Порядок префиксов: `"г.о. "`, `"городской округ "`, `"пос. "`, `"п. "`, `"д. "`, `"с. "`, затем `"город "`, `"г."`, `"г "`; решить по значениям фикстуры |
| LO-04 `CityKey` с `--` | `moskva--siti` | Мелко; DB CHECK допускает | Переводить по словам, пустые отбрасывать (перед Phase 4) |
| LO-11 защита тестов | нет | Критично из-за dev-БД (D-03) | **Взять** (см. §Pattern 13) |
| LO-09 сигналы | `stop()` только через `defer` | Планировщик — реальный долгоживущий цикл | **Взять** |
| IN-04 NUL / невалидный UTF-8 | одна запись с `\u0000` в payload → jsonb отклоняет → падает весь прогон | Данные из сети | **Взять:** в `Parse` пропускать все строки через `strings.ToValidUTF8(s, "")` и `strings.ReplaceAll(s, "\x00", "")` |
| LO-01 URL не обновляется при том же payload | подтверждено ревью | URL входит в payload (см. Pattern 1) → hash меняется | Решено включением `URL` в payload; правка `store` не обязательна (но однострочная, если `UpsertSourceEvent` всё равно меняется) |
| Остальные low/info (LO-02 канонический hash, LO-03 CHECK, LO-05, LO-06, LO-08, LO-10, IN-01..03, IN-05) | — | Не затрагиваются кодом фазы либо решены структурой | Не брать; LO-06 закрыть правилом «`Distances` — только по заголовку и столбцу дистанций» |

### Golden-тесты (ING-02)

```
testdata/calendar-beg.html          # реальная страница (D-01), без правок
testdata/calendar-beg.golden.json   # вывод Parse: []SourceEvent, MarshalIndent
testdata/prepared.golden.json       # Normalize поверх golden при фиксированном now (title_norm, city_key, distances, country)
testdata/edge-*.html                # синтетика: «MIX RUN», «100 200 400 м», 5K, «Беларусь», строка без даты, дубль id, пустая таблица, страница без таблицы
```
Флаг обновления `-update` (`flag.Bool` в тесте): без него сравнивается побайтно, диффы нормализации видны в ревью. Parse не использует `time.Now()`. Тест «структура не найдена»: пустая таблица и страница без `post-row` → `ErrStructureNotFound`.

**Перед коммитом фикстуры** пролистать на секреты: WordPress-страницы часто содержат nonce, e-mail, ключи виджетов и URL админ-AJAX в inline-скриптах; полная страница ≈ 600 КБ. Рекомендация: коммитить страницу без правок в приватный репозиторий (факты, источник указан; MVP §7.3), но убрать/заменить найденные токены и e-mail и зафиксировать это в комментарии рядом с фикстурой; финальное решение по объёму/авторскому праву — владелец (Open Question 3).

### Разметка Марафонца: что известно и что определит фикстура

Известно [CITED: 01-SOURCES-REPORT §2.1 п.7,10,12]: таблица (DataTables на клиенте, все строки в HTML), `<tr id="post-row-<id>" class="post-row …">`; столбцы: дата `dd/mm/yyyy` (+ Unix в `data-sort`), название со ссылкой, дистанции («км в тексте»), город; скрытые: слаги дистанций, область, город, месяц, тип (`trejl`/`shosse`); внешние ссылки регистрации (`reg.russiarunning.com`, `myrace.info`, `toplist.run`, `reg.place`, свои страницы). Неизвестно, пока не открыта фикстура: точный порядок столбцов, форма скрытых ячеек (`display:none`, `data-*`, класс), формат «область» (кириллица или слаг), присутствуют ли **прошедшие** события.

Рекомендованный способ доступа: **по заголовку таблицы** (`<thead><th>` → индекс столбца) либо по классам ячеек, а не по жёстким индексам; отсутствие ожидаемого столбца = `ErrStructureNotFound`. Обход — `for n := range doc.Descendants()` и сопоставление `DataAtom == atom.Tr` + префикс `id`, а не путь `table>tbody>tr` (парсер сам вставляет `<tbody>`, а вёрстка может измениться). `registration_url`: из ссылок строки с хостом ≠ `marathonec.ru`; приоритет — известные регистраторы (`reg.russiarunning.com`, `myrace.info`, `reg.place`), иначе первая внешняя; только схемы `http`/`https`, длина ≤ 2048 (в Phase 4 ссылка пойдёт в Telegram).

### Anti-Patterns to Avoid

- **Применять по ходу разбора.** Любая запись в БД до `Evaluate` ломает «suspect ничего не применяет».
- **Обновлять только `changed`-записи** — политика страны/правила `normalize` изменятся, а события не пересоздадутся.
- **Два независимых вызова `SaveEvent` + `SetMatch`** — сироты при падении между ними.
- **Хранить «сегодня» как `time.Now()` внутри стадий** — стадии получают `now` параметром (детерминизм, golden).
- **`captcha` как слово-индикатор в рабочем коде** — решение D-02: только структура.
- **Ждать `Crawl-delay` без `ctx`** — SIGTERM зависнет на 40 с.
- **Отвечать на 403/429 «умнее»** (смена UA, паузы-обходы): антибот не обходится (MVP §7.3, D-01).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HTML5-разбор таблицы | Регулярные выражения по HTML, самодельный токенайзер | `golang.org/x/net/html` `Parse` + `Descendants()` | Вложенность, необработанные теги, `<tbody>`, сущности (`&nbsp;`), лимит вложенности 512 |
| Распаковка gzip | ручной `gzip.NewReader` | `net/http` Transport (не ставить `Accept-Encoding`) | Автоматически, нет расхождения с `LimitReader` |
| Разбор `Retry-After` HTTP-даты | свой парсер дат | `http.ParseTime` | Три формата RFC 7231 |
| Случайные числа для jitter | самодельный ГПСЧ | `math/rand/v2` (`rand.Int64N`) | Потокобезопасен, без глобального seed |
| Расписание | cron-библиотека | таймер + `ingest_runs.started_at` | MVP §3.1; постоянство между рестартами бесплатно |
| Эксклюзивность прогона | файловый lock / advisory lock на сессии | частичный уникальный индекс `ux_ingest_runs_running` | Работает между процессами, проверено, без выделенного соединения |
| Атомарная запись файла | запись «поверх» | temp-файл + `os.Rename` | Читатель (owner, отчёт) не видит полфайла |
| Медиана | внешняя статистика | `slices.Sorted` + 5 строк | Тривиально, покрыто табличным тестом |
| Справочник субъектов РФ | таблица в БД, внешний API | Go-таблица в `normalize` | Данные не меняются; D-08 |
| Определение captcha по словам | список слов-индикаторов | структурная проверка (0 `post-row` → `failed`) | D-02 |

**Key insight:** сложность фазы — не в HTTP или HTML, а в согласованности состояния (что применено, что нет, что видно пользователю). Поэтому решения принимаются чистыми функциями до записи, а запись — идемпотентна и по возможности атомарна на уровне записи.

## Runtime State Inventory

Не rename/refactor-фаза — раздел не применим. Но есть один пункт миграции: фаза **меняет сигнатуры** существующих `store`-функций (`UpsertSourceEvent` получает `runID`; возвращает больше), значит обновляются тесты `internal/store/store_test.go` (Phase 2) — учесть в плане; данных в БД ещё нет (dev-БД создаётся в этой фазе), миграции данных не требуется.

## Common Pitfalls

### Pitfall 1: «captcha» — вместо неё смотрим структуру
**What goes wrong:** исполнитель добавляет проверку на слова или, наоборот, игнорирует D-01.
**How to avoid:** первая задача — 1 GET (curl с UA: `-A "…(+$OWNER_CONTACT)"`) в файл; затем `grep -o -i -n '.\{80\}captcha.\{80\}' файл` и `grep -c 'post-row-'`. Нет ни одной строки `post-row` → **СТОП** и возврат к владельцу (замена на ПроБЕГ); антибот не обходится. Если строки есть — вхождения `captcha` описываются в отчёте (ожидается шаблонный виджет WordPress) и фаза продолжается. Также посмотреть в теле `ld+json` (2 вхождения по отчёту): типы схем (`"@type"`), не `Event` ли — закрывает открытый вопрос п.5 карточки.
**Warning signs:** тело намного меньше ~600 КБ, нет таблицы.

### Pitfall 2: прошлые события на странице
**What goes wrong:** если календарь содержит прошедшие даты 2026 года (заголовок страницы «Календарь забегов России 2026-2027»), Validate отклонит их как «вне `[сегодня−1 день, …]`», и `rejected/fetched > 20 %`, `date_out_of_range > 5 %` сделают **каждый** прогон `suspect`.
**How to avoid:** посмотреть минимальную дату в фикстуре до настройки порогов. Если прошедших строк много: учитывать их отдельно (`expired`, не `rejected` и не «вне диапазона») либо считать их отклонёнными, но исключить из знаменателя проверок §5.4 — это отступление от буквы ING-03 надо зафиксировать явным решением (Open Question 1). Не подгонять пороги молча.

### Pitfall 3: `LimitReader` тихо усекает
Читать `max+1` байт и считать превышением; не парсить усечённое.

### Pitfall 4: retry нарушает Crawl-delay
Ретрай после 2 с backoff — это второй запрос через 2 с при `Crawl-delay: 40`. Затвор хоста ждёт `minInterval` перед каждой попыткой (прототип проверен: ожидания `[7s 39.99s 7s 39.99s]`).

### Pitfall 5: рестарты и шторм запросов
`worker` с `restart: always` при ошибке БД перезапускается каждые секунды и на старте «первый запуск сразу» долбит сайт. Брать «последняя попытка» из `ingest_runs`.

### Pitfall 6: `running` застрял после падения
Уникальный индекс не даст стартовать новый прогон. `StartRun` сначала закрывает `running` старше `run_timeout + 15 мин`.

### Pitfall 7: контекст отмены при финализации
`FinishRun` под отменённым контекстом не выполнится — прогон останется `running`. Использовать `context.WithoutCancel` + таймаут.

### Pitfall 8: NUL/невалидный UTF-8 в payload
jsonb не принимает `\u0000`; одна такая строка валит `UpsertSourceEvent` и весь прогон. Санитизировать в Parse.

### Pitfall 9: смена политики страны и идемпотентность
Если `apply` трогает только `changed`, после включения RU-fallback ранее `out_of_scope` строки никогда не получат события. Обрабатывать все валидные записи.

### Pitfall 10: сброс `events.status`
Собирая `event.Event` заново каждый раз со `StatusScheduled`, перезапишете `cancelled`/`postponed`. Брать статус существующего события.

### Pitfall 11: скачок после `accept-baseline`
Без границы окна базы по `accepted_at` следующий прогон на новом уровне снова `suspect`. См. Pattern 3.

### Pitfall 12: фикстура с секретами
Nonce, e-mail, ключи в inline-JS. Просмотреть до коммита.

### Pitfall 13: сырые файлы и `data/` в git
`/data/` в `.gitignore` до первого прогона.

### Pitfall 14: тесты на dev-БД
Без `_test`-guard (LO-11) опечатка в `TEST_DATABASE_URL` очистит реальные данные.

### Pitfall 15: 40 секунд между ручным скачиванием и первым прогоном
Затвор хоста живёт в памяти процесса. Между `curl` фикстуры (D-04) и первым `run-once` — не менее 40 с (иначе нарушение `Crawl-delay` через границу процессов). Достаточно отметить в плане; кода не требует.

## Code Examples

### 1. Fetcher (проверен `go vet` и тестами: 503+`Retry-After` → повтор; 403 → один запрос; переполнение → ошибка)

```go
// Source: прототип исследования, скомпилирован и протестирован в scratch (httptest)
type hostGate struct {
	mu   sync.Mutex // 1 одновременный запрос на хост
	last time.Time  // конец предыдущего запроса к хосту
}

func (f *Fetcher) backoff(retry int) time.Duration { // full jitter, retry = 1, 2, ...
	ceil := f.cfg.BackoffBase << (retry - 1)
	if ceil <= 0 || ceil > f.cfg.BackoffMax {
		ceil = f.cfg.BackoffMax
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1))
}

func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(0, t.Sub(now)), true
	}
	return 0, false
}

// Get: до Attempts попыток; каждая ждёт minInterval после конца предыдущего запроса к хосту.
func (f *Fetcher) Get(ctx context.Context, rawURL string, minInterval time.Duration) (RawDoc, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return RawDoc{}, fmt.Errorf("fetch: bad url")
	}
	var last error
	for attempt := 1; attempt <= f.cfg.Attempts; attempt++ {
		doc, err := f.once(ctx, u, minInterval)
		if err == nil {
			return doc, nil
		}
		last = err
		var re *retryErr // повторяем только сеть, 5xx, 429
		if !errors.As(err, &re) || attempt == f.cfg.Attempts || ctx.Err() != nil {
			break
		}
		wait := max(f.backoff(attempt), re.wait)
		if wait > f.cfg.MaxRetryWait {
			return RawDoc{}, fmt.Errorf("fetch: Retry-After %s больше лимита: %w", wait, err)
		}
		if err := f.sleep(ctx, wait); err != nil { // sleep = таймер + ctx.Done()
			return RawDoc{}, err
		}
	}
	return RawDoc{}, fmt.Errorf("fetch: %w", last)
}

func (f *Fetcher) once(ctx context.Context, u *url.URL, minInterval time.Duration) (RawDoc, error) {
	g := f.gate(u.Host)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.last.IsZero() {
		if err := f.sleep(ctx, g.last.Add(minInterval).Sub(f.now())); err != nil {
			return RawDoc{}, err
		}
	}
	defer func() { g.last = f.now() }()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", f.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml") // Accept-Encoding НЕ ставим
	resp, err := f.client.Do(req)
	if err != nil {
		return RawDoc{}, &retryErr{err: err}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		w, _ := retryAfter(resp.Header, f.now())
		return RawDoc{}, &retryErr{err: fmt.Errorf("HTTP %d", resp.StatusCode), wait: w}
	case resp.StatusCode != http.StatusOK:
		return RawDoc{}, fmt.Errorf("HTTP %d (без повтора)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
	if err != nil {
		return RawDoc{}, &retryErr{err: err}
	}
	if int64(len(body)) > f.cfg.MaxBytes {
		return RawDoc{}, fmt.Errorf("ответ больше %d байт", f.cfg.MaxBytes) // не усекаем молча
	}
	if !utf8.Valid(body) {
		return RawDoc{}, errors.New("тело не UTF-8")
	}
	return RawDoc{URL: u.String(), Body: body, FetchedAt: f.now()}, nil
}
```

### 2. Обход строк таблицы (`x/net/html`, проверен на v0.59.0)

```go
// Source: pkg.go.dev/golang.org/x/net@v0.59.0/html (Parse, Node.Descendants, Node.ChildNodes)
// Проверено в scratch: парсер вставляет <tbody> под <table>, tr найден через Descendants.
func rows(r io.Reader) ([]*html.Node, error) {
	doc, err := html.Parse(r) // вход обязан быть UTF-8; вложенность > 512 → ошибка
	if err != nil {
		return nil, err
	}
	var out []*html.Node
	for n := range doc.Descendants() {
		if n.Type == html.ElementNode && n.DataAtom == atom.Tr && strings.HasPrefix(attr(n, "id"), "post-row-") {
			out = append(out, n)
		}
	}
	return out, nil // 0 строк → вызывающий возвращает ErrStructureNotFound (D-02)
}

func text(n *html.Node) string { // текст без script/style, <br> = пробел, пробелы схлопнуты (NBSP входит в strings.Fields)
	var b strings.Builder
	for c := range n.Descendants() {
		switch {
		case c.Type == html.TextNode && c.Parent.DataAtom != atom.Script && c.Parent.DataAtom != atom.Style:
			b.WriteString(c.Data)
		case c.Type == html.ElementNode && c.DataAtom == atom.Br:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
```

Значение `external_id`: `strings.TrimPrefix(attr(tr, "id"), "post-row-")`, проверить `^[0-9]+$` (иначе строка `rejected`).

### 3. Golden-тест Parse

```go
var update = flag.Bool("update", false, "переписать golden-файлы")

func TestParseGolden(t *testing.T) {
	body, err := os.ReadFile("testdata/calendar-beg.html")
	if err != nil { t.Fatal(err) }
	got, err := New().Parse(ingest.RawDoc{URL: "https://marathonec.ru/calendar-beg/", Body: body})
	if err != nil { t.Fatal(err) }
	buf, _ := json.MarshalIndent(got, "", "  ")
	const golden = "testdata/calendar-beg.golden.json"
	if *update {
		if err := os.WriteFile(golden, buf, 0o644); err != nil { t.Fatal(err) }
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(want, buf) { t.Errorf("расхождение с %s; запустите go test -update и просмотрите diff", golden) }
}
```

### 4. Справочник субъектов РФ (проверен: 89 записей, без коллизий ключей)

```go
// internal/normalize/regions.go — первое имя записи каноническое, остальные — варианты написания.
var rfSubjects = [][]string{
	// республики (24)
	{"Республика Адыгея", "Адыгея"}, {"Республика Алтай", "Горный Алтай"}, {"Республика Башкортостан", "Башкирия"},
	{"Республика Бурятия"}, {"Республика Дагестан"}, {"Республика Ингушетия"},
	{"Кабардино-Балкарская Республика", "Кабардино-Балкария", "КБР"}, {"Республика Калмыкия"},
	{"Карачаево-Черкесская Республика", "Карачаево-Черкесия", "КЧР"}, {"Республика Карелия"}, {"Республика Коми"},
	{"Республика Крым"}, {"Республика Марий Эл"}, {"Республика Мордовия"},
	{"Республика Саха (Якутия)", "Якутия", "Саха"},
	{"Республика Северная Осетия — Алания", "Северная Осетия", "Северная Осетия-Алания"},
	{"Республика Татарстан"}, {"Республика Тыва", "Тува"}, {"Удмуртская Республика", "Удмуртия"},
	{"Республика Хакасия"}, {"Чеченская Республика", "Чечня"},
	{"Чувашская Республика — Чувашия", "Чувашия", "Чувашская"},
	{"Донецкая Народная Республика", "ДНР"}, {"Луганская Народная Республика", "ЛНР"},
	// края (9)
	{"Алтайский край"}, {"Забайкальский край"}, {"Камчатский край"}, {"Краснодарский край"},
	{"Красноярский край"}, {"Пермский край"}, {"Приморский край"}, {"Ставропольский край"}, {"Хабаровский край"},
	// области (48)
	{"Амурская область"}, {"Архангельская область"}, {"Астраханская область"}, {"Белгородская область"},
	{"Брянская область"}, {"Владимирская область"}, {"Волгоградская область"}, {"Вологодская область"},
	{"Воронежская область"}, {"Ивановская область"}, {"Иркутская область"}, {"Калининградская область"},
	{"Калужская область"}, {"Кемеровская область — Кузбасс", "Кузбасс", "Кемеровская область"},
	{"Кировская область"}, {"Костромская область"}, {"Курганская область"}, {"Курская область"},
	{"Ленинградская область"}, {"Липецкая область"}, {"Магаданская область"},
	{"Московская область", "Подмосковье"}, {"Мурманская область"}, {"Нижегородская область"},
	{"Новгородская область"}, {"Новосибирская область"}, {"Омская область"}, {"Оренбургская область"},
	{"Орловская область"}, {"Пензенская область"}, {"Псковская область"}, {"Ростовская область"},
	{"Рязанская область"}, {"Самарская область"}, {"Саратовская область"}, {"Сахалинская область"},
	{"Свердловская область"}, {"Смоленская область"}, {"Тамбовская область"}, {"Тверская область"},
	{"Томская область"}, {"Тульская область"}, {"Тюменская область"}, {"Ульяновская область"},
	{"Челябинская область"}, {"Ярославская область"}, {"Запорожская область"}, {"Херсонская область"},
	// города федерального значения (3)
	{"Москва", "г. Москва"}, {"Санкт-Петербург", "СПб", "Петербург", "Питер"}, {"Севастополь"},
	// автономная область и округа (5)
	{"Еврейская автономная область", "ЕАО"}, {"Ненецкий автономный округ", "НАО"},
	{"Ханты-Мансийский автономный округ — Югра", "ХМАО", "ХМАО-Югра", "Югра", "Ханты-Мансийский АО"},
	{"Чукотский автономный округ", "Чукотка"}, {"Ямало-Ненецкий автономный округ", "ЯНАО"},
}

var foreignNames = []string{
	"Беларусь", "Республика Беларусь", "Белоруссия", "Казахстан", "Украина", "Армения", "Азербайджан",
	"Грузия", "Абхазия", "Южная Осетия", "Узбекистан", "Киргизия", "Кыргызстан", "Таджикистан",
	"Туркменистан", "Молдова", "Молдавия", "Латвия", "Литва", "Эстония", "Турция", "Сербия",
	"Черногория", "Болгария", "Кипр", "ОАЭ", "Таиланд", "Вьетнам", "Китай", "Монголия", "Финляндия",
	"Германия", "Франция", "Италия", "Испания", "Греция", "Египет", "Куба",
}
// Индексы rfIndex/foreignIndex строятся в init() по regionKey(...). Тест: len(rfSubjects) == 89 и
// отсутствие коллизий regionKey между разными каноническими именами.
```

Примечание: в проверенной версии был вариант «ХМАО-Югра» без отдельного алиаса, и он оказался `неизвестным` — алиас добавлен выше; поэтому после первого прогона санити-отчёт обязательно просматривается на «НЕТ в перечне» и алиасы добавляются по факту.

### 5. Тест идемпотентности (интеграционный, `TEST_DATABASE_URL`)

```go
// Дважды применяем один и тот же []SourceEvent при фиксированном now.
// Ожидания: число строк source_events/events/event_distances не изменилось;
// first_seen_at, content_hash, payload, events.updated_at — прежние; last_seen_at — новее;
// второй прогон: created=0, changed=0. Затем меняем одну запись (дату) → changed=1, у события
// изменилась start_date и updated_at, число событий прежнее (перенос даты — то же событие).
```

### 6. Тест наблюдения

```go
// Прогон N (ok) без записи X → missing, missed_runs=1; is_listed не изменился.
// Прогон suspect/failed без X → счётчики не меняются.
// Ещё 2 ok-прогона + сдвиг last_seen_at на -73 ч → gone, is_listed=false, events.status прежний.
// Событие с start_date < сегодня → не переходит в missing вообще.
// Запись вернулась → active, missed_runs=0, is_listed=true.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Ручная рекурсия по `Node.FirstChild/NextSibling` | итераторы `Descendants()`, `ChildNodes()`, `Ancestors()` | `x/net` v0.31.0 [CITED: pkg.go.dev] | короче код обхода |
| `math/rand` + `rand.Seed` | `math/rand/v2` (`Int64N`, автоинициализация) | Go 1.22 | без глобального состояния |
| `context.Background()` для завершающих операций | `context.WithoutCancel(ctx)` | Go 1.21 | финализация под отменённым родителем |
| `time.After` в цикле | `time.NewTimer` + `Stop` | давно | нет утечек таймеров |

**Deprecated/outdated:** `ioutil.ReadAll` (использовать `io.ReadAll`); `golang.org/x/net/html/charset` — не нужен для UTF-8 WordPress.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Столбец «область» содержит кириллические названия, а не слаги таксономии | Справочник | Санити-отчёт покажет «НЕТ в перечне» для всех — тогда сравнение по `CityKey(canonical)`; определяется фикстурой |
| A2 | Порядок/форма столбцов (заголовки `<th>`, скрытые ячейки) допускает доступ по имени/классу | Разметка | Парсер придётся привязывать к индексам; определяется фикстурой |
| A3 | В таблице нет прошедших событий (или их <5 %) | Pitfall 2 | Каждый прогон `suspect` до решения по порогам; решение — Open Question 1 |
| A4 | Стартовые пороги, в частности `UnrecognizedMax=0.10`, `OutOfScopeMax=0.20`, `NullGrow=0.20`, `MaxPlausibleDistanceM=1_000_000` | Оценка качества | Ложные `suspect`/пропуски; калибруются на реальных прогонах, конфигурируемы |
| A5 | Приоритет ссылок регистрации: `reg.russiarunning.com` > `myrace.info` > `reg.place` > первая внешняя | Разметка | Ссылка ведёт на агрегатор вместо регистратора; уточняется по фикстуре |
| A6 | `Accept-Encoding` вручную не ставить — Go распаковывает gzip сам | Fetch | Тело придёт сжатым; проверяется тестом с gzip-сервером |
| A7 | С Mac владельца `marathonec.ru` доступен без антибота (с VPS и с машины исследователя был) | Environment | Прогон `failed` «HTTP 403»; тогда решение владельца, обход запрещён |
| A8 | Типы сайта — только `trejl`/`shosse` (по отчёту), остальные метки → `other` | Normalize | Занижение типов; виден в логе нераспознанных меток |
| A9 | Стабильность WP-`id` за пределами одного снимка | Pattern 6 | Смена id = «новое событие» + `gone` старого (Phase 5 дедуп) |
| A10 | Сайт не блокирует при паузе 40 с и одном запросе за 6 ч | Fetch/Scheduler | Блокировка → `failed`; реакция владельца, обход запрещён |
| A11 | `upserted` = все валидные записи, прошедшие upsert (а не только новые/изменённые) | Счётчики | Расхождение с ожиданием владельца при чтении `ingest_runs`; подтверждается планировщиком |

## Open Questions

1. **Прошедшие события в таблице Марафонца (A3)**
   - Known: заголовок страницы «Календарь забегов России 2026-2027»; в отчёте Phase 1 диапазон дат строк не приведён.
   - Unclear: сколько строк с датой < сегодня−1.
   - Recommendation: после D-01 посчитать минимальную дату. Если прошлых > 5 %: считать их `expired` (не `rejected`, не «вне диапазона»), решение владельца/планировщика записать в плане — иначе первая же настройка порогов превратится в подгонку.

2. **Форма столбцов и значений «область»/«город»** — определит фикстура; Task 2 плана (Parse) зависит от Task 1 (D-01) и должен начинаться с просмотра фикстуры, а не с угадывания.

3. **Коммит полной страницы (~600 КБ) как фикстуры**
   - Known: MVP §5.2/D-01 требуют golden на реальном файле; источник — © журнал «Марафонец», ToS нет; собираются факты.
   - Recommendation: коммитить без правок в приватный репозиторий, предварительно убрав найденные токены/e-mail; владелец подтверждает объём и авторско-правовой аспект (MVP §7.3 допускает факты + ссылку на источник).

4. **JSON-LD на странице (`ld+json` ×2)** — проверить `@type` в фикстуре; если `Event` со всем списком — парсер проще, но решение D-10 (HTML) остаётся: сравнить полноту.

5. **Семантика счётчика `upserted` (A11)** — подтвердить в плане.

6. **Дефолт нераспознанных областей (10 %)** — подтвердить владельцем или оставить как стартовый, калибруемый.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | сборка/тесты | ✓ | go1.27.1 darwin/arm64 | — |
| Docker (OrbStack) | dev-БД, тестовая БД | ✓ | 29.4.0, демон отвечает | — |
| PostgreSQL 17 (контейнер `postgres:17`) | БД | ✓ (образ запускался в scratch) | 17 | — |
| `psql` на хосте | ручные запросы | ✗ | — | `docker compose … exec postgres psql` |
| Сеть до `marathonec.ru` с Mac | D-01/D-04, прогон | не проверялось (запрет на запросы в исследовании) | — | при недоступности — решение владельца; обход запрещён |
| Интернет к proxy.golang.org / sum.golang.org | `go get x/net` | ✓ (проверено) | — | — |

**Missing dependencies with no fallback:** нет (сеть до Марафонца проверяется первой задачей фазы).
**Missing dependencies with fallback:** `psql` — использовать `docker compose exec`.

## Security Domain

`security_enforcement: true`, ASVS Level 1 (`.planning/config.json`).

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | нет (нет пользователей/логинов в этой фазе) | — |
| V3 Session Management | нет | — |
| V4 Access Control | ограниченно: CLI `accept-baseline` доступен тому, у кого есть `DATABASE_URL` | доступ к БД = доступ администратора; в Phase 4 — `ADMIN_TG_IDS` |
| V5 Input Validation | **да** — недоверенный HTML/текст/URL из сети | строгая валидация в Validate; `registration_url` только `http/https` ≤ 2048; NUL/UTF-8 санитизация; SQL только параметрами; лимит тела 10 МБ; лимит вложенности HTML (512) |
| V6 Cryptography | нет (SHA-256 для хеша — stdlib `crypto/sha256`, уже в `store`) | — |
| V7 Error Handling/Logging | **да** | не логировать DSN/контакт/тела; причины — без внутренних путей |
| V12 Files | **да** — запись сырых ответов | каталог `0700`/файлы `0600`, имена файлов фиксированные (не из данных сайта), запись через temp+rename |
| V13 API/Web Client | **да** — исходящие запросы | только https-адрес источника из конфигурации, редиректы только на тот же хост, таймауты, rate limit, UA без секретов |

### Known Threat Patterns for этот стек

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Вредоносный/битый HTML (глубокая вложенность, гигантское тело) | DoS | `LimitReader` 10 МБ + ошибка при превышении; `html.Parse` отвергает вложенность > 512; таймаут 30 с |
| SSRF через редирект на внутренний адрес | Tampering/Info Disclosure | `CheckRedirect`: ≤ 3 перехода и только на тот же хост |
| `javascript:`/`data:` в `registration_url` (потом в Telegram) | Tampering | принимать только `http`/`https`, иначе поле пустое |
| Инъекция в SQL из данных сайта | Tampering | параметры `$N` (как в Phase 2); строки не склеивать |
| NUL/невалидный UTF-8 → падение прогона | DoS | санитизация в Parse |
| Утечка контакта владельца через логи/сырые файлы | Info Disclosure | `OWNER_CONTACT` только в заголовке запроса; не в логах, не в `meta.json`, не в фикстуре |
| Фикстура с токенами/e-mail в git | Info Disclosure | просмотр перед коммитом; `.gitignore` для `/data/` |
| Порча каталога сломанным парсером | Tampering | `suspect`/`failed` ничего не применяют (структурно), `gone`-логика только для здоровых прогонов |
| Очистка реальной БД тестами | Tampering | guard `current_database()` оканчивается на `_test` (LO-11) |
| Открытая dev-БД | Info Disclosure | порт только `127.0.0.1`, пароль из `.env`, именованный том |

## Рекомендуемая нарезка на планы (для планировщика)

1. **03-01 — Источник и pure-часть:** (T1, checkpoint) фикстура D-01 + анализ `captcha`/`ld+json`/минимальной даты/формата области — СТОП при отсутствии `post-row`; `go get golang.org/x/net@v0.59.0`; `normalize`: `regions.go` + ME-04/ME-05/римские цифры/City-префиксы по фикстуре (тесты); `Parse` + golden + edge-фикстуры; `Prepare` (Validate+Normalize+Stats) + golden `prepared`.
2. **03-02 — Store, Fetch, качество:** миграция `00002`; `store`: `StartRun/FinishRun/Baseline/SourceHealth/Snapshot`, `UpsertSourceEvent(+runID)`, `SaveEventFor`, `Observe`; `_test`-guard (LO-11); `Fetcher` + тесты `httptest`; `Evaluate` + табличные тесты границ; `rawstore`; интеграционные тесты идемпотентности и наблюдения.
3. **03-03 — Runner, worker, реальные данные:** `Runner` (порядок «чистые стадии → Evaluate → запись»), планировщик, CLI (`run-once`, `accept-baseline`, `sanity-report`), `slog`, `docker-compose.dev.yml`, `.env.example`, `.gitignore` (`/data/`); (checkpoint) первый реальный прогон → `suspect` по D-05 → санити-отчёт → **решение владельца о RU** → переключение политики → `accept-baseline` → второй прогон (идемпотентность на живых данных) → проверка критериев ROADMAP 1–5.

Порядок внутри 03-03 обязателен: отчёт → решение → политика → принятие.

## Sources

### Primary (HIGH confidence)
- proxy.golang.org: `golang.org/x/net/@latest` (v0.59.0, 2026-09-08), `@v/v0.59.0.mod` (go 1.26.0; требования), `@v/list`; `golang.org/x/text/@latest` (v0.42.0)
- sum.golang.org: `lookup/golang.org/x/net@v0.59.0` (хеши совпали с `go.sum` scratch-модуля)
- vuln.go.dev/index/modules.json — записи `golang.org/x/net`, последняя исправлена в 0.56.0
- api.github.com/repos/golang/net — зеркало, не archived, 3044 звезды, `pushed_at` 2026-09-28
- pkg.go.dev/golang.org/x/net@v0.59.0/html — `Parse`, `ParseWithOptions`, `Node` и итераторы (с v0.31.0), UTF-8, лимит вложенности 512
- Код проекта (прочитан целиком в этой сессии): `migrations/00001_init_schema.sql`, `internal/store/store.go`, `events.go`, `internal/normalize/*.go`, `internal/event/event.go`, `cmd/aggregator/main.go`
- Запуски в scratch (проект не менялся): `go get`/`go vet`/`go run`/`go test` для x/net, fetch, регионов, normalize; сценарии SQL на `postgres:17` в одноразовом контейнере (удалён)

### Secondary (MEDIUM confidence)
- `.planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md` §2.1, §4.2, §5, §8 — структура страницы Марафонца (снимки 2026-09-25/26/28; срок годности 7 дней)
- `MVP.md` §3, §4.4, §5, §6, §7.3, §16, §19; `.planning/REQUIREMENTS.md`; `03-CONTEXT.md`; `02-REVIEW.md`; `02-SECURITY.md`

### Tertiary (LOW confidence)
- Context7 не содержит `golang.org/x/net` (поиск дал только `x/crypto`, `x/term`, `x/exp`) — документация по `x/net/html` взята с pkg.go.dev.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — версия, хеши, зависимости, уязвимости проверены; `go get` воспроизведён в копии модуля
- Architecture: HIGH — pipeline, миграция, SQL наблюдения, индекс `running`, fetch и таблица регионов проверены запуском; MEDIUM в части формы страницы Марафонца (нет запроса к сайту, D-01)
- Pitfalls: HIGH для общих (fetch, контексты, идемпотентность), MEDIUM для специфичных (прошедшие события, формат «области» — определит фикстура)

**Research date:** 2026-09-29
**Valid until:** 7 дней для выводов о разметке `marathonec.ru` (срок годности Phase 1); 30 дней для стека и архитектурных решений
