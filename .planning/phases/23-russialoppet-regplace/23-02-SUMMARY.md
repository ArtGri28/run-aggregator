---
phase: 23-russialoppet-regplace
plan: 02
status: done
---

# 23-02: выкат источника Russialoppet — итог

Выкат выполнен владельцем 2026-10-21 по шагам, каждый — по отдельному разрешению. Миграций нет, пакет `deploy/` не менялся (`INSTALL` не нужен), свежая копия перед выкатом не требовалась.

## Шаги

1. `backup-status` — `STATUS OK`; `agg verify` — `DEPLOY VERIFY OK` (образ `ae0ab8e2ffc9`, каталог 828, слияний 139).
2. `ship.sh` — первая попытка: `SMOKE OK`, передача на сервер оборвалась по таймауту SSH (на сервере ничего не изменилось); после проверки связи повтор — `AGG LOAD OK`, образ `b714ab573646`.
3. `agg preflight b714ab573646` — `DEPLOY PREFLIGHT DONE` (лог `~/run-agg-phase4/p23-preflight.log`).
4. `agg deploy b714ab573646` — `DEPLOY APPLY DONE`, `IMAGE_PREV=ae0ab8e2ffc9`; `agg verify` — `DEPLOY VERIFY OK` (`p23-deploy.log`, `p23-verify.log`).
5. Первый прогон `russialoppet` (№ 351): TLS 1.2 с шифрами ECDSA с сервера работает, 271 КБ за 0,4 с; 50 записей, 50 разобраны, отбраковано 0, нераспознанных областей 0; `suspect` «первый прогон источника — требуется принятие». Принят владельцем в боте: создано 50 событий, `events_listed` 829 → 879; проход дедупликации — 0 пар (кандидатов с лыжами O-time нет); слияний 139, автослияние выключено (`p23-verify-accept.log`).

## Приёмка

PRODUCTION ACCEPTANCE 2026-10-21 — владелец: «Всё работает, лыжи видны в боте и Mini App».
