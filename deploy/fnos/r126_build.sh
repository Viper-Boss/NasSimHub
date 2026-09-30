#!/bin/bash
# dev29 (r117~r126 + GPT dev28 合入) deployment: web overlay + api full + hardware full, all --no-cache
set -u
SRC=/volume1/docker/homesim/source/nas-comm-gateway-r6
LOG=/tmp/r126-build.log
cd "$SRC" || exit 1

echo "=== web overlay build $(date) ===" > "$LOG"
printf '%s\n' \
  'FROM nas-comm-web:flat' \
  'USER root' \
  'RUN chmod -R u+w /usr/share/nginx/html && rm -rf /usr/share/nginx/html/*' \
  'COPY dist/ /usr/share/nginx/html/' \
  'RUN chown -R 101:101 /usr/share/nginx/html' \
  'USER 101:101' > /tmp/r126-web-overlay.df
docker build --no-cache -f /tmp/r126-web-overlay.df --tag nas-comm-web:fnos-local-0.1 web/ >> "$LOG" 2>&1
echo "web rc=$?" >> "$LOG"

echo "=== api full build $(date) ===" >> "$LOG"
docker build --no-cache --network=host \
  --add-host mirrors.aliyun.com:112.15.7.206 \
  --add-host mirrors.aliyun.com:39.173.148.49 \
  --build-arg APT_MIRROR=mirrors.aliyun.com \
  --build-arg GO_IMAGE="golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd" \
  --build-arg MODEMDECK_UID=10001 \
  --build-arg MODEMDECK_GID=10001 \
  --build-arg VERSION=fnos-local-0.1 \
  --build-arg BUILD_DATE=unknown \
  --build-arg VCS_REF=dev29-r126 \
  --target runtime \
  --tag nas-comm-api:fnos-local-0.1 \
  --file Dockerfile \
  . >> "$LOG" 2>&1
echo "api rc=$?" >> "$LOG"

echo "=== hardware full build $(date) ===" >> "$LOG"
docker build --no-cache --network=host \
  --add-host mirrors.aliyun.com:112.15.7.206 \
  --add-host mirrors.aliyun.com:39.173.148.49 \
  --build-arg APT_MIRROR=mirrors.aliyun.com \
  --build-arg VERSION=fnos-local-0.1 \
  --build-arg BUILD_DATE=unknown \
  --build-arg VCS_REF=dev29-r126 \
  --target runtime \
  --tag nas-comm-hardware:fnos-local-0.1 \
  --file hardware/Dockerfile \
  . >> "$LOG" 2>&1
echo "hardware rc=$?" >> "$LOG"

echo "=== images $(date) ===" >> "$LOG"
docker images --format '{{.Repository}}:{{.Tag}} {{.ID}}' | grep -E '^nas-comm-(api|web|hardware):fnos-local-0.1' >> "$LOG" 2>&1
echo "=== BUILD ALL DONE $(date) ===" >> "$LOG"
