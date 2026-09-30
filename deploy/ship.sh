#!/usr/bin/env bash
# Доставка образа и пакета скриптов на VPS под runagg (D-05). Запускает владелец на Mac:
#   [TAG=<тег>] [SSH_CONFIG=<файл>] deploy/ship.sh <ssh-алиас runagg>
# TAG — иначе короткий хеш коммита; SSH_CONFIG — отдельный файл настроек ssh (ssh -F),
# передаётся в ssh, scp и в напечатанные команды.
# Порядок: smoke.sh (linux/amd64) -> пакет скриптов и SHA256SUMS в /home/runagg/agg-deploy ->
# сверка SHA256 установленных /usr/local/sbin/agg и /usr/local/lib/aggregator/* с
# репозиторием (не установлено или отличается — STOP и команда разовой установки для
# root-консоли; образ не передаётся) -> docker save | gzip | ssh ... 'gunzip | sudo -n
# /usr/local/sbin/agg load <TAG>' -> команды для следующих шагов. Root по ssh не нужен:
# runagg может через sudo только /usr/local/sbin/agg.
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")/.."

ALIAS=${1:-}
[[ $ALIAS =~ ^[A-Za-z0-9._-]+$ ]] || { echo "usage: $0 <ssh-alias>" >&2; exit 2; }

SSH_CONFIG=${SSH_CONFIG:-}
SSH_OPTS=()
if [ -n "$SSH_CONFIG" ]; then
  [[ $SSH_CONFIG =~ ^[A-Za-z0-9._/-]+$ ]] || { echo "недопустимый путь SSH_CONFIG" >&2; exit 2; }
  [ -f "$SSH_CONFIG" ] || { echo "нет файла SSH_CONFIG" >&2; exit 2; }
  SSH_OPTS=(-F "$SSH_CONFIG")
fi
# ${arr[@]+...}: пустой массив при set -u в bash 3.2 (macOS) иначе считается неопределённым.
ssh_() { ssh ${SSH_OPTS[@]+"${SSH_OPTS[@]}"} "$@"; }

TAG=${TAG:-}
if [ -z "$TAG" ]; then
  # Тег образа должен соответствовать коммиту: правки не в коммите не допускаются.
  if [ -n "$(git status --porcelain -- Dockerfile .dockerignore docker-compose.yml go.mod go.sum cmd internal migrations deploy)" ]; then
    echo "STOP: закоммитьте изменения или задайте TAG явно" >&2
    exit 1
  fi
  TAG=$(git rev-parse --short=12 HEAD)
fi
# Тот же формат, что проверяет agg на VPS: TAG попадает в строку удалённой команды, поэтому
# в нём нет пробелов, кавычек, $, ;, / и ведущего «-».
[[ $TAG =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || { echo "недопустимый TAG" >&2; exit 2; }

mkdir -p "$HOME/run-agg-phase4"
chmod 700 "$HOME/run-agg-phase4"

# Без успешной локальной проверки образ на VPS не уходит (set -e).
deploy/smoke.sh "$TAG"

B=$(mktemp -d)
trap 'rm -rf "$B"' EXIT
cp $(deploy/oneliner.sh files) "$B"/
deploy/oneliner.sh sums > "$B/SHA256SUMS"
(cd "$B" && shasum -a 256 -c --quiet SHA256SUMS)

STAGE=$(ssh_ "$ALIAS" 'install -d -m 700 agg-deploy && cd agg-deploy && pwd')
[ "$STAGE" = /home/runagg/agg-deploy ] || { echo "STOP: каталог пакета на VPS не /home/runagg/agg-deploy — алиас не runagg?" >&2; exit 1; }
scp ${SSH_OPTS[@]+"${SSH_OPTS[@]}"} "$B"/* "$ALIAS:agg-deploy/"

if ! deploy/oneliner.sh installed | ssh_ "$ALIAS" 'sha256sum -c --quiet -'; then
  echo
  echo "STOP: /usr/local/sbin/agg или /usr/local/lib/aggregator на VPS не установлены или отличаются от"
  echo "репозитория. Образ не передавался. Выполните разовую установку в root-консоли провайдера"
  echo "(пакет уже в /home/runagg/agg-deploy), затем повторите deploy/ship.sh."
  echo
  TAG=$TAG SSH_CONFIG=$SSH_CONFIG deploy/oneliner.sh "$ALIAS"
  exit 1
fi

docker save "aggregator:$TAG" | gzip | ssh_ "$ALIAS" "gunzip | sudo -n /usr/local/sbin/agg load $TAG"

TAG=$TAG SSH_CONFIG=$SSH_CONFIG deploy/oneliner.sh "$ALIAS"
echo "IMAGE_TAG=$TAG"
