#!/bin/bash
# snapshot_release.sh - tag current fnos-local-0.1 images as backups before
# deploying new builds, and prune old backups (keep the newest N per image).
#
# Usage (on the NAS):
#   sudo bash snapshot_release.sh [stamp]
#     stamp  optional label, default: YYYYmmdd-HHMMSS
#
# Typical flow before deploying new images:
#   sudo bash deploy/fnos/snapshot_release.sh
#   <build / retag new images as fnos-local-0.1>
#   docker compose --env-file .env.fnos -f docker-compose.yml \
#     -f deploy/fnos/compose.fnos.yml up -d
#
# Rollback later with: sudo bash deploy/fnos/rollback.sh list
set -euo pipefail

STAMP="${1:-$(date +%Y%m%d-%H%M%S)}"
KEEP="${KEEP:-5}"
IMAGES="nas-comm-hardware nas-comm-api nas-comm-web"

echo "=== snapshot_release: stamp=$STAMP keep=$KEEP $(date) ==="
for img in $IMAGES; do
  if docker image inspect "$img:fnos-local-0.1" >/dev/null 2>&1; then
    docker tag "$img:fnos-local-0.1" "$img:backup-$STAMP"
    echo "[snapshot] $img:fnos-local-0.1 -> $img:backup-$STAMP ($(docker image inspect "$img:fnos-local-0.1" --format '{{.Id}}' | cut -c8-19))"
  else
    echo "[snapshot] WARN $img:fnos-local-0.1 not found, skipped"
  fi
done

for img in $IMAGES; do
  docker images --format '{{.Tag}}' "$img" | grep '^backup-' | sort -r | tail -n +"$((KEEP + 1))" | while read -r tag; do
    echo "[prune] removing $img:$tag"
    docker rmi "$img:$tag" >/dev/null 2>&1 || true
  done
done

echo "=== current backups ==="
for img in $IMAGES; do
  tags=$(docker images --format '{{.Tag}}' "$img" | grep '^backup-' | sort -r | tr '\n' ' ')
  echo "$img: ${tags:-none}"
done
echo "=== DONE $(date) ==="
