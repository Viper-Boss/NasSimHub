#!/bin/sh
# Look for a second hardware identifier on an RK3566 board.
#
# Why this exists. The first probe established that the eMMC CID is the only
# burned-in identifier this board exposes, which has one consequence worth
# spending ten minutes to avoid: replacing the storage chip replaces the
# machine's identity, and nothing left in the fingerprint can tell a repaired
# board from a stranger's. If ANY of the values below turn out to be present
# and stable, that stops being true -- a second identifying source lets the
# software say "the chip was replaced, this is the same board" instead of
# shrugging, and a repair stops being a support argument.
#
# Run it on the NAS host, not inside a container:
#   sh nas-second-identifier-probe.sh
#
# Then run it AGAIN after a reboot and diff the two outputs. A value that
# changes between boots is worse than useless as an identity, and the only way
# to find that out is to look twice.

echo "=== kernel / board ==="
uname -srm
cat /sys/firmware/devicetree/base/compatible 2>/dev/null | tr '\0' ' '; echo

echo
echo "=== Rockchip OTP / eFuse (the value this probe is really for) ==="
# The RK3566 has a one-time-programmable area holding a chip ID written at
# manufacture. Where it surfaces depends on the kernel's nvmem bindings, so
# every plausible path is tried rather than guessing one.
for path in \
    /sys/bus/nvmem/devices/rockchip-otp0/nvmem \
    /sys/bus/nvmem/devices/rockchip-efuse0/nvmem \
    /sys/devices/platform/fe38c000.otp/nvmem \
    /sys/devices/platform/*/rockchip-otp*/nvmem
do
    if [ -r "$path" ]; then
        printf '%-58s ' "$path"
        # Hex, because this is binary and most of it is likely zeros. The chip
        # ID is usually a 16-byte run somewhere in the first 64 bytes.
        head -c 64 "$path" 2>/dev/null | od -An -tx1 | tr -d ' \n'
        echo
    else
        printf '%-58s (absent or unreadable)\n' "$path"
    fi
done

echo
echo "=== cpuinfo serial (some ARM kernels expose one here) ==="
grep -i '^serial' /proc/cpuinfo 2>/dev/null || echo "(no Serial line)"

echo
echo "=== other nvmem devices present ==="
ls -1 /sys/bus/nvmem/devices/ 2>/dev/null || echo "(no nvmem devices)"

echo
echo "=== confirming what the first probe found, so this output stands alone ==="
printf '%-58s ' "/sys/block/mmcblk0/device/cid"
cat /sys/block/mmcblk0/device/cid 2>/dev/null || echo "(absent)"
for iface in /sys/class/net/*; do
    name=$(basename "$iface")
    [ "$name" = "lo" ] && continue
    printf '%-20s mac=%-18s assign_type=%s\n' \
        "$name" \
        "$(cat "$iface/address" 2>/dev/null)" \
        "$(cat "$iface/addr_assign_type" 2>/dev/null)"
done

echo
echo "Done. If an OTP path printed a long non-zero hex string, run this again"
echo "after a reboot and confirm it is identical. If it is, say so -- it"
echo "changes the licensing design for the better."
