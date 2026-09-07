#!/bin/bash
# Build api image using the golang:1.26.5-bookworm digest already fully local
# (53eeac... = current tag content) instead of the stale pinned digest 1ecb7edf
# whose download from Docker Hub is effectively blocked. Deployment-only override.
cd /volume1/docker/homesim/source/nas-comm-gateway-r6
echo "=== docker build api (GO_IMAGE override) $(date) ==="
df -h /volume1 | tail -1
docker build \
  --build-arg GO_IMAGE="golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd" \
  --build-arg MODEMDECK_UID=10001 \
  --build-arg MODEMDECK_GID=10001 \
  --build-arg VERSION=fnos-local-0.1 \
  --build-arg BUILD_DATE=unknown \
  --build-arg VCS_REF=unknown \
  --target runtime \
  --tag nas-comm-api:fnos-local-0.1 \
  --file Dockerfile \
  . 2>&1
echo "=== build rc=$? $(date) ==="
docker images 2>&1 | grep fnos-local
echo "=== ALL DONE $(date) ==="
