---
phase: 06-production
plan: 06
subsystem: monitoring
tags: [heartbeat, alerts, healthcheck, deploy]
status: local-done-awaiting-rollout
progress: "задачи 1–4 выполнены 2026-10-05 (решение log-only, отметки, алерты, healthcheck, закрепление образа postgres) — локально, не закоммичено; задачи 5–9 (пакет, INSTALL №2, образ, deploy, verify, проверка остановкой worker) — шаги владельца, не начаты"
requirements-completed: []
requires: [06-05]
key-files:
  created:
    - internal/store/heartbeats.go
    - internal/heartbeat/heartbeat.go
    - internal/alert/infra.go
  modified:
    - cmd/aggregator/main.go
    - internal/bot/bot.go
    - docker-compose.yml
    - deploy/vps/deploy-verify.sh
    - deploy/vps/deploy-apply.sh
    - deploy/deploy-apply-test.sh
---

# Phase 6 Plan 06: мониторинг сервиса (MON-03, MON-04)

Локальная часть готова; в production ничего не выкачено. MON-03 и MON-04 не отмечены до выката и проверки.

## Задача 1 — решение владельца

Ворота: `RESTORE GATE PASSED` (строка `RESTORE GATE: CONFIRMED 2026-10-04` в `06-05-SUMMARY.md`).

Ответ владельца дословно (2026-10-05): «log-only». `worker` следит за `bot` без токена Telegram: `bot_stale` — только в лог; токен остаётся у `bot` (`MON03_BRANCH=log-only`: `TELEGRAM_BOT_TOKEN` объявлен в одном сервисе, `NewSender` в коде нет).

## Задачи 2–4 — что сделано

| Что | Где | Поведение |
|---|---|---|
| Отметки | `store.Beat`, `store.HeartbeatAges`, `heartbeat.Run` | `bot` и `worker` раз в минуту пишут строку `heartbeats`; возраст считает БД. `worker` — отдельной горутиной, прогон её не задерживает; разовые команды `worker` отметку не пишут |
| Гейт бота (LO-06) | `(*Bot).Healthy` | отметка бота ставится, только пока `getMe` отвечает и библиотека Telegram не сообщала об ошибках последние 2 минуты |
| Алерты | `alert.InfraLoop` | `worker_stale` (10 мин), `backup_stale` и `backup_copy_stale` (26 ч, независимо — D-12, D-13), `postgres_down` (три опроса подряд без ответа; остальные ключи при этом не вычисляются); один алерт на смену состояния, напоминание через 24 ч, «Восстановлено: <ключ>» при возврате; при неудачной отправке повтор через 1, 2, 4, 8, 15, 15… минут |
| Остановка бота (IN-07) | `(*Bot).Wait`, `recoverMW` | обработчик получает ctx без отмены от SIGTERM; бот ждёт выполняющиеся обработчики до 15 с |
| healthcheck | `runHealthcheck`, `heartbeatFresh` | без аргумента — только БД; `bot` или `worker` — БД и отметка не старше 5 минут; иное — код 2 |
| compose | `docker-compose.yml` | healthcheck у `bot` (аргумент `bot`) и у `worker`; `stop_grace_period: 20s` у `bot`; блок `postgres` и `x-logging` не менялись |
| Образ postgres (D-26, S10) | `deploy-apply.sh`, `deploy-verify.sh` | `agg deploy` не выполняет `pull`, если `postgres:17` есть на хосте (`postgres image: local, pull skipped`); `agg verify` печатает `postgres_image id=… tag=same\|differs repo_digest=…` и требует `healthy` у `worker` |

`DECISION size_warn=log` (06-01-SUMMARY, дословно) — алерта `backup_size_warn` в коде нет (`SIZE_WARN=off`).

## Проверки (2026-10-05)

- `gofmt`, `go vet`, `go mod verify` — чисто; `go.mod`, `go.sum`, `internal/normalize/country.go`, `migrations/` не изменены; прямые зависимости — прежние четыре.
- Полный прогон `go test -p 1 -count=1 -v ./...` с `TEST_DATABASE_URL`: 543 PASS, 0 FAIL, 0 SKIP, 15 пакетов `ok`. Новые тесты: `TestBeat`, `TestHeartbeatAges`, `TestHeartbeatRun`, `TestInfraDecide`, `TestInfraBackoff`, `TestInfraLoop`, `TestInfraLoopBackoff`, `TestInfraPostgresDown`, `TestInfraLogOnly`, `TestInfraTracer`, `TestHeartbeatFresh`, `TestHealthy`, `TestWaitHandlers`; прежние алерты источников (`TestTick…`) проходят, `internal/alert/alert.go` не изменён.
- `deploy/deploy-apply-test.sh`: `ok   pull skipped: …`, `ok   pull on missing image: …`, четыре случая строки `postgres_image`, итог `DEPLOY APPLY OK`.
- `docker compose config -q` на новом compose — проходит; блок `postgres` и якорь `x-logging` совпадают с `faa8ee5`.
- Эталоны экранов не менялись.

## Отступления от плана (план написан 2026-10-01)

- Миграций пять, а не четыре: `00005` добавлена в Phase 7. Этот план миграций не добавляет; проверки плана «ровно 4 файла» выполнены как «каталог `migrations/` не изменён».
- Статическая проверка «SQL только в `internal/store`» находит строки в тестах Phase 8 (`internal/bot/ux3_test.go`, `stale_test.go`) — они появились до этого плана и им не менялись. В новых файлах плана вне `internal/store` SQL нет.
- Сверх плана: локальные случаи строки `postgres_image` в `deploy/deploy-apply-test.sh` (план оставлял формат без локального теста).
- Задача 9 плана ссылается на `/runs` и остановку `worker` — без изменений.

## Допущения

- Состояние инфраструктурных алертов — в памяти процесса: после перезапуска `bot` неустранённый алерт уходит повторно один раз.
- «Сбой бэкапа → алерт» — это просрочка отметки: при сбое ночного запуска алерт придёт, когда отметке исполнится 26 часов, а не в момент сбоя.
- Отметка `worker` показывает, что процесс жив; зависший планировщик при живом процессе она не обнаруживает.
- Пока Telegram недоступен с VPS дольше 5 минут, контейнер `bot` становится `unhealthy` (отметка не ставится); сам он при этом не перезапускается.
- До выполнения 06-08 активного уведомления о мёртвом боте нет: только лог `worker` и `agg verify`.

## Что меняется для выката

- Пакет `agg` меняется: `SHA256SUMS` был `4c2f486f…f183`, по рабочему дереву станет `8b1e4301…837c` (окончательный — после коммита). Поэтому первый `ship.sh` остановится на сверке и потребуется `INSTALL` №2 в root-консоли.
- После `INSTALL` №2 и до выката `agg verify` ожидаемо даёт `compose-state: FAIL`: новый скрипт требует `healthy` у `worker`, а контейнер ещё прежний.
- После `agg rollback` на прежний образ отметки `bot` и `worker` не пишутся: оба контейнера станут `unhealthy` по новому compose, алертов этого плана нет.

## Не сделано

Задачи 5–9 — шаги владельца, каждый по отдельному разрешению: коммит, `ship.sh` (ожидаемый STOP сверки), `INSTALL` №2, повторный `ship.sh`, `agg verify` и `agg backup-status` до выката (`tag=same`, свежая копия), `agg deploy`, `agg verify` после, необязательная проверка остановкой `worker`.
