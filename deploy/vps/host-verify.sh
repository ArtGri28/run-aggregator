#!/usr/bin/env bash
# A3 VERIFY хоста: ТОЛЬКО ЧТЕНИЕ. Запускает владелец с Mac однострочником из
# deploy/oneliner.sh после host-apply.sh (и перезагрузки, если владелец её делал).
# Состояние перезагрузки печатается отдельной строкой и оценивается владельцем.
# Строки состояния хоста (swap_mb=, uptime_min=, ufw=, ufw_ssh=, ufw_allow=, ssh_*=,
# tinyproxy_listen=, cron=, phase1_sudoers=, tmp_packages=) — без адресов и имён
# хоста; на итог HOST VERIFY они не влияют.
set -u
export LC_ALL=C
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

MEMINFO=/proc/meminfo
UPTIME=/proc/uptime
PHASE1_SUDOERS=/etc/sudoers.d/run-agg
TMPROOT=/tmp

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

printf 'swap_mb=%s\n' "$(awk '$1 == "SwapTotal:" {v = int($2 / 1024)} END {print v + 0}' "$MEMINFO" 2>/dev/null || echo 0)"
printf 'uptime_min=%s\n' "$(awk '{print int($1 / 60)}' "$UPTIME" 2>/dev/null || echo '?')"

# ufw: состояние, разрешённые входящие и открыт ли в нём каждый порт sshd.
UFW=absent ALLOW="" UFW_SSH=n/a
SSH_PORTS=$(sshd -T 2>/dev/null | awk '$1 == "port" && $2 ~ /^[0-9]+$/ {print $2}' | sort -un)
if command -v ufw >/dev/null 2>&1; then
  US=$(ufw status 2>/dev/null)
  UFW=inactive
  if printf '%s\n' "$US" | grep -q '^Status: active'; then
    UFW=active
    ALLOW=$(printf '%s\n' "$US" | awk '$2 == "ALLOW" && $0 !~ /\(v6\)/ {print $1}' | sort -u | tr '\n' ',' | sed 's/,$//')
    UFW_SSH=allowed
    [ -n "$SSH_PORTS" ] || UFW_SSH=missing
    for p in $SSH_PORTS; do
      case ",$ALLOW," in *",$p/tcp,"*|*",$p,"*) ;; *) UFW_SSH=missing ;; esac
    done
  fi
fi
printf 'ufw=%s\nufw_ssh=%s\nufw_allow=%s\n' "$UFW" "$UFW_SSH" "${ALLOW:-none}"

sshd_val() { sshd -T 2>/dev/null | awk -v k="$1" '$1 == k {print $2; f = 1; exit} END {if (!f) print "unknown"}'; }
printf 'ssh_permitrootlogin=%s\n' "$(sshd_val permitrootlogin)"
printf 'ssh_passwordauthentication=%s\n' "$(sshd_val passwordauthentication)"

# tinyproxy_listen: none — не слушает; loopback — только локальные адреса; all — прочие.
printf 'tinyproxy_listen=%s\n' "$(ss -H -tlnp 2>/dev/null | awk '/"tinyproxy"/ {n++; if ($4 !~ /^(127\.|\[::1\])/) a = 1} END {print (n ? (a ? "all" : "loopback") : "none")}')"

if [ "$(systemctl is-active cron 2>&1)" = active ]; then echo "cron=active"; else echo "cron=inactive"; fi
if [ -e "$PHASE1_SUDOERS" ]; then echo "phase1_sudoers=present"; else echo "phase1_sudoers=absent"; fi
N=0
for d in "$TMPROOT"/tmp.*; do
  if [ -d "$d" ] && [ -f "$d/SHA256SUMS" ] && [ -f "$d/agg-install.sh" ]; then N=$((N + 1)); fi
done
printf 'tmp_packages=%s\n' "$N"

if [ -z "$FAILS" ]; then
  echo "HOST VERIFY OK"
else
  echo "HOST VERIFY FAIL: $FAILS"
  exit 1
fi
