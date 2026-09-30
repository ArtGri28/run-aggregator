#!/usr/bin/env bash
# B2 VERIFY развёртывания: ТОЛЬКО ЧТЕНИЕ. Вызывается как «sudo -n /usr/local/sbin/agg verify»
# с Mac через 1–2 минуты после agg deploy; вывод — в
# $HOME/run-agg-phase4/. Секреты не печатаются: состояние берётся из compose ps
# и запросов внутри контейнера БД (пароль для них не нужен).
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
if [ -z "$STATES" ]; then
  echo "CHECK compose-state: manual"
else
  echo "$STATES" | awk '$1=="postgres" && $2=="running" && $3=="healthy" {a=1}
                        $1=="bot" && $2=="running" && $3=="healthy" {b=1}
                        $1=="worker" && $2=="running" {w=1}
                        $1=="migrate" && $2=="exited" && $NF==0 {m=1}
                        END {exit !(a && b && w && m)}'
  res compose-state $?
fi

sec "Порт PostgreSQL"
PUB=$(docker compose -f "$F" port postgres 5432 2>/dev/null)
if echo "$PUB" | grep -Eq ':[1-9][0-9]*$'; then RC=1; else RC=0; fi
res port-not-published $RC
if ss -ltnH 2>/dev/null | awk '{print $4}' | grep -Eq ':5432$'; then RC=1; else RC=0; fi
res port-not-listening $RC

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

sec "Логи bot (последние 15 строк)"
docker compose -f "$F" logs --no-color --tail 15 bot 2>&1
sec "Логи worker (последние 15 строк)"
docker compose -f "$F" logs --no-color --tail 15 worker 2>&1

echo
if [ -z "$FAILS" ]; then
  echo "DEPLOY VERIFY OK"
else
  echo "DEPLOY VERIFY FAIL: $FAILS"
  exit 1
fi
