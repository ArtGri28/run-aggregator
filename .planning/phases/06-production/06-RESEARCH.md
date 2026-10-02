# Phase 6: Production - Research

**Researched:** 2026-10-01
**Domain:** эксплуатационная готовность одного VPS (Docker Compose, cron на хосте, `age`, Telegram Bot API, PostgreSQL 17, bash-скрипты пакета `agg`)
**Confidence:** MEDIUM — репозиторий и схема доставки прочитаны полностью (HIGH); состояние хоста после Phase 4–5 и наличие `age`/`curl`/`gpg` на VPS не известны (аудит не выполнялся), провайдер heartbeat не выбран.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
Все решения 06-CONTEXT.md — архитектурные, не являются разрешением на реализацию, изменения VPS, создание ботов/каналов или выкат.

- **D-01:** Phase 6 — только Production. D-21, L1–L5, калибровка и отмена слияния — Phase 7; UX бота — Phase 8 (D-26 в 05-CONTEXT.md).
- **D-02:** Порядок: **локальный бэкап → внешняя копия → проверка восстановления → мониторинг → изменения хоста и любые действия, способные изменить каталог.** До подтверждённого восстановления хост не меняется.
- **D-03:** Локальный бэкап **не считается** выполнением внешнего хранения. PRD-03 не закрывается, пока не реализована внешняя копия и не проверено восстановление.
- **D-04:** Каждое изменение VPS — только после отдельного разрешения владельца на конкретный шаг. Исполнитель к VPS не подключается; команды владельцу — одной строкой.
- **D-05:** `DEDUP_AUTO_MERGE=false` (D-23 Phase 5); фаза его не меняет.
- **D-06:** Каталог — RU и BY, общий `allowedCountries` (D-24 Phase 5); индивидуальные списки стран у источников не реализуются без отдельного требования.
- **D-07:** Ежедневный зашифрованный дамп PostgreSQL; **RPO не более 24 часов.** Объектные хранилища на первом этапе не используются.
- **D-08:** Ротация локальных копий — **7 ежедневных и 4 еженедельные** (PRD-03); точная логика — раздел «Ротация 7 + 4».
- **D-09:** Шифрование — **`age`**, если аудит VPS подтвердит его наличие. Если `age` нет — сначала варианты владельцу; ничего не устанавливается самостоятельно.
- **D-10:** Приватный ключ создаёт и хранит владелец: основная копия на Mac, резервная на отдельном офлайн-носителе. На VPS и в репозитории — только публичный ключ. Приватный ключ исполнителю не передаётся и не запрашивается.
- **D-11:** Внешняя копия — **закрытый Telegram-канал**; файл отправляет **отдельный Telegram-бот с отдельным токеном** (не бот проекта). Независимая копия пока не настроена.
- **D-12:** Две независимые отметки в существующей таблице `heartbeats` (новых таблиц нет): `backup` — локальная зашифрованная копия создана и прошла проверку; `backup_copy` — отправка внешней копии подтверждена ответом Telegram. Успешная локальная копия **не** считается успешной внешней: отметка `backup_copy` ставится только после подтверждения отправки, отметка `backup` от неё не зависит.
- **D-13:** Просрочка `backup` или `backup_copy` — **26 часов** от последней отметки; бот проекта уведомляет администратора отдельно по каждой отметке (один алерт на смену состояния, напоминание через 24 часа — как существующие алерты).
- **D-14:** Проверка восстановления — **ежемесячно, вручную владельцем на Mac** по runbook. Не проверенная восстановлением копия бэкапом не считается.
- **D-15:** Внешний heartbeat обязателен для Phase 6 (PRD-05); провайдер не выбран — варианты представляются отдельно.
- **D-16:** Планирование Phase 6 разрешено при формально открытой Phase 5; `phase.complete 5` не выполняется.
- **D-17:** Telegram-канал принимается как **временное внешнее хранилище** резервных копий для MVP с ограничениями; PRD-03 в части внешнего хранения закрывается этим решением после реализации и проверенного восстановления.
- **D-18:** Логика ротации 7 + 4 из раздела «Ротация 7 + 4» принята как проектное решение.
- **D-19:** Ежедневный запуск — **03:00 по времени VPS**, только после проверки синхронизации системных часов; проверка синхронизации выполняется и перед каждой ротацией.
- **D-20:** Порог предупреждения о размере копии — **40 МБ**; превышение лимита отправки Telegram (50 МБ) — ошибка внешней копии.
- **D-21:** Минимальный размер пригодной копии не задаётся произвольно: определяется на этапе реализации по реальным тестовым дампам и записывается с обоснованием.
- **D-22:** Токены и идентификаторы вводит владелец непосредственно на VPS; исполнитель их не запрашивает, не видит и не выводит.
- **D-23:** Внешний heartbeat обязателен (PRD-05), провайдер не выбирается при планировании: план содержит контрольную точку выбора и проверку недоступности всего VPS.
- **D-24:** В планах: отдельная контрольная точка с запросом разрешения владельца перед каждым изменением VPS; чёткое разделение локальных тестов, изменений хоста и production deploy; аудит VPS только чтением — отдельная задача-контрольная точка, при планировании не выполняется.

Также locked (разделы 06-CONTEXT.md): «Архитектура бэкапа» (шаги 1–7, запись в `.partial`, проверка, отметки), «Ротация 7 + 4» (определения и 6 защит), «Ограничения Telegram как хранилища», «Секреты и доступ».

### Claude's Discretion
Разбиение на планы и волны, формат runbook, реализация heartbeat `bot`/`worker` в таблице `heartbeats` (есть с миграции 00001), формат лога скрипта бэкапа.

### Deferred Ideas (OUT OF SCOPE)
- Объектное хранилище (S3-совместимое) как вторая независимая копия.
- Защита внешних копий от удаления при взломе VPS (ключ «только запись», версионирование) — вместе с объектным хранилищем.
- Всё, что отнесено к Phase 7/8 (D-21 Phase 5, L1–L5, `/dupes` UX, `/stats`, `/broadcast`, включение автослияния).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| MON-03 | heartbeat `bot`/`worker` раз в минуту, взаимное слежение (>10 мин → алерт), `healthcheck` и docker healthcheck | §3 (heartbeat), §7 (healthcheck), Q «worker→bot» в Open Questions |
| MON-04 | алерты при недоступной PostgreSQL и сбое бэкапа; Telegram недоступен → лог + backoff | §3, §6 |
| PRD-02 | хост подготовлен (swap, часы, ufw, пользователь без root, Compose; обновление/перезагрузка по разрешению) | §9 |
| PRD-03 | ночной шифрованный `pg_dump`, ключ не на VPS, внешнее хранилище, 7+4 | §1, §2, §4 |
| PRD-04 | восстановление проверено в отдельном контейнере, шаги в runbook, периодичность | §5 |
| PRD-05 | внешний heartbeat (dead man's switch) | §8 |
| PRD-06 | runbook: развёртывание, бэкап/восстановление, реакция на алерты, обновление парсера | §11 |
| PRD-07 | судьба `tinyproxy` определена владельцем | §9 |
</phase_requirements>

## Summary

Фаза почти целиком состоит из shell-скриптов хоста, одной небольшой Go-доработки (heartbeat/алерты/healthcheck) и документации. Новых Go-зависимостей не нужно. Главные факты, меняющие план:

1. **Ротацию и бэкап писать на bash, не на Go.** Бэкап должен работать, даже если образ приложения сломан или откатан (`agg rollback`); distroless-образ не имеет оболочки, а Go-подкоманда в образе привязала бы бэкап к `IMAGE_TAG`. ISO-неделя считается чистой целочисленной арифметикой (без GNU `date`, совместимо с bash 3.2 на Mac), проверяется оракулом `python3 isocalendar()` на всех датах 2020–2040 (python3 есть и на Mac, и на VPS — `agg-install.sh` требует его). Тесты — по образцу `deploy/*-test.sh` с заглушками `docker`/`age`/`curl` в `PATH`.
2. **Весь «host-side» код доставляется существующим путём** (`BUNDLE` в `oneliner.sh` → `SHA256SUMS` → `agg-install.sh`), но есть три узких места: имена файлов обязаны подходить под `^[a-z-]+(\.sh|\.yml)?$` (без цифр, подчёркиваний и `.sql`); скрипты root-консоли надо добавить в исключения `LIB=` (`agg-install.sh:46`) и в `installed` (`oneliner.sh`); новые подкоманды `agg` требуют правки `case`, `usage` и строки sudoers в `agg-install.sh:53`.
3. **Состояние инфра-алертов — в памяти, без миграции 00005.** `source_alerts` не подходит: FK на `sources`, `CHECK (state IN ('suspect', 'failed'))` (`migrations/00003_source_alerts.sql:4-5`). D-12 подразумевает «новых таблиц нет». Цена: после перезапуска бота ещё не устраненный алерт уходит повторно один раз. Недоступность БД в любом случае должна детектироваться без БД.
4. **Внешний heartbeat — cron на хосте, а не worker.** Хостовый скрипт раз в 5 минут пингует секретный URL только если БД отвечает и `heartbeats.bot`/`worker` свежие (<10 мин). Он покрывает «весь VPS упал», «Docker/Postgres упал», «бот-алертер мёртв» (когда Telegram-алерт отправить некому) и замену провайдера (меняется один файл с URL). Worker не имеет токена Telegram (`docker-compose.yml`: токен только у `bot`), поэтому «worker следит за bot» = лог + docker healthcheck + остановка внешнего пинга. Это интерпретация MON-03 — нужно подтверждение владельца.
5. **Конфликт порядка D-02 и реальных предпосылок бэкапа:** установка `age` (если нет), каталог, cron, файлы секретов и, возможно, починка часов — это изменения хоста, нужные до первого бэкапа. Их надо явно выделить в «предпосылки бэкапа» (узкие шаги, каждый с разрешением владельца), а swap/ufw/перезагрузка/tinyproxy/SSH — оставить после проверенного восстановления.
6. **На Ubuntu 24.04 пакет `age` — версия 1.1.1 без `age-inspect`** (только `age`, `age-keygen`). Заголовок проверяется побайтово: первые 21 байт `age-encryption.org/v1`. Целостность полезной нагрузки без приватного ключа проверить нельзя — её подтверждает только ежемесячное восстановление.
7. **На Mac владельца `age` не установлен** (проверено `command -v`); для ключа и restore-теста нужен `brew install age` — шаг владельца.

**Primary recommendation:** Реализовать `backup.sh` + `backup-lib.sh` (чистая ротация) + `ext-heartbeat.sh` на bash в `deploy/vps/` с локальным тестовым набором; Go — только `internal/heartbeat`, `internal/alert/infra.go`, `store/heartbeats.go`, `healthcheck bot|worker`; первую установку и секреты делать скриптом root-консоли с подтверждающим словом и скрытым вводом (`read -rs`), как `host-apply.sh`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Ночной `pg_dump` + `age` + ротация | Хост (cron root + bash) | Контейнер `postgres` (исполняет `pg_dump`) | В distroless-образе нет оболочки/`pg_dump`; бэкап не должен зависеть от `IMAGE_TAG` |
| Отметки `backup`/`backup_copy` | Хост (`psql` в контейнере `postgres`) | БД (`heartbeats`) | Отметку ставит тот, кто знает результат; БД — единственное общее хранилище |
| Heartbeat `bot`/`worker` | Приложение (горутина в каждом процессе) | БД | Процесс жив ⇒ пишет; возраст считает SQL `now()` — нет рассинхрона часов |
| Алерты о просрочке/БД | Приложение (`bot`, единственный с токеном и админами) | — | Только у `bot` есть Telegram-токен и `ADMIN_TG_IDS` |
| Алерт «весь хост/бот/БД не отвечает» | Внешний сервис (dead man's switch) | Хост (cron-пинг) | Остальное не может сообщить, когда мертво само |
| Внешняя копия | Хост (`curl` → Bot API, отдельный бот) | Telegram-канал | D-11; токен только в root-файле на хосте |
| Секреты бэкапа/heartbeat | Хост: root-файлы 600 | — | D-22; не `.env` приложения |
| Проверка восстановления | Mac владельца (`age`, `postgres:17` контейнер) | Telegram-клиент (скачивание) | D-14; приватный ключ не покидает Mac |
| Доставка скриптов хоста | Пакет `agg` + hash-checked однострочник | root-консоль провайдера | Существующая граница доверия Phase 4 |

## Standard Stack

### Core
| Инструмент | Версия | Назначение | Почему |
|---|---|---|---|
| `age` | 1.1.1 в Ubuntu noble (universe) [VERIFIED: packages.ubuntu.com/noble/age] | шифрование на публичный ключ | D-09; шифрование требует только публичного ключа [CITED: github.com/FiloSottile/age] |
| `bash` + coreutils | Ubuntu 24.04 | скрипты хоста | уже стандарт `deploy/vps/*` |
| `curl` | на VPS не подтверждён | `sendDocument`, пинг heartbeat | [ASSUMED] установлен; ветвление после аудита |
| `psql`/`pg_dump` 17 | внутри `postgres:17` | дамп, отметки | `deploy-verify.sh:57` уже ходит так: `docker compose -f "$F" exec -T postgres psql -U aggregator -d aggregator -Atc "$1"` |
| `python3 -I` | на VPS есть (проверяется `agg-install.sh`) | разбор JSON ответа Telegram | `jq` на VPS не гарантирован |
| `flock`, `sha256sum`, `timedatectl` | util-linux/coreutils/systemd | блокировка, SHA-256, проверка часов | стандартные |
| Go (без новых зависимостей) | 1.27.1, pgx v5.11.0, go-telegram/bot v1.27.0 [VERIFIED: go.mod:3,6,7] | heartbeat, алерты, healthcheck | CLAUDE.md пользователя: новых зависимостей без одобрения нет |

### Alternatives Considered
| Вместо | Можно | Компромисс |
|---|---|---|
| `age` | `gpg --encrypt -r` | ключевая связка на VPS, больше движущихся частей; только если `age` нельзя поставить и владелец откажется от бинарника |
| Ротация на bash | Go-подкоманда `backup-plan` в образе | `ISOWeek()` из stdlib удобнее, но бэкап зависит от образа/`IMAGE_TAG`, `compose run` тянет весь env сервиса; откат образа ломает бэкап |
| Хостовый cron-пинг | пинг из worker | worker не видит «упал хост», не проверяет БД/бот независимо |
| Состояние алертов в памяти | миграция `00005_infra_alerts` | миграция переживает перезапуск, но противоречит духу D-12 и добавляет store-код; решает единственную проблему — повтор алерта после рестарта |

**Установка (только по разрешению владельца, D-04/D-09):** VPS — `apt-get install -y --no-install-recommends age` (новый режим `host-apply.sh age` со словом подтверждения); Mac — `brew install age` (шаг владельца).

## Package Legitimacy Audit

Seam `gsd_run query package-legitimacy check` поддерживает только `npm|pypi|crates`; пакеты этой фазы — системные (apt/Homebrew), проверить seam-ом нельзя. Go/npm/pip-зависимости фаза не добавляет.

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `age` | Ubuntu noble universe `1.1.1-1ubuntu0.24.04.3` | n/a | n/a | github.com/FiloSottile/age | не проверялся seam-ом | Установка только после разрешения владельца; планировщик ставит `checkpoint:human-verify` перед `apt install` и сверкой `apt-cache policy age` в аудите |
| `age` | Homebrew (Mac) | n/a | n/a | github.com/FiloSottile/age | не проверялся seam-ом | Шаг владельца |

**Removed [SLOP]:** none. **Flagged [SUS]:** none (проверка не применима; см. выше). Имя пакета подтверждено на packages.ubuntu.com (официальный реестр дистрибутива) и в README upstream.

## Architecture Patterns

### System Architecture Diagram

```
 cron 03:00 (root, /etc/cron.d/aggregator-backup)
   |
   v
 backup.sh --flock--> [clock_ok? timedatectl NTPSynchronized] --no--> exit≠0, без дампа/отметок/ротации
   |yes
   v
 docker compose exec -T postgres pg_dump -Fc ──pipe──> age -r <pubkey> ──> aggregator-DATE.dump.age.partial
   |  (pipefail; сбой любого звена => rm .partial, exit≠0)
   v
 verify: size>=MIN, head -c21 == "age-encryption.org/v1", sha256sum → .sha256, rename (атомарно)
   |
   +--> psql INSERT heartbeats('backup')            ← отметка 1
   +--> clock_ok? → backup-lib: retention_select (чистая) → rm только имён по шаблону
   +--> curl --config tg.curl (sendDocument) → JSON ok && document.file_size == local
           |ok                                  |fail
           v                                    v
     psql heartbeats('backup_copy')       лог, exit≠0 (локальная копия и 'backup' остаются)

 бот-процесс:  heartbeat 'bot' (раз в мин, если getMe ok и нет свежих ошибок getUpdates)
 воркер-процесс: heartbeat 'worker' (отдельная горутина, не блокируется прогонами)
 бот: InfraLoop раз в мин → store.HeartbeatAges() (age по now() БД)
        ├─ worker >10 мин / backup >26 ч / backup_copy >26 ч → Telegram админам (state в памяти, напоминание 24 ч)
        └─ запрос/Healthcheck БД упал N раз подряд → «PostgreSQL недоступна» (без БД)
 cron каждые 5 мин: ext-heartbeat.sh → psql: bot,worker свежие (<10 мин)? → curl --config hc.curl → провайдер
        (нет пинга ⇒ провайдер алертит по своему каналу: хост/Docker/БД/бот мертвы)
 Mac владельца: Telegram-клиент → .dump.age → age -d (ключ на Mac) → pg_restore в throwaway postgres:17 → сверка счётчиков
```

### Recommended Project Structure
```
deploy/vps/backup.sh            # ночной запуск (cron) и ручной (agg backup-run)
deploy/vps/backup-lib.sh        # чистые функции: iso_week, retention_select, clock_ok, sql счётчиков; source-ится
deploy/vps/backup-apply.sh      # root-консоль: режимы dirs-cron | telegram | heartbeat (слово подтверждения, скрытый ввод) — в исключения LIB
deploy/vps/ext-heartbeat.sh     # cron каждые 5 мин
deploy/vps/backup-status.sh     # agg backup-status (только чтение)
deploy/backup-test.sh           # локальный набор: ротация, iso-week оракул, поток backup.sh с заглушками
deploy/restore-check.sh         # Mac: расшифровка + restore + счётчики (source backup-lib.sh)
deploy/RUNBOOK.md               # PRD-06
internal/store/heartbeats.go    # Beat(component), HeartbeatAges(ctx)
internal/heartbeat/             # Run(ctx, store, component, every, gate func() bool)
internal/alert/infra.go         # InfraLoop + чистая decide
```

### Pattern 1: Бэкап-конвейер (pipefail, `.partial`)
**What:** дамп никогда не касается диска в открытом виде; результат появляется под финальным именем только после проверки.
```bash
# Source: логика 06-CONTEXT.md «Архитектура бэкапа»; docker compose exec -T как в deploy-verify.sh:57
set -Eeuo pipefail
umask 077
OUT="$DIR/aggregator-$D.dump.age"; P="$OUT.partial"
trap 'rm -f -- "$P" "$P.sha256"' ERR EXIT   # на успехе после mv файлов уже нет
docker compose -f "$F" exec -T postgres pg_dump -U aggregator -d aggregator -Fc \
  | age -r "$(cat /etc/aggregator-backup/age-recipient)" > "$P"
# pipefail ловит падение pg_dump и age; затем проверки
[ "$(stat -c %s "$P")" -ge "$MIN_BYTES" ] || fail "слишком мал"
[ "$(head -c 21 "$P")" = "age-encryption.org/v1" ] || fail "нет заголовка age"
mv -- "$P" "$OUT"
( cd "$DIR" && sha256sum "${OUT##*/}" > "${OUT##*/}.sha256" )
```
`head -c 21` — это ровно длина строки `age-encryption.org/v1` [CITED: C2SP age.md, «First line»]. Обрыв потока при уже записанном заголовке виден только расшифровкой (финальный чанк обязателен) [CITED: C2SP age.md].

### Pattern 2: Отметка из хоста
```bash
mark() { docker compose -f "$F" exec -T postgres psql -U aggregator -d aggregator -v ON_ERROR_STOP=1 -Atc \
  "INSERT INTO heartbeats(component) VALUES ('$1') ON CONFLICT (component) DO UPDATE SET beat_at = now()"; }
```
Схема: `heartbeats(component TEXT PRIMARY KEY, beat_at TIMESTAMPTZ NOT NULL DEFAULT now())` [VERIFIED: migrations/00001_init_schema.sql:97-100]. `$1` — только литералы `backup`/`backup_copy` из скрипта, не пользовательский ввод.

### Pattern 3: Секреты в curl-конфиге (не в argv)
Файл `/etc/aggregator-backup/telegram.curl` (root:root 600) — формат конфига curl, построчно: `url = "https://api.telegram.org/bot<…>/sendDocument"` и `form = "chat_id=<…>"` [опция `form` в конфиге — ASSUMED, проверяется локальным тестом с фиктивным сервером]. Скрипт вызывает `curl -sS --max-time 300 --connect-timeout 20 --config FILE -F document=@FILE -F caption=… -o resp.json -w '%{http_code}'`: токен не попадает в `ps`, лог и историю. `-v`/`--trace` запрещены; ошибки curl печатают хост, не путь с токеном. Тот же приём для heartbeat (`hc.curl` с одной строкой `url`).

### Pattern 4: Чистая ротация (bash, без GNU date)
**Интерфейс:** `retention_select TODAY < строки "YYYY-MM-DD<TAB>usable|bad"` → строки `keep DATE` / `delete DATE` / код возврата ≠0 при защите №2/№4. Дни от эпохи — алгоритм days_from_civil (целочисленный); день недели `(days+3)%7` (пн=0, 1970-01-01 — четверг); четверг недели `T=days-wd+3`; ISO-год = наибольший Y∈{y-1,y,y+1}, у которого `days_from_civil(Y,1,1) ≤ T`; неделя = `(T-jan1(Y))/7+1`. Дата в имени валидируется «туда-обратно» (отвергает `2026-02-31`). [ASSUMED — набросок; обязательный тест: сравнение с `python3 date.isocalendar()` на 2020-01-01…2040-12-31 + фиксированные границы: 2020-12-31→2020-W53, 2021-01-03→2020-W53, 2021-01-04→2021-W01, 2024-12-30→2025-W01.]
Трактовка D-18 для тестов: недели перебираются от новейшей; неделя пропускается, если её представитель уже в ежедневном наборе; берутся первые 4 оставшиеся; итог ≤ 11 файлов. Это единственное допущение формулировки — зафиксировать в тесте (см. Assumptions A5).

### Pattern 5: Heartbeat-писатель и infra-алерты (Go)
- `store.Beat(ctx, component)` — `INSERT … ON CONFLICT (component) DO UPDATE SET beat_at = now()`; `store.HeartbeatAges(ctx)` — `SELECT component, EXTRACT(EPOCH FROM now()-beat_at)`: возраст считает БД, как и запись.
- `internal/heartbeat.Run(ctx, beat func(ctx) error, every, gate func() bool)`: тикер 1 мин; `gate==false` ⇒ запись пропускается (бот: `getMe` не прошёл или `lastTelegramErr` моложе 2 мин). Вызов через `wg.Go` в `runBot` и `runWorker` (шаблон — `wg.Go(func() { loop.Run(ctx) })`, `cmd/aggregator/main.go:207`).
- Worker-горутина heartbeat независима от прогонов (прогон до `INGEST_RUN_TIMEOUT=30m`); зависший планировщик при живом процессе не виден — известное ограничение.
- `alert.InfraLoop` по образцу `alert.Loop` (`Send func(ctx, chatID, text, acceptRunID)` из `b.SendAlert`, `Now`, `Log`, `Every`; чистая `decide`) — состояние `map[key]struct{alerted bool; remindedAt}` в памяти; события: stale→`send`, держится ≥24 ч→`remind`, снова свежий→`recover` (сообщение «восстановлено» для инфра-алертов, в отличие от тихого `reset` у источников). Ключи: `worker_stale`, `backup_stale`, `backup_copy_stale`, `postgres_down`.
- Postgres down: тик `HeartbeatAges` упал или `Healthcheck` упал ⇒ счётчик подряд идущих; при ≥3 (≈3 мин, исключает сглаженные блипы) — алерт `postgres_down` (отправка не использует БД); пока БД недоступна, остальные алерты подавляются (их состояние неизвестно). Отсутствие строки `backup` = просрочка (порядок D-02 гарантирует, что первая отметка появится до включения мониторинга).
- Backoff отправки (MON-04): сейчас `withRetry` повторяет только 429, один раз, до `maxRetryWait=30s` [VERIFIED: internal/bot/tg.go:75, 95-120: `var maxRetryWait = 30 * time.Second`, «Повтор один»]. Для infra: при неудаче отправки следующая попытка через 1→2→4→…→15 мин (в памяти), WARN в лог один раз на смену, сетевые ошибки Telegram — только лог (MVP.md §11.2: «Telegram API недоступен — только лог; ретраи»).

### Anti-Patterns to Avoid
- **Дамп в открытом виде на диск** (даже временно) и `pg_dump` до `age` без `pipefail` — «успешный» пустой файл.
- **Отметка `backup_copy` по коду выхода `curl`** — нужен разбор JSON: `ok==true` и `result.document.file_size == local`.
- **Токен/URL в аргументах** (`curl https://…/bot<TOKEN>/…`, `psql -c`) — виден в `ps` и истории.
- **Ротация до отметки и проверки** текущего запуска; удаление по маске шире `^aggregator-\d{4}-\d{2}-\d{2}\.dump\.age(\.sha256)?$`.
- **Бэкап через `docker compose run` образа приложения** — связывает бэкап с релизом.
- **Docker healthcheck как «самовосстановление»** — без оркестратора `restart: unless-stopped` реагирует только на выход процесса, `unhealthy` контейнер не перезапускается [ASSUMED]; healthcheck даёт видимость (`agg verify`) и сигнал, не лечение.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Шифрование дампа | свой формат/openssl-обвязка | `age -r` | аутентифицированный стрим, ключ только публичный |
| Разбор JSON ответа Telegram | `grep`/`sed` по телу | `python3 -I -c 'import json…'` | `jq` на VPS не гарантирован; python3 проверяется `agg-install.sh` |
| Защита от параллельного запуска | lock-файлы руками | `flock -n /run/aggregator-backup.lock` | атомарно, снимается при смерти процесса |
| Контроль «часы идут верно» | сравнение с HTTP Date | `timedatectl show -p NTPSynchronized --value` | формат `NTPSynchronized=no` уже наблюдался на VPS [VERIFIED: ~/run-agg-phase4/host-preflight.log, вывод `host-preflight.sh:51`] |
| Хранение/перенос секретов | запись токена в `.env` приложения/репозиторий | root-файл 600, `curl --config` | D-22, таблица «Секреты и доступ» |
| ISO-неделя | парсинг `date +%G-%V` (GNU-only) | целочисленная арифметика + тест-оракул | `date -d` нет на macOS; тесты должны идти на Mac и VPS одинаково |
| Планирование | systemd-таймер «потому что модно» | `/etc/cron.d/aggregator-backup` | cron на хосте уже активен [VERIFIED: 01-VPS-AUDIT.md §2 «Сервисы» перечисляет `cron`]; имя без точек [CITED: Debian cron(8)] |

## Common Pitfalls

### Pitfall 1: Пустой/обрезанный «успешный» бэкап
**What goes wrong:** `pg_dump` падает на середине, `age` дописывает финал, файл с валидным заголовком, проверки размера/заголовка проходят.
**Why:** без `pipefail` код выхода — у последнего звена; заголовок age пишется до данных.
**How to avoid:** `set -o pipefail`, проверка `PIPESTATUS`, нижний порог размера (D-21), ежемесячное восстановление как единственная полная проверка. Тест-заглушка `docker` с `exit 1` посреди потока обязана дать: нет файла, нет отметок, нет ротации, код ≠0.
**Warning signs:** размер резко ниже предыдущих; `agg backup-status` показывает расхождение.

### Pitfall 2: cron-окружение
**What goes wrong:** `docker`/`age` не найдены (PATH), `docker compose` не видит `.env`, вывод теряется.
**How to avoid:** в начале скрипта `export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C`; `-f /opt/aggregator/docker-compose.yml` (проектный `.env` рядом подхватывается — как в `deploy-verify.sh`); вывод — в `/var/log/aggregator-backup.log` (root 600) + `logger`. Файл в `/etc/cron.d` — строка с полем пользователя `root`, права root:root 644 [CITED: Debian cron(8)].

### Pitfall 3: Часы не синхронизированы
**What goes wrong:** на VPS `NTPSynchronized=no`, `Packet count: 0` (`01-VPS-AUDIT.md` §2 «Часы», `host-preflight.log`); имена копий и ротация (возраст >7 дней, ISO-недели) опираются на `date`.
**How to avoid:** `clock_ok` перед запуском и перед ротацией; ожидание до 30 мин с проверкой раз в минуту, затем выход ≠0 без дампа (D-19). Причина `Packet count: 0` неизвестна (UDP 123 блокируется провайдером? нужен `timedatectl timesync-status`) — диагностика входит в аудит, лечение (другой NTP-сервер в `timesyncd.conf.d` или chrony) — отдельное разрешение.

### Pitfall 4: ufw и Docker
**What goes wrong:** ufw не защищает опубликованные порты Docker (iptables-цепочки Docker идут раньше) [CITED: MVP.md §10.3 «Порты Docker обходят ufw»]; включение ufw без `allow 22/tcp` блокирует SSH.
**How to avoid:** `ufw allow OpenSSH` и `ufw status` до `ufw enable`, включать из root-консоли провайдера (не по SSH), после — проверка новым ssh-сеансом и `agg verify`; порты проекта не публикуются (`CHECK port-not-published`). `IPV6=yes` в `/etc/default/ufw` проверить. Исходящий трафик (Telegram, NTP, heartbeat) не режется (default allow outgoing) [ASSUMED — поведение по умолчанию].

### Pitfall 5: Бот не может сообщить, что мёртв он сам / БД
**What goes wrong:** бот падает на старте при недоступной БД (`openStore` → `Healthcheck`, `main.go:129-145`), цикл рестартов без единого алерта.
**How to avoid:** внешний heartbeat (§8) как независимый путь; DB-down алерт работает только пока бот жив.

### Pitfall 6: Ложные алерты при первом включении
**What goes wrong:** мониторинг включён раньше первой отметки `backup`.
**How to avoid:** порядок D-02 (бэкап → монитор); при восстановлении БД из дампа строки `backup*` приходят старыми — алерт верный.

### Pitfall 7: Рост одиночного дампа до 50 МБ
**What goes wrong:** `sendDocument` отклоняет >50 МБ [CITED: core.telegram.org/bots/api#senddocument]; предупреждение 40 МБ (D-20) никого не будит, если его некому показать.
**How to avoid:** при 40–50 МБ — `WARN` в лог, в подпись и в `agg backup-status`; >50 МБ — ошибка внешней копии (нет `backup_copy` → алерт через 26 ч). Активный алерт «размер» — вопрос владельцу (Open Questions).

### Pitfall 8: Пакет `agg` и имена файлов
**What goes wrong:** `agg-install.sh` останавливается на имени вне `^[a-z-]+(\.sh|\.yml)?$`; скрипт root-консоли попадает в `/usr/local/lib/aggregator` и в проверку `own … root:root 644`; изменение `docker-compose.yml`/скриптов требует повторного `INSTALL`.
**How to avoid:** имена `backup.sh`, `backup-lib.sh`, `backup-apply.sh`, `ext-heartbeat.sh`, `backup-status.sh`; `backup-apply.sh` добавить в `grep -vxE` (`agg-install.sh:46`) и в `awk` `installed` (`oneliner.sh`) и в `BUNDLE`; перед сборкой плана — один совместный пакет изменений `agg`/sudoers/compose, чтобы владелец делал `INSTALL` один раз (отдельно Phase 4 README: «Если изменились `agg`, скрипты VPS или `docker-compose.yml`, `ship.sh` остановится на сверке SHA256 — повторите шаг 2»).

## Code Examples

### Проверка отправки (разбор ответа без jq)
```bash
# Source: Bot API: «On success, the sent Message is returned»; поля ok, result.document.file_size [CITED: core.telegram.org/bots/api#senddocument]
python3 -I -c 'import json,sys
r=json.load(open(sys.argv[1])); want=int(sys.argv[2])
ok = r.get("ok") is True and r["result"]["document"]["file_size"] == want
sys.exit(0 if ok else 1)' "$RESP" "$(stat -c %s "$OUT")"
```
Подпись (≤1024 символов [CITED: Bot API caption «0-1024 characters after entities parsing»]): `aggregator-DATE.dump.age sha256=<64 hex> size=<байт> counts=<…>`.

### Счётчики для сверки при восстановлении
Единый SQL в `backup-lib.sh` (функция `counters_sql`), запускается backup.sh сразу перед дампом и restore-check.sh на восстановленной БД. Состав — как в `deploy-verify.sh` (строки `events_listed=`, `users=`, `events_merged=`, `dedup_candidates=pending:|confirmed:|rejected:`, `source_alerts=`) плюс `migration=max(version_id)` из `goose_db_version` и `heartbeats=count`. Результат (одна строка) пишется в `.meta` рядом и в подпись. Живой `agg verify` после ночи отличается на прогоны/пользователей — runbook сравнивает с `.meta` и объясняет допустимые расхождения.

### Заглушки в тесте
`PATH="$T/bin:$PATH"`: `docker` (эмитирует байты «дампа» или `exit 1`), `age` (печатает `age-encryption.org/v1` + stdin), `curl` (отдаёт JSON с `file_size`), `timedatectl` (`NTPSynchronized=yes|no`), `date` не подменять — дату подавать параметром `TODAY` (функции чистые). Образец — `deploy/deploy-apply-test.sh` (`run()` с `PATH="$T/bin:$PATH"`).

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| GPG-связка на сервере | `age` + публичный ключ | на VPS нет связки; ключ шифрования ≠ ключ расшифровки |
| `age-inspect` для проверки файла | в Ubuntu 24.04 его нет (1.1.1) | проверка заголовка побайтово; полноценная — только расшифровка |
| `pg_restore -j` из потока | не работает с pipe [CITED: postgresql.org/docs/17/app-pgrestore.html] | на Mac: расшифровать во временный файл и восстановить без `-j` (или через `<` файл); для БД такого размера `-j` не нужен |

**Deprecated:** `docker compose run` для служебных задач вне образа приложения — не использовать для бэкапа.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `curl`, `gpg`, `age`, `flock`, `python3` на VPS: `python3` проверяется `agg-install.sh`; `curl`/`flock` вероятно есть, `age` нет (в базовом образе нет), `gpg` вероятно есть | Standard Stack, Env | План ветвится: нет `curl` ⇒ нужен `apt install curl` (разрешение); нет `age` ⇒ варианты владельцу |
| A2 | Ключ `form = "chat_id=…"` допустим в файле конфига curl | Pattern 3 | Придётся передавать `chat_id` через stdin-конфиг `-K -`; решается локальным тестом до VPS |
| A3 | Публичный ключ age имеет формат `age1` + 58 символов bech32 (62 всего) для валидации ввода | backup-apply | Слишком строгая/слабая проверка; проверить `age-keygen` на Mac в Wave 0 |
| A4 | `docker compose exec -T … pg_dump` пробрасывает код выхода команды | Pattern 1 | Ложный успех при сбое дампа; проверяется тестом на реальном `postgres:17` (docker-compose.test.yml) локально |
| A5 | Трактовка D-18: недели, чей представитель в ежедневном наборе, пропускаются, затем берутся 4 новейшие | Pattern 4 | Иное чтение даёт 3 вместо 4 недель; зафиксировать в тестах, подтвердить владельцем |
| A6 | Бот должен быть администратором канала с правом публикации; `chat_id` приватного канала — число вида `-100…` | §4 | Первый запуск вернёт `chat not found`/403; проверяется первым ручным запуском |
| A7 | `unhealthy` контейнер без оркестратора не перезапускается | Anti-Patterns | Иначе healthcheck worker мог бы вызвать неожиданные рестарты |
| A8 | Часовой пояс VPS не меняет UTC-дату в 03:00 (UTC…UTC+2) | backup.sh | Имя копии на день раньше; имя берётся из `date -u`, поэтому сдвиг влияет только на «сегодня» ротации — проверить `timedatectl` в аудите |
| A9 | `go-telegram/bot` вызывает `WithErrorsHandler` на ошибках `getUpdates` | Pattern 5 | Гейт по `lastTelegramErr` не сработает; LO-06 в 04-REVIEW описывает это поведение, проверить тестом с фиктивным API |
| A10 | `apt install age` работает: universe включён (пакет `docker-compose-v2` из universe уже ставился `host-apply.sh compose`) | Env | Нужен `add-apt-repository universe` (отдельное разрешение) |

## Open Questions

1. **MON-03: как «worker следит за bot», если у worker нет токена?**
   - Знаем: токен Telegram только у `bot` (`docker-compose.yml`, комментарий и `environment` сервиса `bot`).
   - Не ясно: считает ли владелец достаточным лог + docker healthcheck + остановку внешнего пинга.
   - Рекомендация: так и сделать (токен worker не расширяем); вынести вопрос владельцу.
2. **Порядок D-02 и предпосылки бэкапа.** Что именно разрешено до проверенного восстановления: установка `age`, каталог `/var/backups/aggregator`, `/etc/cron.d`, файлы секретов, исправление часов? Рекомендация: считать их «предпосылками бэкапа», каждая — отдельное разрешение (D-04); swap/ufw/SSH/tinyproxy/перезагрузка — после restore-теста.
3. **Что входит в «уборку Phase 1» (CONTEXT, этап 5)?** Requirements не определяют. Кандидаты: `/etc/sudoers.d/run-agg` (6 read-only команд), ключ `runagg`, `/home/runagg/agg-deploy/postgres17.tar`, `/tmp/tmp.*` (IN-06), `/var/tmp/agg-load.*`. Рекомендация: список после аудита, решает владелец.
4. **Размер 40–50 МБ: нужен ли активный алерт?** Минимум без новых сущностей — WARN в лог/подпись/`backup-status`.
5. **Судьба `tinyproxy` (PRD-07)** — решение владельца; фактическое использование неизвестно (лог недоступен `runagg`). Варианты: оставить как есть; `Listen 127.0.0.1`; закрыть 8888 в ufw; отключить; удалить.
6. **Причина `NTPSynchronized=no`** на момент Phase 4; текущее состояние неизвестно — аудит.
7. **IN-05:** digest `postgres:17@sha256:…` и `FROM …@sha256:…` фиксируются осознанно после restore-теста (CONTEXT: «IN-05, IN-06 — только после этапа 3»); restore-тест использовать тот же образ, что в production.
8. **Нужен ли `agg backup-run` (изменяющая подкоманда для runagg)** или первый запуск делает владелец из root-консоли? Рекомендация: добавить (слово `BACKUP` на stdin, как `DEPLOY`), чтобы не гонять владельца в консоль провайдера.
9. **Провайдер внешнего heartbeat и канал его оповещения** — решение владельца (критерии в §8).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Mac: `docker` | smoke, restore-test, локальные тесты PG | ✓ [VERIFIED: command -v] | 29.4.0 | — |
| Mac: `go` | Go-доработки | ✓ | 1.27.1 | — |
| Mac: `python3` | оракул ISO-недель, тесты | ✓ | 3.14.7 | — |
| Mac: `/bin/bash` | тесты скриптов | ✓ | 3.2.57 (не использовать bash 4-изм.: `mapfile`, assoc-массивы) | — |
| Mac: `age` | ключ, restore-test | ✗ | — | `brew install age` (шаг владельца) |
| Mac: `shellcheck`, `gpg` | линт | ✗ | — | `bash -n` + тесты |
| Mac: `flock` | — | ✗ | — | на Mac не нужен (тесты не вызывают flock; в `backup.sh` вызов через переменную `FLOCK`, в тесте заглушка) |
| VPS: `age`, `gpg`, `curl`, `flock`, `jq`, состояние cron | backup.sh | **неизвестно** (аудит не выполнялся) | — | ветвление плана; `cron` активен по 01-VPS-AUDIT.md |
| VPS: `timedatectl`, `docker compose` | часы, отметки | `timedatectl` ✓ (вывод есть в host-preflight.log); `docker compose` ✓ (deploy работает, `p6-verify-accepted.log` → `DEPLOY VERIFY OK`) | — | — |
| VPS: свободный диск | копии (≤11×~40 МБ) | ✓ 26 ГБ свободно / 29 ГБ (`host-preflight.log`, Phase 4) | — | порог ≥500 МБ перед дампом |

**Аудит VPS (read-only, одной просьбой владельцу):** блок через stdin под `runagg`, как в Phase 1 (`ssh <alias> 'bash -s' < блок`, без файлов на VPS): `command -v age gpg curl flock jq python3`, `apt-cache policy age`, `systemctl is-active cron`, `timedatectl show` + `timedatectl timesync-status`, `date +%Z`, `swapon --show`, `df -h /`, `sudo ufw status verbose` (разрешено sudoers Phase 1), `sshd -T` (там же), `ls -ld /var/backups /etc/cron.d`, `ls /etc/cron.d`. Альтернатива (изменение пакета, потребует `INSTALL`): расширить `host-preflight.sh`. Рекомендуется stdin-блок — пакет не меняется. Вывод — в `$HOME/run-agg-phase4/`, IP/хост не печатать.
**Missing with no fallback:** нет. **Missing with fallback:** `age` на Mac и, возможно, на VPS.

## Test and Execution Tiers (вместо полного Validation Architecture: `workflow.nyquist_validation: false`)

| Tier | Что | Где/как | Разрешение |
|---|---|---|---|
| T1 локально, без сети | `deploy/backup-test.sh`: ISO-неделя vs `python3`; таблица ротации (пропуски дней, граница года/ISO-недели, <7 копий, все `bad`, одна пригодная, будущая дата, чужие файлы, `.partial`); поток `backup.sh` с заглушками (успех, сбой `pg_dump`, нет часов, нет заголовка, мал, Telegram отказал → `backup` есть/`backup_copy` нет, размер 40–50/>50 МБ); `ext-heartbeat.sh` (пинг/нет пинга); sudoers/регулярка имён пакета | `bash deploy/backup-test.sh` (<30 с; в Mac /bin/bash 3.2 и brew bash) | нет |
| T1 Go | `internal/alert/infra_test.go` (чистая `decide`, фейковые часы), `internal/store/heartbeats_test.go` (docker-compose.test.yml, `TEST_DATABASE_URL`), healthcheck | `go test ./internal/...` | нет |
| T1 с реальным PG | `pg_dump -Fc` тестовой БД → `age` (контейнер с `age` или Mac после `brew install age`) → `pg_restore`; замер размеров пустой/засеянной БД для D-21 | локально, docker-compose.test.yml | `brew install age` — владелец |
| T1 с фиктивным Telegram | `curl --config` против локального `python3 -m http.server`-заглушки (проверка A2 и разбора JSON) | локально | нет |
| T2 изменения хоста | установка пакета (`INSTALL`), `backup-apply.sh` (каталоги/cron/секреты), `host-apply.sh age|swap|…`, ufw, перезагрузка | root-консоль, по одному разрешению (D-04/D-24) | да, каждый шаг |
| T2 read-only на VPS | аудит A0, `agg host-preflight`, `agg backup-status`, `agg verify` | под `runagg`, логи вне репозитория | один запрос на аудит |
| T3 production deploy | образ с heartbeat/InfraLoop/healthcheck: `ship.sh` → (`INSTALL` если менялся compose/скрипты) → `agg deploy <TAG>` → `agg verify` | как Phase 4–5 | да |
| T4 владелец, ручной | первый `agg backup-run`; restore-тест на Mac; проверка внешнего heartbeat (отключение cron-строки на > grace); контрольная перезагрузка | runbook | да |

## Security Domain

Применимо (`security_enforcement: true`, ASVS level 1).

| ASVS Category | Applies | Standard Control |
|---------------|---------|------------------|
| V2 Authentication | частично | токены ботов — только root-файлы 600; SSH: `PermitRootLogin`/`PasswordAuthentication` (аудит: оба `yes`) — drop-in `sshd_config.d`, `sshd -t` до reload, по разрешению |
| V4 Access Control | да | `runagg` через sudo только `agg` с фиксированными подкомандами; новые подкоманды — без аргументов (`backup-run`, `backup-status`) |
| V5 Input Validation | да | ввод секретов в `backup-apply.sh` — regex (формат токена `^[0-9]+:[A-Za-z0-9_-]{30,}$` как в `env-init.sh`, `chat_id` `^-?[0-9]+$`, URL heartbeat `^https://[A-Za-z0-9./_?=&%-]+$`); имена файлов ротации — строгая маска |
| V6 Cryptography | да | `age` (не самописное); приватный ключ вне VPS (D-10) |
| V8 Data Protection / V14 Config | да | umask 077, права 600/700, логи без токенов, `curl -sS` без `-v` |

| Pattern | STRIDE | Mitigation |
|---------|--------|------------|
| Утечка токена в `ps`/логи/историю | Information Disclosure | `curl --config` из root-файла; скрытый `read -rs`; в вывод — только длины/«OK» |
| Подмена скриптов в `runagg`-каталоге перед запуском root | Elevation | существующий hash-checked однострочник (копия в root-tmp, `SHA256SUMS`); `/usr/local/lib/aggregator` root:root 644 |
| Взлом VPS: удаление/засорение канала токеном бэкапа | Tampering/DoS | принято D-17/раздел «Ограничения»; ущерб ограничен отдельным ботом; чтение без приватного ключа невозможно |
| Поддельный дамп (подмена файла на диске) | Tampering | SHA-256 sidecar + подпись в канале; проверка расшифровкой при restore-тесте |
| Удаление не тех файлов при ротации | Tampering | маска имён, только каталог бэкапов, защиты №1–6 |
| Пинг-URL как секрет | Information Disclosure | `hc.curl` 600; не логировать URL |

## Sources

### Primary (HIGH confidence)
- Репозиторий (прочитан): `06-CONTEXT.md`, `REQUIREMENTS.md`, `ROADMAP.md`, `MVP.md` §10–11, `01-VPS-AUDIT.md`, `04-REVIEW.md`, `deploy/README.md`, `deploy/{ship,oneliner,secrets}.sh`, `deploy/vps/*`, `docker-compose.yml`, `Dockerfile`, `cmd/aggregator/main.go`, `internal/alert/alert.go`, `internal/store/{alerts,store}.go`, `internal/bot/{tg,bot,admin}.go`, `migrations/00001`, `00003`.
- Telegram Bot API: https://core.telegram.org/bots/api#senddocument (50 МБ отправка, 20 МБ `getFile`, caption 0-1024, ответ `Message`, поле `ok`).
- PostgreSQL 17 `pg_restore`: https://www.postgresql.org/docs/17/app-pgrestore.html (stdin, `-j` не с pipe, `--exit-on-error`, `--single-transaction`, `--no-owner`).
- age: https://github.com/FiloSottile/age (рекомендатель `-r`, шифрование без приватного ключа); формат: https://github.com/C2SP/C2SP/blob/main/age.md (`age-encryption.org/v1`, финальный чанк).
- Ubuntu: https://packages.ubuntu.com/noble/age (1.1.1-1ubuntu0.24.04.3, universe), https://packages.ubuntu.com/noble/amd64/age/filelist (`/usr/bin/age`, `/usr/bin/age-keygen`).

### Secondary (MEDIUM)
- Debian cron(8) через manpages.debian.org / веб-поиск: имена в `/etc/cron.d` — буквы, цифры, `_`, `-`; владелец root.
- Поиск по релизам age: `age-inspect` появился позже v1.2.0 (в документации v1.3.x); на noble его нет.

### Tertiary (LOW)
- Поведение `curl` `form` в конфиг-файле, ufw+Docker egress, поведение go-telegram `WithErrorsHandler` — [ASSUMED], см. A2, A9.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH для `age`/Bot API/pg_restore; MEDIUM для набора утилит на VPS (A1).
- Architecture: HIGH для интеграции с `agg`/пакетом (прочитан код); MEDIUM для infra-алертов (дизайн, не код).
- Pitfalls: MEDIUM-HIGH.
- Внешний heartbeat: LOW-MEDIUM — провайдер не выбран.

**Research date:** 2026-10-01
**Valid until:** 2026-10-31 (хост и пакет меняются; перед планом сверить результат аудита VPS)

---

## Приложение A. Ответы по ключевым вопросам (кратко, для планировщика)

**Q2 Часы.** `timedatectl show -p NTPSynchronized --value` → `yes`/`no`. Не синхронизировано: до 30 попыток раз в 60 с, затем выход ≠0, без дампа/отметок/ротации; повторная проверка непосредственно перед ротацией; рассинхрон «в будущее» ловит защита №4. Постоянный `no` = сбой бэкапа → алерт по 26 ч.

**Q3 Heartbeat/пороги.** Пишут: `bot`, `worker` (Go, 60 с), `backup`, `backup_copy` (хост, `psql`). Устаревание: сервисы >10 мин, бэкапы >26 ч, расчёт в SQL. Состояние алертов — память; один алерт на переход, напоминание 24 ч, сообщение о восстановлении. Миграция не нужна. `ext-heartbeat.sh` использует тот же запрос (свежесть `bot`,`worker` <10 мин).

**Q4 Telegram-бэкап и секреты.** Поля `chat_id`, `document` (multipart), `caption`. Подтверждение: `ok==true` и `result.document.file_size==local`. Лимит 50 МБ; предупреждение 40 МБ. Ввод секрета: `backup-apply.sh telegram` в **root-консоли провайдера** (подтверждающее слово, `read -rs`, regex, запись через `mktemp`+`mv` в `/etc/aggregator-backup/telegram.curl`, 600) — исполнитель не участвует (D-22). Запасной путь по образцу `secrets.sh`: скрытый ввод на Mac + stdin по ssh + `agg` подкоманда «только если файла нет» — расширяет поверхность `agg`, использовать лишь если консоль не принимает вставку. Определение `chat_id` приватного канала (отправить сообщение в канал, прочитать `channel_post` через `getUpdates` в терминале владельца) — маленький Mac-скрипт, скрытый ввод токена, печать только id/названия.

**Q5 Restore на Mac.** Скачать `.dump.age` из Telegram-клиента (лимит 20 МБ относится только к `getFile` бота); сверить `shasum -a 256` с подписью; `age -d -i <ключ>` во временный файл; `docker run -d --name restore-test -e POSTGRES_USER=aggregator -e POSTGRES_DB=aggregator -e POSTGRES_PASSWORD=<случайный> postgres:17` (порт не публиковать); `docker exec -i restore-test pg_restore -U aggregator -d aggregator --no-owner --exit-on-error < файл`; те же счётчики `counters_sql`, сравнить с `.meta` из подписи; `migration=` совпадает с `agg verify`/версией образа; удалить контейнер и временный файл. Раз в месяц; запись даты в runbook-журнале. Аварийное восстановление в production (остановить bot/worker, пересоздать БД, `pg_restore`, `migrate`, запуск) — описать, но проверяется только в throwaway.

**Q6 MON-04.** См. Pattern 5: DB-down — по ≥3 подряд неудачным тикам, подавление прочих алертов, отправка без БД; backoff 1→15 мин в памяти; Telegram недоступен — лог (одна запись на смену состояния).

**Q7 Healthcheck.** `healthcheck` без аргумента — как сейчас (DB). `healthcheck bot|worker` — DB + свежесть `heartbeats.<component>` ≤5 мин (с `start_period`). Бот пишет heartbeat только при успешном `getMe` и отсутствии ошибок `getUpdates` за 2 мин (закрывает LO-06: отозванный токен/409/нет сети ⇒ unhealthy и остановка внешнего пинга). Worker: добавить `healthcheck` в compose (сейчас у `worker` его нет — `docker-compose.yml:110` область `stop_grace_period`), `deploy-verify.sh` требовать `healthy` у worker. IN-07: `sync.WaitGroup` в `recoverMW` + `Wait` с таймаутом меньше `stop_grace_period` (у `bot` его нет — задать явно, напр. 20s, таймаут ожидания 15s). Изменение compose ⇒ повторный `INSTALL`.

**Q8 Внешний heartbeat — критерии (провайдер не выбирать):** (1) настраиваемый grace ≥15 мин и период 5 мин; (2) уведомления не через Telegram бота проекта (≥2 независимых канала, напр. e-mail + мессенджер/SMS); (3) пинг по HTTPS GET/POST на секретный URL без учётной записи в запросе; (4) бесплатный/дешёвый тариф с ≥1 монитором; (5) поддержка сигнала «fail»/пауза/тест-уведомление; (6) расположение/политика данных (ЕС; не передаются ПД; в пинг — только факт); (7) собственная надёжность и история инцидентов; (8) доступность из Варшавы и из страны владельца (получение уведомлений); (9) возможность самохостинга/экспорта (смена провайдера = замена одного файла `hc.curl`); (10) срок жизни/ротация URL. Проверка «весь VPS недоступен» без остановки production: вариант A — владелец в root-консоли комментирует строку cron `ext-heartbeat` на время > grace (+ сервисы работают), ждёт алерт у провайдера и восстановление после возврата строки (для провайдера это неотличимо от молчания хоста); вариант B — плановая перезагрузка PRD-02 (простой 1–2 мин < grace) доказывает автозапуск cron/Docker/пингов. Реальную остановку ВМ не делать.

**Q9 Хост — что уже есть.** `host-preflight.sh` (ядро/reboot-required/обновления/пакеты Docker/службы/диск/RAM/swap/`timedatectl show -p NTP -p NTPSynchronized`/проект), `host-apply.sh` (`updates`, `compose`; словa `UPDATES`, `COMPOSE`), `host-verify.sh` (reboot-required, ядро, compose ≥2.20, docker, tinyproxy, NTP). Нет: `swap` (fallocate 1–2 ГБ, `mkswap`, `fstab`), `ufw`, исправление часов, SSH-ужесточение, `tinyproxy`, `age`, cron. Добавить режимы в `host-apply.sh` (`age`, `swap`, `ufw`, при необходимости `clock`) со своими словами и тестом `confirm-test.sh`-типа (расширить пары `install:INSTALL host:…`); `host-verify.sh` дополнить swap/ufw/age/cron-проверками. IN-06: в `oneliner.sh` копировать только `SHA256SUMS` и перечисленные в нём файлы после сверки хеша, в конце `rm -rf "$T"`. IN-05: digest через `docker buildx imagetools inspect` на Mac (чтение), правка `docker-compose.yml:46` и `Dockerfile:7,26`, обновление — осознанное.

**Q10 Подкоманды `agg`.** Только чтение: `backup-status` (возраст отметок, список локальных копий с размером/«пригодна», хвост лога без секретов, наличие cron/файлов секретов и их права без содержимого, `clock_ok`). Изменяющие: `backup-run` (слово `BACKUP` на stdin; запускает `backup.sh` как cron). Всё остальное (`backup-apply.sh`, `host-apply.sh`) — только root-консоль. Правки: `agg` (`usage`, `case`), `agg-install.sh` (sudoers-строка, исключения `LIB`, список регулярки), `oneliner.sh` (`BUNDLE`, `installed`, `root`-блок для `backup-apply.sh`), `deploy-verify.sh` (CHECK-и прав новых файлов), `deploy-apply-test.sh`-подобный тест на `agg`.

**Q11 Runbook (`deploy/RUNBOOK.md`):** 0 принципы (D-04, секреты, никогда `down -v`, логи вне репозитория); 1 развёртывание/обновление/откат (ссылка на README); 2 бэкап (расписание, файлы, `agg backup-status`, ручной запуск, ключи age: создание/хранение/ротация, где публичный ключ); 3 восстановление: ежемесячный тест (Q5) и аварийное; 4 реакция на алерты — таблица «текст алерта → смысл → первая команда → когда эскалировать» для `worker_stale`, `backup_stale`, `backup_copy_stale`, `postgres_down`, а также `failed`/`suspect` (Phase 4–5) и молчание внешнего heartbeat; 5 обновление парсера при смене вёрстки (`worker sanity-report`, `run-once`, `accept-baseline`, golden-фикстуры в `internal/ingest/sources/*/testdata`, `ship.sh`); 6 внешний heartbeat (смена провайдера, проверка); 7 хост (обновления, перезагрузка, ufw, swap, часы); 8 календарь (ежемесячно restore-тест, квартально проверка места на диске и размера дампа). Тест на дрейф: каждый ключ алерта из кода присутствует в RUNBOOK.md (`grep` в `backup-test.sh`).

**Предлагаемая последовательность планов (для волн):** W0 тест-каркас + оракулы + замер размеров дампа (D-21) и проверки A2/A4 локально → W1 `backup-lib`/`backup.sh` + тесты → W2 пакет `agg` (подкоманды, sudoers, имена, IN-06) + `backup-apply.sh` → W3 owner-checkpoints: аудит A0, ключ age (Mac), бот и канал, `INSTALL`, предпосылки, ввод секретов, первый `backup-run` → W4 restore-тест (PRD-04) → W5 Go: heartbeat, InfraLoop, store, healthcheck, IN-07, compose → W6 owner: deploy образа + проверки → W7 внешний heartbeat (выбор провайдера, `ext-heartbeat.sh`, проверка молчания) → W8 хост (PRD-02/07, IN-05, перезагрузка) → W9 runbook + дрейф-тест.
