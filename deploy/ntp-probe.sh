#!/usr/bin/env bash
# Диагностика NTP на VPS: ТОЛЬКО ЧТЕНИЕ и ограниченные сетевые пробы (Phase 6, 06-04, D-19).
# Дополняет deploy/clock-diag.sh: отделяет «сервер известен», «запрос отправлен», «ответ получен»
# и «синхронизация подтверждена». Ничего не исправляет.
# Запуск (владелец, Mac, одной строкой) — блок передаётся через stdin под runagg, файлов на VPS
# не создаёт:
#   ssh -F ~/.ssh/run_agg.conf run-agg-vps 'bash -s' < deploy/ntp-probe.sh
# Вывод: 45 строк «NTP ключ=значение» и «NTP END». Значения проходят по словарю ключа, всё
# остальное становится unknown: адреса, имена серверов и узлов не печатаются.
# Сеть (разрешение владельца 2026-10-03): ровно по одному NTP-запросу (48 байт, UDP/123) на пары
# ntp.ubuntu.com и time.cloudflare.com × IPv4 и IPv6 и один DNS-запрос UDP/53 к 1.1.1.1;
# таймаут 2 с, повторов нет. Имена целей разрешает системный резолвер VPS (getent, getaddrinfo):
# эти запросы к нему скрипт не считает и не ограничивает.
# Повышение прав — только уже разрешённые runagg `sudo -n iptables -S` и `sudo -n ip6tables -S`
# (без запроса пароля; нет прав — unavailable). Время, службы, настройка и firewall не меняются.
#   ntp_synchronized, ntp_enabled     свойства timedatectl
#   server_name      default (из FallbackNTPServers) | custom | none — имя не печатается
#   server_family    v4 | v6 | none — семейство выбранного службой адреса
#   packet_count     число ответов, полученных службой (timedatectl timesync-status)
#   udp123_sockets   UDP-сокеты с портом 123 (ss -uan): лишь косвенный признак активности службы
#   dns_<цель>_<v4|v6>     сколько адресов цели даёт резолвер
#   route_<v4|v6>_default  есть ли маршрут по умолчанию
#   probe_<цель>_<v4|v6>_sent    yes — запрос ушёл в сокет целиком; no — не ушёл; unknown — не проверялось
#   probe_<цель>_<v4|v6>_reply   yes — получен и проверен ответ NTP (адрес и порт отправителя,
#                                режим 4, версия 3–4, stratum 1–15, нет признака «не синхронизирован»,
#                                originate равен случайному transmit запроса); timeout | invalid |
#                                error | skipped | unknown
#   probe_<цель>_<v4|v6>_reason  уточнение отказа; rtt_ms, offset_ms — задержка и смещение часов VPS
#   dns_control_*    контрольный DNS-запрос: проходит ли UDP вне порта 123. reply=yes — ответ от того
#                    же адреса и порта с тем же ID, QR=1, opcode 0, RCODE 0 (NOERROR), один вопрос,
#                    равный заданному; иначе invalid с причиной (пакет при этом вернулся)
#   clocksource      источник времени ядра; ptp_devices, ptp_kvm — есть ли виртуальные часы
#                    хоста KVM (наличие устройства не доказывает их пригодность)
#   ip(6)tables_*    читаются ли правила, политика OUTPUT, число правил с портом 123
#   vps_epoch        время VPS, секунды UTC
# Отсутствие ответов само по себе не доказывает блокировку у провайдера.
# Переменные NTPP_* (цели, порты, каталоги /sys и /dev) — только для локальных тестов.
set -u
export LC_ALL=C

UBUNTU=${NTPP_UBUNTU-ntp.ubuntu.com}
CF=${NTPP_CF-time.cloudflare.com}
NPORT=${NTPP_PORT:-123}
DNS=${NTPP_DNS:-1.1.1.1}
DPORT=${NTPP_DNS_PORT:-53}
SYS=${NTPP_SYS:-/sys}
DEV=${NTPP_DEV:-/dev}

# Печатает строку NTP; пустое значение или не из словаря ключа заменяется на unknown.
val() {
  local re
  case "$1" in
    ntp_synchronized | ntp_enabled | route_v4_default | route_v6_default | ptp_kvm) re='yes|no' ;;
    server_name) re='default|custom|none' ;;
    server_family) re='v4|v6|none' ;;
    *_sent) re='yes|no' ;;
    *_reply) re='yes|timeout|invalid|error|skipped' ;;
    *_reason) re='none|no-address|socket-error|send-error|refused|recv-error|wrong-source|short|bad-mode|bad-version|kod|unsynced|bad-origin|bad-id|not-response|bad-opcode|bad-rcode|bad-question|no-python' ;;
    *_offset_ms) re='-?[0-9]+|none' ;;
    *_rtt_ms) re='[0-9]+|none' ;;
    clocksource) re='[a-z0-9_-]+' ;;
    iptables_read | ip6tables_read) re='yes|unavailable' ;;
    *_output_policy) re='accept|drop' ;;
    *) re='[0-9]+' ;;
  esac
  [[ "$2" =~ ^($re)$ ]] || set -- "$1" unknown
  printf 'NTP %s=%s\n' "$1" "$2"
}
has() { command -v "$1" > /dev/null 2>&1; }
prop() { timedatectl show -p "$1" --value < /dev/null 2> /dev/null | head -1; }
tprop() { timedatectl show-timesync -p "$1" --value < /dev/null 2> /dev/null | head -1; }

# ---- служба и выбранный сервер ------------------------------------------------
val ntp_synchronized "$(prop NTPSynchronized)"
val ntp_enabled "$(prop NTP)"
NAME=""
KIND=""
if has timedatectl; then
  NAME=$(tprop ServerName)
  if [ -z "$NAME" ]; then
    KIND=none
  else
    KIND=custom
    for S in $(tprop FallbackNTPServers); do [ "$S" = "$NAME" ] && KIND=default; done
    for S in $(tprop SystemNTPServers) $(tprop LinkNTPServers); do [ "$S" = "$NAME" ] && KIND=custom; done
  fi
fi
val server_name "$KIND"
FAM=""
if has timedatectl; then
  A=$(tprop ServerAddress)
  case "$A" in
    "") FAM=none ;;
    *:*) FAM=v6 ;;
    *.*) FAM=v4 ;;
  esac
fi
val server_family "$FAM"
PKT=""
pkt_re='^[[:space:]]*Packet count:[[:space:]]*([0-9]+)[[:space:]]*$'
STATUS=$(timedatectl timesync-status < /dev/null 2> /dev/null) || STATUS=""
while IFS= read -r LINE; do
  if [[ $LINE =~ $pkt_re ]]; then PKT=${BASH_REMATCH[1]}; fi
done <<< "$STATUS"
val packet_count "$PKT"

# Сокеты UDP с портом 123 — косвенный признак: служба держит сокет, пока ждёт ответа.
N=""
if has ss; then
  N=0
  while read -r _ _ _ LOC PEER _; do
    case "$LOC $PEER" in *:123\ * | *:123) N=$((N + 1)) ;; esac
  done < <(ss -H -uan < /dev/null 2> /dev/null)
fi
val udp123_sockets "$N"

# ---- имена и маршруты ---------------------------------------------------------
# count_addr <имя> <ahostsv4|ahostsv6>: число разных адресов
count_addr() {
  local a seen="" n=0
  has getent || return 0
  [ -n "$1" ] || { echo 0; return 0; }
  while read -r a _; do
    case " $seen " in *" $a "*) ;; *) seen="$seen $a"; n=$((n + 1)) ;; esac
  done < <(getent "$2" "$1" < /dev/null 2> /dev/null)
  echo "$n"
}
val dns_ubuntu_v4 "$(count_addr "$UBUNTU" ahostsv4)"
val dns_ubuntu_v6 "$(count_addr "$UBUNTU" ahostsv6)"
val dns_cloudflare_v4 "$(count_addr "$CF" ahostsv4)"
val dns_cloudflare_v6 "$(count_addr "$CF" ahostsv6)"
route() {
  has ip || return 0
  if [ -n "$(ip "$1" route show default < /dev/null 2> /dev/null | head -1)" ]; then echo yes; else echo no; fi
}
val route_v4_default "$(route -4)"
val route_v6_default "$(route -6)"

# ---- сетевые пробы: 4 запроса NTP и 1 запрос DNS ------------------------------
PROBE_KEYS="probe_ubuntu_v4 probe_ubuntu_v6 probe_cloudflare_v4 probe_cloudflare_v6 dns_control"
OUT=""
if has python3; then
  OUT=$(python3 -I - "$UBUNTU" "$CF" "$NPORT" "$DNS" "$DPORT" 2> /dev/null <<'PY'
import os, socket, struct, sys, time

ubuntu, cf, nport, dns, dport = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4], int(sys.argv[5])
DELTA = 2208988800  # 1900 -> 1970


def ts(b):
    sec, frac = struct.unpack("!II", b)
    return sec - DELTA + frac / 2 ** 32


def show(key, sent, reply, reason, rtt="none", off="none"):
    print("%s_sent=%s" % (key, sent))
    print("%s_reply=%s" % (key, reply))
    print("%s_reason=%s" % (key, reason))
    if not key.startswith("dns_"):
        print("%s_rtt_ms=%s" % (key, rtt))
        print("%s_offset_ms=%s" % (key, off))


def resolve(host, fam, port):
    if not host:
        return None
    try:
        for ai in socket.getaddrinfo(host, port, fam, socket.SOCK_DGRAM):
            addr = ai[4]
            if fam == socket.AF_INET6 and addr[0].lower().startswith("::ffff:"):
                continue
            return addr
    except (OSError, UnicodeError):
        return None
    return None


def ntp(key, host, fam):
    addr = resolve(host, fam, nport)
    if addr is None:
        return show(key, "no", "skipped", "no-address")
    try:
        s = socket.socket(fam, socket.SOCK_DGRAM)
    except OSError:
        return show(key, "no", "skipped", "socket-error")
    with s:
        s.settimeout(2)
        nonce = os.urandom(8)
        req = bytes([0x23]) + bytes(39) + nonce  # LI=0, VN=4, режим 3 (клиент)
        t1 = time.time()
        try:
            n = s.sendto(req, addr)
        except OSError:
            return show(key, "no", "skipped", "send-error")
        if n != len(req):
            return show(key, "no", "skipped", "send-error")
        try:
            data, src = s.recvfrom(512)
        except socket.timeout:
            return show(key, "yes", "timeout", "none")
        except ConnectionRefusedError:
            return show(key, "yes", "error", "refused")
        except OSError:
            return show(key, "yes", "error", "recv-error")
        t4 = time.time()
    if src[0] != addr[0] or src[1] != addr[1]:
        return show(key, "yes", "invalid", "wrong-source")
    if len(data) < 48:
        return show(key, "yes", "invalid", "short")
    li, vn, mode, stratum = data[0] >> 6, (data[0] >> 3) & 7, data[0] & 7, data[1]
    if mode != 4:
        return show(key, "yes", "invalid", "bad-mode")
    if vn not in (3, 4):
        return show(key, "yes", "invalid", "bad-version")
    if stratum == 0:
        return show(key, "yes", "invalid", "kod")
    if li == 3 or stratum > 15:
        return show(key, "yes", "invalid", "unsynced")
    if data[24:32] != nonce:
        return show(key, "yes", "invalid", "bad-origin")
    t2, t3 = ts(data[32:40]), ts(data[40:48])
    rtt = max(0, round(((t4 - t1) - (t3 - t2)) * 1000))
    off = round((((t2 - t1) + (t3 - t4)) / 2) * 1000)
    show(key, "yes", "yes", "none", str(rtt), str(off))


def dns_control():
    key = "dns_control"
    addr = resolve(dns, socket.AF_INET, dport)
    if addr is None:
        return show(key, "no", "skipped", "no-address")
    qid = os.urandom(2)
    question = b""
    for label in "time.cloudflare.com".split("."):
        question += bytes([len(label)]) + label.encode()
    question += b"\x00\x00\x01\x00\x01"  # QTYPE A, QCLASS IN
    q = qid + b"\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00" + question  # RD, один вопрос
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    except OSError:
        return show(key, "no", "skipped", "socket-error")
    with s:
        s.settimeout(2)
        try:
            n = s.sendto(q, addr)
        except OSError:
            return show(key, "no", "skipped", "send-error")
        if n != len(q):
            return show(key, "no", "skipped", "send-error")
        try:
            data, src = s.recvfrom(512)
        except socket.timeout:
            return show(key, "yes", "timeout", "none")
        except ConnectionRefusedError:
            return show(key, "yes", "error", "refused")
        except OSError:
            return show(key, "yes", "error", "recv-error")
    if src[0] != addr[0] or src[1] != addr[1]:
        return show(key, "yes", "invalid", "wrong-source")
    if len(data) < 12:
        return show(key, "yes", "invalid", "short")
    if data[:2] != qid:
        return show(key, "yes", "invalid", "bad-id")
    if not data[2] & 0x80:
        return show(key, "yes", "invalid", "not-response")
    if (data[2] >> 3) & 0x0F != 0:
        return show(key, "yes", "invalid", "bad-opcode")
    if data[3] & 0x0F != 0:
        return show(key, "yes", "invalid", "bad-rcode")
    if data[4:6] != b"\x00\x01" or data[12:12 + len(question)] != question:
        return show(key, "yes", "invalid", "bad-question")
    show(key, "yes", "yes", "none")


for key, host, fam in (("probe_ubuntu_v4", ubuntu, socket.AF_INET), ("probe_ubuntu_v6", ubuntu, socket.AF_INET6),
                       ("probe_cloudflare_v4", cf, socket.AF_INET), ("probe_cloudflare_v6", cf, socket.AF_INET6)):
    try:
        ntp(key, host, fam)
    except Exception:
        show(key, "unknown", "unknown", "unknown")
try:
    dns_control()
except Exception:
    show("dns_control", "unknown", "unknown", "unknown")
PY
)
fi
# Строки пробы печатаются в постоянном порядке; чего нет в выводе python3 — unknown.
for P in $PROBE_KEYS; do
  SUBS="sent reply reason rtt_ms offset_ms"
  [ "$P" = dns_control ] && SUBS="sent reply reason"
  for X in $SUBS; do
    V=""
    if [ -z "$OUT" ]; then
      if [ "$X" = reason ] && ! has python3; then V=no-python; fi
    else
      while IFS= read -r LINE; do
        case "$LINE" in "${P}_$X="*) V=${LINE#*=} ;; esac
      done <<< "$OUT"
    fi
    val "${P}_$X" "$V"
  done
done

# ---- источник времени ядра и виртуальные часы хоста ---------------------------
CS=""
if [ -r "$SYS/devices/system/clocksource/clocksource0/current_clocksource" ]; then
  read -r CS < "$SYS/devices/system/clocksource/clocksource0/current_clocksource" || true
fi
val clocksource "$CS"
NP=0
for D in "$DEV"/ptp*; do [ -e "$D" ] && NP=$((NP + 1)); done
val ptp_devices "$NP"
KVM=no
for C in "$SYS"/class/ptp/ptp*/clock_name; do
  [ -r "$C" ] || continue
  read -r L < "$C" || true
  [ "$L" = "KVM virtual PTP" ] && KVM=yes
done
val ptp_kvm "$KVM"

# ---- правила iptables: только чтение уже разрешённой командой -----------------
# fw <iptables|ip6tables> <префикс ключа>
fw() {
  local rules pol="" n=0 l re='(^|[^0-9])123([^0-9]|$)'
  if has sudo && rules=$(sudo -n "$1" -S < /dev/null 2> /dev/null); then
    val "${2}_read" yes
    while IFS= read -r l; do
      case "$l" in
        "-P OUTPUT ACCEPT") pol=accept ;;
        "-P OUTPUT DROP") pol=drop ;;
        -A*) if [[ $l =~ $re ]]; then n=$((n + 1)); fi ;;
      esac
    done <<< "$rules"
    val "${2}_output_policy" "$pol"
    val "${2}_rules_123" "$n"
  else
    val "${2}_read" unavailable
    val "${2}_output_policy" ""
    val "${2}_rules_123" ""
  fi
}
fw iptables iptables
fw ip6tables ip6tables

val vps_epoch "$(date -u +%s 2> /dev/null | head -1)"
echo 'NTP END'
