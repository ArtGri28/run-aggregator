#!/usr/bin/env bash
# Аудит VPS: ТОЛЬКО ЧТЕНИЕ, хост не меняется (Phase 6, D-24).
# Назначение: узнать до написания бэкапа, что есть на хосте (age, curl, flock, часы,
# диск, ufw), чтобы владелец выбрал ветку по каждой развилке.
# Запуск (владелец, Mac, одной строкой): блок передаётся через stdin под runagg,
# файлов на VPS не создаётся:
#   ssh -F ~/.ssh/run_agg.conf run-agg-vps 'bash -s' < deploy/host-audit.sh
# Вывод: ровно 14 строк «AUDIT ключ=значение» и «AUDIT END». Адрес и имя хоста не
# печатаются: значения проходят по словарю ключа, всё остальное становится unknown.
# sudo не используется. Состояние ufw этим блоком не читается (нужны права root):
# всегда ufw=unknown, его показывает agg host-verify.
# reboot_required и universe: unknown означает «прочитать не удалось», а не «нет».
set -u
export LC_ALL=C

# Есть ли программа в PATH (сама программа не запускается).
has() { if command -v "$1" >/dev/null 2>&1; then echo present; else echo absent; fi; }

# Печатает строку AUDIT; пустое значение или не из словаря ключа заменяется на unknown.
val() {
  local re
  case "$1" in
    age|gpg|curl|flock|jq|python) re='present|absent' ;;
    cron|ufw) re='active|inactive|failed|unknown' ;;
    ntp_synchronized) re='yes|no|unknown' ;;
    timezone) re='[A-Za-z0-9_/+-]+@[+-][0-9]{4}' ;;
    swap|disk_free_mb) re='[0-9]+' ;;
    *) re='yes|no' ;;
  esac
  [[ "$2" =~ ^($re)$ ]] || set -- "$1" unknown
  printf 'AUDIT %s=%s\n' "$1" "$2"
}

val age "$(has age)"
val gpg "$(has gpg)"
val curl "$(has curl)"
val flock "$(has flock)"
val jq "$(has jq)"
val python "$(has python3)"
val cron "$(systemctl is-active cron 2>/dev/null | head -1)"
val ntp_synchronized "$(timedatectl show -p NTPSynchronized --value 2>/dev/null | head -1)"
TZN=$(timedatectl show -p Timezone --value 2>/dev/null | head -1)
[ -n "$TZN" ] && TZN="$TZN@$(date +%z)"
val timezone "$TZN"
val swap "$(free -m 2>/dev/null | awk '/^Swap:/ {print $2}')"
val disk_free_mb "$(df -Pm /var 2>/dev/null | awk 'NR == 2 {print $4}')"
val ufw unknown
if [ -d /var/run ] && [ -r /var/run ] && [ -x /var/run ]; then
  if [ -f /var/run/reboot-required ]; then RB=yes; else RB=no; fi
else
  RB=unknown
fi
val reboot_required "$RB"
if P=$(apt-cache policy 2>/dev/null) && [ -n "$P" ]; then
  case "$P" in *"/universe"*) UN=yes ;; *) UN=no ;; esac
else
  UN=unknown
fi
val universe "$UN"
echo 'AUDIT END'
