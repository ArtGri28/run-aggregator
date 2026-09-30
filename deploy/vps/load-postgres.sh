#!/usr/bin/env bash
# Запасной путь: загрузка postgres:17, перенесённого с Mac, если VPS не достаёт до Docker Hub.
# МЕНЯЕТ ХОСТ (docker load). Запускает владелец от root в консоли провайдера однострочником
# из deploy/oneliner.sh (сам скрипт проверен по SHA256SUMS пакета):
#   bash load-postgres.sh <SHA256 архива, посчитанный на Mac>
# Архив /home/runagg/agg-deploy/postgres17.tar сначала копируется во временный каталог root,
# хеш копии сверяется с хешем с Mac, и docker load выполняется только для этой копии:
# runagg не может подменить архив ни до, ни после проверки. agg load postgres не принимает.
set -euo pipefail
umask 077
export LC_ALL=C

H=${1:-}
[[ $H =~ ^[0-9a-f]{64}$ ]] || { echo "usage: load-postgres.sh <sha256 архива с Mac>" >&2; exit 2; }
[ "$(id -u)" -eq 0 ] || { echo "STOP: нужен root" >&2; exit 1; }

SRC=/home/runagg/agg-deploy/postgres17.tar
W=$(mktemp -d)
trap 'rm -rf "$W"' EXIT
cp -- "$SRC" "$W/postgres17.tar"
if ! printf '%s  %s\n' "$H" "$W/postgres17.tar" | sha256sum -c --quiet -; then
  echo "STOP: HASH MISMATCH — архив отличается от посчитанного на Mac, docker load не выполнялся"
  exit 1
fi
docker load -i "$W/postgres17.tar"
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' postgres:17)" = linux/amd64 ] \
  || { echo "STOP: postgres:17 не linux/amd64"; exit 1; }
echo "LOAD POSTGRES OK"
