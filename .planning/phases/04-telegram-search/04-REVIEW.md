---
phase: 04-telegram-search
reviewed: 2026-09-30T21:00:00Z
depth: standard
files_reviewed: 39
files_reviewed_list:
  - cmd/aggregator/main.go
  - internal/alert/alert.go
  - internal/bot/admin.go
  - internal/bot/bot.go
  - internal/bot/callback.go
  - internal/bot/config.go
  - internal/bot/filters.go
  - internal/bot/list.go
  - internal/bot/tg.go
  - internal/event/filter.go
  - internal/ingest/runner.go
  - internal/store/alerts.go
  - internal/store/facets.go
  - internal/store/listing.go
  - internal/store/runs.go
  - internal/store/users.go
  - internal/store/storetest/storetest.go
  - migrations/00003_source_alerts.sql
  - Dockerfile
  - .dockerignore
  - docker-compose.yml
  - docker-compose.dev.yml
  - .env.example
  - deploy/ship.sh
  - deploy/oneliner.sh
  - deploy/smoke.sh
  - deploy/secrets.sh
  - deploy/confirm-test.sh
  - deploy/vps/agg
  - deploy/vps/agg-install.sh
  - deploy/vps/env-init.sh
  - deploy/vps/deploy-apply.sh
  - deploy/vps/deploy-preflight.sh
  - deploy/vps/deploy-verify.sh
  - deploy/vps/rollback.sh
  - deploy/vps/host-apply.sh
  - deploy/vps/host-preflight.sh
  - deploy/vps/host-verify.sh
  - deploy/README.md
findings:
  critical: 0
  high: 0
  medium: 1
  low: 6
  info: 8
  total: 15
status: fixed_with_deferrals
fixed: [ME-01, LO-02, LO-04]
deferred: [LO-01, LO-03, LO-05, LO-06, IN-01, IN-02, IN-03, IN-04, IN-05, IN-06, IN-07, IN-08]
---

# Phase 4: Code Review Report

**Reviewed:** 2026-09-30
**Depth:** standard. Глубже проверены граница sudo (`agg`, `agg-install.sh`, однострочник root) и цепочки бот → store → ingest (принятие прогона, фильтры, алерты).
**Files Reviewed:** 39
**Status:** fixed_with_deferrals (ME-01, LO-02, LO-04 исправлены 2026-09-30; остальное отложено)

## Summary

Проверены бот (кодек callback, фильтры, список и карточка, админка, обработка ошибок Bot API, маскирование токена), цикл алертов, SQL выдачи, фасетов, прогонов, алертов и пользователей, миграция `00003`, Dockerfile и compose, а также скрипты доставки и VPS.

Проверки: `go vet ./...` без замечаний, `gofmt -l` пусто, `bash -n` для всех скриптов в `deploy/` проходит. Все тесты проходят на тестовой БД (`go test -p 1 ./...`).

Критичных и высоких замечаний нет. Что проверено и в порядке:
- SQL. Весь SQL параметризован; в `listWhere` склеиваются только константы; в `SimilarCities` метасимволы LIKE исключены через `similarRE`.
- Фильтры и callback. Разбор callback строгий, длина ≤ 64 проверяется при кодировании. Версия фильтров растёт только при реальном изменении (`IS DISTINCT FROM` под блокировкой строки). Устаревший `p:` открывает первую страницу актуальной выдачи.
- Админка. `isAdmin` проверяется в `/runs`, в каждом `r:*` и в `SendAlert`.
- Принятие прогона ботом. Работает через тот же `Runner.Accept` с `ClaimRun` в одной транзакции; незакоммиченный `running` цикл алертов не видит, поэтому ложных алертов во время принятия нет.
- Экранирование. Всё динамическое в HTML экранировано, `cut` применяется до `EscapeString`.
- Секреты. Токен маскируется в `New` и в `redactHandler`; текст пользователя в лог не пишется.
- Граница sudo, включая `env -i`, `--clean-env`, проверку архива в `agg load` и `env_reset`, повторно проверена; обходов не найдено.

Главное замечание — ME-01: запасной путь из README загружает от root непроверенный архив из каталога runagg. Этот путь обходит защиту T-04-42. Остальные замечания касаются надёжности: устаревающая кнопка принятия в алерте, ключи городов длиной 61–64 байта, порядок записи `IMAGE_TAG` при неудачном деплое, ожидания 429 до ответа на callback.

## Fix Status

| ID | Статус | Как закрыто |
|---|---|---|
| ME-01 | исправлено | Запасной путь вынесен в `deploy/vps/load-postgres.sh` (в пакете, проверяется по `SHA256SUMS`, не ставится в lib и недоступен через `agg`): архив копируется во временный каталог root, SHA256 копии сверяется с хешем, посчитанным на Mac, `docker load` — только для копии; при несовпадении `STOP: HASH MISMATCH`. README переписан, прямой `docker load` из `/home/runagg` запрещён. Тест `deploy/load-postgres-test.sh`: совпавший хеш, другой хеш, неверный формат, подмена архива, ссылка на чужой файл. |
| LO-02 | исправлено | `decide`: новый suspect-прогон (другой `RunID` при том же статусе) — `send`; тот же прогон — тишина до суточного напоминания; для failed прежний суточный ритм. Тесты: случаи в `TestDecide`, регрессия `TestTickNewSuspectRun` (без исправления падает). |
| LO-04 | исправлено | `deploy-apply.sh`: прежний `.env` хранится до успешного запуска; при ошибке `up` или обрыве (trap EXIT, в т. ч. HUP) `IMAGE_TAG`/`IMAGE_PREV` возвращаются, затем попытка поднять прежний образ. Тест `deploy/deploy-apply-test.sh`: good → неудачный bad → повторный deploy → rollback → обрыв → отмена; прежний рабочий тег сохраняется (на старой версии — 7 ошибок). |
| LO-01, LO-03, LO-05, LO-06 | отложено | Phase 5 (фильтры, ключи городов, 429) и Phase 6 (healthcheck Telegram, мониторинг). |
| IN-01…IN-08 | отложено | Phase 5/6 (чистка кода, digest образов — Phase 6, ожидание обработчиков при остановке, часовой пояс). |

## Narrative Findings

## Warnings

### ME-01 (medium): запасной путь «перенос postgres:17» — root выполняет `docker load` из каталога runagg без проверки — ИСПРАВЛЕНО

**File:** `deploy/README.md:59` (подсказка ведёт сюда из `deploy/vps/deploy-apply.sh:50`)
**Issue:** Инструкция предлагает положить `postgres17.tar` через `scp` в `/home/runagg/agg-deploy/` и выполнить в root-консоли `docker load -i /home/runagg/agg-deploy/postgres17.tar`. Файл принадлежит runagg, и между `scp` и `docker load` его можно подменить. Хеш не сверяется, состав архива не проверяется.

Это ровно тот сценарий, от которого защищает `agg load` (T-04-42): архив от runagg или от укравшего его ключ может перезаписать любой образ хоста. Например, `postgres:17` с доступом к тому БД или образы чужих сервисов (`amnezia-awg2`). У postgres в compose нет `user:`/`cap_drop`, так что подменённый образ получит больше прав, чем `aggregator:<TAG>` (T-04-43 принят только для образа aggregator).
**Fix:** Действовать как в однострочнике для пакета: копия в каталог root, сверка SHA256, посчитанного на Mac, и только потом загрузка:
```bash
# Mac: печатает хеш для сверки
shasum -a 256 postgres17.tar
# root-консоль
T=$(mktemp -d) && cp /home/runagg/agg-deploy/postgres17.tar "$T"/ && cd "$T" \
  && if echo '<SHA256 с Mac>  postgres17.tar' | sha256sum -c --quiet -; then docker load -i postgres17.tar; else echo 'STOP: HASH MISMATCH'; fi
```
Лучше расширить `agg load`, чтобы он принимал ровно `postgres:17` по отдельной подкоманде с той же проверкой состава, и зафиксировать `postgres:17@sha256:…` в compose (см. IN-05).

## Low

### LO-01 (low): ключ города длиной 61–64 байта в боте недоступен, хотя БД и normalize его допускают

**File:** `internal/bot/callback.go:59,83`, `internal/bot/filters.go:66,483`
**Issue:** `normalize.CityKey` обрезает ключ до 64 байт (`internal/normalize/place.go:76`, в комментарии сказано «keeps city_key inside Telegram callback_data»), и CHECK в `events.city_key` тоже разрешает 64. Бот же принимает не больше 60 (`64 − len("f:c:")`). Для такого города:
- кнопка в топ-10 и в «похожих» молча пропадает (`row.add` при ошибке `encode` кнопку не добавляет);
- текстовый ввод отвечает «Не понял название города»;
- сохранённый фильтр `loadFilters` сбрасывает.

Сейчас таких городов, скорее всего, нет, но ограничения в двух пакетах расходятся.
**Fix:** Согласовать пределы: `maxCityKeyBytes = 60` в `normalize` и CHECK `char_length(city_key) <= 60` новой миграцией. Если ключи длиннее 60 уже есть, сначала проверить их запросом.

### LO-02 (low): кнопка «Принять как базу» в алерте устаревает после следующего suspect-прогона, а нового алерта нет до 24 ч — ИСПРАВЛЕНО

**File:** `internal/alert/alert.go:58-61` (вместе с `internal/store/runs.go:139`)
**Issue:** `decide` сравнивает только `st.State != run.Status`. Когда за suspect-прогоном N идёт suspect-прогон N+1 (типично для D-07: без базы следующий прогон снова «первый»; также после прерванного прогона), новый алерт не отправляется. Кнопка в чате указывает на N, а N больше не последний, и `FinishRun` уже очистил его снимок.

Нажатие даёт «Уже нельзя принять… /runs». Напоминание приходит только через 24 ч, а прогоны идут каждые ~6 ч, так что кнопка из алерта рабочая лишь в первые часы после сообщения. D-15 выполняется частично; обходной путь — `/runs`.
**Fix:** Для suspect со снимком считать сменой состояния и новый прогон:
```go
case st == nil, st.State != run.Status,
	run.Status == "suspect" && run.HasSnapshot && st.RunID != run.RunID:
	return send
```
Либо отправлять короткое «прогон N+1 заменил N, актуальная кнопка — /runs». `RunID` в `source_alerts` для этого уже хранится.

### LO-03 (low): ответ на callback уходит только после всех повторов 429 — до ~1,5 минуты; повтор при `retry_after > 30 с` заведомо бесполезен

**File:** `internal/bot/bot.go:199-207`, `internal/bot/tg.go:89-103,132-140,183-204`
**Issue:** `cb` отвечает `answerCallbackQuery` в `defer`, то есть после `fn`. Внутри `fn` цепочка `show → attempt(withRetry) → attempt без URL (withRetry) → send(attempt(withRetry))` может ждать `maxRetryWait` до трёх раз. Всё это время у пользователя крутится индикатор. К моменту ответа Telegram уже отклоняет запрос («query is too old»), ошибка уходит в лог уровня Debug, и уведомление вроде «Фильтры изменились…» теряется. Кроме того, при `retry_after > 30 с` ожидание обрезается до 30 с, и единственный повтор гарантированно снова получает 429.
**Fix:** При `wait > maxRetryWait` не повторять, сразу возвращать ошибку. Для callback повторять только при коротком `retry_after` (например, ≤ 5 с), чтобы ответ ушёл вовремя. Либо отвечать на callback сразу пустым ответом, а текстовые уведомления для устаревших кнопок показывать в самом сообщении.

### LO-04 (low): `deploy-apply.sh` пишет `IMAGE_TAG`/`IMAGE_PREV` до успешного `up`, и после неудачного деплоя последний рабочий тег теряется — ИСПРАВЛЕНО

**File:** `deploy/vps/deploy-apply.sh:33-42` (запуск — строки 45-55)
**Issue:** `.env` меняется до `compose config`, `pull` и `up`. Если деплой T2 упал (например, на migrate), в `.env` окажется `IMAGE_TAG=T2`, `IMAGE_PREV=T1`. Следующий деплой T3 запишет `IMAGE_PREV=T2`, то есть неработающий тег, и T1 выпадет из отката. `agg rollback` после T3 вернёт сломанный T2.
**Fix:** Писать новый `.env` во временный файл и проверить `docker compose --env-file "$TMP" -f "$F" config -q`. Переносить его на место только после успешного `up -d --wait`. При ошибке оставить прежний `.env` и прямо сообщить, что сервисы могли частично пересоздаться. Другой вариант — обновлять `IMAGE_PREV` только если `CUR` был здоров, то есть `agg verify` прошёл.

### LO-05 (low): сохранённый фильтр по прошедшему месяцу навсегда даёт пустую выдачу с устаревшей подписью

**File:** `internal/bot/filters.go:63-65,186-194,231-234`
**Issue:** `mYYYYMM` хранится абсолютно (D-17 сохраняет фильтры). Когда месяц прошёл, `dateRange` возвращает `from = today > to`: сводка показывает «Дата: сентябрь 2026 · … Показать 0», а список — «Ничего не нашлось». В меню дат этого месяца уже нет. Выйти можно только через «Снять дату» или «Сбросить», но пользователь не видит, почему выдача пуста.
**Fix:** В `loadFilters` (или в `query`) сбрасывать `Date`, если `monthEnd(first) < today`: `today` доступен вызывающему, `loadFilters` можно передать его параметром. Другой вариант — показывать в сводке «(месяц прошёл)».

### LO-06 (low): healthcheck бота проверяет только БД — если бот не принимает апдейты, контейнер всё равно `healthy`, и `agg verify` даёт OK

**File:** `docker-compose.yml:84-89`, `cmd/aggregator/main.go:109-121`, `deploy/vps/deploy-verify.sh:24`
**Issue:** `/aggregator healthcheck` открывает только БД. Отозванный токен (401), второй экземпляр с тем же токеном (409, T-04-40) или недоступный `api.telegram.org` дают бесконечные `telegram` ERROR в логе, но контейнер остаётся `healthy`, `CHECK compose-state: OK` и `DEPLOY VERIFY OK`. Алерты в это время тоже не доходят. Heartbeat отложен в Phase 6, но проверка деплоя сейчас объявляет OK для неработающего бота.
**Fix:** Минимально: в `deploy-verify.sh` проверять хвост логов bot на `"msg":"telegram"` и помечать это FAIL или предупреждением. Правильнее: бот обновляет время последнего успешного `getUpdates`/`getMe` (в файл на tmpfs или в `heartbeats`), а `healthcheck bot` проверяет его свежесть (Phase 6).

## Info

### IN-01 (info): неиспользуемый параметр `v` в `emptyScreen`

**File:** `internal/bot/filters.go:293`
**Issue:** `v int` не используется: кнопки «Снять …» — `f:x:*` без версии. Параметр вводит в заблуждение, будто экран привязан к версии.
**Fix:** Убрать параметр (вызов в `internal/bot/list.go:51`).

### IN-02 (info): `toggleBucket` меняет фильтр относительно текущего состояния, хотя комментарий обещает абсолютный результат

**File:** `internal/bot/filters.go:83,109-118`
**Issue:** Кнопка корзины из старого сообщения переключает корзину от текущего состояния. Два быстрых нажатия дают «выкл→вкл». Это безопасно (под блокировкой строки), но противоречит комментарию «чистые методы с абсолютным результатом» и D-решению об абсолютных значениях callback.
**Fix:** Либо уточнить комментарий, либо кодировать в кнопке желаемое состояние (`f:b:+0`/`f:b:-0`); длина это позволяет.

### IN-03 (info): `Tick` прекращает обход на первой ошибке по одному источнику; если `SaveAlertState` упал после отправки, алерт повторяется каждую минуту

**File:** `internal/alert/alert.go:131-154`
**Issue:** Ошибка `ClearAlertState`, `SourceHealth` или `SaveAlertState` для одного источника пропускает все следующие источники. При сбое записи после успешной отправки следующий тик снова шлёт то же сообщение, пока запись не пройдёт. Пока источник один, влияние минимальное.
**Fix:** Логировать ошибку по источнику и продолжать цикл (`continue`), а общую ошибку вернуть в конце.

### IN-04 (info): callback из не-личного чата отбрасывается без `answerCallbackQuery`

**File:** `internal/bot/bot.go:165-172`
**Issue:** `privateOnlyMW` молча отбрасывает апдейт, и у нажавшего крутится индикатор до таймаута клиента. Это противоречит правилу «answerCallbackQuery на каждый callback». Практически возможно только для сообщения бота, попавшего в группу.
**Fix:** Для `u.CallbackQuery != nil` отвечать пустым `answerCallbackQuery` без записи в `users`.

### IN-05 (info): базовые образы и `postgres:17` не закреплены по digest

**File:** `Dockerfile:7,26`, `docker-compose.yml:46`, `deploy/vps/deploy-apply.sh:46`
**Issue:** `golang:1.27.1`, `distroless/static-debian13:nonroot` и `postgres:17` подтягиваются по плавающему тегу, а `agg deploy` каждый раз делает `pull postgres`. Минорное обновление postgres или подмена в реестре попадают на VPS без решения владельца.
**Fix:** `image: postgres:17@sha256:…` и `FROM …@sha256:…`; обновлять digest осознанно.

### IN-06 (info): однострочник root оставляет копии пакета в `/tmp` и копирует всё содержимое `agg-deploy`

**File:** `deploy/oneliner.sh:59,71`
**Issue:** `T=$(mktemp -d)` никогда не удаляется. `cp $STAGE/*` копирует и посторонние файлы runagg (например, оставшийся `postgres17.tar` на сотни МБ). Если там есть подкаталог, `cp` завершается ошибкой, и строка молча ничего не запускает — без `STOP: HASH MISMATCH`.
**Fix:** Копировать только `SHA256SUMS` и перечисленные в нём файлы, например `for f in SHA256SUMS $(awk '{print $2}' "$STAGE/SHA256SUMS"); do cp -- "$STAGE/$f" "$T/"; done` — после сверки хеша самого `SHA256SUMS`. В конце добавить `; rm -rf "$T"`.

### IN-07 (info): остановка бота не ждёт выполняющихся обработчиков

**File:** `cmd/aggregator/main.go:197-200`
**Issue:** go-telegram запускает обработчики в отдельных горутинах (`ProcessUpdate` → `go r(...)`), а `Start` их не ждёт. После SIGTERM `runBot` закрывает store, и процесс завершается посреди обработчика. Данные не портятся: `UpdateFilters` и `Accept` атомарны, `Accept` работает на `WithoutCancel`. Но админ может не получить сообщение о результате принятия, а пользователь — ответ на нажатие.
**Fix:** Счётчик `sync.WaitGroup` в `recoverMW` (Add/Done вокруг `next`) и `Wait` с таймаутом меньше `stop_grace_period` перед `s.Close()`.

### IN-08 (info): часовой пояс Москвы задан тремя способами

**File:** `internal/bot/admin.go:18`, `internal/alert/alert.go:21`, `internal/bot/bot.go:39`
**Issue:** В `admin.go` и `alert.go` продублирован `time.FixedZone("MSK", 3*3600)`, а `bot.go` загружает `Europe/Moscow` через tzdata (встроена `time/tzdata`). Сейчас результат одинаковый, но источников истины три.
**Fix:** Одна переменная: `b.loc` в боте, а в `alert` передать `*time.Location` полем `Loop` или использовать `time.LoadLocation` так же, как в боте.

---

_Reviewed: 2026-09-30_
_Depth: standard_
