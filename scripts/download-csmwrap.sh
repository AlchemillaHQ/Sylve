#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT="$ROOT/internal/assets/csmwrap"
URL="https://github.com/AlchemillaHQ/Sylve/releases/download/tip"

if [ "${CI+x}" = x ]; then
	printf '%s\n' 'CSMWrap downloads are disabled in CI; stage the source-built bundle instead.' >&2
	exit 1
fi

WORK=$(mktemp -d "${TMPDIR:-/tmp}/sylve-csmwrap-download.XXXXXX")
trap 'rm -rf "$WORK"' 0
trap 'exit 1' HUP INT TERM

for FILE in CSMWrap.iso.gz CSMWrap-build-manifest.json; do
	if command -v fetch >/dev/null 2>&1; then
		fetch -q -o "$WORK/$FILE" "$URL/$FILE"
	else
		curl -fL -o "$WORK/$FILE" "$URL/$FILE"
	fi
done

mkdir -p "$OUTPUT"
cp "$WORK/CSMWrap.iso.gz" "$WORK/CSMWrap-build-manifest.json" "$OUTPUT/"
printf '%s\n' 'CSMWrap: downloaded tip helper.'
