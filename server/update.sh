#!/usr/bin/env bash
# Update an existing installation without replacing its token or preferences.
set -euo pipefail
export PATH="$PATH:/snap/bin"
umask 077
cd -- "$(dirname -- "$0")/.."
if [ "$(id -u)" != 0 ]; then echo "Run as root."; exit 1; fi
for cmd in go systemctl flock; do
 command -v "$cmd" >/dev/null || { echo "Missing required command: $cmd"; exit 1; }
done
test -f /etc/onyc-watch/bot.env
test -x /opt/onyc-watch/onyc-watch
test -f /etc/systemd/system/onyc-watch.service
exec 9>/run/lock/onyc-watch-update.lock
flock -n 9 || { echo "Another update is already running."; exit 1; }
work=$(mktemp -d /opt/onyc-watch/.update-XXXXXX)
stopped=0
swapped=0
backup=""
cleanup() {
 result=$?
 trap - EXIT
 if [ "$result" != 0 ] && [ "$stopped" = 1 ]; then
  echo "Update failed; restoring the previous deployment."
  if [ "$swapped" = 1 ]; then
   systemctl stop onyc-watch
   install -m 0755 "$backup/onyc-watch" /opt/onyc-watch/onyc-watch
   if [ -f "$backup/state.json" ]; then
    install -o onyc-watch -g onyc-watch -m 0600 "$backup/state.json" /var/lib/onyc-watch/state.json
   else
    rm -f -- /var/lib/onyc-watch/state.json
   fi
  fi
  systemctl start onyc-watch || true
 fi
 rm -rf -- "$work"
 exit "$result"
}
trap cleanup EXIT
echo "Testing and building before stopping the running bot..."
go test ./...
go vet ./...
go build -trimpath -o "$work/onyc-watch" .
chmod 0755 "$work/onyc-watch"
"$work/onyc-watch" -h >/dev/null 2>&1
install -d -m 0700 /var/backups/onyc-watch
backup=$(mktemp -d "/var/backups/onyc-watch/$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")
cp -p /opt/onyc-watch/onyc-watch "$backup/onyc-watch"
cp -p /etc/onyc-watch/bot.env "$backup/bot.env"
cp -p /etc/systemd/system/onyc-watch.service "$backup/onyc-watch.service"
systemctl stop onyc-watch
stopped=1
if [ -f /var/lib/onyc-watch/state.json ]; then
 cp -p /var/lib/onyc-watch/state.json "$backup/state.json"
fi
mv -f -- "$work/onyc-watch" /opt/onyc-watch/onyc-watch
swapped=1
initial_restarts=$(systemctl show onyc-watch --property=NRestarts --value)
systemctl start onyc-watch
# A process being active once is insufficient: catch startup failures/restart loops.
for attempt in 1 2 3 4 5 6; do
 sleep 5
 systemctl is-active --quiet onyc-watch
done
restarts=$(systemctl show onyc-watch --property=NRestarts --value)
if [ "$restarts" != "$initial_restarts" ]; then echo "Service restarted during validation."; exit 1; fi
stopped=0
echo "ONyc Watch updated successfully."
echo "Backup: $backup"
echo "Token and saved user settings were preserved."
echo "Check Telegram /start, then Current AUM and My Alerts."
systemctl status onyc-watch --no-pager
