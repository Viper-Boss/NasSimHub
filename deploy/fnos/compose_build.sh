#!/bin/bash
# Build all three images for fnos-local-0.1 and log output.
set -e
cd /volume1/docker/homesim/source/nas-comm-gateway-r6
echo "=== compose config ==="
docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml config --quiet
echo "=== compose build ==="
docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml build 2>&1
echo "=== compose images ==="
docker images | grep -E 'fnos-local-0.1'
echo "=== DONE ==="