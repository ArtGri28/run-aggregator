# Phase 16: текст миграции

**Статус:** утверждён владельцем 2026-10-14 («Всё утверждаю»), хранение — по Q-4 контракта (удаление через 90 дней). Файла в `migrations/` нет; он создаётся планом фазы извлечением блока ниже без изменений — сравнение побайтное.
**Основа:** 16-CONTEXT.md, решения D-08 (заявки об ошибках), D-12 (предложения), D-15 (текст миграции утверждается до кода) и раздел «На усмотрение исполнителя» (имена таблиц и колонок, нужна ли таблица заявок, очистка); `16-UX-CONTRACT.md` (разделы 5, 6, вопросы Q-1, Q-4, Q-7). Стиль — `migrations/00007_support.sql` и `migrations/00010_favorites.sql`.

## Текст миграции

Имя файла — `migrations/00011_reports.sql`.

```sql
-- +goose Up
-- Заявки «Сообщить об ошибке» в карточке старта (Phase 16, D-06…D-08):
-- кто, о каком старте, какая причина и когда. Текста заявки нет: причина
-- выбирается кнопкой. Записывается событие, корневое на момент заявки.
-- Одна заявка на «пользователь — событие — причина»; предел заявок в сутки
-- считается по created_at. Заявка не связывается с сообщением в группе
-- поддержки: ответ на него пользователю не доставляется (D-07).
-- Допустимые причины — в справочнике кода; здесь проверяется только формат.
CREATE TABLE error_reports (
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),
    event_id   BIGINT NOT NULL REFERENCES events(id),
    reason     TEXT NOT NULL CHECK (reason ~ '^[a-z][a-z_]{1,31}$'),
    created_at TIMESTAMPTZ NOT NULL,  -- по часам бота
    PRIMARY KEY (tg_id, event_id, reason)
);
CREATE INDEX error_reports_user_created ON error_reports (tg_id, created_at);

-- Предложения стартов (D-10…D-12): только кто и когда — для предела
-- 3 в сутки и счётчика /quality. Сама ссылка в базе не хранится: она есть
-- только в сообщении бота в группе поддержки или администраторам.
-- Строка пишется до отправки и удаляется, если отправка не удалась.
CREATE TABLE start_suggestions (
    id         BIGSERIAL PRIMARY KEY,
    tg_id      BIGINT NOT NULL CHECK (tg_id > 0),
    created_at TIMESTAMPTZ NOT NULL  -- по часам бота
);
CREATE INDEX start_suggestions_user_created ON start_suggestions (tg_id, created_at);

-- Отметка «жду ссылку»: до expires_at следующее сообщение пользователя
-- считается предложением старта. Отдельная таблица, а не вид отметки в
-- support_awaiting: откат образа не примет ссылку за обращение в поддержку.
-- Режимы «жду ссылку», «жду обращение» и «жду город» не действуют
-- одновременно — это держит код.
CREATE TABLE suggest_awaiting (
    tg_id      BIGINT PRIMARY KEY CHECK (tg_id > 0),
    expires_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE suggest_awaiting;
DROP TABLE start_suggestions;
DROP TABLE error_reports;
```

## Что проверено

- 2026-10-14 блок выполнен целиком — раздел Up, затем раздел Down — на локальной тестовой базе (`aggregator_test`, контейнер PostgreSQL 17, схема версии 10) между `BEGIN` и `ROLLBACK`, с остановкой на первой ошибке. Ошибок нет.
- После раздела Up в транзакции созданы: `error_reports` с первичным ключом `error_reports_pkey`, внешним ключом `error_reports_event_id_fkey`, проверками `error_reports_tg_id_check`, `error_reports_reason_check` и индексом `error_reports_user_created`; `start_suggestions` с `start_suggestions_pkey`, `start_suggestions_tg_id_check` и индексом `start_suggestions_user_created`; `suggest_awaiting` с `suggest_awaiting_pkey` и `suggest_awaiting_tg_id_check`. После раздела Down ни одной из трёх таблиц нет; после `ROLLBACK` в схеме прежние 18 таблиц, версия 10.
- Запросы чтения 1 и 3 (раздел ниже) выполнены на той же базе в транзакции только для чтения: разбираются и выполняются без ошибок.
- Имена `error_reports`, `start_suggestions`, `suggest_awaiting` и имена индексов в миграциях `00001`–`00010` не заняты.
- На production текст не выполнялся.

## Что меняется

- Три новые таблицы. Существующие таблицы, колонки, индексы и ограничения не меняются; строки не переписываются.
- **`error_reports`** — пишет только бот: строка на принятую заявку (R-2 контракта). Первичный ключ — «одна заявка на пару и причину» (D-08): повтор — `INSERT … ON CONFLICT DO NOTHING` без строки, бот отвечает R-3. Индекс `(tg_id, created_at)` — для предела в сутки (Q-1). Внешний ключ на событие — как у `favorites`.
- **`start_suggestions`** — только `tg_id` и время (D-12). `id` нужен, чтобы удалить именно зарезервированную строку при сбое отправки (P-5). Индекс — для предела 3 в сутки.
- **`suggest_awaiting`** — отметка ожидания ссылки, по образцу `support_awaiting` (`00007`). Одна строка на пользователя; потребляется при приёме ссылки под блокировкой строки, как `SupportTake`.
- **Внешнего ключа на `users` нет** ни у одной таблицы: заявка и предложение доступны без `/start`, как поддержка и избранное.
- **Причина проверяется по формату,** как `events.sport` и `outbox.kind`: справочник причин — в коде (`date`, `place`, `distance`, `link`, `cancelled`, `duplicate`, `other`).

## Запросы чтения для `/quality`

Метрики D-02…D-04 считаются по существующим таблицам без новых колонок: `events` (`is_listed`, `merged_into_id`, `start_date`, `sport`, `title`, `city`, `registration_url`), `event_distances`, `source_events` (`event_id`, `source_id`, `state`, `match_state`), `sources`, `ingest_runs` (`status`, `finished_at`, `metrics`). Наброски; `{включённые}` — фрагмент `sportVisible()` (`internal/store/listing.go:47`), `$1` — «сегодня» по Москве.

1. **События по источникам (D-02, D-03)** — одна строка на пару «событие — источник». Признаки даты-заглушки и годной ссылки — функции Go (`event.DateTBD`, `event.ValidURL`), поэтому запрос отдаёт название и ссылку, а доли считает бот; второй реализации в SQL нет (D-03).
   ```sql
   SELECT DISTINCT e.id, e.sport, s.code, e.title,
          COALESCE(e.city, '') <> ''                                   AS has_city,
          EXISTS (SELECT 1 FROM event_distances d WHERE d.event_id = e.id) AS has_dist,
          COALESCE(e.registration_url, '')                             AS reg_url,
          (SELECT count(DISTINCT x.source_id) FROM source_events x
            WHERE x.event_id = e.id) = 1                               AS is_unique
   FROM events e
   JOIN source_events se ON se.event_id = e.id
                        AND se.state <> 'gone' AND se.match_state <> 'out_of_scope'
   JOIN sources s ON s.id = se.source_id
   WHERE e.is_listed AND e.merged_into_id IS NULL AND e.start_date >= $1::date
     AND e.sport IN ({включённые})
   ```
   Отбор событий — тот же, что `futureListed()` (`internal/store/facets.go:32`); частичный индекс `idx_events_listed_start_date_id` применим, записи источника — по `idx_source_events_event_id`. Уникальность — по Q-5 контракта (рекомендация: записи только одного источника в любом состоянии; при альтернативе — `count(*) = 1`). «Всего в выдаче» — число различных `e.id` из этого же результата.
2. **Последний прогон (D-04)** — существующий `store.LatestFinishedRuns` (`internal/store/runs.go:254`); перечень источников —
   ```sql
   SELECT code FROM sources ORDER BY code
   ```
3. **Создано и изменено за 30 дней (D-04):**
   ```sql
   SELECT s.code,
          COALESCE(sum((r.metrics->>'created')::int), 0) AS created,
          COALESCE(sum((r.metrics->>'changed')::int), 0) AS changed
   FROM ingest_runs r JOIN sources s ON s.id = r.source_id
   WHERE r.status = 'ok' AND r.finished_at >= $1
   GROUP BY s.code
   ```
   `$1` — момент «сейчас минус 30 дней». Ключи пишет `withCounts` (`internal/ingest/runner.go:537`) у здоровых и принятых прогонов; у прогонов без ключей сумма их пропускает. Разбивки по видам спорта в метриках прогона нет — свежесть только по источнику.
4. **Заявки за 30 дней (D-05):**
   ```sql
   SELECT reason, count(*) FROM error_reports WHERE created_at >= $1 GROUP BY reason;
   SELECT count(*) FROM start_suggestions WHERE created_at >= $1;
   ```
   Таблицы малы (строки старше 90 дней удаляются) — индекса по одному `created_at` нет: полный просмотр дешевле лишнего индекса.

## Запросы записи (наброски)

- **Заявка:** в одной транзакции — проверка пары (`SELECT 1 … WHERE tg_id = $1 AND event_id = $2 AND reason = $3`) → R-3; число строк пользователя с `created_at > $now − 24 ч` ≥ предела → R-4; иначе `INSERT`. После отправки G-1 строка остаётся; при сбое — `DELETE … WHERE tg_id, event_id, reason`. Упрощение: две одновременные заявки разных пар у пользователя на пределе могут обе пройти проверку предела — перебор на одну заявку; чинить — блокировкой отметки пользователя (`SELECT … FOR UPDATE`), если перебор станет заметен.
- **Предложение:** `SELECT expires_at FROM suggest_awaiting WHERE tg_id = $1 FOR UPDATE` → удаление отметки → предел по `start_suggestions` за 24 часа → `INSERT … RETURNING id`; сбой отправки — `DELETE … WHERE id = $1` и отметка заново.
- **Режимы ввода:** вход в предложение удаляет `support_awaiting` и снимает `awaiting_city_until`; `SupportBegin`, `StartUser` и постановка отметки города удаляют `suggest_awaiting` (сейчас они удаляют только отметку поддержки, `internal/store/profiles.go:102`).

## Очистка

- **Предложение (Q-4 контракта):** служебный шаг бота (`Notifier.chores`, `internal/bot/notify.go:233`), не чаще раза в минуту:
  ```sql
  DELETE FROM error_reports WHERE created_at < $1;      -- $1 = сейчас − 90 дней
  DELETE FROM start_suggestions WHERE created_at < $1;
  ```
  Отметки `suggest_awaiting` отдельно не чистятся: строка одна на пользователя, истёкшую удаляет приём ссылки, `/start` и вход в другой режим — как `support_awaiting`.
- **Вопрос владельцу** — Q-4 контракта: 90 дней (рекомендация), 30 дней или без очистки. Срок меняет только код и BOT-08, текст миграции — нет.

## Блокировки

Упрощение: `error_reports.event_id` ссылается на событие, как `favorites`. Вставка заявки берёт по строке события `FOR KEY SHARE` и ждёт конца транзакции записи прогона того же события (запись прогона держит `FOR UPDATE`, `internal/store/events.go:331`) — секунды, раз в шесть часов на источник; по ответу на Q-10 Phase 14 такой потолок принят. Нажатие дожидается и выполняется. `start_suggestions` и `suggest_awaiting` на события не ссылаются и не ждут.

## Совместимость

- Прежний образ с новой схемой работает: новых таблиц он не читает и не пишет. Кнопки `q:` прежний образ встречает ответом «Кнопка устарела — нажмите /start»; `/suggest` и `/quality` для него — неизвестные команды; отметка `suggest_awaiting` им не читается, поэтому ссылка после отката уходит в поиск, а не в поддержку.
- Новый образ со старой схемой не работает в части заявок и предложений. Миграция применяется при deploy, до запуска нового образа (`deploy/README.md`).
- Тестовая очистка базы (`internal/store/storetest/storetest.go:53`) называет таблицы поимённо: `start_suggestions` и `suggest_awaiting` на события не ссылаются, поэтому их нужно добавить в `TRUNCATE` (`error_reports` очищается через `CASCADE` от `events`, но для ясности тоже называется).

## Откат

- Откат — возврат прежнего образа; таблицы остаются на месте.
- Обратная миграция на production не выполняется. Раздел Down нужен для локальной разработки и тестов миграций; он удаляет три таблицы — заявки, предложения и отметки — без возможности восстановления, кроме как из резервной копии.
