#!/usr/bin/env bash
# Разовое создание /opt/aggregator/.env на VPS (до первого agg deploy). Запускает владелец на Mac:
#   [SSH_CONFIG=<файл>] deploy/secrets.sh <ssh-алиас runagg>
# Ключ runagg не даёт терминала на VPS (restrict), поэтому скрытый ввод делается здесь:
# токен и ADMIN_TG_IDS — без эха, OWNER_CONTACT — обычным вводом. Значения уходят по ssh
# через stdin команде «sudo -n /usr/local/sbin/agg env-init» — не в аргументах команд,
# не в выводе и не в истории оболочки. Существующий .env на VPS не меняется.
set -euo pipefail
export LC_ALL=C

ALIAS=${1:-}
[[ $ALIAS =~ ^[A-Za-z0-9._-]+$ ]] || { echo "usage: [SSH_CONFIG=..] $0 <ssh-alias>" >&2; exit 2; }
SSH_CONFIG=${SSH_CONFIG:-}
SSH_OPTS=()
if [ -n "$SSH_CONFIG" ]; then
  [[ $SSH_CONFIG =~ ^[A-Za-z0-9._/-]+$ ]] || { echo "недопустимый путь SSH_CONFIG" >&2; exit 2; }
  [ -f "$SSH_CONFIG" ] || { echo "нет файла SSH_CONFIG" >&2; exit 2; }
  SSH_OPTS=(-F "$SSH_CONFIG")
fi
[ -t 0 ] || { echo "нужен терминал для скрытого ввода" >&2; exit 1; }

printf 'Токен бота (ввод скрыт): '
IFS= read -rs BOT_SECRET
echo
[[ $BOT_SECRET =~ ^[0-9]+:[A-Za-z0-9_-]{30,}$ ]] || { echo "неверный формат токена" >&2; exit 1; }
printf 'ADMIN_TG_IDS, числовые Telegram ID через запятую (ввод скрыт): '
IFS= read -rs ADMIN_LIST
echo
[[ $ADMIN_LIST =~ ^[0-9]+(,[0-9]+)*$ ]] || { echo "неверный формат ADMIN_TG_IDS" >&2; exit 1; }
printf 'OWNER_CONTACT (почта или ссылка для User-Agent источникам): '
IFS= read -r CONTACT
[[ $CONTACT =~ ^[A-Za-z0-9@._:/+-]+$ ]] || { echo "неверный формат OWNER_CONTACT" >&2; exit 1; }

# printf — встроенная команда: значения не видны в списке процессов.
printf '%s\n' "$BOT_SECRET" "$ADMIN_LIST" "$CONTACT" \
  | ssh ${SSH_OPTS[@]+"${SSH_OPTS[@]}"} "$ALIAS" 'sudo -n /usr/local/sbin/agg env-init'
unset BOT_SECRET ADMIN_LIST CONTACT
