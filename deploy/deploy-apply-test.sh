#!/usr/bin/env bash
# Проверка IMAGE_TAG/IMAGE_PREV в deploy-apply.sh и rollback.sh при неудачном запуске
# (LO-04). Скрипты берутся из deploy/vps, в копиях меняются только пути, проверка root
# и владелец файлов; docker — заглушка: «up» падает, если в файле окружения тег bad*.
# Раздел «Дедупликация» deploy-verify.sh (Phase 7): строки dedup_would_auto и
# dedup_pass_last на заглушке docker (psql и логи); сам SQL dedup_would_auto
# выполняет TestVerifyWouldAuto (internal/store) на тестовой базе.
# Образ postgres (D-26): deploy-apply.sh не выполняет pull имеющегося образа;
# deploy-verify.sh печатает строку postgres_image.
# Mini App (Phase 15): образ caddy при включённом профиле webapp (pull только когда
# образа нет, отказ pull — до переключения сервисов), подсказка rollback.sh, раздел
# «Опубликованные порты» и webapp_profile в deploy-verify.sh, необязательные ключи
# .env в deploy-preflight.sh (значения не печатаются).
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
  image\ inspect*caddy*) [ -e "$T/nocaddy" ] && exit 1; echo sha256:c ;;
  compose*config\ --services) [ -e "$T/wp" ] && echo caddy ;;
  compose*config\ --images) echo caddy:1@sha256:c ;;
  compose*pull\ caddy) [ -e "$T/caddyfail" ] && exit 1 ;;
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
if ! grep -qE 'pull caddy|inspect.*caddy' "$T/docker.log" && ! grep -q 'caddy' "$T/out"; then
  echo "ok   профиль webapp выключен: образ caddy не запрашивается"
else echo "FAIL профиль webapp выключен: $(cat "$T/docker.log")"; FAILS=$((FAILS + 1)); fi

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

# Профиль webapp включён: образ caddy есть — pull пропущен; нет — один pull; pull не
# удался — остановка до переключения сервисов, теги возвращены.
touch "$T/wp"
: > "$T/docker.log"
[ "$(run deploy-apply DEPLOY good4)" = 0 ] || { echo "FAIL deploy good4 с профилем: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
if ! grep -q 'pull caddy' "$T/docker.log" && grep -qx 'caddy image: local, pull skipped' "$T/out"; then
  echo "ok   caddy pull skipped: образ есть на хосте"
else echo "FAIL caddy pull skipped: $(cat "$T/docker.log")"; FAILS=$((FAILS + 1)); fi
touch "$T/nocaddy"
: > "$T/docker.log"
[ "$(run deploy-apply DEPLOY good4)" = 0 ] || { echo "FAIL deploy good4 без образа caddy: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
if [ "$(grep -c 'pull caddy' "$T/docker.log")" = 1 ] && [ "$(grep -c 'up -d' "$T/docker.log")" = 2 ]; then
  echo "ok   caddy pull on missing image: один вызов pull caddy, затем запуск"
else echo "FAIL caddy pull on missing image: $(cat "$T/docker.log")"; FAILS=$((FAILS + 1)); fi
touch "$T/caddyfail"
: > "$T/docker.log"
rc=$(run deploy-apply DEPLOY good7)
if [ "$rc" != 0 ] && grep -q 'Не удалось получить образ caddy' "$T/out" && ! grep -q 'up -d --wait postgres' "$T/docker.log"; then
  echo "ok   caddy pull failed: остановка до переключения сервисов"
else echo "FAIL caddy pull failed (rc=$rc): $(cat "$T/docker.log" "$T/out")"; FAILS=$((FAILS + 1)); fi
expect "неудачный pull caddy при deploy good7" good4 good2
rm -f "$T/wp" "$T/nocaddy" "$T/caddyfail"

[ "$(run rollback ROLLBACK)" = 0 ] || { echo "FAIL rollback: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
expect "rollback" good2 good4
! grep -q 'профиль webapp' "$T/out" || { echo "FAIL rollback: подсказка о профиле без профиля"; FAILS=$((FAILS + 1)); }
# Подсказка о профиле webapp (D-21): печатается до вопроса, .env скрипт в этой части не правит.
echo 'COMPOSE_PROFILES=webapp' >> "$ENVF"
[ "$(run rollback no)" != 0 ] && grep -q 'включён профиль webapp' "$T/out" && [ "$(val COMPOSE_PROFILES)" = webapp ] \
  && echo "ok   rollback: подсказка о профиле webapp, .env не изменён" || { echo "FAIL rollback: нет подсказки о профиле: $(cat "$T/out")"; FAILS=$((FAILS + 1)); }
expect "отмена rollback" good2 good4
grep -v '^COMPOSE_PROFILES=' "$ENVF" > "$T/env.tmp" && cat "$T/env.tmp" > "$ENVF"

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
  *logs*webapp) echo WEBAPP-LOG-LINE ;;
  compose*ps\ -a\ --format*Ports*) cat "$T/ports" 2>/dev/null ;;
  compose*ps\ -a\ --format*State*) cat "$T/states" 2>/dev/null ;;
  compose*config\ --services) cat "$T/services" 2>/dev/null ;;
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

# Опубликованные порты и профиль webapp: состояния и колонка Ports — из файлов заглушки.
base='postgres running healthy 0
migrate exited  0
bot running healthy 0
worker running healthy 0'
on="$base
webapp running healthy 0
caddy running  0"
caddy_ok='caddy 443/udp, 2019/tcp, 0.0.0.0:80->80/tcp, [::]:80->80/tcp, 0.0.0.0:443->443/tcp, [::]:443->443/tcp'
echo "$base" > "$T/states"; printf 'postgres 5432/tcp\nbot \nworker \nmigrate \n' > "$T/ports"
verify "профиль выключен, портов нет" 'webapp_profile=off' 'published=none' 'CHECK compose-state: OK' \
  'CHECK ports-only-caddy: OK' 'CHECK ports-caddy-80-443: OK'
! grep -q 'WEBAPP-LOG-LINE' "$T/vout" || { echo "FAIL verify: логи webapp при выключенном профиле"; FAILS=$((FAILS + 1)); }
echo "$on" > "$T/states"; printf 'postgres 5432/tcp\nwebapp 8080/tcp\n%s\n' "$caddy_ok" > "$T/ports"
verify "профиль включён, у caddy 80 и 443" 'webapp_profile=on' 'published=caddy 80/tcp' 'published=caddy 443/tcp' \
  'CHECK compose-state: OK' 'CHECK ports-only-caddy: OK' 'CHECK ports-caddy-80-443: OK' 'WEBAPP-LOG-LINE'
printf 'postgres 5432/tcp\nwebapp 0.0.0.0:8080->8080/tcp\n%s\n' "$caddy_ok" > "$T/ports"
verify "опубликован порт webapp" 'published=webapp 8080/tcp' 'CHECK ports-only-caddy: FAIL' 'CHECK ports-caddy-80-443: OK'
printf 'postgres 127.0.0.1:5432->5432/tcp\n%s\n' "$caddy_ok" > "$T/ports"
verify "опубликован порт postgres" 'published=postgres 5432/tcp' 'CHECK ports-only-caddy: FAIL'
printf '%s, 0.0.0.0:2019->2019/tcp\n' "$caddy_ok" > "$T/ports"
verify "лишний порт у caddy" 'CHECK ports-only-caddy: OK' 'CHECK ports-caddy-80-443: FAIL'
printf 'caddy 0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp, 0.0.0.0:443->443/udp\n' > "$T/ports"
verify "у caddy опубликован udp" 'CHECK ports-caddy-80-443: FAIL'
printf 'caddy 0.0.0.0:80->80/tcp\n' > "$T/ports"
verify "у caddy нет 443" 'CHECK ports-caddy-80-443: FAIL'
printf '%s\n' "$caddy_ok" > "$T/ports"
printf '%s\nwebapp running unhealthy 0\ncaddy running  0\n' "$base" > "$T/states"
verify "webapp unhealthy" 'webapp_profile=on' 'CHECK compose-state: FAIL' 'CHECK ports-caddy-80-443: OK'
echo "$base" > "$T/states"; echo caddy > "$T/services"; : > "$T/ports"
verify "профиль включён в .env, контейнеров нет" 'webapp_profile=on' 'CHECK compose-state: FAIL' 'CHECK ports-caddy-80-443: FAIL'
rm -f "$T/services"; printf 'bot 0.0.0.0:9000->9000/tcp\n' > "$T/ports"
verify "профиль выключен, опубликован порт bot" 'webapp_profile=off' 'CHECK ports-only-caddy: FAIL' 'CHECK ports-caddy-80-443: FAIL'
! grep -q '0\.0\.0\.0' <(sed -n '/^== Опубликованные порты ==$/,/^== Владельцы/p' "$T/vout") || { echo "FAIL verify: адрес в разделе портов"; FAILS=$((FAILS + 1)); }
rm -f "$T/states" "$T/ports"

# deploy-preflight.sh: только чтение; в копии меняется каталог. Значения ключей не печатаются.
sed -e "s#^D=/opt/aggregator\$#D=$T/opt#" deploy-preflight.sh > "$T/preflight.sh"
grep -q "^D=$T/opt\$" "$T/preflight.sh" || { echo "DEPLOY APPLY FAIL: не удалось подготовить deploy-preflight.sh"; exit 1; }
HEX=$(printf 'ab%.0s' $(seq 32))
cp "$ENVF" "$T/env.base"
preflight() { # preflight <описание> <строки .env> <yes|no: ждём ВНИМАНИЕ> <ожидаемая строка>...
  local d=$1 w=$3 l got=no
  { cat "$T/env.base"; printf '%b' "$2"; } > "$ENVF"
  PATH="$T/vbin:$PATH" bash "$T/preflight.sh" good2 > "$T/pout" 2>&1 || true
  shift 3
  grep -q '^ВНИМАНИЕ' "$T/pout" && got=yes
  [ "$got" = "$w" ] && echo "ok   preflight $d: предупреждение $got" || { echo "FAIL preflight $d: предупреждение $got, ждали $w"; FAILS=$((FAILS + 1)); }
  for l in "$@"; do
    grep -qxF "$l" "$T/pout" && echo "ok   preflight $d: $l" || { echo "FAIL preflight $d: нет «$l»"; FAILS=$((FAILS + 1)); }
  done
  if grep -qE 'SECRETVALUE|example\.invalid|'"$HEX" "$T/pout"; then echo "FAIL preflight $d: значение в выводе"; FAILS=$((FAILS + 1)); fi
}
preflight "ключей Mini App нет" '' no 'COMPOSE_PROFILES: нет (Mini App выключен)' 'WEBAPP_SECRET: нет (Mini App выключен)' \
  'WEBAPP_DOMAIN: нет (Mini App выключен)' 'профиль webapp: выключен'
preflight "секрет неверного формата" 'WEBAPP_SECRET=zz-SECRETVALUE\n' yes 'WEBAPP_SECRET: есть' 'ВНИМАНИЕ: WEBAPP_SECRET — не 64 hex-символа'
preflight "домен неверного формата" 'WEBAPP_DOMAIN=https://mini.example.invalid/\n' yes 'ВНИМАНИЕ: WEBAPP_DOMAIN — не имя хоста'
preflight "профиль без секрета и домена" 'COMPOSE_PROFILES=webapp\n' yes 'профиль webapp: включён' \
  'ВНИМАНИЕ: профиль webapp включён, а WEBAPP_SECRET не задан' 'ВНИМАНИЕ: профиль webapp включён, а WEBAPP_DOMAIN не задан'
preflight "всё задано верно" "COMPOSE_PROFILES=webapp\nWEBAPP_SECRET=$HEX\nWEBAPP_DOMAIN=mini.example.invalid\n" no \
  'COMPOSE_PROFILES: есть' 'WEBAPP_SECRET: есть' 'WEBAPP_DOMAIN: есть' 'профиль webapp: включён'
[ "$FAILS" -eq 0 ] && echo "DEPLOY APPLY OK" || { echo "DEPLOY APPLY FAIL: $FAILS"; exit 1; }
