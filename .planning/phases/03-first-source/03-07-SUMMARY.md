---
phase: 03-first-source
plan: 07
subsystem: политика страны Марафонца (D-07) + принятие базы и второй реальный прогон в dev-БД
tags: [ing-04, ing-05, ing-06, ing-08, ing-11, ing-13, d-06, d-07, d-15, accept-baseline, idempotency]
status: complete

requires:
  - "03-04: Runner.Accept повторяет Prepare по снимку с текущей политикой источника"
  - "03-05: worker accept-baseline / run-once, dev-PostgreSQL"
  - "03-06: прогон 5 (suspect со снимком), решение владельца confirm (D-15)"
provides:
  - "internal/ingest/sources/marathonec/marathonec.go — политика RUOnlyFallback по решению D-07"
  - "internal/ingest/runner_test.go — TestAcceptAfterPolicySwitch"
  - ".planning/phases/03-first-source/03-SANITY-REPORT.md — раздел «Решение» в формате D-07"
  - "dev-БД: 133 события Марафонца, прогон 5 принят, прогон 6 ok"
affects: [phase-4-telegram-search, phase-5-multiple-sources]

actuals:
  tokens: 1600    # chars/4 по диффу коммита 1ef5af7
  tasks: 2
  commits: 1      # git rev-list --count f0b48c9..HEAD
plan_head_before: f0b48c9

tech-stack:
  added: []
  patterns:
    - "Снимок suspect хранит разобранные записи, а не классификацию: принятие после смены политики классифицирует их заново"

key-files:
  created: []
  modified:
    - internal/ingest/sources/marathonec/marathonec.go
    - internal/ingest/sources/marathonec/marathonec_test.go
    - internal/ingest/runner_test.go
    - .planning/phases/03-first-source/03-SANITY-REPORT.md

decisions:
  - "[03-07] Марафонец: CountryFallback = RUOnlyFallback по решению владельца 2026-09-29 (прогон 5, D-07/D-15); новых написаний областей не потребовалось"
  - "[03-07] Базовый уровень: прогон 5 принят вручную (created 133), прогон 6 ok без изменений каталога (created 0, changed 0, unchanged 133)"

metrics:
  duration: "~20min (+ действия владельца)"
  completed: 2026-09-29
---

# Phase 3 Plan 07: решение D-07, принятие базы и второй прогон — Summary

**Политика Марафонца переключена на `RUOnlyFallback` по записанному решению владельца; прогон 5 принят (133 события в каталоге), второй реальный прогон 6 — `ok` без единого созданного или изменённого события. Все пять критериев ROADMAP Phase 3 выполнены.**

## Задача 1: решение и политика (коммит 1ef5af7)

- `03-SANITY-REPORT.md`, «## Решение»: четыре строки формата D-07 — «Значение fallback RU для Марафонца: подтверждён», «Дата: 2026-09-29», «Решение принял: владелец», «Основание: санити-отчёт прогона 5 …; добавленные написания: нет».
- `marathonec.go`: `Policy()` → `normalize.RUOnlyFallback`, комментарий ссылается на дату, прогон 5 и отчёт. `TestSourcePolicy` ожидает `RUOnlyFallback`.
- `regions.go` не менялся (решение `confirm`, 43/43 области в перечне). `prepared.golden.json` не менялся: `TestPreparedGolden` передаёт политику явно. Фикстура побайтно прежняя (SHA-256 `dd4a58d3…3ddf`).
- До передачи владельцу проверено, что принятие не применит устаревшую классификацию: снимок в `ingest_runs.snapshot` — массив `SourceEvent` без страны и out_of_scope, `Runner.Accept` вызывает `Prepare(recs, last.StartedAt, src.Policy())`. Сухой прогон `Prepare` по снимку прогона 5: `NoFallback` — out_of_scope 133; `RUOnlyFallback` — out_of_scope 0, в каталоге 133, область и город у 131.
- Проверки: `DECISION OK`; `go test -p 1 ./...` с тестовой БД — 78 PASS, без неё — 53 PASS / 25 SKIP; `go vet`, `gofmt -l .`, `git diff --check` чисто.

## Задача 2: владелец (accept-baseline и второй прогон)

| Прогон | Команда | Статус | fetched / parsed | created | changed | unchanged | out_of_scope | missing / gone |
|---|---|---|---|---|---|---|---|---|
| 5 | `worker accept-baseline --source marathonec --run 5` | ok (принят вручную) | 133 / 133 | 133 | 0 | 0 | 0 | 0 / 0 |
| 6 | `worker run-once --source marathonec` | ok | 133 / 133 | 0 | 0 | 133 | 0 | 0 / 0 |

- Прогон 6: fetch 598490 Б, baseline_runs = 1, медиана базы 133, подряд сбоев 0.
- Прогоны 1–4 — `failed` из-за DNS (см. 03-06), в каталог ничего не применили.
- Нераспознанных областей: 0 (metrics прогонов 5 и 6, `unrecognized_region = 0`); записей с несколькими областями — 2.
- sha256 сырых ответов `5-0` и `6-0` различаются. Построчное сравнение: 11 пар строк, ни одна не содержит `post-row`; меняются только хэши CSS-переменных WP Rocket, id рекламных вставок, uid скрипта и счётчик просмотров. Данные календаря не менялись — это подтверждают metrics прогона 6 (`created 0, changed 0, unchanged 133`).

## Проверка dev-БД (verify задачи 2 через `docker exec … psql`)

Все условия выполнены, `PHASE DATA OK`:

- принятый прогон: 1 (id 5), `ok`, `accepted_at` заполнен, снимок null, error «принят вручную; исходная причина: первый прогон источника — …»;
- последний прогон (6) `ok`; прогонов источника 6; `running` — 0;
- events 133, `country_code <> 'RU'` — 0; с областью и городом 131 из 133 (98 %);
- max(char_length(description)) = 0 (у Марафонца нет описаний) ≤ 1000; колонок изображений в схеме 0;
- событий без source_event 0, source_event с общим событием 0; source_events 133 (`active` 133, `match_state = auto` 133);
- `is_listed = false` — 0, `gone` — 0; events.status: `scheduled` 133;
- event_distances 448 строк у 132 событий.

## Критерии ROADMAP Phase 3

| # | Критерий | Доказательство | Итог |
|---|---|---|---|
| 1 | Golden-тесты парсера без сети | `TestParseGolden`, `TestPreparedGolden` PASS без `TEST_DATABASE_URL` и сети | выполнен |
| 2 | Повторный прогон без дублей и изменений | `TestRunFixture`, `TestRunSecondRunOK` PASS; на живых данных прогон 6: created 0, changed 0, unchanged 133; 133 source_events на 133 внешних id, общих событий нет | выполнен |
| 3 | Падение или рост объёма → `suspect`, ничего не применяется, причина в `ingest_runs` и логах | `TestRunVolumeSuspect`, `TestRunFirstRunSuspect` PASS; на живых данных прогон 5 был `suspect` с причиной в `ingest_runs.error`, source_events и events оставались 0 до принятия | выполнен |
| 4 | Исчезновение → только `is_listed`, по здоровым прогонам, `events.status` не меняется | `TestObserve`, `TestRunObservation` PASS; на живых данных после двух здоровых прогонов missing 0, gone 0, `is_listed = false` 0, статусы `scheduled` | выполнен |
| 5 | Реальные события с `country_code`, `region`, `city`; каждый запуск в `ingest_runs`; описание ≤ 1000; без изображений; санити-решение по fallback RU | 133 события, все RU, 131/133 с областью и городом; 6 прогонов в `ingest_runs`; описание ≤ 1000; колонок изображений нет; решение «подтверждён» в 03-SANITY-REPORT.md и в политике | выполнен |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing test] Тест принятия после смены политики**
- **Found during:** задача 1 (проверка, что accept применит снимок с новой политикой)
- **Issue:** key_link плана «Accept применяет снимок первого прогона с текущей политикой» не был покрыт тестом: все тестовые источники всегда работали с `RUOnlyFallback`.
- **Fix:** `fixtureSource.noFallback`; `TestAcceptAfterPolicySwitch` — прогон без fallback (всё out_of_scope) → принятие с `RUOnlyFallback` даёт OutOfScope 0 и Created == Upserted > 0.
- **Files modified:** internal/ingest/runner_test.go
- **Commit:** 1ef5af7

**2. Формат раздела «Решение».** Раздел, записанный в 03-06, приведён к четырём строкам плана; цифры основания сохранены, дословная формулировка владельца остаётся в 03-06-SUMMARY.md и D-15.

**3. Проверка dev-БД** выполнена через `docker exec -i aggregator-dev-postgres-1 psql`, а не `docker compose … exec`: compose требует переменных из `.env`, который исполнитель не читает. Запросы те же.

## Known Stubs

Нет.

## Self-Check: PASSED

- FOUND: internal/ingest/sources/marathonec/marathonec.go (RUOnlyFallback, ссылка на 03-SANITY-REPORT)
- FOUND: internal/ingest/runner_test.go (TestAcceptAfterPolicySwitch)
- FOUND: commit 1ef5af7
- FOUND: data/raw/marathonec/5-0.meta.json, 6-0.meta.json
