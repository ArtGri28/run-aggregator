---
phase: 07-deduplication-transparency
plan: 02
subsystem: dedup
tags: [go, dedup, calibration, heroleague]
requires: [07-01]
provides:
  - "dedup.WindowPairs, dedup.Pairs, dedup.ScoredPair — одна логика пар окна и UniquePartner для Pass и калибровки (M2)"
  - "TestCalibration падает на AUTO-паре окна без подтверждённой метки (D-12); TestCalibrationUnlabeledAuto"
  - "«Лига Героев» в каталоге калибровки; строки HEROLEAGUE и BY"
affects: [07-03]
plan_head_before: 75cfcfa
actuals:
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "пары по индексам входного среза; A — сторона с меньшим EventID; skip до Decide"
key-files:
  created:
    - internal/dedup/pairs.go
    - internal/dedup/pairs_test.go
  modified:
    - internal/dedup/pass.go
    - internal/dedup/calibration_test.go
    - internal/dedup/testdata/README.md
key-decisions:
  - "D-11: перебор пар — одна функция пакета dedup; дубли в тесте удалены"
  - "D-13: пересечений «Лиги Героев» и межисточниковых пар Беларуси в фикстурах нет — задокументировано, подтверждено владельцем"
requirements-completed: []
completed: 2026-10-03
duration: около 1 часа
status: complete
---

# Phase 7 Plan 02: одна логика пар и ворота калибровки

**Статус: complete (локально).** Проход и калибровка перебирают пары одной функцией; тест калибровки падает на неразмеченной AUTO-паре; третий источник подключён к каталогу калибровки. Пороги, метки, `calibration.json`, `DEDUP_AUTO_MERGE`, `deploy/`, миграции не менялись. Коммит делает владелец.

DDP-13 выполнен локально (статус в REQUIREMENTS.md — в 07-05).

## Что сделано

- `internal/dedup/pairs.go`: `WindowPairs(sides, fn)` — окно (одна страна, начало второй не позже конца первой + 2 дня, порядок (Start, EventID)); `Pairs(sides, c, skip)` — Decide без `UniquePartner`, партнёры по NameSim ≥ High, Decide с `UniquePartner`; `skip` — отклонённые пары до Decide.
- `internal/dedup/pass.go`: перебор пар — через `Pairs` (счётчик `rejected_skipped` сохранён: страна проверяется до `skip`, как раньше); лишняя сортировка событий удалена (порядок задаёт `WindowPairs`).
- `internal/dedup/calibration_test.go`: `windowPairs`, `countPartners` удалены (используются `dedup.WindowPairs`, `dedup.Pairs`); `unlabeledAuto` + ошибка теста на каждую AUTO-пару окна без подтверждённой метки; «Лига Героев» в `calibSources` (now 2026-10-01 12:00 UTC); отчёт `thirdSourceReport`; `TestCalibrationUnlabeledAuto`.
- `internal/dedup/testdata/README.md`: третья фикстура (SHA-256 `d3b87514…7083`), раздел «Третий источник и Беларусь», подтверждение владельца.

## Результаты

    HEROLEAGUE pairs_window=0 auto=0 window_all=241 max_sim=0.13
    BY cross_source_pairs=0 events marathonec=0 probeg=10 heroleague=0
    CALIBRATION pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0 high=0.70 low=0.30 link_bonus=0.10

- CALIBRATION до и после перехода на общую функцию одинакова; вывод `-calib-dump` (235 строк `PAIR`/`DUMP`) побайтно совпадает с HEAD `75cfcfa` (сравнение на копии вне репозитория).
- Неразмеченных AUTO-пар в окне фикстур — 0 (все 45 размечены владельцем 2026-10-01).

## Решение владельца (задача 3)

Ответ дословно: «Подтверждаю». Вывод принят: у «Лиги Героев» 3 события в каталоге фикстуры (OCR: Волгоград 10.10, Москва 17.10, Санкт-Петербург 03.10), 241 пара окна с другими источниками, максимальная схожесть названий 0,13 < Low 0,30 — пар для разметки нет; события `BY` только у ПроБЕГа (10) — межисточниковых пар Беларуси нет. Ограничение: проверены закоммиченные снимки 2026-09-29…2026-10-01, не production-данные.

## Проверки

- verify задач: `PAIRS OK`, `LABELS DRAFT OK`, `CALIBRATION OK`.
- `TestWindowPairs` (окно, граница +2 дня, другая страна, многодневное событие, ориентация по EventID, партнёры, `skip`).
- Подробный полный прогон `go test -p 1 -count=1 -v ./...` на локальной PostgreSQL 17: 457 PASS, 0 SKIP, 0 FAIL; `gofmt` пуст, `go vet` чисто.
- Мутационная проверка на копии кода: окно +3 дня; партнёры без порога High; proposed-метка как подтверждённая; удалённая метка `a01`; `skip` не применяется — каждый раз падают профильные тесты.

## Deviations from Plan

1. Новых пар в `calibration.json` нет: перебор не нашёл пар «Лиги Героев» со схожестью ≥ Low и межисточниковых пар Беларуси; задача 3 свелась к подтверждению задокументированного вывода (DDP-13 допускает этот исход).
2. Строка `HEROLEAGUE` содержит дополнительные поля `window_all`, `max_sim` (формат verify не нарушен).
3. `internal/ingest/sources/heroleague/testdata/README.md` («пары с этим источником в набор не входят») не менялся — утверждение по-прежнему верно; файл вне списка плана.
4. Коммиты делает владелец; исполнитель их не выполнял.
