---
phase: 18-swim-cycle
plan: 05
status: in-progress
requirements-completed: []
---

# 18-05: выкат фазы 18

План утверждён владельцем 2026-10-19 («утверждаю план 18-05»). Все команды на сервере выполнял владелец; логи — `$HOME/run-agg-phase4/p18-*.log` вне репозитория. Разрешения SwimRocket и O-time записаны в `SOURCE-PERMISSION-LETTERS.md` (получены 2026-10-18).

## Ход выката (2026-10-19)

| Шаг | Результат |
|---|---|
| 1. Копии | `STATUS OK`, `usable=yes`, копий 9, возраст 16637 с |
| 2. Состояние до | `DEPLOY VERIFY OK`; образ `f673d815ee9b`; `events_listed=715`, `events_merged=123`, `dedup_candidates=pending:0|confirmed:123|rejected:8|fresh:0`, `users=44`, `source_alerts=0`; автослияние выключено; прогоны пяти источников `ok` |
| 3. Образ | `SMOKE OK`, `AGG LOAD OK: aggregator:30cd4f8bd0be`; хеш пакета `b73f08b1…` — как при выкатах 16 и 17, `INSTALL` не нужен |
| 4. Preflight | `DEPLOY PREFLIGHT DONE`; профиль `webapp` включён; compose-файлы установленный и развёрнутый одинаковы |
| 5. Deploy | `DEPLOY APPLY DONE`; `IMAGE_PREV=f673d815ee9b`; `postgres` и `caddy` не перезапускались |
| 6. Проверка | `DEPLOY VERIFY OK`; бот, воркер, `webapp` на `30cd4f8bd0be`, `healthy`; `events_listed`, `events_merged`, `users`, автослияние — как до выката. Первые прогоны: SwimRocket 286 — `suspect` (171 запись, прошедших 148), Swimcup 285 — `suspect` (28), CyclingRace 288 — `suspect` (5), **O-time 287 — `failed`: «загрузка: fetch: тело не UTF-8»** |
| 7. Принятие | владелец разрешил принять SwimRocket, Swimcup, CyclingRace кнопкой; O-time не принимается |

## Сбой O-time

Причина: загрузчик (`ingest.Fetcher`) отвергал любое тело не в UTF-8, а O-time отдаёт windows-1251 (`Content-Type: text/html; charset=cp-1251`). Тест адаптера разбирал сохранённую страницу, а тест загрузки шёл на заглушке в UTF-8 — сочетание не проверялось. В каталог ничего не записано.

Исправление (по разрешению владельца 2026-10-19): загрузчик перекодирует тело в UTF-8, если сервер объявил windows-1251 (`windows-1251`, `cp-1251`, `cp1251`); остальные кодировки — ошибка, как раньше. Адаптер O-time перекодирует только тело не в UTF-8 (сохранённую страницу). Тесты: `TestFetchWindows1251`, `TestFetchParseWindows1251` — загрузка сохранённой страницы через тестовый сервер с заголовком O-time и разбор, те же 47 записей. Полный прогон: 550 PASS, 0 FAIL, 0 SKIP. Повторный выкат — тем же порядком: образ, preflight, deploy, проверка.
