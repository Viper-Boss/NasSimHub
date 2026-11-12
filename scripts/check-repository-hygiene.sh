#!/bin/sh

set -eu

violations="$(
    git ls-files | awk '
        BEGIN { IGNORECASE = 1 }
        /^web\/src\/assets\/(notifications|ringtones|tones)\/[^\/]+\.ogg$/ {
            next
        }
        /^internal\/tlsmanager\/cloudflare_origin_roots\.pem$/ {
            next
        }
        /(^|\/)(data|logs|secrets|recordings|private|scratch|backups|one-off|migration|migrations)\// ||
        /(^|\/)cmd\/modemdeck-(import-legacy-telegram|migrate)\// ||
        /(^|\/)internal\/(legacymigrate|legacytelegram)\// ||
        /(^|\/)integration\/legacy_/ ||
        /(^|\/)(Claude outputs|init)\// ||
        /(^|\/)HANDOFF.*\.md$/ ||
        /(^|\/)(AI_HANDOFF|CHANGES-FOR-CLAUDE.*)\.md$/ ||
        /(^|\/)\.reset-confirmation-token$/ ||
        /\.(db|db-journal|db-shm|db-wal|sqlite|sqlite3|dump|backup|bak|sql\.gz)$/ ||
        /\.(key|pem|p12|pfx|jks|keystore)$/ ||
        /\.(wav|flac|ogg|opus)$/ {
            print
        }
    '
)"

if [ -n "${violations}" ]; then
    printf '%s\n' "repository hygiene check rejected tracked private or one-off artifacts:" >&2
    printf '%s\n' "${violations}" >&2
    exit 1
fi

# Tracked files over 1MB are almost always stray binaries (a 42MB agent once
# lived at the root). Legitimate large data files are whitelisted here.
large_violations="$(
    git ls-files -z |
        xargs -0 -r du -k 2>/dev/null |
        awk -v limit=1024 '$1 > limit && $2 != "internal/phonegeo/phone.dat" { print }'
)"

if [ -n "${large_violations}" ]; then
    printf '%s\n' "repository hygiene check rejected tracked files larger than 1MB:" >&2
    printf '%s\n' "${large_violations}" >&2
    exit 1
fi
