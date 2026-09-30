# Phase 4: Telegram Search - Research

**Researched:** 2026-09-30
**Domain:** Go-бот на long polling (`go-telegram/bot`) поверх готовых `store`/`ingest`/PostgreSQL 17 + production Docker Compose на VPS (образ с Mac, скрипты PREFLIGHT/APPLY/VERIFY для владельца)
**Confidence:** HIGH по библиотеке бота, SQL, callback-кодеку, Dockerfile и Compose (исходники библиотеки прочитаны, SQL прогнан на PostgreSQL 17, образ собран и поднят через Compose локально); MEDIUM по действиям на VPS (в VPS доступа нет по ограничению задачи — пакеты и версии проверены по каталогу Ubuntu)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
### Уже зафиксировано до обсуждения (MVP §8, §10.2, §11.2 — не пересматривать)
- Long polling через `go-telegram/bot`; без Redis; состояние фильтров в `users.filters` (jsonb) + `filters_v`; устаревший callback → понятное сообщение.
- `callback_data` ≤ 64 байт, роутинг по префиксу (`f:` фильтры, `p:` страница, `e:` карточка), один обработчик на экран, `answerCallbackQuery` на каждый callback, листание через `editMessageText`, пагинация `LIMIT/OFFSET` по `ORDER BY start_date, id`.
- По умолчанию: будущие даты, вся Россия. Дата: пресеты («30 дней», «этот месяц», «следующий месяц») и выбор месяца. Город: кнопки топ-10 из БД и текстовый ввод с приведением к `city_key`. Регион в интерфейсе не фильтруется.
- В выдаче только `is_listed` и без `merged_into_id`; `cancelled`/`postponed` не скрываются, пометки `[ОТМЕНЁН]`/`[ПЕРЕНЕСЁН]` в начале названия в списке и в карточке.
- Карточка: название, даты и время, город и адрес, тип, дистанции, короткое описание (≤ 1000), кнопка регистрации, источники со ссылками.
- О пользователях — только Telegram ID и настройки поиска.
- Алерт один раз на смену состояния, напоминание через 24 ч; Telegram API недоступен → только лог и ретраи.
- Ошибки Telegram API (блокировка бота, `TooManyRequests`, «message is not modified») не роняют бот (BOT-09).

### Где и как живёт бот
- **D-01:** Бот сразу работает на VPS (не на Mac). — **Reversibility:** reversible — тот же образ запускается локально.
- **D-02:** Один Telegram-бот на всё (будущий production-бот, отдельного dev-бота нет). Токен `TELEGRAM_BOT_TOKEN` только в `.env` на VPS (права 600) и в локальном `.env` владельца; не в репозитории, не в логах, не в ошибках.
- **D-03:** На VPS разворачивается минимальный production-состав MVP §10.2 (PRD-01): `postgres` (PostgreSQL 17, именованный том, порт наружу не публикуется, healthcheck), одноразовый `migrate`, `bot`, `worker` (`restart: unless-stopped`, ротация логов `max-size 10m`, `max-file 3`), образ на Go 1.27.1. Worker собирает Марафонец уже с VPS. Бэкапы, heartbeat и инфраструктурные алерты — Phase 6.
- **D-04:** Подготовка хоста до развёртывания — минимум: установка security-обновлений и перезагрузка, установка плагина Docker Compose; Postgres доступен только во внутренней сети Docker. SSH root/пароль, `ufw`, swap, tinyproxy — отдельными шагами в Phase 6 (PRD-02 остальное, PRD-07). — **Reversibility:** costly — изменения хоста; каждое только с отдельного разрешения владельца.
- **D-05:** Доставка: образ собирается на Mac (`docker build --platform linux/amd64`), переносится `docker save | ssh … docker load`; на VPS только compose-файл и `.env` (600). Исходники, Go и реестр образов на VPS не нужны; git-remote не требуется.
- **D-06:** Все действия на VPS выполняет владелец от root по готовым скриптам по образцу bootstrap Phase 1: PREFLIGHT (только чтение) → APPLY → VERIFY, однострочник с проверкой полного SHA256 перед выполнением (`STOP: HASH MISMATCH`), без больших heredoc для ручной вставки. Проектный ключ `runagg` остаётся только для чтения; у инструментов нет административного доступа.
- **D-07:** БД на VPS создаётся с нуля. Первый прогон worker с VPS по правилу D-05 Phase 3 будет `suspect` («первый прогон источника») и принимается владельцем (кнопкой в алерте, D-15 этой фазы, или CLI). Политика `RUOnlyFallback` Марафонца уже в коде. Данные dev-БД на VPS не переносятся.

### Фильтры
- **D-08:** Корзины дистанций — сплошные диапазоны без дыр: «до 5 км» (≤ 5,5 км), «5–15 км», «15–30 км, полумарафон», «30–43 км, марафон», «ультра > 43 км». Событие попадает в корзину, если хотя бы одна его дистанция в диапазоне. Можно выбрать несколько корзин. Точные границы (включение концов) — на усмотрение планировщика в пределах этих диапазонов.
- **D-09:** Фильтр типа показывает только типы, реально встречающиеся среди выдаваемых событий (сейчас «Шоссе», «Трейл»), плюс «Любой».

### Экраны и список
- **D-10:** После «Найти забег» — один экран-сводка текущих фильтров («Дата: … · Город: … · Дистанция: … · Тип: …») с кнопками [Дата] [Город] [Дистанция] [Тип] [Показать N] [Сбросить]; каждая кнопка открывает подменю и возвращает к сводке. Пошагового мастера нет.
- **D-11:** Список: 5 событий на странице. В тексте сообщения 5 строк вида «17.10 · Москва · Название · 5/10/21 км» с пометками статуса; под ними кнопки «1…5» для карточек, [←] [стр. 2/7] [→] [Фильтры].
- **D-12:** Город не найден → «В каталоге нет забегов в …» и похожие города по началу слова. Пустая выдача → «Ничего не нашлось» с кнопками снять один из фильтров (дату, город, дистанцию, тип).

### Алерты и админка
- **D-13:** Алерты о `failed`/`suspect` отправляет процесс `bot`: раз в минуту читает новые завершённые прогоны из `ingest_runs` и шлёт `ADMIN_TG_IDS`. Токен есть только у бота; worker от Telegram не зависит. Состояние «уже оповещено» (по источнику и состоянию, время последнего напоминания) хранится в БД.
- **D-14:** Отдельного сигнала «всплеск ошибок парсера» нет: он выражается статусом прогона (доля отбраковки и нераспознанных полей → `suspect`, «структура не найдена» → `failed`). Алерт `suspect` показывает причину и счётчики rejected / unrecognized_region / out_of_scope.
- **D-15:** В алерте о `suspect` есть кнопка [Принять как базу] с подтверждением, только для `ADMIN_TG_IDS`; вызывает тот же `Runner.Accept` (атомарный, ME-01 Phase 3). `worker accept-baseline` остаётся.

### Зависимость
- **D-16:** `github.com/go-telegram/bot` одобрен владельцем 2026-09-30 при планировании Phase 4 (MVP §3.1). Точная версия фиксируется, легитимность проверяется так же, как для pgx/goose/x/net. Других новых зависимостей нет.

### Discretion (на усмотрение планировщика)
- Тексты сообщений и эмодзи; формат дат и дистанций в строках; порядок кнопок.
- Схема хранения «уже оповещено» (таблица или колонки) и миграция `00003`.
- Базовый образ (distroless/alpine/scratch), имя compose-проекта, сеть, healthcheck бота.
- Формат `/runs` (последние прогоны по источникам: статус, время, счётчики, причина).
- Как бот узнаёт «похожие города» (префикс по `city_key`/названию).
- Отложенное из Phase 3, если касается кода этой фазы: часовой пояс по субъекту РФ (сейчас `timezone` пуст у 118 из 133) — нужен ли для показа времени в карточке.

### Deferred Ideas (OUT OF SCOPE)
- Бэкапы, heartbeat/dead man's switch, инфраструктурные алерты, runbook — Phase 6.
- SSH только по ключу и без root, `ufw`, swap, синхронизация часов, судьба tinyproxy — Phase 6 (PRD-02 остальное, PRD-07).
- Уборка проектного доступа `runagg` — STATE.md Pending Todos, не позже Phase 6.
- `/dupes` и ежедневная сводка кандидатов — Phase 5.

</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| BOT-01 | Бот на long polling, `/start`, сценарий «Найти забег» | §Standard Stack (v1.27.0); §Pattern 1 (сборка бота, `b.Start(ctx)`, `MatchTypeCommandStartOnly`); подкоманда `bot` в `cmd/aggregator` |
| BOT-02 | Фильтры: дата (пресеты и месяц), город (топ-10 + текст), дистанция (корзины), тип; по умолчанию будущие даты, вся Россия | §Pattern 3 (модель фильтров, пресеты дат), §Pattern 4 (SQL, проверен на PG17 по 133 реальным событиям), §Pattern 5 (город: топ-10, «похожие»), корзины D-08 |
| BOT-03 | Фильтры в `users.filters` с `filters_v`; устаревший callback — понятное сообщение | §Pattern 2 (кодек и семантика `filters_v` как счётчика ревизий, MVP §8.3), §Pattern 6 (stale/неизвестный callback, `MaybeInaccessibleMessage`) |
| BOT-04 | `LIMIT/OFFSET` по `start_date, id`; `editMessageText`; `callback_data` ≤ 64 байт; префиксный роутинг; `answerCallbackQuery` на каждый callback | §Pattern 2 (грамматика, длины), §Pattern 4 (SQL), §Pattern 6 (ровно один answer на callback) |
| BOT-05 | Карточка: название, даты, время, город, адрес, тип, дистанции, описание ≤ 1000, регистрация, источники | §Pattern 7 (экраны, HTML-экранирование, URL-кнопка, `EventSources`), §Часовой пояс (время без конвертации) |
| BOT-06 | Только `is_listed` без `merged_into_id`; `cancelled`/`postponed` с пометкой | §Pattern 4 (предикат совпадает с частичным индексом), §Pattern 7 (пометки), проверка в карточке при устаревшей кнопке |
| BOT-07 | `/runs` только `ADMIN_TG_IDS` | §Pattern 9 (админ-проверка на каждом апдейте, формат `/runs`, SQL `row_number()`) |
| BOT-08 | Только Telegram ID и настройки | §Pattern 1 (свой default-handler: библиотека по умолчанию логирует весь апдейт), §Security Domain |
| BOT-09 | Ошибки Telegram API не роняют бот | §Pattern 6 (типизированные ошибки: проверено на httptest), recover-middleware (обработчики библиотеки идут в горутинах без recover) |
| MON-05 | Алерты `failed`/`suspect`, один на смену состояния, напоминание 24 ч | §Pattern 8 (миграция `00003_source_alerts`, чистая функция `decide`, цикл раз в минуту), кнопка приёма D-15 |
| PRD-01 | Compose: postgres, migrate, bot, worker; образ на Go 1.27.1 | §Pattern 10 (Dockerfile и `docker-compose.yml` собраны и подняты локально), §Pattern 11 (скрипты VPS) |
</phase_requirements>

## Project Constraints (проектные правила)

Источник: проектный файл правил (исключение из глобальных Git-правил; источник истины — `MVP.md` §3.3) и наследие Phase 2–3.

- Разрешены `git add`, `git commit`, обычный `git push` в настроенный remote (remote пока не настроен); **запрещено без подтверждения владельца:** `push --force`, `--force-with-lease`, `reset --hard`, удаление веток/remote и прочие разрушительные операции. Теги не создавать. Сообщения коммитов не содержат упоминаний инструментов автоматизации (правило проекта).
- Источник истины — `MVP.md`; планирование — `.planning/`.
- Весь SQL — только в `internal/store`, константами с `$N`, без склейки строк (02-CONTEXT, 02-SECURITY; подтверждено чтением `internal/store/*.go`).
- Секреты только из окружения; ошибки конфигурации называют переменную, но не повторяют значение (образец: `ingest.LoadConfig`, `databaseURL`); `.env` в `.gitignore`.
- Новая зависимость — только с одобрения владельца: для этой фазы одобрена одна, `github.com/go-telegram/bot` (D-16).
- Интеграционные тесты идут на реальной PostgreSQL 17 и пропускаются без `TEST_DATABASE_URL`; имя базы обязано оканчиваться на `_test`; пакеты гоняются с `-p 1` (общая БД).
- Любые изменения на VPS — только владелец, от root, по готовым скриптам (D-06); у инструментов доступа к VPS нет. **Эта сессия:** к VPS, `marathonec.ru` и другим сайтам-источникам запросов не делалось; файлы `.env` не читались; код проекта не менялся (все пробы — в каталоге scratchpad вне репозитория).

## Summary

Фаза добавляет три вещи поверх готового ядра: (1) пакет `internal/bot` (роутер, экраны, клавиатуры, кодек callback) с SQL выдачи в `internal/store`; (2) цикл алертов в процессе `bot` (`internal/alert`) с состоянием «уже оповещено» в новой таблице `source_alerts` (миграция `00003`); (3) production-упаковку: Dockerfile, `docker-compose.yml`, скрипты для владельца. Единственная новая зависимость — `github.com/go-telegram/bot` **v1.27.0** (2026-09-11): `go 1.18`, **ноль транзитивных зависимостей**, MIT, `go mod verify` и `go.sum` совпадают с `sum.golang.org`.

Главные технические находки, которые меняют план: (a) обработчики библиотеки запускаются `go r(ctx, b, upd)` **без recover** — паника в любом обработчике убивает процесс, нужен middleware; (b) обработчик по умолчанию библиотеки логирует **весь апдейт** через стандартный `log` (текст, имя, username) — надо заменить (BOT-08); (c) `pgx` кодирует `nil`-срез как `NULL`, и `cardinality(NULL) = 0` даёт NULL — выдача молча пустеет (проверено); (d) MVP §10.3 верен: на VPS стоит `docker.io` **из Ubuntu** (29.1.3-0ubuntu3~24.04.2 — версия совпала с аудитом), поэтому Compose ставится пакетом `docker-compose-v2` 2.40.3 из universe, а **не** из репозитория Docker (в постановке задачи предполагался репозиторий Docker — это не так); (e) без `.dockerignore` или узкого `COPY` в образ попадает `.env` — Dockerfile копирует только `go.mod go.sum cmd internal migrations`; (f) именованный том получает владельца 65532 от каталога образа — RAW_DIR `/data/raw` пишется без `chown` (проверено).

Часовой пояс для карточки не нужен: `start_time` — локальное «настенное» время события без конвертации (`internal/event/event.go`), у 133 событий Марафонца времени нет вообще, адреса и описаний тоже нет — карточка показывает время только если оно есть, «сегодня» считается по `Europe/Moscow`. Справочник «субъект → IANA» остаётся в бэклоге (Phase 5+).

**Primary recommendation:** `go get github.com/go-telegram/bot@v1.27.0`; экраны — чистые функции `(состояние) → (текст, клавиатура)`; фильтры — абсолютные значения в `users.filters` + счётчик `filters_v` (растёт только при реальном изменении); выдача — один SQL с `EXISTS … unnest(lo[], hi[])` для корзин; алерты — чистая функция `decide` + `source_alerts`; `Runner.Accept` вызывается из бота с источником без сети (`marathonec.New(nil)`), а `GoneAfter*` берутся из `ingest.LoadConfig(false)`, чтобы бот и worker наблюдали одинаково; production — distroless `static:nonroot`, сборка на Mac с `--platform linux/amd64`, `docker save | gzip | ssh … docker load`.

## Architectural Responsibility Map

«Ярусы» — процессы и хранилища (в проекте нет браузера/фронтенд-сервера).

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Приём апдейтов (long polling), отправка/правка сообщений | Процесс `bot` (`go-telegram/bot`) | — | MVP §3, §8.1: исходящий HTTPS к api.telegram.org, входящих портов нет |
| Экраны, клавиатуры, кодек callback, форматирование | `internal/bot` (чистые функции) | — | Тестируются без сети и БД |
| Хранение фильтров и версии | PostgreSQL `users` через `store` | — | MVP §8.1: без Redis; `filters_v` — счётчик ревизий |
| Выдача: фильтры, count, LIMIT/OFFSET, топ-города, типы | PostgreSQL через `store` | — | Весь SQL только в `store`; индексы Phase 2 |
| Проверка админа (`ADMIN_TG_IDS`) | Процесс `bot` (по `From.ID` каждого апдейта) | — | Единственный «аутентификатор» — Telegram-ID из апдейта, полученного по TLS из long polling |
| Алерты о прогонах | Процесс `bot` (`internal/alert`, тикер 1 мин) | БД (`ingest_runs` читается, `source_alerts` пишется) | D-13: токен только у бота, worker от Telegram не зависит |
| Принятие прогона по кнопке | Процесс `bot` → `ingest.Runner.Accept` | БД (транзакция ClaimRun) | D-15: тот же атомарный путь, что CLI; сеть не нужна |
| Сбор данных | Процесс `worker` (без изменений) | — | Фаза 3 |
| Сборка образа | Mac владельца (`docker build --platform linux/amd64`) | — | D-05: ни Go, ни исходников на VPS |
| Запуск и жизненный цикл контейнеров | Docker Compose на VPS | Docker (`restart: unless-stopped`) | PRD-01 |
| Подготовка хоста, развёртывание, откат | Владелец от root по скриптам | — | D-06: у инструментов доступа нет |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/go-telegram/bot` | **v1.27.0** [VERIFIED: proxy.golang.org, `Time` 2026-09-11T09:33:41Z] | Long polling, роутинг по префиксу, типизированные ошибки Bot API | Решение владельца D-16 / MVP §3.1; ноль зависимостей, `go 1.18` в собственном `go.mod` [VERIFIED: `proxy.golang.org/github.com/go-telegram/bot/@v/v1.27.0.mod` — «module github.com/go-telegram/bot / go 1.18», без `require`] |
| stdlib: `log/slog`, `net/http`, `net/url`, `html`, `time` (+ `time/tzdata`), `encoding/json`, `sync`, `context`, `errors`, `strconv`, `unicode/utf8` | Go 1.27.1 | Логи, валидация URL, экранирование HTML, часовой пояс, тикер алертов | MVP §3.1: планировщик, rate limit, конфиг — stdlib |
| `pgx/v5` v5.11.0, `goose/v3` v3.28.0, `x/net` v0.59.0 | уже в `go.mod` | БД и миграции | Без изменений [VERIFIED: `go.mod`] |

Эффект на `go.mod` проверен в копии (проект не менялся): добавляется одна строка `require github.com/go-telegram/bot v1.27.0`, косвенных зависимостей нет; `go.sum` получает ровно две строки, совпадающие с ответом `sum.golang.org/lookup/github.com/go-telegram/bot@v1.27.0`:

```
github.com/go-telegram/bot v1.27.0 h1:PG9yBDzM8O26oUerE9SZwWlHon4RpH8WcNslZ7CEZmE=
github.com/go-telegram/bot v1.27.0/go.mod h1:i2TRs7fXWIeaceF3z7KzsMt/he0TwkVC680mvdTFYeM=
```

`go mod verify` — «all modules verified»; `go list -deps` показывает только stdlib (с её `vendor/golang.org/x/*`) и подпакет `github.com/go-telegram/bot/models`; `go vet` чисто [VERIFIED: scratch-модуль, Go 1.27.1 darwin/arm64].

### Инфраструктура (образы и пакеты)
| Что | Версия/тег | Назначение | Примечание |
|-----|-----------|------------|------------|
| `golang` | `1.27.1` [VERIFIED: hub.docker.com, обновлён 2026-09-19, есть amd64 и arm64] | Стадия сборки | `FROM --platform=$BUILDPLATFORM` — кросс-компиляция без QEMU |
| `gcr.io/distroless/static-debian13:nonroot` | tag [VERIFIED: `docker manifest inspect` вернул OCI-индекс; `static-debian12:nonroot` тоже существует и на нём образ собран и запущен] | Финальный образ | Содержит ca-certificates, tzdata, `/tmp`, пользователь nonroot 65532 [CITED: github.com/GoogleContainerTools/distroless, base/README.md]. Оболочки нет: healthcheck только в exec-форме |
| `postgres` | `17` [VERIFIED: hub.docker.com, обновлён 2026-09-24] | БД | Тот же образ, что в dev/test |
| `docker-compose-v2` (пакет Ubuntu, universe) | 2.40.3+ds1-0ubuntu1~24.04.1 [CITED: packages.ubuntu.com/noble-updates/docker-compose-v2]; плагин ставится в `/usr/libexec/docker/cli-plugins/docker-compose` [CITED: packages.ubuntu.com/noble-updates/amd64/docker-compose-v2/filelist] | `docker compose` на VPS | Зависит только от libc6; Recommends: docker.io (уже стоит) |
| `docker.io` на VPS | 29.1.3-0ubuntu3~24.04.2 в noble-updates [CITED: packages.ubuntu.com/noble-updates/docker.io] = версия из аудита («Docker 29.1.3, containerd 2.2.1, runc 1.3.4», `01-VPS-AUDIT.md` §2) | Движок | Доказывает, что Docker на VPS — пакет Ubuntu, не репозиторий Docker Inc. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Пакет `docker-compose-v2` из Ubuntu | Репозиторий Docker Inc. (`docker-compose-plugin`) | Потребовал бы менять источник самого `docker.io` (конфликт пакетов `docker.io`/`docker-ce`) — рискованно для хоста с чужими сервисами; отвергнуто |
| `gcr.io/distroless/static` | `scratch`, `alpine` | `scratch` без CA/tzdata/nonroot-пользователя; `alpine` даёт оболочку, но лишнюю поверхность. Distroless проверен на практике |
| Именованный том для `RAW_DIR` | bind-mount `/opt/aggregator/raw` | Bind требует `chown 65532` на хосте вручную; именованный том получает владельца от каталога образа автоматически (проверено) |
| Все обработчики асинхронно (по умолчанию) | `WithNotAsyncHandlers()` в проде | Последовательная обработка блокирует опрос во время ретрая 429; для тестов `WithNotAsyncHandlers` нужен, для прода нет |

**Installation:**
```bash
go get github.com/go-telegram/bot@v1.27.0 && go mod tidy
```

## Package Legitimacy Audit

Шлюз `package-legitimacy check` принимает только экосистемы `npm|pypi|crates` (запуск с `--ecosystem go` вернул usage). Для Go аудит выполнен вручную тем же способом, что для `x/net` в 03-RESEARCH, — по прокси модулей и базе контрольных сумм.

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `github.com/go-telegram/bot` v1.27.0 | proxy.golang.org (Go modules) | первая версия v0.1.0 — 2022-05-02; 27 релизов до v1.27.0 (2026-09-11) [VERIFIED: `@v/list`, `@v/v0.1.0.info`] | не измерялось (у прокси нет публичной статистики; `gh` в среде нет) | github.com/go-telegram/bot [VERIFIED: поле `Origin.URL` в `@latest`]; лицензия MIT (файл `LICENSE` в модуле) | OK (ручной) | Approved владельцем (D-16, MVP §3.1); хеши модуля совпали с `sum.golang.org`; нет транзитивных зависимостей; нет `postinstall`-аналогов (Go), код прочитан: сетевые вызовы только к `b.url` (по умолчанию `https://api.telegram.org`), токен в URL маскируется как `***` в ошибках `*url.Error` и debug-логе |

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none. Замечание: версии 1.25–1.27 вышли в течение двух недель (2026-08-26 … 2026-09-11) — библиотека активно правится; версия фиксируется точно, обновление — отдельным решением.

## Architecture Patterns

### System Architecture Diagram

```
                Telegram (пользователь / админ)
                     │  ▲
     getUpdates      │  │ sendMessage / editMessageText / answerCallbackQuery
     (long poll, TLS)▼  │ (multipart POST; 429 → retry_after, 403 → бот заблокирован)
   ┌────────────────────────────────────────────────────────────────────┐
   │ процесс bot                                                        │
   │  b.Start(ctx) ─► middleware: recover ─► privateOnly ─► роутер      │
   │     /start ─────────────► экран «Найти забег»                      │
   │     f:  ─► handleFilter ─► users.filters (+filters_v) ─► сводка    │
   │     p:  ─► handleList   ─► store.ListEvents/CountEvents ─► список  │
   │     e:  ─► handleCard   ─► store.GetEvent + EventSources ─► карточка│
   │     текст ─► default ─► normalize.CityKey ─► CityExists/Similar    │
   │     /runs, r: ─► проверка From.ID ∈ ADMIN_TG_IDS ─► runs / Accept  │
   │  alert.Loop (тикер 1 мин, отдельная горутина)                      │
   │     LatestFinishedRuns ─► decide(run, source_alerts, now)          │
   │        ─► send / remind / clear ─► sendMessage(ADMIN_TG_IDS)       │
   └───────────────┬───────────────────────────────────┬────────────────┘
                   │ internal/store (весь SQL, pgx)    │ ingest.Runner.Accept
                   ▼                                   │ (marathonec.New(nil): сеть не нужна)
        ┌───────────────────────┐                      ▼
        │ PostgreSQL 17         │◄──────── ClaimRun/FinishRun в одной транзакции
        │ events, event_distances, source_events, sources, ingest_runs,
        │ users, source_alerts (00003)
        └───────────▲───────────┘
                    │ запись каталога / ingest_runs (без изменений)
             процесс worker (Phase 3) ──► marathonec.ru
   Compose: postgres ◄─ migrate (one-shot) ◄─ {bot, worker};  наружу порты не публикуются
```

### Recommended Project Structure
```
cmd/aggregator/main.go        # bot: runBot (конфиг, store, Runner, bot.New, alert.Loop); newSources(f) общий для worker и bot
internal/event/               # + Filter (даты, city_key, тип, диапазоны корзин) — MVP §3.2
internal/store/               # + users.go, listing.go (выдача), alerts.go, runs.go (LatestRuns/LatestFinishedRuns)
internal/bot/                 # config.go, bot.go (сборка, middleware), callback.go (кодек), filters.go, screens.go, handlers.go, tg.go (обёртки send/edit/answer + классификация ошибок)
internal/alert/               # decide.go (чистая функция), loop.go
migrations/00003_source_alerts.sql
Dockerfile, .dockerignore, docker-compose.yml
deploy/                       # ship.sh (Mac), vps/*.sh (владелец), README.md с SHA256-однострочниками; без секретов
```

### Pattern 1: сборка бота (проверено чтением исходников v1.27.0 и запуском)
**What:** `bot.New(token, opts...)` + `b.Start(ctx)` (блокируется до отмены ctx). Обработчики ищутся по порядку регистрации, при отсутствии совпадения — `WithDefaultHandler`.
**Факты из исходников** [VERIFIED: `$GOMODCACHE/github.com/go-telegram/bot@v1.27.0/*.go`, прочитаны в этой сессии]:
- `New` вызывает `getMe` с таймаутом `checkInitTimeout` (по умолчанию 5 с) — при недоступном Telegram `New` вернёт ошибку → процесс выходит с кодом 1, `restart: unless-stopped` перезапускает (fail-fast на неверный токен). Ставим `WithCheckInitTimeout(15*time.Second)`.
- `ProcessUpdate`: `if b.notAsyncHandlers { r(ctx,b,upd); return }; go r(ctx, b, upd)` — обработчик идёт в **новой горутине без recover**: паника завершает процесс. Middleware из `WithMiddlewares` оборачивает и default-handler (первый в списке — внешний).
- `defaultHandler` библиотеки делает `log.Printf("[TGBOT] [UPDATE] %+v", update)` — печатает весь апдейт (текст сообщений, имя, username). Обязательно `WithDefaultHandler`.
- `defaultErrorsHandler` — тоже `log.Printf`; заменить на `slog`. `WithDebug()` не включать (печатает payload запросов).
- `getUpdates`: при 429 ждёт `retry_after`, при прочих ошибках — экспоненциальная пауза до 5 с (`maxTimeoutAfterError`), битый апдейт пропускается и смещение двигается. Повторов **исходящих** `sendMessage`/`editMessageText` библиотека не делает.
- `MatchTypePrefix` — `strings.HasPrefix(callback.Data, pattern)`; `MatchTypeCommandStartOnly` — команда в позиции 0 (`data[e.Offset+1:e.Offset+e.Length] == pattern`, шаблон без «/»). `/start@botname` не совпадёт (в личных чатах не нужно).
- `bot.True()` — указатель на `true` (для `LinkPreviewOptions.IsDisabled`).
- Запросы уходят как `multipart/form-data`, а не JSON; `reply_markup` — JSON-строкой в поле формы.

```go
// Эскиз internal/bot/bot.go. tgbot = github.com/go-telegram/bot
b, err := tgbot.New(cfg.Token,
    tgbot.WithCheckInitTimeout(15*time.Second),
    tgbot.WithErrorsHandler(func(err error) { slog.Error("telegram", "err", err) }),
    tgbot.WithDefaultHandler(h.onDefault),          // текст = город; неизвестный callback = «кнопка устарела»
    tgbot.WithMiddlewares(recoverMW, privateOnlyMW),
    tgbot.WithMessageTextHandler("start", tgbot.MatchTypeCommandStartOnly, h.onStart),
    tgbot.WithMessageTextHandler("runs", tgbot.MatchTypeCommandStartOnly, h.onRuns),
    tgbot.WithCallbackQueryDataHandler("f:", tgbot.MatchTypePrefix, h.onFilter),
    tgbot.WithCallbackQueryDataHandler("p:", tgbot.MatchTypePrefix, h.onList),
    tgbot.WithCallbackQueryDataHandler("e:", tgbot.MatchTypePrefix, h.onCard),
    tgbot.WithCallbackQueryDataHandler("r:", tgbot.MatchTypePrefix, h.onRun), // админ: принятие прогона
)
// Токен — только из окружения; ошибка конфигурации не повторяет значение.
go alertLoop.Run(ctx)
b.Start(ctx) // вернётся после отмены ctx (SIGTERM)

func recoverMW(next tgbot.HandlerFunc) tgbot.HandlerFunc {
    return func(ctx context.Context, b *tgbot.Bot, u *models.Update) {
        defer func() {
            if p := recover(); p != nil { // стек — только в лог, без данных апдейта
                slog.Error("handler panic", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
                if u.CallbackQuery != nil { // не оставлять «часики» на кнопке
                    _, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: u.CallbackQuery.ID})
                }
            }
        }()
        next(ctx, b, u)
    }
}
```
`privateOnlyMW`: пропускает апдейт, только если чат приватный (`models.ChatTypePrivate == "private"`; для callback — `Message.Message.Chat.Type` или `Message.InaccessibleMessage.Chat.Type`); иначе молча возвращается. Это закрывает группы и каналы (состояние фильтров и админ-команды — только личные чаты).

**Порядок обработчиков:** команды регистрируются раньше default; конфликтов префиксов нет (`f:`, `p:`, `e:`, `r:`).
**Параллелизм:** обработчики одного пользователя могут идти одновременно (быстрые нажатия). Изменение фильтров — read-modify-write в одной транзакции `InTx` с `SELECT … FOR UPDATE` строки `users` (см. Pattern 3); значения абсолютные, поэтому повтор безопасен.

### Pattern 2: кодек callback (≤ 64 байт), `filters_v` и устаревшие кнопки
**Семантика `filters_v`:** счётчик ревизий фильтров пользователя, растёт **только при реальном изменении** значения (`filters_v = filters_v + CASE WHEN filters IS DISTINCT FROM $2 THEN 1 ELSE 0 END`). Источник — MVP §8.3: «`p:3:v7`, где `v` — версия фильтров; при несовпадении бот просит обновить выдачу» [CITED: MVP.md §8.3]. Колонка `users.filters_v INTEGER NOT NULL DEFAULT 1` [VERIFIED: `migrations/00001_init_schema.sql:92`, строка `filters_v     INTEGER NOT NULL DEFAULT 1,`].

| Префикс | Формат | Смысл | Макс. длина |
|---------|--------|-------|-------------|
| `f:s` | — | показать сводку (из `/start`, «Назад» из подменю) | 3 |
| `f:d` / `f:d:<v>` | `v` ∈ `a`, `30`, `cm`, `nm`, `mYYYYMM` | подменю даты / установить (`a` = все будущие) | 10 |
| `f:c` / `f:c:<city_key>` / `f:c:-` | `city_key` ≤ 60 байт | подменю города / установить / «любой город» | 64 при ключе 60 |
| `f:b` / `f:b:<i>` / `f:b:-` | `i` ∈ 0..4 | подменю корзин / переключить корзину / сбросить | 5 |
| `f:t` / `f:t:<type>` / `f:t:-` | `type` ∈ шести типов | подменю типа / установить / «любой» | 15 |
| `f:x` / `f:x:<d\|c\|b\|t>` | — | сбросить все фильтры / снять один (D-12) | 5 |
| `p:<page>:v<N>` | page ≥ 1 | страница списка; `N` = `filters_v` на момент отрисовки | ~20 |
| `e:<id>:<page>:v<N>` | id — `events.id` | карточка; «Назад» строит `p:<page>:v<N>` | ~40 |
| `r:a:<runID>` / `r:y:<runID>` / `r:n:<runID>` | runID — `ingest_runs.id` | админ: запросить подтверждение / подтвердить принятие / отмена | ~25 |

**Проблема длины:** `events.city_key` допускает до 64 байт [VERIFIED: `migrations/00001_init_schema.sql:34`, `city_key         TEXT CHECK (city_key IS NULL OR (char_length(city_key) <= 64 AND city_key ~ '^[a-z0-9-]+$'))`], а `f:c:` занимает 4 байта → ключ длиннее 60 не влезет. Реальный максимум в данных Марафонца — 23 байта (133 события, 98 городов), но кодек обязан вернуть ошибку, а кнопка города с длинным ключом — быть пропущена (не упасть на 400 `BUTTON_DATA_INVALID`). Лимит [CITED: core.telegram.org/bots/api#inlinekeyboardbutton]: «callback_data … 1-64 bytes».
**Правило разбора:** строгий парсер `Parse(data string) (Action, error)` — любые лишние/неизвестные части → ошибка → «кнопка устарела». Пользовательский ввод в callback не попадает (только ключи из БД и числа). Тест-свойство: для всех экранов на реальных данных `len(cb) <= 64`.
**Устаревшие кнопки (критерий 2):**
1. `p:`/`e:` с `v` ≠ текущему `filters_v` → `answerCallbackQuery` с `text` («Фильтры изменились — показываю актуальную выдачу», ≤ 200 символов [CITED: core.telegram.org/bots/api#answercallbackquery: «Notification text, 0-200 characters»]) и правка сообщения на страницу 1 актуальной выдачи (а не тупиковое «обновите»).
2. Страница > числа страниц (данные сократились) → зажать до последней.
3. `e:<id>` для события, ставшего `is_listed = false`/слитым → «Событие больше не доступно» + кнопка «К списку».
4. Неизвестный префикс/битый разбор/callback старого формата → default-handler отвечает `answerCallbackQuery` («Кнопка устарела — нажмите /start») — иначе у пользователя остаётся «часики».
5. `CallbackQuery.Message` — `MaybeInaccessibleMessage`: у сообщений старше ~48 ч `Message == nil`, есть `InaccessibleMessage{Chat, MessageID}` [VERIFIED: `models/message.go`, `CallbackQuery.Message MaybeInaccessibleMessage`]; хелпер `msgRef(cq) (chatID int64, msgID int)` берёт данные из любого варианта; при ошибке правки — отправить новое сообщение пользователю (`cq.From.ID`).

### Pattern 3: модель фильтров и пресеты дат
Хранение в `users.filters` (jsonb), значения абсолютные:
```json
{"date":"30","city":"moscow","dist":[1,2],"type":"road"}
```
Пустой объект `{}` (значение по умолчанию колонки) = «все будущие даты, вся Россия, любая дистанция, любой тип». Загрузка терпимая: неизвестные ключи игнорируются, невалидные значения отбрасываются, ошибка разбора → значения по умолчанию (бот не падает на «испорченных» настройках).

| Значение `date` | Диапазон `start_date` (включительно) |
|-----------------|--------------------------------------|
| `""`/`a` | `today … 9999-12-31` |
| `30` | `today … today+30 дней` |
| `cm` | `today … последний день текущего месяца` |
| `nm` | `1-е … последний день следующего месяца` |
| `mYYYYMM` | `max(1-е, today) … последний день месяца` (предлагать месяцы от текущего на 12 вперёд — данные Марафонца охватывают 2026-10 … 2027-10) |

`today` — дата по `Europe/Moscow` (`time.LoadLocation` + `import _ "time/tzdata"` в `cmd/aggregator`, чтобы не зависеть от tzdata базового образа). События хранят **локальную** дату без конвертации (`StartDate is the local wall-clock date`, `internal/event/event.go`), поэтому сравнение идёт по календарной дате; `start_date` из БД приходит как полночь UTC — не переводить её в другой пояс при форматировании.

**Корзины дистанций** (D-08; сплошные полуинтервалы `(lo, hi]` в метрах, так что у дистанции ровно одна корзина и дыр нет; «5 км» с запасом на разметку до 5,5 км):

| idx | Подпись | (lo, hi] м |
|-----|---------|-----------|
| 0 | до 5 км | (0, 5 500] |
| 1 | 5–15 км | (5 500, 15 000] |
| 2 | 15–30 км, полумарафон | (15 000, 30 000] |
| 3 | 30–43 км, марафон | (30 000, 43 000] |
| 4 | ультра > 43 км | (43 000, 2 147 483 647] |

Верхняя граница `2147483647` = максимум `INTEGER` (`event_distances.distance_m INTEGER`). Проверка на 133 реальных событиях: 15 000 (18 шт.) и 30 000 (18) попадают в корзины 1 и 2 соответственно, 42 000 (13) — в марафонскую, 45 000 (2) — в ультра; событие без дистанций (одно из 133) при выбранной корзине в выдачу не попадает — ожидаемо.

**Изменение фильтров** (одна операция на нажатие; повтор идемпотентен):
```go
// store: UpdateFilters читает строку FOR UPDATE, применяет fn, пишет, возвращает новую версию.
func (s *Store) UpdateFilters(ctx context.Context, tgID int64, fn func(cur []byte) []byte) (filters []byte, v int, err error)
```
Реализация — `InTx` (уже есть в `store`): `INSERT … ON CONFLICT (tg_id) DO UPDATE SET last_seen_at = now()` (создание строки), затем `SELECT filters, filters_v FROM users WHERE tg_id = $1 FOR UPDATE`, затем `UPDATE users SET filters = $2, filters_v = filters_v + CASE WHEN filters IS DISTINCT FROM $2::jsonb THEN 1 ELSE 0 END …`. В `users` пишутся только `tg_id`, `filters`, `filters_v`, метки времени (BOT-08); `username`/имя не сохраняются и не логируются.

### Pattern 4: SQL выдачи (прогнан на PostgreSQL 17 по 133 реальным событиям из `prepared.golden.json`)
Предикат `is_listed AND merged_into_id IS NULL` записан буквально как в частичных индексах, поэтому планировщик их использует: для `city_key = 'moscow'` план — `Index Scan using idx_events_city_key_start_date_id`, корзины — `Index Only Scan using event_distances_pkey` в подзапросе [VERIFIED: `EXPLAIN` в одноразовом контейнере postgres:17 со схемой `00001`+`00002`]. Общий фрагмент — Go-константа, склеиваемая с другими константами на этапе компиляции (это по-прежнему константы с `$N`, не построение запроса):

```sql
-- $1 from date, $2 to date (без верхней границы — 9999-12-31), $3 city_key ('' = любой),
-- $4 event_type ('' = любой), $5 lo int[], $6 hi int[] (пары корзин; пусто = любая дистанция)
WHERE e.is_listed AND e.merged_into_id IS NULL
  AND e.start_date >= $1::date AND e.start_date <= $2::date
  AND ($3::text = '' OR e.city_key = $3::text)
  AND ($4::text = '' OR e.event_type = $4::text)
  AND (COALESCE(cardinality($5::int[]), 0) = 0 OR EXISTS (
        SELECT 1 FROM event_distances d, unnest($5::int[], $6::int[]) AS r(lo, hi)
        WHERE d.event_id = e.id AND d.distance_m > r.lo AND d.distance_m <= r.hi))
```
- **Список:** `SELECT e.id, e.title, e.start_date, COALESCE(e.city,''), e.status, COALESCE((SELECT array_agg(d.distance_m ORDER BY d.distance_m) FROM event_distances d WHERE d.event_id = e.id), '{}') FROM events e <WHERE> ORDER BY e.start_date, e.id LIMIT $7 OFFSET $8` (5 строк на страницу, D-11). Результаты для пустых фильтров: 133 события; для «марафон + ультра»: первые 5 — id 19, 40, 48, 83, 120; для «Москва, шоссе, 5–15 км» — id 62, 96, 108, 105, 57. Счёт: `SELECT count(*) FROM events e <WHERE>` — 133 без корзин, 132 со всеми пятью корзинами (событие без дистанций исключается).
- **Ловушка pgx (проверено):** `nil`-срез кодируется как `NULL`; `cardinality(NULL::int[]) = 0` даёт `NULL` и **обнуляет выдачу**. Запрос защищён `COALESCE(cardinality(...), 0)`; кроме того, в Go передавать `[]int32{}`, а не `nil`. На пустом срезе: `cardinality IS NULL` = false, `COALESCE(...)=0` = true; на nil: оба true [VERIFIED: pgx v5.11.0 против postgres:17].
- **Топ-10 городов (фасет):** тот же `WHERE`, но без предиката города и с `AND e.city_key IS NOT NULL`: `SELECT e.city_key, mode() WITHIN GROUP (ORDER BY e.city), count(*) FROM events e <WHERE без города> GROUP BY e.city_key ORDER BY count(*) DESC, e.city_key LIMIT 10`. Кнопки города не ведут в пустую выдачу при выбранных дате/типе/дистанции. Реальные данные: Москва 9, Санкт-Петербург 6, Нижний Новгород 4, затем по 3 (Челябинск, Красноярск, Петрозаводск, Суздаль, Железноводск), затем по 2 (порядок среди равных — по `city_key`; проверено).
- **«Похожие города» (D-12):** префикс по началу слова из `city_key`: `… WHERE <listed> AND e.start_date >= $1 AND (e.city_key LIKE $2 || '%' OR e.city_key LIKE '%-' || $2 || '%') GROUP BY e.city_key ORDER BY count(*) DESC, e.city_key LIMIT 5`. Проверено: `nizh` → Нижний Новгород; `novgorod` → Нижний Новгород и Великий Новгород. `$2` всегда из `normalize.CityKey` (алфавит `[a-z0-9-]`), метасимволов `%`/`_` нет; в Go дополнительно проверять алфавит и длину ≥ 2.
- **Типы (D-09):** `SELECT DISTINCT event_type FROM events e WHERE e.is_listed AND e.merged_into_id IS NULL AND e.start_date >= $1 ORDER BY 1` — сейчас `road` (45) и `trail` (88). Подписи: `road`=Шоссе, `trail`=Трейл (остальные четыре — на выбор планировщика для будущих данных).
- **Источники карточки:** `SELECT s.code, se.url FROM source_events se JOIN sources s ON s.id = se.source_id WHERE se.event_id = $1 AND se.state <> 'gone' AND se.match_state <> 'out_of_scope' ORDER BY s.code`. Отображаемые имена — небольшая Go-таблица `code → название` (`marathonec` → «Марафонец»), для неизвестного кода — сам код. У Марафонца `url` — страница события на marathonec.ru либо страница календаря, если регистрация внешняя (D-13 Phase 3), поэтому «источник со ссылкой» всегда открывается.
- **Дата без верхней границы:** передавать `9999-12-31` (Prepare отбраковывает всё позже today+3 года, поэтому граница не влияет на данные), а не NULL — нет ветки `IS NULL`.
- **Значения схемы, на которые опираются запросы и экраны** (прочитаны в `migrations/00001_init_schema.sql`, дословно): строка 36 `event_type       TEXT NOT NULL CHECK (event_type IN ('road', 'trail', 'cross', 'track', 'obstacle', 'other')),`; строка 37 `status           TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'cancelled', 'postponed')),`; строка 60 `state          TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'missing', 'gone')),`; строка 63 `match_state    TEXT NOT NULL DEFAULT 'new' CHECK (match_state IN ('new', 'auto', 'manual', 'candidate', 'out_of_scope')),`; строка 72 `distance_m INTEGER NOT NULL CHECK (distance_m > 0),`; строка 5 `code TEXT NOT NULL UNIQUE`. Типы Go — `event.Status` (`StatusScheduled|StatusCancelled|StatusPostponed`) и `event.Type` (`TypeRoad|TypeTrail|TypeCross|TypeTrack|TypeObstacle|TypeOther`) в `internal/event/event.go`.
- **Запас производительности:** OFFSET-пагинация с `ORDER BY start_date, id` — как в MVP §8.3; при тысячах событий индекс `idx_events_listed_start_date_id` покрывает порядок. Переход на keyset — не в этой фазе.
- Кэш плана: при повторных вызовах pgx использует подготовленные запросы; для разреженных фильтров возможен generic-план — на этих объёмах несущественно (ASSUMED, не измерялось на больших данных).

### Pattern 5: город — текст, топ-10, «похожие»
Без состояния «жду ввод»: **любое непустое текстовое сообщение (не команда) в личном чате — запрос города**. Так нет колонки для «ожидания», нет гонок и «застрявшего» режима.
1. `q := strings.TrimSpace(text)`; пусто, длиннее 64 рун или начинается с «/» → подсказка «Нажмите /start» (для «/…» команд без обработчика).
2. `key := normalize.CityKey(q)` (синонимы «Москва»/«мск» → `moscow`, «Питер»/«СПб» → `saint-petersburg`, остальное — транслитерация [VERIFIED: `internal/normalize/place.go`]); `""` → «Не понял название города».
3. Если для `key` есть будущие listed-события (`CityName(key, today)`) → установить фильтр (через `UpdateFilters`), показать сводку **новым сообщением** (у текстового ввода нет сообщения для правки).
4. Иначе (D-12): «В каталоге нет забегов в «<как ввёл, экранировано>»» + до 5 кнопок «Нижний Новгород (4)» из `SimilarCities(key)` (`f:c:<key>`) + «Назад».
5. Пустая выдача (D-12): «Ничего не нашлось» + кнопки `f:x:d|c|b|t` только для реально заданных фильтров.

### Pattern 6: ошибки Telegram API (BOT-09) и жизненный цикл callback
Типы ошибок проверены против фиктивного Bot API на `httptest` (ответы 400/403/429 в формате Bot API) [VERIFIED: probe в scratchpad]:

| Ситуация | Как распознаётся | Реакция |
|----------|------------------|---------|
| Бот заблокирован пользователем | `errors.Is(err, tgbot.ErrorForbidden)`; сообщение `forbidden, Forbidden: bot was blocked by the user` | INFO в лог, без повтора; для админа в алертах — «не доставлено» |
| `TooManyRequests` | `var tm *tgbot.TooManyRequestsError; errors.As(err, &tm)` → `tm.RetryAfter` (=7 в пробе). Не использовать `IsTooManyRequestsError`: там type assertion без разворачивания | ждать `min(RetryAfter, 30 с)` и повторить один раз; иначе WARN и отбросить |
| «message is not modified» | `errors.Is(err, tgbot.ErrorBadRequest) && strings.Contains(err.Error(), "message is not modified")` (проверено) | считать успехом (это листание на ту же страницу) |
| Сообщение нельзя править / не найдено | `ErrorBadRequest` + текст описания [ASSUMED: точные формулировки «message to edit not found», «message can't be edited»] | отправить новое сообщение |
| Callback слишком старый | `ErrorBadRequest` + текст [ASSUMED: «query is too old and response timeout expired or query ID is invalid»] | игнорировать (лог DEBUG) |
| Неверный токен / чужой getUpdates | `ErrorUnauthorized` / `ErrorConflict` | лог ERROR; для 409 — другой экземпляр или webhook |

Классификацию держать в **одной** функции `classify(err) kind` с табличным тестом на строках описаний (текст описаний задаёт сервер Telegram, это самая хрупкая часть — [ASSUMED] помечены именно они).
**Ровно один `answerCallbackQuery` на callback:** повторный ответ на тот же `callback_query_id` завершится ошибкой, поэтому обработчик вычисляет результат и в конце (`defer`) отвечает один раз, при необходимости с `Text` (stale-случаи). Порядок: разобрать → выполнить → правка сообщения → ответ; вся цепочка — доли секунды.
**Исходящие сообщения:** длина текста ≤ 4096 символов [CITED: core.telegram.org/bots/api#sendmessage: «1-4096 characters after entities parsing»]; список — 5 строк по ≤ ~120 символов, карточка — описание ≤ 1000 + поля, запас большой. Лимиты отправки: ≈ 1 сообщение/с в один чат, ≈ 30/с в целом [CITED: core.telegram.org/bots/faq].

### Pattern 7: экраны и форматирование
- **Формат текста:** `parse_mode = HTML`, **каждое** динамическое значение (название, город, адрес, описание, ввод пользователя) — через `html.EscapeString` (даёт числовые сущности `&#34;`/`&#39;`, Telegram их принимает [ASSUMED: числовые сущности поддерживаются по документации форматирования]). Ссылки `<a href="…">` только для URL, прошедших проверку (`url.Parse`, схема `http`/`https`, непустой хост, без пробелов/управляющих), с `html.EscapeString` значения. Предпросмотр ссылок отключать: `LinkPreviewOptions{IsDisabled: tgbot.True()}`. Админские сообщения (алерты, `/runs`) — **без** parse_mode (в причинах прогона могут быть `<`, `>`).
- **Кнопка регистрации:** `InlineKeyboardButton{URL: …}` только для проверенного `registration_url`; невалидный URL в кнопке даёт 400 на **всё** сообщение (карточка не покажется) — поэтому проверка обязательна; если у события ссылки нет, кнопки нет, в тексте — источники. В данных Марафонца `registration_url` есть у 105 из 133 событий (внешняя ссылка), у остальных регистрация — страница события на marathonec.ru (источник). Поведение при 400 на кнопке: повторить отправку без URL-кнопок.
- **Список (D-11):** заголовок `Найдено N · страница i/M`, затем 5 строк `17.10 · Москва · Название · 5/10/21 км`, статус — префиксом названия `[ОТМЕНЁН]`/`[ПЕРЕНЕСЁН]` (значения статуса `scheduled|cancelled|postponed`), название обрезать до ~60 рун. Клавиатура: ряд кнопок `1…5` (`e:<id>:<page>:v<N>`), ряд `← стр. i/M →` + `Фильтры` (`f:s`); `←`/`→` только если страница существует.
- **Дистанции:** метры → км: целое число км без дробной части (`21000` → `21`), иначе одна цифра после запятой (`21097` → `21,1`); в списке `5/10/21 км`, в карточке через « / ». `event_distances.label` («Полумарафон») можно показывать в карточке.
- **Карточка (BOT-05):** название (жирным, с пометкой статуса), даты (`start_date`, при `end_date` — диапазон), время **только если `start_time` не пуст** («в 10:00 по местному времени»), `Город, адрес` (адрес — если есть), тип, дистанции, описание (уже ≤ 1000 символов по CHECK `char_length(description) <= 1000`), блок «Источники: <a>Марафонец</a>». Кнопки: `Регистрация` (URL), `К списку` (`p:<page>:v<N>`).
- **Сводка (D-10):** `Дата: 30 дней · Город: вся Россия · Дистанция: любая · Тип: любой`; кнопки `[Дата] [Город] [Дистанция] [Тип]`, `[Показать N]` (`p:1:v<N>`), `[Сбросить]` (`f:x`). N считается тем же `CountEvents`.
- **Подменю корзин** остаётся на экране после переключения (кнопки с отметкой «✓» у выбранных), «Назад» — `f:s`. Подписи месяцев — русские названия (`time.Month` даёт английские).
- **Команды в меню бота:** необязательно; `setMyCommands` для `/start` (общая область) можно вызвать при старте, `/runs` в общее меню не выносить (скрытая админ-команда). На усмотрение планировщика.

### Часовой пояс (отложенный вопрос Phase 3) — решение: не нужен в Phase 4
Проверено по `prepared.golden.json` (133 события): `start_time` — 0 непустых, `address` — 0, `description` — 0, `timezone` — пусто у 118 и `Europe/Moscow` у 15. `Event.StartTime` — «локальное HH:MM», `Event.Timezone` — необязательная IANA-зона; конвертации не требуется, потому что бот показывает локальное время события «как есть». Работа «субъект → IANA» (STATE.md Pending Todos) остаётся на Phase 5+, когда появятся источники со временем. Единственный пояс в Phase 4 — `Europe/Moscow` для вычисления «сегодня» и подписей времени в админских сообщениях.

### Pattern 8: алерты о прогонах (MON-05, D-13, D-14) и миграция `00003`
**Что уже есть в БД** [VERIFIED: `migrations/00001_init_schema.sql:13`, `migrations/00002_ingest_runs_quality.sql`, `internal/store/runs.go`]: `ingest_runs.status TEXT NOT NULL CHECK (status IN ('running', 'ok', 'suspect', 'failed'))`; колонки `error`, `parsed`, `rejected`, `out_of_scope`, `upserted`, `metrics JSONB`, `snapshot JSONB`, `accepted_at`; `RunInterrupted = "прерван: остановка процесса"` (failed, не сбой источника — в `SourceHealth.ConsecutiveFailures` не считается). Ключи `metrics` (из `Stats.Metrics()`): `valid`, `expired`, `date_out_of_range`, `out_of_scope`, `unrecognized_region`, `multi_region`, `dropped_distances`, `empty{city,region,distances}`; у `failed` `metrics = {}`.

**Схема «уже оповещено»** — одна строка на источник, существует только пока источник в плохом состоянии:
```sql
-- +goose Up
CREATE TABLE source_alerts (
    source_id   BIGINT PRIMARY KEY REFERENCES sources(id) ON DELETE RESTRICT,
    state       TEXT NOT NULL CHECK (state IN ('suspect', 'failed')),
    run_id      BIGINT NOT NULL REFERENCES ingest_runs(id) ON DELETE RESTRICT,
    notified_at TIMESTAMPTZ NOT NULL DEFAULT now(),  -- когда состояние объявлено впервые
    reminded_at TIMESTAMPTZ NOT NULL DEFAULT now()   -- последнее отправленное сообщение (первое или напоминание)
);
-- +goose Down
DROP TABLE source_alerts;
```
Проверена на PostgreSQL 17 вместе с запросами ниже. Миграция аддитивная — откат образа на предыдущий тег безопасен. В `testStore` (`internal/store/store_test.go`) добавить `source_alerts` в список `TRUNCATE` (сейчас каскад через `sources` тоже опустошит её, но список должен быть явным).

**Запрос «последний завершённый прогон по каждому источнику»** (без `running`; проверен):
```sql
SELECT DISTINCT ON (r.source_id) r.source_id, s.code, r.id, r.status, r.finished_at, COALESCE(r.error,''),
       r.parsed, r.rejected, r.out_of_scope, COALESCE((r.metrics->>'unrecognized_region')::int, 0),
       (r.snapshot IS NOT NULL)
FROM ingest_runs r JOIN sources s ON s.id = r.source_id
WHERE r.status <> 'running' AND r.finished_at IS NOT NULL
ORDER BY r.source_id, r.id DESC
```
**Чистая функция решения** (табличный тест без сети и БД):
```go
// decide: run — последний завершённый прогон источника, st — строка source_alerts (nil — нет).
func decide(run Run, st *State, now time.Time) Action {
    switch {
    case run.Status == "ok":                                    if st != nil { return Clear }; return None
    case run.Status == "failed" && run.Error == store.RunInterrupted: return None   // остановка процесса — не сбой источника
    case st == nil, st.State != run.Status:                     return Send          // смена состояния
    case now.Sub(st.RemindedAt) >= 24*time.Hour:                return Remind        // напоминание раз в 24 ч
    }
    return None
}
```
- **Порядок «отправить, потом записать»:** `reminded_at`/`state` пишутся только после успешной отправки хотя бы одному админу; если Telegram недоступен (CONTEXT: «Telegram API недоступен → только лог и ретраи») — запись не меняется, следующий тик через минуту повторит. Ответ 429 при отправке — подождать `retry_after` (до 30 с) и повторить в этом же тике.
- **Первый запуск на пустой БД:** первый прогон Марафонца — `suspect` («первый прогон источника — требуется принятие», `Evaluate`, D-05) → алерт с кнопкой; это и есть шаг D-07. Так как решение по **состоянию**, а не по событию, бот не теряет алерт, даже если прогон закончился, пока бот был выключен.
- **Содержимое (D-14), обычный текст без parse_mode:** `Источник marathonec: suspect (прогон 7)`, причины (`ingest_runs.error`, ≤ 1000 символов), `parsed 133 · rejected 0 · out_of_scope 0 · unrecognized_region 0`; для `failed` — причина и число сбоев подряд (`SourceHealth`). Отдельного «всплеска ошибок парсера» нет — он выражен статусом (D-14).
- Кнопка `Принять как базу` (`r:a:<runID>`) — только у `suspect` **со снимком** (`snapshot IS NOT NULL`), только у сообщения для админа.

### Pattern 9: админ-функции — `/runs`, принятие прогона (D-15)
- **Доступ:** `ADMIN_TG_IDS` — список `int64` через запятую; парсится при старте (`strconv.ParseInt`), ошибка называет переменную, не значение; пусто → алерты и `/runs` отключены (WARN при старте). Проверка `From.ID ∈ admins` — в **каждом** админском обработчике, включая callback `r:*` (не полагаться на то, что кнопка была показана только админу). Для не-админа `/runs` не отвечает вовсе (не раскрывать существование команды) — уходит в default-handler.
- **`/runs`:** последние 3 прогона на источник (по `id` убыв.), обычный текст, время в `Europe/Moscow`:
```sql
SELECT * FROM (
  SELECT s.code, r.id, r.status, r.started_at, r.finished_at, r.parsed, r.rejected, r.out_of_scope, r.upserted,
         COALESCE(r.error,'') AS err, row_number() OVER (PARTITION BY r.source_id ORDER BY r.id DESC) AS rn
  FROM ingest_runs r JOIN sources s ON s.id = r.source_id) t
WHERE rn <= 3 ORDER BY code, id DESC
```
  Строка: `#5 suspect 29.09 12:03 · parsed 133 · rej 0 · oos 0 · причина…` (проверено на тестовых данных).
- **Принятие из бота (D-15):** `r:a:<id>` → правка сообщения в подтверждение «Принять прогон 7 (marathonec) как базу? … [Да, принять] [Отмена]» → `r:y:<id>` → `RunSource(runID) → code` (новый `store`-метод: `SELECT s.code FROM ingest_runs r JOIN sources s ON s.id = r.source_id WHERE r.id = $1`) → поиск `ingest.Source` в реестре → `runner.Accept(ctx, src, runID)` → правка сообщения «Принято: создано N, без изменений M», кнопок нет. Ошибки: `errors.Is(err, store.ErrNotAcceptable)` → «Уже нельзя принять: прогон не последний, не suspect или принят»; `store.ErrRunInProgress` → «Идёт прогон, повторите позже»; прочее → общее сообщение + подробности в лог.
- **Как бот строит `Runner`:** `Runner.Accept` использует только `Store`, `Now`, `Log`, `GoneAfterRuns`, `GoneAfter`, `src.Code()` и `src.Policy()` (`Prepare` по снимку) — `Fetch` не вызывается [VERIFIED: `internal/ingest/runner.go:238-299`]. Реестр источников сейчас — литерал в `runWorker` (`sources := []ingest.Source{marathonec.New(fetcher)}`, `cmd/aggregator/main.go:184`). Вынести в `newSources(f *ingest.Fetcher) []ingest.Source`; бот вызывает `newSources(nil)` — никакого сетевого клиента и `OWNER_CONTACT` не требуется. **Ловушка:** `Accept` → `write` → `observe` берёт `GoneAfterRuns`/`GoneAfter` из полей `Runner` (нули → 3 и 72 ч); если у worker они переопределены `OBSERVE_GONE_RUNS`/`OBSERVE_GONE_AFTER`, а бот собирает `Runner` без них, принятие наблюдает иначе, чем worker. Поэтому бот вызывает `ingest.LoadConfig(false)` (без `OWNER_CONTACT`) и копирует оба поля; оба контейнера читают один `.env`.
- Гонка «worker начал прогон, пока админ подтверждает» решена в `Accept` (ClaimRun в транзакции, ME-01 Phase 3) — бот ничего дополнительного не делает.

### Pattern 10: Docker и Compose (PRD-01) — собрано и запущено локально
**Dockerfile** (образ собран на Mac arm64 с `--platform linux/amd64` за 89 с холодно, бинарник статический; `docker run` — печатает usage, `healthcheck` без `DATABASE_URL` — ожидаемая ошибка) [VERIFIED: локальная сборка и запуск]:
```dockerfile
# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/aggregator ./cmd/aggregator \
 && mkdir -p /out/data/raw

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/aggregator /aggregator
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
WORKDIR /data
ENTRYPOINT ["/aggregator"]
```
- **Узкий `COPY`, а не `COPY . .`:** в корне лежит `.env` с секретами (права 600), в контексте сборки он бы попал в слой образа, а образ уезжает на VPS. Дополнительно нужен `.dockerignore`: `.env`, `.env.*` (кроме `.env.example`), `.git`, `.planning`, `.idea`, `data`, `MVP.md`, `*.md`.
- Миграции встроены (`//go:embed *.sql`, `migrations/embed.go`) — отдельный `COPY` SQL в образ не нужен; каталог `migrations` нужен только для сборки.
- `--provenance=false --sbom=false` при сборке (иначе BuildKit добавляет манифест-аттестацию, и в `docker save` попадает индекс из нескольких манифестов). Локально `docker save` даёт архив в формате OCI (`blobs/sha256/…`, 5,4 МБ; образ 24 МБ); что `docker load` на docker.io 29.1.3 читает такой архив — [ASSUMED] (не проверялось, VPS недоступен), проверяется в PREFLIGHT/VERIFY скрипта деплоя.
- **Именованный том и права:** том `raw:/data/raw` при первом монтировании получает владельца `65532:65532` от каталога образа (`drwxr-xr-x 65532 65532`) [VERIFIED: `docker run -v vol:/data/raw …` + `ls -ldn`]. RAW_DIR = `/data/raw` (`RAW_DIR` уже читается `ingest.LoadConfig`, временные файлы `RawStore` создаёт внутри `RAW_DIR` — `internal/ingest/rawstore.go: os.CreateTemp(filepath.Dir(path), ".tmp-*")`, поэтому read-only корневая ФС не мешает).
- **Часовой пояс контейнеров:** оставить UTC (логи `slog` — RFC3339 UTC); «сегодня» бот считает сам по `Europe/Moscow`.

**docker-compose.yml** (production; имя проекта `aggregator`, не пересекается с `aggregator-dev`/`aggregator-test`). Проверено `docker compose config -q`, затем `up -d bot`: цепочка postgres (healthy) → migrate (exit 0, применил 00001, 00002) → bot (healthy через exec-healthcheck `["CMD","/aggregator","healthcheck"]` в distroless без оболочки); повторный `up -d` снова прогоняет `migrate` (идемпотентно); `docker port` postgres пуст (порт не опубликован). Проверка также с `read_only: true`, `cap_drop: [ALL]`, `security_opt: ["no-new-privileges:true"]` — бот и migrate работают [VERIFIED: локально, Compose v5.1.2; на VPS будет 2.40.3 — использованы только давние возможности `depends_on.condition`, `env_file`, `logging`, `healthcheck`, YAML-якоря — [ASSUMED] совместимость с 2.40.3, снимается `docker compose config -q` в PREFLIGHT]:
```yaml
name: aggregator

x-app: &app
  image: aggregator:${IMAGE_TAG:?задайте IMAGE_TAG в .env}
  env_file: .env
  restart: unless-stopped
  read_only: true
  cap_drop: [ALL]
  security_opt: ["no-new-privileges:true"]
  logging: &log
    driver: json-file
    options: { max-size: "10m", max-file: "3" }
  depends_on:
    migrate: { condition: service_completed_successfully }

services:
  postgres:
    image: postgres:17
    restart: unless-stopped
    environment:
      POSTGRES_USER: aggregator
      POSTGRES_DB: aggregator
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?задайте POSTGRES_PASSWORD в .env}
    volumes: [pgdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -h 127.0.0.1 -U aggregator -d aggregator"]
      interval: 5s
      timeout: 3s
      retries: 12
      start_period: 10s
    logging: *log            # ports: нет — наружу не публикуется (MVP §10.3: порты Docker обходят ufw)

  migrate:
    image: aggregator:${IMAGE_TAG:?}
    command: ["migrate"]
    env_file: .env
    restart: "no"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    depends_on:
      postgres: { condition: service_healthy }
    logging: *log

  bot:
    <<: *app
    command: ["bot"]
    healthcheck:
      test: ["CMD", "/aggregator", "healthcheck"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 20s

  worker:
    <<: *app
    command: ["worker"]
    stop_grace_period: 30s
    volumes: [raw:/data/raw]
    environment:
      RAW_DIR: /data/raw

volumes:
  pgdata:
  raw:
```
- **`.env` на VPS** (`/opt/aggregator/.env`, права 600, владелец root) — единственный файл и для интерполяции Compose, и для `env_file`. Имена (значения не в репозитории): `POSTGRES_PASSWORD`, `PGPASSWORD` (то же значение — pgx берёт пароль из окружения, как в dev: `DATABASE_URL` без пароля [VERIFIED: `.env.example`, проверено локально: migrate подключился так], `DATABASE_URL=postgres://aggregator@postgres:5432/aggregator?sslmode=disable` (внутренняя сеть Compose, TLS у postgres нет), `TELEGRAM_BOT_TOKEN`, `ADMIN_TG_IDS`, `OWNER_CONTACT` (нужен worker), `LOG_FORMAT`, `IMAGE_TAG`. Добавить эти имена в `.env.example`.
- **Пароль Postgres действует только при первой инициализации тома** (`POSTGRES_PASSWORD` игнорируется при существующем каталоге данных): смена в `.env` без `ALTER ROLE` приведёт к отказу аутентификации бота.
- **Ротация логов** задана на каждом сервисе (`json-file`, 10m × 3), демон `daemon.json` не трогаем.
- **`docker compose down -v` удаляет том БД** — в скриптах и памятках `-v` не использовать; откат образа — сменой `IMAGE_TAG`.
- Postgres-образ (`postgres:17`) VPS подтянет сам (`docker compose pull postgres`, нужен исходящий доступ к Docker Hub — [ASSUMED] из Варшавы доступен; запасной путь: на Mac `docker pull --platform linux/amd64 postgres:17`, затем `docker save | ssh … docker load`; **не** без `--platform`: Mac по умолчанию возьмёт arm64).
- Одновременно с VPS **не запускать бот с тем же токеном локально**: два `getUpdates` дают `ErrorConflict` (409) у обоих (D-02: один бот на всё).

### Pattern 11: скрипты для VPS (D-04, D-06) — дизайн PREFLIGHT → APPLY → VERIFY
Скрипты лежат в репозитории (`deploy/vps/`), не содержат секретов, IP и хостнеймов; выполняются владельцем от root. Образец Phase 1 (скрипты присылались владельцу, без больших heredoc) сохранён: файл копируется `scp`, запускается **однострочником с проверкой полного SHA256**:
```bash
# владелец на VPS, каталог со скриптами; HASH — полный sha256, напечатанный на Mac командой deploy/oneliner.sh <файл>
if printf '%s  %s\n' "<64 hex>" host-apply.sh | sha256sum -c --quiet -; then bash host-apply.sh; else echo 'STOP: HASH MISMATCH'; fi
```
Логика проверена на Mac (`shasum -a 256 -c --quiet -`): при совпадении выполняется скрипт, при изменении файла печатается `STOP: HASH MISMATCH`; на Ubuntu `sha256sum -c` принимает тот же формат «hash  файл». `if/else`, а не `&&/||`, чтобы сбой самого скрипта не выдавался за несовпадение хеша. Хеши в документации устаревают при любой правке — их генерирует `deploy/oneliner.sh`.

| Шаг | Скрипт | Что делает | Меняет хост |
|-----|--------|-----------|-------------|
| A0 PREFLIGHT | `host-preflight.sh` | Только чтение: ОС (Ubuntu 24.04), `/var/run/reboot-required`, версия ядра (работающее и установленное), `dpkg -l docker.io docker-ce containerd*`, `apt-cache policy docker-compose-v2` (universe доступен), `docker ps -a` с политиками перезапуска (**что затронет перезагрузка** — чужие контейнеры владельца, `tinyproxy`), `systemctl is-enabled docker tinyproxy`, свободное место, состояние часов (`timedatectl`), наличие `/opt/aggregator`, свободные порты не нужны (порты не публикуются) | нет |
| A1 APPLY | `host-apply.sh` | Запрос подтверждения (`YES`); `apt-get update`; **только security-обновления** `unattended-upgrade -v` (пакет уже установлен и включён, по умолчанию берёт `-security`, D-04) [ASSUMED: разрешённые origins по умолчанию — security]; `apt-get install -y --no-install-recommends docker-compose-v2`; перезагрузку **не делает** — печатает «перезагрузите сами: `systemctl reboot`» после проверки вывода A0 | да |
| A2 | владелец | `systemctl reboot` (SSH оборвётся; `docker` и `tinyproxy` включены в автозагрузку — `enabled` в аудите) | да |
| A3 VERIFY | `host-verify.sh` | Только чтение: reboot-required отсутствует, `uname -r` = новейшее установленное, `docker compose version` ≥ 2.20, `systemctl is-active docker`, `tinyproxy` в прежнем состоянии, `apt list --upgradable` (сколько осталось) | нет |
| B0 PREFLIGHT | `deploy-preflight.sh` | Только чтение: `docker compose version`, есть ли `/opt/aggregator`, загружен ли образ `aggregator:<TAG>` (`docker image inspect`), `.env` (**только** права/наличие ключей по именам, значения не печатаются), `docker compose config -q` | нет |
| B1 APPLY | `deploy-apply.sh` | Каталог `/opt/aggregator` (`install -d -m 750`); копирует `docker-compose.yml`; **создаёт `.env` интерактивно**, если его нет (`umask 077`, `read -rs` для токена, `openssl rand -hex 24` для пароля БД, запрос `ADMIN_TG_IDS`, `OWNER_CONTACT`) — значения не попадают ни в аргументы, ни в вывод, ни в файл скрипта; записывает `IMAGE_TAG` (предыдущий сохраняет как `IMAGE_PREV`); `docker compose pull postgres`; `docker compose up -d postgres` → ждёт `healthy` → `docker compose up -d` (migrate → bot, worker) | да |
| B2 VERIFY | `deploy-verify.sh` | `docker compose ps` (postgres/bot healthy, migrate exit 0, worker running), `docker port` postgres пуст, `ss -ltn` не показывает 5432, права `.env` = 600, последние строки логов bot/worker (приложение токен не пишет; `docker inspect` контейнера **не** использовать — печатает окружение, для образа достаточно `docker image inspect --format '{{.Id}}'`) |
| B3 | `rollback.sh` | Меняет `IMAGE_TAG` ↔ `IMAGE_PREV` в `.env`, `docker compose up -d bot worker`, никогда `down -v`; откат безопасен, пока миграции аддитивные (00003 — да) | да |

Доставка образа (с Mac, выполняет владелец; `ship.sh`): `docker build --platform linux/amd64 --provenance=false --sbom=false -t aggregator:$TAG .` → `docker save aggregator:$TAG | gzip | ssh <алиас> 'gunzip | docker load'` → `scp docker-compose.yml deploy/vps/*.sh <алиас>:/root/agg-deploy/`. В PREFLIGHT B0 печатать `docker image ls --format`, чтобы убедиться, что образ загружен и архитектуры совпали (`Architecture=amd64`).

**Первое включение (D-07) — порядок для владельца:** B1 → бот и worker стартуют → worker сразу (нет прогонов) делает первый прогон Марафонца (один GET, Crawl-delay 40 с) → прогон `suspect` («первый прогон источника») → в течение ~минуты бот шлёт админам алерт с `[Принять как базу]` → подтверждение → `Runner.Accept` создаёт 133 события → `/start` в Telegram показывает реальную выдачу. CLI-запасной путь: `docker compose run --rm worker worker accept-baseline --source marathonec` (первое `worker` — имя сервиса, дальше аргументы целиком заменяют `command` сервиса, поэтому подкоманда `worker` повторяется; `OWNER_CONTACT` для `accept-baseline` не нужен — `ingest.LoadConfig(cmd == "" || cmd == "run-once")`, `cmd/aggregator/main.go:178`).
**Безопасность хоста в этой фазе:** `ufw`, swap, синхронизация часов, SSH-политика, `tinyproxy` не трогаются (D-04, Phase 6); PREFLIGHT лишь фиксирует состояние часов (TLS к api.telegram.org требует, чтобы часы не «уплыли»; при `System clock synchronized: no` это записывается в отчёт, не чинится). Резервных копий БД до Phase 6 нет — потеря тома означает повторный сбор каталога (MVP §11.3: «каталог восстанавливается сбором»); невосстановимы только `users.filters` (низкая ценность).

### Тестирование без сети (замена Nyquist-разделу: `workflow.nyquist_validation = false`)
| Что | Как | Команда |
|-----|-----|---------|
| Кодек callback, длины ≤ 64 байт, строгий разбор | табличный тест + перебор всех экранов на реальных `city_key`, `id` до `MaxInt64` | `go test ./internal/bot -run Callback` |
| Пресеты дат, корзины, форматирование км | чистые функции, фиксированное `today` | `go test ./internal/bot -run 'Date|Bucket|Format'` |
| Экраны (текст + клавиатура) | golden-строки; проверка экранирования HTML в названии с `<b>&"'` | `go test ./internal/bot -run Screen` |
| SQL выдачи, города, типы, источники, `UpdateFilters`, `source_alerts` | интеграционные тесты `internal/store` на PostgreSQL 17 (`TEST_DATABASE_URL`, база `…_test`) с тем же набором данных, что в этой сессии (133 события из `prepared.golden.json` можно грузить через `SaveEvent`) | `TEST_DATABASE_URL=postgres://aggregator@127.0.0.1:5433/aggregator_test?sslmode=disable go test -p 1 ./internal/store` |
| Обработчики и алерты против «Telegram» | `httptest`-сервер как Bot API: `tgbot.New("123:TEST", tgbot.WithServerURL(srv.URL), tgbot.WithSkipGetMe(), tgbot.WithNotAsyncHandlers())`, апдейты подаются `b.ProcessUpdate(ctx, &models.Update{…})` синхронно; сервер разбирает **multipart** (`r.ParseMultipartForm`, поля `chat_id`, `message_id`, `text`, `parse_mode`, `reply_markup` — JSON-строка) и умеет отвечать 400/403/429 в формате Bot API; путь запроса — `/bot123:TEST/<метод>` [VERIFIED: probe] | `go test -p 1 ./internal/bot ./internal/alert` |
| `decide` (алерты) | таблица: ok/suspect/failed × нет строки/то же/другое состояние × <24 ч/≥24 ч, прерванный прогон | `go test ./internal/alert` |
| Конфиг (`ADMIN_TG_IDS`, токен) | ошибка не содержит значения | `go test ./internal/bot -run Config` |
| Dockerfile и Compose | `docker build --platform linux/amd64 …` без ошибок; `docker compose config -q` с временным env-файлом; smoke `up -d postgres migrate bot` в одноразовом проекте | вручную/скриптом перед поставкой |
| Скрипты VPS | `bash -n`, `shellcheck` (если есть), тест логики хеш-однострочника; **выполнение на VPS — только владелец**, PREFLIGHT read-only | — |

Полный прогон Go-тестов проекта: `TEST_DATABASE_URL=… go test -p 1 -count=1 ./...` (без переменной интеграционные тесты пропускаются).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Клиент Bot API, long polling, смещение апдейтов, backoff при 429 | свой HTTP-цикл `getUpdates` | `go-telegram/bot` `b.Start(ctx)` | Уже решено и одобрено (D-16); но исходящие ретраи 429 и recover — наши |
| Экранирование HTML для Telegram | свой replace | `html.EscapeString` | Полный набор символов; числовые сущности Telegram принимает |
| Проверка URL для кнопки | регулярное выражение | `net/url.Parse` + схема/хост | Невалидный URL роняет всё сообщение (400) |
| Транслитерация города, `city_key`, синонимы | новый словарь в боте | `normalize.CityKey` | Единственный источник истины, тот же ключ лежит в `events.city_key` |
| Принятие прогона | своя запись в `ingest_runs` | `ingest.Runner.Accept` | Атомарность, ClaimRun, окно базы (ME-01) |
| Схема «уже оповещено» на файлах/в памяти | map в процессе | таблица `source_alerts` | Перезапуск бота не должен повторять алерты (D-13) |
| Пагинация, подсчёт страниц, зажим страницы | — | `LIMIT/OFFSET` + `count(*)` и чистая функция `pages(n, size)` | MVP §8.3 |
| Планировщик тикера алертов | cron-библиотека | `time.Ticker` + `select` на `ctx.Done()` | MVP §3.1: без библиотек |
| Проверка целостности скрипта на VPS | свой формат подписи | `sha256sum -c` | Уже принятый образец Phase 1 |
| Часовой пояс | `FixedZone` вручную | `time.LoadLocation("Europe/Moscow")` + `import _ "time/tzdata"` | Не зависит от базового образа |

**Key insight:** вся нетривиальная логика — в местах, где данные пересекают границы (Telegram ↔ БД, callback ↔ состояние, прогон ↔ алерт); чем больше её вынесено в чистые функции (`Parse`, `decide`, `render`, `Resolve`), тем меньше тестов нужно против реальных сервисов.

## Common Pitfalls

### Pitfall 1: паника в обработчике завершает процесс
**What goes wrong:** библиотека запускает обработчик `go r(ctx,b,upd)` без `recover`; одна неожиданная `nil`-ссылка (например, `CallbackQuery.Message.Message == nil` у старого сообщения) — бот падает, `restart` его поднимает, а тот же апдейт больше не приходит (смещение сдвинуто).
**How to avoid:** `recoverMW` первым в `WithMiddlewares` (Pattern 1); хелперы `msgRef`, `fromID` не разыменовывают указатели без проверки.
**Warning signs:** в логах контейнера «goroutine … panic», рост `RestartCount`.

### Pitfall 2: default-handler библиотеки пишет апдейты в лог
**What goes wrong:** без `WithDefaultHandler` каждый неизвестный апдейт печатается целиком (`%+v`: текст, имя, username) — нарушение BOT-08 и §12.2 MVP.
**How to avoid:** свой default-handler; не включать `WithDebug`; в логах только тип апдейта, префикс callback, код ошибки. Тест: сообщение с именем/текстом не появляется в перехваченном `slog`-выводе.

### Pitfall 3: `nil`-срез в pgx = NULL, выдача пустеет
См. Pattern 4. `COALESCE(cardinality(…),0)` в SQL **и** `[]int32{}` в Go; тест «без корзин → 133 события».

### Pitfall 4: `callback_data` длиннее 64 байт
`f:c:<city_key>` при ключе > 60 байт. Кодек возвращает ошибку, кнопка пропускается; тест-свойство на длины (Pattern 2).

### Pitfall 5: невалидный URL в inline-кнопке роняет сообщение целиком
Регистрационные ссылки приходят с чужих сайтов. Проверка `net/url` до построения клавиатуры; при 400 — повтор без URL-кнопок (Pattern 7). Пробел или управляющий символ в ссылке — частый случай.

### Pitfall 6: `answerCallbackQuery` дважды или ни разу
Нет ответа — «часики» у пользователя; второй ответ — ошибка. Ответ один, в `defer`, с текстом только в stale-ветках (≤ 200 символов).

### Pitfall 7: два экземпляра бота с одним токеном
`getUpdates` возвращает 409 Conflict обоим. Локальный `go run … bot` при работающем VPS запрещён (D-02); для локальной отладки — только фиктивный Bot API из тестов. Признак: `ErrorConflict` в логе ERROR.

### Pitfall 8: секреты в образе и в логах
`COPY . .` кладёт `.env` в образ. Только узкий `COPY`, `.dockerignore`; в distroless нет оболочки, поэтому содержимое образа проверять через `docker save | tar -t` — файлов `.env` быть не должно. Токен встречается в URL запросов: библиотека маскирует его как `***` в `*url.Error` и debug-логе, но **свои** логи не должны печатать URL/`err` внешних HTTP-вызовов без просмотра.

### Pitfall 9: архитектура образов
Mac — arm64, VPS — amd64. Образ приложения — `--platform linux/amd64`; запасной перенос `postgres:17` — `docker pull --platform linux/amd64`. Проверка в PREFLIGHT B0: `docker image inspect --format '{{.Architecture}}'` = `amd64`.

### Pitfall 10: пароль Postgres и `down -v`
Пароль задаётся один раз при инициализации тома; `docker compose down -v` уничтожает БД (бэкапов до Phase 6 нет). В скриптах `-v` не встречается; откат — `IMAGE_TAG`.

### Pitfall 11: бот принимает прогон иначе, чем worker
`GoneAfterRuns`/`GoneAfter` не скопированы из `ingest.LoadConfig(false)` в `Runner` бота (Pattern 9). Тест: бот принимает снимок при `OBSERVE_GONE_RUNS=5` → `Runner` получил 5.

### Pitfall 12: «сегодня» по UTC и сдвиг дат
Между 00:00 и 03:00 по Москве UTC-дата на сутки отстаёт — события «сегодня» пропали бы из выдачи. «Сегодня» — по `Europe/Moscow`; `events.start_date` (полночь UTC) форматировать как календарную дату без `.In(loc)`.

### Pitfall 13: гонка быстрых нажатий
Две одновременные горутины читают старые фильтры и затирают друг друга. `UpdateFilters` — `FOR UPDATE` в транзакции; значения абсолютные (повтор безопасен); `filters_v` растёт только при изменении.

### Pitfall 14: алерты — шум и потерянные алерты
Прерванные остановкой прогоны (`RunInterrupted`) не сбой источника — не алертить (иначе каждый деплой даёт `failed`). Состояние `source_alerts` пишется **после** успешной отправки. Ожидаемое: первый прогон на пустой БД — `suspect` (D-05/D-07), это не ошибка.

### Pitfall 15: перезагрузка VPS задевает чужие сервисы
Хост используется не только проектом (`tinyproxy` включён, контейнеры владельца неизвестны — `docker ps` от `runagg` недоступен). PREFLIGHT A0 обязан показать `docker ps -a` с политикой перезапуска и `systemctl is-enabled` ключевых сервисов до APPLY; `host-apply.sh` не перезагружает.

### Pitfall 16: `docker compose run` заменяет `command`
`docker compose run --rm worker accept-baseline …` запустил бы `/aggregator accept-baseline` (usage, код 2). Правильно: `run --rm worker worker accept-baseline --source marathonec`.

### Pitfall 17: тестовая БД и `TRUNCATE`
`testStore` очищает перечисленные таблицы; новую `source_alerts` добавить в список. Интеграционные тесты только на базе `…_test` (защита `current_database()`), пакеты — `-p 1`.

## Code Examples

### Фиктивный Bot API для тестов обработчиков (проверено запуском)
```go
// Источник: исходники go-telegram/bot v1.27.0 (rawRequest, ProcessUpdate) + probe в scratchpad
type call struct{ Method string; Form map[string]string }

func fakeTelegram(t *testing.T, script func(method string) (status int, body string)) (*httptest.Server, *[]call) {
    var mu sync.Mutex
    calls := &[]call{}
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _ = r.ParseMultipartForm(1 << 20) // библиотека шлёт multipart, не JSON
        method := path.Base(r.URL.Path)  // /bot123:TEST/editMessageText
        form := map[string]string{}
        if r.MultipartForm != nil { for k, v := range r.MultipartForm.Value { form[k] = v[0] } }
        mu.Lock(); *calls = append(*calls, call{method, form}); mu.Unlock()
        status, body := script(method)
        w.WriteHeader(status); _, _ = w.Write([]byte(body))
    }))
    t.Cleanup(srv.Close)
    return srv, calls
}
// 400: {"ok":false,"error_code":400,"description":"Bad Request: message is not modified: …"}
// 403: {"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}
// 429: {"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}
// 200: {"ok":true,"result":{"message_id":5,"date":1,"chat":{"id":1,"type":"private"}}}
b, _ := tgbot.New("123:TEST", tgbot.WithServerURL(srv.URL), tgbot.WithSkipGetMe(), tgbot.WithNotAsyncHandlers())
b.ProcessUpdate(ctx, &models.Update{ID: 1, CallbackQuery: &models.CallbackQuery{ID: "cq", From: models.User{ID: 42}, Data: "p:1:v1",
    Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage,
        Message: &models.Message{ID: 7, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}}}})
```

### Классификация ошибок (проверено на фиктивном сервере)
```go
// Источник: errors.go / raw_request.go v1.27.0; ErrorForbidden/BadRequest — sentinel'ы, 429 — *TooManyRequestsError
switch {
case errors.Is(err, tgbot.ErrorForbidden):                     return kindBlocked
case errors.Is(err, tgbot.ErrorBadRequest) && strings.Contains(err.Error(), "message is not modified"): return kindNotModified
case errors.Is(err, tgbot.ErrorBadRequest):                    return kindBadRequest // fallback: новое сообщение / игнор
}
var tm *tgbot.TooManyRequestsError
if errors.As(err, &tm) { return kindRateLimited /* tm.RetryAfter секунд */ }
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `docker-compose` v1 (Python) | плагин `docker compose` v2 | 2023 (v1 EOL) | На VPS `docker-compose-v2` 2.40.3; команда `docker compose` |
| `docker.io` + плагин из Docker Inc. | `docker.io` + `docker-compose-v2` из одного репозитория Ubuntu | — | Не смешивать репозитории (MVP §10.3) |
| go-telegram/bot < 1.26 | ≥ 1.26: неизвестные варианты полиморфных типов не «залипают» `getUpdates`; ≥ 1.27: повтор POST после GOAWAY HTTP/2 [CITED: `CHANGELOG.md` модуля v1.27.0] | 2026-09-11 | Фиксировать **не ниже v1.27.0**: без неё новый тип апдейта или дренаж HTTP/2-соединения приводили к зависанию/ошибкам всех `sendMessage`/`editMessageText` |
| `Message` в callback всегда доступно | `MaybeInaccessibleMessage` (старое сообщение → только `chat`/`message_id`) | Bot API 7.0 | `msgRef` |

**Deprecated/outdated:** `models.Update.Message` для callback не использовать — данные в `CallbackQuery.Message`.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `docker load` на docker.io 29.1.3 читает OCI-архив, созданный Docker 29.4 на Mac | Pattern 10 | Перенос образа не сработает; запасной путь — `docker buildx build --output type=docker` или сборка `docker save` без containerd-хранилища. Проверяется в B0/B1 на VPS |
| A2 | С VPS доступен Docker Hub (`postgres:17`) | Pattern 10 | Нужен перенос `postgres:17` с Mac (`--platform linux/amd64`) |
| A3 | `unattended-upgrade -v` на этом VPS применяет только `-security` (origins по умолчанию не менялись) | Pattern 11 | Может обновить больше, чем «security»; PREFLIGHT печатает `Allowed-Origins` (`apt-config dump`) |
| A4 | Формулировки описаний Bot API: «message to edit not found», «message can't be edited», «query is too old…» | Pattern 6 | Ветки fallback не сработают; ошибка всё равно не роняет бот (лог), но пользователь может не получить экран — покрыть общей веткой «любая BadRequest при правке → новое сообщение» |
| A5 | Числовые HTML-сущности (`&#34;`, `&#39;`) принимаются parse_mode=HTML | Pattern 7 | Кавычки в названиях покажутся кодами; запасной вариант — `strings.NewReplacer("&","&amp;","<","&lt;",">","&gt;")` (кавычки вне атрибутов не требуют экранирования) |
| A6 | Compose 2.40.3 принимает файл, проверенный на v5.1.2 (используются только давние возможности) | Pattern 10 | `docker compose config -q` в PREFLIGHT B0 поймает до запуска |
| A7 | `static-debian13:nonroot` работает так же, как проверенный `static-debian12:nonroot` (бинарник статический) | Standard Stack | Вернуться на debian12 (изменение одной строки) |
| A8 | Слать ли «источник снова ok» — в требованиях нет; рекомендация: не слать | Pattern 8 | Владелец может захотеть уведомление о восстановлении (добавляется в ветку `Clear`) |
| A9 | Кэш планов pgx не даёт деградации на больших выдачах (не измерялось) | Pattern 4 | Медленные запросы при десятках тысяч событий — тогда `plan_cache_mode` или денормализация min/max (MVP §4.6) |

## Open Questions

1. **Внешние предпосылки от владельца (не блокируют код и тесты, блокируют развёртывание).**
   - Известно: D-02 — один бот, токен только в `.env` на VPS и локальном `.env`.
   - Неясно: создан ли бот в BotFather, известен ли Telegram ID владельца (`ADMIN_TG_IDS`), какой ssh-алиас и пользователь для `ship.sh`.
   - Рекомендация: план включает `checkpoint:human-action` перед B1 (значения вводит владелец в интерактивном скрипте, не в чат).
2. **Достижимость Docker Hub с VPS** (A2) — снимается шагом B1 (`docker compose pull postgres`); при отказе — запасной путь переноса.
3. **Перенос образа и `docker load`** (A1) — первый реальный перенос покажет; в плане предусмотреть проверку `docker image inspect` сразу после загрузки.
4. **Поведение `/start` у существующего пользователя:** сохранять фильтры или сбрасывать. Рекомендация: сохранять (показать сводку), сброс — кнопкой `[Сбросить]`.
5. **Подписи и показ типов `cross|track|obstacle|other`** — сейчас в данных только `road`/`trail` (D-09); достаточно подписей для двух, остальные — простая таблица с запасом.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | сборка, тесты | ✓ (Mac) | 1.27.1 darwin/arm64 | — |
| Docker (OrbStack) | образ, Compose, интеграционные тесты | ✓ (Mac) | 29.4.0 | — |
| Docker Compose (Mac) | локальная проверка compose | ✓ | v5.1.2 | — |
| PostgreSQL 17 (Docker) | интеграционные тесты, dev-БД | ✓ | контейнеры `aggregator-test`/`aggregator-dev` (`postgres:17`) запущены | — |
| `shasum`/`sha256sum`, `ssh`, `scp`, `gzip` | ship.sh, однострочники | ✓ (Mac) | системные | — |
| `gh`, `shellcheck` | необязательно | ✗ | — | Пропустить; `bash -n` для скриптов |
| VPS: Ubuntu 24.04, `docker.io` | запуск | ✓ (по аудиту 2026-09-28) | 29.1.3, containerd 2.2.1, runc 1.3.4 | — |
| VPS: `docker compose` (плагин) | PRD-01 | ✗ | — | Установить `docker-compose-v2` 2.40.3 (шаг A1) |
| VPS: `unattended-upgrades` | security-обновления | ✓ (установлен и включён, аудит) | — | `apt-get upgrade` (обновит всё, не только security) |
| VPS: исходящий доступ к api.telegram.org | бот | ✓ (MVP §10.1: «Telegram API доступен, redirect ~0,13 с») | — | — |
| VPS: исходящий доступ к Docker Hub | `postgres:17` | не проверялось | — | `docker save` с Mac |
| VPS: часы синхронизированы | TLS к Telegram | ✗ на момент аудита (`System clock synchronized: no`, `Packet count: 0`) | — | Фиксируется в PREFLIGHT; исправление — Phase 6 (PRD-02), но при большом расхождении TLS не заработает — тогда точечный шаг с разрешения владельца |
| VPS: swap | память 3,9 ГБ | ✗ | — | Не требуется для postgres+bot+worker (~660 МБ занято до проекта); Phase 6 |

**Missing dependencies with no fallback:** none (Compose-плагин устанавливается шагом A1; токен бота — от владельца).
**Missing dependencies with fallback:** Compose на VPS; Docker Hub с VPS (перенос образа с Mac).

## Security Domain

`security_enforcement` включён (ASVS L1, `security_block_on: high`).

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | частично | Пользователи аутентифицированы Telegram (`From.ID` из апдейта по TLS через long polling; входящих портов и webhook нет). Админ = член `ADMIN_TG_IDS` (аллоулист из окружения) |
| V3 Session Management | нет | Сессий нет; состояние — `users.filters` |
| V4 Access Control | да | Проверка `From.ID ∈ ADMIN_TG_IDS` в каждом админском обработчике (`/runs`, `r:*`, рассылка алертов) — не по факту показа кнопки; групповые чаты игнорируются (`privateOnlyMW`) |
| V5 Input Validation | да | Строгий парсер callback (whitelist префиксов/значений), `normalize.CityKey` для текста, ограничение длины ввода, `html.EscapeString` для вывода, `net/url` для ссылок, параметризованный SQL (`$N`, константы) |
| V6 Cryptography | нет своей | TLS — стандартный `net/http` к api.telegram.org; токен — только из окружения; ничего не изобретаем |
| V7 Error Handling and Logging | да | Логи `slog`: без токена, текста сообщений, имён/username; ошибки конфигурации без значений; свой default-handler и errors-handler |
| V8 Data Protection | да | Хранятся только `tg_id` и настройки (BOT-08); образ и логи без секретов |
| V14 Configuration | да | `.env` 600 на VPS, вне репозитория и образа; контейнеры без root, `read_only`, `cap_drop: ALL`, `no-new-privileges`; порт Postgres не публикуется; ротация логов |

### Known Threat Patterns for Go Telegram bot + Compose on VPS

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Не-админ вызывает `/runs` или принятие прогона (подделанный/пересланный callback) | Elevation of Privilege | Проверка `From.ID` на каждом апдейте; `Accept` защищён `ClaimRun`; ответ без раскрытия |
| HTML/разметка-инъекция через названия/город/описание источника или ввод пользователя | Tampering | `html.EscapeString` для всех динамических значений; админские тексты без parse_mode |
| Подмена ссылки регистрации (`javascript:`, мусор) | Tampering / Spoofing | Только `http`/`https` с хостом; иначе кнопки нет |
| Утечка токена бота через URL в ошибке/логе или образ | Information Disclosure | Маскирование библиотекой; свои логи без `err` HTTP-клиента «как есть»; узкий `COPY` + `.dockerignore`; `.env` 600 |
| Утечка Telegram-профиля пользователей в логи | Information Disclosure | Свой default-handler; не логировать тексты и имена |
| Доступ к Postgres извне | Information Disclosure / Tampering | Порт не публикуется (Docker обходит `ufw`); сеть Compose внутренняя |
| Флуд бота пользователем | Denial of Service | Запросы дешёвые (индексы, LIMIT 5); лимиты Telegram; простая пауза на пользователя — не в объёме фазы |
| Повтор/устаревший callback | Tampering | Абсолютные значения, `filters_v`, стойкость к повтору |
| Подмена скрипта на пути к VPS | Tampering | SHA256-однострочник (`STOP: HASH MISMATCH`), скрипты read-only до APPLY, подтверждение `YES` |
| Два экземпляра getUpdates (перехват апдейтов) | Denial of Service | Один бот; 409 в логах как ERROR |

## Sources

### Primary (HIGH confidence)
- Исходники `github.com/go-telegram/bot@v1.27.0` (`bot.go`, `errors.go`, `handlers.go`, `options.go`, `process_update.go`, `raw_request.go`, `get_updates.go`, `methods*.go`, `models/*.go`, `CHANGELOG.md`) — прочитаны из модульного кэша; поведение проверено пробами на `httptest`.
- `proxy.golang.org` / `sum.golang.org` — версии, `go.mod`, хеши `github.com/go-telegram/bot@v1.27.0`.
- Context7 `/go-telegram/bot` (найден; исходники прочитаны напрямую), `/docker/compose` (`depends_on.condition`, `service_completed_successfully`).
- Локальные запуски: сборка образа `linux/amd64`, `docker compose up` (postgres → migrate → bot), права тома, PostgreSQL 17 с реальными данными, pgx против nil-среза.
- Код проекта: `cmd/aggregator/main.go`, `internal/store/*.go`, `internal/ingest/*.go`, `internal/normalize/place.go`, `internal/event/event.go`, `migrations/00001`, `00002`, `docker-compose.dev.yml`, `.env.example`.
- `MVP.md` §3, §4.6, §8, §10, §11, §12; `04-CONTEXT.md`; `01-VPS-AUDIT.md`.

### Secondary (MEDIUM confidence)
- `core.telegram.org/bots/api` — лимиты `callback_data` (1–64 байта), `text` (1–4096), `answerCallbackQuery.text` (0–200); `core.telegram.org/bots/faq` — лимиты рассылки.
- `packages.ubuntu.com` — `docker.io` 29.1.3-0ubuntu3~24.04.2, `docker-compose-v2` 2.40.3+ds1-0ubuntu1~24.04.1 (noble-updates, universe), путь плагина.
- `github.com/GoogleContainerTools/distroless` (base/README.md) — состав `static`.
- `hub.docker.com` — наличие тегов `golang:1.27.1`, `postgres:17`.

### Tertiary (LOW confidence)
- Формулировки описаний ошибок Bot API (A4), поддержка числовых HTML-сущностей (A5), `docker load` OCI-архива на docker.io 29.1.3 (A1), доступность Docker Hub с VPS (A2), поведение `unattended-upgrade` по умолчанию (A3).

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — версия, зависимости и хеши проверены по прокси и sum-БД, код прочитан и запущен.
- Architecture / SQL / кодек: HIGH — SQL прогнан на PostgreSQL 17 по реальным 133 событиям; ловушка pgx воспроизведена.
- Docker/Compose: HIGH локально (собрано и запущено), MEDIUM для VPS (другая версия Compose, чужой хост).
- Скрипты VPS: MEDIUM — дизайн и хеш-однострочник проверены на Mac; выполнение на VPS невозможно без владельца.
- Pitfalls: HIGH для найденных в коде/пробах, MEDIUM для формулировок ошибок Telegram.

**Research date:** 2026-09-30
**Valid until:** 2026-10-14 (библиотека бота выпускала релиз каждую неделю; версия фиксируется, остальное стабильно ~30 дней)

## Suggested plan decomposition (ориентир для планировщика)

1. **Основа данных и зависимость:** `go get` (одобрено), миграция `00003`, `event.Filter`, `store`: пользователи/`UpdateFilters`, выдача/count, города/типы/источники, `LatestFinishedRuns`/`LatestRuns`/`RunSource`/алерты; интеграционные тесты на реальных данных.
2. **Ядро бота:** конфиг (`TELEGRAM_BOT_TOKEN`, `ADMIN_TG_IDS`), кодек callback, фильтры и пресеты, экраны (сводка, подменю), `tg.go` с обработкой ошибок, middleware, `/start`, текстовый ввод города; тесты на фиктивном Bot API.
3. **Список и карточка:** пагинация, stale-обработка, карточка со статусами/источниками/кнопкой регистрации.
4. **Админка и алерты:** `internal/alert` (`decide`, цикл), `/runs`, принятие через `Runner.Accept`, `newSources` в `cmd`, `runBot`.
5. **Упаковка:** Dockerfile, `.dockerignore`, `docker-compose.yml`, `.env.example`, `deploy/ship.sh`, `deploy/oneliner.sh`; локальная проверка сборки и `compose up` (как в этой сессии).
6. **Скрипты VPS:** `host-*`, `deploy-*`, `rollback.sh`, README с однострочниками (хеши — после финализации файлов).
7. **Развёртывание и приёмка (human):** шаги A0–A3, B0–B2 владельцем; первый прогон → алерт → принятие → проверка сценария `/start → … → регистрация` на реальных данных (критерий 1) и проверка админ-доступа не-админом (критерий 5).





