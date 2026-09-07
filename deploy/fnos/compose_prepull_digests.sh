#!/bin/bash
# Pre-pull all base images needed for compose build at their pinned digests.
# buildx's parallel pull is too slow on this NAS (100KB/s per layer);
# sequential docker pull is ~150KB/s and more reliable.
set -e

pull_one() {
  local img=$1
  echo "=== pull $img ==="
  time docker pull "$img" 2>&1 | tail -5
}

pull_one "docker.io/library/golang@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651"
pull_one "docker.io/library/golang@sha256:4ee9ffa999b4583ce281939cdff828763083610292f252279a0cee77473bd9a7"
pull_one "docker.io/library/debian@sha256:020c0d20b9880058cbe785a9db107156c3c75c2ac944a6aa7ab59f2add76a7bd"
pull_one "docker.io/library/node@sha256:6f7b03f7c2c8e2e784dcf9295400527b9b1270fd37b7e9a7285cf83b6951452d"
pull_one "docker.io/library/alpine@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b"
pull_one "docker.io/library/docker/dockerfile@sha256:b6afd42430b15f2d2a4c5a02b919e98a525b785b1aaff16747d2f623364e39b6"
echo "=== ALL DONE ==="
docker images | head -20