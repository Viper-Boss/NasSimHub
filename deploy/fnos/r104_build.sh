#!/bin/bash
# r104 deployment: snapshot -> web overlay + api full + hardware rebuild
set -u
SRC=/volume1/docker/homesim/source/nas-comm-gateway-r6
cd "$SRC" || exit 1

echo "=== snapshot backup-r104 $(date) ==="
bash deploy/fnos/snapshot_release.sh backup-r104 2>&1 | grep -E "snapshot|WARN"

echo "=== web overlay build $(date) ==="
cat > /tmp/r104-web-overlay.df <<'EOF'
FROM nas-comm-web:flat
USER root
RUN chmod -R u+w /usr/share/nginx/html && rm -rf /usr/share/nginx/html/*
COPY dist/ /usr/share/nginx/html/
RUN chown -R 101:101 /usr/share/nginx/html
USER 101:101
EOF
docker build --no-cache -f /tmp/r104-web-overlay.df --tag nas-comm-web:fnos-local-0.1 web/ 2>&1 | tail -3
echo "web rc=$?"

echo "=== api full build $(date) ==="
docker build --no-cache \
  --build-arg GO_IMAGE="golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd" \
  --build-arg MODEMDECK_UID=10001 \
  --build-arg MODEMDECK_GID=10001 \
  --build-arg VERSION=fnos-local-0.1 \
  --build-arg BUILD_DATE=unknown \
  --build-arg VCS_REF=r104 \
  --target runtime \
  --tag nas-comm-api:fnos-local-0.1 \
  --file Dockerfile \
  . 2>&1 | tail -3
echo "api rc=$?"

echo "=== hardware rebuild (agent changed) $(date) ==="
docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml build hardware 2>&1 | tail -3
echo "hardware rc=$?"

echo "=== images $(date) ==="
docker images --format '{{.Repository}}:{{.Tag}} {{.ID}}' | grep -E '^nas-comm-(api|web|hardware):fnos-local-0.1'
echo "=== BUILD ALL DONE $(date) ==="
