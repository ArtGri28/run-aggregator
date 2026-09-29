---
phase: 01-source-discovery-vps-audit
verified: 2026-09-29T10:13:34Z
status: passed
score: 32/37 must-haves verified (4 roadmap SC + 33 plan truths; 1 via override, 5 uncertain)
covered_files:

  - .planning/REQUIREMENTS.md
  - .planning/phases/01-source-discovery-vps-audit/01-01-PLAN.md
  - .planning/phases/01-source-discovery-vps-audit/01-01-SUMMARY.md
  - .planning/phases/01-source-discovery-vps-audit/01-02-PLAN.md
  - .planning/phases/01-source-discovery-vps-audit/01-02-SUMMARY.md
  - .planning/phases/01-source-discovery-vps-audit/01-03-PLAN.md
  - .planning/phases/01-source-discovery-vps-audit/01-03-SUMMARY.md
  - .planning/phases/01-source-discovery-vps-audit/01-COMMANDS.md
  - .planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md
  - .planning/phases/01-source-discovery-vps-audit/01-VPS-AUDIT.md
  - .planning/phases/01-source-discovery-vps-audit/COVERAGE.md

covered_digest: "v1:sha256:a4b0e77c942b4222947e163a5cc3d146c57b32a02a214d3e7134c5ba58b0a2ad"
behavior_unverified: 0
overrides_applied: 1
overrides:

  - must_have: "Аудит VPS выполнен только на чтение, записан без секретов, и на VPS ничего не изменено"
    reason: "Read-only блоки фазы (vps_audit.sh, src_probe_*.sh) состояние VPS не меняли. Единственное изменение — создание пользователя runagg, узкого /etc/sudoers.d/run-agg на шесть read-only команд и строки authorized_keys с restrict — выполнено владельцем из собственной root-сессии как отдельно согласованный bootstrap механизма запуска (MVP §7.4 п. 1 и п. 4, решение 33) и честно задокументировано в 01-VPS-AUDIT.md §4 с подтверждением владельца от 2026-09-29."
    accepted_by: "владелец"
    accepted_at: "2026-09-29T00:00:00Z"
deferred:

  - truth: "G2 (антибот) по Марафонцу закрыт: происхождение 5 вхождений captcha на HTML-страницах установлено (view-source или отдельный read-only GET с контекстом)"
    addressed_in: "Phase 3"
    evidence: "Phase 3 SC1: «Парсер первого источника проходит golden-тесты на сохранённых файлах» — точка перепроверки, названная в 01-SOURCES-REPORT §8; риск принят владельцем в §4.2 и записан в STATE.md Blockers/Concerns. В тексте success criteria Phase 3 закрытие G2 явно не названо — рекомендуется добавить."
behavior_unverified_items: []
human_verification:

  - test: "Решить, принимается ли неполная проверка утверждённого источника №2 (ПроБЕГ) по MVP §7.2: п. 6 sitemap/RSS «не изучались», п. 4 API «не найден [ASSUMED]», п. 13 частота обновления «не изучена [ASSUMED]»"
    expected: "Владелец явно принимает эти пункты как допущения с переносом проверки на момент подключения ПроБЕГ (Phase 5, до написания парсера — MVP §7.3), либо заказывает gap-план фазы 1 на 1–2 read-only запроса (sitemap.xml/RSS ПроБЕГ)"
    why_human: "Пробел задокументирован исполнителем, но не принят владельцем явно (в отличие от G2 Марафонца и ask-first GET.RUN); это решение по объёму, а не технический факт"
  - test: "Убрать или запланировать удаление проектного доступа к VPS после Phase 1: строка authorized_keys с restrict для ключа run_agg_vps (MVP §7.4 п. 1 требует удаления после Phase 1), а также судьба пользователя runagg и /etc/sudoers.d/run-agg"
    expected: "Есть запись о выполненном удалении либо задача/требование с фазой (например, PRD-02 в Phase 6) и владельцем"
    why_human: "Обязательство из MVP §7.4 не отслеживается ни в ROADMAP, ни в REQUIREMENTS, ни в STATE.md; действие на VPS выполняет только владелец"
  - test: "Подтвердить, что сырые выводы прогонов 1 и 2 (src_probe_marathonec_out.txt, vps_audit_out.txt, src_probe_rest_out.txt, src_probe_getrun_out.txt) лежат только в ~/run-agg-phase1/, прошли redaction_check и owner_leak_check, и что блоки были прочитаны и утверждены (run_block.sh approve) до запуска"
    expected: "REDACTION OK и OWNER LEAK OK по всем четырём файлам; утверждение блоков по sha256 было до прогона"
    why_human: "Проверяющему запрещён доступ к ~/run-agg-phase1/ и VPS; со стороны репозитория подтверждено только, что *_out.txt и run-agg-phase1/ в .gitignore и не отслеживаются"
  - test: "Просмотреть judgment-tier запреты планов (обход CAPTCHA/антибота; сбор ПДн участников и IP клиентов tinyproxy; safety_lint не заменяет ручную проверку; исправления VPS только как предложения для Phase 6; tinyproxy не трогается)"
    expected: "Нарушений нет (предварительная оценка проверяющего: нарушений не найдено — правило 7 в 01-COMMANDS §0, одиночные GET без смены UA и повторов, в отчётах только агрегаты, все действия по VPS в 01-VPS-AUDIT §2 помечены «предложение», фаза 6, «нужно разрешение: да»)"
    why_human: "unverified-prohibition — human review recommended: judgment-tier запреты не имеют автоматического принуждения, вердикт проверяющего не является авторитетным"
---

# Phase 1: Source Discovery / VPS Audit — отчёт о проверке

**Цель фазы:** известно, какие 2–3 источника можно подключать, как получать из них данные и в каком состоянии VPS; решения зафиксированы до написания кода.
**Проверено:** 2026-09-29T10:13:34Z
**Статус:** human_needed
**Повторная проверка:** нет — первичная

## Достижение цели

### Критерии ROADMAP (контракт)

| # | Критерий | Статус | Доказательство |
|---|---|---|---|
| 1 | Для каждого кандидата есть проверка доступности с VPS, robots.txt, ToS, API/JSON/JSON-LD/sitemap/RSS/HTML, лимитов, антибот-защиты, полей и external_id | ? UNCERTAIN | Карточки §2.1–§2.4 `01-SOURCES-REPORT.md`: у каждой 16 пунктов MVP §7.2, подраздел «Проверка с VPS» с таблицей URL/код/размер/маркеры/индикаторы и строкой «Вердикт: …», подраздел external_id. Марафонец (3 URL, прогон 1), Runlist (5 URL), ПроБЕГ (3 URL), GET.RUN (только robots.txt — ask-first, принято владельцем). Пробел: у утверждённого источника №2 ПроБЕГ п. 6 sitemap/RSS «не изучались», п. 4 API «[ASSUMED]» — ступень sitemap/RSS в порядке предпочтений MVP §7.2 не проверена и явно владельцем не принята → решение владельца (human item 1) |
| 2 | Утверждён набор из 2–3 источников; неудобный или недоступный кандидат заменён, антибот не обходится | ✓ VERIFIED | §4.2: `approved_sources: Марафонец, ПроБЕГ`, `first_source_phase3: Марафонец`, дата 2026-09-29, «Решение принял: владелец», таблица решений с country_policy. Runlist — reject (addressCountry ненадёжен, ru_only_fallback запрещён), GET.RUN — reserve. Обход нигде не предлагается (§3, §4.2, §8; правило 7 в §0 `01-COMMANDS.md`; probe — один GET, без смены UA, без повторов, `--noproxy`). Открытый G2 Марафонца принят владельцем как риск и перенесён в Phase 3 (см. Deferred) |
| 3 | Аудит VPS (tinyproxy, часы, ufw, Docker и Compose, swap, порты) по подготовленным read-only командам, запуск обёрткой с ключом проекта после утверждения владельцем, записан без секретов, на VPS ничего не изменено | ✓ PASSED (override) | `vps_audit.sh` прочитан целиком: секции 00–11 только читают (systemctl show/is-active, ss, ufw status, iptables -S, timedatectl, docker --version/info/ps, free, df, apt list без update, sshd -T), последняя строка `main </dev/null 2>&1 \| rd`; safety_lint → SAFETY OK. `01-VPS-AUDIT.md` §1–§5: tinyproxy (Listen/Allow/BasicAuth/ConnectPort/ufw, вердикт «открытый прокси: нет» с разбором 4 условий), часы, ufw/iptables, Docker/Compose, swap, порты; таблица находок по всем секциям 00–11 с фазой 6 и «нужно разрешение: да». redaction_check → REDACTION OK. Запуск через `run_block.sh` зафиксирован в §1 и журнале §7. «Ничего не изменено» буквально не выполнено (bootstrap runagg/sudoers/authorized_keys) — принято владельцем, запись в §4 (override) |
| 4 | Итоговый отчёт содержит способ получения данных, external_id, частоту и число запросов для каждого источника | ✓ VERIFIED | §1 сводная таблица: все четыре колонки заполнены для всех четырёх кандидатов, формула `ceil(N/на странице)+детали`, `(запросов−1)×задержка`, сравнение с 30 мин; неизвестное N у GET.RUN помечено допущением A7. Частота обновления у Марафонца и ПроБЕГ — явные допущения [ASSUMED] (см. human item 1). §5 — решения для Phase 2/3 (форматы дат, задержки, external_id, country_code, HTML-парсер требует одобрения, ловушки, UA, входы normalize) |

### Must-haves планов (сводно)

| План | Истин | Проверено | Неопределено | Комментарий |
|---|---|---|---|---|
| 01-01 | 10 | 8 | 2 | Блоки rd.sh, probe_lib.sh, checks.sh, src_probe_marathonec.sh извлекаются `blk` и проходят `bash -n`; selftest → SELFTEST OK (включая границы 40/39 с, 30/29 с, `xargs -P`, хостнейм); getrun_guard_test → GUARD OK; getrun_gate → GETRUN GATE OK; карточка §2.1 с 16 пунктами и «Вердикт: челлендж». Неопределено: REDACTION OK по сырому `src_probe_marathonec_out.txt` (вне репозитория, доступ запрещён) и факт подтверждения прочтения владельцем |
| 01-02 | 11 | 9 | 2 | §4.1 `getrun_tos: ask-first`, дата, «Решение принял: владелец»; блок `src_probe_getrun.sh` существует и содержит только `dns get.run` и `robots.txt` с `GETRUN_ALLOW=robots`; `src_probe_rest.sh` без get.run, 3 запроса к probeg.org с паузами 30 с; vps_audit.sh без исходящих сетевых команд; §6 TopList и Russia Running + непроверенные кандидаты; §7 проверки исполнителя 1+2 запроса (≤3). Неопределено: redaction сырых выводов прогона 2 и подтверждение прочтения |
| 01-03 | 12 | 12 | 0 | `01-VPS-AUDIT.md` §1–§5 и таблица находок; вердикт tinyproxy с 4 условиями; «Вердикт: …» во всех карточках; §3 G1–G6 + «Рекомендация»; формула в §1; форматы дат в §5; §4.2; country_policy — один токен, у Runlist no_fallback; HTML-парсер требует одобрения; ru_only_fallback Марафонца помечен предварительным в §1, §2.1, §5; подтверждение владельца в §4; меток ожидания нет; REDACTION OK |

**Итог:** 32/37 проверено (1 через override), 5 неопределённых → human_needed. Истин с непроверенным поведением: 0 (поведенческие границы safety_lint и getrun_guard выполнены локально).

### Отложенные пункты

| # | Пункт | Где закрывается | Основание |
|---|---|---|---|
| 1 | G2 Марафонца: 5 вхождений `captcha` на HTML-страницах, view-source не проверялся, механический вердикт «челлендж» | Phase 3 | Phase 3 SC1 (golden-тесты на сохранённых файлах), 01-SOURCES-REPORT §8, STATE.md Blockers. Явного пункта о G2 в success criteria Phase 3 нет — рекомендуется добавить, иначе вопрос держится только на STATE.md |

## Артефакты

| Артефакт | Ожидается | Статус | Детали |
|---|---|---|---|
| `01-COMMANDS.md` | правила, блоки rd/probe_lib/checks/прогонные, команды прогонов | ✓ VERIFIED | 970 строк; 7 блоков, все `bash -n` OK; содержит `bash checks.sh`, `bash vps_audit.sh` |
| `01-SOURCES-REPORT.md` | §1–§8, карточки, решения §4.1/§4.2 | ✓ VERIFIED | 386 строк; содержит `### 2.1 Марафонец (marathonec.ru)`, `getrun_tos:`, `approved_sources:` |
| `01-VPS-AUDIT.md` | аудит DISC-03, tinyproxy MVP §12.3 | ✓ VERIFIED | 79 строк; содержит `## 3. tinyproxy (MVP §12.3)` |
| `COVERAGE.md` | декларация отсутствия внешних API-интеграций | ✓ VERIFIED | Утверждение точно: коммиты фазы 1 (22959da…eb1672a) меняют только `.planning/*`, `MVP.md`, `.gitignore`; кода, модулей, Dockerfile, миграций и .sh-файлов не добавлено. Go-код в репозитории появился в Phase 2 |

## Ключевые связи

| Откуда | Куда | Через | Статус |
|---|---|---|---|
| 01-COMMANDS.md | 01-SOURCES-REPORT.md | вывод src_probe_marathonec → §2.1 «Проверка с VPS» | ✓ WIRED |
| 01-SOURCES-REPORT.md §4.1 | 01-COMMANDS.md | getrun_tos = ask-first определяет блок src_probe_getrun.sh (GETRUN_ALLOW=robots); getrun_gate читает решение из отчёта | ✓ WIRED (GETRUN GATE OK на всех 4 прогонных блоках) |
| 01-COMMANDS.md | 01-SOURCES-REPORT.md / 01-VPS-AUDIT.md | $HOME/run-agg-phase1/*_out.txt → анализ | ✓ WIRED (по тексту; сами файлы не проверялись — запрещено) |
| 01-SOURCES-REPORT.md | REQUIREMENTS.md ING-11 | country_policy (ru_only_fallback предварительно) | ✓ WIRED |
| 01-VPS-AUDIT.md | ROADMAP Phase 6 (PRD-02, PRD-07) | предложения с фазой 6 и разрешением владельца | ✓ WIRED |

## Поведенческие проверки (локально, без сети и VPS)

| Поведение | Команда | Результат | Статус |
|---|---|---|---|
| Самопроверка проверок | `{ rd.sh; checks.sh; selftest } \| bash` | SELFTEST OK | ✓ PASS |
| Прогонные блоки только читают | `safety_lint` по rd.sh+probe_lib.sh+{src_probe_marathonec, vps_audit, src_probe_rest, src_probe_getrun}.sh | SAFETY OK ×4 | ✓ PASS |
| GET.RUN только в рамках ask-first | `getrun_gate 01-SOURCES-REPORT.md …` по 4 блокам | GETRUN GATE OK ×4 | ✓ PASS |
| probe/dns физически отказывают на get.run без разрешения | `getrun_guard_test probe_lib.sh` (заглушки curl/getent) | GUARD OK | ✓ PASS |
| Документы фазы без секретов | `redaction_check` по COMMANDS, SOURCES-REPORT, VPS-AUDIT | REDACTION OK | ✓ PASS |

## Проверка секретов в репозитории

Сканировались все отслеживаемые и неигнорируемые неотслеживаемые текстовые файлы (52 файла); значения совпадений не выводились.

| Категория | Результат |
|---|---|
| Публичные IPv4 (кроме 127.x, 0.0.0.0, 192.0.2/24, 198.51.100/24, 203.0.113/24) | не найдено |
| E-mail | 2 срабатывания — `01-COMMANDS.md` (фикстура selftest) и `01-02-SUMMARY.md` (описание фикса): оба являются именами шаблонных systemd-юнитов с суффиксом `.service`, не адреса |
| Приватные ключи | не найдено |
| Значения OWNER_CONTACT | не найдено: везде только плейсхолдер `<OWNER_CONTACT>` и ввод через `read` |
| Прочие срабатывания redaction_check | `01-01-PLAN.md:127` — описание фикстур selftest (документационные диапазоны IPv4/IPv6, пример присвоения ключа); `02-*` (Phase 2) — строки подключения к тестовой БД на 127.0.0.1/localhost, к фазе 1 не относятся |
| Сырые выводы | `*_out.txt` и `run-agg-phase1/` в `.gitignore`, в git не отслеживаются; .sh-файлов, local.conf, ключей в git нет |
| Сообщения коммитов фазы 1 | упоминаний инструментов и Co-Authored-By нет (0 совпадений); тегов нет |

## Покрытие требований

| Требование | План | Статус | Доказательство |
|---|---|---|---|
| DISC-01 | 01-01, 01-02, 01-03 | ? NEEDS HUMAN | как SC1; ПроБЕГ п. 6/13 — допущения |
| DISC-02 | 01-02, 01-03 | ✓ SATISFIED | §4.2, §3, §6 |
| DISC-03 | 01-02, 01-03 | ✓ SATISFIED (override по «никаких изменений») | vps_audit.sh, 01-VPS-AUDIT.md, §4 |
| DISC-04 | 01-01, 01-03 | ✓ SATISFIED | §1, §5; предварительный статус ru_only_fallback и перечень решений для Phase 3 |

Сиротских требований нет: в REQUIREMENTS.md на Phase 1 отображены только DISC-01..04.

## Найденные замечания

| Файл | Строка | Замечание | Серьёзность |
|---|---|---|---|
| 01-VPS-AUDIT.md | 7, 11, 68 | Неверно передан смысл MVP §19 решения 33: в тексте «решение 33 заменило решение 28: у инструментов нет SSH-доступа», тогда как решение 33 как раз делегирует инструментам запуск read-only блоков через обёртку (MVP §7.4). Фактический исполнитель (владелец через run_block.sh) записан верно, ошибка только в ссылке на решение. Также STATE.md [Init] «у инструментов нет SSH-доступа» устарело | ⚠️ Warning (точность записи) |
| MVP.md §7.4 п. 1 | — | «Открытый ключ … удаляет после Phase 1» — обязательство нигде не отслеживается | ⚠️ Warning (human item 2) |
| ROADMAP Phase 3 | — | Закрытие G2 Марафонца не отражено в success criteria Phase 3 | ⚠️ Warning |
| 01-SOURCES-REPORT.md | 229 | Сказано, что Runlist «остаётся в §6 как не рекомендуется», но в таблице §6 строки Runlist нет | ℹ️ Info |
| 01-SOURCES-REPORT.md | 212, 285 | Опечатки «мехачический», «лимit» (латиница) | ℹ️ Info |

Маркеров TBD/FIXME/XXX/TODO в документах фазы нет.

## Требуется проверка человеком

### 1. Неполная проверка ПроБЕГ (утверждённый источник №2)

**Проверка:** решить, принимаются ли п. 6 (sitemap/RSS «не изучались»), п. 4 (API [ASSUMED]) и п. 13 (частота обновления [ASSUMED]) карточки §2.4.
**Ожидается:** явное принятие с переносом на подключение ПроБЕГ в Phase 5 либо gap-план на 1–2 read-only запроса.
**Почему человек:** решение по объёму; пробел не принят владельцем явно.

### 2. Удаление проектного доступа к VPS после Phase 1

**Проверка:** удалить строку authorized_keys для ключа run_agg_vps (MVP §7.4 п. 1) и решить судьбу runagg и /etc/sudoers.d/run-agg, либо записать это как задачу с фазой.
**Ожидается:** запись о выполнении или отслеживаемая задача.
**Почему человек:** действие на VPS выполняет только владелец.

### 3. Сырые выводы и утверждение блоков

**Проверка:** подтвердить REDACTION OK и OWNER LEAK OK по четырём файлам в ~/run-agg-phase1/ и то, что блоки были утверждены (approve) до прогона.
**Ожидается:** все проверки зелёные, файлы только вне репозитория.
**Почему человек:** доступ проверяющего к этим файлам и VPS запрещён.

### 4. Judgment-tier запреты

**Проверка:** просмотреть запреты на обход антибота, сбор ПДн и IP клиентов tinyproxy, подмену ручной проверки safety_lint, изменения VPS и tinyproxy в этой фазе.
**Ожидается:** нарушений нет (предварительная, не авторитетная оценка проверяющего — нарушений не найдено).
**Почему человек:** автоматического принуждения для этих запретов нет.

## Итог

Цель фазы по существу достигнута: утверждён набор из двух источников (Марафонец — первый для Phase 3, ПроБЕГ — второй), способ получения данных, external_id, число запросов и ограничения зафиксированы, аудит VPS записан без секретов, а все прогонные блоки проходят локальные проверки безопасности. Блокирующих пробелов нет. Осознанно принятые владельцем пункты — G2 Марафонца (перенесён в Phase 3), ask-first для GET.RUN и bootstrap runagg (override) — пробелами не считаются. До перехода дальше нужно решение владельца по неполной проверке ПроБЕГ и по удалению ключа/runagg после Phase 1. Кроме того, стоит исправить ссылку на решение 33 в 01-VPS-AUDIT.md и добавить закрытие G2 Марафонца в критерии Phase 3.

---

_Проверено: 2026-09-29T10:13:34Z_
_Проверяющий: независимая проверка фазы_

## Acknowledged Gaps

Human verification closed by owner UAT on 2026-09-29 (`01-UAT.md`, 5/5 pass):

- ПроБЕГ card gaps (sitemap/RSS not studied; API and update frequency are assumptions) accepted by the owner; to be closed when ПроБЕГ is connected (Phase 5).
- Absence of secrets in the repository and read-only VPS audit confirmed by the owner (UAT tests 3–4).
- Open obligation, not a completion blocker: after Phase 1, remove the `run-agg-phase1` key line from `authorized_keys` on the VPS and decide on the `runagg` user and `/etc/sudoers.d/run-agg` (MVP.md §7.4 item 1). Requires a separate owner-approved VPS change.
- Minor document fixes pending: decision 33 wording in 01-VPS-AUDIT.md (lines 7, 11, 68) and STATE.md [Init]; stale §6 reference and two typos in 01-SOURCES-REPORT.md.

## Post-verification document update (2026-09-29)

Covered digest recomputed after wording-only changes to `01-VPS-AUDIT.md` made during the security audit (`01-SECURITY.md`): the decision 33 description was corrected (tools run owner-approved read-only blocks through the wrapper; they have no administrative access), and the owner's confirmation that run 2 blocks were read and approved before launch was recorded. No verified fact or verdict changed.
