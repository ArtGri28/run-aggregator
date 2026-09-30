#!/usr/bin/env bash
# A1 APPLY хоста (D-04, D-19): МЕНЯЕТ ХОСТ. Запускает владелец от root, по одному
# режиму за раз, каждый со своим словом подтверждения:
#   host-apply.sh updates — security-обновления через unattended-upgrade (без перезагрузки)
#   host-apply.sh compose — плагин Compose из репозитория Ubuntu (пакет docker-compose-v2)
# Хост скрипт не перезагружает: это делает владелец сам, когда убедится, что
# перезагрузка не заденет чужие сервисы (см. вывод host-preflight.sh).
set -euo pipefail
export LC_ALL=C

MODE=${1:-}
case "$MODE" in
  updates|compose) ;;
  *) echo "usage: host-apply.sh updates|compose" >&2; exit 2 ;;
esac
[ "$(id -u)" -eq 0 ] || { echo "нужен root" >&2; exit 1; }

confirm() {
  local ans
  printf 'Введите слово %s для продолжения: ' "$1"
  read -r ans
  [ "$ans" = "$1" ] || { echo "Отменено, хост не изменён."; exit 1; }
}

export DEBIAN_FRONTEND=noninteractive

if [ "$MODE" = updates ]; then
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
else
  confirm COMPOSE
  apt-get update
  # Пакет Ubuntu universe (D-19); репозиторий Docker Inc. не подключается.
  apt-get install -y --no-install-recommends docker-compose-v2
  docker compose version
fi

echo "HOST APPLY $MODE DONE"
