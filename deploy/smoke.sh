#!/usr/bin/env bash
# Локальная проверка образа и production-состава (PRD-01). Запускает Mac,
# ничего не отправляет на VPS и не обращается к Telegram и источникам:
# сеть нужна только для загрузки базовых образов Docker.
#
# Использование: deploy/smoke.sh [TAG]   (TAG по умолчанию smoke)
#   SMOKE_SKIP_BUILD=1 — не пересобирать образ aggregator:TAG.
# Mini App (Phase 15): сервис webapp поднимается с профилем webapp и временным ключом;
# caddy не запускается — проверяется только итоговая конфигурация compose.
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
    # С профилем: иначе контейнер webapp и сети этого проекта остались бы после выхода.
    "${DC[@]}" --profile webapp down -v --remove-orphans >/dev/null 2>&1 || true
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
  -u COMPOSE_PROFILES -u WEBAPP_SECRET -u WEBAPP_DOMAIN
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

# Mini App (Phase 15). caddy в смоке не запускается (нужны 80/443 и домен): его
# часть проверяется по итоговой конфигурации compose.
STEP="состав без профиля webapp"
[ "$("${DC[@]}" config --services | sort | tr '\n' ' ')" = "bot migrate postgres worker " ]
if "${DC[@]}" config | grep -q 'published:'; then exit 1; fi
ok "без профиля webapp: прежние четыре сервиса, опубликованных портов нет"

STEP="конфигурация с профилем webapp"
"${DC[@]}" --profile webapp config -q
# Итоговая конфигурация разбирается по отступам: «<сервис> <порт хоста>/<протокол>» для
# каждого опубликованного порта и «<сервис> <переменная>» для переменных сервиса webapp.
out=$("${DC[@]}" --profile webapp config | awk '
  /^[a-z]/ { top = $1 }
  top == "services:" && /^  [a-z]/ { svc = $1; sub(/:$/, "", svc) }
  top == "services:" && /^    [a-z]/ { key = $1 }
  top == "services:" && key == "ports:" && $1 == "published:" { gsub(/"/, "", $2); pub = $2 }
  top == "services:" && key == "ports:" && $1 == "protocol:" { print "port", svc, pub "/" $2 }
  top == "services:" && key == "environment:" && svc == "webapp" && /^      [A-Z]/ { sub(/:$/, "", $1); print "env", $1 }
' | sort | tr '\n' ' ')
[ "$out" = "env DATABASE_URL env LOG_FORMAT env PGPASSWORD env WEBAPP_ADDR env WEBAPP_SECRET port caddy 443/tcp port caddy 80/tcp " ]
ok "с профилем webapp: порты только у caddy (80 и 443), у webapp нет токена бота и лишних переменных"

STEP="webapp без секрета"
rc=0
out=$("${DC[@]}" --profile webapp run --rm --no-deps webapp webapp 2>&1) || rc=$?
[ "$rc" -eq 1 ]
grep -q 'WEBAPP_SECRET' <<<"$out"
ok "webapp без секрета: код 1, названа переменная"

# Ключ считается так же, как в памятке: подкомандой webapp-secret в сервисе bot (у него токен).
STEP="webapp-secret"
out=$("${DC[@]}" run --rm --no-deps -T bot webapp-secret 2>/dev/null)
[[ $out =~ ^[0-9a-f]{64}$ ]]
printf 'WEBAPP_SECRET=%s\n' "$out" >> "$T/.env"
ok "webapp-secret: 64 hex-символа"

STEP="webapp healthy"
"${DC[@]}" --profile webapp up -d webapp >/dev/null 2>&1
h=""
for _ in $(seq 60); do
  h=$("${DC[@]}" --profile webapp ps --format '{{.Health}}' webapp 2>/dev/null || true)
  [ "$h" != healthy ] || break
  sleep 2
done
[ "$h" = healthy ]
ok "webapp healthy"

# В образе приложения нет оболочки: запрос идёт из контейнера postgres по сети проекта.
# code <путь> — код ответа webapp на GET без заголовка авторизации.
code() {
  "${DC[@]}" exec -T postgres bash -c 'exec 3<>/dev/tcp/webapp/8080 && printf "GET %s HTTP/1.0\r\n\r\n" "$1" >&3 && head -n 1 <&3' _ "$1" | awk '{print $2}'
}
STEP="webapp: API без авторизации и статика"
[ "$(code /api/v1/meta)" = 401 ]
[ "$(code /)" = 200 ]
ok "webapp: /api/v1/meta без заголовка — 401, / — 200"

STEP="порт webapp не опубликован"
out=$("${DC[@]}" --profile webapp port webapp 8080 2>/dev/null || true)
if grep -Eq ':[1-9][0-9]*$' <<<"$out"; then exit 1; fi
ok "порт 8080 не опубликован"

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
