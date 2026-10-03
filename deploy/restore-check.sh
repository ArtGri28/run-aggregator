#!/usr/bin/env bash
# Проверка восстановления резервной копии на Mac владельца (Phase 6, PRD-04, D-14).
# Вызов:  deploy/restore-check.sh <файл.dump.age> <файл ключа> '<подпись>'
#   <файл ключа> — приватный ключ age; <подпись> — подпись сообщения канала бэкапов целиком:
#   «aggregator-ДАТА.dump.age sha256=<64 hex> size=<байт> counts=<строка счётчиков>»;
#   вторая строка «SIZE WARN bytes=<n>» допускается и в сверке не участвует.
# Порядок: SHA-256 и размер файла против подписи -> отдельный контейнер PostgreSQL без сети
# (образ сервиса postgres из docker-compose.yml, данные в tmpfs) -> age -d | pg_restore одним
# конвейером -> строка counters_sql восстановленной БД против counts подписи.
# Вывод: «file=<имя из подписи> sha256=ok size=<байт>», «counts=<строка>», при расхождении
# «expected=…» и «actual=…»; последняя строка — RESTORE OK counters=match migration=<n> (код 0)
# либо RESTORE FAIL reason=sha|decrypt|restore|counters (код 1) — подтверждённый отказ копии или
# восстановления. Проверку нельзя выполнить достоверно (аргументы, подпись, нет программы, Docker
# не запущен, образа нет локально, контейнер не стартовал, PostgreSQL не стал готов за 60 с; во
# время восстановления или запроса счётчиков — отказ docker exec, остановка контейнера, нехватка
# места или памяти, потеря соединения с PostgreSQL) — «STOP: <причина>» в stderr, код 2, строки
# RESTORE нет; о копии такой исход ничего не говорит. Сбой окружения признаётся только по этим
# признакам; прочие ошибки pg_restore — RESTORE FAIL reason=restore.
# При отказе расшифровки, восстановления или запроса счётчиков перед строкой RESTORE FAIL идёт
# строка «detail stage=age|pg_restore|counters exit=<код> …»; для pg_restore — ещё код age и вид
# ошибки kind=archive-version|archive-format|data|query|other (для data — имя таблицы). Сами
# сообщения age, pg_restore и psql не печатаются: PostgreSQL цитирует в них строки данных.
# Прерывание сигналом: «INTERRUPTED signal=<имя>» в stderr, код 130 (INT), 143 (TERM), 129 (HUP),
# строки RESTORE нет, контейнер удаляется.
# Не делает: не обращается к сети, VPS и существующим базам (образ не скачивается: нужен
# локальный, иначе STOP); файлов не создаёт (расшифрованный
# дамп идёт потоком); путь и содержимое ключа не печатает (stderr age скрыт); удаляет только
# свой контейнер aggregator-restore-check-<pid>.
set -uo pipefail
export LC_ALL=C
umask 077

stop() { echo "STOP: $1" >&2; exit 2; }
fail() { echo "RESTORE FAIL reason=$1"; exit 1; }

# ---- окружение --------------------------------------------------------------
[ $# -eq 3 ] || stop "нужно три аргумента: <файл.dump.age> <файл ключа> '<подпись>'"
FILE=$1
KEY=$2
HERE=$(cd "$(dirname "$0")" && pwd) || stop "каталог скрипта недоступен"
[ -f "$FILE" ] && [ -r "$FILE" ] || stop "файл копии не читается"
[ -f "$KEY" ] && [ -r "$KEY" ] || stop "файл ключа не читается"
for p in docker age; do command -v "$p" >/dev/null 2>&1 || stop "нет программы $p"; done
command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1 || stop "нет программы shasum"
[ -r "$HERE/vps/backup-lib.sh" ] || stop "нет deploy/vps/backup-lib.sh"
# shellcheck source=vps/backup-lib.sh
. "$HERE/vps/backup-lib.sh"
type counters_sql >/dev/null 2>&1 || stop "в backup-lib.sh нет counters_sql"

# Образ — тот же, что у сервиса postgres в production-составе.
IMG=$(sed -n '/^  postgres:/,/^  [a-z]/s/^ *image: *//p' "$HERE/../docker-compose.yml" 2>/dev/null | head -1)
[[ $IMG =~ ^[A-Za-z0-9._/:@-]+$ ]] || stop "образ postgres не найден в docker-compose.yml"

# Подпись: значения берутся только из первой строки; CR и завершающие пустые строки отброшены.
CAP=${3//$'\r'/}
L1=${CAP%%$'\n'*}
REST=""
[ "$L1" = "$CAP" ] || REST=${CAP#*$'\n'}
while [ "${REST%$'\n'}" != "$REST" ]; do REST=${REST%$'\n'}; done
cap_re='^(aggregator-[0-9]{4}-[0-9]{2}-[0-9]{2}\.dump\.age) sha256=([0-9a-f]{64}) size=([0-9]+) counts=(.+)$'
warn_re='^SIZE WARN bytes=[0-9]+$'
[[ $L1 =~ $cap_re ]] || stop "подпись не распознана"
NAME=${BASH_REMATCH[1]}
SUM=${BASH_REMATCH[2]}
SIZE=${BASH_REMATCH[3]}
WANT=${BASH_REMATCH[4]}
[ -z "$REST" ] || [[ $REST =~ $warn_re ]] || stop "подпись не распознана: лишняя строка"
[ "${#WANT}" -le 400 ] && [[ $WANT =~ $_cnt_re ]] || stop "подпись не распознана: счётчики"

docker info >/dev/null 2>&1 || stop "Docker не запущен"
docker image inspect "$IMG" >/dev/null 2>&1 || stop "образ $IMG не найден локально (скачивание не выполняется)"

# ---- целостность файла -------------------------------------------------------
[ "$(_sha256 "$FILE")" = "$SUM" ] && [ "$(wc -c < "$FILE" | tr -d ' ')" = "$SIZE" ] || fail sha
echo "file=$NAME sha256=ok size=$SIZE"

# ---- отдельный контейнер без сети --------------------------------------------
# Удаляется только контейнер, запущенный этим процессом: занятое имя — STOP без удаления.
C=aggregator-restore-check-$$
OWN=0
cleanup() {
  trap '' INT TERM HUP
  if [ "$OWN" = 1 ]; then docker rm -f -v "$C" >/dev/null 2>&1; fi
}
trap cleanup EXIT
trap 'echo "INTERRUPTED signal=INT" >&2; exit 130' INT
trap 'echo "INTERRUPTED signal=TERM" >&2; exit 143' TERM
trap 'echo "INTERRUPTED signal=HUP" >&2; exit 129' HUP
if docker container inspect "$C" >/dev/null 2>&1; then stop "имя контейнера $C занято"; fi
OWN=1
docker run -d --pull never --name "$C" --network none --tmpfs /var/lib/postgresql/data \
  -e POSTGRES_USER=aggregator -e POSTGRES_DB=aggregator -e POSTGRES_HOST_AUTH_METHOD=trust \
  "$IMG" >/dev/null 2>&1 || stop "контейнер проверки не запустился"
# По TCP: сервер инициализации слушает только сокет, поэтому ответ даёт уже рабочий сервер.
READY=0
for _ in $(seq 1 60); do
  if docker exec "$C" pg_isready -q -h 127.0.0.1 -U aggregator -d aggregator >/dev/null 2>&1; then READY=1; break; fi
  sleep 1
done
[ "$READY" = 1 ] || stop "PostgreSQL в контейнере не стал готов за 60 с"

# ---- расшифровка и восстановление одним конвейером ---------------------------
# Сообщения pg_restore остаются в переменной: в них бывают строки данных. Последняя строка — коды.
RAW=$({ age -d -i "$KEY" "$FILE" 2>/dev/null \
  | docker exec -i "$C" pg_restore -U aggregator -d aggregator --no-owner --exit-on-error --single-transaction 2>&1 >/dev/null
  echo "ST ${PIPESTATUS[0]} ${PIPESTATUS[1]}"; })
read -r _ AGE_RC PGR_RC <<< "${RAW##*$'\n'}"
[[ $AGE_RC =~ ^[0-9]+$ ]] && [[ $PGR_RC =~ ^[0-9]+$ ]] || stop "коды конвейера восстановления не получены"
# detail_restore: вид ошибки pg_restore по известным сообщениям, без их текста
detail_restore() {
  local kind=other tbl="" tbl_re='COPY failed for table "([a-z_][a-z0-9_]*)"'
  case "$RAW" in
    *"unsupported version"*) kind=archive-version ;;
    *"does not appear to be a valid archive"* | *"could not read from input file"* | *"input file is too short"*) kind=archive-format ;;
    *"COPY failed for table"*) kind=data; if [[ $RAW =~ $tbl_re ]]; then tbl=${BASH_REMATCH[1]}; fi ;;
    *"could not execute query"*) kind=query ;;
  esac
  echo "detail stage=pg_restore exit=$PGR_RC age_exit=$AGE_RC kind=$kind${tbl:+ table=$tbl}"
}
# running: контейнер проверки работает
running() { [ "$(docker inspect -f '{{.State.Running}}' "$C" 2>/dev/null)" = true ]; }
# Подтверждённый сбой окружения — STOP, не отказ копии: код docker exec от 125 (сбой Docker, команда
# не запущена или убита сигналом), контейнер остановился, либо сам pg_restore (строки с его
# именем; строки DETAIL и CONTEXT с данными не учитываются) сообщил о нехватке места, памяти или
# потере соединения с сервером.
if [ "$PGR_RC" != 0 ]; then
  [ "$PGR_RC" -lt 125 ] || stop "docker exec pg_restore завершился с кодом $PGR_RC (сбой Docker или контейнера)"
  running || stop "контейнер проверки остановился во время восстановления (нехватка места или памяти, сбой Docker)"
  PGL=""
  while IFS= read -r LINE; do
    case "$LINE" in "pg_restore: "*) PGL="$PGL$LINE"$'\n' ;; esac
  done <<< "$RAW"
  case "$PGL" in
    *"No space left on device"* | *"out of memory"*) stop "в контейнере проверки не хватило места или памяти" ;;
    *"server closed the connection"* | *"connection to server"*) stop "PostgreSQL в контейнере перестал отвечать во время восстановления" ;;
  esac
fi
# 141 у age при отказе pg_restore — закрытый читателем канал, а не ошибка расшифровки.
if [ "$AGE_RC" = 141 ] && [ "$PGR_RC" != 0 ]; then detail_restore; fail restore; fi
if [ "$AGE_RC" != 0 ]; then echo "detail stage=age exit=$AGE_RC"; fail decrypt; fi
if [ "$PGR_RC" != 0 ]; then detail_restore; fail restore; fi

# ---- счётчики ----------------------------------------------------------------
CNT_RC=0
GOT=$(docker exec -i "$C" psql -U aggregator -d aggregator -v ON_ERROR_STOP=1 -Atc "$(counters_sql)" 2>/dev/null) || CNT_RC=$?
if [ "$CNT_RC" != 0 ]; then
  # 2 — psql не подключился; от 125 — сбой docker exec; ошибка самого запроса — код 1
  if [ "$CNT_RC" = 2 ] || [ "$CNT_RC" -ge 125 ] || ! running; then
    stop "запрос счётчиков не выполнен: сбой Docker или контейнера (код $CNT_RC)"
  fi
  echo "detail stage=counters exit=$CNT_RC"
  fail counters
fi
echo "counts=$GOT"
mig_re=' migration=([0-9]+)$'
if [ "$GOT" != "$WANT" ] || ! [[ $GOT =~ $mig_re ]]; then
  echo "expected=$WANT"
  echo "actual=$GOT"
  fail counters
fi
echo "RESTORE OK counters=match migration=${BASH_REMATCH[1]}"
