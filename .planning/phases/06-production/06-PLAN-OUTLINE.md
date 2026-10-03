# Phase 06 (production) — план-набросок

Только набросок: PLAN.md не написаны, ничего не выполнено. Источники: 06-CONTEXT.md (D-01…D-24, locked), 06-RESEARCH.md (W0–W9 — вход, не порядок), ROADMAP Phase 6, REQUIREMENTS, общий контекст планирования.

Общие правила для всех планов (повторять в `must_haves.prohibitions` каждого PLAN.md):
- Исполнитель не делает `git commit`/`push`, не подключается к VPS, не запускает `ssh`/`ship.sh`/deploy, не ставит пакеты, не создаёт ботов/каналы/аккаунты, не читает `.env` и `~/.ssh`, не запрашивает и не видит токены, chat id, приватные ключи, адрес VPS, адрес пинга.
- Phase 5 не закрывается (`phase.complete 5` не выполняется, D-16); `DEDUP_AUTO_MERGE=false` не меняется (D-05); `allowedCountries` не трогается (D-06); работы Phase 7/8 не выполняются (D-01).
- Каждое изменение VPS — отдельная задача `checkpoint:human-action gate="blocking-human"`: владелец даёт разрешение на конкретный шаг и сам выполняет ОДНУ строку (hash-checked однострочник из `deploy/oneliner.sh`). Упоминание шага в зависимостях или критериях разрешением не является.
- Уровни в каждой задаче помечаются: `[T1 local]` (Mac, автономно), `[T2 host]` (изменение VPS, владелец), `[T2 read-only]` (чтение VPS, владелец), `[T3 deploy]` (новый образ/compose в production, владелец), `[owner Mac]` (действие владельца вне VPS).
- Параллелизма нет намеренно: волны 1…9 повторяют порядок D-02.

## 1. Планы

| Plan ID | Objective | Wave | Depends On | Requirements | Decisions (Phase 6) | Threat IDs | Tier mix | autonomous | Key files |
|---|---|---|---|---|---|---|---|---|---|
| 06-01 | Аудит VPS только чтением, ветвление по его результату (`age` есть/нет, `curl`, часы), ранние решения владельца, ключ `age` у владельца | 1 | — | PRD-02, PRD-03 | D-01, D-04, D-09, D-10, D-16, D-24 | T-06-01…T-06-09 | T1 local + T2 read-only + owner Mac | false | `deploy/host-audit.sh`, `deploy/backup-test.sh` (каркас, проверка «только чтение») |
| 06-02 | Локальный бэкап: конвейер шагов 1–5, проверка «пригодная копия», отметка `backup`, чистая ротация 7+4 с граничными тестами, проверка часов, минимальный размер по реальным дампам | 2 | 06-01 | PRD-03 | D-03, D-07, D-08, D-12, D-18, D-19, D-21 | T-06-10…T-06-19 | T1 local | true | `deploy/vps/backup.sh`, `deploy/vps/backup-lib.sh`, `deploy/backup-test.sh` |
| 06-03 | Внешняя копия (шаги 6–7): отправка отдельным ботом, подтверждение по ответу Bot API, отметка `backup_copy`, пороги 40/50 МБ; пакет `agg`: подкоманды, sudoers, `backup-apply.sh`, `backup-status.sh`, публичный ключ | 3 | 06-02 | PRD-03 | D-03, D-10, D-11, D-12, D-17, D-20, D-22 | T-06-20…T-06-29 | T1 local | true | `deploy/vps/backup.sh`, `deploy/vps/backup-apply.sh`, `deploy/vps/backup-status.sh`, `deploy/vps/backup-recipient`, `deploy/vps/agg`, `deploy/vps/agg-install.sh`, `deploy/vps/host-apply.sh`, `deploy/oneliner.sh`, `deploy/backup-test.sh` |
| 06-04 | Выкат бэкапа на VPS по шагам: бот и канал владельца, пакет, `INSTALL`, предпосылки (`age`, часы, каталоги), первый запуск (только локальная копия), ввод секретов владельцем, внешняя копия, cron 03:00 | 4 | 06-03 | PRD-03, PRD-02 | D-02, D-04, D-05, D-07, D-09, D-11, D-12, D-19, D-21, D-22, D-24 | T-06-30…T-06-39, T-06-SC | T2 host + T2 read-only + owner Mac | false | (репозиторий не меняется, кроме возможной правки `BACKUP_MIN_BYTES` в `deploy/vps/backup-lib.sh` до отправки пакета) |
| 06-05 | Проверка восстановления: `restore-check.sh`, локальный тест на PostgreSQL 17, проверка владельцем на Mac копии из Telegram, раздел runbook, ворота «восстановление подтверждено» | 5 | 06-04 | PRD-04, PRD-03 | D-02, D-03, D-10, D-14, D-17 | T-06-40…T-06-49 | T1 local + owner Mac + T2 read-only | false | `deploy/restore-check.sh`, `deploy/RUNBOOK.md` (создаётся: раздел восстановления и журнал), `deploy/backup-test.sh` |
| 06-06 | Мониторинг: heartbeat `bot`/`worker`, алерты просрочки и недоступной БД, backoff, `healthcheck bot|worker`, LO-06, IN-07; выкат образа и compose | 6 | 06-05 | MON-03, MON-04 | D-04, D-05, D-06, D-12, D-13, D-24 | T-06-50…T-06-59 | T1 local + T2 host (`INSTALL`) + T3 deploy | false | `internal/store/heartbeats.go`, `internal/heartbeat/`, `internal/alert/infra.go`, `cmd/aggregator/main.go`, `internal/bot/` (IN-07, гейт LO-06), `docker-compose.yml`, `deploy/vps/deploy-verify.sh` |
| 06-07 | Хост после подтверждённого восстановления: swap, `ufw`, SSH, обновления и перезагрузка, `tinyproxy`, уборка Phase 1; IN-05, IN-06 | 7 | 06-06 | PRD-02, PRD-07 | D-02, D-04, D-24 | T-06-60…T-06-69 | T1 local + T2 host | false | `deploy/vps/host-apply.sh`, `deploy/vps/host-verify.sh`, `deploy/vps/host-preflight.sh`, `deploy/confirm-test.sh`, `deploy/oneliner.sh` (IN-06), `Dockerfile` (IN-05), `docker-compose.yml` (IN-05 — только по решению) |
| 06-08 | Внешний heartbeat: выбор провайдера владельцем, `ext-heartbeat.sh` (не зависит от провайдера), ввод адреса пинга владельцем, cron, проверка «весь VPS недоступен» | 8 | 06-07 | PRD-05 | D-04, D-15, D-22, D-23, D-24 | T-06-70…T-06-79 | T1 local + T2 host | false | `deploy/vps/ext-heartbeat.sh`, `deploy/vps/backup-apply.sh`, `deploy/vps/agg-install.sh`, `deploy/oneliner.sh`, `deploy/backup-test.sh` |
| 06-09 | Runbook целиком (PRD-06), тест на расхождение с кодом, календарь проверок, итоговая приёмка владельцем | 9 | 06-08 | PRD-06, PRD-04 | D-01, D-14, D-16, D-17 | T-06-80…T-06-89 | T1 local | false | `deploy/RUNBOOK.md`, `deploy/README.md`, `deploy/backup-test.sh` |

Девять планов вместо 6–8: каждое изменение VPS — отдельная контрольная точка, а PLAN.md ограничен ~450 строками. Если нужно ровно 8 — слить 06-08 и 06-09 (получится 10–11 задач в одном плане).

Покрытие:
- Требования: MON-03 → 06-06; MON-04 → 06-06; PRD-02 → 06-01, 06-04, 06-07; PRD-03 → 06-02, 06-03, 06-04, 06-05; PRD-04 → 06-05, 06-09; PRD-05 → 06-08; PRD-06 → 06-09; PRD-07 → 06-07.
- Решения: D-01 → 01, 09; D-02 → 04, 05, 07; D-03 → 02, 03, 05; D-04 → 01, 04, 06, 07, 08; D-05 → 04, 06; D-06 → 06; D-07 → 02, 04; D-08 → 02; D-09 → 01, 04; D-10 → 01, 03, 05; D-11 → 03, 04; D-12 → 02, 03, 04, 06; D-13 → 06; D-14 → 05, 09; D-15 → 08; D-16 → 01, 09; D-17 → 03, 05, 09; D-18 → 02; D-19 → 02, 04; D-20 → 03; D-21 → 02, 04; D-22 → 03, 04, 08; D-23 → 08; D-24 → 01, 04, 06, 07, 08.
- Отложенные замечания 04-REVIEW: LO-06 → 06-06; IN-07 → 06-06; IN-05 → 06-07; IN-06 → 06-07 (CONTEXT: IN-05 и IN-06 только после проверки восстановления — поэтому не в пакете 06-03, вопреки W2 из RESEARCH).

### Скелет задач (для авторов PLAN.md)

**06-01** — 1) `[T1 local]` `deploy/host-audit.sh` (блок только чтения для stdin под `runagg`) + каркас `deploy/backup-test.sh` с проверкой, что в блоке нет изменяющих команд. 2) `[T2 read-only]` human-action: владелец запускает аудит одной строкой. 3) decision: ветвление по аудиту (см. раздел 4, №1). 4) decision: ранние проектные вопросы (№2). 5) `[owner Mac]` human-action: `brew install age`, `age-keygen`, хранение ключа по D-10; исполнителю передаётся только публичный ключ. Ветка «не `age`» = остановка и перепланирование 06-02…06-05, молча не реализуется.

**06-02** — 1) tracer `[T1 local]`: один сквозной путь `backup.sh` на заглушках `docker`/`age`/`psql`: дамп → `.partial` → проверка → переименование → `.sha256` → отметка `backup` → ротация без удалений; строка `BACKUP OK`. 2) `[T1 local]` чистая `retention_select` + `iso_week` + шесть защит; оракул `python3 isocalendar()` 2020–2040; наборы: пропуски дней, граница года и ISO-недели, меньше 7 копий, все непригодны, одна пригодная, будущая дата, чужие файлы, `.partial`; `clock_ok` перед запуском и перед ротацией. 3) `[T1 local]` сбои конвейера (сбой `pg_dump` посреди потока, нет заголовка, мал, часы не синхронизированы → нет файла, нет отметок, нет ротации, код 1); замер `pg_dump -Fc` пустой и засеянной тестовой БД PostgreSQL 17 → `BACKUP_MIN_BYTES` с обоснованием (D-21); проверка A4 (код выхода `docker compose exec -T`).

**06-03** — 1) tracer `[T1 local]`: отправка через настоящий `curl --config` в локальную заглушку Bot API (`python3`), разбор JSON (`ok` и `file_size` = локальному), отметка `backup_copy`, строка `COPY OK`; проверка A2. 2) `[T1 local]` отказы: нет файла секретов, HTTP-ошибка, несовпадение размера, > 50 МБ → `backup` остаётся, `backup_copy` нет, код 2; 40–50 МБ → `SIZE WARN`; повторная отправка той же копии. 3) `[T1 local]` пакет: `backup-status.sh`, `backup-apply.sh` (режимы `dirs`, `telegram`, `cron`), `backup-recipient`, подкоманды `agg`, sudoers, исключения `LIB`, `BUNDLE`/`installed`, режимы `host-apply.sh` `age`/`clock`/`curl` — только те, что выбраны в 06-01; тесты имён пакета и слов подтверждения.

**06-04** — все задачи контрольные: decision (значение `BACKUP_MIN_BYTES`, до отправки пакета) → бот и канал → `ship.sh` → `INSTALL` → `age` (если выбрано) → часы (если нужно) → `dirs` → первый запуск (ожидается код 2: `BACKUP OK`, `COPY FAIL reason=no-config` — доказательство D-12 в production) → `telegram` → второй запуск (`COPY OK`) → `cron` → чтение `agg backup-status` на следующее утро.

**06-05** — 1) tracer `[T1 local]`: `restore-check.sh` на локальном PostgreSQL 17: дамп тестовой БД → настоящий `age` с одноразовым тестовым ключом во временном каталоге → расшифровка → `pg_restore` в отдельный контейнер → сверка счётчиков → `RESTORE OK`. 2) `[T1 local]` `deploy/RUNBOOK.md`: раздел «Восстановление» (ежемесячная проверка, аварийное восстановление, допустимые расхождения счётчиков, ограничения Telegram из CONTEXT) и журнал проверок. 3) `[owner Mac]` human-action: владелец скачивает копию из Telegram и запускает `restore-check.sh`. 4) human-verify (ворота): «восстановление подтверждено» — без него 06-06 и далее не начинаются.

**06-06** — 1) decision: MON-03 (№5). 2) tracer `[T1 local]`: `store.Beat` → строка `heartbeats` → `store.HeartbeatAges` → `InfraLoop` → фейковая отправка `worker_stale` (тестовый PostgreSQL). 3) `[T1 local]` полный `InfraLoop`: `backup_stale`, `backup_copy_stale`, `postgres_down`, напоминание 24 ч, восстановление, backoff 1→15 мин, подавление при недоступной БД. 4) `[T1 local]` `healthcheck bot|worker`, гейт LO-06, IN-07, `docker-compose.yml`, `deploy-verify.sh`; `go.mod`/`go.sum` без изменений. 5–8) `ship.sh` → `INSTALL` №2 → `agg deploy <TAG>` → чтение `agg verify`; 9) проверка алерта остановкой `worker` — по отдельному разрешению, допустим отказ.

**06-07** — 1) decision: `tinyproxy`, уборка Phase 1, SSH, IN-05 (№6–9). 2) tracer `[T1 local]`: режим `host-apply.sh swap` сквозь `confirm-test.sh` (слово → команды-заглушки) + `host-verify.sh`. 3) `[T1 local]` остальные выбранные режимы, IN-06 в `oneliner.sh`, IN-05 в `Dockerfile`. Дальше контрольные: `ship.sh` → `INSTALL` №3 → swap → `ufw` → SSH → `tinyproxy` → обновления → перезагрузка → уборка → чтение `agg host-verify` и `agg verify`. Пишутся только режимы, выбранные в задаче 1.

**06-08** — 1) decision: провайдер (№10). 2) tracer `[T1 local]`: `ext-heartbeat.sh` на заглушках: свежие `bot` и `worker` → `PING OK`; БД недоступна / отметка старше 10 мин → `PING SKIP`, `curl` не вызывается; режимы `backup-apply.sh` `heartbeat`, `heartbeat-cron`, `heartbeat-cron-off`. Дальше контрольные: проверка у провайдера (аккаунт владельца) → `ship.sh` → `INSTALL` №4 → `heartbeat` → `heartbeat-cron` → проверка недоступности.

**06-09** — 1) `[T1 local]` `deploy/RUNBOOK.md` полностью (структура Q11 из RESEARCH) + тест расхождения: каждый вид алерта из кода есть в runbook. 2) `[T1 local]` ссылки из `deploy/README.md`, полный локальный прогон (`bash deploy/backup-test.sh`, `go test ./internal/...`). 3) human-verify: владелец читает runbook и подтверждает; PRD-03 формулируется по D-03/D-17 (временное хранилище, независимой второй копии нет); `phase.complete` не выполняется.

## 2. Cross-plan contracts

### Файлы пакета (`deploy/vps/`, имена по правилу `^[a-z-]+(\.sh|\.yml)?$`)

| Файл | Где на VPS | Создаёт | Назначение |
|---|---|---|---|
| `backup.sh` | `/usr/local/lib/aggregator/backup.sh` | 06-02, дополняет 06-03 | ночной и ручной запуск |
| `backup-lib.sh` | `/usr/local/lib/aggregator/backup-lib.sh` | 06-02 | чистые функции; подключается через `source` из `backup.sh`, `backup-status.sh`, `ext-heartbeat.sh`, `deploy/restore-check.sh`; совместим с bash 3.2 |
| `backup-status.sh` | `/usr/local/lib/aggregator/backup-status.sh` | 06-03 | только чтение |
| `backup-recipient` | `/usr/local/lib/aggregator/backup-recipient` | 06-03 | одна строка — публичный ключ `age` (формат `^age1[0-9a-z]{58}$`); не секрет |
| `backup-apply.sh` | не устанавливается в `lib` (root-консоль, как `host-apply.sh`) | 06-03, дополняет 06-08 | изменения хоста для бэкапа и heartbeat |
| `ext-heartbeat.sh` | `/usr/local/lib/aggregator/ext-heartbeat.sh` | 06-08 | пинг раз в 5 минут |

Вне пакета (Mac): `deploy/backup-test.sh`, `deploy/restore-check.sh`, `deploy/host-audit.sh`, `deploy/RUNBOOK.md`.

Отступление от RESEARCH: публичный ключ лежит в файле пакета `backup-recipient` (таблица «Секреты и доступ» CONTEXT: «в скрипте бэкапа»), а не в `/etc/aggregator-backup/age-recipient`; файл пинга называется `heartbeat.curl`, а не `hc.curl`. Скрипт определения chat id на Mac не пишется: владелец вводит идентификатор сам, способ узнать его — в runbook.

### Функции `backup-lib.sh`

`iso_week DATE`, `retention_select TODAY` (stdin: строки `YYYY-MM-DD<TAB>usable|bad|unverified`; stdout: `keep DATE` / `delete DATE`; код ≠ 0 при защитах №2 и №4), `clock_ok`, `counters_sql`, `copy_state FILE`, `usable FILE`, `usable_new FILE`, `publish OUT`, `mark COMPONENT` (S11). Константы: `BACKUP_MIN_BYTES` (значение и обоснование — 06-02), `BACKUP_WARN_BYTES=40000000`, `BACKUP_MAX_BYTES=50000000`.

### Пути на VPS

| Путь | Права | Создаёт |
|---|---|---|
| `/var/backups/aggregator/` | root:root 700 | `backup-apply.sh dirs` |
| `/var/backups/aggregator/aggregator-YYYY-MM-DD.dump.age` + `.sha256` + `.meta`; временные `.partial`, `.partial.sha256`, `.partial.meta` (S11) | 600 | `backup.sh` |
| `/etc/aggregator-backup/` | root:root 700 | `backup-apply.sh dirs` |
| `/etc/aggregator-backup/telegram.curl` | root:root 600 | `backup-apply.sh telegram` (вводит владелец) |
| `/etc/aggregator-backup/heartbeat.curl` | root:root 600 | `backup-apply.sh heartbeat` (вводит владелец) |
| `/etc/cron.d/aggregator-backup` | root:root 644 | `backup-apply.sh cron`; строка `0 3 * * * root /bin/bash /usr/local/lib/aggregator/backup.sh` |
| `/etc/cron.d/aggregator-heartbeat` | root:root 644 | `backup-apply.sh heartbeat-cron`; строка `*/5 * * * * root /bin/bash /usr/local/lib/aggregator/ext-heartbeat.sh` |
| `/var/log/aggregator-backup.log` | root:root 600 | `backup-apply.sh dirs` |
| `/run/aggregator-backup.lock` | — | `flock` в `backup.sh` |
| `/opt/aggregator/docker-compose.yml` | существует | — |

Шаблон имён для ротации: `^aggregator-[0-9]{4}-[0-9]{2}-[0-9]{2}\.dump\.age(\.sha256|\.meta)?$`; дата в имени — UTC.

### Подкоманды `agg` и слова подтверждения

| Команда | Вид | Слово | План |
|---|---|---|---|
| `agg backup-status` | только чтение | — | 06-03 |
| `agg backup-run` | изменяющая; только если владелец выберет её в 06-01, иначе запуск из root-консоли | `BACKUP` (stdin) | 06-03 |
| `backup-apply.sh dirs` | root-консоль | `DIRS` | 06-03 |
| `backup-apply.sh telegram` | root-консоль, скрытый ввод `read -rs` | `TELEGRAM` | 06-03 |
| `backup-apply.sh cron` | root-консоль | `CRON` | 06-03 |
| `backup-apply.sh heartbeat` | root-консоль, скрытый ввод | `HEARTBEAT` | 06-08 |
| `backup-apply.sh heartbeat-cron` / `heartbeat-cron-off` | root-консоль | `HEARTBEATCRON` / `HEARTBEATOFF` | 06-08 |
| `host-apply.sh age` / `clock` / `curl` | root-консоль; пишутся только выбранные в 06-01 | `AGE` / `CLOCK` / `CURL` | 06-03 |
| `host-apply.sh swap` / `ufw` / `ssh` / `tinyproxy` / `reboot` / `cleanup` | root-консоль; пишутся только выбранные в 06-07 | `SWAP` / `UFW` / `SSH` / `TINYPROXY` / `REBOOT` / `CLEANUP` | 06-07 |

Существующие `INSTALL`, `UPDATES`, `COMPOSE`, `DEPLOY` не меняются. Строка sudoers в `agg-install.sh` дополняется `backup-status` (и `backup-run`, если выбрана), без аргументов.

### Отметки `heartbeats` (новых таблиц и миграции 00005 нет)

| component | Кто пишет | Когда | Порог |
|---|---|---|---|
| `backup` | `backup.sh` (хост, `psql` в контейнере `postgres`) | после проверки локальной копии, до ротации и отправки | 26 ч |
| `backup_copy` | `backup.sh` | только после `ok` и совпадения `file_size` в ответе Bot API | 26 ч |
| `bot` | процесс `bot`, раз в 60 с, только при успешном `getMe` и без ошибок `getUpdates` за 2 мин | — | 10 мин; `healthcheck` — 5 мин |
| `worker` | процесс `worker`, отдельная горутина, раз в 60 с | — | 10 мин; `healthcheck` — 5 мин |

### Go (06-06)

- `internal/store/heartbeats.go`: `Beat(ctx, component string) error`, `HeartbeatAges(ctx) (map[string]time.Duration, error)` — возраст считает БД (`now()`).
- `internal/heartbeat`: `Run(ctx, beat func(context.Context) error, every time.Duration, gate func() bool)`.
- `internal/alert/infra.go`: `InfraLoop`, чистая `decide`; состояние в памяти.
- Виды алертов (ключи, они же строки для теста расхождения с runbook): `worker_stale`, `backup_stale`, `backup_copy_stale`, `postgres_down`; `bot_stale` — только запись в лог `worker`; `backup_size_warn` — только если владелец выберет активный алерт в 06-01.
- `healthcheck` (как сейчас, БД), `healthcheck bot`, `healthcheck worker`.
- `docker-compose.yml`: `healthcheck` у `worker` (`["CMD","/aggregator","healthcheck","worker"]`), у `bot` — аргумент `bot`, `stop_grace_period: 20s` у `bot`. Новых переменных окружения нет; `DEDUP_AUTO_MERGE` не трогается.

### Строки вывода, по которым проверяют следующие задачи

| Источник | Строки |
|---|---|
| `backup.sh` | `BACKUP OK file=<имя> size=<байт>`; `BACKUP FAIL stage=clock|dump|verify|sync|mark`; `ROTATE OK keep=<n> delete=<m>`; `ROTATE ABORT reason=empty-keep|future-date|clock|unexpected-object|delete`; `RECOVER OK|FAIL file=<имя>`; `RECOVER SKIP file=<имя> reason=size`; `ROTATE UNVERIFIED count=<n> dates=<до 10 дат>` (S11); `COPY OK size=<байт>`; `COPY FAIL reason=no-config|too-large|http|mismatch|mark`; `COPY HTTP status=<код HTTP|000> curl=<код выхода|skipped> error_code=<n|none|tmpfile>` — строка перед `COPY FAIL reason=http` (06-03: только коды, без тела ответа и сообщений `curl`; `curl=skipped error_code=tmpfile` — файл для ответа создать не удалось, `curl` не запускался); `SIZE WARN bytes=<n>` |
| коды возврата `backup.sh` | `0` — обе отметки, ротация выполнена; `1` — копии нет или отметка `backup` не записана (в том числе `BACKUP FAIL stage=sync`), ротации нет; `2` — локальная копия и `backup` есть, `backup_copy` нет; `3` — обе отметки есть, ротация остановлена (`ROTATE ABORT`) |
| `backup-status.sh` | `backup_age_s=`, `backup_copy_age_s=`, `copies=`, `latest=<имя> size=<байт> usable=yes|no`, `clock=ok|unsynced`, `cron=present|absent`, `telegram_config=present|absent`, итог `STATUS OK` / `STATUS WARN` |
| `backup-apply.sh` | `APPLY OK mode=<режим>`; для `telegram` дополнительно `TELEGRAM OK` (проверка `getChat`, без вывода значений) |
| `restore-check.sh` | `file=<имя> sha256=ok size=<байт>`; `counts=<строка>`; при расхождении `expected=…` и `actual=…`; перед `RESTORE FAIL` — `detail stage=age exit=<код>` / `detail stage=pg_restore exit=<код> age_exit=<код> kind=archive-version|archive-format|data|query|other [table=<имя>]` / `detail stage=counters exit=<код>` (без текста сообщений и строк данных); последняя строка `RESTORE OK counters=match migration=<n>` (код 0) / `RESTORE FAIL reason=sha|decrypt|restore|counters` (код 1, отказ копии или восстановления); `STOP: <причина>` в stderr, код 2 — проверку нельзя выполнить (окружение, подпись, подтверждённый сбой Docker или контейнера), строк `RESTORE` нет; `INTERRUPTED signal=INT|TERM|HUP` в stderr, коды 130 / 143 / 129, строк `RESTORE` нет |
| `ext-heartbeat.sh` | `PING OK` / `PING SKIP reason=db|bot|worker` |
| `host-audit.sh` | строки `AUDIT <ключ>=<значение>` (`age`, `gpg`, `curl`, `flock`, `jq`, `python`, `cron`, `ntp_synchronized`, `timezone`, `swap`, `disk_free_mb`, `ufw`, `reboot_required`, `universe`), конец `AUDIT END`; адрес и имя хоста не печатаются; sudo блок не вызывает, `ufw` всегда `unknown` (не проверяется этим блоком) |
| `deploy/backup-test.sh` | итог `BACKUP TESTS OK` |

Подпись внешней копии: `aggregator-DATE.dump.age sha256=<64 hex> size=<байт> counts=<строка counters_sql>` (≤ 1024 символов); та же строка счётчиков — в `.meta`.

### Прочее

- Вывод аудита и команд чтения — в `$HOME/run-agg-phase6/` вне репозитория; исполнитель читает только эти файлы.
- `INSTALL` выполняется четыре раза (06-04, 06-06, 06-07, 06-08), каждый — отдельное разрешение.
- Все `<automated>` начинаются с `cd "/Users/grishan.a/Desktop/Бот ТГ" &&`.
- Допущения для записи в планах: bash для бэкапа и ротации; состояние алертов в памяти (повтор одного алерта после перезапуска бота); пороги 40/50 МБ в десятичных байтах; трактовка D-18 (A5) — подтверждается в 06-01.
- Риск для 06-07: образ `postgres:17` на VPS загружается из tar (`load-postgres.sh`) и может не иметь `RepoDigests`; закрепление digest в `docker-compose.yml` тогда не сработает и пересоздаст контейнер БД — вынесено в решение №9.

## 3. Owner permissions

Каждый пункт — отдельное явное разрешение владельца; команду выполняет владелец.

1. Аудит VPS только чтением: блок `deploy/host-audit.sh` под `runagg`, вывод в `$HOME/run-agg-phase6/host-audit.log` — 06-01, задача 2.
2. Mac владельца: `brew install age`, создание ключа `age-keygen`, хранение по D-10 — 06-01, задача 5.
3. Создание бота бэкапов и закрытого канала, бот — администратор канала (аккаунт владельца) — 06-04, задача 2.
4. Отправка пакета бэкапа на VPS (`deploy/ship.sh` запускает владелец) — 06-04, задача 3.
5. `INSTALL` №1 в root-консоли (новые файлы пакета, строка sudoers) — 06-04, задача 4.
6. Установка `age` (`host-apply.sh age`) — только если аудит показал отсутствие и владелец выбрал установку — 06-04, задача 5.
7. Установка `curl` (`host-apply.sh curl`) — только если аудит показал отсутствие — 06-04, задача 5а.
8. Исправление синхронизации часов (`host-apply.sh clock`) — только если аудит показал `ntp_synchronized=no`, способ выбран в 06-01 — 06-04, задача 6.
9. Каталоги и файл лога (`backup-apply.sh dirs`) — 06-04, задача 7.
10. Первый запуск бэкапа (создаёт файл и строку `backup` в production-БД) — 06-04, задача 8.
11. Ввод токена бота бэкапов и идентификатора канала в root-консоли (`backup-apply.sh telegram`) — 06-04, задача 9.
12. Второй запуск: отправка внешней копии в канал, строка `backup_copy` — 06-04, задача 10.
13. Запись в cron на 03:00 (`backup-apply.sh cron`) — 06-04, задача 11.
14. Чтение `agg backup-status` на следующее утро — 06-04, задача 12.
15. Проверка восстановления на Mac: скачивание копии из Telegram, расшифровка приватным ключом, отдельный контейнер `postgres:17`; чтение `agg verify` для сверки — 06-05, задача 3.
16. Отправка образа и пакета мониторинга (`deploy/ship.sh`) — 06-06, задача 5.
17. `INSTALL` №2 (изменён `docker-compose.yml`) — 06-06, задача 6.
18. Production deploy образа (`agg deploy <TAG>`) — 06-06, задача 7.
19. Проверка алерта в production остановкой `worker` дольше 10 минут и запуском обратно — 06-06, задача 9 (владелец может отказать; тогда приёмка по локальным тестам).
20. Отправка пакета хоста (`deploy/ship.sh`) — 06-07, задача 4.
21. `INSTALL` №3 — 06-07, задача 5.
22. Swap 1–2 ГБ (`host-apply.sh swap`) — 06-07, задача 6.
23. Включение `ufw` (`host-apply.sh ufw`, только из консоли провайдера) — 06-07, задача 7.
24. Ужесточение SSH (`host-apply.sh ssh`) — только если выбрано — 06-07, задача 8.
25. Действие с `tinyproxy` (`host-apply.sh tinyproxy`) — только если выбрано — 06-07, задача 9.
26. Обновления пакетов (`host-apply.sh updates`) — 06-07, задача 10.
27. Перезагрузка (`host-apply.sh reboot`) — 06-07, задача 11.
28. Уборка Phase 1 на VPS (`host-apply.sh cleanup`, состав по решению) и удаление локальных файлов Phase 1 на Mac самим владельцем — 06-07, задача 12.
29. Закрепление digest `postgres` в compose с пересозданием контейнера БД — только если выбрано — 06-07, задача 13.
30. Регистрация у провайдера heartbeat, создание проверки и каналов оповещения (аккаунт владельца) — 06-08, задача 3.
31. Отправка пакета heartbeat (`deploy/ship.sh`) — 06-08, задача 4.
32. `INSTALL` №4 — 06-08, задача 5.
33. Ввод адреса пинга в root-консоли (`backup-apply.sh heartbeat`) — 06-08, задача 6.
34. Запись в cron раз в 5 минут (`backup-apply.sh heartbeat-cron`) — 06-08, задача 7.
35. Проверка «весь VPS недоступен»: `heartbeat-cron-off`, ожидание дольше grace, алерт провайдера, `heartbeat-cron`, восстановление — 06-08, задача 8.

Команды только чтения после шага (`agg verify`, `agg host-verify`, `agg backup-status`) тоже запускает владелец; они входят в контрольную точку своего шага и ничего на VPS не меняют.

Все `git commit` делает владелец; в списке разрешений их нет, потому что исполнитель их не выполняет ни при каком ответе.

## 4. Owner decisions as checkpoints

| № | Вопрос | Тип | План, задача | Варианты (исполнитель не выбирает) |
|---|---|---|---|---|
| 1 | Результат аудита: `age` есть/нет; `curl`; часы | `checkpoint:decision` | 06-01, задача 3 | `age` есть → дальше; `age` нет → установить пакет `age` (отдельное разрешение в 06-04) / `gpg` / другое (два последних = перепланирование); часы не синхронизированы → другой NTP-сервер в `timesyncd` / `chrony` / сначала диагностика |
| 2 | Ранние проектные вопросы: нужна ли `agg backup-run`; канал предупреждения о размере 40–50 МБ; трактовка D-18 (A5) | `checkpoint:decision` | 06-01, задача 4 | `backup-run`: подкоманда со словом `BACKUP` / только root-консоль; размер: только `SIZE WARN` в логе, подписи и `backup-status` / ещё и активный алерт `backup_size_warn` через бота; A5: подтвердить / иное чтение |
| 3 | Значение `BACKUP_MIN_BYTES` и его обоснование (D-21) | `checkpoint:decision` | 06-04, задача 1 (до отправки пакета) | принять / изменить |
| 4 | Ворота «восстановление подтверждено» (D-02, D-14) | `checkpoint:human-verify` | 06-05, задача 4 | подтверждено / нет (тогда 06-06…06-09 не начинаются) |
| 5 | MON-03: `worker` без токена Telegram | `checkpoint:decision` | 06-06, задача 1 | лог + docker healthcheck + остановка внешнего пинга / выдать `worker` токен (расширяет доступ) |
| 6 | Судьба `tinyproxy` (PRD-07) | `checkpoint:decision`, reversibility `costly` | 06-07, задача 1 | оставить / `Listen 127.0.0.1` / закрыть 8888 в `ufw` / отключить / удалить |
| 7 | Состав уборки Phase 1 | `checkpoint:decision`, reversibility `costly` | 06-07, задача 1 | по списку из аудита: `/etc/sudoers.d/run-agg`, ключ `run-agg-phase1`, `/home/runagg/agg-deploy/postgres17.tar`, `/tmp/tmp.*`, `/var/tmp/agg-load.*` — каждый пункт да/нет |
| 8 | Ужесточение SSH и включение `ufw` (риск потери доступа) | `checkpoint:decision`, reversibility `costly` | 06-07, задача 1 | делать / не делать; только из консоли провайдера |
| 9 | Объём IN-05 | `checkpoint:decision` | 06-07, задача 1 | только `FROM` в `Dockerfile` (вступает в силу со следующим образом) / ещё и `postgres` в compose (пересоздание контейнера БД, нужен `RepoDigests`) / только проверка идентификатора образа в `deploy-verify.sh` |
| 10 | Провайдер внешнего heartbeat и канал его оповещений (D-23) | `checkpoint:decision` | 06-08, задача 1 | не менее двух вариантов по критериям Q8 из RESEARCH |
| 11 | Перезагрузка на новое ядро | разрешение, reversibility `costly` | 06-07, задача 11 | — |

Не контрольные точки Phase 6: состав версии v1.27.0 (решение о дорожной карте), включение автослияния, объектное хранилище (отложено).

## 5. Сверка планов (дополняет и уточняет раздел 2; при расхождении действует этот раздел)

**S1. Запись решений 06-01.** Раздел «Запись решений» в `06-01-SUMMARY.md`, строки с начала строки, читаются только `grep -x`: `DECISION age=present|install|stop`, `DECISION curl=present|install|stop`, `DECISION clock=ok|ntp-server|chrony|diagnose`, `DECISION backup_run=agg|root-console`, `DECISION size_warn=log|alert`, `DECISION a5=confirmed|other`, `NTP_SERVER=<host>[ <host>…]` (только при `DECISION clock=ntp-server`: имена хостов со слов владельца, проверка `grep -xE 'NTP_SERVER=([A-Za-z0-9-]+\.)+[A-Za-z]{2,}( ([A-Za-z0-9-]+\.)+[A-Za-z]{2,})*'`; без этой строки решение не записывается; из неё 06-03 пишет режим `clock`, по ней 06-04 активирует задачу 6), `AGE_RECIPIENT=age1…`, `AUDIT_SHA256=…`, при остановке `REPLAN <планы>: <причина>`, при `DECISION clock=diagnose` — `REPLAN 06-04: диагностика часов` (06-01…06-03 выполняются, 06-04 ждёт перепланирования с контрольной точкой диагностики только чтением). Планы 06-02, 06-03, 06-04, 06-06 читают именно эти строки. Значения ключей `AUDIT`: `age`, `gpg`, `curl`, `flock`, `jq`, `python` — `present|absent`; `cron` — `active|inactive|failed|unknown`; `ntp_synchronized` — `yes|no|unknown`; `timezone` — `<зона>@<±ЧЧММ>|unknown`; `swap`, `disk_free_mb` — целое; `ufw` — всегда `unknown`: блок `ufw` не читает и sudo не вызывает, значение означает «не проверялось этим блоком» и состоянием firewall не является (в действующей `agg host-verify` строк об `ufw` нет; они появляются только с 06-07, задача 3 и `INSTALL` №3); `reboot_required`, `universe` — `yes|no|unknown` (`unknown` — прочитать не удалось, а не «нет»).

**S2. Коды возврата `backup.sh`.** `0` — обе отметки, ротация выполнена. `1` — нет пригодной копии ИЛИ отметка `backup` не записана (`BACKUP FAIL stage=mark` или `stage=sync` после публикации; пригодная копия при этом не удаляется; S11); ротации и отправки нет. `2` — локальная копия и `backup` есть, `backup_copy` нет. `3` — обе копии и обе отметки есть, ротация остановлена (`ROTATE ABORT`); ничего не удалено (кроме `reason=delete`, S11). Если не удалась и отправка, и ротация — код `2`. Отправка выполняется всегда, когда шаги 1–4 прошли, независимо от `ROTATE ABORT`. Причины отказа отправки: `no-config|too-large|http|mismatch|mark` (`mark` — файл доставлен, `backup_copy` не записана; `COPY OK` печатается только после отметки). В 06-02 до появления шагов 6–7 успешный локальный запуск возвращает `2`; 06-03 меняет это ожидание в `deploy/backup-test.sh`.

**S3. Ворота восстановления.** 06-05 пишет в `06-05-SUMMARY.md` строку `RESTORE GATE: CONFIRMED <дата>` либо `RESTORE GATE: NOT CONFIRMED`. Планы 06-06, 06-07, 06-08, 06-09 в первой задаче проверяют `grep -q '^RESTORE GATE: CONFIRMED '` — не свободную фразу.

**S4. Предупреждение о размере с активным алертом.** Только при `DECISION size_warn=alert`: `mark` в `backup-lib.sh` принимает третью компоненту `backup_size_warn`; `backup.sh` (06-03) вызывает её, когда размер копии в полосе 40–50 МБ; `InfraLoop` (06-06) держит алерт `backup_size_warn`, пока отметка моложе 26 ч (обратное условие по отношению к алертам просрочки); runbook (06-09) это описывает. Границы полосы включены (`BACKUP_WARN_BYTES` ≤ размер ≤ `BACKUP_MAX_BYTES`); сбой записи этой отметки на код запуска и отправку не влияет. При `size_warn=log` ничего из этого не пишется. Строка в таблице `heartbeats`: `backup_size_warn` — `backup.sh`, условно.

**S5. Состояние heartbeat с Mac.** 06-08 дополняет `deploy/vps/backup-status.sh` строками `heartbeat_config=present|absent` и `heartbeat_cron=present|absent` (значения не печатаются; на `STATUS OK|WARN` не влияют). Задачи 6–8 плана 06-08 используют вывод `agg backup-status` как доказательство.

**S6. `counters_sql` и подпись.** Строка счётчиков — пары `ключ=значение` через пробел, содержит `events_listed=` и `users=`, последняя пара `migration=<n>` (формат задаёт 06-02). Первая строка подписи — ровно по разделу 2; `SIZE WARN bytes=<n>` — вторая строка. Интерфейс: `deploy/restore-check.sh <файл.dump.age> <файл ключа> '<подпись>'`; ошибки окружения — `STOP: …` в stderr, код 2.

**S7. Разрешения сверх раздела 3.** Раздел 3 — исходный список; фактический состав — контрольные задачи планов. Добавились: вторая отправка `ship.sh` после `INSTALL` (образ) в 06-06; повторный аудит только чтением после установки `age`/`curl` и чтение `agg verify` в 06-04; `INSTALL` №3б и `agg deploy` для закрепления digest `postgres` в 06-07; откат cron и секретов удалением файла в root-консоли; смена ключа `runagg`, если выбрана (S8). `INSTALL` выполняется четыре раза, при выборе digest в compose — пять.

**S8. Роль и ключи `runagg`** (06-CONTEXT, этап 5). Решение владельца в 06-07, задача 1: только зафиксировать текущее состояние (чтение, `agg host-preflight`) / сменить ключ (новый ключ создаёт владелец на Mac; публичный ключ добавляется до удаления старого; старый удаляется только после успешного `agg verify` с новым) / отложить с записанной причиной. Исполнитель приватных ключей не видит и `~/.ssh` не читает. Смена ключа — отдельная контрольная точка.

**S9. Для 06-09.** Дополнительно: правка строки `deploy/README.md` о перезагрузке, если в 06-07 выбран режим `reboot`; в runbook — поведение после `agg rollback` на образ без мониторинга (отметки `bot`/`worker` останавливаются; прежний образ инфраструктурных алертов не шлёт; внешний пинг прекращается, и алерт присылает только провайдер, если 06-08 уже выполнен); оговорка, что проверка «весь VPS недоступен» имитировала пропажу пингов, а возобновление пингов после настоящей перезагрузки не проверялось, с ручной проверкой после следующей перезагрузки; способ узнать идентификатор канала из 06-04, задача 2; код `3` и `reason=mark`.

**S10. Образ `postgres` закреплён до первого выката (D-26); планы не делятся (D-25).** Механизм в 06-06, вступает в силу с `INSTALL` №2, то есть до `agg deploy`: функция `start` в `deploy/vps/deploy-apply.sh` не выполняет `docker compose pull postgres`, если образ `postgres:17` уже есть на хосте (печатает `postgres image: local, pull skipped`); `pull` остаётся только для хоста без образа. Так выкат не меняет образ и не пересоздаёт контейнер БД независимо от доступности Docker Hub и наличия `RepoDigests`. `deploy/vps/deploy-verify.sh` (`agg verify`) печатает строку `postgres_image id=<sha256:…> tag=same|differs repo_digest=<sha256:…|none>` (`id` — образ работающего контейнера; `tag=same`, если тег `postgres:17` на хосте указывает на тот же образ); `tag=differs` перед выкатом — стоп-условие: тег сдвинут вне выката, и `up` пересоздал бы контейнер БД. Задача выката в 06-06 требует `tag=same` в логе до выката, сверяет `id` в логах до и после и пишет в SUMMARY `POSTGRES IMAGE: UNCHANGED|CHANGED` и `POSTGRES CONTAINER: KEPT|RECREATED`; `CHANGED` или `RECREATED` — стоп-условие. Тест в `deploy/deploy-apply-test.sh`: при имеющемся образе `pull` не вызывается, при отсутствующем — вызывается. Digest в `docker-compose.yml` до выката не ставится: смена строки `image` сама пересоздаёт контейнер БД и требует `RepoDigests`; он остаётся необязательным решением владельца в 06-07 (`in05-compose`), которое теперь читает `postgres_image` из `agg verify`. Осознанная смена образа `postgres` — отдельная процедура в runbook (06-09), не побочный эффект выката.
**S11. Публикация копии, доведение, запись на диск и состояния (06-02, по итогам ревью 2026-10-02).** Уточняет раздел 2 и S2; правила 7 + 4 и решения владельца не меняет.
- *Состояния копии* (`copy_state FILE`): `usable` — заголовок `age`, сумма в `.sha256` совпадает, `.meta` по формату; `unverified` — данные с заголовком есть, суммы или метаданных нет либо они неверны; `bad` — данных нет, ссылка или нет заголовка. `unverified` в 7 + 4 не входит и автоматически не удаляется никогда — только вручную владельцем; число и даты печатает `ROTATE UNVERIFIED count=<n> dates=<до 10 дат>`. Порог `BACKUP_MIN_BYTES` применяется только к новой копии (`usable_new`), уже лежащие копии по нему не переоцениваются.
- *Проверка метаданных ограничена форматом*: `.meta` сверяется с шаблоном строки счётчиков, а не с данными. Соответствие счётчиков данным доказывает только проверка восстановления (06-05).
- *Временные имена*: `.partial`, `.partial.sha256`, `.partial.meta` (пишутся в этом порядке). *Публикация* — три rename: сумма, метаданные, данные последними; итоговое имя, занятое каталогом или ссылкой, — отказ до первого rename. После rename суммы и до rename данных копия дня — `unverified` (прежние данные, новая сумма); побайтно до публикации сохраняется только файл данных прежней копии.
- *Доведение* (`recover`, в начале каждого запуска, до проверки часов): целая временная тройка любой даты, прошедшая ту же проверку, что новая копия, публикуется — `RECOVER OK file=<имя>`; публикация не удалась — `RECOVER FAIL file=<имя>`; тройка ниже порога — `RECOVER SKIP file=<имя> reason=size`, итоговая копия не трогается. Неполные и повреждённые остатки пропускаются молча. Доведение ничего не удаляет и отметку не ставит.
- *Запись на диск*: `sync` трёх временных файлов — после их проверки, до публикации; `sync` каталога копий — после публикации и повторной проверки, до отметки. Ненулевой код любого из них — `BACKUP FAIL stage=sync`, код 1, отметка `backup` не ставится; при первом — публикации нет и прежняя копия дня цела, при втором — новая копия остаётся на диске. Ограничения: локально проверены только место вызовов и обработка кода возврата (заглушка); сам `fsync`, утилита GNU и файловая система проверяются первым запуском на VPS (06-04); устойчивость к отключению питания испытанием не подтверждается; `recover` собственного `sync` не делает — доведённые файлы попадают на диск со следующим `sync` каталога или штатной записью ядра.
- *Отметка `backup`* ставится только после: проверки временной тройки, `sync`, публикации, повторной проверки итоговых файлов (та же сумма и те же счётчики) и `sync` каталога. Она никогда не снимается и не откатывается: при любом сбое остаётся прежняя отметка, алерт приходит по её возрасту. Следствие: если повторный запуск того же дня оборван после rename суммы и до rename данных, до следующего запуска копия дня `unverified` при свежей отметке от первого запуска; следующий запуск доводит публикацию.
- *Ротация*: до первого удаления проверяются все шесть имён каждой удаляемой даты; каталог или иной объект, не файл и не ссылка, — `ROTATE ABORT reason=unexpected-object`, не удалено ничего. Отказ удаления — `ROTATE ABORT reason=delete`: ротация останавливается на этой дате; даты, обработанные раньше, уже удалены целиком, у этой даты могла быть удалена часть файлов (данные удаляются первыми, остаток — состояние `bad`). Слова «ничего не удалено» в S2 верны для причин `empty-keep`, `future-date`, `clock`, `unexpected-object`, но не для `delete`. `BACKUP OK` печатается до ротации и относится к копии и отметке; исход ротации — отдельная строка и код по S2 (в 06-02 — всегда 2).
**S12. Диагностика часов перед выкатом бэкапа (перепланирование 06-04, 2026-10-02).** Уточняет S1 для значения `DECISION clock=diagnose`. 06-04 начинается с трёх шагов, которые хост не меняют: (0) локальный блок `deploy/clock-diag.sh` и его тесты; (0а) владелец по отдельному разрешению запускает блок под `runagg` только чтением — лог `$HOME/run-agg-phase6/p6-04-clock-diag.log`, строки `MAC epoch_before=`, 13 строк `CLOCK ключ=значение` (`ntp_synchronized`, `ntp_enabled`, `can_ntp`, `timesyncd_unit`, `timesyncd_active`, `timesyncd_enabled`, `other_time_service`, `virt`, `ntp_servers_configured`, `server_contacted`, `packet_count`, `stratum`, `epoch`), `CLOCK END`, `MAC epoch_after=`; адреса и имена серверов времени не печатаются; (0б) решение владельца `clock-ok|ntp-server|chrony|stop`. Достаточно для продолжения без изменений хоста: `CLOCK ntp_synchronized=yes` и расхождение `epoch` с Mac не больше 5 секунд. При `clock-ok`, `ntp-server`, `chrony` строка `DECISION clock=diagnose` в `06-01-SUMMARY.md` заменяется на `DECISION clock=ok|ntp-server|chrony` (при `ntp-server` добавляется строка `NTP_SERVER=`), строка `REPLAN 06-04: диагностика часов` снимается; при `ntp-server` и `chrony` режим `host-apply.sh clock` дописывается локально (06-04, задача 0в — по описанию 06-03, задача 4) и запускается только отдельным разрешением в задаче 6. При `stop`, при неоднозначном состоянии и при способе исправления вне этих двух (включение или установка штатной службы, действия провайдера, изменение правила D-19) план останавливается до нового перепланирования; автоматического исправления часов нет. Блок диагностики в пакет не входит (пакет — 17 файлов). До `CLOCK DECISION OK` пакет на VPS не отправляется и `INSTALL` не выполняется.


## OUTLINE COMPLETE
