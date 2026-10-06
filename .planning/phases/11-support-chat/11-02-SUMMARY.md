---
phase: 11-support-chat
plan: 02
subsystem: store
tags: [postgres, migration, support, store]
requires: [11-01]
provides:
  - "migrations/00007_support.sql: таблицы `support_messages` и `support_awaiting` (утверждённая схема), индекс `support_messages_user_created`, секции goose Up и Down"
  - "internal/store/support.go: `SupportOutcome` (`SupportTaken`, `SupportNotWaiting`, `SupportLimited`), `SupportBegin`, `SupportCancel`, `SupportAwaiting`, `SupportTake`, `SupportLink`, `SupportAuthor`"
  - "internal/store/profiles.go: `StartUser` и `UpdateProfileAt` снимают отметку поддержки (D-10); служебный `dropSupportAwaiting`"
  - "internal/store/support_test.go: TestSupportFlow, TestSupportColumns, TestSupportLimit, TestSupportExpired, TestSupportRace, TestSupportExclusive, TestSupportNoUsersWrite, TestSupportChecks"
affects: [11-03, 11-04, 11-05, 11-06]
plan_head_before: 43029a2
actuals:
  tokens: 6000
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "отметка потребляется в той же транзакции, что и проверка срока и лимита: `SELECT ... FOR UPDATE`, затем `DELETE`; второй из двух одновременных вызовов находит строку удалённой"
    - "лимит — число связок автора за окно (`created_at > since`); отдельного счётчика нет"
    - "порядок блокировок везде один: профиль, затем отметка поддержки"
key-files:
  created:
    - migrations/00007_support.sql
    - internal/store/support.go
    - internal/store/support_test.go
  modified:
    - internal/store/profiles.go
    - internal/store/storetest/storetest.go
    - internal/store/store_test.go
key-decisions:
  - "Схема таблиц поддержки утверждена владельцем 2026-10-06 и перенесена в миграцию без изменений (сверено побайтно)"
  - "Отметка поддержки — отдельная таблица без внешнего ключа на `users`: методы поддержки не пишут в `users` и не создают строку профиля"
  - "Сколько обращений допустимо и какого размера окно — параметры `SupportTake` (`since`, `limit`), хранилище значений не знает"
requirements-completed: [SUP-04, SUP-06, SUP-11]
completed: 2026-10-06
status: complete
---

# Phase 11 Plan 02: модель данных поддержки

**Статус: complete.** Миграция `00007_support.sql` и слой `internal/store/support.go` готовы и проверены на локальной тестовой PostgreSQL; `StartUser` и `UpdateProfileAt` снимают отметку поддержки. Ничего не закоммичено — коммит делает владелец. Production и VPS не затрагивались; миграция применена только к локальной тестовой базе самим прогоном тестов.

## Ответ владельца (задача 1)

Ворота: `CONTRACT APPROVED: 2026-10-06`, `OWNER Q-6: default` в `11-01-SUMMARY.md`; строки `REPLAN` нет.

SCHEMA APPROVED: 2026-10-06
«утверждаю схему»

Правок владелец не назвал. Утверждённый текст миграции `migrations/00007_support.sql` (переносится в файл дословно):

```sql
-- +goose Up
-- Связка «сообщение бота в группе поддержки → автор обращения» (D-13
-- Phase 11): по ней ответ из группы доставляется пользователю и считается
-- лимит обращений (D-12, D-14). Текста обращения и ответа в таблице нет.
-- chat_id входит в ключ: номер сообщения уникален только внутри чата, а
-- идентификатор группы может смениться.
CREATE TABLE support_messages (
    chat_id    BIGINT NOT NULL CHECK (chat_id < 0),      -- группа на момент отправки
    message_id INTEGER NOT NULL CHECK (message_id > 0),  -- сообщение бота в этой группе
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),        -- автор обращения
    created_at TIMESTAMPTZ NOT NULL,                     -- время отправки по часам бота
    PRIMARY KEY (chat_id, message_id)
);
CREATE INDEX support_messages_user_created ON support_messages (tg_id, created_at);

-- Отметка «жду обращение» (D-08…D-10 Phase 11): до expires_at следующее
-- сообщение пользователя считается обращением в поддержку. Отдельная
-- таблица, а не колонка user_profiles: поддержка доступна и без строки
-- профиля, а откат образа не примет текст обращения за город проживания.
CREATE TABLE support_awaiting (
    tg_id      BIGINT PRIMARY KEY CHECK (tg_id > 0),
    expires_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE support_awaiting;
DROP TABLE support_messages;
```

Отличия от формулировки D-13 («только связка и время»), показанные владельцу и утверждённые вместе со схемой: вторая таблица `support_awaiting` (Telegram ID и время, без текста); идентификатор группы в ключе связки; индекс `support_messages_user_created` для лимита; три ограничения `CHECK`. Владельцу также показано: внешних ключей на `users` нет; связки хранятся бессрочно (Q-6) и попадают в резервные копии.

## Что сделано

- Задача 1 (ворота и утверждение схемы): выполнена оркестратором, ответ владельца — выше.
- Задача 2 (tracer): тесты `TestSupportFlow` и `TestSupportColumns` написаны первыми и падали на компиляции (методов не было). Затем: `migrations/00007_support.sql` — дословно утверждённый SQL (`diff` с блоком выше пуст); `support.go` целиком (`SupportBegin` — один `InTx`: снятие отметки города без изменения `profile_v` и `updated_at`, затем вставка или замена отметки; `SupportTake` — `FOR UPDATE`, удаление отметки, срок, лимит); списки `TRUNCATE` в `storetest.go` и `store_test.go` дополнены `support_messages`, `support_awaiting`. В логе `TRACER support=taken link=stored author=found`.
- Задача 3 (тесты сначала): шесть тестов написаны, красным был только `TestSupportExclusive` (`StartUser (действующий) не снял отметку поддержки`) — остальное уже обеспечивал `support.go` из задачи 2. Затем в `profiles.go`: `StartUser` удаляет отметку поддержки в обеих ветках (строка профиля создана / уже была); `UpdateProfileAt` удаляет её, если итоговый профиль несёт отметку города, — до проверки «ничего не изменилось», то есть и без записи профиля; при `ErrStaleProfile` и ошибке `fn` ничего не удаляется (возврат до удаления / откат). Окно между проверкой лимита и `SupportLink` помечено комментарием `Упрощение:` в `SupportTake`.

## Экспортируемое API хранилища

- `type SupportOutcome int` с константами `SupportTaken` (отметка потреблена, лимит не исчерпан), `SupportNotWaiting` (отметки нет или истекла), `SupportLimited` (отметка потреблена, лимит исчерпан).
- `(*Store).SupportBegin(ctx, tgID int64, expiresAt time.Time) error` — поставить или продлить отметку «жду обращение»; снять отметку города без изменения `profile_v` (D-10).
- `(*Store).SupportCancel(ctx, tgID int64) error` — снять отметку; отсутствие не ошибка.
- `(*Store).SupportAwaiting(ctx, tgID int64) (expiresAt time.Time, ok bool, err error)` — прочитать отметку без записи (истёкшую тоже отдаёт; решает `SupportTake`).
- `(*Store).SupportTake(ctx, tgID int64, now, since time.Time, limit int) (SupportOutcome, error)` — под блокировкой строки проверить срок, посчитать связки автора за окно и удалить отметку, всё в одной транзакции.
- `(*Store).SupportLink(ctx, chatID int64, messageID int, tgID int64, at time.Time) error` — записать связку; повтор той же пары игнорируется.
- `(*Store).SupportAuthor(ctx, chatID int64, messageID int) (tgID int64, ok bool, err error)` — автор по сообщению бота в группе; другая группа или неизвестное сообщение — `ok = false`.

## Проверки

| Проверка | Результат |
|---|---|
| ворота и схема | `SCHEMA APPROVED: 2026-10-06` в этом файле; миграция совпадает с блоком выше (`diff` пуст) |
| задача 2, команда `<verify>` | хвост вывода: `--- PASS: TestDupesWouldAuto`, `PASS`, `ok  	aggregator/internal/bot	18.575s`, затем `TRACER OK` |
| задача 3, команда `<verify>` | хвост вывода: `--- PASS: TestDupesWouldAuto`, `PASS`, `ok  	aggregator/internal/bot	18.988s`, затем `PLAN 11-02 OK` |
| полный прогон `go test -p 1 -count=1 -v ./...` | код возврата 0; `grep -cE -- '--- (FAIL\|SKIP)'` = 0; верхнего уровня 407 тестов `PASS` (до плана 399), всего с подтестами 626 (до плана 618): +8 тестов `TestSupport*`; все 15 пакетов `ok` (`internal/store` 2.530s, `internal/bot` 19.079s) |
| `gofmt -l .`, `go vet ./...` | чисто |
| `users.go`, `go.mod`, `go.sum`, `deploy/`, `docker-compose.yml`, `internal/bot/`, миграции 00001–00006 | не изменены (`git status --porcelain` пуст) |
| тесты Phase 9 (`TestProfileStart`, `TestProfileUpdateAt`, `TestProfileStaleNoWrite`, `TestProfileRace`, `TestUsersColumns`, `TestUserProfilesColumns`) и `TestS25CityText` | проходят без правки своего кода |

## Deviations from Plan

1. Коммиты не выполнялись (решение владельца: коммиты делает он); `actuals.commits: 0`, `plan_head_before` — HEAD на момент выполнения, изменения лежат в рабочем дереве.
2. Служебный `dropSupportAwaiting` (одно `DELETE` с обёрткой ошибки) размещён в `profiles.go`, а не в `support.go`: его зовут `StartUser` и `UpdateProfileAt`, а команда проверки задачи 3 требует упоминания `support_awaiting` в `profiles.go`. `SupportCancel` использует тот же метод.
3. `SupportBegin` сначала снимает отметку города (только если она стоит), затем пишет отметку поддержки: порядок блокировок «профиль, затем поддержка» совпадает с `StartUser` и `UpdateProfileAt`, поэтому взаимной блокировки нет. Сверх плана в `TestSupportExclusive` добавлен случай «профиль уже несёт отметку города и не меняется — отметка поддержки всё равно снимается»; в `TestSupportChecks` — положительный идентификатор группы и отрицательный автор.
4. `TestSupportRace` повторяет гонку 10 раз подряд (один заход мог бы пройти и без блокировки).

## Known Stubs

Нет.

## Threat Flags

Нет новых поверхностей сверх `<threat_model>` плана: запросы параметризованы, ограничения `CHECK` проверены `TestSupportChecks`, колонки таблиц — `TestSupportColumns`, текст обращения и ответа в хранилище не передаётся, в текстах ошибок только имя операции и ошибка базы.

## Следующий шаг

План 11-03: бот использует `SupportBegin`, `SupportAwaiting`, `SupportTake`, `SupportLink`, `SupportAuthor`, передавая лимит и окно параметрами. Миграция попадёт на production только в 11-06 по отдельному разрешению владельца.

## Self-Check: PASSED

Файлы `migrations/00007_support.sql`, `internal/store/support.go`, `internal/store/support_test.go` на месте; обе проверки (`TRACER OK`, `PLAN 11-02 OK`) и полный прогон пройдены; `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md` не менялись; ничего не закоммичено.
