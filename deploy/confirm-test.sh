#!/usr/bin/env bash
# Проверка слов-подтверждения в скриптах root-консоли (agg-install.sh: INSTALL,
# host-apply.sh: UPDATES/COMPOSE и слова режимов хоста). Код подтверждения берётся из
# самих скриптов, хост не меняется. Режимы swap, ufw, ssh, tinyproxy, reboot, cleanup
# выполняются на копии host-apply.sh с временными путями и командами-заглушками;
# host-verify.sh и host-preflight.sh — на заглушках чтения; однострочник root-консоли
# выполняется с временным каталогом вместо каталога runagg и безвредной командой.
# Каждый случай получает свежие каталоги внутри временного: тест ничего не очищает. Запуск на Mac или Linux: deploy/confirm-test.sh -> CONFIRM OK.
# Пробелы и табуляции по краям read срезает сам (так было и раньше); NUL bash отбрасывает.
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")/vps"

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
# agg-install.sh: от приглашения до сравнения; host-apply.sh: функция confirm целиком.
{ echo 'set -euo pipefail'; sed -n "/^printf 'Введите слово INSTALL/,/= INSTALL \]/p" agg-install.sh; echo 'echo ACCEPTED'; } > "$T/install.sh"
{ echo 'set -euo pipefail'; sed -n '/^confirm() {/,/^}/p' host-apply.sh; echo 'confirm "$1"; echo ACCEPTED'; } > "$T/host.sh"
grep -q 'read -r' "$T/install.sh" && grep -q 'read -r' "$T/host.sh" || { echo "CONFIRM FAIL: код подтверждения не найден"; exit 1; }

FAILS=0
# check <скрипт> <слово> <ввод для printf> <accept|reject>
check() {
  local got
  if printf "$3" | bash "$T/$1.sh" "$2" 2>/dev/null | grep -q ACCEPTED; then got=accept; else got=reject; fi
  if [ "$got" = "$4" ]; then echo "ok   $2 $(printf '%q' "$(printf "$3")") -> $got"; else echo "FAIL $2 $(printf '%q' "$(printf "$3")") -> $got, ждали $4"; FAILS=$((FAILS + 1)); fi
}
for pair in install:INSTALL host:UPDATES host:COMPOSE host:SWAP host:UFW host:SSH host:TINYPROXY host:REBOOT host:CLEANUP; do
  s=${pair%%:*} w=${pair#*:}
  lw=$(printf '%s' "$w" | tr 'A-Z' 'a-z')
  check "$s" "$w" "$w\n" accept
  check "$s" "$w" "$w\r\n" accept
  check "$s" "$w" "  $w  \n" accept
  check "$s" "$w" "${w}X\n" reject
  check "$s" "$w" "$w.\n" reject
  check "$s" "$w" "$w\xc2\xa0\n" reject
  check "$s" "$w" "$w\xe2\x80\x8b\n" reject
  check "$s" "$w" "$w\x1b\n" reject
  check "$s" "$w" "${w%?}\xd0\x90\n" reject # последняя буква заменена кириллической «А»
  check "$s" "$w" "$lw\n" reject
  check "$s" "$w" "\n" reject
  check "$s" "$w" "" reject
done
# Консоль без перевода CR -> LF (как у провайдера): Enter = CR+LF в терминале с -icrnl.
if command -v python3 >/dev/null; then
  for pair in install:INSTALL host:UPDATES; do
    s=${pair%%:*} w=${pair#*:}
    got=$(python3 - "$T/$s.sh" "$w" <<'PY'
import os, pty, select, sys, termios, time
script, word = sys.argv[1], sys.argv[2]
pid, fd = pty.fork()
if pid == 0:
    a = termios.tcgetattr(0)
    a[0] &= ~termios.ICRNL
    termios.tcsetattr(0, termios.TCSANOW, a)
    os.execvp("bash", ["bash", script, word])
time.sleep(0.3)
os.write(fd, word.encode() + b"\r\n")
out = b""
while select.select([fd], [], [], 2)[0]:
    try:
        chunk = os.read(fd, 1024)
    except OSError:
        break
    if not chunk:
        break
    out += chunk
os.waitpid(pid, 0)
print("accept" if b"ACCEPTED" in out else "reject")
PY
)
    if [ "$got" = accept ]; then echo "ok   $w pty CR+LF -icrnl -> accept"; else echo "FAIL $w pty CR+LF -icrnl -> $got"; FAILS=$((FAILS + 1)); fi
  done
fi
# ---- Режимы хоста на заглушках ----
B="$T/bin"; mkdir -p "$B"
ME=$(id -un)
# stub <имя> <тело>: заглушка пишет вызов в $STATE/calls и выполняет тело; каталоги
# состояния ($STATE) и «корня» ($ROOTD) приходят из окружения случая.
stub() { printf '#!/bin/bash\necho "%s $*" >> "$STATE/calls"\n%s\n' "$1" "$2" > "$B/$1"; chmod +x "$B/$1"; }
stub swapon 'case "$1" in --show) cat "$STATE/swapon.out" 2>/dev/null ;; esac; exit 0'
stub dd 'for a in "$@"; do case "$a" in of=*) : > "${a#of=}" ;; esac; done'
stub mkswap '[ ! -e "$STATE/mkswap.fail" ]'
stub df 'echo "Filesystem 1048576-blocks Used Available Capacity Mounted on"; echo "/dev/x 30000 5000 $(cat "$STATE/df" 2>/dev/null || echo 20000) 18% /"'
stub tty 'cat "$STATE/tty" 2>/dev/null || echo /dev/tty1'
# sshd -T: «после» — когда drop-in есть и действует; при $STATE/main.wins drop-in не
# действует, пока основной файл не начинается с директив режима. sshd -t падает при
# $STATE/sshd.t.fail, а при $STATE/sshd.t.fail.main — только на правленом основном файле.
stub sshd 'M="$ROOTD/sshd_config"; fixed=no; head -2 "$M" 2>/dev/null | grep -q "^PasswordAuthentication no" && fixed=yes
case "$1" in
  -T) if [ -e "$ROOTD/sshd.d/00-aggregator.conf" ] && [ -e "$STATE/sshd.after" ] && { [ ! -e "$STATE/main.wins" ] || [ "$fixed" = yes ]; }; then cat "$STATE/sshd.after"; else cat "$STATE/sshd.T"; fi ;;
  -t) [ ! -e "$STATE/sshd.t.fail" ] && { [ ! -e "$STATE/sshd.t.fail.main" ] || [ "$fixed" = no ]; } ;;
esac'
stub ufw 'case "$1" in status) cat "$STATE/ufw.status" 2>/dev/null ;; esac; exit 0'
stub ss 'cat "$STATE/ss.out" 2>/dev/null; exit 0'
stub systemctl 'case "$*" in "is-enabled docker") cat "$STATE/docker.enabled" 2>/dev/null || echo enabled ;; is-enabled*) echo enabled ;; is-active*) echo active ;; esac; exit 0'
stub docker 'case "$*" in ps*) printf "aggregator-bot-1\nvpn\n" ;; *inspect*) echo unless-stopped ;; esac'
stub du 'echo "1M total"'
stub timedatectl 'echo NTP=yes'
stub apt 'exit 0'
SSHD_BEFORE='port 22\npermitrootlogin yes\npasswordauthentication yes\nkbdinteractiveauthentication yes\n'
SSHD_AFTER='port 22\npermitrootlogin without-password\npasswordauthentication no\nkbdinteractiveauthentication no\n'
NCASE=0
# fresh: новые каталоги состояния и «корня» и копии скриптов с путями в них.
fresh() {
  NCASE=$((NCASE + 1))
  S="$T/s$NCASE"; R="$T/r$NCASE"; DROP="$R/sshd.d/00-aggregator.conf"
  mkdir -p "$S" "$R/sshd.d" "$R/tmp" "$R/var"
  export STATE="$S" ROOTD="$R"
  sed -e '/\[ "\$(id -u)" -eq 0 \]/d' \
    -e "s#^SWAPFILE=.*#SWAPFILE=$R/swapfile#" -e "s#^FSTAB=.*#FSTAB=$R/fstab#" \
    -e "s#^SSHD_DROPIN=.*#SSHD_DROPIN=$DROP#" -e "s#^SSHD_MAIN=.*#SSHD_MAIN=$R/sshd_config#" -e "s#^ROOT_KEYS=.*#ROOT_KEYS=$R/root_keys#" \
    -e "s#^UFW_DEFAULT=.*#UFW_DEFAULT=$R/ufw.default#" -e "s#^TMPROOT=.*#TMPROOT=$R/tmp#" \
    -e "s#^LOADROOT=.*#LOADROOT=$R/var#" -e "s#^OWNER=.*#OWNER=$ME#" host-apply.sh > "$T/apply.sh"
  grep -q "^SWAPFILE=$R/swapfile\$" "$T/apply.sh" && grep -q "^OWNER=$ME\$" "$T/apply.sh" && ! grep -q 'id -u' "$T/apply.sh" \
    || { echo "CONFIRM FAIL: не удалось подготовить копию host-apply.sh"; exit 1; }
  sed -e "s#^MEMINFO=.*#MEMINFO=$S/meminfo#" -e "s#^UPTIME=.*#UPTIME=$S/uptime#" -e "s#^PHASE1_SUDOERS=.*#PHASE1_SUDOERS=$S/run-agg#" \
    -e "s#^TMPROOT=.*#TMPROOT=$R/tmp#" -e "s#^export PATH=.*#export PATH=\"$B:/usr/bin:/bin\"#" host-verify.sh > "$T/verify.sh"
  grep -q "^MEMINFO=$S/meminfo\$" "$T/verify.sh" || { echo "CONFIRM FAIL: не удалось подготовить копию host-verify.sh"; exit 1; }
  printf 'UUID=x / ext4 defaults 0 1\n' > "$R/fstab"
  printf "$SSHD_BEFORE" > "$S/sshd.T"; printf "$SSHD_AFTER" > "$S/sshd.after"
  echo 'ssh-ed25519 AAAA test' > "$R/root_keys"; echo 'IPV6=yes' > "$R/ufw.default"
  printf 'Port 22\n#PermitRootLogin no\nUsePAM yes\n' > "$R/sshd_config"
}
# main_yes: основной файл сам задаёт значения, как шаблон провайдера без Include.
main_yes() { printf 'Port 22\nPasswordAuthentication yes\nChallengeResponseAuthentication yes\nKbdInteractiveAuthentication yes\n  permitrootlogin yes\n#PasswordAuthentication yes\nUsePAM yes\n' > "$R/sshd_config"; cp "$R/sshd_config" "$T/main.was"; touch "$S/main.wins"; }
# hm <режим> <ввод> [VAR=значение …]: запуск копии, код в RC, вывод в $T/hout.
# Приглашение ввода слова печатается без перевода строки (его даёт Enter владельца),
# поэтому первая строка после слова в выводе теста идёт за приглашением.
hm() {
  local m=$1 in=$2; shift 2
  : > "$S/calls"; RC=0
  printf "$in" | env -u SSH_CONNECTION -u SSH_TTY "$@" PATH="$B:/usr/bin:/bin" /bin/bash "$T/apply.sh" "$m" > "$T/hout" 2>&1 || RC=$?
}
mods() { grep -vE "^(swapon --show|df |tty|sshd -T|ss |systemctl is-|ufw status|docker |du )" "$S/calls" | tr '\n' ';'; }
# mcase <режим> <описание> <условие>: условие — строка для eval.
mcase() {
  if eval "$3"; then echo "ok   mode $1: $2"; else echo "FAIL mode $1: $2 (rc=$RC; вызовы: $(mods); вывод: $(tail -3 "$T/hout" | tr '\n' ' '))"; FAILS=$((FAILS + 1)); fi
}

fresh; hm swap 'SWAP\n'
mcase swap "верное слово — dd, mkswap, swapon по порядку, одна строка в fstab, откат и итог" \
  '[ "$RC" = 0 ] && [ "$(mods)" = "dd if=/dev/zero of=$R/swapfile bs=1M count=2048 status=none;mkswap $R/swapfile;swapon $R/swapfile;" ] && [ "$(grep -c "^$R/swapfile none swap sw 0 0\$" "$R/fstab")" = 1 ] && [ -f "$R/fstab.agg-bak" ] && grep -q "Откат: swapoff" "$T/hout" && [ "$(tail -1 "$T/hout")" = "HOST APPLY swap DONE" ]'
cp "$R/fstab" "$T/fstab.was"; hm swap 'SWAP\n'
mcase swap "повторный запуск при существующем swap — изменений нет, код 0" '[ "$RC" = 0 ] && [ -z "$(mods)" ] && cmp -s "$R/fstab" "$T/fstab.was" && grep -q "swap уже есть" "$T/hout"'
fresh; cp "$R/fstab" "$T/fstab.was"; hm swap 'swap\n'
mcase swap "неверное слово — изменений нет" '[ "$RC" != 0 ] && [ -z "$(mods)" ] && cmp -s "$R/fstab" "$T/fstab.was" && [ ! -e "$R/swapfile" ]'
hm swap ''
mcase swap "пустой ввод — изменений нет" '[ "$RC" != 0 ] && [ -z "$(mods)" ] && cmp -s "$R/fstab" "$T/fstab.was"'
touch "$S/mkswap.fail"; hm swap 'SWAP\n'
mcase swap "сбой mkswap — файла нет, fstab не изменён" '[ "$RC" != 0 ] && [ ! -e "$R/swapfile" ] && cmp -s "$R/fstab" "$T/fstab.was" && ! grep -q "swapon $R" "$S/calls"'
fresh; echo 3000 > "$S/df"; hm swap 'SWAP\n'
mcase swap "мало места на диске — STOP до слова" '[ "$RC" != 0 ] && [ -z "$(mods)" ] && grep -q "^STOP: на / свободно 3000" "$T/hout"'

fresh; hm ufw 'UFW\n'
mcase ufw "верное слово — запрет входящих, порт sshd, включение" \
  '[ "$RC" = 0 ] && [ "$(mods)" = "ufw default deny incoming;ufw default allow outgoing;ufw allow 22/tcp;ufw --force enable;" ] && grep -q "^ufw status verbose" "$S/calls" && grep -q "Откат: ufw disable" "$T/hout" && grep -q "^IPV6=yes" "$T/hout"'
printf 'port 22\nport 2222\n' > "$S/sshd.T"; hm ufw 'UFW\n'
mcase ufw "два порта sshd — оба разрешены" '[ "$RC" = 0 ] && grep -q "^ufw allow 22/tcp" "$S/calls" && grep -q "^ufw allow 2222/tcp" "$S/calls"'
fresh; hm ufw 'ufw\n'
mcase ufw "неверное слово — изменений нет" '[ "$RC" != 0 ] && [ -z "$(mods)" ]'
hm ufw 'UFW\n' SSH_CONNECTION=x
mcase ufw "сессия SSH (SSH_CONNECTION) — STOP без вызовов" '[ "$RC" != 0 ] && [ -z "$(mods)" ] && grep -q "только из веб-консоли" "$T/hout"'
echo /dev/pts/0 > "$S/tty"; hm ufw 'UFW\n'
mcase ufw "псевдотерминал /dev/pts — STOP без вызовов" '[ "$RC" != 0 ] && [ -z "$(mods)" ]'
fresh; : > "$S/sshd.T"; hm ufw 'UFW\n'
mcase ufw "порты sshd не определены — STOP" '[ "$RC" != 0 ] && [ -z "$(mods)" ] && grep -q "порты sshd не определены" "$T/hout"'

fresh; hm ssh 'SSH\n'
mcase ssh "верное слово — drop-in из трёх директив, sshd -t, reload" \
  '[ "$RC" = 0 ] && [ "$(cat "$DROP")" = "$(printf "PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password")" ] && [ "$(mods)" = "sshd -t;systemctl reload ssh;" ] && grep -q "^Откат: rm -f" "$T/hout" && [ ! -e "$DROP.agg-new" ]'
fresh; hm ssh 'ssh\n'
mcase ssh "неверное слово — drop-in не создан" '[ "$RC" != 0 ] && [ ! -e "$DROP" ] && [ -z "$(mods)" ]'
hm ssh 'SSH\n' SSH_TTY=/dev/pts/1
mcase ssh "сессия SSH (SSH_TTY) — STOP без вызовов" '[ "$RC" != 0 ] && [ ! -e "$DROP" ] && [ -z "$(mods)" ]'
: > "$R/root_keys"; hm ssh 'SSH\n'
mcase ssh "у root нет ключа — STOP до слова" '[ "$RC" != 0 ] && [ ! -e "$DROP" ] && [ -z "$(mods)" ] && grep -q "нет ни одного ключа" "$T/hout"'
fresh; touch "$S/sshd.t.fail"; hm ssh 'SSH\n'
mcase ssh "сбой sshd -t — drop-in удалён, reload не вызван" '[ "$RC" != 0 ] && [ ! -e "$DROP" ] && ! grep -q "systemctl reload" "$S/calls"'
fresh; printf "$SSHD_BEFORE" > "$S/sshd.after"; hm ssh 'SSH\n'
mcase ssh "значения не те, основной файл ни при чём — код не 0, подсказка отката, reload не вызван" '[ "$RC" != 0 ] && grep -q "^STOP: значения не те" "$T/hout" && grep -q "Откат: rm -f" "$T/hout" && ! grep -q "^systemctl reload" "$S/calls" && [ ! -e "$R/sshd_config.agg-bak" ]'
fresh; main_yes; hm ssh 'SSH\n'
mcase ssh "основной файл задаёт значения — показан до слова, строки закомментированы, директивы в начале, копия, reload" \
  '[ "$RC" = 0 ] && grep -q "строка 2:PasswordAuthentication yes" "$T/hout" && cmp -s "$R/sshd_config.agg-bak" "$T/main.was" && [ "$(sed -n 2,4p "$R/sshd_config")" = "$(printf "PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password")" ] && [ "$(grep -c "^#agg# " "$R/sshd_config")" = 4 ] && grep -qx "#agg#   permitrootlogin yes" "$R/sshd_config" && grep -qx "#PasswordAuthentication yes" "$R/sshd_config" && grep -qx "UsePAM yes" "$R/sshd_config" && [ "$(grep -c "systemctl reload ssh" "$S/calls")" = 1 ] && grep -q "Откат: rm -f .*; cat .*sshd_config.agg-bak > .*sshd_config; sshd -t && systemctl reload ssh" "$T/hout" && [ ! -e "$R/sshd_config.agg-new" ]'
hm ssh 'SSH\n'
mcase ssh "повторный запуск после правки основного файла — файл не меняется, копия прежняя" '[ "$RC" = 0 ] && cmp -s "$R/sshd_config.agg-bak" "$T/main.was" && [ "$(grep -c "^PasswordAuthentication no" "$R/sshd_config")" = 1 ]'
fresh; main_yes; touch "$S/sshd.t.fail.main"; hm ssh 'SSH\n'
mcase ssh "sshd -t не принял правленый основной файл — он возвращён, drop-in удалён, reload не вызван" '[ "$RC" != 0 ] && cmp -s "$R/sshd_config" "$T/main.was" && [ ! -e "$DROP" ] && ! grep -q "systemctl reload" "$S/calls" && grep -q "^STOP: sshd -t не принял основной файл" "$T/hout"'
fresh; main_yes; printf "$SSHD_BEFORE" > "$S/sshd.after"; hm ssh 'SSH\n'
mcase ssh "значения не те и после правки основного файла — он возвращён, drop-in удалён, reload не вызван" '[ "$RC" != 0 ] && cmp -s "$R/sshd_config" "$T/main.was" && [ ! -e "$DROP" ] && ! grep -q "systemctl reload" "$S/calls" && grep -q "и после правки основного файла" "$T/hout"'
fresh; main_yes; hm ssh 'ssh\n'
mcase ssh "основной файл задаёт значения, неверное слово — ничего не изменено" '[ "$RC" != 0 ] && cmp -s "$R/sshd_config" "$T/main.was" && [ ! -e "$DROP" ] && [ ! -e "$R/sshd_config.agg-bak" ]'

fresh; hm tinyproxy 'TINYPROXY\n'
mcase tinyproxy "верное слово — служба остановлена и отключена" '[ "$RC" = 0 ] && [ "$(mods)" = "systemctl disable --now tinyproxy;" ] && grep -q "^Откат: systemctl enable --now tinyproxy" "$T/hout"'
hm tinyproxy 'TINYPROXY \xd0\x90\n'
mcase tinyproxy "неверное слово — изменений нет" '[ "$RC" != 0 ] && [ -z "$(mods)" ]'
hm tinyproxy ''
mcase tinyproxy "пустой ввод — изменений нет" '[ "$RC" != 0 ] && [ -z "$(mods)" ]'

fresh; hm reboot 'REBOOT\n'
mcase reboot "верное слово — показ контейнеров, затем systemctl reboot" '[ "$RC" = 0 ] && [ "$(mods)" = "systemctl reboot;" ] && grep -q "^aggregator-bot-1 restart=unless-stopped" "$T/hout" && grep -q "^vpn restart=" "$T/hout"'
hm reboot 'reboot\n'
mcase reboot "неверное слово — перезагрузки нет" '[ "$RC" != 0 ] && [ -z "$(mods)" ]'
echo disabled > "$S/docker.enabled"; hm reboot 'REBOOT\n'
mcase reboot "docker не в автозапуске — STOP до слова" '[ "$RC" != 0 ] && [ -z "$(mods)" ] && grep -q "^STOP: docker не включён" "$T/hout"'
fresh; echo '/swapfile file 2G 0B -2' > "$S/swapon.out"; hm reboot 'REBOOT\n'
mcase reboot "swap включён без строки в fstab — STOP до слова" '[ "$RC" != 0 ] && [ -z "$(mods)" ] && grep -q "^STOP: swap включён" "$T/hout"'

mkdirs() {
  mkdir -p "$R/tmp/tmp.pkg1" "$R/tmp/tmp.pkg2" "$R/tmp/tmp.other" "$R/tmp/keep" "$R/var/agg-load.old" "$R/var/agg-load.new" "$R/var/other.old"
  for d in tmp.pkg1 tmp.pkg2; do touch "$R/tmp/$d/SHA256SUMS" "$R/tmp/$d/agg-install.sh"; done
  touch "$R/tmp/tmp.other/agg-install.sh" "$R/tmp/keep/SHA256SUMS" "$R/tmp/keep/agg-install.sh"
  touch -t 202001010000 "$R/var/agg-load.old" "$R/var/other.old"
}
fresh; mkdirs; hm cleanup 'CLEANUP\n'
mcase cleanup "верное слово — удалены только копии пакета и старые остатки загрузки" \
  '[ "$RC" = 0 ] && [ ! -e "$R/tmp/tmp.pkg1" ] && [ ! -e "$R/tmp/tmp.pkg2" ] && [ -d "$R/tmp/tmp.other" ] && [ -d "$R/tmp/keep" ] && [ ! -e "$R/var/agg-load.old" ] && [ -d "$R/var/agg-load.new" ] && [ -d "$R/var/other.old" ] && grep -q "CLEANUP tmp: removed 2\$" "$T/hout" && grep -qx "CLEANUP load: removed 1" "$T/hout"'
hm cleanup 'CLEANUP\n'
mcase cleanup "повторный запуск — нечего удалять" '[ "$RC" = 0 ] && grep -q "CLEANUP tmp: absent\$" "$T/hout" && grep -qx "CLEANUP load: absent" "$T/hout" && [ -d "$R/tmp/tmp.other" ]'
fresh; mkdirs; hm cleanup 'cleanup\n'
mcase cleanup "неверное слово — ничего не удалено" '[ "$RC" != 0 ] && [ -d "$R/tmp/tmp.pkg1" ] && [ -d "$R/var/agg-load.old" ]'
ln -s "$R/tmp/keep" "$R/tmp/tmp.link"; hm cleanup 'CLEANUP\n'
mcase cleanup "символическая ссылка tmp.* не удаляется и не раскрывается" '[ "$RC" = 0 ] && [ -L "$R/tmp/tmp.link" ] && [ -f "$R/tmp/keep/SHA256SUMS" ]'

# ---- host-verify.sh и host-preflight.sh на заглушках чтения ----
hv() { /bin/bash "$T/verify.sh" > "$T/vout" 2>&1 || true; }
# vline <описание> <ожидаемая строка>…
vline() {
  local d=$1 l; shift
  for l in "$@"; do
    if grep -qxF "$l" "$T/vout"; then echo "ok   host-verify: $l ($d)"; else echo "FAIL host-verify: нет «$l» ($d): $(grep -E '^[a-z0-9_]+=' "$T/vout" | tr '\n' ' ')"; FAILS=$((FAILS + 1)); fi
  done
}
fresh; mkdirs
printf 'MemTotal: 4008000 kB\nSwapTotal: 2097148 kB\n' > "$S/meminfo"; echo '7260.55 100.00' > "$S/uptime"
printf 'Status: active\n\nTo                         Action      From\n--                         ------      ----\n22/tcp                     ALLOW       Anywhere\n22/tcp (v6)                ALLOW       Anywhere (v6)\n' > "$S/ufw.status"
printf "$SSHD_AFTER" > "$S/sshd.T"
printf 'LISTEN 0 1024 0.0.0.0:8888 0.0.0.0:* users:(("tinyproxy",pid=1,fd=3))\nLISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=2,fd=3))\n' > "$S/ss.out"
touch "$S/run-agg"; hv
vline "swap 2 ГБ, ufw включён, пароли выключены" 'swap_mb=2047' 'uptime_min=121' 'ufw=active' 'ufw_ssh=allowed' 'ufw_allow=22/tcp' \
  'ssh_permitrootlogin=without-password' 'ssh_passwordauthentication=no' 'tinyproxy_listen=all' 'cron=active' 'phase1_sudoers=present' 'tmp_packages=2'
fresh
printf 'MemTotal: 4008000 kB\nSwapTotal: 0 kB\n' > "$S/meminfo"; echo 'Status: inactive' > "$S/ufw.status"
printf 'LISTEN 0 1024 127.0.0.1:8888 0.0.0.0:* users:(("tinyproxy",pid=1,fd=3))\n' > "$S/ss.out"; hv
vline "исходное состояние" 'swap_mb=0' 'ufw=inactive' 'ufw_ssh=n/a' 'ufw_allow=none' 'ssh_permitrootlogin=yes' 'ssh_passwordauthentication=yes' 'tinyproxy_listen=loopback' 'phase1_sudoers=absent' 'tmp_packages=0'
printf 'Status: active\n8888/tcp ALLOW Anywhere\n' > "$S/ufw.status"; : > "$S/ss.out"; hv
vline "порт sshd в ufw не открыт" 'ufw_ssh=missing' 'ufw_allow=8888/tcp' 'tinyproxy_listen=none'
! grep -qE '([0-9]{1,3}\.){3}[0-9]{1,3}' "$T/vout" || { echo "FAIL host-verify: адрес в выводе"; FAILS=$((FAILS + 1)); }

printf 'tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=2,fd=3))\nudp UNCONN 0 0 10.1.2.3:39901 0.0.0.0:* users:(("docker-proxy",pid=4,fd=7))\n' > "$S/ss.out"
PATH="$B:/usr/bin:/bin" /bin/bash host-preflight.sh > "$T/pout" 2>&1 || true
for l in 'runagg_keys=0 phase1_keys=0 restrict_keys=0' 'root_keys=0' '22/tcp sshd' '39901/udp docker-proxy' 'phase1_sudoers=absent' 'postgres17_tar_bytes=absent' 'HOST PREFLIGHT DONE'; do
  if grep -qxF "$l" "$T/pout"; then echo "ok   host-preflight: $l"; else echo "FAIL host-preflight: нет «$l»"; FAILS=$((FAILS + 1)); fi
done
if sed -n '/Порты и кандидаты/,/== Проект/p' "$T/pout" | grep -qE '([0-9]{1,3}\.){3}[0-9]{1,3}'; then echo "FAIL host-preflight: адрес в разделе портов"; FAILS=$((FAILS + 1)); fi

# ---- Однострочник root-консоли: временный каталог вместо каталога runagg ----
mkdir -p "$T/shim"
command -v sha256sum >/dev/null || { printf '#!/bin/sh\nexec shasum -a 256 "$@"\n' > "$T/shim/sha256sum"; chmod +x "$T/shim/sha256sum"; }
RAW=$(../oneliner.sh testalias | grep -F 'then bash host-apply.sh updates; else' | sed 's/    #.*$//')
[ -n "$RAW" ] || { echo "CONFIRM FAIL: однострочник не найден"; exit 1; }
# stage: свежий каталог пакета и свежий каталог для временных; строка — под них.
stage() {
  NCASE=$((NCASE + 1))
  ST="$T/st$NCASE"; OT="$T/ot$NCASE"; MARK="$T/mark$NCASE"; LIST="$T/list$NCASE"
  mkdir -p "$ST" "$OT"
  ( cd .. && for f in $(./oneliner.sh files); do cp "../$f" "$ST"/; done && ./oneliner.sh sums > "$ST/SHA256SUMS" )
  LINE=${RAW//\/home\/runagg\/agg-deploy/$ST}
  LINE=${LINE/bash host-apply.sh updates/ls > \"$LIST\"; touch \"$MARK\"}
  printf '%s' "$LINE" | grep -qF "$ST/SHA256SUMS" && printf '%s' "$LINE" | grep -qF "touch \"$MARK\"" || { echo "CONFIRM FAIL: однострочник не подготовлен"; exit 1; }
}
one() { ( cd "$T" && TMPDIR="$OT" PATH="$T/shim:$PATH" /bin/bash -c "$LINE" ) > "$T/oout" 2>&1 || true; }
ocase() { if eval "$2"; then echo "ok   oneliner: $1"; else echo "FAIL oneliner: $1 ($(tr '\n' ' ' < "$T/oout"))"; FAILS=$((FAILS + 1)); fi; }
empty() { [ -z "$(ls -A "$OT")" ]; }
N=$(../oneliner.sh sums | wc -l | tr -d ' ')
stage; one
ocase "пакет совпал — команда запущена, временный каталог удалён" '[ -e "$MARK" ] && empty && [ "$(wc -l < "$LIST" | tr -d " ")" = "$((N + 1))" ]'
stage; echo junk > "$ST/extra.txt"; mkdir "$ST/sub"; one
ocase "лишний файл и подкаталог не мешают и не копируются" '[ -e "$MARK" ] && empty && ! grep -qE "^(extra.txt|sub)\$" "$LIST"'
stage; echo x >> "$ST/host-apply.sh"; one
ocase "изменённый файл пакета — остановка, команда не запущена, каталог удалён" '[ ! -e "$MARK" ] && empty && grep -q "STOP: HASH MISMATCH" "$T/oout"'
stage; echo "0000  extra" >> "$ST/SHA256SUMS"; one
ocase "изменённый SHA256SUMS — остановка, команда не запущена, каталог удалён" '[ ! -e "$MARK" ] && empty && grep -q "STOP: HASH MISMATCH" "$T/oout"'
stage; mv "$ST/SHA256SUMS" "$ST/SHA256SUMS.gone"; one
ocase "нет SHA256SUMS — остановка, каталог удалён" '[ ! -e "$MARK" ] && empty && grep -q "STOP: HASH MISMATCH" "$T/oout"'
[ "$FAILS" -eq 0 ] && echo "CONFIRM OK" || { echo "CONFIRM FAIL: $FAILS"; exit 1; }
