---
phase: 16-catalog-quality
plan: 01
status: done-local
requirements-completed: []
---

# 16-01: миграция 00011 и хранилище заявок, предложений и метрик

Статус: код и тесты готовы локально; бот (экраны, кнопки, команды, вызов очистки) — планы 16-02…16-04. Основа — `16-SCHEMA.md`, `16-UX-CONTRACT.md` (Q-1, Q-4, Q-5, Q-7), `16-CONTEXT.md`.

## Изменения

| Файл | Что |
|---|---|
| `migrations/00011_reports.sql` | блок SQL из `16-SCHEMA.md` без изменений; побайтно совпадает с блоком, извлечённым из документа (`cmp` — без различий) |
| `internal/store/reports.go` | новый: заявки (`AddReport`, `DeleteReport`), предложения и отметка «жду ссылку» (`SuggestBegin`, `SuggestCancel`, `SuggestAwaiting`, `SuggestTake`, `SuggestRelease`), очистка (`PruneReports`), сводка `/quality` (`Quality`) |
| `internal/store/support.go` | `SupportBegin` снимает отметку «жду ссылку» |
| `internal/store/profiles.go` | `dropAwaiting` — отметки поддержки и ссылки; её зовут `StartUser` (обе ветки) и `UpdateProfileAt` при постановке отметки города; `SupportCancel` по-прежнему снимает только поддержку |
| `internal/store/storetest/storetest.go`, `internal/store/store_test.go` | `error_reports`, `start_suggestions`, `suggest_awaiting` в `TRUNCATE` |
| `internal/store/favorites_test.go` | `TestMigration00010`: `DownTo(9)` теперь снимает и 00011 — три новые таблицы исключены из сравнения «остальных таблиц» |
| `internal/store/reports_test.go` | новый: `TestMigration00011`, `TestReports`, `TestSuggest`, `TestSuggestExclusive`, `TestPruneReports`, `TestQuality` |

## API хранилища

```go
type ReportOutcome int // ReportAdded, ReportRepeat, ReportLimited
func (s *Store) AddReport(ctx context.Context, tgID, eventID int64, reason string, now, since time.Time, limit int) (root int64, out ReportOutcome, err error)
func (s *Store) DeleteReport(ctx context.Context, tgID, eventID int64, reason string) error

type SuggestOutcome int // SuggestTaken, SuggestNotWaiting, SuggestLimited
func (s *Store) SuggestBegin(ctx context.Context, tgID int64, expiresAt time.Time) error
func (s *Store) SuggestCancel(ctx context.Context, tgID int64) error
func (s *Store) SuggestAwaiting(ctx context.Context, tgID int64) (expiresAt time.Time, ok bool, err error)
func (s *Store) SuggestTake(ctx context.Context, tgID int64, now, since time.Time, limit int) (id int64, out SuggestOutcome, err error)
func (s *Store) SuggestRelease(ctx context.Context, tgID, id int64, expiresAt time.Time) error

func (s *Store) PruneReports(ctx context.Context, before time.Time) (int, error)

type QualityRow struct {
	Source                                  string
	Sport                                   event.Sport
	Events, Unique, Complete                int
	Dated, WithCity, WithDistance, WithLink int
}
type QualitySource struct {
	Code             string
	HasRun           bool
	Status           string
	FinishedAt       time.Time
	Created, Changed int
}
type Quality struct {
	Total       int
	Rows        []QualityRow    // по коду источника, внутри — по порядку event.Sports
	Sources     []QualitySource // все источники по коду
	Reports     map[string]int  // причина → число за окно
	Suggestions int
}
func (s *Store) Quality(ctx context.Context, today, since time.Time) (Quality, error)
```

Как вызывать в следующих планах:
- **16-02 (`q:r`).** Бот проверяет видимость и дату через `RootEventID`/`GetEvent` (R-5, R-6), затем `AddReport(tg, id, reason, now, now-24h, 10)`; `root` — номер для G-1; сбой доставки — `DeleteReport(tg, root, reason)`. Справочник причин (буква → код) — в боте; база проверяет только формат. Очистка — шаг `do("reports", func() (int, error) { return b.Store.PruneReports(ctx, now.Add(-90*24*time.Hour)) })` в `Notifier.chores`.
- **16-03 (`/suggest`).** `SuggestBegin(tg, now+10m)` на `/suggest` и `q:s`; `SuggestCancel` на `q:c`. В `onText` после поддержки: `SuggestAwaiting` → действующая отметка и негодный текст — P-3 без записи; годный — `SuggestTake(tg, now, now-24h, 3)`: `SuggestTaken` → отправка G-2, сбой — `SuggestRelease(tg, id, now+10m)` (P-5); `SuggestLimited` → P-4; `SuggestNotWaiting` → прежние проверки.
- **16-04 (`/quality`).** `Quality(today, now-30d)`; форматирование, доли, «меньше 5» по причинам и возраст прогона — в боте.

## Проверки

- `migrations/00011_reports.sql` и блок `sql` из `16-SCHEMA.md` (извлечён скриптом) — `cmp` без различий.
- `go vet ./...` — без замечаний; `gofmt -l .` — пусто.
- `TEST_DATABASE_URL=… go test -p 1 -count=1 -v ./...` (контейнер `aggregator-test-postgres-1`): **518 PASS, 0 FAIL, 0 SKIP** (было 512; новые — 6 тестов `reports_test.go`).
- Эталоны `internal/bot/testdata/screens/` не изменены; `internal/bot/`, `web/`, `deploy/` не тронуты.

## Отступления от плана

- `AddReport` сам находит корень слияний (`RootEventID`) в своей транзакции и возвращает его: бот может передать и слитое событие, повтор считается по корню.
- Проверка доступности события (скрыто, слито, выключен вид, прошло) в хранилище заявок не делается — её делает бот до вызова, как в карточке (R-5, R-6 записей не дают).
- `UpdateProfileAt`/`StartUser`: вместо отдельного вызова для отметки ссылки — общий `dropAwaiting` (поддержка + ссылка).
- Правка существующего теста `TestMigration00010`: без неё `DownTo(9)` удаляет и таблицы 00011, и сравнение остальных таблиц расходится.
- Вне перечня плана — `internal/store/store_test.go` (собственный `TRUNCATE` тестов пакета `store`).
- Теста одновременного приёма двух ссылок нет: потребление отметки под `FOR UPDATE` — тот же приём, что `SupportTake` (покрыт `TestSupportRace`).

## «Упрощение:» в коде

- `AddReport` (`internal/store/reports.go`): две одновременные заявки разных пар у пользователя на пределе могут обе пройти проверку — перебор на одну; чинить блокировкой по пользователю. Вставка ждёт конца записи прогона того же события (`FOR KEY SHARE`), как у избранного.
