---
phase: 16-catalog-quality
plan: 02
status: done-local
requirements-completed: []
---

# 16-02: «Сообщить об ошибке» в боте

Статус: код, тесты и эталоны готовы локально. Основа — `16-UX-CONTRACT.md` (разделы 1–4, C-1, R-1…R-8, G-1, S-54, 9, 11, 13; Q-1 = 10 за 24 ч, Q-3 = причина отдельным сообщением, Q-4 = очистка 90 дней). Хранилище — план 16-01 без изменений (`AddReport`, `DeleteReport`, `PruneReports`).

## Изменения

| Файл | Что |
|---|---|
| `internal/bot/reports.go` | новый: справочник причин (буква → код → слово), `onReport` (q:o, q:r, q:x), проверка доступности и даты старта до хранилища (`reportEvent`), экраны R-1…R-7, уведомление R-8, сообщение G-1 (`groupReport`), доставка `deliver` — в группу либо каждому администратору по порядку `ADMIN_TG_IDS`, повтор при 429 (`withRetry`), вид ошибки в лог; сбой доставки — `DeleteReport` |
| `internal/bot/callback.go` | префикс `q`: `cbReportOpen`, `cbReport`, `cbReportCancel`; разбор и сборка по грамматике раздела 4 |
| `internal/bot/bot.go` | `adminIDs` (порядок `ADMIN_TG_IDS`, без повторов); обработчик `q:` регистрируется только при группе или администраторах (`reportsOn`); `cbPrefix` знает `q` |
| `internal/bot/list.go` | `buildCard`: строка «📝 Сообщить об ошибке» после строки избранного, перед «Назад» (из списка, запроса, раздела, `w:n`); `sourcesLine` вынесена из `cardScreen` (общая с G-1) |
| `internal/bot/notify.go` | `Notifier.chores`: шаг `reports` — `PruneReports(now − 90 дней)`; счётчик в строке лога `notify chores` |
| `internal/bot/reports_test.go` | новый: `TestS54Report` (шаги 1–13), `TestReportPartialDelivery`, `TestReportPrune` |
| `internal/bot/contract_test.go` | шестой контракт (Phase 16, порог 3); `pendingScenarios` — S-55 и S-56 не требуются, пока у них нет теста |
| `internal/bot/bot_test.go` | `newTestBot` — с администратором 1 (`Admins: []int64{1}`): иначе у тестового бота нет получателей заявок и строки в карточке нет |
| `internal/bot/fav_test.go` | `TestFavTracer`, `TestS46FavVisibility`, `TestFavMessageKeyboard`: ожидаемые клавиатуры карточки — со строкой заявки |
| `internal/bot/notify_test.go` | `TestS52WatchFuse`, `TestNotifyPrivacy`: администратор 1 тестового бота снимается (у тестов свои администраторы предохранителя); ключ лога `reports` — в списке разрешённых |

## Эталоны

Изменены вручную (Edit по строке «⬅️ Назад»), по одной добавленной строке, удалённых нет — 23 эталона группы К раздела 9: `card`, `card_cancelled`, `s10_card`, `s10_cancelled`, `s15_card`, `s40_card`, `s40_card_all`, `s40_card_tbd`, `s40_card_roller`, `s42_card`, `s44_card`, `s44_card_cancelled`, `s44_search_card`, `s45_card`, `s45_card_link`, `s47_card_not_fav`, `s48_open`, `s44_added`, `s44_already`, `s44_no_user`, `s44_removed`, `s44_search_added`, `s45_card_removed` (`git diff --numstat` — у всех `1 0`). Группа П (9 пустых экранов) — план 16-03, не тронута.

Новые (S-54), написаны по контракту: `s54_reasons`, `s54_sent`, `s54_repeat`, `s54_cancel`, `s54_limit`, `s54_open_gone`, `s54_gone`, `s54_past`, `s54_merged`, `s54_admins`, `s54_failure`, `s54_card_off` — 12 файлов.

Сумма эталонов (`cat internal/bot/testdata/screens/*.txt | shasum -a 256`):
- до: `603a47894c3ba97cb8ac4aff01076bb8ee5ea42b7d5a4131efc3a33b42091fd9`
- после: `93907c37274efe27d57b01b51a793afe6407b88b74d24a8a883b8628a17cb178`

## Проверки

- `go vet ./...` — без замечаний; `gofmt -l .` — пусто.
- `TEST_DATABASE_URL='postgres://aggregator@127.0.0.1:5433/aggregator_test?sslmode=disable' go test -p 1 -count=1 -v ./... > /tmp/t1602.log`: **521 PASS, 0 FAIL, 0 SKIP** (было 518; новые — `TestS54Report`, `TestReportPartialDelivery`, `TestReportPrune`); `TestContractCoverage` — PASS с контрактом Phase 16.
- `git status`: изменены 23 эталона таблицы раздела 9 (группа К) и добавлены 12 эталонов `s54_*`; других эталонов нет.

## Отступления

- **Администратор 1 у `newTestBot`.** Контракт требует строку в 23 эталонах, а прежний тестовый бот без группы и администраторов строку бы не показал (D-07). Группа меняла бы главное меню (поддержка), поэтому выбран администратор 1 — тот же, что в тестах админских команд. Два теста предохранителя снимают его, чтобы перечень получателей был прежним.
- **R-5 — обычный текст** (без `parse_mode`), R-6 и R-7 — HTML: буквально по обозначениям контракта («HTML, если не сказано „обычный текст“»; у R-5 сказано). Текст тот же; владелец может выбрать HTML — меняется `s54_gone`.
- **`q:o`/`q:r` выключенного вида спорта** — как недоступное событие (R-5), по разделу 4.
- **`TestContractCoverage`**: вместо порогов «на будущее» — `pendingScenarios` (S-55, S-56); проверка прежних пяти контрактов не изменена. В 16-03/16-04 номер убирается из перечня вместе с тестом.
- Шаги 10–11 S-54 (администраторы, отказ, без получателей) — в том же `TestS54Report`: проверка покрытия допускает один тест на сценарий.
- Ответ администратора в личке на G-1 (без группы) — обычный текст для бота (поиск), как любой текст; контракт описывает только ответ в группе (шаг 12 — ни одного вызова).

## «Упрощение:» в коде

Новых нет. Действует «Упрощение:» 16-01 в `AddReport` (перебор предела на одну заявку при одновременных нажатиях).
