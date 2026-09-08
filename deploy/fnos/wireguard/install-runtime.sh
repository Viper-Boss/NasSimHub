#!/bin/sh
set -eu
test "$(id -u)" = 0
test "$(uname -m)" = aarch64
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ARCHIVE=${1:?Pass verified mtg-2.2.8-linux-arm64.tar.gz}
EXPECTED=562a94dd4cafcb8f179b76cfeafb76da12747c8e230bc76235bf8746cc189644
ACTUAL=$(sha256sum "$ARCHIVE" | cut -d ' ' -f 1)
test "$ACTUAL" = "$EXPECTED"
test -f "$HERE/runtime.py"
test -f "$HERE/homesim-network.service"
command -v wg >/dev/null
command -v iptables >/dev/null
python3 -m py_compile "$HERE/runtime.py"
BUILD=$(mktemp -d /mnt/docker-data/build/homesim-mtg-install.XXXXXX)
tar -xzf "$ARCHIVE" -C "$BUILD" mtg-2.2.8-linux-arm64/mtg mtg-2.2.8-linux-arm64/LICENSE
install -d -m 755 /usr/local/lib/homesim-network
install -d -m 700 /etc/homesim-wireguard
install -d -m 700 -o 10001 -g 10001 /mnt/docker-data/homesim/data/homesim/wireguard
if test -f /usr/local/bin/mtg; then cp -p /usr/local/bin/mtg "$BUILD/previous-mtg"; fi
if test -f /usr/local/lib/homesim-network/runtime.py; then cp -p /usr/local/lib/homesim-network/runtime.py "$BUILD/previous-runtime.py"; fi
if test -f /etc/systemd/system/homesim-network.service; then cp -p /etc/systemd/system/homesim-network.service "$BUILD/previous.service"; fi
install -m 755 "$BUILD/mtg-2.2.8-linux-arm64/mtg" /usr/local/bin/mtg
install -m 644 "$BUILD/mtg-2.2.8-linux-arm64/LICENSE" /usr/local/lib/homesim-network/MTG-LICENSE
install -m 644 "$HERE/runtime.py" /usr/local/lib/homesim-network/runtime.py
install -m 644 "$HERE/homesim-network.service" /etc/systemd/system/homesim-network.service
systemctl daemon-reload
systemctl enable --now homesim-network.service
systemctl is-active homesim-network.service
echo "Installed. Existing configuration retained; no ports are enabled by this installer. Backup: $BUILD"
