# Phase 15: выкат Mini App в production

Все команды на сервере выполнял владелец; логи — `$HOME/run-agg-phase4/p17-*.log` (этап А) и `p15-enable-*.log` (этап Б) вне репозитория. Домен в репозиторий не записывается — ниже `<домен>`.

## Этап А (2026-10-14, вместе с MyRace)

Выполнен в составе выката 17-02 (`.planning/phases/17-source-coverage/17-02-SUMMARY.md`): пакет установлен словом `INSTALL`, образ `d9f4501f0037`, `IMAGE_PREV=5ebde6c49d95`; `agg verify` — `DEPLOY VERIFY OK`, `webapp_profile=off`, `published=none`, `CHECK ports-only-caddy: OK`, `CHECK ports-caddy-80-443: OK`.

## Этап Б (2026-10-14)

Предусловия: `PRODUCTION CHECK:` фазы 14 записан (`15d793a`); владелец: «домен готов, разрешаю этап Б». A-запись `@` исправлена в панели регистратора; на момент разрешения новый адрес отдавал 1.1.1.1, кэш 8.8.8.8 и 77.88.8.8 ещё держал прежний. Порты хоста: `listening_80_443=0`. Есть ли сетевой экран у провайдера, владелец не знал — трафик на 80/443 подтверждён выдачей сертификата.

| Шаг | Результат |
|---|---|
| 1–3. Строки `.env` | `WEBAPP_SECRET`, `WEBAPP_DOMAIN`, `COMPOSE_PROFILES=webapp` — владелец: «все три строки записаны, везде 1» |
| 4. Preflight | `DEPLOY PREFLIGHT DONE`; три переменные есть, `профиль webapp: включён`, права 600, `compose config -q: OK`; установленный и развёрнутый `docker-compose.yml` одинаковы (`53dadd38…`) |
| 5. Deploy того же тега | `DEPLOY APPLY DONE`; образ `caddy` скачан; созданы сеть `edge`, тома `caddy_data`, `caddy_config`; `webapp` `Healthy`, `caddy` запущен; `bot` и `worker` не пересоздавались; `IMAGE_PREV=5ebde6c49d95` |
| 6. Проверка | `DEPLOY VERIFY OK`; `webapp_profile=on`; `webapp` `Up (healthy)`, `caddy` `Up`; ровно `published=caddy 443/tcp` и `published=caddy 80/tcp`; `CHECK port-not-published`, `ports-only-caddy`, `ports-caddy-80-443` — OK; `events_listed=647`, `events_merged=122`, автослияние выключено; в логе `webapp` только метод, маршрут, код и длительность |
| 7. Сертификат и ответы | HTTPS по HTTP/2 работает; `GET /` — 200, `GET /app.js` — 200, ответ `via: 1.1 Caddy`, `content-type: text/html`; `content-security-policy` есть; `GET /api/v1/meta` без входа — 401 |
| 8. BotFather | владелец: «кнопку добавил, Mini App открывается» |

Замечание: первый `curl -sI` (`HEAD /`) вернул `404`; `GET /` в тот же момент и в логе `webapp` — 200. Локально полный набор маршрутов на `HEAD /` и `HEAD /app.js` отвечает 200. Telegram открывает Mini App через `GET`; причина не установлена.

ROLLOUT DONE: 2026-10-14
«кнопку добавил, Mini App открывается»

## Production-приёмка

Владельцу предложен чек-лист `deploy/README.md` («Что проверить в Telegram»): каталог и листание, фильтры, поиск, карточка и «Регистрация», сердечко в списке и карточке ↔ «⭐ Избранное» бота, календарь, избранное, профиль, «Назад» Telegram, нижняя панель не перекрывает список, тёмная тема. Подпись кнопки меню — «Каталог» (решение владельца).

PRODUCTION ACCEPTANCE: 2026-10-14
«приёмка Mini App пройдена»

## Осталось

- Картинка героя — когда владелец пришлёт файл.
- Причина `404` на `HEAD /`.
- Отметки фазы 15 в `ROADMAP.md`, `STATE.md`, `REQUIREMENTS.md` — по команде владельца о закрытии фазы.
