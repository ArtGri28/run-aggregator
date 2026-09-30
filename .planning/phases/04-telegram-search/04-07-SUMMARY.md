---
phase: 04-telegram-search
plan: 07
subsystem: deploy
tags: [vps, deploy, sudo, runagg, compose, sha256]

requires:
  - phase: 04-telegram-search
    provides: "04-06: образ, docker-compose.yml, deploy-скрипты"
provides:
  - "Проект aggregator работает на VPS (postgres healthy, migrate 0, bot healthy, worker up), образ доставлен с Mac"
  - "Схема доступа без root по ssh: runagg + sudo только /usr/local/sbin/agg (обёртка с проверкой подкоманд, TAG и архива образа)"
  - "Разовая установка agg-install.sh из root-консоли провайдера с проверкой SHA256 пакета"
affects: [04-08, 06]

plan_head_before: 14d3295
commits: "владелец вручную: 95b667f, 6cf681e, be760a7, 07e8911"

key-files:
  created:
    - deploy/vps/agg
    - deploy/vps/agg-install.sh
    - deploy/vps/env-init.sh
    - deploy/secrets.sh
    - deploy/confirm-test.sh
  modified:
    - deploy/ship.sh
    - deploy/oneliner.sh
    - deploy/README.md
    - deploy/vps/deploy-apply.sh
    - deploy/vps/deploy-preflight.sh
    - deploy/vps/deploy-verify.sh
    - deploy/vps/rollback.sh
    - deploy/vps/host-apply.sh
    - docker-compose.yml
    - .gitignore
---

# 04-07: развёртывание на VPS — итог

## Accomplishments

- Проект `aggregator` развёрнут на VPS и здоров: `DEPLOY VERIFY OK`, все CHECK — OK (compose-state, порт 5432 не опубликован и не слушается, `.env` 600, root-owned, runagg не в группе docker).
- Образ доставлен с Mac (`docker save | gzip | ssh … 'gunzip | sudo -n /usr/local/sbin/agg load <TAG>'`), `linux/amd64`, на VPS нет исходников и Go.
- Секреты введены на Mac (`deploy/secrets.sh`, ввод скрыт) и переданы через stdin ssh; в чат, логи и репозиторий не попадали.

## Изменение схемы (решение владельца 2026-09-30)

Root по ssh сервер не принимает; у runagg был только узкий sudo Phase 1. Вместо root по ssh:
- root — только в консоли провайдера: разовая установка `agg-install.sh` и изменение хоста (Compose);
- runagg может через sudo только `/usr/local/sbin/agg` с перечисленными подкомандами (`/etc/sudoers.d/runagg-aggregator`), sudoers Phase 1 не изменён;
- пакет — `/home/runagg/agg-deploy`, `/root/agg-deploy` не используется;
- security review границы sudo (владелец подтвердил «SECURITY BOUNDARY OK»): чистое окружение (`env -i`, признак `--clean-env` в argv), `cd /`, `python3 -I`, проверка `env_reset` при установке, `user: "65532:65532"` в compose, проверка архива ровно `aggregator:<TAG>` до `docker load`; негативные тесты в контейнере Ubuntu 24.04.

## Решение по хосту (задача 2)

- Установлен только `docker-compose-v2` 2.40.3 (Ubuntu noble-updates/universe) через `host-apply.sh compose` в root-консоли, после проверки `apt-get -s` (ставится один пакет).
- `host-apply.sh updates` не выполнялся: security-обновлений 0 (host-preflight). Перезагрузка отложена: `reboot-required: yes` из-за нового ядра 6.8.0-142 (работает 139) — не позже Phase 6.
- Решение записано по ходу переписки; дословная формулировка владельца — «compose-only» по смыслу, подтвердить при приёмке фазы.

## Факты для acceptance

- IMAGE_TAG: первый деплой `07e8911842ef`, итоговый `a4177a83eb8a` (IMAGE_PREV — `07e8911842ef`).
- `pull postgres` прошёл (перенос с Mac не понадобился); OCI-архив принят через `agg load`.
- Host-preflight: Ubuntu 24.04.4, docker.io 29.1.3, containerd 2.2.1, контейнер хоста `amnezia-awg2` (restart=always) и tinyproxy не затронуты; `NTPSynchronized=no` — часы в Phase 6.

## Deviations from Plan

1. Схема доступа изменена (см. выше); задачи 1 и 3 плана переписаны (`be760a7`).
2. Подтверждение словом в root-консоли не принималось: консоль присылает CR вместе с Enter — `ANS=${ANS//$'\r'/}` и вывод полученного значения через `%q` (`07e8911`, тест `deploy/confirm-test.sh`).
3. `.gitignore`: `.DS_Store`, `.idea/`, `.planning/intel/`, `.planning/milestone.lock` (`95b667f`) — иначе ship.sh останавливался на `internal/.DS_Store`.
4. `ship.sh`/`oneliner.sh`: отдельный ssh-конфиг через `SSH_CONFIG` (`6cf681e`).
5. Логи: сохранены `host-preflight.log`, `deploy-preflight-2.log`, `deploy-verify-2.log`, `deploy-verify-accepted.log`, `deploy-verify-final.log`; `host-verify.log` и первые `deploy-preflight/verify.log` не сохранялись — результаты переданы владельцем в переписке. Все сохранённые логи без IP и токенов.

## Issues Encountered

- `alert send failed` (`*fmt.wrapError`) каждую минуту до первого `/start`: Telegram `400 chat not found` — бот не может писать пользователю, который его не запускал. После `/start` алерт пришёл.
