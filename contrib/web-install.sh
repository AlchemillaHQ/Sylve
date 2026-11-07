#!/bin/sh
# Zelta Web Installer
# Downloads Zelta from GitHub and runs install.sh
#
# Usage: curl -fsSL https://zelta.space/web-install.sh | sh
# Or specify branch: curl -fsSL https://zelta.space/web-install.sh | sh -s -- --branch=main
# Source: https://github.com/bell-tower/zelta/blob/main/contrib/web-install.sh

set -e

REPO_ARCHIVE="https://github.com/bell-tower/zelta/archive/refs/heads"
# Parse branch argument: supports 'main', '--branch=main', or '-b=main'
BRANCH="main"
while [ $# -gt 0 ]; do
	case "$1" in
		--branch=*|-b=*)
			BRANCH="${1#*=}"
			shift
			;;
		*)
			break
			;;
	esac
done
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$TMPDIR/zelta-install-$$"
ARCHIVE="$WORKDIR/zelta.tar.gz"

cleanup() {
	cd / >/dev/null 2>&1 || true
	rm -rf "$WORKDIR"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$WORKDIR" || {
	echo "Error: Failed to create temporary directory"
	exit 1
}

download_archive() {
	_url="$1"
	_output="$2"

	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$_url" -o "$_output"
	elif command -v fetch >/dev/null 2>&1; then
		fetch -qo "$_output" "$_url"
	else
		echo "Error: curl or fetch is required but not found"
		return 1
	fi
}

# Download and unpack the branch archive. GitHub accepts branch names with
# slashes in this URL form, e.g. release/bsdcan2026.tar.gz.
echo "Downloading Zelta branch '$BRANCH'..."
download_archive "$REPO_ARCHIVE/$BRANCH.tar.gz" "$ARCHIVE" || {
	echo "Error: Failed to download repository archive"
	exit 1
}

echo "Extracting archive..."
tar -xzf "$ARCHIVE" -C "$WORKDIR" || {
	echo "Error: Failed to extract repository archive"
	exit 1
}

SRCDIR=""
for dir in "$WORKDIR"/zelta-*; do
	if [ -d "$dir" ]; then
		SRCDIR="$dir"
		break
	fi
done

if [ -z "$SRCDIR" ]; then
	echo "Error: Downloaded files appear incomplete"
	exit 1
fi

cd "$SRCDIR"

if [ ! -f "install.sh" ]; then
	echo "Error: Downloaded files appear incomplete"
	exit 1
fi

sh install.sh "$@"
