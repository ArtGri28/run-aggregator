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

## Phase 11: поддержка

Каждая команда — одной строкой. Mac — из корня репозитория; root-консоль — консоль провайдера. **Каждый шаг на production — только по отдельному разрешению владельца на этот шаг**; сам по себе документ ничего не разрешает.

Что изменилось: у сервиса `bot` появилась необязательная переменная `SUPPORT_CHAT_ID`; миграция `00007` создаёт две таблицы поддержки, обе пустые. Изменён `docker-compose.yml`, поэтому `ship.sh` остановится на сверке SHA256 — однострочник разовой установки из его вывода в root-консоли (слово `INSTALL`), затем снова `ship.sh`. Пока `SUPPORT_CHAT_ID` не задана, поддержка выключена целиком: кнопки «Поддержка» нет, `/support` — неизвестная команда, бот ведёт себя как до фазы.

### Этап А: выкат кода без группы

1. **Копия базы.** Перед миграцией — свежая проверенная копия: `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg backup-status'`, ожидается `STATUS OK` и `usable=yes`.
2. **Пакет и образ.** Закоммитить изменения фазы, затем `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps`. На сверке SHA256 — однострочник разовой установки (слово `INSTALL`), затем снова `ship.sh` → `AGG LOAD OK` и `IMAGE_TAG=<TAG>`.
3. **Preflight, deploy, verify.** `agg preflight <TAG>`, `agg deploy <TAG>` (слово `DEPLOY`), через 1–2 минуты `agg verify`, как в разделе «Порядок шагов». Миграция `00007` применяется при deploy.
4. **Проверка.** В строке `started` лога бота — `"support":false`; в Telegram главное меню из трёх кнопок. В `.env` на этом этапе ничего не добавлять.

### Группа

Группу создаёт и ведёт владелец:

- приватная, без тем и без публичной ссылки;
- бот — обычный участник, без прав администратора; режим приватности бота в BotFather остаётся включённым;
- в поддержку и из неё ходит только текст: вложения бот не принимает и не доставляет;
- идентификатор супергруппы начинается с `-100`;
- отвечают те, кого владелец добавил в группу: состав группы и есть контроль доступа, любой участник может написать пользователю от имени бота.

Как узнать идентификатор группы: в веб-клиенте Telegram он обычно виден в адресной строке открытой группы; либо добавьте бота в группу, напишите в группе любую команду (например `/start`) и сразу выполните `agg verify` — в последних 15 строках лога бота будет запись `message from unconfigured group` с `chat_id`. `getUpdates` с Mac не запрашивать: он конфликтует с работающим ботом. Сторонних ботов в группу не добавлять. Идентификатор нигде, кроме `.env` на VPS, не записывается.

### Этап Б: включение

1. Root-консоль (меняет только строку `SUPPORT_CHAT_ID` и печатает число таких строк, а не значение; `<ID группы>` замените сами): `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^SUPPORT_CHAT_ID=' /opt/aggregator/.env; echo 'SUPPORT_CHAT_ID=<ID группы>'; } > "$T" && mv "$T" /opt/aggregator/.env && grep -c '^SUPPORT_CHAT_ID=' /opt/aggregator/.env` — ожидается `1`.
2. Mac: `agg deploy` с тем же `<TAG>`, что развёрнут (`IMAGE_PREV` не меняется), затем `agg verify`.
3. Признак включения: в строке `started` лога бота `"support":true`, в главном меню четвёртая кнопка «💬 Поддержка». Дальше — чек-лист `## Production-приёмка` в `.planning/phases/11-support-chat/11-ACCEPTANCE.md`.

Если группу перенесли в супергруппу, идентификатор меняется: бот пишет в лог запись с новым значением (`message from unconfigured group`), владелец обновляет строку `.env` тем же однострочником и повторяет `agg deploy` того же `<TAG>`.

### Выключение и откат

Выключение — root-консоль, тот же однострочник без добавления строки: `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^SUPPORT_CHAT_ID=' /opt/aggregator/.env; } > "$T" && mv "$T" /opt/aggregator/.env && grep -c '^SUPPORT_CHAT_ID=' /opt/aggregator/.env` — ожидается `0` (код возврата `grep` при этом ненулевой, это нормально). Затем `agg deploy` того же `<TAG>` и `agg verify` (`"support":false`).

Откат образа — `agg rollback` (слово `ROLLBACK`). Таблицы поддержки остаются, прежний образ их не читает; `goose down` не делать.

## Phase 12: вид спорта

**Каждый шаг на production — только по отдельному разрешению владельца на этот шаг**; сам по себе документ ничего не разрешает. Каждая команда — одной строкой. Mac — из корня репозитория; root-консоль — консоль провайдера.

Что изменилось: миграция `00008` добавляет колонку `events.sport` со значением по умолчанию `running`; таблица при этом не переписывается. Новый образ `bot` и `worker`: бот показывает только события включённых видов спорта, сейчас включён один бег, поэтому экраны и выдача прежние. `docker-compose.yml`, скрипты пакета и `.env` не менялись: `ship.sh` на сверке SHA256 не останавливается, переустановка пакета не нужна. Если `ship.sh` всё же остановился на сверке — ничего не устанавливать и сообщить.

### Шаги

1. **Копия базы.** Перед миграцией — свежая проверенная копия: `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg backup-status' 2>&1 | tee "$HOME/run-agg-phase4/p12-backup-status.log"`, ожидается `STATUS OK` и `usable=yes`.
2. **Состояние до выката.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p12-before-verify.log"`. Запомнить `events_merged=` и строки `source_run=`. В root-консоли выполнить второй запрос проверки (см. ниже) и сохранить его вывод: это счётчики прогонов до выката.
3. **Образ.** Закоммитить изменения фазы, затем `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps` → `AGG LOAD OK` и `IMAGE_TAG=<TAG>`.
4. **Preflight.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg preflight <TAG>'`.
5. **Deploy.** `printf 'DEPLOY\n' | ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg deploy <TAG>'` — слово `DEPLOY` передаётся на вход: без него команда остановится на вопросе. Миграция `00008` применяется при deploy.
6. **Проверка после выката.** Через две минуты `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p12-deploy-verify.log"`: ожидается `DEPLOY VERIFY OK`, `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`, `events_merged=` как до выката. Затем оба запроса проверки.
7. **После прогонов.** Когда каждый источник прошёл прогон (расписание — раз в шесть часов), снова `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p12-after-runs-verify.log"` и оба запроса проверки.

### Запросы проверки

Оба запроса только читают. Каждая строка блока — отдельный запрос:

```sql
SELECT 'sport=' || sport || '|' || count(*) FROM events GROUP BY sport ORDER BY sport;
SELECT 'run_counts=' || s.code || '|' || r.id || '|' || r.status || '|created:' || COALESCE(r.metrics->>'created', '?') || '|changed:' || COALESCE(r.metrics->>'changed', '?') || '|unchanged:' || COALESCE(r.metrics->>'unchanged', '?') FROM ingest_runs r JOIN sources s ON s.id = r.source_id WHERE r.id IN (SELECT r2.id FROM ingest_runs r2 WHERE r2.source_id = r.source_id ORDER BY r2.id DESC LIMIT 3) ORDER BY s.code, r.id;
```

Root-консоль, строка блока целиком вместо `<запрос>` (в двойных кавычках): `docker compose -f /opt/aggregator/docker-compose.yml exec -T postgres psql -U aggregator -d aggregator -Atc "<запрос>"`

Первый запрос — распределение событий по видам спорта, второй — три последних прогона каждого источника: номер, статус, число созданных, изменённых и неизменённых записей.

Чего ждать:

- `sport=running|<n>` — одной строкой, сразу после выката; `<n>` — число всех событий. Вторая строка `sport=` — нарушение.
- В `run_counts=` у первого прогона каждого источника после выката — `changed:0` либо единицы, как у прогонов до выката (шаг 2).
- Изменение почти всех записей источника — признак нарушения: новый код переписал прежние записи. Такой прогон не принимать за норму и решать об откате.

### Откат

`agg rollback` (слово `ROLLBACK`) возвращает прежний образ. Колонка `events.sport` остаётся: прежний образ её не читает и не пишет, новые строки получают `running`. `goose down` не делать.

## Phase 13: лыжи

**Каждый шаг на production — только по отдельному разрешению владельца на этот шаг**; сам по себе документ ничего не разрешает. Каждая команда — одной строкой. Mac — из корня репозитория; root-консоль — консоль провайдера.

Что изменилось:

- миграция `00009` заменяет закрытый список значений `events.event_type` проверкой формата; строки не переписываются;
- адаптер Tristyle делает второй запрос списка (лыжи, Беларусь); лыжные записи получают вид спорта `skiing` и дисциплину;
- главное меню: вместо кнопки поиска — кнопки видов спорта, новый текст; вторая строка шапки экрана предложения анкеты и три общих сообщения (ответ на неизвестную команду, ответ на запрос без слов, заголовок результатов текстового запроса) — новые формулировки;
- лыжи включены в справочнике: лыжные события видны, как только появятся в каталоге.

`docker-compose.yml`, скрипты пакета и `.env` не менялись: `ship.sh` на сверке SHA256 не останавливается, переустановка пакета не нужна. Если `ship.sh` всё же остановился на сверке — ничего не устанавливать и сообщить. `DEDUP_*` в `.env` не добавлять: автослияние остаётся выключенным.

### Шаги

1. **Состояние копий.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg backup-status' 2>&1 | tee "$HOME/run-agg-phase4/p13-backup-status.log"`, ожидается `STATUS OK` и `usable=yes`. Это проверка, что копии вообще делаются; свежая копия — шаг 6.
2. **Состояние до выката.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p13-before-verify.log"`. Запомнить `events_merged=`, `events_listed=`, `dedup_candidates=` и строки `source_run=`. В root-консоли выполнить три запроса проверки (см. ниже) и сохранить их вывод: это состояние до выката.
3. **Имя ограничения.** Root-консоль, только чтение: `docker compose -f /opt/aggregator/docker-compose.yml exec -T postgres psql -U aggregator -d aggregator -Atc "SELECT conname || '|' || pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'events'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%event_type%';"` — ожидается ровно одна строка, она начинается с `events_event_type_check|` и перечисляет шесть значений (`road`, `trail`, `cross`, `track`, `obstacle`, `other`). Другое имя, ни одной строки либо несколько строк — дальше не идти и сообщить: миграция `00009` снимает ограничение по этому имени и при другом имени остановится с ошибкой.
4. **Образ.** Закоммитить изменения фазы, затем `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps` → `AGG LOAD OK` и `IMAGE_TAG=<TAG>`.
5. **Preflight.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg preflight <TAG>'`.
6. **Свежая копия непосредственно перед миграцией.** Root-консоль, ручной запуск (как в `deploy/RUNBOOK.md`): `/bin/bash /usr/local/lib/aggregator/backup.sh`. Затем Mac: `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg backup-status' 2>&1 | tee "$HOME/run-agg-phase4/p13-fresh-backup-status.log"` — ожидается `STATUS OK`, `usable=yes` и возраст копии `backup_age_s` не больше 3600 (часа). Копия старше либо статус иной — deploy не выполнять.
7. **Deploy.** `printf 'DEPLOY\n' | ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg deploy <TAG>'` — слово `DEPLOY` передаётся на вход: без него команда остановится на вопросе. Миграция `00009` применяется при deploy, до первого прогона источников.
8. **Проверка после выката.** Через две минуты `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p13-deploy-verify.log"`: ожидается `DEPLOY VERIFY OK`, `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`, `events_merged=` как до выката. Затем первый и второй запросы проверки.
9. **После первых прогонов.** Когда каждый источник прошёл прогон (расписание — раз в шесть часов; у Tristyle — см. «Первый прогон Tristyle»), снова `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p13-after-runs-verify.log"` и три запроса проверки.

### Запросы проверки

Все три запроса только читают. Каждая строка блока — отдельный запрос:

```sql
SELECT 'sport=' || sport || '|' || count(*) FROM events GROUP BY sport ORDER BY sport;
SELECT 'type=' || sport || '|' || event_type || '|' || count(*) FROM events GROUP BY sport, event_type ORDER BY sport, event_type;
SELECT 'run_counts=' || s.code || '|' || r.id || '|' || r.status || '|created:' || COALESCE(r.metrics->>'created', '?') || '|changed:' || COALESCE(r.metrics->>'changed', '?') || '|unchanged:' || COALESCE(r.metrics->>'unchanged', '?') FROM ingest_runs r JOIN sources s ON s.id = r.source_id WHERE r.id IN (SELECT r2.id FROM ingest_runs r2 WHERE r2.source_id = r.source_id ORDER BY r2.id DESC LIMIT 3) ORDER BY s.code, r.id;
```

Root-консоль, строка блока целиком вместо `<запрос>` (в двойных кавычках): `docker compose -f /opt/aggregator/docker-compose.yml exec -T postgres psql -U aggregator -d aggregator -Atc "<запрос>"`

Первый запрос — распределение событий по видам спорта (`sport=<код>|<число>`), второй — распределение дисциплин по видам спорта (`type=<вид спорта>|<дисциплина>|<число>`), третий — три последних прогона каждого источника (тот же запрос, что в разделе о Phase 12).

Чего ждать сразу после выката, до прогона Tristyle:

- `sport=running|<n>` — одной строкой, `<n>` как до выката; строки `type=running|…` — те же, что до выката, и только прежние шесть значений;
- в Telegram: главное меню с новым текстом («Любительские старты в разных городах») и одной кнопкой «🏃 Бег»; ответ на неизвестную команду и заголовок результатов текстового запроса — со словом «старт»;
- любая иная строка `sport=` кроме `running` и `skiing` — нарушение.

Чего ждать после первых прогонов:

- `sport=skiing|<k>` — `<k>` близко к числу событий живого списка Tristyle с фильтром «Лыжи»; в главном меню появилась кнопка «⛷ Лыжи»;
- `type=skiing|…` — только дисциплины лыж: `ski`, `rollerski`, `other`; `type=running|…` — только прежние шесть значений;
- в `run_counts=` у прогонов `marathonec`, `probeg`, `heroleague` после выката — `created` и `changed` ноль либо единицы, как у прогонов до выката (шаг 2); у `tristyle` `created` близко к `<k>`, `changed` — ноль либо единицы;
- `changed`, сопоставимый с числом записей источника, — признак нарушения: новый код переписал прежние записи. Такой прогон не принимать за норму и решать об откате;
- `sport=running|<n>` отличается от значения до выката только на созданные беговые события; `events_merged=` прежнее; пар «бег — лыжи» в `/dupes` нет.

### Первый прогон Tristyle

Число записей источника вырастет на лыжные, поэтому первый прогон Tristyle новым кодом может получить статус `suspect` — это штатно, а не сбой. Записи такого прогона не применяются: лыжных событий в каталоге и кнопки «⛷ Лыжи» нет, пока владелец не посмотрит причины в сообщении бота и не примет прогон кнопкой `[Принять как базу]` → `[Да, принять]`. Если прогон `failed` либо в причинах есть что-то кроме роста числа записей — ничего не повторять и не принимать. Запасной путь принятия, если кнопка недоступна, — в разделе «Запасные пути», с `--source tristyle`.

### Откат

`agg rollback` (слово `ROLLBACK`) возвращает прежний образ. Проверка формата `event_type` в схеме остаётся и прежнему образу не мешает: он пишет только шесть прежних значений, все они формату соответствуют. Лыжные события остаются в базе, но прежний образ их не показывает: лыжи в его справочнике выключены. Выбранный вид спорта и лыжные фильтры пользователей прежний образ не читает: такие фильтры он показывает как беговые, а при первом изменении фильтров незнакомые ключи не сохраняет. Обратную миграцию и `goose down` не выполнять: с закрытым списком лыжные строки нельзя будет обновить.

## Phase 14: избранное

**Каждый шаг на production — только по отдельному разрешению владельца на этот шаг**; сам по себе документ ничего не разрешает. Каждая команда — одной строкой. Mac — из корня репозитория; root-консоль — консоль провайдера.

Предусловие: Phase 13 выкачена (образ `391b9b0fc6bc`, миграция `00009`), проверка её первых прогонов закрыта и приёмка пройдена — в `.planning/phases/13-skiing/13-05-SUMMARY.md` есть обе строки, `PRODUCTION CHECK:` и `PRODUCTION ACCEPTANCE:`. Пока первой строки нет, выкат Phase 14 не начинается: иначе изменения двух фаз в счётчиках первых прогонов не различить.

Что изменилось:

- миграция `00010` создаёт две таблицы, обе пустые; существующие таблицы не меняются;
- в карточке старта — строка избранного, в главном меню — строка «⭐ Избранное»;
- применение прогона записывает в очередь сообщения об изменениях избранных стартов и о стартах, пропавших из календаря (источник перестал их присылать, и событие скрыто из выдачи); сбор и разбор источников не менялись;
- бот отправляет сообщения из очереди по одному с паузой, с 23:00 до 08:00 по Москве — без звука, и после 10:00 по Москве создаёт напоминания за семь дней до старта.

`docker-compose.yml`, скрипты пакета и `.env` не менялись: `ship.sh` на сверке SHA256 не останавливается, переустановка пакета не нужна. Если `ship.sh` всё же остановился на сверке — ничего не устанавливать и сообщить. `DEDUP_*` в `.env` не добавлять: автослияние остаётся выключенным.

### Шаги

1. **Состояние копий.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg backup-status' 2>&1 | tee "$HOME/run-agg-phase4/p14-backup-status.log"`, ожидается `STATUS OK` и `usable=yes`. Это проверка, что копии вообще делаются; свежая копия — шаг 5.
2. **Состояние до выката.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p14-before-verify.log"`. Запомнить `events_merged=`, `events_listed=`, `dedup_candidates=` и строки `source_run=`. В root-консоли выполнить третий запрос проверки (см. ниже) и сохранить его вывод: это счётчики прогонов до выката. Первых двух запросов сейчас не выполнять: их таблиц ещё нет.
3. **Образ.** Закоммитить изменения фазы, затем `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps` → `AGG LOAD OK` и `IMAGE_TAG=<TAG>`.
4. **Preflight.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg preflight <TAG>'`.
5. **Свежая копия непосредственно перед миграцией.** Root-консоль, ручной запуск (как в `deploy/RUNBOOK.md`): `/bin/bash /usr/local/lib/aggregator/backup.sh`. Затем Mac: `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg backup-status' 2>&1 | tee "$HOME/run-agg-phase4/p14-fresh-backup-status.log"` — ожидается `STATUS OK`, `usable=yes` и возраст копии `backup_age_s` не больше 3600 (часа). Копия старше либо статус иной — deploy не выполнять.
6. **Deploy.** `printf 'DEPLOY\n' | ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg deploy <TAG>'` — слово `DEPLOY` передаётся на вход: без него команда остановится на вопросе. Миграция `00010` применяется при deploy, до первого прогона источников.
7. **Проверка после выката.** Через две минуты `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p14-deploy-verify.log"`: ожидается `DEPLOY VERIFY OK`, `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`, `events_merged=` как до выката. Затем три запроса проверки.
8. **После первых прогонов.** Когда каждый источник прошёл прогон (расписание — раз в шесть часов), снова `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p14-after-runs-verify.log"` и три запроса проверки.

### Запросы проверки

Все три запроса только читают. Каждая строка блока — отдельный запрос:

```sql
SELECT 'favorites=' || count(*) || '|users=' || count(DISTINCT tg_id) FROM favorites;
SELECT 'outbox=' || kind || '|' || state || '|' || count(*) FROM (SELECT kind, CASE WHEN done_at IS NOT NULL THEN outcome WHEN sent_at IS NOT NULL THEN 'sent' WHEN claimed_at IS NOT NULL THEN 'unknown' ELSE 'pending' END AS state FROM outbox) o GROUP BY kind, state ORDER BY kind, state;
SELECT 'run_counts=' || s.code || '|' || r.id || '|' || r.status || '|created:' || COALESCE(r.metrics->>'created', '?') || '|changed:' || COALESCE(r.metrics->>'changed', '?') || '|unchanged:' || COALESCE(r.metrics->>'unchanged', '?') FROM ingest_runs r JOIN sources s ON s.id = r.source_id WHERE r.id IN (SELECT r2.id FROM ingest_runs r2 WHERE r2.source_id = r.source_id ORDER BY r2.id DESC LIMIT 3) ORDER BY s.code, r.id;
```

Root-консоль, строка блока целиком вместо `<запрос>` (в двойных кавычках): `docker compose -f /opt/aggregator/docker-compose.yml exec -T postgres psql -U aggregator -d aggregator -Atc "<запрос>"`

Первый запрос — число записей избранного и число пользователей с избранным (`favorites=<n>|users=<m>`); второй — очередь сообщений по виду и состоянию (`outbox=<вид>|<состояние>|<число>`), где состояние — `pending` (ожидает отправки), `sent` (отправлено), `unknown` (взято в отправку, исход неизвестен, повторно не шлётся) либо код исхода закрытой строки; третий — три последних прогона каждого источника (тот же запрос, что в разделе о Phase 12). Запросы возвращают только счётчики: выбирать строки таблиц, идентификаторы пользователей и состав избранного не нужно и нельзя.

Чего ждать сразу после выката:

- `favorites=0|users=0`, строк `outbox=` нет;
- `events_merged=` и режим автослияния — как до выката;
- в Telegram: в главном меню строка «⭐ Избранное» между видами спорта и «⚙️ Мои настройки»; в карточке старта — строка «⭐ В избранное».

Чего ждать после первых прогонов:

- в `run_counts=` у каждого источника `created` и `changed` — ноль либо единицы, как у прогонов до выката (шаг 2); `changed`, сопоставимый с числом записей источника, — признак нарушения: новый код переписал прежние записи, решать об откате;
- строки `outbox=change|…` появляются, только когда у избранного старта что-то изменилось (ссылка, дата, статус);
- строки `outbox=gone|…` появляются, когда избранный старт пропал из календаря: не раньше чем через трое суток после того, как источник перестал его присылать, и только если старт был в избранном; несколько таких строк от одного прогона — повод посмотреть, не терял ли источник записи;
- строка `outbox=…|unknown|…` — оборванная отправка: сообщить;
- строка `outbox=fuse|…` — сработал предохранитель (в его счёт идут и изменения, и скрытия): смотреть сообщение бота и прогон, названный в нём;
- сообщения об изменении статуса не ждать: источники статус не передают.

### Откат

`agg rollback` (слово `ROLLBACK`) возвращает прежний образ. Таблицы остаются: прежний образ их не читает и не пишет. Кнопки избранного в уже отправленных сообщениях отвечают «Кнопка устарела». Строки очереди ждут и после повторного выката уходят, если не истёк срок годности. Обратную миграцию и `goose down` не выполнять: раздел Down удаляет избранное пользователей.

## Phase 15: Mini App

**Каждый шаг на production — только по отдельному разрешению владельца на этот шаг**; сам по себе документ ничего не разрешает. Каждая команда — одной строкой. Mac — из корня репозитория; root-консоль — консоль провайдера.

Предусловия:

- Phase 14 выкачена и проверка её первых прогонов закрыта: в `.planning/phases/14-favorites/14-07-SUMMARY.md` есть строка `PRODUCTION CHECK:`. Mac: `grep -c '^PRODUCTION CHECK:' .planning/phases/14-favorites/14-07-SUMMARY.md` — ожидается не `0`. Пока строки нет, выкат Phase 15 не начинается.
- Для этапа Б — домен владельца с A-записью на адрес сервера. Имя нигде, кроме `.env` на VPS и BotFather, не записывается; ниже оно обозначено `<домен>`. Mac, только чтение: `dig +short A <домен>` — ожидается одна строка с адресом сервера.
- Для этапа Б — порты 80 и 443 на хосте свободны. Root-консоль, только чтение: `ss -ltnH | awk '$4 ~ /:(80|443)$/ {n++} END {print "listening_80_443=" n+0}'` — ожидается `listening_80_443=0`. Если у провайдера есть свой сетевой экран, входящие 80/tcp и 443/tcp в нём должны быть разрешены; `ufw` для этого менять не нужно: порты Docker его обходят.

Что изменилось:

- в `docker-compose.yml` два новых сервиса за профилем `webapp`: `webapp` (тот же образ, подкоманда `webapp`: API и страницы Mini App, наружу не слушает) и `caddy` (официальный образ, зафиксирован тегом и дайджестом: получает и продлевает сертификат, проксирует на `webapp`);
- наружу публикуются только 80/tcp и 443/tcp сервиса `caddy` и только при включённом профиле; порт PostgreSQL по-прежнему не публикуется; `caddy` подключён к отдельной сети и базу не видит;
- миграций нет; бот и worker не менялись: их экраны, сообщения и расписание прежние;
- новые необязательные строки `.env`: `WEBAPP_SECRET` (производный ключ проверки входа; токен бота сервису `webapp` не передаётся), `WEBAPP_DOMAIN`, `COMPOSE_PROFILES`. Пока строки `COMPOSE_PROFILES=webapp` нет, состав сервисов прежний;
- изменены `docker-compose.yml`, `deploy-apply.sh`, `deploy-preflight.sh`, `deploy-verify.sh`, `rollback.sh`: `ship.sh` остановится на сверке SHA256 — это ожидаемо.

### Этап А: образ и пакет, Mini App выключен

1. **Состояние копий.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg backup-status' 2>&1 | tee "$HOME/run-agg-phase4/p15-backup-status.log"`, ожидается `STATUS OK` и `usable=yes`. Миграций нет, свежая копия перед deploy не обязательна.
2. **Состояние до выката.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p15-before-verify.log"`. Запомнить `events_merged=`, `events_listed=` и строки `source_run=`.
3. **Пакет.** Закоммитить изменения фазы, затем `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps`. Ожидаемая остановка: `STOP: … не установлены или отличаются`, образ не передаётся.
4. **Установка пакета.** Root-консоль: однострочник разовой установки из вывода `ship.sh` (его печатает `deploy/oneliner.sh`), слово `INSTALL`. При `STOP: HASH MISMATCH` ничего не запускалось — повторить шаг 3.
5. **Образ.** Снова `SSH_CONFIG=~/.ssh/run_agg.conf deploy/ship.sh run-agg-vps` → `AGG LOAD OK` и `IMAGE_TAG=<TAG>`.
6. **Preflight.** `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg preflight <TAG>'` — в разделе `.env`: `COMPOSE_PROFILES: нет (Mini App выключен)`, `профиль webapp: выключен`, строк `ВНИМАНИЕ` нет.
7. **Deploy.** `printf 'DEPLOY\n' | ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg deploy <TAG>'`.
8. **Проверка.** Через две минуты `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p15-deploy-verify.log"`: ожидается `DEPLOY VERIFY OK`, `webapp_profile=off`, `published=none`, `CHECK ports-only-caddy: OK`, `CHECK ports-caddy-80-443: OK`; в состоянии сервисов — прежние `postgres`, `migrate`, `bot`, `worker`; `events_merged=` и режим автослияния — как до выката. Бот в Telegram работает как раньше.

На этом этапе в `.env` ничего не добавлять. Этап Б можно выполнить позже, когда готов домен.

### Этап Б: включение

Три строки `.env` — root-консоль; каждая команда меняет одну строку, при повторном запуске заменяет её, а не добавляет вторую, и печатает число таких строк, а не значение (ожидается `1`).

1. **Ключ.** Вычисляется из токена бота в одноразовом контейнере сервиса `bot` и на экран не выводится: `test -s /opt/aggregator/.env && ( S=$(docker compose -f /opt/aggregator/docker-compose.yml run --rm --no-deps -T bot webapp-secret 2>/dev/null) && [[ $S =~ ^[0-9a-f]{64}$ ]] && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^WEBAPP_SECRET=' /opt/aggregator/.env; printf 'WEBAPP_SECRET=%s\n' "$S"; } > "$T" && chmod 600 "$T" && mv "$T" /opt/aggregator/.env ) && grep -c '^WEBAPP_SECRET=' /opt/aggregator/.env`. Если напечатан `0` или ничего — ключ не записан (образ без подкоманды `webapp-secret` либо нет токена): дальше не идти.
2. **Домен.** `<домен>` замените сами: `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^WEBAPP_DOMAIN=' /opt/aggregator/.env; echo 'WEBAPP_DOMAIN=<домен>'; } > "$T" && chmod 600 "$T" && mv "$T" /opt/aggregator/.env && grep -c '^WEBAPP_DOMAIN=' /opt/aggregator/.env`.
3. **Профиль.** `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^COMPOSE_PROFILES=' /opt/aggregator/.env; echo 'COMPOSE_PROFILES=webapp'; } > "$T" && chmod 600 "$T" && mv "$T" /opt/aggregator/.env && grep -c '^COMPOSE_PROFILES=' /opt/aggregator/.env`.
4. **Preflight.** Mac: `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg preflight <TAG>'` с развёрнутым `<TAG>` — ожидается `COMPOSE_PROFILES: есть`, `WEBAPP_SECRET: есть`, `WEBAPP_DOMAIN: есть`, `профиль webapp: включён`, `права: 600`, строк `ВНИМАНИЕ` нет, `compose config -q: OK`.
5. **Deploy того же тега.** `printf 'DEPLOY\n' | ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg deploy <TAG>'` — тот же `<TAG>`, что развёрнут (`IMAGE_PREV` не меняется). Образ `caddy` скачивается до запуска сервисов; не скачался — команда останавливается с сообщением, сервисы не переключались. `bot` и `worker` не пересоздаются. Команда ждёт готовности `webapp` — до минуты.
6. **Проверка.** Через две минуты `ssh -F ~/.ssh/run_agg.conf run-agg-vps 'sudo -n /usr/local/sbin/agg verify' 2>&1 | tee "$HOME/run-agg-phase4/p15-enable-verify.log"`: ожидается `DEPLOY VERIFY OK`, `webapp_profile=on`, в состоянии сервисов `webapp` — `Up … (healthy)`, `caddy` — `Up …`, ровно две строки `published=`: `published=caddy 443/tcp` и `published=caddy 80/tcp`, `CHECK port-not-published: OK`, `CHECK ports-only-caddy: OK`, `CHECK ports-caddy-80-443: OK`.
7. **Сертификат и ответы.** Mac, только чтение: `curl -sI https://<домен>/` — ожидается `200` и заголовок `content-security-policy`; `curl -s -o /dev/null -w '%{http_code}\n' https://<домен>/api/v1/meta` — ожидается `401` (без входа из Telegram данные не отдаются). Сертификат выдаётся в течение минуты после запуска `caddy`.

### BotFather

Вход в Mini App — кнопка меню бота; код и экраны бота не меняются. В BotFather: выбрать бота → настройки бота → Menu Button → задать URL `https://<домен>/` и подпись кнопки. Имя бота и домен в репозиторий не записываются. Убрать кнопку — там же (вернуть кнопку меню по умолчанию).

### Что проверить в Telegram

Открыть Mini App кнопкой меню в чате с ботом:

- главная;
- каталог: список стартов открывается и листается;
- фильтры: выдача меняется вместе с фильтром;
- поиск;
- карточка старта;
- сердечко в списке и в карточке: добавление и удаление; старт появляется и пропадает в «⭐ Избранное» бота;
- календарь;
- избранное;
- профиль;
- кнопка «Назад» Telegram возвращает на предыдущий экран;
- нижняя панель не перекрывает последние строки списка;
- тёмная тема.

### Чего ждать и что считать нарушением

- В логах `webapp` (раздел `agg verify`, только при `webapp_profile=on`) — записи `http` с методом, шаблоном маршрута, кодом ответа и длительностью; текста запросов, идентификаторов пользователей и заголовков в них нет. Логи `caddy` `agg verify` не печатает: в них имя домена.
- Нарушение: любая строка `CHECK … FAIL`; строка `published=` не у `caddy` либо с портом, отличным от 80 и 443; `webapp` в состоянии `unhealthy` или `restarting`; `caddy` не `running`. При нарушении — выключить (ниже) и сообщить.
- Сертификат не получен (`curl` сообщает об ошибке сертификата или соединения). Частые причины: A-запись не указывает на сервер либо ещё не разошлась; порт 80 или 443 занят другим процессом хоста либо закрыт сетевым экраном провайдера; в `WEBAPP_DOMAIN` опечатка. Многократно не перезапускать: у центра сертификации есть пределы числа попыток.
- `webapp` перезапускается: в его логах — имя переменной (`WEBAPP_SECRET`), которой не хватает или которая неверного формата; повторить шаг 1 этапа Б.

### Выключение

Root-консоль. Тома и остальные сервисы не затрагиваются; том `caddy_data` с сертификатами не удалять — при повторном включении сертификат возьмётся из него.

1. Убрать профиль: `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^COMPOSE_PROFILES=' /opt/aggregator/.env; } > "$T" && chmod 600 "$T" && mv "$T" /opt/aggregator/.env && grep -c '^COMPOSE_PROFILES=' /opt/aggregator/.env` — ожидается `0` (код возврата `grep` при этом ненулевой, это нормально).
2. Остановить и удалить только контейнеры `caddy` и `webapp`: `docker compose -f /opt/aggregator/docker-compose.yml --profile webapp rm -sf caddy webapp`.
3. Mac: `agg verify` — ожидается `DEPLOY VERIFY OK`, `webapp_profile=off`, `published=none`.

Строки `WEBAPP_SECRET` и `WEBAPP_DOMAIN` можно оставить: без профиля их никто не читает. Кнопку меню в BotFather убрать, иначе она откроет недоступную страницу.

### Откат

Перед `agg rollback` на образ без подкоманды `webapp` (любой образ до этой фазы) сначала выполнить «Выключение»: иначе контейнер `webapp` на прежнем образе будет перезапускаться; `agg rollback` об этом предупреждает, но `.env` сам не правит. Затем `agg rollback` (слово `ROLLBACK`). Миграций в фазе нет: `goose down` не нужен и не выполняется. Избранное, добавленное из Mini App, остаётся и видно в боте.

## Phase 17: MyRace

**Каждый шаг на production — только по отдельному разрешению владельца на этот шаг.** Источник MyRace читает только партнёрский API (`GET https://myrace.info/api/1.0/calendar`) с токеном владельца; сайт не читается. Миграций нет. Выкат совмещён с этапом А Mini App (раздел выше): шаги 1–5 и 7–8 этапа А те же, с логами `p17-*`; между доставкой образа и preflight добавляется строка токена.

### Токен

Root-консоль, одной строкой. Токен (Профиль → Интеграции → API MyRace) вводится скрыто и на экран не выводится; команда заменяет строку, а не добавляет вторую, и печатает число строк (ожидается `1`):

`test -s /opt/aggregator/.env && read -rsp 'MYRACE_TOKEN: ' S && echo && [ -n "$S" ] && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^MYRACE_TOKEN=' /opt/aggregator/.env; printf 'MYRACE_TOKEN=%s\n' "$S"; } > "$T" && chmod 600 "$T" && mv "$T" /opt/aggregator/.env; unset S; grep -c '^MYRACE_TOKEN=' /opt/aggregator/.env`

Токен читают `bot` и `worker` (`x-app-env`), `webapp` его не получает. Без строки (или с пустой) источник не подключается.

### Чего ждать

- После deploy в логе воркера — `next run` с `"source":"myrace"`.
- Первый прогон — `suspect` с причиной «первый прогон источника — требуется принятие»: администратор получает сообщение с кнопкой. Ожидаемо на 2026-10-14: разобрано около 34 записей, вне каталога около 4 (без страны), в каталог около 30; около 10 из них есть у других источников и попадут в ручную очередь пар. Цифры близки — принять кнопкой.
- `failed` с `HTTP 401` — токен не принят (скопирован не целиком или перевыпущен кнопкой Refresh): не принимать, повторить строку токена и deploy того же тега.
- После принятия: в `run_counts=` строка `myrace` с `created` около 30; `events_merged` и режим автослияния без изменений.

### Выключение

Удалить строку токена: `test -s /opt/aggregator/.env && T=$(mktemp /opt/aggregator/.env.XXXXXX) && { grep -v '^MYRACE_TOKEN=' /opt/aggregator/.env; } > "$T" && chmod 600 "$T" && mv "$T" /opt/aggregator/.env; grep -c '^MYRACE_TOKEN=' /opt/aggregator/.env` (ожидается `0`), затем deploy того же тега. Прогоны прекращаются; уже записанные события MyRace остаются в каталоге — наблюдение за пропавшими записями идёт только в прогонах источника. Скрыть их — отдельным решением.

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
- Том `caddy_data` (сертификаты Mini App) не удаляется никогда, как и том с БД: повторный выпуск сертификатов ограничен центром сертификации.
- Наружу публикуются только 80/tcp и 443/tcp сервиса `caddy` и только при включённом профиле `webapp`; у остальных сервисов опубликованных портов нет, `agg verify` это проверяет.
