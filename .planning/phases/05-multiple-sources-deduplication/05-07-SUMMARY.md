---
phase: 05-multiple-sources-deduplication
plan: 07
subsystem: dedup
status: complete
tags: [dedup, calibration, thresholds, fixtures]
requires: [05-02, 05-04, 05-05]
provides:
  - "internal/dedup/calibration_test.go: TestCalibration (PAIR/MATRIX/CALIBRATION), TestCalibrationCheck, judge, флаг -calib-dump"
  - "internal/dedup/testdata/calibration.json: 81 пара (76 реальных + 5 синтетических), все подтверждены владельцем"
  - "internal/dedup/testdata/README.md: формат, правило меток, SHA-256 фикстур, перекалибровка, таблица всех пар"
affects: [05-08]
plan_head_before: 86bdc42
actuals:
  tokens: 12000
  tasks: 2
  commits: 0
key-files:
  created:
    - internal/dedup/calibration_test.go
    - internal/dedup/testdata/calibration.json
    - internal/dedup/testdata/README.md
  modified: []
key-decisions:
  - "Отдельное поле reason у пары (основание метки), note — только обе стороны"
  - "UniquePartner — как в pass.go: партнёр = пара не Separate с NameSim >= High (не Score)"
  - "config.go не менялся: значения по умолчанию решаются только после меток владельца (задача 4)"
metrics:
  duration: "~40 мин"
  completed: null
---

# Phase 5 Plan 07: Калибровочный набор — черновик (ждёт проверки меток владельцем)

Тест `TestCalibration` без БД и сети строит каталог из двух фикстур (Parse + Prepare, только InScope), считает `UniquePartner` по объединению как `dedup.Pass` и вызывает `Decide` с `LoadConfig()` и `AutoMerge = true`; черновик из 49 пар с предложенными метками лежит в `calibration.json`. Выполнены задачи 1–2; остановка на checkpoint задачи 3 (проверка меток владельцем). Задача 4 не начата.

## Задачи

| # | Задача | Результат | Коммит |
|---|---|---|---|
| 1 | Tracer: 4 пары, две фикстуры → Decide → CALIBRATION | `TRACER OK` | нет (коммитит владелец) |
| 2 | Черновик 49 пар, проверка состава | `DRAFT OK` | нет |
| 3 | checkpoint:human-verify — метки владельцем | **ожидает** | — |
| 4 | Метки владельца, прогон, DefaultConfig при нарушениях | не начата | — |

Tracer feedback gate: verify задачи 1 прошёл целиком (c01 auto up=true, c02 candidate, c03 separate/source, c04 separate/dates, `pairs=4`, `DEDUP_HIGH=0.95` → `high=0.95`). Замен пар tracer не было: все четыре стороны нашлись в каталоге.

## Прогон черновика (пороги по умолчанию)

```
MATRIX same: separate=4 candidate=16 auto=13
MATRIX different: separate=6 candidate=10 auto=0
CALIBRATION pairs=49 confirmed=0 proposed=49 false_auto=0 missed=0 high=0.70 low=0.40 link_bonus=0.10 proposed_false_auto=0 proposed_missed=3
```

`separate` у same включает c03 (один источник — инвариант DDP-05); `separate` у different — c45 (один источник) и 5 синтетических.

MISMATCH (proposed) — только пропуски, все с NameSim ниже Low = 0,40:
- c33 missed: М 90522 «Осенний кросс «КультаКарьяла»» × П 83360 «Осенний Кросс «KultaKarjala»», 04.10 Петрозаводск, sim 0,34 (кириллица и латиница, общая ссылка);
- c34 missed: М 72065 «Марафон «Путь к роднику»» × П 79960 «Беговой фестиваль «Путь к роднику»», 03.10 Слюдорудник, sim 0,38 (общая ссылка);
- c35 missed: М 86190 «Фестиваль ВолховМен «На пути из варяг в греки»» × П 83375 «Фестиваль «ВолховMan»», 17.07.2027 Великий Новгород, sim 0,34 (общая ссылка).

Против предложенных меток (42 реальные пары разных источников: same 32, different 10):
- автослияние: 13 пар, все same → ложных 0, точность 13/13; полнота автослияния 13/32;
- same не ниже кандидата: 29/32 (пропуски c33, c34, c35);
- different: 0 auto, все 10 — кандидаты в /dupes (шум, не ошибка);
- 16 same остались кандидатами: разный city_key (c19–c26), даты не пересекаются (c17, c18, c28, c32, c26), NameSim < 0,70 (c27–c32).

Чувствительность (только для владельца, значения по умолчанию не менялись): `DEDUP_HIGH=0.8` → auto 12 (уходит c12 0,75); `0.9` → auto 10 (уходят c01 0,89, c11 0,83); false_auto везде 0. `DEDUP_LOW=0.3` → missed 0, но в окне фикстур добавляется ещё 8 пар разных источников с sim 0,3–0,4, по названиям почти все разные.

## Что проверить владельцу в первую очередь

- Пары с вердиктом auto (13): c01, c05–c16 — их метка решает, будет ли ложное автослияние.
- «Сомнительно»: c16 (Golden Ring: «2026» в названии ПроБЕГа при дате 2027), c18 (Арена марафон 24 и 25.10), c25 (Rosa Wild Fest, «2026» в названии), c26 (кросс Знаменских 02 и 03.10, общая ссылка).
- «Вся Россия» c41–c44 предложены как different (одна запись на все города против одного города).

Полная таблица (id, обе стороны, NameSim, score, вердикт при выключенном и включённом автослиянии, метка, основание) — в `internal/dedup/testdata/README.md`.

## Наблюдения

- Реальных жёстких негативов Pitfall 8 (марафон против полумарафона, дневной против ночного в одном городе и дне) в фикстурах нет: у «Окской тропы» дневной и ночной забеги в разные дни и с разным city_key, поэтому кандидаты. 0 ложных автослияний на этом наборе слабо доказывает защиту от этого случая; его закрывают только даты, город и единственность партнёра. Для проверки нужны другие снимки (продакшн или будущие фикстуры).
- Одноимённых событий разных лет в снимках нет — только 5 синтетических пар.
- У Марафонца внутренний дубль KultaKarjala: 73916 (03.10) и 90522 (04.10) с одной ссылкой регистрации.
- У ПроБЕГа в названиях 83015 и 84536 стоит «2026» при датах 2027.
- Реальных пар достаточно: в окне фикстур 151 пара разных источников с sim ≥ 0,3 (45 auto, 95 candidate, 11 ниже Low); добирать материал не нужно.

## Deviations from Plan

1. **[По указанию оркестратора] Поле `reason`.** У каждой пары отдельное короткое основание метки; `note` — только «A: … | B: …». Тест требует непустые оба поля (`DisallowUnknownFields` учитывает `reason`).
2. **[Rule 3] Псевдонимы импорта `mcsrc`/`pbsrc`.** В `pass_test.go` того же пакета `dedup_test` объявлены функции `marathonec` и `probeg`; импорт под исходными именами не компилируется. Паттерны key_links `marathonec\.Parse\(` / `probeg\.Parse\(` поэтому не совпадают буквально; `Parse` передаётся значением в таблице `calibSources`.
3. **Партнёры по NameSim.** План (п. 2 задачи 1) — `Reasons.Score ≥ High`; сделано как в `pass.go`: пара с вердиктом не Separate и `NameSim ≥ High` (D-20, решение 05-05).
4. **`sim` в строке PAIR — всегда `dedup.NameSim`.** `Decide` не считает схожесть, если пару закрыли ворота раньше (source, dates), и печатал бы 0. Добавлено поле `off=` — вердикт при выключенном автослиянии.
5. **Вид `edge` шире 0,4–0,7.** c33–c35 со схожестью 0,34–0,38 отнесены к `edge`: это пограничные пропуски, важные для полноты.
6. **`-calib-dump`** печатает ещё дистанции, конец и ссылку регистрации (основания меток).
7. **Коммитов нет** — правило владельца; `commits: 0` при изменённом коде ожидаемо.

## Проверка

- `TRACER OK`, `DRAFT OK` (verify задач 1 и 2 дословно).
- `gofmt -l internal/dedup` пусто, `go vet ./internal/dedup/` ok, `env -u TEST_DATABASE_URL go test -count=1 ./internal/dedup/` ok, go.mod/go.sum без изменений.
- SHA-256 фикстур совпадают с README; в тесте нет `net/http`, `storetest`, `pgx`.
- `config.go`, `decide.go`, `sim.go`, `pass.go`, файлы 05-06 не менялись.

## Known Stubs

Нет. Раздел README «Проверка владельцем» — «не проверено» до задачи 3 (по плану).

## Self-Check: PASSED

- Файлы существуют: internal/dedup/calibration_test.go, internal/dedup/testdata/calibration.json, internal/dedup/testdata/README.md.
- Коммитов нет по правилу владельца (git write запрещён).


## Задача 4 (2026-10-01)

- Владелец подтвердил 42 метки (c01, c02, c05–c44) без правок; c03, c04, c45–c49 остаются `proposed: true`.
- До калибровки исправлены дистанции ПроБЕГа (`probeg/parse.go`): `namedToMeters` («марафон», «полумарафон» в списке) и срез баллов «ITRA N»; golden ПроБЕГа обновлены, счётчики Prepare не изменились.
- `DefaultConfig.Low` 0,40 → 0,30 (подбор через `DEDUP_LOW`, шаг 0,05): при 0,35 пропущено 2, при 0,30 — 0. `High`, `LinkBonus`, `AutoMerge=false` без изменений.
- `CALIBRATION pairs=49 confirmed=42 proposed=7 false_auto=0 missed=0 high=0.70 low=0.30 link_bonus=0.10`; MATRIX same: separate=1 candidate=19 auto=13; different: separate=6 candidate=10 auto=0.
- Вывод для 05-08: на подтверждённых метках ложных автослияний нет, но из 45 пар окна фикстур с вердиктом AUTO размечены 13 — `DEDUP_AUTO_MERGE` не включать до решения владельца (разметить остальные 32 или включать после просмотра `/dupes` на проде). Gate плана (`proposed=0`) не пройден до подтверждения 7 структурных пар.

## Проверка всех AUTO-пар (2026-10-01)

- Владелец проверил все 32 ранее не размеченные AUTO-пары окна фикстур — все same; добавлены как a01–a32. `calibration_test.go`: id `^[ca][0-9]{2}$`, размер набора 30–100.
- `CALIBRATION pairs=81 confirmed=74 proposed=7 false_auto=0 missed=0 high=0.70 low=0.30 link_bonus=0.10`; MATRIX same: separate=1 (c03, один источник) candidate=19 auto=45; different: separate=6 candidate=10 auto=0.
- Все 45 AUTO-пар фикстур подтверждены владельцем как same; ни одна different-пара не получает AUTO.
- Остаются неподтверждёнными c03, c04, c45–c49 (структурные/синтетические). `DEDUP_AUTO_MERGE` не включён; решение — при обсуждении 05-08.

## Итог 05-07 (2026-10-01)

- Владелец подтвердил c03 (same), c04, c45–c49 (different); все 81 метка подтверждены, `proposed: true` нет.
- `CALIBRATION pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0 high=0.70 low=0.30 link_bonus=0.10` — gate задачи 4 пройден (граница размера набора в gate расширена до 100 вместе с проверкой в тесте).
- MATRIX same: separate=1 candidate=19 auto=45; different: separate=6 candidate=10 auto=0.
- Значения по умолчанию: High 0,70 (без изменений), Low 0,40 → 0,30, LinkBonus 0,10 (без изменений), AutoMerge=false (без изменений). `decide.go`, `sim.go`, `pass.go` не менялись.
- **Вывод для 05-08:** условие DDP-09 выполнено на размеченном наборе (0 ложных автослияний, все 45 AUTO-пар окна фикстур проверены владельцем). Включение `DEDUP_AUTO_MERGE=true` — отдельное решение владельца при обсуждении 05-08; калибровка сделана на снимках 2026-09-29/30, а не на продакшн-БД.
