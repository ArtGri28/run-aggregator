---
phase: 18-swim-cycle
plan: 05
status: rolled-out
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

## Повторный выкат с исправлением (2026-10-19)

| Шаг | Результат |
|---|---|
| Образ | `SMOKE OK`, `AGG LOAD OK: aggregator:f3ee9869a54c`; хеш пакета `b73f08b1…`, `INSTALL` не нужен |
| Preflight | `DEPLOY PREFLIGHT DONE` |
| Deploy | `DEPLOY APPLY DONE`; `IMAGE_PREV=30cd4f8bd0be` |
| Проверка | `DEPLOY VERIFY OK`; бот, воркер, `webapp` на `f3ee9869a54c`, `healthy`; воркер запущен с девятью источниками. Прогоны SwimRocket 286, Swimcup 285, CyclingRace 288 приняты владельцем кнопкой (`accepted=t`); `events_listed` 715 → 771 (+56 = 23 + 28 + 5); `events_merged=123`, `users=44`, `dedup_candidates` и автослияние — как до выката. `source_alerts=1` — сбой O-time 287. Следующий прогон O-time по расписанию — 2026-10-19 20:04 UTC; Tristyle — 09:47 UTC |

## Telegram

Шаги 9–10: владелец — «В боте и Mini App всё работает» (кнопки плавания и велоспорта, списки, карточки, фильтры Mini App; бег и лыжи как раньше).

ROLLOUT DONE: 2026-10-19

PRODUCTION ACCEPTANCE: 2026-10-19
«В боте и Mini App всё работает»

## Проверка после прогонов (2026-10-20)

`agg verify` (лог `p18-after-runs-verify.log`): `DEPLOY VERIFY OK`; все сервисы на `f3ee9869a54c`, `healthy`. Последние прогоны всех девяти источников — `ok`: O-time 328 (59 записей; первый прогон после исправления принят владельцем), Tristyle 324 (16 записей — бег, плавание и велоспорт), SwimRocket 331, Swimcup 332, CyclingRace 330, MyRace 327, ПроБЕГ 329, Марафонец 325, Лига Героев 326; `source_alerts=0`.

- `events_listed` 771 → 828.
- `events_merged` 123 → 139, `dedup_candidates=pending:0|confirmed:139|rejected:11` — пары O-time с ПроБЕГом и Марафонцем решены владельцем вручную кнопками `/dupes` 2026-10-20 (16 слияний, 3 отказа); автослияние выключено (`dedup_auto_merge_bot=false`, `dedup_auto_merge_worker=false`).
- `users` 44 → 79 — новые пользователи бота, не связано с выкатом.

Фаза закрыта 2026-10-20 по команде владельца («закрой фазу 18»).
