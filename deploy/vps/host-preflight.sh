#!/usr/bin/env bash
# A0 PREFLIGHT хоста: ТОЛЬКО ЧТЕНИЕ, хост не меняется.
# Запускает владелец с Mac однострочником из deploy/oneliner.sh (от root на VPS);
# вывод сохраняется в $HOME/run-agg-phase4/ вне репозитория. Имя хоста и адреса
# не печатаются. Хост используется не только проектом: раздел «контейнеры и
# политики перезапуска» показывает, что затронет перезагрузка.
set -u
export LC_ALL=C

sec() { printf '\n== %s ==\n' "$1"; }

sec "ОС и ядро"
( . /etc/os-release && printf '%s\n' "$PRETTY_NAME" )
RUNNING=$(uname -r)
NEWEST=$(ls /boot/vmlinuz-* 2>/dev/null | sed 's#.*/vmlinuz-##' | sort -V | tail -1)
printf 'работающее ядро: %s\nновейшее установленное: %s\n' "$RUNNING" "${NEWEST:-?}"

sec "Перезагрузка и обновления"
if [ -f /var/run/reboot-required ]; then
  printf 'reboot-required: yes (пакетов в списке: %s)\n' "$(wc -l < /var/run/reboot-required.pkgs 2>/dev/null || echo 0)"
else
  echo "reboot-required: no"
fi
UPG=$(apt list --upgradable 2>/dev/null | grep -c '/')
SEC=$(apt list --upgradable 2>/dev/null | grep -c -- '-security')
printf 'доступно обновлений (по кэшу): %s, из них security: %s\n' "$UPG" "$SEC"
echo "Источники unattended-upgrades:"
apt-config dump 2>/dev/null | grep -E 'Unattended-Upgrade::(Allowed-Origins|Origins-Pattern)' || echo "(не найдены)"

sec "Пакеты Docker"
dpkg-query -W -f='${Package} ${Version}\n' docker.io docker-ce containerd docker-compose-v2 2>&1
echo "Кандидат docker-compose-v2:"
apt-cache policy docker-compose-v2 2>&1 | head -6
echo "Плагин Compose:"
docker compose version 2>&1 | head -2

sec "Контейнеры и политики перезапуска"
docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}'
for c in $(docker ps -a --format '{{.Names}}'); do
  docker container inspect --format '{{.Name}} restart={{.HostConfig.RestartPolicy.Name}}' "$c"
done

sec "Службы"
for s in docker tinyproxy; do
  printf '%s: enabled=%s active=%s\n' "$s" "$(systemctl is-enabled "$s" 2>&1)" "$(systemctl is-active "$s" 2>&1)"
done

sec "Ресурсы и время"
df -h /
free -m
timedatectl show -p NTP -p NTPSynchronized
printf 'date -u на VPS: %s (сравните с Mac)\n' "$(date -u '+%Y-%m-%d %H:%M:%S')"

sec "Проект"
ls -ld /opt/aggregator 2>&1
echo "тома проекта:"
docker volume ls --format '{{.Name}}' | grep '^aggregator_' || echo "(нет)"

echo
echo "HOST PREFLIGHT DONE"
