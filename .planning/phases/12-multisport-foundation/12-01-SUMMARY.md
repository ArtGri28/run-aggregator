---
phase: 12-multisport-foundation
plan: 01
subsystem: database
tags: [postgres, goose, go, ingest, dedup]

requires:
  - phase: 05-deduplication
    provides: "event.Fold, source_events.fields, журнал слияний"
provides:
  - "колонка events.sport (миграция 00008, утверждённый текст)"
  - "справочник видов спорта event.Sports, event.SportOf, event.SportByCode"
  - "поле вида спорта в event.Event, ingest.SourceEvent и source_events.fields"
  - "запись, чтение, сравнение вида спорта события и выбор его в слитом событии"
affects: [12-02, 12-03]

actuals:
  tokens: 9650
  tasks: 3
  commits: 0

tech-stack:
  added: []
  patterns:
    - "правило «пусто = бег» — в одной функции event.SportOf"
    - "у беговой записи ключа sport в fields и payload нет"

key-files:
  created:
    - migrations/00008_events_sport.sql
    - internal/event/event_test.go
    - internal/store/sport_test.go
    - internal/dedup/sport_test.go
  modified:
    - internal/event/event.go
    - internal/event/fold.go
    - internal/event/fold_test.go
    - internal/ingest/source.go
    - internal/ingest/prepare.go
    - internal/ingest/prepare_test.go
    - internal/store/events.go

key-decisions:
  - "Допустимость кода вида спорта проверяется в Prepare (check); схема проверяет только формат"
  - "В fields ключ sport пишется только у небеговой записи и стоит последним"
  - "Запись без ключа sport читается как бег и участвует в свёртке наравне с остальными"

requirements-completed: [MSP-01, MSP-02, MSP-03, MSP-04, MSP-05, MSP-08, MSP-09]

duration: 20min
completed: 2026-10-09
status: complete
---

# Phase 12 Plan 01: вид спорта события — сквозной срез данных

**Колонка `events.sport` со значением по умолчанию `running`, справочник из пяти видов спорта в `internal/event`, необязательный вид спорта в записи источника и в `fields`; прежние записи и события не переписываются.**

## Что сделано

### Задача 1 — сквозной путь
- `migrations/00008_events_sport.sql` получен извлечением блока sql из раздела «Текст миграции» `12-CONTEXT.md`; побайтное сравнение (`cmp`) с блоком проходит.
- `event.Sport`, `event.SportRunning`, поле `Event.Sport` после `Type`.
- `internal/store/events.go`: колонка `sport` в `selectEventCols` и `scanEventRow`, сравнение в `canonicalFieldsEqual`, параметр `$16` в `insertEventSQL` и `$17` в `updateEventSQL`, значение последним в `eventArgs`; `canonicalize` приводит пустой вид спорта к бегу.
- `TestSportTracer`: запись без вида спорта → событие с `sport = running`, ключа `sport` нет ни в `fields`, ни в `payload`, повторный прогон — `ok`, 0 создано, 0 изменено, строка события (`to_jsonb` целиком) та же.
- Проверка задачи: `TRACER OK`.

### Задача 2 — справочник и запись источника
- `event.Sports` — массив из пяти записей (`running`, `skiing`, `swimming`, `cycling`, `multisport`), включён только бег; `SportOf`, `SportByCode`.
- `ingest.SourceEvent.Sport` — последнее поле, ключ `sport` с `omitempty`.
- `Prepare`: причина отбраковки «неизвестный вид спорта» в `check` (после названия, до даты), поэтому запись с кодом вне справочника не становится победителем серии; событие получает `event.SportOf(rec.Sport)`.
- Тесты: `TestSports`, `TestPrepareSport`, новый случай в `TestPayloadStable` (ключ `sport` — последний, после `address` и `timezone`).
- Проверка задачи: `REGISTRY OK`.

### Задача 3 — `fields`, слитое событие, «до и после»
- `sourceFields.Sport` (`sport`, `omitempty`, последним); `marshalFields` пишет его только у небегового события; `unmarshalFields` возвращает `event.SportOf(...)`.
- `event.Fold`: вид спорта — первое заданное значение по приоритету источников.
- Тесты: `TestFieldsSport`, `TestEventSport`, `TestMigration00008`, `TestMergeSport`, `TestFoldSport`, `TestSportBeforeAfter`; в `sameCanonical` добавлено сравнение `Sport`.
- Проверка задачи: `PLAN 12-01 OK`.

## Итоговые проверки

Полный прогон `go test -p 1 -count=1 -v ./...` на тестовой PostgreSQL 17:

| Показатель | Значение |
|---|---|
| Тестов верхнего уровня объявлено (`go test -list`) | 428 (было 419, новых 9) |
| PASS верхнего уровня | 428 |
| FAIL | 0 |
| SKIP | 0 |
| `gofmt -l internal cmd` | пусто |
| `go vet ./...` | чисто |

Строки из вывода тестов:

```
TRACER sport=running fields_key=absent rerun=unchanged
BEFORE-AFTER OK events=544 records=689 merges=1 candidates=79
```

Новые тесты (9): `TestSports`, `TestPrepareSport`, `TestFoldSport`, `TestFieldsSport`, `TestEventSport`, `TestMigration00008`, `TestMergeSport`, `TestSportTracer`, `TestSportBeforeAfter`.

Литерал прежних байтов в `TestFieldsSport` запущен на `marshalFields` до правки и прошёл; со строкой плана он совпал, расхождения нет.

Проверка того, что новые тесты ловят поломку: при записи ключа `sport` у беговой записи и чтении отсутствующего ключа пустым значением падают `TestFieldsSport`, `TestMergeSport`, `TestSportTracer`, `TestSportBeforeAfter`; правка после проверки возвращена.

## Отступления от плана

1. **Коммитов по задачам нет.** Изменения оставлены в рабочем дереве, коммит делает владелец (договорённость плана). Поэтому `actuals.commits` равно 0, шаги «сначала тест, затем код» подтверждены прогонами, а не историей: в задаче 2 тесты до кода не собирались (`undefined: SportSkiing`), в задаче 3 литерал прежних байтов прошёл на неизменённом коде.
2. **Переформатирование соседних строк `event.Event`.** Doc-комментарий поля `Sport` делит блок полей, и `gofmt` перевыровнял семь соседних строк (`Timezone` … `Type`); содержание их не изменилось.
3. **`TestMergeSport`: предусловие проверяется запросом `fields ? 'sport'`**, а не по тексту: JSONB хранит ключи в своём порядке, положение ключа в тексте колонки ничего не значит. Порядок ключей при записи закрепляет `TestFieldsSport`.
4. **Вспомогательный тип `calibSource` в `internal/dedup/sport_test.go`**: `fakeSource` отдаёт одну политику, а плану нужна политика настоящего источника.

Остальное — по плану. `STATE.md`, `ROADMAP.md`, `REQUIREMENTS.md` не изменялись.

## Пометки «Упрощение:»

- `internal/dedup/sport_test.go`, doc-комментарий `TestSportBeforeAfter`: копия данных — каталог из сохранённых страниц источников, а не копия production; состояние «до» получено откатом схемы. На production то же сравнение делает владелец при выкате (план 12-03).

## Что не тронуто

`git diff --stat fa07e2e -- internal/bot internal/ingest/sources internal/normalize deploy docker-compose.yml go.mod go.sum` — пусто. Миграции `00001`–`00007`, код и прежние тесты `internal/dedup`, прежние файлы `internal/store` кроме `events.go`, `.env.example` — без изменений (проверка в команде задачи 3). В `prepare_test.go` только добавления.

## Known Stubs

Нет.

## Дальше

- 12-02: отбор выдачи по включённым видам спорта (`listWhere`, `futureListed`, карточка события) — `event.Sports[].Enabled` пока никем не читается.
- 12-03: миграция на production по разрешению владельца; сравнение «до и после» на копии production.

## Self-Check: PASSED

Файлы из `key-files` существуют; три команды проверки задач напечатали `TRACER OK`, `REGISTRY OK`, `PLAN 12-01 OK`.
