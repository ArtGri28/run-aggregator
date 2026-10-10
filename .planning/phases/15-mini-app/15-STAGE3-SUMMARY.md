# Phase 15, этап 3 — статика и экраны: главная, каталог, фильтры, карточка

Дата: 2026-10-10. База: `15-MINI-APP-PLAN.md` (разделы 5, 7, 8), `15-CONTEXT.md` (D-06, D-08…D-12), `15-STAGE2-SUMMARY.md` (фактические контракты API). Этапы 1–2 не менялись, кроме подключения статики в `server.go`. Бот, эталоны экранов, compose, deploy, миграции, go.mod, `cmd/` — не тронуты. Коммитов нет.

## Что сделано

### `web/` (новое)
- `embed.go`: `package web`, `//go:embed static` → `web.FS` (по образцу `migrations/embed.go`).
- `static/index.html` — одна страница, `lang="ru"`, viewport, `color-scheme: dark`, `telegram-web-app.js` и `app.js` (`type="module"`); контейнер `<main id="app">`; inline-скриптов и стилей нет.
- `static/app.css` — тёмная природная тема: сине-графитовый фон (`#0f1a24`), лаймовый акцент (`#b9f23a`), переменные CSS с тёмными значениями по умолчанию; размеры касания ≥ 44 px; плашки значков, плитки, список, чипы, карточка, заглушка.
- `static/app.js` — `Telegram.WebApp`: `ready()`, `expand()`, тема, `BackButton`; маршруты по hash `#/`, `#/catalog`, `#/filters`, `#/event/<id>`; неизвестный hash (в т. ч. параметры запуска Telegram) → `#/`; пустой `initData` → заглушка «Откройте приложение из Telegram».
- `static/api.js` — `fetch` к `/api/v1/…` с `Authorization: tma <initData>` (никогда в URL), разбор `{"error":…}` в `ApiError{status, code, retryAfter}`, таймаут 10 с через `AbortController`, без повторов; `meta()` — один запрос на сеанс.
- `static/ui.js` — создание узлов только через `textContent`/атрибуты (`el`), внешние ссылки только `http(s)` (`link`), даты «2 октября 2026»/«02.10», флаг из кода страны, километры как у бота, формы слова, пометки «Отменён»/«Перенесён», заглушка и показ ошибок: 401 → заглушка, 429 → «Повторите через N с» и кнопка «Повторить», 404 → «Событие больше не доступно», иначе — «Не удалось загрузить данные» и «Повторить».
- `static/icons.js` — свои SVG-контуры 24×24 (`createElementNS`, без строк разметки): бег — шоссе, трейл, кросс, стадион, с препятствиями, прочее (флаг); лыжи — лыжные гонки, лыжероллеры; плашка окрашивается CSS (лыжи — голубая).
- `static/screens/home.js` — бренд «Где старт?», слоган «Все старты в одном месте», плитки видов спорта с числом стартов из `meta` (бег всегда, остальные — только с будущими стартами, как `menuSports` бота); клик по виду ставит фильтр и открывает каталог; кнопки «Все старты», «Фильтры»; «Календарь», «Избранное», «Профиль» — выключенные плитки с пометкой «скоро».
- `static/screens/catalog.js` — `GET /events` по сохранённым фильтрам: заголовок «Найдено: N», строка активных фильтров (ссылка на фильтры), строки «дата · флаг город / название / дистанции» со значком вида спорта; «Ещё» подгружает следующую страницу; клик → карточка.
- `static/screens/filters.js` — единственный владелец состояния фильтров (`localStorage`, один ключ `gde-start.filters.v1`): вид спорта, страна (`meta.countries` + «Все страны»), город (топ из `/cities` по текущему фильтру + подсказка `/cities/suggest` от 2 символов с задержкой 300 мс; скрыт при «все страны»), даты (этот/следующий месяц, `meta.months`), корзины дистанции и дисциплина выбранного вида спорта; «Сбросить»; `toQuery` собирает параметры по контракту (город только со страной, корзины/дисциплина только с видом спорта).
- `static/screens/event.js` — `GET /events/{id}`: крупный значок по виду и дисциплине, название с пометкой статуса, подпись дисциплины (для лыж — с видом спорта), дата (диапазон при `end_date`), время старта, страна с флагом, город и адрес, дистанции с подписями (формат карточки бота), описание (`white-space: pre-line`), источники со ссылками, «Регистрация» только при `registration_url`, `is_favorite` — «★ В избранном» без действия; `BackButton` → каталог.

### `internal/webapp/server.go`
- `mux.Handle("/", srv.logged(static()))`: `fs.Sub(web.FS, "static")` + `http.FileServerFS`; путь без файла или каталог → `index.html` (`http.ServeFileFS`); заголовки на каждом ответе статики: `Cache-Control: no-cache`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'self'; script-src 'self' https://telegram.org; connect-src 'self'; img-src 'self' data:; style-src 'self'`. Статика без `Middleware`; `/api/` по-прежнему за ним (специфичнее `/`). Лог — тот же `http method= route= status= ms=` (маршрут `/`, без пути запроса).

### `Dockerfile`
- `COPY web ./web` рядом с `COPY migrations`; комментарий дополнен одной фразой.

### `internal/webapp/server_test.go`
- `TestAuthRequired`: корень → 200 (вместо 404), `/api/v1/nope` без заголовка → 401.
- `TestStatic` (новый): `GET /` → 200, `text/html`, содержит `lang="ru"`, `telegram-web-app.js`, `type="module"`, `app.js`, viewport; нет строк, похожих на ключ/токен; все четыре заголовка защиты и `Cache-Control`; CSP без `unsafe-*`; `/app.js` → `text/javascript` с `no-cache`; `/app.css` → `text/css`; `/nope`, `/event/5`, `/catalog/`, `/screens/`, `/screens` → байт-в-байт `index.html`; лог `route=/ status=200` без пути.
- `TestStaticSources` (новый): в `web/static` только `.html .css .js .svg` (≥ 9 файлов); ни в одном файле нет строк, похожих на ключ; ни один `.js` не содержит `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write`, `eval(`, `new Function`, `http://` (кроме пространства имён SVG для `createElementNS`), `initDataUnsafe`; `localStorage` — только в `screens/filters.js`.

## Проверки

- `gofmt -l .` пуст; `go vet ./...`, `go build ./...` — без замечаний.
- Полный прогон `TEST_DATABASE_URL=… go test -p 1 -count=1 -v ./...`: **499 PASS, 0 FAIL, 0 SKIP** (было 497; +2: `TestStatic`, `TestStaticSources`).
- Синтаксис всех 10 ES-модулей проверен парсером (копии в `.mjs` вне репозитория); в проекте Node/`package.json` не появились.
- Образ: `docker build --platform linux/amd64 -t aggregator:stage3-check .` собрался (первая попытка упала на загрузке базового образа с gcr.io — сетевая ошибка, повтор прошёл); образ удалён. `deploy/smoke.sh` не запускался.
- Ручная проверка вне Telegram: бинарник `aggregator webapp` на `127.0.0.1` против тестовой базы (`WEBAPP_SECRET` — hex из выдуманной строки): `GET /` → 200 `text/html` с CSP/nosniff/Referrer-Policy/`no-cache`; `/app.js` → `text/javascript`; `/nope` → `index.html`; `/api/v1/nope` → 401 `{"error":"unauthorized"}`; лог — только `method/route/status/ms`; остановлен по SIGTERM (`stopping`). Бот не запускался.
- `git diff --stat`: `Dockerfile` (+3/−1), `internal/webapp/server.go`, `internal/webapp/server_test.go`; новое — только `web/`. `git diff --stat -- docker-compose.yml .dockerignore deploy migrations go.mod go.sum cmd internal/bot internal/store internal/bot/testdata` пуст.
- Поиск названий инструментов разработки в `web/`, изменённых файлах — пусто (совпадения только по CSS-свойству `cursor`). `innerHTML|eval\(|http://` в `web/` — пусто, кроме пространства имён SVG.
- Размер статики: 10 файлов, ~36 КБ (56 КБ на диске).

## Отступления и примечания

- Добавлен модуль `web/static/ui.js` (общие помощники экранов) — имена файлов на усмотрение исполнителя (CONTEXT).
- Тема: при светлой схеме Telegram переменные фона/текста/подсказок/ссылок берутся из `themeParams`; при тёмной остаётся палитра приложения (лаймовый акцент — всегда). Иначе фирменная тёмная тема терялась бы во всех тёмных клиентах.
- В тесте запрета `http://` разрешено ровно одно вхождение — пространство имён SVG `http://www.w3.org/2000/svg`, без которого `createElementNS` не создаёт SVG-узлы (альтернатива — разметка строкой — нарушила бы запрет `innerHTML`).
- Главная повторяет правило бота: бег показывается всегда, остальные виды — только с будущими стартами (в задании «вид с нулём стартов не показывать, как бот»).
- `/favicon.ico` и прочие несуществующие пути отвечают `index.html` 200 — следствие правила «любой не-`/api/` путь без файла → index.html».
- Кнопка «Регистрация» и ссылки источников — обычные `<a target="_blank" rel="noopener noreferrer">`, только `http(s)` (сервер уже фильтрует по `event.ValidURL`, клиент проверяет повторно).

## Пометки «Упрощение:»

- `internal/webapp/server.go`, `staticCacheControl`: `Cache-Control: no-cache` без хеша версии в имени файла; при росте трафика — хеш в имени и долгий `max-age`.
- `web/static/screens/filters.js`, `DEFAULT_COUNTRY`: страна по умолчанию (`RU`) продублирована на клиенте; при смене `DefaultCountry` бота — отдать в `meta`.

## Не входит в этап

Избранное (кнопка добавления) и профиль (этап 4), календарь (этап 5), текстовый поиск (этап 6), кнопка в боте (D-07 — не делается), compose/Caddy/deploy и проверка в Telegram по HTTPS (этап 8, D-09). Запись решения в `MVP.md`/`PROJECT.md` (D-13) — отдельно.
