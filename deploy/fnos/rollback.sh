#!/bin/bash
# rollback.sh - restore previously snapshotted images back to fnos-local-0.1
# and recreate the affected containers. Never rebuilds anything.
#
# Usage (on the NAS, from the repo root):
#   sudo bash deploy/fnos/rollback.sh list              # show available backups
#   sudo bash deploy/fnos/rollback.sh <stamp>           # roll back ALL services
#   sudo bash deploy/fnos/rollback.sh <stamp> api web   # roll back selected services
#
# <stamp> is the timestamp shown by "list" (e.g. 20260908-130000).
# Services: hardware | api | web  (web maps to the compose service "modemdeck")
set -euo pipefail

REPO=/volume1/docker/homesim/source/nas-comm-gateway-r6
IMAGES="nas-comm-hardware nas-comm-api nas-comm-web"

compose_up() {
  cd "$REPO"
  docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml up -d "$@"
}

case "${1:-}" in
  ""|list)
    echo "=== available backups (newest first) ==="
    for img in $IMAGES; do
      tags=$(docker images --format '{{.Tag}}' "$img" | grep '^backup-' | sort -r | tr '\n' ' ')
      echo "$img: ${tags:-none}"
    done
    echo "usage: rollback.sh <stamp> [service ...]   services: hardware api web"
    exit 0
    ;;
esac

STAMP="$1"; shift
SERVICES="${*:-hardware api web}"

echo "=== rollback: stamp=$STAMP services=$SERVICES $(date) ==="
for img in $IMAGES; do
  if ! docker image inspect "$img:backup-$STAMP" >/dev/null 2>&1; then
    echo "[rollback] ERROR $img:backup-$STAMP not found"; exit 1
  fi
done

svc_for() {
  case "$1" in
    hardware) echo "hardware" ;;
    api) echo "api" ;;
    web) echo "modemdeck" ;;
    *) echo "" ;;
  esac
}
img_for() {
  case "$1" in
    hardware) echo "nas-comm-hardware" ;;
    api) echo "nas-comm-api" ;;
    web) echo "nas-comm-web" ;;
  esac
}

UP_ARGS=""
for svc in $SERVICES; do
  img=$(img_for "$svc")
  cs=$(svc_for "$svc")
  [ -n "$cs" ] || { echo "unknown service: $svc"; exit 1; }
  # safety net: keep whatever is currently live under a distinct tag
  if docker image inspect "$img:fnos-local-0.1" >/dev/null 2>&1; then
    pre="rollback-prev-$(date +%Y%m%d-%H%M%S)"
    docker tag "$img:fnos-local-0.1" "$img:$pre"
    echo "[rollback] preserved live $img:fnos-local-0.1 as $img:$pre"
  fi
  docker tag "$img:backup-$STAMP" "$img:fnos-local-0.1"
  echo "[rollback] $img:backup-$STAMP -> $img:fnos-local-0.1"
  UP_ARGS="$UP_ARGS $cs"
done

echo "[rollback] recreating containers:$UP_ARGS"
compose_up $UP_ARGS
sleep 2
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}' | grep nas-comm || true
echo "=== DONE (wait ~40s for health checks) $(date) ==="
