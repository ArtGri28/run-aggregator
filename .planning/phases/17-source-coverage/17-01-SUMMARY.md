---
phase: 17-source-coverage
plan: 01
status: done-local
requirements-completed: []
---

# 17-01: источник MyRace по API

Статус: код и тесты готовы локально; выкат — план 17-02. План утверждён владельцем 2026-10-14 («утверждаю план 17-01»).

## Изменения

| Файл | Что |
|---|---|
| `internal/ingest/fetch.go` | `GetAuth` — `Get` с заголовком `Authorization` и `Accept: application/json`; `Get` вызывает его с пустым ключом. Ключ не попадает ни в `RawDoc`, ни в тексты ошибок |
| `internal/ingest/sources/myrace/myrace.go` | `Source`: страницы `/calendar?limit=100` по `next_page_token`, пауза секунда, предел 50 страниц — ошибка, а не усечение; `Policy` — `NoFallback` |
| `internal/ingest/sources/myrace/parse.go` | разбор страницы по D-01…D-05: отбор видов спорта, подпись типа (`obstacle` > `trail` > `running`), дистанции из названий беговых активностей, ссылка на страницу события как ссылка на регистрацию, страна только из `location.country` |
| `internal/ingest/sources/myrace/testdata/` | две страницы из образца 2026-10-14 (14 событий), README с SHA-256 |
| `cmd/aggregator/main.go` | `newSources` добавляет `myrace`, только если `MYRACE_TOKEN` непуст |
| `internal/bot/list.go` | название источника «MyRace» |
| `docker-compose.yml` | `MYRACE_TOKEN` в якоре `x-app-env` (bot и worker); `webapp` его не получает |
| `.env.example` | пустая строка `MYRACE_TOKEN=` с пояснением |

## Результат на образце (день загрузки 2026-10-14)

Разобрано 13 записей (акватлон пропущен разбором), `Valid` 12, `Expired` 1 (абонемент с датой 2025-12-17), `OutOfScope` 2 (Ереван без страны, событие без `location`), в каталог — 10. Шифры («ARKHYZ 15») и возрастные группы дистанций не дают; «Лонг ~23 km», «6,5км», «21.1 км» разбираются.

## Проверки

- `go vet ./...` — без замечаний; `gofmt -l` — пусто.
- Полный прогон с `TEST_DATABASE_URL`: 512 PASS, 0 FAIL, 0 SKIP (было 505; новые — `TestFetchAuth` и шесть тестов `myrace`). Первый запуск упал на подключении к базе: Docker был выключен; после запуска тестовой базы — зелёный.
- Значение токена нигде в репозитории не задано; в `.env.example` строка пустая.
- Эталоны экранов бота не менялись.

## Не сделано (план 17-02)

Строка `MYRACE_TOKEN` в `.env` сервера, выкат образа, первый прогон и сверка прироста, сообщение техлиду о публикации.
