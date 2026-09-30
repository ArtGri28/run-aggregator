#!/usr/bin/env bash
# B0 PREFLIGHT развёртывания: ТОЛЬКО ЧТЕНИЕ, хост не меняется.
# Использование: deploy-preflight.sh <TAG>. Запускает владелец с Mac однострочником
# из deploy/oneliner.sh (от root); вывод — в $HOME/run-agg-phase4/ вне репозитория.
# Значения из .env не читаются в переменные и не печатаются: только «есть/нет».
set -u
export LC_ALL=C

TAG=${1:-}
[[ $TAG =~ ^[A-Za-z0-9._-]{1,64}$ ]] || { echo "usage: deploy-preflight.sh <TAG>" >&2; exit 2; }
D=/opt/aggregator
F=$D/docker-compose.yml

sec() { printf '\n== %s ==\n' "$1"; }

sec "Compose"
docker compose version 2>&1 | head -2

sec "Образ aggregator:$TAG"
IMG=$(docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}}' "aggregator:$TAG" 2>&1)
echo "$IMG"
case "$IMG" in
  *linux/amd64) echo "образ: OK (linux/amd64)" ;;
  *) echo "образ: НЕТ или другая архитектура" ;;
esac

sec "Каталог $D"
if [ -d "$D" ]; then
  stat -c '%a %U:%G %n' "$D"
else
  echo "каталога нет (будет создан в APPLY)"
fi

sec ".env"
if [ -f "$D/.env" ]; then
  printf 'права: %s (ожидается 600)\n' "$(stat -c '%a' "$D/.env")"
  for k in POSTGRES_PASSWORD TELEGRAM_BOT_TOKEN ADMIN_TG_IDS OWNER_CONTACT IMAGE_TAG; do
    if grep -q "^$k=." "$D/.env"; then echo "$k: есть"; else echo "$k: нет"; fi
  done
else
  echo ".env: нет (будет создан в APPLY)"
fi

sec "Конфигурация compose"
if [ -f "$D/.env" ] && [ -f "$F" ]; then
  if docker compose -f "$F" config -q 2>&1 | head -5; [ "${PIPESTATUS[0]}" -eq 0 ]; then
    echo "compose config -q: OK"
  else
    echo "compose config -q: FAIL"
  fi
else
  echo "пропущено: нет .env или compose-файла в $D"
fi

sec "SHA256 compose-файла (сравните с выводом oneliner.sh на Mac)"
for f in /root/agg-deploy/docker-compose.yml "$F"; do
  if [ -f "$f" ]; then sha256sum "$f"; else echo "нет файла: $f"; fi
done

sec "Проект"
if [ -f "$D/.env" ] && [ -f "$F" ]; then
  docker compose -f "$F" ps -a 2>&1
else
  echo "проект ещё не создан"
fi

echo
echo "DEPLOY PREFLIGHT DONE"
