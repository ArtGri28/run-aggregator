---
phase: 06-production
plan: 07
subsystem: deploy
tags: [host, swap, ufw, ssh, tinyproxy, reboot, cleanup]
status: in-progress
progress: "задача 1 — решения владельца получены 2026-10-05, ворота GATE P7 не сняты (нет логов p7-base-backup-status и p7-base-verify); задачи 2–3 выполнены локально 2026-10-05 (режимы swap, ufw, ssh, tinyproxy, reboot, cleanup; строки host-verify и host-preflight; однострочник без копий пакета), не закоммичено; хост не менялся"
requirements-completed: []
requires: [06-06]
---

# Phase 6 Plan 07: подготовка хоста (PRD-02, PRD-07)

Хост не менялся. Все шаги на VPS — только по отдельным разрешениям владельца.

## Задача 1 — состояние хоста и решения владельца (2026-10-05)

`agg host-preflight` — исполнитель, по разрешению владельца «разрешаю agg host-preflight»; лог `$HOME/run-agg-phase6/p7-base-host-preflight.log` вне репозитория, `HOST PREFLIGHT DONE`, `rc=0`, адресов нет.

| Что | Состояние |
|---|---|
| Swap | 0; память 3914 МБ, доступно 3135 МБ |
| Диск `/` | 29 ГБ, занято 18 %, свободно 24 ГБ |
| Ядро | работает `6.8.0-139-generic`, новейшее установленное `6.8.0-142-generic`; `reboot-required: yes` (3 пакета) |
| Обновления | 29 по кэшу, security — 0 |
| Часы | `NTP=yes`, `NTPSynchronized=yes` |
| Службы | `docker` enabled/active; `tinyproxy` enabled/active |
| Контейнеры | `aggregator-bot-1`, `aggregator-worker-1` — `healthy`, `restart=unless-stopped`; `aggregator-postgres-1` — `Up 4 days (healthy)`, `restart=unless-stopped`; чужой контейнер VPN (`amnezia-awg2`, `restart=always`, работает 4 недели) |
| `ufw`, sshd, порты | текущей проверкой не печатаются; по аудиту Phase 1 (2026-09-28): `ufw` — `Status: inactive`, tinyproxy слушал 8888 на всех интерфейсах, порт VPN опубликован через Docker |

Сведения владельца (2026-10-05, дословно): «Захожу под root по паролю. прокси уже не пользуюсь»; «Вхожу через консоль в termius. Давно хотел перейти на ssh ключ чтобы не могли подобрать пароль к серверу». Termius — SSH-клиент: владелец входит под `root` по SSH с паролем; прежние шаги «root-консоли» выполнялись в SSH-сессии.

Решения владельца (2026-10-05): на предложенный набор и вариант SSH — дословно «да, веб-консоль есть, с набором согласен».

| № | Вопрос | Решение |
|---|---|---|
| 1 | swap | `swap-2g` |
| 2 | ufw | `ufw-do` — из веб-консоли провайдера, с проверкой SSH и VPN сразу после |
| 3 | SSH | **вариант вне плана, добавлен решением владельца — `ssh-root-key`**: `PasswordAuthentication no`, `KbdInteractiveAuthentication no`, `PermitRootLogin prohibit-password` (root входит только по ключу, вход по паролю выключен для всех). Ключ создаёт и добавляет владелец в Termius; исполнитель ключей не видит |
| 4 | tinyproxy | `tinyproxy-disable` |
| 5 | роль и ключ runagg | `runagg-record` |
| 6 | уборка | `tmp` — да, `load` — да; `sudoers` — нет, `key` — нет, `tar` — нет |
| 7 | IN-05 | `in05-check` — уже выполнено в 06-06 (строка `postgres_image`), в этом плане ничего не пишется |
| 8 | режим `reboot` | да |

Строка решений: `swap=2g ufw=do ssh=root-key tinyproxy=disable cleanup=sudoers:нет,key:нет,tar:нет,tmp:да,load:да runagg=record in05=check reboot=да`

Режимы, которые будут написаны: `swap`, `ufw`, `ssh`, `tinyproxy`, `reboot`, `cleanup` (пункты `tmp` и `load`). Отказы: пункты уборки `sudoers`, `key`, `tar`; закрепление digest в `Dockerfile` и compose; смена ключа `runagg`.

Следствия, названные владельцу:

- Режимы `ufw` и `ssh` выполняются только из веб-консоли провайдера (она не зависит от SSH): запуск из сессии Termius режим отклонит.
- Режим `ssh` для варианта `ssh-root-key` дополнительно отказывает, если у `root` нет ни одного ключа в `authorized_keys`.
- Наличие веб-консоли — со слов владельца; вход в неё перед шагами `ufw` и `ssh` проверяется отдельно.
- Перезагрузка прервёт и VPN владельца на несколько минут; разрешение на неё — отдельно (задача 11).

## Задачи 2–3 — локальная реализация (2026-10-05)

| Что | Где | Поведение |
|---|---|---|
| `swap` (`SWAP`) | `host-apply.sh` | `SWAP_MB=2048`; при существующем swap — «изменений нет», код 0; свободно меньше `SWAP_MB` + 2048 МБ — STOP; `dd`, `chmod 600`, `mkswap`, `swapon`, при сбое файл удаляется; копия `fstab.agg-bak`, одна строка в `fstab`; строка отката |
| `ufw` (`UFW`) | `host-apply.sh` | только из веб-консоли (`console_only`); порты — из `sshd -T`, нет портов — STOP; запрет входящих, разрешение исходящих, `allow <порт>/tcp`, `--force enable`; откат `ufw disable` |
| `ssh` (`SSH`) | `host-apply.sh` | только из веб-консоли; вариант `ssh-root-key`: drop-in `00-aggregator.conf` с `PasswordAuthentication no`, `KbdInteractiveAuthentication no`, `PermitRootLogin prohibit-password`; STOP до слова, если у `root` нет ни одного ключа; сбой `sshd -t` — drop-in удалён, служба не трогается; действующие значения сверяются после (расхождение — код ≠ 0 и подсказка отката) |
| `tinyproxy` (`TINYPROXY`) | `host-apply.sh` | `systemctl disable --now tinyproxy`; откат `systemctl enable --now tinyproxy` |
| `reboot` (`REBOOT`) | `host-apply.sh` | до слова — контейнеры и политики перезапуска, предупреждения о тех, что не вернутся сами; STOP, если `docker` не в автозапуске или swap включён без строки в `fstab`; затем `systemctl reboot` |
| `cleanup` (`CLEANUP`) | `host-apply.sh` | пункты `tmp` (каталоги `/tmp/tmp.*` владельца root с `SHA256SUMS` и `agg-install.sh`, кроме текущего; символические ссылки не трогаются) и `load` (`/var/tmp/agg-load.*` старше суток); строки `CLEANUP <пункт>: removed <n>|absent` |
| Строки состояния | `host-verify.sh` | `swap_mb=`, `uptime_min=`, `ufw=`, `ufw_ssh=`, `ufw_allow=`, `ssh_permitrootlogin=`, `ssh_passwordauthentication=`, `tinyproxy_listen=`, `cron=`, `phase1_sudoers=`, `tmp_packages=`; условия `HOST VERIFY OK` не менялись |
| Порты и кандидаты на уборку | `host-preflight.sh` | порты как `порт/протокол процесс` без адресов; `runagg_keys= phase1_keys= restrict_keys=`, `root_keys=` (только числа); `postgres17_tar_bytes=`, `tmp_packages=`, `load_dirs=` |
| IN-06 | `oneliner.sh` | во временный каталог копируется `SHA256SUMS`, сверяется его хеш, затем только перечисленные в нём файлы; каталог удаляется при любом исходе; ручная строка `systemctl reboot` заменена однострочником режима `reboot`; режимы `ufw` и `ssh` вынесены в раздел «Веб-консоль провайдера (не SSH)» |

Проверки: `deploy/confirm-test.sh` — 174 строки `ok`, `CONFIRM OK` (слова шести режимов; 29 случаев `ok   mode …`; 23 строки `ok   host-verify: …`; 7 — `ok   host-preflight: …`; 5 — `ok   oneliner: …`); `deploy/deploy-apply-test.sh` — `DEPLOY APPLY OK`; `deploy/load-postgres-test.sh` — `LOAD POSTGRES TEST OK`; `/bin/bash deploy/backup-test.sh` — `BACKUP TESTS OK`; синтаксис всех скриптов под `/bin/bash` 3.2; `go.mod`, `go.sum`, `docker-compose.yml`, `Dockerfile` не менялись; в пакете 18 файлов, как до плана. Режимы в `usage`: `updates|compose|age|swap|ufw|ssh|tinyproxy|reboot|cleanup` — совпадают со списком решений. Хост не затронут: всё на заглушках и временных каталогах.

`SHA256SUMS` пакета по рабочему дереву: `e16289aa…da79` (был `8b1e4301…837c`) — первый `ship.sh` остановится на сверке, нужен `INSTALL` №3.

Отступления от плана:

- Вариант SSH `ssh-root-key` и проверка наличия ключа у `root` — сверх плана, по решению владельца.
- Тест не очищает каталоги по шаблону: каждый случай получает свежие каталоги внутри временного.
- Приглашение ввода слова печатается без перевода строки (функция `confirm` не менялась), поэтому в выводе первая строка после слова идёт за приглашением.

Не проверено локально (проверяется только на хосте): поведение `ufw` вместе с Docker и VPN-контейнером владельца; `systemctl reload ssh` при сокет-активации; фактические значения `sshd -T` и наличие других файлов настроек SSH с тем же приоритетом.

## Не сделано

- Ворота задачи 1 (`GATE P7 OK`): нет логов `p7-base-backup-status.log` и `p7-base-verify.log` — нужны два чтения по разрешению владельца.
- Задачи 4–15 — шаги владельца на VPS, каждый по отдельному разрешению.
