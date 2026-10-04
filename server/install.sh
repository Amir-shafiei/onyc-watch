#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "$0")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
cd -- "$script_dir"
if [ "$(id -u)" != 0 ]; then echo "Run this installer as root."; exit 1; fi
case "$(uname -m)" in
 x86_64) binary=onyc-watch-linux-amd64 ;;
 aarch64|arm64) binary=onyc-watch-linux-arm64 ;;
 *) echo "Unsupported CPU architecture"; exit 1 ;;
esac
command -v python3 >/dev/null || { echo "Install python3 first: apt-get update && apt-get install -y python3"; exit 1; }
if [ ! -f "$binary" ]; then
 command -v go >/dev/null || { echo "No release binary or Go compiler found. Install Go 1.24+ first."; exit 1; }
 echo "No packaged binary found; building from the cloned source..."
 (cd -- "$repo_root" && go build -trimpath -o "$script_dir/$binary" .)
fi
# Never create a second deployment over an existing service or data directory.
if [ -e /etc/systemd/system/onyc-watch.service ] || [ -e /etc/onyc-watch/bot.env ] || [ -e /var/lib/onyc-watch/state.json ]; then
 echo "Existing installation detected. Stop here to preserve its settings and data."; exit 1
fi
id onyc-watch >/dev/null 2>&1 || useradd --system --user-group --home-dir /var/lib/onyc-watch --shell /usr/sbin/nologin onyc-watch
install -d -m 0755 /opt/onyc-watch
install -d -m 0700 /etc/onyc-watch
install -d -m 0700 -o onyc-watch -g onyc-watch /var/lib/onyc-watch
install -m 0755 "$binary" /opt/onyc-watch/onyc-watch
# Parse only known settings; never carry the Windows localhost proxy to Ubuntu.
python3 - <<'PY'
from pathlib import Path
import json,re,os
source=Path('upload.env')
if not source.is_file():raise SystemExit('Missing uploaded configuration')
allowed={'TELEGRAM_BOT_TOKEN','ALLOWED_USER_IDS','POLL_INTERVAL','STALE_AFTER','ALERT_COOLDOWN'}
settings={}
for line in source.read_text(encoding='utf-8-sig').splitlines():
 line=line.strip()
 if not line or line.startswith('#'):continue
 key,sep,value=line.partition('=')
 if key.strip() in allowed and sep:settings[key.strip()]=value.strip().strip(chr(34)+chr(39))
if not re.fullmatch(r'[0-9]+:[A-Za-z0-9_-]+',settings.get('TELEGRAM_BOT_TOKEN','')):raise SystemExit('Missing or invalid bot token')
for key,value in settings.items():
 if not re.fullmatch(r'[A-Za-z0-9_:,. -]+',value) and value!='':raise SystemExit('Invalid configuration value for '+key)
settings['DATA_DIR']='/var/lib/onyc-watch'
Path('/etc/onyc-watch/bot.env').write_text(''.join(k+'='+v+'\n' for k,v in settings.items()),encoding='utf8')
os.chmod('/etc/onyc-watch/bot.env',0o600)
state=Path('upload-state.json')
if state.exists():
 data=json.loads(state.read_text(encoding='utf8'))
 if data.get('Version')!=1 or not isinstance(data.get('Users'),dict):raise SystemExit('Invalid state upload')
 Path('/var/lib/onyc-watch/state.json').write_bytes(state.read_bytes())
 os.chmod('/var/lib/onyc-watch/state.json',0o600)
PY
chown -R onyc-watch:onyc-watch /var/lib/onyc-watch
install -m 0644 onyc-watch.service /etc/systemd/system/onyc-watch.service
systemctl daemon-reload
systemctl enable --now onyc-watch.service
sleep 5
if ! systemctl is-active --quiet onyc-watch.service; then
 echo "Service did not stay running. Inspect: journalctl -u onyc-watch -n 40 --no-pager"; exit 1
fi
rm -f -- upload.env upload-state.json
echo "ONyc Watch installed and running. Keep the Windows instance stopped."
echo "Logs: journalctl -u onyc-watch -f"
