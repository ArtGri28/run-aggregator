# Roadmap: Telegram-бот — агрегатор забегов

## Overview

Путь от проверки источников до работающего сервиса на VPS. Сначала выясняем, какие источники реально доступны и разрешены (Phase 1, логический «Phase 0» из `MVP.md`), затем строим основу данных, подключаем первый источник с контролем качества, даём пользователю поиск в Telegram, добавляем остальные источники с дедупликацией и выводим всё в production с бэкапами и мониторингом. Качество каталога (полнота, актуальность, отсутствие дублей, стабильность сбора) важнее числа функций бота. Источник истины — `MVP.md`.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Source Discovery / VPS Audit** - Проверить 2–3 источника с VPS и провести аудит VPS только чтением (логический Phase 0) (completed 2026-09-29)
- [x] **Phase 2: Foundation** - Модуль Go, схема PostgreSQL 17, домен, `store`, `normalize`, тестовая инфраструктура (completed 2026-09-29)
- [x] **Phase 3: First Source** - Сбор данных из первого источника: Fetch/Parse/Validate, `ingest_runs`, контроль качества, алерты (completed 2026-09-30)
- [x] **Phase 4: Telegram Search** - Бот на long polling: фильтры, список, карточка, пагинация, админ-доступ (completed 2026-09-30)
- [ ] **Phase 5: Multiple Sources & Deduplication** - Источники 2–3, кандидаты в дубли, `/dupes`, калибровка порогов
- [ ] **Phase 6: Production** - Docker Compose на VPS, бэкапы и проверка восстановления, heartbeat, runbook

## Phase Details

### Phase 1: Source Discovery / VPS Audit

**Goal**: Известно, какие 2–3 источника можно подключать, как получать из них данные и в каком состоянии VPS; решения зафиксированы до написания кода
**Depends on**: Nothing (first phase)
**Requirements**: DISC-01, DISC-02, DISC-03, DISC-04
**Success Criteria** (what must be TRUE):

  1. Для каждого кандидата есть проверка доступности с VPS, `robots.txt`, ToS, API/JSON/JSON-LD/sitemap/RSS/HTML, лимитов, антибот-защиты, полей и `external_id`
  2. Утверждён набор из 2–3 источников; неудобный или недоступный кандидат заменён, антибот не обходится
  3. Аудит VPS (`tinyproxy`, часы, `ufw`, Docker и Compose, swap, порты) выполнен по подготовленным командам только на чтение (запуск обёрткой с отдельным ключом проекта после утверждения блоков владельцем), записан без секретов, и на VPS ничего не изменено
  4. Итоговый отчёт содержит способ получения данных, `external_id`, частоту и число запросов для каждого источника

**Plans**: 3/3 plans executed

Plans:
**Wave 1**

- [x] 01-01-PLAN.md — Tracer: команды только на чтение, проверки безопасности и прогон с VPS для Марафонца → карточка в отчёте

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 01-02-PLAN.md — Решение владельца по соглашению GET.RUN, команды прогона 2 (аудит VPS и остальные источники), публичные карточки, прогон 2 владельцем

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 01-03-PLAN.md — Анализ прогона 2: 01-VPS-AUDIT.md, ворота G1–G6, решение владельца DISC-02, итоговый отчёт DISC-04

### Phase 2: Foundation

**Goal**: Есть каркас проекта и схема данных, на которую можно сажать сбор данных и поиск
**Depends on**: Phase 1
**Requirements**: FND-01, FND-02, FND-03, FND-04, FND-05, FND-06
**Success Criteria** (what must be TRUE):

  1. Один бинарник собирается и запускает подкоманды `bot`, `worker`, `migrate`, `healthcheck`
  2. Подкоманда `migrate` создаёт всю схему MVP в PostgreSQL 17 с нуля
  3. `normalize` превращает «СПб», «Питер» и «Санкт-Петербург» в один `city_key`, «Россия» и «Russia» в `country_code = RU`, а «21,1 км» и «полумарафон» в метры; длинное описание обрезается до 1000 символов (проверено тестами)
  4. Интеграционный тест на реальной PostgreSQL проходит upsert `source_events` и `events`
  5. Секреты берутся из окружения и не попадают в репозиторий

**Plans**: 2/2 plans executed

Plans:
**Wave 1**

- [x] 02-01-PLAN.md — Tracer: бинарник, goose-миграции из embed, healthcheck, тестовая PostgreSQL 17; домен event и идемпотентные upsert source_events/events с интеграционными тестами

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 02-02-PLAN.md — Пакет normalize: город/регион/city_key (словарь D-03 + транслитерация), часовой пояс, country_code, дистанции, title_norm, описание, тип, дата и время

### Phase 3: First Source

**Goal**: Реальные данные первого источника попадают в БД идемпотентно, а подозрительные прогоны не портят каталог
**Depends on**: Phase 1, Phase 2
**Requirements**: ING-01, ING-02, ING-03, ING-04, ING-05, ING-06, ING-07, ING-08, ING-09, ING-10, ING-11, ING-13, MON-01, MON-02
**Success Criteria** (what must be TRUE):

  1. Парсер первого источника проходит golden-тесты на сохранённых файлах без обращения к сети
  2. Повторный прогон не создаёт дублей и не меняет данные, если источник не изменился
  3. Прогон с резким падением или ростом числа записей помечается `suspect` и ничего не применяет; статус и причина видны в `ingest_runs` и структурных логах (Telegram-алерты появляются в Phase 4)
  4. Исчезновение записи из источника скрывает событие через `is_listed` только по здоровым прогонам и не меняет `events.status`
  5. Реальные события первого источника лежат в БД с `country_code`, `region`, `city`, каждый запуск виден в `ingest_runs`; описание не длиннее 1000 символов, изображения не сохраняются; санити-проверка на этих данных окончательно подтверждает (или отклоняет) предварительный статус кандидата на fallback `RU` из Phase 1

**Plans**: 7/7 plans executed

Plans:
**Wave 1**

- [x] 03-01-PLAN.md — D-01: владелец скачивает /calendar-beg/ с Mac; разбор структуры и вхождений captcha, очищенная golden-фикстура (D-12)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 03-02-PLAN.md — Tracer: фикстура → Parse (x/net/html) → Prepare → store → PostgreSQL 17, идемпотентность; golden-тесты Parse

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 03-03-PLAN.md — Справочник субъектов РФ, правила normalize по фикстуре, полный Prepare (D-07, D-09, D-11) и оценка прогона Evaluate (D-05)

**Wave 4** *(blocked on Wave 3 completion)*

- [x] 03-04-PLAN.md — Миграция 00002, жизненный цикл прогона и наблюдение в store; Runner «оценка до записи» и accept-baseline (D-06)

**Wave 5** *(blocked on Wave 4 completion)*

- [x] 03-05-PLAN.md — Fetcher, конфигурация, сырые ответы, планировщик, санити-отчёт, подкоманды worker, dev-PostgreSQL (D-03)

**Wave 6** *(blocked on Wave 5 completion)*

- [x] 03-06-PLAN.md — Владелец: первый реальный прогон (suspect), санити-отчёт RU и решение по fallback (D-07)

**Wave 7** *(blocked on Wave 6 completion)*

- [x] 03-07-PLAN.md — Запись решения и политики источника, accept-baseline и второй прогон владельцем, проверка критериев ROADMAP

### Phase 4: Telegram Search

**Goal**: Пользователь находит забег в Telegram по дате, городу, дистанции и типу и переходит на регистрацию
**Depends on**: Phase 3
**Requirements**: BOT-01, BOT-02, BOT-03, BOT-04, BOT-05, BOT-06, BOT-07, BOT-08, BOT-09, MON-05, PRD-01
**Note**: PRD-01 перенесён из Phase 6 решением владельца 2026-09-30 (D-03 в 04-CONTEXT.md)
**Success Criteria** (what must be TRUE):

  1. Пользователь проходит `/start → Найти забег → фильтры → список → карточка → регистрация` на реальных данных
  2. Листание списка работает вперёд и назад без потери фильтров, устаревшие кнопки не ломают бот
  3. Карточка показывает даты, время, город, адрес, тип, дистанции, короткое описание, ссылку на регистрацию и источники
  4. Скрытые и слитые события в выдаче не появляются; отменённые и перенесённые **не скрываются** и показываются явно с пометкой `[ОТМЕНЁН]` или `[ПЕРЕНЕСЁН]` в списке и в карточке
  5. `/runs` и Telegram-алерты о `failed` и `suspect` прогонах доступны только администратору (`ADMIN_TG_IDS`); о пользователях хранятся только Telegram ID и настройки поиска

**Plans**: 8/8 plans executed

Plans:
**Wave 1**

- [x] 04-01-PLAN.md — Tracer: `/start → Найти забег → сводка → Показать → список` на реальных событиях через фиктивный Bot API и PostgreSQL 17; go-telegram/bot v1.27.0, кодек callback, users/выдача в store, runBot

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 04-02-PLAN.md — Фильтры: сводка D-10, пресеты дат и месяцы, город (топ-10 и текст), корзины D-08, типы D-09, «не найдено»/«ничего не нашлось» D-12

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 04-03-PLAN.md — Листание, карточка с регистрацией и источниками, пометки статуса, скрытые/слитые; ошибки Telegram API без падений (BOT-09)

**Wave 4** *(blocked on Wave 3 completion)*

- [x] 04-04-PLAN.md — Миграция 00003_source_alerts, store для алертов и /runs, пакет alert: решение, текст, цикл раз в минуту (MON-05)

**Wave 5** *(blocked on Wave 4 completion)*

- [x] 04-05-PLAN.md — `/runs`, принятие прогона кнопкой с подтверждением (D-15), SendAlert, runBot с циклом алертов; общий newRunner для worker и bot

**Wave 6** *(blocked on Wave 5 completion)*

- [x] 04-06-PLAN.md — Dockerfile, docker-compose.yml (PRD-01), smoke.sh, ship.sh, однострочники SHA256, скрипты VPS PREFLIGHT/APPLY/VERIFY, deploy/README.md

**Wave 7** *(blocked on Wave 6 completion)*

- [x] 04-07-PLAN.md — Владелец: BotFather и ID, доставка образа, PREFLIGHT хоста, решение по изменениям хоста (D-04), подготовка хоста и развёртывание

**Wave 8** *(blocked on Wave 7 completion)*

- [x] 04-08-PLAN.md — Владелец: первый прогон с VPS → алерт → принятие кнопкой (D-07); приёмка пяти критериев ROADMAP на production-боте

### Phase 5: Multiple Sources & Deduplication

**Goal**: Несколько источников дают единый каталог без дублей и без ложных слияний
**Depends on**: Phase 4
**Requirements**: ING-12, DDP-01, DDP-02, DDP-03, DDP-04, DDP-05, DDP-06, DDP-07, DDP-08, DDP-09, DDP-10
**Success Criteria** (what must be TRUE):

  1. Подключены второй и (если утверждён) третий источники; добавление нового источника не требует изменения общего интерфейса ingestion и основной архитектуры pipeline, источник подключается через предусмотренный source adapter/parser contract (source-specific код добавлять разрешено)
  2. Одно и то же соревнование из нескольких источников показывается одним событием с объединёнными дистанциями и всеми источниками
  3. Разные соревнования, включая одноимённые в разные годы, не сливаются автоматически; сомнительные случаи попадают в `/dupes`
  4. Администратор подтверждает и отклоняет кандидатов, получает ежедневную сводку; отклонённая пара не возвращается
  5. Пороги откалиброваны на размеченном наборе реальных пар и настраиваются без правки кода

**Plans**: TBD

### Phase 6: Production

**Goal**: Сервис работает на VPS, переживает перезапуск, сообщает о сбоях и восстанавливается из проверенного бэкапа
**Depends on**: Phase 5
**Requirements**: MON-03, MON-04, PRD-02, PRD-03, PRD-04, PRD-05, PRD-06, PRD-07
**Note**: часть PRD-02 (compose-плагин, обновления и перезагрузка) выполняется в Phase 4
**Success Criteria** (what must be TRUE):

  1. `docker compose up` на VPS поднимает `postgres`, `bot`, `worker`; после перезапуска контейнеров и хоста данные целы, а прогоны продолжаются
  2. Остановка `worker` или `bot`, недоступная БД и сбой бэкапа приводят к алерту; падение хоста замечает внешний heartbeat
  3. Ночной зашифрованный бэкап уходит во внешнее хранилище, ключ расшифровки не на VPS, хранится 7 ежедневных и 4 еженедельных копии
  4. Восстановление из бэкапа проверено, шаги записаны в runbook
  5. Хост подготовлен по разрешению владельца (Compose, swap, часы, `ufw`), судьба `tinyproxy` решена владельцем

**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Source Discovery / VPS Audit | 3/3 | Complete    | 2026-09-29 |
| 2. Foundation | 2/2 | Complete    | 2026-09-29 |
| 3. First Source | 7/7 | Complete    | 2026-09-30 |
| 4. Telegram Search | 8/8 | Complete    | 2026-09-30 |
| 5. Multiple Sources & Deduplication | 0/TBD | Not started | - |
| 6. Production | 0/TBD | Not started | - |
