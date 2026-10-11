#!/usr/bin/env bash
# B2 VERIFY развёртывания: ТОЛЬКО ЧТЕНИЕ. Вызывается как «sudo -n /usr/local/sbin/agg verify»
# с Mac через 1–2 минуты после agg deploy; вывод — в
# $HOME/run-agg-phase4/. Секреты не печатаются: состояние берётся из compose ps
# и запросов внутри контейнера БД (пароль для них не нужен).
# Раздел «Дедупликация» (Phase 5): прогоны по источникам, заполненность fields,
# очередь кандидатов, слияния и режим автослияния из строки started каждого сервиса;
# потенциальные автослияния (Phase 7): dedup_would_auto, dedup_pass_last.
# compose-state требует healthy у postgres, bot и worker (у bot и worker — по
# отметке компоненты в heartbeats). Строка postgres_image — образ работающего
# контейнера БД: id, совпадает ли с ним тег postgres:17 на хосте (tag=same|differs)
# и дайджест реестра; на итог проверки не влияет.
# Mini App (Phase 15): строка webapp_profile=on|off — включён ли профиль webapp (сервис
# caddy есть в конфигурации или контейнер webapp/caddy существует). При on compose-state
# требует ещё webapp (healthy) и caddy (running). Раздел «Опубликованные порты»: порты
# есть только у caddy и только 80/tcp и 443/tcp; при off опубликованных портов нет вовсе.
# Логи caddy не печатаются: в них имя домена.
set -u
export LC_ALL=C

D=/opt/aggregator
F=$D/docker-compose.yml
FAILS=""
res() { # res <имя> <0 = OK>
  if [ "$2" -eq 0 ]; then echo "CHECK $1: OK"; else echo "CHECK $1: FAIL"; FAILS="${FAILS:+$FAILS, }$1"; fi
}
sec() { printf '\n== %s ==\n' "$1"; }

sec "Состояние сервисов"
docker compose -f "$F" ps -a
STATES=$(docker compose -f "$F" ps -a --format '{{.Service}} {{.State}} {{.Health}} {{.ExitCode}}' 2>/dev/null)
WP=off
if docker compose -f "$F" config --services 2>/dev/null | grep -qx caddy; then WP=on; fi
if echo "$STATES" | grep -Eq '^(webapp|caddy) '; then WP=on; fi
echo "webapp_profile=$WP"
if [ -z "$STATES" ]; then
  echo "CHECK compose-state: manual"
else
  echo "$STATES" | awk -v wp="$WP" '$1=="postgres" && $2=="running" && $3=="healthy" {a=1}
                        $1=="bot" && $2=="running" && $3=="healthy" {b=1}
                        $1=="worker" && $2=="running" && $3=="healthy" {w=1}
                        $1=="migrate" && $2=="exited" && $NF==0 {m=1}
                        $1=="webapp" && $2=="running" && $3=="healthy" {x=1}
                        $1=="caddy" && $2=="running" {c=1}
                        END {exit !(a && b && w && m && (wp != "on" || (x && c)))}'
  res compose-state $?
fi
# tag=same выставляется только после успешного сравнения; контейнера нет — id=none.
PGI=none PGT=differs PGD=none
PGC=$(docker compose -f "$F" ps -q postgres 2>/dev/null | head -1)
if [ -n "$PGC" ]; then
  I=$(docker container inspect --format '{{.Image}}' "$PGC" 2>/dev/null) || I=""
  if [[ $I =~ ^sha256:[0-9a-f]{64}$ ]]; then
    PGI=$I
    TI=$(docker image inspect --format '{{.Id}}' postgres:17 2>/dev/null) || TI=""
    [ "$TI" = "$PGI" ] && PGT=same
    R=$(docker image inspect --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' "$PGI" 2>/dev/null) || R=""
    R=${R##*@}
    [[ $R =~ ^sha256:[0-9a-f]{64}$ ]] && PGD=$R
  fi
fi
echo "postgres_image id=$PGI tag=$PGT repo_digest=$PGD"

sec "Порт PostgreSQL"
PUB=$(docker compose -f "$F" port postgres 5432 2>/dev/null)
if echo "$PUB" | grep -Eq ':[1-9][0-9]*$'; then RC=1; else RC=0; fi
res port-not-published $RC
if ss -ltnH 2>/dev/null | awk '{print $4}' | grep -Eq ':5432$'; then RC=1; else RC=0; fi
res port-not-listening $RC

sec "Опубликованные порты"
# Строки «<сервис> <порт хоста>/<протокол>» из колонки Ports (опубликованный порт содержит
# «->»); адреса не печатаются, окружение контейнеров не читается.
PUBS=$(docker compose -f "$F" ps -a --format '{{.Service}} {{.Ports}}' 2>/dev/null | awk '{
  for (i = 2; i <= NF; i++) if ($i ~ /->/) { p = $i; sub(/,$/, "", p); sub(/->.*\//, "/", p); sub(/^.*:/, "", p); print $1, p }
}' | sort -u)
if [ -n "$PUBS" ]; then echo "$PUBS" | sed 's/^/published=/'; else echo "published=none"; fi
if [ -n "$PUBS" ] && echo "$PUBS" | grep -vq '^caddy '; then RC=1; else RC=0; fi
res ports-only-caddy $RC
# on: у caddy ровно 80/tcp и 443/tcp; off: опубликованных портов нет вовсе.
if [ "$WP" = on ]; then
  [ "$(echo "$PUBS" | grep '^caddy ')" = "caddy 443/tcp
caddy 80/tcp" ]
else
  [ -z "$PUBS" ]
fi
res ports-caddy-80-443 $?

sec "Владельцы и права (runagg не может подменить)"
# own <путь> <владелец:группа права>
own() { [ "$(stat -c '%U:%G %a' "$1" 2>/dev/null)" = "$2" ]; }
own "$D/.env" "root:root 600"
res env-perms $?
RC=0
own "$D" "root:root 750" || RC=1
own "$F" "root:root 640" || RC=1
own /usr/local/sbin/agg "root:root 755" || RC=1
own /usr/local/lib/aggregator "root:root 755" || RC=1
for f in /usr/local/lib/aggregator/*; do own "$f" "root:root 644" || RC=1; done
own /etc/sudoers.d/runagg-aggregator "root:root 440" || RC=1
res root-owned $RC
if id -nG runagg 2>/dev/null | tr ' ' '\n' | grep -qx docker; then RC=1; else RC=0; fi
res runagg-not-in-docker $RC

sec "Сводка БД"
q() { docker compose -f "$F" exec -T postgres psql -U aggregator -d aggregator -Atc "$1"; }
DB_OK=0
q "SELECT 'run=' || id || '|' || status || '|' || parsed || '|accepted=' || CASE WHEN accepted_at IS NOT NULL THEN 't' ELSE 'f' END || '|' || translate(left(COALESCE(error, ''), 80), E'\n\r|', '   ') FROM ingest_runs ORDER BY id DESC LIMIT 3" || DB_OK=1
q "SELECT 'events_listed=' || count(*) FROM events WHERE is_listed AND merged_into_id IS NULL" || DB_OK=1
q "SELECT 'users=' || count(*) FROM users" || DB_OK=1
q "SELECT 'users_columns=' || string_agg(column_name, ',' ORDER BY column_name) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'users'" || DB_OK=1
q "SELECT 'source_alerts=' || count(*) FROM source_alerts" || DB_OK=1
res db-summary $DB_OK

sec "Дедупликация"
DD_OK=0
q "SELECT DISTINCT ON (r.source_id) 'source_run=' || s.code || '|' || r.id || '|' || r.status || '|' || r.parsed || '|accepted=' || CASE WHEN r.accepted_at IS NOT NULL THEN 't' ELSE 'f' END FROM ingest_runs r JOIN sources s ON s.id = r.source_id ORDER BY r.source_id, r.id DESC" || DD_OK=1
q "SELECT 'fields_null=' || s.code || '|' || count(se.id) FROM sources s LEFT JOIN source_events se ON se.source_id = s.id AND se.event_id IS NOT NULL AND se.state = 'active' AND se.match_state <> 'out_of_scope' AND se.fields IS NULL GROUP BY s.code ORDER BY s.code" || DD_OK=1
q "SELECT 'dedup_candidates=pending:' || count(*) FILTER (WHERE state = 'pending') || '|confirmed:' || count(*) FILTER (WHERE state = 'confirmed') || '|rejected:' || count(*) FILTER (WHERE state = 'rejected') || '|fresh:' || count(*) FILTER (WHERE state = 'pending' AND notified_at IS NULL) FROM dedup_candidates" || DD_OK=1
q "SELECT 'events_merged=' || count(*) FROM events WHERE merged_into_id IS NOT NULL" || DD_OK=1
q "SELECT 'merged=' || m.id || '>' || r.id || '|' || m.start_date || '|' || translate(left(m.title, 60), E'\n\r|', '   ') || '|' || translate(left(r.title, 60), E'\n\r|', '   ') FROM events m JOIN events r ON r.id = m.merged_into_id ORDER BY m.id DESC LIMIT 10" || DD_OK=1
# Режим автослияния — из последней строки started текущего контейнера (окружение не печатается).
for s in bot worker; do
  v=$(docker compose -f "$F" logs --no-color "$s" 2>/dev/null | grep -F '"msg":"started"' | tail -1 | grep -oE '"dedup_auto_merge":(true|false)' | cut -d: -f2)
  echo "dedup_auto_merge_$s=${v:-unknown}"
done
# Потенциальные автослияния (Phase 7): pending-кандидаты с признаком would_auto и
# счётчики последней строки «dedup pass» worker; нет строки или полей — unknown.
q "SELECT 'dedup_would_auto=pending:' || count(*) FROM dedup_candidates WHERE state = 'pending' AND reasons->>'would_auto' = 'true'" || DD_OK=1
l=$(docker compose -f "$F" logs --no-color worker 2>/dev/null | grep -F '"msg":"dedup pass"' | tail -1)
v=""
for k in would_auto would_auto_hidden would_auto_shown would_auto_decided; do
  n=$(echo "$l" | grep -oE "\"$k\":[0-9]+" | cut -d: -f2)
  [ -n "$n" ] || { v=""; break; }
  v="${v:+$v|}${k#would_auto_}:$n"
done
echo "dedup_pass_last=${v:-unknown}"
res dedup-summary $DD_OK

sec "Логи bot (последние 15 строк)"
docker compose -f "$F" logs --no-color --tail 15 bot 2>&1
sec "Логи worker (последние 15 строк)"
docker compose -f "$F" logs --no-color --tail 15 worker 2>&1
if [ "$WP" = on ]; then
  sec "Логи webapp (последние 15 строк)"
  docker compose -f "$F" logs --no-color --tail 15 webapp 2>&1
fi

echo
if [ -z "$FAILS" ]; then
  echo "DEPLOY VERIFY OK"
else
  echo "DEPLOY VERIFY FAIL: $FAILS"
  exit 1
fi
