#!/bin/bash
# Функции бэкапа (Phase 6, PRD-03): подключаются через source из backup.sh и тестов;
# при подключении ничего не выполняется и опции оболочки не меняются. Совместимо с bash 3.2
# (Mac) и Ubuntu: без словарей, без mapfile, без GNU date и stat -c.
# Строка счётчиков (counters_sql): пары ключ=значение через пробел, есть events_listed= и
# users=, последняя пара migration=<n>; она же идёт в .meta, в подпись внешней копии и в
# сверку при восстановлении.

# Минимальный размер пригодной копии (D-21). Замер 2026-10-02 на одноразовом PostgreSQL 17.11
# (`deploy/backup-test.sh pg`, мигрированная схема, версии 1–4): pg_dump -Fc пустой базы
# E = 25984–25985 байт (колеблется на 1–2 байта), та же база с 3000 тестовыми событиями
# S = 58118–58328 байт (до 200 байт между прогонами). Правило: E, округлённое вниз до 1000. Дамп production содержит ту же схему плюс
# данные и не меньше дампа пустой схемы, а age только добавляет заголовок и метки, поэтому
# зашифрованная копия не меньше открытого дампа пустой схемы. Порог ловит пустой поток и
# ранний обрыв; обрыв после порога ловят статус конвейера и ежемесячное восстановление.
# Значение утверждает владелец перед выкатом (06-04). Порог применяется только к новой копии
# (usable_new): уже лежащие копии по нему не переоцениваются, изменение константы их не удаляет.
BACKUP_MIN_BYTES=25000
BACKUP_WARN_BYTES=40000000
BACKUP_MAX_BYTES=50000000

# SHA-256 файла (только хеш): sha256sum на Ubuntu, shasum -a 256 на Mac.
_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

# Дни от 1970-01-01 для даты по календарю (days-from-civil) -> _R. Без подоболочек.
_dfc() {
  local y=$1 m=$2 d=$3 era yoe doy doe
  if [ "$m" -le 2 ]; then y=$((y - 1)); fi
  if [ "$y" -ge 0 ]; then era=$((y / 400)); else era=$(((y - 399) / 400)); fi
  yoe=$((y - era * 400))
  if [ "$m" -gt 2 ]; then doy=$(((153 * (m - 3) + 2) / 5 + d - 1)); else doy=$(((153 * (m + 9) + 2) / 5 + d - 1)); fi
  doe=$((yoe * 365 + yoe / 4 - yoe / 100 + doy))
  _R=$((era * 146097 + doe - 719468))
}

# Проверка и разбор даты YYYY-MM-DD (формат, месяц, день с учётом високосного года) -> _Y _M _D.
_date_re='^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
_parse() {
  local dim
  [[ $1 =~ $_date_re ]] || return 1
  _Y=$((10#${1:0:4})) _M=$((10#${1:5:2})) _D=$((10#${1:8:2}))
  [ "$_M" -ge 1 ] && [ "$_M" -le 12 ] || return 1
  case $_M in
    4 | 6 | 9 | 11) dim=30 ;;
    2) if [ $((_Y % 4)) -eq 0 ] && { [ $((_Y % 100)) -ne 0 ] || [ $((_Y % 400)) -eq 0 ]; }; then dim=29; else dim=28; fi ;;
    *) dim=31 ;;
  esac
  [ "$_D" -ge 1 ] && [ "$_D" -le "$dim" ]
}

# Номер дня для даты -> _R (код 1 для неверной даты).
_days() {
  _parse "$1" || return 1
  _dfc "$_Y" "$_M" "$_D"
}

# iso_week DATE: печатает YYYY-Www (ISO 8601); неверная дата -> код 1, вывода нет.
iso_week() {
  local days wd t y jan
  _days "$1" || return 1
  days=$_R
  wd=$((((days + 3) % 7 + 7) % 7)) # понедельник = 0
  t=$((days - wd + 3))             # четверг этой недели
  y=$_Y
  _dfc $((y + 1)) 1 1
  if [ "$_R" -le "$t" ]; then
    y=$((y + 1)) jan=$_R
  else
    _dfc "$y" 1 1
    if [ "$_R" -le "$t" ]; then jan=$_R; else y=$((y - 1)); _dfc "$y" 1 1; jan=$_R; fi
  fi
  printf '%04d-W%02d\n' "$y" $(((t - jan) / 7 + 1))
}

# retention_select TODAY: stdin — строки «YYYY-MM-DD<TAB>usable|bad»; stdout — «keep DATE» и
# «delete DATE» (stdin допускает и признак unverified: такая дата не попадает ни в keep, ни в
# delete, но проверяется на будущую дату). Правила 7 + 4 (D-08, D-18): 7 самых новых пригодных + представители 4 самых
# новых ISO-недель, чей представитель (самая новая пригодная копия недели) не вошёл в
# ежедневные (A5). Непригодная удаляется, только если старше 7 дней и есть более новая
# пригодная. Коды: 4 — есть дата позже TODAY (вывод пуст); 2 — пригодных нет, TODAY неверен
# или keep не содержит самую новую пригодную (вывод пуст). Файлов, часов и сети не трогает.
retention_select() {
  local IFS=$' \t\n' line d st rest seen="" usable_in="" bad_in="" today_n n newest i
  local daily="" weekly="" keep="" out="" wk wseen="" nweeks=0
  _days "${1:-}" || return 2
  today_n=$_R
  while IFS=$'\t' read -r d st rest || [ -n "$d" ]; do
    if [ -n "$rest" ] || { [ "$st" != usable ] && [ "$st" != bad ] && [ "$st" != unverified ]; } || ! _parse "$d"; then
      echo "retention_select: строка пропущена: неверная дата или признак" >&2
      continue
    fi
    case " $seen " in *" $d "*) echo "retention_select: дата повторяется, пропущена: $d" >&2; continue ;; esac
    seen="$seen $d"
    if [[ $d > $1 ]]; then return 4; fi
    case "$st" in usable) usable_in="$usable_in $d" ;; bad) bad_in="$bad_in $d" ;; esac
  done
  [ -n "$usable_in" ] || return 2
  usable_in=$(printf '%s\n' $usable_in | sort -r | tr '\n' ' ')
  newest=${usable_in%% *}
  i=0
  for d in $usable_in; do
    i=$((i + 1))
    if [ "$i" -le 7 ]; then daily="$daily $d"; fi
  done
  # Представитель недели — первая (самая новая) пригодная копия недели в убывающем списке;
  # неделя с представителем из ежедневных пропускается, берутся первые 4 из остальных.
  for d in $usable_in; do
    wk=$(iso_week "$d")
    case " $wseen " in *" $wk "*) continue ;; esac
    wseen="$wseen $wk"
    case " $daily " in *" $d "*) continue ;; esac
    if [ "$nweeks" -lt 4 ]; then weekly="$weekly $d"; nweeks=$((nweeks + 1)); fi
  done
  keep="$daily $weekly"
  case " $keep " in *" $newest "*) ;; *) return 2 ;; esac
  for d in $usable_in; do
    case " $keep " in
      *" $d "*) out="$out"$'keep '"$d"$'\n' ;;
    esac
  done
  for d in $usable_in; do
    case " $keep " in *" $d "*) ;; *) out="$out"$'delete '"$d"$'\n' ;; esac
  done
  for d in $(printf '%s\n' $bad_in | sort -r); do
    _days "$d"
    n=$_R
    if [ $((today_n - n)) -gt 7 ] && [[ $newest > $d ]]; then out="$out"$'delete '"$d"$'\n'; fi
  done
  printf '%s' "$out"
}

# Часы синхронизированы (D-19): код 0 только при NTPSynchronized=yes.
clock_ok() {
  [ "$(timedatectl show -p NTPSynchronized --value 2>/dev/null)" = yes ]
}

# Текст одного SELECT (к БД не обращается): строка счётчиков для сверки при восстановлении.
# Известное ограничение: backup.sh снимает счётчики отдельным запросом до pg_dump, не из
# снимка дампа. Если данные изменились в секунды между ними (регистрация пользователя,
# прогон источника, разбор дублей), сверка при восстановлении даст reason=counters при
# целой копии. Расхождение само по себе копию исправной не делает: порядок разбора — в
# deploy/RUNBOOK.md, «Допустимые расхождения счётчиков».
counters_sql() {
  cat <<'SQL'
SELECT 'events_listed=' || (SELECT count(*) FROM events WHERE is_listed AND merged_into_id IS NULL)
  || ' users=' || (SELECT count(*) FROM users)
  || ' events_merged=' || (SELECT count(*) FROM events WHERE merged_into_id IS NOT NULL)
  || ' dedup_candidates=pending:' || (SELECT count(*) FROM dedup_candidates WHERE state = 'pending')
  || '|confirmed:' || (SELECT count(*) FROM dedup_candidates WHERE state = 'confirmed')
  || '|rejected:' || (SELECT count(*) FROM dedup_candidates WHERE state = 'rejected')
  || ' source_alerts=' || (SELECT count(*) FROM source_alerts)
  || ' heartbeats=' || (SELECT count(*) FROM heartbeats)
  || ' migration=' || (SELECT max(version_id) FROM goose_db_version)
SQL
}

# Формат строки счётчиков (.meta): пары ключ=значение через пробел, не длиннее 400 символов.
_cnt_re='^[a-z_]+=[0-9a-z:|]+( [a-z_]+=[0-9a-z:|]+)*$'
meta_ok() {
  local l
  [ -f "$1" ] && [ ! -L "$1" ] || return 1
  l=$(head -n 1 "$1")
  [ "${#l}" -le 400 ] && [[ $l =~ $_cnt_re ]]
}

# _is_copy FILE: обычный файл (не ссылка) с заголовком age.
_is_copy() {
  [ -f "$1" ] && [ ! -L "$1" ] && [ "$(head -c 21 "$1" | tr -d '\0')" = "age-encryption.org/v1" ]
}

# triple_ok DATA SUMFILE METAFILE: данные с заголовком age, сумма в SUMFILE (первое поле)
# совпадает с SHA-256 данных, METAFILE по формату счётчиков.
triple_ok() {
  local want
  _is_copy "$1" || return 1
  [ -f "$2" ] && [ ! -L "$2" ] && meta_ok "$3" || return 1
  want=$(head -n 1 "$2" | cut -d' ' -f1)
  [ -n "$want" ] && [ "$(_sha256 "$1")" = "$want" ]
}

# copy_state FILE: состояние уже лежащей копии — по формату, заголовку, сумме и метаданным,
# без порога размера:
#   usable     — заголовок age, верная сумма в FILE.sha256, FILE.meta по формату;
#   unverified — данные с заголовком age есть, но суммы или метаданных нет либо они неверны:
#                копия не считается ни проверенной, ни пригодной и автоматически не удаляется;
#   bad        — данных нет, это ссылка или нет заголовка age.
copy_state() {
  if ! _is_copy "$1"; then echo bad; return 0; fi
  if triple_ok "$1" "$1.sha256" "$1.meta"; then echo usable; else echo unverified; fi
}

# usable FILE: код 0 только для состояния usable (порог размера не применяется).
usable() { [ "$(copy_state "$1")" = usable ]; }

# size_ok FILE: размер не меньше BACKUP_MIN_BYTES (D-21).
size_ok() { [ "$(wc -c < "$1" | tr -d ' ')" -ge "$BACKUP_MIN_BYTES" ]; }

# usable_new FILE: проверка новой копии — usable и размер не меньше порога.
usable_new() { usable "$1" && size_ok "$1"; }

# publish OUT: публикация тройки OUT.partial, OUT.partial.sha256, OUT.partial.meta под
# итоговыми именами: сумма, метаданные, данные последним rename. После первого rename и до
# последнего под итоговыми именами лежат прежние данные с новой суммой — состояние unverified,
# а не «пригодна с чужими метаданными»; новые данные целы в OUT.partial, поэтому при
# прерывании на любом шаге следующий запуск доводит публикацию (recover в backup.sh).
publish() {
  local o=$1 p=$1.partial t
  # Итоговое имя, занятое каталогом или ссылкой, — отказ до первого rename.
  for t in "$o" "$o.sha256" "$o.meta"; do
    if [ -L "$t" ] || { [ -e "$t" ] && [ ! -f "$t" ]; }; then return 1; fi
  done
  if [ -e "$p.sha256" ]; then mv -f -- "$p.sha256" "$o.sha256" || return 1; fi
  if [ -e "$p.meta" ]; then mv -f -- "$p.meta" "$o.meta" || return 1; fi
  mv -f -- "$p" "$o"
}

# mark COMPONENT: отметка в heartbeats через psql в контейнере postgres ($F — compose-файл).
# Принимает только литералы backup и backup_copy (DECISION size_warn=log: третьей нет).
mark() {
  case "${1:-}" in backup | backup_copy) ;; *) return 2 ;; esac
  [ -n "${F:-}" ] || return 2
  docker compose -f "$F" exec -T postgres psql -U aggregator -d aggregator -v ON_ERROR_STOP=1 -Atc \
    "INSERT INTO heartbeats(component) VALUES ('$1') ON CONFLICT (component) DO UPDATE SET beat_at = now()" >/dev/null
}
