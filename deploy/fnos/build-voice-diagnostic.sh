#!/bin/bash
set -euo pipefail
src=/mnt/docker-data/build/codex-voice-20260914
mkdir -p "$src"
tar -xf /tmp/codex-voice-api-source.tar -C "$src"
cd "$src"
docker build \
  --build-arg GO_IMAGE=golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd \
  --build-arg VERSION=codex-voice-control-20260914 \
  --target runtime -t nas-comm-api:codex-voice-control-20260914 .
printf '\nVOICE_BUILD_SUCCEEDED\n'
