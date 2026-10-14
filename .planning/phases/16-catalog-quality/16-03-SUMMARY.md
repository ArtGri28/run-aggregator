---
phase: 16-catalog-quality
plan: 03
status: done-local
requirements-completed: []
---

# 16-03: «Предложить старт»

Статус: код, тесты и эталоны готовы локально. Основа — `16-UX-CONTRACT.md` (разделы 1–4, E-1, P-1…P-5, G-2, S-55, 9, 11, 13; Q-7 = отдельная отметка `suggest_awaiting` на 10 минут, режимы ввода взаимоисключающие). Хранилище — план 16-01 без изменений (`SuggestBegin`, `SuggestCancel`, `SuggestAwaiting`, `SuggestTake`, `SuggestRelease`; взаимное снятие отметок — в `SuggestBegin`, `SupportBegin`, `StartUser`, `UpdateProfileAt`).

## Изменения

| Файл | Что |
|---|---|
| `internal/bot/suggest.go` | новый: `suggestRow` (строка E-1, только при группе или администраторах), P-1 (`suggestPrompt`), `onSuggest` (q:s — отметка и P-1 новым сообщением; q:c — снять отметку и главное меню, как `h:x`), `/suggest` (`onSuggestCmd`), приём ссылки `suggestText` (P-2…P-5), отказ на сообщение без текста `suggestNonText` (P-3); G-2 уходит через `deliver` из 16-02 |
| `internal/bot/callback.go` | `cbSuggest` (`q:s`), `cbSuggestCancel` (`q:c`): разбор и сборка |
| `internal/bot/reports.go` | `onReport` передаёт q:s и q:c в `onSuggest` |
| `internal/bot/bot.go` | `/suggest` регистрируется вместе с `q:` только при `reportsOn`; `onDefault`: сообщение без текста проверяется в режиме поддержки (при группе) и в режиме ссылки |
| `internal/bot/filters.go` | `onText`: порядок — поддержка, ссылка, затем предел 64 символа, город, поиск; `emptyScreen` — параметр `suggest row` перед «Назад» |
| `internal/bot/search.go` | `searchScreen` — параметр `suggest row` на экране «ничего не нашлось» перед «Назад» |
| `internal/bot/list.go` | `emptyScreen(…, b.suggestRow())` |
| `internal/bot/suggest_test.go` | новый: `TestS55Suggest` (шаги 1–9 и без получателей) |
| `internal/bot/contract_test.go` | `pendingScenarios` — только S-56 |
| `internal/bot/filters_test.go`, `ux_test.go`, `ux3_test.go`, `callback_test.go` | ожидаемые кнопки пустых экранов — с `q:s`; вызовы `emptyScreen` с новым параметром |

## Приём ссылки

Текст без пробелов по краям; начинается с «/» — не ссылка, отметка не трогается (команды как раньше). Формат — `event.ValidURL` и не длиннее 500 символов (руны); отказ P-3 до записи — отметка и окно прежние. Затем `SuggestTake` (потребление отметки, предел 3 за скользящие 24 часа, строка до отправки) → `deliver("suggest", …)`; ни одно сообщение не дошло — `SuggestRelease` (строка удалена, отметка на 10 минут от текущего момента) и P-5. Ссылка экранируется в G-2; в лог — только `outcome` (refused, limited, sent) и вид ошибки Bot API.

## Эталоны

Изменены вручную (Edit по строке «⬅️ Назад»), по одной добавленной строке `[💡 Предложить старт] → q:s`, удалённых нет — 9 эталонов группы П раздела 9: `empty_hints`, `empty_pick_country`, `s05_none`, `s11_hints`, `s11_pick_country`, `s25_expired`, `s29_expired`, `s40_empty_country`, `s40_empty_hints` (`git diff --numstat` — у всех `1 0`).

Новые (S-55), написаны по контракту: `s55_invite_button`, `s55_invite_command`, `s55_refuse`, `s55_sent`, `s55_limit`, `s55_admins`, `s55_failure` — 7 файлов.

Сумма эталонов (`cat internal/bot/testdata/screens/*.txt | shasum -a 256`):
- до: `93907c37274efe27d57b01b51a793afe6407b88b74d24a8a883b8628a17cb178`
- после: `68bf71a24f66b6513090bf34f33ddd2c84d68fed5885bfd7f69cd224b1233d84`

## Проверки

- `go vet ./...` — без замечаний; `gofmt -l .` — пусто.
- `TEST_DATABASE_URL='postgres://aggregator@127.0.0.1:5433/aggregator_test?sslmode=disable' go test -p 1 -count=1 -v ./... > /tmp/t1603.log`: **522 PASS, 0 FAIL, 0 SKIP** (было 521; новый — `TestS55Suggest`); `TestContractCoverage` — PASS.
- `git status`: изменены 9 эталонов группы П и добавлены 7 эталонов `s55_*`; других эталонов нет.

## Отступления

- **Каталог S-55 — как у `empty_hints` (`countryFixture`), а не каталог Phase 14.** Шаг 1 сверяется с `empty_hints`, а этот эталон строится на четырёх событиях `countryFixture`; остальным шагам каталог не важен. Бот сценария — с группой и администратором 1.
- **Четыре прежних теста** (`TestEmptyResult`, `TestTextSearch`, `TestEmptyScreenHints`, `TestEmptyPickCountry`) сверяли полный список кнопок пустых экранов; у тестового бота есть администратор 1 (16-02), поэтому `q:s` добавлен в ожидаемые списки — как строка в 9 эталонах.
- **Текст из одних пробелов в режиме ссылки** — P-3 (не ссылка), а не «Нажмите /start»: окно отметки действует.
- **Все проверки плана — в одном `TestS55Suggest`** (формат, длина 500/501, предел, истёкшее окно, отмена и старое приглашение, переключение поддержка ↔ ссылка, `/start`, без группы — администраторам, сбой доставки, ссылка не в базе — поиск по `to_jsonb` всех таблиц, ссылка не в логе, без получателей — нет строки, `/suggest` неизвестна, `q:s` устарела): проверка покрытия допускает один тест на сценарий.
- Ответы P-2…P-5 отправляются `supportSay` (обычный текст без клавиатуры) — общий помощник ответов поддержки.

## «Упрощение:» в коде

Новых нет.
