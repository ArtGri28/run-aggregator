---
phase: 03-first-source
plan: 01
subsystem: ingest/marathonec (testdata)
tags: [fixture, golden, marathonec, d-01, d-12]
status: complete

requires:
  - "Phase 1: 01-SOURCES-REPORT §2.1 (ожидаемая структура /calendar-beg/, открытый вопрос G2)"
provides:
  - "internal/ingest/sources/marathonec/testdata/calendar-beg.html — golden-фикстура реальной страницы (133 строки post-row), очищена по D-12"
  - "internal/ingest/sources/marathonec/testdata/README.md — происхождение, SHA-256, замены, контексты captcha, JSON-LD, карта столбцов, даты, значения для normalize"
affects: [03-02-parse, 03-03-normalize, 03-06-first-run]

actuals:
  tokens: 150000   # chars/4: фикстура 146703 (копия ответа) + README 3296
  tasks: 2
  commits: 0
  plan_head_before: d49788c8df5a1a665c502649c88432f96e4053c1

tech-stack:
  added: []
  patterns:
    - "Фикстура — байтовая копия ответа с точечной заменой значений nonce (Python, бинарный режим, CRLF/LF сохранены)"

key-files:
  created:
    - internal/ingest/sources/marathonec/testdata/calendar-beg.html
    - internal/ingest/sources/marathonec/testdata/README.md
  modified: []

key-decisions:
  - "G2 закрыт: 5 вхождений captcha — CSS формы комментариев темы Newsmag (4) и var tds_captcha=\"\" (1); шаблонный элемент, не челлендж"
  - "Область в строках — слаг таксономии oblast; русские названия есть только в data-filters таблицы"
  - "Ссылка названия — единственная ссылка строки: у 105 из 133 строк она внешняя (регистрация), страницы события на сайте у таких строк в таблице нет"

metrics:
  started: 2026-09-29T15:49:06Z
  completed: 2026-09-29T15:56:00Z
  duration: "~7 мин (задача 2); задача 1 — действие владельца"
---

# Phase 3 Plan 01: Golden-фикстура /calendar-beg/ и закрытие G2 Summary

Реальная страница календаря Марафонца (133 строки `post-row`, HTTP 200) сохранена как golden-фикстура с двумя заменёнными nonce; все пять вхождений `captcha` — шаблон темы, челленджа нет; структура таблицы, даты и форматы значений записаны в README как вход для 03-02 и 03-03.

## Выполнено

| Задача | Что | Коммит |
|---|---|---|
| 1 | Владелец выполнил один GET с Mac: `200 114266 text/html; charset=UTF-8` (gzip), `FETCH SAVED` | — (вне репозитория) |
| 2 | Разбор, очистка D-12, README; `FIXTURE OK` | не закоммичено: коммиты делает владелец |

## Ключевые факты

- Стоп-условие D-01/D-02 не сработало: код 200, 133 уникальных `post-row`, `<title>Календарь забегов России 2026-2027</title>`.
- captcha ×5: строка 18 (`<style id="wpr-usedcss">`, селекторы `.td-warning-captcha`/`-score` ×4), строка 266 (`var tds_captcha="";` в `td-generated-header-js`). Вывод — шаблонный элемент.
- JSON-LD ×2: `BlogPosting`/`WebPage`/`BreadcrumbList`… (AIOSEO) и `BreadcrumbList`; `Event` нет — D-10 без изменений.
- Таблица: 9 столбцов по `data-name`: `cf_racedate`, `title`, `tax_distance`, `tax_city`, скрытые `hf_tax_distanciaslovami`, `hf_tax_oblast`, `hf_tax_city`, `hf_tax_month`, `hf_tax_racetype`. Скрытость — только `data-visible="false"` на `<th>`.
- Даты 02/10/2026 … 01/10/2027; прошедших (D-11) 0; дальше +3 лет 0; `data-sort` = полночь UTC даты во всех строках; слаг месяца расходится с датой в 2 строках.
- Строк стало 133 против 148–150 в Phase 1: сайт убирает прошедшие забеги.

## Deviations from Plan

None — план выполнен как написан. Уточнение формулировки: в README ключ JSON-LD назван без символа `@` («ключ `graph`»), чтобы выполнялась проверка плана «в README нет `@`, кроме redacted@example.invalid и шаблона».

## Расхождения с допущениями исследования (для владельца и 03-02/03-03)

1. Research A5 / 01-SOURCES-REPORT п.12 предполагали «ссылку на страницу события + внешние ссылки регистрации» в строке. Факт: одна ссылка на строку — в названии; 28 ведут на `marathonec.ru`, 105 — сразу на внешние сайты (`reg.russiarunning.com` 59, `myrace.info` 18, `toplist.run` 13, …, `vk.com/away.php?to=…` 2). Для 105 строк URL страницы события на сайте в таблице нет. Нужно решить в 03-02, что писать в `SourceEvent.URL` и `RegistrationURL` для таких строк. Фрагмент `#new_tab` (104 ссылки) — маркер плагина.
2. Research A1: область — слаг (`moskovskaya-oblast`), не кириллица; названия — только в `data-filters` (83 термина, все РФ, с нестандартными «Москва и Московская область», «СПБ и Ленинградская область», дублем Марий Эл). 2 строки с несколькими областями (одна — 27 слагов).
3. Дистанции: `км` (дробные через точку), `миль`, `1 час`; `5K`, «100 200 400 м» и «полумарафон» словом в видимом столбце нет. Названий с латиницей капсом нет; «X-Race» — риск ложного срабатывания правила римских цифр.
4. Город: префиксов «г.о./п./с./д.» нет; 2 значения с двумя городами через запятую.

## Known Stubs

Нет.

## Threat Flags

Нет. Фикстура не содержит e-mail и значений nonce/токенов (проверено verify плана); контакт владельца в репозиторий не попал; заголовки и cookie не копировались (сайт cookie не выставил).

## Self-Check: PASSED

- FOUND: internal/ingest/sources/marathonec/testdata/calendar-beg.html
- FOUND: internal/ingest/sources/marathonec/testdata/README.md
- Коммитов 0 — по правилу проекта изменения коммитит владелец вручную.
