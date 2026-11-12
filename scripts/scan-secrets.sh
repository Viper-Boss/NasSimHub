#!/bin/sh
# Scan the index snapshot (the checkout in CI), excluding .git and deps.
# Print only redacted findings. Historical cleanup is a separate owner task.
set -eu
cd "${1:-.}"
scan_dir=$(mktemp -d /tmp/nassimhub-secrets.XXXXXXXX)
trap 'rm -rf -- "$scan_dir"' EXIT HUP INT TERM
mkdir "$scan_dir/tree"
git -c safe.directory="$PWD" checkout-index --all --prefix="$scan_dir/tree/"
gitleaks dir "$scan_dir/tree" --config="$scan_dir/tree/.gitleaks.toml" --no-banner --no-color --redact=100
