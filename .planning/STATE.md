---
gsd_state_version: "1.0"
milestone: v1.27.0
current_phase: 5
current_phase_name: Multiple Sources & Deduplication
status: planning
stopped_at: Phase 5 context gathered
last_updated: "2026-09-30T19:07:03.528Z"
last_activity: 2026-09-30
last_activity_desc: Phase 4 complete, transitioned to Phase 5
state_head: c10feb201cbced50cb208fe67291cc66230fe162
progress:
  total_phases: 6
  completed_phases: 4
  total_plans: 20
  completed_plans: 20
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-09-25)

**Core value:** Полный, актуальный и без дублей каталог забегов
**Current focus:** Phase 4 — Telegram Search

## Current Position

Phase: 5 — Multiple Sources & Deduplication
Plan: Not started
Status: Ready to plan
Last activity: 2026-09-30 — Phase 4 complete, transitioned to Phase 5

Progress: [███████░░░] 67%

## Performance Metrics

**Velocity:**

- Total plans completed: 20
- Average duration: -
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 2 | 2 | - | - |
| 1 | 3 | - | - |
| 3 | 7 | - | - |
| 4 | 8 | - | - |

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*
**Per-Plan Metrics:**

| Plan | Duration | Tasks | Files |
|------|----------|-------|-------|
| Phase 01 P01 | n/a | 3 tasks | 2 files |
| Phase 01 P02 | n/a (ожидание владельца между сессиями) | 3 tasks | 2 files |
| Phase 01 P03 | не измерялась | 3 tasks | 3 files |
| Phase 02 P01 | ~50min | 2 tasks | 11 files |
| Phase 02 P02 | ~25min | 2 tasks | 6 files |
| Phase 03 P01 | ~7min (+ действие владельца) | 2 tasks | 2 files |
| Phase 03 P02 | ~15min | 2 tasks | 14 files |
| Phase 03 P03 | ~14min | 2 tasks | 14 files |
| Phase 03 P04 | ~13min | 2 tasks | 8 files |
| Phase 03 P05 | 35min | 3 tasks | 18 files |
| Phase 03 P07 | ~20min (+ действия владельца) | 2 tasks | 4 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table (full log in MVP.md, section 19).
Recent decisions affecting current work:

- [Init]: Phase 1 (discovery) обязательна перед разработкой; соответствует логическому Phase 0 из MVP.md
- [Init]: Источники не утверждены; цель — 2–3 качественных, неудобный источник заменяется
- [Init]: Системные изменения на VPS (обновление, перезагрузка, пакеты, swap, tinyproxy) только с отдельного разрешения владельца
- [Init]: Политика Git: commit_docs = true; GSD и исполнитель коммитят самостоятельно (не только .planning/); обычный push только в remote проекта; force-push, reset --hard, удаление веток и remote запрещены без подтверждения; теги отключены; в сообщениях коммитов нет упоминаний ИИ
- [Init]: ~~У инструментов нет SSH-доступа к VPS~~ заменено решением 33 (MVP §7.4): инструменты запускают только утверждённые владельцем read-only блоки через обёртку с отдельным ключом и пользователем `runagg`; административные действия — только владелец; Telegram-алерты админу с Phase 4
- [Phase 01]: GET.RUN: getrun_tos = ask-first (§4.1), решение владельца от 2026-09-26; с VPS в этой фазе только dns get.run и robots.txt
- [Phase 01]: Runlist: addressCountry в JSON-LD не показывает реальную страну события (иностранное событие 48IstanbulMarathon отдало RU) — вариант правила «страна из JSON-LD» ненадёжен, решение по country_policy остаётся за планом 01-03
- [Phase 01]: tinyproxy: Allow ограничен 127.0.0.1/::1 (не открытый прокси по критерию плана), но порт 8888 слушает на 0.0.0.0/[::] при неактивном ufw — единственный барьер сетевого уровня отсутствует, судьба tinyproxy решается в Phase 6 (PRD-07)
- [Phase 01]: DISC-02 — утверждён набор Марафонец (первый источник Phase 3) + ПроБЕГ; Runlist и GET.RUN не вошли (2026-09-29)
- [Phase 01]: Марафонец включён в набор без подтверждения открытого вопроса G2 (captcha на HTML-страницах) — владелец принял это как риск, закрыть до/во время Phase 3
- [Phase 01]: tinyproxy — вердикт «не открытый прокси» (Allow ограничивает доступ), но без сетевого барьера при неактивном ufw; закрытие — Phase 6 (PRD-07) с разрешения владельца
- [Phase 02]: [Phase 2/01]: все суррогатные ключи BIGINT (включая sources.id); только 2 частичных idx_events_* индекса из MVP.md §4.6 (без лишнего idx_events_start_date_id из исследования)
- [Phase 02]: [Phase 2/01]: коммиты делает владелец вручную (git commit исполнителя отклоняется системой разрешений); код 02-01/02-02 закоммичен владельцем (31152a0…8db6ff4, исправления ревью b369701)
- [Phase 02]: [Phase 2/02]: TitleNorm повторяет шаг до неподвижной точки (идемпотентность при «10 - км»); Distances отбрасывает значения > MaxInt32 (distance_m INTEGER); словарь городов — только moscow и saint-petersburg
- [Phase 3]: [Phase 03/01]: G2 закрыт — captcha ×5 на /calendar-beg/ это CSS формы комментариев темы Newsmag (×4) и var tds_captcha="" (×1), шаблонный элемент, не челлендж; 133 строки post-row, HTTP 200
- [Phase 3]: [Phase 03/01]: область в строках Марафонца — слаг таксономии oblast (русские названия только в data-filters таблицы); у 105 из 133 строк ссылка названия внешняя (регистрация), страницы события на сайте в строке нет — решить в 03-02
- [Phase 3]: [03-02] D-13: только ссылка названия; marathonec.ru → URL события, внешняя → registration_url, URL = страница календаря; #new_tab срезается
- [Phase 3]: [03-02] D-14: SourceEvent.RegionSlugs/Regions (все области строки), Region = название из data-filters при одной области, пусто при нескольких; нет словаря областей → ErrStructureNotFound
- [Phase 3]: [03-03] D-14: составные «Москва и Московская область»/«СПБ и Ленинградская область» — синонимы областей, ClassifyPlace уточняет до города федерального значения по городу; многообластные записи — Stats.MultiRegion, вне порога D-09
- [Phase 3]: [03-03] Distances распознаёт мили (реальная строка фикстуры 68272); K/К слитно, склейка 100 200 400 м, MaxPlausibleDistanceM = 1000 км
- [Phase 3]: [03-04] StartRun получил startedAt: started_at = часы Runner.Now, Accept повторяет Prepare с этим моментом (детерминизм тестов, точное совпадение Run/Accept)
- [Phase 3]: [03-04] Окно базы — ok с id ≥ последнего принятого (≤ 7); снимок suspect в ingest_runs.snapshot живёт до принятия или следующего завершённого прогона
- [Phase 3]: [03-04] Observe: gone-записи больше не трогаются; Missing = ненайденные и оставшиеся missing; events.status наблюдением не меняется
- [Phase 3]: [03-05] RawDoc.URL — запрошенный адрес (CalendarURL), не адрес после редиректа: payload стабилен (D-13)
- [Phase 3]: [03-05] параметры Fetcher читаются из окружения (FETCH_*), как требует ING-01
- [Phase 3]: [03-05] санити-отчёт выделяет записи с несколькими областями отдельной строкой
- [Phase 3]: [03-07] Марафонец: CountryFallback = RUOnlyFallback по решению владельца 2026-09-29 (прогон 5, D-07/D-15); новых написаний областей не потребовалось
- [Phase 3]: [03-07] Базовый уровень Марафонца принят (прогон 5, created 133); второй реальный прогон 6 ok: created 0, changed 0, unchanged 133

### Pending Todos

- [Phase 1 cleanup, не позже Phase 6, только с отдельного разрешения владельца]: удалить строку `run-agg-phase1` из `authorized_keys` на VPS, решить судьбу пользователя `runagg` и `/etc/sudoers.d/run-agg`, удалить локальные `~/.ssh/run_agg_*`, `~/run-agg-phase1/local.conf` и `approved.sha256` (MVP §7.4 п.1; 01-SECURITY.md T-01-07)
- [Phase 3]: отложенные находки ревью Phase 2 — ME-04 «Russian Federation», ME-05 `5K`/`10K`, правило римских цифр («MIX RUN»), проверка правдоподобия дистанции в Validate (склейка «100 200 400 м»), low-находки 02-REVIEW.md
- [Phase 5+]: часовой пояс по субъекту РФ (сейчас timezone пуст у 118 из 133 событий Марафонца; известны только Москва и СПб) — справочник «субъект → IANA-зона» в normalize/regions.go

### Blockers/Concerns

- [Git]: remote ещё не настроен, push невозможен до настройки владельцем
- ~~[Phase 1 → Phase 3]: у утверждённого источника Марафонец на HTML-страницах с VPS слово captcha x5 (механический вердикт: челлендж), источник не установлен~~ — ЗАКРЫТО 2026-09-29 планом 03-01: один GET /calendar-beg/ с Mac владельца дал HTTP 200 и 133 строки post-row; все 5 вхождений captcha — шаблон темы Newsmag (4 селектора `.td-warning-captcha*` формы комментариев в `<style id="wpr-usedcss">` и `var tds_captcha="";` в настройках темы), челленджа нет (internal/ingest/sources/marathonec/testdata/README.md, раздел «captcha (D-01)»)
- [Phase 1, справочно, не блокирует]: Runlist (не утверждён) /event/skazka26 с VPS 2026-09-28 весит ~30% от baseline 2026-09-25 при точном совпадении структурных маркеров — G2 не закрыт, причина не установлена; не блокирует Phase 3, т.к. Runlist не входит в утверждённый набор (01-SOURCES-REPORT §2.3, §4.2)
- [Phase 6]: VPS — reboot-required (новое ядро), 24 обновления apt (3 security), unattended-upgrades включён; SSH permitrootlogin=yes и passwordauthentication=yes; docker compose plugin не установлен; tinyproxy не открытый прокси по критерию плана, но без сетевого барьера на порт 8888 при неактивном ufw — все находки требуют отдельного разрешения владельца (01-VPS-AUDIT.md, PRD-02/PRD-07)

## Deferred Items

Items acknowledged and deferred at milestone close, most recent first:

| Category | Item | Status | Deferred At | Milestone |
|----------|------|--------|-------------|-----------|
| *(none)* | | | | |

## Session Continuity

Last session: 2026-09-30T19:07:03.431Z
Stopped at: Phase 5 context gathered
Resume file: .planning/phases/05-multiple-sources-deduplication/05-CONTEXT.md
