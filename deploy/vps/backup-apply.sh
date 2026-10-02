#!/usr/bin/env bash
# Настройка бэкапа на VPS (Phase 6, PRD-03): МЕНЯЕТ ХОСТ. Запускает владелец от root в
# консоли провайдера однострочником из deploy/oneliner.sh (сверка SHA256 пакета), по одному
# режиму за раз, каждый со своим словом подтверждения. Через agg и под runagg недоступен.
#   backup-apply.sh dirs     — каталоги копий и секретов (700), файл лога (600); слово DIRS
#   backup-apply.sh telegram — файл секретов отправки /etc/aggregator-backup/telegram.curl
#                              (600): токен бота бэкапов и идентификатор закрытого канала
#                              вводит владелец, ввод скрыт (D-22); слово TELEGRAM
#   backup-apply.sh cron     — /etc/cron.d/aggregator-backup: запуск backup.sh в 03:00; слово CRON
# Секреты обрабатываются только встроенными командами bash и уходят в curl через stdin
# или файл: в аргументах команд, выводе и логах их нет. Внешняя копия в Telegram —
# временное хранилище (D-17), независимой второй копии нет.
set -euo pipefail
umask 077
export LC_ALL=C
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
DIR=/var/backups/aggregator
CONFDIR=/etc/aggregator-backup
LOG=/var/log/aggregator-backup.log
ENVF=/opt/aggregator/.env
CRON=/etc/cron.d/aggregator-backup
RUN=/usr/local/lib/aggregator/backup.sh
API=https://api.telegram.org
CONF=$CONFDIR/telegram.curl

MODE=${1:-}
case "$MODE" in
  dirs|telegram|cron) ;;
  *) echo "usage: backup-apply.sh dirs|telegram|cron" >&2; exit 2 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo "нужен root" >&2; exit 1; }
stop() { echo "STOP: $*" >&2; exit 1; }

confirm() {
  local ans
  printf 'Введите слово %s для продолжения: ' "$1"
  read -r ans
  ans=${ans//$'\r'/} # консоль провайдера может присылать CR вместе с Enter
  [ "$ans" = "$1" ] || { printf 'Отменено, хост не изменён (получено: %q).\n' "$ans"; exit 1; }
}

case "$MODE" in
  dirs)
    echo "Будет создано (root:root), существующее не обрезается:"
    echo "  $DIR/ (700)"
    echo "  $CONFDIR/ (700)"
    echo "  $LOG (600)"
    confirm DIRS
    install -d -o root -g root -m 700 "$DIR" "$CONFDIR"
    if [ -e "$LOG" ]; then chmod 600 "$LOG"; else install -o root -g root -m 600 /dev/null "$LOG"; fi
    ;;
  telegram)
    [ -d "$CONFDIR" ] || stop "нет каталога $CONFDIR — сначала режим dirs"
    command -v curl > /dev/null || stop "нет команды curl"
    echo "Будет записан $CONF (root:root 600) после проверки getChat."
    echo "Нужен отдельный бот бэкапов (не бот проекта), добавленный администратором в закрытый канал."
    confirm TELEGRAM
    printf 'Токен бота бэкапов (ввод скрыт): '
    IFS= read -rs TOK || true
    echo
    printf 'Идентификатор канала (ввод скрыт): '
    IFS= read -rs CHAT || true
    echo
    TOK=${TOK//$'\r'/}
    CHAT=${CHAT//$'\r'/}
    tok_re='^[0-9]+:[A-Za-z0-9_-]{30,}$'
    chat_re='^-?[0-9]+$'
    [[ $TOK =~ $tok_re ]] || stop "неверный формат токена"
    [[ $CHAT =~ $chat_re ]] || stop "неверный формат идентификатора канала"
    # Бот бэкапов должен быть отдельным (D-11): сравнение с токеном бота проекта.
    if [ -r "$ENVF" ]; then
      while IFS= read -r line || [ -n "$line" ]; do
        case "$line" in
          TELEGRAM_BOT_TOKEN=*) [ "${line#TELEGRAM_BOT_TOKEN=}" != "$TOK" ] || stop "это токен бота проекта — нужен отдельный бот бэкапов" ;;
        esac
      done < "$ENVF"
    fi
    # Проверка, что бот видит канал: токен и идентификатор уходят в curl конфигом через stdin.
    # До её успеха секреты на диск не пишутся; во временном файле ответа их нет.
    TMP=""
    ANS=$(mktemp "$CONFDIR/.getchat.XXXXXX")
    trap 'rm -f -- "$ANS"; [ -z "$TMP" ] || rm -f -- "$TMP"' EXIT
    RC=0
    HTTP=$(printf 'url = "%s/bot%s/getChat"\nform = "chat_id=%s"\n' "$API" "$TOK" "$CHAT" \
      | curl -sS --max-time 30 --connect-timeout 20 --config - -o "$ANS" -w '%{http_code}' 2> /dev/null) || RC=$?
    [[ $HTTP =~ ^[0-9]{3}$ ]] || HTTP=000
    # Недоверенный ответ: нужен только признак ok, тело не печатается.
    RES=$(python3 -I -c 'import json, sys
try:
    d = json.load(open(sys.argv[1], "rb"))
except Exception:
    d = None
print("ok" if isinstance(d, dict) and d.get("ok") is True else "bad")' "$ANS" 2> /dev/null) || RES=bad
    if [ "$RC" -ne 0 ] || [ "$HTTP" != 200 ] || [ "$RES" != ok ]; then
      stop "getChat не подтвердил доступ бота к каналу (HTTP $HTTP, curl $RC); прежний файл секретов не изменён"
    fi
    TMP=$(mktemp "$CONFDIR/.telegram.XXXXXX")
    printf 'url = "%s/bot%s/sendDocument"\nform = "chat_id=%s"\n' "$API" "$TOK" "$CHAT" > "$TMP"
    unset TOK CHAT line
    chmod 600 "$TMP"
    mv -f -- "$TMP" "$CONF"
    echo "TELEGRAM OK"
    ;;
  cron)
    [ -f "$RUN" ] || stop "нет $RUN — сначала установка пакета (INSTALL)"
    [ -d "$DIR" ] && [ -d "$CONFDIR" ] || stop "нет каталогов бэкапа — сначала режим dirs"
    [ "$(timedatectl show -p NTPSynchronized --value 2> /dev/null)" = yes ] || stop "часы не синхронизированы (NTPSynchronized не yes)"
    LINE="0 3 * * * root /bin/bash $RUN"
    echo "Будет записан $CRON (root:root 644) с единственной строкой:"
    echo "  $LINE"
    confirm CRON
    # Временное имя с точками cron в /etc/cron.d не читает.
    TMP=$(mktemp "${CRON%/*}/.aggregator-backup.XXXXXX")
    trap 'rm -f -- "$TMP"' EXIT
    printf '%s\n' "$LINE" > "$TMP"
    chmod 644 "$TMP"
    mv -f -- "$TMP" "$CRON"
    ;;
esac

echo "APPLY OK mode=$MODE"
