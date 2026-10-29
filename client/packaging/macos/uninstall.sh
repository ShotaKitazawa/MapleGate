#!/bin/sh
# Removes the client. Run with sudo. A tunnel in use is taken down by stopping the daemon.
set -eu
USER_NAME=$(stat -f %Su /dev/console)
USER_ID=$(id -u "$USER_NAME")
launchctl bootout "gui/$USER_ID" /Library/LaunchAgents/com.maplegate.gamevpn.plist 2>/dev/null || true
launchctl bootout system /Library/LaunchDaemons/com.maplegate.gamevpnd.plist 2>/dev/null || true
rm -f /Library/LaunchAgents/com.maplegate.gamevpn.plist /Library/LaunchDaemons/com.maplegate.gamevpnd.plist
rm -f /usr/local/bin/gamevpn /usr/local/bin/gamevpnd /var/run/gamevpnd.sock /var/run/gamevpn-tunnel.json
rm -rf /usr/local/share/gamevpn
pkgutil --forget com.maplegate.gamevpn >/dev/null 2>&1 || true
echo "Removed. Your sign in and settings stay in ~/Library/Application Support/gamevpn; delete that folder to remove them too."
