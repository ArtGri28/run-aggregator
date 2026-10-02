#!/usr/bin/env bash
# Диагностика синхронизации часов VPS: ТОЛЬКО ЧТЕНИЕ, хост не меняется (Phase 6, D-19, D-24).
# Назначение: до выката бэкапа узнать, почему NTPSynchronized не yes, чтобы владелец выбрал,
# что делать. Сам блок ничего не исправляет.
# Запуск (владелец, Mac, одной строкой): блок передаётся через stdin под runagg, файлов на VPS
# не создаётся:
#   ssh -F ~/.ssh/run_agg.conf run-agg-vps 'bash -s' < deploy/clock-diag.sh
# Вывод: ровно 13 строк «CLOCK ключ=значение» и «CLOCK END». Значения проходят по словарю
# ключа, всё остальное становится unknown: адреса и имена серверов времени, имя хоста и
# содержимое файлов настройки не печатаются. Права не повышаются, время, настройки и службы
# не меняются, сетевых обращений блок не делает (читается только состояние, которое уже
# знает systemd).
#   ntp_synchronized, ntp_enabled, can_ntp   свойства timedatectl
#   timesyncd_unit, timesyncd_active, timesyncd_enabled   служба systemd-timesyncd
#   other_time_service       активна ли другая служба времени (chrony, ntpsec, ntp, openntpd)
#   virt                     вид виртуализации (none — нет)
#   ntp_servers_configured   задана ли строка NTP= в настройке timesyncd
#   server_contacted, packet_count, stratum   из timedatectl timesync-status
#   epoch                    время VPS, секунды UTC
set -u
export LC_ALL=C

CONF=/etc/systemd/timesyncd.conf

# Печатает строку CLOCK; пустое значение или не из словаря ключа заменяется на unknown.
val() {
  local re
  case "$1" in
    ntp_synchronized|ntp_enabled|can_ntp|ntp_servers_configured|server_contacted) re='yes|no' ;;
    timesyncd_unit) re='loaded|not-found|masked' ;;
    timesyncd_active) re='active|inactive|failed|activating' ;;
    timesyncd_enabled) re='enabled|disabled|masked|static' ;;
    other_time_service) re='none|chrony|ntpsec|ntp|openntpd|several' ;;
    virt) re='[a-z0-9-]+' ;;
    *) re='[0-9]+' ;;
  esac
  [[ "$2" =~ ^($re)$ ]] || set -- "$1" unknown
  printf 'CLOCK %s=%s\n' "$1" "$2"
}
# Есть ли программа в PATH (сама программа не запускается).
has() { command -v "$1" >/dev/null 2>&1; }
# Свойство timedatectl одной строкой.
prop() { timedatectl show -p "$1" --value 2>/dev/null | head -1; }

val ntp_synchronized "$(prop NTPSynchronized)"
val ntp_enabled "$(prop NTP)"
val can_ntp "$(prop CanNTP)"
val timesyncd_unit "$(systemctl show systemd-timesyncd -p LoadState --value 2>/dev/null | head -1)"
val timesyncd_active "$(systemctl is-active systemd-timesyncd 2>/dev/null | head -1)"
val timesyncd_enabled "$(systemctl is-enabled systemd-timesyncd 2>/dev/null | head -1)"

# Другие службы времени: имя единственной активной, several — если их больше одной.
OTHER=unknown
if has systemctl; then
  OTHER=none
  for U in chrony ntpsec ntp openntpd; do
    if [ "$(systemctl is-active "$U" 2>/dev/null | head -1)" = active ]; then
      if [ "$OTHER" = none ]; then OTHER=$U; else OTHER=several; fi
    fi
  done
fi
val other_time_service "$OTHER"

val virt "$(systemd-detect-virt 2>/dev/null | head -1)"

# Строка NTP= с непустым значением в настройке timesyncd; сами серверы не печатаются.
# unknown — ни один файл настройки прочитать не удалось.
SRV=unknown
ntp_re='^[[:space:]]*NTP=[[:space:]]*[^[:space:]#]'
for FILE in "$CONF" "$CONF".d/*.conf; do
  [ -f "$FILE" ] && [ -r "$FILE" ] || continue
  [ "$SRV" = yes ] || SRV=no
  while IFS= read -r LINE || [ -n "$LINE" ]; do
    if [[ $LINE =~ $ntp_re ]]; then SRV=yes; fi
  done < "$FILE"
done
val ntp_servers_configured "$SRV"

# Состояние обмена с сервером времени. Из строки Server берётся только признак «есть адрес».
CONTACT=unknown
PKT=""
STR=""
addr_re='^[0-9A-Fa-f.:]*[.:][0-9A-Fa-f.:]*$'
srv_re='^[[:space:]]*Server:[[:space:]]*([^[:space:]]*)'
pkt_re='^[[:space:]]*Packet count:[[:space:]]*([0-9]+)[[:space:]]*$'
str_re='^[[:space:]]*Stratum:[[:space:]]*([0-9]+)[[:space:]]*$'
STATUS=$(timedatectl timesync-status 2>/dev/null) || STATUS=""
while IFS= read -r LINE; do
  if [[ $LINE =~ $srv_re ]]; then
    if [[ ${BASH_REMATCH[1]} =~ $addr_re ]]; then CONTACT=yes; else CONTACT=no; fi
  elif [[ $LINE =~ $pkt_re ]]; then
    PKT=${BASH_REMATCH[1]}
  elif [[ $LINE =~ $str_re ]]; then
    STR=${BASH_REMATCH[1]}
  fi
done <<< "$STATUS"
val server_contacted "$CONTACT"
val packet_count "$PKT"
val stratum "$STR"

val epoch "$(date -u +%s 2>/dev/null | head -1)"
echo 'CLOCK END'
