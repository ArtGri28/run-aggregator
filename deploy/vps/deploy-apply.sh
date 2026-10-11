#!/usr/bin/env bash
# B1 APPLY развёртывания: МЕНЯЕТ ХОСТ. Использование: deploy-apply.sh <TAG>.
# Вызывается как «sudo -n /usr/local/sbin/agg deploy <TAG>» (подтверждение словом читается из stdin).
# Что делает: каталог /opt/aggregator, compose-файл из /usr/local/lib/aggregator (root),
# IMAGE_TAG/IMAGE_PREV в .env (сам .env создаёт agg env-init), затем postgres -> migrate,
# bot, worker. Всё в /opt/aggregator — root:root, runagg туда не пишет и не читает.
# Тома проекта не удаляются никогда.
# Имеющийся на хосте образ postgres:17 выкат не обновляет (pull только когда образа
# нет): новый образ под тем же тегом пересоздал бы контейнер БД. Смена образа
# postgres — отдельная процедура runbook.
# При включённом профиле webapp (COMPOSE_PROFILES в .env) так же, до запуска сервисов,
# проверяется образ caddy из compose-файла: есть на хосте — pull пропускается.
set -euo pipefail
umask 077
export LC_ALL=C

TAG=${1:-}
[[ $TAG =~ ^[A-Za-z0-9._-]{1,64}$ ]] || { echo "usage: deploy-apply.sh <TAG>" >&2; exit 2; }
[ "$(id -u)" -eq 0 ] || { echo "STOP: нужен root" >&2; exit 1; }

D=/opt/aggregator
F=$D/docker-compose.yml
SRC=/usr/local/lib/aggregator

[ -f "$SRC/docker-compose.yml" ] || { echo "STOP: нет $SRC/docker-compose.yml (выполните разовую установку agg-install.sh)"; exit 1; }
[ -f "$D/.env" ] || { echo "STOP: нет $D/.env — сначала deploy/secrets.sh на Mac (agg env-init)"; exit 1; }
ARCH=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "aggregator:$TAG" 2>/dev/null) || ARCH=""
[ "$ARCH" = linux/amd64 ] || { echo "STOP: образа aggregator:$TAG нет или он не linux/amd64"; exit 1; }

printf 'Развернуть aggregator:%s в %s. Введите слово DEPLOY: ' "$TAG" "$D"
read -r ANS
[ "$ANS" = DEPLOY ] || { echo "Отменено, хост не изменён."; exit 1; }

install -d -o root -g root -m 750 "$D"
install -o root -g root -m 640 "$SRC/docker-compose.yml" "$F"

# Меняются только IMAGE_TAG (новый) и IMAGE_PREV (прежний); остальное не трогается.
CUR=$(grep -m1 '^IMAGE_TAG=' "$D/.env" | cut -d= -f2- || true)
OLD_PREV=$(grep -m1 '^IMAGE_PREV=' "$D/.env" | cut -d= -f2- || true)
PREV=$OLD_PREV
if [ -n "$CUR" ] && [ "$CUR" != "$TAG" ]; then PREV=$CUR; fi

# Прежний .env хранится до успешного запуска: при ошибке или обрыве (ssh, сигнал)
# IMAGE_TAG/IMAGE_PREV возвращаются, и последний рабочий тег не выпадает из отката.
OLD=$(mktemp "$D/.env.prev.XXXXXX")
cp "$D/.env" "$OLD"
restore() { if [ -f "$OLD" ]; then mv "$OLD" "$D/.env"; echo "Возвращены IMAGE_TAG=${CUR:-(нет)} и IMAGE_PREV=${OLD_PREV:-(нет)}."; fi; }
trap restore EXIT

TMP=$(mktemp "$D/.env.XXXXXX")
{ grep -vE '^(IMAGE_TAG|IMAGE_PREV)=' "$D/.env" || true; } > "$TMP"
printf '%s=%s\n' IMAGE_TAG "$TAG" >> "$TMP"
if [ -n "$PREV" ]; then printf '%s=%s\n' IMAGE_PREV "$PREV" >> "$TMP"; fi
chmod 600 "$TMP"
mv "$TMP" "$D/.env"
echo "Обновлены IMAGE_TAG=$TAG и IMAGE_PREV=${PREV:-(нет)}."

# start — запуск с текущим .env; внутри «if !» set -e не действует, поэтому каждый шаг явно.
start() {
  docker compose -f "$F" config -q || return 1
  if docker image inspect --format '{{.Id}}' postgres:17 >/dev/null 2>&1; then
    echo "postgres image: local, pull skipped"
  elif ! docker compose -f "$F" pull postgres; then
    echo "Не удалось получить postgres:17. Запасной путь — перенос образа с Mac, см. deploy/README.md."
    return 1
  fi
  if docker compose -f "$F" config --services | grep -qx caddy; then
    CADDY=$(docker compose -f "$F" config --images | grep -m1 '^caddy[:@]') || return 1
    if docker image inspect --format '{{.Id}}' "$CADDY" >/dev/null 2>&1; then
      echo "caddy image: local, pull skipped"
    elif ! docker compose -f "$F" pull caddy; then
      echo "Не удалось получить образ caddy (профиль webapp включён). Сервисы не переключались: повторите позже либо уберите COMPOSE_PROFILES из .env, см. deploy/README.md."
      return 1
    fi
  fi
  docker compose -f "$F" up -d --wait postgres && docker compose -f "$F" up -d
}

if ! start; then
  echo "STOP: запуск aggregator:$TAG не удался."
  restore
  if [ -n "$CUR" ]; then
    echo "Поднимаю прежний образ aggregator:$CUR."
    docker compose -f "$F" up -d || echo "Прежний образ тоже не поднялся — смотрите agg verify."
  fi
  exit 1
fi
rm -f "$OLD"

echo "DEPLOY APPLY DONE: проверьте agg verify через 1–2 минуты"
