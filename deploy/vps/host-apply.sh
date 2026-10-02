#!/usr/bin/env bash
# A1 APPLY хоста (D-04, D-19): МЕНЯЕТ ХОСТ. Запускает владелец от root, по одному
# режиму за раз, каждый со своим словом подтверждения:
#   host-apply.sh updates — security-обновления через unattended-upgrade (без перезагрузки)
#   host-apply.sh compose — плагин Compose из репозитория Ubuntu (пакет docker-compose-v2)
#   host-apply.sh age     — пакет age из репозитория Ubuntu (шифрование резервных копий)
# Хост скрипт не перезагружает: это делает владелец сам, когда убедится, что
# перезагрузка не заденет чужие сервисы (см. вывод host-preflight.sh).
set -euo pipefail
export LC_ALL=C

MODE=${1:-}
case "$MODE" in
  updates|compose|age) ;;
  *) echo "usage: host-apply.sh updates|compose|age" >&2; exit 2 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo "нужен root" >&2; exit 1; }

confirm() {
  local ans
  printf 'Введите слово %s для продолжения: ' "$1"
  read -r ans
  ans=${ans//$'\r'/} # консоль провайдера может присылать CR вместе с Enter
  [ "$ans" = "$1" ] || { printf 'Отменено, хост не изменён (получено: %q).\n' "$ans"; exit 1; }
}

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
    echo "Перезагрузка — только вручную владельцем: systemctl reboot"
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
esac

echo "HOST APPLY $MODE DONE"
