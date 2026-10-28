#!/bin/bash
# A candidate binary runs until agent-promote.sh judges it; otherwise the last good one does.
D=/opt/gamevpn
if [ -x "$D/agent.new" ]; then
  exec "$D/agent.new"
fi
exec "$D/agent.good"
