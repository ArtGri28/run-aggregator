---
phase: 23-russialoppet-regplace
plan: 01
status: done
---

# 23-01: источник russialoppet.ru — итог

Выполнен 2026-10-20 по утверждению владельца («План утверждаю, делай 23-01»). Локально; на production не выкачено (23-02).

## Изменения

- `internal/ingest/fetch.go` — поле `FetchConfig.TLS` и `Fetcher.WithTLS`: копия загрузчика со своими настройками TLS и своим затвором хоста; `nil` — прежнее поведение.
- `internal/ingest/sources/russialoppet` — адаптер: один GET календаря сезона, TLS 1.2 только с шифрами ECDHE-ECDSA (D-07), проверка сертификата стандартная; образец `events-2027.html` (51 строка), README с таблицей фактов, эталон 50 записей.
- `cmd/aggregator/main.go` — источник в `newSources`; `internal/bot/list.go` — подпись «Russialoppet» (бот и Mini App).
- `deploy/README.md` — раздел «Phase 23: Russialoppet».

## Проверки

`go vet ./...`, `gofmt -l` — чисто; полный прогон: 573 PASS (569 прежних + 4 новых), 0 FAIL, 0 SKIP. Эталоны экранов не менялись. Новых запросов к сайту не было (образец — загрузка проверки 2026-10-20).

## Отклонения от плана

- Тест TLS проверяет клиент с `TLSConfig` против двух тестовых серверов (RSA отклоняется, ECDSA принимается) и что `WithTLS` не меняет исходный загрузчик; запись в `sources` создаётся конвейером, как у Phase 18.
