#!/bin/bash
# Локальный зашифрованный бэкап PostgreSQL (Phase 6, D-07): запускается от root по cron в
# 03:00 (/bin/bash /usr/local/lib/aggregator/backup.sh) или вручную из root-консоли.
# Шаги: дамп -> age -> .partial с суммой и метаданными -> проверка -> публикация (сумма,
# метаданные, данные последними) -> повторная проверка итоговых файлов -> отметка backup -> ротация.
# Перед публикацией и перед отметкой — sync (ошибка: BACKUP FAIL stage=sync, отметки нет).
# Открытый дамп на диск не пишется. Коды: 1 — копии нет или отметка backup не записана;
# 2 — локальная копия и backup есть, backup_copy нет (шагов 6–7 здесь пока нет).
set -Eeuo pipefail
umask 077
export LC_ALL=C
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
L=/usr/local/lib/aggregator
DIR=/var/backups/aggregator
F=/opt/aggregator/docker-compose.yml
LOCK=/run/aggregator-backup.lock
LOG=/var/log/aggregator-backup.log
[ "$(id -u)" -eq 0 ] || { echo "STOP: нужен root" >&2; exit 1; }

# shellcheck source=backup-lib.sh
. "$L/backup-lib.sh"

# В stdout и в лог идут только имена, размеры, счётчики и стадии.
say() {
  printf '%s\n' "$*"
  printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >> "$LOG" 2>/dev/null || true
}
fail() { say "BACKUP FAIL stage=$1"; exit 1; }

if ! { exec 9> "$LOCK"; } 2> /dev/null || ! flock -n 9; then
  echo "STOP: бэкап уже запущен или блокировка недоступна" >&2
  exit 1
fi

# Прерванная публикация прошлого запуска. Временные данные доводятся до итоговых имён, только
# если тройка цела и проходит ту же проверку, что новая копия (сумма, метаданные, порог размера):
#   - временная сумма есть (публикация не начиналась) — нужны и временные метаданные;
#   - временной суммы нет (она уже под итоговым именем) — сумма берётся из итогового имени,
#     метаданные — из временного, а если и его уже нет, из итогового.
# Каждый запуск начинается с этого шага, поэтому целая временная тройка всегда новее итоговой
# копии своей даты. Отметка backup не ставится. Ничего не удаляется: отклонённые остатки
# убирает следующий дамп этой даты или ротация вместе со своей датой.
recover() {
  local p o sc mc re='^aggregator-([0-9]{4}-[0-9]{2}-[0-9]{2})\.dump\.age$'
  for p in "$DIR"/aggregator-*.dump.age.partial; do
    [ -f "$p" ] && [ ! -L "$p" ] || continue
    o=${p%.partial}
    [[ ${o##*/} =~ $re ]] && _parse "${BASH_REMATCH[1]}" || continue
    sc=$p.sha256 mc=$p.meta
    if [ ! -e "$sc" ] && [ ! -L "$sc" ]; then
      sc=$o.sha256
      if [ ! -e "$mc" ] && [ ! -L "$mc" ]; then mc=$o.meta; fi
    fi
    triple_ok "$p" "$sc" "$mc" || continue
    if ! size_ok "$p"; then say "RECOVER SKIP file=${o##*/} reason=size"; continue; fi
    if publish "$o" && usable_new "$o"; then say "RECOVER OK file=${o##*/}"; else say "RECOVER FAIL file=${o##*/}"; fi
  done
}
recover

# Часы (D-19): до 30 попыток с паузой 60 с; дата UTC берётся один раз — это и имя копии,
# и TODAY ротации.
n=0
until clock_ok; do
  n=$((n + 1))
  [ "$n" -lt 30 ] || fail clock
  sleep 60
done
D=$(date -u +%F)

OUT=$DIR/aggregator-$D.dump.age
P=$OUT.partial
# До начала публикации временные файлы при любом выходе удаляются; после её начала остаются —
# по ним следующий запуск доводит публикацию.
PUB=0
trap '[ "$PUB" = 1 ] || rm -f -- "$P" "$P.sha256" "$P.meta"' EXIT

# Предпосылки дампа.
[ -d "$DIR" ] && [ -w "$DIR" ] && [ -f "$F" ] || fail dump
KEY=$(head -n 1 "$L/backup-recipient" 2> /dev/null || true)
key_re='^age1[0-9a-z]{58}$'
[[ $KEY =~ $key_re ]] || fail dump
CNT=$(docker compose -f "$F" exec -T postgres psql -U aggregator -d aggregator -Atc "$(counters_sql)") || fail dump
{ [ "${#CNT}" -le 400 ] && [[ $CNT =~ $_cnt_re ]]; } || fail dump

# Шаги 1–2: дамп сразу в age, статус каждого звена конвейера учитывается (pipefail).
# Копия этого дня, если она уже есть, до публикации не затрагивается.
rm -f -- "$P" "$P.sha256" "$P.meta"
docker compose -f "$F" exec -T postgres pg_dump -U aggregator -d aggregator -Fc | age -r "$KEY" > "$P" || fail dump

# Шаг 3: сумма и метаданные во временных именах, проверка новой копии (с порогом размера).
# Порядок записи тот же, что у публикации: временные метаданные без временной суммы бывают
# только после начала публикации, и recover на это опирается.
SUM=$(_sha256 "$P") || fail verify
[ -n "$SUM" ] || fail verify
printf '%s  %s\n' "$SUM" "aggregator-$D.dump.age" > "$P.sha256" || fail verify
printf '%s\n' "$CNT" > "$P.meta" || fail verify
usable_new "$P" || fail verify
# Запись на диск до публикации (fsync трёх временных файлов): при ошибке публикация не
# начинается, временные файлы удаляются, прежняя копия и прежняя отметка не затрагиваются.
sync "$P" "$P.sha256" "$P.meta" || fail sync

# Публикация и повторная проверка итоговых файлов: те же сумма и счётчики, что у проверенной
# временной копии.
PUB=1
publish "$OUT" || fail verify
{ usable_new "$OUT" && [ "$(head -n 1 "$OUT.sha256" | cut -d' ' -f1)" = "$SUM" ] && [ "$(head -n 1 "$OUT.meta")" = "$CNT" ]; } || fail verify
# Запись каталога на диск (fsync: переименования) — до отметки: при ошибке отметки нет.
sync "$DIR" || fail sync

# Шаг 4 (D-12): отметка backup — только после проверки итоговой копии, записи на диск и до ротации.
SIZE=$(wc -c < "$OUT" | tr -d ' ')
if ! mark backup
then fail mark
fi
say "BACKUP OK file=aggregator-$D.dump.age size=$SIZE"

# Шаг 5: ротация 7 + 4. ROTATE_ABORTED=1 при любом ROTATE ABORT — вход итогового кода
# (шаги 6–7 и коды 0 и 3 добавляет следующий план). До первого удаления проверяются все имена
# удаляемых дат: каталог или иной объект, не файл и не ссылка, — ROTATE ABORT
# reason=unexpected-object, не удалено ничего. Отказ самого удаления — reason=delete: ротация
# останавливается на этой дате. Данные удаляются отдельным вызовом и первыми: если они не
# удалились, остальные пять имён даты не трогаются (копия остаётся с суммой и метаданными);
# если отказ позже — остаток даты без данных, состояние bad.
# Копия в состоянии unverified (нет верной суммы или метаданных) не удаляется никогда и в
# число пригодных не входит; их число печатается строкой ROTATE UNVERIFIED.
ROTATE_ABORTED=0
rotate_abort() { say "ROTATE ABORT reason=$1"; ROTATE_ABORTED=1; }
rotate() {
  local name d f s list="" sel rc=0 act dt nk=0 nd=0 nu=0 ud="" states=""
  local name_re='^aggregator-([0-9]{4}-[0-9]{2}-[0-9]{2})\.dump\.age(\.sha256|\.meta|\.partial|\.partial\.sha256|\.partial\.meta)?$'
  if ! clock_ok; then rotate_abort clock; return 0; fi
  for f in "$DIR"/aggregator-*; do
    if [ -d "$f" ] && [ ! -L "$f" ]; then continue; fi
    name=${f##*/}
    [[ $name =~ $name_re ]] || continue
    d=${BASH_REMATCH[1]}
    _parse "$d" || continue
    case " $list " in *" $d "*) continue ;; esac
    list="$list $d"
  done
  for d in $list; do
    act=$(copy_state "$DIR/aggregator-$d.dump.age")
    states="$states$d"$'\t'"$act"$'\n'
    if [ "$act" = unverified ]; then nu=$((nu + 1)); if [ "$nu" -le 10 ]; then ud="$ud${ud:+,}$d"; fi; fi
  done
  if [ "$nu" -gt 0 ]; then say "ROTATE UNVERIFIED count=$nu dates=$ud"; fi
  sel=$(printf '%s' "$states" | retention_select "$D") || rc=$?
  if [ "$rc" -eq 4 ]; then rotate_abort future-date; return 0; fi
  if [ "$rc" -ne 0 ] || ! printf '%s\n' "$sel" | grep -qx "keep $D"; then rotate_abort empty-keep; return 0; fi
  while read -r act dt; do
    [ "$act" = delete ] || continue
    for s in "" .sha256 .meta .partial .partial.sha256 .partial.meta; do
      f=$DIR/aggregator-$dt.dump.age$s
      if [ -e "$f" ] && [ ! -L "$f" ] && [ ! -f "$f" ]; then rotate_abort unexpected-object; return 0; fi
    done
  done <<< "$sel"
  while read -r act dt; do
    case "$act" in
      keep) nk=$((nk + 1)) ;;
      delete)
        [[ $dt =~ $_date_re ]] && _parse "$dt" && [ "$dt" != "$D" ] || continue
        if ! rm -f -- "$DIR/aggregator-$dt.dump.age" \
          || ! rm -f -- "$DIR/aggregator-$dt.dump.age.sha256" "$DIR/aggregator-$dt.dump.age.meta" \
            "$DIR/aggregator-$dt.dump.age.partial" "$DIR/aggregator-$dt.dump.age.partial.sha256" \
            "$DIR/aggregator-$dt.dump.age.partial.meta"; then
          rotate_abort delete
          return 0
        fi
        nd=$((nd + 1))
        ;;
    esac
  done <<< "$sel"
  say "ROTATE OK keep=$nk delete=$nd"
}
rotate

# Здесь добавляются шаги 6–7 (внешняя копия, отметка backup_copy) и итоговый код по ним.
exit 2
