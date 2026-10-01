---
phase: 05-multiple-sources-deduplication
plan: 05
subsystem: store, dedup, ingest
status: complete
tags: [dedup, merge, fold, migration, advisory-lock, candidates]
requires: [05-01, 05-02, 05-03]
provides:
  - "migrations/00004_dedup.sql: source_events.fields JSONB; dedup_candidates.decided_at/decided_by/notified_at; idx_dedup_candidates_state (state, id)"
  - "(*Store).SaveEventFor(ctx, sourceEventID, e, fold event.FoldConfig) — fields + Fold связанных записей через saveEventTx"
  - "store: dedupLockKey, ErrNotMergeable, LockDedup, DedupSource, DedupEvent, DedupEvents, RejectedPairs, UpsertCandidate, PrunePendingCandidates, MergeEvents, Candidate, NextCandidate, CandidateByID, ConfirmCandidate, RejectCandidate, CandidateCounts, LastDigestAt, MarkCandidatesNotified"
  - "ingest.Runner.Fold event.FoldConfig; write начинается с LockDedup"
  - "dedup.Pass(ctx, s *store.Store, c Config) error"
affects: [05-06, 05-07, 05-08]
tech-stack:
  added: []
  patterns:
    - "pg_advisory_xact_lock(dedupLockKey) во всех путях записи каталога"
    - "собственная JSON-структура store (sourceFields) вместо json.Marshal(event.Event)"
key-files:
  created:
    - migrations/00004_dedup.sql
    - internal/store/dedup.go
    - internal/store/dedup_test.go
    - internal/dedup/pass.go
    - internal/dedup/pass_test.go
  modified:
    - internal/store/events.go
    - internal/store/store_test.go
    - internal/ingest/runner.go
key-decisions:
  - "Checkpoint (владелец): schema-a — fields = неэкспортируемая sourceFields с явными snake_case-ключами (D-19)"
  - "Checkpoint (владелец): link-strict — уже реализовано в 05-02 (D-20); decide.go/decide_test.go не менялись, TestDecideLink содержит случай «NameSim в [High−бонус, High) с общей ссылкой → Candidate»"
  - "UniquePartner в Pass считается по NameSim >= High (не по Score с бонусом ссылки), согласованно с D-20"
  - "MergeEvents не переводит out_of_scope-записи проигравшего в auto/manual (match_state сохраняется)"
  - "ConfirmCandidate сначала помечает строку confirmed, затем сливает: иначе MergeEvents удалил бы её как pending с участием проигравшего"
  - "CandidateByID: pending, у которого стороны уже в одном корне, — ErrNotFound (как пропуск в NextCandidate)"
metrics:
  duration: "~45 мин"
  completed: 2026-10-01
actuals:
  tokens: 15000
  tasks: 4
  commits: 0
plan_head_before: 86bdc42
---

# Phase 5 Plan 05: хранение и проход дедупликации Summary

Миграция 00004 (`source_events.fields`, поля решения и сводки у `dedup_candidates`, индекс очереди), `SaveEventFor` с записью `fields` и `event.Fold` всех записей события через единственную точку `saveEventTx`, слой store для слияния и очереди `/dupes` под общей advisory-блокировкой и идемпотентный `dedup.Pass`: отклонённые пары по корням, правило одного источника, `MaxPerEvent`, чистка устаревших pending, автослияние только при включённом конфиге.

## Решения checkpoint:decision

- **schema-a** (D-19): `sourceFields` с ключами title, title_norm, start_date (YYYY-MM-DD), end_date, start_time (HH:MM), timezone, country_code, region, city, city_key, address, event_type, description, registration_url, distances [{m, label}]; необязательные — omitempty; status/id/служебных полей нет; неизвестные ключи при чтении игнорируются (`json.Unmarshal` в типизированную структуру).
- **link-strict** (D-20): уже сделано в 05-02 — `Decide` требует `NameSim >= High` без бонуса. Дополнительно `Pass` считает `UniquePartner` по `NameSim >= High`.

## Задачи

| # | Задача | Результат | Коммит |
|---|--------|-----------|--------|
| 0 | checkpoint:decision | schema-a, link-strict | — |
| 1 | Tracer: две записи → Run/Accept → PostApply = Pass → pending; AutoMerge → MergeEvents | `TRACER OK`: `TRACER dedup pending=1 merged=auto loser_listed=false root_sources=2 distances=1000,2000,10000,20000` | не коммитилось (коммитит владелец) |
| 2 | store: миграция туда-обратно, MergeEvents, Fold в SaveEventFor, очередь и сводка | TestMigration00004, TestSaveEventForFold, TestMergeEvents, TestMergeEventsConcurrent, TestCandidateQueue, TestCandidateDigest — PASS | не коммитилось |
| 3 | Pass: отклонённые, один источник, MaxPerEvent, stale, инвариант Fold, параллельные прогоны | TestPassRejected, TestPassSameSource, TestPassMaxPerEvent, TestPassStale, TestFoldInvariant (`INVARIANT sources=3 changed=0 updated_at=same`), TestParallelRuns, TestPassFailureKeepsRun — PASS | не коммитилось |

Tracer feedback gate: verify задачи 1 повторён целиком после реализации — прошёл, расширение продолжено.

## Проверка

- Статика плана: `go mod verify`, `go vet ./...`, `gofmt -l` пусто, прямые зависимости прежние, SQL вне `internal/store` нет, `DELETE FROM events` нет, `pg_trgm` нет, `env -u TEST_DATABASE_URL go test ./...` ok → `STATIC OK`.
- `replaceDistances(` — 3 строки (определение + 2 вызова в `saveEventTx`).
- БД: `TEST_DATABASE_URL=…/agg_w1b_test go test -p 1 -count=1 -timeout 5m -v ./...` — все 10 пакетов ok, 341 PASS, `--- SKIP` 0, все тесты из verify задач 1–3 найдены; строки TRACER и INVARIANT на месте.
- Миграция: Up (через `Migrate`), `DownTo(3)` убирает 4 колонки и индекс, `Up` возвращает; FK `source_events.event_id` и `events.merged_into_id` — `confdeltype = 'r'`.
- Мутационная проверка тестов (изменения откатаны): без Fold в SaveEventFor падает TestFoldInvariant; без проверки отклонённых — TestPassRejected; без MaxPerEvent и без UniquePartner — TestPassMaxPerEvent; без чистки pending — TestPassStale.
- Acceptance-grep всех трёх задач выполнены (см. plan): idx ×2, REFERENCES 0, сигнатуры SaveEventFor/Pass/MergeEvents, `LockDedup(` в `write`, `RootEventID(` в store/dedup.go ×2 и в pass.go ×2, `merged_into_id` в pass.go нет.

## Deviations from Plan

1. **[Окружение] Тестовая БД `agg_w1b_test`** вместо `aggregator_test` (указание оркестратора); `docker compose up` не выполнялся — контейнер уже работал.
2. **[Окружение] Коммитов нет** — правило владельца (git write запрещён); изменения в рабочем дереве, `commits: 0`.
3. **[Rule 1] MergeEvents сохраняет `match_state = out_of_scope`** у переносимых записей проигравшего: иначе слияние вернуло бы в выдачу запись вне каталога (`CASE WHEN match_state = 'out_of_scope' …`).
4. **[Rule 1] ConfirmCandidate: решение до слияния.** План: MergeEvents → строка confirmed. Если сторона кандидата не корень (event_id указывает на слитое событие), шаг чистки в MergeEvents удалил бы эту pending-строку до её подтверждения. Строка помечается confirmed первой, затем MergeEvents.
5. **[Rule 1] PrunePendingCandidates с пустым keep**: nil-срез ушёл бы в pgx как NULL, `id = ANY(NULL)` не удалил бы ничего; nil заменяется пустым срезом.
6. **UniquePartner по NameSim** (решение владельца) вместо «по Score» из текста плана.
7. **link-strict не потребовал правок** `decide.go`/`decide_test.go` — сделано в 05-02.
8. **TestMergeEventsConcurrent и TestMigration00004** — проверка 40P01 через `errors.As(*pgconn.PgError)`; возврат на последнюю версию в TestMigration00004 — `defer` (не `t.Cleanup`: соединение goose закрывается раньше).

## Заметки для 05-06/05-08

- `Runner.Fold` нужно заполнить в `newRunner` (`cfg.Fold()`), `PostApply` — `dedup.Pass(ctx, s, cfg)`; иначе Fold идёт по порядку записей без допуска.
- `LockDedup` в `write` сериализует запись всех источников (комментарий `ponytail:` в runner.go).
- После выката записи Марафонца с `fields = NULL` не участвуют в `Pass` до первого прогона нового образа (событие не `Complete`).
- `Pass` удаляет все pending, которые не вернулись в этом проходе, включая пары прошедших событий и событий с неполными `fields`; при повторном появлении пара создаётся заново с `notified_at` NULL.
- Пакет `dedup` теперь импортирует `store` (Pass); doc-комментарий пакета в `config.go` («Чистые функции, без БД») устарел — файл вне списка плана, не менялся.

## Known Stubs

Нет.

## Threat Flags

Нет — новых поверхностей сверх `<threat_model>` (T-05-21…27 покрыты тестами и гейтами выше).

## Self-Check: PASSED

- FOUND: migrations/00004_dedup.sql, internal/store/dedup.go, internal/store/dedup_test.go, internal/dedup/pass.go, internal/dedup/pass_test.go
- FOUND: изменения в internal/store/events.go, internal/store/store_test.go, internal/ingest/runner.go
- Коммиты: не создавались по правилу владельца (проверять нечего)
