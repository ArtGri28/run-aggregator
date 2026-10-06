---
phase: 11-support-chat
plan: 04
subsystem: bot
tags: [telegram, support, routing, group, snapshots]
requires: [11-01, 11-02, 11-03]
provides:
  - "internal/bot/bot.go: `chatMW` вместо `privateOnlyMW` — единственное место маршрутизации чатов (личный чат → прежние обработчики; настроенная группа → `onSupportGroup` без `next`; callback из любого не личного чата → один пустой ответ; сообщение из иной группы или супергруппы → одна запись Info с идентификатором и типом чата; остальное молча); `isPrivate` не менялась"
  - "internal/bot/support.go: `onSupportGroup` (ответ на сообщение со связкой → сообщение автору обращения через `b.send`, обычный текст, кнопка «💬 Ответить» `h:r`; нетекстовый ответ → G-3; ошибка доставки → G-2 «заблокировал» (любой 403) или «ошибка отправки»; перенос группы → одна запись Error), `isServiceMessage`, `groupSay` (сообщение в группу ответом на сообщение отвечавшего: `reply_parameters` + `allow_sending_without_reply`, обычный текст, одна попытка), константы `groupBlocked`, `groupFailed`, `groupNotText`"
  - "internal/bot/support_test.go: TestS32SupportReply, TestS33ReplyNotDelivered, TestS34GroupIgnored, TestSupportNoTextInLogs, TestSupportStoresOnlyLinks, шаг 8 в TestS35SupportDisabled; хелперы `inChat`, `groupReply`, `groupMedia`, `groupCallback`, `toUser`, `onlyFields`, `replyParams`, `userAPI`, `groupBroken`, `openRequest`, `replyEnv`, `supportDialog`, `dialogLeaks`"
affects: [11-05, 11-06]
plan_head_before: 12f50a6
actuals:
  tokens: 12000
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "адресат ответа определяется только связкой `SupportAuthor(группа, сообщение, на которое ответили)`; автор ответа и `ADMIN_TG_IDS` не читаются (D-04)"
    - "пользователю уходит новое сообщение бота через существующий `b.send` с `plain: true`: в запрос попадают `chat_id`, `text`, `reply_markup`, `link_preview_options` — других полей нет (тест `onlyFields`), поэтому вложения, пересылка и копирование невозможны по построению (D-11)"
    - "в лог — только вид события (`reply sent`, `reply not text`) и вид ошибки (`kindName`); исключения без данных пользователя: Info о чужой группе (Q-7) и Error о переносе группы"
key-files:
  created:
    - internal/bot/testdata/screens/ (8 новых эталонов s32_* … s34_*)
  modified:
    - internal/bot/bot.go
    - internal/bot/support.go
    - internal/bot/support_test.go
key-decisions:
  - "Служебные сообщения Telegram (закрепление — у него reply на закреплённое и нет текста, вступление, выход, смена названия и фото, видеочат, темы) распознаёт `isServiceMessage` и игнорирует до поиска связки: иначе закрепление обращения было бы принято за нетекстовый ответ и получило бы G-3 (дополнение А контракта, S-34 шаг 6)"
  - "Сообщение в группу (G-2, G-3) уходит одной попыткой без `withRetry` (контракт S-33 шаг 6: «повторов нет»); сбой только в лог видом ошибки. Доставка пользователю идёт через `b.send` с единственным повтором после 429"
  - "Ошибка хранилища при поиске связки — запись Error в лог, в группу ничего: ответ на обычное сообщение участника во время сбоя базы не должен порождать сообщения G-2 на каждое такое сообщение"
requirements-completed: [SUP-08, SUP-09, SUP-10, SUP-11]
completed: 2026-10-06
status: complete
---

# Phase 11 Plan 04: сторона группы в поддержке

**Статус: complete.** Участник группы отвечает reply на обращение — автор получает текст ответа новым сообщением бота с кнопкой «💬 Ответить» (без имени отвечавшего, без разметки); нажатие кнопки снова открывает режим ввода и следующее сообщение уходит новым обращением. Нетекстовый ответ не доставляется, об этом бот пишет в группе (G-3); недоставленный ответ отмечается в группе (G-2). Всё остальное в группе бот игнорирует: обычные сообщения, команды (и у администратора), поиск, служебные сообщения, чужие группы; кнопки получают один пустой ответ. Ничего не закоммичено (коммиты делает владелец); production, VPS и `.env` не затрагивались; бот с настоящим токеном не запускался.

## Что сделано

- **Задача 1 (tracer).** Тест `TestS32SupportReply` написан первым (красный: ноль вызовов вместо доставки). `bot.go`: `privateOnlyMW` заменён на `chatMW` (в списке опций и в комментарии); `isPrivate` не тронута, `TestPrivateOnly` проходит без правок. `support.go`: `onSupportGroup` — нет reply → выход; `SupportAuthor(m.Chat.ID, ReplyToMessage.ID)`; нет связки → выход; доставка `b.send(ctx, автор, screen{text: m.Text, kb: «💬 Ответить», plain: true})`; запись `event="reply sent"`. Тест проверяет поля запроса пользователю (`onlyFields`), отсутствие имени, username и id отвечавшего, неизменность `users`/`user_profiles`/таблиц поддержки, дословную доставку HTML-символов, второй ответ, анонимного администратора (без `From`), администратора из `ADMIN_TG_IDS`, «💬 Ответить» → приглашение новым сообщением, новое обращение с новой связкой, отсутствие режима «чат», ответ через 30 суток, сбой доставки (одно сообщение в группу ответом на 906), логи. Эталоны `s32_reply`, `s32_reply_html`, `s32_invite` — вручную.
- **Задача 2.** `TestS33ReplyNotDelivered` написан первым (красный: общий текст вместо «заблокировал»). `onSupportGroup`: ответ без текста (в том числе фото с подписью) → пользователю ни одного вызова, в группу G-3; ошибка доставки через `classify`: `kindBlocked` (любой 403) → G-2 «заблокировал бота», иное (400, 500, 429 после исчерпанного повтора) → G-2 «ошибка отправки»; `groupSay` — `reply_parameters` на сообщение отвечавшего с `allow_sending_without_reply`, без `parse_mode`. Связка при недоставке остаётся (шаг «разблокировка» доставляет следующий ответ). Эталоны `s33_blocked`, `s33_error`, `s33_nontext`.
- **Задача 3.** `TestS34GroupIgnored`, `TestSupportNoTextInLogs`, `TestSupportStoresOnlyLinks` и шаг 8 `TestS35SupportDisabled` написаны первыми (красные: нет записи об иной группе, закрепление получало G-3, нет записи о переносе). `onSupportGroup`: `MigrateToChatID != 0` → одна запись Error с новым идентификатором и выход; `isServiceMessage`. `chatMW`: сообщение из группы или супергруппы с иным идентификатором (и при выключенной поддержке) — одна запись Info `message from unconfigured group chat_id=… chat_type=…`, на каждое сообщение. Эталоны `s34_callback`, `s34_reply_after_noise`.

## Проверки (реальные хвосты вывода)

| Проверка | Результат |
|---|---|
| задача 1, команда `<verify>` | хвост: `--- PASS: TestHeartbeatFresh`, `PASS`, `ok  	aggregator/cmd/aggregator	1.011s`, затем `TRACER OK`; в логе `TRACER reply=sent button=reply next=sent` |
| задача 2, команда `<verify>` | хвост: `PASS`, `ok  	aggregator/cmd/aggregator	0.548s`, затем `DELIVERY OK` |
| задача 3, команда `<verify>` | хвост: `PASS`, `ok  	aggregator/cmd/aggregator	0.641s` (после последней правки теста — `0.340s`), затем `PLAN 11-04 OK` |
| `go test -p 1 -count=1 -v ./...` | код возврата 0; `grep -cE -- '--- (FAIL\|SKIP)'` = **0**; все 15 пакетов `ok`; верхнего уровня **419** тестов `PASS` (было 414: +5 — `TestS32…TestS34`, `TestSupportNoTextInLogs`, `TestSupportStoresOnlyLinks`), всего с подтестами **638** (было 633) |
| `gofmt -l .`, `go vet ./...` | пусто, чисто |
| `go.mod`, `go.sum`, `deploy/`, `docker-compose.yml`, миграции, `internal/store/`, `snapshot_test.go`, контракты Phase 8 и 9, `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md` | не изменены |
| код поддержки: `CopyMessage`, `ForwardMessage`, `SendPhoto`, `SendDocument`, `SendMediaGroup` | вне комментариев нет (проверяет каждая команда `<verify>`) |

Команды `<verify>` выполнялись целиком в виде одного скрипта, лежавшего вне репозитория (проверки плана без изменений, по одному вызову на задачу); после работы скрипт удалён.

## Эталоны

`git status --short internal/bot/testdata/screens` — только `??` (8 новых файлов), модифицированных и удалённых нет; в каталоге было 165 файлов, стало 173. Файлы написаны вручную по контракту (текст — из `11-UX-CONTRACT.md`, раздел 4 H-4 и раздел 5 G-2, G-3); тест их только читает. Обычный инструмент записи обрезал концевые пробелы в строках `parse_mode: `, `text: `, `show_alert: `; они дописаны скриптом, содержимое от этого не менялось.

Созданы: `s32_reply`, `s32_reply_html`, `s32_invite`, `s33_blocked`, `s33_error`, `s33_nontext`, `s34_callback`, `s34_reply_after_noise`. `s32_invite` совпадает по содержимому с `s26_invite_reply` (тот же экран H-1 новым сообщением плюс пустой ответ на callback) — по контракту это два имени.

## Чувствительность тестов (временные мутации, возвращены)

Исходные `bot.go` и `support.go` сохранялись копией вне репозитория; после каждой мутации возвращались копией, после последней — `cmp` без отличий и повторный зелёный прогон.

| Мутация | Что должно ловиться | Результат |
|---|---|---|
| (а1) `chatMW`: `case isPrivate(u)` → `isPrivate(u) \|\| сообщение из группы поддержки` (команды и текст группы идут к обычным обработчикам) | любой тест группы | падают `TestS32SupportReply`, `TestS33ReplyNotDelivered`, `TestS34GroupIgnored`, `TestSupportNoTextInLogs`, `TestSupportStoresOnlyLinks` |
| (а2) `chatMW`: `onSupportGroup` → `next` | то же | падают те же пять тестов |
| (б1) `onSupportGroup`: при `!ok` (нет связки) адресат подставляется и ответ доставляется | ответ на сообщение без связки | падают `TestS33ReplyNotDelivered` («ответ на сообщение бота 102 без связки: вызовы [sendMessage chat_id:1001 …]») и `TestS34GroupIgnored` («ответ на сообщение участника: вызовы [sendMessage chat_id:1001 …], want 0») |
| (б2) `chatMW` обслуживает любую группу, а связка ищется в настроенной | ответ из чужой группы с тем же номером | падают `TestS34GroupIgnored`, `TestSupportNoTextInLogs` |

Первая попытка (б1) не собралась (неиспользуемая переменная, затем несовпадение типов) — это не результат теста, мутация переписана и прогнана заново.

## Отличия контракта и плана

- Эталоны не показывают `reply_parameters` (`renderCall` из `snapshot_test.go` его не печатает, а файл менять нельзя): сверка идёт эталоном по тексту и `parse_mode`, а `reply_parameters.message_id` (номер сообщения отвечавшего, не обращения) и `allow_sending_without_reply` проверяются отдельно в `TestS33ReplyNotDelivered` (`replyParams`).
- План (задача 3) говорит «остальные ветки игнорирования уже дают выход» — служебное сообщение «закреплено» (reply на обращение, текста нет) уходило в ветку нетекстового ответа и получало G-3. Контракт (дополнение А, S-34 шаг 6) требует игнорировать — добавлен `isServiceMessage` (по контракту).
- Контракт S-33 шаг 6 («повторов нет») — сообщение в группу идёт одним вызовом `SendMessage` без `withRetry`; доставка пользователю — через `b.send` с единственным повтором после 429, как в S-33 шаг 3.
- План (задача 3, действие 2): запись о чужой группе в `bot.go`; контракт (дополнение И, S-35 шаг 8) требует её и при выключенной поддержке — так и сделано, шаг 8 S-35, оставленный в 11-03, добавлен в `TestS35SupportDisabled`. Пишется только для чатов типа `group` и `supergroup`.
- Текст ответа проверяется на пустоту (`m.Text == ""`), а не на тип вложения: любое сообщение без текста в ответе — «нетекстовое» (дополнение Г).

## Deviations from Plan

1. **[Rule 2 — недостающая корректность]** `isServiceMessage` (см. выше): без него закрепление обращения вызывало ложное G-3.
2. **[Rule 3 — блокирующее]** В `TestS35SupportDisabled` добавлен шаг 8 (запись о чужой группе при выключенной поддержке) и из `TestS33ReplyNotDelivered` вынесен общий хелпер `groupBroken` — чтобы тот же сценарий ошибок группы использовал `supportDialog`. Ожидания остальных тестов не менялись.
3. Ошибка хранилища в `SupportAuthor` — только запись Error без G-2 (ключевое решение выше). Если владелец хочет, чтобы и такая ошибка сообщалась в группе, это правка одной ветки.
4. В `TestS34GroupIgnored` перед `captureLogs` выполняется `replyEnv`, поэтому одна строка `INFO support event=sent` попадает в вывод `go test -v` мимо буфера; на результаты не влияет.

## Для плана 11-05

- `TestContractCoverage`: убрать исключение (множество `phase11`) — теперь у S-32, S-33, S-34 есть тесты `TestS32SupportReply`, `TestS33ReplyNotDelivered`, `TestS34GroupIgnored` и все эталоны из контракта: `s32_reply`, `s32_reply_html`, `s32_invite`, `s33_blocked`, `s33_error`, `s33_nontext`, `s34_callback`, `s34_reply_after_noise`. Сейчас тест зелёный при исключении.
- Запись о чужой группе (Q-7) пишется на каждое сообщение, как велит контракт. В группе с включённым режимом приватности бот получает только команды, ответы на свои сообщения и служебные сообщения, поэтому объём мал; после того как владелец узнал идентификатор группы, строку можно оставить — она ничего не раскрывает.
- Для production-приёмки (11-06) проверить в настоящем Telegram то, что на фиктивном API проверить нельзя: приходит ли боту ответ участника при режиме приватности «включён»; что `allow_sending_without_reply` и `reply_parameters` отображают G-2 и G-3 под сообщением отвечавшего; что 403 при доставке показывается как G-2 «заблокировал бота»; что адреса в тексте ответа остаются кликабельными, а `<b>` не разбирается; что закрепление сообщения в группе не вызывает ответа бота.
- Имена тестов для чек-листа приёмки: `TestS32SupportReply` (S-32), `TestS33ReplyNotDelivered` (S-33), `TestS34GroupIgnored` (S-34), `TestSupportNoTextInLogs`, `TestSupportStoresOnlyLinks`.

## Known Stubs

Нет.

## Threat Flags

Нет новых поверхностей сверх `<threat_model>` плана. T-11-19 (`TestS32SupportReply`, шаги 4 и мутация б1), T-11-20 (`TestS34GroupIgnored` шаги 1–3, мутации а1, а2), T-11-21 (шаг 7 `TestS34GroupIgnored`, мутация б2), T-11-22 (`onlyFields` в `TestS32SupportReply`, `TestS33ReplyNotDelivered` шаг 5), T-11-40 (шаг 2 `TestS32SupportReply`), T-11-23 (проверка полей запроса, `formsLeak`), T-11-24 (`TestS33ReplyNotDelivered`, связка остаётся), T-11-25 (`TestSupportNoTextInLogs`, `TestSupportStoresOnlyLinks`), T-11-26 (шаг 6 `TestS34GroupIgnored`) закрыты тестами.

## Не проверено

- Поведение настоящего Telegram (см. «Для плана 11-05»): режим приватности группы, отображение reply_parameters, формулировка 403 для удалённых аккаунтов (дополнение Д).
- `deploy/smoke.sh` не запускался (в этом плане он не менялся).

## Self-Check: PASSED

Файлы `internal/bot/support.go`, `internal/bot/bot.go`, `internal/bot/support_test.go` и 8 новых эталонов на месте; три команды `<verify>` (`TRACER OK`, `DELIVERY OK`, `PLAN 11-04 OK`), полный прогон (0 упавших и пропущенных, 419/638), `gofmt`, `go vet` пройдены; `git status` по каталогу эталонов — только `??`; временные мутации возвращены (`cmp` без отличий); `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md` не менялись; ничего не закоммичено (`git rev-list --count 12f50a6..HEAD` = 0).
