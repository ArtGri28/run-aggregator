---
phase: 07-deduplication-transparency
plan: 05
subsystem: deploy
tags: [bash, agg-verify, dedup, would_auto, runbook]
requires: [07-04]
provides:
  - "agg verify: строки dedup_would_auto=pending:<n> и dedup_pass_last=would_auto:…|hidden:…|shown:…|decided:… (или unknown)"
  - "deploy/RUNBOOK.md: раздел «Отмена слияния событий»"
  - "DDP-11…15 — Complete (локально)"
affects: [06-04, 06-05]
plan_head_before: 1aabc09
actuals:
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "SQL строки agg verify выполняется Go-тестом на тестовой базе ровно в том виде, что в скрипте (как inventoryQuery в 07-04)"
key-files:
  created:
    - internal/store/verify_test.go
    - .planning/phases/07-deduplication-transparency/07-05-SUMMARY.md
  modified:
    - deploy/vps/deploy-verify.sh
    - deploy/deploy-apply-test.sh
    - deploy/RUNBOOK.md
    - .planning/REQUIREMENTS.md
    - .planning/ROADMAP.md
    - .planning/STATE.md
key-decisions:
  - "Предусловие D-03 — условие 2: разрешение владельца изменить пакет до CLOCK OK (2026-10-03)"
  - "dedup_pass_last — из логов worker, как dedup_auto_merge_*; нет строки или любого из четырёх счётчиков — unknown"
requirements-completed: [DDP-11, DDP-12, DDP-13, DDP-14, DDP-15]
completed: 2026-10-03
duration: около 1 часа
status: complete
---

# Phase 7 Plan 05: agg verify — потенциальные автослияния

**Статус: complete (локально).** `agg verify` показывает потенциальные автослияния; пакет `agg` изменён по разрешению владельца и на VPS не отправлялся. VPS, production-база, миграции на production, deploy, `ship.sh`, `DEDUP_AUTO_MERGE` не затрагивались. Коммит делает владелец. Phase 7 не закрыта.

## Предусловие (задача 1)

`CLOCK OK` в `06-04-SUMMARY.md` нет — задача 6 Phase 6 не выполнена. Использовано условие 2 плана: разрешение владельца.

Ответ владельца дословно (2026-10-03): «Разрешаю изменить пакет».

Сигнал продолжения плана — «разрешаю изменить пакет» — получен. Последствие, показанное владельцу до ответа: пакет на VPS устареет, повторная отправка `ship.sh` — отдельным разрешением.

SHA256SUMS до изменения: `45b91be46d448941e0bca84fbf1c1af0aa497ee377527d59ff351c8366342bc0`.

## Реализация (задача 2)

- `deploy/vps/deploy-verify.sh`, раздел «Дедупликация», после `dedup_auto_merge_*`:
  - `dedup_would_auto=pending:<n>` — `count(*)` pending-кандидатов с `reasons->>'would_auto' = 'true'`;
  - `dedup_pass_last=would_auto:<n>|hidden:<n>|shown:<n>|decided:<n>` — из последней строки `"msg":"dedup pass"` логов worker (поля `would_auto`, `would_auto_hidden`, `would_auto_shown`, `would_auto_decided` из 07-01); нет строки или любого поля (старый образ) — `unknown`.
  - Ошибка запроса учитывается в `CHECK dedup-summary`, как у прежних строк. Названий, адресов и секретов в новых строках нет.
- `deploy/deploy-apply-test.sh`: `deploy-verify.sh` запускается как есть на заглушке docker. Проверяются: новая строка по последней (не первой) строке прохода; `unknown` без строки и без счётчиков; прежние строки `dedup_candidates`, `events_merged`, `dedup_auto_merge_*`, `CHECK dedup-summary: OK` без изменений.
- `internal/store/verify_test.go` `TestVerifyWouldAuto`: читает SQL `dedup_would_auto=` из `deploy-verify.sh` и выполняет его на тестовой базе — pending с `true` (2), `false`, без ключа, rejected с `true` → `dedup_would_auto=pending:2`.
- `deploy/RUNBOOK.md`: раздел «Отмена слияния событий» по `07-UNMERGE.md` — только после RESTORE GATE, выката Phase 7 с миграцией `00005` и решения владельца на каждую отмену; ручное слияние не означает класс A; в `agg` подкоманда не выведена. Файл не входит в пакет.

## Статусы и пакет (задача 3)

- REQUIREMENTS: DDP-11…15 — Complete (локально; основания — SUMMARY 07-01…07-05). На production не выкачено.
- ROADMAP: 07-05 отмечен, Phase 7 — 5/5, In Progress (не закрыта; phase.complete — только по команде владельца).
- **Новый SHA256SUMS пакета (18 файлов): `4c2f486fb461f0af1b6e06aa3370f195991bf460e2b2f2c53eb8ca455479f183`**; `deploy-verify.sh` — `0bc46df2…dc4c5`. Состав пакета не изменился (18 файлов), `docker-compose.yml` не изменён.
- **Пакет на VPS (`2db41ff`) устарел.** Однострочники root-консоли, созданные из текущего репозитория (в том числе CLOCK, задача 6 плана 06-04), дадут `STOP: HASH MISMATCH`, пока пакет не отправлен заново `ship.sh` — только отдельным разрешением владельца. Записано в STATE (блокеры).

## Проверки

| Проверка | Результат |
|---|---|
| задача 1 | `PRECONDITION OK` (разрешение владельца; `CLOCK OK` нет) |
| задача 2 | `VERIFY LINES OK`: `DEPLOY APPLY OK`, 18 файлов пакета, `docker-compose.yml` без изменений |
| полный `deploy/backup-test.sh` | `BACKUP TESTS OK`, 93 ok, код 0 |
| задача 3 | `PLAN 07-05 OK`; SUMMARY по схеме — valid; `validate consistency` — passed, 0 предупреждений |
| `TestVerifyWouldAuto` | PASS на PostgreSQL 17 (тестовая база) |
| `gofmt -l`, `go vet ./...` | чисто |
| полный подробный прогон Go | 470 PASS, 0 SKIP, 0 FAIL |
| мутации (копия вне репозитория) | без `state = 'pending'`; без условия `would_auto`; ключ без закрывающей кавычки (`would_auto` совпадает с `would_auto_*`); строка `started` вместо `dedup pass`; первая строка вместо последней; без сброса при пропуске поля — каждая ловится тестом (SQL — Go и shell, разбор лога — shell) |

## Deviations from Plan

1. Добавлен `internal/store/verify_test.go` (нет в `files_modified`): заглушка psql не выполняет SQL, поэтому смысл запроса проверяется на тестовой базе тем же текстом, что в скрипте; заглушка проверяет только форму запроса и вывод.
2. `.planning/STATE.md` обновлён задачей 3 (запись о хеше), в `files_modified` не указан.
3. Отдельного теста `deploy-verify.sh` до плана не было — раздел добавлен в `deploy-apply-test.sh`, как указано в плане.
4. `dedup pass` пишет и bot (проход после принятия кнопкой); `dedup_pass_last` берётся только из worker, как в плане.
5. Коммиты делает владелец; исполнитель их не выполнял.
