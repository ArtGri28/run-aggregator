---
phase: 03-first-source
plan: 03
subsystem: normalize (справочник регионов, правила по фикстуре) + ingest (Prepare, Evaluate)
tags: [normalize, regions, prepare, quality, golden, d-05, d-07, d-08, d-09, d-11, d-14]
status: complete

requires:
  - "03-02: Parse, Prepare, Stats, SourceEvent с RegionSlugs/Regions, edge-rows.html, флаг update"
  - "Phase 2: internal/normalize"
provides:
  - "internal/normalize/regions.go — RegionKind, ClassifyRegion (89 субъектов РФ, иностранные названия), ClassifyPlace (уточнение по городу, D-14)"
  - "internal/normalize — Country различает иностранную и нераспознанную явную страну (ME-04); префиксы г.о./п./пос./с./д. (LO-07); CityKey без «--» (LO-04); римские цифры ≤ 299 без M/D; K/К слитно с числом (ME-05); мили; склейка «100 200 400 м»; MaxPlausibleDistanceM"
  - "internal/ingest/prepare.go — Stats: UnrecognizedRegion, MultiRegion, DroppedDistances, Empty, Unrecognized; Stats.Metrics()"
  - "internal/ingest/quality.go — RunStatus, Thresholds, DefaultThresholds, Baseline, Evaluate, median"
  - "testdata/prepared.golden.json — что попадёт в каталог из фикстуры"
affects: [03-04-runs-observe, 03-05-fetch-cli, 03-06-first-run, 03-07-decision, phase-5-second-source]

actuals:
  tokens: 34600   # chars/4: новые файлы 110741 Б (из них golden JSON 74920 Б) + git diff 27615 Б
  tasks: 2
  commits: 0      # коммиты делает владелец вручную
  plan_head_before: 9a7a5880ec241b49650e2a1c7b326bf253aff27f

tech-stack:
  added: []
  patterns:
    - "Справочник субъектов — Go-таблица в normalize, сравнение по regionKey (служебные слова отброшены), индексы в init()"
    - "Оценка прогона — чистая функция Evaluate(Stats, Baseline, Thresholds); failed выставляет вызывающий код"
    - "Метрики прогона — Stats.Metrics() с фиксированным порядком полей и долями пустых полей (4 знака)"

key-files:
  created:
    - internal/normalize/regions.go
    - internal/normalize/regions_test.go
    - internal/ingest/quality.go
    - internal/ingest/quality_test.go
    - internal/ingest/sources/marathonec/prepared_test.go
    - internal/ingest/sources/marathonec/testdata/prepared.golden.json
  modified:
    - internal/normalize/place.go
    - internal/normalize/place_test.go
    - internal/normalize/normalize.go
    - internal/normalize/normalize_test.go
    - internal/normalize/distance.go
    - internal/normalize/distance_test.go
    - internal/ingest/prepare.go
    - internal/ingest/prepare_test.go

key-decisions:
  - "D-14: составные термины «Москва и Московская область», «СПБ и Ленинградская область» — варианты написания Московской и Ленинградской области; ClassifyPlace(region, city) заменяет субъект вокруг города федерального значения на сам город, если город — Москва, Санкт-Петербург или Севастополь"
  - "D-14: запись с несколькими областями (len(RegionSlugs) > 1) — Stats.MultiRegion, region пуст, страна по политике; не входит ни в Empty[region], ни в UnrecognizedRegion"
  - "Все 83 термина таксономии «Область» фикстуры классифицируются как субъекты РФ без новых синонимов, кроме двух составных (TestClassifyRegionMarathonec)"
  - "Мили (миль/мили/миля, 1609.344 м) распознаются в Distances: реальная строка фикстуры «100 миль, 14 км, 50 миль, 55 км» иначе теряла две дистанции"
  - "Слаговый индекс ClassifyRegion (допущение A1) не делался: по D-14 Parse уже переводит слаг в название через data-filters"

metrics:
  started: 2026-09-29T16:13:00Z
  completed: 2026-09-29T16:27:00Z
  duration: "~14 мин"
---

# Phase 3 Plan 03: справочник субъектов РФ, Prepare по D-07/D-09/D-11/D-14 и Evaluate — итоги

Справочник из 89 субъектов РФ классифицирует все 83 названия областей Марафонца. Составные «Москва и …» и «СПБ и …» уточняются по городу. `Prepare` считает нераспознанные, иностранные, многообластные записи, пустые поля и отброшенные дистанции. Чистая функция `Evaluate` выносит решение ok/suspect по MVP §5.4 с учётом D-05. На фикстуре результат такой: 133 записи в каталоге с RU при `RUOnlyFallback`, 0 при `NoFallback`, 0 нераспознанных областей.

## Выполнено

| Задача | Что | Коммит |
|---|---|---|
| 1 (tdd) | `regions.go` и тесты; правки `place.go`, `normalize.go`, `distance.go` и их тестов. Результат проверки — `NORMALIZE OK` | не закоммичено: коммиты делает владелец |
| 2 (tdd) | `prepare.go` (регионы, дистанции, метрики), `quality.go`, тесты, `prepared_test.go`, golden. Результаты проверок — `PREPARE OK`, `TRACER STILL OK` | не закоммичено |

## Результаты проверок

- `NORMALIZE OK`: все 12 обязательных тестов в статусе PASS, дополнительно `TestClassifyRegionMarathonec` и `TestClassifyPlace`. `normalize` не импортирует net, os, database и сторонние пакеты.
- `PREPARE OK`: все 13 обязательных тестов в статусе PASS; golden — валидный JSON с `"country_code": "RU"`; `time.Now` в `prepare.go`/`quality.go` не используется.
- `TRACER STILL OK`: `TestRunFixture` на PostgreSQL 17 даёт `created=133`, затем `unchanged=133`; `TestRunStructureNotFound` — PASS, без SKIP.
- Проверки `STATIC OK` из 03-02 повторены и зелёные: go mod verify, три прямые зависимости, нет `html/charset`, нет слова-индикатора антибота в Go, SQL есть только в `internal/store`.
- `go test -p 1 -count=1 ./...` без `TEST_DATABASE_URL`: 34 PASS, 8 SKIP (тесты с БД), 0 FAIL. С `TEST_DATABASE_URL`: 42 PASS, 0 SKIP, 0 FAIL (считались только тесты верхнего уровня).
- `go vet ./...`, `gofmt -l .` (пусто), `git diff --check` — чисто. Ссылок на AI/Claude/GSD в коде нет.
- SHA-256 фикстуры по-прежнему `dd4a58d3…3ddf`. `calendar-beg.golden.json` не менялся: Parse от normalize не зависит.

## Golden `prepared.golden.json` (новый файл)

Новый файл, поэтому регенерации нет. Это Prepare(Parse(фикстура), 2026-09-29 12:00 UTC, RUOnlyFallback):

- Parsed 133, Valid 133, Rejected 0, Expired 0, DateOutOfRange 0, OutOfScope 0 — совпадает с README («Даты (D-11)»: 0 прошедших и 0 дальше 3 лет).
- UnrecognizedRegion 0, Unrecognized пуст, поэтому для санити-отчёта 03-06 **нераспознанных областей на фикстуре нет**.
- MultiRegion 2: `24926` (27 областей, город «Вся Россия») и `87418` («Майкоп, Солохаул»).
- Empty: distances 1. Это `81654` «Часовой забег «Осенний вальс»» с единственной дистанцией «1 час» (забег на время, по MVP §16 это не дистанция). Пустых city/region нет.
- DroppedDistances 0.
- Регионы после классификации: Москва 9, Санкт-Петербург 6 (у обоих `timezone` = Europe/Moscow), Московская область 10, Ленинградская 5 и т. д.; у 118 записей timezone пуст, так как в `cityZones` есть только Москва и СПб — поведение Phase 2.
- `NoFallback` (D-07): ни одна из 133 записей не попадает в каталог, OutOfScope = Valid.

## Отложенные находки Phase 2: что взято и почему

Реальные значения фикстуры: дистанции `N км` с десятичной точкой, `50 миль`, `100 миль`, `1 час`. `5K` и склеек нет, латинских названий целиком заглавными нет, префиксов города нет, прошедших строк 0.

| Находка | Решение | Основание |
|---|---|---|
| Римские цифры (MIX RUN) | **Взята** | В фикстуре реальное ложное срабатывание: «City Trail 6 X-Race» давало `city trail 6 race`, теперь `city trail 6 x race`. Регулярное выражение ≤ 299 без M/D, одиночные I/V/X снимаются только в названии с кириллицей |
| ME-04 (явная страна) | **Взята** | Нужна для D-09: `Country` отличает известную иностранную (через `ClassifyRegion` → `RegionForeign`) от нераспознанной, которая идёт по политике |
| ME-05 `5K`/`10К` | **Взята**, хотя в фикстуре нет | Плановый must-have и строка `edge-rows.html` («MIX RUN», «5K, 10K»). Правило дешёвое: одна альтернатива в `distanceRe`, K только слитно с числом, «5 K» и «5кросс» не распознаются. Полезно для ПроБЕГ (Phase 5) |
| Склейка «100 200 400 м» | **Взята**, хотя в фикстуре нет | На Марафонце склейка невозможна: каждое значение — отдельный `<span>` через «, ». Must-have и строка `edge-rows.html`. Страховка на любой случай — отсечение > 1000 км в Prepare (`DroppedDistances`) |
| LO-07 префиксы города | **Взята**, хотя в фикстуре нет | Правка списка из 9 строк в коде, который план и так менял; важно для деревень у ПроБЕГ |
| LO-04 `CityKey` с «--» | **Взята** | Транслитерация по словам, пустые слова отбрасываются; контракт city_key |
| Мили (не из ревью) | **Добавлена** (Rule 2) | Реальная строка `68272` «100 миль, 14 км, 50 миль, 55 км» давала [14000 55000], теперь [14000 55000 80467 160934] |
| «1 час» | Без изменений | Забег на время — не дистанция (MVP §16); считается в Empty[distances] |
| LO-06 | Без изменений | Уже закрыто в 03-02: дистанции только из столбца, при пустом — из названия |
| Слаговый индекс в ClassifyRegion (A1) | **Не взят** | Заменён D-14: Parse отдаёт русское название из `data-filters`. Слаг без названия (`neizvestnaya-oblast` в edge) остаётся нераспознанным и попадёт в отчёт |

## TDD

- Задача 1: RED был настоящим. После добавления `regions.go` и константы падали 8 случаев `TestDistances` (5K, 10К, склейка, мили), 4 — `TestTitleNorm` (MIX RUN, Run I Love, Забег 5K, X-Race), 6 — `TestCity` (префиксы), `TestCityKey` («moskva--siti»), 3 — `TestCountry` (Russian Federation, Rossiya, Атлантида). RED по регионам слабый: `regions_test.go` и `regions.go` написаны вместе, поэтому до реализации была только ошибка компиляции.
- Задача 2: сначала поля `Stats` и заглушки `quality.go`, после этого RED был поведенческим. Падали `TestPrepareRegions` (область не классифицирована, Беларусь в каталоге, счётчики 0), `TestPrepareDistancePlausibility`, `TestStatsMetrics`, `TestEvaluate`, `TestMedian`, `TestPreparedEdges` (Беларусь, нераспознанные) и `TestPreparedGolden` (нет файла). Слабый RED: `TestPrepareExpired`, `TestPrepareNoFallback`, `TestPrepareLongDescription`, `TestPrepareDeterministic` прошли сразу — это поведение уже было в 03-02. Строки MIX RUN и склейки в `TestPreparedEdges` прошли благодаря задаче 1.
- Проверка тестов временными мутациями (исходники восстановлены, тесты снова зелёные):
  - убран составной синоним — падают 3 теста;
  - отключено уточнение по городу — 1;
  - знаменатель D-11 заменён на Parsed — 1;
  - убрана причина D-05 — 1;
  - убран счётчик MultiRegion — 3;
  - отключена иностранная ветка в `Country` — 1.

## Deviations from Plan

### По D-14 и фактам фикстуры

**1. [D-14] Составные названия и город.** План предполагал `ClassifyRegion(s)` и слаговый индекс (A1). Сделано иначе: «Москва и Московская область» и «СПБ и Ленинградская область» — синонимы в записях Московской и Ленинградской области, так что `ClassifyRegion` без города даёт субъект РФ. Для санити-отчёта 03-05 этого достаточно. Добавлена `ClassifyPlace(region, city)`: если город — Москва, Санкт-Петербург или Севастополь, а область — окружающий его субъект, результат — сам город. `Prepare` вызывает `ClassifyPlace`; сигнатура `ClassifyRegion(s string)` не изменилась. Проверка по городу срабатывает и на «Московская область» + «Москва», а не только на составной термин. «Тверская область» + «Москва» не меняется.

**2. [D-14] Записи с несколькими областями.** Добавлено поле `Stats.MultiRegion` и ключ `multi_region` в `Metrics()`. Таких записей нет в `Empty["region"]` и в `UnrecognizedRegion`, поэтому они не влияют ни на порог D-09, ни на рост пустых полей. Если хотя бы одна из областей иностранная, это не проверяется: в таксономии все 83 термина российские.

**3. Прочие нестандартные написания D-14** («Республика Северная Осетия», «… АО», «Марий-Эл», «Чувашская Республика», «Кемеровская область», «Республика Саха (Якутия)») уже совпадают со справочником research через `regionKey`. Новые синонимы не понадобились; это закреплено тестом по всем 83 терминам.

### Автоисправления

**4. [Rule 2] Мили в `Distances`.** Единицы `миль`/`мили`/`миля` (1 миля = 1609.344 м) нужны для реальной строки фикстуры, см. таблицу выше.

**5. `Metrics()`: дополнительные ключи.** Кроме ключей из плана добавлены `out_of_scope` и `multi_region`: `out_of_scope` сравнивается порогом в `Evaluate`, `multi_region` введён по D-14.

**6. `TestPrepareValidate`** расширен проверкой примеров `external_id` в `Rejects`, второй тест не создавался. Существующий тест 03-02 даёт Rejected = 5 вместо 4 из текста плана: там есть ещё случай «позже трёх лет».

**7. Дополнительные тесты** — `TestClassifyRegionMarathonec`, `TestClassifyPlace` и случаи в `TestEvaluate`: порог из параметра меняет текст причины; нераспознанные области проверяются и на первом прогоне; рост пустых полей до накопления базы не проверяется.

## Для следующих планов

- 03-04: `Metrics()` возвращает `{"valid","expired","date_out_of_range","out_of_scope","unrecognized_region","multi_region","dropped_distances","empty":{"city","region","distances"}}`. Поле `Baseline.EmptyShare` заполняется из `empty` прогонов базы.
- 03-05, санити-отчёт: для классификации достаточно `ClassifyRegion(Region(…))`, составные термины получат «в перечне РФ». У записей с несколькими областями `Region` пуст, поэтому в отчёте нужна отдельная строка, иначе они попадут в «область пуста». Для многообластной строки `24926` город — «Вся Россия» (`city_key` `vsya-rossiya`), это стоит показать владельцу.
- `normalize.Timezone` знает только Москву и СПб: у 118 из 133 событий timezone пуст (поведение Phase 2, вне этого плана).

## Known Stubs

Нет.

## Threat Flags

Новой поверхности нет. Меры плана закрыты:
- T-03-12: NoFallback проверен тестом и golden; иностранная область всегда вне каталога; пороги 10 % и 20 %.
- T-03-13: D-05, медиана, доли, рост пустых полей.
- T-03-14: склейка и отсечение > 1000 км.
- T-03-16: `TestPrepareLongDescription`.

## Self-Check: PASSED

- Все 6 созданных и 8 изменённых файлов на месте (`git status --short`).
- Коммитов нет по правилу проекта: `commits: 0` при `plan_head_before` = HEAD `9a7a588`.
