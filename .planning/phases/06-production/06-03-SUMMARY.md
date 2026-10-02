---
phase: 06-production
plan: 03
subsystem: deploy
tags: [backup, telegram, curl, sudoers, bash]
status: awaiting-owner-decision
requirements-completed: []
requires: [06-02]
provides:
  - deploy/vps/backup.sh (шаги 6-7: send_copy, reply_check, коды 0/1/2/3)
  - deploy/vps/backup-status.sh (agg backup-status, только чтение)
  - deploy/vps/backup-apply.sh (root-консоль: dirs, telegram, cron)
  - deploy/vps/backup-recipient (публичный ключ age)
  - deploy/vps/agg, agg-install.sh (подкоманда backup-status, запись sudoers, исключение в LIB=)
  - deploy/vps/host-apply.sh (case по режимам, режим age)
  - deploy/oneliner.sh (BUNDLE из 17 файлов, раздел команд бэкапа)
  - deploy/backup-test.sh (заглушка Bot API, случаи copy-*, status-*, apply-*, pkg-*, host-words, oneliner-lines)
key-files:
  created:
    - deploy/vps/backup-status.sh
    - deploy/vps/backup-apply.sh
    - deploy/vps/backup-recipient
  modified:
    - deploy/vps/backup.sh
    - deploy/vps/agg
    - deploy/vps/agg-install.sh
    - deploy/vps/host-apply.sh
    - deploy/oneliner.sh
    - deploy/backup-test.sh
plan_head_before: a406da5
commits: 0
actuals:
  tasks: 4
  commits: 0
---

# Phase 6 Plan 03: внешняя копия бэкапа и пакет для неё

**Статус: локальная реализация подготовлена и проверена локальными тестами; шаг ожидает решения владельца** — 2026-10-02. Формально шаг не закрыт. Всё выполнено на Mac; VPS не затрагивался, в Telegram ничего не отправлялось, настоящие токен и идентификатор канала не использовались. Исполнитель коммитов не делал (`commits: 0`); изменения закоммичены владельцем: `016c833`.

**PRD-03 не закрыт.** Внешняя копия в production появится только в 06-04, восстановление проверяется в 06-05. Telegram — временное хранилище (D-17), независимой второй копии нет; локальная копия внешней не считается (D-03).

## Что сделано

- `deploy/vps/backup.sh`, шаги 6–7: `send_copy` после ротации — пороги размера, проверка файла секретов, `curl --config`, разбор ответа (`reply_check`, `python3 -I`), `mark backup_copy`, `COPY OK size=<байт>`. Итоговый код: 0 — обе отметки и ротация выполнена; 2 — отправка не удалась; 3 — обе отметки, ротация остановлена; 1 — как в 06-02.
- `deploy/vps/backup-status.sh`: только чтение; семь строк состояния, `SIZE WARN`, строки `WARN <ключ>`, `STATUS OK` (код 0) или `STATUS WARN` (код 1).
- `deploy/vps/backup-apply.sh`: режимы `dirs` (слово `DIRS`), `telegram` (`TELEGRAM`, два скрытых ввода, проверка `getChat`), `cron` (`CRON`).
- `deploy/vps/backup-recipient`: одна строка, получена командой из строки `AGE_RECIPIENT=` файла `06-01-SUMMARY.md`.
- Пакет: `agg backup-status`, запись sudoers без аргументов, исключение `backup-apply\.sh` в `LIB=`, `BUNDLE` из 17 файлов, `installed` — 13 строк, раздел однострочников бэкапа, третий аргумент `ro()` (каталог логов).
- `deploy/vps/host-apply.sh`: ветки переведены в `case "$MODE"`, добавлен режим `age` (слово `AGE`); `confirm()` не менялась.

## Решение 06-01 → что написано

| Строка в `06-01-SUMMARY.md` | Что написано |
|---|---|
| `DECISION backup_run=root-console` | подкоманды `backup-run` нет ни в `agg`, ни в sudoers, ни в однострочниках; ручной запуск — строка `/bin/bash /usr/local/lib/aggregator/backup.sh` для root-консоли |
| `DECISION age=install` | режим `host-apply.sh age` написан (показ `apt-cache policy age` до слова, затем `apt-get update`, установка, `age --version`); на VPS не запускался |
| `DECISION curl=present` | режим `curl` не написан |
| `DECISION clock=diagnose` | режим `clock` не написан: ждёт перепланирования 06-04 (`REPLAN 06-04: диагностика часов`) |
| `DECISION size_warn=log` | вызова `mark backup_size_warn` в коде нет; `SIZE WARN` — в выводе, подписи и `backup-status` |

## Результат A2

`curl` 8.7.1 (Mac): поле `form = "chat_id=…"` в файле конфига вместе с `-F document=@…` и `--form-string caption=…` в командной строке доходит до сервера — заглушка получила `chat_id`, документ и подпись в одном запросе. Запасной вариант (идентификатор в строке запроса) не понадобился. На `curl` из Ubuntu 24.04 не проверено — подтверждается первой отправкой в 06-04.

## Тесты

Заглушка Bot API — `python3` на `127.0.0.1`, случайный порт; токен и идентификатор канала выдуманы и собираются при запуске из частей, адрес настоящего API в тестах не используется.

| Группа | Случаи |
|---|---|
| copy | 13: `copy-tracer`, `copy-no-config`, `copy-http` (HTTP 500, HTTP 500 с телом успешного ответа, 403 с `ok=false`, не JSON, закрытый порт), `copy-mismatch`, `copy-mark-fail`, `copy-too-large`, `copy-max-edge`, `copy-size-warn`, `copy-resend`, `copy-after-dump-fail`, `copy-after-rotate-abort`, `copy-tmpfile`, `copy-secret-silence` |
| status | 8: `status-ok`, `status-copy-missing`, `status-stale`, `status-db-down`, `status-unusable`, `status-size-warn`, `status-warns` (сверх плана), `status-readonly` |
| apply | 9: `recipient-format`, `apply-words`, `apply-dirs`, `apply-telegram`, `apply-to-backup`, `apply-telegram-bad`, `apply-telegram-project-token`, `apply-cron`, `apply-static` (сверх плана) |
| пакет | 7: `pkg-names`, `pkg-lib`, `pkg-sudoers`, `pkg-agg`, `pkg-optional`, `host-words`, `oneliner-lines` |

Случая `pkg-run` нет: подкоманда `backup-run` не выбрана. Ожидания 06-02 изменены только там, где это задаёт план: раздел `tracer` получает файл секретов заглушки и ждёт код 0, `COPY OK` и обе отметки; остальные случаи идут без файла секретов и ждут прежний код 2 и строку `COPY FAIL reason=no-config`.

## Проверки

- `/bin/bash deploy/backup-test.sh` -> `BACKUP TESTS OK` под bash 3.2 и с `backup.sh` под bash 5.3 (без Docker). Полный набор повторно запущен после добавления последнего режима заглушки (HTTP 500 с телом успешного ответа) и после исправлений по аудиту и прошёл под обеими версиями.
- `deploy/confirm-test.sh` -> `CONFIRM OK`; `deploy/deploy-apply-test.sh` -> `DEPLOY APPLY OK`; `deploy/load-postgres-test.sh` -> `LOAD POSTGRES TEST OK`.
- `bash -n` всех скриптов `deploy/`; `git diff --check`; `deploy/oneliner.sh sums` — 17 строк, `installed` — 13; `go.mod`, `go.sum`, `docker-compose.yml`, `cmd`, `internal`, `migrations` не изменены; строк с префиксом секретного ключа `age` в `deploy`, `cmd`, `internal`, `migrations` нет.
- Мутации на копии (каждая поймана): нет проверки `file_size`; нет проверки кода HTTP; нет порога 50 МБ; сбой `mark backup_copy` игнорируется; нет кода 3; подробный вывод `curl`; нет сравнения с токеном бота проекта; ответ `getChat` не проверяется; порог возраста 93601; права файла секретов не проверяются.
- Режим `pg` (Docker) и команды проверки плана целиком (`COPY TRACER OK`, `COPY FAILURES OK`, `BACKUP SCRIPTS OK`, `BACKUP PACKAGE OK`) отдельно не запускались: каждая повторяет полный набор; их условия проверены по частям перечисленными выше командами.

## Deviations from Plan

**1. Строка `COPY HTTP status=<код HTTP|000> curl=<код выхода|skipped> error_code=<n|none|tmpfile>`.** План требует при причине `http` печатать только код HTTP и числовой `error_code`, формат не задаёт. Выбрана отдельная строка перед `COPY FAIL reason=http`; сама строка `COPY FAIL reason=http` — ровно по контракту. Если файл для ответа создать не удалось, `curl` не запускается: `status=000 curl=skipped error_code=tmpfile`. Решение владельца 2026-10-02: строку оставить; она внесена в набросок и планы 06-04, 06-09.

**2. Длительность набора.** План ограничивает полный `deploy/backup-test.sh` 90 секундами. Фактически набор шёл дольше ещё до этого плана; сейчас один прогон — около 15 минут при двух параллельных запусках. Случаи с файлами 40–50 МБ добавляют около двух минут. Требование плана не выполнено.

**3. Разбор ответа вынесен в функцию `reply_check`.** Текст `python3` лежит в `backup.sh` (отдельного файла в пакете нет): подстановка команды с вложенным here-document в bash 3.2 ненадёжна.

**4. Итоговая строка после `confirm`.** Приглашение `confirm()` печатается без перевода строки; когда слово приходит не с терминала, `APPLY OK mode=…` и `HOST APPLY … DONE` оказываются в той же строке после приглашения. В root-консоли перевод строки даёт Enter владельца. Поведение `host-apply.sh` было таким и раньше; тесты сверяют окончание строки.

**5. Сравнение с токеном бота проекта** выполняется, только если файл окружения приложения читается; если файла нет, проверка пропускается без сообщения.

**6. Дополнительные случаи** `status-warns` и `apply-static`; в `copy-http` — пять вариантов вместо четырёх.

## Исправления после аудита (2026-10-02)

1. `backup-apply.sh telegram`: файл с токеном и идентификатором канала создаётся только после успешной проверки `getChat`; до неё секреты на диск не пишутся (во временном файле ответа их нет).
2. Сбой создания файла для ответа в `send_copy`: строка `COPY HTTP status=000 curl=skipped error_code=tmpfile` вместо `curl=none`; случай `copy-tmpfile`.
3. Однострочник `backup-status` сам создаёт каталог `$HOME/run-agg-phase6` (700); проверен на пустом `HOME` с заглушкой `ssh`. Прежние однострочники с логом в `run-agg-phase4` не менялись.
4. В тесте имя узла настоящего API больше не повторяется: оно собрано из частей и используется только для подмены константы `API=` на адрес заглушки и для проверки, что константа в `backup-apply.sh` одна. Запросов к настоящему API тесты не делают: все адреса в них — `127.0.0.1` с портом заглушки, копия `backup-apply.sh` проверяется на отсутствие `https://`.

## Известные ограничения

- Отправка ограничена `curl --max-time 300`: копия должна уйти за 5 минут (для 50 МБ — канал не медленнее примерно 1,4 Мбит/с), иначе `COPY FAIL reason=http`; локальная копия и `backup` остаются. Описание — в runbook (06-09).
- `backup-status`: при отметке с временем из будущего (сбитые часы) оба возраста печатаются как `unknown`, хотя БД доступна; итог — `STATUS WARN`. Поведение оставлено (решение владельца 2026-10-02).

- В Telegram ничего не отправлялось: формат ответа Bot API воспроизведён заглушкой. Право бота публиковать в канал `getChat` не подтверждает — это покажет первая отправка в 06-04 (A6).
- `curl`, `install`, `mktemp`, `read -rs` в консоли провайдера, `ls -l` для проверки прав — проверены только на Mac.
- `backup-status` не показывает копии `unverified` (решение владельца); они видны только в логе бэкапа.
- Повторный запуск в тот же день отправляет в канал ещё один документ с тем же именем; копии в канале не ротируются (D-17).
- Ответ API временно лежит в каталоге копий (`.copy-response.*`, удаляется сразу после разбора и в `trap`); при жёстком обрыве файл может остаться и будет удалён следующей отправкой.
- `deploy/README.md` не менялся (не входит в файлы плана); описание команд бэкапа — задача 06-09.

## Known Stubs

Нет.

## Threat Flags

Нет новых поверхностей сверх `threat_model` плана.

## Self-Check

- Файлы на месте: `deploy/vps/backup-status.sh`, `deploy/vps/backup-apply.sh`, `deploy/vps/backup-recipient`; изменены шесть файлов из списка плана.
- Локальный набор без Docker зелёный под bash 3.2 и 5.3 (с оговоркой из раздела «Проверки»).
- На VPS ничего не устанавливалось и не запускалось; коммитов исполнителя нет, изменения закоммичены владельцем (`016c833`).
