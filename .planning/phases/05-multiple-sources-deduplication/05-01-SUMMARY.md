---
phase: 05-multiple-sources-deduplication
plan: 01
subsystem: ingest, normalize
status: complete
tags: [ingest, prepare, series, end_date, post-apply, sanity-report, ru-cities]
requires: []
provides:
  - "ingest.SourceEvent.EndDate (json end_date,omitempty), SourceEvent.GroupKey (json group_key,omitempty)"
  - "ingest.Policy.OutOfScopeMax"
  - "ingest.Stats.Collapsed (json ,omitempty), метрика collapsed (omitempty)"
  - "ingest.Runner.PostApply func(ctx context.Context) error"
  - "раздел «## Страна и серии» в SanityReport"
  - "normalize.IsRFCity(city string) bool"
  - "cityPrefixes + пгт./п.г.т./посёлок/поселок/ст-ца/станица/хутор/аул/латинская c.; maxCityKeyBytes = 60"
affects: [05-04, 05-05, 05-06]
tech-stack:
  added: []
  patterns: ["go:embed справочника в normalize", "предварительный проход Prepare для победителя серии"]
key-files:
  created:
    - internal/normalize/ru_cities.go
    - internal/normalize/ru_cities.txt
    - internal/normalize/ru_cities_ambiguous.txt
  modified:
    - internal/ingest/source.go
    - internal/ingest/prepare.go
    - internal/ingest/prepare_test.go
    - internal/ingest/runner.go
    - internal/ingest/runner_test.go
    - internal/ingest/report.go
    - internal/ingest/report_test.go
    - internal/normalize/place.go
    - internal/normalize/place_test.go
decisions:
  - "Checkpoint (решение владельца): option-a — EndDate/GroupKey с omitempty в конце SourceEvent после Description, Stats.Collapsed с json:\",omitempty\"; payload/content_hash Марафонца и оба golden не меняются"
  - "Проверки записи Prepare вынесены в check(); предварительный проход выбирает победителя серии по тем же критериям и с тем же учётом дублей external_id"
  - "Классификация страны в отчёте: RU — CountryCode(Country) == RU (эквивалент Country(…, NoFallback).inScope); иностранная — иной код или RegionForeign страны/области; иначе «не указана»"
metrics:
  duration: "~25 мин"
  completed: 2026-10-01
actuals:
  tokens: 10500
  tasks: 4
  commits: 0
plan_head_before: 86bdc42
requirements-completed: [ING-12, DDP-05]
---

# Phase 5 Plan 01: Контракт ingest для второго источника Summary

Аддитивные `SourceEvent.EndDate`/`GroupKey` (omitempty, payload Марафонца побайтно прежний), сворачивание серий в `Prepare` до ближайшей даты ≥ today−1 со счётчиком `Collapsed`, `Policy.OutOfScopeMax` источника в `Run`, nil-safe хук `Runner.PostApply`, раздел «Страна и серии» в санити-отчёте и встроенный справочник 1097 городов РФ (`IsRFCity`, Zenodo 5151423, CC BY 4.0) с новыми префиксами поселений и `maxCityKeyBytes = 60`.

## Решение checkpoint:decision

**option-a** (решение владельца, передано оркестратором): `EndDate string \`json:"end_date,omitempty"\`` и `GroupKey string \`json:"group_key,omitempty"\`` — в конце `SourceEvent` после `Description`; `Stats.Collapsed int \`json:",omitempty"\``. Payload/content_hash Марафонца и `marathonec/testdata/*` не изменились (`git diff --quiet` — 0).

## Задачи

| # | Задача | Результат | Коммит |
|---|--------|-----------|--------|
| 0 | checkpoint:decision | option-a | — |
| 1 | Tracer: EndDate + GroupKey → Prepare → Run/Accept → PG | `TRACER OK`: `TRACER created=2 events=2 nearest=2026-10-06 end_date=2026-11-10` | не коммитилось (владелец коммитит вручную) |
| 2 | OutOfScopeMax, PostApply, collapsed в логе, «Страна и серии» | `RUNNER OK` | не коммитилось |
| 3 | IsRFCity, префиксы, maxCityKeyBytes = 60 | `NORMALIZE OK`, индекс 1090 ключей (1097 − 7 неоднозначных) | не коммитилось |

Tracer feedback gate: `<verify>` задачи 1 повторён целиком после реализации — прошёл, расширение продолжено.

## Проверка

- `towns.csv` скачан с zenodo.org во временный каталог сессии (не в репозиторий): 209447 байт, md5 `3690d86f15b6727858b8a55ba5489e95`, SHA-256 `97f08b0e…5261a` — совпали с планом; столбец `city` через python3 csv, `LC_ALL=C sort -u` → 1097 строк.
- `go mod verify`, `go vet ./...`, `gofmt -l cmd internal migrations` — чисто; прямые зависимости не изменились; SQL вне `internal/store` нет.
- `env -u TEST_DATABASE_URL go test -count=1 ./...` — все пакеты ok.
- `TEST_DATABASE_URL=…/agg_w1a_test go test -p 1 -count=1 -timeout 5m -v ./...` — все пакеты ok, `--- SKIP` 0 (использовалась отдельная БД `agg_w1a_test`, а не `aggregator_test` из плана — параллельные исполнители).

## Deviations from Plan

1. **[Окружение] Тестовая БД:** вместо `aggregator_test` — `agg_w1a_test` (указание оркестратора, параллельные планы 05-02/05-03); `docker compose up` не выполнялся — контейнер уже запущен.
2. **[Окружение] Коммиты:** per-task коммиты не создавались — правило владельца (git write запрещён), изменения оставлены в рабочем дереве; `commits: 0`.
3. **Классификация RU в отчёте** — через `CountryCode(rec.Country) == "RU"` вместо `Country(…, NoFallback)`: результат тот же (при явной стране `Country` возвращает `code == "RU"`), без лишнего вызова.

Иначе — план выполнен как написан.

## Заметки для следующих планов

- `Evaluate` считает долю rejected от `parsed − expired`; свёрнутые записи серий входят в знаменатель (они валидны). Если у ПроБЕГа серий много, доля отбраковки разбавляется — учесть при калибровке порогов в 05-04.
- `maxCityKeyBytes = 60`: города с ключом 61–64 байта получат более короткий `city_key` при следующем прогоне (у Марафонца таких нет — golden не изменился).

## Known Stubs

Нет.

## Threat Flags

Нет — новых поверхностей сверх `<threat_model>` плана (T-05-01…05 закрыты тестами TestIsRFCity, TestPrepareGroupKey, TestPostApply, TestRunOutOfScopeMax и проверкой хешей).

## Self-Check: PASSED

- FOUND: internal/normalize/ru_cities.go, ru_cities.txt, ru_cities_ambiguous.txt
- FOUND: изменения в internal/ingest/{source,prepare,runner,report}.go и тестах, internal/normalize/place.go, place_test.go
- Коммиты: не создавались по правилу владельца (проверять нечего)
