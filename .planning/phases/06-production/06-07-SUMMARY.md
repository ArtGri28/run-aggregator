---
phase: 06-production
plan: 07
subsystem: deploy
tags: [host, swap, ufw, ssh, tinyproxy, reboot, cleanup]
status: in-progress
progress: "задача 1 — решения владельца получены 2026-10-05, ворота GATE P7 не сняты (нет логов p7-base-backup-status и p7-base-verify); задачи 2–3 выполнены локально 2026-10-05 (режимы swap, ufw, ssh, tinyproxy, reboot, cleanup; строки host-verify и host-preflight; однострочник без копий пакета), коммит 397723a; задачи 4–5 выполнены: пакет доставлен, INSTALL №3 выполнен владельцем, состояние хоста снято; задачи 6 (swap 2 ГБ) и 9 (tinyproxy отключён) выполнены владельцем и подтверждены agg host-verify; ключ root для входа по SSH добавлен и проверен владельцем; ufw, ssh, cleanup, обновления, перезагрузка — не выполнялись"
requirements-completed: []
requires: [06-06]
---

# Phase 6 Plan 07: подготовка хоста (PRD-02, PRD-07)

На хосте: `INSTALL` №3, swap 2 ГБ, tinyproxy отключён (2026-10-05). `ufw`, настройки SSH, уборка, обновления и перезагрузка не выполнялись. Все шаги на VPS — только по отдельным разрешениям владельца.

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

## Ворота задачи 1 и задачи 4–5 (2026-10-05)

- `GATE P7 OK`: логи `p7-base-backup-status.log`, `p7-base-host-preflight.log`, `p7-base-verify.log` (исполнитель, по разрешениям владельца) — `STATUS OK`, `backup_copy_age_s=30380`, `DEPLOY VERIFY OK`, строка `postgres_image id=sha256:d74eeac9…c46f`, `HOST PREFLIGHT DONE`; адресов и токенов нет.
- Задача 4: `p7-ship.log` — `SMOKE OK`, ожидаемый `STOP` сверки, образ не передавался (`SHIP PACKAGE OK`); однострочник соответствует репозиторию (`ONELINER MATCHES REPO`, `SHA256SUMS` `e16289aa…da79`). Код — коммит владельца `397723a`.
- Задача 5: `INSTALL` №3 — ответ владельца дословно: «готово, AGG INSTALL DONE, разрешаю host-verify и host-preflight». Однострочник новой формы (IN-06) отработал на хосте. Установленные файлы совпали с репозиторием (`oneliner.sh installed` → `sha256sum -c` на VPS, код 0).

Фактическое состояние хоста после `INSTALL` №3 (`p7-install-host-verify.log`, `p7-install-host-preflight.log`; `HOST VERIFY OK`, `HOST PREFLIGHT DONE`):

| Строка | Значение |
|---|---|
| `swap_mb` | 0 |
| `uptime_min` | 42976 (около 30 суток) |
| `ufw` / `ufw_ssh` / `ufw_allow` | `inactive` / `n/a` / `none` — совпадает с аудитом Phase 1 |
| `ssh_permitrootlogin` / `ssh_passwordauthentication` | `yes` / `yes` |
| `tinyproxy_listen` | `all` |
| `cron` | `active` |
| `phase1_sudoers` | `present` |
| `tmp_packages` | 17 |
| порты | `22/tcp sshd`, `8888/tcp tinyproxy`, `39901/udp docker-proxy` (VPN), `22203/tcp containerd`, `53` systemd-resolve |
| `runagg_keys= phase1_keys= restrict_keys=` | 1 / 1 / 1 — единственный ключ `runagg` — ключ Phase 1 (действующий; пункт уборки `key` не выбран) |
| `root_keys` | 1 — чей это ключ, исполнителю неизвестно; уточняется у владельца до шага `ssh` |
| `postgres17_tar_bytes` | `absent`; `load_dirs=0` |

## Задачи 6 и 9 — swap и tinyproxy (2026-10-05)

Команды готовил исполнитель, выполнял владелец в сессии SSH под `root` (Termius). Порядок отличается от плана: `tinyproxy` выполнен раньше `ufw` и SSH — эти шаги независимы.

| Шаг | Результат |
|---|---|
| `host-apply.sh swap`, слово `SWAP` | вывод владельца: «Будет создан /swapfile на 2048 МБ … (свободно на /: 24566 МБ)», таблица `swapon` (`/swapfile file 2G 0B -2`), строка отката, `HOST APPLY swap DONE` |
| `host-apply.sh tinyproxy`, слово `TINYPROXY` | две попытки отменены скриптом из-за слова (лишние байты русской раскладки; опечатка `TYNYPROXY`) — «Отменено, хост не изменён»; третья — ответ владельца «готово» |
| `agg host-verify` (исполнитель, по разрешению владельца) | `p7-swap-tinyproxy-host-verify.log`: `swap_mb=2047`, `tinyproxy: enabled=disabled active=inactive`, `tinyproxy_listen=none`, `HOST VERIFY OK`; `tmp_packages=17` — не выросло после трёх запусков однострочника новой формы (IN-06 работает на хосте) |

Откат swap (из вывода скрипта): `swapoff /swapfile`, удаление строки `/swapfile` из `/etc/fstab`, удаление файла; копия прежнего файла — `/etc/fstab.agg-bak`. Откат tinyproxy: `systemctl enable --now tinyproxy`.

## Подготовка к шагу `ssh` — ключ root (2026-10-05)

Выполнена владельцем в Termius, исполнитель ключей не видел. Со слов владельца: ключ создан (ED25519, с парольной фразой), экспортирован на сервер для `root`; после удаления пароля из настроек хоста подключение в новом окне прошло — дословно: «После удаления пароля и нажал коннект и подключился в новом окне». Про ключ, который был у `root` до этого (`root_keys=1` в `p7-install-host-preflight.log`), владелец дословно: «Ключ не помню чтобы добавлял, но наверное когда-то это делал» — его происхождение не установлено, он не удалялся.

Не подтверждено проверкой исполнителя: число ключей у `root` после экспорта (ожидается 2) — увидит следующий `agg host-preflight`.

## Отступления после задач 6 и 9 (2026-10-05)

- **Пароль `root`.** Владелец не вспомнил пароль `root` (хранился в настройках Termius; поле очищено при переводе хоста на ключ по инструкции исполнителя — исполнитель не спросил заранее, записан ли пароль в другом месте). Владелец задал новый командой `passwd` в сессии SSH; исполнитель пароля не видел. Вход в веб-консоль провайдера проверен владельцем, дословно: «вошёл в консоль, tty ответил /dev/tty1».
- **`ufw` отложен решением владельца.** На вопрос «А для чего мы это делаем сейчас?» исполнитель ответил, что после отключения tinyproxy наружу почти ничего не слушает и `ufw` даёт в основном защиту на будущее; владелец выбрал, дословно: «Вариант 3, давай выключать пароли» — `ufw` отдельным шагом позже. Режим `ufw` в пакете остаётся; пункт PRD-02 «ufw» открыт.
- **Запуск режимов из веб-консоли.** Вставка в неё не работает, поэтому вместо однострочника `ROOT <режим>`: в сессии SSH пакет сверяется по хешу и копируется в `/root/agg-pkg` (700, только root), в веб-консоли набирается `cd /root/agg-pkg && sha256sum -c --quiet SHA256SUMS && bash host-apply.sh <режим>`.
- **Первая попытка режима `ssh` — STOP, пароли не выключены.** Из веб-консоли: «Ключей у root: 2», слово `SSH` принято, drop-in записан, `sshd -t` прошёл, но действующие значения остались `yes` — режим напечатал `STOP: значения не те, что ожидались: passwordauthentication kbdinteractiveauthentication permitrootlogin` и откат. Вход по паролю остался включён; доступ не терялся; drop-in лежит и не действует.
- **Причина** (вывод `grep` владельца, только чтение): в `/etc/ssh/sshd_config` строки 15–18 — `PasswordAuthentication yes`, `ChallengeResponseAuthentication yes`, `KbdInteractiveAuthentication yes`, `PermitRootLogin yes`; строки `Include` в основном файле нет, каталог `sshd_config.d` не читается (в том числе `60-cloudimg-settings.conf`).
- **Доработка режима `ssh`** (локально): если после записи drop-in значения не те, а основной файл сам их задаёт, режим делает копию `sshd_config.agg-bak` (один раз), комментирует мешающие строки (`#agg# `, включая `ChallengeResponseAuthentication` и строки в блоках `Match`), добавляет три директивы в начало файла, проверяет `sshd -t` и действующие значения; при любом сбое возвращает основной файл из копии, удаляет drop-in и не перезагружает sshd. Строки основного файла показываются до слова. `sshd` перезагружается только после успешной сверки значений (раньше — до неё). Тесты: 10 случаев `ok   mode ssh: …`, `deploy/confirm-test.sh` — 179 строк `ok`, `CONFIRM OK`; `DEPLOY APPLY OK`, `LOAD POSTGRES TEST OK`. `SHA256SUMS` пакета — `0ea4636e…c2e2`; установленные файлы (`agg`, `lib`) не меняются — `INSTALL` не нужен, нужен `ship.sh` и новая копия в `/root/agg-pkg`.

## Не сделано

- Задачи 6–15 — шаги владельца на VPS, каждый по отдельному разрешению.
