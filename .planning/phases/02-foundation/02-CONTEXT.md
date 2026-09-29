# Phase 2: Foundation - Context

**Gathered:** 2026-09-29
**Status:** Ready for planning

<domain>
## Phase Boundary

Каркас проекта на Go: один бинарник с подкомандами `bot`, `worker`, `migrate`, `healthcheck`; полная схема PostgreSQL 17 через `goose`-миграции; слой `store` на `pgx/v5`+`pgxpool` (весь SQL только там); пакет `normalize` (title_norm, city_key, даты/часовой пояс, дистанции в метрах, тип, country_code); тестовая инфраструктура на реальной PostgreSQL. Никакой сетевой логики (Fetch/Parse источников), никакого Telegram-бота, никакого дедупа — это следующие фазы. `healthcheck` в этой фазе — только проверка соединения с БД, без heartbeat-таблицы и алертов (Phase 6).

</domain>

<decisions>
## Implementation Decisions

### Тестовая инфраструктура (FND-05)
- **D-01:** Интеграционные тесты получают реальную PostgreSQL 17 через отдельный `docker-compose.test.yml` (только сервис `postgres:17`, без `bot`/`worker`/`migrate`). Отдельный файл от production-состава Docker Compose из `MVP.md` §10.2 — тот появится в Phase 6.

### Подкоманда healthcheck (FND-01)
- **D-02:** `healthcheck` в Phase 2 делает реальную проверку — подключается к PostgreSQL через `pgxpool` и выполняет `SELECT 1`; код выхода 0 при успехе, 1 при ошибке. Пригодно для `HEALTHCHECK` в Docker Compose и для ручной проверки. Heartbeat-таблица и мониторинг/алерты из `MVP.md` §11 — Phase 6, здесь не реализуются.

### Начальный словарь городов (`city_key`, MVP §6)
- **D-03:** Словарь синонимов в Phase 2 минимальный — только города/регионы, реально встречающиеся у двух утверждённых источников Phase 1 (Марафонец, ПроБЕГ), плюс очевидные синонимы (СПб/Питер/Санкт-Петербург и подобные). Расширяется по мере подключения новых источников в следующих фазах, а не зашивается заранее общероссийским списком. Неизвестный город получает `city_key` транслитерацией (уже зафиксировано в `MVP.md` §6) — ничего не ломается.

### Claude's Discretion
- Точная структура каталогов внутри `internal/` (кроме уже зафиксированной в `MVP.md` §3.2), формат имён файлов миграций `goose`, конкретный список переменных окружения (кроме того, что секреты не коммитятся) — оставлено на усмотрение планировщика/исполнителя.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Архитектура и стек
- `MVP.md` §3 (Архитектура), §3.1 (Стек), §3.2 (Структура проекта), §3.3 (Репозиторий и Git)

### Модель данных
- `MVP.md` §4 (вся секция: таблицы, поля events, дистанции, статус/состояние, слияние, индексы, совместимость с будущим)

### Ingestion pipeline (контракты, не реализация в этой фазе)
- `MVP.md` §5.1-5.2 (payload, структура пакета источника — нужно для того, чтобы store/схема не блокировали Phase 3)

### Нормализация
- `MVP.md` §6 (вся секция: title_norm, city_key, регион, даты/время, дистанции, тип, country_code, описание/изображения)

### Инфраструктура (для отличия от Phase 2 test-compose)
- `MVP.md` §10.2 (Docker Compose production-состав — НЕ трогать в Phase 2, только для контраста с D-01)

### Требования и роадмап
- `.planning/REQUIREMENTS.md` — FND-01..FND-06
- `.planning/ROADMAP.md` — раздел Phase 2

### Итоги Phase 1 (источники и их данные, важны для normalize/country_code реализации)
- `.planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md` §1, §2.1, §2.4, §4.2 (утверждённые источники, country_policy, external_id по каждому)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
Нет — репозиторий пока не содержит кода на Go (только `.planning/`, `MVP.md`, `.gitignore`). Phase 2 создаёт код с нуля.

### Established Patterns
Нет установленных паттернов кода — это первая кодовая фаза проекта. Все паттерны задаются `MVP.md` §3.2 (структура пакетов) и решениями этой фазы.

### Integration Points
`internal/store` — единственная точка входа для Phase 3 (первый источник) и последующих фаз: `event`-домен и `store`-интерфейсы, которые Phase 2 создаёт, должны быть готовы принять данные от `ingest`-пакета без изменения общего контракта (`MVP.md` §5.2).

</code_context>

<specifics>
## Specific Ideas

No specific requirements beyond the decisions above — стандартные подходы для Go/PostgreSQL проекта в остальном.

</specifics>

<deferred>
## Deferred Ideas

- Production Docker Compose (postgres/migrate/bot/worker, §10.2) — Phase 6.
- Heartbeat-таблица, мониторинг, алерты (§11) — Phase 6.
- Расширение словаря городов за пределы двух утверждённых источников — по мере подключения новых источников (Phase 3+).

### Reviewed Todos (not folded)
None — discussion stayed within phase scope.

</deferred>

---

*Phase: 2-Foundation*
*Context gathered: 2026-09-29*
