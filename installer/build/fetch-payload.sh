#!/bin/sh
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=pkgbase.conf
. "$HERE/pkgbase.conf"
. "$HERE/pkgbase.sh"

STAGE=${1:-${STAGE_DIR:-/home/freebsd-ic/stage-live}}
PAYLOAD_PACKAGES=${PAYLOAD_PACKAGES:-1}
case "$PAYLOAD_PACKAGES" in 0|1) ;; *) echo "PAYLOAD_PACKAGES must be 0 or 1" >&2; exit 1 ;; esac
PAYLOAD="$STAGE/var/db/freebsd-sylve/offline"
REPO="$PAYLOAD/repo"
TIP="$PAYLOAD/tip"

if [ "$(id -u)" -ne 0 ]; then
	echo "must run as root: sudo sh $0" >&2
	exit 1
fi
if [ ! -d "$STAGE/usr/local/etc/pkg/repos" ]; then
	echo "stage is missing pkg repo config (run stage-live.sh first): $STAGE" >&2
	exit 1
fi
STAGE_OSVERSION=$(stage_osversion)
if [ -z "${SYLVE_PAYLOAD_DIR:-}" ] && ! command -v jq >/dev/null 2>&1; then
	echo "jq is required to read GitHub release asset digests (pkg install jq)" >&2
	exit 1
fi

echo "==> offline payload: $PAYLOAD"
if [ "$PAYLOAD_PACKAGES" = 0 ]; then
	test -s "$REPO/meta.conf" && test -s "$REPO/packagesite.pkg" || {
		echo "cached offline package repository is incomplete: $REPO" >&2
		exit 1
	}
fi
mkdir -p "$PAYLOAD"
payload_tmp=$(mktemp -d "$PAYLOAD/.payload.XXXXXX")
trap 'rm -rf "$payload_tmp"' EXIT
trap 'exit 1' HUP INT TERM
tip_tmp="$payload_tmp/tip"
repo_input="$REPO"
mkdir "$tip_tmp"

if [ "$PAYLOAD_PACKAGES" = 1 ]; then
	repo_input="$payload_tmp/repo"
	mkdir "$repo_input"
	echo "==> resolving newest samba4 package"
	samba=$(stage_pkg search '^samba4' | awk '
	/^samba4[0-9]+-/ {
		name=$1; sub(/-.*/, "", name)
		version=substr(name, 7)+0
		if (version>max) {max=version; best=name}
	}
	END {print best}')
	[ -n "$samba" ] || {
		echo "could not resolve samba4* in the stage repositories" >&2
		exit 1
	}
	echo "    samba: $samba"

	deps="FreeBSD-set-base FreeBSD-kernel-generic pkg bhyve-firmware dnsmasq libvirt qemu-tools swtpm $samba"
	echo "==> fetching packages and dependencies"
	# shellcheck disable=SC2086
	stage_pkg fetch -y -d -o "$repo_input" $deps

	echo "==> building local repository metadata"
	pkg repo "$repo_input"
else
	samba=$(sed -n 's/.*"samba" *: *"\([^"]*\)".*/\1/p' "$PAYLOAD/manifest.json" | head -1)
	test -n "$samba" || { echo "cached payload has no Samba package identity" >&2; exit 1; }
fi

if [ -n "${SYLVE_PAYLOAD_DIR:-}" ]; then
	echo "==> using the supplied Sylve release binary and metadata"
	for asset in sylve-amd64 sylve-version.json SHA256SUMS; do
		cp "$SYLVE_PAYLOAD_DIR/$asset" "$tip_tmp/$asset"
	done
else
	echo "==> fetching Sylve tip binary and release metadata"
	release_url=https://github.com/AlchemillaHQ/Sylve/releases/download/tip
	release_json="$tip_tmp/github-release.json"
	fetch -qo "$release_json" https://api.github.com/repos/AlchemillaHQ/Sylve/releases/tags/tip
	asset_url=$(jq -er --arg name sylve-amd64 '
		.assets[] | select(.name == $name) | .browser_download_url |
		select(type == "string" and startswith("https://"))
	' "$release_json")
	asset_digest=$(jq -er --arg name sylve-amd64 '
		.assets[] | select(.name == $name) | .digest |
		capture("^sha256:(?<sum>[a-fA-F0-9]{64})$").sum | ascii_downcase
	' "$release_json")
	fetch -qo "$tip_tmp/sylve-amd64" "$asset_url"
	fetch -qo "$tip_tmp/sylve-version.json" "$release_url/sylve-version.json"
	printf '%s  sylve-amd64\n' "$asset_digest" > "$tip_tmp/SHA256SUMS"
	rm "$release_json"
fi

want=$(awk '
	$2 == "sylve-amd64" {print $1; exit}
	$1 == "SHA256" && $2 == "(sylve-amd64)" && $3 == "=" {print $4; exit}
' "$tip_tmp/SHA256SUMS" | tr 'A-F' 'a-f')
[ -n "$want" ] || {
	echo "SHA256SUMS has no sylve-amd64 entry" >&2
	exit 1
}
got=$(sha256 -q "$tip_tmp/sylve-amd64")
[ "$want" = "$got" ] || {
	echo "Sylve checksum mismatch: want $want got $got" >&2
	exit 1
}
echo "    Sylve sha256: $got"
tipver=$(sed -n 's/.*"version" *: *"\([^"]*\)".*/\1/p' "$tip_tmp/sylve-version.json" | head -1)
tipcommit=$(sed -n 's/.*"commit" *: *"\([^"]*\)".*/\1/p' "$tip_tmp/sylve-version.json" | head -1)
if [ -z "$tipver" ] || ! printf '%s\n' "$tipcommit" | grep -Eq '^[0-9a-f]{40}$'; then
	echo "Sylve release metadata is incomplete" >&2
	exit 1
fi
count=$(find "$repo_input" -name '*.pkg' ! -name 'packagesite*' | wc -l | tr -d ' ')

cat >"$payload_tmp/manifest.json" <<EOF
{
  "bakedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "packages": $count,
  "samba": "$samba",
  "tip": {
    "version": "$tipver",
    "commit": "$tipcommit"
  }
}
EOF
if [ "$PAYLOAD_PACKAGES" = 1 ]; then
	rm -rf "$REPO"
	mv "$repo_input" "$REPO"
fi
rm -rf "$TIP"
mv "$tip_tmp" "$TIP"
mv "$payload_tmp/manifest.json" "$PAYLOAD/manifest.json"

echo "==> payload: $count packages, Sylve ${tipver:-unknown} (${tipcommit:-unknown})"
du -sh "$PAYLOAD"
