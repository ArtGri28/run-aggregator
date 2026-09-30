# Phase 4: Telegram Search - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-09-30
**Phase:** 4-Telegram Search
**Areas discussed:** Где и как живёт бот, Фильтры: дистанции и тип, Экраны и список, Алерты и админка

---

## Где и как живёт бот

| Option | Description | Selected |
|--------|-------------|----------|
| Локально на Mac | go run поверх dev-БД, на VPS — в фазе 6 | |
| Сразу на VPS | Круглосуточно, часть фазы 6 переезжает сюда | ✓ |

| Option | Description | Selected |
|--------|-------------|----------|
| Отдельный dev-бот | Тестовый бот в BotFather, прод — в фазе 6 | |
| Один бот на всё | Сразу будущий production-бот | ✓ |

| Option | Description | Selected |
|--------|-------------|----------|
| postgres + bot + worker | Минимальный production Compose; бэкапы/heartbeat — фаза 6 | ✓ |
| Весь состав фазы 6 сразу | Вместе с бэкапами, heartbeat, алертами | |
| Только bot + postgres, данные с Mac | Worker на Mac, перенос дампом | |

| Option | Description | Selected |
|--------|-------------|----------|
| Минимум сейчас | security-обновления + перезагрузка, compose-плагин, Postgres только во внутренней сети | ✓ |
| Вся жесткость сейчас | + SSH по ключу без root, ufw, tinyproxy | |
| Ничего не трогать | Только compose-плагин | |

| Option | Description | Selected |
|--------|-------------|----------|
| Образ собирается на Mac | docker save \| ssh docker load | ✓ |
| Сборка на VPS | Исходники на сервер, build там | |
| Через git-remote и реестр | GitHub/GitLab + ghcr.io | |

| Option | Description | Selected |
|--------|-------------|----------|
| Вы по готовым командам | PREFLIGHT → APPLY → VERIFY с SHA256, от root | ✓ |
| Отдельный deploy-пользователь | Группа docker, развёртывание инструментами | |

---

## Фильтры: дистанции и тип

| Option | Description | Selected |
|--------|-------------|----------|
| Сплошные диапазоны | ≤5,5 / 5–15 / 15–30 / 30–43 / >43 км, мультивыбор | ✓ |
| Точные корзины MVP | ≤5, 8–12, 20–22,5, 41–43, >43 — с дырами | |

| Option | Description | Selected |
|--------|-------------|----------|
| Только типы из БД | Сейчас «Шоссе», «Трейл» + «Любой» | ✓ |
| Все типы сразу | Шесть типов, часть с пустой выдачей | |

---

## Экраны и список

| Option | Description | Selected |
|--------|-------------|----------|
| Один экран-сводка | Сводка фильтров + кнопки подменю | ✓ |
| Сразу список | Список ближайших, фильтры кнопкой | |

| Option | Description | Selected |
|--------|-------------|----------|
| 5 кнопок-событий | 5 строк в тексте, кнопки 1…5, навигация | ✓ |
| 10 кнопок-названий | Кнопка на событие, 10 на страницу | |

| Option | Description | Selected |
|--------|-------------|----------|
| Подсказка + ослабить | Похожие города; кнопки снять фильтр | ✓ |
| Просто сообщение | «Ничего не найдено» + «Изменить фильтры» | |

---

## Алерты и админка

| Option | Description | Selected |
|--------|-------------|----------|
| Бот по ingest_runs | Бот раз в минуту читает прогоны, состояние в БД | ✓ |
| Worker напрямую | Worker шлёт сам, токен в двух процессах | |

| Option | Description | Selected |
|--------|-------------|----------|
| Через статус прогона | suspect/failed уже покрывают всплеск | ✓ |
| Отдельный порог | Доп. алерт при росте rejected при ok | |

| Option | Description | Selected |
|--------|-------------|----------|
| Да, в алерте | Кнопка [Принять как базу] для ADMIN_TG_IDS | ✓ |
| Нет, только CLI | accept-baseline через SSH | |

---

## Claude's Discretion

- Тексты, эмодзи, форматы, порядок кнопок; хранение «уже оповещено»; базовый образ и детали compose; формат `/runs`; поиск похожих городов; часовой пояс для показа времени.

## Deferred Ideas

- Бэкапы, heartbeat, инфраструктурные алерты, runbook; SSH/ufw/swap/часы/tinyproxy — Phase 6.
- `/dupes` — Phase 5.
