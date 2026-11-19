#!/bin/sh
# Sets up (or updates) Shiron on a Linux server, as root:  sudo sh setup.sh
# Put next to this script: shiron-linux-arm64 or shiron-linux-amd64, and optionally
# shiron.conf and shiron.db (to move your existing settings).
# Safe to run again: it only updates the program and keeps your data and token.
set -eu

DIR=/opt/shiron
cd "$(dirname "$0")"

case "$(uname -m)" in
  aarch64|arm64) BIN=shiron-linux-arm64 ;;
  x86_64)        BIN=shiron-linux-amd64 ;;
  *) echo "Unsupported CPU: $(uname -m)"; exit 1 ;;
esac
[ -f "$BIN" ] || { echo "Missing $BIN next to this script."; exit 1; }

# sqlite3 is only for the daily backup.
if ! command -v sqlite3 >/dev/null; then
  if command -v apt-get >/dev/null; then apt-get update -qq && apt-get install -y -qq sqlite3
  elif command -v dnf >/dev/null; then dnf install -y -q sqlite
  fi
fi

id shiron >/dev/null 2>&1 || useradd --system --home "$DIR" --shell /usr/sbin/nologin shiron
mkdir -p "$DIR/backups"

systemctl stop shiron 2>/dev/null || true
install -m 755 "$BIN" "$DIR/shiron"
[ -f shiron.conf ] && install -m 644 shiron.conf "$DIR/shiron.conf"
if [ -f shiron.db ]; then
  if [ -f "$DIR/shiron.db" ]; then
    echo "Keeping the database already on this server (delete $DIR/shiron.db first to replace it)."
  else
    install -m 600 shiron.db "$DIR/shiron.db"
  fi
fi

# The token is typed here, on the server, so it never travels in a command or a file copy.
if [ ! -s "$DIR/token.txt" ]; then
  printf "Paste the bot token (hidden), then Enter: "
  stty -echo; read -r TOKEN; stty echo; echo
  [ -n "$TOKEN" ] || { echo "No token given."; exit 1; }
  (umask 077; printf '%s\n' "$TOKEN" > "$DIR/token.txt")
  unset TOKEN
fi
chown -R shiron: "$DIR"
chmod 600 "$DIR/token.txt"

cat > /etc/systemd/system/shiron.service <<EOF
[Unit]
Description=Shiron Discord bot
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$DIR/shiron
WorkingDirectory=$DIR
User=shiron
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$DIR

[Install]
WantedBy=multi-user.target
EOF

# Daily database backup, keeping the last 14 days.
cat > /etc/systemd/system/shiron-backup.service <<EOF
[Unit]
Description=Back up Shiron's database

[Service]
Type=oneshot
User=shiron
ExecStart=/bin/sh -c 'sqlite3 $DIR/shiron.db ".backup $DIR/backups/shiron-\$(date +%%F).db" && find $DIR/backups -name "shiron-*.db" -mtime +14 -delete'
EOF
cat > /etc/systemd/system/shiron-backup.timer <<EOF
[Unit]
Description=Daily Shiron database backup

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now shiron shiron-backup.timer
sleep 3
systemctl --no-pager --lines=15 status shiron || true
echo
echo "Done. Live log: sudo journalctl -u shiron -f"
