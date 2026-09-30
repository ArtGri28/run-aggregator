#!/usr/bin/env bash
# Проверка слов-подтверждения в скриптах root-консоли (agg-install.sh: INSTALL,
# host-apply.sh: UPDATES/COMPOSE). Код подтверждения берётся из самих скриптов, хост не
# меняется. Запуск на Mac или Linux: deploy/confirm-test.sh -> CONFIRM OK.
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
for pair in install:INSTALL host:UPDATES host:COMPOSE; do
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
[ "$FAILS" -eq 0 ] && echo "CONFIRM OK" || { echo "CONFIRM FAIL: $FAILS"; exit 1; }
