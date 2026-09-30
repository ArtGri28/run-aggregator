#!/usr/bin/env bash
# Доставка образа и скриптов на VPS (D-05). Запускает владелец на Mac:
#   deploy/ship.sh <ssh-алиас>          [TAG=<тег>] — иначе короткий хеш коммита
# Порядок: локальная проверка образа (smoke.sh, linux/amd64) -> каталог на VPS ->
# docker save | gzip | ssh ... docker load -> копирование compose-файла и скриптов ->
# однострочники с SHA256. На VPS ничего не запускается, кроме создания каталога и
# загрузки образа; исходники, Go и реестр образов на VPS не нужны.
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")/.."

ALIAS=${1:-}
[[ $ALIAS =~ ^[A-Za-z0-9._-]+$ ]] || { echo "usage: $0 <ssh-alias>" >&2; exit 2; }

TAG=${TAG:-}
if [ -z "$TAG" ]; then
  # Тег образа должен соответствовать коммиту: правки не в коммите не допускаются.
  if [ -n "$(git status --porcelain -- Dockerfile .dockerignore docker-compose.yml go.mod go.sum cmd internal migrations deploy)" ]; then
    echo "STOP: закоммитьте изменения или задайте TAG явно" >&2
    exit 1
  fi
  TAG=$(git rev-parse --short=12 HEAD)
fi
[[ $TAG =~ ^[A-Za-z0-9._-]{1,64}$ ]] || { echo "недопустимый TAG" >&2; exit 2; }

mkdir -p "$HOME/run-agg-phase4"
chmod 700 "$HOME/run-agg-phase4"

# Без успешной локальной проверки образ на VPS не уходит (set -e).
deploy/smoke.sh "$TAG"

ssh "$ALIAS" 'install -d -m 700 /root/agg-deploy'
docker save "aggregator:$TAG" | gzip | ssh "$ALIAS" 'gunzip | docker load'
scp docker-compose.yml deploy/vps/*.sh "$ALIAS:/root/agg-deploy/"

TAG=$TAG deploy/oneliner.sh "$ALIAS" deploy/vps/*.sh
echo "IMAGE_TAG=$TAG"
