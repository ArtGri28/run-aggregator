---
phase: 05-multiple-sources-deduplication
plan: 06
subsystem: bot, dedup, alert, cmd
status: complete
tags: [dedup, telegram, admin, digest, wiring]
requires: [05-03, 05-04, 05-05]
provides:
  - "internal/bot/dupes.go: (*Bot).onDupes, (*Bot).onDupe, dupeNext, dupeOne, dupeDecide, dupeScreen, dupeSide, signals, (*Bot).SendDupesDigest"
  - "callback d:o, d:<m|y|n|x|s>:<cid>; cbDupeOpen, cbDupeAsk, cbDupeYes, cbDupeNo, cbDupeRej, cbDupeSkip; 'd' в cbPrefix"
  - "(*Bot).Loc() *time.Location — единственный часовой пояс (IN-08)"
  - "dedup.DigestLoop{Store, Admins, Send, Now, Loc, At, Every, Log}, Run, Tick, digestDue"
  - "alert.Loop.Loc, message(…, loc), Tick с errors.Join (IN-03)"
  - "cmd: newRunner(s, cfg, dcfg) с Fold и PostApply = dedup.Pass; dedup.LoadConfig до openStore в bot и worker; DigestLoop в runBot"
affects: [05-07, 05-08]
tech-stack:
  added: []
  patterns:
    - "isAdmin первой строкой каждого обработчика d:* и /dupes"
    - "сводка: время последней отправки из БД (max notified_at) + страховка в памяти процесса"
key-files:
  created:
    - internal/bot/dupes.go
    - internal/bot/dupes_test.go
    - internal/dedup/digest.go
    - internal/dedup/digest_test.go
  modified:
    - internal/bot/callback.go
    - internal/bot/callback_test.go
    - internal/bot/bot.go
    - internal/bot/admin.go
    - internal/alert/alert.go
    - internal/alert/alert_test.go
    - cmd/aggregator/main.go
    - cmd/aggregator/main_test.go
key-decisions:
  - "Экран после [Да, слить] начинается с «Слито: #<больший> → #<меньший>.»; стороны берутся CandidateByID перед ConfirmCandidate (ErrNotFound — сразу «уже обработан»)"
  - "После [Разные] — префикс «Кандидат #<cid>: разные.» над следующим кандидатом"
  - "Пустая очередь (pending = 0) — «Кандидатов нет» без клавиатуры при любом afterID"
  - "DigestLoop помнит время своей последней отправки (поле sent): если Pass удалил всех отмеченных pending, max(notified_at) откатывается и без страховки сводка ушла бы второй раз за день"
metrics:
  duration: "~40 мин"
  completed: 2026-10-01
actuals:
  tokens: 15000
  tasks: 3
  commits: 0
plan_head_before: 86bdc42
---

# Phase 5 Plan 06: /dupes, ежедневная сводка и проводка дедупликации Summary

Очередь `/dupes` для администратора (по одному кандидату, [Слить] → [Да, слить] / [Отмена], [Разные], [Пропустить], [С начала]), `dedup.DigestLoop` со сводкой раз в сутки после `DEDUP_DIGEST_AT` только при новых кандидатах, проводка `PostApply = dedup.Pass` и `Runner.Fold` из `DEDUP_*` для worker и бота, долг Phase 4: IN-03 (`errors.Join` в `alert.Loop.Tick`) и IN-08 (один часовой пояс — `(*Bot).Loc()`).

## Задачи

| # | Задача | Результат | Коммит |
|---|--------|-----------|--------|
| 1 | Tracer: /dupes → первый pending → [Разные] → следующий; не-админ — ответ неизвестной команды | TestTracerDupes PASS: `TRACER dupes nonadmin=unknown_command first=shown reject=rejected next=shown pending=1` | не коммитилось (коммитит владелец) |
| 2 | Подтверждение слияния, пропуск, d:o, «уже обработан», не-админ на всех d:*, SendDupesDigest, IN-08 в боте | TestDupesMerge, TestDupesSkipAndOpen, TestDupesNonAdmin, TestDupesWithoutRunner, TestSendDupesDigest, TestCbPrefix, TestCallbackRoundTrip/Reject (d:*) PASS | не коммитилось |
| 3 | DigestLoop, IN-03/IN-08 в alert, проводка в cmd/aggregator | TestDigestDue, TestDigestTick, TestDigestNoAdmins, TestTickContinuesAfterError, TestMessage, TestNewRunner, TestWorkerDedupConfigFirst PASS | не коммитилось |

Tracer feedback gate: после задачи 1 verify повторён (все тесты пакета bot) — прошёл, расширение продолжено.

## Тексты

Экран кандидата (обычный текст, без `parse_mode`):

```
Кандидат #1 · оценка 0,62 · ожидают 2
Сигналы: название 0,89 · даты пересекаются · город совпал

А · #2
09.10.2026 · Казань
Казанский марафон (ПроБЕГ)
10/21,1 км
ПроБЕГ: https://example.org/probeg/2

Б · #1
09.10.2026 · Казань
Казанский марафон
10/21,1 км
Марафонец: https://example.org/marathonec/1
```
Кнопки: [Слить] `d:m:1` [Разные] `d:x:1` / [Пропустить] `d:s:1`. Прочие сигналы: «даты рядом (±1 день)», «город разный», «ссылка регистрации совпала», «серия». Диапазон дат — `dd.mm.yyyy–dd.mm.yyyy`; без города — «город не указан».

Подтверждение: тот же экран + «Слить? Останется #1, #2 скроется из выдачи, его источники перейдут к оставшемуся. Отменить из бота нельзя.» и [Да, слить] `d:y:1` [Отмена] `d:n:1`.

Итоги: «Слито: #2 → #1.» / «Кандидат #1: разные.» / «Кандидат #1 уже обработан.» над следующим кандидатом; конец очереди — «Кандидатов дальше нет. Ожидают: N» + [С начала] `d:o`; пусто — «Кандидатов нет»; без Runner — уведомление «Слияние недоступно»; ошибка слияния — «Не удалось слить, подробности в логе».

Сводка: «Сводка дублей: ожидают решения N, из них новых M. Открыть очередь — кнопкой ниже или командой /dupes» + [Открыть /dupes] `d:o`.

## Проверка

- Статика плана: `go mod verify`, `go vet ./...`, `gofmt -l` пусто, прямые зависимости прежние, SQL вне `internal/store` нет, `DELETE FROM events` нет, `pg_trgm` нет, `FixedZone` в admin.go/alert.go нет, `merged_into_id`/`MergedIntoID` в dupes.go нет, `env -u TEST_DATABASE_URL go test ./...` ok → `STATIC OK`.
- БД (`agg_w1c_test`): `go test -p 1 -count=1 -timeout 5m -v ./...` — 10 пакетов ok, 355 PASS, `--- SKIP` 0; все тесты из verify задач 1–3 найдены.
- Acceptance-grep: `b.cb(b.onDupe)` и регистрация `dupes` — по одной строке; `isAdmin(` в onDupes, onDupe, SendDupesDigest; `Store.ConfirmCandidate(` — одна строка в ветке `cbDupeYes`; `case "d":` — одна; `errors.Join` в alert.go; `Loc *time.Location` в alert.go и digest.go; `Loc: b.Loc()` ×2; `dedup.LoadConfig()` ×2, обе до `openStore(ctx)`.
- Мутация: без страховки `sent` в DigestLoop падает TestDigestTick («после чистки очереди»: две отправки за день).

## Deviations from Plan

1. **[Окружение] Тестовая БД `agg_w1c_test`** вместо `aggregator_test` (указание оркестратора); `docker compose up` не выполнялся — контейнер уже работал.
2. **[Окружение] Коммитов нет** — правило владельца (git write запрещён); изменения в рабочем дереве, `commits: 0`.
3. **[Rule 2] Страховка сводки в памяти (`DigestLoop.sent`).** План: «состояния в памяти нет». `Pass` удаляет pending, не вернувшиеся в проходе; если удалены все отмеченные, `LastDigestAt` возвращает нулевое время, и новый кандидат в тот же день вызвал бы вторую сводку (нарушение «не чаще раза в сутки»). Источник истины — по-прежнему БД (перезапуск в тот же день не повторяет сводку, пока отмеченные строки живы); `sent` закрывает случай без перезапуска. Остаток: перезапуск + полная чистка отмеченных в тот же день — повторная сводка возможна (без новой колонки/таблицы не закрыть).
4. **Пустая очередь** — «Кандидатов нет» при pending = 0 и после действия (например, «Кандидат #1: разные.\n\nКандидатов нет»), не только при afterID = 0.
5. **[Слить]/[Отмена]/[Да, слить]** читают кандидата через `CandidateByID`; для [Да, слить] — ради текста «Слито: #больший → #меньший»; гонка между чтением и `ConfirmCandidate` влияет только на текст.
6. **TestCbPrefix** — отдельный тест вместо проверки внутри существующего (behavior: `cbPrefix("d:x:1") == "d"`).
7. **TestDupesNonAdmin** нажимает и `d:n:<cid>` (шесть видов вместо пяти).
8. **alert.message** в тестах для failed/без снимка получает `time.UTC` (nil-локация паникует в `time.In`; Loop сам подставляет UTC через `loc()`).

## Known Stubs

Нет.

## Threat Flags

Нет — новых поверхностей сверх `<threat_model>` (T-05-28…35 покрыты тестами и гейтами выше).

## Self-Check: PASSED

- FOUND: internal/bot/dupes.go, internal/bot/dupes_test.go, internal/dedup/digest.go, internal/dedup/digest_test.go
- FOUND: изменения в internal/bot/{callback.go,callback_test.go,bot.go,admin.go}, internal/alert/{alert.go,alert_test.go}, cmd/aggregator/{main.go,main_test.go}
- Коммиты: не создавались по правилу владельца (проверять нечего)
