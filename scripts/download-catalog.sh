#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT="$ROOT/internal/assets/catalog/daemonless"
URL="https://catalog.daemonless.io/v1/daemonless"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/sylve-catalog-download.XXXXXX")
trap 'rm -rf "$WORK"' 0
trap 'exit 1' HUP INT TERM

download() {
	mkdir -p "$(dirname "$WORK/$1")"
	if command -v fetch >/dev/null 2>&1; then
		fetch -q -o "$WORK/$1" "$URL/$1"
	else
		curl -fsSL -o "$WORK/$1" "$URL/$1"
	fi
}

download catalog.json
FILES=$(sed -nE 's/^ *"(icon|manifest_url)": "([^"]*)",?$/\2/p' "$WORK/catalog.json" |
	grep -E '^(icon\.(svg|png)|(icons|manifests)/[A-Za-z0-9][A-Za-z0-9._-]*)$' | sort -u)
for FILE in $FILES; do
	download "$FILE" || {
		rm -f "$WORK/$FILE"
		printf '%s\n' "Catalog: skipped $FILE" >&2
	}
done

rm -rf "$OUTPUT"
mkdir -p "$OUTPUT"
cp -R "$WORK/." "$OUTPUT/"
printf '%s\n' 'Catalog: downloaded the built-in copy.'
