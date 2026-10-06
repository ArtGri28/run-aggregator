#!/usr/bin/env bash
# Локальная проверка образа и production-состава (PRD-01). Запускает Mac,
# ничего не отправляет на VPS и не обращается к Telegram и источникам:
# сеть нужна только для загрузки базовых образов Docker.
#
# Использование: deploy/smoke.sh [TAG]   (TAG по умолчанию smoke)
#   SMOKE_SKIP_BUILD=1 — не пересобирать образ aggregator:TAG.
# Compose работает в отдельном проекте aggregator-smoke с временным env-файлом
# и фиктивными значениями; по выходу удаляется только этот проект (с его
# томами) и временный каталог. Значения env-файла не печатаются.
set -euo pipefail
export LC_ALL=C
cd "$(dirname "$0")/.."

TAG=${1:-smoke}
IMG="aggregator:$TAG"
STEP=старт
T=""
CID=""
DC=()

cleanup() {
  local rc=$?
  trap - EXIT
  if [ -n "$T" ]; then
    "${DC[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    rm -rf "$T"
  fi
  [ -z "$CID" ] || docker rm -f "$CID" >/dev/null 2>&1 || true
  if [ "$rc" -ne 0 ]; then
    echo "SMOKE FAIL: $STEP" >&2
  else
    echo "SMOKE OK"
  fi
  exit "$rc"
}
trap cleanup EXIT

# ok "имя шага" — печатает результат уже выполненной проверки.
ok() { echo "ok: $1"; }

STEP="сборка образа"
if [ "${SMOKE_SKIP_BUILD:-}" != 1 ]; then
  t0=$(date +%s)
  docker build -q --platform linux/amd64 --provenance=false --sbom=false -t "$IMG" . >/dev/null
  echo "время сборки: $(( $(date +%s) - t0 )) с"
fi
echo "размер образа: $(docker image inspect --format '{{.Size}}' "$IMG" | awk '{printf "%.1f МБ", $1/1048576}')"

STEP="архитектура и пользователь образа"
[ "$(docker image inspect --format '{{.Architecture}} {{.Config.User}}' "$IMG")" = "amd64 65532:65532" ]
ok "linux/amd64, пользователь 65532"

STEP="содержимое образа (нет .env, .planning, .git, MVP.md)"
CID=$(docker create --platform linux/amd64 "$IMG")
LIST=$(mktemp)
docker export "$CID" | tar -t > "$LIST"
docker rm "$CID" >/dev/null
CID=""
if grep -Eq '(^|/)(\.env|\.planning|\.git|MVP\.md)(/|\.|$)' "$LIST"; then
  rm -f "$LIST"
  exit 1
fi
rm -f "$LIST"
ok "в образе нет секретов и служебных файлов"

STEP="временный проект"
T=$(mktemp -d)
umask 077
cp docker-compose.yml "$T/docker-compose.yml"
{
  printf 'POSTGRES_PASSWORD=%s\n' "$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
  printf 'IMAGE_TAG=%s\n' "$TAG"
  printf 'TELEGRAM_BOT_TOKEN=0:smoke\n'
  printf 'ADMIN_TG_IDS=1\n'
  printf 'OWNER_CONTACT=smoke@example.invalid\n'
} > "$T/.env"
# Переменные оболочки с теми же именами снимаются: иначе compose подставил бы чужие значения.
DC=(env -u TELEGRAM_BOT_TOKEN -u ADMIN_TG_IDS -u SUPPORT_CHAT_ID -u POSTGRES_PASSWORD -u OWNER_CONTACT
  -u IMAGE_TAG -u DATABASE_URL -u PGPASSWORD -u LOG_FORMAT
  -u DEDUP_HIGH -u DEDUP_LOW -u DEDUP_AUTO_MERGE -u DEDUP_LINK_BONUS
  -u DEDUP_MAX_PER_EVENT -u DEDUP_DISTANCE_TOL_M -u DEDUP_SOURCE_PRIORITY -u DEDUP_DIGEST_AT
  docker compose -p aggregator-smoke --project-directory "$T" -f "$T/docker-compose.yml")

STEP="compose config"
"${DC[@]}" config -q
ok "compose config"

STEP="postgres healthy"
"${DC[@]}" up -d --wait postgres >/dev/null 2>&1
ok "postgres healthy"

STEP="migrate"
"${DC[@]}" run --rm migrate >/dev/null 2>&1
ok "migrate завершился с кодом 0"

STEP="healthcheck"
"${DC[@]}" run --rm --no-deps bot healthcheck >/dev/null 2>&1
ok "healthcheck"

STEP="bot без токена"
rc=0
out=$("${DC[@]}" run --rm --no-deps -e TELEGRAM_BOT_TOKEN= bot bot 2>&1) || rc=$?
[ "$rc" -eq 1 ]
grep -q 'TELEGRAM_BOT_TOKEN' <<<"$out"
ok "bot без токена: код 1, названа переменная"

STEP="worker sanity-report на пустой БД"
rc=0
out=$("${DC[@]}" run --rm --no-deps worker worker sanity-report --source marathonec 2>&1) || rc=$?
[ "$rc" -eq 1 ]
grep -q 'нет прогонов' <<<"$out"
ok "sanity-report: код 1, нет прогонов"

STEP="порт postgres не опубликован"
out=$("${DC[@]}" port postgres 5432 2>/dev/null || true)
# Compose v5 для неопубликованного порта печатает «invalid IP:0», опубликованный оканчивается на :<порт>.
if grep -Eq ':[1-9][0-9]*$' <<<"$out"; then exit 1; fi
ok "порт 5432 не опубликован"

# Конфигурация дедупликации читается до БД и сети (05-06), поэтому бот
# с фиктивным токеном к Telegram не обращается.
STEP="DEDUP_* в bot и worker"
printf 'DEDUP_HIGH=x9q\n' >> "$T/.env"
rc=0
out=$("${DC[@]}" run --rm --no-deps worker worker sanity-report --source marathonec 2>&1) || rc=$?
[ "$rc" -eq 1 ]
grep -q 'DEDUP_HIGH' <<<"$out"
if grep -q 'x9q' <<<"$out"; then exit 1; fi
rc=0
out=$("${DC[@]}" run --rm --no-deps bot bot 2>&1) || rc=$?
[ "$rc" -eq 1 ]
grep -q 'DEDUP_HIGH' <<<"$out"
if grep -q 'x9q' <<<"$out"; then exit 1; fi
ok "DEDUP_* доходят до bot и worker (ошибка называет DEDUP_HIGH без значения)"
