#!/usr/bin/env bash
# Проверка IMAGE_TAG/IMAGE_PREV в deploy-apply.sh и rollback.sh при неудачном запуске
# (LO-04). Скрипты берутся из deploy/vps, в копиях меняются только пути, проверка root
# и владелец файлов; docker — заглушка: «up» падает, если в файле окружения тег bad*.
# Раздел «Дедупликация» deploy-verify.sh (Phase 7): строки dedup_would_auto и
# dedup_pass_last на заглушке docker (psql и логи); сам SQL dedup_would_auto
# выполняет TestVerifyWouldAuto (internal/store) на тестовой базе.
# Образ postgres (D-26): deploy-apply.sh не выполняет pull имеющегося образа;
# deploy-verify.sh печатает строку postgres_image.
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
  image\ inspect*postgres:17) [ -e "$T/nopg" ] && exit 1; echo sha256:x ;;
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
if ! grep -q 'pull postgres' "$T/docker.log" && grep -qx 'postgres image: local, pull skipped' "$T/out"; then
  echo "ok   pull skipped: образ postgres:17 есть на хосте"
else echo "FAIL pull skipped: $(cat "$T/docker.log")"; FAILS=$((FAILS + 1)); fi

# Образа postgres:17 нет — pull ровно один; повтор уже развёрнутого тега.
touch "$T/nopg"
: > "$T/docker.log"
[ "$(run deploy-apply DEPLOY good2)" = 0 ] || { echo "FAIL deploy good2 без образа postgres: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
rm -f "$T/nopg"
if [ "$(grep -c 'pull postgres' "$T/docker.log")" = 1 ] && ! grep -q 'pull skipped' "$T/out"; then
  echo "ok   pull on missing image: один вызов pull postgres"
else echo "FAIL pull on missing image: $(cat "$T/docker.log")"; FAILS=$((FAILS + 1)); fi
expect "повтор deploy good2" good2 good1

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

# deploy-verify.sh: только чтение, запускается как есть; docker отвечает на запросы
# раздела «Дедупликация» и отдаёт логи из $T/<сервис>.log.
mkdir -p "$T/vbin"
PA=$(printf 'a%.0s' $(seq 64)) PB=$(printf 'b%.0s' $(seq 64))
cat > "$T/vbin/docker" <<EOF
#!/usr/bin/env bash
case "\$*" in
  compose*ps\ -q\ postgres) [ -e "$T/nopgc" ] || echo c0ffee ;;
  container\ inspect*) echo sha256:$PA ;;
  image\ inspect*postgres:17) [ -e "$T/pgtag" ] && cat "$T/pgtag" || echo sha256:$PA ;;
  image\ inspect*RepoDigests*) [ -e "$T/nodigest" ] || echo registry.example/library/postgres@sha256:$PB ;;
  *psql*"'dedup_would_auto=pending:'"*"state = 'pending'"*"reasons->>'would_auto' = 'true'"*) echo dedup_would_auto=pending:2 ;;
  *psql*"'dedup_candidates=pending:'"*) echo 'dedup_candidates=pending:4|confirmed:1|rejected:0|fresh:0' ;;
  *psql*"'events_merged='"*) echo events_merged=1 ;;
  *logs*worker) cat "$T/worker.log" 2>/dev/null ;;
  *logs*bot) cat "$T/bot.log" 2>/dev/null ;;
esac
exit 0
EOF
chmod +x "$T/vbin/docker"
verify() { # verify <описание> <ожидаемая строка>...
  local d=$1 l; shift
  PATH="$T/vbin:$PATH" bash deploy-verify.sh > "$T/vout" 2>&1 || true
  for l in "$@"; do
    grep -qxF "$l" "$T/vout" && echo "ok   verify $d: $l" || { echo "FAIL verify $d: нет «$l»"; FAILS=$((FAILS + 1)); }
  done
}
started='{"time":"t","level":"INFO","msg":"started","dedup_auto_merge":false}'
pass() { printf '{"time":"t","level":"INFO","msg":"dedup pass","pairs":9,"would_auto":%s,"would_auto_shown":%s,"would_auto_hidden":%s,"would_auto_decided":%s}\n' "$@"; }
{ echo "$started"; pass 7 7 0 0; pass 3 2 1 0; } > "$T/worker.log"
verify "последний проход" 'dedup_would_auto=pending:2' 'dedup_pass_last=would_auto:3|hidden:1|shown:2|decided:0' \
  'dedup_candidates=pending:4|confirmed:1|rejected:0|fresh:0' 'events_merged=1' \
  'dedup_auto_merge_worker=false' 'dedup_auto_merge_bot=unknown' 'CHECK dedup-summary: OK'
echo "$started" > "$T/worker.log"
verify "нет строки dedup pass" 'dedup_pass_last=unknown'
{ echo "$started"; echo '{"time":"t","level":"INFO","msg":"dedup pass","pairs":9,"would_auto":3}'; } > "$T/worker.log"
verify "строка без счётчиков would_auto_*" 'dedup_pass_last=unknown'
# Строка postgres_image: образ контейнера, сравнение с тегом postgres:17, дайджест.
verify "образ postgres" "postgres_image id=sha256:$PA tag=same repo_digest=sha256:$PB"
echo "sha256:$PB" > "$T/pgtag"; touch "$T/nodigest"
verify "тег postgres:17 сдвинут" "postgres_image id=sha256:$PA tag=differs repo_digest=none"
: > "$T/pgtag"
verify "тега postgres:17 нет" "postgres_image id=sha256:$PA tag=differs repo_digest=none"
rm -f "$T/pgtag" "$T/nodigest"; touch "$T/nopgc"
verify "контейнера postgres нет" 'postgres_image id=none tag=differs repo_digest=none'
! grep -qE 'registry\.example' "$T/vout" || { echo "FAIL verify: имя реестра в выводе"; FAILS=$((FAILS + 1)); }
[ "$FAILS" -eq 0 ] && echo "DEPLOY APPLY OK" || { echo "DEPLOY APPLY FAIL: $FAILS"; exit 1; }
