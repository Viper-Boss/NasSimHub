#!/bin/sh

set -eu

tests_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
source_repo=$(CDPATH='' cd -- "${tests_dir}/../.." && pwd)
fixture=$(mktemp -d)

cleanup() {
    rm -rf -- "$fixture"
}
trap cleanup EXIT HUP INT TERM

fail() {
    printf 'release-image-plan-test: %s\n' "$*" >&2
    exit 1
}

mkdir -p \
    "${fixture}/deploy" \
    "${fixture}/web/src" \
    "${fixture}/cmd/modemdeck" \
    "${fixture}/cmd/modemdeck-updater" \
    "${fixture}/internal/ota" \
    "${fixture}/internal/release" \
    "${fixture}/agent/cmd/modemdeck-agent" \
    "${fixture}/hardware/bin"
cp "${source_repo}/deploy/release-image-plan.sh" "${fixture}/deploy/release-image-plan.sh"
chmod 0755 "${fixture}/deploy/release-image-plan.sh"

write_manifest() {
    {
        printf '%s\n' '{' '  "schema_version": 2,' '  "images": {'
        printf '%s\n' \
            '    "api": "ghcr.io/viper-boss/nassimhub",' \
            '    "web": "ghcr.io/viper-boss/nassimhub-web",' \
            '    "hardware": "ghcr.io/viper-boss/nassimhub-hardware",' \
            '    "updater": "ghcr.io/viper-boss/nassimhub-updater"' \
            '  }'
        printf '%s\n' '}'
    } >"${fixture}/deploy/release-manifest.json"
}

git -C "$fixture" init --quiet
git -C "$fixture" config user.name ModemDeck
git -C "$fixture" config user.email modemdeck@example.invalid
printf '%s\n' base >"${fixture}/Dockerfile"
printf '%s\n' base >"${fixture}/web/src/main.ts"
printf '%s\n' base >"${fixture}/cmd/modemdeck/main.go"
printf '%s\n' base >"${fixture}/cmd/modemdeck-updater/main.go"
printf '%s\n' base >"${fixture}/internal/ota/controller.go"
printf '%s\n' base >"${fixture}/agent/cmd/modemdeck-agent/main.go"
printf '%s\n' base >"${fixture}/hardware/bin/entrypoint"
write_manifest
git -C "$fixture" add .
git -C "$fixture" commit --quiet -m v1.9.5
git -C "$fixture" tag -a v1.9.3 -m v1.9.3
git -C "$fixture" tag -a v1.9.5 -m v1.9.5

printf '%s\n' changed >"${fixture}/Dockerfile"
write_manifest
git -C "$fixture" add .
git -C "$fixture" commit --quiet -m v1.9.6
git -C "$fixture" tag -a v1.9.6 -m v1.9.6
plan=$(cd "$fixture" && deploy/release-image-plan.sh \
    v1.9.6 HEAD v1.9.5 viper-boss false)
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "build") | .component) | join(",")')" \
    = 'api,web,updater' ] ||
    fail "shared Dockerfile change did not select exactly API, Web, and Updater: $plan"
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "retain") | .component) | join(",")')" \
    = hardware ] || fail "unchanged Hardware was not retained: $plan"

printf '%s\n' changed-again >"${fixture}/web/src/main.ts"
write_manifest
git -C "$fixture" add .
git -C "$fixture" commit --quiet -m v1.9.7
git -C "$fixture" tag -a v1.9.7 -m v1.9.7
plan=$(cd "$fixture" && deploy/release-image-plan.sh \
    v1.9.7 HEAD v1.9.6 viper-boss false)
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "build") | .component) | join(",")')" = web ] ||
    fail "Web-only change selected another container: $plan"
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "retain") | .source_version) | unique | join(",")')" \
    = v1.9.6 ] || fail "retained images did not use the previous release: $plan"

printf '%s\n' test-only >"${fixture}/agent/cmd/modemdeck-agent/main_test.go"
git -C "$fixture" add .
git -C "$fixture" commit --quiet -m v1.9.8
git -C "$fixture" tag -a v1.9.8 -m v1.9.8
plan=$(cd "$fixture" && deploy/release-image-plan.sh \
    v1.9.8 HEAD v1.9.7 viper-boss false)
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "build")) | length')" = 0 ] ||
    fail "test-only release rebuilt a runtime image: $plan"
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "retain")) | length')" = 4 ] ||
    fail "test-only release did not retain all runtime images: $plan"

printf '%s\n' runtime-change >"${fixture}/agent/cmd/modemdeck-agent/main.go"
write_manifest
git -C "$fixture" add .
git -C "$fixture" commit --quiet -m v1.9.9
git -C "$fixture" tag -a v1.9.9 -m v1.9.9
plan=$(cd "$fixture" && deploy/release-image-plan.sh \
    v1.9.9 HEAD v1.9.8 viper-boss false)
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "build") | .component) | join(",")')" = hardware ] ||
    fail "Hardware runtime change selected another container: $plan"

# The signing key's package is linked by the updater. Changing it must rebuild
# the updater, or a release ships an updater verifying against the old key.
printf '%s\n' key-change >"${fixture}/internal/release/verify.go"
git -C "$fixture" add .
git -C "$fixture" commit --quiet -m v1.9.10
git -C "$fixture" tag -a v1.9.10 -m v1.9.10
plan=$(cd "$fixture" && deploy/release-image-plan.sh \
    v1.9.10 HEAD v1.9.9 viper-boss false)
printf '%s' "$plan" | jq -e '.include | map(select(.mode == "build") | .component) | index("updater")' >/dev/null ||
    fail "a change to internal/release did not rebuild the updater: $plan"

# First release under this namespace: nothing to retain, so everything builds.
plan=$(cd "$fixture" && deploy/release-image-plan.sh \
    v1.9.10 HEAD v1.9.9 viper-boss false true)
[ "$(printf '%s' "$plan" | jq -r '.include | map(select(.mode == "build")) | length')" = 4 ] ||
    fail "force_all did not build every component: $plan"

# Published names must be the ones gateways hold a manifest to.
[ "$(printf '%s' "$plan" | jq -r '.include | map(.image) | sort | join(",")')" \
    = 'nassimhub,nassimhub-hardware,nassimhub-updater,nassimhub-web' ] ||
    fail "published image names do not match internal/release/images.go: $plan"

printf '%s\n' 'release-image-plan-test: ok'
