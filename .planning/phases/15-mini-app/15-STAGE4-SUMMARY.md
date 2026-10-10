# Phase 15, этап 4 — избранное (чтение и запись) и профиль (только чтение)

Дата: 2026-10-10. База: `15-MINI-APP-PLAN.md` (разделы 5, 7, 8), `15-CONTEXT.md` (D-04, D-10, D-11, D-12), `15-STAGE2-SUMMARY.md`, `15-STAGE3-SUMMARY.md`. `internal/store`, `auth.go`, `middleware.go`, `cmd/`, compose, Dockerfile, deploy, миграции, go.mod, эталоны экранов и поведение бота не тронуты. Коммитов нет.

## Что сделано

### `internal/bot/catalog.go`
- `bot.Dictionary` дополнен справочниками анкеты `ProfileGenders`, `ProfileAges`, `ProfileCountries`, `ProfileSports` (`[]Option`, порядок кнопок бота) — из тех же переменных `profile*`, что экраны бота. Второго аксессора нет; поведение бота не меняется.

### `internal/webapp/dict.go`
- `itemJSON` (строка списка без дистанций) вынесен из `listItemJSON` встраиванием — JSON каталога прежний; `favListJSON`, `favItemsJSON`.
- `profileJSON` и `profileJSONOf`: код и подпись из справочников анкеты; незаполненное поле — `null`.

### `internal/webapp/server.go`
- Маршруты: `GET /api/v1/favorites`, `PUT /api/v1/favorites/{id}`, `DELETE /api/v1/favorites/{id}`, `GET /api/v1/profile`.
- Пользователь — только `TgID(ctx)`; параметры и тело запроса не читаются (тело ограничено прежним `MaxBytesReader`).
- `limiter` получил поле `max`; два счётчика: чтение 60/мин (`rateLimit`), запись 20/мин (`writeLimit`), раздельные; `limited(l, next)` — общая обёртка.
- `pathID`: идентификатор в пути — `^[1-9][0-9]{0,18}$` и без переполнения `int64`, иначе 400.
- `writeJSON` не перезаписывает `Cache-Control`, заданный обработчиком; ответы по пользователю и ответы записи — `no-store`.

### `web/static/`
- `api.js`: общий `request(method, …)`, `get`/`put`/`del` без тела, ответ 204 → `null`.
- `ui.js`: `errorText` (текст ошибки для действий записи), `showError` использует его.
- `screens/favorites.js` (новый): «Избранное: N», строки «дата · флаг город / название» со значком вида спорта, «Ещё», кнопка «✕» (`aria-label` «Убрать из избранного») в строке, пустое состояние с подсказкой и кнопкой «К каталогу».
- `screens/profile.js` (новый): пол, возраст, «Живу», спорт — только чтение; нет профиля — «Анкета заполняется в боте»; кнопка «Изменить в боте» / «Вернуться в бот» вызывает `Telegram.WebApp.close()`; имени бота и deep-link нет.
- `screens/event.js`: кнопка «В избранное» / «Убрать из избранного» (состояние обновляется сразу, пометка «★ В избранном» в шапке); `limit` — «В избранном уже N стартов — это предел…» (N из `meta.limits.favorites`), `past` — «Старт уже прошёл.», 404 — «Событие больше не доступно.», 429 — «Повторите через N с».
- `screens/home.js`: «Избранное» и «Профиль» — плитки-ссылки; «Календарь» — по-прежнему «скоро».
- `app.js`: маршруты `#/favorites`, `#/profile`; «Назад» карточки ведёт в список, из которого она открыта (каталог или избранное).
- `app.css`: строка избранного (`.fav-row`), правило `[hidden] { display: none !important; }`.

## Контракты

- `GET /api/v1/favorites?page=&limit=` → `{total,page,pages,items[{id,title,start_date,country_code,city,status,sport}]}` — без `distances` (`ListFavorites` их не заполняет); только будущие видимые; запись на слитое событие — корнем, один раз; порядок — дата, id. `limit` 1…50 (по умолчанию 20), `page` 1…10000, иначе 400. Страница за пределом — пустой `items`.
- `PUT /api/v1/favorites/{id}` → `AddFavorite(tgID, id, today, now, bot.Catalog().FavLimit)`; `200 {"outcome":"added"|"already"|"limit"|"past"}`; нет события / скрыто / вид спорта выключен → `404 {"error":"gone"}`. Без тела, идемпотентно.
- `DELETE /api/v1/favorites/{id}` → `RemoveFavorite`; всегда `204` без тела.
- `GET /api/v1/profile` → нет строки: `{"exists":false}`; есть: `{"exists":true,"status","gender":{code,label}|null,"age_group":{code,label}|null,"country":{code,label}|null,"city","sports":[{code,label}]}`. Без `awaiting_*`, ключа города, версии и `tg_id`.
- Предел частоты: чтение 60/мин, запись 20/мин на пользователя → 429 `rate_limited` с `Retry-After`.
- `Cache-Control: no-store` — `favorites`, `profile`, ответы `PUT`/`DELETE`, карточка `events/{id}`.
- Лог прежний: `http method= route=<шаблон> status= ms=`.

## Проверки

- `gofmt -l .` пуст; `go vet ./...`, `go build ./...` — без замечаний.
- Полный прогон `TEST_DATABASE_URL=… go test -p 1 -count=1 -v ./...`: **502 PASS, 0 FAIL, 0 SKIP** (было 499; +3: `TestFavorites`, `TestWriteLimit`, `TestProfile`).
- `TestFavorites` (в начале `DELETE FROM favorites`): `added` → повтор `already` (одна запись в базе); `is_favorite` в карточке `true` у своего и `false` у другого пользователя, после `DELETE` — `false`; изоляция двух пользователей; `?tg_id=` и тело `{"tg_id":…}` игнорируются на `GET`/`PUT`/`DELETE`; путей с идентификатором пользователя нет (9 запросов → 404 `not_found`); `DELETE` трижды подряд и для несуществующего события → 204; 7 негодных `id` × `PUT`/`DELETE` → 400; 5 негодных `page`/`limit` → 400; `gone` (нет события, скрыто), `past`; список — порядок, страницы, без `distances`, прошедший не показан, слитое — корнем, `PUT` слитого → `already`, `DELETE` корня убирает запись на слитое; `limit` при 50 записях, `already` при пределе, после удаления — `added`; число строк `users`/`user_profiles` не изменилось; лог — есть строки `PUT`/`DELETE`/`GET` с шаблоном маршрута, нет `tg_id`, идентификаторов пользователей и событий, `initData`, строки запроса.
- `TestWriteLimit`: 20 запросов записи → 21-й `PUT` и `DELETE` → 429 с `Retry-After: 60`; чтение того же пользователя и запись другого не затронуты.
- `TestProfile`: без строки — ровно `{"exists":false}` и `no-store`, строк не создано; со строкой (`StartUser` + `UPDATE` в тесте) — коды и подписи, порядок видов спорта как у бота; в ответе нет `awaiting`, `tg_id`, ключа города, версии; незаполненная анкета — `null` и `[]`; чужой профиль не виден; число строк не изменилось; лог без пользователя и города.
- `TestStaticSources`: файлов ≥ 12, `screens/favorites.js` и `screens/profile.js` на месте; запреты прежние.
- Пробы чувствительности (файл восстановлен побайтно, `cmp`): (а) пользователь из query `tg_id` → `TestFavorites` падает («?tg_id= показал чужое избранное»); (б) предел избранного снят → `TestFavorites` падает («PUT сверх предела: 200 "added", want 200 limit»).
- Синтаксис 12 ES-модулей проверен парсером (копии `.mjs` вне репозитория).
- `git diff --stat`: `internal/bot/catalog.go`, `internal/webapp/{dict,server,server_test}.go`, `web/static/{api,app,ui}.js`, `app.css`, `screens/{event,home}.js`; новые — `web/static/screens/{favorites,profile}.js`. Diff по `docker-compose.yml Dockerfile .dockerignore deploy migrations go.mod go.sum cmd internal/store internal/webapp/auth.go internal/bot/testdata` пуст; в `internal/bot` изменён только `catalog.go`.
- Поиск названий инструментов разработки и `innerHTML|eval\(` в изменённых файлах — пусто; `localStorage` — только `screens/filters.js`; `initDataUnsafe` нет.
- Локально: `aggregator webapp` на `127.0.0.1` против тестовой базы — `PUT`/`DELETE /api/v1/favorites/1`, `GET /favorites`, `GET /profile` без заголовка → 401 `{"error":"unauthorized"}`; лог — метод, шаблон, код, длительность; остановлен по SIGTERM. Бот не запускался.

## Отступления и примечания

- Карточка `GET /events/{id}` теперь `Cache-Control: no-store` (было `private, max-age=60`): в ней состояние избранного, и кэш клиента показывал бы прежнее после добавления или удаления.
- Строгая проверка `id` в пути (`pathID`) применена и к карточке: `+5`, `007` теперь 400 (раньше принимались `ParseInt`).
- Запись считается только своим счётчиком (20/мин) и не расходует предел чтения.
- `gone` возвращается кодом ошибки `404 {"error":"gone"}`, а не полем `outcome` (раздел 5 плана).
- Поля `v` в профиле нет (нужно только для `PATCH`, которого в v1 нет).
- Подписи анкеты отдаются как в справочнике бота, со значками («🇷🇺 Россия», «🏃 Бег»).
- `app.css`: `[hidden] { display: none !important; }` — без него `.btn { display: inline-flex }` перекрывал атрибут `hidden`, и кнопка «Ещё» каталога (этап 3) не скрывалась на последней странице.
- Пустое состояние избранного и экраны не обещают уведомлений и ничего не говорят о данных пользователя.

## Пометки «Упрощение:»

- `internal/webapp/server.go`, `limiter` — прежняя: счётчики в памяти одного процесса.
- `web/static/screens/favorites.js`, `remove`: после удаления список перечитывается с первой страницы; при длинных списках — удалять строку на месте и догружать недостающую.

## Не входит в этап

Правка профиля и фильтров из Mini App (`PATCH /profile` — D-10), календарь (этап 5), текстовый поиск (этап 6), кнопка в боте (D-07), compose/Caddy/deploy и проверка в Telegram по HTTPS (этап 8, D-09). Экраны в браузере вручную не открывались: проверены тесты API, синтаксис модулей и ответы сервера.
