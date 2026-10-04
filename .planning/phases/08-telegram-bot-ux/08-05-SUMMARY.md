---
phase: 08-telegram-bot-ux
plan: 05
subsystem: bot
tags: [go, telegram, list, card, empty-state]
requires: [08-04]
provides:
  - "дата в строке списка с годом для событий не текущего года (P7)"
  - "единый возврат «⬅️ Назад» в списке, карточке, «событие недоступно» и пустой выдаче"
  - "тесты сценариев S-09…S-11 контракта"
affects: [08-06, 08-07]
plan_head_before: 6fcdada
actuals:
  tasks: 2
  commits: 0
tech-stack:
  added: []
  patterns: []
key-files:
  created: []
  modified:
    - internal/bot/list.go
    - internal/bot/filters.go
    - internal/bot/scenario_test.go
    - internal/bot/list_test.go
    - internal/bot/callback_test.go
    - internal/bot/tg_test.go
    - internal/bot/ux_test.go
    - internal/bot/testdata/screens/
key-decisions:
  - "Текущий год для формата даты — год «сегодня» по Москве (b.today()), как у фильтров дат"
requirements-completed: []
completed: 2026-10-04
duration: около 40 минут
status: complete
---

# Phase 8 Plan 05: список, карточка, пустые состояния

**Статус: complete (локально).** Реализованы сценарии S-09…S-11 утверждённого контракта. Запросы каталога не менялись (в выдаче по-прежнему только видимые и не слитые события); миграций нет. Telegram API не вызывался, VPS и production не затрагивались. Коммит делает владелец.

Выполнялся поверх незакоммиченных изменений 08-04 (`plan_head_before` — последний коммит владельца `6fcdada`).

## Что сделано

- **P7:** `listDate` — в строке списка `ДД.ММ` для события текущего года и `ДД.ММ.ГГГГ` для другого; `listScreen` получает год «сегодня» по Москве. Карточка не менялась.
- **Возврат:** «⬅️ К фильтрам» (список, пустая выдача) и «⬅️ К списку» (карточка, «Событие больше не доступно») заменены на `backLabel` («⬅️ Назад»); цели прежние — поиск и та же страница списка.
- **Тесты сценариев** `TestS09List`, `TestS10Card`, `TestS11Empty`.

Гарантии карточки Phase 4 (название, дата и время, место, дистанции, тип, описание, регистрация, источники, пометка статуса) не менялись — `TestCardStructure`, `TestCard` проходят.

## Эталоны

- Новые 13: `s09_page1`, `s09_page2`, `s09_last`, `s09_back`, `s10_card`, `s10_cancelled`, `s10_gone`, `s10_list_after_gone`, `s11_hints`, `s11_after_unset`, `s11_pick_country`, `s11_country_menu`, `s11_back`.
- Изменены вручную 12, отличия проверены построчно: подпись возврата — `card`, `card_cancelled`, `gone`, `empty_hints`, `empty_pick_country`, `list_page1`, `list_filters_changed`, `s15_card`, `s15_gone`, `s15_list`, `s15_no_user_list`; `list_last_page` — подпись возврата и три даты с годом 2027.
- Всего эталонов 71.

## Проверки

| Проверка | Результат |
|---|---|
| задача 1 | `TRACER OK` (`TRACER scenario=S-09 steps=4 match=yes`) |
| задача 2 | `PLAN 08-05 OK` |
| `TestS09List` | листание «→», «←», «стр.»; на последней странице даты `.2027`, на первой года нет; «Назад» → поиск |
| `TestS10Card` | карточка со 2-й страницы, «Назад» → 2-я страница; [ОТМЕНЁН]; скрытое событие → «Событие больше не доступно» |
| `TestS11Empty` | «Снять дату» → поиск, версия +1; «Выбрать страну» → меню стран; «Назад» → поиск |
| `gofmt -l .`, `go vet ./...` | чисто |
| полный подробный прогон | 496 PASS, 0 SKIP, 0 FAIL (было 493) |
| миграции, deploy, go.mod | без изменений |
| мутации (копия вне репозитория) | дата всегда без года; всегда с годом; «Назад» карточки на 1-ю страницу; старая подпись возврата; скрытое событие показывается; пустая выдача без возврата — все 6 ловятся |

## Deviations from Plan

1. `internal/bot/place.go` и `internal/bot/list_test.go` из `files_modified`: `place.go` не потребовался; дополнительно изменены `callback_test.go`, `tg_test.go`, `ux_test.go` (новая сигнатура `listScreen` и подпись возврата).
2. Прежний `TestListPaging` сверяет дату через `listDate`; буквальный формат проверяют снимки и `TestS09List`.
3. Коммиты делает владелец; исполнитель их не выполнял.
