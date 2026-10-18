#!/bin/sh
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$HERE/.." && pwd)
# shellcheck source=pkgbase.conf
. "$HERE/pkgbase.conf"
. "$HERE/pkgbase.sh"

STAGE=${1:-${STAGE_DIR:-/home/freebsd-ic/stage-live}}
STAGE_PACKAGES=${STAGE_PACKAGES:-1}
case "$STAGE_PACKAGES" in 0|1) ;; *) echo "STAGE_PACKAGES must be 0 or 1" >&2; exit 1 ;; esac

if [ "$(id -u)" -ne 0 ]; then
	echo "must run as root (pkg installs into the staging root): sudo sh $0" >&2
	exit 1
fi
if [ "$STAGE_PACKAGES" = 0 ]; then
	test -x "$STAGE/sbin/init" &&
		grep -Fqx "branch=$FBSD_BRANCH" "$STAGE/var/db/freebsd-sylve/stage.conf" &&
		grep -Fqx "abi=${FBSD_ABI:-native}" "$STAGE/var/db/freebsd-sylve/stage.conf" &&
		grep -Fqx "pkgbase-repo=$PKGBASE_REPO" "$STAGE/var/db/freebsd-sylve/stage.conf" &&
		grep -Fqx "ports-repo=$PORTS_REPO" "$STAGE/var/db/freebsd-sylve/stage.conf" &&
		grep -Fqx "kmods-repo=$KMODS_REPO" "$STAGE/var/db/freebsd-sylve/stage.conf" &&
		grep -Fqx "pkgbase-packages=$PKGBASE_PACKAGES" "$STAGE/var/db/freebsd-sylve/stage.conf" &&
		grep -Fqx "live-packages=$LIVE_PACKAGES" "$STAGE/var/db/freebsd-sylve/stage.conf" || {
		echo "cached stage is missing or uses a different FreeBSD/package configuration: $STAGE" >&2
		exit 1
	}
fi

# Bootstrap pkgbase before the target's version header exists.
STAGE_OSVERSION=$((FBSD_MAJOR * 100000))
if [ -f "$STAGE/usr/include/sys/param.h" ]; then
	STAGE_OSVERSION=$(stage_osversion)
fi
export ASSUME_ALWAYS_YES=yes

echo "==> stage: $STAGE (FreeBSD $FBSD_MAJOR, $FBSD_BRANCH${FBSD_ABI:+, ABI $FBSD_ABI})"
mkdir -p "$STAGE"

# pkg resolves fingerprints inside --rootdir, before packages are installed.
echo "==> keyring"
mkdir -p "$STAGE/usr/share/keys"
[ -d "$STAGE/usr/share/keys/pkg" ] || cp -R /usr/share/keys/pkg "$STAGE/usr/share/keys/"

echo "==> package repository config"
mkdir -p "$STAGE/usr/local/etc/pkg/repos" "$STAGE/etc/pkg"
write_repos

if [ "$STAGE_PACKAGES" = 1 ]; then
	echo "==> updating package catalogues"
	stage_pkg update

	echo "==> installing pkgbase sets"
	# shellcheck disable=SC2086
	stage_pkg install -y $PKGBASE_PACKAGES
	STAGE_OSVERSION=$(stage_osversion)
	echo "==> target FreeBSD OSVERSION: $STAGE_OSVERSION"
	write_repos

	if [ -n "${LIVE_PACKAGES:-}" ]; then
		echo "==> installing kiosk packages"
		# shellcheck disable=SC2086
		stage_pkg install -y $LIVE_PACKAGES
	fi

	echo "==> removing retired kiosk remote-console packages"
	for package in wayvnc x11vnc grim; do
		if stage_pkg info -e "$package" >/dev/null 2>&1; then
			stage_pkg -o RUN_SCRIPTS=false delete -y "$package"
		fi
	done
	stage_pkg -o RUN_SCRIPTS=false autoremove -y

	# /usr/sbin/pkg is a network bootstrap shim, unusable for offline installs.
	echo "==> installing pkg into the live root"
	stage_pkg install -y pkg
fi

echo "==> applying live overlay"
tar -C "$REPO_ROOT/live" -cf - . | tar --uid 0 --gid 0 -C "$STAGE" -xpf -
rm -f "$STAGE/root/.ssh/authorized_keys" "$STAGE/var/run/freebsd-sylve/access.json"
rm -f "$STAGE/usr/local/libexec/sylve-installerd-run" \
	"$STAGE/usr/local/libexec/sylve-live-rc" \
	"$STAGE/usr/local/share/freebsd-sylve/welcome.html"
install -m 444 "$REPO_ROOT/backend/internal/api/access-words.LICENSE" "$STAGE/usr/local/share/freebsd-sylve/access-words.LICENSE"

echo "==> installing installer daemon"
if [ -f "$REPO_ROOT/out/sylve-installerd" ]; then
	install -m 555 "$REPO_ROOT/out/sylve-installerd" "$STAGE/usr/local/sbin/sylve-installerd"
	echo "    $(du -h "$REPO_ROOT/out/sylve-installerd" | awk '{print $1}') binary installed"
else
	echo "warning: $REPO_ROOT/out/sylve-installerd not found; image will lack the daemon" >&2
fi

echo "==> pkg configuration in the live root"
mkdir -p "$STAGE/usr/share/keys" "$STAGE/usr/local/etc/pkg/repos" "$STAGE/etc/pkg"
# pkgbase can overwrite the keyring installed before staging.
cp -R /usr/share/keys/pkg "$STAGE/usr/share/keys/"
write_repos

echo "==> enabling kiosk autologin on ttyv0"
if [ -f "$STAGE/etc/gettytab" ] && ! grep -q '^autologin:' "$STAGE/etc/gettytab"; then
	cat >>"$STAGE/etc/gettytab" <<'EOF'
autologin:\
	:al=root:im=:tc=default:
EOF
fi
sed -i '' 's/:al=root:tc=default:/:al=root:im=:tc=default:/' "$STAGE/etc/gettytab"
if [ -f "$STAGE/etc/ttys" ]; then
	sed -i '' '/^ttyv0[[:space:]]/s|"/usr/libexec/getty Pc"|"/usr/libexec/getty autologin"|' "$STAGE/etc/ttys"
fi

echo "==> fixing overlay permissions"
chmod 700 "$STAGE/root"
chmod 755 "$STAGE/root/.login"
chmod 755 "$STAGE/usr/local/bin/kiosk-session" "$STAGE/usr/local/bin/kiosk-launch" \
	"$STAGE/usr/local/bin/x11-kiosk-session"
chmod 644 "$STAGE/usr/local/share/freebsd-sylve/xorg-vesa.conf" \
	"$STAGE/usr/local/share/freebsd-sylve/xorg-scfb.conf"
chmod 755 "$STAGE/usr/local/share/freebsd-sylve"
chmod 555 "$STAGE/usr/local/etc/rc.d/sylve_installerd" \
	"$STAGE/etc/sylve-live-rc"

if [ -f "$STAGE/etc/syslog.conf" ]; then
	sed -i '' 's|^\(\*\.err;kern\.warning;auth\.notice;mail\.crit[[:space:]]*/dev/console\)|#\1 # disabled: kiosk VT|' "$STAGE/etc/syslog.conf"
fi

echo "==> trimming live root"
# Mesa needs LLVM shared libraries, not its toolchain or static archives.
rm -rf "$STAGE/var/cache/pkg"
rm -rf "$STAGE/usr/local/llvm19/bin" \
	"$STAGE/usr/local/llvm19/include" \
	"$STAGE/usr/local/llvm19/lib/clang" \
	"$STAGE/usr/local/llvm19/libexec" \
	"$STAGE/usr/local/llvm19/share" \
	"$STAGE/usr/local/llvm19/libdata"
find "$STAGE/usr/local/llvm19" -name '*.a' -delete 2>/dev/null || true
rm -rf "$STAGE/usr/local/include" \
	"$STAGE/usr/local/share/doc" \
	"$STAGE/usr/local/share/gtk-doc" \
	"$STAGE/usr/local/share/gir-1.0" \
	"$STAGE/usr/local/lib32"
if [ -d "$STAGE/usr/local/share/locale" ]; then
	find "$STAGE/usr/local/share/locale" -mindepth 1 -maxdepth 1 \
		! -name 'en*' ! -name 'C' -exec rm -rf {} +
fi

echo "==> recording package state"
mkdir -p "$STAGE/var/db/freebsd-sylve"
stage_pkg info >"$STAGE/var/db/freebsd-sylve/pkg-versions.txt"
{
	echo "staged-at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "branch=$FBSD_BRANCH"
	echo "abi=${FBSD_ABI:-native}"
	echo "pkgbase-repo=${PKGBASE_REPO}"
	echo "ports-repo=${PORTS_REPO}"
	echo "kmods-repo=$KMODS_REPO"
	echo "pkgbase-packages=$PKGBASE_PACKAGES"
	echo "live-packages=$LIVE_PACKAGES"
} >"$STAGE/var/db/freebsd-sylve/stage.conf"

if [ "${OFFLINE_PAYLOAD:-1}" = "1" ]; then
	echo "==> offline install payload"
	sh "$HERE/fetch-payload.sh" "$STAGE"
else
	echo "==> offline install payload skipped (OFFLINE_PAYLOAD=$OFFLINE_PAYLOAD)"
fi

echo "==> staged $(stage_pkg info | wc -l | tr -d ' ') packages, $(du -sh "$STAGE" | awk '{print $1}')"
