#!/bin/sh
# Can the api container derive a machine identity on THIS machine?
#
# Run it on the NAS. No arguments, changes nothing.
#
#   sh deploy/fnos/machine-id-probe.sh
#
# WHY IT EXISTS. internal/license/machineid.go was measured on one Rockchip
# RK3566 board. Other hardware exposes a different set of identifiers, and
# permissions differ between boards running the same SoC family. If the api
# container -- which runs as MODEMDECK_UID, default 10001, with cap_drop: ALL
# -- can read no deriving source, then on this machine:
#
#   * no machine licence can ever bind, because a certificate would name an
#     identity the gateway cannot reproduce;
#   * the clone detection in internal/provision is inert, so a data directory
#     copied from another NAS is not caught.
#
# Both failures are silent. This is how you find out before a customer does.
#
# ---------------------------------------------------------------------------
# WHAT THE FIRST VERSION OF THIS SCRIPT GOT WRONG, because the same mistake is
# easy to reintroduce and it produced a confident, wrong answer.
#
# It tested readability by running `su -s /bin/sh -c ... "#10001"`. `su` takes
# a USER NAME; the "#uid" form is sudo's, not su's. So the command failed on
# every machine, the script fell through to a root read, and reported
# "ROOT ONLY" for every file that existed -- including files that were mode
# 444 and perfectly readable by the runtime user. It printed the mode right
# beside that verdict, and the two contradicted each other.
#
# The damage was not the wrong line. It was that a 0400 root-only DMI file and
# a 0444 world-readable eMMC CID produced identical output, which is precisely
# the distinction the script exists to make.
#
# So readability is now COMPUTED from the permission bits, which needs no
# subprocess and cannot fail silently. A real attempt as the runtime user is
# made as well when a mechanism for it exists, and the method used is always
# named. A check that could not run says so; it never becomes a verdict.
# ---------------------------------------------------------------------------

set -u

RUNTIME_UID="${MODEMDECK_UID:-10001}"
RUNTIME_GID="${MODEMDECK_GID:-10001}"
AGENT_GID="${MODEMDECK_AGENT_GID:-10002}"
API_CONTAINER="${MODEMDECK_API_CONTAINER:-modemdeck-api}"

# ---------------------------------------------------------------------------
# Picking a way to test as the runtime user.
#
# Preference order is faithfulness, not convenience. Running inside the api
# container is the only method that also reproduces its mount namespace, so it
# is tried first; the rest are host-side approximations, which is fine because
# /sys and /proc are shared with the host.
# ---------------------------------------------------------------------------
ATTEMPT_METHOD="none"

if command -v docker >/dev/null 2>&1 &&
    docker exec -u "$RUNTIME_UID" "$API_CONTAINER" true >/dev/null 2>&1; then
    ATTEMPT_METHOD="docker"
elif command -v setpriv >/dev/null 2>&1 &&
    setpriv --reuid="$RUNTIME_UID" --regid="$RUNTIME_GID" --clear-groups true >/dev/null 2>&1; then
    ATTEMPT_METHOD="setpriv"
elif command -v sudo >/dev/null 2>&1 &&
    sudo -n -u "#$RUNTIME_UID" true >/dev/null 2>&1; then
    ATTEMPT_METHOD="sudo"
fi

# attempt_read returns 0 if the runtime user really could read the path.
# Returns 3 when there is no way to try, which is NOT a failure to read.
attempt_read() {
    case "$ATTEMPT_METHOD" in
        docker)  docker exec -u "$RUNTIME_UID" "$API_CONTAINER" \
                     head -c 1 "$1" >/dev/null 2>&1 ;;
        setpriv) setpriv --reuid="$RUNTIME_UID" --regid="$RUNTIME_GID" --clear-groups \
                     head -c 1 "$1" >/dev/null 2>&1 ;;
        sudo)    sudo -n -u "#$RUNTIME_UID" head -c 1 "$1" >/dev/null 2>&1 ;;
        *)       return 3 ;;
    esac
}

# computed_read decides from the permission bits alone.
#
# Also walks the parents: a mode 444 file under a mode 700 directory is not
# reachable, and checking only the file would call it readable.
computed_read() {
    path="$1"

    parent="$(dirname "$path")"
    while [ "$parent" != "/" ] && [ -n "$parent" ]; do
        pbits="$(stat -c '%a %u %g' "$parent" 2>/dev/null)" || return 1
        bits_allow "$pbits" 1 || return 1
        next="$(dirname "$parent")"
        [ "$next" = "$parent" ] && break
        parent="$next"
    done

    bits="$(stat -c '%a %u %g' "$path" 2>/dev/null)" || return 1
    bits_allow "$bits" 4
}

# bits_allow "<mode> <uid> <gid>" <wanted>  -- wanted is 4 (read) or 1 (execute).
#
# The group is treated as NOT ours unless it is one the api service is actually
# given, which is the conservative reading: guessing membership would turn a
# maybe into a yes, and a yes here means shipping.
bits_allow() {
    mode="$(echo "$1" | cut -d' ' -f1)"
    owner="$(echo "$1" | cut -d' ' -f2)"
    group="$(echo "$1" | cut -d' ' -f3)"
    wanted="$2"

    # Keep the low three digits; a setuid bit would make it four.
    while [ "${#mode}" -gt 3 ]; do mode="${mode#?}"; done
    while [ "${#mode}" -lt 3 ]; do mode="0${mode}"; done

    o="$(echo "$mode" | cut -c1)"
    g="$(echo "$mode" | cut -c2)"
    w="$(echo "$mode" | cut -c3)"

    if [ "$owner" = "$RUNTIME_UID" ]; then
        digit="$o"
    elif [ "$group" = "$RUNTIME_GID" ] || [ "$group" = "$AGENT_GID" ]; then
        digit="$g"
    else
        digit="$w"
    fi
    [ "$((digit & wanted))" -eq "$wanted" ]
}

deriving=0
blocked=0

# check <path>. $DERIVES says whether a readable value here would actually
# produce a machine ID, and only those are counted.
#
# The distinction is not cosmetic and the first version of this script did not
# make it: it counted /proc/cpuinfo Serial as a usable source and printed
# "CAN derive (1 usable source)" for a board whose only deriving source it had
# just reported as unreadable. choosePrimary refuses cpu-serial as an identity
# on purpose -- a batch-shared value would give two boards one machine ID, and
# one customer's certificate would unlock another's gateway. Counting it here
# produced a green light for exactly the machine that has no identity.
check() {
    path="$1"
    if [ ! -e "$path" ]; then
        printf '    %-46s absent\n' "$path"
        return
    fi
    bits="$(stat -c '%a %U:%G' "$path" 2>/dev/null || echo '?')"

    if computed_read "$path"; then
        verdict="READABLE"
    else
        verdict="ROOT ONLY"
    fi

    # Cross-check against a real read, and believe the real one when they
    # disagree -- the bits can be right while a mount option, SELinux or a
    # namespace makes the actual answer different.
    note=""
    attempt_read "$path"
    case "$?" in
        0) [ "$verdict" = "ROOT ONLY" ] && note=" (bits said no, real read said YES)"
           verdict="READABLE" ;;
        3) note=" [from permission bits; no way to test as uid $RUNTIME_UID here]" ;;
        *) [ "$verdict" = "READABLE" ] && note=" (bits said yes, real read said NO)"
           verdict="ROOT ONLY" ;;
    esac

    if [ "$verdict" = "READABLE" ]; then
        value="$(head -c 48 "$path" 2>/dev/null | tr -d '\0\n' | tr -c '[:print:]' '.')"
        printf '    %-46s READABLE  [%s] %s%s\n' "$path" "$bits" "$value" "$note"
        [ "$DERIVES" = "yes" ] && deriving=$((deriving + 1))
    else
        printf '    %-46s ROOT ONLY [%s] <- present, gateway cannot read%s\n' \
            "$path" "$bits" "$note"
        [ "$DERIVES" = "yes" ] && blocked=$((blocked + 1))
    fi
}

printf 'machine identity probe\n'
printf '  runtime user:  uid %s, gid %s (+%s)\n' "$RUNTIME_UID" "$RUNTIME_GID" "$AGENT_GID"
printf '  kernel:        %s\n' "$(uname -srm)"
case "$ATTEMPT_METHOD" in
    docker)  printf '  tested via:    docker exec -u %s %s (most faithful)\n' \
                 "$RUNTIME_UID" "$API_CONTAINER" ;;
    setpriv) printf '  tested via:    setpriv --reuid=%s\n' "$RUNTIME_UID" ;;
    sudo)    printf '  tested via:    sudo -u #%s\n' "$RUNTIME_UID" ;;
    *)       printf '  tested via:    PERMISSION BITS ONLY -- no docker/setpriv/sudo\n' ;;
esac
printf '\n  sources that CAN derive an identity:\n'

DERIVES=yes
check /sys/firmware/devicetree/base/serial-number
check /sys/devices/soc0/serial_number
check /sys/class/dmi/id/product_uuid
check /sys/class/dmi/id/board_serial
# mmcblk only, and not the boot/rpmb partitions -- emmcSources in
# internal/license/machineid.go skips those because they repeat the same CID.
# An x86 NAS on SATA or NVMe has no mmcblk at all, which leaves it the two DMI
# files above and nothing else.
for cid in /sys/block/mmcblk*/device/cid; do
    case "$cid" in *boot*|*rpmb*) continue ;; esac
    [ -e "$cid" ] && check "$cid"
done

printf '\n  recorded, but NEVER used to derive an identity:\n'
DERIVES=no
if [ -r /proc/cpuinfo ] && grep -qs '^Serial' /proc/cpuinfo; then
    check /proc/cpuinfo
else
    printf '    %-46s absent\n' '/proc/cpuinfo Serial'
fi
printf '    %-46s regenerated on reinstall\n' '/etc/machine-id'
printf '    %-46s never derives; see choosePrimary\n' 'MAC addresses'

printf '\n'
if [ "$deriving" -gt 0 ]; then
    printf 'RESULT: this machine CAN derive an identity (%d readable source(s)).\n' "$deriving"
    [ "$blocked" -gt 0 ] && printf '        %d further source(s) exist but are out of reach.\n' "$blocked"
    exit 0
fi

printf 'RESULT: this machine CANNOT derive an identity.\n'
if [ "$blocked" -gt 0 ]; then
    printf '        %d source(s) are present but unreadable by uid %s.\n' "$blocked" "$RUNTIME_UID"
    printf '        A PERMISSION problem: the identity is there, out of reach.\n'
    printf '        Fix: have the privileged hardware container read it and pass\n'
    printf '        the fingerprint over the agent socket.\n'
else
    printf '        No burned-in identifier is present at all.\n'
    printf '        A HARDWARE problem: nothing to reach. Machine licences\n'
    printf '        cannot be bound on this platform by any means.\n'
fi
printf '\n        Send this output back before shipping to this platform.\n'
exit 1
