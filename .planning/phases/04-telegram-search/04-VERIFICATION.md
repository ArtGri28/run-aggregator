---
phase: 04-telegram-search
verified: 2026-09-30T23:00:00Z
re_verification: false
status: passed
score: 11/11 requirements verified (1 с оговоркой); ROADMAP success criteria 5/5
release: aggregator:95f33a07c0c4 (коммит 95f33a0)
covered_files:
  - .dockerignore
  - .env.example
  - .gitignore
  - .planning/phases/04-telegram-search/04-01-PLAN.md
  - .planning/phases/04-telegram-search/04-01-SUMMARY.md
  - .planning/phases/04-telegram-search/04-02-PLAN.md
  - .planning/phases/04-telegram-search/04-02-SUMMARY.md
  - .planning/phases/04-telegram-search/04-03-PLAN.md
  - .planning/phases/04-telegram-search/04-03-SUMMARY.md
  - .planning/phases/04-telegram-search/04-04-PLAN.md
  - .planning/phases/04-telegram-search/04-04-SUMMARY.md
  - .planning/phases/04-telegram-search/04-05-PLAN.md
  - .planning/phases/04-telegram-search/04-05-SUMMARY.md
  - .planning/phases/04-telegram-search/04-06-PLAN.md
  - .planning/phases/04-telegram-search/04-06-SUMMARY.md
  - .planning/phases/04-telegram-search/04-07-PLAN.md
  - .planning/phases/04-telegram-search/04-07-SUMMARY.md
  - .planning/phases/04-telegram-search/04-08-PLAN.md
  - .planning/phases/04-telegram-search/04-08-SUMMARY.md
  - .planning/phases/04-telegram-search/04-CONTEXT.md
  - .planning/phases/04-telegram-search/04-REVIEW.md
  - .planning/REQUIREMENTS.md
  - cmd/aggregator/main_test.go
  - cmd/aggregator/main.go
  - deploy/confirm-test.sh
  - deploy/deploy-apply-test.sh
  - deploy/load-postgres-test.sh
  - deploy/oneliner.sh
  - deploy/README.md
  - deploy/secrets.sh
  - deploy/ship.sh
  - deploy/smoke.sh
  - deploy/vps/agg
  - deploy/vps/agg-install.sh
  - deploy/vps/deploy-apply.sh
  - deploy/vps/deploy-preflight.sh
  - deploy/vps/deploy-verify.sh
  - deploy/vps/env-init.sh
  - deploy/vps/host-apply.sh
  - deploy/vps/host-preflight.sh
  - deploy/vps/host-verify.sh
  - deploy/vps/load-postgres.sh
  - deploy/vps/rollback.sh
  - docker-compose.dev.yml
  - docker-compose.yml
  - Dockerfile
  - go.mod
  - go.sum
  - internal/alert/alert_test.go
  - internal/alert/alert.go
  - internal/bot/admin_test.go
  - internal/bot/admin.go
  - internal/bot/bot_test.go
  - internal/bot/bot.go
  - internal/bot/callback_test.go
  - internal/bot/callback.go
  - internal/bot/config_test.go
  - internal/bot/config.go
  - internal/bot/filters_test.go
  - internal/bot/filters.go
  - internal/bot/list_test.go
  - internal/bot/list.go
  - internal/bot/tg_test.go
  - internal/bot/tg.go
  - internal/event/filter.go
  - internal/ingest/runner.go
  - internal/store/alerts_test.go
  - internal/store/alerts.go
  - internal/store/facets_test.go
  - internal/store/facets.go
  - internal/store/listing_test.go
  - internal/store/listing.go
  - internal/store/runs.go
  - internal/store/store_test.go
  - internal/store/storetest/storetest.go
  - internal/store/users_test.go
  - internal/store/users.go
  - migrations/00003_source_alerts.sql
covered_digest: "v1:sha256:c08a02a34343b743232594577324b486b47fd8c385817ecb3679a0d34dedd7e6"
behavior_unverified: 0
overrides_applied: 0
---

# Phase 4: Telegram Search — Verification Report

**Цель фазы:** пользователь находит забег в Telegram по дате, городу, дистанции и типу и переходит на регистрацию.
**Проверено:** 2026-09-30, по коду и тестам на коммите `95f33a0`, логам `$HOME/run-agg-phase4/` (вне репозитория) и приёмке владельца C1–C5 (`04-08-SUMMARY.md`).
**Статус:** passed.

Источники доказательств:
- **Тесты.** `go test -p 1 -count=1 ./...` на тестовой БД PostgreSQL 17: 7 пакетов ok, 228 PASS, 0 FAIL, 0 SKIP. `gofmt -l` пусто, `go vet` без замечаний.
- **Проверки скриптов развёртывания.** `deploy/confirm-test.sh` → `CONFIRM OK`, `deploy/deploy-apply-test.sh` → `DEPLOY APPLY OK`, `deploy/load-postgres-test.sh` → `LOAD POSTGRES TEST OK`, проверка инструментов 04-07 → `DEPLOY TOOLING OK`, `deploy/smoke.sh` → `SMOKE OK`.
- **Логи VPS** (сохранены владельцем, без IP и токенов): `deploy-verify-accepted.log`, `deploy-verify-final.log`, `deploy-verify-3.log`.
- **Приёмка владельца на production-боте:** «C1 ок; C2 ок; C3 ок; C4 ок; C5 ок, со второго аккаунта /runs не работает».

## ROADMAP Success Criteria

| # | Критерий | Статус | Доказательство |
|---|---|---|---|
| 1 | `/start → Найти забег → фильтры → список → карточка → регистрация` на реальных данных | ✓ VERIFIED | Владелец: C1 на production-боте с 134 событиями (прогон 1 принят, `events_listed=134` в `deploy-verify-3.log`). Тест `TestTracerSearch` (тот же путь на фикстуре Марафонца и PostgreSQL 17) |
| 2 | Листание вперёд и назад без потери фильтров; устаревшие кнопки не ломают бот | ✓ VERIFIED | Владелец: C2 (листание, старое сообщение после смены фильтров → «Фильтры изменились…», первая страница). Тесты `TestListPaging`, `TestListStaleAndClamp`, `TestInaccessibleMessage`, `TestAnswerOncePerCallback`, `TestCallbackReject` |
| 3 | Карточка: даты, время, город, адрес, тип, дистанции, описание, регистрация, источники | ✓ VERIFIED (с оговоркой) | Владелец: C3 (дата, город, тип, все дистанции, «Источники: Марафонец», [К списку]). Код `internal/bot/list.go:171–215` выводит время старта, адрес, описание (≤ 1000), кнопку регистрации (только http/https). Тесты `TestCard`, `TestCardEscapingAndURLs`, `TestLongTexts`, `TestValidURL`. **Оговорка:** у Марафонца нет времени, адреса и описания — на реальных данных не наблюдались; положительного теста на строку «Старт в …» нет, адрес и описание в `TestLongTexts` проверяются по длине |
| 4 | Скрытые и слитые не показываются; отменённые и перенесённые — с пометкой в списке и карточке | ✓ VERIFIED | Тесты `TestHiddenAndMerged` (скрытое и слитое пропадают из выдачи, их карточки — «Событие больше не доступно»), `TestStatusMarks` (`[ОТМЕНЁН]`, `[ПЕРЕНЕСЁН]` в строке списка и в карточке), `TestListEvents` (SQL `is_listed AND merged_into_id IS NULL`). На реальных данных таких событий нет (C4) — проверено только тестами |
| 5 | `/runs` и алерты только администратору; о пользователях — только Telegram ID и настройки | ✓ VERIFIED | Владелец: C5 (`/runs` у админа — экран прогонов; со второго аккаунта — нет данных), алерт с кнопкой получен и принят. Тесты `TestRunsNonAdmin`, `TestAcceptNonAdmin`, `TestSendAlert` (не-админу — ошибка и ни одного вызова API), `TestUsersColumns`, `TestNoPersonalDataInLogs`. Логи VPS: `users_columns=created_at,filters,filters_v,last_seen_at,tg_id` |

## Requirements Coverage

| Требование | Статус | Доказательство |
|---|---|---|
| BOT-01 long polling, `/start`, «Найти забег» | ✓ | `internal/bot/bot.go` (`tgbot.New`, `Start` — long polling), маршрут `start`; `TestTracerSearch`; C1 |
| BOT-02 фильтры даты (пресеты, месяц), города (топ-10, текст), дистанции (корзины), типа; по умолчанию будущие даты и вся Россия; регион в SQL, не в UI | ✓ | `internal/bot/filters.go`; тесты `TestDateRange`, `TestMonthOptions`, `TestBucketMembership`, `TestFilterFlow`, `TestCityText`, `TestEmptyResult`, `TestResetFilters`, `TestTopCities`, `TestSimilarCities`, `TestEventTypes`; C1, C4.1–4.2 («Показать 134» после сброса). Регион — колонка `events.region` (`migrations/00001_init_schema.sql`), фильтра по региону в UI нет (запрет в 04-02-PLAN) |
| BOT-03 `users.filters` + `filters_v`, устаревший callback понятным сообщением | ✓ | `internal/store/users.go` (`UpdateFilters`: версия растёт только при изменении); `TestUserFilters`, `TestUserFiltersConcurrent`, `TestLoadFilters`, `TestCallbackReject` («Кнопка устарела — нажмите /start»); C2, C4.3 (фильтры сохраняются после `/start`, D-17) |
| BOT-04 `LIMIT/OFFSET` по `start_date, id`, `editMessageText`, callback ≤ 64 байт, роутинг по префиксу, `answerCallbackQuery` на каждый callback | ✓ | `internal/store/listing.go:62–63` (`ORDER BY e.start_date, e.id LIMIT … OFFSET …`); `bot.go` (маршруты `f:`/`p:`/`e:`/`r:`); `TestCallbackLength`, `TestCallbackRoundTrip`, `TestListPaging`, `TestAnswerOncePerCallback`, `TestCallbackPanicAnswered` |
| BOT-05 карточка события | ✓ (оговорка) | См. критерий 3 |
| BOT-06 только `is_listed` без `merged_into_id`; отменённые и перенесённые с пометкой | ✓ | См. критерий 4 |
| BOT-07 `/runs` только `ADMIN_TG_IDS` | ✓ | `internal/bot/admin.go:36` (`isAdmin(m.From.ID)`), `r:*` и `SendAlert` тоже проверяют; `TestRunsNonAdmin`, `TestRunsAdmin`, `TestRunsWithoutButtons`; C5 |
| BOT-08 о пользователях только Telegram ID и настройки | ✓ | `TestUsersColumns`; `deploy-verify-final.log`: `users_columns=created_at,filters,filters_v,last_seen_at,tg_id`; `TestNoPersonalDataInLogs`, `TestLogRedactsToken`; `TestPrivateOnly` (апдейты из групп — ни одного вызова API; отсутствие записи в `users` этим тестом не проверяется) |
| BOT-09 ошибки Telegram API без падения | ✓ | `internal/bot/tg.go` (`classify`, повтор 429, «not modified» — успех, запасная отправка); `TestClassify`, `TestSendBlocked`, `TestRateLimitRetry`, `TestEditNotModified`, `TestEditFallbackToSend`, `TestURLButtonFallback`, `TestCallbackPanicAnswered`. На VPS: при 400 `chat not found` (до `/start` владельца) бот работал дальше, алерт повторялся раз в минуту |
| MON-05 алерт админу при `failed`/`suspect` и всплеске ошибок парсера; один на смену состояния, напоминание через 24 ч | ✓ | `internal/alert/alert.go` (`decide`, `message`, `Tick`); всплеск ошибок парсера выражается статусом прогона (D-14); `TestDecide`, `TestTick`, `TestTickNewSuspectRun` (LO-02), `TestTickSendFailure`, `TestAlertToAccept`; на VPS алерт о suspect-прогоне 1 пришёл с кнопкой, принят (`04-08-SUMMARY.md`), `source_alerts=0` после принятия |
| PRD-01 Compose: postgres 17 (том, без публикации порта, healthcheck), одноразовый migrate, bot, worker (`unless-stopped`, ротация логов), Go 1.27.1 | ✓ | `docker-compose.yml` (`postgres:17`, том `pgdata`, без `ports`, healthcheck; `migrate` с `restart: "no"`; `restart: unless-stopped`; json-file 10m×3); `Dockerfile` (`golang:1.27.1`); `SMOKE OK`; `deploy-verify-3.log`: сервисы на `95f33a07c0c4`, migrate `Exited (0)`, `CHECK port-not-published: OK`, `CHECK port-not-listening: OK` |

**Score:** 11/11 требований; критерии ROADMAP 5/5 (критерий 3 и BOT-05 — с оговоркой по полям, которых нет у источника).

## Deploy Verification

| Проверка | Результат (`deploy-verify-3.log`) |
|---|---|
| Образ | bot, worker, migrate — `aggregator:95f33a07c0c4` |
| Сервисы | postgres healthy, bot healthy, worker Up, migrate `Exited (0)` |
| Порт PostgreSQL | не опубликован, на хосте не слушается |
| Права | `.env` root 600; `agg`, lib, compose, sudoers — root; runagg не в группе docker |
| БД | `run=1|ok|134|accepted=t`, `events_listed=134`, `source_alerts=0` |
| Итог | `DEPLOY VERIFY OK` |

Откат: `IMAGE_TAG=95f33a07c0c4`, `IMAGE_PREV=a4177a83eb8a` — по сообщению владельца (значения `.env` в логи не выводятся).

## Gaps Summary

Пробелов, блокирующих цель, нет.

Не подтверждено на реальных данных, только тестами или кодом:
- вывод времени, адреса и описания в карточке — полей нет у источника;
- пометки `[ОТМЕНЁН]` и `[ПЕРЕНЕСЁН]`, скрытые и слитые события — таких событий в данных нет;
- LO-02 (новый алерт на новый suspect-прогон) и путь неудачного деплоя LO-04 — не возникали.

Отложенные замечания ревью — `04-REVIEW.md`, раздел «Deferred».
