---
phase: 22-extended-track-card
plan: 02
status: done
---

# 22-02: выкат времени старта — итог

Выкат выполнен владельцем 2026-10-23 по шагам, каждый — по отдельному разрешению. Миграций нет, пакет `deploy/` не менялся (`INSTALL` не нужен), новых запросов к сайтам нет.

## Шаги

1. `ship.sh` — `SMOKE OK`, `AGG LOAD OK`, образ `f76b92acb5cd`.
2. `agg preflight f76b92acb5cd` — `DEPLOY PREFLIGHT DONE` (`p22-preflight.log`).
3. `agg deploy f76b92acb5cd` — `DEPLOY APPLY DONE`, `IMAGE_PREV=e479978480a0`; `agg verify` — `DEPLOY VERIFY OK` (`p22-verify.log`): все 11 источников `ok`, каталог 935, слияний 191 (пары reg.place разобраны владельцем), `pending:0`, автослияние выключено.
4. Ручной прогон Лиги Героев в root-консоли (`worker run-once --source heroleague`): прогон № 451 — `ok`, 15 записей; `verify` — `DEPLOY VERIFY OK` (`p22-verify-runs.log`). Число `changed` в `verify` не видно (прогон вне воркера).

## Приёмка

PRODUCTION ACCEPTANCE 2026-10-23 — владелец: «Всё работает, время старта видно в боте и Mini App».
