---
phase: 06-production
plan: 06
subsystem: monitoring
tags: [heartbeat, alerts, healthcheck, deploy]
status: deployed-awaiting-stop-test-decision
progress: "задачи 1–8 выполнены 2026-10-05: решение log-only, локальная реализация (коммит 269b37c), пакет и INSTALL №2, образ 269b37cf9ec8, deploy, MONITORING DEPLOYED OK; задача 9 (проверка остановкой worker) — ждёт решения владельца"
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

Мониторинг выкачен в production 2026-10-05 (образ `269b37cf9ec8`). MON-03 и MON-04 в `REQUIREMENTS.md` не отмечены до решения по задаче 9.

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

## Задачи 5–8 — выкат (2026-10-05)

Команды готовил исполнитель, выполнял владелец; логи — `$HOME/run-agg-phase6/mon-*.log` вне репозитория, адресов и токенов в них нет. Код — коммит владельца `269b37c`.

| Задача | Шаг | Результат |
|---|---|---|
| 5 | `ship.sh`, первый запуск | `mon-ship-1.log`: `SMOKE OK`, ожидаемый `STOP` сверки, образ не передавался — `SHIP PACKAGE OK`; однострочник установки соответствует репозиторию (`ONELINER MATCHES REPO`, `SHA256SUMS` `8b1e4301…837c`) |
| 6 | `INSTALL` №2 в root-консоли | ответ владельца дословно: «готово, AGG INSTALL DONE» |
| 6а | `ship.sh`, второй запуск | `mon-ship-2.log`: `STOP` нет (установленные файлы совпали с репозиторием), `AGG LOAD OK: aggregator:269b37cf9ec8` — `IMAGE LOADED TAG=269b37cf9ec8` |
| 7, шаг 1 | `agg verify` и `agg backup-status` до выката | `mon-pre-deploy.log`: `postgres_image id=sha256:d74eeac9…c46f tag=same repo_digest=sha256:d74eeac9…c46f` — `POSTGRES TAG: SAME`; `aggregator-postgres-1 … postgres:17 … 4 days ago, Up 4 days (healthy)`; `backup_copy_age_s=11538`, `latest=aggregator-2026-10-05.dump.age size=202474 usable=yes`; `compose-state: FAIL` — ожидаемо (прежний контейнер `worker` без healthcheck) — `DEPLOY TAG=269b37cf9ec8` |
| 7, шаг 2 | `agg deploy 269b37cf9ec8`, слово `DEPLOY` | `mon-deploy.log`: `IMAGE_PREV=636deea312f7`, `postgres image: local, pull skipped`, контейнер `postgres` — `Healthy` без пересоздания, `DEPLOY APPLY DONE` (09:28 МСК) |
| 8 | `agg verify` через 2,5 минуты (исполнитель, по разрешению владельца) | `mon-verify.log`: `DEPLOY VERIFY OK`, `rc=0`, `CHECK compose-state: OK`; `aggregator-bot-1 … aggregator:269b37cf9ec8 … Up 2 minutes (healthy)`, `aggregator-worker-1 … aggregator:269b37cf9ec8 … Up 2 minutes (healthy)`; `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`; `events_listed=522`, `events_merged=96`, `source_alerts=0` — `MONITORING DEPLOYED OK` |

POSTGRES IMAGE: UNCHANGED
POSTGRES CONTAINER: KEPT

Образ `postgres` до и после — один идентификатор (`sha256:d74eeac9…c46f`); строка `aggregator-postgres-1` до и после — `4 days ago`, `Up 4 days (healthy)` (D-26).

Сообщения бота после выката: ответ владельца дословно — «ПОка ничего не приходило» (ложных алертов нет).

## Не сделано

Задача 9 — проверка алерта остановкой `worker` дольше 10 минут и запуском обратно: ждёт решения владельца (разрешение или отказ). До неё алерты `worker_stale` и «Восстановлено» в production не наблюдались — только автотесты (`TestInfraTracer`, `TestInfraLoop`).
