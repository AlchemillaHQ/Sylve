#!/bin/sh
set -eu
set -o pipefail

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=pkgbase.conf
. "$HERE/pkgbase.conf"

STAGE=${1:-${STAGE_DIR:-/home/freebsd-ic/stage-live}}
OUT=${2:-${OUT_DIR:-/home/freebsd-ic/out}}
case "$FBSD_BRANCH" in
main) channel=CURRENT ;;
stable/*) channel=STABLE ;;
*) channel=RELEASE ;;
esac
release="$FBSD_MAJOR-$channel"
label="SYLVE_${FBSD_MAJOR}_$channel"
ISO_NAME=${ISO_NAME:-freebsd-${release}-sylve-installer-amd64.iso}

if [ "$(id -u)" -ne 0 ]; then
	echo "must run as root: sudo sh $0" >&2
	exit 1
fi
mkdir -p "$OUT"
available=$(df -kP "${TMPDIR:-/tmp}" | awk 'NR == 2 {print $4}')
stage_size=$(du -sk "$STAGE" | awk '{print $1}')
case "$available:$stage_size" in *[!0-9:]*|:|:*|*:) echo "cannot determine ISO scratch space" >&2; exit 1 ;; esac
required=$((stage_size * 2 + 4194304))
if [ "$available" -lt "$required" ]; then
	echo "ISO assembly needs at least $required KiB free in ${TMPDIR:-/tmp}" >&2
	exit 1
fi

WORK=$(mktemp -d "${TMPDIR:-/tmp}/sylve-iso.XXXXXX")
iso_tmp=
sum_tmp=
cleanup() {
	status=$?
	trap - EXIT
	if [ -d "$WORK/root" ]; then chflags -R noschg "$WORK/root"; fi
	for file in "$iso_tmp" "$sum_tmp"; do
		if [ -n "$file" ]; then rm -f "$file"; fi
	done
	rm -rf "$WORK"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

root="$WORK/root"
tree="$root"
mkdir -p "$root"
echo "==> preparing $release live root"
tar -C "$STAGE" -cf - . | tar -xpf - -C "$root"
test "$(stat -f %i "$root/rescue/sh")" = "$(stat -f %i "$root/rescue/ls")"

offline="$root/var/db/freebsd-sylve/offline"
if [ -d "$offline" ] && [ ! -L "$offline" ]; then
	mkdir -p "$root/usr/local/share/freebsd-sylve"
	mv "$offline" "$root/usr/local/share/freebsd-sylve/offline"
	ln -s /usr/local/share/freebsd-sylve/offline "$offline"
fi
if [ -d "$root/boot/modules" ] && [ ! -L "$root/boot/modules" ]; then
	mkdir -p "$root/usr/local/lib"
	mv "$root/boot/modules" "$root/usr/local/lib/sylve-kmods"
	ln -s /usr/local/lib/sylve-kmods "$root/boot/modules"
fi
mkdir -p "$root/conf/base"
for directory in etc var root; do
	cp -Rp "$root/$directory" "$root/conf/base/$directory"
done
printf '\nvfs.root.mountfrom="cd9660:/dev/iso9660/%s"\nsylve.release="%s"\n' \
	"$label" "$release" >>"$root/boot/loader.conf.local"
printf '/dev/iso9660/%s / cd9660 ro 0 0\n' "$label" >>"$root/etc/fstab"
cp -p "$root/etc/fstab" "$root/conf/base/etc/fstab"

echo "==> building compressed read-only /usr filesystem"
makefs -t ffs -B little -o version=2 "$WORK/usr.ufs" "$root/usr"
# GEOM_UZIP requires every frame to expand to a complete compression cluster.
usr_size=$(stat -f %z "$WORK/usr.ufs")
truncate -s "$(((usr_size + 131071) / 131072 * 131072))" "$WORK/usr.ufs"
mkuzip -A zstd -C 3 -s 131072 -o "$root/usr.ufs.uzip" "$WORK/usr.ufs"
chflags -R noschg "$root/usr" "$root/var" "$root/root"
rm -rf "$root/usr" "$root/var" "$root/root"
mkdir -p "$root/usr" "$root/var" "$root/root"
chmod 700 "$root/root"

echo "==> building EFI system partition"
mkdir -p "$WORK/efi/EFI/BOOT"
cp "$root/boot/loader.efi" "$WORK/efi/EFI/BOOT/BOOTX64.EFI"
makefs -t msdos -s 4m -o fat_type=16 -o sectors_per_cluster=1 \
	-o volume_label=EFI "$WORK/efi.img" "$WORK/efi"
fsck_msdosfs -n "$WORK/efi.img"

echo "==> building BIOS/UEFI El Torito ISO"
iso_tmp=$(mktemp "$OUT/.sylve-iso.XXXXXX")
makefs -D -t cd9660 -o rockridge -o label="$label" \
	-o "bootimage=i386;$tree/boot/cdboot" -o no-emul-boot \
	-o "bootimage=i386;$WORK/efi.img" -o no-emul-boot -o platformid=efi \
	"$iso_tmp" "$tree"

esp_start=
esp_size=
for entry in $(etdump --format shell "$iso_tmp"); do
	eval "$entry"
	if [ "$et_platform" = efi ]; then
		esp_start=$((et_lba * 2048))
		esp_size=$((et_sectors * 512))
		break
	fi
done
test -n "$esp_start" && test -n "$esp_size"
iso_size=$(stat -f %z "$iso_tmp")
mkimg -s gpt --capacity "$iso_size" -b "$root/boot/pmbr" \
	-p "freebsd-boot:=$root/boot/isoboot" \
	-p "efi::$esp_size:$esp_start" -o "$WORK/hybrid.img"
dd if="$WORK/hybrid.img" of="$iso_tmp" bs=32k count=1 conv=notrunc status=none

sum_tmp=$(mktemp "$OUT/.sylve-sha256.XXXXXX")
sha256 -q "$iso_tmp" >"$sum_tmp"
chmod =rw "$iso_tmp" "$sum_tmp"
mv "$iso_tmp" "$OUT/$ISO_NAME"
iso_tmp=
mv "$sum_tmp" "$OUT/$ISO_NAME.sha256"
sum_tmp=
echo "==> $OUT/$ISO_NAME"
ls -lh "$OUT/$ISO_NAME"
