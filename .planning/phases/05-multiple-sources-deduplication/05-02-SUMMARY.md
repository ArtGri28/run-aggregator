---
phase: 05-multiple-sources-deduplication
plan: 02
subsystem: dedup
tags: [go, dedup, trigram, jaccard, fold, config]
requires: []
provides:
  - "event.FoldConfig, event.Part, event.Fold — свёртка полей слитого события по приоритету источников, дистанции с допуском"
  - "dedup.NameSim, dedup.LinkKey (неэкспортируемые simKey, grams)"
  - "dedup.SourceRef, Side, Pair, Verdict (Separate/Candidate/AutoMerge), Reasons, Decide"
  - "dedup.Config, DefaultConfig, LoadConfig (DEDUP_*), (Config).Fold"
affects: [05-05, 05-06, 05-07]
plan_head_before: 8a7dad9
actuals:
  tokens: 11100
  tasks: 3
  commits: 0
tech-stack:
  added: []
  patterns:
    - "чистые функции без БД; таблица переменных окружения и setValue по образцу ingest/config.go"
key-files:
  created:
    - internal/event/fold.go
    - internal/event/fold_test.go
    - internal/dedup/config.go
    - internal/dedup/config_test.go
    - internal/dedup/sim.go
    - internal/dedup/sim_test.go
    - internal/dedup/decide.go
    - internal/dedup/decide_test.go
  modified: []
key-decisions:
  - "D-20 link-strict (решение владельца): AutoMerge требует Reasons.NameSim >= High; LinkBonus меняет только Score (порядок и показ в /dupes)"
  - "Даты сравниваются по календарным полям t.Date() как есть, без перевода в UTC (StartDate — местная дата, event.go)"
  - "Место в Fold: первая часть с City, иначе с Region, иначе с Timezone/Address — чтобы одна часть без города не теряла поля"
requirements-completed: [DDP-01, DDP-02, DDP-03, DDP-04, DDP-05, DDP-10]
completed: 2026-10-01
duration: около 30 минут
status: complete
---

# Phase 5 Plan 02: Ядро дедупликации Summary

**Чистое ядро без БД: триграммный Jaccard по рунам над simKey(TitleNorm), решение по паре с воротами страны, источников, окна ±1 день и порогом имени, автослияние выключено по умолчанию и требует NameSim >= High без бонуса ссылки (D-20), детерминированная свёртка полей по приоритету источников с объединением дистанций в пределах 100 м, настройки DEDUP_*.**

## Задачи

1. **Tracer.** Реальная пара Марафонец 90435 / строка ПроБЕГа «27 03.10»: `TRACER sim=0.89 off=candidate on=auto distances=1000,2000,10000,20000 title=marathonec type=trail`.
2. **LinkKey, бонус ссылки, таблицы NameSim и Decide.** Окно дат, город (D-06), один источник и group_key (DDP-05), границы порогов, JSON Reasons, соцсети, `?`/`#`, пустой путь.
3. **Таблицы Fold и LoadConfig.** Приоритет, группы, неизвестные коды, допуск дистанций, одна часть = normalizeDistances, все перестановки дают одинаковый результат, вход не меняется; проверки диапазонов DEDUP_*, ошибка называет переменную без значения.

## Проверка

- `TRACER OK`, `SIM OK`, `CORE OK` (команды verify задач 1–3, объединённые).
- `go test -race ./internal/event/ ./internal/dedup/` — ok; `go vet`, `gofmt -l` — чисто; go.mod/go.sum без изменений; в dedup/event нет импортов store/ingest/bot/alert/pgx.
- Полосы NameSim (фактические значения): «окская тропа дневной/ночной» 0.577, «жемчужина золотого кольца»/«суздальский …» 0.641 — обе в [0.4, 0.7); «белые ночи»/«алые паруса» 0.095.
- Статику всего репозитория (`go vet ./...`, `go test ./...`) не запускал: 05-01 и 05-03 параллельно меняют ingest, normalize, store, bot.

## Deviations from Plan

**1. [Решение владельца D-20, link-strict] Бонус ссылки не поднимает пару до автослияния**
- **План:** Flagged assumption DDP-04 и TestDecideLink — бонус может поднять NameSim из [High − LinkBonus, High) до AutoMerge.
- **Сделано:** условие автослияния — `s >= c.High` по чистому NameSim; бонус влияет только на `Reasons.Score`. Кейс TestDecideLink «c.Low = s−0.01, c.High = s+0.05, LinkBonus 0.10, общая ссылка, все условия» ожидает **Candidate** (Link true, Score = min(1, s+0.10)). Добавлен кейс «NameSim = High с общей ссылкой → AutoMerge»: ссылка автослиянию не мешает.
- **Файлы:** internal/dedup/decide.go, internal/dedup/decide_test.go.

**2. [Rule 1] Календарные даты без перевода в UTC**
- План: «Y/M/D в UTC». Сделано: `t.Date()` в собственной зоне значения. Для дат из БД (полночь UTC) результат тот же; для даты, созданной в зоне Москвы, перевод в UTC сдвинул бы день назад, а event.go определяет StartDate как местную дату без пересчёта.

**3. [Rule 2] Запасной выбор группы места в Fold**
- План: первая часть с City, иначе с Region. Добавлен третий шаг — с Timezone или Address, иначе одна часть без города и региона теряла бы их (TestFoldSingle).

**4. LinkKey: `?`/`#` проверяются по исходной строке**
- `url.URL` не различает пустой фрагмент («…/2080#») и его отсутствие, поэтому проверка — `strings.ContainsAny(raw, "?#")` до разбора. Поведение строже плана, не мягче.

## Known Stubs

Нет.

## Открытые вопросы владельцу

- Outline (Cross-plan contracts) перечисляет группы Fold как (StartDate, EndDate) и (Region, City, CityKey, Timezone), а план — с StartTime и Address. Сделано по плану; outline стоит обновить, чтобы 05-05 не ориентировался на старый состав.
- `UniquePartner` в 05-05 по контракту считается по «score ≥ High». При D-20 логичнее считать по NameSim ≥ High, иначе ссылка может отнять у пары единственность, не дав ей автослияния. Решить при выполнении 05-05.

## Self-Check: PASSED

- Все 8 файлов из key-files существуют; коммитов нет — по правилам владельца изменения оставлены в рабочем дереве (commits: 0 при наличии кода, это ожидаемо: коммитит владелец).
