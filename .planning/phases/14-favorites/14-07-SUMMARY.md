---
phase: 14-favorites
plan: 07
status: rolled-out-awaiting-checks
requirements-completed: []
---

# 14-07: выкат и приёмка в production

Статус: выкат выполнен; ждут первые прогоны источников, напоминание и приёмка. Все команды на сервере выполняет владелец; логи — в `$HOME/run-agg-phase4/p14-*.log` вне репозитория.

## Разрешения владельца (2026-10-10)

Состояние перед разрешением: образ production `391b9b0fc6bc`, миграция `00009`; `main` = `origin/main` = `5ebde6c`, дерево чистое; ворота Phase 13 (`PRODUCTION CHECK:`, `PRODUCTION ACCEPTANCE:`), восстановления и локальной приёмки Phase 14 на месте; файлы пакета с `391b9b0` не менялись.

SHIP PERMITTED: 2026-10-10
BACKUP PERMITTED: 2026-10-10
DEPLOY PERMITTED: 2026-10-10
MIGRATION PERMITTED: 2026-10-10
«разрешаю доставку образа, резервную копию, выкат и миграцию»

Запросы только чтением в root-консоли этой фразой не разрешены; разрешение запрошено отдельно перед шагом 3 и получено:

QUERY PERMITTED: 2026-10-10
«разрешаю запрос»

## Ход выката

| Шаг | Результат |
|---|---|
| 1. Состояние копий | `STATUS OK`, `usable=yes`, копий 7, последняя от 2026-10-10, возраст 23981 с (для миграции слишком старая — свежая копия на шаге 6) |
| 2. Состояние до | `DEPLOY VERIFY OK`; бот и воркер на образе `391b9b0fc6bc`, `healthy`; `events_listed=559`, `events_merged=104`, `dedup_candidates=pending:0|confirmed:104|rejected:8|fresh:0`, `users=44`, `source_alerts=0`; автослияние выключено в боте и воркере; последние прогоны: marathonec 127, probeg 129, heroleague 128 (принят вручную), tristyle 126 |
| 3. Запрос «до» | `run_counts=` по трём последним прогонам каждого источника — все `ok`: heroleague 119, 123 — `unchanged:2`, 128 — `unchanged:1`; marathonec 120, 124, 127 — `created:0|changed:0|unchanged:110`; probeg 121 — `changed:1|unchanged:399`, 125 — `created:2|unchanged:400`, 129 — `unchanged:402`; tristyle 118, 122 — `unchanged:9`, 126 — `created:5|unchanged:9` |

### До выката (строки запроса)

run_counts=heroleague|119|ok|created:0|changed:0|unchanged:2
run_counts=heroleague|123|ok|created:0|changed:0|unchanged:2
run_counts=heroleague|128|ok|created:0|changed:0|unchanged:1
run_counts=marathonec|120|ok|created:0|changed:0|unchanged:110
run_counts=marathonec|124|ok|created:0|changed:0|unchanged:110
run_counts=marathonec|127|ok|created:0|changed:0|unchanged:110
run_counts=probeg|121|ok|created:0|changed:1|unchanged:399
run_counts=probeg|125|ok|created:2|changed:0|unchanged:400
run_counts=probeg|129|ok|created:0|changed:0|unchanged:402
run_counts=tristyle|118|ok|created:0|changed:0|unchanged:9
run_counts=tristyle|122|ok|created:0|changed:0|unchanged:9
run_counts=tristyle|126|ok|created:5|changed:0|unchanged:9

| 4. Доставка образа | `SMOKE OK`, `AGG LOAD OK`, `IMAGE_TAG=5ebde6c49d95` (лог `p14-ship.log`); остановки на сверке хешей не было, переустановка пакета не требовалась и не выполнялась |
| 5. Preflight | `DEPLOY PREFLIGHT DONE`; образ `aggregator:5ebde6c49d95` linux/amd64, `.env` права 600 и все пять переменных на месте, `compose config -q` OK; хеш `docker-compose.yml` установленного и развёрнутого одинаков (`f4be88eb…ddadfb`); сервисы всё ещё на `391b9b0fc6bc` |
| 6. Свежая копия перед миграцией | `STATUS OK`, `usable=yes`, `backup_age_s=8`, `backup_copy_age_s=7`, копий 7, размер 225486 байт (лог `p14-fresh-backup-status.log`); строка `exit=` ручного запуска копии не передавалась |
| 7. Deploy с миграцией `00010` | `DEPLOY APPLY DONE`; `IMAGE_TAG=5ebde6c49d95`, `IMAGE_PREV=391b9b0fc6bc`; `migrate` пересоздан и завершился (`Exited`), `bot` и `worker` пересозданы и запущены; `postgres` не перезапускался (лог `p14-deploy.log`) |
| 8. Состояние после | `DEPLOY VERIFY OK`; бот и воркер на `5ebde6c49d95`, `healthy`; `migrate` — `Exited (0)`; `events_listed=559`, `events_merged=104`, `dedup_candidates=pending:0|confirmed:104|rejected:8|fresh:0`, `users=44`, `source_alerts=0` — как до выката; автослияние выключено в боте и воркере; `dedup_pass_last=unknown` (воркер только что перезапущен); расписание прогонов: tristyle 13:38, marathonec 14:03, heroleague 14:18, probeg 16:12 UTC |
| 9. Запросы после выката | `favorites=0|users=0`; строк `outbox=` нет (очередь пуста) |
| 10. Telegram | владелец: «меню новое»; затем владелец: «все пункты проверил, расхождений нет» (пустой раздел, «Бег» и «Лыжи», строка избранного в карточке, кнопка в старом сообщении) |

ROLLOUT DONE: 2026-10-10
«favorites=0|users=0, outbox пусто, меню новое»

Образ `5ebde6c49d95` (прежний `391b9b0fc6bc`), миграция `00010` применена; свежая копия сделана за 8 секунд до deploy; `events_merged=104` и автослияние без изменений; таблицы избранного и очереди пусты.
