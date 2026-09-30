#!/usr/bin/env bash
# Проверка IMAGE_TAG/IMAGE_PREV в deploy-apply.sh и rollback.sh при неудачном запуске
# (LO-04). Скрипты берутся из deploy/vps, в копиях меняются только пути, проверка root
# и владелец файлов; docker — заглушка: «up» падает, если в файле окружения тег bad*.
# Хост не меняется. Запуск на Mac или Linux: deploy/deploy-apply-test.sh -> DEPLOY APPLY OK.
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")/vps"

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/opt" "$T/lib" "$T/bin"
echo 'services: {}' > "$T/lib/docker-compose.yml"
for s in deploy-apply rollback; do
  sed -e "s#^D=/opt/aggregator\$#D=$T/opt#" -e "s#^SRC=/usr/local/lib/aggregator\$#SRC=$T/lib#" \
    -e '/\[ "\$(id -u)" -eq 0 \]/d' -e 's/ -o root -g root//' "$s.sh" > "$T/$s.sh"
  grep -q "^D=$T/opt\$" "$T/$s.sh" && ! grep -q 'id -u' "$T/$s.sh" || { echo "DEPLOY APPLY FAIL: не удалось подготовить $s.sh"; exit 1; }
done
ENVF="$T/opt/.env"
cat > "$T/bin/docker" <<EOF
#!/usr/bin/env bash
echo "docker \$*" >> "$T/docker.log"
case "\$*" in
  image\ inspect*) echo linux/amd64 ;;
  compose*up\ -d) grep -q '^IMAGE_TAG=bad' "$ENVF" && exit 1 # падает приложение, postgres поднялся
    grep -q '^IMAGE_TAG=hup' "$ENVF" && kill -HUP "\$PPID" ;; # обрыв ssh во время запуска
esac
exit 0
EOF
chmod +x "$T/bin/docker"

FAILS=0
val() { grep -m1 "^$1=" "$ENVF" | cut -d= -f2- || true; }
expect() { # expect <описание> <IMAGE_TAG> <IMAGE_PREV>
  local tag prev
  tag=$(val IMAGE_TAG) prev=$(val IMAGE_PREV)
  if [ "$tag" = "$2" ] && [ "$prev" = "$3" ]; then echo "ok   $1: IMAGE_TAG=$tag IMAGE_PREV=${prev:-(нет)}"
  else echo "FAIL $1: IMAGE_TAG=$tag IMAGE_PREV=${prev:-(нет)}, ждали $2/${3:-(нет)}"; FAILS=$((FAILS + 1)); fi
}
run() { # run <скрипт> <слово> [TAG] -> код выхода
  local rc=0
  printf '%s\n' "$2" | PATH="$T/bin:$PATH" bash "$T/$1.sh" ${3:+"$3"} > "$T/out" 2>&1 || rc=$?
  echo "$rc"
}

printf 'POSTGRES_PASSWORD=x\nIMAGE_TAG=good1\n' > "$ENVF"
chmod 600 "$ENVF"
[ "$(run deploy-apply DEPLOY good2)" = 0 ] || { echo "FAIL deploy good2: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
expect "deploy good2" good2 good1

: > "$T/docker.log"
rc=$(run deploy-apply DEPLOY bad3)
[ "$rc" != 0 ] && grep -q 'STOP: запуск aggregator:bad3 не удался' "$T/out" \
  || { echo "FAIL deploy bad3 должен упасть (rc=$rc): $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
expect "неудачный deploy bad3" good2 good1
[ "$(grep -c 'up -d$' "$T/docker.log")" = 2 ] \
  || { echo "FAIL после неудачи прежний образ не поднимался: $(cat "$T/docker.log")"; FAILS=$((FAILS + 1)); }

[ "$(run deploy-apply DEPLOY good4)" = 0 ] || { echo "FAIL deploy good4: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
expect "повторный deploy good4" good4 good2

[ "$(run rollback ROLLBACK)" = 0 ] || { echo "FAIL rollback: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
expect "rollback" good2 good4

[ "$(run deploy-apply DEPLOY hup6)" != 0 ] || { echo "FAIL deploy hup6 должен прерваться"; FAILS=$((FAILS + 1)); }
expect "обрыв (HUP) во время deploy hup6" good2 good4

[ "$(run deploy-apply no good5)" != 0 ] || { echo "FAIL отмена словом"; FAILS=$((FAILS + 1)); }
expect "отмена deploy good5" good2 good4

[ "$(val POSTGRES_PASSWORD)" = x ] || { echo "FAIL прочие ключи изменились"; FAILS=$((FAILS + 1)); }
left=$(find "$T/opt" -name '.env.*' | wc -l | tr -d ' ')
[ "$left" = 0 ] || { echo "FAIL остались временные файлы: $left"; FAILS=$((FAILS + 1)); }
[ "$FAILS" -eq 0 ] && echo "DEPLOY APPLY OK" || { echo "DEPLOY APPLY FAIL: $FAILS"; exit 1; }
