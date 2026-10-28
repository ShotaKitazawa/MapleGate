#!/bin/bash
set -euo pipefail
D=/opt/gamevpn

[ -f "$D/agent.new" ] || exit 0
if [ -f /run/gamevpn/healthy ]; then
  mv -f "$D/agent.new" "$D/agent.good"
else
  rm -f "$D/agent.new"
  systemctl restart vpn-agent.service
fi
