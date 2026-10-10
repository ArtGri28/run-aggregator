# Phase 15: Mini App «Где старт?» — исследование и план реализации

Документ — результат исследования только чтением (2026-10-10). Код, миграции, конфигурация, `docker-compose.yml`, пакет `deploy/` и VPS не менялись. Реализация — только после подтверждения плана владельцем; каждый шаг на production — по отдельному разрешению.

Ссылки на код даны как `путь:строка` по состоянию рабочей копии на дату исследования. `go build ./...` и `go vet ./...` проходят (Go 1.27.1).

## 1. Фактическая архитектура

### Процессы

Один бинарник `cmd/aggregator/main.go` с подкомандами `bot`, `worker`, `migrate`, `healthcheck [bot|worker]`, `unmerge` (`cmd/aggregator/main.go:36`, `:78-92`). Один Docker-образ (distroless, nonroot 65532, статический бинарник — `Dockerfile:23-30`), в production — три долгоживущих контейнера и один одноразовый (`docker-compose.yml`): `postgres` (PostgreSQL 17), `migrate` (goose, миграции встроены в бинарник — `migrations/embed.go`, `internal/store/store.go:97-114`), `bot`, `worker`. Все сервисы приложения: `read_only`, `cap_drop: [ALL]`, `no-new-privileges`, пользователь 65532 (`docker-compose.yml:52-60`). **Ни один порт наружу не публикуется** (`docker-compose.yml:6-7`; проверка `port-not-published` и `port-not-listening` в `deploy/vps/deploy-verify.sh:53-58`). `ufw` пропускает только SSH; порты Docker обходят `ufw` (`MVP.md` §10.3–10.4, `deploy/README.md:303`).

```
Telegram Bot API ⇄ (long polling) bot ──► store ──► PostgreSQL ◄── store ◄── worker ──► сайты-источники (HTTP-клиент)
                                      │                                         │
                                      ├─ alert.Loop, dedup.DigestLoop,          ├─ ingest.Runner.Loop (каждые 6 ч ± jitter)
                                      │  alert.InfraLoop, bot.Notifier (outbox) ├─ dedup.Pass после применения прогона
                                      └─ heartbeat "bot" (getMe)                └─ heartbeat "worker"
```

- `runBot` (`cmd/aggregator/main.go:225-275`): конфигурация из окружения (`internal/bot/config.go:29-56`: `TELEGRAM_BOT_TOKEN` обязателен, `ADMIN_TG_IDS`, `SUPPORT_CHAT_ID`), `store.Open`, `bot.New`, затем горутины циклов и `b.Start(ctx)` — long polling (`internal/bot/bot.go:105`).
- `runWorker` (`cmd/aggregator/main.go:280-365`): планировщик источников, прогоны, дедупликация. Источники регистрируются только в `newSources` (`:198-200`).
- Healthcheck контейнеров — по отметке в таблице `heartbeats` не старше 5 минут (`cmd/aggregator/main.go:41-49`, `:137-161`); бот ставит отметку только пока отвечает `getMe` (`internal/bot/bot.go:110-118`). `runHealthcheck` принимает только `bot` и `worker` (`cmd/aggregator/main.go:138`).

### HTTP

**HTTP-сервера в проекте нет.** `net/http` импортируется в одном не-тестовом файле — `internal/ingest/fetch.go:11` — и используется только как клиент для загрузки сайтов-источников (`http.Client`, `http.Transport`, `NewRequestWithContext` — `fetch.go:60`, `:86`, `:92`, `:208`). `httptest.NewServer` встречается только в тестах. Нет REST API, маршрутизатора, middleware HTTP, CORS, авторизации по HTTP, TLS, reverse proxy, webhook (`MVP.md` §2.3 и §8.1 явно исключают webhook, домен и reverse proxy из MVP). Единственный «middleware» — обёртки библиотеки Telegram `recoverMW` и `chatMW` (`internal/bot/bot.go:69`, `:220-259`).

### Бот

- Библиотека `github.com/go-telegram/bot v1.27.0` (`go.mod:7`). Принимаются только `message` и `callback_query` (`bot.go:70`).
- Маршрутизация по префиксу `callback_data` (`bot.go:76-85`): `m` меню, `f:` фильтры, `s:` настройки, `u:` профиль, `p:` страница, `e:` карточка, `g:` поиск, `r:`/`d:` админ, `h:` поддержка, `w:` избранное. Грамматика и кодек — `internal/bot/callback.go:15-50`, строгий разбор `parseCallback` (`:284-425`), `encode` с проверкой 64 байт (`:457-520`).
- Экран — `screen{text, kb, plain, quiet}` (`internal/bot/tg.go:15-20`); отправка/правка — `send`/`show`/`showKeyboard` с повтором после 429 и откатом URL-кнопок (`tg.go:150-240`).
- Личный чат: только `chatMW` решает, чей апдейт; группы (кроме поддержки) отбрасываются (`bot.go:243-259`).
- «Сегодня» — календарная дата по Европе/Москве как полночь UTC (`bot.go:207-210`); так же хранятся `start_date`.
- Эталоны экранов — `internal/bot/testdata/screens/*.txt`, сверяются побайтно, правятся только вручную (`internal/bot/snapshot_test.go:15-69`); покрытие сценариев контрактов проверяет `TestContractCoverage` (`internal/bot/contract_test.go:14-60`).

### Данные

- Домен — `internal/event/event.go`: `Event` (`:127-158`: заголовок, даты, время, `CountryCode`, `Region`, `City`, `CityKey`, `Address`, `Type`, `Sport`, `Status`, `Description` ≤ 1000, `RegistrationURL`, `Distances`, `IsListed`, `MergedIntoID`), справочник видов спорта `Sports` с признаком `Enabled` (`:57-63`: включены `running` и `skiing`), дисциплины по видам `SportTypes` (`:74-77`), `Filter` (`internal/event/filter.go:17-29`). **Поля изображения/фото у события нет.**
- Весь SQL — только в `internal/store` (`store.go:1-4`). Видимость в выдаче: `is_listed AND merged_into_id IS NULL` + включённые виды спорта (`listing.go:24-58`, `facets.go:31-35`). Корень слияний — `RootEventID` (`listing.go:200-222`), общий для бота, дедупликации и избранного.
- Схема — `migrations/00001…00010`. Таблицы: `sources`, `ingest_runs`, `events`, `source_events`, `event_distances`, `dedup_candidates`, `users` (`tg_id`, `filters jsonb`, `filters_v`, метки времени), `heartbeats`, `source_alerts`, `event_merges`/`event_merge_records`/`event_merge_chain`, `user_profiles` (`00006`), `support_messages`/`support_awaiting` (`00007`), `events.sport` (`00008`), формат `event_type` (`00009`), `favorites` и `outbox` (`00010`).
- Поток данных Mini App (будущий): браузер Telegram → HTTPS → новый HTTP-процесс → `store` → PostgreSQL. Бот и worker не затрагиваются.

### Избранное, уведомления, профиль, «календарь»

- Избранное: `store/favorites.go` (`AddFavorite` с пределом и проверкой видимости `:68-106`, `RemoveFavorite` `:111-120`, `IsFavorite` `:124-133`, `ListFavorites` `:138-164`); строка `users` избранному не нужна (`:30-31`). Экраны и предел 50 — `internal/bot/favorites.go:18-32`.
- Очередь `outbox` пишет применение прогона и служебная часть бота; отправляет только бот — `bot.Notifier` (`internal/bot/notify.go:90-174`). Mini App к очереди не обращается.
- Профиль: `store/profiles.go` (`PeekProfile` без записи `:116-125`, `UpdateProfileAt` с версией `:140-199`, строка создаётся только в `StartUser` при `/start` `:71-100`). Справочники значений анкеты — `internal/bot/callback.go:139-146`.
- **Экрана «календарь» в боте нет.** Есть фильтр дат: периоды «этот/следующий месяц», месяцы на год вперёд, пресет 30 дней (`internal/bot/filters.go:459-497`, `:766-783`). Запроса «число событий по дням» в `store` нет.

### Тесты и конфигурация

- Интеграционные тесты — на PostgreSQL из `docker-compose.test.yml` (порт только `127.0.0.1:5433`, `trust`, tmpfs), `TEST_DATABASE_URL` из `.env.example:8`; без переменной тесты пропускаются; имя базы обязано оканчиваться на `_test` (`internal/store/storetest/storetest.go:21-58`). Бот тестируется против фиктивного Bot API на `httptest` (`internal/bot/bot_test.go:40-100`, `newBotWith` с `WithServerURL`, `WithSkipGetMe`, `WithNotAsyncHandlers` `:157-159`); фикстура каталога — HTML Марафонца (`:33-35`). В `internal/bot` 27 тестовых файлов, ~190 тестов.
- Переменные окружения — `.env.example`; CI отсутствует (`MVP.md` §3.3: «CI в MVP не входит»; каталогов `.github`/`.gitlab-ci.yml` нет).
- Безопасность: секреты только в окружении (`MVP.md` §12.1); о пользователях хранятся Telegram ID, настройки поиска, ответы анкеты, связки поддержки, избранное и очередь — без username, имени, телефона, текста обращений (`BOT-08` в `.planning/REQUIREMENTS.md:52`, `PROJECT.md:58`, `MVP.md` §8.5, §12.2); в логах нет текста запросов, названий стартов и идентификаторов пользователей (`FAV-11`, `internal/bot/favorites.go:139`). Правила деплоя — `deploy/README.md` («Логи и секреты» `:282-286`, «Правила» `:298-304`).

### HTML-прототип

В репозитории HTML-прототипа Mini App **нет**: `*.html` встречаются только в `testdata/` источников, `data/raw/` и `.planning/phases/05-*/samples/`. Каталогов `web/`, `static/`, `frontend/` нет. Прототип — ориентир по интерфейсу вне репозитория, не источник данных (так и записано в `ROADMAP.md`, Phase 15).

## 2. Что можно переиспользовать

Готово и переиспользуется без изменений (пакет `internal/store`, всё только через `*store.Store`):

| Потребность Mini App | Метод | Файл |
|---|---|---|
| Каталог с фильтрами, пагинация | `ListEvents`, `CountEvents` | `internal/store/listing.go:95-124`, `:157-164` |
| Карточка | `RootEventID`, `GetEvent`, `EventSources` | `listing.go:200-222`, `events.go:143-157`, `listing.go:171-189` |
| Фасеты фильтров | `TopCities`, `Countries`, `EventTypes`, `CityName`, `SimilarCities` | `facets.go:53-71`, `:145-165`, `:124-140`, `:111-120`, `:80-107` |
| Текстовый поиск (кандидаты) | `SearchCandidates` | `listing.go:130-154` |
| Избранное | `AddFavorite`, `RemoveFavorite`, `IsFavorite`, `ListFavorites` | `favorites.go:68-164` |
| Профиль (чтение/правка) | `PeekProfile`, `UpdateProfileAt` | `profiles.go:116-125`, `:140-199` |
| Настройки поиска (чтение без записи) | `PeekFilters` | `users.go:35-44` |
| Подключение, healthcheck, heartbeat | `store.Open`, `Healthcheck`, `Beat`, `HeartbeatAges`; `heartbeat.Run` | `store.go:38-48`, `:86-92`; `heartbeats.go:11-22`; `internal/heartbeat/heartbeat.go:19` |
| Домен и фильтр | `event.Event`, `event.Filter`, `event.Sports`, `event.SportTypes`, `event.ValidURL` | `internal/event/event.go`, `filter.go`, `watch.go:20-26` |
| Ключ города из текста | `normalize.CityKey` | `internal/normalize/` (используется в `bot/filters.go:1258`) |
| Образ, подкоманды, конфигурация из окружения | `main.go`, `Dockerfile`, якоря `x-app`/`x-db-env` compose | `cmd/aggregator/main.go`, `Dockerfile`, `docker-compose.yml:22-60` |
| Тестовая база и фикстуры | `storetest.Open`, фикстура Марафонца, `httptest` | `storetest.go`, `bot_test.go:33-35` |

Логика, которая нужна Mini App, но живёт в `internal/bot` **неэкспортированной** (переиспользовать можно только через небольшой экспорт либо перенос):

- корзины дистанций по видам спорта `buckets`, `skiBuckets`, `sportBuckets` — `internal/bot/filters.go:134-162`;
- подписи дисциплин `typeLabels`, порядок меню `menuTypes` — `filters.go:164-197`; названия стран `countryNames`, флаг `countryFlag` — `internal/bot/country.go:10-40`; названия источников `sourceNames` — `list.go:254`;
- периоды дат `dateRange`, `monthOptions` — `filters.go:462-497`; страна по умолчанию `defaultCountry = "RU"` — `filters.go:28`;
- формат фильтров пользователя `userFilters`/`loadFilters` (jsonb `users.filters`) — `filters.go:34-62`, `:205-274`;
- показываемая часть адреса `visibleAddress` — `internal/bot/place.go:18-32`;
- поиск по словам `stems`/`matchEvents` — `internal/bot/search.go:54-166`;
- предел избранного `favLimit = 50` и тексты исходов — `internal/bot/favorites.go:19-41`;
- справочники анкеты `profileGenders`, `profileAges`, `profileCountries`, `profileSports` — `callback.go:139-146`;
- «сегодня» по Москве — `bot.go:207-210` (три строки, можно повторить).

Поддержка Mini App в библиотеке Telegram (проверено по исходникам модуля `go-telegram/bot@v1.27.0` в кэше модулей):

- `models.InlineKeyboardButton.WebApp *WebAppInfo` (`models/reply_markup.go:45`) и `models.KeyboardButton.WebApp` (`:79`); `models.WebAppInfo{URL}` (`models/web_app.go:10-12`);
- кнопка меню: `models.MenuButtonWebApp{Text, WebApp}` (`models/menu_button.go:80-87`), метод `Bot.SetChatMenuButton` (`methods.go:624`), параметры `SetChatMenuButtonParams{ChatID, MenuButton}` (`methods_params.go:786-789`);
- проверка `initData`: `bot.ValidateWebappRequest(values url.Values, token string)` (`common.go:104-143`) — считает HMAC правильно, но **не проверяет `auth_date`**, сравнивает хеш обычным `!=` (не константное время) и дополнительно `url.QueryUnescape` уже декодированных значений (`:114`) — при значении с символом `%` даст ложный отказ. Поэтому для сервера лучше своя функция на стандартной библиотеке (раздел 6), ~40 строк.

## 3. Пробелы и риски

1. **Нет HTTP-сервера, TLS, домена.** Telegram открывает Mini App только по HTTPS с валидным сертификатом. Это инфраструктурное изменение (домен, сертификат, публикация порта, reverse proxy или TLS в Go) — не предусмотрено `MVP.md` §8.1/§10.4 и требует отдельного согласования владельца (раздел 9).
2. **Публикация порта обходит `ufw`** (`deploy/README.md:303`); сейчас проверка `agg verify` гарантирует «ничего не опубликовано». Новая проверка должна различать «опубликован только порт прокси/TLS» и «опубликована БД».
3. **Фото событий отсутствуют** в модели и в источниках (`event.Event`, `MVP.md` §12.2: изображения не копируются). Экраны с фото в дизайне — пробел; источник фото не придумывается. Варианты на решение владельца: заглушка по виду спорта/дисциплине (значок), без фото.
4. **«Календарь» не имеет запроса в `store`**: нужен новый метод «число видимых событий по датам в диапазоне» (только чтение, без изменения схемы).
5. **Справочники и часть логики заперты в `internal/bot`** (список в разделе 2). Без экспорта Mini App либо дублирует их (две копии корзин и подписей разойдутся), либо требует правки бота. Рекомендация — один экспортируемый аксессор в `internal/bot` (раздел 7, этап 2), без изменения поведения бота.
6. **Строка `users` и `user_profiles` создаются только из бота** (`Filters`/`touchUser` `users.go:18-30`, `StartUser` `profiles.go:71`). Пользователь, открывший Mini App без `/start`, не имеет профиля; избранному строка не нужна (`favorites.go:30-31`). Решение: Mini App ничего не создаёт в `users`/`user_profiles` (только `Peek*`), правка профиля — через бота или отдельным решением (раздел 9).
7. **Фильтры пользователя** хранятся в `users.filters` в формате бота; синхронизация фильтров между ботом и Mini App потребовала бы записи через `UpdateFilters` и знания формата `userFilters`. В первой версии состояние фильтров Mini App хранится на клиенте (Telegram `CloudStorage`/`localStorage`), в базу не пишется.
8. **Доверие к данным браузера.** Любой запрос может прийти не из Telegram. Единственный способ узнать пользователя — проверенный `initData` (раздел 6). Без него нельзя ни читать избранное, ни писать.
9. **Повтор `initData`.** Проверенная строка действует до истечения `auth_date` — утечка строки (логи, реферер) равна утечке сессии. Следствия: не логировать `initData` и заголовок авторизации, не передавать его в query-string, окно срока жизни — конечное.
10. **Нагрузка и злоупотребления.** Сейчас у каталога один «клиент» — бот с лимитами Telegram. Открытый API можно опрашивать чаще. Нужны ограничения `http.Server` (таймауты, размер тела), предел `limit`, ограничение частоты на пользователя (раздел 5).
11. **Логи.** Правило «без текста запросов, названий и идентификаторов пользователей» (`FAV-11`, `BOT-08`) распространяется на HTTP-лог: не писать `q`, `initData`, `tg_id`; писать метод, шаблон маршрута, код ответа, длительность.
12. **Процесс и образ.** `Dockerfile` копирует только `cmd`, `internal`, `migrations` (`Dockerfile:14-17`); каталог со статикой нужно добавить в `COPY`. Изменение `docker-compose.yml` или пакета `deploy/vps/*` останавливает `ship.sh` на сверке SHA256 и требует повторной установки пакета (`INSTALL`) владельцем.
13. **Готовых инструментов фронтенда нет** (нет `package.json`, Node, сборщика). Добавление фреймворка/сборщика — новая зависимость, по правилу `MVP.md` §3.1 требует одобрения.
14. **Таблица `favorites` не входит в `TRUNCATE` тестовой базы** (`storetest.go:50-52`): очищается каскадом через `events`; при самостоятельных тестах API на избранное учитывать (как в `internal/bot/fav_test.go:303` — `DELETE FROM favorites`).
15. **Планирование.** Каталог `.planning/phases/15-mini-app/` до этого документа не существовал; в рабочей копии есть незакоммиченное переименование `15-catalog-quality` → `16-catalog-quality` и правка `ROADMAP.md` (снимок `git status`), выполненные вне этого исследования.

## 4. Варианты архитектуры Mini App и рекомендация

Общее для всех вариантов: Telegram требует HTTPS → нужны домен и сертификат; статика и API удобнее на одном origin (тогда CORS не нужен вовсе); проверка `initData` требует токена бота либо производного ключа.

### Вариант А. API внутри процесса `bot`

`runBot` дополнительно поднимает `http.Server` при заданном `WEBAPP_ADDR`.

- Плюсы: минимум нового кода (нет новой подкоманды), токен уже в процессе, одно изменение compose (порт/`expose`).
- Минусы: HTTP-поверхность в процессе, который держит токен и long polling; перегрузка или паника в HTTP-части влияет на доставку сообщений (`Notifier`) и отметку heartbeat; стоп-грейс бота (20 с) и ожидание обработчиков (`Wait`) придётся согласовывать с `Server.Shutdown`; healthcheck бота привязан к `getMe` — сбой HTTP он не увидит.

### Вариант Б. Новая подкоманда `aggregator webapp` в том же бинарнике и образе, отдельный сервис compose

Процесс: `store.Open` → `http.Server` (API + статика из `embed.FS`) → heartbeat `webapp`. Соответствует принципу «один бинарник, подкоманды; один образ, разные `command`» (`cmd/aggregator/main.go:1-3`, `MVP.md` §3).

- Плюсы: изоляция от бота (токен бота в контейнер можно не передавать — достаточно производного ключа, раздел 6); независимые перезапуск, healthcheck, `stop_grace_period`; те же якоря `x-app` (read-only, nonroot, cap_drop) без правок; тесты API — обычные `httptest` + `storetest`.
- Минусы: новая строка `services:` и якорь переменных в compose; новая подкоманда `healthcheck webapp`; переустановка пакета `agg` (INSTALL) — но это нужно и в варианте А, потому что compose меняется в любом случае.

### Вариант В. Отдельный API-процесс вне бинарника (другой сервис/репозиторий)

Отдельный модуль Go или другой язык.

- Плюсы: полная независимость релизов.
- Минусы: дублирование `store`, модели и правил видимости/слияний (`RootEventID`, `sportVisible`), второй образ, второй пакет деплоя, две точки правды для схемы. Противоречит `MVP.md` §3 (без микросервисов).

### Отдача статики

1. Из того же Go-процесса через `embed.FS` + `http.FileServerFS` (Go ≥ 1.22; у проекта 1.27) — стандартная библиотека, один origin с API, нет CORS, нет отдельного образа.
2. Из reverse proxy (Caddy/nginx) с тома — лишний канал доставки файлов (ship.sh переносит только образ), прокси и так нужен лишь для TLS.

### TLS

1. Reverse proxy с автоматическим сертификатом (например, контейнер Caddy) — новый образ на VPS и публикация 80/443 из него; Go-процесс слушает только во внутренней сети (`expose`, не `ports`).
2. TLS в Go через `autocert` — новая зависимость `golang.org/x/crypto`, прямой доступ к 443 из контейнера приложения (требует `cap_net_bind_service` или порта > 1024 с перенаправлением) — больше правок базовых якорей безопасности.
3. Сертификат, выданный вручную и смонтированный в контейнер — прост, но срок жизни и ротация на владельце.

### Рекомендация

**Вариант Б + статика из `embed.FS` + TLS на reverse proxy, выбранном владельцем.** Основания: архитектура уже рассчитана на подкоманды одного бинарника; `store` переиспользуется целиком; бот не меняется до последнего этапа; токен бота остаётся только у `bot`; API изолирован и проверяется теми же средствами (`storetest`, `httptest`). Вариант А — допустимый запасной путь, если владелец откажется от нового сервиса compose; вариант В не рекомендуется.

Фронтенд — без фреймворка и сборщика: одна HTML-страница, CSS и несколько модулей JS в `web/`, скрипт Telegram `telegram-web-app.js` (обязателен для `initData`, темы и кнопок Telegram). Семь экранов и тёмная тема не требуют фреймворка; добавить его можно позже отдельным решением (новая зависимость — `MVP.md` §3.1).

## 5. Структура фронтенда и контракты API

### Структура

```
web/
  embed.go          // package web; //go:embed static  → web.FS (как migrations/embed.go)
  static/
    index.html      // одна страница, тёмная тема, lang="ru"
    app.css         // палитра из themeParams Telegram с тёмными значениями по умолчанию
    app.js          // маршрутизация по hash: #/ #/catalog #/filters #/event/<id> #/calendar #/favorites #/profile
    api.js          // fetch с заголовком Authorization: tma <initData>, разбор ошибок
    screens/*.js    // по модулю на экран (главная, каталог, фильтры, карточка, календарь, избранное, профиль)
internal/webapp/
  auth.go           // проверка initData (раздел 6)
  server.go         // http.ServeMux, обработчики, общие ответы, ограничения
  dict.go           // JSON-представление справочников (из bot.Catalog(), см. этап 2)
```

Экраны по согласованному дизайну → данные:

| Экран | Данные | Источник |
|---|---|---|
| Главная | виды спорта с числом будущих событий, вход в каталог/календарь/избранное/профиль | `GET /meta` |
| Каталог | список с пагинацией, строка активных фильтров | `GET /events` |
| Фильтры | страна, город (топ-10 + подсказка по тексту), даты (периоды/месяцы), дистанция (корзины вида спорта), дисциплина | `GET /meta`, `GET /cities`, `GET /cities/suggest` |
| Карточка | все поля карточки бота (`list.go:279-346`) + состояние избранного | `GET /events/{id}` |
| Календарь | число событий по дням месяца, по дню — список | `GET /calendar` (новый метод store) + `GET /events?from=&to=` |
| Избранное | будущие избранные старты, добавить/убрать | `GET /favorites`, `PUT/DELETE /favorites/{id}` |
| Профиль | ответы анкеты (только чтение в v1), ссылка «изменить в боте» | `GET /profile` |

Фото в карточке — отсутствует в данных (раздел 3, п. 3): в макете место фото занимает значок вида спорта/дисциплины, пока владелец не решит иначе.

### Общие правила API

- Префикс `/api/v1`; JSON UTF-8; даты — `YYYY-MM-DD`; дистанции — метры; коды стран ISO alpha-2; коды видов спорта и дисциплин — как в `event.Sports`/`event.SportTypes`.
- Версионирование — префиксом пути; добавление полей без смены версии; удаление/переименование — только `/api/v2`.
- Авторизация: заголовок `Authorization: tma <initData>` на **всех** запросах (и на чтении каталога) — ограничивает поверхность до клиентов Telegram и даёт ключ для ограничения частоты. Ошибка проверки → `401 {"error":"unauthorized"}`; `initData` никогда не передаётся в query-string и не логируется.
- Ограничение частоты: по `tg_id`, в памяти процесса, например 60 запросов/мин на чтение и 20/мин на запись → `429` с `Retry-After`. Упрощение: счётчик в памяти одного процесса; при нескольких экземплярах — перенести в базу или прокси.
- Защита сервера: `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxBytesReader` на тело (API принимает тело только у `PATCH /profile`, если он появится), предел `limit ≤ 50`, `page ≤ 10000` (как `maxPage` в `callback.go:281`).
- Ошибки: `{"error":"<код>"}` с кодами `unauthorized`, `bad_request`, `not_found`, `gone`, `rate_limited`, `internal`; текст ошибок базы наружу не уходит (как в боте — `errNotice`).
- Логи: `slog` как в остальном проекте (`main.go:55-60`); поля — метод, шаблон маршрута, код, длительность; без `q`, без `tg_id`, без заголовков.
- Кэширование: ответы чтения каталога — `Cache-Control: private, max-age=60`; статика — `max-age` с хешем версии в имени файла или `no-cache` (Упрощение: `no-cache` в первой версии).

### Эндпоинты (v1)

Только чтение (этапы 2, 5, 6):

- `GET /api/v1/meta` → `{ "today":"YYYY-MM-DD", "sports":[{"code","title","emoji","count"}], "types":{"running":[{"code","label"}],…}, "buckets":{"running":[{"i","label","lo","hi"}],…}, "countries":[{"code","name","count"}], "months":[{"code":"m202611","label":"ноябрь 2026"}], "limits":{"page_size":20,"favorites":50} }`. Источники: `event.Sports`, `CountEvents` по виду спорта (как `menuSports`, `filters.go:1091-1108`), `Countries` (`facets.go:145`), справочники из `bot.Catalog()`.
- `GET /api/v1/events` — параметры: `sport` (код; пусто — все включённые), `country` (код; `*` — все; по умолчанию `RU` как `defaultCountry`), `city` (ключ, допустим только вместе со страной ≠ `*` — как `filters.go:240-243`), `type`, `dist` (индексы корзин через запятую, корзины выбранного вида спорта), `periods` (`cm,nm,mYYYYMM` — семантика `dateRange`), `from`/`to` (явный диапазон; взаимоисключим с `periods`), `page` (с 1), `limit` (по умолчанию 20, ≤ 50). Ответ: `{ "total", "page", "pages", "items":[{"id","title","start_date","country_code","city","status","sport","distances":[m…]}] }` — поля `store.ListItem` (`listing.go:61-71`). Отбор — `event.Filter` через `ListEvents`/`CountEvents`; `From` не раньше «сегодня».
- `GET /api/v1/events/{id}` → корень через `RootEventID`; невидимое/слитое/выключенного вида спорта/несуществующее → `404 {"error":"gone"}` (как `buildCard`, `list.go:213-222`). Ответ: `{ "id","title","status","sport","type","type_label","start_date","end_date","start_time","timezone","country_code","country_name","city","address" (visibleAddress),"description","registration_url" (только если `event.ValidURL`),"distances":[{"m","label"}],"sources":[{"code","name","url"}],"is_favorite":bool }`.
- `GET /api/v1/cities?sport=&country=&…те же фильтры…&limit=10` → `TopCities` → `[{"key","name","count"}]`.
- `GET /api/v1/cities/suggest?country=&q=` → `SimilarCities(country, normalize.CityKey(q), q, today, 5)` → тот же формат; `q` ≤ 64 символов (`maxCityInput`, `filters.go:1206`).
- `GET /api/v1/calendar?sport=&country=&from=&to=` → новый метод store (`CountByDay`): `[{"date","count"}]` по видимым событиям; диапазон ≤ 62 дней.
- `GET /api/v1/search?q=&country=&limit=20` → `SearchCandidates` + экспортированное сопоставление слов из `internal/bot/search.go` → `{ "total", "items":[…ListItem…], "cities":[…] }` (этап 6).

Чтение и запись по пользователю (этап 4; `tg_id` только из проверенного `initData`):

- `GET /api/v1/favorites?page=&limit=` → `ListFavorites` → `{ "total","page","pages","items":[…ListItem без distances…] }`.
- `PUT /api/v1/favorites/{id}` → `AddFavorite(tgID, id, today, now, 50)`; ответ `200 {"outcome":"added"|"already"|"limit"|"past"}` либо `404 {"error":"gone"}` (`store.FavOutcome`, `favorites.go:13-27`). Без тела запроса; идемпотентно.
- `DELETE /api/v1/favorites/{id}` → `RemoveFavorite`; всегда `204`.
- `GET /api/v1/profile` → `PeekProfile`; нет строки → `{"exists":false}`; есть → `{ "exists":true, "status","gender","age_group","country","city","sports":[…], "v" }` (без служебных `awaiting_*`).
- `PATCH /api/v1/profile` — **не в v1** (требует решения о создании строки профиля вне `/start` и о правилах допустимых значений; при включении — `UpdateProfileAt` с `v` из клиента, конфликт версии → `409`).

Что не выносится в API: админские данные (`/runs`, `/dupes`, `/stats`), поддержка, очередь `outbox`, записи `users.filters`.

## 6. Проверка `initData` на сервере

Где живёт: `internal/webapp/auth.go` — одна функция вида «строка `initData`, секрет, «сейчас», окно → (`tg_id`, ошибка)» и middleware, который кладёт `tg_id` в `context`. Только стандартная библиотека: `net/url`, `sort`, `crypto/hmac`, `crypto/sha256`, `crypto/subtle`, `encoding/hex`, `encoding/json`, `strconv`, `time`.

Порядок шагов (по документации Telegram «Validating data received via the Mini App»):

1. Взять строку из заголовка `Authorization: tma <initData>` (схема `tma`); пустая/иная схема → отказ. Query-string не принимать.
2. Ограничить длину строки (например, 4 КиБ) до любого разбора.
3. Разобрать как `application/x-www-form-urlencoded` (`url.ParseQuery`): получить пары ключ → значение (однократное декодирование). Повторяющиеся ключи → отказ.
4. Извлечь и удалить `hash` (64 hex-символа; иначе отказ). Если есть `signature` (Ed25519 для сторонних серверов) — игнорировать: сервер проверяет HMAC, потому что знает токен.
5. Собрать `data_check_string`: все оставшиеся пары как `key=value` (значения — уже декодированные), отсортировать по ключу в байтовом порядке, соединить `\n`.
6. Секрет: `secret_key = HMAC_SHA256(key = "WebAppData", message = bot_token)`. Вычислять один раз при старте процесса из `TELEGRAM_BOT_TOKEN` **либо** принимать уже производный ключ из окружения (`WEBAPP_SECRET`, hex), чтобы контейнер `webapp` не держал токен бота. Производный ключ позволяет подделать `initData`, но не позволяет действовать от имени бота — он менее опасен, но всё равно секрет (`.env`, 600, не логировать).
7. Ожидаемый хеш: `HMAC_SHA256(key = secret_key, message = data_check_string)` в hex; сравнить с `hash` через `subtle.ConstantTimeCompare` (после `hex.DecodeString`).
8. Только после совпадения хеша: разобрать `auth_date` (unix-секунды); отказать, если поле отсутствует, не число, в будущем более чем на допуск (например, 5 минут) или старше окна (предложение — 24 часа: `initData` обновляется при каждом открытии Mini App; окно короче ломает долгие сессии, окно длиннее увеличивает цену утечки). Окно — константа в коде с пометкой, при необходимости — переменная окружения.
9. Только после шагов 7–8: разобрать `user` (JSON) и взять `id` (int64 > 0). Больше ничего из `user` не сохранять и не логировать (`BOT-08`): `first_name`, `username`, `language_code`, `photo_url` используются только на клиенте для приветствия. `query_id`, `start_param` — не нужны в v1.
10. Результат — `tg_id` в `context` запроса; обработчики читают только его. Ошибка любого шага → `401` без различения причины в ответе (в лог — код причины без значений).

Тесты (`auth_test.go`): положительный вектор, собранный в тесте из тестового токена и фиксированных полей (хеш вычисляется теми же примитивами — проверяет сортировку и формат); отрицательные: изменённое поле, лишний/повторный ключ, неверная длина хеша, просроченный `auth_date`, `auth_date` из будущего, `user.id ≤ 0`, слишком длинная строка; сравнение с результатом `bot.ValidateWebappRequest` из библиотеки на том же векторе (как перекрёстная проверка HMAC).

На клиенте: `initData` берётся из `window.Telegram.WebApp.initData` при каждом запросе; при `401` — показать «откройте приложение из Telegram заново»; страница вне Telegram (пустой `initData`) показывает только заглушку.

## 7. План реализации малыми этапами

Этапы независимы от деплоя до этапа 8; каждый оставляет бота без изменений поведения (кроме этапа 7). Коммиты, образ и выкат — по правилам проекта и только с разрешения владельца.

### Этап 0. Согласования (без кода)

Цель: получить решения владельца из раздела 9 (домен, TLS/прокси, порт, переменные, фото, профиль). Критерий готовности: записанные решения в `15-CONTEXT.md` (создаёт владелец/обсуждение фазы).

### Этап 1. Проверка `initData`

- Создать: `internal/webapp/auth.go`, `internal/webapp/auth_test.go`.
- Изменить: ничего.
- Тесты: раздел 6; `go test ./internal/webapp/`.
- Готово: все векторы проходят; функция не зависит от `net/http` (чистая), middleware — отдельная функция.

### Этап 2. Подкоманда `webapp` и API только чтения (meta, events, event, cities)

- Создать: `internal/webapp/server.go` (`http.ServeMux` с шаблонами методов `GET /api/v1/…`, обработчики, JSON-ответы, таймауты, предел `limit`, ограничение частоты), `internal/webapp/dict.go`, `internal/webapp/server_test.go`.
- Изменить: `cmd/aggregator/main.go` — `runWebapp` (переменные `WEBAPP_ADDR`, `WEBAPP_SECRET` либо `TELEGRAM_BOT_TOKEN`; `openStore`; heartbeat `webapp`; `Server.Shutdown` по ctx), `runHealthcheck` принимает `webapp` (`main.go:138`), `usage`. `internal/bot` — один экспортируемый аксессор (например, `bot.Catalog()`), возвращающий корзины по видам спорта, подписи дисциплин, названия стран и источников, страну по умолчанию, предел избранного, и экспорт `VisibleAddress` (переименование `visibleAddress`, `place.go:18`); поведение бота не меняется, эталоны не трогаются. `.env.example` — описание новых переменных (без значений).
- Тесты: `httptest.NewServer` над `webapp.New(store, …)` + `storetest.Open` + фикстура Марафонца (как `bot_test.go`): `401` без заголовка; `meta` содержит оба включённых вида спорта; `events` — пагинация, фильтр страны/города/корзины/периода совпадает с `CountEvents` при тех же `event.Filter`; `events/{id}` для слитого события возвращает корень, для скрытого — `404 gone`; `limit` > 50 → `400`; ограничение частоты → `429`. Тест в `cmd/aggregator/main_test.go` на разбор аргументов `healthcheck webapp`.
- Готово: `go build`, `go vet`, тесты; `aggregator webapp` локально отвечает на `GET /api/v1/meta` с валидным `initData`, собранным тестовой утилитой; существующие тесты бота без изменений.

### Этап 3. Статика и экраны: главная, каталог, фильтры, карточка

- Создать: `web/embed.go`, `web/static/index.html`, `app.css`, `app.js`, `api.js`, `screens/{home,catalog,filters,event}.js`.
- Изменить: `internal/webapp/server.go` — `http.FileServerFS(web.FS)` на `/` (c `index.html` для всех не-API путей), заголовки `Cache-Control`; `Dockerfile:14-17` — `COPY web ./web`; `.dockerignore` не меняется.
- Тесты: Go-тест «корень отдаёт `index.html` с `telegram-web-app.js` и без inline-секретов»; `go vet`; ручная проверка в браузере вне Telegram — заглушка «откройте из Telegram»; проверка в Telegram — только после этапа 8 (нужен HTTPS) либо через HTTPS-туннель на локальную машину по решению владельца.
- Готово: образ собирается `deploy/smoke.sh` без изменений смока (сервис `webapp` в смок не входит до этапа 8); тёмная тема, русский интерфейс, навигация по четырём экранам.

### Этап 4. Избранное и профиль (чтение)

- Создать: `web/static/screens/{favorites,profile}.js`.
- Изменить: `internal/webapp/server.go` — `GET/PUT/DELETE /api/v1/favorites…`, `GET /api/v1/profile`; `events/{id}` получает `is_favorite`; `server_test.go`.
- Тесты: исходы `added/already/limit/past/gone` (повтор логики `internal/bot/fav_test.go`, с `DELETE FROM favorites` в начале), удаление идемпотентно, `GET /profile` без строки → `exists:false`, `tg_id` берётся только из `initData` (подмена `id` в теле/пути невозможна по контракту — проверка, что путь не содержит `tg_id`).
- Готово: экраны «Избранное» и «Профиль» работают на тестовой базе; в логах нет `tg_id` (тест по образцу `TestNotifyPrivacy`).

### Этап 5. Календарь

- Создать: `web/static/screens/calendar.js`.
- Изменить: `internal/store/listing.go` или новый `internal/store/calendar.go` — `CountByDay(ctx, f event.Filter) ([]DayCount, error)` на `listWhere()` + `GROUP BY e.start_date` (без изменения схемы; индекс `idx_events_listed_start_date_id` уже есть), `listing_test.go`; `server.go` — `GET /api/v1/calendar`.
- Тесты: store — счётчики по дням совпадают с `CountEvents` по каждому дню фикстуры; API — диапазон > 62 дней → `400`.
- Готово: экран календаря показывает месяц с числами событий, по дню — список через `GET /events?from=&to=`.

### Этап 6. Текстовый поиск

- Изменить: `internal/bot/search.go` — экспорт сопоставления (`SearchWords`, `MatchEvents`) без изменения логики; `server.go` — `GET /api/v1/search`; тесты API на фикстуре (те же случаи, что `TestS05*`).
- Готово: результаты API совпадают с результатами бота на фикстуре для 3–5 запросов.

### Этап 7. Вход из бота

- Изменить: `internal/bot/config.go` — необязательная `WEBAPP_URL` (только `https://`, иначе ошибка старта); `internal/bot/filters.go:1066-1082` `mainMenu` — при заданном URL ряд с `InlineKeyboardButton{Text, WebApp: &models.WebAppInfo{URL}}`; новые эталоны экранов (`s01_menu` с кнопкой — отдельный эталон при заданном URL, старые не меняются); `renderCall` (`snapshot_test.go:33-41`) — вывод `web_app:<url>`; при согласии владельца — `SetChatMenuButton` один раз при старте бота (нулевой вариант без кода: кнопка меню задаётся владельцем в BotFather).
- Тесты: `config_test.go` (валидация URL), снимок меню с кнопкой и без; `TestContractCoverage` не затрагивается (новый сценарий — в контракте экранов Phase 15 после приёмки владельцем).
- Готово: без `WEBAPP_URL` поведение и эталоны бота прежние побайтно.

### Этап 8. Деплой (только документы и пакет; выполнение — владелец)

- Изменить: `docker-compose.yml` — сервис `webapp` на якоре `x-app` (переменные `DATABASE_URL`, `PGPASSWORD`, `LOG_FORMAT`, `WEBAPP_ADDR`, `WEBAPP_SECRET`; `expose`, не `ports`; healthcheck `healthcheck webapp`) и сервис reverse proxy по решению владельца (единственный с `ports: 80/443`); `deploy/vps/deploy-verify.sh` — проверка «опубликованы только порты прокси», `deploy/vps/deploy-preflight.sh:37` — новые ключи `.env`; `deploy/vps/env-init.sh` — не меняется (ключ добавляется однострочником, как `SUPPORT_CHAT_ID`); `deploy/README.md` — раздел «Phase 15» с шагами по образцу Phase 11/14; `deploy/smoke.sh` — проверка `webapp` в смоке.
- Готово: `ship.sh` останавливается на сверке SHA256 (ожидаемо), владелец выполняет `INSTALL`, `deploy`, `verify`; `DEPLOY VERIFY OK` с новыми строками; Mini App открывается из бота.

## 8. Стратегия тестирования

- **Единичные тесты без базы**: `initData` (раздел 6), разбор параметров фильтров → `event.Filter` (таблица случаев, включая `city` при `country=*`, корзины по виду спорта, периоды), JSON-представление справочников, ограничитель частоты (детерминированные часы, как `Bot.Now`).
- **Интеграционные с PostgreSQL** (`TEST_DATABASE_URL`, `docker-compose.test.yml`, порт 127.0.0.1:5433): обработчики через `httptest` на `storetest.Open` с фикстурой Марафонца, проверка совпадения результатов API с `store` напрямую и с поведением бота (та же фикстура даёт те же `total` и id, что в эталонах `list_page1.txt`, `card.txt`).
- **Приватность**: тест-двойник `TestNotifyPrivacy` для HTTP-лога (захват `slog` в буфер, поиск `tg_id`, `q`, заголовка авторизации).
- **Инварианты видимости**: скрытое, слитое, прошедшее, выключенного вида спорта событие — `404 gone` во всех точках, где бот показывает «Событие больше не доступно»; слитое → корень.
- **Эталоны ответов API**: по примеру экранов — JSON-снимки `internal/webapp/testdata/*.json`, сверка побайтно после нормализации; правятся вручную.
- **Бот**: после этапа 7 — существующие эталоны без изменений (побайтно) при пустом `WEBAPP_URL`; новый снимок меню с кнопкой.
- **Образ и состав**: `deploy/smoke.sh` расширяется проверкой `webapp` (запуск, `healthcheck webapp`, `GET /api/v1/meta` → `401` без заголовка).
- **Ручная приёмка владельцем** в Telegram (тёмная тема, семь экранов, добавление/удаление избранного и его отражение в боте) — чек-лист в документе приёмки фазы, по образцу `14-ACCEPTANCE.md`.
- Нагрузочная проверка — не нужна на этом масштабе (сотни событий, `outbox.go:23-25`); при росте — отдельное решение.

## 9. Действия, требующие отдельного согласования владельца

1. **Домен и DNS** для Mini App (Telegram требует HTTPS) — покупка/делегирование, запись A/AAAA; имя в репозиторий не записывается (`MVP.md` §12.1).
2. **TLS**: выбор между reverse proxy с автоматическими сертификатами (новый образ на VPS), `autocert` в Go (новая зависимость), ручным сертификатом; где хранится ключ; кто продлевает.
3. **Публикация портов 80/443** с VPS — впервые в проекте; обход `ufw` портами Docker; правка проверки `agg verify`; решение, что порт PostgreSQL по-прежнему не публикуется (это сохраняется).
4. **Новый сервис compose `webapp`** (и прокси) — изменение `docker-compose.yml`, пакета `deploy/vps/*`, повторная установка пакета (`INSTALL`), `deploy/README.md`.
5. **Новые переменные окружения** в `/opt/aggregator/.env`: `WEBAPP_ADDR`, `WEBAPP_SECRET` (производный ключ) **или** передача `TELEGRAM_BOT_TOKEN` в `webapp`; `WEBAPP_URL` для бота; добавление — однострочником в root-консоли по образцу Phase 11.
6. **Доступ к БД из нового процесса**: тот же пользователь `aggregator` (как `bot`/`worker`) или отдельная роль только для чтения + `favorites` — отдельная роль потребовала бы SQL вне миграций goose (решение владельца; Упрощение первой версии — тот же пользователь).
7. **Миграции**: не требуются для v1. Потребуются, если владелец захочет: признак «фото/обложка» у события; хранение настроек Mini App в базе; ключ сессии вместо `initData`.
8. **Хранение новых данных о пользователях**: план **не добавляет** ничего к `BOT-08` — `tg_id` из `initData` используется только для `favorites` и чтения `user_profiles`; `first_name`/`username`/`photo_url` из `initData` в базу и логи не попадают. Любое отклонение (например, создание строки профиля из Mini App, `PATCH /profile`, аналитика открытий) — отдельное решение и правка `BOT-08`, `PROJECT.md`, `MVP.md` §8.5.
9. **Фото событий**: отсутствуют; решение — значок/заглушка или отложить экраны с фото.
10. **Фронтенд-инструменты**: отказ от фреймворка/сборщика в v1 (рекомендация) либо явное одобрение новой зависимости.
11. **Кнопка входа**: BotFather (без кода) против кнопки в главном меню бота (изменение эталонов, приёмка экранов).
12. **Срок жизни `initData`** (окно `auth_date`, предложение 24 ч) и лимиты частоты.
13. **Изменение `MVP.md`/`PROJECT.md`**: Mini App сейчас в «не входит» (`MVP.md` §2.3, §17; `PROJECT.md:34`) — запись решения в журнал решений `MVP.md` §19.
14. Любой шаг на VPS (`INSTALL`, `deploy`, правка `.env`, установка прокси) — только по отдельному разрешению на конкретный шаг.

## 10. Резюме

**Готово и переиспользуется**: вся работа с данными (`internal/store`: выдача, карточка, фасеты, корень слияний, избранное, профиль), модель `internal/event`, образ и схема подкоманд одного бинарника, heartbeat/healthcheck, тестовая инфраструктура (`storetest`, фикстуры, `httptest`), правила приватности и логов. Библиотека Telegram поддерживает `web_app` у кнопок и `MenuButtonWebApp`; её проверка `initData` неполна (нет `auth_date`, не константное сравнение).

**Чего не хватает**: HTTP-сервера, TLS и домена, порта наружу; экспорта справочников и части логики из `internal/bot`; запроса «по дням» для календаря; фото событий (нет в данных); решений владельца по инфраструктуре и хранению.

**Рекомендованный вариант**: новая подкоманда `aggregator webapp` (API + встроенная статика на стандартной библиотеке) отдельным сервисом compose на существующих якорях безопасности; TLS — на reverse proxy по выбору владельца; фронтенд без фреймворка; все запросы API с проверенным `initData`; v1 — чтение каталога, календарь, избранное (чтение/запись), профиль (чтение); схема БД не меняется.

**Минимальное первое изменение** (после подтверждения плана): этап 1 — `internal/webapp/auth.go` с проверкой `initData` и тестами; он не трогает бота, базу, compose и может быть принят независимо от инфраструктурных решений.

---

## Решения владельца

PLAN APPROVED: 2026-10-10 — вариант Б (подкоманда `aggregator webapp`, статика из `embed.FS`, TLS на reverse proxy по выбору владельца) и этап 1 (проверка `initData`: `internal/webapp/auth.go`, `auth_test.go`).
«утверждаю вариант Б и этап 1»

Этапы 2–8 и все пункты раздела 9 — по отдельным решениям владельца.
