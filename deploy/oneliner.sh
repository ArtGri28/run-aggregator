#!/usr/bin/env bash
# Печатает однострочники для запуска скриптов на VPS с проверкой полного SHA256.
# Запускает владелец на Mac после deploy/ship.sh. Ничего не выполняет на VPS.
#
# Использование: [TAG=<тег образа>] [AGG_DIR=<каталог на VPS>] [SSH_CONFIG=<файл>] deploy/oneliner.sh <ssh-алиас> <файл>...
# Для каждого скрипта печатается:
#   - VPS-форма: хеш сверяется на самом VPS; при несовпадении скрипт не запускается
#     и печатается «STOP: HASH MISMATCH» (if/else, а не &&/||: сбой самого скрипта
#     не выдаётся за несовпадение хеша);
#   - Mac-форма: команда ssh для запуска с Mac. Скрипты только чтения пишут вывод
#     в $HOME/run-agg-phase4/ вне репозитория; меняющие хост идут через ssh -t
#     (интерактивное подтверждение) без tee.
set -euo pipefail
export LC_ALL=C

usage() { echo "usage: [TAG=..] [AGG_DIR=..] $0 <ssh-alias> <file>..." >&2; exit 2; }
[ $# -ge 2 ] || usage
ALIAS=$1
shift
[[ $ALIAS =~ ^[A-Za-z0-9._-]+$ ]] || { echo "недопустимый ssh-алиас" >&2; exit 2; }
DIR=${AGG_DIR:-/root/agg-deploy}
[[ $DIR =~ ^[A-Za-z0-9._/-]+$ ]] || { echo "недопустимый AGG_DIR" >&2; exit 2; }
# SSH_CONFIG — отдельный файл настроек ssh: в Mac-формы добавляется -F <файл>.
SSH_CONFIG=${SSH_CONFIG:-}
if [ -n "$SSH_CONFIG" ] && ! [[ $SSH_CONFIG =~ ^[A-Za-z0-9._/-]+$ ]]; then
  echo "недопустимый путь SSH_CONFIG" >&2
  exit 2
fi
SSHF=${SSH_CONFIG:+ -F $SSH_CONFIG}
TAG=${TAG:-<TAG>}
if [ "$TAG" != "<TAG>" ] && ! [[ $TAG =~ ^[A-Za-z0-9._-]{1,64}$ ]]; then
  echo "недопустимый TAG" >&2
  exit 2
fi

sha() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    sha256sum "$1" | cut -d' ' -f1
  fi
}

# emit <файл> <режим: ro|rw> [аргументы скрипта...]
emit() {
  local file=$1 mode=$2 name hash vps
  shift 2
  name=$(basename "$file")
  hash=$(sha "$file")
  vps="cd $DIR && if printf '%s  %s\\n' $hash $name | sha256sum -c --quiet -; then bash $name${*:+ $*}; else echo 'STOP: HASH MISMATCH'; fi"
  echo "== $name${*:+ $*} =="
  echo "VPS:"
  echo "$vps"
  echo "Mac:"
  if [ "$mode" = ro ]; then
    echo "ssh$SSHF $ALIAS \"$vps\" 2>&1 | tee \"\$HOME/run-agg-phase4/${name%.sh}.log\""
  else
    echo "ssh$SSHF -t $ALIAS \"$vps\""
  fi
  echo
}

for f in "$@"; do
  [ -f "$f" ] || { echo "нет файла: $f" >&2; exit 2; }
  case "$(basename "$f")" in
    host-apply.sh)
      emit "$f" rw updates
      emit "$f" rw compose ;;
    deploy-apply.sh) emit "$f" rw "$TAG" ;;
    rollback.sh) emit "$f" rw ;;
    deploy-preflight.sh) emit "$f" ro "$TAG" ;;
    *) emit "$f" ro ;;
  esac
done

COMPOSE="$(dirname "$0")/../docker-compose.yml"
[ ! -f "$COMPOSE" ] || echo "SHA256 docker-compose.yml: $(sha "$COMPOSE")"
