#!/bin/sh
# End-to-end test of NasSimHub Node support against a real Node process.
#
#   scripts/nassimhub-node-e2e.sh [NODE_REPO]
#
# NODE_REPO is a nassimhub-node checkout whose mock-nassimhub-node has the
# -tls and -platform flags (default: $NASSIMHUB_NODE_REPO, else
# ../nassimhub-node-dev1). The mock serves the real Node protocol over the real
# device-certificate TLS; the test drives discovery, pairing, pinned trust,
# SMS in both directions, restarts, USB->Wi-Fi failover, an impostor and
# unpairing through the gateway's own store and communication service.
#
# Linux only: the Wi-Fi leg listens on 127.0.0.2.
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
node_repo=${1:-${NASSIMHUB_NODE_REPO:-$repo_dir/../nassimhub-node-dev1}}
[ -d "$node_repo/agent/cmd/mock-nassimhub-node" ] || {
    echo "no mock-nassimhub-node under $node_repo" >&2
    exit 1
}

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT HUP INT TERM

echo "building mock-nassimhub-node from $node_repo"
(cd "$node_repo" && GOFLAGS= CGO_ENABLED=0 go build -o "$work/mock-nassimhub-node" ./agent/cmd/mock-nassimhub-node)
"$work/mock-nassimhub-node" -h 2>&1 | grep -q -- '-tls' || {
    echo "this mock-nassimhub-node has no -tls flag; apply the r122 mock patch" >&2
    exit 1
}

cd "$repo_dir"
NSH_E2E_NODE_BIN="$work/mock-nassimhub-node" \
    go test ./internal/nassimhubnode/ -run E2E -count=1 -v
