#!/usr/bin/env bash
# A1 APPLY хоста (D-04, D-19): МЕНЯЕТ ХОСТ. Запускает владелец от root, по одному
# режиму за раз, каждый со своим словом подтверждения:
#   host-apply.sh updates — security-обновления через unattended-upgrade (без перезагрузки)
#   host-apply.sh compose — плагин Compose из репозитория Ubuntu (пакет docker-compose-v2)
#   host-apply.sh age     — пакет age из репозитория Ubuntu (шифрование резервных копий)
# Режим — что меняет — откат:
#   swap      — файл подкачки SWAP_MB МБ и строка в fstab — swapoff, строка и файл удаляются
#   ufw       — входящие закрыты, кроме портов sshd; только из веб-консоли — ufw disable
#   ssh       — вход по паролю выключен для всех, root только по ключу; только из
#               веб-консоли — удаление drop-in и reload ssh
#   tinyproxy — служба остановлена и отключена — systemctl enable --now tinyproxy
#   reboot    — перезагрузка хоста после показа того, что вернётся — отката нет
#   cleanup   — копии пакета в /tmp и остатки загрузки образа — отката нет
# Перезагружает хост только режим reboot: до слова он показывает контейнеры и
# политики перезапуска, чтобы владелец видел, что заденет перезагрузка.
# Опубликованные порты Docker ufw не затрагивает.
set -euo pipefail
export LC_ALL=C

SWAPFILE=/swapfile
FSTAB=/etc/fstab
SWAP_MB=2048
SSHD_DROPIN=/etc/ssh/sshd_config.d/00-aggregator.conf
ROOT_KEYS=/root/.ssh/authorized_keys
UFW_DEFAULT=/etc/default/ufw
TMPROOT=/tmp
LOADROOT=/var/tmp
OWNER=root

MODE=${1:-}
case "$MODE" in
  updates|compose|age|swap|ufw|ssh|tinyproxy|reboot|cleanup) ;;
  *) echo "usage: host-apply.sh updates|compose|age|swap|ufw|ssh|tinyproxy|reboot|cleanup" >&2; exit 2 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo "нужен root" >&2; exit 1; }

confirm() {
  local ans
  printf 'Введите слово %s для продолжения: ' "$1"
  read -r ans
  ans=${ans//$'\r'/} # консоль провайдера может присылать CR вместе с Enter
  [ "$ans" = "$1" ] || { printf 'Отменено, хост не изменён (получено: %q).\n' "$ans"; exit 1; }
}

# console_only: ufw и ssh меняются только из веб-консоли провайдера — ошибка в
# сессии SSH лишила бы входа без возможности отката.
console_only() {
  if [ -n "${SSH_CONNECTION:-}" ] || [ -n "${SSH_TTY:-}" ] || tty 2>/dev/null | grep -q '^/dev/pts/'; then
    echo "STOP: режим $MODE выполняется только из веб-консоли провайдера, не по SSH. Хост не изменён."
    exit 1
  fi
}

# sshd_val <директива>: действующее значение из sshd -T ("" — не прочитано).
sshd_val() { sshd -T 2>/dev/null | awk -v k="$1" '$1 == k {print $2; exit}'; }

# pkg_dirs: копии пакета во временных каталогах root — каталоги TMPROOT/tmp.* с
# файлами SHA256SUMS и agg-install.sh, кроме текущего.
pkg_dirs() {
  local d here
  here=$(pwd -P)
  for d in "$TMPROOT"/tmp.*; do
    [ -d "$d" ] && [ ! -L "$d" ] || continue
    [ -f "$d/SHA256SUMS" ] && [ -f "$d/agg-install.sh" ] || continue
    [ "$(cd "$d" && pwd -P)" != "$here" ] || continue
    [ -n "$(find "$d" -maxdepth 0 -user "$OWNER" 2>/dev/null)" ] || continue
    printf '%s\n' "$d"
  done
}

# load_dirs: остатки прерванной загрузки образа — каталоги LOADROOT/agg-load.*
# владельца root старше суток.
load_dirs() { find "$LOADROOT" -maxdepth 1 -type d -name 'agg-load.*' -user "$OWNER" -mtime +0 2>/dev/null; }

export DEBIAN_FRONTEND=noninteractive

case "$MODE" in
  updates)
    echo "Разрешённые источники unattended-upgrade:"
    apt-config dump 2>/dev/null | grep -E 'Unattended-Upgrade::(Allowed-Origins|Origins-Pattern)' || echo "(не найдены)"
    confirm UPDATES
    apt-get update
    # Только разрешённые источники (по умолчанию security), D-04.
    unattended-upgrade -v
    if [ -f /var/run/reboot-required ]; then
      echo "reboot-required: yes"
    else
      echo "reboot-required: no"
    fi
    echo "Перезагрузка — отдельным режимом: host-apply.sh reboot"
    ;;
  compose)
    confirm COMPOSE
    apt-get update
    # Пакет Ubuntu universe (D-19); репозиторий Docker Inc. не подключается.
    apt-get install -y --no-install-recommends docker-compose-v2
    docker compose version
    ;;
  age)
    # Источник пакета показывается до подтверждения; репозитории не добавляются.
    echo "Источник пакета age:"
    apt-cache policy age
    confirm AGE
    apt-get update
    apt-get install -y --no-install-recommends age
    age --version
    ;;
  swap)
    if [ -n "$(swapon --show --noheadings 2>/dev/null)" ] || [ -e "$SWAPFILE" ]; then
      echo "swap уже есть, изменений нет"
      swapon --show
    else
      FREE=$(df -Pm / | awk 'NR == 2 {print $4}')
      [[ ${FREE:-} =~ ^[0-9]+$ ]] && [ "$FREE" -ge $((SWAP_MB + 2048)) ] || { echo "STOP: на / свободно ${FREE:-?} МБ, нужно не меньше $((SWAP_MB + 2048)). Хост не изменён."; exit 1; }
      echo "Будет создан $SWAPFILE на $SWAP_MB МБ и добавлена строка в $FSTAB (свободно на /: $FREE МБ)."
      confirm SWAP
      # dd, а не fallocate: годится для любой файловой системы.
      if ! { dd if=/dev/zero of="$SWAPFILE" bs=1M count="$SWAP_MB" status=none && chmod 600 "$SWAPFILE" && mkswap "$SWAPFILE" >/dev/null && swapon "$SWAPFILE"; }; then
        rm -f "$SWAPFILE"
        echo "STOP: swap не создан, файл удалён, $FSTAB не менялся."
        exit 1
      fi
      cp -p "$FSTAB" "$FSTAB.agg-bak"
      grep -q "^$SWAPFILE[[:space:]]" "$FSTAB" || printf '%s none swap sw 0 0\n' "$SWAPFILE" >> "$FSTAB"
      swapon --show
      echo "Откат: swapoff $SWAPFILE && grep -v '^$SWAPFILE[[:space:]]' $FSTAB > $FSTAB.agg-new; cat $FSTAB.agg-new > $FSTAB && rm -f $FSTAB.agg-new $SWAPFILE"
    fi
    ;;
  ufw)
    console_only
    command -v ufw >/dev/null 2>&1 || { echo "STOP: ufw не установлен. Хост не изменён."; exit 1; }
    PORTS=$(sshd -T 2>/dev/null | awk '$1 == "port" && $2 ~ /^[0-9]+$/ {print $2}' | sort -un | tr '\n' ' ')
    [ -n "${PORTS// /}" ] || { echo "STOP: порты sshd не определены (sshd -T). Хост не изменён."; exit 1; }
    echo "Слушающие порты сейчас (протокол/порт, без адресов):"
    ss -H -tuln 2>/dev/null | awk '{n = split($5, a, ":"); print "  " $1 "/" a[n]}' | sort -u
    echo "Будут правила: входящие — запретить, исходящие — разрешить, разрешить входящие tcp на порты sshd: $PORTS"
    echo "Опубликованные порты Docker ufw не затрагивает. Остальные входящие (в том числе 8888) закрываются."
    grep '^IPV6=' "$UFW_DEFAULT" 2>/dev/null || echo "IPV6= не задано в $UFW_DEFAULT"
    echo "После включения, не закрывая консоль: проверьте новый вход по SSH и VPN. Не работает — ufw disable."
    confirm UFW
    ufw default deny incoming
    ufw default allow outgoing
    for p in $PORTS; do ufw allow "$p/tcp"; done
    ufw --force enable
    ufw status verbose
    echo "Откат: ufw disable"
    ;;
  ssh)
    console_only
    echo "Действующие значения sshd:"
    for k in permitrootlogin passwordauthentication kbdinteractiveauthentication; do printf '  %s %s\n' "$k" "$(sshd_val "$k")"; done
    KEYS=$(grep -cvE '^[[:space:]]*(#|$)' "$ROOT_KEYS" 2>/dev/null || true)
    [[ ${KEYS:-0} =~ ^[0-9]+$ ]] && [ "${KEYS:-0}" -ge 1 ] || { echo "STOP: у root нет ни одного ключа в $ROOT_KEYS — сначала добавьте ключ и проверьте вход по нему. Хост не изменён."; exit 1; }
    echo "Ключей у root: $KEYS."
    echo "Будет записан $SSHD_DROPIN: вход по паролю выключается для ВСЕХ учётных записей, root входит только по ключу."
    echo "До слова убедитесь: вход root по ключу уже проверен в отдельной сессии. Веб-консоль провайдера остаётся запасным входом."
    confirm SSH
    printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' > "$SSHD_DROPIN.agg-new"
    chmod 644 "$SSHD_DROPIN.agg-new"
    mv "$SSHD_DROPIN.agg-new" "$SSHD_DROPIN"
    if ! sshd -t; then
      rm -f "$SSHD_DROPIN"
      echo "STOP: sshd -t не принял настройки, drop-in удалён, sshd не перезагружался."
      exit 1
    fi
    systemctl reload ssh || systemctl restart ssh
    echo "Действующие значения sshd после изменения:"
    BAD=""
    for k in passwordauthentication kbdinteractiveauthentication; do
      v=$(sshd_val "$k"); printf '  %s %s\n' "$k" "$v"
      [ "$v" = no ] || BAD="$BAD $k"
    done
    v=$(sshd_val permitrootlogin); printf '  permitrootlogin %s\n' "$v"
    # sshd -T печатает prohibit-password как without-password.
    [ "$v" = prohibit-password ] || [ "$v" = without-password ] || BAD="$BAD permitrootlogin"
    echo "Откат: rm -f $SSHD_DROPIN && sshd -t && systemctl reload ssh"
    [ -z "$BAD" ] || { echo "STOP: значения не те, что ожидались:$BAD — другой файл настроек задаёт их раньше. Выполните откат или разберитесь, не закрывая консоль."; exit 1; }
    echo "Не закрывая консоль: откройте новую сессию SSH по ключу и убедитесь, что вход по паролю отклонён."
    ;;
  tinyproxy)
    printf 'tinyproxy сейчас: enabled=%s active=%s\n' "$(systemctl is-enabled tinyproxy 2>&1)" "$(systemctl is-active tinyproxy 2>&1)"
    echo "Служба будет остановлена и отключена; пакет и конфигурация остаются."
    confirm TINYPROXY
    systemctl disable --now tinyproxy
    printf 'tinyproxy: enabled=%s active=%s\n' "$(systemctl is-enabled tinyproxy 2>&1)" "$(systemctl is-active tinyproxy 2>&1)"
    echo "Откат: systemctl enable --now tinyproxy"
    ;;
  reboot)
    echo "Контейнеры и политики перезапуска:"
    docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}'
    for c in $(docker ps -a --format '{{.Names}}'); do
      pol=$(docker container inspect --format '{{.HostConfig.RestartPolicy.Name}}' "$c" 2>/dev/null) || pol="?"
      printf '%s restart=%s\n' "$c" "$pol"
      case "$c" in
        aggregator-migrate-*) ;;
        aggregator-*) [ "$pol" = unless-stopped ] || echo "ПРЕДУПРЕЖДЕНИЕ: $c не вернётся сам (restart=$pol)" ;;
        *) case "$pol" in always|unless-stopped) ;; *) echo "ПРЕДУПРЕЖДЕНИЕ: чужой контейнер $c не вернётся сам (restart=$pol)" ;; esac ;;
      esac
    done
    if [ -f /var/run/reboot-required ]; then echo "reboot-required: yes"; else echo "reboot-required: no"; fi
    DOCKER_EN=$(systemctl is-enabled docker 2>&1)
    printf 'docker: enabled=%s\ncron: enabled=%s\n' "$DOCKER_EN" "$(systemctl is-enabled cron 2>&1)"
    [ "$DOCKER_EN" = enabled ] || { echo "STOP: docker не включён в автозапуск — после перезагрузки сервисы не вернутся. Хост не изменён."; exit 1; }
    if [ -n "$(swapon --show --noheadings 2>/dev/null)" ] && ! grep -qE '^[^#].*[[:space:]]swap[[:space:]]' "$FSTAB"; then
      echo "STOP: swap включён, но строки в $FSTAB нет — после перезагрузки он пропадёт. Хост не изменён."
      exit 1
    fi
    echo "Хост будет перезагружен: все сервисы хоста, включая чужие, недоступны несколько минут."
    confirm REBOOT
    systemctl reboot
    ;;
  cleanup)
    PKG=$(pkg_dirs)
    LOAD=$(load_dirs) || true
    echo "Копии пакета в $TMPROOT: $(printf '%s\n' "$PKG" | grep -c .)"
    echo "Остатки загрузки образа в $LOADROOT старше суток: $(printf '%s\n' "$LOAD" | grep -c .)"
    [ -z "$PKG$LOAD" ] || du -sch $PKG $LOAD 2>/dev/null | tail -1
    confirm CLEANUP
    for item in "tmp:$TMPROOT/tmp.:$PKG" "load:$LOADROOT/agg-load.:$LOAD"; do
      name=${item%%:*} rest=${item#*:}
      prefix=${rest%%:*} list=${rest#*:}
      n=0
      for d in $list; do
        # Удаляется только каталог с ожидаемым началом пути.
        case "$d" in "$prefix"*) rm -rf -- "$d" && n=$((n + 1)) ;; esac
      done
      if [ "$n" -gt 0 ]; then echo "CLEANUP $name: removed $n"; else echo "CLEANUP $name: absent"; fi
    done
    ;;
esac

echo "HOST APPLY $MODE DONE"
