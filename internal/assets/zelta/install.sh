#!/bin/sh
#
# Zelta Installer
#
# Note that this installer will clobber /usr/local/share/zelta, /usr/local/bin/zelta,
# and examples, but not other existing files.

if [ "$(id -u)" -eq 0 ]; then
	: ${ZELTA_BIN:="/usr/local/bin"}
	: ${ZELTA_SHARE:="/usr/local/share/zelta"}
	: ${ZELTA_ETC:="/usr/local/etc/zelta"}
	: ${ZELTA_DOC:="/usr/local/man"}
elif [ -z "$ZELTA_BIN" ] || [ -z "$ZELTA_SHARE" ] || [ -z "$ZELTA_ETC" ] || [ -z "$ZELTA_DOC" ]; then
	: ${ZELTA_BIN:="$HOME/bin"}
	: ${ZELTA_SHARE:="$HOME/.local/share/zelta"}
	: ${ZELTA_ETC:="$HOME/.config/zelta"}
	: ${ZELTA_DOC:="$ZELTA_SHARE/doc"}
fi

: ${ZELTA_CONF:="$ZELTA_ETC/zelta.conf"}
: ${ZELTA_ENV:="$ZELTA_ETC/zelta.env"}
ZELTA="$ZELTA_BIN/zelta"

copy_file() {
	if [ -z "$3" ]; then
		ZELTA_MODE="644"
	else
		ZELTA_MODE="$3"
	fi
	if [ ! -f "$2" ] || [ "$1" -nt "$2" ]; then
		echo "installing: $1 -> $2"
		cp "$1" "$2"
		chmod "$ZELTA_MODE" "$2"
	fi
}

mkdir -p "$ZELTA_BIN" "$ZELTA_SHARE" "$ZELTA_ETC" || {
    echo "Error: Failed to create directories"
    exit 1
}

copy_file bin/zelta "$ZELTA" 755
for file in share/zelta/zelta-*; do
    copy_file "$file" "${ZELTA_SHARE}/${file##*/}"
done

if [ -n "$ZELTA_DOC" ]; then
	for section in 7 8; do
		mandir="${ZELTA_DOC}/man${section}"
		mkdir -p "$mandir"
		for file in doc/*.${section}; do
			copy_file "$file" "$mandir/${file##*/}"
		done
	done
fi

# Environment and default overrides
copy_file zelta.env "${ZELTA_ENV}.example"
if [ ! -s "$ZELTA_ENV" ]; then
	copy_file zelta.env "$ZELTA_ENV"
fi

# Example zelta policy
copy_file zelta.conf "${ZELTA_CONF}.example"
if [ ! -s "$ZELTA_CONF" ]; then
	copy_file zelta.conf "$ZELTA_CONF"
fi

# Check if installed zelta will be used
_existing_zelta=$(command -v zelta 2>/dev/null || echo "")
if [ -n "$_existing_zelta" ] && [ "$_existing_zelta" != "$ZELTA" ]; then
	echo
	echo "Warning: A different 'zelta' appears first in PATH."
	echo "Installed: $ZELTA"
	echo "Found:     $_existing_zelta"
	echo "To use the newly installed version, ensure $ZELTA_BIN precedes '$(dirname "$_existing_zelta")' in PATH."
fi

# Post-installation summary for non-root users
if [ "$(id -u)" -ne 0 ] && [ -z "$ZELTA_QUIET" ]; then
	echo
	echo "Zelta installed to user directories."
	echo "To use zelta, ensure these are set in your shell startup file:"
	echo
	echo "    export ZELTA_BIN=\"$ZELTA_BIN\""
	echo "    export ZELTA_SHARE=\"$ZELTA_SHARE\""
	echo "    export ZELTA_ETC=\"$ZELTA_ETC\""
	echo "    export ZELTA_DOC=\"$ZELTA_DOC\""
	echo "    export PATH=\"\$ZELTA_BIN:\$PATH\""
	echo
fi
