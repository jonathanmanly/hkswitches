#!/bin/sh
# Installs or upgrades hkswitches as a service that starts on boot.
# Run with: sudo ./install.sh   (deploy.sh runs this on the Pi for you)
#
# Safe to re-run. It never creates, changes or deletes anything in
# /var/lib/hkswitches (config, switch states, HomeKit pairing); it only
# reads config.json there to back it up to /var/backups/hkswitches.
set -e
cd "$(dirname "$0")"
if [ "$(id -u)" -ne 0 ]; then echo "Run with sudo: sudo ./install.sh"; exit 1; fi

DATA=/var/lib/hkswitches
BACKUPS=/var/backups/hkswitches
UNIT=/etc/systemd/system/hkswitches.service
STAMP=$(date +%Y%m%d-%H%M%S)

if systemctl is-active --quiet homebridge 2>/dev/null; then
  echo "Note: a Homebridge service is still running. Stop it with:"
  echo "  sudo systemctl disable --now homebridge"
fi

if [ -f "$DATA/config.json" ]; then
  install -d -m 700 "$BACKUPS"
  cp -p "$DATA/config.json" "$BACKUPS/config.json.$STAMP"
  echo "Backed up config to $BACKUPS/config.json.$STAMP"
fi

if [ -x /usr/local/bin/hkswitches ]; then
  echo "Upgrading $(/usr/local/bin/hkswitches -version 2>/dev/null || echo 'old version') -> $(./hkswitches -version)"
fi
install -m 755 hkswitches /usr/local/bin/hkswitches

if [ ! -f "$UNIT" ]; then
  install -m 644 hkswitches.service "$UNIT"
elif ! cmp -s hkswitches.service "$UNIT"; then
  install -d -m 700 "$BACKUPS"
  cp -p "$UNIT" "$BACKUPS/hkswitches.service.$STAMP"
  install -m 644 hkswitches.service "$UNIT"
  echo "Updated $UNIT (old one saved in $BACKUPS)"
fi
systemctl daemon-reload
systemctl enable --quiet hkswitches
systemctl restart hkswitches
sleep 3

echo
journalctl -u hkswitches -n 10 --no-pager || true
echo
/usr/local/bin/hkswitches -version
if ! systemctl is-active --quiet hkswitches; then
  echo "hkswitches is NOT running. See: journalctl -u hkswitches -n 50"
  exit 1
fi

IP=$(hostname -I | awk '{print $1}')
echo "hkswitches is running."
echo "Open http://$IP:8080/ (or http://$(hostname).local:8080/) to see the pairing code and manage switches."
echo "Config file: $DATA/config.json   Logs: journalctl -u hkswitches -f"
