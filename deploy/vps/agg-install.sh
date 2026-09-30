#!/usr/bin/env bash
# Разовая установка обёртки agg (и повтор при её обновлении): МЕНЯЕТ ХОСТ.
# Запускает владелец от root в консоли провайдера однострочником из deploy/oneliner.sh:
# он копирует пакет из /home/runagg/agg-deploy во временный каталог root, сверяет SHA256
# файла SHA256SUMS и всех файлов по нему и только тогда запускает этот скрипт.
# Ставит (всё root:root, runagg на запись недоступно):
#   /usr/local/sbin/agg (755)
#   /usr/local/lib/aggregator/ (755): скрипты и docker-compose.yml (644)
#   /etc/sudoers.d/runagg-aggregator (440): runagg может через sudo только agg с
#   перечисленными подкомандами
# Не меняет: sudoers Phase 1, группы runagg (в docker не добавляется), ssh, пакеты,
# Docker, /opt/aggregator. Подтверждение — словом INSTALL.
set -euo pipefail
umask 022
export LC_ALL=C PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
cd "$(dirname "$0")"

A=/usr/local/sbin/agg
L=/usr/local/lib/aggregator
S=/etc/sudoers.d/runagg-aggregator
stop() { echo "STOP: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || stop "нужен root"
# Файлы из каталога runagg могли бы смениться после проверки хеша — работаем только с копией root.
[ "$(stat -c '%u %a' .)" = "0 700" ] || stop "запускайте однострочником (временный каталог root), не из /home/runagg"
sha256sum -c --quiet SHA256SUMS || stop "SHA256 файлов пакета не совпадает с SHA256SUMS"
id runagg >/dev/null 2>&1 || stop "нет пользователя runagg"
for c in docker python3 sudo visudo; do command -v "$c" >/dev/null || stop "нет команды $c"; done
if id -nG runagg | tr ' ' '\n' | grep -qx docker; then
  stop "runagg в группе docker — это равносильно root; уберите его из группы до установки"
fi
# BASH_ENV, LD_PRELOAD и т. п. действуют на /bin/bash раньше первой строки agg — их должен
# убрать sudo: для runagg нужен env_reset без env_keep/setenv (так в Ubuntu по умолчанию).
DEF=$(sudo -l -U runagg | sed -n '/^Matching Defaults/,/^$/p')
printf '%s\n' "$DEF" | grep -qw env_reset || stop "у runagg в sudo нет env_reset"
if printf '%s\n' "$DEF" | grep -qE '!env_reset|env_keep|setenv|!env_delete'; then
  stop "sudo для runagg сохраняет окружение (env_keep/setenv/!env_reset) — agg небезопасен"
fi
# Каталоги на пути к установленным файлам не должны быть доступны на запись никому, кроме root.
for d in / /usr /usr/local /usr/local/sbin /usr/local/lib /etc /etc/sudoers.d; do
  m=$(stat -c '%a' "$d")
  [ "$(stat -c '%u' "$d")" = 0 ] && [ $((8#$m & 8#022)) -eq 0 ] || stop "$d не root или доступен на запись группе/всем"
done

# В /usr/local/lib/aggregator — всё из пакета, кроме самой обёртки и скриптов root-консоли.
LIB=$(awk '{print $2}' SHA256SUMS | grep -vxE 'agg|agg-install\.sh|host-apply\.sh')
for f in agg $LIB; do
  [[ $f =~ ^[a-z-]+(\.sh|\.yml)?$ ]] || stop "недопустимое имя файла в SHA256SUMS"
done

cat > sudoers.new <<EOF
# runagg: только обёртка развёртывания aggregator (deploy/vps/agg-install.sh).
runagg ALL=(root) NOPASSWD: $A load *, $A preflight *, $A deploy *, $A env-init, $A verify, $A rollback, $A host-preflight, $A host-verify
EOF
visudo -cqf sudoers.new || stop "sudoers.new не прошёл visudo -c"

echo "Будет установлено (root:root):"
echo "  $A (755)"
echo "  $L/ (755):" $LIB "(644)"
echo "  $S (440):"
sed 's/^/    /' sudoers.new
[ ! -e "$A" ] || echo "  (обновление: $A уже есть)"
printf 'Введите слово INSTALL: '
read -r ANS
[ "$ANS" = INSTALL ] || { echo "Отменено, хост не изменён."; exit 1; }

install -d -o root -g root -m 755 "$L"
for f in $LIB; do install -o root -g root -m 644 "$f" "$L/$f"; done
install -o root -g root -m 755 agg "$A"
install -o root -g root -m 440 sudoers.new "$S"
if ! visudo -cq; then
  rm -f "$S"
  stop "общая конфигурация sudo не прошла visudo -c; $S удалён, остальное установлено"
fi

echo
sha256sum "$A" "$L"/*
echo
sudo -l -U runagg
echo
echo "AGG INSTALL DONE"
