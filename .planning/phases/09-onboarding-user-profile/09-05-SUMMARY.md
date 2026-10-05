---
phase: 09-onboarding-user-profile
plan: 05
subsystem: bot
tags: [telegram, tests, contract-coverage, profile, logs, local-acceptance]
requires: [09-01, 09-02, 09-03, 09-04]
provides:
  - "internal/bot/contract_test.go: строгий `TestContractCoverage` — множество сценариев двух контрактов равно множеству тестов `TestSNN…`, у каждого сценария есть эталон `sNN_*`; временное исключение из 09-03 удалено"
  - "internal/bot/profile_test.go: `TestProfileNoLimits`, `TestProfileNoAnswersInLogs`, `forceProfileStatusMW`"
  - "internal/bot/bot_test.go: `newTestBot` добавляет обёртку статуса анкеты, когда задан `forcedProfileStatus`"
  - ".planning/phases/09-onboarding-user-profile/09-ACCEPTANCE.md: фактические имена эталонов и результат полного прогона в разделе «Локальная приёмка»"
affects: [09-06]
plan_head_before: bebeda06fb905a54ffdbf7487ecc79523a5de307
actuals:
  tokens: 6000
  tasks: 2
  commits: 0
tech-stack:
  added: []
  patterns:
    - "статус анкеты в тестах выставляется middleware после обработки `/start` прямым UPDATE через отдельное соединение; неудача считается атомарным счётчиком, а не роняет тест"
key-files:
  created: []
  modified:
    - internal/bot/contract_test.go
    - internal/bot/bot_test.go
    - internal/bot/profile_test.go
    - .planning/phases/09-onboarding-user-profile/09-ACCEPTANCE.md
key-decisions: []
requirements-completed: [PRF-01, PRF-02, PRF-03, PRF-04, PRF-05, PRF-06, PRF-07, PRF-08, PRF-09, PRF-10]
status: complete
---

# Phase 9 Plan 5: Тесты и локальная приёмка

Строгое равенство сценариев двух контрактов, тестов и эталонов, доказательство «отказ от анкеты ничего не ограничивает» (S-01…S-17 при трёх статусах анкеты) и проверка логов и `callback_data` на ответы анкеты; задача 2 (просмотр эталонов владельцем) ещё не выполнена.

## Что сделано (задача 1)

- `TestContractCoverage`: из ожидаемого множества убрано исключение «сценарий второго контракта без теста»; сравниваются все 25 сценариев обоих контрактов с тестами `TestSNN…`; порог — не меньше 17 сценариев в контракте Phase 8 и не меньше 8 в контракте Phase 9; лишние и отсутствующие перечисляются в ошибке; каждому сценарию нужен эталон `sNN_*`.
- `newTestBot`: если `forcedProfileStatus` не пуст, к опциям бота добавляется `forceProfileStatusMW`; вне `TestProfileNoLimits` переменная пуста и фикстура прежняя.
- `TestProfileNoLimits`: для статусов `skipped`, `in_progress`, `completed` запускаются как подтесты `TestS01MainMenu`…`TestS17InternalError` с прежними эталонами. После каждого `/start` статус выставляется UPDATE с проверкой «затронута ровно одна строка»; подтест падает, если статус не выставлен, а весь прогон по статусу падает, если он не выставлялся ни разу. Статус `not_started` покрыт обычным запуском тех же сценариев.
- `TestProfileNoAnswersInLogs`: проход анкеты с приметными ответами (пол «Женский», возраст «45–54», Беларусь, город текстом «Секретоград», «Велоспорт» и «Плавание»), редактирование всех полей, `/profile`, устаревшая кнопка и две кнопки вне грамматики (одна с текстом города в данных). В логах нет «Секретоград», «Женский», «45–54», `bike`, `swim`, `u:s:`, `u:a:`; `callback_data` кнопок всех показанных экранов состоит только из `[A-Za-z0-9:_*-]`.
- `09-ACCEPTANCE.md`: вместо масок `sNN_*` вписаны фактические имена эталонов S-18…S-25, добавлен подраздел «Результат полного прогона». Колонки «Отметка» пусты.

Поведение бота и эталоны не менялись; изменены только тестовые файлы и документ приёмки.

## Проверка

Команда `<verify>` задачи 1 завершилась строкой `COVERAGE OK`. Хвост вывода (фактический):

```
RC=0
PASS_TOP=397   (верхний уровень; 616 вместе с подтестами)
FAIL=0
SKIP=0
ok  	aggregator/cmd/aggregator	0.573s
ok  	aggregator/internal/alert	0.746s
ok  	aggregator/internal/bot	19.243s
ok  	aggregator/internal/dedup	2.008s
ok  	aggregator/internal/event	0.448s
ok  	aggregator/internal/heartbeat	0.719s
ok  	aggregator/internal/ingest	3.002s
ok  	aggregator/internal/ingest/sources/heroleague	0.550s
ok  	aggregator/internal/ingest/sources/klbamatar	1.257s
ok  	aggregator/internal/ingest/sources/marathonec	0.875s
ok  	aggregator/internal/ingest/sources/probeg	1.060s
ok  	aggregator/internal/ingest/sources/runin	0.639s
ok  	aggregator/internal/ingest/sources/tristyle	0.663s
ok  	aggregator/internal/normalize	0.474s
ok  	aggregator/internal/store	2.346s
COVERAGE OK
```

`gofmt -l .` пусто, `go vet ./...` чисто; состав изменённых эталонов Phase 8 относительно `SNAPSHOT BASE: 5771770` — ровно семь утверждённых файлов; `go.mod`, `go.sum`, `deploy/`, прежние миграции и контракт Phase 8 не тронуты.

### Чувствительность `TestContractCoverage`

Проверено подменой через `go test -overlay` (файлы репозитория не правились, подмены лежали во временном каталоге и удалены):

- в копии контракта Phase 9 заголовок S-25 заменён — тест падает на пороге (найдено 7 сценариев, нужно ≥ 8);
- в копию контракта Phase 9 добавлен лишний сценарий S-26 — тест падает с перечислением расхождения и «S-26: нет эталона s26_*»;
- в копии тестов `TestS25CityText` переименован — тест падает: в множестве тестов нет S-25, контракт его требует.

Чувствительность `TestProfileNoAnswersInLogs` проверена так же: подмена `profile.go` с записью города в строку лога роняет тест на «Секретоград».

## Deviations from Plan

None - plan executed exactly as written. Мелкая деталь исполнения: функция `forceProfileStatusMW` и счётчики лежат в `profile_test.go` рядом с `TestProfileNoLimits`, а в `bot_test.go` добавлено только подключение в `newTestBot`.

## Неподтверждённое и ожидающее

- Задача 2 (локальная приёмка, `checkpoint:decision`, `blocking-human`) не начата: колонки «Отметка» в `09-ACCEPTANCE.md` пусты, строки с итогом приёмки нет, `REQUIREMENTS.md`, `ROADMAP.md` и `STATE.md` не менялись, PRF-01…PRF-10 не отмечены.
- Просмотр владельцем семи изменённых эталонов Phase 8 и новых эталонов S-18…S-25 не выполнялся.
- Изменения оставлены в рабочем дереве, коммитов нет (`actuals.commits: 0`).

## Known Stubs

None.

## Threat Flags

None.

## Ответ владельца (задача 2)

Владельцу показаны: результат задачи 1 и повторного полного прогона (397 тестов верхнего уровня, 616 с подтестами, 0 FAIL, 0 SKIP), состав семи изменённых эталонов Phase 8 рядом с разделом 9 контракта, выборка новых экранов и команды просмотра; владелец вывел полный diff эталонов от `SNAPSHOT BASE` и эталоны S-23.

LOCAL ACCEPTANCE: 2026-10-05
«локальная приёмка пройдена»

Расхождений владелец не назвал. В `09-ACCEPTANCE.md` проставлены 15 отметок «принято 2026-10-05» (8 сценариев, 7 изменённых эталонов Phase 8); PRF-01…PRF-10 отмечены выполненными локально. Phase 9 не закрыта: на production код и миграция `00006` не выкачены — это план 09-06, только по отдельному разрешению владельца. Коммитов исполнителем не было; VPS и production не затрагивались.
