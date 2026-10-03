---
phase: 08-telegram-bot-ux
plan: 01
subsystem: bot
tags: [go, telegram, snapshots, audit]
requires: []
provides:
  - "сверка экранов со снимками без автообновления (internal/bot/snapshot_test.go)"
  - "20 эталонов экранов b165c10 (internal/bot/testdata/screens)"
  - "08-AUDIT.md: карта экранов, сценарии, callback, проблемы P1–P10"
affects: [08-02, 08-03]
plan_head_before: 67919e2
actuals:
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "снимок вызова Bot API: метод, parse_mode, текст, клавиатура «[подпись] → callback_data»; эталоны только читаются тестами"
key-files:
  created:
    - internal/bot/snapshot_test.go
    - internal/bot/audit_test.go
    - internal/bot/testdata/screens/
    - .planning/phases/08-telegram-bot-ux/08-AUDIT.md
  modified: []
key-decisions:
  - "Эталоны созданы вручную из вывода упавших тестов после сверки с кодом b165c10; тесты файлов не пишут (D-03)"
  - "TestAuditUsersTouch фиксирует исходное поведение (P1, P2); заменяется в 08-03"
requirements-completed: []
completed: 2026-10-03
duration: около 1 часа
status: complete
---

# Phase 8 Plan 01: аудит интерфейса — снимки экранов и 08-AUDIT.md

**Статус: complete (локально).** Код бота не менялся (изменены только новые тестовые файлы и эталоны). Telegram API не вызывался, VPS и production не затрагивались. Коммит делает владелец.

## Что сделано

- `internal/bot/snapshot_test.go`: `renderCall` (метод, parse_mode, текст, клавиатура построчно; url-кнопки — `→ url:<адрес>`; ответ на callback — текст и show_alert), `matchSnapshot`/`matchSnapshotIn` — точное сравнение, при расхождении или отсутствии эталона тест падает и печатает полученный снимок; записи файлов и флага обновления нет. `TestSnapshotSelf` — изменение в один символ и отсутствующий эталон обнаруживаются.
- `internal/bot/audit_test.go`: `TestAuditStart` (tracer), `TestAuditFilterScreens`, `TestAuditListAndCard`, `TestAuditEmptyScreens`, `TestAuditStaleCallback`, `TestAuditUsersTouch`.
- 20 эталонов: start, summary_default, summary_filtered, date_menu, city_menu, bucket_menu, type_menu, country_menu, city_text_found, city_text_not_found, city_text_command, list_page1, list_last_page, card, card_cancelled, gone, empty_hints, empty_pick_country, list_filters_changed, stale_callback.
- `08-AUDIT.md`: карта экранов, сценарии, таблица callback (что меняет `filters`/`filters_v`/`last_seen_at`), проблемы P1–P10 со ссылками на решения, вне объёма.

## Главные находки (для контракта 08-02)

- P1: старая кнопка `f:` меняет текущие фильтры (версии нет).
- P2: любой разборчивый callback `f:`/`p:`/`e:` обновляет `users.last_seen_at`; неизвестный и неразборчивый — нет.
- P3: при panic обработчика ответ на callback без текста.
- P4–P6: нет «Мои настройки», сброс в сводке без подтверждения, главное меню из одной кнопки, разнородные возвраты.
- P7: дата в списке без года — события следующего года на последней странице выглядят прошедшими.
- P8: «Моск» не подсказывает Москву (ключ-синоним `moscow` против префикса `mosk`).

## Проверки

| Проверка | Результат |
|---|---|
| задача 1 | `TRACER OK` (`TRACER snapshot=start match=yes`), `TestSnapshotSelf` PASS |
| задача 2 | `SNAPSHOTS OK` — 20 эталонов, пакет `internal/bot` без FAIL/SKIP, `TestAuditUsersTouch` PASS; повторный прогон `-count=2` стабилен |
| задача 3 | `AUDIT OK` |
| `gofmt -l .`, `go vet ./...` | чисто |
| полный подробный прогон | 477 PASS, 0 SKIP, 0 FAIL (было 470: +7 тестов) |
| не-тестовый код, миграции, deploy, go.mod | без изменений (`git status` по pathspec пуст) |
| мутации (копия вне репозитория) | заголовок списка, текст подменю типа, текст устаревшей кнопки, строка карточки — каждый раз тесты аудита падают |

## Deviations from Plan

1. Кнопки-ссылки в снимке — `→ url:<адрес>` (в плане `→ url`): адрес из фикстуры нужен, чтобы ловить подмену ссылки.
2. `TestSnapshotSelf` сверяет с настоящим эталоном `start` и его копией в памяти, а не с файлом во временном каталоге: проверка плана запрещает `os.WriteFile` в `snapshot_test.go`.
3. Эталоны перенесены из вывода упавших тестов одноразовым разбором лога в каталоге вне репозитория после построчного просмотра; сами тесты файлов не пишут.
4. Коммиты делает владелец; исполнитель их не выполнял.
