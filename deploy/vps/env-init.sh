#!/usr/bin/env bash
# Создание /opt/aggregator/.env (root:root 600): МЕНЯЕТ ХОСТ, разово до первого развёртывания.
# Вызывается только как «sudo -n /usr/local/sbin/agg env-init». Три строки stdin: токен бота,
# ADMIN_TG_IDS, OWNER_CONTACT. У ключа runagg нет терминала на VPS (restrict), поэтому
# скрытый ввод делает deploy/secrets.sh на Mac и передаёт значения по ssh через stdin:
# не в аргументах команд, не в выводе и не в истории. Пароль БД генерируется здесь.
# Существующий .env не меняется.
set -euo pipefail
umask 077
export LC_ALL=C

D=/opt/aggregator
[ "$(id -u)" -eq 0 ] || { echo "STOP: нужен root" >&2; exit 1; }
[ ! -e "$D/.env" ] || { echo "STOP: $D/.env уже есть, не меняю"; exit 1; }

IFS= read -r BOT_SECRET || true
IFS= read -r ADMIN_LIST || true
IFS= read -r CONTACT || true
[[ $BOT_SECRET =~ ^[0-9]+:[A-Za-z0-9_-]{30,}$ ]] || { echo "STOP: неверный формат токена"; exit 1; }
[[ $ADMIN_LIST =~ ^[0-9]+(,[0-9]+)*$ ]] || { echo "STOP: неверный формат ADMIN_TG_IDS"; exit 1; }
[[ $CONTACT =~ ^[A-Za-z0-9@._:/+-]+$ ]] || { echo "STOP: неверный формат OWNER_CONTACT"; exit 1; }
DB_SECRET=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')

install -d -o root -g root -m 750 "$D"
TMP=$(mktemp "$D/.env.XXXXXX")
{
  printf '%s=%s\n' POSTGRES_PASSWORD "$DB_SECRET"
  printf '%s=%s\n' TELEGRAM_BOT_TOKEN "$BOT_SECRET"
  printf '%s=%s\n' ADMIN_TG_IDS "$ADMIN_LIST"
  printf '%s=%s\n' OWNER_CONTACT "$CONTACT"
} > "$TMP"
chmod 600 "$TMP"
mv "$TMP" "$D/.env"
unset BOT_SECRET ADMIN_LIST CONTACT DB_SECRET
echo "ENV INIT DONE: созданы ключи POSTGRES_PASSWORD TELEGRAM_BOT_TOKEN ADMIN_TG_IDS OWNER_CONTACT (IMAGE_TAG поставит agg deploy)"
