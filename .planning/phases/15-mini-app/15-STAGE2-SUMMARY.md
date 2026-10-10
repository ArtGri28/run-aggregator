# Phase 15, этап 2 — подкоманда `webapp` и API только чтения

Дата: 2026-10-10. База: `15-MINI-APP-PLAN.md` (разделы 5, 7, 8), `15-CONTEXT.md` (D-03, D-04, D-10…D-12). Этап 1 (`auth.go`, `middleware.go`, `auth_test.go`) не менялся. Бот, эталоны экранов, compose, Dockerfile, deploy, миграции, go.mod — не тронуты. Коммитов нет.

## Что сделано

### `internal/bot`
- `catalog.go` (новый): `bot.Catalog() bot.Dictionary` — единственный экспортируемый аксессор справочников: `DefaultCountry`, `FavLimit`, `MaxPage`, `MaxCityInput`, `Buckets` (корзины по видам спорта: подпись, `(Lo, Hi]`), `TypeLabels`, `Types` (дисциплины в порядке меню, без «Другого»), `CountryNames`, `SourceNames`, `Months(today)`, `ValidPeriod`, `DateRange`. Значения читаются из тех же переменных пакета, что и экраны бота.
- `place.go`, `list.go`, `country_test.go`: `visibleAddress` → `VisibleAddress` (механическое переименование, вызовы и тест обновлены).

### `internal/webapp`
- `dict.go` (новый): JSON-типы ответов (`metaJSON`, `listJSON`, `cardJSON`, `cityJSON` и вложенные) и сборка из `bot.Dictionary` / `event.Sports` / `store`. Ссылки (`registration_url`, `sources[].url`) — только по `event.ValidURL`; `address` — через `bot.VisibleAddress`.
- `server.go` (новый): `New(store, secret, now, logger) http.Handler`; `NewServer(addr, h) *http.Server` (`ReadHeaderTimeout` 5 с, `ReadTimeout` 10 с, `WriteTimeout` 15 с, `IdleTimeout` 60 с); `Shutdown(srv)` с грейсом 10 с. Маршруты `http.ServeMux` с шаблонами методов; цепочка на каждом маршруте: лог → `Middleware` (initData) → ограничение частоты → обработчик; `/api/` — 404 `not_found` (тоже за проверкой). `MaxBytesReader` 64 КиБ на тело.
- `server_test.go` (новый): интеграционные тесты на `httptest.NewServer` + `storetest.Open` + фикстура Марафонца.

### `cmd/aggregator`
- `main.go`: подкоманда `webapp` (`webappConfig` → `WEBAPP_ADDR` host:port, `WEBAPP_SECRET` hex 64; `openStore`; heartbeat `webapp` раз в минуту; остановка по ctx через `webapp.Shutdown`; отказ прослушивания завершает горутины); подкоманда `webapp-secret` (`bot.LoadConfig` → `hex(SecretFromToken)` одной строкой в stdout, в лог ничего); `runHealthcheck` принимает `webapp` (БД + возраст отметки ≤ 5 мин, HTTP-пинг не нужен); `usage`.
- `main_test.go`: `TestHeartbeatFresh` расширен (`healthcheck webapp`, `webapp bot` → usage); `TestWebappConfig` — разбор переменных, ошибка называет переменную и не повторяет значение, `runWebapp` падает на `WEBAPP_ADDR` до БД, `webapp-secret` без токена — ошибка `TELEGRAM_BOT_TOKEN`.

### `.env.example`
- `WEBAPP_ADDR`, `WEBAPP_SECRET` с описанием и способом получения (`aggregator webapp-secret`); значений нет.

## Контракты (раздел 5 плана)

- Все запросы: `Authorization: tma <initData>`; отказ → 401.
- `GET /api/v1/meta` → `today`, `sports[{code,title,emoji,count}]` (все включённые, счётчик во всех странах, как `menuSports`), `types{sport:[{code,label}]}`, `buckets{sport:[{i,label,lo,hi}]}`, `countries[{code,name,count}]`, `months[{code,label}]`, `limits{page_size:20,favorites:50}`.
- `GET /api/v1/events?sport&country&city&type&dist&periods|from,to&page&limit` → `{total,page,pages,items[{id,title,start_date,country_code,city,status,sport,distances}]}`. `country` по умолчанию `RU`, `*` — все; `city` только со страной ≠ `*` и в грамматике ключа; `dist` — индексы корзин выбранного вида спорта (без `sport` — 400); `type` — дисциплина вида спорта (без `sport` — любого включённого); `periods` и `from`/`to` взаимоисключимы; `From` не раньше «сегодня» по Москве; `limit` 1…50 (по умолчанию 20), `page` 1…10000. Любая ошибка параметра → 400 `bad_request`.
- `GET /api/v1/events/{id}` → корень через `RootEventID`; нет / скрыто / слито / вид спорта выключен → 404 `gone`; `id` не число → 400. Поля карточки по плану, `registration_url` и `end_date` опускаются, если пусты; `is_favorite` через `IsFavorite` для пользователя из `initData`.
- `GET /api/v1/cities?…те же фильтры…&limit=10` → `[{key,name,count}]` (`TopCities`, фильтр города не учитывается).
- `GET /api/v1/cities/suggest?country&q` → тот же формат (`SimilarCities(country, normalize.CityKey(q), q, today, 5)`); `q` обязателен, ≤ 64 рун.
- Ошибки: `{"error":"<код>"}`: `bad_request`, `not_found`, `gone`, `rate_limited`, `internal`. Ограничение частоты: 60 запросов/мин на `tg_id` → 429 с `Retry-After` (секунды до конца окна). `Cache-Control: private, max-age=60` на ответах 200.
- Лог `slog`: `http method=… route=<шаблон> status=… ms=…`, причина отказа initData — кодом (`auth rejected code=…`); без `q`, заголовков, `initData`, `tg_id`.

## Проверки

- `gofmt -l .` пуст; `go vet ./...`, `go build ./...` — без замечаний.
- Полный прогон `go test -p 1 -count=1 -v ./...` с `TEST_DATABASE_URL`: **497 PASS, 0 FAIL, 0 SKIP** (было 490; +7: `TestWebappConfig`, `TestAuthRequired`, `TestMeta`, `TestEvents`, `TestEventCard`, `TestCities`, `TestRateLimit`).
- Покрытие тестами API: 401 без заголовка и с чужой схемой; `meta` — оба включённых вида спорта, корзины, дисциплины, страны, месяцы, пределы, `Cache-Control`; `events` — пагинация (страницы 1/2, страница за пределом), 18 неверных наборов параметров → 400, 12 фильтров (страна, город, корзины, дисциплина, периоды, явный диапазон, `from` раньше «сегодня») с `total == CountEvents` при том же `event.Filter`; `events/{id}` — обычное, без ссылки, с негодной ссылкой, адрес через `VisibleAddress`, `is_favorite` после `AddFavorite` (и `false` у другого пользователя), слитое → корень, скрытое и несуществующее → `gone`, выключенный вид спорта → `gone`, не число → 400; `cities` (совпадение с `TopCities`, `limit` > 50 → 400), `cities/suggest` (`Моск` → `moscow`, пусто, `q` > 64 → 400, `q` пуст → 400, 64 руны — 200); приватность лога (есть строки запросов с методом/маршрутом/кодом; нет `tg_id`, текста `q`, `initData`, `Authorization`, `auth_date`, `hash=`); 429 после 60 запросов с `Retry-After: 60`, другой пользователь не затронут.
- Пробы чувствительности (файл восстановлен побайтно, `cmp`): снятие предела `limit` → `TestEvents` падает (`limit=51` → 200); замена `RootEventID` на сам `id` → `TestEventCard` падает (слитое → `gone` вместо корня).
- `git diff --stat`: `.env.example`, `cmd/aggregator/main.go`, `cmd/aggregator/main_test.go`, `internal/bot/country_test.go`, `internal/bot/list.go`, `internal/bot/place.go`; новые: `internal/bot/catalog.go`, `internal/webapp/{dict,server,server_test}.go`. `git diff --stat -- internal/bot/testdata docker-compose.yml Dockerfile deploy migrations go.mod go.sum` пуст.
- Локальная проверка бинарника против тестовой базы: `webapp-secret` с выдуманным токеном печатает 64 hex-символа в stdout, stderr пуст; без токена — ошибка `TELEGRAM_BOT_TOKEN`; `webapp` без `WEBAPP_ADDR` — ошибка с именем переменной; `webapp` на `127.0.0.1` отвечает 401 на `/api/v1/meta` без заголовка, `healthcheck webapp` проходит, SIGTERM останавливает с записью `stopping`.
- Поиск названий инструментов разработки в изменённых файлах — пусто.

## Отступления и примечания

- Тело ответа 401 приведено к `{"error":"unauthorized"}` правкой `middleware.go` (этап 1) при проверке этапа; расхождения с планом нет.
- Аксессор назван `bot.Catalog()` с типом результата `bot.Dictionary` (в плане имя «например»).
- `sports` в `meta` перечисляет все включённые виды спорта со счётчиком (в том числе с нулём) — клиент решает, показывать ли; бот прячет небеговой вид без событий.
- Неизвестный путь под `/api/` и запрос неподдерживаемым методом — 404 `not_found` (не 405).
- Несуществующее событие — 404 `gone` (как `buildCard` бота: `ErrNotFound` и скрытое неразличимы для клиента).

## Пометки «Упрощение:»

- `internal/webapp/server.go`, `limiter`: счётчик частоты в памяти одного процесса; при нескольких экземплярах `webapp` — счётчик в базе или на прокси.
- `cmd/aggregator/main.go`, `runWebapp`: пользователь базы тот же, что у `bot` и `worker` (D-04); правило «webapp пишет только в `favorites`» держит код; отдельная роль только для чтения — позже отдельным решением.

## Не входит в этап

Статика и экраны (этап 3), избранное и профиль по пользователю (этап 4), календарь (этап 5), текстовый поиск (этап 6), кнопка в боте (D-07 — не делается), compose/Caddy/deploy (этап 8). Запись решения в `MVP.md`/`PROJECT.md` (D-13) — отдельно.
