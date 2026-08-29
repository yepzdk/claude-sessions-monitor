#!/bin/sh
#
# csm installer — downloads the release binary for this OS/arch, verifies its
# SHA-256 against the release's checksums.txt, and installs it.
#
#   curl -fsSL https://raw.githubusercontent.com/yepzdk/claude-sessions-monitor/main/install.sh | sh
#
# Re-running it is how you upgrade: it always overwrites the target path.
#
# Knobs (all optional):
#   CSM_VERSION        version to install ("0.6.0" or "v0.6.0"); default: latest
#   CSM_INSTALL_DIR    where to put the binary; default: ~/.local/bin
#   CSM_BASE_URL       directory holding the release assets; overrides the
#                      GitHub URL entirely (used by the test suite to point at
#                      a local server)
#   CSM_SKIP_CHECKSUM  set to 1 to install without verifying (last resort)
#
# A version may also be passed as the first argument, which is the only form
# that survives `curl ... | sh -s -- 0.6.0`.

set -eu

REPO="yepzdk/claude-sessions-monitor"
RELEASES="https://github.com/${REPO}/releases"

die() {
	echo "install.sh: $*" >&2
	exit 1
}

# --- what are we installing, and for what machine --------------------------

version="${1:-${CSM_VERSION:-}}"

os=""
case "$(uname -s)" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*) die "unsupported OS '$(uname -s)' — csm builds for Linux and macOS" ;;
esac

arch=""
case "$(uname -m)" in
	x86_64 | amd64) arch="amd64" ;;
	aarch64 | arm64) arch="arm64" ;;
	*) die "unsupported architecture '$(uname -m)' — csm builds for amd64 and arm64" ;;
esac

asset="csm-${os}-${arch}"

# CSM_BASE_URL wins outright so a test can serve assets from anywhere. Without
# it, an explicit version addresses that release directly and the default walks
# GitHub's /latest/download alias, which avoids an API call (and its rate limit)
# just to learn the newest tag.
if [ -n "${CSM_BASE_URL:-}" ]; then
	base="${CSM_BASE_URL%/}"
elif [ -n "$version" ]; then
	case "$version" in
		v*) tag="$version" ;;
		*) tag="v${version}" ;;
	esac
	base="${RELEASES}/download/${tag}"
else
	base="${RELEASES}/latest/download"
fi

# --- tools -----------------------------------------------------------------

# Both downloaders are told to fail loudly on HTTP errors: curl without -f
# writes GitHub's 404 page into the file and exits 0, which would otherwise be
# "installed" as a binary.
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
else
	die "need curl or wget to download the release"
fi

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		return 1
	fi
}

# --- download into a scratch dir that always gets cleaned up ---------------

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading ${asset}..."
fetch "${base}/${asset}" "${tmp}/${asset}" ||
	die "could not download ${base}/${asset}"

if [ "${CSM_SKIP_CHECKSUM:-}" = "1" ]; then
	echo "Skipping checksum verification (CSM_SKIP_CHECKSUM=1)."
else
	fetch "${base}/checksums.txt" "${tmp}/checksums.txt" ||
		die "could not download ${base}/checksums.txt — releases before v0.7.0 have no checksums file; re-run with CSM_SKIP_CHECKSUM=1 to install anyway"

	# The file is `<sha>  <name>` per line, names unqualified. Anchoring on the
	# name keeps csm-linux-arm64 from matching a line for csm-linux-arm64.deb.
	want="$(awk -v a="$asset" '$2 == a { print $1 }' "${tmp}/checksums.txt")"
	[ -n "$want" ] || die "checksums.txt has no entry for ${asset}"

	got="$(sha256_of "${tmp}/${asset}")" ||
		die "no sha256 tool found (sha256sum, shasum, or openssl) — re-run with CSM_SKIP_CHECKSUM=1 to install without verifying"

	if [ "$want" != "$got" ]; then
		die "checksum mismatch for ${asset}: expected ${want}, got ${got}"
	fi
	echo "Checksum verified."
fi

# --- install ---------------------------------------------------------------

install_dir="${CSM_INSTALL_DIR:-${HOME}/.local/bin}"
mkdir -p "$install_dir" || die "could not create ${install_dir}"

target="${install_dir}/csm"
chmod 0755 "${tmp}/${asset}"

# Move into place via the destination directory so the final step is a rename
# within one filesystem: an interrupted copy can leave a half-written binary on
# PATH, a rename cannot. Copy first (mv across filesystems is itself a copy)
# then rename over the target, which also succeeds while an old csm is running.
cp "${tmp}/${asset}" "${target}.new" 2>/dev/null ||
	die "could not write to ${install_dir} — set CSM_INSTALL_DIR to somewhere writable, or re-run with sudo"
chmod 0755 "${target}.new"
mv "${target}.new" "$target" || die "could not install to ${target}"

echo "Installed $("$target" -v 2>/dev/null || echo csm) to ${target}"

# --- PATH advice -----------------------------------------------------------

case ":${PATH}:" in
	*":${install_dir}:"*) ;;
	*)
		echo
		echo "${install_dir} is not on your PATH. Add it:"
		echo
		echo "  echo 'export PATH=\"${install_dir}:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
		echo
		;;
esac
