#!/bin/sh
#
# Render PKGBUILD.template for one release.
#
#   packaging/aur/render.sh <version> <checksums.txt> <LICENSE> > PKGBUILD
#
# <version> carries no leading 'v' (pkgver must start with a digit, same rule
# as the .deb). The hashes come from the release's own checksums.txt rather
# than being recomputed here, so the AUR package, install.sh, `csm -upgrade`
# and the Homebrew formula all vouch for the same bytes.

set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 <version> <checksums.txt> <LICENSE>" >&2
	exit 1
fi

version="${1#v}"
checksums="$2"
license="$3"

template="$(dirname "$0")/PKGBUILD.template"
[ -f "$template" ] || { echo "$0: missing $template" >&2; exit 1; }
[ -f "$checksums" ] || { echo "$0: missing $checksums" >&2; exit 1; }
[ -f "$license" ] || { echo "$0: missing $license" >&2; exit 1; }

sum_for() {
	sum="$(awk -v n="$1" '$2 == n { print $1 }' "$checksums")"
	[ -n "$sum" ] || { echo "$0: $checksums has no entry for $1" >&2; exit 1; }
	echo "$sum"
}

amd64="$(sum_for csm-linux-amd64)"
arm64="$(sum_for csm-linux-arm64)"
# The LICENSE is served from the git tag, not the release assets, so it is the
# one hash not already in checksums.txt.
lic="$(sha256sum "$license" | cut -d' ' -f1)"

sed \
	-e "s/@PKGVER@/${version}/g" \
	-e "s/@SHA256_AMD64@/${amd64}/g" \
	-e "s/@SHA256_ARM64@/${arm64}/g" \
	-e "s/@SHA256_LICENSE@/${lic}/g" \
	"$template"
