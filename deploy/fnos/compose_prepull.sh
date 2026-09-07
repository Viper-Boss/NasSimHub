#!/bin/bash
# Pre-pull required base images directly so they end up in docker image cache
# (buildx pulls via concurrent resolver which is much slower on this NAS).
set -e
echo "=== pull golang:1.26.5-bookworm ==="
time docker pull docker.io/library/golang:1.26.5-bookworm 2>&1
echo "=== pull golang:1.26.5-trixie ==="
time docker pull docker.io/library/golang:1.26.5-trixie 2>&1
echo "=== DONE ==="