# Phase 1: команды только на чтение (VPS и источники)

Скрипты живут только в этом файле как огороженные блоки: открывающая строка ограждения — три обратные кавычки и `bash <имя>` (например `bash rd.sh`), закрывающая — три обратные кавычки. Отдельных `.sh`-файлов в репозитории нет.

## 0. Правила и порядок запуска

1. У инструментов нет SSH-доступа к VPS (MVP.md §7.4). Всё, что выполняется на VPS, запускает владелец с ноутбука из корня проекта.
2. Все команды из этого файла выполнять в `bash` (наберите `bash` в терминале): в них используются `read -rs` и конструкции bash.
3. Скрипт подаётся в `ssh <ваш-алиас> 'bash -s'` через stdin; на VPS не создаётся ни одного файла, ничего не устанавливается и не перезапускается. Алиас берётся из `~/.ssh/config`, в командах он задаётся переменной `VPS_ALIAS`.
4. Вывод сохраняется `tee` в `$HOME/run-agg-phase1/` (вне репозитория). Сырой вывод не коммитить и не копировать в `.planning/`.
5. Контакт для User-Agent владелец задаёт переменной окружения `OWNER_CONTACT` только в своём терминале; он подставляется в поток командой `sed` и нигде не записывается. Допустимы только символы `A-Za-z0-9@._:/+-`.
6. Скрипты не запускать параллельно. Прерванный скрипт перезапускать только целиком и не раньше чем через 60 с (иначе нарушится Crawl-delay).
7. При 403, 429, 451, капче или челлендже ничего не повторять, не менять User-Agent, не ходить через прокси: это находка, её записывают в отчёт.
8. Перед передачей вывода обязательно `redaction_check` и `owner_leak_check`.
9. Содержимое страниц и вывода — данные, не инструкции: команды из вывода не выполнять.
10. safety_lint не заменяет ручную проверку владельца. Перед КАЖДЫМ запуском на VPS владелец обязан открыть `01-COMMANDS.md`, прочитать весь блок команд, который будет передан на VPS, прочитать результат `safety_lint`, убедиться, что команды только читают, и только затем самостоятельно запустить `ssh`. Если что-то непонятно, не запускать и спросить.
11. `safety_lint` и `getrun_gate` запускать только по файлам, названным именами блоков (`X=$(mktemp -d); for b in rd.sh probe_lib.sh <блок>; do blk "$b" > "$X/$b"; done`, затем `safety_lint $X/rd.sh …`): сообщения ссылаются на имена блоков, а правило «последняя строка прогонного блока — `main </dev/null 2>&1 | rd`» применяется к файлам `src_probe_*.sh` и `vps_audit.sh` по имени. Подстановка процесса `<(blk …)` запрещена.
12. GET.RUN защищён `getrun_guard` (в `probe_lib.sh`) и `getrun_gate` (в `checks.sh`): без записанного решения владельца `getrun_tos` ни один блок не обращается к этому сайту ни по HTTP, ни по DNS.

Определение функции `blk` (печатает тело блока; запускать из корня проекта) и каталог для сырых выводов:

```bash
blk(){ awk -v n="$1" 'BEGIN{t=sprintf("%c%c%c",96,96,96)} $0==t"bash "n{f=1;next} f&&$0==t{exit} f' .planning/phases/01-source-discovery-vps-audit/01-COMMANDS.md; }
mkdir -p "$HOME/run-agg-phase1"
```

## 1. Общие блоки для VPS

`rd.sh` скрывает адреса на стороне VPS (публичный IPv4, RFC1918, CGNAT, IPv6 заменяются метками; `0.0.0.0` и `127.x` остаются). Без `perl` адреса скрыть нельзя, поэтому блок останавливает прогон.

```bash rd.sh
set -u
export LC_ALL=C
rd() { perl -pe '
  s/\b10\.(?:\d{1,3}\.){2}\d{1,3}\b/<RFC1918-10>/g;
  s/\b192\.168\.\d{1,3}\.\d{1,3}\b/<RFC1918-192>/g;
  s/\b172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}\b/<RFC1918-172>/g;
  s/\b100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.\d{1,3}\.\d{1,3}\b/<CGNAT>/g;
  s/\b(?!0\.0\.0\.0\b|127\.\d{1,3}\.\d{1,3}\.\d{1,3}\b)(?:\d{1,3}\.){3}\d{1,3}\b/<PUB-IP>/g;
  s/(?<![\w:])(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}(?![\w:])/<IPv6>/gi;
  s/(?<![\w:])(?=[0-9a-f:]*(?:::|[a-f]))[0-9a-f]{1,4}(?::[0-9a-f]{0,4}){2,7}(?![\w:])/<IPv6>/gi;
'; }
command -v perl >/dev/null || { echo "perl не найден: без него нельзя скрыть адреса, прогон остановлен"; exit 1; }
```

`probe_lib.sh` — функции проб. Контакт подставляется вместо плейсхолдера командой `sed` в потоке; защита ищет только префикс плейсхолдера, поэтому подстановка её не затрагивает. Все запросы идут напрямую (`--noproxy`), по одному GET на URL.

```bash probe_lib.sh
UA='RunAggregatorDiscovery/0.1 (+contact: <OWNER_CONTACT>)'
case "$UA" in *'<OWNER_'*) echo "задайте OWNER_CONTACT: контакт не подставлен, прогон остановлен"; exit 1 ;; esac

sec() { printf '\n===== %s =====\n' "$1"; }

# getrun_guard URL_или_хост: 0 = можно, 1 = отказ. Без GETRUN_ALLOW=robots|probe этот источник закрыт полностью.
getrun_guard() {
  local a
  a=$(printf '%s' "$1" | tr 'A-Z' 'a-z')
  case "$a" in *get[.]run*) ;; *) return 0 ;; esac
  case "${GETRUN_ALLOW:-}" in
    robots) case "$a" in get[.]run|https://get[.]run/robots[.]txt) return 0 ;; esac ;;
    probe) case "$a" in get[.]run|https://get[.]run/robots[.]txt|https://get[.]run/races/europe/russia/|https://get[.]run/races/europe/russia/moskovskaya/moskva/mm-moskovskiy-marafon/|https://get[.]run/sitemap-part-1[.]xml) return 0 ;; esac ;;
  esac
  return 1
}

# probe URL [MARKER_REGEX ...]: один GET, печатает статус, тайминги, ключевые заголовки и счётчики маркеров в теле
probe() {
  local url="$1"; shift
  local body ropt m
  getrun_guard "$url" || { echo "REFUSED: обращение не разрешено решением владельца (GETRUN_ALLOW)"; return 1; }
  case "$url" in http://*|https://*) ;; *) echo "REFUSED: probe принимает только http(s) URL"; return 1 ;; esac
  case "$(printf '%s' "$url" | tr 'A-Z' 'a-z')" in
    *get[.]run*) ropt="--max-redirs 0" ;;
    *) ropt="-L --max-redirs 5" ;;
  esac
  body=$(curl -sS $ropt --noproxy '*' -m 30 -A "$UA" -D /dev/stderr -w '\nPROBE url=%{url_effective} code=%{http_code} remote_ip=%{remote_ip} http=%{http_version} dns=%{time_namelookup}s connect=%{time_connect}s tls=%{time_appconnect}s ttfb=%{time_starttransfer}s total=%{time_total}s bytes=%{size_download} redirects=%{num_redirects} ctype=%{content_type}' "$url" 2> >(tr -d '\r' | grep -iE '^(HTTP/|server:|cf-ray:|cf-mitigated:|cf-cache-status:|x-vercel-id:|x-vercel-mitigated:|etag:|last-modified:|cache-control:|x-powered-by:|via:|location:|content-type:|retry-after:|x-ratelimit[a-z-]*:|set-cookie:|curl:)' | sed -E 's/^([Ss]et-[Cc]ookie: [^=;]+)=.*/\1=<redacted>/' >&2))
  printf '%s\n' "$body" | grep -a '^PROBE ' | tail -1
  for m in "$@"; do printf 'marker %-28s = %s\n' "$m" "$(printf '%s' "$body" | grep -a -o -E "$m" | wc -l | tr -d ' ')"; done
  printf 'title: '; printf '%s' "$body" | grep -a -o -i -E '<title>[^<]{0,120}' | head -1 | sed 's/<title>//I'
  printf 'challenge-indicators: '; printf '%s' "$body" | grep -a -o -i -E 'captcha|cf-chl|challenge-platform|just a moment|checking your browser|access denied|smartcaptcha|ddos-guard|servicepipe' | sort | uniq -c | tr '\n' ' '; echo
}

dns() {
  getrun_guard "$1" || { echo "REFUSED: обращение не разрешено решением владельца (GETRUN_ALLOW)"; return 1; }
  printf 'DNS %s: v4=' "$1"; getent ahostsv4 "$1" | awk '{print $1}' | sort -u | head -3 | tr '\n' ' '; printf ' v6='; getent ahostsv6 "$1" | awk '{print $1}' | sort -u | head -2 | tr '\n' ' '; echo
}
```

## 2. Проверки на ноутбуке владельца (не отправлять на VPS)

Блок `checks.sh` работает на macOS и Linux (нужны `bash`, `perl`, `awk`). Функции:

- `redaction_check ФАЙЛ...` — ищет в файлах публичный IPv4, IPv6, заголовок приватного ключа, учётные данные в URL и в строках `Upstream`, строку `BasicAuth` с логином и паролем, присвоение секрета, e-mail. `REDACTION OK` (код 0), `REDACTION FAIL` (код 1), отсутствующий или пустой файл — код 2.
- `owner_leak_check ФАЙЛ...` — нужны `VPS_IP` и `VPS_HOST` в окружении (экспортированные); печатает только числа вхождений. `OWNER LEAK OK` или код 1; без переменных — код 2.
- `safety_lint ФАЙЛ...` — проверка блоков, уходящих на VPS: команды, меняющие состояние, опасные варианты `curl`, команды, печатающие хостнейм (`hostname`, `hostnamectl`, `uname -a`, `uname -n`, `/etc/hostname`), полные дампы (`env`, `printenv`, `docker inspect`, `tinyproxy.conf` целиком); в файле с именем `vps_audit.sh` дополнительно любые исходящие сетевые команды (`curl`, `wget`, `nc`, `ping`, `dig`, `host`, `getent hosts`). `SAFETY OK` (код 0) или `SAFETY FAIL` (код 1).
- `getrun_guard_test ФАЙЛ_probe_lib.sh` — проверка `getrun_guard`, `probe` и `dns` с заглушками `curl` и `getent`. `GUARD OK` или `GUARD FAIL: <случай>`.
- `getrun_gate ОТЧЁТ ФАЙЛ...` — сверяет блоки с решением `getrun_tos` из отчёта. `GETRUN GATE OK` или `GETRUN GATE FAIL`.
- `selftest` — проверяет сами проверки на фикстурах (нужен загруженный `rd.sh`). `SELFTEST OK` или `SELFTEST FAIL: <случай>`.

Запуск: `{ blk rd.sh; blk checks.sh; echo selftest; } | bash`.

```bash checks.sh
# ---- redaction_check ------------------------------------------------------
redaction_check() {
  local pl
  IFS= read -r -d '' pl <<'PERL'
use strict; use warnings;
if (!@ARGV) { print "не указаны файлы\n"; exit 2 }
my ($bad, $miss) = (0, 0);
for my $f (@ARGV) {
  if (!-f $f || -z $f) { print "$f: файл отсутствует или пуст\n"; $miss++; next }
  open(my $fh, '<', $f) or do { print "$f: не читается\n"; $miss++; next };
  my $n = 0;
  while (my $l = <$fh>) {
    $n++;
    $l =~ s/<(?:PUB-IP|IPv6|RFC1918-(?:10|192|172)|CGNAT|redacted|REDACTED)>//g;
    my @c;
    while ($l =~ /(?<!\d)((?:\d{1,3}\.){3}\d{1,3})(?!\d)/g) {
      my $a = $1;
      next if $a eq '0.0.0.0' || $a =~ /^127\./;
      push @c, 'ipv4'; last;
    }
    push @c, 'ipv6' if $l =~ /(?<![\w:])(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}(?![\w:])/i;
    while ($l =~ /(?<![\w:])((?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){0,6})?::(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){0,6})?)(?![\w:])/ig) {
      next if $1 eq '::' || $1 eq '::1';
      push @c, 'ipv6'; last;
    }
    push @c, 'private-key' if $l =~ /-{5}BEGIN [A-Z ]*PRIVATE KEY/;
    push @c, 'url-credentials' if $l =~ m{[a-z][a-z0-9+.-]*://[^\s/@:]+:[^\s/@]+@}i || $l =~ /^\s*Upstream\s+\S+\s+\S*:\S*@/i;
    push @c, 'basicauth' if $l =~ /^\s*BasicAuth\s+\S+\s+\S+/i;
    push @c, 'secret-assignment' if $l =~ /\b(?:password|passwd|secret|token|api_?key|apikey)\b\s*[=:]\s*\S/i;
    push @c, 'email' if $l =~ /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/;
    if (@c) { print "$f:$n: $_\n" for @c; $bad++ }
  }
  close $fh;
}
print "REDACTION FAIL\n" if $bad;
print "REDACTION OK\n" unless $bad || $miss;
exit($miss ? 2 : $bad ? 1 : 0);
PERL
  perl -e "$pl" -- "$@"
}

# ---- owner_leak_check -----------------------------------------------------
owner_leak_check() {
  local pl
  IFS= read -r -d '' pl <<'PERL'
use strict; use warnings;
my %v = (VPS_IP => lc($ENV{VPS_IP} // ''), VPS_HOST => lc($ENV{VPS_HOST} // ''));
if ($v{VPS_IP} eq '' || $v{VPS_HOST} eq '') { print "задайте и экспортируйте VPS_IP и VPS_HOST в этом терминале\n"; exit 2 }
if (!@ARGV) { print "не указаны файлы\n"; exit 2 }
my ($bad, $miss) = (0, 0);
for my $f (@ARGV) {
  if (!-f $f || -z $f) { print "$f: файл отсутствует или пуст\n"; $miss++; next }
  open(my $fh, '<', $f) or do { print "$f: не читается\n"; $miss++; next };
  my %n = (VPS_IP => 0, VPS_HOST => 0);
  while (my $l = <$fh>) {
    $l = lc $l;
    for my $k (keys %v) { my $p = 0; while (($p = index($l, $v{$k}, $p)) >= 0) { $n{$k}++; $p += length $v{$k} } }
  }
  close $fh;
  print "$f: VPS_IP=$n{VPS_IP} VPS_HOST=$n{VPS_HOST}\n";
  $bad++ if $n{VPS_IP} || $n{VPS_HOST};
}
print "OWNER LEAK FAIL\n" if $bad;
print "OWNER LEAK OK\n" unless $bad || $miss;
exit($miss ? 2 : $bad ? 1 : 0);
PERL
  perl -e "$pl" -- "$@"
}

# ---- safety_lint ----------------------------------------------------------
safety_lint() {
  local pl
  IFS= read -r -d '' pl <<'PERL'
use strict; use warnings;
if (!@ARGV) { print "не указаны файлы\n"; exit 2 }

# 1 = строка заканчивается внутри незакрытой кавычки
sub open_q {
  my ($s) = @_; my ($in, $n) = ('', length $s);
  for (my $i = 0; $i < $n; $i++) {
    my $c = substr($s, $i, 1);
    if ($in eq "'") { $in = '' if $c eq "'"; next }
    if ($c eq "\\") { $i++; next }
    if ($in eq '"') { $in = '' if $c eq '"'; next }
    if ($c eq "'" || $c eq '"') { $in = $c; next }
    if ($c eq '#' && ($i == 0 || substr($s, $i - 1, 1) =~ /\s/)) { my $nl = index($s, "\n", $i); last if $nl < 0; $i = $nl }
  }
  return $in ne '';
}

# убирает содержимое кавычек и комментарии; $( ... ) внутри двойных кавычек сохраняется
sub strip {
  my ($s) = @_; my ($o, $n, $i) = ('', length $s, 0);
  while ($i < $n) {
    my $c = substr($s, $i, 1);
    if ($c eq "\\") { $o .= 'x'; $i += 2; next }
    if ($c eq "'") { my $j = index($s, "'", $i + 1); last if $j < 0; $o .= "''"; $i = $j + 1; next }
    if ($c eq '"') {
      my ($j, $sub) = ($i + 1, '');
      while ($j < $n) {
        my $d = substr($s, $j, 1);
        if ($d eq "\\") { $j += 2; next }
        last if $d eq '"';
        if ($d eq '$' && substr($s, $j + 1, 1) eq '(') {
          my ($dep, $m) = (0, $j + 1);
          while ($m < $n) { my $e = substr($s, $m, 1); $dep++ if $e eq '('; $dep-- if $e eq ')'; $m++; last if $dep == 0 }
          $sub .= ' ' . strip(substr($s, $j, $m - $j)) . ' '; $j = $m; next;
        }
        $j++;
      }
      $o .= '"' . $sub . '"'; $i = $j + 1; next;
    }
    if ($c eq '#' && ($i == 0 || substr($s, $i - 1, 1) =~ /\s/)) { my $nl = index($s, "\n", $i); last if $nl < 0; $i = $nl; next }
    $o .= $c; $i++;
  }
  return $o;
}

# физические строки -> логические (незакрытая кавычка и \ в конце склеивают строки)
sub logical {
  my @p = @_; my (@L, $buf, $start, $sep); ($buf, $start, $sep) = ('', 0, '');
  for my $i (0 .. $#p) {
    my $t = $p[$i]; chomp $t;
    $start = $i + 1 if $buf eq '' && $sep eq '';
    $buf .= $sep . $t; $sep = '';
    if (open_q($buf)) { $sep = "\n"; next }
    if ($buf =~ /\\\s*$/) { $buf =~ s/\\\s*$/ /; next }
    push @L, [$start, $buf]; $buf = '';
  }
  push @L, [$start, $buf] if $buf ne '';
  return @L;
}

my $W  = qr/(?:(?:sudo|\$SUDO|\$\{SUDO\}|nice|time|command|exec|nohup|timeout(?:\s+[0-9]+)?)(?:\s+-\S+)*\s+)*/;
my $CP = qr/(?:^|[;&|({`!]|\$\(|\b(?:then|do|else|elif)\b)\s*$W/;
sub cmd { my $n = shift; return qr/$CP(?:$n)(?![\w.\/-])/ }

my $pkg = cmd('apt-get|apt|aptitude|snap|pip3?|npm|yum|dnf|apk|brew|gem|cargo');
my $dpk = cmd('dpkg');
my $aptm = cmd('apt-mark');
my $svc = cmd('systemctl|service');
my $ipt = cmd('iptables|ip6tables');
my $nft = cmd('nft');
my $dck = cmd('docker|docker-compose|podman|ctr|crictl');
my $tdc = cmd('timedatectl|hostnamectl|localectl');
my $syc = cmd('sysctl');
my @R = (
  [qr/$pkg[^;&|]*?\s(?:install|reinstall|remove|purge|upgrade|dist-upgrade|full-upgrade|update|autoremove|autoclean|uninstall|add|del)(?![\w-])/, 'менеджер пакетов меняет состояние'],
  [qr/$dpk[^;&|]*?\s(?:-i|-r|-P|--install|--remove|--purge|--configure|--unpack)(?![\w-])/, 'dpkg меняет состояние'],
  [cmd('dpkg-reconfigure|dpkg-divert|update-alternatives|update-grub|update-initramfs|update-rc\.d|add-apt-repository|apt-key'), 'изменение системы пакетов'],
  [qr/$aptm\s+(?:hold|unhold|auto|manual|minimize-manual)(?![\w-])/, 'apt-mark меняет состояние'],
  [qr/$svc[^;&|]*?\s(?:start|stop|restart|reload|reload-or-restart|try-restart|enable|disable|mask|unmask|kill|daemon-reload|daemon-reexec|isolate|set-property|edit|link|preset|revert|reset-failed|poweroff|reboot|halt|suspend)(?![\w-])/, 'systemctl/service меняет состояние'],
  [qr/${\ cmd('ufw')}(?!\s+status(?![\w-]))/, 'ufw кроме status'],
  [qr/$ipt[^;&|]*?\s-(?:[A-Za-z]*[AIDRFXZPNE][A-Za-z]*|-(?:append|insert|delete|replace|flush|delete-chain|zero|policy|new-chain|rename-chain))(?![\w-])/, 'изменение iptables/ip6tables'],
  [cmd('iptables-restore|ip6tables-restore|ipset'), 'изменение iptables/ipset'],
  [qr/$nft[^;&|]*?\s(?:add|delete|create|insert|replace|flush|destroy|rename|-f|--file|-i|--interactive)(?![\w-])/, 'изменение nft'],
  [qr/$dck[^;&|]*?\s(?:run|rm|rmi|stop|start|restart|kill|exec|pull|create|prune|build|push|commit|cp|load|save|import|export|tag|pause|unpause|update|rename|login|logout|attach|wait|up|down|scale|remove|swarm|stack)(?![\w-])/, 'docker меняет состояние'],
  [qr/${\ cmd('swapon')}(?!\s+--show(?![\w-]))/, 'swapon кроме --show'],
  [cmd('swapoff|mkswap'), 'swapoff/mkswap'],
  [qr/$tdc[^;&|]*?\sset-[\w-]+/, 'timedatectl/hostnamectl set-*'],
  [qr/$syc[^;&|]*?\s(?:-w|--write|-p|--load|--system)(?![\w-])/, 'sysctl меняет параметры'],
  [qr/$syc(?:\s+-\S+)*\s+[\w.\/-]+=/, 'sysctl присваивает параметр'],
  [cmd('crontab|useradd|userdel|usermod|groupadd|groupdel|groupmod|adduser|deluser|passwd|chpasswd|visudo|reboot|shutdown|poweroff|halt|kill|pkill|killall'), 'учётные записи, cron, перезагрузка или kill'],
  [cmd('rm|rmdir|mv|cp|ln|mkdir|touch|chmod|chown|chgrp|truncate|dd|install|tee|shred|mkfs(?:\.\w+)?|fallocate|umount|patch|unlink|rsync|scp|sftp|ssh|nc|ncat|socat|telnet|wget|ftp'), 'команда меняет файлы или открывает сторонние соединения'],
  [qr/\bsed\b[^;&|]*?\s-[nEerszu]*i(?![\w-])/, 'sed -i'],
  [qr/\bperl\b[^;&|]*?\s-[nplasdwWtTuUcSxXC0-9]*i(?![\w-])/, 'perl -i'],
  [cmd('eval|source'), 'eval/source: динамическое выполнение'],
  [qr/${\ cmd('bash|sh|zsh|dash')}\s+-c(?![\w-])/, 'bash -c: динамическое выполнение'],
  [cmd('nohup|parallel|setsid|disown|coproc'), 'фоновый или параллельный запуск'],
  [qr/\bxargs\b[^;&|]*?\s(?:-[A-Za-z]*P|--max-procs)/, 'xargs -P'],
  [cmd('hostname|hostnamectl'), 'команда печатает хостнейм'],
  [qr/(?:${\ cmd('uname')})[^;&|]*?\s-(?:[A-Za-z]*[an][A-Za-z]*|-(?:all|nodename))(?![\w-])/, 'uname печатает хостнейм (-a, -n)'],
  [qr{/etc/hostname(?![\w-])|/proc/sys/kernel/hostname}, 'чтение файла с хостнеймом'],
  [cmd('env|printenv'), 'полный дамп окружения (env, printenv)'],
  [qr/$dck[^;&|]*?\sinspect(?![\w-])/, 'docker inspect печатает окружение контейнера'],
);
my $CURL = cmd('curl');
my $DUMP = cmd('cat|less|more|head|tail|nl|tac|bat');
my $NETC = cmd('curl|wget|nc|ncat|telnet|ping|ping6|dig|nslookup|host|traceroute|mtr');
my $NETG = qr/${\ cmd('getent')}\s+a?hosts/;
my @C = (
  [qr/\s(?:-X|--request)(?![\w-])/, 'curl: не простой GET (-X)'],
  [qr/\s(?:-[dFT]|--(?:data[\w-]*|form[\w-]*|upload-file|json))(?![\w-])/, 'curl: отправка тела (-d, -F, -T, --data*)'],
  [qr/\s(?:-[OcKkxu]|--(?:remote-name[\w-]*|cookie-jar|config|insecure|proxy[\w-]*|user))(?![\w-])/, 'curl: запись файла, прокси, учётные данные или -k'],
  [qr/\s(?:-o|--output)\s+(?!\/dev\/null(?![\w\/]))/, 'curl: вывод в файл (-o)'],
  [qr/\s(?:-D|--dump-header)\s+(?!\/dev\/(?:stderr|stdout)(?![\w\/]))/, 'curl: заголовки в файл (-D)'],
  [qr/\s-[A-Za-z]*[dFTOcKkxu][A-Za-z]*(?=\s|$)/, 'curl: опция в связке (-d, -F, -T, -O, -c, -K, -k, -x, -u)'],
  [qr/\s-(?=[A-Za-z]{2})[A-Za-z]*o[A-Za-z]*(?=\s|$)/, 'curl: -o в связке опций'],
);

sub delay { my $h = lc shift; $h =~ s/^www\.//; return 40 if $h eq 'marathonec.ru'; return 30 if $h eq 'probeg.org'; return 3 }

my ($rc, $bad) = (0, 0);
for my $f (@ARGV) {
  if (!-f $f || -z $f) { print "$f: файл отсутствует или пуст\n"; $rc = 2; next }
  open(my $fh, '<', $f) or do { print "$f: не читается\n"; $rc = 2; next };
  my @phys = <$fh>; close $fh;
  my $base = $f; $base =~ s{.*/}{};
  my (%acc, %seen, %url, %dup, @F);
  my $add = sub { my ($ln, $m) = @_; push @F, [$ln, $m] unless $dup{"$ln|$m"}++ };
  for my $e (logical(@phys)) {
    my ($ln, $raw) = @$e;
    next if $raw =~ /^\s*#/;
    my $k = strip($raw);
    for my $r (@R) { $add->($ln, $r->[1]) if $k =~ $r->[0] }
    my $r = $k;
    $r =~ s/\d*>\s*>\(//g; $r =~ s/>\(//g; $r =~ s/\d*>&[\d-]//g; $r =~ s{&>\s*/dev/null}{}g;
    $r =~ s{\d*>>?\s*/dev/(?:null|stderr|stdout)(?![\w/])}{}g;
    $add->($ln, 'перенаправление вывода в файл') if $r =~ />/;
    $add->($ln, 'фоновый запуск (одиночный &)') if $k =~ /(?<![&>|<])&(?![&>])/;
    $add->($ln, 'вывод tinyproxy.conf целиком (cat, less, head, tail без фильтра)') if $r =~ /(?:${DUMP})[^;&|]*tinyproxy\.conf[^;&|]*(?:\z|;|&&|\|\|)/;
    if ($base eq 'vps_audit.sh' && ($k =~ $NETC || $k =~ $NETG || $k =~ m{/dev/(?:tcp|udp)/})) {
      $add->($ln, 'vps_audit.sh: исходящая сетевая команда запрещена (curl, wget, ping, dig, host, getent hosts, /dev/tcp)');
    }
    if ($k =~ /$CURL(.*)/s) {
      my $kr = $1; my $rr = ($raw =~ /\bcurl\b(.*)/s) ? $1 : '';
      s/(?:\d*>\s*>?\(|[|;]|&&).*//s for ($kr, $rr);
      for my $c (@C) { $add->($ln, $c->[1]) if $kr =~ $c->[0] }
      $add->($ln, 'curl: -A/--user-agent не "$UA"') if $rr =~ /\s(?:-A|--user-agent)(?:\s+|=)(?!"\$UA"(?:\s|$))/;
      $add->($ln, 'curl: заголовок User-Agent через -H') if $rr =~ /\s(?:-H|--header)(?:\s+|=)["']?\s*user-agent/i;
    }
    while ($raw =~ /(?:^|[\s;&|({])(?:sleep\s+([0-9]+(?:\.[0-9]+)?)|probe\s+["']?(https?:\/\/([^\/\s"'?#]+)[^\s"']*))/g) {
      my ($sl, $u, $h) = ($1, $2, $3);
      if (defined $sl) { $acc{$_} += $sl for keys %seen; next }
      $h = lc $h; $h =~ s/:\d+$//; $h =~ s/^www\.//;
      $add->($ln, "один и тот же URL в probe дважды: $u") if $url{$u}++;
      if ($seen{$h} && $acc{$h} < delay($h)) { $add->($ln, "пауза $acc{$h} с перед probe $h меньше Crawl-delay " . delay($h) . " с") }
      $seen{$h} = 1; $acc{$h} = 0;
    }
  }
  if ($base =~ /^(?:src_probe_[\w-]+|vps_audit)\.sh$/) {
    my ($last) = grep { /\S/ && !/^\s*#/ } reverse @phys; $last //= ''; chomp $last;
    push @F, [scalar(@phys), 'последняя строка прогонного блока должна быть: main </dev/null 2>&1 | rd'] unless $last eq 'main </dev/null 2>&1 | rd';
  }
  for my $x (sort { $a->[0] <=> $b->[0] } @F) { print "$f:$x->[0]: $x->[1]\n"; $bad++ }
}
if ($rc == 2) { exit 2 }
if ($bad) { print "SAFETY FAIL\n"; exit 1 }
print "SAFETY OK\n"; exit 0;
PERL
  perl -e "$pl" -- "$@"
}

# ---- getrun_gate ----------------------------------------------------------
getrun_gate() {
  local pl
  IFS= read -r -d '' pl <<'PERL'
use strict; use warnings;
my ($rep, @files) = @ARGV;
my (@fail, $dec); $dec = '';
if (!defined $rep || !-f $rep || -z $rep) { push @fail, 'отчёт с решением getrun_tos отсутствует или пуст' }
else {
  open(my $fh, '<', $rep) or die "не читается $rep";
  while (my $l = <$fh>) {
    if ($l =~ /^\s*(?:[-*]\s*)?`?getrun_tos`?\s*:\s*`?\s*([A-Za-z-]+)(?![A-Za-z|\/-])/) { $dec = lc $1; last }
  }
  close $fh;
}
$dec = '' unless $dec =~ /^(?:reject|ask-first|probe)$/;
my %ok;
if ($dec eq 'ask-first') { $ok{$_} = 1 for ('get.run', 'https://get.run/robots.txt') }
elsif ($dec eq 'probe') {
  $ok{$_} = 1 for ('get.run', 'https://get.run/robots.txt', 'https://get.run/races/europe/russia/',
    'https://get.run/races/europe/russia/moskovskaya/moskva/mm-moskovskiy-marafon/', 'https://get.run/sitemap-part-1.xml');
}
push @fail, 'не указаны файлы' unless @files;
for my $f (@files) {
  if (!-f $f || -z $f) { push @fail, "$f: файл отсутствует или пуст"; next }
  open(my $fh, '<', $f) or do { push @fail, "$f: не читается"; next };
  my $n = 0;
  while (my $l = <$fh>) {
    $n++;
    next if $l =~ /^\s*#/;
    while ($l =~ /([^\s'"()<>;|&]*get[.]run[^\s'"()<>;|&]*)/ig) {
      next if $ok{lc $1};
      push @fail, "$f:$n: обращение к этому источнику не разрешено решением (" . ($dec || 'не записано') . ")"; last;
    }
  }
  close $fh;
}
if (@fail) { print "GETRUN GATE FAIL: $_\n" for @fail; exit 1 }
print "GETRUN GATE OK\n";
PERL
  perl -e "$pl" -- "$@"
}

# ---- getrun_guard_test ----------------------------------------------------
getrun_guard_test() (
  f="${1:-}"
  [ -s "$f" ] || { echo "GUARD FAIL: нет файла probe_lib.sh"; exit 1; }
  set -u
  T=$(mktemp -d) || exit 1
  trap 'rm -rf "$T"' EXIT
  sed 's|<OWNER_CONTACT>|guard-test-contact|g' "$f" > "$T/lib.sh"
  ( . "$T/lib.sh" ) >/dev/null 2>&1 </dev/null || { echo "GUARD FAIL: probe_lib.sh не загружается"; exit 1; }
  curl() { : > "$T/curl"; printf '%s\n' "$*" > "$T/args"; }
  getent() { : > "$T/getent"; }
  . "$T/lib.sh" >/dev/null 2>&1 </dev/null
  fail() { echo "GUARD FAIL: $1"; exit 1; }
  chk() { local l="$1"; shift; "$@" || fail "$l"; }
  ref() { rm -f "$T/curl" "$T/getent"; local o; o=$("$@" 2>&1 </dev/null); [ ! -e "$T/curl" ] && [ ! -e "$T/getent" ] && case "$o" in *REFUSED*) return 0 ;; esac; return 1; }
  okg() { rm -f "$T/curl"; "$@" >/dev/null 2>&1 </dev/null; [ -e "$T/curl" ] && grep -q -e '--max-redirs 0' "$T/args" && ! grep -Eq '(^| )-L( |$)' "$T/args"; }
  oko() { rm -f "$T/curl"; "$@" >/dev/null 2>&1 </dev/null; [ -e "$T/curl" ] && grep -q -e '-L --max-redirs 5' "$T/args" && grep -q -e '--noproxy' "$T/args"; }
  okd() { rm -f "$T/getent"; "$@" >/dev/null 2>&1 </dev/null; [ -e "$T/getent" ]; }
  H=https://get.run
  others() { local u; for u in https://marathonec.ru/robots.txt https://www.runlist.ru/ https://probeg.org/robots.txt; do chk "другой хост ($1): $u" oko probe "$u"; done; }

  unset GETRUN_ALLOW
  chk "без GETRUN_ALLOW: robots.txt" ref probe $H/robots.txt
  chk "без GETRUN_ALLOW: dns" ref dns get.run
  chk "без GETRUN_ALLOW: верхний регистр URL" ref probe https://GET.RUN/robots.txt
  chk "без GETRUN_ALLOW: dns Get.Run" ref dns Get.Run
  chk "без GETRUN_ALLOW: страница" ref probe $H/races/europe/russia/
  others "без GETRUN_ALLOW"

  GETRUN_ALLOW=robots
  chk "robots: robots.txt" okg probe $H/robots.txt
  chk "robots: robots.txt в верхнем регистре" okg probe https://GET.RUN/robots.txt
  chk "robots: dns get.run" okd dns get.run
  chk "robots: dns Get.Run" okd dns Get.Run
  chk "robots: страница списка отказ" ref probe $H/races/europe/russia/
  chk "robots: sitemap отказ" ref probe $H/sitemap-part-1.xml
  others "robots"

  GETRUN_ALLOW=probe
  for u in robots.txt races/europe/russia/ races/europe/russia/moskovskaya/moskva/mm-moskovskiy-marafon/ sitemap-part-1.xml; do chk "probe: разрешён $u" okg probe $H/$u; done
  chk "probe: dns get.run" okd dns get.run
  chk "probe: races/europe/ отказ" ref probe $H/races/europe/
  chk "probe: oferta отказ" ref probe $H/oferta/
  chk "probe: robots.txt с query отказ" ref probe "$H/robots.txt?x=1"
  chk "probe: http вместо https отказ" ref probe http://get.run/robots.txt
  chk "probe: похожий хост отказ" ref probe https://get.run.example.org/robots.txt
  others "probe"

  for v in yes PROBE all ""; do
    GETRUN_ALLOW=$v
    chk "неизвестное значение '$v': robots.txt" ref probe $H/robots.txt
    chk "неизвестное значение '$v': dns" ref dns get.run
  done
  others "неизвестное значение"
  echo "GUARD OK"
)

# ---- selftest -------------------------------------------------------------
selftest() (
  T=$(mktemp -d) || { echo "SELFTEST FAIL: mktemp"; exit 1; }
  trap 'rm -rf "$T"' EXIT
  fail() { echo "SELFTEST FAIL: $1"; exit 1; }
  type rd >/dev/null 2>&1 || fail "rd не загружен (перед checks.sh нужен блок rd.sh)"

  # --- redaction_check: каждую категорию ловит отдельно
  red_bad() { local o r; o=$(redaction_check "$T/f" 2>&1 </dev/null); r=$?; [ "$r" = 1 ] || fail "redaction_check не поймал: $1"; case "$o" in *"$2"*) ;; *) fail "redaction_check: нет категории $2 для: $1" ;; esac; }
  printf 'inet %s.%s.%s.%s/24 scope global\n' 203 0 113 7 > "$T/f"; red_bad "публичный IPv4" ipv4
  printf 'addr %s:%s:%s:%s:%s:%s:%s:%s\n' 2001 db8 0 0 0 0 0 1 > "$T/f"; red_bad "IPv6 полный" ipv6
  printf 'addr %s::%s scope global\n' 2001:db8 1 > "$T/f"; red_bad "IPv6 сжатый" ipv6
  printf '%s%s\n' '-----BEGIN OPENSSH ' 'PRIVATE KEY-----' > "$T/f"; red_bad "заголовок приватного ключа" private-key
  printf 'Upstream http %s:%s%s%s:%s\n' user pass @ host 8080 > "$T/f"; red_bad "Upstream с логином и паролем" url-credentials
  printf '%s%s%s:%s%s%s\n' https :// user pass @ host > "$T/f"; red_bad "учётные данные в URL" url-credentials
  printf 'BasicAuth %s %s\n' admin hunter2 > "$T/f"; red_bad "BasicAuth логин пароль" basicauth
  printf '%s=%s\n' api_key abcdef123 > "$T/f"; red_bad "присвоение секрета" secret-assignment
  printf '%s@%s.%s\n' someone example org > "$T/f"; red_bad "e-mail" email
  printf '%s\n' '0.0.0.0:8888' '127.0.0.53%lo:53' '[::]:22' '::1' '<PUB-IP> <RFC1918-10> <IPv6>' 'passwordauthentication no' 'число строк BasicAuth в конфиге (только счётчик): 0' '12:34:56' 'Docker version 27.5.1' 'server: nginx/1.24.0' 't.me/agetrun' > "$T/ok"
  o=$(redaction_check "$T/ok" 2>&1 </dev/null) || fail "redaction_check ложно сработал: $o"
  case "$o" in *"REDACTION OK"*) ;; *) fail "нет REDACTION OK на чистом файле" ;; esac
  redaction_check "$T/nope" >/dev/null 2>&1 </dev/null; [ $? -eq 2 ] || fail "redaction_check: отсутствующий файл должен давать код 2"
  : > "$T/empty"; redaction_check "$T/empty" >/dev/null 2>&1 </dev/null; [ $? -eq 2 ] || fail "redaction_check: пустой файл должен давать код 2"

  # --- rd: замена адресов, после неё redaction_check проходит
  { printf 'inet %s.%s.%s.%s/24\n' 203 0 113 7; printf 'gw %s.%s.%s.%s\n' 10 1 2 3; printf 'gw %s.%s.%s.%s\n' 192 168 0 5
    printf 'gw %s.%s.%s.%s\n' 172 16 5 4; printf 'a %s:%s:%s:%s:%s:%s:%s:%s\n' 2001 db8 0 0 0 0 0 1; printf 'b %s::%s\n' 2001:db8 1
    printf 'c %s::%s%%lo\n' fe80 1; echo '0.0.0.0:8888'; echo '127.0.0.1'; echo '12:34:56'; } | rd > "$T/rdout"
  for m in '<PUB-IP>' '<RFC1918-10>' '<RFC1918-192>' '<RFC1918-172>' '<IPv6>'; do grep -qF "$m" "$T/rdout" || fail "rd не поставил метку $m"; done
  grep -qxF '0.0.0.0:8888' "$T/rdout" && grep -qxF '127.0.0.1' "$T/rdout" && grep -qxF '12:34:56' "$T/rdout" || fail "rd изменил 0.0.0.0, 127.0.0.1 или время"
  o=$(redaction_check "$T/rdout" 2>&1 </dev/null) || fail "после rd redaction_check не проходит: $o"

  # --- safety_lint
  lint_bad() { printf '%s\n' "$@" > "$T/fx.sh"; safety_lint "$T/fx.sh" >/dev/null 2>&1 </dev/null; [ $? -eq 1 ] || fail "safety_lint не поймал: $*"; }
  lint_ok() { local o; printf '%s\n' "$@" > "$T/fx.sh"; o=$(safety_lint "$T/fx.sh" 2>&1 </dev/null); [ $? -eq 0 ] || fail "safety_lint ложно сработал: $* ($o)"; }
  lint_bad 'systemctl restart tinyproxy'
  lint_bad 'apt-get install -y docker-compose-v2'
  lint_bad 'ufw enable'
  lint_bad 'docker system prune -f'
  lint_bad 'swapon /swapfile'
  lint_bad 'echo 1 > /etc/x'
  lint_bad 'echo 1 >> /tmp/x'
  lint_bad 'sed -i s/a/b/ /etc/x'
  lint_bad 'curl -X POST https://example.com/'
  lint_bad "curl -A 'Mozilla/5.0' https://example.com/"
  lint_bad 'sleep 5 &'
  lint_bad 'xargs -P 4 echo'
  lint_bad 'curl -sSo /tmp/f https://example.com/'
  lint_bad 'curl -o /tmp/f https://example.com/'
  lint_bad 'iptables -A INPUT -j DROP'
  lint_bad 'docker compose up -d'
  lint_bad 'apt update'
  lint_bad 'echo "n: $(tee /tmp/z)"'
  lint_bad 'probe https://marathonec.ru/a' 'sleep 39' 'probe https://marathonec.ru/b'
  lint_bad 'probe https://probeg.org/a' 'sleep 29' 'probe https://probeg.org/b'
  lint_bad 'probe https://example.com/x' 'sleep 3' 'probe https://example.com/x'
  lint_ok 'swapon --show'
  lint_ok 'ufw status verbose'
  lint_ok 'systemctl show tinyproxy -p ActiveState'
  lint_ok 'docker ps -a'
  lint_ok 'cmd 2>&1'
  lint_ok 'cmd >/dev/null'
  lint_ok 'cmd 2> >(tr -d x >&2)'
  lint_ok "awk 'NR>5{print \$1}'"
  lint_ok "perl -pe 's/x/<PUB-IP>/g'"
  lint_ok "curl -sS --noproxy '*' -A \"\$UA\" -D /dev/stderr https://example.com/"
  lint_ok 'probe https://marathonec.ru/a' 'sleep 40' 'probe https://marathonec.ru/b'
  lint_ok 'probe https://probeg.org/a' 'sleep 30' 'probe https://probeg.org/b'
  lint_ok 'iptables -S'
  lint_ok 'nft list ruleset'
  lint_ok 'curl -o /dev/null https://example.com/'
  lint_ok 'apt list --upgradable'
  lint_ok 'apt-cache policy docker-compose-v2'
  lint_ok 'dpkg -l'
  lint_ok 'systemctl is-enabled docker'
  lint_bad 'uname -a'
  lint_bad 'uname -n'
  lint_bad 'hostname'
  lint_bad 'hostnamectl'
  lint_bad 'cat /etc/hostname'
  lint_bad 'docker inspect x'
  lint_bad 'printenv'
  lint_bad 'env'
  lint_bad 'cat /etc/tinyproxy/tinyproxy.conf'
  lint_bad 'sudo cat /etc/tinyproxy/tinyproxy.conf 2>&1'
  lint_ok 'uname -r'
  lint_ok "grep -iE '^(Port|Listen)' /etc/tinyproxy/tinyproxy.conf"
  lint_ok 'echo "hostname: не печатается"'
  # правила по имени файла: сетевые команды запрещены только в vps_audit.sh
  vlint_bad() { local o r; printf '%s\n' "$@" 'main </dev/null 2>&1 | rd' > "$T/vps_audit.sh"; o=$(safety_lint "$T/vps_audit.sh" 2>&1 </dev/null); r=$?; [ "$r" -eq 1 ] || fail "safety_lint не поймал в vps_audit.sh: $*"; case "$o" in *'vps_audit.sh: исходящая сетевая'*) ;; *) fail "safety_lint: нет сообщения о сети для: $*" ;; esac; }
  vlint_bad 'curl -sS -m 10 -I https://www.google.com'
  vlint_bad 'getent ahostsv4 example.com'
  vlint_bad 'dig example.com'
  vlint_bad 'echo x; ping -c1 example.com'
  o=$(printf '%s\n' 'timedatectl' 'uname -r' 'main </dev/null 2>&1 | rd' > "$T/vps_audit.sh"; safety_lint "$T/vps_audit.sh" 2>&1 </dev/null) || fail "safety_lint ложно сработал на vps_audit.sh: $o"
  o=$(printf '%s\n' 'curl -sS https://example.com/' > "$T/probe_lib.sh"; safety_lint "$T/probe_lib.sh" 2>&1 </dev/null) || fail "safety_lint: curl в probe_lib.sh должен проходить: $o"
  printf '%s\n' 'main() { echo x; }' 'main </dev/null 2>&1 | rd' > "$T/src_probe_fx.sh"
  o=$(safety_lint "$T/src_probe_fx.sh" 2>&1 </dev/null) || fail "safety_lint: прогонный блок с rd отклонён: $o"
  printf '%s\n' 'main() { echo x; }' 'main' > "$T/src_probe_fx.sh"
  safety_lint "$T/src_probe_fx.sh" >/dev/null 2>&1 </dev/null; [ $? -eq 1 ] || fail "safety_lint: прогонный блок без rd должен отклоняться"
  safety_lint "$T/nope.sh" >/dev/null 2>&1 </dev/null; [ $? -eq 2 ] || fail "safety_lint: отсутствующий файл должен давать код 2"

  # --- getrun_gate
  mk() { local n="$1"; shift; printf '%s\n' "$@" > "$T/$n"; }
  mk b_none 'probe https://marathonec.ru/robots.txt' 'echo "REFUSED: обращение не разрешено решением владельца (GETRUN_ALLOW)"'
  mk b_robots 'dns get.run' "probe https://get.run/robots.txt 'Disallow'"
  mk b_races 'probe https://get.run/races/europe/russia/'
  mk b_p4 'dns get.run' 'probe https://get.run/robots.txt' 'probe https://get.run/races/europe/russia/' 'probe https://get.run/races/europe/russia/moskovskaya/moskva/mm-moskovskiy-marafon/' 'probe https://get.run/sitemap-part-1.xml'
  mk b_eu 'probe https://get.run/races/europe/'
  mk b_of 'probe https://get.run/oferta/'
  mk b_up 'probe https://GET.RUN/robots.txt'
  mk b_dnsup 'dns Get.Run'
  mk b_cm '# probe https://get.run/oferta/' 'probe https://marathonec.ru/robots.txt'
  : > "$T/b_empty"
  rep() { if [ -n "$1" ]; then printf 'getrun_tos: %s\n' "$1" > "$T/rep"; else printf 'заметка без решения\n' > "$T/rep"; fi; }
  gate_ok() { local d="$1"; shift; rep "$d"; getrun_gate "$T/rep" "$@" >/dev/null 2>&1 </dev/null || fail "getrun_gate ложный отказ ($d): $*"; }
  gate_no() { local d="$1"; shift; rep "$d"; getrun_gate "$T/rep" "$@" >/dev/null 2>&1 </dev/null && fail "getrun_gate пропустил ($d): $*"; return 0; }
  gate_ok reject "$T/b_none"; gate_ok reject "$T/b_cm"
  for b in b_robots b_races b_p4 b_eu b_of b_up b_dnsup; do gate_no reject "$T/$b"; done
  gate_ok ask-first "$T/b_none" "$T/b_robots" "$T/b_up" "$T/b_dnsup"
  for b in b_races b_p4 b_eu b_of; do gate_no ask-first "$T/$b"; done
  gate_ok probe "$T/b_none" "$T/b_robots" "$T/b_races" "$T/b_p4" "$T/b_up" "$T/b_dnsup"
  gate_no probe "$T/b_eu"; gate_no probe "$T/b_of"; gate_no probe "$T/b_none" "$T/b_eu"
  gate_ok '' "$T/b_none" "$T/b_cm"
  for b in b_robots b_races b_p4 b_up b_dnsup; do gate_no '' "$T/$b"; done
  gate_ok bogus "$T/b_none"; gate_no bogus "$T/b_robots"
  gate_no reject "$T/nope"; gate_no reject "$T/b_empty"
  getrun_gate "$T/nope_report" "$T/b_none" >/dev/null 2>&1 </dev/null && fail "getrun_gate: без отчёта должен отказывать"

  # --- getrun_guard_test: защита, которая ничего не запрещает или запрещает не всё, должна проваливаться
  printf '%s\n' "UA='x'" 'getrun_guard() { return 0; }' 'probe() { getrun_guard "$1"; curl "$1"; }' 'dns() { getrun_guard "$1"; getent "$1"; }' > "$T/lib_bad1.sh"
  o=$(getrun_guard_test "$T/lib_bad1.sh" 2>&1 </dev/null) && fail "getrun_guard_test пропустил защиту, которая ничего не запрещает"
  case "$o" in *"GUARD FAIL"*) ;; *) fail "getrun_guard_test: нет GUARD FAIL для слабой защиты" ;; esac
  printf '%s\n' "UA='x'" 'getrun_guard() { case "$1" in *get.run*) return 1 ;; esac; return 0; }' 'probe() { getrun_guard "$1" && curl -L "$1"; }' 'dns() { getrun_guard "$1" && getent "$1"; }' > "$T/lib_bad2.sh"
  getrun_guard_test "$T/lib_bad2.sh" >/dev/null 2>&1 </dev/null && fail "getrun_guard_test пропустил защиту без GETRUN_ALLOW"
  getrun_guard_test "$T/nope.sh" >/dev/null 2>&1 </dev/null && fail "getrun_guard_test: отсутствующий файл должен проваливаться"
  echo "SELFTEST OK"
)
```

## 3. Прогон 1 (tracer): Марафонец

Три GET к `marathonec.ru`: `robots.txt`, `/calendar-beg/`, `/trail-burya/`, между ними по 40 с (Crawl-delay). К GET.RUN обращений нет. Прогон занимает около 1,5 минут.

```bash src_probe_marathonec.sh
main() {
  sec "A. Марафонец (marathonec.ru): robots Crawl-delay 40, Disallow /*?*"
  dns marathonec.ru
  probe https://marathonec.ru/robots.txt 'Crawl-delay' 'Disallow'
  echo "(пауза 40 с: соблюдаем Crawl-delay)"; sleep 40
  probe https://marathonec.ru/calendar-beg/ 'post-row-[0-9]+' 'reg\.russiarunning\.com' 'ld\+json'
  echo "(пауза 40 с)"; sleep 40
  probe https://marathonec.ru/trail-burya/ 'reg\.place' 'ld\+json'
  echo; echo "===== КОНЕЦ ====="
}
main </dev/null 2>&1 | rd
```

#### Перед запуском (обязательно)

`safety_lint` не заменяет ручную проверку владельца. Перед запуском на VPS:

1. Откройте `01-COMMANDS.md`.
2. Прочитайте ПОЛНОСТЬЮ все блоки, которые уйдут на VPS: `blk rd.sh`, `blk probe_lib.sh`, `blk src_probe_marathonec.sh` (выведите их в терминал и прочитайте).
3. Запустите проверку ниже (она сама записывает блоки в файлы `rd.sh`, `probe_lib.sh`, `src_probe_marathonec.sh` во временном каталоге и вызывает `safety_lint`, `getrun_gate` и `getrun_guard_test` по этим именам; подстановку процесса `<(blk …)` не использовать) и прочитайте её вывод целиком.
4. Убедитесь, что все команды только читают, а URL только из ожидаемого списка: три страницы `marathonec.ru`, к GET.RUN обращений нет.
5. Только после этого самостоятельно запустите `ssh`.
6. Если что-то непонятно, не запускайте и спросите.

```bash
X=$(mktemp -d); for b in rd.sh probe_lib.sh src_probe_marathonec.sh; do blk "$b" > "$X/$b"; done
{ blk rd.sh; blk checks.sh; echo "safety_lint $X/rd.sh $X/probe_lib.sh $X/src_probe_marathonec.sh; getrun_gate .planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md $X/rd.sh $X/probe_lib.sh $X/src_probe_marathonec.sh; getrun_guard_test $X/probe_lib.sh"; } | bash
```

Ожидаемо: `SAFETY OK`, `GETRUN GATE OK`, `GUARD OK`.

Команда запуска. Контакт (допустимы только символы `A-Za-z0-9@._:/+-`) и алиас вводятся один раз в этом терминале и нигде не записываются:

```bash
printf 'Контакт для User-Agent (e-mail или Telegram): '; read -r OWNER_CONTACT
printf '%s' "$OWNER_CONTACT" | grep -Eq '^[A-Za-z0-9@._:/+-]+$' || echo "недопустимый контакт: запускать нельзя"
export OWNER_CONTACT
printf 'ssh-алиас VPS из ~/.ssh/config: '; read -r VPS_ALIAS
{ blk rd.sh; blk probe_lib.sh; blk src_probe_marathonec.sh; } \
  | sed "s|<OWNER_CONTACT>|${OWNER_CONTACT}|g" \
  | ssh "${VPS_ALIAS:?задайте ssh-алиас}" 'bash -s' \
  | tee "$HOME/run-agg-phase1/src_probe_marathonec_out.txt"
```

Проверка вывода. `VPS_IP` и `VPS_HOST` вводятся без отображения и экспортируются только в этом терминале:

```bash
{ blk rd.sh; blk checks.sh; echo 'redaction_check "$HOME/run-agg-phase1/src_probe_marathonec_out.txt"'; } | bash
printf 'IP VPS (не отображается): '; read -rs VPS_IP; echo; printf 'hostname VPS (не отображается): '; read -rs VPS_HOST; echo; export VPS_IP VPS_HOST
{ blk rd.sh; blk checks.sh; echo 'owner_leak_check "$HOME/run-agg-phase1/src_probe_marathonec_out.txt"'; } | bash
```

Ожидаемо: `REDACTION OK` и `OWNER LEAK OK`. Если нет, вывод никому не передавать и сообщить.

### Как читать результат

Baseline с рабочей машины (2026-09-25) для Марафонца: `/calendar-beg/` содержит около 150 строк `post-row-[0-9]+` и весит около 608 КБ; `robots.txt` содержит `Crawl-delay: 40`.

Вердикт по каждому URL (порог фиксирован этим планом и действует во всех планах фазы):

- `доступен`: код 200, нет индикаторов челленджа, нет заголовков `cf-mitigated` и `x-vercel-mitigated`, ключевой маркер и размер не меньше 50 % baseline (ровно 50 % считается доступным; для `/calendar-beg/` ключевой маркер `post-row-[0-9]+`, baseline 150, размер около 608 КБ);
- `подмена`: код 200, но маркер или размер меньше 50 % baseline;
- `челлендж`: есть индикаторы челленджа или mitigated-заголовки;
- `блокировка`: 403, 429 или 451;
- `сеть недоступна`: `curl: (6)`, `(7)`, `(28)`, `(35)`; `curl: (60)` записывается как проблема сертификатов на VPS, `-k` не использовать.

Для URL без baseline вердикт только по коду и индикаторам. Итоговый вердикт источника — худший из вердиктов его URL. Шестой вердикт `не проверялся` допустим только у GET.RUN при `getrun_tos: reject`.

## 4. Прогон 2: аудит VPS

Порядок прогона 2, скрипты не запускать параллельно: сначала `vps_audit.sh` (этот раздел), затем `src_probe_rest.sh` (§5), затем `src_probe_getrun.sh` (§5, блок существует только при `getrun_tos` = ask-first или probe в §4.1 `01-SOURCES-REPORT.md`). Каждый скрипт запускается отдельной командой и завершается до начала следующего.

`vps_audit.sh` только читает локальное состояние VPS: ничего не устанавливает, не перезапускает, не пишет файлы и не делает исходящих сетевых запросов (нет `curl`, DNS-запросов и обращений к внешним сайтам; время проверяется только локальными средствами). Хостнейм не печатается ни одной командой, IP клиентов tinyproxy не печатаются (только счётчики), `BasicAuth` и `Upstream` считаются, но не показываются. Если вход не под `root`, используется `sudo -n` (без запроса пароля; при отказе блок напишет ошибку и пойдёт дальше). Весь вывод проходит через `rd` на стороне VPS.

```bash vps_audit.sh
sec() { printf '\n===== %s =====\n' "$1"; }

main() {
  SUDO=""; [ "$(id -u)" -ne 0 ] && SUDO="sudo -n"

  sec "00 OS, ядро, аптайм"
  sed -n 's/^PRETTY_NAME=//p' /etc/os-release
  uname -r; uptime -p; systemd-detect-virt 2>/dev/null
  [ -e /var/run/reboot-required ] && { echo "reboot-required: YES"; cat /var/run/reboot-required.pkgs 2>/dev/null | head -20; } || echo "reboot-required: no"

  sec "01 tinyproxy: статус и происхождение"
  systemctl show tinyproxy -p LoadState -p ActiveState -p SubState -p UnitFileState -p FragmentPath -p ActiveEnterTimestamp 2>&1
  ps -o pid,user,etime,args -C tinyproxy 2>&1
  dpkg -l 'tinyproxy*' 2>&1 | awk 'NR>5{print $1,$2,$3}'
  apt-mark showmanual 2>/dev/null | grep -i '^tinyproxy' || echo "tinyproxy: не в списке manual (или не установлен)"
  zgrep -h ' install tinyproxy' /var/log/dpkg.log* 2>/dev/null | head -3
  stat -c '%n  изменён: %y  владелец: %U' /etc/tinyproxy /etc/tinyproxy/tinyproxy.conf 2>&1
  for v in http_proxy https_proxy HTTP_PROXY HTTPS_PROXY no_proxy; do
    if [ -n "${!v:-}" ]; then echo "переменная $v в этой ssh-сессии: задана"; else echo "переменная $v в этой ssh-сессии: не задана"; fi
  done
  echo "строк с proxy в /etc/environment (только счётчик): $(cat /etc/environment 2>/dev/null | grep -ci proxy)"

  sec "02 tinyproxy: директивы конфигурации (только allowlist; пароли не печатаются)"
  $SUDO grep -iE '^[[:space:]]*(Port|Listen|Bind|BindSame|Allow|Deny|MaxClients|Timeout|ConnectPort|DisableViaHeader|Anonymous|FilterURLs|ReversePath|LogLevel|Syslog|User|Group|StatHost)\b' /etc/tinyproxy/tinyproxy.conf 2>&1
  echo "строк BasicAuth в конфиге (только счётчик): $($SUDO grep -ciE '^[[:space:]]*BasicAuth' /etc/tinyproxy/tinyproxy.conf 2>/dev/null)"
  echo "строк Upstream в конфиге (только счётчик): $($SUDO grep -ciE '^[[:space:]]*Upstream' /etc/tinyproxy/tinyproxy.conf 2>/dev/null)"

  sec "03 tinyproxy: сколько и кем используется (только агрегаты)"
  LOG=$($SUDO grep -iE '^[[:space:]]*LogFile' /etc/tinyproxy/tinyproxy.conf 2>/dev/null | head -1 | sed -E 's/^[^"]*"([^"]+)".*/\1/')
  echo "LogFile: ${LOG:-не задан}"
  if [ -n "${LOG:-}" ] && $SUDO test -r "$LOG"; then
    $SUDO stat -c 'размер=%s байт, последнее изменение=%y' "$LOG"
    echo "соединений в логе: $($SUDO grep -c 'Connect (file descriptor' "$LOG")"
    echo "разных клиентов: $($SUDO grep -oE 'Connect \(file descriptor [0-9]+\): [0-9a-fA-F.:]+' "$LOG" | awk '{print $NF}' | sort -u | wc -l)"
    echo "из них вне RFC1918 и loopback: $($SUDO grep -oE 'Connect \(file descriptor [0-9]+\): [0-9.]+' "$LOG" | awk '{print $NF}' | sort -u | grep -vE '^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|127\.)' | wc -l)"
  fi

  sec "03b НЕОБЯЗАТЕЛЬНО (можно удалить эти строки перед запуском): топ-10 запрашиваемых хостов tinyproxy"
  if [ -n "${LOG:-}" ] && $SUDO test -r "$LOG"; then
    $SUDO grep -oE 'Request \(file descriptor [0-9]+\): (CONNECT|GET|POST|HEAD) [^ ]+' "$LOG" | awk '{print $NF}' | sed -E 's#^https?://##; s#[:/].*$##' | sort | uniq -c | sort -rn | head -10
  fi

  sec "04 слушающие порты (TCP/UDP)"
  $SUDO ss -H -tulpn 2>&1

  sec "05 фаервол: ufw и iptables"
  $SUDO ufw status verbose 2>&1
  $SUDO ufw status numbered 2>&1
  grep -E '^(IPV6|DEFAULT_[A-Z_]+_POLICY)=' /etc/default/ufw 2>&1
  $SUDO iptables -S 2>&1 | head -60
  $SUDO ip6tables -S 2>&1 | head -30

  sec "06 часы и синхронизация (только локальные средства)"
  timedatectl 2>&1
  timedatectl timesync-status 2>&1
  for s in systemd-timesyncd chrony chronyd ntp ntpsec; do printf '%s: ' "$s"; systemctl is-active "$s" 2>&1 | head -1; done
  command -v chronyc >/dev/null && chronyc tracking 2>&1
  journalctl -u systemd-timesyncd -n 15 --no-pager -o cat 2>&1

  sec "07 Docker и Compose"
  command -v docker && docker --version 2>&1
  docker compose version 2>&1 | head -2
  command -v docker-compose && docker-compose --version 2>&1
  dpkg -l 2>/dev/null | awk '/^ii +(docker|containerd|runc|podman)/{print $2,$3}'
  systemctl is-active docker 2>&1; systemctl is-enabled docker 2>&1
  apt-cache policy docker-compose-v2 2>&1 | head -4
  $SUDO docker info --format 'server={{.ServerVersion}} driver={{.Driver}} cgroup={{.CgroupVersion}} containers={{.Containers}} running={{.ContainersRunning}}' 2>&1
  $SUDO docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}' 2>&1
  $SUDO docker system df 2>&1
  ls -l /etc/docker 2>&1

  sec "08 память, swap, диск, inodes"
  free -m; swapon --show 2>&1; echo "swappiness=$(cat /proc/sys/vm/swappiness)"; grep -i swap /etc/fstab || echo "swap в fstab: нет"
  nproc
  df -hT -x tmpfs -x devtmpfs -x overlay -x squashfs 2>&1
  df -i / 2>&1
  lsblk -o NAME,SIZE,TYPE,MOUNTPOINT 2>&1

  sec "09 обновления (без apt update и без установки)"
  echo "кэш apt обновлён: $(stat -c %y /var/lib/apt/periodic/update-success-stamp 2>/dev/null || echo неизвестно)"
  echo "доступно обновлений (по локальному кэшу): $(apt list --upgradable 2>/dev/null | tail -n +2 | wc -l)"
  echo "из них security: $(apt list --upgradable 2>/dev/null | grep -c -i security)"
  echo "unattended-upgrades: установлен=$(dpkg -l unattended-upgrades 2>/dev/null | grep -c '^ii'), enabled=$(systemctl is-enabled unattended-upgrades 2>&1 | head -1)"

  sec "10 SSH: эффективные настройки входа (без ключей)"
  $SUDO sshd -T 2>&1 | grep -iE '^(port|permitrootlogin|passwordauthentication|pubkeyauthentication|kbdinteractiveauthentication|permitemptypasswords|maxauthtries|allowusers|allowgroups) '
  echo "uid текущего пользователя: $(id -u) (0 = root)"

  sec "11 запущенные сервисы (имена юнитов)"
  systemctl list-units --type=service --state=running --no-pager --no-legend 2>&1 | awk '{print $1}'
  echo; echo "===== КОНЕЦ ====="
}
main </dev/null 2>&1 | rd
```

#### Перед запуском (обязательно)

`safety_lint` не заменяет ручную проверку владельца. Перед запуском на VPS:

1. Откройте `01-COMMANDS.md`.
2. Прочитайте ПОЛНОСТЬЮ все блоки, которые уйдут на VPS: `blk rd.sh`, `blk vps_audit.sh` (выведите их в терминал и прочитайте).
3. Запустите проверку ниже (она сама записывает блоки в файлы `rd.sh` и `vps_audit.sh` во временном каталоге и вызывает `safety_lint` и `getrun_gate` по этим именам; подстановку процесса `<(blk …)` не использовать) и прочитайте её вывод целиком.
4. Убедитесь, что все команды только читают: нет установки пакетов, `apt update`, записи файлов, `systemctl`, `ufw`, `docker` в изменяющих режимах, а исходящих сетевых запросов в аудите нет.
5. Только после этого самостоятельно запустите `ssh`.
6. Если что-то непонятно, не запускайте и спросите. Необязательный подблок `03b` (топ хостов tinyproxy) можно удалить из блока до запуска.

```bash
X=$(mktemp -d); for b in rd.sh vps_audit.sh; do blk "$b" > "$X/$b"; done
{ blk rd.sh; blk checks.sh; echo "safety_lint $X/rd.sh $X/vps_audit.sh; getrun_gate .planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md $X/rd.sh $X/vps_audit.sh"; } | bash
```

Ожидаемо: `SAFETY OK` и `GETRUN GATE OK`.

Команда запуска. Алиас вводится в этом терминале и нигде не записывается (если он уже задан в этом терминале в прогоне 1, повторный ввод пропускается):

```bash
[ -n "${VPS_ALIAS:-}" ] || { printf 'ssh-алиас VPS из ~/.ssh/config: '; read -r VPS_ALIAS; }
{ blk rd.sh; blk vps_audit.sh; } \
  | ssh "${VPS_ALIAS:?задайте ssh-алиас}" 'bash -s' \
  | tee "$HOME/run-agg-phase1/vps_audit_out.txt"
```

Проверка вывода. `VPS_IP` и `VPS_HOST` вводятся без отображения и экспортируются только в этом терминале:

```bash
{ blk rd.sh; blk checks.sh; echo 'redaction_check "$HOME/run-agg-phase1/vps_audit_out.txt"'; } | bash
[ -n "${VPS_IP:-}" ] && [ -n "${VPS_HOST:-}" ] || { printf 'IP VPS (не отображается): '; read -rs VPS_IP; echo; printf 'hostname VPS (не отображается): '; read -rs VPS_HOST; echo; export VPS_IP VPS_HOST; }
{ blk rd.sh; blk checks.sh; echo 'owner_leak_check "$HOME/run-agg-phase1/vps_audit_out.txt"'; } | bash
```

Ожидаемо: `REDACTION OK` и `OWNER LEAK OK`. Если нет, вывод никому не передавать, заменить найденное на `<REDACTED>` вручную и проверить снова или сообщить.

### Как читать блок tinyproxy

`Listen` без значения означает все интерфейсы (`0.0.0.0`). Если строк `Allow` и `Deny` нет, действует «разрешить всем». Хотя бы одна запись `BasicAuth` включает обязательную аутентификацию (в выводе только счётчик строк). Без `ConnectPort` для `CONNECT` разрешены все порты. «Открытый прокси» = слушает не на loopback И нет ограничений `Allow` И `BasicAuth` = 0 И порт не закрыт фаерволом (`ufw`, `iptables`, облачный фаервол). Внешнюю достижимость с самого VPS проверить нельзя. Формат строк лога (`Connect (file descriptor N): …`, `Request (file descriptor N): …`) предполагается по памяти; если формат иной, счётчики покажут 0.

## 5. Прогон 2: остальные источники

Порядок и запрет параллельного запуска: см. начало §4. `src_probe_rest.sh` запрашивает только Runlist и ПроБЕГ; в нём нет ни одного обращения к GET.RUN (ни HTTP, ни DNS, ни в комментариях), Марафонец повторно не запрашивается (он уже проверен в прогоне 1). Паузы: 3 с между запросами к Runlist, 30 с между запросами к ПроБЕГ (Crawl-delay 30). Всего 5 GET к Runlist и 3 GET к ПроБЕГ, около 2–3 минут.

```bash src_probe_rest.sh
main() {
  sec "C. Runlist (www.runlist.ru): robots Allow /"
  dns www.runlist.ru
  probe https://www.runlist.ru/robots.txt 'Allow' 'Sitemap'
  sleep 3
  probe https://www.runlist.ru/ '__next_f' 'beginDate' 'reg\.russiarunning\.com/event/'
  sleep 3
  probe https://www.runlist.ru/event/skazka26 'ld\+json' 'SportsEvent' 'addressCountry'
  sleep 3
  probe https://www.runlist.ru/events 'beginDate' 'reg\.russiarunning\.com/event/' 'page=[0-9]+'
  sleep 3
  probe https://www.runlist.ru/sitemap.xml '<loc>https://www\.runlist\.ru/event/[^<]+' '<lastmod>'

  sec "D. ПроБЕГ (probeg.org): robots Crawl-delay 30"
  dns probeg.org
  probe https://probeg.org/robots.txt 'Crawl-delay' 'Disallow'
  echo "(пауза 30 с: соблюдаем Crawl-delay)"; sleep 30
  probe https://probeg.org/races/ 'href="/event/[0-9]+/"' 'href="/races/city/[0-9]+/"' '<tr'
  echo "(пауза 30 с)"; sleep 30
  probe https://probeg.org/races/city/2310/ 'href="/event/[0-9]+/"'
  echo; echo "===== КОНЕЦ ====="
}
main </dev/null 2>&1 | rd
```

Baseline с рабочей машины (2026-09-25 и 2026-09-26) для сверки: `www.runlist.ru/` около 896–917 КБ, `__next_f` 11, `beginDate` около 178–183, `reg\.russiarunning\.com/event/` около 178; `/event/skazka26` около 182 КБ, `ld+json` 4, `SportsEvent` 2, `addressCountry` 2; `probeg.org/races/` 372 821 Б, около 1246 ссылок на события и 212 на города; `probeg.org/races/city/2310/` около 116 КБ, около 207 ссылок на события. Вердикты выносятся по порогам из §3 (для URL без baseline только по коду и индикаторам).

#### Перед запуском (обязательно)

`safety_lint` не заменяет ручную проверку владельца. Перед запуском на VPS:

1. Откройте `01-COMMANDS.md`.
2. Прочитайте ПОЛНОСТЬЮ все блоки, которые уйдут на VPS: `blk rd.sh`, `blk probe_lib.sh`, `blk src_probe_rest.sh` (выведите их в терминал и прочитайте).
3. Запустите проверку ниже (она сама записывает блоки в файлы `rd.sh`, `probe_lib.sh`, `src_probe_rest.sh` во временном каталоге и вызывает `safety_lint`, `getrun_gate` и `getrun_guard_test` по этим именам; подстановку процесса `<(blk …)` не использовать) и прочитайте её вывод целиком.
4. Убедитесь, что все команды только читают, а URL только из ожидаемого списка: пять страниц `www.runlist.ru` и три страницы `probeg.org`; обращений к GET.RUN нет.
5. Только после этого самостоятельно запустите `ssh`.
6. Если что-то непонятно, не запускайте и спросите.

```bash
X=$(mktemp -d); for b in rd.sh probe_lib.sh src_probe_rest.sh; do blk "$b" > "$X/$b"; done
{ blk rd.sh; blk checks.sh; echo "safety_lint $X/rd.sh $X/probe_lib.sh $X/src_probe_rest.sh; getrun_gate .planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md $X/rd.sh $X/probe_lib.sh $X/src_probe_rest.sh; getrun_guard_test $X/probe_lib.sh"; } | bash
```

Ожидаемо: `SAFETY OK`, `GETRUN GATE OK`, `GUARD OK`.

Команда запуска. Контакт (допустимы только символы `A-Za-z0-9@._:/+-`) и алиас вводятся один раз в этом терминале и нигде не записываются (если они уже заданы в этом терминале, повторный ввод пропускается):

```bash
[ -n "${OWNER_CONTACT:-}" ] || { printf 'Контакт для User-Agent (e-mail или Telegram): '; read -r OWNER_CONTACT; }
printf '%s' "$OWNER_CONTACT" | grep -Eq '^[A-Za-z0-9@._:/+-]+$' || echo "недопустимый контакт: запускать нельзя"
export OWNER_CONTACT
[ -n "${VPS_ALIAS:-}" ] || { printf 'ssh-алиас VPS из ~/.ssh/config: '; read -r VPS_ALIAS; }
{ blk rd.sh; blk probe_lib.sh; blk src_probe_rest.sh; } \
  | sed "s|<OWNER_CONTACT>|${OWNER_CONTACT}|g" \
  | ssh "${VPS_ALIAS:?задайте ssh-алиас}" 'bash -s' \
  | tee "$HOME/run-agg-phase1/src_probe_rest_out.txt"
```

Проверка вывода (`VPS_IP` и `VPS_HOST` задаются как в §4):

```bash
{ blk rd.sh; blk checks.sh; echo 'redaction_check "$HOME/run-agg-phase1/src_probe_rest_out.txt"'; } | bash
[ -n "${VPS_IP:-}" ] && [ -n "${VPS_HOST:-}" ] || { printf 'IP VPS (не отображается): '; read -rs VPS_IP; echo; printf 'hostname VPS (не отображается): '; read -rs VPS_HOST; echo; export VPS_IP VPS_HOST; }
{ blk rd.sh; blk checks.sh; echo 'owner_leak_check "$HOME/run-agg-phase1/src_probe_rest_out.txt"'; } | bash
```

Ожидаемо: `REDACTION OK` и `OWNER LEAK OK`.

Страницы GET.RUN не запрашиваются до ответа GET.RUN: getrun_tos = ask-first (§4.1), с VPS выполняются только `dns get.run` и robots.txt. Это выполняет блок `src_probe_getrun.sh`, он запускается ТРЕТЬИМ, после `src_probe_rest.sh` и только после завершения предыдущих скриптов.

```bash src_probe_getrun.sh
# запускать только по решению getrun_tos в §4.1 01-SOURCES-REPORT.md
GETRUN_ALLOW=robots
main() {
  sec "B. GET.RUN (get.run): ask-first, только robots.txt"
  dns get.run
  probe https://get.run/robots.txt 'Disallow' 'Sitemap'
  echo; echo "===== КОНЕЦ ====="
}
main </dev/null 2>&1 | rd
```

Здесь `GETRUN_ALLOW=robots` задаётся единственный раз в этом блоке; `probe_lib.sh` без этой переменной отказывает на любом обращении к этому сайту. Запрос разрешения у GET.RUN (через t.me/agetrun) отправляет владелец сам.

#### Перед запуском (обязательно)

`safety_lint` не заменяет ручную проверку владельца. Перед запуском на VPS:

1. Откройте `01-COMMANDS.md`.
2. Прочитайте ПОЛНОСТЬЮ все блоки, которые уйдут на VPS: `blk rd.sh`, `blk probe_lib.sh`, `blk src_probe_getrun.sh` (выведите их в терминал и прочитайте).
3. Запустите проверку ниже (она сама записывает блоки в файлы `rd.sh`, `probe_lib.sh`, `src_probe_getrun.sh` во временном каталоге и вызывает `safety_lint`, `getrun_gate` и `getrun_guard_test` по этим именам; подстановку процесса `<(blk …)` не использовать) и прочитайте её вывод целиком.
4. Убедитесь, что блок делает ровно два обращения к этому сайту: `dns` имени и один GET `robots.txt`; других URL нет.
5. Только после этого самостоятельно запустите `ssh`.
6. Если что-то непонятно, не запускайте и спросите.

```bash
X=$(mktemp -d); for b in rd.sh probe_lib.sh src_probe_getrun.sh; do blk "$b" > "$X/$b"; done
{ blk rd.sh; blk checks.sh; echo "safety_lint $X/rd.sh $X/probe_lib.sh $X/src_probe_getrun.sh; getrun_gate .planning/phases/01-source-discovery-vps-audit/01-SOURCES-REPORT.md $X/rd.sh $X/probe_lib.sh $X/src_probe_getrun.sh; getrun_guard_test $X/probe_lib.sh"; } | bash
```

Ожидаемо: `SAFETY OK`, `GETRUN GATE OK`, `GUARD OK`.

Команда запуска (`OWNER_CONTACT` и `VPS_ALIAS` уже заданы в этом терминале; иначе введите их как выше):

```bash
{ blk rd.sh; blk probe_lib.sh; blk src_probe_getrun.sh; } \
  | sed "s|<OWNER_CONTACT>|${OWNER_CONTACT:?задайте OWNER_CONTACT}|g" \
  | ssh "${VPS_ALIAS:?задайте ssh-алиас}" 'bash -s' \
  | tee "$HOME/run-agg-phase1/src_probe_getrun_out.txt"
```

Проверка вывода:

```bash
{ blk rd.sh; blk checks.sh; echo 'redaction_check "$HOME/run-agg-phase1/src_probe_getrun_out.txt"'; } | bash
{ blk rd.sh; blk checks.sh; echo 'owner_leak_check "$HOME/run-agg-phase1/src_probe_getrun_out.txt"'; } | bash
```

Ожидаемо: `REDACTION OK` и `OWNER LEAK OK`.
