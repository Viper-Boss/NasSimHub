#!/bin/bash
# wg-up.sh / wg-down.sh - bring the WireGuard server up or down on the NAS.
# wg-up.sh enables IP forwarding and (best effort) NAT so phones can reach
# the LAN and internet through the NAS. Not wired into compose on purpose:
# GPT6's UI integration will manage it later.
set -euo pipefail

IFACE=${WG_IFACE:-wg0}

case "${1:-up}" in
  up)
    # Kernel module (no-op if already loaded)
    modprobe wireguard 2>/dev/null || true

    if [ ! -f /etc/wireguard/$IFACE.conf ]; then
      echo "[wg] /etc/wireguard/$IFACE.conf missing; create it from wg0.conf.tmpl first" >&2
      exit 1
    fi

    # IP forwarding
    if [ "$(cat /proc/sys/net/ipv4/ip_forward)" != "1" ]; then
      echo 1 > /proc/sys/net/ipv4/ip_forward
      echo "[wg] ip_forward enabled (runtime)"
    fi

    wg-quick up $IFACE
    echo "[wg] $IFACE up: $(wg show $IFACE listen-port)"

    # Best effort: expose the built-in NKN node RPC (tcp/30003) to tunnel
    # peers. Failure never blocks the tunnel itself.
    NKN_FWD="$(cd "$(dirname "$0")" && pwd)/nkn-forward.sh"
    if [ -f "$NKN_FWD" ]; then
      sudo bash "$NKN_FWD" up || echo "[wg] nkn-forward failed (built-in node RPC not exposed)" >&2
    fi
    ;;
  down)
    NKN_FWD="$(cd "$(dirname "$0")" && pwd)/nkn-forward.sh"
    if [ -f "$NKN_FWD" ]; then
      sudo bash "$NKN_FWD" remove || true
    fi
    wg-quick down $IFACE
    echo "[wg] $IFACE down"
    ;;
  status)
    wg show $IFACE 2>/dev/null || echo "[wg] $IFACE not up"
    ;;
  *)
    echo "usage: $0 {up|down|status}" >&2
    exit 2
    ;;
esac
