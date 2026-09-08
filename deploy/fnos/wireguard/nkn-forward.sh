#!/bin/bash
# nkn-forward.sh - expose the built-in NKN node's JSON-RPC (port 30003, TCP)
# inside the api container to WireGuard peers on the NAS.
#
# Why: the nknd subprocess runs inside nas-comm-api with no host port mapping
# (NAT'd node, only serves local clients). Phones connected to the WireGuard
# tunnel (wg0, 10.66.0.0/24) reach it at http://10.66.0.1:30003 via DNAT.
# Forwarded traffic is accepted through DOCKER-USER (docker's own hook chain)
# so docker's FORWARD DROP policy never blocks it.
#
# Usage (root on the NAS):
#   nkn-forward.sh [up|remove|status]
#     up      (default) resolve the api container IP and (re)install the rules
#     remove  delete the rules (tunnel down / cleanup)
#     status  print the current rules
#
# Idempotent: rules are tagged with the comment "modemdeck-nkn-rpc" and
# re-installed from scratch on every run, so re-running after a container
# rebuild (new IP) is safe. wg-ctl.sh up calls this automatically.

set -euo pipefail

IFACE=${WG_IFACE:-wg0}
API_CONTAINER=${NKN_API_CONTAINER:-nas-comm-api}
PORT=${NKN_RPC_PORT:-30003}
TAG="modemdeck-nkn-rpc"

if [ "$(id -u)" != "0" ]; then
  echo "[nkn-fwd] must run as root (sudo)" >&2
  exit 1
fi

user_chain() {
  # docker's DOCKER-USER chain exists whenever docker is running; fall back
  # to FORWARD for unusual setups.
  if iptables -nL DOCKER-USER >/dev/null 2>&1; then
    echo "DOCKER-USER"
  else
    echo "FORWARD"
  fi
}

remove_rules() {
  local table
  for table in "nat PREROUTING" "nat POSTROUTING"; do
    set -- $table
    iptables -t "$1" -S "$2" 2>/dev/null | grep -F -- "$TAG" | while IFS= read -r spec; do
      # spec: "-A PREROUTING ..." -> delete the same rule
      iptables -t "$1" "${spec/#-A /-D }" 2>/dev/null || true
    done
  done
  local chain
  chain=$(user_chain)
  iptables -S "$chain" 2>/dev/null | grep -F -- "$TAG" | while IFS= read -r spec; do
    iptables "${spec/#-A /-D }" 2>/dev/null || true
  done
}

show_rules() {
  echo "# nat/PREROUTING"
  iptables -t nat -S PREROUTING 2>/dev/null | grep -F -- "$TAG" || echo "(none)"
  echo "# nat/POSTROUTING"
  iptables -t nat -S POSTROUTING 2>/dev/null | grep -F -- "$TAG" || echo "(none)"
  echo "# filter/$(user_chain)"
  iptables -S "$(user_chain)" 2>/dev/null | grep -F -- "$TAG" || echo "(none)"
}

container_ip() {
  docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$API_CONTAINER" 2>/dev/null | head -n1
}

install_rules() {
  local cip
  cip=$(container_ip)
  if [ -z "$cip" ]; then
    echo "[nkn-fwd] container $API_CONTAINER not found/running; rules NOT installed" >&2
    exit 1
  fi

  # Start clean so a rebuilt container (new IP) never leaves stale rules.
  remove_rules

  # 1. DNAT: packets arriving on wg0, tcp/30003 -> api container.
  iptables -t nat -A PREROUTING -i "$IFACE" -p tcp --dport "$PORT" \
    -m comment --comment "$TAG" -j DNAT --to-destination "$cip:$PORT"

  # 2. Let the forwarded NEW connection through (DOCKER-USER is traversed
  #    before docker's DROP; insert at top so the default RETURN can't
  #    short-circuit an appended rule).
  iptables -I "$(user_chain)" 1 -i "$IFACE" -d "$cip" -p tcp --dport "$PORT" \
    -m comment --comment "$TAG" -j ACCEPT

  # 3. Explicit accept for the reply path (container -> wg peer).
  iptables -I "$(user_chain)" 1 -i docker0 -o "$IFACE" -s "$cip" -p tcp --sport "$PORT" \
    -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment "$TAG" -j ACCEPT

  # 4. MASQUERADE toward the container so replies always come back through
  #    the host (conntrack finishes the translation; survives container
  #    routing quirks).
  iptables -t nat -A POSTROUTING -o docker0 -d "$cip" -p tcp --dport "$PORT" \
    -m comment --comment "$TAG" -j MASQUERADE

  echo "[nkn-fwd] wg0 tcp/$PORT -> $cip:$PORT ($API_CONTAINER)"
  echo "[nkn-fwd] phones on the tunnel can use: http://10.66.0.1:$PORT"
}

case "${1:-up}" in
  up)     install_rules ;;
  remove) remove_rules; echo "[nkn-fwd] rules removed" ;;
  status) show_rules ;;
  *)
    echo "usage: $0 {up|remove|status}" >&2
    exit 2
    ;;
esac
