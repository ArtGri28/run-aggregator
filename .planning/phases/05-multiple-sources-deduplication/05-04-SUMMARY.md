---
phase: 05-multiple-sources-deduplication
plan: 04
subsystem: ingest/sources/probeg, cmd/aggregator
status: complete
tags: [probeg, source-adapter, series, group_key, country, fixture, golden]
requires: [05-01]
provides:
  - "пакет internal/ingest/sources/probeg: Code, CalendarURL, CrawlDelay, Source, New, Policy (NoFallback, 30 с, OutOfScopeMax 0.40), Fetch (один GET), Parse"
  - "testdata: year_ahead-2026-09-30.html (SHA-256 d0166c26…d443189), README.md, year_ahead.golden.json, prepared.golden.json — фикстура и для калибровки 05-07"
  - "newSources = marathonec + probeg"
affects: [05-05, 05-07, 05-08]
tech-stack:
  added: []
  patterns: ["год даты по RawDoc.FetchedAt в Parse", "страна по строке места + IsRFCity только для голого города", "go test -overlay для проверки при чужом незавершённом коде"]
key-files:
  created:
    - internal/ingest/sources/probeg/probeg.go
    - internal/ingest/sources/probeg/parse.go
    - internal/ingest/sources/probeg/probeg_test.go
    - internal/ingest/sources/probeg/parse_test.go
    - internal/ingest/sources/probeg/prepared_test.go
    - internal/ingest/sources/probeg/testdata/README.md
    - internal/ingest/sources/probeg/testdata/year_ahead-2026-09-30.html
    - internal/ingest/sources/probeg/testdata/year_ahead.golden.json
    - internal/ingest/sources/probeg/testdata/prepared.golden.json
  modified:
    - cmd/aggregator/main.go
    - cmd/aggregator/main_test.go
decisions:
  - "OutOfScopeMax ПроБЕГа = 0.40: доля out_of_scope на фикстуре 155/553 = 28,0 %, +0,05 = 0,33 ≤ 0,40 ≤ 0,50"
  - "Ссылка названия — только /event/<n>/ одним сегментом: кнопка /event/<n>/reg/ («Зарегистрироваться через наш сайт») не событие и не участник серии"
  - "Место из нескольких компонентов с нераспознанным первым — Region = компонент, Country пусто (IsRFCity только для голого города, одного компонента)"
metrics:
  duration: "~45 мин"
  completed: 2026-10-01
actuals:
  tokens: 16000
  tasks: 3
  commits: 0
plan_head_before: 86bdc42
requirements-completed: [ING-12, DDP-05]
---

# Phase 5 Plan 04: Источник ПроБЕГ Summary

Пакет `probeg`: один GET `https://probeg.org/races/year_ahead/` за прогон с Crawl-delay 30 с; `Parse` выводит год по `FetchedAt` (до 07.09.2027 без Expired), раскрывает 15 групповых строк в 943 записи с `GroupKey` (191 серия, 145 без города), пропускает 2 отменённые, страну ставит по первому компоненту места или справочнику `IsRFCity` для голого города, чистит дистанции от скобок и неходовых видов. Источник зарегистрирован в `newSources`, tracer проходит настоящий Runner до PostgreSQL.

## Задачи

| # | Задача | Результат | Коммит |
|---|--------|-----------|--------|
| 1 | Tracer: httptest → Fetch → Parse → Run (suspect) → Accept → PG; newSources | `TRACER probeg first=suspect created=1 distances=[1000 2000 10000 20000] source=https://probeg.org/event/83327/` | не коммитилось (владелец коммитит вручную) |
| 2 | Полный Parse, фикстура + SHA-256 + golden | TestParseDates/Distances/Rows/FixtureShape/Golden/Edges, TestPlaceVerdicts — PASS | не коммитилось |
| 3 | Prepare golden, ворота OutOfScopeMax, санити-отчёт | TestPreparedGolden/Stats, TestPolicyGate, TestSanityReportFixture — PASS | не коммитилось |

Tracer feedback gate: `<verify>` задачи 1 повторён после реализации (с overlay, см. ниже) — прошёл; после появления рабочего `runner.go` от 05-05 повторён без overlay — прошёл.

## Числа фикстуры

- 379 строк = 362 одиночных + 15 групповых + 2 отменённых (72982, 73219).
- Parse: 1305 записей (362 + 943 в сериях), 191 ключ серии, из них 145 без города.
- Prepare (now = 2026-09-30 12:00 UTC): Parsed 1305 = Valid 553 + Collapsed 752; Rejected 0, Expired 0, DateOutOfRange 0, UnrecognizedRegion 0; OutOfScope 155 (Беларусь 10 + серии без города 145) — 28,0 %.
- Санити-отчёт: `Серии (group_key): ключей — 191 | записей — 943 | ключей без города — 145`; иностранная — 10.
- **OutOfScopeMax = 0.40** (оставлен ориентир плана): 0,280 + 0,05 = 0,33 ≤ 0,40 ≤ 0,50. С общим порогом 0,20 каждый прогон был бы suspect (TestPolicyGate это проверяет). 05-08 сверяет с первым реальным прогоном.

## Страна по месту (TestPlaceVerdicts, по строкам, без отменённых — 377)

- Префикс субъекта РФ: **195** строк (в т. ч. «г. Москва» ×4, «г. Санкт-Петербург» ×8, «Республика Крым» ×5).
- Префикс иностранной страны: **10** строк — только Беларусь ×10 (Колбовичи, Малорита, Гомель, Пышки, Любань, Раубичи, Клиденяты, Молодечно, Вязье, Минск).
- Голый город → RU: **167** строк, 41 место:
  - субъекты-города: Москва ×42, Санкт-Петербург ×9;
  - по справочнику городов: Архангельск ×1, Великий Новгород ×2, Волгоград ×6, Вологда ×2, Воронеж ×1, Екатеринбург ×7, Иркутск ×6, Казань ×2, Кемерово ×4, Краснодар ×1, Красноярск ×6, Курск ×1, Липецк ×1, Магадан ×1, Мурманск ×1, Нижний Новгород ×3, Новосибирск ×5, Омск ×4, Оренбург ×2, Пенза ×3, Пермь ×3, Псков ×1, Ростов-на-Дону ×19, Рязань ×2, Самара ×2, Саратов ×2, Смоленск ×1, Ставрополь ×1, Тамбов ×2, Тверь ×2, Томск ×4, Тула ×4, Тюмень ×1, Ульяновск ×1, Уфа ×2, Хабаровск ×2, Челябинск ×6, Якутск ×1, Ярославль ×1.
- Голый город → не RU: **0** строк (отклонённых нет).
- Групповые строки без города: **5** строк → 145 ключей серий без города (вне каталога, D-14).
- Ни одного голого города, явно иностранного или спорного, в снимке нет; `ru_cities_ambiguous.txt` не менялся.

## Deviations from Plan

1. **[Rule 1 - Bug] Кнопка `/event/<n>/reg/` превращала строку в «серию».** Событие 81983 имеет в ячейке названия кнопку «Зарегистрироваться через наш сайт» со ссылкой `/event/81983/reg/`; по префиксу `/event/` строка становилась групповой (2 лишних ключа и запись без id). Ссылкой события считается только `^/event/([^/]+)/$`; случай добавлен в TestParseEdges. Ссылка `/event/<n>/reg/` в `RegistrationURL` не берётся (правило «только текст „Регистрация“»).
2. **[Rule 2 - Safety] `IsRFCity` только для голого города.** План (Pattern 1) применял справочник к последнему компоненту при любом нераспознанном первом. Сделано строже: несколько компонентов с нераспознанным первым → `Region` = компонент, `Country` пусто (видно в отчёте как нераспознанная область). На фикстуре не срабатывает (все префиксы — субъекты РФ или Беларусь); проверено TestParseEdges («Минская область, Минск»).
3. **[Дополнительно, по указанию оркестратора] TestPlaceVerdicts** — список голых городов с вердиктом и счётчики по виду места в `-v` выводе; падает, если голый город фикстуры отклонён (новый случай требует решения по справочнику).
4. **[Окружение] Тестовая БД** `agg_w1a_test` вместо `aggregator_test`, `docker compose up` не выполнялся (контейнер уже запущен) — указание оркестратора.
5. **[Окружение] Параллельный 05-05:** на момент задач 1–2 `internal/ingest/runner.go` не компилировался (смена подписи `SaveEventFor`). Проверка шла через `go test -overlay` с копией `runner.go` в каталоге сессии (передан `event.FoldConfig{}`), файлы 05-05 не тронуты. К задаче 3 05-05 обновил `runner.go`; финальная проверка — без overlay.
6. **[Окружение] Коммиты** не создавались — правило владельца; `commits: 0`.
7. Ключ `Stats.Collapsed` в golden — `"Collapsed"` (тег `json:",omitempty"` от 05-01), а не `"collapsed"`, как в тексте проверки плана.

## Проверка

- `gofmt -l cmd internal migrations` — пусто; `go vet ./...`, `go mod verify` — ok; прямые зависимости прежние; SQL вне `internal/store` нет; `git diff --quiet -- internal/ingest/sources/marathonec/testdata` — 0.
- Фикстура `cmp` с samples и SHA-256 `d0166c26…d443189` совпали; в golden нет `(+N`, «человек», `72982`; есть `07.09.2027`.
- `TEST_DATABASE_URL=…/agg_w1a_test go test -p 1 -count=1 -timeout 5m ./...` — все пакеты ok, `--- SKIP` 0 (на момент проверки и с текущим кодом 05-05).

## Known Stubs

Нет.

## Вопросы владельцу

- «Республика Крым» (5 строк) попадает в каталог как RU через перечень субъектов РФ (`rfSubjects`, Phase 3), а не через справочник городов. Если для ПроБЕГа это нужно иначе — решение по перечню, не по `Parse`.
- «Республика Крым, Балаклава» получает регион «Республика Крым», хотя Балаклава — Севастополь; на каталог (RU) не влияет.
- Москва-группа содержит разовые события «Пробег «А побегать?» …» с разными названиями — каждое получает свой ключ серии (по одному элементу), не сворачивается.

## Threat Flags

Нет — новых поверхностей сверх `<threat_model>` плана (T-05-15…20 закрыты: `clean`/`absURL`/`eventBase` и TestParseEdges; порядок страны и TestPreparedStats; счётчик запросов в TestSourceFetch; TestPolicyGate; SHA фикстуры; все Fetch в тестах — httptest).

## Self-Check: PASSED

- FOUND: все 9 созданных файлов пакета probeg и testdata; изменения в cmd/aggregator/main.go, main_test.go.
- Коммиты: не создавались по правилу владельца (проверять нечего).
