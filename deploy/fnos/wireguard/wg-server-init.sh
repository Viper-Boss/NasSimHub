#!/bin/bash
# wg-server-init.sh - one-shot WireGuard server bootstrap for the fnOS NAS.
# Generates server keypair + a pool of peer pre-shared keys under
# /etc/wireguard/keys and prints the server public key.
#
# Usage (on the NAS):  sudo bash wg-server-init.sh
# Idempotent: existing keys are kept.
set -euo pipefail

KEYDIR=/etc/wireguard/keys
mkdir -p "$KEYDIR" && chmod 700 "$KEYDIR"

if [ ! -f "$KEYDIR/server.key" ]; then
  wg genkey > "$KEYDIR/server.key"
  echo "[init] generated server private key"
fi
if [ ! -f "$KEYDIR/server.pub" ]; then
  wg pubkey < "$KEYDIR/server.key" > "$KEYDIR/server.pub"
fi

# Pre-generate PSKs for the first 8 phone peers (peer0..peer7).
for i in 0 1 2 3 4 5 6 7; do
  if [ ! -f "$KEYDIR/psk$i" ]; then
    wg genpsk > "$KEYDIR/psk$i"
  fi
done

chmod 600 "$KEYDIR"/*
echo "[init] server public key: $(cat "$KEYDIR/server.pub")"
echo "[init] PSK pool ready at $KEYDIR/psk0..psk7"
