#!/bin/sh
set -eu
wireguard-go wg0
ip addr add 10.66.0.1/24 dev wg0
printf '%s' "$SERVER_KEY" >/tmp/server.key
wg set wg0 private-key /tmp/server.key listen-port 51820
ip link set wg0 up
exec /opt/agent/vpn-agent
