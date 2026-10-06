---
phase: 09-onboarding-user-profile
verified: 2026-10-06T00:00:00Z
status: passed
score: 5/5 must-haves verified
covered_files:
  - ".planning/REQUIREMENTS.md"
  - ".planning/phases/09-onboarding-user-profile/09-01-PLAN.md"
  - ".planning/phases/09-onboarding-user-profile/09-01-SUMMARY.md"
  - ".planning/phases/09-onboarding-user-profile/09-02-PLAN.md"
  - ".planning/phases/09-onboarding-user-profile/09-02-SUMMARY.md"
  - ".planning/phases/09-onboarding-user-profile/09-03-PLAN.md"
  - ".planning/phases/09-onboarding-user-profile/09-03-SUMMARY.md"
  - ".planning/phases/09-onboarding-user-profile/09-04-PLAN.md"
  - ".planning/phases/09-onboarding-user-profile/09-04-SUMMARY.md"
  - ".planning/phases/09-onboarding-user-profile/09-05-PLAN.md"
  - ".planning/phases/09-onboarding-user-profile/09-05-SUMMARY.md"
  - ".planning/phases/09-onboarding-user-profile/09-06-PLAN.md"
  - ".planning/phases/09-onboarding-user-profile/09-06-SUMMARY.md"
  - "internal/bot/bot.go"
  - "internal/bot/callback.go"
  - "internal/bot/filters.go"
  - "internal/bot/profile.go"
  - "internal/bot/stats.go"
  - "internal/store/profiles.go"
  - "migrations/00006_user_profiles.sql"
covered_digest: "v1:sha256:6ac30dfe3fd3f5d2aa4db49f637f6d29b0f1909a0e59dfaa0807add87f5710af"
behavior_unverified: 0
overrides_applied: 0
re_verification: false
gaps: []
---

# Phase 9: Onboarding & User Profile — отчёт о проверке

**Цель фазы:** Новый пользователь может добровольно ответить на четыре вопроса о себе, а любой пользователь — посмотреть и изменить ответы в `/profile`; отказ от анкеты ничего не ограничивает, профиль не зависит от настроек поиска.
**Проверено:** 2026-10-06
**Статус:** passed
**Повторная проверка:** нет, первичная

Проверка идёт по коду и тестам, а не по SUMMARY: прочитаны `migrations/00006_user_profiles.sql`, `internal/store/profiles.go`, `internal/bot/profile.go`, `callback.go`, `filters.go`, `bot.go`, `stats.go`, тесты и эталоны; полный прогон тестов выполнен заново.

## Прогон проверок

| Проверка | Команда | Результат |
|---|---|---|
| Тесты | `TEST_DATABASE_URL=… go test -p 1 -count=1 -v ./...` | все пакеты `ok`; 618 запусков (399 верхнего уровня + 219 подтестов): **PASS 618, FAIL 0, SKIP 0** |
| Форматирование | `gofmt -l .` | пусто |
| Статический анализ | `go vet ./...` | чисто |
| Рабочее дерево | `git status --short` | чисто (ничего не изменялось проверкой, кроме этого файла) |

В `09-ACCEPTANCE.md` записано «397 верхнего уровня, 616 всего» на момент локальной приёмки; разница в два теста верхнего уровня (`TestStats`, `TestProfileCounts`) объясняется коммитом `6f02fbc` (`/stats`), добавленным после фазы.

Ключевые тесты фазы, все PASS: `TestContractCoverage` (два контракта, S-01…S-25, у каждого сценария есть тест и эталон), `TestS18Offer`, `TestS19SkipAll`, `TestS20Wizard`, `TestS21SkipQuestion`, `TestS22StaleProfile`, `TestS23Profile`, `TestS24Edit`, `TestS25CityText`, `TestProfileIndependence`, `TestProfileNoLimits` (S-01…S-17 при статусах `skipped`, `in_progress`, `completed`; `not_started` — обычный запуск), `TestProfileNoAnswersInLogs`, `TestProfileStart`, `TestProfileUpdateAt`, `TestProfileStaleNoWrite`, `TestProfileRace`, `TestProfileChecks`, `TestProfileCascade`, `TestUserProfilesColumns`, кодек `u:` (round-trip, отказ, длина, `cbPrefix`).

## Достижение цели

### Наблюдаемые истины (критерии успеха ROADMAP)

| # | Истина | Статус | Доказательства |
|---|--------|--------|----------------|
| 1 | Контракт экранов, PRF и новая формулировка BOT-08 утверждены владельцем до изменения кода | ✓ VERIFIED | `09-UX-CONTRACT.md` шапка: «Статус: утверждён 2026-10-05»; в `09-01-SUMMARY.md` строка `CONTRACT APPROVED: 2026-10-05` с ответом владельца дословно; `SCHEMA APPROVED: 2026-10-05` в `09-02-SUMMARY.md`; текст миграции в SUMMARY побайтно равен `migrations/00006_user_profiles.sql` (сверено). Формулировка BOT-08 в `REQUIREMENTS.md:52`, `MVP.md` §8.5 и §12.2, `PROJECT.md:58` совпадает с D-12 |
| 2 | Первый `/start` (и следующий `/start` действующего пользователя без строки профиля) один раз предлагает анкету; пропуск анкеты и вопроса возможен; поиск и остальное работают при любом статусе | ✓ VERIFIED | `onStart` → `Store.StartUser`: строка профиля создаётся в транзакции `/start` до отправки (`INSERT … ON CONFLICT DO NOTHING RETURNING`, `offered` только у создавшего строку), повторный `/start` — главное меню; `applyProfile`: пропуск анкеты допустим только из `not_started`; «Пропустить вопрос» — кнопка `u:a` (навигация без записи значения). `TestS18Offer`, `TestS19SkipAll`, `TestS21SkipQuestion`; `TestProfileNoLimits` прогоняет S-01…S-17 с теми же эталонами при `skipped`, `in_progress`, `completed` и проверяет, что статус действительно выставлен |
| 3 | Любой ответ можно изменить через `/profile`; устаревшие кнопки профиля не меняют данные | ✓ VERIFIED | `/profile` и `u:o` → `profileScreen` (P-1) → `u:a:<поле>:e` → `u:s:…:e`; `UpdateProfileAt` проверяет `profile_v` под `SELECT … FOR UPDATE` в той же транзакции, версия растёт только при изменении ответа/статуса. `TestS24Edit` — все четыре поля, рост `profile_v`, снятие всех видов спорта, смена страны очищает город; `TestS22StaleProfile`, `TestProfileStaleNoWrite`, `TestProfileRace` — изменяющая устаревшая кнопка даёт «Кнопка устарела — нажмите /start» без записи, навигационная показывает экран по текущему состоянию |
| 4 | Действия с профилем не меняют `users.filters`, действия с фильтрами не меняют профиль; город проживания текстом не попадает в поиск | ✓ VERIFIED | `profile.go` не вызывает `Filters`/`PeekFilters`/`UpdateFilters*`, не использует `userFilters`/`loadFilters` (grep пуст); `filters.go`, `search.go`, `settings.go`, `list.go` и `store/users.go` не обращаются к `user_profiles`; таблица `users` не менялась (`users.go` без правок после фазы 8). `TestProfileIndependence` сравнивает таблицы `users` и `user_profiles` целиком до/после обеих серий действий. `residenceText` перехватывает текст только при действующей отметке `awaiting_city_until` (10 минут), в `users` не пишет; после истечения текст идёт в поиск (`TestS25CityText`, `s25_expired.txt`, финал `TestProfileIndependence`) |
| 5 | Владелец прошёл приёмку в Telegram по чек-листу | ✓ VERIFIED (по записи владельца, см. ниже) | Не перепроверялось: `ROLLOUT PERMITTED`, `ROLLOUT DONE`, `PRODUCTION ACCEPTANCE` с датой 2026-10-05 и ответами владельца дословно в `09-06-SUMMARY.md`; `LOCAL ACCEPTANCE: 2026-10-05` в `09-05-SUMMARY.md`; отметки в `09-ACCEPTANCE.md` |

**Счёт:** 5/5 истин подтверждено; behavior_unverified = 0.

### Требования

| Требование | Статус | Доказательства в коде и тестах |
|---|---|---|
| PRF-01 анкета один раз на `/start` | ✓ SATISFIED | `StartUser` (D-06), PK `tg_id` — одна строка на пользователя; `TestProfileStart`, `TestS18Offer` (включая сорвавшуюся отправку) |
| PRF-02 пропуск анкеты и вопроса | ✓ SATISFIED | `u:n` → `skipped`; «Пропустить вопрос» не пишет; `TestS19SkipAll`, `TestS21SkipQuestion` |
| PRF-03 отказ ничего не ограничивает | ✓ SATISFIED | `TestProfileNoLimits` на трёх статусах + `not_started`; изменились только 7 эталонов Phase 8 (O-1 вместо первого меню, третья кнопка) — все приняты владельцем |
| PRF-04 пол `m/f`, 6 возрастов, 8 видов спорта; иное отвергается | ✓ SATISFIED | справочники `profileGenders/Ages/Countries/Sports` — единственный источник допустимых значений в `parseProfile`; дополнительно `CHECK` в `00006` (gender, age 0..5, sports `<@` восьми кодов, country `^[A-Z]{2}$`); `TestCallbackReject` (набор `u:s:…` с чужими значениями), `TestProfileChecks` |
| PRF-05 изменение любого ответа, растёт `profile_v` | ✓ SATISFIED | `TestS24Edit`, `TestProfileUpdateAt` |
| PRF-06 профиль независим от поиска | ✓ SATISFIED | см. истину 4; `TestProfileIndependence` |
| PRF-07 устаревшие кнопки `u:` не меняют данные | ✓ SATISFIED | `UpdateProfileAt` → `ErrStaleProfile`; недопустимые переходы статуса и ключ города вне каталога — тоже `ErrStaleProfile`; `TestS22StaleProfile`, `TestProfileStaleNoWrite` |
| PRF-08 город текстом не попадает в поиск | ✓ SATISFIED | `residenceText` вызывается первым в `onText`; `TestS25CityText`, `TestProfileIndependence` (б) |
| PRF-09 ответы не в логах и не свободным текстом в `callback_data` | ✓ SATISFIED | в `profile.go`/`stats.go` логируются только `err` и флаг `residence=true`; `cb` логирует только `cbPrefix` (одна буква); `TestProfileNoAnswersInLogs` ищет приметные значения («Секретоград», «Женский», «45–54», `bike`, `swim`, `u:s:`, `u:a:`) в логах и проверяет алфавит `callback_data` всех экранов профиля |
| PRF-10 контракт и эталоны утверждены до кода | ✓ SATISFIED | `CONTRACT APPROVED`, `SCHEMA APPROVED`, статус контракта; REQUIREMENTS.md: PRF-01…10 `[x]`, трассировка `Complete`. Порядок «до кода» подтверждается лишь записями владельца в SUMMARY; хронологию коммитов отдельно не восстанавливал |

Осиротевших требований нет: все десять ID из ROADMAP присутствуют в REQUIREMENTS.md и закрыты.

### Артефакты

| Артефакт | Статус | Детали |
|---|---|---|
| `migrations/00006_user_profiles.sql` | ✓ VERIFIED | FK `ON DELETE CASCADE`, CHECK-и на статус/значения/длины, парные CHECK для отметки ожидания, `Down` есть; `users` не изменяется |
| `internal/store/profiles.go` | ✓ VERIFIED | `StartUser`, `PeekProfile`, `UpdateProfileAt` (версия под блокировкой строки), служебная отметка ожидания версию не меняет; реальные запросы, не заглушки |
| `internal/bot/profile.go` | ✓ VERIFIED | экраны O-1…O-6, P-1/P-2, `onProfile`, `residenceText`; экранирование города (`html.EscapeString`) |
| `internal/bot/callback.go` | ✓ VERIFIED | грамматика `u:`, версия — последнее поле, значения из справочников, ≤ 64 байт, ключ города ≤ 49 байт; `encode` возвращает ошибку, если результат не разбирается обратно |
| `internal/bot/filters.go`, `bot.go` | ✓ WIRED | `/start` → `StartUser`; `mainMenu` с «👤 Мой профиль» (`u:o`); обработчики `/profile`, префикс `u:`; `onText` → `residenceText` до поиска |
| `internal/bot/testdata/screens/` | ✓ VERIFIED | 147 файлов; эталоны S-18…S-25 присутствуют; `snapshot_test.go` не пишет файлы (правка эталонов только вручную) |

### Сквозная проверка данных (уровень 4)

Профиль читается из `user_profiles` реальным запросом (`profileSelect`) и выводится в `profileScreen`; города кнопок — из каталога событий (`TopCities`, `CityName`); заглушек и статических ответов нет.

### Anti-patterns

В файлах фазы нет `TBD`/`FIXME`/`XXX`; пустых реализаций нет. Единственный маркер `Упрощение:` — в `stats.go` (вне фазы, см. ниже).

## Коммит `6f02fbc` (`/stats`) — вне фазы, влияние на гарантии

Админская команда `/stats` (`internal/bot/stats.go`, `store.ProfileCounts`, эталон `stats.txt`) добавлена отдельным решением владельца и не является результатом фазы.

- **PRF-06 (независимость):** не нарушена. `onStats` не вызывает `StartUser`, не пишет ни в `users`, ни в `user_profiles`, фильтры не читает. `ProfileCounts` читает `count(*)` из `users` и агрегаты из `user_profiles` по одному признаку (без `tg_id`, без города, без сочетаний признаков).
- **PRF-09 (ответы не в логах):** не нарушена. Логируется только `err` при сбое запроса/отправки; тест `TestStats` проверяет, что в сводке нет города и идентификаторов; не-администратор получает ответ неизвестной команды («Нажмите /start…»), ничего не читается.
- Запрет «аналитика внутри фазы» не нарушен: коммиты фазы (`e31b5ae`, `bebeda0`, `824ef87`) `/stats` не содержат.
- Замечание (не пробел фазы): в самом коде `stats.go` отмечено, что каждая малая группа (< 5) скрывается отдельно, поэтому единственную скрытую группу разбивки можно вычислить вычитанием из «Предложена»; итоги по статусам точные. Для администратора с доступом к базе это ничего не раскрывает; если сводку увидит кто-то без доступа к базе — скрывать вторую группу. Экран P-1 обещает «обезличенную статистику» — сводка этому соответствует (агрегаты, без идентификаторов).

## Что не перепроверялось (записи владельца)

Следующее проверить по репозиторию нельзя; принято как записанное в SUMMARY дословно, повторно не тестировалось:

- production-выкат (образ `824ef877f1a3`, миграция `00006`, `DEPLOY VERIFY OK`) и состояние production (сейчас образ `6f02fbc093c8` после выката `/stats`, без миграции);
- production-приёмка владельца в Telegram («production-приёмка пройдена», 2026-10-05).

Строки владельца на месте: `CONTRACT APPROVED:`, `SCHEMA APPROVED:`, `LOCAL ACCEPTANCE:`, `ROLLOUT PERMITTED:`, `ROLLOUT DONE:`, `PRODUCTION ACCEPTANCE:` (все 2026-10-05). Поэтому человеческая проверка по фазе уже выполнена, статус `human_needed` не нужен.

Честные оговорки к production-приёмке:

1. Отметки «принято 2026-10-05» в таблице production-приёмки `09-ACCEPTANCE.md` проставлены по общему ответу владельца «production-приёмка пройдена», **без отчёта по каждому шагу**; в частности, владелец не отчитывался отдельно по шагу 14 (пауза более 10 минут) и по шагу 15 (второй аккаунт, «при наличии»).
2. Формулировка шага 12 чек-листа («кнопка поля в старом сообщении профиля → Кнопка устарела») расходится с контрактом S-22: кнопки полей навигационные и при устаревшей версии открывают экран по текущему состоянию, а «Кнопка устарела» отвечает устаревшая изменяющая кнопка. Замечание записано над таблицей в `09-ACCEPTANCE.md` и показано владельцу до приёмки; поведение кода соответствует контракту (`TestS22StaleProfile`).

## Сомнения и несинхронность документов (не блокируют фазу)

- `ROADMAP.md`: заголовок фазы в списке — `[ ]`, строка «**Plans**: 1/6 plans executed» устарела (все шесть планов отмечены `[x]`), в таблице прогресса — «6/6, In Progress». Это ожидаемо: закрытие фазы по команде владельца (`09-06-SUMMARY.md`: «фаза не закрыта — закрытие по команде владельца»); при закрытии строку «1/6» надо исправить.
- Известное ограничение D-10, принятое владельцем: поисковый запрос, отправленный в 10-минутное окно ожидания города, сохранится как город проживания; ответ бота это показывает и даёт «Изменить» (тест `s25_text_*`).
- Для города вне справочника ключ в кнопках не создаётся, версия профиля ≥ 100000 делает кнопку города каталога слишком длинной — такая кнопка не показывается, город вводят текстом (описано в `callback.go`, покрыто `TestCallbackLength`). Практический риск пренебрежимо мал.

## Пробелы

Нет.

---

_Проверено: 2026-10-06_
