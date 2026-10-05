---
phase: 09-onboarding-user-profile
plan: 02
subsystem: store
tags: [postgres, migration, profile, store]
requires: [09-01]
provides:
  - "migrations/00006_user_profiles.sql: таблица `user_profiles` (утверждённая схема), секции goose Up и Down"
  - "internal/store/profiles.go: `ErrStaleProfile`, `ProfileNotStarted`/`InProgress`/`Completed`/`Skipped`, `Profile`, `StartUser`, `PeekProfile`, `UpdateProfileAt`"
  - "internal/store/profiles_test.go: TestProfileStart, TestUserProfilesColumns, TestProfileCascade, TestProfileUpdateAt, TestProfileStaleNoWrite, TestProfileRace, TestProfileChecks"
affects: [09-03, 09-04, 09-05, 09-06]
plan_head_before: 5771770
actuals:
  tokens: 5000
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "профиль меняется по образцу `updateFilters`: `SELECT ... FOR UPDATE` и сравнение `profile_v` в одной транзакции, чужая версия или нет строки — `ErrStaleProfile` без записи"
    - "NULL в базе — нулевое значение поля Go (пустая строка, `AgeGroup = -1`, нулевое время)"
key-files:
  created:
    - migrations/00006_user_profiles.sql
    - internal/store/profiles.go
    - internal/store/profiles_test.go
  modified:
    - internal/store/storetest/storetest.go
    - internal/store/store_test.go
key-decisions:
  - "Схема `user_profiles` утверждена владельцем 2026-10-05 и перенесена в миграцию без изменений"
  - "`StartUser` — единственное место, где одна транзакция касается и `users`, и `user_profiles`; `offered = true` только у вызова, создавшего строку"
  - "Отметка ожидания города (`awaiting_city_*`) не меняет `profile_v` и `updated_at`: кнопка, показанная до ввода города, не устаревает от самого ввода"
  - "`started_at` и `completed_at` ставятся один раз по параметру `now` при реальном изменении ответов или статуса; переходы статусов хранилище не решает"
requirements-completed: []
completed: 2026-10-05
status: complete
---

# Phase 9 Plan 02: модель данных профиля

**Статус: complete.** Миграция `00006_user_profiles.sql` и слой `internal/store/profiles.go` готовы и проверены на локальной тестовой PostgreSQL. План был прерван один раз (после задачи 1 и чернового кода задач 2–3) и возобновлён: черновик проверен, миграция сверена с утверждённым текстом побайтно (совпала), недостающее (`UpdateProfileAt` и четыре теста) дописано. Ничего не закоммичено — коммит делает владелец. Production, VPS и существующие таблицы не затрагивались; миграция применена только к локальной тестовой базе самим прогоном тестов.

## Ответ владельца (задача 1)

Ворота: чек-лист третьей редакции Phase 8 — 16 строк, все «принято»; `CONTRACT APPROVED: 2026-10-05` в `09-01-SUMMARY.md`.

SCHEMA APPROVED: 2026-10-05
«утверждаю схему»

Правок владелец не назвал. Утверждённый текст миграции `migrations/00006_user_profiles.sql` (переносится в файл дословно):

```sql
-- +goose Up
-- Профиль пользователя (D-07 Phase 9, BOT-08 в новой редакции): ответы
-- добровольной анкеты отдельно от настроек поиска. Строка создаётся в
-- транзакции /start; таблица users не меняется. profile_v — своя версия,
-- не filters_v: устаревшая кнопка профиля ничего не пишет.
CREATE TABLE user_profiles (
    tg_id                  BIGINT PRIMARY KEY REFERENCES users(tg_id) ON DELETE CASCADE,
    status                 TEXT NOT NULL DEFAULT 'not_started'
                           CHECK (status IN ('not_started', 'in_progress', 'completed', 'skipped')),
    gender                 TEXT CHECK (gender IN ('m', 'f')),
    age_group              SMALLINT CHECK (age_group BETWEEN 0 AND 5),
    residence_country_code TEXT CHECK (residence_country_code ~ '^[A-Z]{2}$'),  -- ZZ — другая страна
    residence_city         TEXT CHECK (char_length(residence_city) BETWEEN 1 AND 64),
    residence_city_key     TEXT CHECK (char_length(residence_city_key) <= 64 AND residence_city_key ~ '^[a-z0-9-]+$'),
    sports                 TEXT[] NOT NULL DEFAULT '{}'
                           CHECK (sports <@ ARRAY['run', 'trail', 'ocr', 'bike', 'swim', 'ski', 'tri', 'row']::text[]),
    profile_v              INTEGER NOT NULL DEFAULT 1,
    awaiting_city_until    TIMESTAMPTZ,                  -- до какого момента текст считается городом; NULL — не ждём
    awaiting_city_mode     TEXT CHECK (awaiting_city_mode IN ('w', 'e')),  -- анкета или редактирование
    offered_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at             TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (residence_city IS NULL OR residence_country_code IS NOT NULL),
    CHECK (residence_city_key IS NULL OR residence_city IS NOT NULL),
    CHECK ((awaiting_city_until IS NULL) = (awaiting_city_mode IS NULL))
);

-- +goose Down
DROP TABLE user_profiles;
```

Отличия от перечня D-07, утверждённые вместе со схемой: значение по умолчанию у `status`; колонка `awaiting_city_mode`; три ограничения таблицы (город только при выбранной стране; ключ города только при заданном городе; время и режим ожидания заданы оба или ни один). Индексов, кроме первичного ключа, нет.

## Что сделано

- Задача 1 (ворота и утверждение схемы): выполнена до прерывания, ответ владельца — выше.
- Задача 2 (tracer, транзакция `/start`): файл миграции — дословно утверждённый SQL; `StartUser` (один `InTx`: `touchUser`, затем `INSERT ... ON CONFLICT DO NOTHING RETURNING profile_v`; если строка уже была — один `UPDATE`, снимающий `awaiting_city_*`); `PeekProfile` (одно чтение, без записей); списки `TRUNCATE` в `storetest.go` и `store_test.go` дополнены `user_profiles` перед `users`; тесты `TestProfileStart` (в логе `TRACER start=offered profile=not_started`), `TestUserProfilesColumns`, `TestProfileCascade`.
- Задача 3 (`UpdateProfileAt`, тесты сначала): четыре теста написаны и падали на компиляции (метода не было), затем добавлен `UpdateProfileAt`. Проверка версии и запись — в одной транзакции под `FOR UPDATE`; ошибка `fn` возвращается без обёртки; неизменённый профиль не пишется вовсе; `profile_v`/`updated_at` растут только при изменении ответов или статуса; пустой и `nil`-срез `Sports` пишутся как `'{}'`, пустые строки и `AgeGroup = -1` — как `NULL`; ошибка базы оборачивается без значений ответов. Методов удаления профиля и очистки ответов нет.

## Проверки

| Проверка | Результат |
|---|---|
| задача 2, команда `<verify>` | хвост вывода: `--- PASS: TestDupesWouldAuto`, `PASS`, `ok  	aggregator/internal/bot	12.777s`, затем `TRACER OK` |
| задача 3, команда `<verify>` | хвост вывода: `--- PASS: TestDupesWouldAuto`, `PASS`, `ok  	aggregator/internal/bot	12.758s`, затем `PLAN 09-02 OK` |
| полный прогон `go test -p 1 -count=1 ./...` | все пакеты `ok` (`internal/store` 2.1s, `internal/bot` 12.8s), без FAIL; с `-v` строк `--- SKIP` — 0 |
| `gofmt -l internal/store`, `go vet ./internal/store/...` | чисто |
| `users.go`, `go.mod`, `go.sum`, `deploy/`, `docker-compose.yml`, миграции 00001–00005 | не изменены (`git status`) |

## Deviations from Plan

1. План выполнен в два захода: первый исполнитель был прерван, второй принял черновик (миграция, `StartUser`, `PeekProfile`, три теста, правки `TRUNCATE`) без переписывания. В черновике ошибок не найдено; миграция совпала с утверждённым SQL побайтно.
2. Коммиты не выполнялись (решение владельца: коммиты делает он); `actuals.commits: 0`, `plan_head_before` — HEAD на момент завершения, изменения лежат в рабочем дереве.
3. Сверх перечня плана в `TestProfileChecks` добавлен случай «статус x» (ограничение на `status`); в `TestProfileStaleNoWrite` — случай «пользователь есть, профиля нет».

## Known Stubs

Нет.

## Threat Flags

Нет новых поверхностей сверх `<threat_model>` плана: запросы параметризованы, ограничения `CHECK` проверены `TestProfileChecks`, значения ответов в тексты ошибок не подставляются.

## Следующий шаг

План 09-03: бот использует `StartUser`, `PeekProfile`, `UpdateProfileAt`. Миграция попадёт на production только в 09-06 по отдельному разрешению владельца.

## Self-Check: PASSED

Файлы `migrations/00006_user_profiles.sql`, `internal/store/profiles.go`, `internal/store/profiles_test.go` на месте; обе проверки (`TRACER OK`, `PLAN 09-02 OK`) и полный прогон пройдены; `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md` не менялись.
