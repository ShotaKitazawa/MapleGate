#!/bin/sh
# Removes the client. Run with sudo. A tunnel in use is taken down by stopping the daemon.
set -eu
USER_NAME=$(stat -f %Su /dev/console)
USER_ID=$(id -u "$USER_NAME")
# at the login window the console owner is root, and there is no tray to stop
[ "$USER_NAME" = root ] || launchctl bootout "gui/$USER_ID" /Library/LaunchAgents/com.maplegate.gamevpn.plist 2>/dev/null || true
launchctl bootout system /Library/LaunchDaemons/com.maplegate.gamevpnd.plist 2>/dev/null || true
rm -f /Library/LaunchAgents/com.maplegate.gamevpn.plist /Library/LaunchDaemons/com.maplegate.gamevpnd.plist
rm -f /usr/local/bin/gamevpn /usr/local/bin/gamevpnd /var/run/gamevpnd.sock /var/run/gamevpn-tunnel.json
rm -f /etc/newsyslog.d/gamevpnd.conf /var/log/gamevpnd.log*
rm -rf /usr/local/share/gamevpn /Applications/MapleGate.app
pkgutil --forget com.maplegate.gamevpn >/dev/null 2>&1 || true
echo "Removed. Your sign in and settings stay in ~/Library/Application Support/gamevpn; delete that folder to remove them too."
