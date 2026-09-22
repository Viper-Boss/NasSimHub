#!/bin/sh
# Can the api container derive a machine identity on THIS machine?
#
# Run this on the fnOS NAS. It needs no arguments and changes nothing.
#
# WHY IT EXISTS. internal/license/machineid.go was measured on a Rockchip
# RK3566 board, where the eMMC CID is world-readable, so the api container can
# read it even though it runs as an unprivileged user. An x86 NAS has no eMMC
# CID: its only deriving sources are two DMI files, and the kernel publishes
# those mode 0400 owned by root, precisely because they identify the machine.
#
# The api container runs as MODEMDECK_UID (10001 by default) with cap_drop:
# ALL. If it cannot read either DMI file then CollectFingerprint finds no
# deriving source and refuses, which means, on this machine:
#
#   * no machine licence can ever be bound -- a certificate would be issued
#     against an identity the gateway cannot reproduce;
#   * the clone detection added in r110 is inert, so a data directory copied
#     from another NAS is NOT caught.
#
# Both failures are silent. This script is how you find out before a customer
# does.
#
# It reports what the RUNTIME USER can read, not what root can read. Running
# the checks as root is what makes this look fine when it is not.

set -u

RUNTIME_UID="${MODEMDECK_UID:-10001}"

printf 'machine identity probe\n'
printf '  runtime user: uid %s\n' "$RUNTIME_UID"
printf '  kernel:       %s\n' "$(uname -srm)"
printf '\n'

# read_as prints whether a path is readable by the runtime user.
#
# Falls back to a root read only to say "root can, the gateway cannot", which
# is the single most useful line this script can print: it distinguishes
# "absent on this hardware" from "present but out of reach", and those have
# completely different remedies.
read_as() {
    path="$1"
    if [ ! -e "$path" ]; then
        printf '    %-46s absent\n' "$path"
        return 1
    fi
    mode="$(stat -c '%a %U' "$path" 2>/dev/null || echo '?')"
    if su -s /bin/sh -c "head -c 64 '$path' >/dev/null 2>&1" "#$RUNTIME_UID" 2>/dev/null; then
        value="$(su -s /bin/sh -c "head -c 64 '$path'" "#$RUNTIME_UID" 2>/dev/null | tr -d '\0\n')"
        printf '    %-46s READABLE  [%s]  %s\n' "$path" "$mode" "$value"
        return 0
    fi
    if head -c 1 "$path" >/dev/null 2>&1; then
        printf '    %-46s ROOT ONLY [%s]  <- present, gateway cannot read\n' "$path" "$mode"
        return 2
    fi
    printf '    %-46s unreadable [%s]\n' "$path" "$mode"
    return 2
}

deriving_found=0
blocked_found=0

note() {
    case "$1" in
        0) deriving_found=$((deriving_found + 1)) ;;
        2) blocked_found=$((blocked_found + 1)) ;;
    esac
}

printf '  sources that CAN derive an identity:\n'
read_as /sys/firmware/devicetree/base/serial-number; note $?
read_as /sys/devices/soc0/serial_number;             note $?
read_as /sys/class/dmi/id/product_uuid;              note $?
read_as /sys/class/dmi/id/board_serial;              note $?

# eMMC CID, whichever block device enumerated it.
for cid in /sys/block/*/device/cid; do
    [ -e "$cid" ] || continue
    read_as "$cid"; note $?
done

# The SoC serial from /proc/cpuinfo, which survives replacing the storage.
if grep -qs '^Serial' /proc/cpuinfo; then
    printf '    %-46s READABLE  [cpuinfo]\n' '/proc/cpuinfo Serial'
    deriving_found=$((deriving_found + 1))
else
    printf '    %-46s absent\n' '/proc/cpuinfo Serial'
fi

printf '\n  recorded but NEVER used to derive an identity:\n'
read_as /etc/machine-id >/dev/null 2>&1 && \
    printf '    %-46s (regenerated on reinstall)\n' '/etc/machine-id'
printf '    %-46s (never derives; see choosePrimary)\n' 'MAC addresses'

printf '\n'
if [ "$deriving_found" -gt 0 ]; then
    printf 'RESULT: this machine CAN derive an identity (%d usable source(s)).\n' "$deriving_found"
    printf '        Machine licences bind, and clone detection works.\n'
    exit 0
fi

printf 'RESULT: this machine CANNOT derive an identity.\n'
if [ "$blocked_found" -gt 0 ]; then
    printf '        %d source(s) exist but are root-only, so the api container\n' "$blocked_found"
    printf '        cannot read them. This is a PERMISSION problem, not a\n'
    printf '        hardware one -- the identity is there, out of reach.\n'
else
    printf '        No burned-in identifier is present at all.\n'
fi
printf '\n'
printf '        Consequences, both silent:\n'
printf '          * machine licences cannot bind on this hardware;\n'
printf '          * a data directory copied from another NAS is NOT detected.\n'
printf '\n'
printf '        Send this output back before shipping to this platform.\n'
exit 1
