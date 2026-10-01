---
phase: "05"
slug: "multiple-sources-deduplication"
status: verified
# threats_open = число открытых угроз уровня workflow.security_block_on (high) и выше
threats_open: 0
threats_open_below_threshold: 0
threats_total: 45
threats_closed: 45
asvs_level: 1
block_on: high
register_authored_at_plan_time: true
audited_head: 6d9efa3
created: "2026-10-01"
---

# Phase 05 — Security

> Контракт безопасности фазы: реестр угроз, принятые риски, журнал аудита.
> Аудит выполнен только чтением на HEAD `6d9efa3`; код, тесты, конфигурация и VPS не менялись. Релиз работает с ручным слиянием, `DEDUP_AUTO_MERGE=false` (D-23).

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| Внешний набор данных → репозиторий | `towns.csv` (Zenodo) → встроенный справочник | имена городов РФ, решают попадание в каталог |
| Сайт источника → `Parse`/`Prepare` | недоверенный HTML probeg.org и marathonec | названия, даты, место, страна, ссылки, `GroupKey` |
| `Parse` → каталог | страна и серии решают выдачу бота | `Country`, `GroupKey`, счётчики прогона |
| Источник → `source_events.fields`/`payload` | нормализованные поля определяют слитое событие | `fields` JSONB, `group_key` |
| Runner → шаг после применения | `PostApply` исполняет дедупликацию после записи | контекст, ошибка прохода |
| `dedup.Pass` → каталог | автоматическое решение меняет видимость событий | `merged_into_id`, `is_listed`, кандидаты |
| Окружение → `Config` | `DEDUP_*` задаёт пороги, автослияние, время сводки | значения env |
| Telegram-клиент → бот | `callback_data` (`e:`, `d:*`), команды, тип чата | id событий и кандидатов, tg id |
| Администратор → `Confirm/RejectCandidate` | ручное решение меняет каталог для всех | id кандидата, `decided_by` |
| БД → резолвер корня | цепочка `merged_into_id` без CHECK на цикл | id событий |
| Источник → карточка, экран кандидата, сводка | текст и URL источников уходят в Telegram | названия, города, ссылки |
| Bot API → бот | ответы 429 с произвольным `retry_after` | время ожидания |
| Исполнитель → probeg.org | запросы к чужому сайту в разработке и тестах | HTTP-запросы |
| Фикстуры → калибровочный тест | недоверенный HTML определяет вердикты | названия, даты, города |
| Метки владельца → решение об автослиянии | размеченный набор — основание включения | `label`, `proposed` |
| runagg → root (пакет в `/home/runagg/agg-deploy`) | compose и `deploy-verify.sh` исполняет root | файлы пакета, `SHA256SUMS` |
| Сайт ПроБЕГа → production-каталог | первый прогон применяется после принятия | записи прогона |
| Конфигурация → автослияние | одна строка `.env` включает необратимые слияния | `DEDUP_AUTO_MERGE` |
| Логи и сообщения бота → исполнитель, чат | вывод verify, алерты, санити-отчёт | строки логов без секретов |

---

## Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation | Status | Evidence |
|-----------|----------|-----------|----------|-------------|------------|--------|----------|
| T-05-01 | Tampering | `ru_cities.txt` из `towns.csv` | medium | mitigate | только Zenodo, закреплённые хеши, атрибуция, тест | closed | `internal/normalize/ru_cities.txt:1-6` (SHA-256 = `05-01-PLAN.md:261`); `ru_cities.go:11-36`; `TestIsRFCity` |
| T-05-02 | Spoofing | `IsRFCity`, одноимённые города | medium | mitigate | список неоднозначных → false; no_fallback; «не указана» в отчёте | closed | `ru_cities.go:33-35`; `probeg/parse.go:317`; `probeg/probeg.go:44`; `ingest/report.go:163-167,185-186` |
| T-05-03 | Tampering | сворачивание по `GroupKey` | medium | mitigate | только равный непустой ключ; `Collapsed` виден | closed | `ingest/prepare.go:102,136-138`; `runner.go:473`; `report.go:187`; `TestPrepareGroupKey`; лог worker `collapsed=752` |
| T-05-04 | DoS | `Runner.PostApply` | low | mitigate | после коммита, таймаут 5 мин, ошибка только WARN | closed | `ingest/runner.go:104,194-208,325`; `TestPostApply` |
| T-05-05 | Tampering | `Policy.OutOfScopeMax` | low | accept | принятый риск AR-05-01 | closed | `probeg/probeg.go:24`; `runner.go:171-174`; `TestRunOutOfScopeMax` |
| T-05-06 | Tampering | `Decide`: ложное автослияние | medium | mitigate | выключено по умолчанию; даты, город, единственный партнёр, High | closed | `dedup/config.go:47`; `decide.go:109,117-130`; `TestDefaultConfig`, `TestDecide`, `TestDecideDates`, `TestDecideSources` |
| T-05-07 | Spoofing | `LinkKey` | medium | mitigate | соцсети, пустой путь, `?`/`#` → ""; бонус только при NameSim ≥ Low | closed | `dedup/sim.go:65,72-92`; `decide.go:101-104`; `TestLinkKey`, `TestDecideLink` |
| T-05-08 | Tampering | `LoadConfig` | medium | mitigate | диапазоны, Low ≤ High, допуск ≤ 1000, ошибка без значения | closed | `dedup/config.go:85-113`; `TestLoadConfig`; `deploy/smoke.sh:123-135` |
| T-05-09 | Tampering | `Fold` | low | mitigate | первое непустое по приоритету, детерминизм | closed | `event/fold.go:41-50,73-101`; `TestFold`, `TestFoldDeterministic` |
| T-05-10 | Info Disclosure | `onCard` по старой/поддельной кнопке | medium | mitigate | `parseCallback`, `RootEventID`, скрытый корень → «недоступно» | closed | `bot/callback.go:179-192`; `bot/list.go:137-155`; `TestMergedCardOpensRoot`, `TestHiddenAndMerged` |
| T-05-11 | DoS | `RootEventID`, цикл | medium | mitigate | `maxMergeHops`, цикл → ошибка | closed | `store/listing.go:127,136-153`; `TestRootEventID` |
| T-05-12 | DoS | ожидание 429 в callback | low | mitigate | предел 5 с, без повтора сверх предела | closed | `bot/tg.go:70,105-116`; `TestCallbackRetryLimit` |
| T-05-13 | Info Disclosure | callback из группы | low | mitigate | пустой ответ, обработчик не вызывается | closed | `bot/bot.go:172-198`; `TestPrivateOnly` |
| T-05-14 | Tampering | ссылка и подпись ПроБЕГа в карточке | medium | mitigate | `validURL` + `html.EscapeString`, фиксированные подписи | closed | `bot/list.go:197,260-262,277-283`; `TestCardSourceNames` |
| T-05-15 | Tampering | `parse.go`: названия и ссылки | medium | mitigate | `clean`, `absURL`, `eventBase` | closed | `probeg/parse.go:27,191,409-428`; `parse_test.go:365-377` |
| T-05-16 | Spoofing | `resolvePlace`: иностранное событие в каталоге РФ | medium | mitigate | порядок определения страны, `IsRFCity` только для голого города, NoFallback | closed | `probeg/parse.go:299-320`; `TestParseRows`, `TestPreparedStats`; оговорка 4 |
| T-05-17 | DoS | `Fetch`/`Parse` | low | mitigate | один GET, предел тела | closed | `probeg/probeg.go:49-55`; `probeg_test.go:95-111`; `ingest/fetch.go:238-243` |
| T-05-18 | Tampering | `OutOfScopeMax` | low | mitigate | значение в коде, `TestPolicyGate` | closed | `probeg/probeg.go:24,44`; `prepared_test.go:132-158` |
| T-05-19 | Info Disclosure | фикстура в репозитории | low | mitigate | csrf → REDACTED, SHA, тестовый UA | closed | SHA-256 фикстуры = `probeg/testdata/README.md:13`; `value="REDACTED"` |
| T-05-20 | Repudiation | запросы исполнителя к probeg.org | low | mitigate | тесты только на httptest | closed | `probeg_test.go:97-103,118-124` |
| T-05-21 | Tampering | `Pass` → `MergeEvents` | high | mitigate | только при `AutoMerge` и вердикте с `UniquePartner`; отклонённые по корням | closed | `dedup/pass.go:82-85,95-123`; `TestTracerPass`, `TestPassRejected`, `TestPassSameSource`, `TestPassMaxPerEvent`; логи `dedup_auto_merge_*=false` |
| T-05-22 | Tampering | `MergeEvents`, потеря данных | high | mitigate | ничего не удаляется, одна точка записи | closed | `store/dedup.go:210-240`; `saveEventTx` в `events.go` и `dedup.go:237`; `TestMigration00004`, `TestMergeEvents` |
| T-05-23 | DoS | блокировки | medium | mitigate | одна advisory-блокировка, порядок по id | closed | `store/dedup.go:19,28-33,180-187`; `ingest/runner.go:337`; `TestMergeEventsConcurrent`, `TestParallelRuns` |
| T-05-24 | Repudiation | решения | low | mitigate | `decided_at`, `decided_by`, `match_state`; решённые не удаляются | closed | `migrations/00004_dedup.sql:7-8`; `store/dedup.go:148,246,253,419` |
| T-05-25 | Tampering | JSONB `fields`/`reasons` | low | mitigate | типизированный Marshal/Unmarshal, параметры `$N`, SQL только в store | closed | `store/events.go:370-420`; `store/dedup.go:124-132` |
| T-05-26 | EoP | `Confirm/RejectCandidate` без проверки прав | medium | transfer | перенос в 05-06 (`isAdmin`) | closed | вызовы только из `bot/dupes.go:60,77` после `isAdmin` (`:45`); `TestDupesNonAdmin` |
| T-05-27 | DoS | шум пар | low | mitigate | окно ±1 день, `MaxPerEvent`, ошибка не валит прогон | closed | `dedup/pass.go:47-76,133`; `TestPassFailureKeepsRun` |
| T-05-28 | EoP | `onDupe` от не-админа | high | mitigate | `isAdmin` первой строкой | closed | `bot/dupes.go:44-48`; `TestDupesNonAdmin` |
| T-05-29 | Info Disclosure | `/dupes` для не-админа | medium | mitigate | ответ как на неизвестную команду; `errNotAdmin` | closed | `bot/dupes.go:32-35,262-264`; `TestTracerDupes`, `TestSendDupesDigest` |
| T-05-30 | Tampering | callback `d:*` | medium | mitigate | строгий разбор, `encode` с обратным разбором | closed | `bot/callback.go:123-137,204-216,241-254`; `TestCallbackReject` |
| T-05-31 | Tampering | случайное/повторное слияние | high | mitigate | только `d:y` после подтверждения; атомарно; `ErrNotMergeable` | closed | `bot/dupes.go:55-77,98-99`; `store/dedup.go:361-389`; `TestDupesMerge`; оговорка 7 |
| T-05-32 | Tampering / Info Disclosure | текст источника в экране и сводке | medium | mitigate | обычный текст, `cut`, `validURL`, предпросмотр выключен | closed | `bot/dupes.go:189,208,210,220,265`; `bot/tg.go:163-166,206-209` |
| T-05-33 | Repudiation | решения администратора | low | mitigate | `decided_by`; в логе id кандидата и действие | closed | `store/dedup.go:385,411,419`; `bot/dupes.go:96`; `TestNoPersonalDataInLogs` |
| T-05-34 | DoS | `DigestLoop` | low | mitigate | раз в сутки, только при новых, отметка после успеха | closed | `dedup/digest.go:59-67,82-108`; `TestDigestDue`, `TestDigestTick` |
| T-05-35 | Tampering | `DEDUP_*` в окружении | low | mitigate | проверка до БД, по умолчанию выключено, права `.env` | closed | `dedup/config.go:95-107`; `cmd/aggregator/main.go:185-189,242-245`; `TestWorkerDedupConfigFirst`; `CHECK env-perms: OK` |
| T-05-36 | Tampering | `calibration.json`: метки под пороги | high | mitigate | ворота proposed=0, false_auto=0, missed=0 | closed | `TestCalibration`: `pairs=81 confirmed=81 proposed=0 false_auto=0 missed=0`; оговорка 3 |
| T-05-37 | Tampering | переобучение порогов | medium | mitigate | `AutoMerge=false` по умолчанию, пороги через env | closed | `dedup/config.go:47,71-78`; `TestDefaultConfig`, `TestCalibrationCheck` |
| T-05-38 | Info Disclosure | `note` в `calibration.json` | low | accept | принятый риск AR-05-02 (формулировка уточнена владельцем) | closed | `internal/dedup/testdata/calibration.json`: заметки a01–a32 содержат дистанции и ссылки регистрации, из них 3 с параметрами или якорем (строки 1172, 1223, 1240); контактов, e-mail и числа участников нет; файл и тесты не менялись |
| T-05-39 | Tampering | установка изменённых compose и `deploy-verify.sh` | high | mitigate | однострочник с проверкой SHA256, `STOP: HASH MISMATCH` | closed | `deploy/oneliner.sh:59`; `deploy/vps/agg-install.sh:26`; `deploy/ship.sh:58-66`; `CHECK root-owned: OK`; оговорка 6 |
| T-05-40 | Tampering | принятие первого прогона ПроБЕГа | high | mitigate | только suspect с одной причиной; сверка с фикстурой | closed | лог worker: прогон 4 `suspect`, причина «первый прогон», parsed 1312 (фикстура 1305), out_of_scope 156 (155), collapsed 752 (752); оговорка 5 |
| T-05-41 | Tampering | ложные автослияния | high | mitigate | выключено и проверяется; включение — отдельным решением | closed | `dedup/config.go:47`; `docker-compose.yml:39`; `dedup_auto_merge_bot/worker=false` в трёх логах; D-23; оговорки 1–2 |
| T-05-42 | Info Disclosure | токен, IP, контакт в логах | medium | mitigate | окружение не печатается, логи вне репозитория | closed | `deploy/vps/deploy-verify.sh:73-77`; скан трёх логов: IPv4 0, строк вида токена 0 |
| T-05-43 | Tampering | правка `/opt/aggregator/.env` | medium | mitigate | `test -s`, `grep -v` одного ключа, `mktemp`, `mv`; откат при сбое | closed | `deploy/README.md` (раздел «Автослияние»); `deploy/vps/deploy-apply.sh:38-50,67-69`; правка не выполнялась |
| T-05-44 | DoS | лишние запросы к источникам | low | mitigate | один GET за прогон, без повторов при 403/429/451 | closed | `probeg/probeg.go:49-55`; в логах у ПроБЕГа один прогон |
| T-05-45 | DoS | откат на образ Phase 4 после 00004 | low | mitigate | goose не требует новых версий, колонки NULL, порядок в README | closed | `go.mod` goose v3.28.0; `migrations/00004_dedup.sql:4-9`; `deploy/vps/rollback.sh:5-6` |

*Status: open · closed · open — below high threshold (non-blocking)*
*Severity: critical > high > medium > low — в `threats_open` входят только открытые угрозы уровня `workflow.security_block_on` (high) и выше*
*Disposition: mitigate (нужна реализация) · accept (задокументированный риск) · transfer (перенос)*

Пути в Evidence — от `internal/`, если не указано иное. Все названные тесты существуют и проходят на HEAD `6d9efa3` (локальная тестовая БД, `-p 1`).

---

## Оговорки по закрытым угрозам

1. **Автослияние (T-05-21, T-05-36, T-05-37, T-05-41).** Задача 6 плана 05-08 не выполнялась; контроль релиза — флаг остаётся выключенным (D-23). Шаги «включить → просмотреть `merged=` → выключить» на практике не проверялись. D-21 (`would_auto`) не реализован и остаётся обязательным условием включения (Phase 6). При включении флага T-05-41 проверяется заново.
2. **86 слияний (T-05-41).** `confirmed:86` = `events_merged=86` при `dedup_auto_merge=false`. `agg verify` не печатает разбивку auto/manual, поэтому «все слияния ручные» следует из состояния флага, а не из данных БД.
3. **T-05-36.** Ворота подтверждены прогоном теста. Проверка меток владельцем зафиксирована только документами. Набор построен на Марафонце и ПроБЕГе, пар `heroleague` нет (D-22). Тест не падает при появлении неразмеченной AUTO-пары (M2 ревью, Phase 6).
4. **T-05-16.** Коммит `e8005e8` (вне планов фазы) изменил меру: иностранный первый компонент с ISO-кодом даёт `Country` этой страны, каталог — `{RU, BY}` (`normalize/country.go:50`); инвариант теста ослаблен до «InScope ⇒ разрешённая страна». Подмены страны нет: RU ставится только по субъекту РФ или `IsRFCity`. Решение о поддержке RU и BY записано в `05-CONTEXT.md` (D-24).
5. **T-05-40.** Счётчики принятого прогона совпадают с фикстурой. Не сохранены вывод `sanity-report` и снимок состояния до принятия; проверки `DEPLOY P5 OK` и `ACCEPTED P5 OK` не запускались, логов `p5-*.log` нет.
6. **T-05-39.** Применение однострочника в root-консоли напрямую не зафиксировано; косвенно — `ship.sh` не передаёт образ до совпадения файлов, а в логе есть раздел «Дедупликация» (установлен `deploy-verify.sh` версии Phase 5).
7. **T-05-31.** Остаток L1 ревью: «Да, слить» пересчитывает корни сторон. При одном администраторе не проявляется; Phase 6.
8. **T-05-34.** Перезапуск бота и полная чистка отмеченных pending в тот же день может дать вторую сводку (05-06-SUMMARY).
9. **T-05-44, T-05-45.** Выполнялся ли `run-once` Марафонца, в логах не записано; откат в production не выполнялся, закрыто по коду и документации.
10. **Вне реестра.** Коммиты `e8005e8` и `6d9efa3` сделаны вне планов: источник `heroleague`, Беларусь в каталоге, callback фильтра страны, правки UX. Угрозы для них не формулировались.

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-05-01 | T-05-05 | Порог `OutOfScopeMax` задаётся кодом источника (не env), действует только в `Run` и только для этого источника (`TestRunOutOfScopeMax`); остальные проверки `Evaluate` (объём, отбраковка, рост пустых полей) не меняются | план 05-01 | 2026-10-01 |
| AR-05-02 | T-05-38 | В calibration.json заметки содержат название события, дату, город, дистанции и публичные ссылки регистрации, полученные из тестовых фикстур. Контакты, адреса электронной почты и сведения о количестве участников отсутствуют. Риск принят для текущей фазы | владелец | 2026-10-01 |

AR-05-02 заменяет формулировку принятия из плана 05-07 («только название, дата и город; ссылок с параметрами нет»), которая не соответствовала файлу.

Перенос: T-05-26 (проверка прав) перенесён из store в бот (05-06, T-05-28…35), проверка в коде есть.

---

## Security Audit 2026-10-01

| Metric | Count |
|--------|-------|
| Threats found | 45 |
| Closed | 45 |
| Open (below `high`, non-blocking) | 0 |
| Open at or above `high` | 0 |

По severity: high 8/8 закрыто, medium 17/17, low 20/20. Аудитор: gsd-security-auditor, ASVS L1, только чтение. Аудитор вернул T-05-38 открытой (low); закрыта принятием риска AR-05-02 владельцем 2026-10-01.
