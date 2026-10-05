---
phase: 05-multiple-sources-deduplication
verified: 2026-10-05
status: passed
score: 16/16 must-haves verified
covered_files:
  - ".planning/REQUIREMENTS.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-01-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-01-SUMMARY.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-02-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-02-SUMMARY.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-03-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-03-SUMMARY.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-04-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-04-SUMMARY.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-05-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-05-SUMMARY.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-06-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-06-SUMMARY.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-07-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-07-SUMMARY.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-08-PLAN.md"
  - ".planning/phases/05-multiple-sources-deduplication/05-08-SUMMARY.md"
  - "cmd/aggregator/main.go"
  - "deploy/README.md"
  - "deploy/vps/deploy-verify.sh"
  - "docker-compose.yml"
  - "internal/bot/callback.go"
  - "internal/bot/dupes.go"
  - "internal/bot/list.go"
  - "internal/dedup/config.go"
  - "internal/dedup/decide.go"
  - "internal/dedup/digest.go"
  - "internal/dedup/pass.go"
  - "internal/dedup/sim.go"
  - "internal/event/fold.go"
  - "internal/ingest/prepare.go"
  - "internal/ingest/runner.go"
  - "internal/ingest/source.go"
  - "internal/ingest/sources/heroleague/heroleague.go"
  - "internal/ingest/sources/probeg/parse.go"
  - "internal/ingest/sources/probeg/probeg.go"
  - "internal/normalize/country.go"
  - "internal/normalize/place.go"
  - "internal/normalize/ru_cities.go"
  - "internal/store/dedup.go"
  - "internal/store/events.go"
  - "internal/store/listing.go"
  - "migrations/00004_dedup.sql"
covered_digest: "v1:sha256:9b1c9b1d974a5631e51bb6e121aaf35641075d92485d6a21b1173fdf51a282a1"
behavior_unverified: 0
overrides_applied: 0
gaps: []
re_verification: true
human_verification: []
---

# Phase 5: Multiple Sources & Deduplication — Verification Report

**Phase Goal:** Несколько источников дают единый каталог без дублей и без ложных слияний.
**Verified:** 2026-10-01
**Status:** passed (повторная проверка 2026-10-05 — раздел «Повторная проверка 2026-10-05» в конце; текст ниже — отчёт прежней проверки, сохранён как история)
**Re-verification:** No — initial verification
**HEAD при проверке:** `6d9efa3` (рабочее дерево: изменены только `.planning/*` и `deploy/README.md`, в `heroleague/testdata/README.md` добавлен новый файл; Go-код рабочего дерева совпадает с HEAD)

## Итог

Блокеров (FAILED) нет. Цель фазы достигнута в границах релиза, который владелец определил 2026-10-01: ручное слияние через `/dupes`, `DEDUP_AUTO_MERGE=false` (D-23), три источника (marathonec, probeg, heroleague, D-22), страны каталога RU и BY (D-24). Два пункта из 16 не получают статус VERIFIED и вынесены владельцу:

- **DDP-03 — PARTIAL.** Путь «высокая уверенность -> автоматическая связь» реализован и проверен автотестами и калибровкой, но выключен в production и там никогда не включался.
- **Критерий 2 — PARTIAL.** Объединение дистанций и источников проверено кодом и тестами, в production есть косвенные счётчики (86 слияний, выдача -86), саму слитую карточку никто не видел.

Статус `human_needed`, потому что список проверок человеком непуст (разделы «Human Verification Required» и «Ограничения базы доказательств»).

Различение источников доказательств в таблицах ниже: **(a)** код + автотесты на HEAD, проверено в этом прогоне; **(b)** сохранённые логи `agg verify` в production; **(c)** только слова SUMMARY / владельца.

Проверки, выполненные в этом прогоне: `gofmt -l .` — пусто; `go vet ./...` — пусто; `go test -p 1 -count=1 ./...` на локальной тестовой БД — все пакеты `ok`, 443 `--- PASS`, 0 `FAIL`, 0 `SKIP`; `TestCalibration`: `CALIBRATION pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0 high=0.70 low=0.30 link_bonus=0.10`.

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Статус | Доказательство |
|---|-------|--------|----------------|
| 1 | Второй и третий источники подключены; новый источник не требует изменения общего интерфейса ingestion и архитектуры pipeline | ✓ VERIFIED (с оговоркой) | (a) `ingest.Source` (4 метода) не менялся: `git diff 86bdc42 HEAD -- internal/ingest/source.go` затрагивает только `SourceEvent` и `Policy`; регистрация одной строкой `cmd/aggregator/main.go:150`; `internal/ingest` не импортирует `sources/*`. Golden Марафонца не менялись (в `marathonec/` с 86bdc42 только +26 строк в `prepared_test.go`), `TestPayloadStable` PASS. (b) `source_run=probeg|4|ok|1312|accepted=t`, `source_run=heroleague|7|ok|20|accepted=t`. Оговорка: общий конвейер получил аддитивные расширения под новые источники — `EndDate`, `GroupKey`, `Policy.OutOfScopeMax`, `Runner.PostApply`, сворачивание серий в `Prepare` (план 05-01) и `Address`, `Timezone`, ветка не-RU области в `Prepare` (коммит `e8005e8`, вне планов). Интерфейс не менялся, форма pipeline не менялась, но «ни одной правки общего кода» неверно; ROADMAP это допускает («source-specific код добавлять разрешено») |
| 2 | Одно соревнование из нескольких источников — одно событие с объединёнными дистанциями и всеми источниками | ⚠ PARTIAL | (a) `event.Fold` объединяет дистанции с допуском (`internal/event/fold.go:37`, `TestFoldDistances`), `MergeEvents` переносит `source_events` на победителя и пересобирает поля через `saveEventTx` (`store/dedup.go:166-240`, `TestMergeEvents`: «union within 100 m»), `EventSources` корня отдаёт все источники, `TestMergedCardOpensRoot` PASS. (b) косвенно: `events_merged=86`, `events_listed` 538 (10:42) -> 452 (13:49), ровно -86; десять последних строк `merged=` — очевидные дубли («Elbrus Wild Trail» / «Elbrus Wild Trail 2027» и т. п.). Не подтверждено: слитая карточка в production не наблюдалась (human-verification #1). Слияния только ручные (автослияние выключено) |
| 3 | Разные соревнования, включая одноимённые в разные годы, не сливаются автоматически; сомнительные случаи — в `/dupes` | ✓ VERIFIED | (a) ворота `Decide`: страна, источник, даты (`decide.go:79,92`); год из названия отбрасывается `simKey`, поэтому одноимённые разных лет защищены датами: `TestDecideDates` («через день» -> `Separate`/`dates`), в `calibration.json` пять пар `other-year` (c04, c46–c49), метка `different`, сторона b на год позже при худшем `UniquePartner=true`, ни одна не AUTO (`TestCalibration`). Сомнительные (разный город, соседние дни, средняя схожесть) -> `Candidate` (`TestDecide`, `TestTracerDupes`). (b) `dedup_candidates=pending:104` на 10:42 при `dedup_auto_merge_*=false`; автоматических слияний нет. Ограничение: реальных одноимённых пар разных лет в снимках нет, года проверены на 5 синтетических парах |
| 4 | Администратор подтверждает и отклоняет кандидатов, получает ежедневную сводку; отклонённая пара не возвращается | ✓ VERIFIED | (a) `isAdmin` первой строкой в `onDupes`/`onDupe` (`bot/dupes.go:32,45`), `d:y` с подтверждением, `ConfirmCandidate`/`RejectCandidate` (`store/dedup.go:361,395`), отклонённые пропускаются по корням (`dedup/pass.go:60,82`, `rejectedRoots`), `UpsertCandidate` не трогает решённые (`store/dedup.go:123`); `TestDupesMerge`, `TestDupesNonAdmin`, `TestPassRejected`, `TestDigestDue`, `TestDigestTick`, `TestSendDupesDigest` PASS; `DigestLoop` запущен в `runBot` (`main.go:205-208`). (b) `confirmed:86|rejected:8`; `digest sent pending=104 fresh=104`; после принятия heroleague `dedup pass events=455 pairs=0 candidates=0 merged=0 rejected_skipped=8` — отклонённые 8 пар пропущены и не вернулись. Не наблюдено верификатором: экраны Telegram, периодичность сводки по расписанию (human-verification #2, #3) |
| 5 | Пороги откалиброваны на размеченном наборе реальных пар и настраиваются без правки кода | ✓ VERIFIED | (a) `calibration.json`: 81 пара (76 из реальных снимков Марафонца и ПроБЕГа 29–30.09 + 5 синтетических), все `proposed=false`; `TestCalibration` на HEAD: `false_auto=0 missed=0`; пороги по умолчанию `High 0.7`, `Low 0.3` (`dedup/config.go:44-50`) подобраны по калибровке; `LoadConfig` читает восемь `DEDUP_*` с проверкой диапазонов (`config.go:70-113`, `TestLoadConfig`, `TestWorkerDedupConfigFirst`); `docker-compose.yml:36-44` передаёт `DEDUP_*` в bot и worker. (c) метки подтверждены владельцем — только по 05-07-SUMMARY и README набора. Метки в production-базе не проверялись (набор строится из фикстур, не из production) |

**Score:** 14/16 must-haves verified (5 критериев ROADMAP + 11 требований; PARTIAL: критерий 2 и DDP-03).

### Requirements Coverage

| Requirement | Описание (кратко) | Статус | Доказательство |
|-------------|-------------------|--------|----------------|
| ING-12 | Новый источник без изменения интерфейса ingestion; второй и (утверждённый) третий источники подключены | ✓ VERIFIED (с оговоркой критерия 1) | (a) `Source` не менялся, `probeg.New`, `heroleague.New` в `main.go:150`, `TestPayloadStable`, `TestPolicyGate`; (b) оба источника `ok`, `accepted=t`. Третий источник утверждён владельцем (D-22), в планах фазы его не было |
| DDP-01 | Кандидаты по стране и пересечению диапазонов дат | ✓ VERIFIED (с формулировкой) | (a) `Decide`: равный непустой `CountryCode` (`decide.go:79`), `Pass` отсекает пары разных стран до `Decide` (`pass.go:76`), `TestDecide` («страна пустая/разная»). Реализация шире текста требования: в кандидаты попадают и пары с зазором до 1 дня (D-05), автослияние — только при пересечении. Текст DDP-01 в REQUIREMENTS.md этого допуска не отражает |
| DDP-02 | Схожесть названий в Go без `pg_trgm`; схема и пороги конфигурируемы | ✓ VERIFIED | (a) `dedup/sim.go` (триграммный Jaccard по рунам); `grep pg_trgm` по `*.go`/`*.sql` пуст; `LoadConfig`; значения по умолчанию 0,7 / 0,3 совпадают с обновлённой формулировкой REQUIREMENTS; `TestLoadConfig`, `TestDefaultConfig`, `TestNameSim` PASS |
| DDP-03 | Высокая -> автоматическая связь; средняя -> отдельное событие + запись в `dedup_candidates`; низкая -> отдельное событие | ⚠ PARTIAL | **Подтверждено:** средняя и низкая ветки — (a) `Decide`/`Pass` (`TestDecide`, `TestTracerPass`), (b) `pending:104` кандидатов, прочее остаётся отдельными событиями. Высокая ветка **в коде и тестах**: `decide.go:109` (`AutoMerge` при флаге, `NameSim >= High` без бонуса ссылки, пересечение дат, равный непустой `city_key`, `UniquePartner`), `pass.go:112-123` сливает через `MergeEvents`; `TestTracerPass` слияние `auto`, калибровка 45 AUTO-пар окна без ложных. **Не подтверждено:** в production автоматическая связь выключена (`DEDUP_AUTO_MERGE=false`, `config.go:47`, `dedup_auto_merge_bot/worker=false` во всех трёх логах) и никогда не включалась (задача 6 плана 05-08 не выполнялась); шаги «включить -> проверить `merged=` -> выключить» не отрабатывались. **Рассуждение:** функциональность требования создана и откалибрована, отключение — осознанное решение владельца (D-08 «выключено до калибровки», D-23 «релиз с ручным слиянием»); D-21 (`would_auto`) в требование DDP-03 дословно не входит и пробелом фазы не считается. Но буквальный текст «автоматическая связь» в релизе не работает, поэтому PARTIAL и явное решение владельца (human-verification #4), а не VERIFIED |
| DDP-04 | Совпадение ссылки регистрации — только доп. сигнал, не основание для автослияния | ✓ VERIFIED | (a) `LinkKey` (`sim.go:72`): соцсети, пустой путь, `?`/`#` -> ""; бонус только при `NameSim >= Low` (`decide.go:101-104`); автослияние требует `NameSim >= High` без бонуса (D-20, `decide.go:109`); `TestLinkKey`, `TestDecideLink` PASS |
| DDP-05 | Записи одного источника не склеиваются без `group_key`; дистанция не участвует в идентичности | ✓ VERIFIED | (a) `sourcesAllowed` (`decide.go:117`) — пара одного источника только при равном непустом `GroupKey`; `Prepare` сворачивает только по равному непустому ключу (`prepare.go:102-137`); дистанций в `Side`/`Decide` нет; `TestDecideSources`, `TestPassSameSource`, `TestPrepareGroupKey` PASS. (b) `collapsed=752` у ПроБЕГа совпало с фикстурой (упоминание в 05-SECURITY T-05-03/T-05-40) |
| DDP-06 | `source_events` не удаляются, `event_id` переключается, проигравший получает `merged_into_id` и скрывается, цепочки схлопываются до корня | ✓ VERIFIED | (a) `MergeEvents` (`store/dedup.go:223-226`): `UPDATE source_events SET event_id`, `UPDATE events SET merged_into_id, is_listed=false`, перевешивание цепочки одним шагом; `RootEventID` с `maxMergeHops` (`listing.go:127-153`); `TestMergeEvents`, `TestMergeEventsConcurrent`, `TestRootEventID`, `TestMigration00004` PASS. (b) `events_merged=86`, выдача -86 |
| DDP-07 | `/dupes` только для `ADMIN_TG_IDS`, подтвердить/отклонить, отклонённая не предлагается повторно | ✓ VERIFIED | (a) см. критерий 4; `callback d:*` с проверкой админа на каждом нажатии; `TestDupesNonAdmin`, `TestPassRejected`. (b) `rejected:8`, `rejected_skipped=8`, `pairs=0` после принятия heroleague. Экраны Telegram верификатором не наблюдались |
| DDP-08 | Ежедневная сводка вместо уведомления на каждую запись | ✓ VERIFIED | (a) `DigestLoop.Tick` (`digest.go:74`): раз в сутки после `DEDUP_DIGEST_AT` (по умолчанию 10:00 МСК), только при `fresh > 0`, отметка `notified_at` после успешной отправки; `TestDigestDue`, `TestDigestTick`. (b) `digest sent` (pending 104, fresh 104) 10:41 МСК. Периодичность по расписанию в production не наблюдалась (см. human #3) |
| DDP-09 | Пороги и схема оценки откалиброваны на размеченном наборе реальных пар (порядка 30–50); автослияние на нём без ложных слияний | ✓ VERIFIED (с границами) | (a) `TestCalibration` на HEAD: 81 пара, `confirmed=81 proposed=0 false_auto=0 missed=0`; MATRIX: different -> `auto=0`; набор размечен, пороги `Low 0,4 -> 0,3` выбраны по пропускам c33–c35. **Рассуждение по D-21:** DDP-09 требует калибровки и отсутствия ложных слияний на наборе — это выполнено; тест полноты AUTO-пар окна фикстур (D-21, замечание M2 ревью) в формулировку требования не входит и в этом релизе актуален только при включении автослияния, поэтому не является пробелом фазы. Границы: набор не содержит пар heroleague и пар с BY; построен на снимках 29–30.09, не на production-базе; тест не падает при появлении неразмеченной AUTO-пары (вручную на HEAD все 45 AUTO-пар окна размечены — по ревью; в этом прогоне подтверждено лишь `proposed=0`, `false_auto=0`); подтверждение меток владельцем — (c). Включать автослияние до D-21 нельзя (условие, зафиксированное D-23) |
| DDP-10 | Дистанции слитого события — объединение с допуском по метрам | ✓ VERIFIED (a) | `event.Fold` (`fold.go:37`, допуск `DEDUP_DISTANCE_TOL_M`, по умолчанию 100, максимум 1000), `TestFoldDistances`, `TestFoldDeterministic`, `store.TestMergeEvents` (21100 + {21097, 42195} -> union), `TestFoldInvariant` PASS. В production объединённые дистанции слитой карточки не наблюдались |

### Orphaned requirements

Нет. Все 11 ID из ROADMAP присутствуют в `requirements:` планов 05-01…05-08 и в таблице REQUIREMENTS.md (все «Pending» — чекбоксы не отмечены намеренно, фаза не закрыта).

## Required Artifacts (уровни: существует / содержательный / подключён / данные текут)

| Артефакт | Роль | Статус | Детали |
|----------|------|--------|--------|
| `internal/dedup/{sim,decide,config,pass,digest}.go` | ядро, проход, сводка | ✓ VERIFIED | не заглушки; `Pass` вызывается через `Runner.PostApply` (`main.go`, `newRunner`), `DigestLoop` в `runBot` |
| `internal/event/fold.go` | свёртка полей и дистанций | ✓ VERIFIED | используется `SaveEventFor` и `MergeEvents` |
| `internal/store/dedup.go`, `events.go`, `listing.go` | слияния, кандидаты, резолвер корня, единая запись | ✓ VERIFIED | SQL только в `internal/store` (grep `pgx` вне store пуст) |
| `migrations/00004_dedup.sql` | `source_events.fields`, `decided_at/by`, `notified_at`, индекс | ✓ VERIFIED | колонки NULL, `Down` симметричен (`TestMigration00004`); в production применена (`fields_null=…|0` в логах) |
| `internal/bot/{dupes,callback,list}.go` | `/dupes`, `d:*`, карточка корня | ✓ VERIFIED (a) | обычный текст без `parse_mode` (`plain: true`), `validURL`; `sourceNames` включает `heroleague` |
| `internal/ingest/sources/probeg/` | адаптер ПроБЕГа | ✓ VERIFIED | один GET `CalendarURL`, `NoFallback`, `OutOfScopeMax 0.40`, фикстура + golden, SHA фикстуры совпал с README (`d0166c26…`) |
| `internal/ingest/sources/heroleague/` | третий источник (вне планов) | ✓ VERIFIED (a) | один GET JSON-API, `RUOnlyFallback`, SHA фикстуры совпал (`d3b87514…`); пар в калибровке нет |
| `internal/normalize/{ru_cities.go,ru_cities.txt,ru_cities_ambiguous.txt,country.go}` | справочник городов РФ, страны каталога | ✓ VERIFIED | `ru_cities.txt` — Zenodo, атрибуция в шапке, 1103 строки; неоднозначных 11 |
| `docker-compose.yml`, `deploy/vps/deploy-verify.sh`, `deploy/README.md`, `.env.example` | выкат Phase 5 | ✓ VERIFIED | якорь `x-app-env`, раздел «Дедупликация» в verify, `DEDUP_AUTO_MERGE` не включён по умолчанию нигде |

### Key Link Verification

| From | To | Via | Статус | Детали |
|------|----|-----|--------|--------|
| `Runner.Run`/`Accept` | `dedup.Pass` | `PostApply` (`runner.go:197,325`; `main.go newRunner`) | ✓ WIRED | вызывается после `FinishRun`/принятия, ошибка только WARN; `TestPostApply` |
| `dedup.Pass` | `store.MergeEvents`/`UpsertCandidate` | вердикт `Decide` | ✓ WIRED | слияние только при `c.AutoMerge` (`pass.go:112`) |
| `bot /dupes` callbacks | `store.ConfirmCandidate`/`RejectCandidate` | `b.Store.*` после `isAdmin` | ✓ WIRED | `Runner.Fold` передаётся из `newRunner` |
| `SaveEventFor` | `event.Fold` -> `saveEventTx` | `parts > 1` | ✓ WIRED | `store/events.go:544-552` |
| `bot.onCard` | `store.RootEventID` | карточка слитого | ✓ WIRED | `bot/list.go:137` |
| `DigestLoop.Send` | `Bot.SendDupesDigest` | `main.go:205` | ✓ WIRED | кнопка `cbDupeOpen` |
| `DEDUP_*` env | `bot` и `worker` | `docker-compose.yml:36-44`, `LoadConfig` в обоих | ✓ WIRED | подтверждено `dedup_auto_merge` в строках `started` обоих контейнеров |

### Data-Flow Trace (Level 4)

| Артефакт | Данные | Источник | Реальные данные | Статус |
|----------|--------|----------|-----------------|--------|
| Экран `/dupes` | кандидат, стороны, сигналы | `dedup_candidates` + `GetEvent` + `EventSources` | да | ✓ FLOWING (a); в production `pending:104 -> 0`, решения записаны |
| Карточка корня | дистанции, источники | `events` + `event_distances` + `source_events` корня | да | ✓ FLOWING (a); в production не наблюдалась |
| Сводка | `pending`, `fresh` | `CandidateCounts` | да | ✓ FLOWING (b): `pending 104 fresh 104` |
| `Pass` | события окна | `DedupEvents` (только `Complete`, т. е. все записи с `fields`) | да | ✓ FLOWING; `fields_null=…|0` у трёх источников |

### Behavioral Spot-Checks

| Поведение | Команда | Результат | Статус |
|-----------|---------|-----------|--------|
| Весь набор тестов | `go test -p 1 -count=1 ./...` (локальная БД) | все пакеты `ok`; 443 PASS / 0 FAIL / 0 SKIP | ✓ PASS |
| Калибровка | `TestCalibration` (в составе прогона) | `pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0` | ✓ PASS |
| Форматирование и vet | `gofmt -l .`, `go vet ./...` | пусто | ✓ PASS |
| Отладочные маркеры | `grep TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER` по `internal cmd deploy migrations` | 0 совпадений | ✓ PASS |
| Зависимости | `git diff 86bdc42 HEAD -- go.mod go.sum` | пусто | ✓ PASS |
| Probe execution | скрипты `probe-*.sh` планами фазы не объявлены | — | SKIPPED |

## D-17 инварианты и prohibitions

| Инвариант / запрет | Статус | Доказательство |
|--------------------|--------|----------------|
| D-17.1 единственная точка записи полей и дистанций `events` — `saveEventTx` | ✓ VERIFIED (a) | Полный grep INSERT/UPDATE по `events`/`event_distances` вне `_test.go`: `store/events.go:228,240,350` (все внутри `saveEventTx`/`insertEventSQL`/`updateEventSQL`), `replaceDistances` (`events.go:188-193`) вызывается только из `saveEventTx`. Вызывающие `saveEventTx`: `SaveEvent`, `SaveEventFor` (`events.go:524,552`), `MergeEvents` (`dedup.go:237`). Прочие записи в `events` — только служебные `is_listed` (`events.go:586`, `observe.go:42`) и `merged_into_id`/`is_listed` в `MergeEvents` (`dedup.go:223,226`); канонические поля не затрагиваются |
| D-17.2 события не удаляются | ✓ VERIFIED (a) | `grep -ri "delete from (events|source_events)"` по `*.go` и `*.sql` — пусто. Есть `DELETE` только из `event_distances` (внутри `replaceDistances`), `source_alerts` и `pending`-кандидатов `dedup_candidates` (`dedup.go:148,253`; `confirmed`/`rejected` не удаляются). FK `merged_into_id`, `source_events.event_id` — `ON DELETE RESTRICT` (`00001_init_schema.sql:41,62`) |
| D-17.3 общий резолвер корня `store.RootEventID` | ✓ VERIFIED (a) | единственная реализация `listing.go:134`; потребители: `bot/list.go:137`, `dedup/pass.go:166,173`, `store/dedup.go:303,307`; другого хождения по `merged_into_id` в боте и дедупликации нет |
| 05-01 запрет: город РФ вне справочника / fallback для `no_fallback` | ✓ VERIFIED (a) | `probeg.Policy` = `NoFallback`; `TestIsRFCity`, `TestParseRows` |
| 05-01/05-05 запрет: склейка внутри источника иначе, чем по `GroupKey` | ✓ VERIFIED (a) | см. DDP-05 |
| 05-01 запрет: менять payload/`content_hash`/golden Марафонца | ✓ VERIFIED (a) | `TestPayloadStable`; golden Марафонца в git без изменений с `86bdc42` |
| 05-02 запрет: автослияние по ссылке при `NameSim < Low`; дистанции в решении; автослияние по умолчанию | ✓ VERIFIED (a) | `TestDecideLink`, `TestDefaultConfig`; `config.go:47` |
| 05-03 запрет: карточка скрытого корня; корень не через `RootEventID`; SQL вне store; новые зависимости; запросы к ПроБЕГу | ✓ VERIFIED (a) | `TestHiddenAndMerged`; grep SQL вне store пуст; `go.mod/go.sum` без изменений; тесты ПроБЕГа на `httptest` |
| 05-04 запрет: Country «Россия» без определения; GroupKey одиночным; запись для «Забег отменён!» | ✓ VERIFIED (a) | `TestParseRows`, `TestPreparedStats`; judgment-часть прочитана в `probeg/parse.go` и принята ревью (05-SECURITY T-05-15/16) |
| 05-05 запрет: удаление `events`/`source_events`, изменение FK; запись в обход `saveEventTx` | ✓ VERIFIED (a) | см. D-17.1/D-17.2; `TestMigration00004` |
| 05-06 запрет: менять каталог без «Да, слить»; `/dupes` не-администратору; `parse_mode` на тексте источника | ✓ VERIFIED (a) | `dupeScreen` -> `plain: true`; `d:a`/`d:n` только экраны; `TestDupesMerge`, `TestDupesNonAdmin` |
| 05-07 запрет: автослияние по умолчанию; подгонка меток; правка `decide/sim/pass` в калибровочном плане | ✓ VERIFIED (a)/(c) | `DefaultConfig().AutoMerge=false`; факт неподгонки меток (judgment) — только (c) по SUMMARY, `proposed=false` у всех 81 |
| 05-08 запрет: автослияние по умолчанию в compose/.env.example/README; исполнитель на VPS; принятие ПроБЕГа с причинами кроме «первый прогон» | ✓ VERIFIED (a)/(b) | `.env.example:47` — закомментировано `false`; `docker-compose.yml:39` `${DEDUP_AUTO_MERGE:-}`; (b) причина принятия ПроБЕГа — только «первый прогон источника» |

**Unverified-prohibition flags (judgment-tier):** подтверждение «метки не подгонялись под пороги» (05-07) и «исполнитель не подключался к VPS» (05-08) — non-authoritative оценка по документам, человеческий просмотр рекомендован; в `human_verification` отдельным пунктом не вынесены, потому что на релиз не влияют.

## D-24 согласованность реализации

Подтверждено в коде: список стран каталога один на все источники — `allowedCountries = {RU, BY}` в `internal/normalize/country.go:50`, проверка `IsAllowedCountry` (`country.go:53`) и `Country`/`countryIn` (`place.go:173-188`) без списка на уровне источника. Источник определяет только страну записи и политику при её отсутствии: `Policy.CountryFallback` — `NoFallback` у probeg (`probeg.go:44`), `RUOnlyFallback` у marathonec (`marathonec.go:40`) и heroleague (`heroleague.go:38`). Запись с явной другой страной остаётся `out_of_scope` при любой политике. В `Pass` пары разных стран отсекаются (`pass.go:76`).

Это **расхождение формулировки и реализации, не пробел**: D-24 пишет «ограничения по странам задаются на уровне источников», тогда как фактически ограничение общее, а источники лишь выбирают страну записи и fallback; то же прямо оговорено в самом D-24 («отдельного списка у источника нет… в этой фазе не выполняется»). Ни одно требование и ни один критерий Phase 5 пер-источникового списка не требует. Для BY-событий отдельной калибровки дедупликации нет (автослияние выключено).

## Ограничения базы доказательств

1. **Исторические логи отсутствуют.** Не существуют `p5-deploy-verify.log`, `p5-verify-fields.log`, `p5-verify-accepted.log` (имена из `deploy/README.md`/плана 05-08); их роль играют `deploy-verify.log` (01.10 10:42, образ `faa8ee511c52`), `p6-deploy-verify.log` (13:49, `e8005e8f2940`), `p6-verify-accepted.log` (13:55, `e8005e8f2940`). Нет снимка до принятия ПроБЕГа (состояние «кандидатов 0, слияний 0» не зафиксировано), не сохранены вывод `sanity-report`, тексты алертов, `IMAGE_PREV`, строка `run-once`. Автоматические ворота `DEPLOY P5 OK` и `ACCEPTED P5 OK` в написанном виде **никогда не запускались**; критерии 05-08 проверены вручную по строкам логов (это сделал и я в этом прогоне: перечитаны строки `CHECK …`, `source_run=`, `fields_null=`, `dedup_candidates=`, `events_merged=`, `events_listed=`, `dedup_auto_merge_*`, `digest sent`, `dedup pass`, теги образов).
2. **Текущий тег образа после 13:55 не подтверждён.** Коммит `6d9efa3` (локальный HEAD, «Improve Telegram bot search UX») **не считается развёрнутым**: в сохранённых логах виден только `e8005e8f2940`. Всё сказанное в отчёте о production относится к образам `faa8ee511c52` и `e8005e8f2940`, всё о HEAD — к коду и тестам.
3. **Нет полной верификации Telegram UX.** Слитая карточка в production, экраны `/dupes` и кнопка сводки верификатором не наблюдались; есть только автотесты с фейковым Bot API (`TestMergedCardOpensRoot`, `TestTracerDupes`, `TestDupesMerge`, `TestSendDupesDigest`) и строки лога (`dupe decided`, `digest sent`). Подтверждение — владельцу (раздел Human Verification).
4. **Слияния ручные только по состоянию флага.** `agg verify` не печатает разбивку auto/manual, `match_state` слияний не показан; «86 слияний — ручные» следует из `dedup_auto_merge_*=false` во всех трёх снимках и из того, что задача 6 не выполнялась (и `merged=` десять последних пар выглядят корректными дублями), но не из данных БД.
5. **heroleague.** Принят отдельно (прогон 7: `suspect` -> `ok`, `accepted=t`; parsed 20, expired 17 -> 3 события, `events_listed` 452 -> 455). Критерии D-22 «страна RU у всех записей» и «`UnrecognizedRegion` ≤ 10 %» не подтверждены: вывод `sanity-report --source heroleague` не сохранён. Планами фазы источник не охвачен; проверка «разрешённости» источника (аналог ворот Phase 1) в `.planning` не документирована — в коде указано лишь, что `robots.txt` не задаёт Crawl-delay и используется открытый JSON-API страницы.
6. **Первое наблюдённое `dedup pass` после принятия ПроБЕГа** (`events=404 pairs=0 candidates=0`) отрабатывало до заполнения `fields` у Марафонца (в проход входят только полные события); 104 кандидата появились после прогона Марафонца, строка которого (`run-once`) не сохранена. На итог не влияет, но цепочку «принятие -> кандидаты» нельзя восстановить по логам полностью.
7. **Калибровка не на production-данных.** Набор — фикстуры Марафонца (29.09) и ПроБЕГа (30.09); heroleague и BY в него не входят. Одноимённые события разных лет — только 5 синтетических пар.
8. **Слияние необратимо из бота** (экран подтверждения это сообщает); SQL-рунбук отмены не написан (ревью H1/Phase 6). 86 production-слияний проверить на ложные можно только человеком.

## Отложено в Phase 6 (не пробелы Phase 5)

Решение владельца D-23 (2026-10-01); источники: `05-CONTEXT.md` «Deferred», `05-REVIEW.md`. Ни одно из этих пунктов не требуется дословно ни критериями, ни требованиями Phase 5.

| Пункт | Суть | Почему не пробел |
|-------|------|------------------|
| D-21 / H1 | `would_auto` в `Decide`/`Pass`, видимость в `/dupes` и `deploy-verify.sh`, счётчик ошибок прохода | В коде отсутствует (grep `would_auto|WouldAuto` пуст), как и заявлено; нужен только для безопасного включения автослияния, которое не включено |
| M2 / D-21 | Тест полноты AUTO-пар калибровки + общая функция пар для `Pass` и теста; пары heroleague в калибровке | Не часть формулировки DDP-09 (см. рассуждение выше) |
| L1 | «Да, слить» пересчитывает корни: ожидаемая пара корней в callback | Администратор один (`admins=1` в строках `started`) |
| L2 | `MarkCandidatesNotified` отмечает кандидатов, появившихся между подсчётом и отправкой | Вторая сводка в худшем случае не теряется |
| L3 | Savepoint на пару, отдельный контекст для `PostApply` | `post-apply failed` в сохранённых логах — 0 |
| L4 | `GroupKey` серии ПроБЕГа без места | Условие: смена ключа после принятия даёт разовый всплеск `changed` |
| L5 | Статус проигравшего события, `match_state=manual` перезаписывается в `auto` при слиянии | Источники статус не выставляют |
| Прочее | сортировка очереди `/dupes` по оценке, SQL-рунбук отмены слияния, UX бота, технический долг Phase 4 (LO-06, IN-05…IN-07) | Усовершенствования |

**Риск потери:** в `ROADMAP.md` у Phase 6 («Production») цель, требования и критерии (MON-03, MON-04, PRD-02…PRD-07) этих пунктов не содержат; перенос зафиксирован только в `05-CONTEXT.md`, `05-REVIEW.md` и `STATE.md`. Расширение состава Phase 6 нужно оформить при её обсуждении, иначе D-21 останется обязательным условием автослияния без владельца.

## Расхождения документации и статусов

Намеренно не исправлялись (фаза не закрыта):

- `REQUIREMENTS.md`: чекбоксы ING-12 и DDP-01…DDP-10 не отмечены, таблица «Pending»; `ROADMAP.md`: Phase 5 не отмечена в списке фаз, в таблице «In Progress»; `STATE.md`: `status: executing`, `Resume file` — 05-08-SUMMARY.
- Найденный дрейф, помимо этого:
  1. `STATE.md`: во frontmatter `total_plans: 28`, `completed_plans: 27`, в «Performance Metrics» «Total plans completed: 28»; `last_activity_desc` («05-08 в работе») устарело относительно «Current Position».
  2. `ROADMAP.md`: у плана 05-08 стоит `[x]`, хотя задача 6 плана не выполнялась, а ворота `DEPLOY P5 OK`/`ACCEPTED P5 OK` не запускались; критерий «Plans: 8/8 plans executed» верен только в смысле «выполнены с отступлениями».
  3. `REQUIREMENTS.md` DDP-01: «по стране и пересечению диапазонов дат» — реализовано окно ±1 день для кандидатов (D-05); DDP-02 содержит и старые 0,7 / 0,4, и обновлённые 0,7 / 0,3.
  4. `05-07-SUMMARY.md`: заголовок «черновик (ждёт проверки меток владельцем)», `completed: null`, `status: complete` — история в теле файла дописана, шапка нет.
  5. `05-08-SUMMARY.md`: во frontmatter `commits: 0`, а в теле — `faa8ee5` (задачи 1–2); `requirements-completed: [ING-12, DDP-08, DDP-09]` при незапущенных автоматических воротах.
  6. D-07 («по полю, первое непустое по приоритету») реализован группами (заголовок, даты, место берутся целиком из одной записи — `fold.go` комментарий); это заявлено в коде, но в формулировке решения не отражено.
  7. `.env.example` и `DefaultConfig.SourcePriority` — `marathonec,probeg`; `heroleague` в списке нет и стоит после известных (безвредно, но приоритет явно не задан).
  8. Рабочее дерево: `deploy/README.md` изменён и `heroleague/testdata/README.md` не отслеживается — не закоммичено (коммитит владелец).

## Anti-Patterns Found

| Файл | Строка | Паттерн | Severity | Влияние |
|------|--------|---------|----------|---------|
| — | — | `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` | — | не найдено ни одного совпадения |
| `internal/dedup/pass.go` | 27-31 | Ошибка прохода откатывает весь проход (L3) | ℹ️ Info | отложено в Phase 6, в логах 0 случаев |
| `internal/store/dedup.go` | 361-389 | Корни пересчитываются при подтверждении (L1) | ℹ️ Info | один администратор |

Блокеров нет.

## Human Verification Required

Подробно — во frontmatter `human_verification`:

1. **Слитая карточка в production** — открыть карточку события из `merged=` (ожидается объединение дистанций и источников; кнопка проигравшего открывает корень).
2. **Экраны `/dupes`** — пройти кандидата: [Слить] -> «Да, слить» / [Отмена], [Разные], [Пропустить], конец очереди; проверить, что не-администратор команду не видит.
3. **Ежедневная сводка по расписанию** — подтвердить отправку в 10:00 МСК при новых кандидатах и работу кнопки [Открыть /dupes].
4. **Решение владельца по DDP-03** — принять «реализовано, откалибровано, выключено» либо пометить требование частично выполненным до Phase 6.
5. **Фактический тег образа на VPS** — подтвердить, что работает после 13:55 (в частности, развёрнут ли `6d9efa3`).

## Gaps Summary

Пробелов, блокирующих цель фазы, нет (`gaps: []`). Два PARTIAL (критерий 2, DDP-03) сводятся к одному: автоматическая связь выключена по решению владельца, а слитое событие в production наблюдалось только косвенно, через счётчики и список `merged=`. Приёмка трёх источников подтверждена сохранёнными локальными логами (с перечисленными выше провалами в полноте), а не воспроизводимыми воротами плана. Закрытие фазы (`phase.complete`, отметки в ROADMAP/REQUIREMENTS/STATE) — решение владельца после просмотра раздела Human Verification.

---

_Verified: 2026-10-01_

## Решение владельца после верификации (2026-10-01)

DDP-03 принят как выполненный в состоянии «реализовано, откалибровано, выключено» (D-25 в `05-CONTEXT.md`). Остальные пункты раздела проверки человеком (слитая карточка, экраны `/dupes`, сводка по расписанию, фактический тег образа) остаются открытыми; статус отчёта не менялся.

## Повторная проверка 2026-10-05

**Status:** passed. **Score:** 16/16.

Проверки этого прогона (HEAD `2d2ebba`; код совпадает с `f1aa1c6` — после него менялись только документы): `gofmt -l .` — пусто; `go vet ./...` — чисто; `go test -p 1 -count=1 -v ./...` с `TEST_DATABASE_URL` — 530 PASS, 0 FAIL, 0 SKIP, все 14 пакетов `ok`. Production — только чтением, по разрешениям владельца: `agg verify` 2026-10-05 08:38 МСК (`run-agg-phase4/p14-verify.log`): `DEPLOY VERIFY OK`, образ `aggregator:636deea312f7`, `events_merged=96`, `dedup_candidates=pending:0|confirmed:96|rejected:8|fresh:0`, `dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`, `dedup_would_auto=pending:0`.

Ответ владельца дословно (2026-10-05) на вопросы 1–5 и два пункта без наблюдения: «1–5 да, оба пункта снимаю, закрывай фазы».

Ключевые тесты фазы на текущем коде: `TestCalibration` — `CALIBRATION pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0 high=0.70 low=0.30`; `TestMergedCardOpensRoot`, `TestTracerPass`, `TestPassRejected`, `TestDupesNonAdmin`, `TestDigestDue`, `TestDigestTick`, `TestFoldDistances` — PASS. Интерфейс `ingest.Source` (4 метода) не менялся: четвёртый источник `tristyle` подключён 2026-10-04 одной строкой регистрации — критерий 1 подтверждён ещё раз на практике.

| Пункт прежней проверки | Итог | Основание |
|---|---|---|
| Критерий 2 (PARTIAL): карточка слитого события в production | VERIFIED | вопрос 1 — «да»: владелец слил 7 пар Tristyle с ПроБЕГом через `/dupes` 2026-10-04…05 и подтвердил карточку слитого события (одна карточка, объединённые дистанции, оба источника) |
| DDP-03 (PARTIAL): автослияние реализовано, в релизе выключено | VERIFIED решением владельца | вопрос 4 — «да»: требование считается выполненным «в коде и конфигурации»; `DEDUP_AUTO_MERGE=false` не менялся, включение — отдельным решением после D-21 |
| Экран `/dupes` в production | VERIFIED | вопрос 2 — «да»; в логе бота 2026-10-04 — семь строк `dupe decided … action: merge`, слияний без подтверждения нет (`merged: 0` в проходе) |
| Ежедневная сводка по расписанию 10:00 МСК | снят владельцем | в production наблюдались только отправки вне слота (сразу после прохода); суточная периодичность проверена `TestDigestDue`, `TestDigestTick`. Владелец: «оба пункта снимаю» |
| Тег образа на VPS | VERIFIED | `agg verify`: `aggregator:636deea312f7` |

Не проверено и остаётся известным ограничением: сводка дублей строго по расписанию в production не наблюдалась (пункт снят решением владельца, не наблюдением).
