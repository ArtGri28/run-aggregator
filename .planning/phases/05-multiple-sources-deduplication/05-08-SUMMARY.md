---
phase: 05-multiple-sources-deduplication
plan: 08
subsystem: deploy
tags: [deploy, dedup, compose, smoke, verify]
status: complete
progress: "задачи 1–2 закоммичены (faa8ee5); задачи 3–4 выполнены владельцем, подтверждены сохранёнными логами с оговорками; задача 5 — keep-off (D-23); задача 6 не выполнялась"
requirements-completed: [ING-12, DDP-08, DDP-09]
requires: [05-06, 05-07]
key-files:
  modified:
    - docker-compose.yml
    - deploy/smoke.sh
    - deploy/vps/deploy-verify.sh
    - deploy/README.md
    - .env.example
plan_head_before: bdb7025
commits: 0
---

# Phase 5 Plan 08: выкат Phase 5

**Статус: complete** — выкат выполнен владельцем 2026-10-01, автослияние не включалось (D-23). Итог задач 3–6 — в конце файла.

## Задача 1 (tracer) — `TRACER OK`

- `docker-compose.yml`: якорь `x-app-env` (`<<: *db-env` + восемь `DEDUP_*: ${DEDUP_*:-}`), `bot` и `worker` берут `<<: *app-env`, `migrate` — по-прежнему db-env; шапка дополнена.
- `deploy/smoke.sh`: восемь `DEDUP_*` в `env -u` массива `DC`; последний шаг «DEDUP_* в bot и worker»: `DEDUP_HIGH=x9q` в env-файле → worker и bot завершаются с кодом 1, вывод называет `DEDUP_HIGH` и не содержит значения.
- Smoke на собранном образе: все шаги ok, `ok: DEDUP_* доходят до bot и worker (ошибка называет DEDUP_HIGH без значения)`, `SMOKE OK`.

## Задача 2 — `VERIFY TOOLING OK`

- `deploy/vps/deploy-verify.sh`: раздел `sec "Дедупликация"` — `source_run=`, `fields_null=`, `dedup_candidates=`, `events_merged=`, до 10 `merged=`, `dedup_auto_merge_bot=`/`_worker=` из последней строки `started` логов, `res dedup-summary`. Все 10 запросов файла выполнены на тестовой БД со схемой 00004; формат строк проверен в откатываемой транзакции (`source_run=probeg|1|suspect|5|accepted=f`, `fields_null=marathonec|0`).
- `deploy/README.md`: заголовок «Phase 4–5», раздел «Phase 5: ПроБЕГ и дедупликация» (7 шагов однострочниками), «Автослияние (D-08)» (включение/выключение одной строкой `.env`), «Откат Phase 5»; accept-baseline в «Запасных путях» — для любого `--source <код>`.
- `.env.example`: закомментированный блок `DEDUP_*` со значениями `DefaultConfig` (0.7 / 0.3 / false / 0.10 / 3 / 100 / marathonec,probeg / 10:00).
- `deploy/oneliner.sh` не менялся, пакет — 12 файлов.

## Решения

- `DEDUP_AUTO_MERGE` остаётся false; пороги не менялись; все `DEDUP_*` в compose — `${NAME:-}` (пусто = `DefaultConfig`).

## Deviations from Plan

None — задачи 1–2 выполнены как написано. Коммиты не делались (делает владелец).

## Ожидает владельца

Задачи 3–6: коммит изменений, `ship.sh` → INSTALL → `ship.sh` → `agg deploy`/`agg verify`, прогон Марафонца, санити-отчёт и принятие ПроБЕГа, `/dupes` и сводка, решение по автослиянию.

## Наблюдаемое состояние VPS (2026-10-01, по сохранённым локальным логам `agg verify`)

Задачи 1–2 закоммичены в `faa8ee5`. Логи сняты владельцем; исполнитель к VPS не подключался.

| Лог в `~/run-agg-phase4/` | Время | Образ | Что видно |
|---|---|---|---|
| `deploy-verify.log` | 10:42 | `aggregator:faa8ee511c52` | marathonec прогон 5 `ok`, ПроБЕГ прогон 4 `ok` и `accepted=t`; `fields_null=0` у обоих; кандидатов `pending:104`, `events_merged=0`; `dedup_auto_merge_bot/worker=false` |
| `p6-deploy-verify.log` | 13:49 | `aggregator:e8005e8f2940` | добавлен heroleague, прогон 7 `suspect`; `pending:0`, `confirmed:86`, `rejected:8`, `events_merged=86`; автослияние `false` |
| `p6-verify-accepted.log` | 13:55 | `aggregator:e8005e8f2940` | heroleague прогон 7 `ok`, `accepted=t`; `fields_null=0` у трёх источников; автослияние `false` |

При `dedup_auto_merge=false` в обоих логах 86 слияний могли быть только ручными (`/dupes`). Имена логов `p6-*` не совпадают с `p5-*` из `deploy/README.md`. Образ `6d9efa3` (коммит UX бота, 14:50) в этих логах не виден.

## Итог задач 3–6 (аудит 2026-10-01 по сохранённым логам)

Логи плана (`p5-deploy-verify.log`, `p5-verify-fields.log`, `p5-verify-accepted.log`) не создавались; их роль выполняют `deploy-verify.log`, `p6-deploy-verify.log`, `p6-verify-accepted.log`. Автоматические проверки `DEPLOY P5 OK` и `ACCEPTED P5 OK` в написанном виде не выполнялись (другие имена файлов, нет снимка до принятия); критерии ниже проверены по строкам логов вручную.

| Критерий | Статус | Доказательство |
|---|---|---|
| Образ развёрнут, сервисы работают | подтверждено | `DEPLOY VERIFY OK`, `CHECK compose-state: OK`; `aggregator:faa8ee511c52`, затем `aggregator:e8005e8f2940`; IP и токенов в логах нет |
| Автослияние выключено | подтверждено | `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false` во всех трёх логах |
| `fields` Марафонца заполнены | подтверждено | `fields_null=marathonec|0`; прогоны 5 и 6 `ok`, parsed 134 |
| Первый прогон ПроБЕГа — `suspect` только как первый | подтверждено по логу worker | прогон 4: причина «первый прогон источника — требуется принятие»; parsed 1312, rejected 0, expired 0, out_of_scope 156 |
| Сверка с фикстурой | частично | parsed 1312 против 1305 в фикстуре; out_of_scope 156 против 155; «Страна» и «Серии» из `sanity-report` не сохранены |
| Состояние до принятия (кандидатов 0, слияний 0) | не сохранено | самый ранний лог снят после принятия ПроБЕГа |
| ПроБЕГ принят вручную | подтверждено | `source_run=probeg|4|ok|1312|accepted=t`, «принят вручную» |
| Алертов нет, `fields` заполнены | подтверждено | `source_alerts=0`, `fields_null=probeg|0` |
| `/dupes` разобран, слияния только по решению администратора | подтверждено | `dedup_candidates=pending:0|confirmed:86|rejected:8|fresh:0`, `events_merged=86` (86 ≤ 86) |
| Выдача согласована со слияниями | частично | `events_listed` 538 → 452 (−86); прирост от принятия ПроБЕГа не измерен |
| Ежедневная сводка | подтверждено | лог бота: `digest sent`, pending 104, fresh 104, 2026-10-01 10:41 МСК |
| Карточка слитого события в production | не подтверждено | проверено только тестами `TestMergedCardOpensRoot`, `TestTracerPass` |
| «Лига Героев» (D-22): отдельный прогон и принятие после ПроБЕГа | подтверждено | прогон 7: `suspect` (13:49) → `ok`, `accepted=t` (13:55); parsed 20, expired 17, rejected 0, out_of_scope 0; `fields_null=heroleague|0` |
| «Лига Героев»: страна RU и `UnrecognizedRegion` | не сохранено | вывод `sanity-report` не сохранён |

Не записаны (в логах нет): `IMAGE_PREV`, текст алертов, строка `run-once`, доля «один забег» среди просмотренных кандидатов. Текущий тег образа после 13:55 не проверялся; коммит `6d9efa3` развёрнутым не считается.

**Задача 5.** Ворота: `CALIBRATION pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0` → `CALIBRATION GATE PASSED`. Решение владельца 2026-10-01: «Оставляем ручное слияние дублей. DEDUP_AUTO_MERGE=false.» — keep-off (D-23).

**Задача 6.** Не выполнялась: автослияние не включалось. Условие включения — D-21 (Phase 6).
