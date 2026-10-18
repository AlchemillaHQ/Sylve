#!/bin/sh
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/sylve-pkgbase-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
trap 'exit 1' HUP INT TERM

for major in 15 16; do
(
	FBSD_MAJOR=$major
	FBSD_ABI=FreeBSD:$major:amd64
	PORTS_REPO=latest
	unset KMODS_REPO LIVE_PACKAGES
	. "$HERE/pkgbase.conf"
	. "$HERE/pkgbase.sh"
	STAGE="$WORK/stage $major"
	mkdir -p "$STAGE/etc/pkg" "$STAGE/usr/local/etc/pkg/repos" "$STAGE/usr/include/sys"
	write_repos
	grep -Fq 'FreeBSD-ports-kmods {' "$STAGE/etc/pkg/FreeBSD.conf"
	grep -Fq 'pkg+https://pkg.FreeBSD.org/${ABI}/kmods_latest' "$STAGE/etc/pkg/FreeBSD.conf"
	grep -Fq 'pkg+https://pkg.FreeBSD.org/${ABI}/latest' "$STAGE/etc/pkg/FreeBSD.conf"
	printf '%s\n' $LIVE_PACKAGES | grep -Fqx drm-612-kmod
	printf '%s\n' $LIVE_PACKAGES | grep -Fqx gpu-firmware-kmod
	if printf '%s\n' $LIVE_PACKAGES | grep -Fqx drm-kmod; then exit 1; fi

	pkg() { printf '%s\n' "$@" >"$WORK/args-$major"; }
	STAGE_OSVERSION=$((major * 100000))
	stage_pkg install -y FreeBSD-set-base
	grep -Fqx "OSVERSION=$STAGE_OSVERSION" "$WORK/args-$major"
	version=$((major * 100000 + 27))
	printf '#define __FreeBSD_version %s\n' "$version" >"$STAGE/usr/include/sys/param.h"
	STAGE_OSVERSION=$(stage_osversion)
	test "$STAGE_OSVERSION" = "$version"
	stage_pkg install -y drm-612-kmod
	grep -Fqx "ABI=FreeBSD:$major:amd64" "$WORK/args-$major"
	grep -Fqx "OSVERSION=$version" "$WORK/args-$major"
	grep -Fqx "$STAGE" "$WORK/args-$major"
	stage_pkg fetch -y pkg
	grep -Fqx "OSVERSION=$version" "$WORK/args-$major"

	for invalid in '' invalid "$(((major + 1) * 100000))"; do
		printf '#define __FreeBSD_version %s\n' "$invalid" >"$STAGE/usr/include/sys/param.h"
		if stage_osversion >/dev/null 2>&1; then
			echo "accepted invalid target OSVERSION: $invalid" >&2
			exit 1
		fi
	done
	rm "$STAGE/usr/include/sys/param.h"
	if stage_osversion >/dev/null 2>&1; then exit 1; fi

	PORTS_REPO=quarterly
	unset KMODS_REPO
	. "$HERE/pkgbase.conf"
	test "$KMODS_REPO" = kmods_quarterly
	write_repos
	grep -Fq 'pkg+https://pkg.FreeBSD.org/${ABI}/kmods_quarterly' "$STAGE/etc/pkg/FreeBSD.conf"
	KMODS_REPO=custom-kmods
	LIVE_PACKAGES='firefox drm-66-kmod'
	. "$HERE/pkgbase.conf"
	printf '%s\n' $LIVE_PACKAGES | grep -Fqx drm-66-kmod
	write_repos
	grep -Fq 'pkg+https://pkg.FreeBSD.org/${ABI}/custom-kmods' "$STAGE/etc/pkg/FreeBSD.conf"
)
done
mkdir -p "$WORK/bin" "$WORK/legacy/sbin" "$WORK/legacy/var/db/freebsd-sylve"
printf '#!/bin/sh\necho 0\n' >"$WORK/bin/id"
chmod +x "$WORK/bin/id"
touch "$WORK/legacy/sbin/init"
chmod +x "$WORK/legacy/sbin/init"
printf 'branch=main\nabi=FreeBSD:16:amd64\npkgbase-repo=base_latest\nports-repo=latest\n' \
	>"$WORK/legacy/var/db/freebsd-sylve/stage.conf"
if PATH="$WORK/bin:$PATH" FBSD_MAJOR=16 FBSD_BRANCH=main FBSD_ABI=FreeBSD:16:amd64 \
    STAGE_PACKAGES=0 sh "$HERE/stage-live.sh" "$WORK/legacy" >"$WORK/legacy.log" 2>&1; then
	echo 'accepted a cached stage with no kernel-module/package policy' >&2
	exit 1
fi
grep -Fq 'cached stage is missing or uses a different FreeBSD/package configuration' "$WORK/legacy.log"
echo 'Package repository, DRM dependency and target OSVERSION tests passed.'
