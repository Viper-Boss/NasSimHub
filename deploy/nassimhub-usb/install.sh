#!/bin/sh
# Install (or remove) the NAS side of the NasSimHub stick's USB link.
#
#   sudo sh install.sh            install and apply to a stick already plugged in
#   sudo sh install.sh status     show which interface carries the link
#   sudo sh install.sh uninstall  remove everything this installed
#
# Touches only: /usr/local/libexec/nassimhub-usb-link,
# /etc/udev/rules.d/90-nassimhub-usb.rules and
# /etc/systemd/system/nassimhub-usb-link@.service.
set -eu

here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
script=/usr/local/libexec/nassimhub-usb-link
rules=/etc/udev/rules.d/90-nassimhub-usb.rules
unit=/etc/systemd/system/nassimhub-usb-link@.service

need_root() {
    [ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)" >&2; exit 1; }
}

# stick_interfaces: network interfaces whose USB device is a NasSimHub stick.
stick_interfaces() {
    for path in /sys/class/net/*; do
        [ -e "$path/device" ] || continue
        usb=$(readlink -f "$path/device/..")
        if [ "$(cat "$usb/idVendor" 2>/dev/null)" = 1d6b ] &&
            [ "$(cat "$usb/idProduct" 2>/dev/null)" = 0104 ] &&
            [ "$(cat "$usb/manufacturer" 2>/dev/null)" = NasSimHub ]; then
            basename "$path"
        fi
    done
}

case "${1:-install}" in
install)
    need_root
    install -d -m 0755 /usr/local/libexec
    install -m 0755 "$here/nassimhub-usb-link" "$script"
    install -m 0644 "$here/90-nassimhub-usb.rules" "$rules"
    install -m 0644 "$here/nassimhub-usb-link@.service" "$unit"
    systemctl daemon-reload
    udevadm control --reload
    # Apply to a stick that is already plugged in.
    udevadm trigger --action=add --subsystem-match=net
    udevadm settle || true
    for iface in $(stick_interfaces); do
        systemctl restart "nassimhub-usb-link@$iface.service" || true
    done
    echo "installed"
    sh "$here/install.sh" status || true
    ;;
status)
    found=0
    for iface in $(stick_interfaces); do
        found=1
        state=$(systemctl is-active "nassimhub-usb-link@$iface.service" 2>/dev/null || true)
        printf '%s: unit %s; ' "$iface" "${state:-unknown}"
        "$script" status "$iface" || true
    done
    [ "$found" -eq 1 ] || { echo "no NasSimHub stick is plugged in over USB"; exit 1; }
    ;;
uninstall)
    need_root
    for iface in $(stick_interfaces); do
        systemctl stop "nassimhub-usb-link@$iface.service" || true
    done
    rm -f "$rules" "$unit" "$script"
    systemctl daemon-reload
    udevadm control --reload
    echo "removed"
    ;;
*)
    echo "usage: $0 [install|status|uninstall]" >&2
    exit 2
    ;;
esac
