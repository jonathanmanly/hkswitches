#!/bin/sh
# Installs hkswitches as a service that starts on boot. Run with: sudo ./install.sh
set -e
cd "$(dirname "$0")"
if [ "$(id -u)" -ne 0 ]; then echo "Run with sudo: sudo ./install.sh"; exit 1; fi

if systemctl is-active --quiet homebridge 2>/dev/null; then
  echo "Note: a Homebridge service is still running. Stop it with:"
  echo "  sudo systemctl disable --now homebridge"
fi

install -m 755 hkswitches /usr/local/bin/hkswitches
install -m 644 hkswitches.service /etc/systemd/system/hkswitches.service
systemctl daemon-reload
systemctl enable --now hkswitches
sleep 3

IP=$(hostname -I | awk '{print $1}')
echo
echo "hkswitches is running."
echo "Open http://$IP:8080/ (or http://$(hostname).local:8080/) to see the pairing code and manage switches."
echo "Config file: /var/lib/hkswitches/config.json   Logs: journalctl -u hkswitches -f"
