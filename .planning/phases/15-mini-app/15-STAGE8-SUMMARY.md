# Phase 15, этап 8 — локальная подготовка выката Mini App

Дата: 2026-10-10. База: `15-CONTEXT.md` (D-01…D-05, D-07, D-09, D-19…D-21), `15-MINI-APP-PLAN.md` (раздел 3 п. 1, 2, 12; раздел 7, этап 8; раздел 9).

**Статус: локальная подготовка выполнена, выкат не начат.** На сервер не выполнялось ничего: ни ssh, ни `ship.sh`, ни `secrets.sh`, ни `oneliner.sh` с алиасом. Коммитов нет. Код приложения (`cmd/`, `internal/`, `web/`, `migrations/`), `Dockerfile`, `go.mod`, `docker-compose.test.yml`, `docker-compose.dev.yml` не менялись.

## Что изменено по файлам

- `docker-compose.yml` — сервисы `webapp` и `caddy` за профилем `webapp`, сеть `edge`, тома `caddy_data` и `caddy_config`, комментарий-шапка. Блоки `postgres`, `migrate`, `bot`, `worker` и якоря не изменены (см. «Проверки»).
  - `webapp`: `<<: *app`, `command: ["webapp"]`, окружение `<<: *db-env` + `WEBAPP_ADDR: 0.0.0.0:8080` (константа) + `WEBAPP_SECRET: ${WEBAPP_SECRET:-}`; `expose: ["8080"]`, без `ports`; сети `default` и `edge`; `stop_grace_period: 15s` (грейс сервера 10 с); healthcheck `["CMD", "/aggregator", "healthcheck", "webapp"]` с интервалами `bot`. Токен бота, `ADMIN_TG_IDS`, `OWNER_CONTACT`, `DEDUP_*` не передаются.
  - `caddy`: образ с тегом и дайджестом, `command: ["caddy", "reverse-proxy", "--from", "${WEBAPP_DOMAIN:-localhost}", "--to", "webapp:8080"]` (без файла конфигурации), `ports: ["80:80", "443:443"]`, только сеть `edge`, тома `/data` и `/config`, `restart: unless-stopped`, `logging: *log`, `depends_on: webapp: service_healthy`, ограничения безопасности (ниже).
- `deploy/vps/deploy-verify.sh` — строка `webapp_profile=on|off`; `compose-state` при `on` дополнительно требует `webapp` (running, healthy) и `caddy` (running); новый раздел «Опубликованные порты»: строки `published=<сервис> <порт>/<протокол>` либо `published=none`, проверки `CHECK ports-only-caddy` (порты есть только у `caddy`) и `CHECK ports-caddy-80-443` (при `on` у `caddy` ровно 80/tcp и 443/tcp; при `off` опубликованных портов нет вовсе); логи `webapp` (15 строк) при `on`. Проверки порта PostgreSQL — без изменений. Источник портов — колонка `Ports` из `docker compose ps --format`; `docker inspect` контейнеров не используется, адреса и окружение не печатаются. Логи `caddy` не печатаются: в них имя домена.
- `deploy/vps/deploy-preflight.sh` — в разделе `.env`: необязательные `COMPOSE_PROFILES`, `WEBAPP_SECRET`, `WEBAPP_DOMAIN` («есть» / «нет (Mini App выключен)»), формат секрета (64 hex) и домена (имя хоста) без вывода значений, строка «профиль webapp: включён/выключен», предупреждения `ВНИМАНИЕ: …`, если профиль включён, а секрет или домен не заданы.
- `deploy/vps/deploy-apply.sh` — при включённом профиле (сервис `caddy` есть в `config --services`) образ `caddy` проверяется до запуска сервисов: есть локально — `caddy image: local, pull skipped`; нет — `docker compose pull caddy`; не удалось — сообщение и остановка до `up` (прежние `IMAGE_TAG`/`IMAGE_PREV` возвращаются существующим механизмом). Логика тегов и порядок `postgres → migrate → остальные` не менялись.
- `deploy/vps/rollback.sh` — только сообщение-подсказка перед вопросом, если в `.env` включён профиль `webapp` (D-21); `.env` в этой части не правится, логика прежняя.
- `deploy/smoke.sh` — проверки Mini App (ниже); очистка проекта `aggregator-smoke` теперь с `--profile webapp`.
- `deploy/deploy-apply-test.sh` — новые случаи для apply, rollback, verify и preflight (ниже). Новых тестовых файлов нет.
- `deploy/README.md` — раздел `## Phase 15: Mini App` между «Phase 14: избранное» и «Хеши»; две строки в «Правила». Остальные разделы не менялись (в диффе 87 добавленных строк, удалённых нет).
- `.env.example` — `WEBAPP_ADDR` оставлена и помечена: нужна только для локального запуска `aggregator webapp`, на сервере задана в `docker-compose.yml` и в `.env` сервера не добавляется; добавлены описания `WEBAPP_DOMAIN` и `COMPOSE_PROFILES` (закомментированы, без значений).
- `deploy/oneliner.sh`, `deploy/ship.sh`, `deploy/vps/agg`, `agg-install.sh`, `env-init.sh` — не менялись; новых файлов пакета и новых подкоманд `agg` нет.

## Caddy: версия и дайджест

`caddy:2.11.7@sha256:f2a1290d0463aad60660d4ec134943f183ee2a5f6c3eb7bf32dd984f2f020772`

Как получены: `docker pull --platform linux/amd64 caddy:2` → `RepoDigests` из `docker image inspect`; версия — `caddy version` в контейнере (`v2.11.7`). `docker buildx imagetools inspect` показал, что дайджест — многоплатформенный индекс (`application/vnd.oci.image.index.v1+json`) с манифестом `linux/amd64`, и что тег `caddy:2.11.7` указывает на тот же дайджест. Ссылка `caddy:2.11.7@sha256:…` находится командой `docker image inspect` (так образ ищет `deploy-apply.sh`).

## Ограничения безопасности `caddy`

Проверено запуском во временном проекте вне репозитория (тот же образ, порты `127.0.0.1:18080→80` и `127.0.0.1:18443→443`, заглушка вместо `webapp`):

- сработали все: `read_only: true`, `cap_drop: [ALL]` + `cap_add: [NET_BIND_SERVICE]`, `security_opt: ["no-new-privileges:true"]`. Контейнер стартует, `https` через прокси отдаёт ответ заглушки (200), `http` — перенаправление 308 на `https`; хранилище сертификатов пишется в том `/data`. Эффективные привилегии процесса — только `NET_BIND_SERVICE`.
- снятых ограничений нет; `tmpfs` не понадобился. Единственная запись об ошибке — попытка установить корневой сертификат локального центра в `/tmp` (только для `--from localhost`, то есть без домена); на работу прокси не влияет, с настоящим доменом локальный центр не используется.
- `user:` не задан (процесс — root контейнера с одной привилегией): образ рассчитан на это, тома `/data` и `/config` принадлежат root.

## Сеть

Отдельная сеть `edge`: `caddy` подключён только к ней, `webapp` — к `edge` и к сети по умолчанию (база). Проверено во временном проекте: из контейнера `caddy` имя сервиса в сети по умолчанию не разрешается, сервис в `edge` доступен. Блоки `postgres`, `migrate`, `bot`, `worker` для этого не менялись. Без профиля сеть `edge` и тома `caddy_*` в итоговой конфигурации отсутствуют.

## Проверки

- `docker compose config -q` с минимальным env-файлом вне репозитория: без профиля — OK, сервисы `bot migrate postgres worker`, строк `published` нет; с `--profile webapp` — OK, сервисы `bot caddy migrate postgres webapp worker`, порты только у `caddy`: 80/tcp и 443/tcp; окружение `webapp`: `DATABASE_URL`, `LOG_FORMAT`, `PGPASSWORD`, `WEBAPP_ADDR`, `WEBAPP_SECRET`.
- `git diff --numstat -- docker-compose.yml`: `59 5`. Пять удалённых строк — только комментарий-шапка (строки 1, 5–6, 19–20 прежнего файла). Прежние строки 21–128 (от `name:` до конца блока `worker`, включая якоря) побайтно равны новым 25–132 (`diff` пуст); добавления — после блока `worker` и в `volumes:`.
- Профиль из `.env` (временный проект, запуск через `env -i` из `/`, как делает `agg`): `COMPOSE_PROFILES=webapp` в `.env` каталога проекта включает сервис; после удаления строки `up -d` контейнер профиля не трогает; `docker compose --profile webapp rm -sf <сервис>` удаляет только названный контейнер, тома остаются.
- Однострочники этапа Б и выключения из памятки выполнены на временном файле с заглушкой `docker`: повторный запуск не дублирует строку, права 600, временных файлов не остаётся, при отказе вычисления ключа файл не меняется и ничего не печатается; значение ключа не выводится.
- `bash -n` для всех изменённых скриптов — без ошибок. `shellcheck` не установлен — не запускался.
- Тесты скриптов пакета: `deploy/deploy-apply-test.sh` → `DEPLOY APPLY OK` (78 строк `ok`); `deploy/load-postgres-test.sh` → `LOAD POSTGRES TEST OK`; `deploy/confirm-test.sh` → `CONFIRM OK`; `deploy/backup-test.sh` → `BACKUP TESTS OK` (без Docker, 0 строк `FAIL`).
  - apply: профиль выключен — образ `caddy` не запрашивается; включён и образ есть — pull пропущен; образа нет — один `pull caddy`, затем запуск; pull не удался — остановка до `up -d --wait postgres`, теги возвращены.
  - rollback: без профиля подсказки нет; с профилем — подсказка, `.env` не изменён.
  - verify: профиль выключен и портов нет → OK; включён, у `caddy` 80 и 443 → OK (и логи `webapp`); опубликован порт `webapp`, `postgres` или `bot` → `ports-only-caddy: FAIL`; лишний порт, udp либо отсутствие 443 у `caddy` → `ports-caddy-80-443: FAIL`; `webapp` unhealthy или профиль включён без контейнеров → `compose-state: FAIL`; адресов в разделе портов нет.
  - preflight: ключей нет → «нет (Mini App выключен)»; секрет или домен неверного формата → `ВНИМАНИЕ` без вывода значения; профиль без секрета и домена → два предупреждения; всё верно → предупреждений нет.
- `deploy/smoke.sh` → `SMOKE OK` (проект `aggregator-smoke`, свой временный каталог и env-файл; после выхода контейнеров, томов и сетей проекта не осталось). Новые шаги: без профиля — четыре прежних сервиса и нет опубликованных портов; с профилем — `config -q`, порты только у `caddy` (80 и 443), у `webapp` ровно пять переменных; `webapp` без секрета — код 1 и имя переменной; `webapp-secret` в сервисе `bot` — 64 hex; `webapp` healthy; из контейнера `postgres` средствами bash (`/dev/tcp`, без новых образов): `GET /api/v1/meta` без заголовка → 401, `GET /` → 200; порт 8080 не опубликован. `caddy` в смоке не запускается.
- `gofmt -l .` пуст; `go build ./...` — без ошибок; полный прогон `go test -p 1 -count=1 -v ./...` с `TEST_DATABASE_URL`: **505 PASS, 0 FAIL, 0 SKIP**, 16 пакетов `ok` (включая тесты, читающие `deploy/README.md` и `deploy/vps/deploy-verify.sh`).
- `deploy/oneliner.sh files` и `sums` работают, 18 файлов (состав не менялся): `agg`, `agg-install.sh`, `deploy-apply.sh`, `deploy-preflight.sh`, `deploy-verify.sh`, `env-init.sh`, `host-apply.sh`, `host-preflight.sh`, `host-verify.sh`, `load-postgres.sh`, `rollback.sh`, `docker-compose.yml`, `backup.sh`, `backup-lib.sh`, `backup-status.sh`, `backup-recipient`, `backup-apply.sh`, `clock-apply.sh`. Изменились пять из них (`docker-compose.yml`, `deploy-apply.sh`, `deploy-preflight.sh`, `deploy-verify.sh`, `rollback.sh`), поэтому `ship.sh` ожидаемо остановится на сверке SHA256 и потребует `INSTALL`.
- `git diff --stat`: `.env.example`, `deploy/README.md`, `deploy/deploy-apply-test.sh`, `deploy/smoke.sh`, `deploy/vps/{deploy-apply,deploy-preflight,deploy-verify,rollback}.sh`, `docker-compose.yml`; новый файл — этот. Правка `15-CONTEXT.md` в рабочей копии была до этапа и здесь не менялась.
- Поиск в добавленных строках: названий инструментов разработки нет; строк-разделителей и `rm -rf` нет; адреса — только `0.0.0.0` и `127.0.0.1`; имя хоста — только выдуманное в зоне `.invalid` (тест preflight).

## Порядок шагов владельца

Полностью — `deploy/README.md`, раздел «Phase 15: Mini App». Каждый шаг — по отдельному разрешению.

1. Предусловия: строка `PRODUCTION CHECK:` в `14-07-SUMMARY.md` (D-20; на момент подготовки её нет); для этапа Б — домен с A-записью и свободные 80/443.
2. Этап А (Mini App выключен): `agg backup-status`, `agg verify` до; `ship.sh` → ожидаемая остановка на сверке хешей; `INSTALL` в root-консоли; снова `ship.sh`; `agg preflight <TAG>`; `agg deploy <TAG>`; `agg verify` → `webapp_profile=off`, `published=none`, состав прежний.
3. Этап Б (включение): три однострочника в root-консоли (`WEBAPP_SECRET` — вычисляется в одноразовом контейнере `bot` и не печатается; `WEBAPP_DOMAIN`; `COMPOSE_PROFILES=webapp`); `agg preflight`; `agg deploy` того же тега; `agg verify` → `webapp_profile=on`, порты только 80/443 у `caddy`; `curl` с Mac (200 и CSP; 401 на API).
4. BotFather: Menu Button с URL Mini App.
5. Приёмка в Telegram по чек-листу раздела.
6. Выключение и откат — подразделы памятки (перед `agg rollback` на образ без `webapp` профиль убирается).

## Риски

- Публикация 80/443 — первые опубликованные порты проекта; порты Docker обходят `ufw`. Контроль — `agg verify` (две новые проверки).
- Сертификат зависит от A-записи и доступности 80/443 снаружи; при ошибках многократные перезапуски упираются в пределы центра сертификации. Том `caddy_data` не удалять.
- После удаления строки `COMPOSE_PROFILES` команда `agg deploy` контейнеры `webapp` и `caddy` не останавливает: нужна отдельная команда из подраздела «Выключение». `agg verify` в этом случае покажет `webapp_profile=on` (контейнеры существуют).
- Версия Compose на сервере локально не проверялась: проверки опираются на `docker compose ps --format` с полем `.Ports` и на `docker compose config --services/--images` (локально — Compose v5.1.2). Формат состояния через `--format` скрипт использовал и раньше.
- Первая проверка готовности `webapp` выполняется через интервал healthcheck (30 с): `agg deploy` на этапе Б ждёт до минуты, прежде чем запустить `caddy`.
- `agg deploy` при включённом профиле требует доступа сервера к Docker Hub за образом `caddy` (один раз). Запасного пути переноса образа с Mac, как для `postgres:17`, нет.

## Пометки «Упрощение:»

- `docker-compose.yml`, сервис `webapp`: тот же пользователь БД, что у `bot` и `worker` (D-04); отдельная роль — позже отдельным решением.

## Отступления

- `deploy/smoke.sh`: изменена существующая строка очистки — к `down -v --remove-orphans` проекта `aggregator-smoke` добавлен `--profile webapp`; без этого контейнер `webapp` и сети смок-проекта оставались после выхода. Команда по-прежнему относится только к проекту `aggregator-smoke`.
- Признак «профиль включён» в `deploy-verify.sh` — не только наличие контейнера `caddy`, но и сервис `caddy` в активной конфигурации либо контейнер `webapp`: иначе включённый в `.env` профиль с неподнявшимся `caddy` прошёл бы проверку как «выключен».
- `ports-caddy-80-443` при включённом профиле требует, чтобы оба порта были опубликованы (а не «не больше чем 80 и 443»).
- Чек-лист приёмки в памятке перечисляет экраны без подробностей интерфейса; отдельного документа приёмки фазы нет.
- `shellcheck` не установлен — не запускался.

## Что не сделано

- Ничего на сервере: пакет не доставлялся, `INSTALL`, `deploy`, правка `.env`, BotFather — шаги владельца.
- Запасной путь переноса образа `caddy` с Mac; отдельная роль БД; healthcheck у `caddy`; HTTP/3 (443/udp не публикуется).
