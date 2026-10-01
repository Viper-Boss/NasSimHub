#!/usr/bin/env bash
#
# Build release artifacts for nassimhub-agent.
#
# Produces, under dist/:
#   nassimhub-agent-<version>-linux-<arch>              the binary
#   nassimhub-agent-<version>-linux-<arch>.tar.gz       tarball with unit + config
#   nassimhub-agent_<version>_<debarch>.deb             Debian package
#   SHA256SUMS                                          checksums for all of the above
#
# The .deb is assembled with `ar` and `tar` rather than with dpkg-deb, so this
# script runs on a machine with no Debian tooling installed. That is not a
# preference: the build host is a container with a Go toolchain and little else,
# and a packaging step that only works on a Debian box is a step that does not
# get run.
#
# Nothing here touches a device. It writes files into dist/ and stops.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$here"

version="${VERSION:-$(cat VERSION 2>/dev/null || echo 0.0.0-dev)}"
version="${version//$'\n'/}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%d)}"
dist="$here/dist"
architectures="${ARCHS:-arm64 amd64}"

# The target that matters is arm64: MSM8916 is a Cortex-A53. amd64 is built so
# the same artifact can be exercised on a development machine.
declare -A deb_arch=([arm64]=arm64 [amd64]=amd64 [arm]=armhf)

# ---------------------------------------------------------------------------

# build_deb assembles a .deb from a built binary.
#
# A .deb is an `ar` archive of three members in a fixed order: debian-binary,
# control.tar.gz, data.tar.gz. Nothing about it needs dpkg to create.
build_deb() {
	local binary="$1" architecture="$2" package_version="$3"
	local root="$dist/.deb-$architecture"
	rm -rf "$root"
	mkdir -p "$root/data/usr/bin" \
		"$root/data/lib/systemd/system" \
		"$root/data/etc/nassimhub" \
		"$root/data/usr/share/doc/nassimhub-agent" \
		"$root/control"

	install -m 0755 "$binary" "$root/data/usr/bin/nassimhub-agent"
	install -m 0644 deploy/nassimhub-agent.service "$root/data/lib/systemd/system/nassimhub-agent.service"
	mkdir -p "$root/data/lib/systemd/system/nassimhub-agent.service.d"
	install -m 0644 deploy/nassimhub-agent-resources.conf "$root/data/lib/systemd/system/nassimhub-agent.service.d/resources.conf"
	install -m 0644 deploy/nassimhub-agent.conf "$root/data/etc/nassimhub/agent.conf"
	if [ -f deploy/INSTALL.md ]; then
		install -m 0644 deploy/INSTALL.md "$root/data/usr/share/doc/nassimhub-agent/INSTALL.md"
	fi

	local installed_size
	installed_size=$(du -ks "$root/data" | cut -f1)

	cat > "$root/control/control" <<-EOF
		Package: nassimhub-agent
		Version: $package_version
		Section: net
		Priority: optional
		Architecture: $architecture
		Maintainer: NasSimHub <nassimhub@example.invalid>
		Installed-Size: $installed_size
		Depends: adduser
		Description: NasSimHub Node agent
		 Turns a 4G Wi-Fi stick into a SIM node a NasSimHub NAS can manage.
		 .
		 The agent owns device identity, pairing, modem and network status, SMS
		 and call commands, Wi-Fi provisioning and a bounded diagnostic log. It
		 has no runtime dependencies beyond the Go standard library: no database,
		 no container, no interpreter.
	EOF

	# The configuration file is a conffile, so dpkg preserves an operator's
	# edits across upgrades instead of overwriting them.
	echo "/etc/nassimhub/agent.conf" > "$root/control/conffiles"

	# md5sums is what `dpkg --verify` reads. Producing it here means an operator
	# can check an installed device against the package without the build host.
	(cd "$root/data" && find . -type f -printf '%P\0' | sort -z |
		xargs -0 md5sum > "$root/control/md5sums") 2>/dev/null || true

	cat > "$root/control/preinst" <<-'EOF'
		#!/bin/sh
		set -e
		# A dedicated unprivileged account. The agent needs no privileges at all
		# in this stage; when the hardware backend lands it will need access to
		# specific device nodes, which is a group membership rather than root.
		if ! getent passwd nassimhub >/dev/null; then
		    adduser --system --group --no-create-home \
		        --home /var/lib/nassimhub \
		        --gecos "NasSimHub Node agent" nassimhub >/dev/null
		fi
		exit 0
	EOF

	cat > "$root/control/postinst" <<-'EOF'
		#!/bin/sh
		set -e
		if [ "$1" = "configure" ]; then
		    # The state directory holds the device identity. It is created here
		    # with restrictive permissions rather than left to systemd, because
		    # an upgrade on a device that already has an identity must not widen
		    # the permissions on the key that identity is derived from.
		    mkdir -p /var/lib/nassimhub
		    chown nassimhub:nassimhub /var/lib/nassimhub
		    chmod 0700 /var/lib/nassimhub
		    mkdir -p /var/log/nassimhub
		    chown nassimhub:nassimhub /var/log/nassimhub
		    chmod 0750 /var/log/nassimhub

		    # Refuse to enable a service whose configuration does not parse.
		    # Starting it would fail anyway; failing here says why.
		    if ! /usr/bin/nassimhub-agent -config /etc/nassimhub/agent.conf -check-config; then
		        echo "nassimhub-agent: /etc/nassimhub/agent.conf is not valid; not starting" >&2
		        exit 1
		    fi

		    if [ -d /run/systemd/system ]; then
		        systemctl daemon-reload >/dev/null 2>&1 || true
		        systemctl enable nassimhub-agent.service >/dev/null 2>&1 || true
		        systemctl restart nassimhub-agent.service >/dev/null 2>&1 || true
		    fi
		fi
		exit 0
	EOF

	cat > "$root/control/prerm" <<-'EOF'
		#!/bin/sh
		set -e
		if [ "$1" = "remove" ] && [ -d /run/systemd/system ]; then
		    systemctl stop nassimhub-agent.service >/dev/null 2>&1 || true
		    systemctl disable nassimhub-agent.service >/dev/null 2>&1 || true
		fi
		exit 0
	EOF

	cat > "$root/control/postrm" <<-'EOF'
		#!/bin/sh
		set -e
		if [ -d /run/systemd/system ]; then
		    systemctl daemon-reload >/dev/null 2>&1 || true
		fi
		# purge removes the package's own files but NOT /var/lib/nassimhub.
		#
		# That directory holds the device identity, and device_id is derived from
		# it. Deleting it would make the device appear on the NAS as a different
		# device after a reinstall, and the user would have to re-pair something
		# they never replaced. Removing software should not change what a device
		# IS. An operator who genuinely wants a new identity deletes it by hand.
		exit 0
	EOF

	chmod 0755 "$root/control"/preinst "$root/control"/postinst \
		"$root/control"/prerm "$root/control"/postrm

	# Reproducible-ish: fixed ownership and a fixed mtime, so two builds of the
	# same source produce the same bytes.
	local mtime="${SOURCE_DATE_EPOCH:-0}"
	tar --owner=root:0 --group=root:0 --numeric-owner \
		--mtime="@$mtime" --sort=name \
		-C "$root/data" -czf "$root/data.tar.gz" .
	tar --owner=root:0 --group=root:0 --numeric-owner \
		--mtime="@$mtime" --sort=name \
		-C "$root/control" -czf "$root/control.tar.gz" .
	echo "2.0" > "$root/debian-binary"

	local output="$dist/nassimhub-agent_${package_version}_${architecture}.deb"
	rm -f "$output"
	# Member order is part of the format, not a convention.
	(cd "$root" && ar rc "$output" debian-binary control.tar.gz data.tar.gz)
	rm -rf "$root"
	echo "    $(basename "$output")  $(($(stat -c %s "$output") / 1024)) KiB"
}

# ---------------------------------------------------------------------------

command -v go >/dev/null || { echo "go is not installed" >&2; exit 1; }
command -v ar >/dev/null || { echo "ar is not installed (binutils)" >&2; exit 1; }

rm -rf "$dist"
mkdir -p "$dist"

echo "nassimhub-agent $version ($build_date)"
echo

for arch in $architectures; do
	binary="$dist/nassimhub-agent-$version-linux-$arch"
	echo "==> building linux/$arch"
	# CGO off gives a static binary with no libc version to match, which is what
	# makes one artifact work on both a musl-based rootfs and a glibc one. On
	# this hardware that is the difference between shipping one build and four.
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
		-trimpath \
		-ldflags "-s -w -X main.version=$version -X main.buildDate=$build_date" \
		-o "$binary" \
		./agent/cmd/nassimhub-agent

	echo "    $(basename "$binary")  $(($(stat -c %s "$binary") / 1024)) KiB"

	echo "==> tarball for $arch"
	staging="$dist/.stage-$arch"
	rm -rf "$staging"
	mkdir -p "$staging/nassimhub-agent-$version"
	cp "$binary" "$staging/nassimhub-agent-$version/nassimhub-agent"
	cp deploy/nassimhub-agent.service deploy/nassimhub-agent.conf "$staging/nassimhub-agent-$version/"
	[ -f deploy/INSTALL.md ] && cp deploy/INSTALL.md "$staging/nassimhub-agent-$version/"
	tar --owner=root:0 --group=root:0 --numeric-owner --sort=name \
		-C "$staging" -czf "$binary.tar.gz" "nassimhub-agent-$version"
	rm -rf "$staging"
	echo "    $(basename "$binary.tar.gz")"

	target_deb_arch="${deb_arch[$arch]:-}"
	if [ -z "$target_deb_arch" ]; then
		echo "    no debian architecture mapped for $arch; skipping .deb"
		continue
	fi
	echo "==> debian package for $target_deb_arch"
	build_deb "$binary" "$target_deb_arch" "$version"
done

echo
echo "==> checksums"
(cd "$dist" && sha256sum nassimhub-agent* > SHA256SUMS && cat SHA256SUMS)
echo
echo "artifacts in $dist"
