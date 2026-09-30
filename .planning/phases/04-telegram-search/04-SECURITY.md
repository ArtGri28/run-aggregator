---
phase: "04"
slug: "telegram-search"
status: verified
# threats_open = count of OPEN threats at or above workflow.security_block_on severity (the blocking gate)
threats_open: 0
asvs_level: 1
created: "2026-09-30"
release: "aggregator:95f33a07c0c4"
---

# Phase 04 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| Telegram → бот | апдейты и callback_data от любого пользователя | недоверенный ввод |
| бот → PostgreSQL | фильтры и ввод города доходят до SQL | параметры запросов |
| бот → Telegram | тексты с данными источника, ссылки регистрации | недоверенный текст источника |
| процесс → логи | ошибки библиотеки, токен в URL Bot API | токен бота (secret) |
| администратор ↔ бот | `/runs`, алерты, принятие прогона | право менять каталог |
| Mac → VPS | образ, пакет скриптов, секреты | код, выполняемый root; токен |
| runagg → root (`sudo agg`) | единственная точка повышения прав для обычных шагов | подкоманды и TAG |
| root-консоль провайдера → хост | разовая установка и изменения хоста | код, выполняемый root |

---

## Threat Register

Статус — по состоянию на релиз `95f33a07c0c4` (коммит `95f33a0`); доказательство — тест (проходит: `go test ./...` 228 PASS), проверка скриптов или лог VPS (`$HOME/run-agg-phase4/`, вне репозитория).

| Threat ID | Category | Component | Severity | Disposition | Status | Evidence |
|-----------|----------|-----------|----------|-------------|--------|----------|
| T-04-01 | I | токен бота в ошибках, логах, выводе | high | mitigate | closed | `TestLoadConfig` (ошибка называет переменную без значения); `WithDebug` не используется; библиотека маскирует токен в URL сетевой ошибки; вторая линия — `redactHandler` в `logger()` (`TestLogRedactsToken`, в т. ч. настоящая сетевая ошибка) |
| T-04-02 | I | персональные данные в логах и БД | high | mitigate | closed | `TestNoPersonalDataInLogs`; `TestUsersColumns` (information_schema; в плане ошибочно назван `TestUserFilters`); VPS: `users_columns=created_at,filters,filters_v,last_seen_at,tg_id` |
| T-04-03 | D | паника обработчика | high | mitigate | closed | `recoverMW` первым; `TestCallbackPanicAnswered` |
| T-04-04 | T | подделанный или устаревший callback_data | medium | mitigate | closed | `TestCallbackReject`, `TestListStaleAndClamp`; приёмка C2 |
| T-04-05 | T | SQL-инъекция через фильтры | high | mitigate | closed | весь SQL в `internal/store`, параметры `$N`; ревью 04-REVIEW: «весь SQL параметризован, в `listWhere` склеиваются только константы» |
| T-04-06 | T | тесты очищают dev-БД | medium | mitigate | closed | `storetest.Open`/`exec`: имя базы должно оканчиваться на `_test` |
| T-04-07 | E | бот в группах | medium | mitigate | closed | `privateOnlyMW`; `TestPrivateOnly` (0 вызовов API на апдейты из групп) |
| T-04-08 | T | HTML-инъекция через эхо ввода и названия городов | high | mitigate | closed | `html.EscapeString`; `TestCityText`, `TestFilterLabels` |
| T-04-09 | T | инъекция в LIKE | high | mitigate | closed | `SimilarCities`: алфавит `[a-z0-9-]` и длина до запроса; `TestSimilarCities` (`a%b`) |
| T-04-10 | D | длинный city_key ломает клавиатуру | medium | mitigate | closed | кнопка без кодируемого callback пропускается; остаток — LO-01 (ключи 61–64 байта недоступны), Phase 5 |
| T-04-11 | T | гонка быстрых нажатий | medium | mitigate | closed | `UpdateFilters` с блокировкой строки; `TestUserFiltersConcurrent` |
| T-04-12 | D | очень длинный ввод | low | mitigate | closed | `maxCityInput = 64` (`filters.go:463`), текст в лог не пишется |
| T-04-13 | T | ссылка регистрации `javascript:`/`data:` | high | mitigate | closed | `validURL` (только http/https с хостом, ≤ 2048); `TestValidURL`, `TestCardEscapingAndURLs` |
| T-04-14 | T | HTML-инъекция через данные события | high | mitigate | closed | `TestCardEscapingAndURLs`, `TestLongTexts` |
| T-04-15 | D | ошибки Telegram API роняют обработчик | high | mitigate | closed | `classify`, повтор 429, «not modified»; `TestClassify`, `TestSendBlocked`, `TestRateLimitRetry`, `TestEditNotModified`, `TestURLButtonFallback`; VPS: при 400 `chat not found` бот работал; пустая клавиатура → 400 исправлено (`TestRunsWithoutButtons`); остаток — LO-03, Phase 5 |
| T-04-16 | I | карточка скрытого или слитого события | medium | mitigate | closed | `TestHiddenAndMerged` («Событие больше не доступно») |
| T-04-17 | D | «часики» при ошибке ответа | low | mitigate | closed | `TestAnswerOncePerCallback`; остаток — IN-04 (callback из не-личного чата без ответа), Phase 5 |
| T-04-18 | I | алерты посторонним | high | mitigate | closed | `Loop.Admins`; `TestNoAdmins`, `TestTick` (адресаты), `TestSendAlert` (не-админу — ошибка, 0 вызовов API) |
| T-04-19 | D | спам алертами | medium | mitigate | closed | `TestDecide`, `TestTick`; LO-02: новый алерт только на новый suspect-прогон, для failed — суточный ритм (`TestTickNewSuspectRun`) |
| T-04-20 | R | потерянный алерт | medium | mitigate | closed | `TestTickSendFailure`; VPS: до `/start` владельца попытка повторялась каждую минуту, после — алерт доставлен |
| T-04-21 | T | разметка в причинах прогона | low | mitigate | closed | алерт без `parse_mode` (`TestSendAlert`) |
| T-04-22 | E | не-админ вызывает `/runs` или `r:*` | high | mitigate | closed | `isAdmin` в каждом админ-обработчике; `TestRunsNonAdmin`, `TestAcceptNonAdmin`; приёмка C5 со второго аккаунта |
| T-04-23 | I | раскрытие админ-команд | medium | mitigate | closed | не-админу `/runs` отвечает как неизвестная команда (`TestRunsNonAdmin`; C5) |
| T-04-24 | T | двойное или гоночное принятие | high | mitigate | closed | `Runner.Accept` с `ClaimRun`, подтверждение; `TestAcceptFlow`, `TestAcceptRefusals`, `TestAcceptCancel` |
| T-04-25 | T | принятие с иными параметрами | medium | mitigate | closed | общий `newRunner`; `TestNewRunner` |
| T-04-26 | I | токен в выводе при ошибке запуска | high | mitigate | closed | ошибки конфигурации без значений; `New` маскирует токен в ошибке |
| T-04-27 | I | `.env` в слое образа | high | mitigate | closed | узкий COPY, `.dockerignore`; `SMOKE OK` проверяет список файлов образа |
| T-04-28 | I | PostgreSQL доступна извне | high | mitigate | closed | нет `ports` в compose; `SMOKE OK`; VPS: `CHECK port-not-published: OK`, `CHECK port-not-listening: OK` |
| T-04-29 | E | контейнеры с root и возможностями | medium | mitigate | closed | distroless nonroot, `user: "65532:65532"` в compose, `read_only`, `cap_drop: ALL`, `no-new-privileges` |
| T-04-30 | T | подмена скрипта на пути к VPS | high | mitigate | closed | однострочник с SHA256 `SHA256SUMS` на копии в каталоге root; негативные тесты в контейнере Ubuntu 24.04 (подмена файла, подмена `SHA256SUMS`, symlink → `STOP: HASH MISMATCH`) |
| T-04-31 | I | секреты в выводе скриптов | high | mitigate | closed | `deploy/secrets.sh` (`read -rs`, stdin ssh), `env-init.sh` (umask 077, printf, только имена ключей); `config -q`, `inspect --format`; логи VPS проверены: 0 строк, похожих на токен |
| T-04-32 | I | токен в чужих контейнерах | medium | mitigate | closed | переменные по сервисам, токен только у `bot` (`docker-compose.yml`) |
| T-04-33 | D | потеря БД удалением томов | high | mitigate | closed | в `deploy/vps` нет `down -v`; откат — `IMAGE_TAG`; LO-04: неудачный деплой не теряет прежний тег (`deploy/deploy-apply-test.sh`) |
| T-04-34 | D | перезагрузка и обновления задевают чужие сервисы | high | mitigate | closed | host-preflight (amnezia-awg2, tinyproxy видны); установлен только `docker-compose-v2` после `apt-get -s`; перезагрузка отложена владельцем |
| T-04-35 | I | IP или имя хоста в репозитории | medium | mitigate | closed | проверка IPv4 в `deploy`, compose, Dockerfile (`DEPLOY TOOLING OK`); в диффах фазы IP нет |
| T-04-36 | D | обновления и перезагрузка останавливают чужие сервисы | high | mitigate | closed | см. T-04-34; решение владельца в 04-07-SUMMARY |
| T-04-37 | I | токен и ID в чате, истории, логе | high | mitigate | closed | ввод только в `secrets.sh`; в переписке и логах токена нет (проверено сканированием логов) |
| T-04-38 | I | IP и имя хоста в логах | medium | mitigate | closed | 4 лога `deploy-*`: 0 IPv4 кроме 127.0.0.1/0.0.0.0; каталог логов 700 вне репозитория |
| T-04-39 | T | выполнение изменённого скрипта | high | mitigate | closed | см. T-04-30; `agg-install.sh` отказывается работать не из каталога root 700 (тест в контейнере); `ship.sh` сверяет SHA256 установленного agg и скриптов (тест: устаревший agg → STOP) |
| T-04-40 | D | два экземпляра бота с одним токеном | medium | mitigate | closed | инструкция README; тесты — фиктивный Bot API; в сохранённых хвостах логов бота (по 15 строк) нет `Conflict` — доказательство ограниченное |
| T-04-41 | T | принятие прогона с аномальными данными | high | mitigate | closed | принят прогон 1 только с причиной «первый прогон источника»; до принятия снимок сверён с эталоном Phase 3 (133 + 1 новая запись, 0 изменённых, дистанции 451/133/115/10/1) — 04-08-SUMMARY |
| T-04-42 | S | блокировка сайтом при первом прогоне | medium | mitigate | closed | прогон 1: `fetch done docs=1 bytes=599032`, 403/429/451 не было |
| T-04-43 | E | админ-функции у постороннего | high | mitigate | closed | C5 со второго аккаунта; `TestRunsNonAdmin`, `TestAcceptNonAdmin` |
| T-04-44 | I | секреты и адреса в логах приёмки | medium | mitigate | closed | см. T-04-38; сканирование `deploy-verify-accepted.log`, `deploy-verify-final.log`, `deploy-verify-3.log` |
| T-04-45 | E | runagg (или его ключ) получает root через sudo | high | mitigate | closed | sudoers только `agg` с подкомандами; `agg`: `env -i` + `--clean-env` в argv, `cd /`, `python3 -I`, строгий TAG; `agg-install.sh`: `env_reset` без `env_keep`/`setenv`, runagg не в docker, каталоги только root. Security review границы sudo в контейнере Ubuntu 24.04: 78 значений TAG, 14 значений `env-init`, враждебное окружение, symlink/hardlink, `-E`/`-D`/`VAR=` — отказ, следов выполнения нет; владелец: «SECURITY BOUNDARY OK». VPS: `CHECK root-owned: OK`, `CHECK runagg-not-in-docker: OK` |
| T-04-46 | T | подменённый архив через `agg load` | high | mitigate | closed | проверка архива до `docker load` (чужой тег, два образа, `..`, symlink, чужое имя в `index.json` — отказ, образ не появился; тест в контейнере); запасной путь `postgres:17` — `load-postgres.sh` со сверкой SHA256 копии (ME-01, `deploy/load-postgres-test.sh`) |
| T-04-47 | E | вредный образ `aggregator:<TAG>` от runagg | medium | accept | accepted | см. Accepted Risks Log |

*Категории STRIDE: S — Spoofing, T — Tampering, R — Repudiation, I — Information Disclosure, D — Denial of Service, E — Elevation of Privilege.*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-04-01 | T-04-47 | Украденный ключ runagg позволяет загрузить и запустить свой `aggregator:<TAG>`. Контейнер работает по root-owned compose-файлу: пользователь 65532 (задан в compose, USER образа не действует), `cap_drop: ALL`, `read_only`, `no-new-privileges`, без каталогов хоста и портов. Худший случай — код с доступом к БД проекта и токену бота, не root хоста. Пересмотр роли runagg и его ключа — Phase 6. | владелец | 2026-09-30 |
| AR-04-02 | T-04-45 | `BASH_ENV`, `LD_PRELOAD` и т. п. действуют на `/bin/bash` раньше первой строки `agg`; защита — `env_reset` sudo (удаляет их), проверяется `agg-install.sh` при установке. Если настройки sudo на VPS позже изменят, защита держится только на повторной установке. | владелец | 2026-09-30 |
| AR-04-03 | — | Перезагрузка VPS на новое ядро (`reboot-required: yes`) и синхронизация часов (`NTPSynchronized=no`) отложены: хост общий (amnezia-awg2, tinyproxy), security-обновлений 0. Не позже Phase 6. | владелец | 2026-09-30 |

### Known residual items (not open threats; tracked in 04-REVIEW.md «Deferred»)

- LO-01 (ключи городов 61–64 байта), LO-03 (ожидание 429 до ответа на callback), LO-05 (фильтр по прошедшему месяцу), IN-01…IN-04, IN-08 — Phase 5.
- LO-06 (healthcheck бота не проверяет Telegram), IN-05 (образы не закреплены по digest), IN-06 (остатки однострочника в `/tmp`), IN-07 (остановка бота не ждёт обработчиков) — Phase 6.

---

## Security Audit Trail

| Date | Activity | Result |
|------|----------|--------|
| 2026-09-30 | Модели угроз планов 04-01…04-08 | 47 угроз; T-04-45…47 перенумерованы (при смене схемы доступа совпали с номерами 04-08) |
| 2026-09-30 | Security review границы sudo вокруг `/usr/local/sbin/agg` (контейнер Ubuntu 24.04, sudo 1.9.15) | найдено и исправлено: окружение и cwd вызывающего в `agg load`, подделываемый признак очистки, `python3` без `-I`, USER образа; итог «SECURITY BOUNDARY OK» (владелец) |
| 2026-09-30 | Code review фазы (04-REVIEW.md) | critical 0, high 0, medium 1 (ME-01), low 6, info 8; ME-01, LO-02, LO-04 исправлены (`95f33a0`) |
| 2026-09-30 | Сканирование логов VPS и диффов фазы | IPv4 (кроме 127.0.0.1/0.0.0.0) — 0, строк вида токена — 0 |
| 2026-09-30 | Post-deploy verify релиза `95f33a07c0c4` | `DEPLOY VERIFY OK`, все CHECK (порт, права, root-owned, runagg-not-in-docker) — OK |

---

## Sign-Off

- [x] Все угрозы из моделей планов 04-01…04-08 имеют disposition и доказательство
- [x] Открытых угроз уровня high и выше нет (`threats_open: 0`)
- [x] Принятые риски записаны в Accepted Risks Log
- [x] Отложенные замечания ревью перечислены и привязаны к фазам

**Approval:** verified 2026-09-30
