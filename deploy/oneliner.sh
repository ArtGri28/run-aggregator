#!/usr/bin/env bash
# Команды для VPS после deploy/ship.sh; сам ничего не выполняет.
#   deploy/oneliner.sh files                   — файлы пакета (для ship.sh)
#   deploy/oneliner.sh sums                    — содержимое SHA256SUMS пакета
#   deploy/oneliner.sh installed               — ожидаемые SHA256 установленных agg и lib
#   [TAG=<тег>] [SSH_CONFIG=<файл>] deploy/oneliner.sh <ssh-алиас runagg>
# Два вида команд:
#   - root-консоль провайдера (разовая установка и изменения хоста): пакет из
#     /home/runagg/agg-deploy копируется во временный каталог root, сверяется SHA256
#     файла SHA256SUMS и по нему всех файлов; при несовпадении — «STOP: HASH MISMATCH»,
#     ничего не запускается;
#   - Mac: ssh под runagg и «sudo -n /usr/local/sbin/agg <команда>»; вывод команд
#     только чтения — в $HOME/run-agg-phase4/ (бэкап — в $HOME/run-agg-phase6/) вне репозитория.
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")/.."

BUNDLE="deploy/vps/agg deploy/vps/agg-install.sh deploy/vps/deploy-apply.sh deploy/vps/deploy-preflight.sh
  deploy/vps/deploy-verify.sh deploy/vps/env-init.sh deploy/vps/host-apply.sh deploy/vps/host-preflight.sh
  deploy/vps/host-verify.sh deploy/vps/load-postgres.sh deploy/vps/rollback.sh docker-compose.yml
  deploy/vps/backup.sh deploy/vps/backup-lib.sh deploy/vps/backup-status.sh deploy/vps/backup-recipient
  deploy/vps/backup-apply.sh deploy/vps/clock-apply.sh"
STAGE=/home/runagg/agg-deploy
AGG=/usr/local/sbin/agg
LIB=/usr/local/lib/aggregator

sha() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$@"; else sha256sum "$@"; fi | cut -d' ' -f1
}
sums() { for f in $BUNDLE; do printf '%s  %s\n' "$(sha "$f")" "${f##*/}"; done; }

case "${1:-}" in
  files) echo $BUNDLE; exit 0 ;;
  sums) sums; exit 0 ;;
  installed)
    # Как в agg-install.sh: agg — в /usr/local/sbin, в lib — всё, кроме скриптов root-консоли.
    sums | awk -v a="$AGG" -v l="$LIB" '$2 == "agg" {print $1 "  " a; next}
      $2 != "agg-install.sh" && $2 != "host-apply.sh" && $2 != "load-postgres.sh" && $2 != "backup-apply.sh" && $2 != "clock-apply.sh" {print $1 "  " l "/" $2}'
    exit 0 ;;
esac

[ $# -eq 1 ] || { echo "usage: [TAG=..] [SSH_CONFIG=..] $0 <ssh-alias> | files | sums | installed" >&2; exit 2; }
ALIAS=$1
[[ $ALIAS =~ ^[A-Za-z0-9._-]+$ ]] || { echo "недопустимый ssh-алиас" >&2; exit 2; }
SSH_CONFIG=${SSH_CONFIG:-}
if [ -n "$SSH_CONFIG" ] && ! [[ $SSH_CONFIG =~ ^[A-Za-z0-9._/-]+$ ]]; then
  echo "недопустимый путь SSH_CONFIG" >&2
  exit 2
fi
SSHF=${SSH_CONFIG:+ -F $SSH_CONFIG}
TAG=${TAG:-<TAG>}
if [ "$TAG" != "<TAG>" ] && ! [[ $TAG =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]; then
  echo "недопустимый TAG" >&2
  exit 2
fi

H=$(sums | sha)
# root <команда>: однострочник для root-консоли; if/else — сбой самого скрипта не выдаётся
# за несовпадение хеша.
root() {
  echo "T=\$(mktemp -d) && cp $STAGE/* \"\$T\"/ && cd \"\$T\" && if echo '$H  SHA256SUMS' | sha256sum -c --quiet - && sha256sum -c --quiet SHA256SUMS; then $*; else echo 'STOP: HASH MISMATCH'; fi"
}
# ro <подкоманда> <имя лога> [каталог логов в $HOME]: без третьего аргумента лог идёт в
# run-agg-phase4 (каталог создаёт ship.sh); заданный каталог строка создаёт сама (700).
ro() {
  local mk=""
  if [ -n "${3:-}" ]; then mk="mkdir -p -m 700 \"\$HOME/$3\" && "; fi
  echo "${mk}ssh$SSHF $ALIAS 'sudo -n $AGG $1' 2>&1 | tee \"\$HOME/${3:-run-agg-phase4}/$2.log\""
}
rw() { echo "ssh$SSHF $ALIAS 'sudo -n $AGG $1'"; }

cat <<EOF
== Root-консоль провайдера: разовая установка (слово INSTALL) ==
SHA256 SHA256SUMS: $H
$(root bash agg-install.sh)

Если вставка в консоли не работает — две короткие команды; сверьте напечатанный хеш
SHA256SUMS с строкой выше глазами, при «FILES OK» запустите вторую:
T=\$(mktemp -d) && cp $STAGE/* "\$T"/ && cd "\$T" && sha256sum SHA256SUMS && sha256sum -c --quiet SHA256SUMS && echo FILES OK
bash agg-install.sh

== Root-консоль: изменения хоста, только по решению владельца ==
$(root bash host-apply.sh updates)
systemctl reboot   # вручную, когда готовы
$(root bash host-apply.sh compose)

== Root-консоль: бэкап, каждый шаг только по отдельному разрешению владельца; внешняя копия — временное хранилище в Telegram (D-17) ==
$(root bash host-apply.sh age)
$(root bash backup-apply.sh dirs)
/bin/bash /usr/local/lib/aggregator/backup.sh; echo "exit=\$?"   # ручной запуск бэкапа
$(root bash backup-apply.sh telegram)    # секреты вводит владелец, ввод скрыт
$(root bash backup-apply.sh cron)

== Root-консоль: серверы времени (06-04), только по отдельному разрешению владельца на изменение VPS ==
$(root bash clock-apply.sh apply)    # слово CLOCK
$(root bash clock-apply.sh undo)    # откат, слово UNCLOCK

== Root-консоль: запасной путь postgres:17 (только если agg deploy не смог скачать) ==
$(root bash load-postgres.sh '<SHA256 postgres17.tar с Mac>')

== Mac, под runagg ==
host-preflight: $(ro host-preflight host-preflight)
host-verify:    $(ro host-verify host-verify)
.env (разово): ${SSH_CONFIG:+SSH_CONFIG=$SSH_CONFIG }deploy/secrets.sh $ALIAS
preflight:      $(ro "preflight $TAG" deploy-preflight)
deploy:         $(rw "deploy $TAG")    # слово DEPLOY
verify:         $(ro verify deploy-verify)
rollback:       $(rw rollback)    # слово ROLLBACK
backup-status:  $(ro backup-status backup-status run-agg-phase6)
EOF
