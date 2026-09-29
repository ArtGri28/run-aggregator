---
phase: 03-first-source
plan: 02
subsystem: ingest (contract, Parse, Prepare, Runner) + store
tags: [tracer, parse, golden, x-net-html, idempotency, d-02, d-10, d-11, d-13, d-14]
status: complete

requires:
  - "03-01: testdata/calendar-beg.html (133 строки post-row) и README (карта столбцов, SHA-256)"
  - "Phase 2: internal/store (UpsertSourceEvent, SaveEvent, SetMatch), internal/normalize"
provides:
  - "internal/ingest/source.go — контракт Source/SourceEvent/RawDoc/Policy, ErrStructureNotFound"
  - "internal/ingest/sources/marathonec/parse.go — Parse на golang.org/x/net/html (D-13 ссылки, D-14 области)"
  - "internal/ingest/prepare.go — Prepare (Validate + Normalize), Prepared, Stats, ReasonStat"
  - "internal/ingest/runner.go — Runner.Run: Fetch → Parse → Prepare → UpsertSourceEvent / SaveEventFor | SetMatch out_of_scope"
  - "internal/store/events.go — SaveEventFor (одна транзакция), saveEventTx"
  - "golden calendar-beg.golden.json и синтетические edge-*.html"
affects: [03-03-normalize-quality, 03-04-runs-observe, 03-05-fetch-cli, phase-5-second-source]

actuals:
  tokens: 32700   # chars/4 по созданным файлам (124410 Б, из них golden JSON ~78 КБ) и диффу go.mod/go.sum/events.go (6329 Б)
  tasks: 2
  commits: 0      # коммиты делает владелец вручную
  plan_head_before: c6545c6913a4e519b5480ccaa1d572dae1534077

tech-stack:
  added:
    - "golang.org/x/net v0.59.0 (прямая; пакеты html и html/atom; go.sum h1:5zfYln+w5XCxwrnMMJPufRgNoXEaGxl0wo5GqPXyues= совпадает с планом); indirect x/sync v0.23.0, x/text v0.42.0"
  patterns:
    - "Столбцы таблицы ищутся по data-name у <th>, строки — tr с id post-row- через Node.Descendants внутри найденной таблицы"
    - "Структурная поломка (нет таблицы с 6 обязательными столбцами, нет словаря областей data-filters, 0 строк) → ErrStructureNotFound; слов-индикаторов в коде нет"
    - "Тесты internal/ingest изолируются уникальным кодом источника, без SQL и TRUNCATE; guard _test в TestMain"

key-files:
  created:
    - internal/ingest/source.go
    - internal/ingest/prepare.go
    - internal/ingest/prepare_test.go
    - internal/ingest/runner.go
    - internal/ingest/runner_test.go
    - internal/ingest/sources/marathonec/parse.go
    - internal/ingest/sources/marathonec/parse_test.go
    - internal/ingest/sources/marathonec/testdata/calendar-beg.golden.json
    - internal/ingest/sources/marathonec/testdata/edge-no-table.html
    - internal/ingest/sources/marathonec/testdata/edge-empty-table.html
    - internal/ingest/sources/marathonec/testdata/edge-rows.html
  modified:
    - go.mod
    - go.sum
    - internal/store/events.go

key-decisions:
  - "D-13 в Parse: берётся только ссылка ячейки названия; хост marathonec.ru и поддомены → SourceEvent.URL, registration_url пуст; внешняя → registration_url, URL = doc.URL (страница календаря); #new_tab срезается, &#038; раскодирует парсер, vk.com/away.php сохраняется"
  - "D-14 в Parse: SourceEvent получил поля RegionSlugs []string (region_slugs) и Regions []string (regions) — все области строки в порядке источника; Region = название при одной области, \"\" при нескольких; слаг без названия в data-filters → название = слаг"
  - "Нет словаря областей в data-filters или любого из 6 используемых столбцов → ErrStructureNotFound (D-02 + D-14)"
  - "TypeLabel — слаг скрытого столбца hf_tax_racetype (trejl/shosse), normalize.Type уже знает оба"
  - "SaveEventFor возвращает (created=true, changed=false) для нового события; для связанного — статус из БД (Pitfall 10)"

metrics:
  started: 2026-09-29T16:02:00Z
  completed: 2026-09-29T16:12:00Z
  duration: "~15 мин"
---

# Phase 3 Plan 02: Tracer фикстура → PostgreSQL 17, golden Parse Summary

Реальная страница Марафонца проходит насквозь `Parse` (x/net/html) → `Prepare` → `UpsertSourceEvent`/`SaveEventFor` → PostgreSQL 17: первый прогон создаёт 133 события, второй — 0 созданных, 0 изменённых, 133 без изменений; `Parse` закреплён golden-тестом и синтетическими крайними случаями, ссылки и области разбираются по D-13/D-14.

## Выполнено

| Задача | Что | Коммит |
|---|---|---|
| 1 (tracer) | x/net v0.59.0; контракт `source.go`; `Parse`; `Prepare`; `saveEventTx` + `SaveEventFor`; `Runner.Run`; `runner_test.go` — `STATIC OK`, `TRACER OK` | не закоммичено: коммиты делает владелец |
| 2 (tdd) | `parse_test.go`, golden, `edge-*.html` — `PARSE OK` | не закоммичено |

## Результаты проверок

- Tracer: `TRACER created=133 unchanged=0 out_of_scope=0 expired=0 rejected=0`; второй прогон `created=0 changed=0 unchanged=133`, `upserted=133` оба раза (ROADMAP критерий 2).
- Golden: 133 записи = 133 уникальных `post-row`; 28 со своей ссылкой (URL события, без регистрации), 105 с `registration_url`; `#new_tab` и `&#038;` в выходе нет; `trejl` 88 / `shosse` 45; 2 строки с несколькими областями (`24926` — 27 слагов, `87418` — 2), у них `region` пуст. 5 случайных строк (86140, 72430, 89600, 63367, 72065) сверены с HTML и словарём `data-filters` — совпадают.
- SHA-256 фикстуры `dd4a58d3…3ddf` равен строке README; `git status` по фикстуре пуст (D-12).
- `go test -p 1 ./...` с `TEST_DATABASE_URL`: 27 PASS, 0 SKIP, 0 FAIL. Без неё: 19 PASS, 8 SKIP (тесты с БД), 0 FAIL.
- `go vet ./...`, `gofmt -l .`, `git diff --check`, `go mod verify` — чисто.

## TDD (задача 2)

RED был слабым: `Parse` написан в задаче 1 (tracer), поэтому до golden падал только `TestParseGolden` (нет файла), а `TestParseStructureNotFound`, `TestParseEdges`, `TestCleanText` прошли сразу. Чтобы проверить, что тесты что-то ловят, сделаны 6 временных мутаций `parse.go`: не срезать `#new_tab`, не считать поддомен своим, не удалять U+0000, заполнять `Region` при нескольких областях, не проверять длину 2048 — каждая роняет тесты; мутация «любой элемент вместо `td`» не ловится (на этой разметке эквивалентна). Исходный файл восстановлен. `TestPrepareValidate` написан после `Prepare`, тоже не по TDD.

## Deviations from Plan

### Решения владельца D-13/D-14 вместо допущений A5/A1

**1. [D-13] Ссылки: вместо приоритета хостов — только ссылка названия**
- План (п. 3, TestParseEdges): выбирать `registration_url` среди всех ссылок строки по приоритету russiarunning > myrace > reg.place > первая внешняя.
- Сделано: только первая ссылка ячейки названия; своя (marathonec.ru и поддомены) → `URL`, `registration_url` пуст; внешняя → `registration_url`, `URL` = `doc.URL`. Ссылки в других ячейках игнорируются — в `edge-rows.html` строка «Ссылка russiarunning» содержит в ячейке города ссылки `javascript:`, marathonec.ru и example.org, и результат — ссылка russiarunning из названия. Проверки `javascript:`, `data:`, > 2048 применены к ссылке названия (отдельные строки edge). Добавлены случаи vk away с `&#038;`, относительная ссылка, поддомен `WWW.Marathonec.ru`, строка без ссылки.
- Для URL события взят `doc.URL`, а не константа: `const CalendarURL` появится в `marathonec.go` в 03-05, а Fetch передаёт этот же адрес.

**2. [D-14] Область: слаг → название, новые поля SourceEvent**
- План: `Region` — текст ячейки «как есть»; `SourceEvent` без дополнительных полей.
- Сделано: `Region` — русское название из `data-filters` (`tax_oblast.terms`); добавлены `RegionSlugs`/`Regions` (json `region_slugs`/`regions`, omitempty), чтобы 03-03 считала строки с несколькими областями отдельно (`len(RegionSlugs) > 1`) и видела слаги для санити-отчёта. Контракт `SourceEvent` в таблице Artifacts надо читать с этими двумя полями.
- Добавлен структурный отказ: нет словаря областей → `ErrStructureNotFound` (тест `no region names`).

### Прочее

**3. Stats без полей 03-03.** В `Stats` объявлены только заполняемые сейчас поля (Parsed, Valid, Rejected, Expired, DateOutOfRange, OutOfScope, Rejects); `UnrecognizedRegion`, `DroppedDistances`, `Empty`, `Unrecognized` добавит 03-03 вместе с логикой.

**4. [Rule 2] Добавлен `internal/ingest/prepare_test.go` (`TestPrepareValidate`).** Must-have ING-03 (отбраковка без id/названия/с плохой датой/дубликат, +3 года → rejected и date_out_of_range, раньше −1 дня → expired, NoFallback → out_of_scope) иначе не проверялся бы: в фикстуре 0 отбракованных. Имя совпадает с тестом из списка 03-03 — 03-03 его расширит.

**5. Строки edge-rows.html.** В файле 22 строки `post-row`; кроме перечисленных в плане есть строка из двух ячеек (недостающие поля → ""), строка с двумя областями, строка со слагом области не из словаря.

## Для плана 03-03

- Области в golden — русские названия таксономии, в том числе нестандартные: «Москва и Московская область», «СПБ и Ленинградская область», «Республика Северная Осетия», «Марий-Эл»/«Республика Марий Эл», «… АО» (README, «Значения для normalize»). Классификация — задача 03-03 (D-14).
- Строка с несколькими областями: `Region == ""`, `len(RegionSlugs) > 1`; `Regions` — их названия.
- `Prepare` сейчас кладёт в `Event.Region` `normalize.Region(rec.Region)` без классификации.

## Known Stubs

Нет. `Runner.Run` не вызывается ни одной подкомандой до 03-05 — так задумано планом.

## Threat Flags

Нет новой поверхности сверх threat_model плана: T-03-06 (html.Parse, Descendants), T-03-07 (http/https, ≤ 2048, внешний хост), T-03-08 (SQL только в store, проверено grep), T-03-09 (`clean`), T-03-10 (`SaveEventFor` в одной транзакции), T-03-11 (guard `_test`), T-03-SC (хеш go.sum, `go mod verify`, без html/charset) — закрыты.

## Self-Check: PASSED

- Все 11 созданных файлов и 3 изменённых существуют (`git status --short`).
- Коммитов нет по правилу проекта (коммиты делает владелец), `commits: 0` при `plan_head_before` = HEAD `c6545c6`.
