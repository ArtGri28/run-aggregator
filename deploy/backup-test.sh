#!/usr/bin/env bash
# Локальные тесты Phase 6 (бэкап, аудит, восстановление): хост не меняется, сети нет.
# Каждый раздел — функция t_имя, список вызовов внизу файла. Разделы:
#   t_host_audit  — deploy/host-audit.sh печатает 14 ключей по контракту, только читает;
#   selftest      — итоговая строка раздела: ok только без провалов;
#   tracer, iso-oracle, retention, rotate-dir, rotate-state, publish, sync, clock-rotate, fail-paths — копия
#   deploy/vps/backup.sh на заглушках (docker, age, timedatectl, flock, sleep, sync, date) и чистые
#   функции deploy/vps/backup-lib.sh (ISO-неделя по оракулу python3, ротация 7 + 4).
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
cleanup() {
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
  mkdir -p "$E/bin" "$E/lib" "$E/dir" "$E/opt"
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
    -e "$rd" "$VPS/backup.sh" > "$E/backup.sh"
  if [ "$(grep -c "^export PATH=$E/bin" "$E/backup.sh")" != 1 ] || ! grep -q "^L=$E/lib\$" "$E/backup.sh" \
    || ! grep -q "^DIR=$E/dir\$" "$E/backup.sh" || ! grep -q "^F=$E/opt/docker-compose.yml\$" "$E/backup.sh" \
    || ! grep -q "^LOCK=$E/lock\$" "$E/backup.sh" || ! grep -q "^LOG=$E/log\$" "$E/backup.sh"; then
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
    echo "$*" | sed -n "s/.*VALUES ('\([a-z_]*\)').*/\1/p" >> "$E/marks.log"
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

  run_bk
  N=$((AGEHDR + BACKUP_MIN_BYTES + 1000))
  need '[ "$RC" = 2 ]' # S2: внешней копии в этом плане нет — код 2, не 0
  need '[ -f "$f" ] && [ -f "$f.sha256" ] && [ -f "$f.meta" ]'
  need '[ -z "$(ls "$E"/dir/*.partial* 2>/dev/null)" ]'
  need 'usable "$f"'
  need 'sum_ok "$E/dir" "aggregator-2026-10-01.dump.age"'
  need '[ "$(cut -c1-10 < <(ls -l "$f"))" = "-rw-------" ]'
  need '[ "$(cat "$f.meta")" = "events_listed=5 users=2 events_merged=0 dedup_candidates=pending:0|confirmed:0|rejected:0 source_alerts=0 heartbeats=0 migration=4" ]'
  need '[ "$(cat "$E/marks.log")" = backup ]'
  need 'grep -q -- "-r $TESTKEY" "$E/age.log"'
  need 'grep -qx "BACKUP OK file=aggregator-2026-10-01.dump.age size=$N" "$E/out"'
  need 'grep -qx "ROTATE OK keep=1 delete=0" "$E/out"'
  need 'grep -q "BACKUP OK" "$E/log"'
  need '[ -z "$(leak)" ]'
  need '[ "$(calls "date -u +%F")" = 1 ]' # дата взята один раз
  need '[[ backup.sh =~ $pkg_re ]] && [[ backup-lib.sh =~ $pkg_re ]]'
  report "tracer: дамп -> .partial -> проверка -> имя -> .sha256/.meta -> backup -> ротация, код 2, открытого текста нет"
  npg=$(grep -v '^ *#' "$VPS/backup.sh" | grep -c 'pg_dump' || true)
  need '[ "$npg" = 1 ]'
  need '[ "$(grep -v "^ *#" "$VPS/backup.sh" | grep -c "mark backup\$")" = 1 ]'
  need '[ "$(grep -v "^ *#" "$VPS/backup.sh" | grep -c "ROTATE_ABORTED=1")" -ge 1 ]'
  need '! grep -v "^ *#" "$VPS/backup.sh" | grep -q "backup_copy"'
  report "tracer: дамп только в конвейере, единственный вызов отметки — backup, отметки backup_copy в скрипте нет"
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
  need '[ "$RC" = 2 ] && [ "$(cat "$E/marks.log")" = backup ]'
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
  need 'grep -qx "ROTATE ABORT reason=clock" "$E/out" && ! grep -q "ROTATE OK" "$E/out"'
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
t_tracer
t_iso_oracle
t_retention
t_rotate_dir
t_rotate_state
t_publish
t_sync
t_clock_rotate
t_fail_paths
if [ "${1:-}" = pg ]; then t_pg; fi

if [ "$FAILS" -eq 0 ]; then echo "BACKUP TESTS OK"; else echo "BACKUP TESTS FAIL: $FAILS"; exit 1; fi
