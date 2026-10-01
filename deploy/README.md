# Развёртывание на VPS (Phase 4–5, PRD-01)

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

## Phase 5: ПроБЕГ и дедупликация

Каждая команда — одной строкой. Mac — из корня репозитория; root-консоль — консоль провайдера.

1. **Пакет и образ.** Закоммитить изменения фазы, затем `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps`. Изменились `docker-compose.yml` и `deploy-verify.sh`, поэтому `ship.sh` остановится на сверке SHA256 — однострочник разовой установки из его вывода в root-консоли (слово `INSTALL`), затем снова `ship.sh` → `AGG LOAD OK` и `IMAGE_TAG=<TAG>`.
2. **Deploy.** Mac: `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg deploy <TAG>'` (слово `DEPLOY`), через 2 минуты `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p5-deploy-verify.log"`. `DEDUP_*` в `.env` не добавлять: значения по умолчанию из кода, автослияние выключено (`dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`).
3. **Первый прогон ПроБЕГа.** У источника нет прогонов, демон worker запускает его сразу → `suspect`, алерт с `[Принять как базу]`. Пока не принимать. Если прогон failed (403/429/451, «структура не найдена») или в причинах есть что-то кроме первого прогона — ничего не повторять и не принимать.
4. **Поля Марафонца.** Если в `/runs` нет прогона marathonec, начатого после выката, — root-консоль: `docker compose -f /opt/aggregator/docker-compose.yml run --rm --no-deps worker worker run-once --source marathonec`. Заполняет `source_events.fields` (без них проход дедупликации события не оценивает); один запрос к сайту.
5. **Санити-отчёт ПроБЕГа.** Root-консоль: `docker compose -f /opt/aggregator/docker-compose.yml run --rm --no-deps -T worker worker sanity-report --source probeg 2>&1 | sed -n 1,20p`. Сверить «Строк» и «Страна и серии» с `internal/ingest/sources/probeg/testdata/README.md` (раздел «Prepare на фикстуре»).
6. **Принятие.** Кнопка `[Принять как базу]` → `[Да, принять]` или root-консоль: `docker compose -f /opt/aggregator/docker-compose.yml run --rm --no-deps worker worker accept-baseline --source probeg`. Принятие запускает проход дедупликации.
7. **Проверка.** `agg verify` (как в шаге 2, лог `p5-verify-accepted.log`): ожидаются `fields_null=marathonec|0`, `fields_null=probeg|0`, `dedup_auto_merge_*=false`. Кандидаты — в `/dupes`; сводка «ожидают решения N, из них новых M» приходит в `DEDUP_DIGEST_AT` (10:00 МСК) или в течение минуты после прохода, если это время уже прошло; кандидатов нет — сводки нет.

Раздел «Дедупликация» в `agg verify`: `source_run=` — последний прогон каждого источника, `fields_null=` — активные записи без полей, `dedup_candidates=` — очередь (`fresh` — ещё не попавшие в сводку), `events_merged=` и до 10 строк `merged=<слитое>><корень>|…` — слияния, `dedup_auto_merge_bot=`/`_worker=` — режим из строки `started` каждого сервиса.

### «Лига Героев» (третий источник)

Источник `heroleague` входит в релиз (образ с коммита `e8005e8`). Порядок тот же, что для ПроБЕГа, после его принятия и разбора `/dupes`: первый прогон → `suspect` и алерт; санити-отчёт: `docker compose -f /opt/aggregator/docker-compose.yml run --rm --no-deps -T worker worker sanity-report --source heroleague 2>&1 | sed -n 1,20p`; принятие — кнопка `[Принять как базу]` → `[Да, принять]` или `… worker worker accept-baseline --source heroleague`; затем `agg verify`: `source_run=heroleague|…|ok|…|accepted=t`, `fields_null=heroleague|0`. Пока источник не принят, алерт `suspect` приходит на каждый новый прогон.

Выкат 2026-10-01: логи сохранены как `deploy-verify.log`, `p6-deploy-verify.log`, `p6-verify-accepted.log` (вместо `p5-*`).

### Автослияние (D-08)

Сейчас выключено решением владельца (ручное слияние через `/dupes`). До включения обязательна доработка «потенциальные автослияния» (Phase 6): без неё нельзя заранее увидеть, какие пары будут слиты; пары «Лиги Героев» в калибровку не входили.

Включать только если `internal/dedup/testdata/README.md` («Проверка владельцем») и SUMMARY калибровки фиксируют 0 ложных слияний, и только отдельным решением. Слияние скрывает событие для всех пользователей; «разлияния» нет — выключение останавливает только новые слияния.

1. Root-консоль (меняет только строку `DEDUP_AUTO_MERGE`, печатает её): `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^DEDUP_AUTO_MERGE=' /opt/aggregator/.env; echo DEDUP_AUTO_MERGE=true; } > "$T" && mv "$T" /opt/aggregator/.env && grep '^DEDUP_AUTO_MERGE=' /opt/aggregator/.env`
2. Mac: `agg deploy` с тем же `<TAG>`, что развёрнут (`IMAGE_PREV` не меняется), затем `agg verify` — ожидается `dedup_auto_merge_bot=true` и `dedup_auto_merge_worker=true`.
3. После ближайшего прогона любого источника — снова `agg verify` и просмотр строк `merged=`.

Выключение — root-консоль: `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^DEDUP_AUTO_MERGE=' /opt/aggregator/.env; echo DEDUP_AUTO_MERGE=false; } > "$T" && mv "$T" /opt/aggregator/.env && grep '^DEDUP_AUTO_MERGE=' /opt/aggregator/.env`, затем `agg deploy` того же `<TAG>` и `agg verify` (`dedup_auto_merge_*=false`). Уже выполненные слияния выключение не отменяет.

### Откат Phase 5

`agg rollback` (слово `ROLLBACK`) возвращает прежний образ. Схема 00004 остаётся: goose старого образа не требует неизвестных ему версий, новые колонки допускают NULL. `goose down` не делать. События ПроБЕГа остаются в выдаче, но не обновляются; `DEDUP_*` старый образ не читает.

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
  Для любого источника — `--source <код>` (`marathonec`, `probeg`). Слово `worker` дважды: первое — имя сервиса, дальше аргументы целиком заменяют `command` сервиса.
- **Перенос `postgres:17` с Mac**, если VPS не достаёт до Docker Hub (`agg deploy` остановится с подсказкой). `agg load` принимает только `aggregator:<TAG>`, поэтому загрузка — в root-консоли и только после сверки SHA256 с Mac:
  1. Mac: `docker pull --platform linux/amd64 postgres:17 && docker save postgres:17 -o postgres17.tar && shasum -a 256 postgres17.tar` — запомнить хеш.
  2. Mac: `scp -F ~/.ssh/run_agg.conf postgres17.tar run-agg-vps:agg-deploy/`.
  3. Root-консоль: однострочник `load-postgres.sh` из вывода `oneliner.sh`, вместо `<SHA256 postgres17.tar с Mac>` — хеш из шага 1. Скрипт копирует архив во временный каталог root, сверяет хеш копии и только тогда делает `docker load` этой копии; при несовпадении — `STOP: HASH MISMATCH`, ничего не загружается. Никогда не делать `docker load` прямо из `/home/runagg`: runagg может подменить файл.

## Правила

- **Никогда `docker compose down -v`**: удалит том с БД, а бэкапов до Phase 6 нет.
- Пароль Postgres действует только при первой инициализации тома: менять его в `.env` без `ALTER ROLE` нельзя.
- Не перезагружать хост скриптами; решение о перезагрузке принимает владелец.
- Порт PostgreSQL наружу не публикуется (порты Docker обходят ufw); `agg verify` это проверяет.
- Вне этой фазы (Phase 6): ssh root/пароль, ufw, swap, часы, `tinyproxy`, резервные копии, пересмотр роли `runagg` и его ключа.
