#!/usr/bin/env bash
# Refuse to ship an .fpk that carries one installation's identity to everyone.
#
#   ./deploy/fnos/check-fpk.sh NasSimHub-1.2.3.fpk
#
# WHY THIS EXISTS. The packaging step is the one part of shipping this product
# that lives nowhere in the repository: no script builds the .fpk, so nothing
# reviews it and nothing tests it. Everything that makes an installation ITSELF
# lives in the data directory, and if that directory is inside the package then
# every customer receives, and runs as, the same installation:
#
#   the administrator account -- so the first-run setup page never appears,
#   and whoever built the package can log into every unit sold;
#   the NKN identity seed -- so every customer answers on the SAME NKN
#   address, their traffic collides, and anyone holding the package can
#   receive messages meant for any of them;
#   the SQLite database -- so the packager's own SMS, contacts and call
#   history are distributed with the product;
#   the anti-fraud counters -- so a new unit can start its first day already
#   at its limit;
#   the TLS key and the forwarding tokens.
#
# None of it announces itself. The unit boots, logs in, and works.
#
# WHAT THIS DOES NOT REPLACE. The gateway also detects a copied data directory
# at runtime and refuses to serve (internal/provision). That is a safety net,
# and on an x86 NAS it may not even be able to fire -- see
# deploy/fnos/machine-id-probe.sh. Not shipping the state in the first place is
# the actual fix; this script is how you find out that you did.

set -uo pipefail

package="${1:-}"
if [ -z "$package" ] || [ ! -f "$package" ]; then
    printf 'usage: %s <package.fpk>\n' "$0" >&2
    exit 2
fi

# The listing, however the package happens to be wrapped.
#
# An .fpk is an archive; which kind is not something to assume, so the format
# is detected rather than declared. A package this cannot read is REFUSED
# rather than passed -- "I could not look" must never read as "nothing found",
# which is the whole failure mode this script guards against.
listing=""
case "$(file -b --mime-type "$package" 2>/dev/null)" in
    application/gzip|application/x-gzip)
        listing="$(tar -tzf "$package" 2>/dev/null)" ;;
    application/x-tar)
        listing="$(tar -tf "$package" 2>/dev/null)" ;;
    application/zip)
        listing="$(unzip -Z1 "$package" 2>/dev/null)" ;;
    *)
        # Try each anyway: `file` is a guess, and being wrong about the
        # wrapper is not a reason to ship unchecked.
        listing="$(tar -tzf "$package" 2>/dev/null || tar -tf "$package" 2>/dev/null || unzip -Z1 "$package" 2>/dev/null)" ;;
esac

if [ -z "$listing" ]; then
    printf 'REFUSED: could not read %s as tar, tar.gz or zip.\n' "$package" >&2
    printf '         Not shipping something this cannot inspect.\n' >&2
    exit 2
fi

entries="$(printf '%s\n' "$listing" | wc -l | tr -d ' ')"
printf 'inspecting %s (%s entries)\n\n' "$package" "$entries"

findings=0

# report prints every match for one pattern.
#
# Matched against the whole path, so a data directory nested anywhere inside
# the package is caught -- the mistake is usually "the build copied the working
# deployment folder", which buries it a few levels down.
report() {
    local pattern="$1" why="$2"
    local hits
    hits="$(printf '%s\n' "$listing" | grep -Ei -- "$pattern" || true)"
    [ -z "$hits" ] && return 0
    findings=$((findings + 1))
    printf 'FOUND  %s\n' "$why"
    printf '%s\n' "$hits" | sed 's/^/         /' | head -12
    local count
    count="$(printf '%s\n' "$hits" | wc -l | tr -d ' ')"
    [ "$count" -gt 12 ] && printf '         ... and %d more\n' "$((count - 12))"
    printf '\n'
}

# The database: the administrator account, and the packager's own messages,
# contacts and call history.
report '(^|/)modemdeck\.db($|-wal$|-shm$)' \
    'the SQLite database (admin account, SMS, contacts, call history)'

# The NKN identity, sealed inside the homesim settings file.
report '(^|/)(settings|integration-draft)\.json$' \
    'homesim settings (carries the sealed NKN identity seed)'

# The NKN node's own wallet.
report '(^|/)wallet\.(json|pswd)$' \
    'the NKN node wallet and its password'

# Anti-fraud counters: a new unit would inherit a spent daily allowance.
report '(^|/)(call_guard|message_guard)\.json$' \
    'anti-fraud counters (a new unit would start with the allowance spent)'

# Licence state, including the fingerprint that clone detection compares.
report '(^|/)license/' \
    'licence certificates and the activation fingerprint'

# TLS: one private key shared by every customer.
report '(^|/)tls/.*\.(key|pem|crt)$' \
    'TLS material (every customer would share one private key)'

# Integration tokens: the customer's messages would reach the packager.
report '(^|/)(bark|forwarders|telegram|call-filter|line-quotas|f50|network-switches|sim-auto-unlock)\.json$' \
    'integration settings (Bark/Telegram/forwarding tokens)'

# The settings key. It lives outside the data directory, so it is a separate
# mistake with the same consequence: a shared key means every customer can
# decrypt every other customer's sealed values.
report '(^|/)(settings\.key|modemdeck_settings_key|secrets/)' \
    'the settings encryption key (must be generated per installation)'

# A real .env, as opposed to the example. Usually carries absolute paths and
# sometimes a key.
report '(^|/)\.env(\.fnos)?$' \
    'a populated .env (ship only the .example)'

# Contacts backup and recordings.
report '(^|/)(backups/contacts|recordings)/' \
    'contact backups or call recordings'

if [ "$findings" -eq 0 ]; then
    printf 'OK: no per-installation state found in the package.\n'
    printf '\n'
    printf 'Still confirm, because this checks only what is INSIDE the file:\n'
    printf '  * the install hook generates a fresh settings key per unit;\n'
    printf '  * the install hook creates an EMPTY data directory;\n'
    printf '  * deploy/fnos/machine-id-probe.sh passes on the target NAS,\n'
    printf '    or the runtime clone detection cannot fire there.\n'
    exit 0
fi

printf 'REFUSED: %d kind(s) of per-installation state are inside this package.\n' "$findings"
printf '         Every customer would receive this one installation:\n'
printf '         the same login, the same NKN address, the same database.\n'
printf '\n'
printf '         Build the package from a clean tree, not from the working\n'
printf '         deployment directory.\n'
exit 1
