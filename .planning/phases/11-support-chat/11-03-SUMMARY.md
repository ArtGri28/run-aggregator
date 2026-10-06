SNAPSHOT BASE: 1c0edcf

---
phase: 11-support-chat
plan: 03
subsystem: bot
tags: [telegram, support, config, callback, snapshots]
requires: [11-01, 11-02]
provides:
  - "internal/bot/support.go: `supportPrompt` (H-1), `onSupportCmd` (/support), `onSupport` (кнопки `h:o`, `h:r`, `h:x`), `supportText` (приём обращения), `supportNonText` (отказ R-2), `supportSend`/`supportFailed`, `supportHeader` (шапка G-1), `kindName` (вид ошибки для лога); константы `awaitSupportFor`, `maxSupportText`, `supportLimit`, `supportWindow`"
  - "internal/bot/config.go: `Config.SupportChatID`, чтение `SUPPORT_CHAT_ID` (строго отрицательное целое; пусто — поддержка выключена)"
  - "internal/bot/callback.go: грамматика `h:<o|r|x>` (`cbSupport`, `cbSupportReply`, `cbSupportCancel`), буква `h` в `cbPrefix`"
  - "internal/bot/filters.go: `mainMenu(support bool)`; `onText` первым делом вызывает `supportText`"
  - "internal/bot/bot.go: поле `supportChat`; команда `support` и обработчик `h:` регистрируются только при включённой поддержке; третья ветка `onDefault` для сообщений без текста"
  - "docker-compose.yml, .env.example, deploy/smoke.sh, cmd/aggregator/main.go: переменная `SUPPORT_CHAT_ID` и признак `support` в строке лога `started`"
  - "internal/bot/support_test.go: TestS26SupportEntry, TestS27SupportText, TestS28SupportNonText, TestS29SupportCancel, TestS30SupportRejects, TestS31SupportSendFailure, TestS35SupportDisabled; хелперы `newSupportBot`, `supportEnv`, `groupReplies`, `groupSends`, `feed`, `split`, `named`, `mediaUpdate`, `supportState`, `renderAll`, `setNow`"
affects: [11-04, 11-05, 11-06]
plan_head_before: 1c0edcf
actuals:
  tokens: 17000
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "отметка читается без записи (`SupportAwaiting`), потребляется `SupportTake` под блокировкой строки; лимит и окно передаются параметрами хранилища"
    - "в группу уходит новое текстовое сообщение бота (`SendMessage` внутри `withRetry`, идентификатор из ответа идёт в `SupportLink`); пересылки, копирования и отправки вложений нет — проверка в команде каждой задачи"
    - "в лог — только вид события и вид ошибки (`kindName` поверх `classify`); исключение — идентификатор группы при её переносе (не данные пользователя)"
key-files:
  created:
    - internal/bot/support.go
    - internal/bot/support_test.go
    - internal/bot/testdata/screens/ (18 новых эталонов s26_* … s35_*)
  modified:
    - internal/bot/config.go
    - internal/bot/config_test.go
    - internal/bot/bot.go
    - internal/bot/bot_test.go
    - internal/bot/filters.go
    - internal/bot/callback.go
    - internal/bot/callback_test.go
    - internal/bot/contract_test.go
    - cmd/aggregator/main.go
    - docker-compose.yml
    - .env.example
    - deploy/smoke.sh
key-decisions:
  - "Отказ R-4 (сбой отправки в группу) возвращает отметку на 10 минут от момента сбоя через `SupportBegin`; отказы R-1 и R-2 отметку не трогают; R-3 оставляет её снятой (уже потреблена `SupportTake`) — Q-4 default"
  - "Ошибка чтения отметки или `SupportTake` — запись в лог и `errNotice`, сообщение считается обработанным: текст не должен молча уйти в поиск, когда пользователь ждал обращения"
  - "Сбой записи связки после успешной отправки в группу не отменяет подтверждение H-2 (дополнение Ж контракта)"
requirements-completed: [SUP-02, SUP-03, SUP-04, SUP-05, SUP-06, SUP-07]
completed: 2026-10-06
status: complete
---

# Phase 11 Plan 03: сторона пользователя в поддержке

**Статус: complete.** Без `SUPPORT_CHAT_ID` бот ведёт себя как до фазы (16 эталонов меню не тронуты, `/support` — неизвестная команда, кнопки `h:` — «Кнопка устарела», вложения не замечаются). С группой: вход кнопкой «💬 Поддержка» и `/support`, окно 10 минут с «Отмена», приём только текста до 2000 единиц UTF-16, отказы по длине, лимиту (5 в час) и для нетекстовых сообщений, отправка обращения одним сообщением бота в группу с шапкой, связка в базу, подтверждение. Ничего не закоммичено (коммиты делает владелец); production, VPS и `.env` не затрагивались; бот с настоящим токеном не запускался.

## Что сделано

- **Задача 1 (tracer).** `SNAPSHOT BASE: 1c0edcf` записан до первого изменения кода. Тесты написаны первыми (красные на компиляции: нет `Config.SupportChatID`). `config.go`: `SUPPORT_CHAT_ID` — пробелы обрезаются, пусто = 0, ноль, положительное число, нечисловое и дробное значение — ошибка «SUPPORT_CHAT_ID: неверное значение» без повтора значения. `docker-compose.yml`: `SUPPORT_CHAT_ID: ${SUPPORT_CHAT_ID:-}` только у `bot`; `.env.example` — закомментированная строка; `deploy/smoke.sh` — `-u SUPPORT_CHAT_ID`; `main.go` — булев `support` в строке `started`. `bot.go`: поле `supportChat`, команда `support` регистрируется только при ненулевом значении. `support.go`: приглашение, `/support`, `supportText` (порядок: выключено → отметка без записи → пустой текст или `/` → длина в UTF-16 → `SupportTake` → отправка), `supportSend` (`SendMessage` в `withRetry`, `SupportLink` после успеха, подтверждение), `supportFailed` (R-4, `MigrateError` через `errors.As`, отметка возвращается). `onText` вызывает `supportText` первой проверкой — до приведения к одной строке и до предела 64 символа. `contract_test.go`: третий контракт, пороги 17/8/10, сценарии Phase 11 без теста (S-32…S-34) исключены из ожидаемого множества до 11-05. Эталоны S-27 и `s31_failure` написаны вручную.
- **Задача 2.** Тесты кодека, S-26, S-29, S-35 написаны первыми (красные: `h:` не разбирается, меню из трёх кнопок, `/start` без четвёртой). `callback.go`: грамматика `h:<o|r|x>` без версии, `encode`; `bot.go`: обработчик `h:` через `b.cb(b.onSupport)` только при включённой поддержке, буква `h` в `cbPrefix`; `filters.go`: `mainMenu(support bool)`, `onStart`/`onMenu` передают `b.supportChat != 0`; `support.go`: `onSupport` (`h:o` правит сообщение, `h:r` присылает новое, `h:x` снимает отметку и правит на главное меню через `onMenu`), кнопка «Отмена» в `supportPrompt`. D-10 проверено в обе стороны на фикстуре городов с ботом с группой.
- **Задача 3.** Тесты S-28, S-30, S-31 написаны первыми; красным был только S-28 (нет ветки для сообщений без текста), S-30 и S-31 прошли сразу — лимит, длина и сбой отправки уже реализованы кодом задачи 1. `onDefault`: третья ветка `supportNonText` при включённой поддержке; отказ R-2 только при действующей отметке, содержимое сообщения нигде не читается.

## Проверки (реальные хвосты вывода)

| Проверка | Результат |
|---|---|
| ворота (`CONTRACT APPROVED:`, `OWNER Q-1: default`, нет `REPLAN`, `SCHEMA APPROVED:`) | выполнены |
| задача 1, команда `<verify>` | хвост: `--- PASS: TestHeartbeatFresh`, `PASS`, `ok  	aggregator/cmd/aggregator	0.596s`, затем `TRACER OK`; в логе `TRACER support=sent link=stored confirm=sent` |
| задача 2, команда `<verify>` | хвост: `PASS`, `ok  	aggregator/cmd/aggregator	0.607s`, затем `ENTRY OK` |
| задача 3, команда `<verify>` | хвост: `PASS`, `ok  	aggregator/cmd/aggregator	0.544s`, затем `PLAN 11-03 OK` |
| `go test -p 1 -count=1 -v ./...` | код возврата 0; `grep -cE -- '--- (FAIL\|SKIP)'` = **0**; все 15 пакетов `ok`; верхнего уровня **414** тестов `PASS` (было 407: +7 `TestS26…TestS35`), всего с подтестами **633** (было 626) |
| `gofmt -l .`, `go vet ./...` | пусто, чисто |
| `go.mod`, `go.sum`, `deploy/vps/`, миграции, `internal/store/`, `snapshot_test.go`, контракты Phase 8 и 9, `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md` | не изменены |
| код поддержки: `CopyMessage`, `ForwardMessage`, `SendPhoto`, `SendDocument`, `SendMediaGroup` | вне комментариев нет |
| compose (локально, `--env-file /dev/null`, без `.env`) | `config` собирается с пустым и с заданным `SUPPORT_CHAT_ID`; переменная есть только у `bot` |

## Эталоны

`git status --short internal/bot/testdata/screens` — только `??` (18 новых файлов), модифицированных и удалённых нет; в каталоге было 147 файлов, стало 165. Файлы написаны вручную по контракту; `s35_menu`, `s35_stale`, `s35_support_command` побайтно совпадают с прежними `s18_start_skipped`, `s14_stale`, `s05_command` (тест `TestS35SupportDisabled` дополнительно сверяет меню с `s18_start_skipped`).

Созданы: `s26_menu`, `s26_invite_button`, `s26_invite_command`, `s26_invite_reply`, `s27_group`, `s27_group_html`, `s27_group_nouser`, `s27_confirm`, `s28_refuse`, `s29_cancel`, `s29_start_menu`, `s29_expired`, `s30_too_long`, `s30_limit`, `s31_failure`, `s35_menu`, `s35_support_command`, `s35_stale`.

## Отличия контракта и плана

- План (задача 1, поведение 2) смешивает перевод строки и HTML-символы в одном тексте; контракт разделяет их (шаг 1 — `s27_group`, шаг 3 — `s27_group_html`, шаг 4 — `s27_group_nouser`). Сделано по контракту.
- Эталон отказа R-4 плана (задача 1) назван в контракте `s31_failure`; создан в задаче 1 под этим именем и используется и в S-27, и в S-31.
- Кнопки `h:o`, `h:r`, `h:x` плана 11-03 (S-35 шаг 7) дают одно и то же уведомление — эталон один, `s35_stale`, как в контракте.
- Шаг 8 сценария S-35 (сообщение из любой группы при выключенной поддержке — один `Info`-лог с идентификатором и типом чата, Q-7) здесь не реализован: он требует изменений `privateOnlyMW` и маршрутизации чатов, а это план 11-04. Сейчас `privateOnlyMW` по-прежнему отбрасывает групповые сообщения молча.
- Шаги 4 и 5 S-35 (поиск, список, карточка, профиль) проверены частично: профиль — окончание текста «🏅 Спорт: не указан», поиск — ответ на текст; список и карточка покрыты прежними тестами S-02 и S-09.
- Эталоны `s26_invite_button`, `s26_invite_reply`, `s29_cancel` содержат и вызов Bot API, и пустой `answerCallbackQuery` (в порядке вызовов), как прежние эталоны с `edited`.

## Deviations from Plan

1. **[Rule 2 — обработка ошибок]** Ошибка хранилища при чтении отметки (`supportText`) или в `SupportTake` не возвращает `false` (как `residenceText`), а отвечает `errNotice` и считает сообщение обработанным: иначе текст обращения молча ушёл бы в поиск. В `supportNonText` ошибка чтения — только запись в лог без ответа.
2. **[Rule 3 — блокирующее]** В `bot_test.go` добавлен `newBotWith(t, api, s, cfg)`; `newTestBot` вызывает его с прежним `Config{Token: "123:TEST"}` (бот без группы), `newSupportBot` — с `SupportChatID`.
3. Логика исключения в `TestContractCoverage`: сценарий третьего контракта без теста выпадает и из ожидаемых тестов, и из требования эталона, но остаётся «известным» для проверки лишних эталонов (иначе появившийся `s31_failure` в задаче 1 уронил бы прогон).
4. TDD по задачам: красные стадии — компиляция (задача 1), кодек/меню/`/start` (задача 2), нетекстовые сообщения (задача 3). `TestS30` и `TestS31` зелёные сразу — их логика в коде задачи 1 по плану.
5. `deploy/smoke.sh` не запускался (собирает образ и поднимает compose-проект); вместо этого проверена сборка `docker compose config` с пустым и заданным `SUPPORT_CHAT_ID`, а `-u SUPPORT_CHAT_ID` — командой `grep` задачи 1.

## Для плана 11-04

- Апдейты из группы по-прежнему отбрасывает `privateOnlyMW` (`isPrivate`). Маршрутизатор чатов должен стоять раньше него или внутри него: сообщение из группы `supportChat` — в обработчик ответов; из чужой группы — один `Info`-лог с идентификатором и типом чата (и при выключенной поддержке, Q-7; отсюда же шаг 8 S-35); callback в группе — прежний пустой `answerCallbackQuery`.
- `onDefault` теперь передаёт любое сообщение без текста в `supportNonText`. Пока групповые апдейты отсекает `privateOnlyMW`, ветка работает только для личных чатов; при пропуске групповых апдейтов дальше нужно развести по типу чата, иначе вложение в группе получит отказ R-2 пользователю.
- Для доставки ответа: `Store.SupportAuthor(ctx, chatID, messageID)`; кнопка «💬 Ответить» — `callback{Kind: cbSupportReply}` (`h:r`, `b.send(ctx, id, screen{plain: true, kb: keyboard(btn("💬 Ответить", ...))})`); `kindName(err)` — вид ошибки для лога; ответ уходит `withRetry`.
- Тестовая оснастка: `supportEnv`, `groupReplies` (идентификаторы 101, 102, …), `groupSends` (ответ на n-ю отправку в группу), `feed`/`split`/`named`/`mediaUpdate`, `supportState`, `setNow`; `fakeAPI` не умеет присылать апдейты с типом `supergroup` — `startUpdate` придётся копировать с другим `Chat`.
- 11-05: убрать исключение в `TestContractCoverage` (множество `phase11`) после появления тестов S-32…S-34.

## Known Stubs

Нет.

## Threat Flags

Нет новых поверхностей сверх `<threat_model>` плана. T-11-11…T-11-18 и T-11-41 закрыты тестами: `TestLoadConfig` (значения `SUPPORT_CHAT_ID`), шаг с HTML-символами `TestS27SupportText` (экранирование), `TestS30SupportRejects` (лимит), проверки таблиц и логов в `TestS27`, `TestS28`, `TestS31`, `TestCallbackReject` (грамматика `h:`), `TestS29` (порядок «поддержка, город, поиск»), `TestS28SupportNonText` (вложения не уходят в группу), конфигурация compose.

## Не проверено

- Поведение настоящего Telegram: разбор HTML-сущностей `&#34;`/`&#39;` и отображение `@username` в сообщении группы проверены только на фиктивном API — подтверждается на production-приёмке (11-06).
- `deploy/smoke.sh` целиком (см. отклонение 5).

## Self-Check: PASSED

Файлы `internal/bot/support.go`, `internal/bot/support_test.go`, 18 эталонов на месте; три команды `<verify>` (`TRACER OK`, `ENTRY OK`, `PLAN 11-03 OK`), полный прогон (0 упавших и пропущенных, 414/633), `gofmt`, `go vet` пройдены; `git status` по каталогу эталонов — только `??`; `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md` не менялись; ничего не закоммичено (`git rev-list --count 1c0edcf..HEAD` = 0).
