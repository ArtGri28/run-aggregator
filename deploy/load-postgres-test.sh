#!/usr/bin/env bash
# Проверка запасного пути load-postgres.sh (ME-01): docker load выполняется только для копии
# во временном каталоге и только при совпадении SHA256 с хешем с Mac. В копии скрипта
# меняются путь архива и проверка root; docker — заглушка. Хост не меняется.
# Запуск на Mac или Linux: deploy/load-postgres-test.sh -> LOAD POSTGRES TEST OK.
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")/vps"

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/stage" "$T/bin"
sed -e "s#^SRC=/home/runagg/agg-deploy/postgres17.tar\$#SRC=$T/stage/postgres17.tar#" \
  -e '/\[ "\$(id -u)" -eq 0 \]/d' load-postgres.sh > "$T/load.sh"
grep -q "^SRC=$T/stage/" "$T/load.sh" && ! grep -q 'id -u' "$T/load.sh" || { echo "LOAD POSTGRES TEST FAIL: подготовка"; exit 1; }
cat > "$T/bin/docker" <<EOF
#!/usr/bin/env bash
echo "docker \$*" >> "$T/docker.log"
case "\$*" in image\ inspect*) echo linux/amd64 ;; esac
EOF
command -v sha256sum >/dev/null || printf '#!/bin/sh\nexec shasum -a 256 "$@"\n' > "$T/bin/sha256sum"
chmod +x "$T/bin"/*
sha() { if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }

FAILS=0
check() { # check <описание> <хеш> <ждём docker load: yes|no> <ждём код>
  local rc=0
  : > "$T/docker.log"
  PATH="$T/bin:$PATH" bash "$T/load.sh" "$2" > "$T/out" 2>&1 || rc=$?
  local loaded=no
  grep -q '^docker load' "$T/docker.log" && loaded=yes
  if [ "$loaded" = "$3" ] && [ "$rc" = "$4" ] && ! grep -q "docker load -i $T/stage" "$T/docker.log"; then
    echo "ok   $1 (rc=$rc, docker load: $loaded)"
  else
    echo "FAIL $1: rc=$rc, docker load: $loaded; $(cat "$T/docker.log" "$T/out")"; FAILS=$((FAILS + 1))
  fi
}

printf 'postgres image archive' > "$T/stage/postgres17.tar"
GOOD=$(sha "$T/stage/postgres17.tar")
check "хеш совпал" "$GOOD" yes 0
check "хеш другой" "$(printf '%064d' 0)" no 1
check "неверный формат хеша" "abc; id" no 2
printf 'подменённый архив' > "$T/stage/postgres17.tar"
check "архив подменён после расчёта хеша на Mac" "$GOOD" no 1
ln -sf /etc/hosts "$T/stage/postgres17.tar"
check "архив — ссылка на чужой файл" "$GOOD" no 1
[ -z "$(find "${TMPDIR:-/tmp}" -maxdepth 1 -newer "$T/load.sh" -name 'tmp.*' -type d -exec test -e '{}/postgres17.tar' ';' -print 2>/dev/null)" ] \
  || { echo "FAIL временные каталоги не удалены"; FAILS=$((FAILS + 1)); }
[ "$FAILS" -eq 0 ] && echo "LOAD POSTGRES TEST OK" || { echo "LOAD POSTGRES TEST FAIL: $FAILS"; exit 1; }
