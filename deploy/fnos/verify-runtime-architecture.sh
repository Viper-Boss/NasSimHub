#!/bin/sh
# Check actual ELF architecture; image metadata alone cannot detect a copied foreign binary.
set -eu
mode=container
if [ "${1:-}" = --image ]; then mode=image; shift; fi
target=${1:-nas-comm-api}
binary=${2:-/usr/local/bin/modemdeck}
case "$(uname -m)" in
  aarch64|arm64) expected=183; arch=arm64 ;;
  x86_64|amd64) expected=62; arch=amd64 ;;
  *) echo 'Unsupported host architecture' >&2; exit 1 ;;
esac
if [ "$mode" = image ]; then image=$target; else image=$(docker inspect --format '{{.Image}}' "$target"); fi
image_arch=$(docker image inspect --format '{{.Architecture}}' "$image")
[ "$image_arch" = "$arch" ] || { echo "Image architecture $image_arch differs from host $arch" >&2; exit 1; }
if [ "$mode" = image ]; then
  bytes=$(docker run --rm --network none --read-only --cap-drop=ALL --security-opt no-new-privileges --entrypoint /bin/sh "$image" -c 'od -An -tu1 -j18 -N2 "$1"' sh "$binary")
else
  bytes=$(docker exec "$target" od -An -tu1 -j18 -N2 "$binary")
fi
set -- $bytes
[ "$#" = 2 ] || { echo 'Cannot read ELF machine field' >&2; exit 1; }
machine=$(($1 + 256 * $2))
[ "$machine" = "$expected" ] || { echo "Foreign ELF machine=$machine; expected=$expected ($arch)" >&2; exit 1; }
echo "Native runtime verified: $target $arch ELF=$machine"
