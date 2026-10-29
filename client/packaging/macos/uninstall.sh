#!/bin/sh
# Removes the client. Run with sudo. A tunnel in use is taken down by stopping the daemon.
set -eu
USER_NAME=$(stat -f %Su /dev/console)
USER_ID=$(id -u "$USER_NAME")
# at the login window the console owner is root, and there is no tray to stop
[ "$USER_NAME" = root ] || launchctl bootout "gui/$USER_ID" /Library/LaunchAgents/com.maplegate.tray.plist 2>/dev/null || true
launchctl bootout system /Library/LaunchDaemons/com.maplegate.daemon.plist 2>/dev/null || true
rm -f /Library/LaunchAgents/com.maplegate.tray.plist /Library/LaunchDaemons/com.maplegate.daemon.plist
rm -f /usr/local/bin/maplegate /usr/local/bin/maplegated /var/run/maplegated.sock /var/run/maplegate-tunnel.json
rm -f /etc/newsyslog.d/maplegated.conf /var/log/maplegated.log*
rm -rf /usr/local/share/maplegate /Applications/MapleGate.app
pkgutil --forget com.maplegate.client >/dev/null 2>&1 || true
echo "Removed. Your sign in and settings stay in ~/Library/Application Support/MapleGate; delete that folder to remove them too."
