#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE="$ROOT/third-party/csmwrap/source"
OUTPUT="$ROOT/.cache/csmwrap"
OS=$(uname -s)
KEY_ONLY=false
VERIFY_ONLY=false
STAGE=false

fail() {
	printf '%s\n' "$*" >&2
	exit 1
}

checksum() {
	case "$OS" in
		FreeBSD) sha256 -q "$@" ;;
		Linux)
			SUM=$(sha256sum "$@") || return 1
			printf '%s\n' "${SUM%% *}"
			;;
	esac
}

file_size() {
	case "$OS" in
		FreeBSD) stat -f '%z' "$1" ;;
		Linux) stat -c '%s' "$1" ;;
	esac
}

verify() {
	for FILE in CSMWrap.iso CSMWrap.iso.gz CSMWrap-build-manifest.json SHA256SUMS; do
		[ -f "$OUTPUT/$FILE" ] || return 1
	done
	ISO_SIZE=$(file_size "$OUTPUT/CSMWrap.iso") || return 1
	ISO_HASH=$(checksum "$OUTPUT/CSMWrap.iso") || return 1
	GZIP_HASH=$(checksum "$OUTPUT/CSMWrap.iso.gz") || return 1
	MANIFEST_HASH=$(checksum "$OUTPUT/CSMWrap-build-manifest.json") || return 1
	jq -e --arg inputs "$INPUT_SHA" --arg toolchain "${CSMWRAP_TOOLCHAIN_ID:-}" \
		--arg iso "$ISO_HASH" --arg gzip "$GZIP_HASH" --argjson size "$ISO_SIZE" '
		.architecture == "x86_64" and .inputs_sha256 == $inputs and
		.size == $size and .size > 0 and .size <= 67108864 and .sha256 == $iso and
		.files == {"CSMWrap.iso": $iso, "CSMWrap.iso.gz": $gzip} and
		($toolchain == "" or .toolchain == $toolchain)
	' "$OUTPUT/CSMWrap-build-manifest.json" >/dev/null || return 1
	gzip -t "$OUTPUT/CSMWrap.iso.gz" || return 1
	[ "$(gzip -dc "$OUTPUT/CSMWrap.iso.gz" | checksum)" = "$ISO_HASH" ] || return 1
	printf '%s  %s\n' "$ISO_HASH" CSMWrap.iso "$GZIP_HASH" CSMWrap.iso.gz \
		"$MANIFEST_HASH" CSMWrap-build-manifest.json | cmp -s - "$OUTPUT/SHA256SUMS"
}

tool_version() {
	"$1" --version 2>&1 | sed -n '1p'
}

build() (
	case "$OS" in
		FreeBSD)
			MAKE=${CSMWRAP_MAKE:-gmake}
			CC=${CSMWRAP_CC:-gcc14}
			LD=${CSMWRAP_LD:-/usr/local/bin/ld.bfd}
			OBJCOPY=${CSMWRAP_OBJCOPY:-/usr/local/bin/objcopy}
			OBJDUMP=${CSMWRAP_OBJDUMP:-/usr/local/bin/objdump}
			STRIP=${CSMWRAP_STRIP:-/usr/local/bin/strip}
			FORMATTER=${CSMWRAP_FORMATTER:-newfs_msdos}
			JOBS=$(sysctl -n hw.ncpu)
			;;
		Linux)
			MAKE=${CSMWRAP_MAKE:-make}
			CC=${CSMWRAP_CC:-gcc}
			LD=${CSMWRAP_LD:-ld.bfd}
			OBJCOPY=${CSMWRAP_OBJCOPY:-objcopy}
			OBJDUMP=${CSMWRAP_OBJDUMP:-objdump}
			STRIP=${CSMWRAP_STRIP:-strip}
			FORMATTER=${CSMWRAP_FORMATTER:-mkfs.fat}
			JOBS=$(getconf _NPROCESSORS_ONLN)
			;;
	esac
	PYTHON=${CSMWRAP_PYTHON:-python3}
	if [ -z "${CSMWRAP_PYTHON:-}" ] && ! command -v "$PYTHON" >/dev/null 2>&1; then
		PYTHON=python3.11
	fi
	for TOOL in "$MAKE" "$CC" "$LD" "$OBJCOPY" "$OBJDUMP" "$STRIP" "$PYTHON" \
		"$FORMATTER" nasm xxd mcopy mdir xorriso file tar gzip; do
		command -v "$TOOL" >/dev/null 2>&1 || fail "missing build tool: $TOOL"
	done
	mkdir -p "$OUTPUT"
	WORK=$(mktemp -d "$OUTPUT/build.XXXXXX")
	trap 'rm -rf "$WORK"' 0
	trap 'exit 1' HUP INT TERM
	TREE="$WORK/source"
	mkdir -p "$TREE"
	git -C "$SOURCE" ls-files --recurse-submodules -z > "$WORK/files"
	tar -cf "$WORK/source.tar" -C "$SOURCE" --null -T "$WORK/files"
	tar -xf "$WORK/source.tar" -C "$TREE"
	VERSION=$(printf '%s\n' "$SOURCE_INFO" | jq -r '.version')
	SEABIOS_VERSION=$(printf '%s\n' "$SOURCE_INFO" | jq -r '.submodules.seabios[:8]')
	printf '%s\n' "$SEABIOS_VERSION" > "$TREE/seabios/.version"
	if ! (
		cd "$TREE" || exit 1
		"$MAKE" -j "$JOBS" ARCH=x86_64 "BUILD_VERSION=$VERSION" \
			"CC=$CC" "HOST_CC=$CC" "LD=$LD" "OBJCOPY=$OBJCOPY" \
			"OBJDUMP=$OBJDUMP" "STRIP=$STRIP" "PYTHON=$PYTHON"
	) > "$OUTPUT/build.log" 2>&1; then
		fail "CSMWrap build failed; see $OUTPUT/build.log"
	fi
	EFI="$TREE/bin-x86_64/csmwrap.efi"
	KIND=$(file "$EFI")
	case "$KIND" in
		*PE32+*x86-64*) ;;
		*) fail "unexpected EFI format: $KIND" ;;
	esac
	mkdir -p "$WORK/efi/EFI/BOOT" "$WORK/iso"
	cp "$EFI" "$WORK/efi/EFI/BOOT/BOOTX64.EFI"
	cp "$ROOT/third-party/csmwrap/csmwrap.ini" "$WORK/efi/EFI/BOOT/csmwrap.ini"
	FAT="$WORK/iso/efi.img"
	case "$OS" in
		FreeBSD) "$FORMATTER" -C 8m -F 16 -c 2 -L CSMWRAP -I 0x43534d57 "$FAT" ;;
		Linux)
			truncate -s 8388608 "$FAT"
			"$FORMATTER" -F 16 -s 2 -n CSMWRAP -i 43534d57 "$FAT"
			;;
	esac
	mcopy -s -i "$FAT" "$WORK/efi/EFI" ::
	mdir -i "$FAT" ::/EFI/BOOT
	xorriso -as mkisofs -V CSMWRAP -e efi.img -no-emul-boot \
		-isohybrid-gpt-basdat -part_like_isohybrid -o "$OUTPUT/CSMWrap.iso" "$WORK/iso"
	xorriso -indev "$OUTPUT/CSMWrap.iso" -report_el_torito plain \
		> "$OUTPUT/boot-catalog.txt" 2>&1
	grep -q UEFI "$OUTPUT/boot-catalog.txt" && grep -q /efi.img "$OUTPUT/boot-catalog.txt" \
		|| fail "missing UEFI El Torito boot image"
	gzip -n -c "$OUTPUT/CSMWrap.iso" > "$OUTPUT/CSMWrap.iso.gz"
	ISO_SIZE=$(file_size "$OUTPUT/CSMWrap.iso")
	ISO_HASH=$(checksum "$OUTPUT/CSMWrap.iso")
	GZIP_HASH=$(checksum "$OUTPUT/CSMWrap.iso.gz")
	jq -n --argjson source "$SOURCE_INFO" --arg inputs "$INPUT_SHA" \
		--arg toolchain "${CSMWRAP_TOOLCHAIN_ID:-$(uname -sr)}" \
		--arg make "$(tool_version "$MAKE")" --arg gcc "$(tool_version "$CC")" \
		--arg binutils "$(tool_version "$LD")" --arg nasm "$(tool_version nasm)" \
		--arg xorriso "$(tool_version xorriso)" --arg mtools "$(tool_version mcopy)" \
		--rawfile configuration "$ROOT/third-party/csmwrap/csmwrap.ini" \
		--argjson size "$ISO_SIZE" --arg iso "$ISO_HASH" --arg gzip "$GZIP_HASH" '
		{architecture: "x86_64", source: $source, inputs_sha256: $inputs,
		 toolchain: $toolchain, tools: {make: $make, gcc: $gcc, binutils: $binutils,
		 nasm: $nasm, xorriso: $xorriso, mtools: $mtools}, configuration: $configuration,
		 size: $size, sha256: $iso, files: {"CSMWrap.iso": $iso, "CSMWrap.iso.gz": $gzip}}
	' > "$OUTPUT/CSMWrap-build-manifest.json"
	MANIFEST_HASH=$(checksum "$OUTPUT/CSMWrap-build-manifest.json")
	printf '%s  %s\n' "$ISO_HASH" CSMWrap.iso "$GZIP_HASH" CSMWrap.iso.gz \
		"$MANIFEST_HASH" CSMWrap-build-manifest.json > "$OUTPUT/SHA256SUMS"
)

while [ "$#" -gt 0 ]; do
	case "$1" in
		--key) KEY_ONLY=true ;;
		--verify) VERIFY_ONLY=true ;;
		--stage) STAGE=true ;;
		--output)
			[ "$#" -gt 1 ] || fail "--output requires a directory"
			OUTPUT=$2
			shift
			;;
		--output=*) OUTPUT=${1#*=} ;;
		-h|--help)
			printf 'Usage: %s [--key] [--verify] [--stage] [--output directory]\n' "$0"
			exit 0
			;;
		*) fail "unknown argument: $1" ;;
	esac
	shift
done
case "$OS" in
	Linux|FreeBSD) ;;
	*) fail "unsupported build host: $OS" ;;
esac
for TOOL in git jq; do
	command -v "$TOOL" >/dev/null 2>&1 || fail "missing tool: $TOOL"
done
STATUS=$(git -C "$ROOT" submodule status --recursive -- third-party/csmwrap/source)
if [ -z "$STATUS" ] || printf '%s\n' "$STATUS" | grep -q '^[^ ]'; then
	fail "CSMWrap submodules are not initialized at their pins; run git submodule update --init --recursive"
fi
git -C "$SOURCE" diff --quiet HEAD --ignore-submodules=untracked \
	|| fail "CSMWrap pinned sources have local changes"
SOURCE_INFO=$(printf '%s\n' "$STATUS" | jq -RncS --arg prefix third-party/csmwrap/source/ '
	[inputs | split(" ") | map(select(length > 0))] as $pins |
	{commit: $pins[0][0], version: ($pins[0][0][:8] + "-sylve"),
	 submodules: ($pins[1:] | map({key: (.[1] | ltrimstr($prefix)), value: .[0]}) | from_entries)}
')
KEY_INPUTS=$SOURCE_INFO
for FILE in .gitmodules third-party/csmwrap/csmwrap.ini scripts/build-csmwrap.sh \
	.github/actions/csmwrap/action.yaml; do
	FILE_HASH=$(checksum "$ROOT/$FILE")
	KEY_INPUTS="$KEY_INPUTS
$FILE $FILE_HASH"
done
INPUT_SHA=$(printf '%s\n' "$KEY_INPUTS" | checksum)
if [ "$KEY_ONLY" = true ]; then
	printf '%s\n' "$INPUT_SHA"
	exit 0
fi
case "$OUTPUT" in
	/*) ;;
	*) OUTPUT="$PWD/$OUTPUT" ;;
esac
if ! verify; then
	[ "$VERIFY_ONLY" = false ] || fail "CSMWrap bundle is stale, incomplete, or corrupt"
	build
	verify || fail "CSMWrap built bundle failed verification"
fi
if [ "$STAGE" = true ]; then
	mkdir -p "$ROOT/internal/assets/csmwrap"
	cp "$OUTPUT/CSMWrap.iso.gz" "$OUTPUT/CSMWrap-build-manifest.json" "$ROOT/internal/assets/csmwrap/"
fi
printf 'CSMWrap.iso: %s bytes, SHA256 %s\n' "$ISO_SIZE" "$ISO_HASH"
