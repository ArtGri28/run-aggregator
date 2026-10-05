SNAPSHOT BASE: 5771770

---
phase: 09-onboarding-user-profile
plan: 03
subsystem: bot
tags: [telegram, onboarding, profile, callback, snapshots]
requires: [09-01, 09-02]
provides:
  - "internal/bot/profile.go: `offerScreen`, `questionScreen`, `onProfile` (кнопки `u:`), справочники D-04 используются из `callback.go`"
  - "internal/bot/callback.go: грамматика `u:` (`parseProfile`), виды `cbProfile`…`cbProfDone`, поля `Field`/`Mode`, справочники `profileGenders/Ages/Countries/Sports`, `maxProfileCityKeyBytes = 49`"
  - "internal/bot/filters.go: `onStart` через `Store.StartUser` (предложение O-1 один раз)"
  - "internal/bot/profile_test.go: TestS18Offer, TestS19SkipAll, TestS20Wizard, TestS21SkipQuestion, TestS22StaleProfile"
affects: [09-04, 09-05, 09-06]
plan_head_before: 5771770
actuals:
  tokens: 17000
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "кнопки `u:` получают версию `profile_v` при построении экрана (`V: pv`), `stamp` для них не используется"
    - "изменяющая кнопка и навигационная проходят через `UpdateProfileAt`; допустимость статуса проверяется внутри функции изменения под блокировкой строки; устаревшая навигационная читает `PeekProfile` без записи"
key-files:
  created:
    - internal/bot/profile.go
    - internal/bot/profile_test.go
    - internal/bot/testdata/screens/ (новые эталоны s18_* … s22_*, 32 файла)
  modified:
    - internal/bot/callback.go
    - internal/bot/callback_test.go
    - internal/bot/bot.go
    - internal/bot/filters.go
    - internal/bot/country.go
    - internal/bot/contract_test.go
    - internal/bot/stale_test.go
    - internal/bot/scenario_test.go
    - internal/bot/ux_test.go
    - internal/bot/country_ui_test.go
    - internal/bot/bot_test.go
    - internal/bot/settings_test.go
    - internal/bot/testdata/screens/start.txt
    - internal/bot/testdata/screens/s01_start.txt
key-decisions:
  - "Главное меню в этом плане остаётся с двумя кнопками: третья кнопка «👤 Мой профиль» и правка пяти эталонов меню Phase 8 — в 09-04 (так сказано в запретах исполнения и в плане 09-04)"
  - "Кнопки `u:` режима `e` и `u:o` на экранах не появляются; изменяющие из них и `u:o` отвечают «Кнопка устарела» без записи; навигационная `u:a` любого режима показывает экран (этого требует S-22 контракта)"
  - "Режим `w` показывается только при статусе `in_progress`, иначе экран поля в режиме `e` (дополнение В контракта) — для устаревшей и для актуальной навигационной кнопки одинаково"
requirements-completed: []
completed: 2026-10-05
status: complete
---

# Phase 9 Plan 03: анкета в боте

**Статус: complete.** Новый пользователь на первом `/start` получает предложение анкеты (O-1), может пройти четыре вопроса или пропустить анкету и любой вопрос; устаревшие, недопустимые и поддельные кнопки `u:` ничего не пишут. Ничего не закоммичено (коммиты делает владелец); production, VPS и бот с боевым токеном не затрагивались.

## Что сделано

- **Задача 1 (tracer).** `SNAPSHOT BASE: 5771770` записан до первого изменения кода. Грамматика `u:` в `callback.go` (`parseProfile`, `encode`, справочники D-04, ключ города не длиннее 49 байт), регистрация `b.cb(b.onProfile)` по префиксу `u:`, буква `u` в `cbPrefix`; `onStart` вызывает `Store.StartUser` и по `offered` отправляет `offerScreen` или главное меню (тексты логов прежние, при сорвавшейся отправке ничего не откатывается); «Пропустить» — `UpdateProfileAt` (только из `not_started`) и главное меню правкой через `onMenu`. `TestContractCoverage` читает два контракта. Эталоны `start.txt` и `s01_start.txt` — содержимое O-1. Тесты Phase 8 по разделу 9 контракта переведены на второй `/start`.
- **Задача 2.** `questionScreen` (O-2…O-5 в обоих режимах, чистая функция), `onProfile`: «Заполнить анкету», сохранение пола, возраста, страны (смена страны очищает город, ставится отметка ожидания на 10 минут), города кнопкой (название из каталога через `CityName`), переключение вида спорта (порядок D-04), «✅ Готово» с уведомлением «Спасибо! Анкета сохранена»; «Пропустить вопрос» и «⬅️ Назад» без записи. `countryNames["KZ"] = "Казахстан"`.
- **Задача 3.** `dbState` включает `user_profiles`; `TestS22StaleProfile` по таблице случаев (чужая версия, нет строки профиля и `users`, недопустимый для статуса переход, город вне каталога и без страны, строки вне грамматики, отметка ожидания не снимается устаревшими `u:` и другими кнопками, актуальная кнопка применяется, внутренняя ошибка при закрытом пуле). Сорвавшаяся отправка O-1 (D-06) — отдельный шаг `TestS18Offer`.

## Проверки (реальные хвосты вывода)

| Проверка | Результат |
|---|---|
| задача 1, команда `<verify>` | хвост: `--- PASS: TestDupesWouldAuto`, `PASS`, `ok  	aggregator/internal/bot	13.793s`, затем `TRACER OK` |
| задача 2, команда `<verify>` | хвост: `PASS`, `ok  	aggregator/internal/bot	13.902s`, затем `WIZARD OK` |
| задача 3, команда `<verify>` | проверки остальной части команды те же, что в задаче 2 (прошли); `grep -q "'user_profiles'" stale_test.go` и список из 11 тестов — `PLAN 09-03 OK` (`ok  	aggregator/internal/bot	14.223s`) |
| полный прогон `go test -p 1 -count=1 -v ./...` | `rc=0`, `grep -cE -- '--- (FAIL|SKIP)'` = 0, все пакеты `ok` (`internal/bot` 14.340s, `internal/store` 2.267s) |
| `gofmt -l internal cmd` | пусто |
| `go vet ./...` | чисто |
| мутации (проверка чувствительности) | `awaitCityFor` = 9 минут — `TestS20Wizard` падает; убрана проверка статуса у `u:z` — `TestS22StaleProfile` падает; код возвращён |

Охранные проверки команд (`git status --porcelain` по `go.mod`, `go.sum`, `deploy`, `docker-compose.yml`, контракту Phase 8, прежним миграциям; запрет записи файлов в `snapshot_test.go`; запрет обращения `profile.go` к фильтрам) проходят. Файлы плана 09-02 за время работы были закоммичены владельцем — на проверки это не повлияло.

## Эталоны

Все эталоны написаны вручную из текста утверждённого контракта (разделы 4, 5, 8, 9), без переключателей автообновления и без записи из теста.

- **Изменены (из Phase 8 — только они):** `start.txt`, `s01_start.txt` (содержимое O-1). Остальные эталоны Phase 8 не менялись (`git status` по `testdata/screens` это подтверждает).
- **Созданы (32):** `s18_offer_new`, `s18_offer_existing`, `s18_start_not_started`, `s18_start_in_progress`, `s18_start_completed`, `s18_start_skipped`, `s18_start_after_failed_offer`; `s19_skip`, `s19_search`, `s19_skip_again`; `s20_q1`, `s20_q2`, `s20_q3_country`, `s20_q3_city`, `s20_q4`, `s20_q4_two`, `s20_done`; `s21_skip_q1`, `s21_back_to_q1`, `s21_back_to_menu`, `s21_skip_city`, `s21_q4_back`, `s21_interrupt`, `s21_all_skipped`; `s22_stale_save`, `s22_stale_nav`, `s22_stale_city_nav`, `s22_no_row_nav`, `s22_no_row_save`, `s22_bad_transition`, `s22_unknown`, `s22_city_not_in_catalog`.
- **Не созданы:** `s22_no_row_profile` (`u:o` без строки профиля показывает P-1 — это 09-04).
- **Для 09-04:** эталоны, в которых показано главное меню (`s18_start_*`, `s19_skip`, `s20_done`, `s21_back_to_menu`, `s21_interrupt`, `s21_all_skipped`), сейчас с двумя кнопками, как текущее `mainMenu()`; когда 09-04 добавит третью кнопку, строку `[👤 Мой профиль] → u:o` надо дописать вручную и в них (по контракту E-01 их три).

## Отличия контракта и плана

1. Контракт S-20 шаг 6: виды спорта «Бег» и «Велоспорт»; в `<behavior>` плана — «Бег» и «Плавание». Выполнено по контракту.
2. Контракт S-22 шаги 2–4: устаревшая и «нет строки» навигационная `u:a` с режимом `e` показывает экран поля; план (задача 3) относит «кнопки режима `e`» к отвечающим «Кнопка устарела». Выполнено по контракту для навигационной `u:a`; изменяющие кнопки режима `e` и `u:o` отвечают «Кнопка устарела» (как требует план) до 09-04. Тест `TestS22StaleProfile` фиксирует это поведение (`u:s:…:e:…`, `u:o`) — 09-04 должна изменить эти случаи вместе с включением режима правки.
3. Контракт S-22 шаг 4: `u:o` без строки профиля даёт P-1 (эталон `s22_no_row_profile`) — относится к 09-04, здесь `u:o` — «Кнопка устарела».
4. Контракт раздел 9: у главного меню третья кнопка и правка пяти эталонов — 09-04; здесь главное меню не менялось (запрет исполнения менять другие эталоны Phase 8). Поэтому `TestStartScreen` после второго `/start` по-прежнему ждёт две кнопки (`f:s`, `s:o`); 09-04 поменяет на три.
5. Кнопки городов экрана города без «✓» у текущего города: контракт «✓» для города не оговаривает.

## Deviations from Plan

1. **[Rule 3 - Blocking] `TestCountryMenu` (Phase 8).** Тест использовал `KZ` как «страну без названия» и ждал подпись `✓ 🇰🇿 KZ (0)`. После обязательного добавления `"KZ": "Казахстан"` в `countryNames` ожидание изменено на `✓ 🇰🇿 Казахстан (0)`. Смысл теста (текущая страна остаётся в меню без событий) сохранён; эталоны не затронуты.
2. **TDD-порядок.** Код задач 1–3 был написан до тестов (тесты и эталоны следом, в рамках той же задачи); чувствительность тестов проверена двумя мутациями (см. выше).
3. **`TestCallbackProfileRoundTrip`** — отдельный тест со всеми значениями справочников в обоих режимах; в `TestCallbackRoundTrip` добавлены строки `u:o`, `u:b/n/z`, `v2147483647`, города. Отдельное имя — чтобы не раздувать общий список.
4. Коммиты не выполнялись (решение владельца): `actuals.commits: 0`; `plan_head_before` — HEAD на начало работы.
5. Промежуточный временный каталог мутационной проверки остался в системном временном каталоге (вне репозитория).

## Known Stubs

Нет. Единственная намеренная заглушка — ответ «Кнопка устарела» на `u:o` и изменяющие кнопки режима `e`: экраны профиля и правки появятся в 09-04.

## Threat Flags

Нет новых поверхностей сверх `<threat_model>`: идентификатор пользователя берётся только из `q.From.ID`; значения из кнопок проверяются грамматикой и справочниками D-04, город — по каталогу; в лог попадают только `prefix=u` и `err`; `profile.go` не обращается к фильтрам и таблице `users` (проверено командой `<verify>`).

## Self-Check: PASSED

Файлы `internal/bot/profile.go`, `internal/bot/profile_test.go` и 32 новых эталона на месте; `TRACER OK`, `WIZARD OK`, `PLAN 09-03 OK` получены; полный прогон — 0 FAIL, 0 SKIP; `gofmt` и `go vet` чисты; `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md`, миграции, `internal/store`, `go.mod`, `go.sum`, `deploy/`, контракты не менялись.
