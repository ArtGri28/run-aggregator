# Phase 9: Onboarding & User Profile - Pattern Map

**Mapped:** 2026-10-04
**Files analyzed:** 22 (новых 10, изменяемых 12)
**Analogs found:** 22 / 22 (без аналога — только отдельные элементы, см. «No Analog Found»)
**Источник списка файлов:** `09-CONTEXT.md` (`<decisions>`, `<plans>`, `<screens>`); RESEARCH.md нет.
**Проверка отслеживаемости:** все названные аналоги — в `git ls-files` (tracked); зеркал и gitignored-копий нет. Номера строк даны на рабочем дереве (на `5d81e5a` файлы-аналоги не изменены; незакоммиченные `cmd/aggregator`, `internal/bot/list.go`, `internal/ingest/**` не затрагиваются и не используются).

## File Classification

| Новый/изменяемый файл | План | Роль | Поток данных | Ближайший аналог | Качество |
|---|---|---|---|---|---|
| `migrations/00006_user_profiles.sql` (new) | 09-02 | migration | CRUD | `migrations/00005_merge_log.sql` + `00001_init_schema.sql` (`users`) | exact |
| `internal/store/profiles.go` (new) | 09-02 | service/repository | CRUD, версия под блокировкой строки | `internal/store/users.go` | exact |
| `internal/store/profiles_test.go` (new) | 09-02 | test | CRUD + гонка | `internal/store/users_test.go` | exact |
| `internal/store/storetest/storetest.go` (mod) | 09-02 | test helper | batch (TRUNCATE) | он же, строка 52-53 | exact |
| `internal/store/store_test.go` (mod) | 09-02 | test helper | batch (TRUNCATE) | он же, строка 57-58 | exact |
| `internal/bot/callback.go` (mod) | 09-03 | utility (кодек) | request-response | он же: ветки `s:` / `g:` | exact |
| `internal/bot/callback_test.go` (mod) | 09-03 | test | request-response | он же: `TestCallbackRoundTrip`, `TestCallbackReject`, `TestCallbackLength` | exact |
| `internal/bot/bot.go` (mod) | 09-03 | config/route | event-driven (регистрация) | он же, строки 54-64, 236-245 | exact |
| `internal/bot/profile.go` (new) | 09-03/04 | controller + screens | request-response, CRUD | `internal/bot/settings.go` + `internal/bot/filters.go` | role-match (best) |
| `internal/bot/country.go` (mod) | 09-04 | utility | transform | он же | exact |
| `internal/bot/filters.go` (mod: `onStart`, `mainMenu`, `onText`) | 09-03/04 | controller | request-response | он же | exact |
| `internal/bot/profile_test.go` (new) | 09-03/04 | test | request-response | `internal/bot/settings_test.go`, `scenario_test.go`, `stale_test.go` | exact |
| `internal/bot/stale_test.go` (mod: `dbState`) | 09-04/05 | test helper | — | он же, строки 33-39 | exact |
| `internal/bot/contract_test.go` (mod) | 09-05 | test | batch (чтение файла) | он же | exact |
| `internal/bot/testdata/screens/*.txt` (new + правка `s01_start`, `start`, `s01_start_again`, `s01_menu`, `s12_menu`, `s0*_menu`, `s02_menu`, …) | 09-03/05 | test fixture | — | `s01_start.txt`, `bucket_menu.txt`, `s14_stale.txt` | exact |
| `.planning/phases/09-.../09-UX-CONTRACT.md` (new) | 09-01 | docs | — | `08-UX-CONTRACT.md` | exact |
| `.planning/phases/09-.../09-ACCEPTANCE.md` (new) | 09-05/06 | docs | — | `08-ACCEPTANCE.md` | exact |
| `.planning/REQUIREMENTS.md` (mod) | 09-01 | docs | — | существующие строки BOT-08 и т.п. | exact |
| `MVP.md` §8.5, §12.2 (mod) | 09-01 | docs | — | те же разделы | exact |

## Pattern Assignments

### `migrations/00006_user_profiles.sql` (migration, CRUD)

**Analog:** `migrations/00005_merge_log.sql` (формат goose, русские комментарии, `ON DELETE`, индекс) и `migrations/00001_init_schema.sql` строки 89-95, 104 (`users`, откуда берётся FK).

**Формат goose Up/Down** (`00005_merge_log.sql` 1-8, 28-31): первая строка `-- +goose Up`, затем русский комментарий-обоснование над `CREATE TABLE`, в конце `-- +goose Down` и `DROP TABLE`. Колонки выровнены пробелами; `CHECK (... IN (...))` для перечислений; `TIMESTAMPTZ NOT NULL DEFAULT now()`.
```sql
-- +goose Up
-- Журнал слияний (D-14 Phase 7, вариант c): ...
CREATE TABLE event_merges (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    winner_id   BIGINT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    match_state TEXT NOT NULL CHECK (match_state IN ('auto', 'manual')),
    merged_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    undone_at   TIMESTAMPTZ,                         -- когда отменено; NULL — действует
    ...
);
-- +goose Down
DROP TABLE event_merge_chain;
```

**Ограничения значений из `00001`** (строки 29-30): `CHECK (country_code ~ '^[A-Z]{2}$')`, `CHECK (city_key IS NULL OR (char_length(city_key) <= 64 AND city_key ~ '^[a-z0-9-]+$'))` — копировать для `residence_country_code` (плюс допустить `ZZ`, он совпадает с `^[A-Z]{2}$`) и `residence_city_key`; `char_length(residence_city) <= 64` — по тому же образцу `description CHECK (... char_length(description) <= 1000)`.

**Ссылка на `users`** (`00001` 89-95): `tg_id BIGINT PRIMARY KEY`. Новая таблица: `tg_id BIGINT PRIMARY KEY REFERENCES users(tg_id) ON DELETE CASCADE` (D-07; в `00005` везде `RESTRICT`, здесь `CASCADE` — осознанное отличие по D-07).

**Применить к колонкам D-07:**
- `status TEXT NOT NULL CHECK (status IN ('not_started','in_progress','completed','skipped'))`;
- `gender TEXT CHECK (gender IN ('m','f'))` (NULL допускается: `IN` от NULL даёт NULL, CHECK проходит);
- `age_group SMALLINT CHECK (age_group BETWEEN 0 AND 5)`;
- `sports TEXT[] NOT NULL DEFAULT '{}' CHECK (sports <@ ARRAY['run','trail','ocr','bike','swim','ski','tri','row'])`;
- `profile_v INTEGER NOT NULL DEFAULT 1` (как `filters_v`, `00001` строка 92);
- `awaiting_city_until TIMESTAMPTZ`, `offered_at/started_at/completed_at/updated_at` — `TIMESTAMPTZ`, `offered_at` и `updated_at` `NOT NULL DEFAULT now()`.
- Down: одна строка `DROP TABLE user_profiles;`.

Миграция подхватывается автоматически: `migrations/embed.go` — `//go:embed *.sql`; `aggregator migrate` и `storetest.Open` вызывают `s.Migrate`. Таблицу `users` не менять — иначе упадёт `TestUsersColumns` (`internal/store/users_test.go` 91-109).

---

### `internal/store/profiles.go` (repository, CRUD с версией под блокировкой)

**Analog:** `internal/store/users.go` (99 строк). Копировать структуру целиком: ошибка-сторож, `Peek…` без записи, `Update…At` в `InTx` под `FOR UPDATE`.

**Ошибка-сторож** (users.go 11-13):
```go
// ErrStaleFilters — версия фильтров не совпала с ожидаемой либо пользователя
// нет: кнопка, которая просила изменение, устарела.
var ErrStaleFilters = errors.New("filters: устаревшая версия")
```
→ `ErrStaleProfile` (отдельная ошибка: версии раздельные, D-08). В `bot` её обработка зеркальна `errors.Is(err, store.ErrStaleFilters) → return staleNotice`.

**Чтение без записи** (users.go 32-44) — шаблон для `PeekProfile` (нет строки → `ok=false`, нулевой профиль и версия 0; ни `INSERT`, ни `last_seen_at`):
```go
err = s.db.QueryRow(ctx, `SELECT filters, filters_v FROM users WHERE tg_id = $1`, tgID).Scan(&raw, &v)
if errors.Is(err, pgx.ErrNoRows) {
	return []byte(`{}`), 0, false, nil
}
if err != nil {
	return nil, 0, false, fmt.Errorf("peek filters: %w", err)
}
```

**Версия под блокировкой строки в одной транзакции** (users.go 63-99) — ядро для `UpdateProfileAt`:
```go
err = s.InTx(ctx, func(tx *Store) error {
	err := tx.db.QueryRow(ctx, `SELECT filters, filters_v FROM users WHERE tg_id = $1 FOR UPDATE`, tgID).Scan(&cur, &v)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && v != wantV) {
		return ErrStaleFilters
	}
	if err != nil { return fmt.Errorf("update filters: lock: %w", err) }
	next, err := fn(cur)
	if err != nil { return err }
	const q = `UPDATE users SET filters = $2,
	    filters_v = filters_v + CASE WHEN filters IS DISTINCT FROM $2::jsonb THEN 1 ELSE 0 END, ...
	    RETURNING filters, filters_v`
	...
})
```
Для профиля: `profile_v = profile_v + CASE WHEN (gender, age_group, residence_*, sports, status) IS DISTINCT FROM (...) THEN 1 ELSE 0 END` — версия растёт только при реальном изменении (PRF-05: «растёт `profile_v`»); `updated_at = now()` — тоже только при изменении. `InTx` — `internal/store/store.go` 53-66 (транзакционный `*Store` с `tx.db`).

**`/start`-транзакция (D-06)** — создание строки профиля до отправки экрана. Образец «upsert + заблокированная строка в одном запросе» — `touchUser` (users.go 18-21):
```go
const touchUser = `
	INSERT INTO users (tg_id) VALUES ($1)
	ON CONFLICT (tg_id) DO UPDATE SET last_seen_at = now()
	RETURNING filters, filters_v`
```
Скопировать приём `INSERT ... ON CONFLICT (tg_id) DO NOTHING RETURNING` для `user_profiles` в `InTx` вместе с `touchUser` (пользователь создаётся первым, FK). Возвращать `offered bool` (строка создана именно этим вызовом ⇒ показать экран O-1; строка уже была ⇒ главное меню).

**Что не копировать:** `UpdateFilters` без версии (`wantV < 0`) — профилю нужен только вариант с версией плюс `/start`/старт анкеты; текстовый ввод города — отдельный метод `SaveResidenceCityText` (тоже `FOR UPDATE`, проверка `awaiting_city_until > now()` в запросе, не в Go — одним `UPDATE ... WHERE awaiting_city_until > $2 RETURNING`; время — параметр `b.now()` ради тестов, как `from time.Time` в facets.go).

**Массив спорта:** pgx передаёт `[]string` как `TEXT[]` без обёрток; в `store` явных `TEXT[]` пока нет (аналога нет — см. «No Analog Found»), проверить сканирование `[]string` в тесте.

---

### `internal/store/profiles_test.go` (test, CRUD + гонка)

**Analog:** `internal/store/users_test.go` (внутренний пакет `store`, `testStore(t)` из `store_test.go` 52-63).

**Шаблон «нет пользователя → ничего не создаётся; чужая версия → ErrStale + строка не изменилась»** (users_test.go 126-157): `userRow(t, id)` читает строку целиком через `to_jsonb` для сравнения до/после; скопировать как `profileRow` (`SELECT COALESCE((SELECT to_jsonb(p)::text FROM user_profiles p WHERE tg_id = $1), '')`; хелпер `scalar[string]` уже есть в пакете).
```go
if _, _, err := s.UpdateFiltersAt(ctx, 42, 7, withKey("type", "road", 0)); !errors.Is(err, ErrStaleFilters) { ... }
if row := userRow(t, 42); row != before { t.Fatalf("устаревшее изменение записало в users:\n%s\n%s", before, row) }
```

**Гонка двух изменений с одной версией** (users_test.go 160-187): две горутины, `time.Sleep(50ms)` внутри `fn` как окно гонки, ждём ровно одну `ErrStale` и версию +1. Для профиля нужен тот же сценарий (на `UpdateProfileAt`; чтобы окно создать — хук/`pg_sleep` в запросе либо паузу в `fn`, как `withKey(..., pause)`).

**Тест колонок** — по образцу `TestUsersColumns` (users_test.go 91-109): список колонок `user_profiles` равен D-07 (BOT-08 в новой редакции D-12). Существующий `TestUsersColumns` не менять — он доказывает, что `users` не тронута (D-07/D-08).

**CASCADE:** тест «удаление `users` удаляет профиль» (D-07) — прямой `DELETE FROM users` через `testStorePkg.pool`.

---

### `internal/store/storetest/storetest.go` и `internal/store/store_test.go` (TRUNCATE-списки)

**Analog:** они сами. Два независимых места, список в обоих одинаковый:
```go
const q = `TRUNCATE TABLE sources, ingest_runs, source_events, events,
	event_distances, dedup_candidates, users, heartbeats, source_alerts RESTART IDENTITY CASCADE`
```
(storetest.go 52-53; store_test.go 57-58). `CASCADE` по FK очистит `user_profiles` и сам, но список правят явно: добавить `user_profiles` в обоих (перед `users`), чтобы список оставался самодокументирующим и не зависел от каскада. Править оба — иначе тесты `bot` и `store` расходятся по поведению.

---

### `internal/bot/callback.go` (+ `callback_test.go`) — кодек `u:`

**Analog:** сам файл; ближайшие ветки — `case "s":` (206-224) и `case "g":` (249-261): несколько подвидов с разной длиной, версия последним полем, `kinds`-таблица.

**Комментарий-грамматика** (14-38): добавить строки в тот же блок, тем же стилем:
```
//	u:o                              мой профиль (без версии)
//	u:<b|n|z>:v<P>                   начать / пропустить анкету / готово
//	u:a:<g|a|k|c|s>:<w|e>:v<P>       показать вопрос
//	u:s:<поле>:<значение>:<w|e>:v<P> сохранить ответ
```
И дописать «Версия — всегда последнее поле»: у `u:` версия — `profile_v`, не `filters_v`.

**cbKind** (43-69): добавить после `cbSearchBack` — `cbProfile, cbProfStart, cbProfSkip, cbProfAsk, cbProfSave, cbProfDone` (порядок `iota` не важен, значений нигде не хранится).

**Разбор:** новая ветка `case "u":` — строго, по таблице D-14. Паттерн проверки версии — `version(p[len(p)-1])` (166-172, 210); числа — `num()` (150-164); «значение вне грамматики — `errCallback`». Валидаторы значений в стиле `validCountry`/`validType` (102-126): `validGender` (`m|f`), `validAge` (`0..5` через `num(...,0,5)` — учтите: `num` отвергает ведущие нули, `"0"` допускается), `validSport` (по списку D-04, тип `var profileSports = []struct{Code,Label string}` по образцу `eventTypes`/`buckets`), страна — `countryRE` + `ZZ`, ключ города — `cityKeyRE` с лимитом 49 байт (`const maxProfileCityKeyBytes = 49` по образцу `maxCityKeyBytes` 90). Отдельное `case "u"` не пересекается с `f/s/p/e/g/r/d` — `parseCallback` switch по `p[0]`.

**Структура `callback`** (74-81): поля `Value`, `V` уже есть; режим `w|e` и поле удобно положить в `Value` составной строкой или добавить два поля (`Field`, `Mode string`) — не ломая существующие литералы `callback{Kind: ...}` (все литералы именованные).

**encode** (292-337): добавить `case`ы с `fmt.Sprintf("u:s:%s:%s:%s:v%d", ...)`; общий хвост (330-336) уже проверяет ≤ 64 байт и round-trip через `parseCallback` — ничего менять не надо, и это гарантирует «иное значение кодек отвергает» (PRF-04).

**Тесты** (`callback_test.go`): дописать в списки `TestCallbackRoundTrip` (11-36) строки `u:o`, `u:b:v1`, `u:s:s:run:e:v7`, `u:s:c:<ключ>:w:v2147483647`; `TestCallbackReject` (71-94) — `u:s:g:x:w:v1`, `u:s:a:6:e:v1`, `u:s:s:other:e:v1`, ключ города 50 байт, `u:z`, без версии; `TestCallbackLength` (103-150) — отдельный блок на границы длины `u:s:c:<ключ>:<w|e>:v<P>`: 6 + ключ + 4 (`:w:v`) + цифры версии = 49 + 6 + 4 + 5 = 64 при `P` до 99999; при `P` = MaxInt32 (10 цифр) ключ 49 байт уже не помещается, `encode` вернёт ошибку и `row.add` пропустит кнопку — решить в контракте 09 (понизить предел ключа или принять, что город тогда вводится текстом). Образец теста длины — строки 105-114.

**Версия и `stamp`:** `stamp` (filters.go 818-840) проставляет версию только кнопкам `f:`/`s:` — `strings.HasPrefix(..., "f:") || ...("s:")`. Для `u:` нужна собственная `stampProfile(sc, pv)` (копия цикла, префикс `u:`, исключая `u:o`), либо экраны профиля строятся сразу с `V: pv`. Лучше второе: экраны профиля получают `pv` аргументом и кладут `V: pv` в `callback{...}` (как `summaryScreen(..., v, ...)` кладёт `V: v` в «Показать N», filters.go 555) — тогда `stamp` для `u:` не нужен, а `u:o` остаётся без версии.

---

### `internal/bot/bot.go` (регистрация, `cbPrefix`)

**Analog:** сам файл.

**Регистрация** (54-64): добавить рядом с `"s:"`:
```go
tgbot.WithCallbackQueryDataHandler("u:", tgbot.MatchTypePrefix, b.cb(b.onProfile)),
```
и команду по образцу `/runs`/`/dupes` (56-57):
```go
tgbot.WithMessageTextHandler("profile", tgbot.MatchTypeCommandStartOnly, b.onProfileCmd),
```
Кнопка главного меню `u:o` ведёт в тот же `b.onProfile` (`cbProfile`).

**`cbPrefix`** (237-245): список букв `f s p e g r d` — добавить `data[0] == 'u'`, иначе `callback stale prefix=?` и тесты логов (`TestS14…` проверяют `prefix=f`) не отличат профиль. Тест — `TestCbPrefix` (`callback_test.go` 95-101).

**Обёртка `cb`** (210-234): без изменений — `onProfile` обязан возвращать `string` (текст уведомления), `staleNotice` для устаревшей кнопки, `errNotice` для ошибок. Лог только `prefix`, без значений (PRF-09).

---

### `internal/bot/profile.go` (controller + screens, request-response/CRUD)

**Analog:** `internal/bot/settings.go` (95 строк: константа `from…`, экран-функция, `onSettings` с ветвлением «пишущая/читающая кнопка») + `internal/bot/filters.go` (меню с «✓», `cbState`, `updateAt`, `onStart`, `onText`).

**Imports** (settings.go 3-11):
```go
import (
	"context"
	"errors"
	"fmt"

	"github.com/go-telegram/bot/models"

	"aggregator/internal/store"
)
```
Добавить `html` (экранирование города, как `html.EscapeString(cityName)` в `activeLine`, filters.go 503), `slices`, `strings`.

**Режим экрана `w`/`e` — как `fromSettings`/`fromSearch`** (settings.go 13-18):
```go
const (
	fromSettings = "o"
	fromSearch   = "f"
)
```
→ `modeWizard = "w"`, `modeEdit = "e"`; передаётся в `Value`/поле кнопки, «Назад» и «Готово» выбираются по режиму тем же приёмом, что `resetConfirmScreen(from)` (40-49): `cancel := callback{Kind: cbSettings}; if from == fromSearch { cancel = back }`.

**Экран-функция без обращений к БД** (settings.go 24-36): чистая функция `profileScreen(p profile, cityLabel string, pv int) screen` (P-1), `questionScreen(field, mode, p, pv)` (O-2…O-5, P-2). Текст — HTML, значения экранируются; кнопки — `btn(text, callback{...})`, ряд из двух — `var r row; r.add(...); r.add(...)` (summaryScreen, filters.go 545-549). Подпись «⬅️ Назад» — константа `backLabel` (540), **последняя строка клавиатуры, одна на экран** (принцип 1 контракта Phase 8).

**Мультивыбор с «✓» — `bucketMenu`/`typeMenu`** (filters.go 629-636, 659-669): 
```go
rows = append(rows, btn(check(slices.Contains(f.Dist, i), bk.emoji+" "+bk.label), callback{Kind: cbBucket, Value: strconv.Itoa(i)}))
...
r.add(check(f.Type == string(t), typeEmoji(t)+" "+typeLabel(string(t))), callback{...})
if len(r) == 2 { rows, r = append(rows, r), nil }
```
→ виды спорта: 8 кнопок по две в ряд (приём из `typeMenu` 658-669), `check(slices.Contains(p.Sports, code), emoji+" "+label)`, кнопка `u:s:s:<code>:<mode>:v<P>` переключает отметку (toggle, как `toggleBucket`, filters.go 253-261), «✅ Готово». `check` — filters.go 596-601 («✓ » префикс).

**Страна** — как `countryMenu` (filters.go 737-775): ряд кнопок с подписью `countryLabel(code)` (country.go 33-41), «✓» у текущей. Но набор — **фиксированный** (RU, BY, KZ, ZZ), не из каталога. Для `ZZ` `countryFlag("ZZ")` вернёт псевдо-флаг (`countryRE` пропускает) — нужен явный случай «🌍 Другая страна» перед вызовом `countryLabel` (по образцу ветки `allCountries` в country.go 34-36).

**Город кнопками из каталога** — как `cityMenu` (filters.go 639-650):
```go
cities, err := b.Store.TopCities(ctx, f.query(b.today()), 10)
for _, c := range cities {
	rows = append(rows, btn(fmt.Sprintf("%s (%d)", c.Name, c.N), callback{Kind: cbCity, Value: c.Key}))
}
```
Профильная версия: `b.Store.TopCities(ctx, event.Filter{From: b.today(), Country: code}, 10)` (поле страны в `event.Filter` — проверить имя в `internal/event`; `countryMenu` строит `event.Filter{From: b.today()}`, filters.go 738), **без числа событий в подписи** (город проживания — не фильтр), ключ в кнопке ≤ 49 байт, длиннее — только текстом (D-14). `f.query` профилю **не использовать** — код профиля не читает `users.filters` (D-08).

**Обработчик — шаблон `onSettings`** (settings.go 53-95): две ветви — пишущая (`updateAt` → `errors.Is(err, ErrStale…) → return staleNotice`) и читающая (`cbState` → экран по текущему состоянию), затем один `b.show(ctx, q, screen)`, ошибки показа — `Warn`, возврат `notice`:
```go
if c.Kind == cbResetYes {
	f, v, err = b.updateAt(ctx, q.From.ID, c, userFilters.reset)
	if errors.Is(err, store.ErrStaleFilters) {
		return staleNotice
	}
	notice = resetNotice
} else {
	raw, v, _, err = b.cbState(ctx, q.From.ID, c)
	...
}
if err != nil {
	b.logger().Error("settings", "err", err)
	return errNotice
}
...
if err := b.show(ctx, q, stamp(sc, v)); err != nil {
	b.logger().Warn("show settings", "err", err)
}
return notice
```
Профильные аналоги: `b.profileUpdateAt(ctx, id, c, fn)` ≈ `updateAt` (filters.go 790-799: `if c.Legacy {stale}` — у `u:` Legacy нет; ветку убрать) и `b.profileState(ctx, id, c)` ≈ `cbState` (806-813: `Peek` → при `!ok || c.V != v` stale без записи). **Различие по D-15:** устаревшая *навигационная* кнопка показывает экран по текущему состоянию без записи; если строки профиля нет — экран предложения/главное меню без создания строки (как `PeekFilters` не создаёт `users`).

**Каждая кнопка `u:` снимает `awaiting_city_until`** (D-10): делать в той же транзакции, что и запись (ветви `u:s`, `u:a`, `u:b`, `u:n`, `u:z`) — но для устаревшей кнопки не писать ничего (PRF-07), т.е. снимать отметку только под актуальной версией.

**Профиль не трогает `users`** (D-08, PRF-06): использовать только `Store.…Profile…`; не вызывать `b.Store.Filters` из `profile.go` (в отличие от `onStart`, который вызывает `Filters` и обязан остаться единственным местом касания обеих таблиц — вынести в отдельные вызовы).

**Журналирование (PRF-09):** по образцу `onText` — `b.logger().Info("text", "city", true)` (filters.go 992): в лог только булев признак/вид, никогда текст города, пол, возраст, спорт. Для города текстом — как в комментарии `onText` «Текст в лог не пишется (BOT-08)».

---

### `internal/bot/filters.go` — правки `onStart`, `mainMenu`, `onText`

**Analog:** сам файл.

**`onStart`** (843-855) — сейчас: `b.Store.Filters` → `send(stamp(mainMenu(), v))`. Новое: вместо отдельного `Filters` — транзакция «users + профиль» (`Store.StartUser`: `touchUser` + `INSERT ... ON CONFLICT DO NOTHING` профиля, возвращает `v`, `offered`), затем `offered ? offerScreen(pv) : stamp(mainMenu(), v)`. Тот же `b.send` и тот же формат лога ошибки (`b.logger().Error("start", "err", err)`; `Warn("send start", ...)`). Снятие `awaiting_city_until` на `/start` (D-10) — в той же транзакции. «Сорвавшаяся отправка повторного предложения не вызывает» (D-06) обеспечено тем, что строка уже создана — не откатывать при ошибке `send`.

**`mainMenu`** (858-862) — добавить третью строку `btn("👤 Мой профиль", callback{Kind: cbProfile})`. `mainMenu()` без аргументов, и `stamp` не трогает `u:o` (нет версии, и `stamp` смотрит только `f:`/`s:`). Это меняет эталоны `start`, `s01_start`, `s01_start_again`, `s01_menu`, `s02_menu`, `s12_back_to_menu`, `s12_menu` и любые другие со строкой `[⚙️ Мои настройки] → s:o:v` (найти `grep -l "Мои настройки\] → s:o" internal/bot/testdata/screens`). Менять эталоны **вручную только после принятия diff владельцем** (D-13 / audit п.3).

**`onText`** (950-1013) — вставка новой ветки **до** поиска. Место: после нормализации и проверки пустоты/длины (965-969), до `b.Store.Filters` (972). Существующий приём «ранний возврат с `say(...)` и `return`» — те же `say`/`fail`:
```go
text := strings.Join(strings.Fields(m.Text), " ")
if text == "" || utf8.RuneCountInString(text) > maxCityInput || strings.HasPrefix(text, "/") {
	say(screen{text: "Нажмите /start, чтобы найти забег", plain: true})
	return
}
```
Новая ветка: `if ok, err := b.Store.SaveResidenceCity(ctx, m.From.ID, text, key, b.now()); ok { say(residenceCityAck(...)); return }` — срабатывает только при действующей `awaiting_city_until`; иначе поток идёт дальше в поиск без изменений (PRF-08: после истечения текст идёт в поиск). `key := normalize.CityKey(text)` уже вычисляется ниже (981) — для профиля вызывать отдельно и сверять с каталогом выбранной страны через `b.Store.CityName(ctx, country, key, today)` (filters.go 986, возвращает `n > 0` если город есть), иначе `residence_city_key = NULL` и храним текст (D-09). Ответ — `🏠 Город проживания: {город}` с `[Дальше]`/`[Изменить]` (O-4), текст через `html.EscapeString`.
Для `/…` команд ветка не вызывается (проверка `HasPrefix(text, "/")` стоит раньше).

**Не трогать:** остальной `onText`, `searchResults` и `b.update` — фильтры не меняются (PRF-08).

---

### `internal/bot/country.go` (названия стран)

**Analog:** сам файл, строка 10:
```go
var countryNames = map[string]string{"RU": "Россия", "BY": "Беларусь", "PL": "Польша"}
```
Добавить `"KZ": "Казахстан"`. `ZZ` в `countryNames` не класть (псевдо-код не-страны; иначе он попадёт в `countryMenu` через каталог — каталог строится из `events.country_code`, и `ZZ` туда не попадает, но подпись «Другая страна» должна быть отдельным условием в профиле). Тесты — `country_test.go` (проверить, нет ли теста на точный состав `countryNames`: `grep -n countryNames internal/bot/country_test.go`). Влияние на существующие экраны: `countryName("KZ")` раньше возвращал `KZ`, теперь «Казахстан» — это попадёт в эталоны, если фикстуры содержат KZ-события (в `countryFixture` — только RU/BY).

Код ISO ↔ написание: справочник `normalize/country.go` (строки 5-40, `countryNames` — написание → ISO; `CountryCode` в `place.go` 137) — для профиля **не нужен** (страна выбирается кнопкой по D-04, ввод страны текстом отложен).

---

### `internal/bot/profile_test.go` (test, request-response)

**Analog:** `internal/bot/settings_test.go` (структура сценария «нажал → снимок → проверка БД»), `scenario_test.go` (хелперы `startSnap`, шаги по контракту), `stale_test.go` (`dbState`, `onlyToast`, `edited`, `raw`), `audit_test.go` (`pressSnap`, `textSnap`).

**Фикстура:** `countryFixture(t)` (country_test.go 33-44) — три страны/города, фейковый Bot API, `newTestBot`. Для профиля подходит как есть (города `moscow`, `minsk`, `lyuban`).

**Структура сценария** (settings_test.go 10-28):
```go
func TestS12Settings(t *testing.T) {
	s, api, b := countryFixture(t)
	startSnap(t, b, api, "s12_menu")
	def := pressSnap(t, b, api, "s:o", "s12_default")
	...
	// Экран только показывает: фильтры и версия не меняются.
	f1, v1 := filtersV(t, b)
	pressSnap(t, b, api, ..., "s12_to_search")
	if f2, v2 := filtersV(t, b); v2 != v1 || ... { t.Errorf("настройки изменили фильтры: ...") }
}
```
Профильные сценарии (имена `TestS18…`, `TestS19…` — нумерация продолжается с S-18, регэксп `contractScenarioRE` в contract_test.go 15-17 требует формат `### S-NN <название> — тест \`TestSNN<Имя>\`` в `09-UX-CONTRACT.md` и эталоны `sNN_*.txt`). **Важно:** `TestContractCoverage` сейчас сверяет тесты `TestSNN*` со **всем** множеством сценариев одного контракта (`reflect.DeepEqual(got, want)`, строка 52) — при расширении на второй файл `want` = объединение S-01…S-17 из Phase 8 и новых из Phase 9, а `len(want) < 17` проверку поправить.

**Хелперы, которые нужно дописать под `u:` (нельзя использовать как есть):**
- `fresh` (filters_test.go 162-174), `versioned`/`noV` (170-184) знают только `f:`/`s:` и версию `filters_v`. Для `u:` версия — `profile_v`; написать `profressh(t, b, data)` (подставляет `:v<profile_v>` для `u:` кроме `u:o`) или формировать кнопки через `callback{...}.encode()` с `V` из `b.Store.PeekProfile`. Не расширять `fresh` для `u:` молча: `press` вызывает `fresh`, и `Filters(ctx, 42)` создаёт строку `users` — для тестов независимости (PRF-06) это допустимо только до замера `dbState`.
- `dbState` (stale_test.go 33-39) собирает `users` и `events`:
```go
return dbScalar(t, `SELECT jsonb_build_object(
	'users', (SELECT jsonb_agg(to_jsonb(u) ORDER BY u.tg_id) FROM users u),
	'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM events e))::text`)
```
Добавить ключ `'user_profiles'` — но **это поменяет сравнение во всех существующих тестах, где `dbState` берётся до/после** (безвредно: профиль в них не пишется, значения `null` совпадают). Для теста независимости (PRF-06) лучше две узкие функции `usersState`/`profilesState` (`to_jsonb(u)` / `to_jsonb(p)` по таблице), а `dbState` расширить третьим ключом. Не писать `last_seen_at`-зависимых сравнений без учёта: `/start` и актуальные кнопки обновляют `users.last_seen_at`; «действия с профилем не меняют строку `users`» (PRF-06) — значит кнопки `u:` не должны вызывать `touchUser`/`Filters`, и тест сравнивает `users` целиком.

**Тесты на устаревшие кнопки** (PRF-07): копировать структуру `TestS14StaleFilter` (stale_test.go 80-121): `raw(b, api, 42, data)` без подстановки версии → `onlyToast(...)` → `matchSnapshot(t, "sNN_stale", renderCall(t, a))` → `dbState` до/после равен → лог `callback stale prefix=u` (`captureLogs(t)`, bot_test.go 349). И `TestS15…` для навигации (stale_test.go 123+): `edited(t, data, calls)` возвращает снимок правки + ответа.

**Логи без ПД** (PRF-09): по образцу `TestNoPersonalDataInLogs` (bot_test.go 388-418) — отправить текст-город «СекретныйГород» при действующей отметке, нажать кнопки профиля, и проверить, что в `captureLogs` нет `"СекретныйГород"`, пола, возраста, кодов спорта.

**Время:** `b.Now` подменяется (`newTestBot`, bot_test.go 150-159, `fixtureNow`) — истечение окна 10 минут проверять сдвигом `b.Now`, не `Sleep`.

**Снимки:** `renderCall` (snapshot_test.go 23-47) формирует `method / parse_mode / --- текст --- / [подпись] → data`; `matchSnapshot` сверяет с `testdata/screens/<имя>.txt`; эталон создаётся **вручную** (автообновления нет) — после утверждения 09-UX-CONTRACT.md. Проверка длины callback — `checkCallbackLengths(t, call)` (вызывается внутри `press`/`edited`).

---

### `internal/bot/contract_test.go` (расширение на второй файл)

**Analog:** сам файл (87 строк). Константа и чтение одного файла:
```go
const uxContract = "../../.planning/phases/08-telegram-bot-ux/08-UX-CONTRACT.md"
...
doc, err := os.ReadFile(uxContract)
...
for _, m := range contractScenarioRE.FindAllStringSubmatch(string(doc), -1) { ... want[m[1]] = m[2] }
if len(want) < 17 { t.Fatalf(...) }
```
Сделать `var uxContracts = []string{".../08-UX-CONTRACT.md", ".../09-onboarding-user-profile/09-UX-CONTRACT.md"}`, прочитать оба и слить в один `want`; дублей номеров между файлами быть не должно (следить: `if prev, dup := want[...]` → `t.Errorf`), порог `< 17` → число сценариев Phase 8 + 1 (иначе потеря второго файла не обнаружится). Контракт Phase 8 не править (D-16). Путь жёсткий и относительный от пакета — сохранить стиль (`../../.planning/...`). Нумерация новых сценариев — S-18… (двузначная, `[0-9]{2}`; `scenarioSnapshotRE` — `^s([0-9]{2})_.+\.txt$`). Служебные эталоны без префикса `sNN_` (как `bucket_menu`, `start`) контрактом не проверяются — для общих экранов профиля можно называть без `sNN_`.

---

### `internal/bot/testdata/screens/*.txt` (эталоны)

**Analog формата:** `s01_start.txt`
```
method: sendMessage
parse_mode: HTML
---
🏁 <b>Где старт?</b>
...
---
[🔎 Найти забег] → f:s:v1
[⚙️ Мои настройки] → s:o:v1
```
и `s14_stale.txt` для уведомлений:
```
method: answerCallbackQuery
text: Кнопка устарела — нажмите /start
show_alert: 
```
(строка `show_alert: ` с хвостовым пробелом). Правки существующих эталонов меню — только после одобрения diff владельцем. Файлы создаются вручную как правка файла вместе с контрактом (комментарий `screensDir`, snapshot_test.go 14-16). Для `editMessageText` + `answerCallbackQuery` вместе (`edited`, stale_test.go 56-72) эталон — склейка двух снимков.

---

### `.planning/phases/09-onboarding-user-profile/09-UX-CONTRACT.md` и `09-ACCEPTANCE.md`

**Analog:** `.planning/phases/08-telegram-bot-ux/08-UX-CONTRACT.md` и `08-ACCEPTANCE.md`.

**Структура контракта** (08-UX-CONTRACT.md): шапка (`**Статус:**`, `**Основа:**`, `**Обозначения:**` — `<P>` вместо `<N>`: `user_profiles.profile_v`); `## 1. Принципы` (один возврат, экран меняется на месте, данные меняет только явное действие, одно уведомление на нажатие) — сослаться, не дублировать; `## 2. Дерево навигации` (ASCII-дерево; дополнить веткой «Мой профиль → вопрос → …»); `## 3. Формат callback` — таблица «Вид | Формат | Меняет данные | Версия» (D-14 уже в таком виде); `## 4. Экраны` — блоки `### E-NN <название>` с телом экрана и кнопками в формате `[подпись] → data`; `## 5. Устаревшие кнопки` — таблица «Случай | Что видит | Запись в БД» (скопировать три строки + дописать «нет строки профиля»); `## 8. Сценарии` — **строго формат** `### S-NN <Название> — тест \`TestSNN<Имя>\`` с нумерованными шагами (его парсит `contractScenarioRE`; точка с запятой и тире в разделителе — длинное «—»). Сверить регэксп: `(?m)^### S-([0-9]{2}) .*тест \`(TestS[0-9]{2}[A-Za-z0-9]*)\``.

**Чек-лист** (`08-ACCEPTANCE.md`): таблицы «Сценарий | Тест | Эталоны | Что проверить глазами | Отметка»; блок «Критерий «пройдено»» (полный прогон `go test -p 1 -count=1 -v ./...` с `TEST_DATABASE_URL` — 0 FAIL, 0 SKIP, `gofmt -l .` пусто, `go vet ./...`, `TestContractCoverage`); production-чек-лист в виде «# | Шаг в Telegram | Ожидаемый результат | Сценарий | Отметка»; пустая колонка «Отметка» до приёмки владельцем.

---

## Shared Patterns

### Версия под блокировкой строки (stale-guard)
**Source:** `internal/store/users.go` 54-99; `internal/bot/filters.go` 790-813; `internal/bot/bot.go` 18, 216-226
**Apply to:** `profiles.go`, `profile.go` (все кнопки `u:` с записью).
Проверка версии и запись — в одной транзакции под `FOR UPDATE`; чужая версия/нет строки → `ErrStale…` → `return staleNotice` ("Кнопка устарела — нажмите /start") и **ни одной записи**; навигация читает без записи (`Peek…`). `b.cb` уже даёт «ровно один ответ» и различает `staleNotice`/`errNotice` в логах.

### Ответ на callback и показ экрана
**Source:** `internal/bot/tg.go` 200-226 (`show`), 161 (`send`), `internal/bot/bot.go` 210-234 (`cb`)
**Apply to:** все обработчики `u:`.
`show` правит сообщение, при ошибке 400 шлёт новое; `send` — для `/start`, `/profile`, ответа на текст. Ошибки показа — `Warn`, не меняют уведомление. «Анкета сохранена»-уведомление — возвращаемая строка из обработчика (как `resetNotice` в settings.go 20).

### Логи без персональных данных (BOT-08, PRF-09)
**Source:** `internal/bot/filters.go` 949, 992; `internal/bot/bot.go` 236-245; `internal/bot/bot_test.go` 388-418
**Apply to:** `profile.go`, `onText`-ветка города, `onStart`.
В лог — только вид действия/булевы и `err`; текст города, значения полей, `tg_id` — не логировать. `cbPrefix` для `u`.

### Экранирование пользовательского текста
**Source:** `internal/bot/filters.go` 503 (`html.EscapeString(cityName)`)
**Apply to:** вывод `residence_city` в P-1 и O-4 (parse_mode HTML). Город-текст не попадает ни в `callback_data`, ни в логи (D-09).

### Строгая грамматика callback и 64 байта
**Source:** `internal/bot/callback.go` 176-288, 292-337; `internal/bot/tg.go` 25-31 (`row.add` молча пропускает кнопку, не влезающую в 64 байта)
**Apply to:** все новые кнопки: строить только через `btn(...)`/`row.add(...)`; значения валидируются в `parseCallback` и в `encode` (round-trip).

### Единая подпись возврата и один «Назад» на экран
**Source:** `internal/bot/filters.go` 536-540 (`back`, `backLabel`); контракт Phase 8, раздел 1, принцип 1
**Apply to:** все экраны профиля (последняя строка, одна кнопка «⬅️ Назад»); исключение — O-4 после ввода города (`[Дальше]`/`[Изменить]` — по контракту 09).

### Изоляция профиля и фильтров (D-08)
**Source:** `internal/store/users.go` (единственный код `users`)
**Apply to:** `profiles.go` не читает/не пишет `users`, кроме создания строки в `/start`-транзакции (`touchUser`, FK); `profile.go` не вызывает `userFilters`/`b.update`/`b.cbState`. Тест независимости (PRF-06) — снимки `usersState`/`profilesState` до/после в обе стороны.

### Тестовая инфраструктура
**Source:** `internal/store/storetest/storetest.go` (`Open`: проверка `_test`-базы, миграции, TRUNCATE); `internal/bot/country_test.go` 33-44 (`countryFixture`); `internal/bot/bot_test.go` 150-181 (`newTestBot`, `startUpdate`, `cbUpdate`)
**Apply to:** `profile_test.go`. Тесты без `TEST_DATABASE_URL` пропускаются (`t.Skip`); критерий приёмки — 0 SKIP на полном прогоне.

## No Analog Found

| Элемент | Роль | Поток | Причина / что использовать |
|---|---|---|---|
| Колонка `TEXT[]` с `CHECK (sports <@ ARRAY[...])` и сканирование `[]string` | migration/store | CRUD | В `migrations/` массивных колонок нет, в `store` нет запросов с `TEXT[]`-колонками (только `[]string` аргумент в `dedup.go` 233). Опираться на документацию pgx v5 (нативная поддержка `[]string` ↔ `text[]`) и покрыть тестом в `profiles_test.go`. |
| Диалог с состоянием «жду текст» (`awaiting_city_until`) | controller | event-driven | Все обработчики без состояния (`onText` — «без состояния «жду ввод»», filters.go 944). Новый приём: окно в БД + проверка в `UPDATE ... WHERE awaiting_city_until > $now`. Образцы времени — `b.now()` (bot.go 144-149) и `fixtureNow` в тестах. |
| Переход статусов анкеты (конечный автомат `<states>`) | store/service | CRUD | Аналога конечного автомата в проекте нет; ближайшее — `match_state` в `00005_merge_log.sql` (`CHECK ... IN`). Переходы кодируются в одном запросе `UPDATE ... SET status = CASE ...` внутри `UpdateProfileAt`, правила — из `09-CONTEXT.md` `<states>`. |
| Фиксированный список стран анкеты (RU, BY, KZ, ZZ) | screen | — | `countryMenu` строит список из каталога событий; здесь список константный — собрать как `[]struct{Code, Label string}` по образцу `buckets` (filters.go 65-73). |

## Metadata

**Analog search scope:** `migrations/`, `internal/store/` (+`storetest`), `internal/bot/` (код, тесты, `testdata/screens`), `internal/normalize/`, `.planning/phases/08-telegram-bot-ux/`.
**Files scanned:** ~30 (прочитаны полностью: `users.go`, `callback.go`, `bot.go`, `settings.go`, `country.go`, `contract_test.go`, `snapshot_test.go`, `storetest.go`; фрагментами: `filters.go`, `tg.go`, `stale_test.go`, `scenario_test.go`, `settings_test.go`, `users_test.go`, `store_test.go`, `callback_test.go`, `bot_test.go`, `facets.go`, `place.go`, `08-UX-CONTRACT.md`, `08-ACCEPTANCE.md`).
**Pattern extraction date:** 2026-10-04

**Замечания для планировщика (риски, найденные при чтении кода):**
1. `stamp` (filters.go 826) и тестовые `versioned`/`noV`/`fresh` (filters_test.go) знают только `f:`/`s:`; версию `profile_v` для `u:` ставить при построении кнопок (`V: pv`), а не через `stamp`.
2. Предел 64 байта: `u:s:c:<ключ>:<w|e>:v<P>` = 6 + ключ + 3 + `v<P>`; при ключе 49 байт кнопка помещается только при `P` ≤ 99999 (5 цифр), при 6+ цифрах `row.add` её молча пропустит — в тесте длины проверять реальные границы, как `TestCallbackLength` для `f:c:`.
3. `countryFlag("ZZ")` вернёт «флаг» из букв Z — для «Другая страна» нужна явная подпись «🌍 Другая страна».
4. `TestContractCoverage` требует равенства множеств тестов и сценариев — после расширения на второй файл любой `TestSNN…` без записи в `09-UX-CONTRACT.md` (и наоборот) роняет тест.
5. `TRUNCATE`-список продублирован в `storetest.go` и `store_test.go` — править оба.
6. Эталоны главного меню меняются массово (D-13); `grep -l "s:o:v" internal/bot/testdata/screens` даёт полный список файлов для diff владельцу.
