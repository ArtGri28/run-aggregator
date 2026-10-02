#!/usr/bin/env bash
# Локальные тесты Phase 6 (бэкап, аудит, восстановление): хост не меняется, сети нет.
# Каждый раздел — функция t_имя, список вызовов внизу файла. Разделы:
#   t_host_audit  — deploy/host-audit.sh печатает 14 ключей по контракту, только читает;
#   selftest      — итоговая строка раздела: ok только без провалов;
#   tracer, iso-oracle, retention, rotate-dir, rotate-state, publish, sync, clock-rotate, fail-paths — копия
#   deploy/vps/backup.sh на заглушках (docker, age, timedatectl, flock, sleep, sync, date) и чистые
#   функции deploy/vps/backup-lib.sh (ISO-неделя по оракулу python3, ротация 7 + 4);
#   copy-* — шаги 6–7 backup.sh: настоящий curl отправляет копию в локальную заглушку Bot API
#   (python3, 127.0.0.1, случайный порт); токен и идентификатор канала выдуманы и собираются
#   при запуске, адрес настоящего API не используется;
#   recipient-format, status-*, apply-* — backup-status.sh и backup-apply.sh на копиях с
#   подменёнными путями; pkg-*, host-words, oneliner-lines — состав пакета, agg, sudoers,
#   режимы host-apply.sh и однострочники.
# Без аргумента Docker не нужен. С аргументом pg поднимается одноразовый PostgreSQL 17
# (копия docker-compose.test.yml: проект backup-probe-test, порт 127.0.0.1:55433, данные в
# tmpfs): замер дампов для BACKUP_MIN_BYTES (D-21), проверка counters_sql и допущение A4.
# Запуск на Mac или Linux (системный bash 3.2):  /bin/bash deploy/backup-test.sh [pg] -> BACKUP TESTS OK
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")"
HERE=$PWD

T=$(mktemp -d)
PROBE_FILE=""
API_PID=""
cleanup() {
  if [ -n "$API_PID" ]; then kill "$API_PID" 2>/dev/null || true; fi
  if [ -n "$PROBE_FILE" ]; then docker compose -f "$PROBE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true; fi
  rm -rf "$T"
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP
FAILS=0

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
# stub <каталог> <имя> <тело>: исполняемая заглушка; имя и аргументы дописываются в <каталог>/calls.log
stub() {
  { echo '#!/bin/bash'; echo "echo \"$2\${*:+ \$*}\" >> \"$1/calls.log\""; echo "$3"; } > "$1/$2"
  chmod +x "$1/$2"
}

# ---- t_host_audit ----------------------------------------------------------
# Запрещённые слова блока аудита: тот же список, что в verify плана 06-01.
FORBID='(apt-get|apt |dpkg|snap |systemctl +(start|stop|restart|reload|enable|disable|mask|unmask|edit|set-|daemon)|timedatectl +set|ufw +(enable|disable|allow|deny|reject|limit|delete|insert|reset|reload|default|logging|route)|(^|[^a-z_-])(rm|mv|cp|ln|dd|tee|touch|mkdir|rmdir|chmod|chown|truncate|install|mktemp|mkswap|swapon|swapoff|fallocate|reboot|shutdown|poweroff|crontab|kill|pkill|usermod|passwd|visudo|scp|ssh|wget|nc|eval|source|exec|docker|agg|hostname|hostnamectl|ifconfig|ip|ss|uname|env|printenv|cat)([^a-z_-]|$))'
ALLOWED='systemctl is-active cron
timedatectl show -p NTPSynchronized --value
timedatectl show -p Timezone --value
free -m
df -Pm /var
apt-cache policy'
PROBED='^(age|gpg|curl|flock|jq|python3)( |$)'

# audit_dir <имя> <программы через пробел>: каталог сценария с заглушками чтения.
audit_dir() {
  local d="$T/$1" p
  mkdir -p "$d/bin" "$d/work" "$d/home"
  : > "$d/bin/calls.log"
  for p in awk grep sed date head tr cut wc; do ln -s "$(command -v $p)" "$d/bin/$p"; done
  for p in $2; do stub "$d/bin" "$p" 'exit 0'; done
  stub "$d/bin" systemctl 'echo active'
  stub "$d/bin" timedatectl 'case "$*" in *NTPSynchronized*) echo yes ;; *Timezone*) echo Europe/Warsaw ;; esac'
  stub "$d/bin" free "printf '              total        used        free\nMem:           1963         500        1000\nSwap:             0           0           0\n'"
  stub "$d/bin" df "printf 'Filesystem 1048576-blocks Used Available Capacity Mounted on\n/dev/vda1 40000 14000 26000 36%% /\n'"
  stub "$d/bin" sudo 'echo "Status: active"' # вызова быть не должно: его нет в ALLOWED
  stub "$d/bin" apt-cache "echo ' 500 http://archive.example/ubuntu noble/universe amd64 Packages'"
}
# run_audit <сценарий> [скрипт]: по умолчанию настоящий блок, иначе его копия из rr_script.
run_audit() { (cd "$T/$1/work" && PATH="$T/$1/bin" HOME="$T/$1/home" /bin/bash "${2:-$HERE/host-audit.sh}"); }
# rr_script <сценарий>: копия блока, где /var/run заменён каталогом сценария (сам блок без ручек).
rr_script() {
  sed "s#/var/run#$T/$1/run#g" "$HERE/host-audit.sh" > "$T/$1/audit.sh"
  [ "$(grep -c "$T/$1/run" "$T/$1/audit.sh")" -ge 2 ] || bad "host-audit: /var/run в блоке не найден ($1)"
}
# want <14 значений в порядке контракта>
want() {
  local k i=1
  for k in age gpg curl flock jq python cron ntp_synchronized timezone swap disk_free_mb ufw reboot_required universe; do
    printf 'AUDIT %s=%s\n' "$k" "${!i}"
    i=$((i + 1))
  done
  echo 'AUDIT END'
}
chk_audit() { # chk_audit <сценарий> <описание> <ожидаемый вывод> [скрипт]
  local got
  got=$(run_audit "$1" "${4:-}")
  if [ "$got" = "$3" ]; then ok "host-audit: $2"; else bad "host-audit: $2"; printf '%s\n' "$got" | sed 's/^/     got: /'; fi
}

t_host_audit() {
  local TZO RB B n
  TZO=$(date +%z)
  if [ -d /var/run ] && [ -r /var/run ] && [ -x /var/run ]; then
    if [ -f /var/run/reboot-required ]; then RB=yes; else RB=no; fi
  else RB=unknown; fi

  audit_dir all "age gpg curl flock jq python3"
  chk_audit all "все есть, 14 ключей по контракту" \
    "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown "$RB" yes)"

  audit_dir part "gpg flock python3"
  stub "$T/part/bin" timedatectl 'case "$*" in *NTPSynchronized*) echo no ;; *Timezone*) echo Europe/Warsaw ;; esac'
  stub "$T/part/bin" apt-cache "echo ' 500 http://archive.example/ubuntu noble/main amd64 Packages'"
  chk_audit part "нет age, curl, jq; часы не синхронизированы; нет universe" \
    "$(want absent present absent present absent present active no "Europe/Warsaw@$TZO" 0 26000 unknown "$RB" no)"

  audit_dir junk "age gpg curl flock jq python3"
  stub "$T/junk/bin" timedatectl 'echo "node.example.com 10.0.0.1"'
  stub "$T/junk/bin" systemctl 'exit 0'
  stub "$T/junk/bin" df "printf 'Filesystem a b c d\n/dev/vda1 x y zzz 1%% /\n'"
  chk_audit junk "мусор вместо значений -> unknown" \
    "$(want present present present present present present unknown unknown unknown 0 unknown unknown "$RB" yes)"
  if run_audit junk | grep -vqE '^AUDIT (END|[a-z_]+=[A-Za-z0-9_/+@-]+)$'; then
    bad "host-audit: строка вне грамматики лога"
  else
    ok "host-audit: каждая строка по грамматике лога (адрес и имя хоста не пройдут)"
  fi

  # universe: ошибка чтения -> unknown, а не no
  audit_dir aptfail "age gpg curl flock jq python3"
  stub "$T/aptfail/bin" apt-cache "echo ' 500 http://archive.example/ubuntu noble/universe amd64 Packages'; exit 100"
  chk_audit aptfail "universe: apt-cache завершился ошибкой -> unknown" \
    "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown "$RB" unknown)"
  audit_dir aptempty "age gpg curl flock jq python3"
  stub "$T/aptempty/bin" apt-cache ':'
  chk_audit aptempty "universe: пустой вывод apt-cache -> unknown" \
    "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown "$RB" unknown)"
  audit_dir aptgone "age gpg curl flock jq python3"
  rm "$T/aptgone/bin/apt-cache"
  chk_audit aptgone "universe: apt-cache отсутствует -> unknown" \
    "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown "$RB" unknown)"

  # reboot_required: каталог не читается -> unknown; читается -> yes/no по файлу
  audit_dir rrmiss "age gpg curl flock jq python3"; rr_script rrmiss
  chk_audit rrmiss "reboot_required: каталога нет -> unknown" \
    "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown unknown yes)" "$T/rrmiss/audit.sh"
  audit_dir rrno "age gpg curl flock jq python3"; rr_script rrno; mkdir "$T/rrno/run"
  chk_audit rrno "reboot_required: каталог читается, файла нет -> no" \
    "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown no yes)" "$T/rrno/audit.sh"
  audit_dir rryes "age gpg curl flock jq python3"; rr_script rryes; mkdir "$T/rryes/run"; : > "$T/rryes/run/reboot-required"
  chk_audit rryes "reboot_required: файл есть -> yes" \
    "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown yes yes)" "$T/rryes/audit.sh"
  audit_dir rrdeny "age gpg curl flock jq python3"; rr_script rrdeny; mkdir "$T/rrdeny/run"; : > "$T/rrdeny/run/reboot-required"
  if [ "$(id -u)" = 0 ]; then
    ok "host-audit: reboot_required: каталог без прав — пропущено (запуск от root)"
  else
    chmod 000 "$T/rrdeny/run"
    chk_audit rrdeny "reboot_required: каталог без прав на чтение -> unknown" \
      "$(want present present present present present present active yes "Europe/Warsaw@$TZO" 0 26000 unknown unknown yes)" "$T/rrdeny/audit.sh"
    chmod 700 "$T/rrdeny/run"
  fi

  # только чтение: журнал вызовов, ненужные программы, чистота рабочего каталога и HOME
  for n in all part junk aptfail aptempty aptgone rrmiss rrno rryes rrdeny; do
    if [ ! -s "$T/$n/bin/calls.log" ] || [ -n "$(grep -vxF "$ALLOWED" "$T/$n/bin/calls.log" || true)" ]; then
      bad "host-audit: журнал вызовов ($n) содержит лишнее или пуст"; continue
    fi
    if grep -qE "$PROBED" "$T/$n/bin/calls.log"; then bad "host-audit: проверяемая программа выполнена ($n)"; continue; fi
    if grep -q '^sudo' "$T/$n/bin/calls.log"; then bad "host-audit: вызван sudo ($n)"; continue; fi
    if [ -n "$(ls -A "$T/$n/work")$(ls -A "$T/$n/home")" ]; then bad "host-audit: блок оставил файлы ($n)"; continue; fi
    ok "host-audit: только чтение ($n): разрешённые вызовы, sudo не вызывался, программы не запускались, файлов нет"
  done

  # статика: тело блока без комментариев
  B=$(grep -v '^[[:space:]]*#' "$HERE/host-audit.sh")
  if printf '%s\n' "$B" | grep -nE "$FORBID"; then bad "host-audit: в блоке изменяющее или сетевое слово"
  else ok "host-audit: нет изменяющих и сетевых слов"; fi
  n=$(printf '%s\n' "$B" | sed -E 's#2?>/dev/null##g; s#2>&1##g; s#>&2##g' | tr -cd '>' | wc -c | tr -d ' ')
  if [ "$n" = 0 ]; then ok "host-audit: перенаправления только в /dev/null"; else bad "host-audit: лишние перенаправления ($n)"; fi
  if printf '%s\n' "$B" | grep -n 'sudo'; then bad "host-audit: в блоке есть sudo"
  else ok "host-audit: sudo в блоке нет"; fi
  if printf '%s\n' "$B" | grep -qx 'val ufw unknown'; then ok "host-audit: ufw не читается, всегда unknown"
  else bad "host-audit: ufw должен печататься как unknown без чтения"; fi
}

# ---- Phase 6, 06-02: backup.sh и backup-lib.sh ------------------------------
VPS=$HERE/vps
SUMMARY01=$HERE/../.planning/phases/06-production/06-01-SUMMARY.md
# shellcheck source=vps/backup-lib.sh
. "$VPS/backup-lib.sh"
AGEHDR=22 # «age-encryption.org/v1» + перевод строки: заголовок заглушки age
TESTKEY=age1$(printf '%058d' 0 | tr 0 q) # формат ключа, не настоящий ключ
PB=""
# need <условие>: проверка; провал дописывается в PB, report печатает итог раздела.
need() { if ! eval "$1"; then PB="$PB
       - $1"; fi; }
report() { if [ -z "$PB" ]; then ok "$1"; else bad "$1$PB"; fi; PB=""; }
# sec_end <раздел> <FAILS до раздела> [пояснение]: итоговая строка раздела со сценариями.
# ok — только если в разделе не было провалов; иначе строка FAIL с их числом (в FAILS они
# уже учтены строками сценариев, счётчик здесь не меняется).
sec_end() {
  if [ "$FAILS" -eq "$2" ]; then ok "$1: $SC_N сценариев${3:-}"
  else echo "FAIL $1: провалено $((FAILS - $2)) из $SC_N сценариев"; fi
}
# Самопроверка итоговой строки раздела: при провале сценария строки ok быть не должно.
t_selftest() {
  local o1 o2
  o1=$(FAILS=0; SC_N=3; sec_end demo 0 " (x)")
  o2=$(FAILS=0; SC_N=3; bad "demo: сценарий" >/dev/null; sec_end demo 0)
  need '[ "$o1" = "ok   demo: 3 сценариев (x)" ]'
  need '[ "$o2" = "FAIL demo: провалено 1 из 3 сценариев" ] && ! printf "%s\n" "$o2" | grep -q "^ok"'
  report "selftest: итоговая строка раздела — ok только без провалов"
}

# dates_between <от> <до>: даты подряд, по одной в строке (оракул — python3).
dates_between() {
  python3 -c '
import datetime, sys
a = datetime.date.fromisoformat(sys.argv[1]); b = datetime.date.fromisoformat(sys.argv[2])
while a <= b:
    print(a.isoformat()); a += datetime.timedelta(days=1)' "$1" "$2"
}

# Шаблон пригодной копии: заголовок age + заполнение до BACKUP_MIN_BYTES + 1000 байт.
{ printf 'age-encryption.org/v1\n'; head -c $((BACKUP_MIN_BYTES + 1000 - AGEHDR)) /dev/zero | tr '\0' x; } > "$T/tpl.age"
TPLSUM=$(_sha256 "$T/tpl.age")

# mkcopy <каталог> <дата> good|badsum|nosum|nometa|badmeta|nohdr|orphan|empty|partial
mkcopy() {
  local f="$1/aggregator-$2.dump.age"
  case "$3" in
    good)
      cp "$T/tpl.age" "$f"
      printf '%s  %s\n' "$TPLSUM" "${f##*/}" > "$f.sha256"
      echo "events_listed=1 users=1 migration=4" > "$f.meta"
      ;;
    badsum) # данные целы, сумма неверна
      mkcopy "$1" "$2" good
      printf '%064d  %s\n' 0 "${f##*/}" > "$f.sha256"
      ;;
    nosum) mkcopy "$1" "$2" good; rm "$f.sha256" ;;
    nometa) mkcopy "$1" "$2" good; rm "$f.meta" ;;
    badmeta) mkcopy "$1" "$2" good; echo "x; rm -rf /" > "$f.meta" ;;
    nohdr) # нет заголовка age, сумма верна
      head -c 30000 /dev/zero | tr '\0' y > "$f"
      printf '%s  %s\n' "$(_sha256 "$f")" "${f##*/}" > "$f.sha256"
      echo "events_listed=1 users=1 migration=4" > "$f.meta"
      ;;
    orphan) # файлы суммы и метаданных без данных
      printf '%s  %s\n' "$TPLSUM" "${f##*/}" > "$f.sha256"
      echo "events_listed=1 users=1 migration=4" > "$f.meta"
      ;;
    empty)
      : > "$f"
      printf '%s  %s\n' "$TPLSUM" "${f##*/}" > "$f.sha256"
      ;;
    partial)
      cp "$T/tpl.age" "$f.partial"
      printf '%s  %s\n' "$TPLSUM" "${f##*/}" > "$f.partial.sha256"
      ;;
  esac
}
# fill_range <каталог> <от> <до>: пригодные копии на каждую дату
fill_range() {
  local d
  for d in $(dates_between "$2" "$3"); do mkcopy "$1" "$d" good; done
}
# sum_ok <каталог> <имя>: sha256sum -c в каталоге копии (на Mac — shasum -a 256 -c)
sum_ok() {
  if command -v sha256sum >/dev/null 2>&1; then (cd "$1" && sha256sum -c --quiet "$2.sha256" >/dev/null 2>&1)
  else (cd "$1" && shasum -a 256 -c "$2.sha256" >/dev/null 2>&1); fi
}

# mk_env <имя> [keeproot]: окружение запуска копии backup.sh в $T/<имя>; E — его каталог.
# В копии заменены только пути, PATH и проверка root; docker, age, timedatectl, flock, sleep и
# date — заглушки в $E/bin (журнал вызовов — $E/bin/calls.log), переключатели — файлы sw.*.
mk_env() {
  local rd='/\[ "\$(id -u)" -eq 0 \]/d'
  E=$T/$1
  if [ "${2:-}" = keeproot ]; then rd='/^# нет такой строки$/d'; fi
  mkdir -p "$E/bin" "$E/lib" "$E/dir" "$E/opt" "$E/etc"
  : > "$E/opt/docker-compose.yml"
  : > "$E/bin/calls.log"
  : > "$E/marks.log"
  cp "$VPS/backup-lib.sh" "$E/lib/backup-lib.sh"
  printf '%s\n' "$TESTKEY" > "$E/lib/backup-recipient"
  echo 2026-10-01 > "$E/today"
  echo yes > "$E/clock.seq"
  echo $((BACKUP_MIN_BYTES + 1000)) > "$E/sw.dumpbytes"
  sed -e "s#^export PATH=.*#export PATH=$E/bin:/usr/bin:/bin:/usr/sbin:/sbin#" \
    -e "s#^L=/usr/local/lib/aggregator\$#L=$E/lib#" \
    -e "s#^DIR=/var/backups/aggregator\$#DIR=$E/dir#" \
    -e "s#^F=/opt/aggregator/docker-compose.yml\$#F=$E/opt/docker-compose.yml#" \
    -e "s#^LOCK=/run/aggregator-backup.lock\$#LOCK=$E/lock#" \
    -e "s#^LOG=/var/log/aggregator-backup.log\$#LOG=$E/log#" \
    -e "s#^CONF=/etc/aggregator-backup/telegram.curl\$#CONF=$E/etc/telegram.curl#" \
    -e "$rd" "$VPS/backup.sh" > "$E/backup.sh"
  if [ "$(grep -c "^export PATH=$E/bin" "$E/backup.sh")" != 1 ] || ! grep -q "^L=$E/lib\$" "$E/backup.sh" \
    || ! grep -q "^DIR=$E/dir\$" "$E/backup.sh" || ! grep -q "^F=$E/opt/docker-compose.yml\$" "$E/backup.sh" \
    || ! grep -q "^LOCK=$E/lock\$" "$E/backup.sh" || ! grep -q "^LOG=$E/log\$" "$E/backup.sh" \
    || ! grep -q "^CONF=$E/etc/telegram.curl\$" "$E/backup.sh"; then
    bad "backup: не удалось подготовить копию backup.sh ($1)"
  fi
  if [ "${2:-}" != keeproot ] && grep -q 'id -u' "$E/backup.sh"; then bad "backup: проверка root осталась в копии ($1)"; fi
  # docker: pg_dump -> «открытый дамп» из sw.dumpbytes байт с маркером; INSERT в heartbeats ->
  # компонент в marks.log; прочий psql -> строка счётчиков (sw.counters: fail|bad; sw.cnt — своя строка).
  sed "s#__E__#$E#g" > "$E/bin/docker" <<'EOF'
#!/bin/bash
E=__E__
echo "docker $*" >> "$E/bin/calls.log"
case "$*" in
  *pg_dump*)
    n=$(cat "$E/sw.dumpbytes")
    gen() { [ "$1" -gt 0 ] || return 0; { printf 'PLAINTEXT-DUMP-MARKER'; head -c "$n" /dev/zero | tr '\0' x; } | head -c "$1"; }
    if [ "$(cat "$E/sw.dump" 2>/dev/null)" = half ]; then gen $((n / 2)); exit 1; fi
    gen "$n"
    ;;
  *"INSERT INTO heartbeats"*)
    [ ! -e "$E/sw.mark" ] || exit 1
    case "$*" in *"('backup_copy')"*) [ ! -e "$E/sw.markcopy" ] || exit 1 ;; esac
    echo "$*" | sed -n "s/.*VALUES ('\([a-z_]*\)').*/\1/p" >> "$E/marks.log"
    ;;
  *"now() - beat_at"*)
    [ ! -e "$E/sw.agesfail" ] || exit 1
    cat "$E/sw.ages" 2>/dev/null || echo "none none"
    ;;
  *psql*)
    case "$(cat "$E/sw.counters" 2>/dev/null)" in
      fail) exit 1 ;;
      bad) echo "x; rm -rf /" ;;
      *)
        if [ -f "$E/sw.cnt" ]; then cat "$E/sw.cnt"
        else echo "events_listed=5 users=2 events_merged=0 dedup_candidates=pending:0|confirmed:0|rejected:0 source_alerts=0 heartbeats=0 migration=4"; fi
        ;;
    esac
    ;;
esac
EOF
  sed "s#__E__#$E#g" > "$E/bin/age" <<'EOF'
#!/bin/bash
E=__E__
echo "age $*" >> "$E/age.log"
echo "age $*" >> "$E/bin/calls.log"
case "$(cat "$E/sw.age" 2>/dev/null)" in
  fail) cat > /dev/null; exit 1 ;;
  nohdr) ;;
  *) printf 'age-encryption.org/v1\n' ;;
esac
tr 'A-Za-z' 'N-ZA-Mn-za-m'
EOF
  chmod +x "$E/bin/docker" "$E/bin/age"
  stub "$E/bin" flock 'echo "$PPID" > '"$E/pid; test ! -e $E/sw.flock" # pid запуска — для killstub
  stub "$E/bin" sleep ':'
  # curl: настоящий; аргументы каждого вызова пишутся в curl.args (секретов в них быть не должно),
  # имена в каталоге секретов на момент вызова — в curl.ls
  printf '%s\n' '#!/bin/bash' "echo \"\$*\" >> \"$E/curl.args\"" "ls -A \"$E/etc\" >> \"$E/curl.ls\" 2>/dev/null" \
    "exec $REALCURL \"\$@\"" > "$E/bin/curl"
  chmod +x "$E/bin/curl"
  # sync: на диск ничего не сбрасывает. Пишет в sync.log аргументы, имена в каталоге копий и
  # число отметок на момент вызова; при номере вызова из sw.syncfail возвращает 1. Проверяется
  # только место и порядок вызовов и обработка кода возврата, не сам fsync.
  stub "$E/bin" sync "E=$E"'; n=$(($(cat "$E/sync.n" 2>/dev/null || echo 0) + 1)); echo "$n" > "$E/sync.n"
echo "$* | $(ls -A "$E/dir" | grep 2026-10-01 | tr "\n" " ")| marks=$(grep -c . "$E/marks.log")" | sed "s#$E/dir/##g; s#$E/dir#DIR#g" >> "$E/sync.log"
[ "$n" != "$(cat "$E/sw.syncfail" 2>/dev/null)" ]'
  # mv: настоящий /bin/mv; при номере вызова из sw.mvkill запуск обрывается (kill -9) до rename
  printf '%s\n' '#!/bin/bash' "E=$E" 'n=$(($(cat "$E/mv.n" 2>/dev/null || echo 0) + 1)); echo "$n" > "$E/mv.n"' \
    'if [ "$n" = "$(cat "$E/sw.mvkill" 2>/dev/null)" ]; then kill -9 $PPID; exit 1; fi' 'exec /bin/mv "$@"' > "$E/bin/mv"
  chmod +x "$E/bin/mv"
  stub "$E/bin" timedatectl "E=$E"'; n=$(cat "$E/clock.n" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$E/clock.n"
l=$(sed -n "${n}p" "$E/clock.seq"); [ -n "$l" ] || l=$(tail -n 1 "$E/clock.seq"); echo "$l"'
  stub "$E/bin" date "case \"\$*\" in \"-u +%F\") cat $E/today ;; *) exec /bin/date \"\$@\" ;; esac"
}
# killstub <программа>: обёртка настоящей программы в $E/bin; при номере вызова из
# sw.<программа>kill запуск backup.sh обрывается (kill -9 по pid из $E/pid).
killstub() {
  printf '%s\n' '#!/bin/bash' "E=$E" "p=$1" 'n=$(($(cat "$E/$p.n" 2>/dev/null || echo 0) + 1)); echo "$n" > "$E/$p.n"' \
    'if [ "$n" = "$(cat "$E/sw.${p}kill" 2>/dev/null)" ]; then kill -9 "$(cat "$E/pid")"; exit 1; fi' "exec $(PATH=/usr/bin:/bin command -v "$1") \"\$@\"" > "$E/bin/$1"
  chmod +x "$E/bin/$1"
}
# run_bk: запуск копии backup.sh в $E; код -> RC, вывод -> $E/out, ошибки -> $E/err
run_bk() {
  RC=0
  /bin/bash "$E/backup.sh" > "$E/out" 2> "$E/err" || RC=$?
}
# calls <строка>: сколько раз в журнале заглушек есть ровно такая строка; ncalls <начало строки> — по началу
calls() { grep -cxF -- "$1" "$E/bin/calls.log" || true; }
ncalls() { grep -c -- "^$1" "$E/bin/calls.log" || true; }
# leak: файлы с открытым текстом дампа в $E (кроме самих заглушек)
leak() { grep -rl --exclude-dir=bin PLAINTEXT-DUMP-MARKER "$E" || true; }

# ---- Phase 6, 06-03: внешняя копия (шаги 6–7) --------------------------------
REALCURL=$(PATH=/usr/bin:/bin:/usr/local/bin command -v curl || true)
# Выдуманные значения собираются из частей: строки, похожей на токен, в файле нет.
TOK_A=7700001
TOK_B=$(printf '%035d' 0 | tr 0 k)
TOK="$TOK_A:$TOK_B"
CHAT="-100$((5500011122))"
API_PORT=""
# api_start: заглушка Bot API. Режим ответа читается из $T/api.mode на каждый запрос:
#   ok | http500 | ok500 (HTTP 500 с телом успешного ответа) | forbidden | garbage | mismatch. Журнал $T/api.log — строка на запрос:
#   <метод> token=match|other chat_id=<id> file=<имя> bytes=<n> sha256=<hex> reply=<код> caption=<JSON-строка>
# Сам токен в журнал не пишется.
api_start() {
  echo ok > "$T/api.mode"
  : > "$T/api.log"
  python3 -I - "$T/api.log" "$T/api.mode" "$T/api.port" "$TOK" "$CHAT" <<'PYAPI' &
import hashlib, http.server, json, re, sys, urllib.parse

log, modef, portf, token, chat = sys.argv[1:6]


class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, code, body):
        b = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(b)
        return code

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        m = re.fullmatch(r"/bot([^/]+)/(sendDocument|getChat)", urllib.parse.urlsplit(self.path).path)
        fields, fname, doc = {}, "", None
        bm = re.search(r'boundary=("?)([^";]+)\1', self.headers.get("Content-Type", ""))
        if bm:
            for part in body.split(b"--" + bm.group(2).encode()):
                head, sep, val = part.partition(b"\r\n\r\n")
                if not sep:
                    continue
                if val.endswith(b"\r\n"):
                    val = val[:-2]
                h = head.decode("utf-8", "replace")
                nm = re.search(r'[; ]name="([^"]*)"', h)
                if not nm:
                    continue
                if nm.group(1) == "document":
                    fn = re.search(r'filename="([^"]*)"', h)
                    doc, fname = val, (fn.group(1) if fn else "")
                else:
                    fields[nm.group(1)] = val.decode("utf-8", "replace")
        else:
            fields = {k: v[0] for k, v in urllib.parse.parse_qs(body.decode("utf-8", "replace")).items()}
        with open(modef) as f:
            mode = f.read().strip()
        method = m.group(2) if m else "unknown"
        tk = "match" if m and m.group(1) == token else "other"
        n = len(doc) if doc is not None else 0
        if tk != "match":
            code = self.reply(404, {"ok": False, "error_code": 404, "description": "Not Found"})
        elif mode == "http500":
            code = self.reply(500, b"Internal Server Error BODY-MARKER")
        elif mode == "forbidden":
            code = self.reply(403, {"ok": False, "error_code": 403, "description": "Forbidden: BODY-MARKER"})
        elif mode == "garbage":
            code = self.reply(200, b"<html>BODY-MARKER not json</html>")
        elif method == "getChat":
            if fields.get("chat_id") == chat:
                code = self.reply(200, {"ok": True, "result": {"id": int(chat), "type": "channel"}})
            else:
                code = self.reply(400, {"ok": False, "error_code": 400, "description": "Bad Request: chat not found"})
        else:
            size = n - 1 if mode == "mismatch" else n
            code = self.reply(500 if mode == "ok500" else 200, {"ok": True, "result": {"message_id": 1, "document": {"file_name": fname, "file_size": size}}})
        with open(log, "a") as f:
            f.write("%s token=%s chat_id=%s file=%s bytes=%d sha256=%s reply=%d caption=%s\n" % (
                method, tk, fields.get("chat_id", ""), fname, n,
                hashlib.sha256(doc).hexdigest() if doc is not None else "-", code,
                json.dumps(fields.get("caption", ""), ensure_ascii=False)))


srv = http.server.HTTPServer(("127.0.0.1", 0), H)
with open(portf, "w") as f:
    f.write(str(srv.server_address[1]))
srv.serve_forever()
PYAPI
  API_PID=$!
  local i=0
  while [ ! -s "$T/api.port" ] && [ "$i" -lt 100 ]; do /bin/sleep 0.1; i=$((i + 1)); done
  API_PORT=$(cat "$T/api.port" 2>/dev/null || true)
  if [ -z "$API_PORT" ] || [ -z "$REALCURL" ]; then bad "copy: заглушка Bot API не запустилась или нет curl"; return 1; fi
}
# api_mode <режим>: режим ответа заглушки и чистый журнал
api_mode() { echo "$1" > "$T/api.mode"; : > "$T/api.log"; }
# api_conf [порт]: конфиг curl текущего окружения $E — адрес заглушки с выдуманным токеном и
# идентификатор канала (A2: поле form в файле конфига)
api_conf() {
  printf 'url = "http://127.0.0.1:%s/bot%s/sendDocument"\nform = "chat_id=%s"\n' "${1:-$API_PORT}" "$TOK" "$CHAT" > "$E/etc/telegram.curl"
  chmod 600 "$E/etc/telegram.curl"
}
# api_n <начало строки>: сколько строк журнала заглушки начинается так
api_n() { grep -c -- "^$1" "$T/api.log" || true; }
# caption: подпись последнего sendDocument (раскодированная, с переводами строк)
caption() {
  grep '^sendDocument ' "$T/api.log" | tail -n 1 | sed 's/.* caption=//' | python3 -I -c 'import json, sys; sys.stdout.write(json.loads(sys.stdin.read()))'
}
# nosecret: выдуманные токен и идентификатор канала не попали в вывод, ошибки, лог запуска и
# аргументы curl; нарушение дополнительно считается в LEAKS для случая copy-secret-silence
LEAKS=0
nosecret() {
  if cat "$E/out" "$E/err" "$E/log" "$E/curl.args" 2>/dev/null | grep -qF -e "$TOK_B" -e "$CHAT"; then LEAKS=$((LEAKS + 1)); return 1; fi
  if cat "$E/out" "$E/err" "$E/log" 2>/dev/null | grep -qF BODY-MARKER; then LEAKS=$((LEAKS + 1)); return 1; fi
}
# cend <id> <описание>: итог случая — «ok   <id> …» или «FAIL <id> …»
cend() { if [ -z "$PB" ]; then ok "$1 $2"; else bad "$1 $2$PB"; fi; PB=""; }
# today_ok: сегодняшняя копия пригодна, три файла на месте, временных нет
today_ok() {
  local f=$E/dir/aggregator-2026-10-01.dump.age
  usable "$f" && [ -f "$f.sha256" ] && [ -f "$f.meta" ] && [ -z "$(ls "$E"/dir/*.partial* "$E"/dir/.copy-response.* 2>/dev/null)" ]
}
# only_backup: в marks.log только отметки backup (ни одной backup_copy)
only_backup() { [ -s "$E/marks.log" ] && ! grep -qvx backup "$E/marks.log"; }
# big <байт>: длина заглушечного дампа, при которой файл копии ровно <байт>
big() { echo $(($1 - AGEHDR)) > "$E/sw.dumpbytes"; }

t_copy() {
  local f N cap before sha m
  # copy-tracer: один сквозной путь до COPY OK
  mk_env copytr
  api_mode ok; api_conf
  f=$E/dir/aggregator-2026-10-01.dump.age
  run_bk
  N=$((AGEHDR + BACKUP_MIN_BYTES + 1000))
  need '[ "$RC" = 0 ]'
  need 'grep -q "^BACKUP OK " "$E/out" && grep -qx "ROTATE OK keep=1 delete=0" "$E/out" && grep -qx "COPY OK size=$N" "$E/out"'
  need '[ "$(wc -l < "$T/api.log" | tr -d " ")" = 1 ] && [ "$(api_n "sendDocument token=match chat_id=$CHAT file=aggregator-2026-10-01.dump.age bytes=$N ")" = 1 ]'
  sha=$(cut -d' ' -f1 "$f.sha256")
  need 'grep -q " sha256=$sha reply=200 " "$T/api.log" && [ "$sha" = "$(_sha256 "$f")" ]'
  cap=$(caption)
  local cap_re='^aggregator-[0-9]{4}-[0-9]{2}-[0-9]{2}\.dump\.age sha256=[0-9a-f]{64} size=[0-9]+ counts=[a-z_]+=[0-9a-z:|]+( [a-z_]+=[0-9a-z:|]+)*$' mig_re=' migration=[0-9]+$'
  need '[[ $cap =~ $cap_re ]] && [ "$(printf "%s\n" "$cap" | wc -l | tr -d " ")" = 1 ] && [ "${#cap}" -le 1024 ]'
  need '[ "$cap" = "aggregator-2026-10-01.dump.age sha256=$sha size=$N counts=$(cat "$f.meta")" ]'
  need '[[ $cap == *" counts=events_listed="* ]] && [[ $cap == *" users="* ]] && [[ $cap =~ $mig_re ]]'
  need '[ "$(cat "$E/marks.log")" = "backup
backup_copy" ]'
  need 'grep -q -- "--config $E/etc/telegram.curl" "$E/curl.args" && ! grep -q "127.0.0.1\|/bot\|://" "$E/curl.args"'
  need 'today_ok && nosecret'
  cend copy-tracer "копия -> curl --config -> ответ Bot API -> backup_copy -> COPY OK, код 0"

  # copy-no-config
  mk_env copync
  api_mode ok
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK " "$E/out" && grep -qx "COPY FAIL reason=no-config" "$E/out" && ! grep -q "COPY OK" "$E/out"'
  need 'today_ok && only_backup && [ ! -s "$T/api.log" ] && [ ! -e "$E/curl.args" ] && nosecret'
  cend copy-no-config "конфига нет -> COPY FAIL reason=no-config, код 2, запросов нет"

  # copy-http: HTTP 500, HTTP 500 с телом успешного ответа, HTTP 403 с ok=false, HTTP 200 не JSON,
  # закрытый порт
  mk_env copyhttp
  for m in http500 ok500 forbidden garbage closed; do
    if [ "$m" = closed ]; then api_mode ok; api_conf 1; else api_mode "$m"; api_conf; fi
    run_bk
    need '[ "$RC" = 2 ] && grep -q "^BACKUP OK " "$E/out" && grep -qx "COPY FAIL reason=http" "$E/out" && ! grep -q "COPY OK" "$E/out"'
    case "$m" in
      http500 | ok500) need 'grep -qx "COPY HTTP status=500 curl=0 error_code=none" "$E/out"' ;;
      forbidden) need 'grep -qx "COPY HTTP status=403 curl=0 error_code=403" "$E/out"' ;;
      garbage) need 'grep -qx "COPY HTTP status=200 curl=0 error_code=none" "$E/out"' ;;
      closed) need 'grep -qE "^COPY HTTP status=000 curl=[1-9][0-9]* error_code=none\$" "$E/out" && [ ! -s "$T/api.log" ]' ;;
    esac
    need 'today_ok && only_backup && nosecret'
  done
  cend copy-http "HTTP 500, 403 с ok=false, не JSON, закрытый порт -> COPY FAIL reason=http, код 2"

  # copy-mismatch
  mk_env copymm
  api_mode mismatch; api_conf
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK " "$E/out" && grep -qx "COPY FAIL reason=mismatch" "$E/out" && ! grep -q "COPY OK" "$E/out"'
  need 'today_ok && only_backup && [ "$(api_n "sendDocument token=match")" = 1 ] && nosecret'
  cend copy-mismatch "file_size на 1 меньше -> COPY FAIL reason=mismatch, код 2"

  # copy-mark-fail
  mk_env copymk
  api_mode ok; api_conf
  : > "$E/sw.markcopy"
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK " "$E/out" && grep -qx "COPY FAIL reason=mark" "$E/out" && ! grep -q "COPY OK" "$E/out"'
  need 'today_ok && only_backup && [ "$(api_n "sendDocument token=match")" = 1 ] && nosecret'
  cend copy-mark-fail "доставлено, запись backup_copy упала -> COPY FAIL reason=mark, код 2"

  # copy-too-large
  mk_env copybig
  api_mode ok; api_conf
  big $((BACKUP_MAX_BYTES + 1))
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK " "$E/out" && grep -qx "SIZE WARN bytes=$((BACKUP_MAX_BYTES + 1))" "$E/out" && grep -qx "COPY FAIL reason=too-large" "$E/out"'
  need 'today_ok && only_backup && [ ! -e "$E/curl.args" ] && [ ! -s "$T/api.log" ] && nosecret'
  cend copy-too-large "размер BACKUP_MAX_BYTES + 1 -> SIZE WARN, COPY FAIL reason=too-large, curl не вызывался"

  # copy-max-edge
  mk_env copymax
  api_mode ok; api_conf
  big "$BACKUP_MAX_BYTES"
  run_bk
  need '[ "$RC" = 0 ] && grep -qx "SIZE WARN bytes=$BACKUP_MAX_BYTES" "$E/out" && grep -qx "COPY OK size=$BACKUP_MAX_BYTES" "$E/out"'
  need '[ "$(api_n "sendDocument token=match chat_id=$CHAT file=aggregator-2026-10-01.dump.age bytes=$BACKUP_MAX_BYTES ")" = 1 ]'
  need '[ "$(cat "$E/marks.log")" = "backup
backup_copy" ] && today_ok && nosecret'
  cend copy-max-edge "размер ровно BACKUP_MAX_BYTES -> SIZE WARN, COPY OK, код 0"

  # copy-size-warn: ровно порог и на 1 меньше; DECISION size_warn=log — третьей отметки нет
  mk_env copywarn
  api_mode ok; api_conf
  big "$BACKUP_WARN_BYTES"
  run_bk
  cap=$(caption)
  need '[ "$BACKUP_WARN_BYTES" = 40000000 ] && [ "$RC" = 0 ] && grep -qx "SIZE WARN bytes=40000000" "$E/out" && grep -qx "COPY OK size=40000000" "$E/out"'
  need '[ "$(printf "%s\n" "$cap" | wc -l | tr -d " ")" = 2 ] && [ "$(printf "%s\n" "$cap" | sed -n 2p)" = "SIZE WARN bytes=40000000" ]'
  need '[ "$(printf "%s\n" "$cap" | sed -n 1p)" = "aggregator-2026-10-01.dump.age sha256=$(cut -d" " -f1 "$E/dir/aggregator-2026-10-01.dump.age.sha256") size=40000000 counts=$(cat "$E/dir/aggregator-2026-10-01.dump.age.meta")" ]'
  need '[ "$(cat "$E/marks.log")" = "backup
backup_copy" ] && nosecret'
  mk_env copywarn1
  api_mode ok; api_conf
  big $((BACKUP_WARN_BYTES - 1))
  run_bk
  cap=$(caption)
  need '[ "$RC" = 0 ] && ! grep -q "SIZE WARN" "$E/out" && grep -qx "COPY OK size=$((BACKUP_WARN_BYTES - 1))" "$E/out"'
  need '[ "$(printf "%s\n" "$cap" | wc -l | tr -d " ")" = 1 ] && [ "$(cat "$E/marks.log")" = "backup
backup_copy" ] && nosecret'
  if grep -qx 'DECISION size_warn=log' "$SUMMARY01" 2>/dev/null; then
    need '! grep -q "backup_size_warn" "$VPS/backup.sh"' # решение log: вызова отметки размера в коде нет
  else
    PB="$PB
       - DECISION size_warn=log не найдено: при alert нужны вызов mark backup_size_warn и его проверки"
  fi
  cend copy-size-warn "ровно BACKUP_WARN_BYTES -> SIZE WARN в выводе и второй строкой подписи; на 1 меньше — нет"

  # copy-resend: сбой, затем две успешные отправки той же даты
  mk_env copyre
  api_mode http500; api_conf
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "COPY FAIL reason=http" "$E/out"'
  echo ok > "$T/api.mode"
  run_bk
  need '[ "$RC" = 0 ] && grep -q "^COPY OK size=" "$E/out"'
  run_bk
  need '[ "$RC" = 0 ] && grep -q "^COPY OK size=" "$E/out"'
  need '[ "$(grep -c "^sendDocument token=match .* file=aggregator-2026-10-01.dump.age .* reply=200 " "$T/api.log")" = 2 ] && [ "$(api_n sendDocument)" = 3 ]'
  need '[ "$(ls -A "$E/dir" | wc -l | tr -d " ")" = 3 ] && today_ok && nosecret'
  need '[ "$(cat "$E/marks.log")" = "backup
backup
backup_copy
backup
backup_copy" ]'
  cend copy-resend "повторный запуск отправляет копию той же даты заново; локально одна копия"

  # copy-after-dump-fail: шаги 1–4 не прошли — отправки нет
  mk_env copydf
  api_mode ok; api_conf
  echo half > "$E/sw.dump"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=dump" "$E/out" && ! grep -q "COPY" "$E/out" && [ ! -s "$T/api.log" ] && [ ! -e "$E/curl.args" ]'
  mk_env copymf
  api_mode ok; api_conf
  : > "$E/sw.mark"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=mark" "$E/out" && ! grep -q "COPY" "$E/out" && [ ! -s "$T/api.log" ] && [ ! -e "$E/curl.args" ] && [ ! -s "$E/marks.log" ]'
  cend copy-after-dump-fail "сбой дампа или отметки backup -> код 1, строк COPY и запросов нет"

  # copy-after-rotate-abort: ротация остановлена, отправка выполняется
  mk_env copyra
  api_mode ok; api_conf
  mkcopy "$E/dir" 2099-01-01 good
  mkcopy "$E/dir" 2026-08-10 partial
  before=$(names "$E/dir")
  run_bk
  need '[ "$RC" = 3 ] && grep -qx "ROTATE ABORT reason=future-date" "$E/out" && ! grep -q "ROTATE OK" "$E/out" && grep -q "^COPY OK size=" "$E/out"'
  need '[ -z "$(comm -23 <(printf "%s\n" "$before") <(names "$E/dir"))" ] && [ "$(cat "$E/marks.log")" = "backup
backup_copy" ] && nosecret'
  api_mode http500
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE ABORT reason=future-date" "$E/out" && grep -qx "COPY FAIL reason=http" "$E/out"'
  need '[ -z "$(comm -23 <(printf "%s\n" "$before") <(names "$E/dir"))" ] && nosecret'
  cend copy-after-rotate-abort "ROTATE ABORT и удавшаяся отправка -> код 3; с HTTP 500 -> код 2"

  # copy-tmpfile: файл для ответа создать не удалось — curl не запускался
  mk_env copytmp
  api_mode ok; api_conf
  stub "$E/bin" mktemp 'exit 1'
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK " "$E/out" && grep -qx "COPY HTTP status=000 curl=skipped error_code=tmpfile" "$E/out" && grep -qx "COPY FAIL reason=http" "$E/out"'
  need 'today_ok && only_backup && [ ! -e "$E/curl.args" ] && [ ! -s "$T/api.log" ] && nosecret'
  cend copy-tmpfile "нет файла для ответа -> COPY HTTP curl=skipped error_code=tmpfile, COPY FAIL reason=http, curl не вызывался"

  # copy-secret-silence: итог по всем случаям выше
  need '[ "$LEAKS" = 0 ]'
  need '! grep -nE -- "(--trace|--verbose|set -[a-z]*x)" "$VPS/backup.sh"'
  need '[ "$(grep -v "^ *#" "$VPS/backup.sh" | grep -c "curl ")" = 1 ] && grep -v "^ *#" "$VPS/backup.sh" | grep "curl " | grep -q -- "--config \"\$CONF\""'
  cend copy-secret-silence "токен, идентификатор канала и тело ответа не попали в вывод, лог и аргументы curl"
}

# ---- Phase 6, 06-03: backup-status.sh, backup-apply.sh, пакет ------------------
CRONLINE='0 3 * * * root /bin/bash /usr/local/lib/aggregator/backup.sh'
# Имя узла настоящего API собрано из частей: тесты к нему не обращаются, адрес в копиях
# скриптов заменяется на заглушку, а в исходном backup-apply.sh проверяется только константа.
APIHOST="api.tele""gram.org"
# st_env <имя>: окружение mk_env + копия backup-status.sh с путями в $E; по умолчанию всё в
# порядке: две пригодные копии, свежие отметки, часы, cron, файл секретов 600.
st_env() {
  mk_env "$1"
  sed -e "s#^export PATH=.*#export PATH=$E/bin:/usr/bin:/bin:/usr/sbin:/sbin#" \
    -e "s#^L=/usr/local/lib/aggregator\$#L=$E/lib#" \
    -e "s#^DIR=/var/backups/aggregator\$#DIR=$E/dir#" \
    -e "s#^F=/opt/aggregator/docker-compose.yml\$#F=$E/opt/docker-compose.yml#" \
    -e "s#^CRON=/etc/cron.d/aggregator-backup\$#CRON=$E/cron#" \
    -e "s#^CONF=/etc/aggregator-backup/telegram.curl\$#CONF=$E/etc/telegram.curl#" "$VPS/backup-status.sh" > "$E/backup-status.sh"
  if ! grep -q "^L=$E/lib\$" "$E/backup-status.sh" || ! grep -q "^DIR=$E/dir\$" "$E/backup-status.sh" \
    || ! grep -q "^CRON=$E/cron\$" "$E/backup-status.sh" || ! grep -q "^CONF=$E/etc/telegram.curl\$" "$E/backup-status.sh" \
    || ! grep -q "^F=$E/opt/docker-compose.yml\$" "$E/backup-status.sh" || [ "$(grep -c "^export PATH=$E/bin" "$E/backup-status.sh")" != 1 ]; then
    bad "status: не удалось подготовить копию backup-status.sh ($1)"
  fi
  fill_range "$E/dir" 2026-09-30 2026-10-01
  echo "100 200" > "$E/sw.ages"
  printf '%s\n' "$CRONLINE" > "$E/cron"
  echo "секреты не читаются" > "$E/etc/telegram.curl"
  chmod 600 "$E/etc/telegram.curl"
}
run_st() {
  RC=0
  /bin/bash "$E/backup-status.sh" > "$E/out" 2> "$E/err" || RC=$?
}
# st_lines <семь строк состояния через перевод строки>: первые семь строк вывода ровно такие
st_head() { sed -n '1,7p' "$E/out"; }
# tree <каталог>...: имена, права и суммы файлов — для проверки «только чтение»
tree() { for d in "$@"; do ls -lR "$d" | cut -c1-10,30-; find "$d" -type f -exec shasum -a 256 {} + | sort; done; }

t_status() {
  local N L7 before
  N=$((BACKUP_MIN_BYTES + 1000))
  L7="backup_age_s=100
backup_copy_age_s=200
copies=2
latest=aggregator-2026-10-01.dump.age size=$N usable=yes
clock=ok
cron=present
telegram_config=present"
  st_env stok
  run_st
  need '[ "$RC" = 0 ] && [ "$(cat "$E/out")" = "$L7
STATUS OK" ] && [ ! -s "$E/err" ]'
  cend status-ok "семь строк контракта и STATUS OK, код 0"

  st_env stmiss
  echo "100 none" > "$E/sw.ages"
  run_st
  need '[ "$RC" = 1 ] && grep -qx "backup_age_s=100" "$E/out" && grep -qx "backup_copy_age_s=none" "$E/out"'
  need '[ "$(sed -n "8,\$p" "$E/out")" = "WARN backup_copy_age
STATUS WARN" ]'
  cend status-copy-missing "backup свежая, backup_copy нет -> backup_copy_age_s=none, STATUS WARN, код 1"

  st_env ststale
  echo "93601 100" > "$E/sw.ages"
  run_st
  need '[ "$RC" = 1 ] && [ "$(sed -n "8,\$p" "$E/out")" = "WARN backup_age
STATUS WARN" ]'
  echo "93600 93600" > "$E/sw.ages"
  run_st
  need '[ "$RC" = 0 ] && grep -qx "STATUS OK" "$E/out" && ! grep -q "^WARN" "$E/out"'
  cend status-stale "возраст 93601 с -> STATUS WARN; 93600 с — не причина"

  st_env stdb
  : > "$E/sw.agesfail"
  run_st
  need '[ "$RC" = 1 ] && [ "$(st_head)" = "backup_age_s=unknown
backup_copy_age_s=unknown
$(printf "%s\n" "$L7" | sed -n "3,7p")" ]'
  need '[ "$(sed -n "8,\$p" "$E/out")" = "WARN backup_age
WARN backup_copy_age
STATUS WARN" ]'
  echo "мусор; rm -rf /" > "$E/sw.ages"; rm "$E/sw.agesfail"
  run_st
  need '[ "$RC" = 1 ] && grep -qx "backup_age_s=unknown" "$E/out" && grep -qx "backup_copy_age_s=unknown" "$E/out"'
  cend status-db-down "БД недоступна или ответ не по формату -> оба возраста unknown, остальные строки есть, STATUS WARN"

  st_env stbad
  mkcopy "$E/dir" 2026-10-01 badsum
  run_st
  need '[ "$RC" = 1 ] && grep -qx "latest=aggregator-2026-10-01.dump.age size=$N usable=no" "$E/out"'
  need '[ "$(sed -n "8,\$p" "$E/out")" = "WARN usable
STATUS WARN" ]'
  cend status-unusable "у последней копии не сходится SHA-256 -> usable=no, STATUS WARN"

  st_env stsize
  { printf 'age-encryption.org/v1\n'; head -c $((BACKUP_WARN_BYTES - AGEHDR)) /dev/zero | tr '\0' x; } > "$E/dir/aggregator-2026-10-01.dump.age"
  printf '%s  %s\n' "$(_sha256 "$E/dir/aggregator-2026-10-01.dump.age")" aggregator-2026-10-01.dump.age > "$E/dir/aggregator-2026-10-01.dump.age.sha256"
  run_st
  need '[ "$RC" = 1 ] && grep -qx "latest=aggregator-2026-10-01.dump.age size=$BACKUP_WARN_BYTES usable=yes" "$E/out"'
  need '[ "$(sed -n "8,\$p" "$E/out")" = "SIZE WARN bytes=$BACKUP_WARN_BYTES
WARN size
STATUS WARN" ]'
  cend status-size-warn "последняя копия не меньше BACKUP_WARN_BYTES -> SIZE WARN, STATUS WARN"

  # прочие причины: нет копий, часы, cron, файл секретов (нет или права не 600)
  st_env stwarn
  rm "$E"/dir/aggregator-*
  echo no > "$E/clock.seq"
  echo "# пусто" > "$E/cron"
  chmod 644 "$E/etc/telegram.curl"
  run_st
  need '[ "$RC" = 1 ] && [ "$(cat "$E/out")" = "backup_age_s=100
backup_copy_age_s=200
copies=0
latest=none
clock=unsynced
cron=absent
telegram_config=present
WARN copies
WARN clock
WARN cron
WARN telegram_config
STATUS WARN" ]'
  rm "$E/etc/telegram.curl" "$E/cron"
  run_st
  need 'grep -qx "telegram_config=absent" "$E/out" && grep -qx "WARN telegram_config" "$E/out" && grep -qx "cron=absent" "$E/out"'
  cend status-warns "нет копий, часы, cron, файл секретов -> по строке WARN на причину"

  st_env stro
  mkcopy "$E/dir" 2026-08-10 partial
  mkcopy "$E/dir" 2026-08-11 nosum
  before=$(tree "$E/dir" "$E/etc" "$E/lib" "$E/opt"; cat "$E/cron")
  run_st
  need '[ "$(tree "$E/dir" "$E/etc" "$E/lib" "$E/opt"; cat "$E/cron")" = "$before" ]'
  need '[ "$(ncalls docker)" = 1 ] && ! grep -qiE "insert|update|delete|drop|alter|create" "$E/bin/calls.log" && [ ! -s "$E/marks.log" ]'
  need '[ ! -e "$E/log" ] && [ ! -e "$E/lock" ]'
  need '! grep -v "^ *#" "$VPS/backup-status.sh" | grep -nE "(^|[^a-z_])(rm|mv|cp|install|mkdir|chmod|chown|touch|tee|mark|publish|flock)([^a-z_]|\$)|>>|INSERT|UPDATE|DELETE"'
  cend status-readonly "каталоги до и после запуска одинаковы, записи в БД нет"
}

# ap_env <имя>: окружение mk_env + копия backup-apply.sh с путями в $E, без проверки root и
# владельца; адрес API — заглушка. Каталогов копий и секретов и лога ещё нет.
ap_env() {
  mk_env "$1"
  rmdir "$E/etc"
  mkdir -p "$E/var" "$E/crond"
  sed -e "s#^export PATH=.*#export PATH=$E/bin:/usr/bin:/bin:/usr/sbin:/sbin#" \
    -e "s#^DIR=/var/backups/aggregator\$#DIR=$E/var/backups#" \
    -e "s#^CONFDIR=/etc/aggregator-backup\$#CONFDIR=$E/etc#" \
    -e "s#^LOG=/var/log/aggregator-backup.log\$#LOG=$E/var/backup.log#" \
    -e "s#^ENVF=/opt/aggregator/.env\$#ENVF=$E/opt/app.env#" \
    -e "s#^CRON=/etc/cron.d/aggregator-backup\$#CRON=$E/crond/aggregator-backup#" \
    -e "s#^RUN=/usr/local/lib/aggregator/backup.sh\$#RUN=$E/lib/backup.sh#" \
    -e "s#^API=https://$APIHOST\$#API=http://127.0.0.1:$API_PORT#" \
    -e '/\[ "\$(id -u)" -eq 0 \]/d' -e 's/ -o root -g root//g' "$VPS/backup-apply.sh" > "$E/backup-apply.sh"
  if ! grep -q "^DIR=$E/var/backups\$" "$E/backup-apply.sh" || ! grep -q "^CONFDIR=$E/etc\$" "$E/backup-apply.sh" \
    || ! grep -q "^LOG=$E/var/backup.log\$" "$E/backup-apply.sh" || ! grep -q "^ENVF=$E/opt/app.env\$" "$E/backup-apply.sh" \
    || ! grep -q "^CRON=$E/crond/aggregator-backup\$" "$E/backup-apply.sh" || ! grep -q "^RUN=$E/lib/backup.sh\$" "$E/backup-apply.sh" \
    || ! grep -q "^API=http://127.0.0.1:$API_PORT\$" "$E/backup-apply.sh" || grep -q 'id -u\|root -g' "$E/backup-apply.sh" \
    || grep -q "https://\|$APIHOST" "$E/backup-apply.sh" \
    || [ "$(grep -c "^export PATH=$E/bin" "$E/backup-apply.sh")" != 1 ]; then
    bad "apply: не удалось подготовить копию backup-apply.sh ($1)"
  fi
}
# run_ap <режим> <строки stdin>...
run_ap() {
  local mode=$1
  shift
  RC=0
  printf '%s\n' "$@" | /bin/bash "$E/backup-apply.sh" "$mode" > "$E/out" 2> "$E/err" || RC=$?
}
perm() { ls -ld "$1" | cut -c1-10; }
# conf_want: ожидаемое содержимое файла секретов для выдуманных значений
conf_want() { printf 'url = "http://127.0.0.1:%s/bot%s/sendDocument"\nform = "chat_id=%s"\n' "$API_PORT" "$TOK" "$CHAT"; }

t_apply() {
  local KEY c SK c1 c2
  # recipient-format
  KEY=$(cat "$VPS/backup-recipient")
  local key_re='^age1[0-9a-z]{58}$'
  need '[[ $KEY =~ $key_re ]] && [ "$(wc -l < "$VPS/backup-recipient" | tr -d " ")" = 1 ]'
  need 'grep -qx "AGE_RECIPIENT=$KEY" "$SUMMARY01"'
  need 'grep -q "\"\$L/backup-recipient\"" "$VPS/backup.sh"'
  SK="AGE-SECRET""-KEY-" # префикс приватного ключа age; собран из частей, чтобы файл теста его не содержал
  need '! grep -rIq -- "$SK" "$HERE" "$HERE/../cmd" "$HERE/../internal" "$HERE/../migrations"'
  cend recipient-format "backup-recipient — одна строка age1…, равная ключу из 06-01; backup.sh читает её из своего каталога"

  # apply-words: неверное слово — ничего не создано; confirm() дословно как в host-apply.sh
  ap_env apword
  api_mode ok
  run_ap dirs NO
  need '[ "$RC" = 1 ] && [ ! -e "$E/var/backups" ] && [ ! -e "$E/etc" ] && [ ! -e "$E/var/backup.log" ] && ! grep -q "APPLY OK" "$E/out"'
  mkdir -p "$E/etc" "$E/var/backups"
  : > "$E/lib/backup.sh"
  run_ap telegram telegram "$TOK" "$CHAT"
  need '[ "$RC" = 1 ] && [ -z "$(ls -A "$E/etc")" ] && [ ! -s "$T/api.log" ] && ! grep -q "APPLY OK\|TELEGRAM OK" "$E/out" && nosecret'
  run_ap cron cron
  need '[ "$RC" = 1 ] && [ -z "$(ls -A "$E/crond")" ] && ! grep -q "APPLY OK" "$E/out"'
  RC=0; /bin/bash "$E/backup-apply.sh" other > "$E/out" 2> "$E/err" < /dev/null || RC=$?
  need '[ "$RC" = 2 ] && grep -q "^usage: backup-apply.sh dirs|telegram|cron\$" "$E/err"'
  c1=$(sed -n '/^confirm() {/,/^}/p' "$VPS/backup-apply.sh")
  c2=$(sed -n '/^confirm() {/,/^}/p' "$VPS/host-apply.sh")
  need '[ -n "$c1" ] && [ "$c1" = "$c2" ]'
  cend apply-words "неверное слово в каждом режиме -> ничего не создано, код 1"

  # apply-dirs
  ap_env apdirs
  run_ap dirs "DIRS"$'\r'
  need '[ "$RC" = 0 ] && [ "$(perm "$E/var/backups")" = "drwx------" ] && [ "$(perm "$E/etc")" = "drwx------" ] && [ "$(perm "$E/var/backup.log")" = "-rw-------" ]'
  need 'tail -n 1 "$E/out" | grep -q ": APPLY OK mode=dirs\$" && [ ! -s "$E/var/backup.log" ]'
  echo "прежняя строка" > "$E/var/backup.log"
  chmod 644 "$E/var/backup.log"
  run_ap dirs DIRS
  need '[ "$RC" = 0 ] && [ "$(cat "$E/var/backup.log")" = "прежняя строка" ] && [ "$(perm "$E/var/backup.log")" = "-rw-------" ]'
  cend apply-dirs "слово DIRS -> каталоги 700, лог 600, повтор лог не обрезает"

  # apply-telegram (продолжение того же окружения: каталоги уже есть)
  api_mode ok
  run_ap telegram TELEGRAM "$TOK"$'\r' "$CHAT"$'\r'
  need '[ "$RC" = 0 ] && grep -qx "TELEGRAM OK" "$E/out" && [ "$(tail -n 1 "$E/out")" = "APPLY OK mode=telegram" ]'
  need '[ "$(wc -l < "$T/api.log" | tr -d " ")" = 1 ] && [ "$(api_n "getChat token=match chat_id=$CHAT ")" = 1 ]'
  need '[ "$(perm "$E/etc/telegram.curl")" = "-rw-------" ] && [ "$(cat "$E/etc/telegram.curl")" = "$(conf_want)" ] && [ "$(ls -A "$E/etc")" = telegram.curl ]'
  need 'grep -q -- "--config -" "$E/curl.args" && nosecret'
  need '[ "$(wc -l < "$E/curl.ls" | tr -d " ")" = 1 ] && grep -q "^\.getchat\." "$E/curl.ls"' # при getChat в каталоге только файл ответа
  cend apply-telegram "TELEGRAM, токен и идентификатор на stdin -> getChat, файл 600, TELEGRAM OK; значений нет в выводе и аргументах curl"

  # apply-to-backup: конфиг из режима telegram даёт COPY OK в backup.sh
  api_mode ok
  run_bk
  need '[ "$RC" = 0 ] && grep -q "^COPY OK size=" "$E/out" && [ "$(api_n "sendDocument token=match chat_id=$CHAT ")" = 1 ] && nosecret'
  cend apply-to-backup "файл секретов из режима telegram -> COPY OK в backup.sh"

  # apply-telegram-bad: формат токена, формат идентификатора, getChat с ok=false — прежний файл цел
  cp "$E/etc/telegram.curl" "$E/prev.curl"
  api_mode ok
  run_ap telegram TELEGRAM "abc" "$CHAT"
  need '[ "$RC" = 1 ] && grep -q "^STOP: неверный формат токена" "$E/err" && [ ! -s "$T/api.log" ]'
  run_ap telegram TELEGRAM "$TOK" "12ab"
  need '[ "$RC" = 1 ] && grep -q "^STOP: неверный формат идентификатора" "$E/err" && [ ! -s "$T/api.log" ] && nosecret'
  run_ap telegram TELEGRAM "$TOK" "-1009"
  need '[ "$RC" = 1 ] && grep -q "^STOP: getChat не подтвердил доступ бота к каналу (HTTP 400, curl 0)" "$E/err" && [ "$(api_n "getChat token=match chat_id=-1009 ")" = 1 ]'
  need 'cmp -s "$E/etc/telegram.curl" "$E/prev.curl" && [ "$(ls -A "$E/etc")" = telegram.curl ] && ! grep -q "TELEGRAM OK\|APPLY OK" "$E/out" && nosecret'
  rm "$E/etc/telegram.curl"
  api_mode forbidden
  run_ap telegram TELEGRAM "$TOK" "$CHAT"
  need '[ "$RC" = 1 ] && [ -z "$(ls -A "$E/etc")" ] && grep -q "HTTP 403" "$E/err" && nosecret'
  need '! grep -q "^\.telegram\." "$E/curl.ls"' # файл с секретами не создавался ни при одном вызове getChat
  cend apply-telegram-bad "неверный формат или getChat с ok=false -> файла нет (прежний цел), код 1"

  # apply-telegram-project-token
  ap_env approj
  mkdir -p "$E/etc"
  printf 'POSTGRES_PASSWORD=x\nTELEGRAM_BOT_TOKEN=%s\nADMIN_TG_IDS=1\n' "$TOK" > "$E/opt/app.env"
  api_mode ok
  run_ap telegram TELEGRAM "$TOK" "$CHAT"
  need '[ "$RC" = 1 ] && grep -q "^STOP: это токен бота проекта" "$E/err" && [ -z "$(ls -A "$E/etc")" ] && [ ! -s "$T/api.log" ] && nosecret'
  cend apply-telegram-project-token "токен бота проекта -> STOP, файла нет"

  # apply-cron
  ap_env apcron
  mkdir -p "$E/etc" "$E/var/backups"
  : > "$E/lib/backup.sh"
  run_ap cron CRON
  need '[ "$RC" = 0 ] && [ "$(cat "$E/crond/aggregator-backup")" = "0 3 * * * root /bin/bash $E/lib/backup.sh" ] && [ "$(perm "$E/crond/aggregator-backup")" = "-rw-r--r--" ]'
  need '[ "$(ls -A "$E/crond")" = aggregator-backup ] && tail -n 1 "$E/out" | grep -q ": APPLY OK mode=cron\$"'
  # строка контракта: путь запуска в скрипте и строка, которую ищет backup-status.sh
  need 'grep -qx "RUN=/usr/local/lib/aggregator/backup.sh" "$VPS/backup-apply.sh" && grep -qxF "CRON_LINE='"'"'$CRONLINE'"'"'" "$VPS/backup-status.sh"'
  need 'grep -qxF "    LINE=\"0 3 * * * root /bin/bash \$RUN\"" "$VPS/backup-apply.sh"'
  rm "$E/crond/aggregator-backup"
  echo no > "$E/clock.seq"
  run_ap cron CRON
  need '[ "$RC" = 1 ] && grep -q "^STOP: часы не синхронизированы" "$E/err" && [ -z "$(ls -A "$E/crond")" ]'
  echo yes > "$E/clock.seq"
  rm "$E/lib/backup.sh"
  run_ap cron CRON
  need '[ "$RC" = 1 ] && grep -q "^STOP: нет " "$E/err" && [ -z "$(ls -A "$E/crond")" ]'
  cend apply-cron "CRON при синхронизированных часах -> одна строка расписания; часы или backup.sh — STOP"

  need '[ "$(grep -c "read -rs" "$VPS/backup-apply.sh")" = 2 ] && [ "$(grep -c "$APIHOST" "$VPS/backup-apply.sh")" = 1 ] && grep -qx "API=https://$APIHOST" "$VPS/backup-apply.sh"'
  need '! grep -nE -- "(--trace|--verbose|set -[a-z]*x)" "$VPS/backup-apply.sh"'
  cend apply-static "два скрытых ввода, одна строка API=, без подробного вывода"
}

# decided <точная строка решения 06-01>
decided() { grep -qx -- "$1" "$SUMMARY01" 2>/dev/null; }

t_pkg() {
  local f n ONE LIBW LIBI SUDO m W c x y z u o
  ONE=$("$HERE/oneliner.sh" testalias)
  # pkg-names
  n=0
  local pkg_re='^[a-z-]+(\.sh|\.yml)?$'
  for f in $("$HERE/oneliner.sh" files); do
    n=$((n + 1))
    need '[ -f "$HERE/../$f" ] && [[ ${f##*/} =~ $pkg_re ]]'
  done
  need '[ "$n" = 17 ] && [ "$("$HERE/oneliner.sh" sums | wc -l | tr -d " ")" = 17 ]'
  cend pkg-names "17 файлов пакета существуют, имена по шаблону"

  # pkg-lib: список LIB= из agg-install.sh на SHA256SUMS пакета равен набору lib из installed
  mkdir -p "$T/pkg"
  "$HERE/oneliner.sh" sums > "$T/pkg/SHA256SUMS"
  LIBW=$(cd "$T/pkg" && eval "$(grep '^LIB=' "$VPS/agg-install.sh")" && printf '%s\n' $LIB | sort)
  LIBI=$("$HERE/oneliner.sh" installed | awk '{print $2}' | sed -n 's#^/usr/local/lib/aggregator/##p' | sort)
  need '[ -n "$LIBW" ] && [ "$LIBW" = "$LIBI" ] && [ "$(grep -c "^LIB=" "$VPS/agg-install.sh")" = 1 ]'
  need '[ "$("$HERE/oneliner.sh" installed | wc -l | tr -d " ")" = 13 ] && "$HERE/oneliner.sh" installed | grep -q "  /usr/local/sbin/agg\$"'
  need '! printf "%s\n" "$LIBW" | grep -q "backup-apply"'
  for f in backup.sh backup-lib.sh backup-status.sh backup-recipient; do need 'printf "%s\n" "$LIBW" | grep -qx "$f"'; done
  cend pkg-lib "LIB= и installed совпадают; backup-apply.sh в lib нет, скрипты бэкапа и ключ есть"

  # pkg-sudoers
  SUDO=$(grep '^runagg ALL=' "$VPS/agg-install.sh")
  W='runagg ALL=(root) NOPASSWD: $A load *, $A preflight *, $A deploy *, $A env-init, $A verify, $A rollback, $A host-preflight, $A host-verify, $A backup-status'
  if decided 'DECISION backup_run=agg'; then W="$W, \$A backup-run"; fi
  need '[ "$SUDO" = "$W" ]'
  cend pkg-sudoers "строка sudoers: прежние восемь записей и backup-status без аргументов"

  # pkg-agg
  mk_env pkgagg
  sed -e "s#^L=/usr/local/lib/aggregator\$#L=$E/lib#" -e '/\[ "\$(id -u)" -eq 0 \]/d' "$VPS/agg" > "$E/agg"
  need 'grep -q "^L=$E/lib\$" "$E/agg" && ! grep -q "id -u" "$E/agg"'
  printf '%s\n' 'echo "STATUS STUB args=$#"' > "$E/lib/backup-status.sh"
  printf '%s\n' 'echo "APPLY STUB"' > "$E/lib/backup-apply.sh"
  RC=0; /bin/bash "$E/agg" --clean-env backup-status > "$E/out" 2> "$E/err" || RC=$?
  need '[ "$RC" = 0 ] && [ "$(cat "$E/out")" = "STATUS STUB args=0" ]'
  RC=0; /bin/bash "$E/agg" --clean-env backup-status extra > "$E/out" 2> "$E/err" || RC=$?
  need '[ "$RC" = 2 ] && [ ! -s "$E/out" ]'
  for m in backup-apply backup-apply.sh backup nope; do
    RC=0; /bin/bash "$E/agg" --clean-env "$m" > "$E/out" 2> "$E/err" || RC=$?
    need '[ "$RC" = 2 ] && [ ! -s "$E/out" ]'
  done
  need '! grep -q "backup-apply" "$VPS/agg"'
  cend pkg-agg "agg backup-status запускает backup-status.sh; лишний аргумент и backup-apply -> код 2"

  # pkg-optional: необязательные части есть ровно там, где их выбрал владелец в 06-01
  c=no; x=no; y=no; z=no
  if decided 'DECISION backup_run=agg'; then c=yes; fi
  grep -q 'backup-run' "$VPS/agg" && x=yes
  grep -q 'backup-run' "$VPS/agg-install.sh" && y=yes
  printf '%s\n' "$ONE" | grep -q 'backup-run' && z=yes
  need '[ "$x" = "$c" ] && [ "$y" = "$c" ] && [ "$z" = "$c" ]'
  for m in age curl clock; do
    case "$m" in
      age) W='DECISION age=install' ;;
      curl) W='DECISION curl=install' ;;
      clock) W='DECISION clock=(ntp-server|chrony)' ;;
    esac
    c=no; x=no; y=no; z=no; u=no; o=no
    if grep -qxE "$W" "$SUMMARY01"; then c=yes; fi
    grep -qE "^  $m\\)\$" "$VPS/host-apply.sh" && x=yes                                   # ветка case
    grep -E '^  [a-z|]+\) ;;$' "$VPS/host-apply.sh" | grep -qE "[ |]$m[|)]" && y=yes      # список режимов
    grep -E 'usage: host-apply.sh ' "$VPS/host-apply.sh" | grep -qE "[ |]$m[|\"]" && u=yes # usage
    grep -qE "^ +confirm $(printf %s "$m" | tr a-z A-Z)\$" "$VPS/host-apply.sh" && z=yes   # слово
    printf '%s\n' "$ONE" | grep -q "then bash host-apply.sh $m; else" && o=yes             # однострочник
    need '[ "$x" = "$c" ] && [ "$y" = "$c" ] && [ "$u" = "$c" ] && [ "$z" = "$c" ] && [ "$o" = "$c" ]'
  done
  cend pkg-optional "backup-run и режимы age, curl, clock — только выбранные в 06-01, согласованно в case, usage, слове и однострочниках"

  # host-words: режимы host-apply.sh на заглушках
  mk_env hostw
  sed -e '/\[ "\$(id -u)" -eq 0 \]/d' "$VPS/host-apply.sh" > "$E/host-apply.sh"
  need '! grep -q "id -u" "$E/host-apply.sh"'
  for f in apt-get apt-cache apt-config unattended-upgrade age; do stub "$E/bin" "$f" ':'; done
  stub "$E/bin" docker ':'
  hw() { : > "$E/bin/calls.log"; RC=0; printf '%s\n' "$2" | PATH="$E/bin:/usr/bin:/bin" /bin/bash "$E/host-apply.sh" "$1" > "$E/out" 2> "$E/err" || RC=$?; }
  if decided 'DECISION age=install'; then
    hw age age
    need '[ "$RC" = 1 ] && [ "$(cat "$E/bin/calls.log")" = "apt-cache policy age" ] && ! grep -q "HOST APPLY" "$E/out"'
    hw age "AGE"$'\r'
    need '[ "$RC" = 0 ] && [ "$(cat "$E/bin/calls.log")" = "apt-cache policy age
apt-get update
apt-get install -y --no-install-recommends age
age --version" ] && tail -n 1 "$E/out" | grep -q ": HOST APPLY age DONE\$"'
  fi
  hw compose NO
  need '[ "$RC" = 1 ] && [ ! -s "$E/bin/calls.log" ]'
  hw compose COMPOSE
  need '[ "$RC" = 0 ] && [ "$(cat "$E/bin/calls.log")" = "apt-get update
apt-get install -y --no-install-recommends docker-compose-v2
docker compose version" ] && tail -n 1 "$E/out" | grep -q ": HOST APPLY compose DONE\$"'
  hw updates NO
  need '[ "$RC" = 1 ] && [ "$(cat "$E/bin/calls.log")" = "apt-config dump" ]'
  hw updates UPDATES
  need '[ "$RC" = 0 ] && [ "$(cat "$E/bin/calls.log")" = "apt-config dump
apt-get update
unattended-upgrade -v" ] && [ "$(tail -n 1 "$E/out")" = "HOST APPLY updates DONE" ]'
  RC=0; PATH="$E/bin:/usr/bin:/bin" /bin/bash "$E/host-apply.sh" nope > "$E/out" 2> "$E/err" < /dev/null || RC=$?
  need '[ "$RC" = 2 ] && grep -q "^usage: host-apply.sh " "$E/err"'
  cend host-words "неверное слово — пакеты не ставятся; верное — ожидаемые вызовы; updates и compose как прежде"

  # oneliner-lines
  for m in dirs telegram cron; do
    need 'printf "%s\n" "$ONE" | grep "sha256sum -c --quiet SHA256SUMS; then bash backup-apply.sh $m; else echo .STOP: HASH MISMATCH.; fi" | grep -q "^T=\$(mktemp -d)"'
  done
  need 'printf "%s\n" "$ONE" | grep -q "^/bin/bash /usr/local/lib/aggregator/backup.sh; echo \"exit=\$?\""'
  need 'printf "%s\n" "$ONE" | grep -qF "sudo -n /usr/local/sbin/agg backup-status'"'"' 2>&1 | tee \"\$HOME/run-agg-phase6/backup-status.log\""'
  # строка backup-status на чистой машине: каталога логов нет, ssh — заглушка (сети нет)
  mk_env onel
  mkdir "$E/home"
  stub "$E/bin" ssh 'echo "STATUS STUB"'
  f=$(printf '%s\n' "$ONE" | sed -n 's/^backup-status: *//p')
  need '[ -n "$f" ] && [ "$(printf "%s\n" "$f" | wc -l | tr -d " ")" = 1 ]'
  RC=0; HOME="$E/home" PATH="$E/bin:/usr/bin:/bin" /bin/bash -c "$f" > "$E/out" 2> "$E/err" || RC=$?
  need '[ "$RC" = 0 ] && [ "$(cat "$E/home/run-agg-phase6/backup-status.log")" = "STATUS STUB" ] && [ "$(perm "$E/home/run-agg-phase6")" = "drwx------" ]'
  need '[ "$(cat "$E/bin/calls.log")" = "ssh testalias sudo -n /usr/local/sbin/agg backup-status" ]'
  need 'printf "%s\n" "$ONE" | grep -q "^host-verify:    ssh testalias "' # прежние строки без mkdir
  need 'printf "%s\n" "$ONE" | grep -qF "tee \"\$HOME/run-agg-phase4/host-verify.log\""'
  need '! printf "%s\n" "$ONE" | grep -qE "[0-9]{6,}:[A-Za-z0-9_-]{30,}|([0-9]{1,3}\.){3}[0-9]{1,3}|api\.telegram"'
  cend oneliner-lines "однострочники бэкапа с проверкой хеша, ручной запуск, agg backup-status с логом в run-agg-phase6"
}

t_tracer() {
  local rc f N npg pkg_re='^[a-z-]+(\.sh|\.yml)?$'
  mk_env tracer
  f=$E/dir/aggregator-2026-10-01.dump.age
  # mark: два литерала (S4), посторонний компонент и пустая F — код 2 без обращения к БД
  rc=0; (F=$E/opt/docker-compose.yml; PATH="$E/bin:$PATH"; mark postgres) >/dev/null 2>&1 || rc=$?
  need '[ "$rc" = 2 ]'; need '[ ! -s "$E/marks.log" ]'
  rc=0; (F=; PATH="$E/bin:$PATH"; mark backup) >/dev/null 2>&1 || rc=$?
  need '[ "$rc" = 2 ]'; need '[ ! -s "$E/marks.log" ]'
  rc=0; (F=$E/opt/docker-compose.yml; PATH="$E/bin:$PATH"; mark backup_size_warn) >/dev/null 2>&1 || rc=$?
  if grep -qx 'DECISION size_warn=alert' "$SUMMARY01" 2>/dev/null; then
    PB="$PB
       - DECISION size_warn=alert: в mark нужна третья компонента backup_size_warn и её проверка"
  else
    need '[ "$rc" = 2 ]'; need '[ ! -s "$E/marks.log" ]' # DECISION size_warn=log: третьей компоненты нет
  fi
  rc=0; (F=$E/opt/docker-compose.yml; PATH="$E/bin:$PATH"; mark backup_copy) >/dev/null 2>&1 || rc=$?
  need '[ "$rc" = 0 ]'; need '[ "$(cat "$E/marks.log")" = backup_copy ]'
  : > "$E/marks.log"
  report "tracer: mark принимает только backup и backup_copy"

  api_mode ok; api_conf
  run_bk
  N=$((AGEHDR + BACKUP_MIN_BYTES + 1000))
  need '[ "$RC" = 0 ]' # S2: обе отметки, ротация выполнена
  need 'grep -qx "COPY OK size=$N" "$E/out"'
  need '[ -f "$f" ] && [ -f "$f.sha256" ] && [ -f "$f.meta" ]'
  need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  need 'usable "$f"'
  need 'sum_ok "$E/dir" "aggregator-2026-10-01.dump.age"'
  need '[ "$(cut -c1-10 < <(ls -l "$f"))" = "-rw-------" ]'
  need '[ "$(cat "$f.meta")" = "events_listed=5 users=2 events_merged=0 dedup_candidates=pending:0|confirmed:0|rejected:0 source_alerts=0 heartbeats=0 migration=4" ]'
  need '[ "$(cat "$E/marks.log")" = "backup
backup_copy" ]'
  need 'grep -q -- "-r $TESTKEY" "$E/age.log"'
  need 'grep -qx "BACKUP OK file=aggregator-2026-10-01.dump.age size=$N" "$E/out"'
  need 'grep -qx "ROTATE OK keep=1 delete=0" "$E/out"'
  need 'grep -q "BACKUP OK" "$E/log"'
  need '[ -z "$(leak)" ]'
  need '[ "$(calls "date -u +%F")" = 1 ]' # дата взята один раз
  need '[[ backup.sh =~ $pkg_re ]] && [[ backup-lib.sh =~ $pkg_re ]]'
  report "tracer: дамп -> .partial -> проверка -> имя -> .sha256/.meta -> backup -> ротация -> отправка -> backup_copy, код 0, открытого текста нет"
  npg=$(grep -v '^ *#' "$VPS/backup.sh" | grep -c 'pg_dump' || true)
  need '[ "$npg" = 1 ]'
  need '[ "$(grep -v "^ *#" "$VPS/backup.sh" | grep -c "mark backup\$")" = 1 ]'
  need '[ "$(grep -v "^ *#" "$VPS/backup.sh" | grep -c "ROTATE_ABORTED=1")" -ge 1 ]'
  need '[ "$(grep -v "^ *#" "$VPS/backup.sh" | grep -c "mark backup_copy")" = 1 ]'
  report "tracer: дамп только в конвейере, по одному вызову отметок backup и backup_copy"
}

t_iso_oracle() {
  local d x rc out n start
  if ! command -v python3 >/dev/null 2>&1; then bad "iso-oracle: нужен python3 (оракул)"; return; fi
  python3 -c '
import datetime
d = datetime.date(2020, 1, 1)
while d <= datetime.date(2040, 12, 31):
    c = d.isocalendar()
    print(d.isoformat(), "%04d-W%02d" % (c[0], c[1]))
    d += datetime.timedelta(days=1)' > "$T/iso.pairs"
  cut -d' ' -f1 "$T/iso.pairs" > "$T/iso.dates"
  cut -d' ' -f2 "$T/iso.pairs" > "$T/iso.want"
  start=$SECONDS
  while read -r d; do iso_week "$d"; done < "$T/iso.dates" > "$T/iso.got"
  n=$(wc -l < "$T/iso.got" | tr -d ' ')
  need 'cmp -s "$T/iso.got" "$T/iso.want"'
  need '[ "$n" = 7671 ]'
  need '[ $((SECONDS - start)) -le 20 ]'
  need '[ "$(PATH=/nonexistent; iso_week 2026-10-01)" = 2026-W40 ]' # без внешних команд
  for x in "2020-12-31 2020-W53" "2021-01-03 2020-W53" "2021-01-04 2021-W01" "2024-12-30 2025-W01" \
    "2026-12-31 2026-W53" "2027-01-03 2026-W53" "2027-01-04 2027-W01" "2024-02-29 2024-W09"; do
    need '[ "$(iso_week "${x% *}")" = "${x#* }" ]'
  done
  for x in 2026-02-31 2026-13-01 2026-00-10 2025-02-29 26-1-1 2026-04-31 "" abc; do
    rc=0; out=$(iso_week "$x" 2>/dev/null) || rc=$?
    need '[ "$rc" != 0 ] && [ -z "$out" ]'
  done
  report "iso-oracle: $n дат"
}

# rs <TODAY> <ДАТА:u|b|другое>...: запуск retention_select; RS_IN — ввод, RS_OUT — вывод, RS_RC — код.
rs() {
  local t=$1 a st
  shift
  RS_IN=""
  for a in "$@"; do
    case "${a#*:}" in u) st=usable ;; b) st=bad ;; *) st=${a#*:} ;; esac
    RS_IN="$RS_IN${a%%:*}"$'\t'"$st"$'\n'
  done
  RS_RC=0
  RS_OUT=$(printf '%s' "$RS_IN" | retention_select "$t" 2> "$T/rs.err") || RS_RC=$?
}
rs_set() { printf '%s\n' "$RS_OUT" | sed -n "s/^$1 //p" | sort | tr '\n' ' '; }
# srt <даты через пробел>: отсортированный список в том же виде, что rs_set
srt() { printf '%s\n' $1 | sed '/^$/d' | sort | tr '\n' ' '; }
# inv: инварианты вывода с кодом 0
inv() {
  local newest all us k dl
  k=$(printf '%s\n' "$RS_OUT" | sed -n 's/^keep //p' | sort)
  dl=$(printf '%s\n' "$RS_OUT" | sed -n 's/^delete //p' | sort)
  newest=$(printf '%s' "$RS_IN" | awk -F'\t' '$2 == "usable" {print $1}' | sort | tail -1)
  all=$(printf '%s' "$RS_IN" | cut -f1 | sort)
  us=$(printf '%s' "$RS_IN" | awk -F'\t' '$2 == "usable" {print $1}' | sort)
  need '[ "$(printf "%s\n" "$k" | grep -c .)" -le 11 ]'
  need 'printf "%s\n" "$k" | grep -qx "$newest"'
  need '[ -z "$(comm -12 <(printf "%s\n" "$k") <(printf "%s\n" "$dl"))" ]'
  need '[ -z "$(comm -13 <(printf "%s\n" "$all") <(printf "%s\n" "$dl") | grep .)" ]'
  need '[ -z "$(comm -13 <(printf "%s\n" "$us") <(printf "%s\n" "$k") | grep .)" ]'
}
RS_N=0
# rs_chk <имя> <код> <keep> <delete> [условие]: код возврата, множества keep/delete и инварианты;
# при ненулевом коде вывод обязан быть пустым. Тишина при успехе, итог — в t_retention.
rs_chk() {
  local name=$1 wrc=$2 wkeep=${3:-} wdel=${4:-} wext=${5:-}
  RS_N=$((RS_N + 1))
  need '[ "$RS_RC" = "$wrc" ]'
  if [ "$wrc" = 0 ]; then
    need '[ "$(rs_set keep)" = "$(srt "$wkeep")" ]'
    need '[ "$(rs_set delete)" = "$(srt "$wdel")" ]'
    inv
  else
    need '[ -z "$RS_OUT" ]'
  fi
  if [ -n "$wext" ]; then need "$wext"; fi
  if [ -n "$PB" ]; then bad "retention: набор $name$PB"; PB=""; fi
}

t_retention() {
  local all60 RS_FAILS0=$FAILS
  all60=$(dates_between 2026-08-03 2026-10-01)
  # R1: подряд 60 дней — A5 (DECISION a5=confirmed): неделя с ежедневным представителем пропущена
  rs 2026-10-01 $(printf '%s:u ' $all60)
  rs_chk R1 0 "2026-09-25 2026-09-26 2026-09-27 2026-09-28 2026-09-29 2026-09-30 2026-10-01 2026-09-20 2026-09-13 2026-09-06 2026-08-30" \
    "$(printf '%s\n' $all60 | grep -vxE '2026-09-(25|26|27|28|29|30)|2026-10-01|2026-09-20|2026-09-13|2026-09-06|2026-08-30' | tr '\n' ' ')" \
    '[ "$(printf "%s\n" "$RS_OUT" | grep -c "^delete ")" = 49 ]'
  # R2: пропуски дней
  rs 2026-10-01 2026-10-01:u 2026-09-29:u 2026-09-26:u 2026-09-20:u 2026-09-19:u 2026-09-12:u 2026-09-05:u \
    2026-08-29:u 2026-08-22:u 2026-08-15:u 2026-08-08:u 2026-08-01:u 2026-07-25:u
  rs_chk R2 0 "2026-10-01 2026-09-29 2026-09-26 2026-09-20 2026-09-19 2026-09-12 2026-09-05 2026-08-29 2026-08-22 2026-08-15 2026-08-08" \
    "2026-08-01 2026-07-25"
  # R3: граница года и ISO-недели (2026-W53)
  rs 2027-01-10 2027-01-04:u 2027-01-05:u 2027-01-06:u 2027-01-07:u 2027-01-08:u 2027-01-09:u 2027-01-10:u \
    2027-01-02:u 2026-12-29:u 2026-12-27:u 2026-12-20:u 2026-12-13:u 2026-12-06:u
  rs_chk R3 0 "2027-01-04 2027-01-05 2027-01-06 2027-01-07 2027-01-08 2027-01-09 2027-01-10 2027-01-02 2026-12-27 2026-12-20 2026-12-13" \
    "2026-12-29 2026-12-06"
  # R4: меньше 7 копий
  rs 2026-10-01 2026-10-01:u 2026-09-30:u 2026-09-01:u
  rs_chk R4 0 "2026-10-01 2026-09-30 2026-09-01" ""
  # R5: все непригодны (защита 2) и пустой ввод — код 2, вывод пуст
  rs 2026-10-01 2026-09-30:b 2026-09-29:b
  rs_chk R5a 2
  rs 2026-10-01
  rs_chk R5b 2
  # R6: одна пригодная (защита 3)
  rs 2026-10-01 2026-09-20:u 2026-09-10:b 2026-09-22:b 2026-09-25:b
  rs_chk R6 0 "2026-09-20" "2026-09-10"
  # R7: возраст непригодной — удаляется только старше 7 дней
  rs 2026-10-01 2026-10-01:u 2026-09-30:b 2026-09-24:b 2026-09-23:b
  rs_chk R7 0 "2026-10-01" "2026-09-23"
  # R8: будущая дата (защита 4), пригодная или нет — код 4, вывод пуст
  rs 2026-10-01 2026-10-01:u 2026-10-02:b
  rs_chk R8a 4
  rs 2026-10-01 2026-10-01:u 2099-01-01:u
  rs_chk R8b 4
  # R10: unverified не попадает ни в keep, ни в delete и не считается пригодной; будущая — код 4
  rs 2026-10-01 2026-10-01:u 2026-08-01:unverified 2026-08-02:b 2026-09-30:unverified
  rs_chk R10a 0 "2026-10-01" "2026-08-02"
  rs 2026-10-01 2026-08-01:unverified 2026-08-02:b
  rs_chk R10b 2
  rs 2026-10-01 2026-10-01:u 2026-10-02:unverified
  rs_chk R10c 4
  # R9: неверные строки пропускаются с предупреждением; дубль даты тоже; неверный TODAY — код 2
  rs 2026-10-01 2026-10-01:u 2026-02-31:u 2026-09-30:maybe 2026-09-29:u
  rs_chk R9a 0 "2026-10-01 2026-09-29" "" '[ "$(grep -c "пропущена" "$T/rs.err")" = 2 ]'
  rs 2026-10-01 2026-10-01:u 2026-10-01:b 2026-09-29:u
  rs_chk R9b 0 "2026-10-01 2026-09-29" "" '[ "$(grep -c "пропущена" "$T/rs.err")" = 1 ]'
  rs 2026-13-01 2026-10-01:u
  rs_chk R9c 2
  if [ "$FAILS" -eq "$RS_FAILS0" ]; then ok "retention: $RS_N наборов"; fi
}

# ---- pg: одноразовый PostgreSQL 17 (только с аргументом pg) -----------------
dc() { docker compose -f "$PROBE_FILE" "$@"; }
t_pg() {
  local esz ssz pgv line rc1 rc2 cnt_re='^[a-z_]+=[0-9a-z:|]+( [a-z_]+=[0-9a-z:|]+)*$' mig_re=' migration=[0-9]+$'
  if ! docker info >/dev/null 2>&1; then bad "pg: Docker не запущен"; return; fi
  # Копия docker-compose.test.yml с другим именем проекта и портом: контейнер и сеть свои,
  # существующие тестовые контейнеры не затрагиваются.
  sed -e 's/^name: aggregator-test$/name: backup-probe-test/' -e 's/127\.0\.0\.1:5433:5432/127.0.0.1:55433:5432/' \
    "$HERE/../docker-compose.test.yml" > "$T/probe.yml"
  if ! grep -q '^name: backup-probe-test$' "$T/probe.yml" || ! grep -q '127\.0\.0\.1:55433:5432' "$T/probe.yml"; then
    bad "pg: не удалось подготовить compose пробы"; return
  fi
  PROBE_FILE=$T/probe.yml
  if ! dc up -d --wait >"$T/probe.log" 2>&1; then bad "pg: контейнер не поднялся: $(tail -3 "$T/probe.log")"; return; fi
  dc exec -T postgres dropdb -U aggregator --if-exists backup_probe_test >/dev/null
  dc exec -T postgres createdb -U aggregator backup_probe_test
  if ! (cd "$HERE/.." && DATABASE_URL='postgres://aggregator@127.0.0.1:55433/backup_probe_test?sslmode=disable' \
    go run ./cmd/aggregator migrate) >"$T/migrate.log" 2>&1; then
    bad "pg: миграции не применились: $(tail -3 "$T/migrate.log")"; return
  fi
  pgv=$(dc exec -T postgres psql -U aggregator -d backup_probe_test -Atc 'SHOW server_version')
  pgv=${pgv%% *}
  esz=$(dc exec -T postgres pg_dump -U aggregator -d backup_probe_test -Fc | wc -c | tr -d ' ')
  dc exec -T postgres psql -U aggregator -d backup_probe_test -v ON_ERROR_STOP=1 -q -c \
    "INSERT INTO events (title, title_norm, start_date, country_code, event_type)
     SELECT 'Probe event ' || g, 'probe event ' || g, DATE '2027-01-01' + (g % 365), 'RU', 'road' FROM generate_series(1, 3000) g" >/dev/null
  ssz=$(dc exec -T postgres pg_dump -U aggregator -d backup_probe_test -Fc | wc -c | tr -d ' ')
  echo "PG DUMP empty=$esz seeded=$ssz pg=$pgv"
  need '[ "$esz" -gt 0 ]'
  need '[ "$ssz" -gt "$esz" ]'
  need '[ "$BACKUP_MIN_BYTES" -le "$esz" ]'
  need '[ $((2 * BACKUP_MIN_BYTES)) -ge "$esz" ]'
  need '[ $((BACKUP_MIN_BYTES % 1000)) = 0 ]'
  report "pg: BACKUP_MIN_BYTES=$BACKUP_MIN_BYTES по дампу пустой схемы (empty=$esz, seeded=$ssz)"
  # counters_sql настоящим psql: одна строка по формату S6
  line=$(dc exec -T postgres psql -U aggregator -d backup_probe_test -Atc "$(counters_sql)")
  need '[ "$(printf "%s\n" "$line" | wc -l | tr -d " ")" = 1 ]'
  need '[ "${#line}" -le 400 ]'
  need '[[ $line =~ $cnt_re ]]'
  need '[[ $line == events_listed=* ]] && [[ $line == *" users="* ]] && [[ $line =~ $mig_re ]]'
  report "pg: counters_sql на настоящей БД: $line"
  # A4: код выхода команды в контейнере доходит до хоста через docker compose exec -T
  rc1=0; dc exec -T postgres pg_dump -U aggregator -d no_such_db_test -Fc 2>/dev/null | cat >/dev/null || rc1=$?
  rc2=0; dc exec -T postgres sh -c 'head -c 100000 /dev/zero; exit 7' | cat > "$T/a4.out" || rc2=$?
  if [ "$rc1" != 0 ] && [ "$rc2" = 7 ] && [ "$(wc -c < "$T/a4.out" | tr -d ' ')" = 100000 ]; then
    echo "A4 OK"
  else
    echo "A4 FAIL pg_dump_missing_db=$rc1 stream_exit7=$rc2"; FAILS=$((FAILS + 1))
  fi
}

SC_N=0
# scn <имя>: конец сценария — провал печатается, успех молчит (итог раздела — ok-строка с числом)
scn() { SC_N=$((SC_N + 1)); if [ -n "$PB" ]; then bad "$1$PB"; PB=""; fi; }
# names <каталог>: отсортированный список всех имён в каталоге
names() { ls -A "$1" | sort; }
# has_all <каталог> <дата>...: у каждой даты есть копия, .sha256 и .meta
has_all() { local dir=$1 d; shift; for d in "$@"; do [ -f "$dir/aggregator-$d.dump.age" ] && [ -f "$dir/aggregator-$d.dump.age.sha256" ] && [ -f "$dir/aggregator-$d.dump.age.meta" ] || return 1; done; }
# has_none <каталог> <дата>...: ни одного из пяти имён даты нет
has_none() {
  local dir=$1 d s
  shift
  for d in "$@"; do
    for s in "" .sha256 .meta .partial .partial.sha256 .partial.meta; do
      if [ -e "$dir/aggregator-$d.dump.age$s" ] || [ -L "$dir/aggregator-$d.dump.age$s" ]; then return 1; fi
    done
  done
}

t_rotate_dir() {
  local d f keep r1 gone sc0=$SC_N f0=$FAILS before
  SC_N=0
  keep="2026-09-25 2026-09-26 2026-09-27 2026-09-28 2026-09-29 2026-09-30 2026-10-01 2026-09-20 2026-09-13 2026-09-06 2026-08-30"
  # A: 59 пригодных копий и чужие объекты (защиты 1, 3, 5)
  mk_env rotA
  fill_range "$E/dir" 2026-08-03 2026-09-30
  : > "$E/dir/notes.txt"
  : > "$E/dir/aggregator-latest.dump.age"
  : > "$E/dir/aggregator-2026-08-05.dump.age.bak"
  mkcopy "$E/dir" 2026-02-31 good
  mkdir "$E/dir/aggregator-2026-08-06.dump.age.d"
  echo outside > "$E/aggregator-2026-08-04.dump.age"
  run_bk
  gone=$(dates_between 2026-08-03 2026-10-01 | grep -vxE '2026-09-(25|26|27|28|29|30)|2026-10-01|2026-09-20|2026-09-13|2026-09-06|2026-08-30' | tr '\n' ' ')
  need '[ "$RC" = 2 ]'
  need 'grep -qx "ROTATE OK keep=11 delete=49" "$E/out"'
  need 'has_all "$E/dir" $keep'
  need 'has_none "$E/dir" $gone'
  need '[ "$(ls -A "$E/dir" | wc -l | tr -d " ")" = 40 ]' # 11 x 3 + 7 чужих объектов
  need '[ -f "$E/dir/notes.txt" ] && [ -f "$E/dir/aggregator-latest.dump.age" ] && [ -f "$E/dir/aggregator-2026-08-05.dump.age.bak" ]'
  need '[ -f "$E/dir/aggregator-2026-02-31.dump.age" ] && [ -d "$E/dir/aggregator-2026-08-06.dump.age.d" ]'
  need '[ "$(cat "$E/aggregator-2026-08-04.dump.age")" = outside ]'
  need '[ "$(cat "$E/marks.log")" = backup ]'
  scn "rotate-dir: A 59 копий и чужие объекты"
  # B1: старый одиночный .partial удаляется (нет пригодной копии той же даты, есть более новая)
  mk_env rotB1
  mkcopy "$E/dir" 2026-08-10 partial
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE OK keep=1 delete=1" "$E/out"'
  need 'has_none "$E/dir" 2026-08-10'
  scn "rotate-dir: B1 одиночный старый .partial удалён"
  # B2: целые временные данные рядом с копией той же даты, сумма и метаданные уже под
  # итоговыми именами (прерванная публикация перед rename данных) — публикация доводится
  mk_env rotB2
  mkcopy "$E/dir" 2026-09-29 good
  cp "$T/tpl.age" "$E/dir/aggregator-2026-09-29.dump.age.partial"
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE OK keep=2 delete=0" "$E/out"'
  need 'grep -qx "RECOVER OK file=aggregator-2026-09-29.dump.age" "$E/out"'
  need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  need 'usable "$E/dir/aggregator-2026-09-29.dump.age" && sum_ok "$E/dir" aggregator-2026-09-29.dump.age'
  scn "rotate-dir: B2 прерванная публикация рядом с копией доведена"
  # C: ссылка с именем копии удаляется, цель вне каталога цела
  mk_env rotC
  echo victim > "$E/outside.txt"
  ln -s "$E/outside.txt" "$E/dir/aggregator-2026-08-07.dump.age"
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE OK keep=1 delete=1" "$E/out"'
  need '[ ! -L "$E/dir/aggregator-2026-08-07.dump.age" ] && [ ! -e "$E/dir/aggregator-2026-08-07.dump.age" ]'
  need '[ "$(cat "$E/outside.txt")" = victim ]'
  scn "rotate-dir: C ссылка удалена, цель цела"
  # D: копия из будущего (защита 4) — ротация прервана, ничего не удалено
  mk_env rotD
  fill_range "$E/dir" 2026-09-10 2026-09-24
  mkcopy "$E/dir" 2099-01-01 good
  mkcopy "$E/dir" 2026-08-10 partial
  before=$(names "$E/dir")
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE ABORT reason=future-date" "$E/out" && ! grep -q "ROTATE OK" "$E/out"'
  need '[ -z "$(comm -23 <(printf "%s\n" "$before") <(names "$E/dir"))" ]' # ни одно прежнее имя не пропало
  need '[ "$(cat "$E/marks.log")" = backup ]'
  scn "rotate-dir: D дата 2099-01-01 -> ROTATE ABORT reason=future-date"
  # E: ответ ротации без keep сегодняшней даты — прерывание (защита 2 в применении)
  mk_env rotE
  fill_range "$E/dir" 2026-09-29 2026-09-30
  printf '%s\n' 'retention_select() { cat > /dev/null; printf "keep 2026-01-01\ndelete 2026-09-30\n"; }' >> "$E/lib/backup-lib.sh"
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE ABORT reason=empty-keep" "$E/out"'
  need 'has_all "$E/dir" 2026-09-29 2026-09-30'
  scn "rotate-dir: E keep без сегодняшней копии -> ROTATE ABORT reason=empty-keep"
  # F: ненулевой код выбора — прерывание, ничего не удалено
  mk_env rotF
  fill_range "$E/dir" 2026-09-29 2026-09-30
  printf '%s\n' 'retention_select() { cat > /dev/null; return 2; }' >> "$E/lib/backup-lib.sh"
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE ABORT reason=empty-keep" "$E/out"'
  need 'has_all "$E/dir" 2026-09-29 2026-09-30'
  scn "rotate-dir: F код 2 выбора -> ROTATE ABORT reason=empty-keep"
  # G: каталог с именем временного файла у пригодной даты из набора удаления — ротация
  # прервана до первого удаления, запуск завершён штатно
  mk_env rotG
  fill_range "$E/dir" 2026-08-03 2026-09-30
  mkdir "$E/dir/aggregator-2026-08-10.dump.age.partial"
  before=$(names "$E/dir")
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK" "$E/out" && [ "$(cat "$E/marks.log")" = backup ]'
  need 'grep -qx "ROTATE ABORT reason=unexpected-object" "$E/out" && ! grep -q "ROTATE OK" "$E/out"'
  need '[ -z "$(comm -23 <(printf "%s\n" "$before") <(names "$E/dir"))" ] && [ ! -s "$E/err" ]'
  need 'has_all "$E/dir" $(dates_between 2026-08-03 2026-10-01)'
  need '[ "$RC" = 2 ] && [ "$(grep -c "^BACKUP OK file=aggregator-2026-10-01.dump.age size=" "$E/out")" = 1 ] && usable "$E/dir/aggregator-2026-10-01.dump.age"'
  need '[ "$(grep -c "^ROTATE ABORT" "$E/out")" = 1 ] && [ "$(wc -l < "$E/marks.log" | tr -d " ")" = 1 ]'
  scn "rotate-dir: G каталог в наборе удаления -> ROTATE ABORT reason=unexpected-object, удалений нет"
  # H: каталог с именем файла суммы у даты без данных (bad) и ещё одна старая дата без данных
  mk_env rotH
  mkcopy "$E/dir" 2026-08-05 orphan
  echo "events_listed=1 users=1 migration=4" > "$E/dir/aggregator-2026-08-10.dump.age.meta"
  mkdir "$E/dir/aggregator-2026-08-10.dump.age.sha256"
  before=$(names "$E/dir")
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE ABORT reason=unexpected-object" "$E/out" && ! grep -q "ROTATE OK" "$E/out"'
  need '[ -z "$(comm -23 <(printf "%s\n" "$before") <(names "$E/dir"))" ] && [ ! -s "$E/err" ]'
  need '[ "$(cat "$E/marks.log")" = backup ] && usable "$E/dir/aggregator-2026-10-01.dump.age"'
  need '[ "$RC" = 2 ] && [ "$(grep -c "^BACKUP OK file=aggregator-2026-10-01.dump.age size=" "$E/out")" = 1 ] && usable "$E/dir/aggregator-2026-10-01.dump.age"'
  need '[ "$(grep -c "^ROTATE ABORT" "$E/out")" = 1 ] && [ "$(wc -l < "$E/marks.log" | tr -d " ")" = 1 ]'
  scn "rotate-dir: H каталог у даты без данных -> ROTATE ABORT, ни одно имя не удалено"
  # I: отказ удаления посреди набора — ROTATE ABORT reason=delete, каждая дата либо цела, либо
  # удалена целиком, оставляемые копии целы
  mk_env rotI
  fill_range "$E/dir" 2026-08-03 2026-09-30
  stub "$E/bin" rm 'case "$*" in *aggregator-2026-08-2*) exit 1 ;; esac; exec /bin/rm "$@"'
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE ABORT reason=delete" "$E/out" && ! grep -q "ROTATE OK" "$E/out"'
  need 'has_all "$E/dir" $keep && has_all "$E/dir" $(dates_between 2026-08-03 2026-08-29) && has_none "$E/dir" 2026-09-24 2026-08-31'
  for d in $(dates_between 2026-08-03 2026-10-01); do need 'has_all "$E/dir" "$d" || has_none "$E/dir" "$d"'; done
  need '[ "$(cat "$E/marks.log")" = backup ]'
  need '[ "$RC" = 2 ] && [ "$(grep -c "^BACKUP OK file=aggregator-2026-10-01.dump.age size=" "$E/out")" = 1 ] && usable "$E/dir/aggregator-2026-10-01.dump.age"'
  need '[ "$(grep -c "^ROTATE ABORT" "$E/out")" = 1 ] && [ "$(wc -l < "$E/marks.log" | tr -d " ")" = 1 ]'
  scn "rotate-dir: I отказ удаления -> ROTATE ABORT reason=delete, частично удалённых дат нет"
  # J: не удаляется именно файл данных. Заглушка ведёт себя как настоящий rm: отказавшее имя
  # пропускает, остальные аргументы удаляет, код 1. Сумма, метаданные и временные файлы этой
  # даты обязаны остаться: копия не должна превратиться в данные без подтверждающих файлов.
  mk_env rotJ
  fill_range "$E/dir" 2026-08-03 2026-09-30
  f=$E/dir/aggregator-2026-08-29.dump.age
  for d in .partial .partial.sha256 .partial.meta; do echo leftover > "$f$d"; done
  stub "$E/bin" rm 'rc=0; a=(); for x; do case "$x" in */aggregator-2026-08-29.dump.age) rc=1 ;; *) a+=("$x") ;; esac; done
/bin/rm "${a[@]}" || rc=1; exit $rc'
  run_bk
  need '[ "$RC" = 2 ] && [ "$(grep -c "^ROTATE ABORT reason=delete\$" "$E/out")" = 1 ] && ! grep -q "ROTATE OK" "$E/out"'
  need '[ "$(grep -c "^BACKUP OK file=aggregator-2026-10-01.dump.age size=" "$E/out")" = 1 ] && [ "$(cat "$E/marks.log")" = backup ]'
  need 'same3 "$f" "$T/tpl.age" "$TPLSUM  aggregator-2026-08-29.dump.age" "events_listed=1 users=1 migration=4" && usable "$f"'
  for d in .partial .partial.sha256 .partial.meta; do need '[ "$(cat "$f$d")" = leftover ]'; done
  need 'has_all "$E/dir" $keep && has_all "$E/dir" $(dates_between 2026-08-03 2026-08-28) && has_none "$E/dir" 2026-09-24 2026-08-31'
  for d in $(dates_between 2026-08-03 2026-08-28) $keep; do need 'usable "$E/dir/aggregator-$d.dump.age"'; done
  scn "rotate-dir: J отказ удаления файла данных -> остальные пять файлов даты целы, ROTATE ABORT reason=delete"
  sec_end rotate-dir "$f0"
  SC_N=$sc0
}

# st <файл>: состояние копии по copy_state
st() { copy_state "$1"; }
t_rotate_state() {
  local sc0=$SC_N f0=$FAILS d before
  SC_N=0
  # U1: нет суммы, неверная сумма, нет метаданных, неверные метаданные — копии остаются;
  # файлы суммы без данных и файл без заголовка age — удаляются по прежнему правилу
  mk_env stU1
  mkcopy "$E/dir" 2026-08-10 nosum
  mkcopy "$E/dir" 2026-08-11 badsum
  mkcopy "$E/dir" 2026-08-12 nometa
  mkcopy "$E/dir" 2026-08-13 badmeta
  mkcopy "$E/dir" 2026-08-05 orphan
  mkcopy "$E/dir" 2026-08-06 nohdr
  for d in 2026-08-10 2026-08-11 2026-08-12 2026-08-13; do need '[ "$(st "$E/dir/aggregator-$d.dump.age")" = unverified ]'; done
  need '[ "$(st "$E/dir/aggregator-2026-08-05.dump.age")" = bad ] && [ "$(st "$E/dir/aggregator-2026-08-06.dump.age")" = bad ]'
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE OK keep=1 delete=2" "$E/out"'
  need 'grep -qx "ROTATE UNVERIFIED count=4 dates=2026-08-10,2026-08-11,2026-08-12,2026-08-13" "$E/out"'
  for d in 2026-08-10 2026-08-11 2026-08-12 2026-08-13; do
    need '[ -f "$E/dir/aggregator-$d.dump.age" ] && cmp -s "$E/dir/aggregator-$d.dump.age" "$T/tpl.age"'
    need '! usable "$E/dir/aggregator-$d.dump.age"'
  done
  need '[ -f "$E/dir/aggregator-2026-08-11.dump.age.sha256" ] && [ -f "$E/dir/aggregator-2026-08-10.dump.age.meta" ]'
  need 'has_none "$E/dir" 2026-08-05 2026-08-06'
  scn "rotate-state: U1 копии без верной суммы или метаданных не удаляются"
  # U2: одни только непроверенные старые копии и сбой дампа — ничего не удалено
  mk_env stU2
  mkcopy "$E/dir" 2026-08-10 nosum
  mkcopy "$E/dir" 2026-08-11 badsum
  echo half > "$E/sw.dump"
  before=$(names "$E/dir")
  run_bk
  need '[ "$RC" = 1 ] && [ "$(names "$E/dir")" = "$before" ] && ! grep -q ROTATE "$E/out"'
  scn "rotate-state: U2 сбой дампа при непроверенных копиях — каталог не изменён"
  # U3: порог поднят после создания старых копий — они остаются пригодными и не удаляются
  mk_env stU3
  fill_range "$E/dir" 2026-08-01 2026-08-05
  sed "s/^BACKUP_MIN_BYTES=.*/BACKUP_MIN_BYTES=$((BACKUP_MIN_BYTES + 5000))/" "$VPS/backup-lib.sh" > "$E/lib/backup-lib.sh"
  need 'grep -qx "BACKUP_MIN_BYTES=$((BACKUP_MIN_BYTES + 5000))" "$E/lib/backup-lib.sh"'
  echo $((BACKUP_MIN_BYTES + 6000)) > "$E/sw.dumpbytes"
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE OK keep=6 delete=0" "$E/out" && ! grep -q UNVERIFIED "$E/out"'
  need 'has_all "$E/dir" $(dates_between 2026-08-01 2026-08-05)'
  scn "rotate-state: U3 порог поднят — старые копии меньше порога не удалены"
  # U4: порог поднят, новая копия меньше порога — отказ, старые копии целы, отметки нет
  mk_env stU4
  fill_range "$E/dir" 2026-08-01 2026-08-05
  sed "s/^BACKUP_MIN_BYTES=.*/BACKUP_MIN_BYTES=$((BACKUP_MIN_BYTES + 5000))/" "$VPS/backup-lib.sh" > "$E/lib/backup-lib.sh"
  before=$(names "$E/dir")
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=verify" "$E/out" && [ "$(names "$E/dir")" = "$before" ]'
  need '[ ! -s "$E/marks.log" ] && ! grep -q ROTATE "$E/out"'
  scn "rotate-state: U4 порог поднят — новая копия ниже порога отклонена, старые целы"
  # U5: старые 60 копий, часть без суммы внутри окна удаления — 7 + 4 считается по пригодным,
  # непроверенные остаются сверх них
  mk_env stU5
  fill_range "$E/dir" 2026-08-03 2026-09-30
  rm "$E/dir/aggregator-2026-08-04.dump.age.sha256" "$E/dir/aggregator-2026-09-27.dump.age.sha256"
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "ROTATE UNVERIFIED count=2 dates=2026-08-04,2026-09-27" "$E/out"'
  need 'grep -qx "ROTATE OK keep=11 delete=47" "$E/out"'
  need '[ -f "$E/dir/aggregator-2026-08-04.dump.age" ] && [ -f "$E/dir/aggregator-2026-09-27.dump.age" ]'
  need 'has_all "$E/dir" 2026-09-24 2026-09-25 2026-09-26 2026-09-28 2026-09-29 2026-09-30 2026-10-01'
  scn "rotate-state: U5 непроверенные копии не входят в 7 + 4 и не удаляются"
  sec_end rotate-state "$f0"
  SC_N=$sc0
}

CNT1="events_listed=5 users=2 events_merged=0 dedup_candidates=pending:0|confirmed:0|rejected:0 source_alerts=0 heartbeats=0 migration=4"
CNT2="events_listed=6 users=3 events_merged=1 dedup_candidates=pending:1|confirmed:0|rejected:0 source_alerts=0 heartbeats=1 migration=4"
# sumline <файл> <дата>: ожидаемая строка файла суммы для этих данных
sumline() { printf '%s  aggregator-%s.dump.age' "$(_sha256 "$1")" "$2"; }
# same3 <копия> <данные> <строка суммы> <строка счётчиков>: точное содержимое трёх итоговых файлов
same3() { cmp -s "$1" "$2" && [ "$(cat "$1.sha256")" = "$3" ] && [ "$(cat "$1.meta")" = "$4" ]; }
# pub_first <имя>: окружение с копией этого дня от первого успешного запуска (счётчики CNT1,
# сумма S1); второй запуск даст другой размер и счётчики CNT2
pub_first() {
  mk_env "$1"
  fill_range "$E/dir" 2026-09-29 2026-09-30
  run_bk
  need '[ "$RC" = 2 ] && [ "$(cat "$E/marks.log")" = backup ] && grep -qx "COPY FAIL reason=no-config" "$E/out"'
  cp "$E/dir/aggregator-2026-10-01.dump.age" "$E/first.age"
  S1=$(sumline "$E/first.age" 2026-10-01)
  need 'same3 "$E/dir/aggregator-2026-10-01.dump.age" "$E/first.age" "$S1" "$CNT1"'
  echo $((BACKUP_MIN_BYTES + 3000)) > "$E/sw.dumpbytes"
  echo "$CNT2" > "$E/sw.cnt"
}
# kill_run <программа> <номер вызова>: запуск обрывается (kill -9) на этом вызове программы;
# счётчик вызовов и переключатель после запуска обнуляются (номер вызова нулём не бывает)
kill_run() {
  echo "$2" > "$E/sw.${1}kill"; echo 0 > "$E/$1.n"
  { run_bk; } 2>/dev/null
  echo 0 > "$E/sw.${1}kill"; echo 0 > "$E/$1.n"
}
# pub_kill <номер mv>: запуск оборван перед этим rename (публикация: 1 сумма, 2 метаданные, 3 данные)
pub_kill() { kill_run mv "$1"; }
t_publish() {
  local sc0=$SC_N f0=$FAILS f g k v N2 S1 S2 before
  SC_N=0
  N2=$((AGEHDR + BACKUP_MIN_BYTES + 3000))
  for k in 1 2 3; do
    pub_first "pubK$k"
    f=$E/dir/aggregator-2026-10-01.dump.age
    pub_kill "$k"
    need '[ "$RC" = 137 ]'
    need '[ "$(cat "$E/marks.log")" = backup ]' # отметки от оборванного запуска нет
    need '[ -f "$f.partial" ] && [ "$(wc -c < "$f.partial" | tr -d " ")" = "$N2" ] && ! cmp -s "$f.partial" "$E/first.age"'
    cp "$f.partial" "$E/new.age"
    S2=$(sumline "$E/new.age" 2026-10-01)
    case "$k" in
      1)
        need 'same3 "$f" "$E/first.age" "$S1" "$CNT1" && usable "$f" && sum_ok "$E/dir" aggregator-2026-10-01.dump.age'
        need '[ "$(cat "$f.partial.sha256")" = "$S2" ] && [ "$(cat "$f.partial.meta")" = "$CNT2" ]'
        ;;
      2) # прежние данные с новой суммой: не пригодна
        need 'same3 "$f" "$E/first.age" "$S2" "$CNT1" && [ "$(st "$f")" = unverified ]'
        need '[ ! -e "$f.partial.sha256" ] && [ "$(cat "$f.partial.meta")" = "$CNT2" ]'
        ;;
      3) # прежние данные с новыми суммой и метаданными: не пригодна
        need 'same3 "$f" "$E/first.age" "$S2" "$CNT2" && [ "$(st "$f")" = unverified ]'
        need '[ ! -e "$f.partial.sha256" ] && [ ! -e "$f.partial.meta" ]'
        ;;
    esac
    # следующий запуск: дамп падает, но прерванная публикация доведена; отметки новой нет
    echo half > "$E/sw.dump"
    run_bk
    need '[ "$RC" = 1 ] && grep -qx "RECOVER OK file=aggregator-2026-10-01.dump.age" "$E/out" && grep -qx "BACKUP FAIL stage=dump" "$E/out"'
    need 'same3 "$f" "$E/new.age" "$S2" "$CNT2" && usable "$f" && sum_ok "$E/dir" aggregator-2026-10-01.dump.age'
    need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
    need '[ "$(cat "$E/marks.log")" = backup ]'
    need 'has_all "$E/dir" 2026-09-29 2026-09-30'
    scn "publish: обрыв перед rename №$k — данные первой копии целы, содержимое трёх файлов точное, публикация доведена"
  done
  # обрыв внутри самого доведения, дважды подряд: каждый следующий запуск продолжает с того же места
  pub_first pubRec
  f=$E/dir/aggregator-2026-10-01.dump.age
  pub_kill 1
  cp "$f.partial" "$E/new.age"
  S2=$(sumline "$E/new.age" 2026-10-01)
  pub_kill 2 # recover: сумма перенесена, обрыв перед метаданными
  need '[ "$RC" = 137 ] && same3 "$f" "$E/first.age" "$S2" "$CNT1" && [ "$(st "$f")" = unverified ]'
  need '[ ! -e "$f.partial.sha256" ] && [ "$(cat "$f.partial.meta")" = "$CNT2" ] && cmp -s "$f.partial" "$E/new.age"'
  pub_kill 2 # recover: метаданные перенесены, обрыв перед данными
  need '[ "$RC" = 137 ] && same3 "$f" "$E/first.age" "$S2" "$CNT2" && [ "$(st "$f")" = unverified ]'
  need '[ ! -e "$f.partial.meta" ] && cmp -s "$f.partial" "$E/new.age" && [ "$(cat "$E/marks.log")" = backup ]'
  echo half > "$E/sw.dump"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "RECOVER OK file=aggregator-2026-10-01.dump.age" "$E/out"'
  need 'same3 "$f" "$E/new.age" "$S2" "$CNT2" && usable "$f" && [ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  need '[ "$(cat "$E/marks.log")" = backup ]'
  scn "publish: два обрыва внутри доведения — итог равен данным оборванного запуска, отметок не добавилось"
  # доведение копии прошлой даты: на следующий день публикация вчерашней копии доводится,
  # отметка ставится только за сегодняшнюю
  pub_first pubPrev
  f=$E/dir/aggregator-2026-10-01.dump.age
  pub_kill 3
  cp "$f.partial" "$E/new.age"
  S2=$(sumline "$E/new.age" 2026-10-01)
  echo 2026-10-02 > "$E/today"
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "RECOVER OK file=aggregator-2026-10-01.dump.age" "$E/out" && grep -q "^BACKUP OK file=aggregator-2026-10-02.dump.age" "$E/out"'
  need 'same3 "$f" "$E/new.age" "$S2" "$CNT2" && usable "$f" && usable "$E/dir/aggregator-2026-10-02.dump.age"'
  need '[ "$(cat "$E/dir/aggregator-2026-10-02.dump.age.meta")" = "$CNT2" ] && grep -qx "ROTATE OK keep=4 delete=0" "$E/out"'
  need '[ "$(wc -l < "$E/marks.log" | tr -d " ")" = 2 ] && [ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  scn "publish: прерванная публикация вчерашней копии доведена на следующий день"
  # обрыв после записи временных файлов, до проверки порога, дамп ниже порога: следующий запуск
  # такую тройку не публикует, копия дня и её отметка прежние
  pub_first pubSmall
  f=$E/dir/aggregator-2026-10-01.dump.age
  killstub wc
  echo 500 > "$E/sw.dumpbytes"
  kill_run wc 1
  need '[ "$RC" = 137 ] && triple_ok "$f.partial" "$f.partial.sha256" "$f.partial.meta" && [ "$(cat "$f.partial.meta")" = "$CNT2" ]'
  need '[ "$(wc -c < "$f.partial" | tr -d " ")" = $((AGEHDR + 500)) ] && same3 "$f" "$E/first.age" "$S1" "$CNT1"'
  echo half > "$E/sw.dump"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "RECOVER SKIP file=aggregator-2026-10-01.dump.age reason=size" "$E/out" && ! grep -q "RECOVER OK" "$E/out"'
  need 'same3 "$f" "$E/first.age" "$S1" "$CNT1" && usable "$f" && [ "$(cat "$E/marks.log")" = backup ]'
  scn "publish: временная тройка ниже порога не публикуется, прежняя копия дня цела"
  # тот же обрыв при дампе обычного размера: целая непубликованная тройка доводится
  pub_first pubWc
  f=$E/dir/aggregator-2026-10-01.dump.age
  killstub wc
  kill_run wc 1
  need '[ "$RC" = 137 ] && same3 "$f" "$E/first.age" "$S1" "$CNT1" && [ -f "$f.partial.sha256" ] && [ -f "$f.partial.meta" ]'
  cp "$f.partial" "$E/new.age"
  S2=$(sumline "$E/new.age" 2026-10-01)
  echo half > "$E/sw.dump"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "RECOVER OK file=aggregator-2026-10-01.dump.age" "$E/out" && same3 "$f" "$E/new.age" "$S2" "$CNT2"'
  need '[ "$(cat "$E/marks.log")" = backup ]'
  scn "publish: обрыв между записью временных файлов и публикацией — целая тройка доведена"
  # временные файлы прошлой даты рядом с пригодной копией: неполные, повреждённые, ссылки,
  # ниже порога — не публикуются, итоговая копия и набор имён каталога не изменены, отметок нет;
  # ok — контроль: целая тройка публикуется
  { printf 'age-encryption.org/v1\n'; head -c $((BACKUP_MIN_BYTES + 2000)) /dev/zero | tr '\0' y; } > "$T/new.age"
  { printf 'age-encryption.org/v1\n'; head -c 500 /dev/zero | tr '\0' y; } > "$T/small.age"
  for v in ok dataonly nometa badsum nosum badmeta lnsum lnmeta lndata small; do
    mk_env "pubS$v"
    fill_range "$E/dir" 2026-09-29 2026-09-30
    f=$E/dir/aggregator-2026-09-30.dump.age
    g=$T/new.age
    if [ "$v" = small ]; then g=$T/small.age; fi
    S2=$(sumline "$g" 2026-09-30)
    cp "$g" "$E/out.age"; echo "$S2" > "$E/out.sha256"; echo "$CNT2" > "$E/out.meta"
    # вариант: какие из трёх временных файлов есть и в каком виде (file, ссылка ln, bad, нет)
    case "$v" in
      dataonly) set -- file no no ;;
      nometa) set -- file file no ;; # обрыв между записью суммы и метаданных
      badsum) set -- file bad file ;;
      nosum) set -- file no file ;; # итоговая сумма — от других данных
      badmeta) set -- file file bad ;;
      lnsum) set -- file ln file ;;
      lnmeta) set -- file file ln ;;
      lndata) set -- ln file file ;;
      *) set -- file file file ;;
    esac
    case "$1" in file) cp "$g" "$f.partial" ;; ln) ln -s "$E/out.age" "$f.partial" ;; esac
    case "$2" in file) echo "$S2" > "$f.partial.sha256" ;; ln) ln -s "$E/out.sha256" "$f.partial.sha256" ;;
      bad) printf '%064d  %s\n' 0 "${f##*/}" > "$f.partial.sha256" ;; esac
    case "$3" in file) echo "$CNT2" > "$f.partial.meta" ;; ln) ln -s "$E/out.meta" "$f.partial.meta" ;;
      bad) echo "x; id" > "$f.partial.meta" ;; esac
    before=$(names "$E/dir")
    echo half > "$E/sw.dump"
    run_bk
    need '[ "$RC" = 1 ] && [ ! -s "$E/marks.log" ]'
    if [ "$v" = ok ]; then
      need 'grep -qx "RECOVER OK file=aggregator-2026-09-30.dump.age" "$E/out" && same3 "$f" "$g" "$S2" "$CNT2" && usable "$f"'
      need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
    else
      if [ "$v" = small ]; then need 'grep -qx "RECOVER SKIP file=aggregator-2026-09-30.dump.age reason=size" "$E/out" && ! grep -q "RECOVER OK" "$E/out"'
      else need '! grep -q RECOVER "$E/out"'; fi
      need 'same3 "$f" "$T/tpl.age" "$TPLSUM  aggregator-2026-09-30.dump.age" "events_listed=1 users=1 migration=4" && usable "$f"'
      need '[ "$(names "$E/dir")" = "$before" ]'
      need 'cmp -s "$E/out.age" "$g" && [ "$(cat "$E/out.sha256")" = "$S2" ] && [ "$(cat "$E/out.meta")" = "$CNT2" ]'
    fi
    scn "publish: временные файлы прошлой даты, вариант $v"
  done
  # K3 и потеря временных данных: итоговая копия не проверена, не удаляется, отметки нет;
  # следующий успешный запуск публикует новую
  pub_first pubLost
  f=$E/dir/aggregator-2026-10-01.dump.age
  pub_kill 3
  rm "$f.partial"
  echo half > "$E/sw.dump"
  run_bk
  need '[ "$RC" = 1 ] && ! grep -q RECOVER "$E/out"'
  need '[ "$(st "$f")" = unverified ] && cmp -s "$f" "$E/first.age" && [ "$(cat "$E/marks.log")" = backup ]'
  rm "$E/sw.dump"
  run_bk
  need '[ "$RC" = 2 ] && usable "$f" && sum_ok "$E/dir" aggregator-2026-10-01.dump.age && [ "$(wc -c < "$f" | tr -d " ")" = "$N2" ]'
  need '! grep -q UNVERIFIED "$E/out" && [ "$(wc -l < "$E/marks.log" | tr -d " ")" = 2 ]'
  scn "publish: обрыв перед rename данных и потеря временного файла — копия unverified, затем заменена"
  # первый запуск дня оборван перед rename данных, копии этого дня ещё не было
  mk_env pubNew
  f=$E/dir/aggregator-2026-10-01.dump.age
  pub_kill 3
  need '[ "$RC" = 137 ] && [ ! -e "$f" ] && [ -f "$f.partial" ] && [ ! -s "$E/marks.log" ]'
  run_bk
  need '[ "$RC" = 2 ] && grep -qx "RECOVER OK file=aggregator-2026-10-01.dump.age" "$E/out" && usable "$f"'
  need '[ "$(cat "$E/marks.log")" = backup ] && [ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  scn "publish: первый запуск дня оборван — следующий доводит и создаёт копию"
  # обрыв до публикации (на этапе дампа): временный файл без суммы не публикуется
  pub_first pubDump
  f=$E/dir/aggregator-2026-10-01.dump.age
  cp "$T/tpl.age" "$f.partial"
  echo "events_listed=9 users=9 migration=4" > "$f.partial.meta"
  echo half > "$E/sw.dump"
  run_bk
  need '[ "$RC" = 1 ] && ! grep -q RECOVER "$E/out" && cmp -s "$f" "$E/first.age" && usable "$f"'
  need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  scn "publish: временные данные без суммы не публикуются, копия дня цела"
  # итоговая проверка после публикации: имя суммы занято каталогом — отказ, отметки нет
  mk_env pubDir
  f=$E/dir/aggregator-2026-10-01.dump.age
  mkdir "$f.sha256"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=verify" "$E/out" && [ ! -s "$E/marks.log" ]'
  need '! grep -q "ROTATE\|BACKUP OK" "$E/out" && [ ! -e "$f" ] && [ -f "$f.partial" ]'
  scn "publish: итоговое имя суммы занято каталогом — публикация не начата, отметки backup нет"
  # повторная проверка после публикации: rename данных «успешен», но итоговый файл подменён
  mk_env pubSwap
  f=$E/dir/aggregator-2026-10-01.dump.age
  printf '%s\n' '#!/bin/bash' 'for a; do last=$a; done' '/bin/mv "$@" || exit 1' \
    'case "$last" in *.dump.age) printf x >> "$last" ;; esac' > "$E/bin/mv"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=verify" "$E/out" && [ ! -s "$E/marks.log" ]'
  need '! grep -q "ROTATE\|BACKUP OK" "$E/out" && [ "$(st "$f")" = unverified ]'
  scn "publish: итоговая копия не прошла повторную проверку — отметки backup нет"
  # сбой записи итоговых метаданных (имя занято каталогом): публикация не начата по данным
  pub_first pubMeta
  f=$E/dir/aggregator-2026-10-01.dump.age
  rm "$f.meta"; mkdir "$f.meta"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=verify" "$E/out" && cmp -s "$f" "$E/first.age"'
  need '[ "$(cat "$E/marks.log")" = backup ]'
  scn "publish: метаданные не опубликованы — данные прежней копии не затёрты, новой отметки нет"
  sec_end publish "$f0"
  SC_N=$sc0
}

t_clock_rotate() {
  # часы в порядке перед запуском, сбились перед ротацией: копия и отметка есть, удалений нет
  mk_env clockrot
  printf 'yes\nno\n' > "$E/clock.seq"
  fill_range "$E/dir" 2026-09-11 2026-09-30
  run_bk
  need '[ "$RC" = 2 ]'
  need 'grep -q "^BACKUP OK file=aggregator-2026-10-01.dump.age" "$E/out"'
  need 'grep -qx "ROTATE ABORT reason=clock" "$E/out" && ! grep -q "ROTATE OK" "$E/out" && grep -qx "COPY FAIL reason=no-config" "$E/out"'
  need 'has_all "$E/dir" $(dates_between 2026-09-11 2026-10-01)'
  need '[ "$(cat "$E/marks.log")" = backup ]'
  need '[ "$(calls "timedatectl show -p NTPSynchronized --value")" = 2 ]'
  report "clock-rotate: часы сбились перед ротацией -> ROTATE ABORT reason=clock, удалений нет"
}

# sync_fail <номер вызова>: запуск, в котором этот вызов sync завершается ошибкой
sync_fail() { echo "$1" > "$E/sw.syncfail"; echo 0 > "$E/sync.n"; run_bk; echo 0 > "$E/sw.syncfail"; }
t_sync() {
  local sc0=$SC_N f0=$FAILS f S1 S2 n=aggregator-2026-10-01.dump.age
  SC_N=0
  # порядок: sync временной тройки — до публикации; sync каталога — после публикации, до отметки
  mk_env syncOrd
  run_bk
  need '[ "$RC" = 2 ] && [ "$(cat "$E/marks.log")" = backup ]'
  need '[ "$(cat "$E/sync.log")" = "$n.partial $n.partial.sha256 $n.partial.meta | $n.partial $n.partial.meta $n.partial.sha256 | marks=0
DIR | $n $n.meta $n.sha256 | marks=0" ]'
  scn "sync: два вызова, первый до публикации, второй после неё и до отметки"
  # ошибка первого sync при копии этого дня: публикации нет, прежняя копия и её отметка целы
  pub_first syncF1
  f=$E/dir/$n
  sync_fail 1
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=sync" "$E/out" && ! grep -q "BACKUP OK\|ROTATE" "$E/out"'
  need 'same3 "$f" "$E/first.age" "$S1" "$CNT1" && usable "$f" && [ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  need '[ "$(cat "$E/marks.log")" = backup ] && [ "$(wc -l < "$E/sync.log" | tr -d " ")" = 3 ]'
  scn "sync: ошибка перед публикацией — прежняя копия дня цела, новой отметки нет, прежняя на месте"
  # ошибка второго sync при копии этого дня: новая копия опубликована, новой отметки нет
  pub_first syncF2
  f=$E/dir/$n
  sync_fail 2
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=sync" "$E/out" && ! grep -q "BACKUP OK\|ROTATE" "$E/out"'
  need 'usable "$f" && [ "$(cat "$f.meta")" = "$CNT2" ] && ! cmp -s "$f" "$E/first.age" && [ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  need '[ "$(cat "$E/marks.log")" = backup ]'
  scn "sync: ошибка после публикации — новой отметки нет, прежняя на месте"
  # первый запуск дня, ошибка второго sync: копия есть, отметки нет вовсе
  mk_env syncF3
  sync_fail 2
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=sync" "$E/out" && [ ! -s "$E/marks.log" ] && usable "$E/dir/$n"'
  need '! grep -q "BACKUP OK\|ROTATE" "$E/out"'
  scn "sync: первый запуск дня, ошибка после публикации — отметки нет"
  sec_end sync "$f0" " (заглушка: порядок вызовов и код возврата; сам fsync не проверяется)"
  SC_N=$sc0
}

FP_N=0
# fp_case <описание> <настройка (eval)> <ожидаемая строка> [условие] [keeproot]:
# сбой до отметки — код 1, сегодняшнего файла и .partial нет, прежние копии целы, отметок и ротации нет.
fp_case() {
  local desc=$1 setup=$2 want=$3 extra=${4:-} wf d
  FP_N=$((FP_N + 1))
  mk_env "fp$FP_N" "${5:-}"
  fill_range "$E/dir" 2026-09-28 2026-09-30
  eval "$setup"
  run_bk
  case "$want" in STOP:*) wf=$E/err ;; *) wf=$E/out ;; esac
  need '[ "$RC" = 1 ]'
  need 'grep -qF -- "$want" "$wf"'
  need '[ -z "$(ls "$E"/dir/aggregator-2026-10-01* 2>/dev/null)" ]'
  if [ -d "$E/dir" ]; then
    for d in 2026-09-28 2026-09-29 2026-09-30; do need 'usable "$E/dir/aggregator-$d.dump.age"'; done
    need '[ "$(ls -A "$E/dir" | wc -l | tr -d " ")" = 9 ]'
  fi
  need '[ ! -s "$E/marks.log" ]'
  need '! grep -q "ROTATE" "$E/out"'
  need '[ -z "$(leak)" ]'
  if [ -n "$extra" ]; then need "$extra"; fi
  scn "fail-paths: $desc"
}

t_fail_paths() {
  local sc0=$SC_N f0=$FAILS d n1 n2
  SC_N=0
  local clk='[ "$(calls "timedatectl show -p NTPSynchronized --value")" = 30 ] && [ "$(ncalls docker)" = 0 ] && [ "$(calls "sleep 60")" = 29 ]'
  local noage='[ ! -e "$E/age.log" ]'
  local nodocker='[ "$(ncalls docker)" = 0 ] && ! grep -q "BACKUP FAIL" "$E/out"'
  fp_case "сбой pg_dump посреди потока" 'echo half > "$E/sw.dump"' "BACKUP FAIL stage=dump"
  fp_case "age завершается ошибкой" 'echo fail > "$E/sw.age"' "BACKUP FAIL stage=dump"
  fp_case "age не пишет заголовок" 'echo nohdr > "$E/sw.age"' "BACKUP FAIL stage=verify"
  fp_case "размер на 1 байт меньше BACKUP_MIN_BYTES" 'echo $((BACKUP_MIN_BYTES - 1 - AGEHDR)) > "$E/sw.dumpbytes"' "BACKUP FAIL stage=verify"
  fp_case "пустой поток" 'echo 0 > "$E/sw.dumpbytes"' "BACKUP FAIL stage=verify"
  fp_case "sync временных файлов завершился ошибкой" 'echo 1 > "$E/sw.syncfail"' "BACKUP FAIL stage=sync"
  fp_case "часы не синхронизированы 30 попыток" 'echo no > "$E/clock.seq"' "BACKUP FAIL stage=clock" "$clk"
  fp_case "нет backup-recipient" 'rm "$E/lib/backup-recipient"' "BACKUP FAIL stage=dump" "$noage"
  fp_case "ключ не по формату" 'echo age1short > "$E/lib/backup-recipient"' "BACKUP FAIL stage=dump" "$noage"
  fp_case "в ключе посторонние символы" 'echo "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq; id" > "$E/lib/backup-recipient"' "BACKUP FAIL stage=dump" "$noage"
  fp_case "нет compose-файла" 'rm "$E/opt/docker-compose.yml"' "BACKUP FAIL stage=dump" "$noage"
  fp_case "нет каталога бэкапов" 'mv "$E/dir" "$E/dir.gone"' "BACKUP FAIL stage=dump" '[ ! -e "$E/dir" ] && '"$noage"
  fp_case "запрос счётчиков завершился ошибкой" 'echo fail > "$E/sw.counters"' "BACKUP FAIL stage=dump" "$noage"
  fp_case "строка счётчиков не по формату" 'echo bad > "$E/sw.counters"' "BACKUP FAIL stage=dump" "$noage"
  fp_case "блокировка занята" 'touch "$E/sw.flock"' "STOP:" "$nodocker"
  if [ "$(id -u)" = 0 ]; then
    ok "fail-paths: не root — пропущено (запуск от root)"
  else
    fp_case "запуск не от root" ':' "STOP:" "$nodocker" keeproot
  fi

  # сбой отметки: код 1, проверенная копия остаётся, ротации нет, прежние копии целы
  mk_env fpmark
  fill_range "$E/dir" 2026-09-28 2026-09-30
  echo 1 > "$E/sw.mark"
  run_bk
  need '[ "$RC" = 1 ] && grep -qx "BACKUP FAIL stage=mark" "$E/out"'
  need 'usable "$E/dir/aggregator-2026-10-01.dump.age"'
  need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  need '! grep -q "ROTATE\|BACKUP OK" "$E/out"'
  need '[ ! -s "$E/marks.log" ]'
  need 'has_all "$E/dir" 2026-09-28 2026-09-29 2026-09-30'
  scn "fail-paths: сбой отметки: копия остаётся, ротации нет"

  # часы: нет, нет, да — запуск проходит
  mk_env fpclock
  printf 'no\nno\nyes\n' > "$E/clock.seq"
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK" "$E/out" && grep -qx "ROTATE OK keep=1 delete=0" "$E/out"'
  need '[ "$(calls "sleep 60")" = 2 ]'
  scn "fail-paths: часы no, no, yes — запуск проходит"

  # граница: копия ровно BACKUP_MIN_BYTES проходит
  mk_env fpedge
  echo $((BACKUP_MIN_BYTES - AGEHDR)) > "$E/sw.dumpbytes"
  run_bk
  need '[ "$RC" = 2 ] && grep -q "^BACKUP OK file=aggregator-2026-10-01.dump.age size=$BACKUP_MIN_BYTES\$" "$E/out"'
  scn "fail-paths: размер ровно BACKUP_MIN_BYTES проходит"

  # повторный запуск в тот же день: копия заменена, две отметки backup, backup_copy нет
  mk_env fprepeat
  fill_range "$E/dir" 2026-09-28 2026-09-30
  run_bk
  need '[ "$RC" = 2 ]'
  n1=$(wc -c < "$E/dir/aggregator-2026-10-01.dump.age" | tr -d ' ')
  echo $((BACKUP_MIN_BYTES + 2000)) > "$E/sw.dumpbytes"
  run_bk
  n2=$(wc -c < "$E/dir/aggregator-2026-10-01.dump.age" | tr -d ' ')
  need '[ "$RC" = 2 ] && [ "$n1" != "$n2" ] && [ "$n2" = $((AGEHDR + BACKUP_MIN_BYTES + 2000)) ]'
  need 'usable "$E/dir/aggregator-2026-10-01.dump.age"'
  need 'sum_ok "$E/dir" aggregator-2026-10-01.dump.age'
  need '[ "$(cat "$E/marks.log")" = "backup
backup" ]'
  need '! grep -q backup_copy "$E/marks.log"'
  need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  scn "fail-paths: повторный запуск в тот же день заменяет копию, отметки backup x2"

  sec_end fail-paths "$f0"
  SC_N=$sc0
}

# ---- список разделов (следующие планы дописывают сюда) ----------------------
t_host_audit
t_selftest
api_start || true
t_tracer
t_iso_oracle
t_retention
t_rotate_dir
t_rotate_state
t_publish
t_sync
t_clock_rotate
t_fail_paths
t_copy
t_status
t_apply
t_pkg
if [ "${1:-}" = pg ]; then t_pg; fi

if [ "$FAILS" -eq 0 ]; then echo "BACKUP TESTS OK"; else echo "BACKUP TESTS FAIL: $FAILS"; exit 1; fi
