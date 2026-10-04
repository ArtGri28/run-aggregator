---
phase: 08-telegram-bot-ux
plan: 08
subsystem: bot
tags: [deploy, acceptance]
status: acceptance-passed
progress: "задачи 1–3 выполнены 2026-10-04: предусловия, выкат владельцем (образы fbdca0f5eecb, затем 11d6d4bd8ee0 со второй редакцией интерфейса), DEPLOY VERIFY OK, production-приёмка пройдена владельцем"
requirements-completed: [UX-03]
requires: [08-07, 08-09]
---

# Phase 8 Plan 08: production-приёмка

**Статус: задачи плана выполнены 2026-10-04.** Выкат выполнен дважды (первая и вторая редакция интерфейса), владелец прошёл приёмку в Telegram. UX-03 — Complete. Phase 8 не закрыта: закрытие — только по команде владельца.

## Задача 1 — предусловия и разрешение

- Ворота восстановления: в `06-05-SUMMARY.md` строка ворот с датой 2026-10-04 (ответ владельца «восстановление подтверждено»).
- Пакет `agg` на VPS актуален: `SHA256SUMS 4c2f486f…f183`, доставлен и установлен 2026-10-04 (06-04-SUMMARY.md); при доставке образа остановки на установке не было.
- Решение владельца о выкате дословно (2026-10-04): «Готовим выкат сейчас и выкатываем». Формулировки «разрешаю выкат», которую ищет автоматическая проверка задачи 1, в ответе нет — проверка в исходном виде не проходит; основание — ответ выше и то, что команды выката владелец выполнял сам.

## Задача 2 — выкат владельцем (2026-10-04)

Команды готовил исполнитель, выполнял владелец. Логи — вне репозитория.

| Шаг | Кто | Результат |
|---|---|---|
| Свежая копия перед миграцией (`backup.sh`, root-консоль) | владелец | со слов владельца: «готово, rc=0»; исполнителем не проверялось |
| `deploy/ship.sh` | владелец | лог `run-agg-phase6/p10-ship.log`: `SMOKE OK`, `AGG LOAD OK: aggregator:fbdca0f5eecb`, `IMAGE_TAG=fbdca0f5eecb` |
| `agg preflight fbdca0f5eecb` | владелец | лог `p10-preflight.log`: образ `linux/amd64`, идентификатор совпал с собранным на Mac; хеш compose-файла совпал с репозиторием; `.env` 600, пять ключей есть; `DEPLOY PREFLIGHT DONE` |
| `agg deploy fbdca0f5eecb`, слово `DEPLOY` | владелец | лог `p10-deploy.log`: `IMAGE_TAG=fbdca0f5eecb`, `IMAGE_PREV=e8005e8f2940`; миграция завершилась; `bot` и `worker` пересозданы; `postgres` не перезапускался; `DEPLOY APPLY DONE` |
| `agg verify` через 2 минуты | исполнитель, по разрешению владельца | лог `run-agg-phase4/p8-deploy-verify.log`: `DEPLOY VERIFY OK`, `rc=0` |

Что показал `agg verify` после выката:

- `bot`, `worker`, `migrate` — образ `aggregator:fbdca0f5eecb`; `bot` — `healthy`; `migrate` — `Exited (0)`.
- `events_listed=520`, `users=2`, `events_merged=89`, `dedup_candidates=pending:0|confirmed:89|rejected:8|fresh:0` — те же числа, что до выката и в восстановленной копии.
- `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`.
- Новые строки Phase 7: `dedup_would_auto=pending:0`, `dedup_pass_last=unknown` (после выката ещё не было прохода дедупликации).
- Проверки порта, прав и владельцев — `OK`; в логе нет адресов и строк, похожих на токен.

Образ `fbdca0f5eecb` собран из коммита `fbdca0f`: код тот же, что в `8cd29bf` (Phase 7 и первая редакция Phase 8); после него менялись только документы. Миграция `00005` применена на production этим выкатом. `.env` и `DEDUP_AUTO_MERGE` не менялись.

Автоматическая проверка задачи 2 (`DEPLOYED OK`) по этому логу выполняется: есть `DEPLOY VERIFY OK`, `dedup_auto_merge_bot=false`, нет строк, похожих на токен.

## Выкат второй редакции интерфейса (2026-10-04)

Вторая редакция (контракт, раздел 13; код — коммит `2052184`) выкачена тем же порядком. Образ `11d6d4bd8ee0` собран из коммита `11d6d4b` (после `2052184` менялись только документы). Миграций в этой версии нет, пакет `agg` не менялся. Свежая копия перед этим выкатом не делалась: схема базы не меняется, копия от 2026-10-04 проверена восстановлением.

| Шаг | Кто | Результат |
|---|---|---|
| `deploy/ship.sh` | владелец | лог `run-agg-phase6/p11-ship.log`: `SMOKE OK`, `AGG LOAD OK: aggregator:11d6d4bd8ee0`, `IMAGE_TAG=11d6d4bd8ee0` |
| `agg preflight 11d6d4bd8ee0` | владелец | лог `p11-preflight.log`: образ `linux/amd64`, идентификатор совпал с собранным на Mac; хеш compose-файла совпал с репозиторием; `DEPLOY PREFLIGHT DONE` |
| `agg deploy 11d6d4bd8ee0`, слово `DEPLOY` | владелец | лог `p11-deploy.log`: `IMAGE_TAG=11d6d4bd8ee0`, `IMAGE_PREV=fbdca0f5eecb`; `DEPLOY APPLY DONE` |
| `agg verify` через 2 минуты | исполнитель, по разрешению владельца | лог `run-agg-phase4/p11-deploy-verify.log`: `DEPLOY VERIFY OK`, `rc=0` |

`agg verify` после второго выката: `bot`, `worker`, `migrate` — образ `aggregator:11d6d4bd8ee0`, `bot` — `healthy`, `postgres` не перезапускался; `events_listed=520`, `users=2`, `events_merged=89`, `dedup_candidates=pending:0|confirmed:89|rejected:8|fresh:0`; `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`; `dedup_would_auto=pending:0`, `dedup_pass_last=unknown` (проходов дедупликации после выката ещё не было); проверки порта, прав и владельцев — `OK`; в логе нет адресов и строк, похожих на токен.

## Задача 3 — production-приёмка (UX-03)

Владелец прошёл приёмку в Telegram на образе `11d6d4bd8ee0` по чек-листу второй редакции (`08-ACCEPTANCE.md`, 16 шагов). Ответ владельца дословно (2026-10-04): «production-приёмка пройдена, разрешаю записать в документы». Расхождений владелец не назвал; ответ общий, без отметок по отдельным шагам.

UX-03 — Complete. Phase 8 этой записью не закрывается: `phase.complete` — только по команде владельца.

Не проверено приёмкой: первая редакция интерфейса на образе `fbdca0f5eecb` отдельно не принималась — она заменена второй редакцией через час после выката.

## Откат

`agg rollback` (слово `ROLLBACK`) возвращает предыдущий образ — после второго выката это `fbdca0f5eecb` (первая редакция Phase 8); образ `e8005e8f2940` остаётся на VPS, схема `00005` с ним совместима (проверено локально в Phase 7). Откат не выполнялся.
