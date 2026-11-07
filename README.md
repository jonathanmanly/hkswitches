# hkswitches: HomeKit dummy switches for the Pi Zero W

A single small program that puts a HomeKit bridge full of dummy switches on your network.
It doesn't need Node, Homebridge or any other runtime, and it runs on the original Pi Zero W (ARMv6).
You manage it from a web page or by editing a config file.

## Install

1. Copy the folder to the Pi (from your computer):
   ```
   scp -r hkswitches pi@<pi-address>:~/
   ```
   Replace `pi` with your username on the Pi.
2. On the Pi:
   ```
   cd ~/hkswitches
   sudo ./install.sh
   ```
3. Open the address the installer prints (e.g. `http://192.168.1.50:8080/`) in a browser.
4. In the Home app tap **+**, then **Add Accessory**, and scan the QR code on that page. Or tap **More options** and type the 8‑digit code.
   Tap **Add Anyway** when it warns that the accessory is uncertified.

If the old Homebridge is still installed, stop it first with `sudo systemctl disable --now homebridge`.

## Switch settings

| Setting | What it does |
|---|---|
| **Name** | Name the switch gets when it first appears in the Home app |
| **Turn back after (seconds)** | `0` = stays where you put it. Any other number = flips back to its resting state after that many seconds (decimals OK). Use it to trigger an automation with a short pulse. |
| **Reverse** | The resting state becomes **on** instead of off |
| **Remember on/off** | Keeps the state across reboots. When off, the switch starts in its resting state. |

Each switch has a fixed ID that is never reused, so renaming or deleting other switches won't break your automations.
Renaming a switch here doesn't rename it in the Home app if it's already there. Rename it in the Home app too.

## Config file

`/var/lib/hkswitches/config.json` holds the same settings as the web page. After editing it, run:
```
sudo nano /var/lib/hkswitches/config.json
sudo systemctl reload hkswitches
```
To add a switch by hand, add an entry with no `id`. One is assigned automatically:
```json
{ "name": "Movie Mode", "auto_off_seconds": 0, "reverse": false, "remember_state": true }
```
Other settings in the file:
- `web_password`: when set, the web page asks for a password. Leave the username blank or type anything.
- `web_port`: the port for the web page.
- `hap_port`: the port HomeKit uses.
- `pin`: the 8‑digit pairing code.
Changes to these settings take effect after `sudo systemctl restart hkswitches`.

## Scripting

```
curl -X POST http://<pi>:8080/api/switches/2/on      # also /off and /toggle
curl http://<pi>:8080/api/status
```

## Troubleshooting

- **Logs:** `journalctl -u hkswitches -f`
- **Start pairing over** (for example, after removing the bridge from the Home app):
  ```
  sudo systemctl stop hkswitches
  sudo rm -rf /var/lib/hkswitches/homekit
  sudo systemctl start hkswitches
  ```
  Your switch list and settings are kept.
- **Uninstall:**
  ```
  sudo systemctl disable --now hkswitches
  sudo rm /usr/local/bin/hkswitches /etc/systemd/system/hkswitches.service
  ```
