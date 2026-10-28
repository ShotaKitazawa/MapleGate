#!/bin/bash
# Runs on every boot and must be idempotent: the node is stopped and started, never rebuilt.
set -euo pipefail

# shellcheck source=/dev/null
. /etc/gamevpn/env

D=/opt/gamevpn
mkdir -p "$D" /var/lib/gamevpn /run/gamevpn /etc/wireguard

# t4g.nano has 512MB and dnf is OOM-killed without swap
if ! swapon --show | grep -q .; then
  [ -f /swapfile ] || { fallocate -l 1G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null; }
  swapon /swapfile
fi

need=()
command -v wg >/dev/null || need+=(wireguard-tools)
command -v nft >/dev/null || need+=(nftables)
if [ ${#need[@]} -gt 0 ]; then
  for _ in 1 2 3 4 5; do
    dnf install -y "${need[@]}" && break
    sleep 10
  done
fi

[ -s /etc/wireguard/server.key ] || (umask 077 && wg genkey >/etc/wireguard/server.key)

if [ ! -f /etc/wireguard/wg0.conf ]; then
  (
    umask 077
    cat >/etc/wireguard/wg0.conf <<CONF
[Interface]
Address = ${WG_ADDRESS}
ListenPort = ${WG_PORT}
PrivateKey = $(cat /etc/wireguard/server.key)
CONF
  )
fi

echo 'net.ipv4.ip_forward=1' >/etc/sysctl.d/99-gamevpn.conf
sysctl --system >/dev/null

install -m0644 "$D/node/nftables.conf" /etc/sysconfig/nftables.conf
nft delete table inet gamevpn 2>/dev/null || true
nft delete table ip gamevpn_nat 2>/dev/null || true
nft -f /etc/sysconfig/nftables.conf

systemctl enable --now wg-quick@wg0

# Best effort: the session API only reads this value, so a failed write leaves the previous one in place.
aws ssm put-parameter --region "$AWS_REGION" --name "$PUBKEY_PARAM" --type String --overwrite \
  --value "$(wg pubkey </etc/wireguard/server.key)" >/dev/null || echo "could not publish the server public key" >&2

if aws s3 cp "s3://${BUCKET}/agent/vpn-agent" "$D/agent.fetch" --only-show-errors; then
  if cmp -s "$D/agent.fetch" "$D/agent.good" 2>/dev/null; then
    rm -f "$D/agent.fetch"
  else
    chmod 0755 "$D/agent.fetch"
    mv -f "$D/agent.fetch" "$D/agent.new"
  fi
else
  echo "agent fetch failed, keeping the last known good binary" >&2
fi

install -m0755 "$D/node/agent-run.sh" "$D/agent-run.sh"
install -m0755 "$D/node/agent-promote.sh" "$D/agent-promote.sh"
install -m0644 "$D"/node/units/* /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now vpn-agent.service vpn-max-uptime.timer vpn-agent-promote.timer
