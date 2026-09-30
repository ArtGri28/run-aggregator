#!/usr/bin/env bash
# A3 VERIFY хоста: ТОЛЬКО ЧТЕНИЕ. Запускает владелец с Mac однострочником из
# deploy/oneliner.sh после host-apply.sh (и перезагрузки, если владелец её делал).
# Состояние перезагрузки печатается отдельной строкой и оценивается владельцем.
set -u
export LC_ALL=C

FAILS=""
fail() { FAILS="${FAILS:+$FAILS, }$1"; }

if [ -f /var/run/reboot-required ]; then echo "reboot-required: yes"; else echo "reboot-required: no"; fi

RUNNING=$(uname -r)
NEWEST=$(ls /boot/vmlinuz-* 2>/dev/null | sed 's#.*/vmlinuz-##' | sort -V | tail -1)
if [ "$RUNNING" = "$NEWEST" ]; then
  printf 'ядро: работает новейшее (%s)\n' "$RUNNING"
else
  printf 'ядро: работает %s, новейшее установленное %s\n' "$RUNNING" "${NEWEST:-?}"
fi

VER=$(docker compose version --short 2>/dev/null | sed -E 's/^v?([0-9]+)\.([0-9]+).*/\1 \2/')
read -r MAJ MIN <<<"$VER"
if [[ ${MAJ:-} =~ ^[0-9]+$ && ${MIN:-} =~ ^[0-9]+$ ]] && { [ "$MAJ" -gt 2 ] || { [ "$MAJ" -eq 2 ] && [ "$MIN" -ge 20 ]; }; }; then
  printf 'docker compose: %s.%s (нужно не ниже 2.20)\n' "$MAJ" "$MIN"
else
  echo "docker compose: нет плагина или версия ниже 2.20"
  fail "compose"
fi

DOCKER=$(systemctl is-active docker 2>&1)
printf 'docker: %s\n' "$DOCKER"
[ "$DOCKER" = active ] || fail "docker"

printf 'tinyproxy: enabled=%s active=%s\n' "$(systemctl is-enabled tinyproxy 2>&1)" "$(systemctl is-active tinyproxy 2>&1)"
printf 'осталось обновлений (по кэшу): %s\n' "$(apt list --upgradable 2>/dev/null | grep -c '/')"
timedatectl show -p NTP -p NTPSynchronized

if [ -z "$FAILS" ]; then
  echo "HOST VERIFY OK"
else
  echo "HOST VERIFY FAIL: $FAILS"
  exit 1
fi
