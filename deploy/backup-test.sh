#!/usr/bin/env bash
# Локальные тесты Phase 6 (бэкап, аудит, восстановление): без сети, без docker, хост не
# меняется. Каждый раздел — функция t_имя, список вызовов внизу файла.
# Сейчас: t_host_audit — блок deploy/host-audit.sh печатает 14 ключей по контракту,
# только читает и не вызывает sudo (журнал вызовов заглушек, статический разбор текста).
# Запуск на Mac или Linux (системный bash 3.2):  /bin/bash deploy/backup-test.sh -> BACKUP TESTS OK
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")"
HERE=$PWD

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
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

# ---- список разделов (следующие планы дописывают сюда) ----------------------
t_host_audit

if [ "$FAILS" -eq 0 ]; then echo "BACKUP TESTS OK"; else echo "BACKUP TESTS FAIL: $FAILS"; exit 1; fi
