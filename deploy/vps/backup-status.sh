#!/bin/bash
# Состояние бэкапа (Phase 6, PRD-03): ТОЛЬКО ЧТЕНИЕ, ничего не меняет ни на диске, ни в БД.
# Вызывается как «sudo -n /usr/local/sbin/agg backup-status». Печатает семь строк состояния,
# затем причины предупреждения и итог:
#   backup_age_s=<сек|none|unknown>       возраст отметки локальной копии (считает БД)
#   backup_copy_age_s=<сек|none|unknown>  возраст отметки внешней копии; none — отметки нет,
#                                         unknown — БД недоступна
#   copies=<n>                            файлов копий в каталоге
#   latest=<имя> size=<байт> usable=yes|no  (или latest=none)
#   clock=ok|unsynced
#   cron=present|absent
#   telegram_config=present|absent        содержимое файла не читается
#   SIZE WARN bytes=<n>                   если последняя копия не меньше 40 МБ (D-20)
#   WARN <ключ>                           по строке на причину
#   STATUS OK (код 0) | STATUS WARN (код 1)
# Локальная копия внешней не считается: отметки backup и backup_copy показаны раздельно.
set -uo pipefail
export LC_ALL=C
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
L=/usr/local/lib/aggregator
DIR=/var/backups/aggregator
F=/opt/aggregator/docker-compose.yml
CRON=/etc/cron.d/aggregator-backup
CONF=/etc/aggregator-backup/telegram.curl
MAX_AGE=93600 # 26 ч: порог возраста отметок
CRON_LINE='0 3 * * * root /bin/bash /usr/local/lib/aggregator/backup.sh'

# shellcheck source=backup-lib.sh
. "$L/backup-lib.sh"

WARNS=""
warn() { WARNS="$WARNS$1"$'\n'; }
q() { docker compose -f "$F" exec -T postgres psql -U aggregator -d aggregator -Atc "$1"; }

# Отметки: один запрос, возраст в секундах считает БД.
A=unknown
C=unknown
ages_re='^([0-9]+|none) ([0-9]+|none)$'
R=$(q "SELECT coalesce((SELECT floor(extract(epoch FROM now() - beat_at))::bigint::text FROM heartbeats WHERE component = 'backup'), 'none') || ' ' || coalesce((SELECT floor(extract(epoch FROM now() - beat_at))::bigint::text FROM heartbeats WHERE component = 'backup_copy'), 'none')" 2> /dev/null) || R=""
if [[ $R =~ $ages_re ]]; then
  A=${BASH_REMATCH[1]}
  C=${BASH_REMATCH[2]}
fi
echo "backup_age_s=$A"
echo "backup_copy_age_s=$C"
age_bad() { [ "$1" = none ] || [ "$1" = unknown ] || [ "$1" -gt "$MAX_AGE" ]; }
if age_bad "$A"; then warn backup_age; fi
if age_bad "$C"; then warn backup_copy_age; fi

# Копии: обычные файлы по шаблону имени; последняя — с самой новой датой в имени.
N=0
LATEST=""
name_re='^aggregator-[0-9]{4}-[0-9]{2}-[0-9]{2}\.dump\.age$'
for f in "$DIR"/aggregator-*.dump.age; do
  [ -f "$f" ] && [ ! -L "$f" ] || continue
  [[ ${f##*/} =~ $name_re ]] || continue
  N=$((N + 1))
  if [[ ${f##*/} > $LATEST ]]; then LATEST=${f##*/}; fi
done
echo "copies=$N"
[ "$N" -gt 0 ] || warn copies
SIZE=0
if [ -n "$LATEST" ]; then
  SIZE=$(wc -c < "$DIR/$LATEST" | tr -d ' ')
  if usable "$DIR/$LATEST"; then U=yes; else U=no; warn usable; fi
  echo "latest=$LATEST size=$SIZE usable=$U"
else
  echo "latest=none"
fi

if clock_ok; then echo "clock=ok"; else echo "clock=unsynced"; warn clock; fi
if [ -f "$CRON" ] && grep -qxF -- "$CRON_LINE" "$CRON"; then echo "cron=present"; else echo "cron=absent"; warn cron; fi
# Файл секретов: только наличие и права (600), содержимое не читается.
if [ -f "$CONF" ] && [ ! -L "$CONF" ]; then
  echo "telegram_config=present"
  [ "$(ls -l "$CONF" | cut -c1-10)" = "-rw-------" ] || warn telegram_config
else
  echo "telegram_config=absent"
  warn telegram_config
fi
if [ "$SIZE" -ge "$BACKUP_WARN_BYTES" ]; then echo "SIZE WARN bytes=$SIZE"; warn size; fi

if [ -z "$WARNS" ]; then
  echo "STATUS OK"
  exit 0
fi
printf '%s' "$WARNS" | sed 's/^/WARN /'
echo "STATUS WARN"
exit 1
