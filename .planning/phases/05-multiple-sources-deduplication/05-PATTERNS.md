# Phase 5: Multiple Sources & Deduplication - Pattern Map

**Mapped:** 2026-09-30
**Files analyzed:** 38 (новых ~24, изменяемых ~14)
**Analogs found:** 36 / 38 (2 без аналога: `event.Fold`, `dedup` advisory-lock/пул `Pass`; частичные аналоги указаны)

Все аналоги — git-отслеживаемые файлы (`git ls-files`), зеркал/runtime-копий нет. Номера строк — на состояние дерева 2026-09-30. Проект: Go, SQL только в `internal/store`, новых зависимостей нет, тексты и комментарии — по-русски, идентификаторы — английские (стиль существующего кода).

## File Classification

| Новый / изменяемый файл | Роль | Поток данных | Ближайший аналог | Качество |
|---|---|---|---|---|
| `internal/ingest/sources/probeg/probeg.go` | source (Fetch/Policy) | request-response (1 GET) | `internal/ingest/sources/marathonec/marathonec.go` | exact |
| `internal/ingest/sources/probeg/parse.go` | parser | transform (HTML→SourceEvent) | `internal/ingest/sources/marathonec/parse.go` | exact |
| `internal/ingest/sources/probeg/{parse,prepared,probeg}_test.go` | test | golden/transform | `marathonec/{parse_test,prepared_test,marathonec_test}.go` | exact |
| `internal/ingest/sources/probeg/testdata/{README.md,*.html,*.golden.json}` | fixture | file-I/O | `marathonec/testdata/README.md`, `calendar-beg.golden.json` | exact |
| `internal/normalize/ru_cities.go` (+ `ru_cities.txt`, `ru_cities_ambiguous.txt`) | utility (справочник) | transform (lookup) | `internal/normalize/regions.go` (индекс в `init`) + `migrations/embed.go` (`//go:embed`) | role-match |
| `internal/normalize/place.go` (правка: префиксы, `maxCityKeyBytes=60`) | utility | transform | он сам (`cityPrefixes`, строки 15, 76) | exact |
| `internal/ingest/source.go` (правка: `EndDate`, `GroupKey`, `Policy.OutOfScopeMax`) | model/contract | — | он сам (`omitempty`-поля, строки 38-43) | exact |
| `internal/ingest/prepare.go` (правка: `EndDate`, сворачивание `GroupKey`, `Stats.Collapsed`) | service | batch/transform | он сам | exact |
| `internal/ingest/runner.go` (правка: `PostApply`, `Policy.OutOfScopeMax` в `Evaluate`) | service | batch | он сам (`Run`, `Accept`) | exact |
| `internal/ingest/report.go` (правка: строки «страна», «серии») | utility | transform | он сам (`SanityReport`) | exact |
| `internal/event/fold.go` | utility (чистая) | transform | `internal/store/events.go` `normalizeDistances` (строки 157-172) + `canonicalFieldsEqual` | role-match |
| `internal/dedup/sim.go`, `decide.go` | utility (чистые) | transform | `internal/ingest/quality.go` `Evaluate`/`share`/`median` | role-match |
| `internal/dedup/config.go` | config | env→struct | `internal/ingest/config.go` (`LoadConfig`, `setValue`) | exact |
| `internal/dedup/pass.go` | service | batch (идемпотентный) | `internal/ingest/runner.go` `write`/`apply`/`observe` + `store.InTx` | role-match |
| `internal/dedup/digest.go` | service (цикл) | event-driven (тики) | `internal/alert/alert.go` `Loop` (`Tick`, `sendAll`, `Run`) | exact |
| `internal/dedup/testdata/calibration.json` + `calibration_test.go` | test/fixture | golden | `marathonec/prepared_test.go` (`-update`, SHA-сверка) | role-match |
| `internal/store/dedup.go` | store | CRUD + транзакция | `internal/store/events.go` (`SaveEventFor`, `SetOutOfScope`) + `internal/store/alerts.go` | exact |
| `internal/store/events.go` (правка: `SaveEventFor` → `fields` + `Fold`) | store | CRUD | он сам, строки 369-429 | exact |
| `internal/store/dedup_test.go` | test (PG) | CRUD | `internal/store/store_test.go` (`TestSaveEventFor`, `TestSetOutOfScope`) | exact |
| `migrations/00004_dedup.sql` | migration | DDL | `migrations/00003_source_alerts.sql` | exact |
| `internal/bot/dupes.go` | controller (handler) | request-response | `internal/bot/admin.go` (`onRuns`, `onRun`, `runsScreen`) | exact |
| `internal/bot/callback.go` (правка: `d:*`) | utility (кодек) | transform | он сам, `case "r":` (173-183), `encode` (202-207) | exact |
| `internal/bot/bot.go` (правка: хендлеры, `cbPrefix`, `privateOnlyMW`) | config/router | request-response | он сам (строки 54-59, 163-217) | exact |
| `internal/bot/list.go` (правка: `sourceNames`, `onCard` → корень) | controller | request-response | он сам (строки 119-163) | exact |
| `internal/bot/tg.go` (LO-03), `filters.go` (LO-05, IN-01/02), `admin.go`/`alert.go` (IN-08), `alert.go` (IN-03) | долг Phase 4 | — | сами файлы | exact |
| `internal/bot/dupes_test.go` | test | request-response | `internal/bot/admin_test.go` (`newAdminEnv`, `TestRunsNonAdmin`) | exact |
| `cmd/aggregator/main.go` (правка: `newSources`, `newRunner`, `runBot`) | config/wiring | — | он сам (строки 146-201) | exact |
| `docker-compose.yml`, `.env.example` (правка: `DEDUP_*`) | config | — | строки `QUALITY_*`/`OBSERVE_*` в обоих | exact |
| `internal/dedup/pass_test.go`, `digest_test.go` | test | — | `internal/alert/alert_test.go` (`recorder`, фейковые часы) | exact |

## Pattern Assignments

### `internal/ingest/sources/probeg/probeg.go` (source, request-response)

**Analog:** `internal/ingest/sources/marathonec/marathonec.go`

Копировать целиком, меняя константы и `Policy`:

**Структура** (marathonec.go:11-53):
```go
const (
	Code = "marathonec"
	CalendarURL = "https://marathonec.ru/calendar-beg/"
	CrawlDelay = 40 * time.Second
)
type Source struct { f *ingest.Fetcher; url string }
var _ ingest.Source = (*Source)(nil)
func New(f *ingest.Fetcher) *Source { return &Source{f: f, url: CalendarURL} }
func (s *Source) Code() string { return Code }
func (s *Source) Policy() ingest.Policy {
	return ingest.Policy{CountryFallback: normalize.RUOnlyFallback, CrawlDelay: CrawlDelay}
}
func (s *Source) Fetch(ctx context.Context) ([]ingest.RawDoc, error) {
	doc, err := s.f.Get(ctx, s.url, CrawlDelay)
	if err != nil { return nil, err }
	return []ingest.RawDoc{doc}, nil
}
func (s *Source) Parse(doc ingest.RawDoc) ([]ingest.SourceEvent, error) { return Parse(doc) }
```
Для ПроБЕГа: `Code = "probeg"`, `CalendarURL = "https://probeg.org/races/year_ahead/"` (D-13), `CrawlDelay = 30 * time.Second`, `CountryFallback: normalize.NoFallback`, `OutOfScopeMax` (новое поле `Policy`, ~0,35-0,40, [ASSUMED] — подобрать по первому прогону). Один `Get` на прогон: `RawDoc.FetchedAt` заполняет Fetcher (нужен `Parse` для года). Тест `s.url = srv.URL + ...` — приём из marathonec_test.go:36-49.

---

### `internal/ingest/sources/probeg/parse.go` (parser, transform)

**Analog:** `internal/ingest/sources/marathonec/parse.go`

**Импорты и утилиты — скопировать как есть** (parse.go:6-17, 214-249): `bytes`, `fmt`, `net/url`, `strings`, `golang.org/x/net/html`, `.../atom`, `aggregator/internal/ingest`; функции `isElem`, `attr`, `text` (NBSP/`<br>`→пробел, строки 227-243), `clean` (IN-04: невалидный UTF-8 и U+0000, 245-249), `absURL` (192-212, maxURLLen=2048), `externalID` (153-160).

**Каркас `Parse`** (parse.go:44-67) — чистая функция, ошибка структуры через `ingest.ErrStructureNotFound`:
```go
root, err := html.Parse(bytes.NewReader(doc.Body))
if err != nil { return nil, fmt.Errorf("разбор HTML: %w", err) }
table, cols := findTable(root)
if table == nil {
	return nil, fmt.Errorf("%w: нет таблицы со столбцами %s", ingest.ErrStructureNotFound, strings.Join(requiredCols, ", "))
}
var out []ingest.SourceEvent
for n := range table.Descendants() {
	if isElem(n, atom.Tr) && /* признак строки */ { out = append(out, parseRow(n, ...)) }
}
if len(out) == 0 {
	return nil, fmt.Errorf("%w: нет строк ...", ingest.ErrStructureNotFound)
}
```
Отличия ПроБЕГа (RESEARCH Q1): таблица без `data-name` — искать по строкам `<tr style="font-size: 11px;">` с 5-6 `<td>` (заголовок «№, Дата, Название, Город, Дистанции, Ссылки»); структуру проверять по селекторам, не по словам (D-02 Phase 3).

**Разбор строки** (parse.go:114-151): `cells` из `tr.ChildNodes()`, замыкание `cell(name)`, затем заполнение `ingest.SourceEvent{ExternalID, Title, Date, Distances, City, ...}`, `ev.URL, ev.RegistrationURL = links(...)`. Для ПроБЕГа `URL = "https://probeg.org/event/<n>/"`, `RegistrationURL` — только ссылка с текстом «Регистрация» (не «Сайт»).

**Новая логика (аналога нет, эскиз — RESEARCH Code Examples 599-616 и Pattern 1, 487-507):** название = первая ссылка `/event/<n>/`, исключая текст `^\s*\d+\s+человек`; >1 ссылки ⇒ серия: раскрыть на записи, `GroupKey = "series:" + normalize.TitleNorm(title)`; страна внутри `Parse` (`resolvePlace`: `normalize.ClassifyRegion(parts[0])` → `RegionForeign`/`RegionRF`, иначе `normalize.IsRFCity(city)`); год даты выводить по `doc.FetchedAt` и отдавать `dd.mm.yyyy` (не менять `normalize.yearFor`); «Забег отменён!» — пропускать (D-15); дистанции — текст span до « (» (Pitfall 3); серии без города — `Country=""` (D-14, попадут в out_of_scope).

---

### `internal/ingest/sources/probeg/*_test.go` и `testdata/` (test, golden)

**Analog:** `marathonec/parse_test.go`, `prepared_test.go`, `marathonec_test.go`, `testdata/README.md`

**Golden + SHA-сверка с README** (parse_test.go:37-104) — копировать механику: `flag.Bool("update", ...)`, чтение фикстуры, регэксп `SHA-256 очищенной фикстуры: ([0-9a-f]{64})` по README, двойной `Parse` на детерминизм, сравнение с `*.golden.json` (`-update` переписывает).

**Prepared golden** (prepared_test.go:19-117): `fixtureNow` = дата снятия фикстуры (для ПроБЕГа — 2026-09-30 12:00 UTC), `preparedRecord`, `prepareFile`, негативные проверки политики (`NoFallback`). Вызов: `ingest.Prepare(recs, fixtureNow, ingest.Policy{CountryFallback: normalize.NoFallback})`.

**Edge-фикстуры** (`edge-no-table.html`, `edge-empty-table.html`, `edge-rows.html` у Марафонца) → аналогично для ПроБЕГа: групповая строка, «N человек», «Забег отменён!», `dd.mm–dd.mm`, Беларусь, голый город, дистанция с `(+6322м)`.

**Фикстура:** копия `../../../../.planning/phases/05-multiple-sources-deduplication/samples/probeg-year_ahead-2026-09-30.html` → `probeg/testdata/` (csrf уже `REDACTED`; SHA-256 копии `d0166c26...d443189`, указан в RESEARCH Q1). `testdata/README.md` — по образцу Марафонца: происхождение/URL/дата/SHA, таблица замен, структура, дата-форматы, значения для normalize.

---

### `internal/normalize/ru_cities.go` (utility, lookup)

**Analog (индекс):** `internal/normalize/regions.go` строки 87-112; **analog (embed):** `migrations/embed.go`.

```go
// regions.go:87-112 — ключ нормализации + индекс в init
func regionKey(s string) string { ... }          // нижний регистр, ё→е, пунктуация→пробел
var rfIndex = map[string]string{}
func init() { for _, names := range rfSubjects { for _, n := range names { rfIndex[regionKey(n)] = names[0] } } }
```
```go
// migrations/embed.go:8-15
import "embed"
//go:embed *.sql
var FS embed.FS
```
Для справочника: `//go:embed ru_cities.txt` / `ru_cities_ambiguous.txt` → `string`, построить `map[string]bool` в `init`, ключ — как `cityLookupKey` из place.go:111-113 (город, lower, ё→е, схлопнутые разделители). `func IsRFCity(city string) bool` = есть в индексе и нет в ambiguous. Файл `.txt` — по одному имени в строке, шапка с атрибуцией CC BY-4.0 (Zenodo 5151423). Тест — таблица по образцу `place_test.go:11-33` (`tests := []struct{in, want string}` + цикл).

---

### `internal/normalize/place.go` (правка, utility)

**Analog:** он сам.

- Префиксы (place.go:15): `var cityPrefixes = []string{"городской округ ", "г.о. ", "город ", "пос. ", "п. ", "д. ", "с. ", "г.", "г "}` — **самые длинные первыми**; добавить `пгт.`, `п.г.т.`, `посёлок `/`поселок `, `ст-ца `/`станица `, `хутор `, `аул `, латинскую `c.`. Цикл `City` (20-29) использует `strings.EqualFold(s[:len(p)], p)` по байтам — для кириллических префиксов с `ё` проверить тестом (`len` в байтах одинаков у регистров, но `EqualFold` на срезе байт может резать руну; добавить в `TestCity` кейсы).
- LO-01: `maxCityKeyBytes = 64` (place.go:76) → `60` (бот принимает ≤ 60, `bot/callback.go:59`). Использование — `CityKey` строки 102-105. Обновить `TestCityKey` (проверка длины в `place_test.go`).

---

### `internal/ingest/source.go` (правка, контракт)

**Analog:** он сам (строки 24-44, 53-59).

Новые поля — **только необязательные `omitempty`**, в конец структуры, чтобы байты `payload` (→ `content_hash`) Марафонца не менялись:
```go
Country         string   `json:"country,omitempty"`      // уже так (строка 40)
RegistrationURL string   `json:"registration_url,omitempty"`
// добавить после Description:
EndDate  string `json:"end_date,omitempty"`
GroupKey string `json:"group_key,omitempty"`
```
`Policy` (53-59) — добавить `OutOfScopeMax float64` (0 = общий порог). `SourceEvent.Status` **не** вводить (D-15).

---

### `internal/ingest/prepare.go` (правка, batch/transform)

**Analog:** он сам.

- `EndDate`: в `event.Event{...}` литерале (prepare.go:172-186) добавить `EndDate` через `normalize.Date(rec.EndDate, now)` (тот же вызов, что `date, dateOK := normalize.Date(rec.Date, now)` на строке 95), nil при пустом/равном старту.
- Сворачивание `GroupKey`: после основного цикла (перед `st.Valid = len(out)`, строка 189) — оставить на непустой `GroupKey` одну запись с ближайшей датой ≥ `today-1` (`minDate` в строке 74); остальные — `st.Collapsed++` и удалить из `out`. Порядок выхода = порядок входа — сохранить.
- **Ловушка golden:** `Stats` без json-тегов, `TestPreparedGolden` Марафонца сериализует `ingest.Stats` целиком (`prepared.golden.json:2-17`: `"Parsed"`, `"Valid"`, ...). Новое поле `Collapsed int` добавит `"Collapsed": 0` и сломает golden Марафонца. Решение планировщика: либо поле с тегом `json:",omitempty"` (golden не меняется), либо осознанный `go test -update` с правкой одного ключа. Обязательно зафиксировать в плане.
- `Metrics()` (196-216): при желании добавить `collapsed` в структуру — значения только чисел (как сейчас).
- `seen`-дедуп по `ExternalID` (86-93) оставить: у ПроБЕГа каждая неделя серии — новый `<n>`.

---

### `internal/ingest/runner.go` (правка: `Policy.OutOfScopeMax`, `PostApply`)

**Analog:** он сам.

- Порог: `runner.go:160` `res.Status, res.Reasons = Evaluate(st, base, r.thresholds())`. Для источника: `t := r.thresholds(); if m := src.Policy().OutOfScopeMax; m > 0 { t.OutOfScopeMax = m }; Evaluate(st, base, t)`. `Accept` (238-296) `Evaluate` не вызывает — правка только в `Run`.
- Хук: новое поле `PostApply func(ctx context.Context) error` рядом с `Now`/`Log` (строки 42-45); вызывать **после** `finish(...)` в успешной ветке `Run` (строка 179) и после `Accept` (строка 295), через `fctx` (`finalizeCtx`), ошибка — `r.logger().Warn(...)`, статус прогона не меняется. Образец логирования — `log.Warn("raw save failed", "err", err)` (строка 197).
- Установка: `newRunner` в `cmd/aggregator/main.go:153-164` — одинаково для `worker` и `bot` (Pitfall 11 из комментария 150-152).

---

### `internal/ingest/report.go` (правка, transform)

**Analog:** он сам (`SanityReport`, строки 37-137).

Добавить в заголовок/таблицы строки «страна: RU / иностранная / не определена», «записей с `group_key` → ключей», «свёрнуто». Стиль — `fmt.Fprintf(&sb, ...)`, `writeList`, `mdCell`, детерминированный порядок (`slices.SortedFunc(maps.Keys(...))`). Тест — `report_test.go` (сравнение с ожидаемыми подстроками).

---

### `internal/dedup/config.go` (config, env→struct)

**Analog:** `internal/ingest/config.go`

**Таблица переменных + `setValue`** (config.go:41-79, 98-136): копировать структуру `vars := []struct{name string; dst any; positive bool}` и цикл с `os.LookupEnv` / `strings.TrimSpace`; ошибка `fmt.Errorf("%s: %w", v.name, err)` — **называет переменную, не значение**. `setValue` уже поддерживает `*string`, `*time.Duration`, `*int`, `*int64`, `*float64`; для `DEDUP_AUTO_MERGE` (bool) добавить `case *bool` с `strconv.ParseBool`, для `DEDUP_SOURCE_PRIORITY` — `*string` + `strings.Split(..., ",")`, для `DEDUP_DIGEST_AT` — `*string` формата `HH:MM`. Лучше не импортировать `ingest.setValue` (неэкспортируемая) — скопировать небольшую функцию в `dedup` (цикла импортов `dedup → ingest` избегать: `ingest` импортирует `store`, `dedup` импортирует `store` и, через `PostApply`, вызывается из `main`).

Переменные (RESEARCH Q3): `DEDUP_HIGH=0.7`, `DEDUP_LOW=0.4`, `DEDUP_AUTO_MERGE=false`, `DEDUP_LINK_BONUS=0.10`, `DEDUP_MAX_PER_EVENT=3`, `DEDUP_DISTANCE_TOL_M=100`, `DEDUP_SOURCE_PRIORITY=marathonec,probeg`, `DEDUP_DIGEST_AT=10:00`.

---

### `internal/dedup/sim.go`, `decide.go` (чистые функции)

**Analog:** `internal/ingest/quality.go` — `Evaluate` (60-92): чистая функция, ветвление по порогам, причины строками; `share`/`median` (94-125) как образец мелких чистых хелперов; табличные тесты `quality_test.go`.

**Ядро метрики** — готово в RESEARCH Q3 (строки 226-249, `grams`/`jaccard` по рунам с подбивкой `"  "+s+" "`). `Decide` — эскиз RESEARCH 618-642 (`Separate`/`Candidate`/`AutoMerge`). Условия авто: `AutoMerge` включён и `score ≥ High` и диапазоны пересекаются и `city_key` равен и непуст и ≤ 1 партнёра ≥ high (D-05, D-06, D-08). `linkBonus` — только при `name_sim ≥ Low` (DDP-04).

---

### `internal/dedup/digest.go` (service, event-driven)

**Analog:** `internal/alert/alert.go` — `Loop`

**Скелет** (alert.go:23-36, 99-111, 179-205): struct с `Store`, `Admins`, `Send`, `Now`, `Log`, `Every`; методы `now()`, `log()`; `Run` — первый `tick()` сразу, затем `time.NewTicker`:
```go
func (l *Loop) Run(ctx context.Context) {
	if len(l.Admins) == 0 { l.log().Warn("ADMIN_TG_IDS пуст: алерты отключены"); return }
	every := l.Every; if every <= 0 { every = time.Minute }
	tick := func() { if err := l.Tick(ctx); err != nil && ctx.Err() == nil { l.log().Warn("alert tick", "err", err) } }
	tick()
	t := time.NewTicker(every); defer t.Stop()
	for { select { case <-ctx.Done(): return; case <-t.C: tick() } }
}
```
**Принцип «состояние пишется только после успешной отправки»** (alert.go:113-115, 150-158, `sendAll` 165-177): `if !l.sendAll(...) { continue }` → потом `Store.Save...`. Для сводки: `UPDATE dedup_candidates SET notified_at = now() WHERE state='pending' AND notified_at IS NULL` только после успеха хотя бы одному админу. В лог — тип ошибки, не текст (`"err_type", fmt.Sprintf("%T", err)`, alert.go:171).

**Отличия (учесть сразу):** `Loc *time.Location` полем вместо `FixedZone` (закрывает IN-08); `Send(ctx, chatID, text string) error`, кнопка `[Открыть /dupes]` (`d:o`) добавляется на стороне бота; IN-03 — не останавливать обход на первой ошибке (`continue` + `errors.Join`). Тесты — `alert_test.go`: `recorder` (строки 98-113), фейковые `Now`, `storetest.Open`.

---

### `internal/dedup/pass.go` (service, batch)

**Analog (частичный):** `internal/ingest/runner.go` `write`/`apply` (298-353) + `store.InTx` (store.go:50-66).

Оркестрация без SQL: все запросы через `Store`-методы. Транзакция — `s.InTx(ctx, func(tx *store.Store) error {...})`. Advisory-lock (`pg_advisory_xact_lock(<const>)`) — метод `store` (SQL только там). Аналога «lock в транзакции» в репозитории нет → см. No Analog.

---

### `internal/event/fold.go` (utility, transform)

**Analog (частичный):** `internal/store/events.go`
- `normalizeDistances` (157-172): дедуп по метрам, метка первого вхождения, сортировка `sort.Slice(out, func(i,j int) bool { return out[i].Meters < out[j].Meters })`. `Fold` дистанций = то же + допуск `tolM` (элемент добавляется, если в уже взятых нет значения в пределах допуска; метка от первого).
- `canonicalFieldsEqual` (208-224) — список полей события; `Fold` обязан давать одинаковый результат для одинакового входа (иначе `Changed` растёт каждый прогон — Pitfall 6).

Пары полей из одного источника: (`Title`,`TitleNorm`), (`StartDate`,`EndDate`), (`Region`,`City`,`CityKey`,`Timezone`); `Type` — первое не `TypeOther`. Тип `event.Event` — `internal/event/event.go:51-79`. Тест табличный, стиль `normalize_test.go`/`quality_test.go`.

---

### `internal/store/events.go` (правка `SaveEventFor`)

**Analog:** он сам, строки 369-429.

**Сейчас:** для связанной записи `e.ID = *eventID` → читается `status` → `saveEventTx(ctx, tx, e)` (строки 405-414) перезаписывает поля события полями этой записи. Комментарий 376-377: «Choosing fields by source priority ... is Phase 5's job».

**Паттерн порядка блокировок (обязателен):** сначала `source_events ... FOR UPDATE` (строка 387), потом `events ... FOR UPDATE` (строка 408). `MergeEvents` и `Pass` обязаны лочить в том же порядке (source_events по id, затем events по id) — иначе тупик (Pitfall 10).

**Изменение:** в транзакции (1) `UPDATE source_events SET fields = $2 WHERE id = $1` (JSON нормализованного `event.Event`), (2) `SELECT fields ... WHERE event_id = $1 AND state <> 'gone' AND match_state <> 'out_of_scope'` (тот же предикат, что в `listEventSQL`, строки 448-451, и `EventSources`, listing.go:102-109), (3) при > 1 строке `e = event.Fold(...)`, затем прежний `saveEventTx`. `priority []string` передавать параметром (из `Runner`), не глобально. Регрессионный тест — два прогона двух источников подряд: `Changed = 0` и `updated_at` не сдвигается (образец `TestSaveEventFor` в store_test.go:626-666: сравнение `before.UpdatedAt`/`after.UpdatedAt`).

---

### `internal/store/dedup.go` (store, CRUD + транзакция)

**Analog:** `internal/store/events.go` (`SaveEventFor`, `SetOutOfScope` 458-486) и `internal/store/alerts.go` (простые CRUD 25-67).

**Транзакция с блокировкой и ошибками** (events.go:458-486):
```go
tx, err := s.db.Begin(ctx)
if err != nil { return fmt.Errorf("set out of scope: begin: %w", err) }
defer func() { _ = tx.Rollback(ctx) }()
var eventID *int64
err = tx.QueryRow(ctx, "SELECT event_id FROM source_events WHERE id = $1 FOR UPDATE", sourceEventID).Scan(&eventID)
if errors.Is(err, pgx.ErrNoRows) { return fmt.Errorf("set out of scope %d: %w", sourceEventID, ErrNotFound) }
...
if err := tx.Commit(ctx); err != nil { return fmt.Errorf("set out of scope: commit: %w", err) }
```
`s.db.Begin` внутри `InTx` открывает savepoint — методы вложены безопасно (store.go:27-32).

**Чтение списков** — `pgx.CollectRows` (alerts.go:31-38):
```go
rows, err := s.db.Query(ctx, q, ...)
list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (AlertState, error) {
	var a AlertState; err := row.Scan(...); return a, err
})
```
**Upsert** — `ON CONFLICT ... DO UPDATE` (alerts.go:49-54; events.go:60-78). Кандидат: `ON CONFLICT (source_event_id, event_id) DO UPDATE SET score, reasons WHERE state = 'pending'`.

**Новые ошибки** — `var ErrNotMergeable = errors.New(...)` по образцу `ErrNotFound` (events.go:20); `store.ErrNotAcceptable` и `ErrRunInProgress` используются ботом через `errors.Is` (admin.go:130-133) — то же для `ErrNotMergeable`.

**`MergeEvents`** — шаги RESEARCH Q4 (342-350): оба события корневые; `UPDATE source_events SET event_id = winner, match_state = 'auto'|'manual'` (строки **не** удаляются); `UPDATE events SET merged_into_id = winner, is_listed = false WHERE id = loser`; `UPDATE events SET merged_into_id = winner WHERE merged_into_id = loser` (схлопывание); пересбор полей через `event.Fold` + `replaceDistances` (events.go:186-199). `is_listed` победителя — `listEventSQL` (448-451).

**Правило `merged_into_id`:** `SaveEvent` его не трогает (events.go:282-284, 363-364) — не менять.

---

### `internal/store/dedup_test.go` (test, PG)

**Analog:** `internal/store/store_test.go`

`testStore(t)` (строки 55-64; TRUNCATE уже содержит `dedup_candidates`), `TestMain` c защитой `_test` (18-53), хелперы `newRun`, `scalar[T]`, `exec`, `isNotFound`, `sampleEvent()` (212-…, 530-603). Формат проверок: `if got := scalar[int64](t, "SELECT ...", id); got != want { t.Fatalf(...) }`. Тесты из другого пакета — `storetest.Open(t)` (storetest.go:21-58); при появлении новых таблиц добавить их в оба `TRUNCATE` (storetest.go:52-53 и store_test.go:59-60) — в Phase 5 новых таблиц нет, колонки только.

---

### `migrations/00004_dedup.sql` (migration)

**Analog:** `migrations/00003_source_alerts.sql`

Формат: `-- +goose Up` / `-- +goose Down`, русские комментарии к колонкам, `Down` симметричен. Готовое тело — RESEARCH Q4 (строки 299-311): `ALTER TABLE source_events ADD COLUMN fields JSONB;`, `ALTER TABLE dedup_candidates ADD COLUMN decided_at TIMESTAMPTZ, decided_by BIGINT, notified_at TIMESTAMPTZ;`, `CREATE INDEX idx_dedup_candidates_state ON dedup_candidates (state, id);`. `//go:embed *.sql` (migrations/embed.go:14-15) подхватит файл сам. Все колонки допускают NULL (аддитивно, откат `agg rollback` безопасен).

---

### `internal/bot/dupes.go` (controller, request-response)

**Analog:** `internal/bot/admin.go`

**Команда `/dupes` — не-админ получает ответ неизвестной команды** (admin.go:31-51):
```go
func (b *Bot) onRuns(ctx context.Context, api *tgbot.Bot, u *models.Update) {
	m := u.Message
	if m == nil || m.From == nil { return }
	if !b.isAdmin(m.From.ID) { b.onText(ctx, api, u); return }
	runs, err := b.Store.RecentRuns(ctx, runsPerSource)
	sc := screen{plain: true}
	if err != nil { b.logger().Error("recent runs", "err", err); sc.text = errNotice } else { sc = b.runsScreen(runs) }
	if err := b.send(ctx, m.Chat.ID, sc); err != nil { b.logger().Warn("send runs", "err", err) }
}
```
**Callback-обработчик: `isAdmin` на каждом нажатии, подтверждение второй кнопкой, `say`, типы ошибок** (admin.go:84-139):
```go
func (b *Bot) onRun(ctx context.Context, q *models.CallbackQuery, c callback) string {
	if !b.isAdmin(q.From.ID) { b.logger().Warn("admin callback from non-admin"); return "" }
	say := func(text string) string {
		if err := b.show(ctx, q, screen{text: text, plain: true}); err != nil { b.logger().Warn("show run", "err", err) }
		return ""
	}
	...
	if c.Kind == cbRunAsk {
		var yn row
		yn.add("Да, принять", callback{Kind: cbRunYes, ID: c.ID})
		yn.add("Отмена", callback{Kind: cbRunNo, ID: c.ID})
		sc := screen{plain: true, kb: keyboard(yn), text: fmt.Sprintf("Принять прогон ...", c.ID, code)}
		...
		return ""
	}
	res, err := b.Runner.Accept(ctx, src, c.ID)
	switch {
	case err == nil: return say(...)
	case errors.Is(err, store.ErrNotAcceptable): return say("Уже нельзя принять: ...")
	case errors.Is(err, store.ErrRunInProgress): return say("Сейчас идёт прогон ...")
	}
	b.logger().Error("accept run", ...); return say("Не удалось принять прогон, подробности в логе")
}
```
Для `/dupes`: `d:m` → экран подтверждения (как `cbRunAsk`), `d:y` → `Store.MergeEvents(...)` (+ `ErrNotMergeable` → «Кандидат уже обработан» + следующий), `d:x` → отклонить без подтверждения, `d:s` → следующий `pending` с `id > cid`, `d:o` → первый `pending`. Экран — `screen{plain: true}` (названия бывают с `<`, `&`), `cut(t.String(), maxAdminText)` (admin.go:78, константа 23), `linkPreviewOff` уже в `send`/`show` (tg.go:48-50, 146, 189). Kм дистанций — `kmList` (list.go:241-250); имя источника — `sourceName(code)` (list.go:158-163); ссылки — `validURL` (list.go:224-230).

**`b.show` правит сообщение, к которому привязан callback, при 400 — шлёт новым** (tg.go:183-207) — «следующий кандидат в том же сообщении» (D-09) достигается через `show`.

---

### `internal/bot/callback.go` (правка, кодек)

**Analog:** он сам.

Грамматика в комментарии (16-25) — добавить строки `d:o`, `d:<m|y|n|x|s>:<cid>`. Виды `cbKind` (29-41) — добавить `cbDupeOpen, cbDupeAsk, cbDupeYes, cbDupeNo, cbDupeRej, cbDupeSkip`. Разбор — копия `case "r":` (173-183):
```go
case "r":
	if len(p) != 3 { return callback{}, errCallback }
	kinds := map[string]cbKind{"a": cbRunAsk, "y": cbRunYes, "n": cbRunNo}
	k, ok := kinds[p[1]]
	if !ok { return callback{}, errCallback }
	id, err := num(p[2], 1, math.MaxInt64)
	return callback{Kind: k, ID: id}, err
```
`d:o` — без id (`len(p) == 2`). Кодирование — `case cbRunAsk: s = fmt.Sprintf("r:a:%d", c.ID)` (202-207). Длина ≤ 23 < `maxCallbackBytes = 64` (25); `encode` сам проверяет длину и обратный разбор (211-216). Тест — `callback_test.go` (таблица на границе 64 байт).

---

### `internal/bot/bot.go` (правка, router)

**Analog:** он сам.

- Регистрация (bot.go:54-59): добавить `tgbot.WithMessageTextHandler("dupes", tgbot.MatchTypeCommandStartOnly, b.onDupes)` и `tgbot.WithCallbackQueryDataHandler("d:", tgbot.MatchTypePrefix, b.cb(b.onDupe))`.
- `cbPrefix` (bot.go:212-217): добавить `data[0] == 'd'` в условие — иначе `d:` логируется как `?`.
- IN-04: `privateOnlyMW` (163-172) — для `u.CallbackQuery != nil` в не-личном чате ответить пустым `answerCallbackQuery` вместо молчаливого `return` (использовать `b.answer`; middleware — функция без `b`, потребуется метод `b.privateOnlyMW` или замыкание).
- `Bot` struct (22-33) — `loc *time.Location` уже есть; использовать в IN-08 вместо `FixedZone`.

---

### `internal/bot/list.go` (правка: `sourceNames`, `onCard`)

- `sourceNames` (list.go:155-156): `map[string]string{"marathonec": "Марафонец", "probeg": "ПроБЕГ"}`.
- `onCard` (119-145): ветка `case !e.IsListed || e.MergedIntoID != nil: sc = goneScreen(...)` (131-132) → для `MergedIntoID != nil` (D-16) пройти по цепочке до корня (цепочки схлопнуты, один шаг: `GetEvent(ctx, *e.MergedIntoID)`), затем собрать `cardScreen` корня; остальное (`!IsListed` без merge) — как сейчас. Тест — `list_test.go`.

---

### Долг Phase 4 (LO-03, LO-05, IN-01…IN-04, IN-08)

- **LO-03** `internal/bot/tg.go` `withRetry` (87-103): сейчас `time.NewTimer(min(wait, maxRetryWait))` ждёт до 30 с; для `retry_after > maxRetryWait` возвращать `err` сразу (без ожидания). Тест — `tg_test.go`.
- **LO-05 / IN-01 / IN-02** `internal/bot/filters.go`: `loadFilters` (58-78) — после проверки `validDate`, сбрасывать `f.Date`, если это `mYYYYMM` и `monthEnd(first) < today` (`monthEnd` — 168-171; `today` — параметр/через `b.today()` bot.go:144-147); `emptyScreen(f userFilters, v int)` (293) — убрать неиспользуемый `v` и обновить вызовы; поправить комментарий `toggleBucket`. Тест — `filters_test.go`.
- **IN-03** `alert.go` `Tick` (116-163): `return err` внутри цикла по `runs` → `errs = append(errs, err); continue`, в конце `errors.Join(errs...)`.
- **IN-08** `admin.go:18` и `alert.go:21` `time.FixedZone("MSK", 3*3600)` → `Loc *time.Location` (поле `alert.Loop`, `b.loc` в боте); `message(run, ...)` (alert.go:74) берёт `run.FinishedAt.In(msk)` — передать `loc` параметром. `main.go:194` — `alert.Loop{..., Loc: loc}`.

---

### `cmd/aggregator/main.go` (правка, wiring)

**Analog:** он сам.

- `newSources` (146-148): `return []ingest.Source{marathonec.New(f), probeg.New(f)}` + импорт `"aggregator/internal/ingest/sources/probeg"`. Тест `TestNewSources` в `main_test.go` дописать.
- `newRunner` (153-164): добавить `PostApply: func(ctx context.Context) error { return dedup.Pass(ctx, s, dcfg) }` (конфиг `dedup.LoadConfig()` читать рядом с `ingest.LoadConfig`; ошибка конфига — до БД, как в `runBot`, строки 171-178).
- `runBot` (170-201): рядом с `loop := &alert.Loop{...}; wg.Go(func() { loop.Run(ctx) })` (194-196) добавить `digest := &dedup.DigestLoop{...Send: b.SendDupesDigest...}; wg.Go(func() { digest.Run(ctx) })`. `wg.Wait()` до закрытия store (199) уже есть.

---

### `docker-compose.yml`, `.env.example` (config)

- `.env.example:37-40` — блок закомментированных необязательных переменных (`# OBSERVE_*`, `# QUALITY_*`): добавить строку `# DEDUP_HIGH=0.7  DEDUP_LOW=0.4  DEDUP_AUTO_MERGE=false ...` тем же форматом.
- `docker-compose.yml:9` комментарий: «Необязательные OBSERVE_* и QUALITY_* ... добавляются одинаково в bot и worker» → `DEDUP_*` передавать **в оба** сервиса (`bot`, строки ~77-83; `worker`, ~92-97) как `${DEDUP_HIGH:-}` (пусто → значение по умолчанию в коде; `LoadConfig` уже пропускает пустые — config.go:73-75). Изменение compose ⇒ на VPS повторный шаг root-консоли (deploy/README.md) — решение владельца, не выполнять.

---

## Shared Patterns

### Админ-доступ (`isAdmin`) на каждом обработчике
**Source:** `internal/bot/admin.go:36-38, 85-88`; `internal/bot/bot.go:74`
**Apply to:** `/dupes`, все `d:*`, `SendDupesDigest`
```go
if !b.isAdmin(q.From.ID) { b.logger().Warn("admin callback from non-admin"); return "" }
// команда: if !b.isAdmin(m.From.ID) { b.onText(ctx, api, u); return }
// исходящее: if !b.isAdmin(chatID) { return errNotAdmin }   // admin.go:144-147
```

### Админ-сообщения: plain, усечение, без превью
**Source:** `internal/bot/admin.go:20-24, 148`; `internal/bot/tg.go:15-19, 48-50`
**Apply to:** `dupes.go`, сводка
`screen{text: cut(text, maxAdminText), plain: true}` (без `parse_mode`), `maxAdminText = 4000`; названия событий из недоверенных источников — всегда через `cut(...)`.

### Ошибки: оборачивание с контекстом, sentinel + `errors.Is`
**Source:** `internal/store/events.go:18-20, 386-393`; `internal/bot/admin.go:101-107, 130-133`
**Apply to:** `store/dedup.go`, `dedup/pass.go`, `bot/dupes.go`
Формат `fmt.Errorf("<операция>: <шаг>: %w", err)`; `errors.Is(err, pgx.ErrNoRows)` → `ErrNotFound`; бот переводит sentinel в текст администратору, остальное — `b.logger().Error(...)` + `errNotice` (filters.go:416).

### SQL только в `internal/store`, параметры `$N`
**Source:** `internal/store/store.go:1-4`
**Apply to:** `dedup.Pass`, `digest.go`, `bot/dupes.go` — не содержат SQL; только методы `*store.Store`.

### Порядок блокировок: `source_events` → `events`, по возрастанию id
**Source:** `internal/store/events.go:387, 408`
**Apply to:** `MergeEvents`, любая запись, трогающая оба

### Логирование без секретов и без данных пользователя
**Source:** `internal/alert/alert.go:171`; `internal/bot/bot.go:78-133`; `internal/ingest/runner.go:414-439`
**Apply to:** `digest.go`, `pass.go`, `dupes.go`
В лог — код источника/тип ошибки/счётчики, не тексты сообщений, не токены, не тела страниц, не `OWNER_CONTACT`.

### Конфигурация из окружения
**Source:** `internal/ingest/config.go:34-95, 98-136`
**Apply to:** `internal/dedup/config.go`
Ошибка называет переменную, но не значение; пустая переменная = значение по умолчанию.

### Тесты: golden + `-update`, PG через `storetest.Open`, фейковые Telegram/часы
**Source:** `marathonec/parse_test.go:20, 91-103`; `internal/store/storetest/storetest.go:21-58`; `internal/bot/bot_test.go:32-35, 44-120, 151-178`; `internal/alert/alert_test.go:98-113`
**Apply to:** все новые `_test.go`. Команда: `go test -p 1 -count=1 ./...` с `TEST_DATABASE_URL` (тесты store делают TRUNCATE).

---

## No Analog Found

| Файл / аспект | Роль | Поток данных | Причина |
|---|---|---|---|
| `event.Fold` (приоритет по полю, пары-связки, допуск дистанций) | utility | transform | Нет слияния полей нескольких записей; опора — RESEARCH Q4 (322) и только частичные хелперы `normalizeDistances`/`canonicalFieldsEqual` |
| `pg_advisory_xact_lock` для `Pass` | store | lock/batch | В репозитории нет advisory-lock; есть только `FOR UPDATE` и `ClaimRun` (`store/runs.go`, `tx.ClaimRun` в runner.go:275). Использовать RESEARCH Pitfall 10: `SELECT pg_advisory_xact_lock($1)` в начале `InTx` |
| Похожесть названий (Jaccard по триграммам) | utility | transform | Нет fuzzy-сравнения; взять готовый код из RESEARCH Q3 (226-249) |
| `internal/dedup/testdata/calibration.json` + `TestCalibration` | test | golden | Аналог — только механика golden (`-update`, SHA в README); формат пар и порогов — RESEARCH Q6 (386-397); метки проверяет владелец (checkpoint:human-verify) |
| Групповые строки ПроБЕГа (раскрытие серии, год по `FetchedAt`) | parser | transform | У Марафонца серий и дат без года нет; опора — RESEARCH Q1 и фикстура `samples/probeg-year_ahead-2026-09-30.html` |

## Metadata

**Analog search scope:** `internal/{ingest,ingest/sources/marathonec,normalize,store,store/storetest,bot,alert,event}`, `cmd/aggregator`, `migrations`, `docker-compose.yml`, `.env.example`
**Files scanned:** ~45 (прочитаны целиком или по блокам: marathonec.go/parse.go/тесты/README, source.go, prepare.go, runner.go, quality.go, config.go, report.go, alert.go, admin.go, bot.go, callback.go, tg.go, list.go, filters.go (фрагменты), place.go, regions.go, events.go, listing.go, alerts.go, store.go, storetest.go, store_test.go (фрагменты), event.go, main.go, миграции 00001/00003, embed.go)
**Pattern extraction date:** 2026-09-30
**Обязательные оговорки для планировщика:**
1. `ingest.Stats` без json-тегов попадает в `marathonec/testdata/prepared.golden.json` — новое поле `Collapsed` требует `omitempty`-тега или обновления golden.
2. Новые поля `SourceEvent` — только `omitempty` и в конец структуры (иначе `content_hash` Марафонца меняется и все 133 записи считаются изменёнными).
3. `SaveEventFor` сейчас затирает общее событие полями текущей записи — без `fields` + `Fold` слияние бессмысленно (RESEARCH Q4).
4. `Accept` не вызывает `Evaluate`; `Policy.OutOfScopeMax` подключать только в `Run`.
