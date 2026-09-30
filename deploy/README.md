# Развёртывание на VPS (Phase 4, PRD-01)

Памятка владельца. Скрипты лежат в репозитории и не содержат IP-адресов, имён хостов и секретов. Исходники, Go и реестр образов на VPS не нужны: образ собирается на Mac и передаётся по ssh.

## Схема доступа

- **Root по ssh не используется.** Все обычные шаги идут с Mac по ssh под `runagg` (ключ Phase 1, `SSH_CONFIG=~/.ssh/run_agg.conf`, алиас `run-agg-vps`).
- **runagg может через sudo только `/usr/local/sbin/agg`** с перечисленными подкомандами (`/etc/sudoers.d/runagg-aggregator`). В группу `docker` он не входит, произвольного sudo нет, sudoers Phase 1 (`/etc/sudoers.d/run-agg`) не меняется.
- **`agg` не наследует окружение и каталог вызывающего:** сразу перезапускается через `env -i` из `/`; `agg-install.sh` проверяет, что sudo для runagg сбрасывает окружение (`env_reset` без `env_keep`/`setenv`).
- **`agg` строго проверяет аргументы:** подкоманда из списка, `TAG` только `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`. `agg load` принимает архив только если в нём ровно образ `aggregator:<TAG>` (иначе `docker load` не выполняется).
- **Всё, что исполняет или читает root, принадлежит root:** `/usr/local/sbin/agg`, `/usr/local/lib/aggregator/*` (скрипты и `docker-compose.yml`), `/opt/aggregator/docker-compose.yml`, `/opt/aggregator/.env` (600). runagg подменить их не может.
- **Root нужен только в консоли провайдера:** разовая установка `agg` (и повтор, если изменились `agg`, скрипты VPS или `docker-compose.yml`), а также изменения хоста — обновления, перезагрузка, плагин Compose.
- **У ключа runagg нет терминала на VPS** (`restrict` в `authorized_keys`). Поэтому токен вводится на Mac без эха (`deploy/secrets.sh`) и идёт по ssh через stdin. Слова-подтверждения (`DEPLOY`, `ROLLBACK`) читаются так же — через stdin.

## Предпосылки

- Бот создан в BotFather, токен под рукой.
- Известен ваш числовой Telegram ID (пойдёт в `ADMIN_TG_IDS`).
- `ssh -F ~/.ssh/run_agg.conf run-agg-vps true` проходит.
- Код закоммичен: `ship.sh` берёт тег образа из коммита и останавливается, если файлы сборки изменены и не закоммичены (иначе задайте `TAG=...` явно).
- Docker на Mac запущен.

## Порядок шагов

Команды с Mac — из корня репозитория. Скрипты не запускать параллельно. Все команды для VPS печатает `deploy/oneliner.sh` (его вызывает `ship.sh`).

1. **Доставка пакета.** `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps`. Собирает образ, проверяет `smoke.sh`, копирует пакет скриптов и `SHA256SUMS` в `/home/runagg/agg-deploy/`. При первом запуске останавливается со `STOP: … не установлены` и печатает однострочник разовой установки — это ожидаемо, образ ещё не передаётся.
2. **Разовая установка (root-консоль провайдера).** Однострочник из вывода: копирует пакет во временный каталог root, сверяет SHA256 файла `SHA256SUMS` и по нему всех файлов, запускает `agg-install.sh`; тот показывает, что поставит, и ждёт слова `INSTALL`. При `STOP: HASH MISMATCH` ничего не запускается.
3. **A0 host-preflight (лог).** Mac-команда `agg host-preflight` из вывода, пишет `$HOME/run-agg-phase4/host-preflight.log`. Прочтите: контейнеры хоста и политики перезапуска, reboot-required, обновления, часы.
4. **Решение по хосту.** Хост используется не только проектом: перезагрузка может задеть чужие сервисы. Обновления и перезагрузку можно отложить, плагин Compose нужен в любом случае.
5. **Изменения хоста (root-консоль, по решению).** Однострочники `host-apply.sh updates` (слово `UPDATES`), затем вручную `systemctl reboot`, затем `host-apply.sh compose` (слово `COMPOSE`).
6. **A3 host-verify (лог).** Ожидается `HOST VERIFY OK`.
7. **Доставка образа.** Снова `SSH_CONFIG=… deploy/ship.sh run-agg-vps`: SHA256 установленных `agg` и скриптов совпали с репозиторием → `docker save | gzip | ssh … 'gunzip | sudo -n /usr/local/sbin/agg load <TAG>'`, в конце `AGG LOAD OK` и `IMAGE_TAG=<TAG>`.
8. **Секреты (разово).** `SSH_CONFIG=… deploy/secrets.sh run-agg-vps`: токен и `ADMIN_TG_IDS` без эха, `OWNER_CONTACT` обычным вводом; на VPS `agg env-init` создаёт `/opt/aggregator/.env` (root, 600) и пароль БД. Существующий `.env` не меняется.
9. **B0 preflight `<TAG>` (лог).** Образ `linux/amd64`, наличие ключей `.env`, SHA256 compose-файла.
10. **B1 deploy `<TAG>`.** Слово `DEPLOY`. Ставит compose-файл в `/opt/aggregator`, пишет `IMAGE_TAG`, поднимает postgres, migrate, bot и worker.
11. **B2 verify (лог).** Через 1–2 минуты. Ожидается `DEPLOY VERIFY OK`, в том числе `CHECK root-owned: OK` и `CHECK runagg-not-in-docker: OK`.
12. **Первое включение (D-07).** Первый прогон Марафонца получает статус `suspect`; бот пришлёт администраторам алерт с кнопкой `[Принять как базу]`. Пока не принято, каталог пуст.

Повторные обновления: `ship.sh` (шаг 7), затем B0 по желанию, B1 с новым `<TAG>`, B2. Прежний тег сохраняется как `IMAGE_PREV`; откат — `agg rollback` (слово `ROLLBACK`). Если изменились `agg`, скрипты VPS или `docker-compose.yml`, `ship.sh` остановится на сверке SHA256 — повторите шаг 2.

## Хеши

`SHA256SUMS` — хеши всех файлов пакета; его собственный хеш печатается в однострочнике для root-консоли. Однострочник работает только с копией пакета во временном каталоге root: файлы в `/home/runagg` после проверки уже не читаются. Если вставка в консоли не работает, `oneliner.sh` печатает вариант из двух коротких команд: сверить хеш `SHA256SUMS` глазами, затем `bash agg-install.sh`.

**`STOP: HASH MISMATCH`** — пакет на VPS отличается от репозитория. Ничего не запускалось. Ничего не править вручную: повторите `ship.sh` и возьмите новый однострочник.

## Логи и секреты

- Логи запусков хранятся только в `$HOME/run-agg-phase4/` вне репозитория; не коммитить и не копировать в `.planning/`.
- Не печатать `.env`, не использовать `docker inspect` контейнеров (печатает окружение с секретами) и `docker compose config` без `-q`; для образа — `docker image inspect --format ...`.
- Не запускать бот локально с тем же токеном: два `getUpdates` дают 409 Conflict обоим.

## Запасные пути

- **Принятие базы из CLI** (root-консоль), если кнопка недоступна:
  `docker compose -f /opt/aggregator/docker-compose.yml run --rm worker worker accept-baseline --source marathonec`
  Слово `worker` дважды: первое — имя сервиса, дальше аргументы целиком заменяют `command` сервиса.
- **Перенос `postgres:17` с Mac**, если VPS не достаёт до Docker Hub (`agg deploy` остановится с подсказкой): на Mac `docker pull --platform linux/amd64 postgres:17 && docker save postgres:17 -o postgres17.tar`, скопировать файл в `/home/runagg/agg-deploy/` (`scp -F …`) и загрузить в root-консоли `docker load -i /home/runagg/agg-deploy/postgres17.tar`. `agg load` принимает только `aggregator:<TAG>`.

## Правила

- **Никогда `docker compose down -v`**: удалит том с БД, а бэкапов до Phase 6 нет.
- Пароль Postgres действует только при первой инициализации тома: менять его в `.env` без `ALTER ROLE` нельзя.
- Не перезагружать хост скриптами; решение о перезагрузке принимает владелец.
- Порт PostgreSQL наружу не публикуется (порты Docker обходят ufw); `agg verify` это проверяет.
- Вне этой фазы (Phase 6): ssh root/пароль, ufw, swap, часы, `tinyproxy`, резервные копии, пересмотр роли `runagg` и его ключа.
