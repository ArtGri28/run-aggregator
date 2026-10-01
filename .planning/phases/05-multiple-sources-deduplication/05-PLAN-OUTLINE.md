# Phase 05: Multiple Sources & Deduplication — Plan Outline

**Phase:** 05-multiple-sources-deduplication
**Plans:** 8 plan(s) in 4 wave(s)
**Mode flags:** TRACER_MODE=true, REVERSIBILITY_GATES=true, MVP_MODE=false, granularity coarse, ASVS L1 (block on high)

8 планов вместо 5–7: при слиянии любой пары задачи выходят за правило «не более ~5 файлов на задачу» (адаптер ПроБЕГа с фикстурой 618 КБ, слой хранения + `Pass`, бот), а три плана волны 1 независимы и идут параллельно.

| Plan ID | Objective | Wave | Depends On | Requirements | Decisions (D-NN) | Threat ID range | Key files |
|---------|-----------|------|-----------|-------------|------------------|-----------------|-----------|
| 05-01 | Общий конвейер под второй источник: справочник городов РФ и префиксы поселений (+LO-01), поля контракта `EndDate`/`GroupKey`, сворачивание серий в `Prepare` (`Stats.Collapsed`), `Policy.OutOfScopeMax`, хук `Runner.PostApply`, строки санити-отчёта (страна, серии) | 1 | none | ING-12, DDP-05 | D-01, D-03, D-04, D-14, D-15 | T-05-01…T-05-05 | internal/normalize/{ru_cities.go,ru_cities.txt,ru_cities_ambiguous.txt,ru_cities_test.go,place.go,place_test.go}; internal/ingest/{source.go,prepare.go,prepare_test.go,runner.go,runner_test.go,report.go,report_test.go} |
| 05-02 | Чистое ядро дедупликации: `event.Fold` (поля по приоритету, объединение дистанций с допуском), `dedup` sim/decide/config (окно ±1 день, триграммный Jaccard в Go, сигнал ссылки, правило одного источника, условия автослияния, `DEDUP_*`) | 1 | none | DDP-01, DDP-02, DDP-03, DDP-04, DDP-05, DDP-10 | D-05, D-06, D-07, D-08 | T-05-06…T-05-09 | internal/event/{fold.go,fold_test.go}; internal/dedup/{sim.go,sim_test.go,decide.go,decide_test.go,config.go,config_test.go} |
| 05-03 | Карточка слитого события открывает корень (`store.RootEventID`), подпись «ПроБЕГ» в источниках; долг Phase 4 в боте: LO-03, LO-05, IN-01, IN-02, IN-04 | 1 | none | DDP-06 | D-16, D-17 | T-05-10…T-05-14 | internal/store/{listing.go,listing_test.go}; internal/bot/{list.go,list_test.go,filters.go,filters_test.go,tg.go,tg_test.go,bot.go,bot_test.go} |
| 05-04 | Адаптер ПроБЕГа через `ingest.Source`: один GET `/races/year_ahead/`, чистый `Parse` (год по `FetchedAt`, диапазоны дат, групповые строки → `GroupKey`, страна по месту и справочнику, пропуск «Забег отменён!», дистанции без мусора), фикстура + SHA-256 + golden, регистрация в `newSources` | 2 | 05-01 | ING-12, DDP-05 | D-01, D-02, D-03, D-13, D-14, D-15 | T-05-15…T-05-20 | internal/ingest/sources/probeg/{probeg.go,parse.go,probeg_test.go,parse_test.go,prepared_test.go,testdata/README.md,testdata/year_ahead-2026-09-30.html,testdata/year_ahead.golden.json,testdata/prepared.golden.json}; cmd/aggregator/{main.go,main_test.go} |
| 05-05 | Хранение и проход дедупликации: миграция `00004_dedup.sql`, `SaveEventFor` пишет `source_events.fields` и собирает поля через `event.Fold`, `store` слияния/кандидатов/отклонений, `dedup.Pass` (advisory lock, кандидаты, автослияние при включённом конфиге) | 2 | 05-01, 05-02, 05-03 | DDP-01, DDP-03, DDP-05, DDP-06, DDP-07, DDP-10 | D-05, D-06, D-07, D-08, D-10, D-17 | T-05-21…T-05-27 | migrations/00004_dedup.sql; internal/store/{events.go,dedup.go,dedup_test.go,store_test.go}; internal/ingest/runner.go; internal/dedup/{pass.go,pass_test.go} |
| 05-06 | `/dupes` для администратора (по одному кандидату, [Слить]→[Да, слить], [Разные], [Пропустить]), ежедневная сводка `DigestLoop` 10:00 МСК, проводка `PostApply`/`DigestLoop`/конфига в `cmd/aggregator`; долг Phase 4: IN-03, IN-08 | 3 | 05-03, 05-04, 05-05 | DDP-07, DDP-08 | D-09, D-10, D-11, D-17 | T-05-28…T-05-35 | internal/bot/{dupes.go,dupes_test.go,callback.go,callback_test.go,bot.go,admin.go}; internal/dedup/{digest.go,digest_test.go}; internal/alert/{alert.go,alert_test.go}; cmd/aggregator/{main.go,main_test.go} |
| 05-07 | Калибровочный набор 30–50 пар из двух фикстур (черновик с `proposed`-метками), `TestCalibration` (0 автослияний на `different`, каждая `same` ≥ кандидат), проверка меток владельцем (`checkpoint:human-verify`), итоговые пороги по умолчанию — autonomous: false | 3 | 05-02, 05-04 | DDP-02, DDP-09 | D-08, D-12 | T-05-36…T-05-38 | internal/dedup/{calibration_test.go,testdata/calibration.json,testdata/README.md}; internal/dedup/config.go (только значения по умолчанию, если калибровка их меняет) |
| 05-08 | Выкат на VPS — autonomous: false: `DEDUP_*` в `bot` и `worker` compose + `.env.example`, однострочники владельца (ship.sh → root-консоль с проверкой хеша → agg load/deploy/verify), первый прогон ПроБЕГа `suspect` → санити-отчёт → ручное принятие, повторный прогон Марафонца для `fields`, включение `DEDUP_AUTO_MERGE` только после калибровки | 4 | 05-06, 05-07 | ING-12, DDP-08, DDP-09 | D-04, D-08, D-11 | T-05-39…T-05-45 | docker-compose.yml; .env.example; deploy/README.md; deploy/oneliner.sh |

## Plan notes (форма каждого плана)

- **autonomous: false:** 05-01 и 05-05 (одна `checkpoint:decision` по REVERSIBILITY_GATES), 05-07 (метки владельца), 05-08 (действия владельца). Остальные — autonomous: true.
- **05-01** — `checkpoint:decision` (односторонняя дверь: новые поля `SourceEvent` меняют байты `payload` ПроБЕГа и, значит, `content_hash`; рекомендация — `omitempty` в конце структуры, `Stats.Collapsed` с `json:",omitempty"`, golden Марафонца не меняется) → tracer: запись с `EndDate`+`GroupKey` от тестового источника → `Prepare` → `Runner.Run` → событие с `end_date`, серия свёрнута в одну ближайшую дату (интеграционный тест PG) → расширение: `OutOfScopeMax`+`PostApply`+отчёт; справочник городов+префиксы (длинные первыми, кириллические тесты)+`maxCityKeyBytes` 64→60. Справочник: `towns.csv` Zenodo record 5151423 (CC BY-4.0) скачивается один раз (не probeg.org), в шапке `ru_cities.txt` — атрибуция, дата, SHA-256 исходника; без сети — `checkpoint:human-action` «положить файл».
- **05-02** — tracer: реальная пара Марафонец/ПроБЕГ (названия из RESEARCH Q3) → `Decide` = кандидат при `AutoMerge=false`, автослияние при `true` и совпавшем `city_key` → `Fold` двух событий даёт объединённые дистанции и поля по приоритету. Потом таблицы: кириллица, перестановка, стоп-слова, ссылка-соцсеть, один источник, `group_key`.
- **05-03** — tracer: событие с `merged_into_id` → callback карточки → карта корня (`RootEventID`, httptest Bot API). Потом LO-05/IN-01/IN-02 (filters.go), затем LO-03 (tg.go) + IN-04 (`privateOnlyMW` отвечает пустым `answerCallbackQuery`).
- **05-04** — tracer: `Fetch` (httptest) → `Parse` одной обычной строки фикстуры → `Prepare` → валидное RU-событие; `newSources` содержит `probeg`. Потом полный `Parse` на фикстуре (ловушки RESEARCH Pitfalls 1–4, «N человек», серии без города → out_of_scope), golden `Parse` и `Prepare`. Фикстура — копия `samples/probeg-year_ahead-2026-09-30.html`, SHA-256 `d0166c26f30c9928994dbf16e6ebe7f23e472e14d0d8b87450cd1bc58d443189` в `testdata/README.md`. probeg.org не запрашивать.
- **05-05** — `checkpoint:decision` (форма миграции 00004 и формат JSON в `fields`) → tracer: две записи одного забега от двух тестовых источников → `Runner.Run` ×2 → `PostApply`=`dedup.Pass` → строка `dedup_candidates` `pending` при выключенном автослиянии; при включённом — `MergeEvents`: проигравший `merged_into_id`, `source_events` целы → расширение: регрессия Fold (второй проход `Changed=0`, `updated_at` не сдвинулся), отклонённые пары по корням, параллельный `SaveEventFor` без тупика, промотирование `pending` после включения автослияния, `DEDUP_MAX_PER_EVENT`.
- **05-06** — tracer: администратор `/dupes` → экран первого `pending` → [Разные] → `rejected`, показан следующий; не-админ — ответ неизвестной команды. Потом [Слить]→[Да, слить]/[Отмена], [Пропустить], `d:o`, «Кандидат уже обработан»; затем `DigestLoop` + IN-03 + IN-08 + проводка в `main.go`.
- **05-07** — tracer: 3–5 пар → `TestCalibration` на двух фикстурах проходит → расширение до 30–50 пар (явные дубли, одноимённые разные, пограничные, «Вся Россия», дневной/ночной, 4–6 синтетических «другой год» с `synthetic: true`) → `checkpoint:human-verify` (владелец правит метки, `proposed: false`) → прогон теста; при ложных автослияниях — правка значений по умолчанию в `config.go`. Конфиг `workflow.human_verify_mode=end-of-phase` здесь не применяется: метки — блокирующее условие D-08/DDP-09 по прямому указанию оркестратора.
- **05-08** — по образцу 04-07/04-08: задача auto (compose: `${DEDUP_…:-}` в оба сервиса, `.env.example`, README, однострочники в `oneliner.sh`), затем `checkpoint:human-action` с точными однострочными командами: выкат образа (изменение compose ⇒ повтор шага 2 root-консоли с проверкой хеша), первый прогон ПроБЕГа `suspect` → `worker sanity-report --source probeg` → принятие кнопкой или `accept-baseline`; `worker run-once --source marathonec` для `fields`; затем включение `DEDUP_AUTO_MERGE=true` правкой `/opt/aggregator/.env` под root и `agg deploy <TAG>`. Без больших heredoc, без действий исполнителя на VPS.

## Coverage map

- **Requirements:** ING-12 → 05-01, 05-04, 05-08; DDP-01 → 05-02, 05-05; DDP-02 → 05-02, 05-07; DDP-03 → 05-02, 05-05; DDP-04 → 05-02; DDP-05 → 05-01, 05-02, 05-04, 05-05; DDP-06 → 05-03, 05-05; DDP-07 → 05-05, 05-06; DDP-08 → 05-06, 05-08; DDP-09 → 05-07, 05-08; DDP-10 → 05-02, 05-05.
- **Decisions:** D-01 → 05-01, 05-04; D-02 → 05-04; D-03 → 05-01, 05-04; D-04 → 05-01, 05-08; D-05 → 05-02, 05-05; D-06 → 05-02, 05-05; D-07 → 05-02, 05-05; D-08 → 05-02, 05-05, 05-07, 05-08; D-09 → 05-06; D-10 → 05-05, 05-06; D-11 → 05-06, 05-08; D-12 → 05-07; D-13 → 05-04; D-14 → 05-01, 05-04; D-15 → 05-01, 05-04; D-16 → 05-03; D-17 → 05-03, 05-05, 05-06.
- **Phase 4 deferred:** LO-01 → 05-01; LO-03, LO-05, IN-01, IN-02, IN-04 → 05-03; IN-03, IN-08 → 05-06.
- **Deferred ideas (не планируются):** третий источник, регион в UI, `/stats`, `/broadcast`, избранное, подписки, `event_changes`, `SourceEvent.Status`, LO-06, IN-05…IN-07.

## Cross-plan contracts

Имена фиксированы; планы, которые их создают, не переименовывают, планы-потребители не дублируют.

**ingest (05-01 создаёт; 05-04, 05-05, 05-06 используют)**
- `ingest.SourceEvent`: в конец структуры после `Description` — `EndDate string` с тегом `json:"end_date,omitempty"` и `GroupKey string` с тегом `json:"group_key,omitempty"`. Поле `Status` не вводится (D-15). `content_hash` Марафонца не меняется.
- `ingest.Policy.OutOfScopeMax float64` (0 = общий порог); применяется только в `Runner.Run` перед `Evaluate`, `Accept` не трогается.
- `ingest.Stats.Collapsed int` с тегом `json:",omitempty"`; новые счётчики страны/серий, если появятся, — тоже `omitempty`. `marathonec/testdata/prepared.golden.json` не меняется.
- Сворачивание в `Prepare`: на каждый непустой `GroupKey` остаётся одна запись с ближайшей датой ≥ today−1, остальные → `Stats.Collapsed`; порядок вывода = порядок ввода.
- `ingest.Runner.PostApply func(ctx context.Context) error`: nil-safe; вызывается после `finish` в успешной ветке `Run` и после `Accept`; ошибка → `Warn`, статус прогона не меняется.
- `ingest.Runner.Fold event.FoldConfig` (05-05 добавляет поле и передаёт в `SaveEventFor`; 05-06 заполняет в `newRunner`).

**normalize (05-01)**
- `normalize.IsRFCity(city string) bool`: неоднозначные имена из `ru_cities_ambiguous.txt` → false. Файлы `internal/normalize/ru_cities.txt` и `internal/normalize/ru_cities_ambiguous.txt` встраиваются через `//go:embed`.
- `maxCityKeyBytes = 60`; `cityPrefixes` дополнены `пгт.`, `п.г.т.`, `посёлок`/`поселок`, `ст-ца`/`станица`, `хутор`, `аул`, латинская `c.` и упорядочены от длинных к коротким.

**probeg (05-04)**
- Пакет `internal/ingest/sources/probeg`: `Code = "probeg"`, `CalendarURL = "https://probeg.org/races/year_ahead/"`, `CrawlDelay = 30 * time.Second`, `New(f *ingest.Fetcher) *Source`, политика `ingest.Policy{CountryFallback: normalize.NoFallback, CrawlDelay: CrawlDelay, OutOfScopeMax: <по фикстуре, ориентир 0.40>}`.
- Ключ серии: `GroupKey = "series:" + normalize.TitleNorm(title)`. `SourceEvent.Country = "Россия"` ставит сам `Parse`; без страны — пусто (no_fallback).
- `Parse` выводит год по `doc.FetchedAt` и отдаёт `Date`/`EndDate` в `dd.mm.yyyy`.
- Подпись источника в боте: `sourceNames["probeg"] = "ПроБЕГ"` (05-03).

**event (05-02)**
- `event.FoldConfig{Priority []string; DistanceTolM int}`.
- `event.Part{Source string; Event event.Event}`; `event.Fold(parts []event.Part, cfg event.FoldConfig) event.Event`: детерминирован; по полю — первое непустое по `Priority` (неизвестные коды — после известных, при равенстве — порядок входа); группы из одного источника: (`Title`,`TitleNorm`), (`StartDate`,`EndDate`), (`Region`,`City`,`CityKey`,`Timezone`); `Type` — первое не `TypeOther`; `Status` не трогает; дистанции — объединение с допуском `DistanceTolM`, метка от первого, сортировка по метрам.

**dedup (05-02 ядро; 05-05 `Pass`; 05-06 `DigestLoop`; 05-07 калибровка)**
- `dedup.Config{High, Low float64; AutoMerge bool; LinkBonus float64; MaxPerEvent int; DistanceTolM int; SourcePriority []string; DigestAt string}`; `dedup.DefaultConfig()`; `dedup.LoadConfig() (Config, error)` (ошибка называет переменную, не значение); `(Config).Fold() event.FoldConfig`.
- Env и значения по умолчанию: `DEDUP_HIGH=0.7`, `DEDUP_LOW=0.4`, `DEDUP_AUTO_MERGE=false`, `DEDUP_LINK_BONUS=0.10`, `DEDUP_MAX_PER_EVENT=3`, `DEDUP_DISTANCE_TOL_M=100`, `DEDUP_SOURCE_PRIORITY=marathonec,probeg`, `DEDUP_DIGEST_AT=10:00` (МСК).
- `dedup.NameSim(a, b string) float64` (триграммный Jaccard по рунам над `simKey`); `dedup.LinkKey(rawURL string) string` (хост+путь; "" — не сигнал: пустой путь, соцсети, `?`/`#`).
- `dedup.SourceRef{Code, GroupKey string}`; `dedup.Side{EventID int64; Sources []SourceRef; Title string; Start, End time.Time; CountryCode, CityKey, RegURL string}`; `dedup.Pair{A, B Side; UniquePartner bool}`.
- `dedup.Verdict` с константами `Separate`, `Candidate`, `AutoMerge`; `dedup.Reasons` (сериализуется в `dedup_candidates.reasons`); `dedup.Decide(p Pair, c Config) (Verdict, Reasons)`.
- `dedup.Pass(ctx context.Context, s *store.Store, c Config) error` (05-05).
- `dedup.DigestLoop{Store *store.Store; Admins []int64; Send func(ctx context.Context, chatID int64, text string) error; Now func() time.Time; Loc *time.Location; At string; Every time.Duration; Log *slog.Logger}` с методами `Run(ctx)` и `Tick(ctx) error` (05-06).

**store (05-03 `RootEventID`; 05-05 остальное; 05-06 потребитель)**
- `(*Store).RootEventID(ctx context.Context, id int64) (int64, error)` в `internal/store/listing.go`: идёт по `merged_into_id` до корня; `ErrNotFound`, если события нет. Используется D-16 и дедупликацией (D-17.3).
- `(*Store).SaveEventFor(ctx, sourceEventID int64, e *event.Event, fold event.FoldConfig) (created, changed bool, err error)` — единственная точка записи канонических полей/дистанций через `saveEventTx` (D-17.1).
- `store.ErrNotMergeable`; `(*Store).MergeEvents(ctx, loserID, winnerID int64, matchState string, decidedBy *int64, fold event.FoldConfig) error` (`matchState` ∈ `auto`, `manual`; победитель — корень с меньшим id; без `DELETE FROM events`).
- Для бота: `store.Candidate{ID int64; Score float64; Reasons json.RawMessage; EventA, EventB int64}` (корни); `(*Store).NextCandidate(ctx, afterID int64) (Candidate, error)` (`ErrNotFound` — очередь пуста); `(*Store).CandidateByID(ctx, id int64) (Candidate, error)`; `(*Store).ConfirmCandidate(ctx, id, adminID int64, fold event.FoldConfig) error` (`ErrNotMergeable`, если уже не `pending`); `(*Store).RejectCandidate(ctx, id, adminID int64) error`.
- Для сводки: `(*Store).CandidateCounts(ctx) (pending, fresh int, err error)` (`fresh` — `pending` с `notified_at IS NULL`); `(*Store).LastDigestAt(ctx) (time.Time, error)` (`MAX(notified_at)`); `(*Store).MarkCandidatesNotified(ctx) error`.
- Блокировка `Pass`: `pg_advisory_xact_lock` с константой `dedupLockKey` внутри `InTx`; порядок блокировок везде `source_events` → `events`, по возрастанию id.
- Идентичность строки кандидата: `event_id` = корень с меньшим id (B), `source_event_id` = минимальный `source_events.id` события A; отклонённость проверяется по корням обеих сторон.

**migration 00004 (05-05)** — `migrations/00004_dedup.sql`: `source_events.fields JSONB` (NULL); `dedup_candidates.decided_at TIMESTAMPTZ`, `decided_by BIGINT`, `notified_at TIMESTAMPTZ` (NULL); `CREATE INDEX idx_dedup_candidates_state ON dedup_candidates (state, id)`; `Down` симметричен. Новых таблиц нет.

**bot (05-06)** — callback: `d:o`, `d:m:<cid>`, `d:y:<cid>`, `d:n:<cid>`, `d:x:<cid>`, `d:s:<cid>` (≤ 23 байт); виды `cbDupeOpen`, `cbDupeAsk`, `cbDupeYes`, `cbDupeNo`, `cbDupeRej`, `cbDupeSkip`; обработчики `(*Bot).onDupes` (команда `dupes`, `MatchTypeCommandStartOnly`) и `(*Bot).onDupe` (префикс `d:`), `'d'` в `cbPrefix`; `(*Bot).SendDupesDigest(ctx context.Context, chatID int64, text string) error` прикрепляет [Открыть /dupes] (`d:o`). `alert.Loop.Loc *time.Location`; `time.FixedZone` удаляется из `admin.go` и `alert.go` (IN-08).

**deploy (05-08)** — `DEDUP_HIGH`, `DEDUP_LOW`, `DEDUP_AUTO_MERGE`, `DEDUP_LINK_BONUS`, `DEDUP_MAX_PER_EVENT`, `DEDUP_DISTANCE_TOL_M`, `DEDUP_SOURCE_PRIORITY`, `DEDUP_DIGEST_AT` передаются в оба сервиса `bot` и `worker` как `${NAME:-}`.

**Prohibitions (в `must_haves.prohibitions` соответствующих планов, без дескрипторов):**
- ни один файл фазы не содержит `DELETE FROM events`; FK на `events` остаются `ON DELETE RESTRICT` (05-05, 05-06);
- совпадение ссылки регистрации само не создаёт кандидата и не поднимает пару до автослияния (05-02);
- записи одного источника без равного непустого `group_key` не склеиваются (05-02, 05-05);
- автослияние выключено по умолчанию и включается только конфигурацией после калибровки (05-02, 05-07, 05-08);
- из бота слияние только после [Да, слить]; не-админ не видит `/dupes` и не меняет каталог через `d:*` (05-06);
- событие без определённой страны РФ не попадает в каталог (05-01, 05-04);
- нет запросов к probeg.org от исполнителей, новых Go-зависимостей, `pg_trgm`, SQL вне `internal/store` (все);
- текст источника уходит в Telegram только в `plain` (05-06);
- сводка не чаще раза в сутки и только при новых кандидатах (05-06).

**Flagged assumptions (spec-less fallback, каждый план выносит в свой раздел):** ING-12 «третий источник, если утверждён» — не утверждён, не подключается; DDP-01 окно ±1 день для кандидатов (D-05) шире «пересечения дат», автослияние — только при пересечении; DDP-03 «высокая уверенность → авто» до калибровки даёт кандидата (D-08); DDP-04 соцсети и пустой путь — не сигнал; DDP-07 отклонение блокирует пару по корням (после слияния A с C пара (A∪C, B) тоже не предлагается); DDP-09 калибровка на снимках 2026-09-29/30, а не на продакшн-БД; DDP-10 допуск 100 м — допущение A1.

## COVERAGE decision (ai-integration)

Детектор `api-coverage.cjs` по разделу ROADMAP фазы: `detected: false`. Проверка по содержанию подтверждает: внешнего API в фазе нет — ПроБЕГ берётся одним GET публичной HTML-страницы и разбирается `x/net/html`, без API, SDK, ключей и эндпоинтов; Telegram Bot API — интеграция Phase 4 (матрица в 04 COVERAGE.md), фаза добавляет только команду и callback на уже включённых методах. Чтобы повторный запуск детектора при запечатывании (по тексту планов со словами «Bot API», «wiring») не заблокировал фазу ложным срабатыванием, записан `COVERAGE.md` с обоснованным `No external API integration: …` без матрицы.

## Assumption-delta result

Детектор `assumption-delta` сработал (`pluralization`, термин «multiple» в заголовке фазы). Модель идентичности уже рассчитана на много источников: каноническое `events` и записи `source_events` с `UNIQUE (source_id, external_id)`, `dedup_candidates` и `merged_into_id` есть с миграции 00001, поэтому основное существительное — каноническое событие с N записями источников — не меняется. Монополию теряет одно неявное допущение: поля события берутся из той записи, которая пишется сейчас (`SaveEventFor` перезаписывает событие полями этой записи). Решение — **promote**: поля события становятся производной всех связанных записей (`event.Fold` по `source_events.fields` и настраиваемому приоритету, D-07), а «поля текущей записи» — частный случай для одного источника; `add-alongside` не берём, иначе два источника перетирали бы общее событие каждые 6 часов. Инвариантный тест (принят, план 05-05): два прогона двух источников подряд — на втором проходе `Changed = 0` и `updated_at` не меняется, для любого числа источников события.

## OUTLINE COMPLETE
