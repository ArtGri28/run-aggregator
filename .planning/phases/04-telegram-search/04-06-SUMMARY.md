---
phase: 04-telegram-search
plan: 06
subsystem: deploy
tags: [docker, compose, distroless, deploy, vps, sha256]

requires:
  - phase: 04-telegram-search
    provides: "04-05: подкоманды bot/worker/migrate/healthcheck в одном бинарнике, runBot с алертами"
provides:
  - "Dockerfile (golang:1.27.1 -> distroless static nonroot, пользователь 65532, узкий COPY) и .dockerignore"
  - "docker-compose.yml: production-состав PRD-01 (postgres 17, migrate, bot, worker), токен только у bot"
  - "deploy/smoke.sh: локальная проверка образа и compose в проекте aggregator-smoke без сети к Telegram"
  - "deploy/ship.sh, deploy/oneliner.sh: доставка образа и однострочники с полным SHA256 (STOP: HASH MISMATCH)"
  - "deploy/vps/*.sh: host-preflight/apply/verify, deploy-preflight/apply/verify, rollback"
  - "deploy/README.md: порядок действий владельца, запасные пути, правила"
affects: [04-07, 04-08]

actuals:
  tokens: 8300
  tasks: 3
  commits: 0

plan_head_before: 1734d29e1394bdd33ad31566578f6a408081b181
commits: "uncommitted — owner commits manually"

tech-stack:
  added: []
  patterns:
    - "Переменные раздаются сервисам по одной (x-db-env + точечные), общий файл переменных не подключается"
    - "Скрипты VPS: PREFLIGHT (только чтение) -> APPLY (слово подтверждения) -> VERIFY; запуск однострочником с SHA256"
    - "Секреты вводятся read -rs и пишутся builtin printf во временный файл 600, затем mv"

key-files:
  created:
    - Dockerfile
    - .dockerignore
    - docker-compose.yml
    - deploy/smoke.sh
    - deploy/ship.sh
    - deploy/oneliner.sh
    - deploy/README.md
    - deploy/vps/host-preflight.sh
    - deploy/vps/host-apply.sh
    - deploy/vps/host-verify.sh
    - deploy/vps/deploy-preflight.sh
    - deploy/vps/deploy-apply.sh
    - deploy/vps/deploy-verify.sh
    - deploy/vps/rollback.sh
  modified:
    - .env.example
    - docker-compose.dev.yml

key-decisions:
  - "Переменные сервисам передаются по одной (отличие от 04-RESEARCH Pattern 10): токен только у bot, OWNER_CONTACT и RAW_DIR только у worker; общего env_file нет"
  - "Пароль БД в compose один (POSTGRES_PASSWORD), сервисы получают его как PGPASSWORD; отдельной строки PGPASSWORD в .env на VPS нет"
  - "Скрипты, не входящие в список известных (oneliner.sh), считаются скриптами только чтения (Mac-форма с tee)"

requirements-completed: [PRD-01]

duration: 45min
completed: 2026-09-30
status: complete
---

# Phase 4 Plan 06: Упаковка и доставка (Docker, compose, скрипты VPS) Summary

**Один образ `aggregator` на distroless nonroot и production-compose с postgres/migrate/bot/worker проходят локальную проверку `smoke.sh` без сети к Telegram; для VPS готовы скрипты доставки и развёртывания по схеме PREFLIGHT -> APPLY -> VERIFY с запуском через проверку полного SHA256.**

## Accomplishments

- **Образ:** `golang:1.27.1` с `--platform=$BUILDPLATFORM` -> `gcr.io/distroless/static-debian13:nonroot`, `USER 65532:65532`, `COPY` только `go.mod go.sum cmd internal migrations`. Размер образа `docker image inspect` 5,6 МиБ (сжатый размер), 25,8 МБ на диске по `docker images`. Время сборки: 18 с холодная на linux/amd64 (базовые образы уже загружены), 3 с с кэшем.
- **Compose (`name: aggregator`):** postgres 17 (том `pgdata`, healthcheck `pg_isready`, без публикации портов), одноразовый `migrate`, `bot` (healthcheck `/aggregator healthcheck`), `worker` (том `raw`, `stop_grace_period: 30s`). У приложения `restart: unless-stopped`, `read_only`, `cap_drop: [ALL]`, `no-new-privileges`, json-file 10m x 3, `depends_on` с `service_completed_successfully` / `service_healthy`.
- **`smoke.sh`:** сборка linux/amd64; проверка архитектуры и пользователя; список файлов образа без `.env`, `.planning`, `.git`, `MVP.md`; временный проект `aggregator-smoke` с фиктивным env-файлом во временном каталоге; `config -q`; postgres healthy; `migrate` код 0; `healthcheck` код 0; `bot` без токена код 1 с именем переменной; `worker sanity-report` на пустой БД код 1 «нет прогонов»; порт 5432 не опубликован. По выходу удаляется только проект `aggregator-smoke` с томами и временный каталог.
- **Доставка:** `ship.sh` (тег из коммита, остановка при незакоммиченных файлах сборки, `smoke.sh` до отправки, `docker save | gzip | ssh ... docker load`, `scp`, однострочники) и `oneliner.sh` (VPS-форма с `if/else` и `STOP: HASH MISMATCH`, Mac-форма: `tee` в `$HOME/run-agg-phase4/` для скриптов чтения, `ssh -t` для меняющих; `host-apply.sh` — две строки `updates` и `compose`).
- **Скрипты VPS:** host-preflight/verify и deploy-preflight/verify только читают; host-apply (`UPDATES`, `COMPOSE`, без перезагрузки), deploy-apply (`DEPLOY`, `.env` 600 через скрытый ввод, обновление только `IMAGE_TAG`/`IMAGE_PREV`), rollback (`ROLLBACK`). Удаления томов нет нигде.
- **README** с порядком A0–B2, первым включением D-07, запасными путями (CLI-принятие с повтором `worker`, перенос `postgres:17` с `--platform linux/amd64`) и запретами.

## Task Commits

Коммитов нет: по правилам владельца коммиты делает он вручную ("uncommitted — owner commits manually"). Файлы по задачам, предлагаемый тип `feat`:

**Задача 1 (образ, compose, smoke):**
- `Dockerfile` (новый), `.dockerignore` (новый), `docker-compose.yml` (новый)
- `.env.example` (добавлен блок production: `TELEGRAM_BOT_TOKEN=`, `ADMIN_TG_IDS=`, `POSTGRES_PASSWORD=`, `IMAGE_TAG=`)
- `docker-compose.dev.yml` (только строка шапки)
- `deploy/smoke.sh` (новый)

**Задача 2 (доставка, однострочники, подготовка хоста):**
- `deploy/ship.sh`, `deploy/oneliner.sh` (новые)
- `deploy/vps/host-preflight.sh`, `deploy/vps/host-apply.sh`, `deploy/vps/host-verify.sh` (новые)

**Задача 3 (развёртывание, откат, памятка):**
- `deploy/vps/deploy-preflight.sh`, `deploy/vps/deploy-apply.sh`, `deploy/vps/deploy-verify.sh`, `deploy/vps/rollback.sh` (новые)
- `deploy/README.md` (новый)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Проверка порта в smoke.sh под Compose v5**
- **Found during:** Задача 1
- **Issue:** `compose port postgres 5432` для неопубликованного порта печатает `invalid IP:0`, а не пустую строку; проверка «вывод пуст» падала.
- **Fix:** порт считается опубликованным, только если вывод оканчивается на `:<порт больше 0>`. Ту же форму использует `deploy-verify.sh`.
- **Files modified:** `deploy/smoke.sh`, `deploy/vps/deploy-verify.sh`

**2. [Rule 1 - Bug] Пользователь образа и `set -e`**
- **Issue:** пользователь читается как `.Config.User`, а не `.User`; конструкции `a && b` и `! cmd` не прерывают скрипт при `set -e`, поэтому проверки в `smoke.sh` записаны отдельными командами или через `if ...; then exit 1; fi`.
- **Files modified:** `deploy/smoke.sh`

**3. [Уточнение плана] Запасной путь в deploy-apply.sh**
- Если `pull postgres` не удался, но `postgres:17` уже есть на хосте, скрипт продолжает; иначе `STOP` с отсылкой к README.

**4. [Уточнение плана] Вывод oneliner.sh**
- Имя лога — `<скрипт без .sh>.log` (как требует проверка `host-preflight.log`); неизвестные файлы считаются скриптами только чтения; SHA256 `docker-compose.yml` берётся из корня репозитория независимо от текущего каталога.

**Итого:** 2 исправления, 2 уточнения; цели плана не затронуты.

## Known Stubs

Нет.

## Issues Encountered

- Первый запуск проверки Задачи 1 подвис из-за моей ошибки в самой команде запуска (лишняя строка ждала ввод с терминала); к скриптам это не относится, повторный запуск прошёл.
- Читать временный `.env` при локальной проверке `deploy-apply.sh` хук запретил; проверка выполнена на копиях скриптов с другим именем файла (см. ниже).

## Проверка

- `PACKAGING OK` (с `SMOKE OK`): выполнено полностью, включая сборку; после прогона проект `aggregator-smoke`, его тома и сети отсутствуют.
- `HOST SCRIPTS OK`: `bash -n`, маркеры, отсутствие меняющих команд в скриптах чтения, `host-apply` без перезагрузки, однострочник выполняет неизменённый файл и печатает `STOP: HASH MISMATCH` для изменённого (на Mac `sha256sum` заменён обёрткой над `shasum -a 256`).
- `DEPLOY SCRIPTS OK`: `bash -n` всех `deploy/vps/*.sh`, нет `down -v`, `config` только с `-q`, `inspect` только с `--format`, нет IPv4 (кроме 127.0.0.1/0.0.0.0), нет строк, похожих на токен.
- Дополнительно, локально без VPS: `deploy-apply.sh` и `rollback.sh` прогнаны на копиях с подменой каталога и заглушкой `docker` через pty: создание файла окружения (права 600, ровно пять ключей, пароль 48 hex, токен не напечатан), обновление (`IMAGE_TAG=tag2`, `IMAGE_PREV=tag1`, остальные ключи целы), откат (теги поменялись местами).
- `TEST_DATABASE_URL=... go test -p 1 -count=1 -timeout 5m ./...` — все пакеты `ok`; `go vet ./...` чисто; `go.mod`, `go.sum` не менялись.
- Контейнеры `aggregator-dev-postgres-1` и `aggregator-test-postgres-1` не тронуты (работали до и после). К VPS, Telegram и источникам обращений не было; `ssh`, `scp`, `ship.sh` и скрипты `deploy/vps/*.sh` на реальном хосте не запускались.

Не проверено (только `bash -n` и чтение): `host-preflight.sh`, `host-verify.sh`, `deploy-preflight.sh`, `deploy-verify.sh` используют GNU-утилиты и пути Ubuntu (`stat -c`, `sha256sum`, `ss`, `timedatectl`, `/boot`), на Mac не выполняются.

## Threat Flags

Новой поверхности сверх `<threat_model>` нет. Митигации: T-04-27 (узкий COPY + `.dockerignore`, smoke проверяет список файлов образа), T-04-28 (нет публикации портов; smoke и deploy-verify), T-04-29 (nonroot, `read_only`, `cap_drop`, `no-new-privileges`), T-04-30 (хеш-однострочник, проверено локально), T-04-31 (скрытый ввод, `printf`, `config -q`, `inspect --format`), T-04-32 (токен только у `bot`, awk-проверка), T-04-33 (нет удаления томов в `deploy/vps`), T-04-34 (preflight показывает контейнеры и политики, `host-apply` не перезагружает, раздельные слова подтверждения), T-04-35 (нет IP и имён хостов в файлах).

## Next Phase Readiness

Что владельцу знать перед 04-07:

- Файлы нужно закоммитить до `deploy/ship.sh`: без явного `TAG=...` он остановится при незакоммиченных изменениях в файлах сборки и `deploy/`; тег образа берётся из короткого хеша коммита.
- Предположения, которые снимут PREFLIGHT/VERIFY на VPS: `docker load` на docker.io 29.1.3 читает архив `docker save` в формате OCI; Compose 2.40.3 из Ubuntu понимает якоря и слияние `<<:` в `environment` (проверка `compose config -q` в B0); `compose ps --format` с шаблоном (если не поддержан, `deploy-verify.sh` печатает `CHECK compose-state: manual`).
- `deploy-verify.sh` считает FAIL любую службу на хосте, слушающую :5432 (в том числе чужую); при FAIL смотрите, кто слушает.
- Список источников unattended-upgrade на VPS неизвестен (предполагаются только security): `host-apply.sh updates` покажет его до подтверждения, читайте вывод.
- Образ на диске занимает 25,8 МБ; smoke оставляет локально `aggregator:smoke` (можно удалить `docker rmi aggregator:smoke`).
- Блокеров нет.

## Self-Check: PASSED

- Файлы существуют: `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `deploy/smoke.sh`, `deploy/ship.sh`, `deploy/oneliner.sh`, `deploy/README.md`, семь файлов в `deploy/vps/`; изменены `.env.example`, `docker-compose.dev.yml`.
- Коммитов нет по условию запуска; `plan_head_before` = 1734d29e1394.
- Проверки `PACKAGING OK`, `HOST SCRIPTS OK`, `DEPLOY SCRIPTS OK` и тесты Go выполнены и прошли.

---
*Phase: 04-telegram-search*
*Completed: 2026-09-30*
