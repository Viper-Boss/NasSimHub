#!/bin/bash
# Build reviewed sources only. Does not restart services or alter data.
set -euo pipefail
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO=${NAS_SOURCE_DIR:-$(cd -- "$SCRIPT_DIR/../.." && pwd)}
cd "$REPO"
VERSION=${BUILD_VERSION:-$(tr -d '\r\n' < VERSION)}
REF=$(git rev-parse --verify HEAD)
for service in "${@:-api}"; do
    case "$service" in
        api) file=Dockerfile; target=runtime; image=nas-comm-api ;;
        web) file=Dockerfile; target=web-runtime; image=nas-comm-web ;;
        hardware) file=hardware/Dockerfile; target=runtime; image=nas-comm-hardware ;;
        *) echo "Usage: bash deploy/fnos/build.sh [api web hardware]" >&2; exit 2 ;;
    esac
    docker build --file "$file" --target "$target" \
        --build-arg VERSION="$VERSION" --build-arg VCS_REF="$REF" \
        --tag "$image:$VERSION" .
done
