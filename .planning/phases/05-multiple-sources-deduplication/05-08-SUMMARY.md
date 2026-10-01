---
phase: 05-multiple-sources-deduplication
plan: 08
subsystem: deploy
tags: [deploy, dedup, compose, smoke, verify]
status: in-progress
progress: "tasks 1–2 done, awaiting owner VPS steps"
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

# Phase 5 Plan 08: выкат Phase 5 — промежуточный итог

**Статус: tasks 1–2 done, awaiting owner VPS steps** (задачи 3–6 — действия владельца на VPS).

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
