#!/usr/bin/env bash
# B1 APPLY развёртывания: МЕНЯЕТ ХОСТ. Использование: deploy-apply.sh <TAG>.
# Запускает владелец от root через ssh -t (нужен терминал для скрытого ввода).
# Что делает: каталог /opt/aggregator, compose-файл из /root/agg-deploy, при
# отсутствии .env — интерактивное создание (права 600), затем postgres -> migrate,
# bot, worker. Значения секретов не попадают в аргументы команд, вывод и историю:
# ввод без эха, запись builtin-командой printf. Тома проекта не удаляются никогда.
set -euo pipefail
umask 077
export LC_ALL=C

TAG=${1:-}
[[ $TAG =~ ^[A-Za-z0-9._-]{1,64}$ ]] || { echo "usage: deploy-apply.sh <TAG>" >&2; exit 2; }
[ "$(id -u)" -eq 0 ] || { echo "STOP: нужен root" >&2; exit 1; }

D=/opt/aggregator
F=$D/docker-compose.yml
SRC=/root/agg-deploy

[ -f "$SRC/docker-compose.yml" ] || { echo "STOP: нет $SRC/docker-compose.yml (запустите deploy/ship.sh)"; exit 1; }
ARCH=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "aggregator:$TAG" 2>/dev/null) || ARCH=""
[ "$ARCH" = linux/amd64 ] || { echo "STOP: образа aggregator:$TAG нет или он не linux/amd64"; exit 1; }

printf 'Развернуть aggregator:%s в %s. Введите слово DEPLOY: ' "$TAG" "$D"
read -r ANS
[ "$ANS" = DEPLOY ] || { echo "Отменено, хост не изменён."; exit 1; }

install -d -m 750 "$D"
install -m 640 "$SRC/docker-compose.yml" "$F"

if [ ! -f "$D/.env" ]; then
  [ -t 0 ] || { echo "STOP: для ввода секретов нужен терминал (ssh -t)"; exit 1; }
  echo "Создаётся $D/.env (значения не показываются)."
  printf 'Токен бота (ввод скрыт): ' >&2
  read -rs BOT_SECRET
  echo >&2
  [[ $BOT_SECRET =~ ^[0-9]+:[A-Za-z0-9_-]{30,}$ ]] || { echo "STOP: неверный формат токена"; exit 1; }
  printf 'ADMIN_TG_IDS, числовые Telegram ID через запятую (ввод скрыт): ' >&2
  read -rs ADMIN_LIST
  echo >&2
  [[ $ADMIN_LIST =~ ^[0-9]+(,[0-9]+)*$ ]] || { echo "STOP: неверный формат ADMIN_TG_IDS"; exit 1; }
  printf 'OWNER_CONTACT (почта или ссылка для User-Agent источникам): '
  read -r CONTACT
  [[ $CONTACT =~ ^[A-Za-z0-9@._:/+-]+$ ]] || { echo "STOP: неверный формат OWNER_CONTACT"; exit 1; }
  DB_SECRET=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')

  TMP=$(mktemp "$D/.env.XXXXXX")
  {
    printf '%s=%s\n' POSTGRES_PASSWORD "$DB_SECRET"
    printf '%s=%s\n' TELEGRAM_BOT_TOKEN "$BOT_SECRET"
    printf '%s=%s\n' ADMIN_TG_IDS "$ADMIN_LIST"
    printf '%s=%s\n' OWNER_CONTACT "$CONTACT"
    printf '%s=%s\n' IMAGE_TAG "$TAG"
  } > "$TMP"
  chmod 600 "$TMP"
  mv "$TMP" "$D/.env"
  unset BOT_SECRET ADMIN_LIST CONTACT DB_SECRET
  echo "Созданы ключи: POSTGRES_PASSWORD TELEGRAM_BOT_TOKEN ADMIN_TG_IDS OWNER_CONTACT IMAGE_TAG"
else
  # Обновление: меняются только IMAGE_TAG (новый) и IMAGE_PREV (прежний); остальное не трогается.
  CUR=$(grep -m1 '^IMAGE_TAG=' "$D/.env" | cut -d= -f2- || true)
  OLD_PREV=$(grep -m1 '^IMAGE_PREV=' "$D/.env" | cut -d= -f2- || true)
  PREV=$OLD_PREV
  if [ -n "$CUR" ] && [ "$CUR" != "$TAG" ]; then PREV=$CUR; fi
  TMP=$(mktemp "$D/.env.XXXXXX")
  { grep -vE '^(IMAGE_TAG|IMAGE_PREV)=' "$D/.env" || true; } > "$TMP"
  printf '%s=%s\n' IMAGE_TAG "$TAG" >> "$TMP"
  if [ -n "$PREV" ]; then printf '%s=%s\n' IMAGE_PREV "$PREV" >> "$TMP"; fi
  chmod 600 "$TMP"
  mv "$TMP" "$D/.env"
  echo "Обновлены IMAGE_TAG=$TAG и IMAGE_PREV=${PREV:-(нет)}."
fi

docker compose -f "$F" config -q
if ! docker compose -f "$F" pull postgres; then
  if docker image inspect --format '{{.Id}}' postgres:17 >/dev/null 2>&1; then
    echo "pull не удался, но postgres:17 уже есть на хосте — продолжаем."
  else
    echo "STOP: не удалось получить postgres:17. Запасной путь — перенос образа с Mac, см. deploy/README.md."
    exit 1
  fi
fi
docker compose -f "$F" up -d --wait postgres
docker compose -f "$F" up -d

echo "DEPLOY APPLY DONE: проверьте deploy-verify.sh через 1–2 минуты"
