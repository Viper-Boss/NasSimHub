#!/bin/bash
# Pre-pull remaining base images needed by compose build.
# (golang:1.26.5-bookworm was pulled by compose_prepull.sh earlier.)
set -e
echo "=== pull node:24.18.0-bookworm-slim ==="
time docker pull docker.io/library/node:24.18.0-bookworm-slim 2>&1
echo "=== pull golang:1.26.5-trixie ==="
time docker pull docker.io/library/golang:1.26.5-trixie 2>&1
echo "=== pull debian:trixie-slim ==="
time docker pull docker.io/library/debian:trixie-slim 2>&1
echo "=== DONE ==="