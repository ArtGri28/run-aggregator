#!/usr/bin/env bash
# Серверы времени для systemd-timesyncd (Phase 6, 06-04, задача 0в, D-19): МЕНЯЕТ ХОСТ.
# Запускает владелец от root, только по отдельному разрешению на изменение VPS:
#   clock-apply.sh apply — drop-in /etc/systemd/timesyncd.conf.d/aggregator.conf с NTP=<NTP_SERVERS>
#                          и перезапуск systemd-timesyncd (слово CLOCK)
#   clock-apply.sh undo  — удаление только этого drop-in и перезапуск службы (слово UNCLOCK)
# Входит в пакет agg (SHA256SUMS), но не ставится в /usr/local/lib/aggregator: запуск только
# однострочником root-консоли из deploy/oneliner.sh с проверкой хеша.
# NTP_SERVERS — побайтно строка NTP_SERVER= из 06-01-SUMMARY.md (решение владельца, источник записан
# там же): только DNS-имена. Каждое имя должно разрешаться в IPv4 (getent ahostsv4), иначе STOP до записи.
# Адреса и имена не печатаются: вывод — счётчики и метки.
# Не меняет: основной timesyncd.conf, чужие drop-in, FallbackNTP, D-19 и clock_ok; пакеты не ставит.
# Автоматического отката после перезапуска нет: при расхождении — STOP и подсказка про undo.
set -euo pipefail
export LC_ALL=C

NTP_SERVERS='0.europe.pool.ntp.org 1.europe.pool.ntp.org 2.europe.pool.ntp.org 3.europe.pool.ntp.org'
D=/etc/systemd/timesyncd.conf.d
MAIN=/etc/systemd/timesyncd.conf
F=$D/aggregator.conf
MARK='# aggregator 06-04 clock'

MODE=${1:-}
case "$MODE" in
  apply|undo) ;;
  *) echo "usage: clock-apply.sh apply|undo" >&2; exit 2 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo "нужен root" >&2; exit 1; }

stop() { echo "STOP: $1"; exit 1; }
confirm() {
  local ans
  printf 'Введите слово %s для продолжения: ' "$1"
  read -r ans
  ans=${ans//$'\r'/} # консоль провайдера может присылать CR вместе с Enter
  [ "$ans" = "$1" ] || { printf 'Отменено, хост не изменён (получено: %q).\n' "$ans"; exit 1; }
}
want() { printf '%s\n[Time]\nNTP=%s\n' "$MARK" "$NTP_SERVERS"; }
# ours: файл создан этим скриптом — ровно три строки: метка, [Time], NTP=
ours() { [ -f "$F" ] && [ ! -L "$F" ] && [ "$(sed -n 1p "$F")" = "$MARK" ] && [ "$(sed -n 2p "$F")" = "[Time]" ] \
  && sed -n 3p "$F" | grep -q '^NTP=' && [ "$(wc -l < "$F" | tr -d ' ')" = 3 ]; }
tsp() { timedatectl show-timesync -p "$1" --value 2> /dev/null || true; }
# state: строки после перезапуска — без адресов и имён
state() {
  local i a nm sync=no list
  for i in $(seq 1 12); do
    [ "$(timedatectl show -p NTPSynchronized --value 2> /dev/null || true)" = yes ] && { sync=yes; break; }
    sleep 5
  done
  a=$(tsp ServerAddress)
  nm=$(tsp ServerName)
  list=" $NTP_SERVERS "
  case "$nm" in
    "") echo "server=none" ;;
    *) case "$list" in *" $nm "*) echo "server=listed" ;; *) echo "server=other" ;; esac ;;
  esac
  case "$a" in "") echo "server_family=none" ;; *:*) echo "server_family=v6" ;; *) echo "server_family=v4" ;; esac
  echo "packet_count=$(timedatectl timesync-status 2> /dev/null | sed -n 's/^[[:space:]]*Packet count:[[:space:]]*\([0-9][0-9]*\)[[:space:]]*$/\1/p' | head -1)"
  if [ "$sync" = yes ]; then echo "clock=ok"; else echo "clock=unsynced"; fi
}

# ---- общие проверки ------------------------------------------------------------
V=$(systemctl --version 2> /dev/null | sed -n '1s/^systemd \([0-9][0-9]*\).*/\1/p')
[ -n "$V" ] && [ "$V" -ge 239 ] || stop "нужен systemd 239 или новее"
[ "$(systemctl show systemd-timesyncd -p LoadState --value 2> /dev/null)" = loaded ] || stop "служба systemd-timesyncd не загружена"

if [ "$MODE" = undo ]; then
  if [ ! -e "$F" ] && [ ! -L "$F" ]; then echo "HOST APPLY clock-undo NOTHING"; exit 0; fi
  ours || stop "файл $F создан не этим скриптом или изменён — не удаляется"
  confirm UNCLOCK
  rm -f -- "$F"
  systemctl restart systemd-timesyncd
  state
  echo "HOST APPLY clock-undo DONE"
  exit 0
fi

# ---- apply: серверы из решения владельца ---------------------------------------
[ -n "$NTP_SERVERS" ] || stop "серверы не заданы: нужно решение владельца (DECISION clock=ntp-server, NTP_SERVER=)"
n=0
seen=" "
# S1: ([A-Za-z0-9-]+\.)+[A-Za-z]{2,}; строже: строчные, метка 1–63 без дефиса по краям, TLD из букв,
# имя не длиннее 253, без точки в конце; IPv4 и имена без точки не проходят.
lbl='[a-z0-9]([a-z0-9-]*[a-z0-9])?'
name_re="^($lbl\.)+[a-z][a-z]+\$"
set -f
for s in $NTP_SERVERS; do
  [[ $s =~ $name_re ]] && [ "${#s}" -le 253 ] || stop "серверы — только DNS-имена (строчные буквы, цифры, дефис)"
  for l in ${s//./ }; do [ "${#l}" -le 63 ] || stop "метка имени длиннее 63 символов"; done
  case "$seen" in *" $s "*) stop "имя повторяется" ;; esac
  seen="$seen$s "
  n=$((n + 1))
done
set +f
[ "$n" -le 4 ] || stop "больше 4 серверов"
[ "$seen" = " $NTP_SERVERS " ] || stop "список серверов — имена через один пробел"
echo "servers=$n"

# Чужая настройка серверов и серверы от networkd — не смешиваются со списком.
ntp_re='^[[:space:]]*(NTP|FallbackNTP)[[:space:]]*='
for f in "$MAIN" "$D"/*.conf; do
  [ -f "$f" ] && [ "$f" != "$F" ] || continue
  if grep -qE "$ntp_re" "$f"; then stop "в настройке уже задана строка NTP= или FallbackNTP= (${f##*/})"; fi
done
[ -z "$(tsp LinkNTPServers)" ] || stop "служба получает серверы от сети (LinkNTPServers)"
if [ -e "$F" ] || [ -L "$F" ]; then
  if [ -f "$F" ] && [ ! -L "$F" ] && [ "$(cat "$F")" = "$(want)" ]; then echo "HOST APPLY clock UNCHANGED"; exit 0; fi
  stop "файл $F уже есть с другим содержимым — не перезаписывается"
fi
# Каждое имя разрешается в IPv4; адреса не печатаются. Иначе — до записи и перезапуска.
k=0
for s in $NTP_SERVERS; do getent ahostsv4 "$s" > /dev/null 2>&1 && k=$((k + 1)); done
echo "dns_v4=$k/$n"
[ "$k" = "$n" ] || stop "не все имена серверов разрешаются в IPv4; файл не создан, служба не перезапускалась"

confirm CLOCK
mkdir -p -m 755 "$D"
T=$D/.aggregator.tmp # без суффикса .conf: служба его не читает
want > "$T"
chmod 644 "$T"
if [ "$(cat "$T")" != "$(want)" ]; then rm -f -- "$T"; stop "временный файл записан неверно"; fi
mv -f -- "$T" "$F"
# До перезапуска служба файл не читала: при расхождении удалить его безопасно.
if [ "$(systemd-analyze cat-config systemd/timesyncd.conf 2> /dev/null | grep -cE '^[[:space:]]*NTP=')" != 1 ] \
  || ! systemd-analyze cat-config systemd/timesyncd.conf 2> /dev/null | grep -qxF "NTP=$NTP_SERVERS"; then
  rm -f -- "$F"
  stop "объединённая настройка не содержит ровно одну ожидаемую строку NTP=; файл удалён, служба не перезапускалась"
fi
systemctl restart systemd-timesyncd
if [ "$(tsp SystemNTPServers)" != "$NTP_SERVERS" ]; then
  echo "servers=mismatch"
  stop "служба прочитала другой список серверов; файл оставлен — откат: clock-apply.sh undo"
fi
echo "servers=match"
state
echo "HOST APPLY clock DONE"
