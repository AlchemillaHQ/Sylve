#!/bin/sh
# Zelta One-Shot Installer
# Downloads latest Zelta from GitHub and runs install.sh
#
# Usage: curl -fsSL https://raw.githubusercontent.com/bellhyve/zelta/main/contrib/install-from-git.sh | sh
# Or specify branch: curl ... | sh -s -- --branch=develop

set -e

REPO="https://github.com/bell-tower/zelta.git"
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

# Detect git
if ! command -v git >/dev/null 2>&1; then
    echo "Error: git is required but not found"
    exit 1
fi

# Clone to temp location
echo "Downloading Zelta from GitHub..."
git clone --depth=1 --branch="$BRANCH" "$REPO" "$WORKDIR" || {
    echo "Error: Failed to clone repository"
    exit 1
}

cd "$WORKDIR"

# Verify we got a real repo
if [ ! -f "install.sh" ] || [ ! -d ".git" ]; then
    echo "Error: Downloaded files appear incomplete"
    rm -rf "$WORKDIR"
    exit 1
fi

# Preserve commit timestamps to avoid unnecessary reinstallation
_commit_ts=$(git log -1 --format=%ct 2>/dev/null) || _commit_ts=""
if [ -n "$_commit_ts" ]; then
	# Convert Unix timestamp to touch -t format (YYYYMMDDHHMM.SS)
	# Try BSD date (-r seconds) first, then GNU date (-d @seconds)
	_touch_ts=$(date -u -r "$_commit_ts" "+%Y%m%d%H%M.%S" 2>/dev/null || \
	            date -u -d "@$_commit_ts" "+%Y%m%d%H%M.%S" 2>/dev/null || \
	            echo "")
	if [ -n "$_touch_ts" ]; then
		find . -type f -exec touch -t "$_touch_ts" {} +
	fi
fi

# Show what we're installing
echo
echo "Installing Zelta from commit: $(git rev-parse --short HEAD)"
echo

# Run the real installer (suppress its user guidance)
ZELTA_QUIET=1 sh install.sh "$@"
_exit=$?

# Post-installation guidance for non-root users
if [ "$(id -u)" -ne 0 ] && [ -z "$ZELTA_QUIET" ]; then
	echo
	echo "=========================================================="
	echo "INSTALLATION COMPLETE - ACTION REQUIRED"
	echo "=========================================================="
	echo
	echo "Zelta has been installed to user directories."
	echo "To make zelta available in this and future shell sessions,"
	echo "add the following to your shell startup file"
	echo "(~/.bashrc, ~/.zshrc, ~/.profile, etc.):"
	echo
	echo "    # Zelta configuration"
	echo "    export ZELTA_BIN=\"${ZELTA_BIN:-$HOME/bin}\""
	echo "    export ZELTA_SHARE=\"${ZELTA_SHARE:-$HOME/.local/share/zelta}\""
	echo "    export ZELTA_ETC=\"${ZELTA_ETC:-$HOME/.config/zelta}\""
	echo "    export ZELTA_DOC=\"${ZELTA_DOC:-$HOME/.local/share/zelta/doc}\""
	echo "    export PATH=\"\$ZELTA_BIN:\$PATH\""
	echo
	echo "Then reload your configuration: source ~/.bashrc"
	echo "(or the appropriate file for your shell)"
	echo "=========================================================="
	echo
fi

# Cleanup
cd /
rm -rf "$WORKDIR"

exit $_exit
