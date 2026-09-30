#!/usr/bin/env bash
# B3 откат образа: МЕНЯЕТ ХОСТ. Вызывается как «sudo -n /usr/local/sbin/agg rollback»
# (подтверждение словом читается из stdin).
# Меняет местами IMAGE_TAG и IMAGE_PREV в /opt/aggregator/.env и пересоздаёт
# сервисы. Тома не затрагиваются. Откат безопасен, пока миграции аддитивные:
# goose старого образа не требует отсутствующих у него более новых версий.
set -euo pipefail
umask 077
export LC_ALL=C

D=/opt/aggregator
F=$D/docker-compose.yml
[ "$(id -u)" -eq 0 ] || { echo "STOP: нужен root" >&2; exit 1; }
[ -f "$D/.env" ] || { echo "STOP: нет $D/.env"; exit 1; }

CUR=$(grep -m1 '^IMAGE_TAG=' "$D/.env" | cut -d= -f2- || true)
PREV=$(grep -m1 '^IMAGE_PREV=' "$D/.env" | cut -d= -f2- || true)
[ -n "$PREV" ] || { echo "STOP: нет предыдущего образа"; exit 1; }
[[ $PREV =~ ^[A-Za-z0-9._-]{1,64}$ ]] || { echo "STOP: IMAGE_PREV в .env имеет неверный формат"; exit 1; }
docker image inspect --format '{{.Id}}' "aggregator:$PREV" >/dev/null 2>&1 \
  || { echo "STOP: образа aggregator:$PREV на хосте нет"; exit 1; }

printf 'Откат с %s на %s. Введите слово ROLLBACK: ' "${CUR:-(нет)}" "$PREV"
read -r ANS
[ "$ANS" = ROLLBACK ] || { echo "Отменено, хост не изменён."; exit 1; }

TMP=$(mktemp "$D/.env.XXXXXX")
{ grep -vE '^(IMAGE_TAG|IMAGE_PREV)=' "$D/.env" || true; } > "$TMP"
printf '%s=%s\n' IMAGE_TAG "$PREV" >> "$TMP"
if [ -n "$CUR" ]; then printf '%s=%s\n' IMAGE_PREV "$CUR" >> "$TMP"; fi
chmod 600 "$TMP"
mv "$TMP" "$D/.env"

docker compose -f "$F" up -d

echo "ROLLBACK DONE: $PREV"
