#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BUILD="$ROOT/scripts/build-csmwrap.sh"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/sylve-csmwrap-test.XXXXXX")
trap 'rm -rf "$WORK"' 0
trap 'exit 1' HUP INT TERM
OUTPUT="$WORK/bundle with spaces"
mkdir -p "$OUTPUT" "$WORK/mock"
KEY=$("$BUILD" --key)
REAL_GIT=$(command -v git)
STATUS=$("$REAL_GIT" -C "$ROOT" submodule status --recursive -- third-party/csmwrap/source)
ROOT_COMMIT=$(printf '%s\n' "$STATUS" | awk 'NR == 1 { print $1 }')
SEABIOS_COMMIT=$(printf '%s\n' "$STATUS" | awk '$2 ~ /\/seabios$/ { print $1 }')
export REAL_GIT STATUS ROOT_COMMIT SEABIOS_COMMIT
export CSMWRAP_TOOLCHAIN_ID=builder
export CSMWRAP_PYTHON="$WORK/no-python"
export CSMWRAP_CC="$WORK/no-compiler"
CHECKS=0

fail() {
	printf '%s\n' "$*" >&2
	exit 1
}

checksum() {
	case "$(uname -s)" in
		FreeBSD) sha256 -q "$1" ;;
		Linux)
			SUM=$(sha256sum "$1")
			printf '%s\n' "${SUM%% *}"
			;;
	esac
}

write_sums() (
	for FILE in CSMWrap.iso CSMWrap.iso.gz CSMWrap-build-manifest.json; do
		printf '%s  %s\n' "$(checksum "$OUTPUT/$FILE")" "$FILE"
	done > "$OUTPUT/SHA256SUMS"
)

bundle() {
	printf 'source-built ISO fixture' > "$OUTPUT/CSMWrap.iso"
	gzip -n -c "$OUTPUT/CSMWrap.iso" > "$OUTPUT/CSMWrap.iso.gz"
	jq -n --arg key "$KEY" --arg iso "$(checksum "$OUTPUT/CSMWrap.iso")" \
		--arg gzip "$(checksum "$OUTPUT/CSMWrap.iso.gz")" \
		--argjson size "$(wc -c < "$OUTPUT/CSMWrap.iso")" '
		{architecture: "x86_64", inputs_sha256: $key, toolchain: "builder",
		 sha256: $iso, size: $size, files: {"CSMWrap.iso": $iso, "CSMWrap.iso.gz": $gzip}}
	' > "$OUTPUT/CSMWrap-build-manifest.json"
	write_sums
}

update_manifest() {
	jq "$1" "$OUTPUT/CSMWrap-build-manifest.json" > "$WORK/manifest.json"
	mv "$WORK/manifest.json" "$OUTPUT/CSMWrap-build-manifest.json"
	write_sums
}

verify() {
	"$BUILD" --output "$OUTPUT" --verify
}

reject() {
	LABEL=$1
	shift
	if "$@" > "$WORK/rejection.log" 2>&1; then
		fail "accepted $LABEL"
	fi
	CHECKS=$((CHECKS + 1))
}

bundle
verify >/dev/null
CHECKS=$((CHECKS + 1))
update_manifest '.inputs_sha256 = "stale"'
reject 'stale inputs' verify
bundle
reject 'stale toolchain' env CSMWRAP_TOOLCHAIN_ID=different "$BUILD" --output "$OUTPUT" --verify
for FILE in CSMWrap.iso CSMWrap.iso.gz; do
	bundle
	printf corrupt > "$OUTPUT/$FILE"
	reject "corrupt $FILE" verify
done
bundle
update_manifest 'del(.files["CSMWrap.iso.gz"])'
reject 'incomplete manifest' verify
bundle
printf 'different ISO' | gzip -n -c > "$OUTPUT/CSMWrap.iso.gz"
HASH=$(checksum "$OUTPUT/CSMWrap.iso.gz")
update_manifest ".files[\"CSMWrap.iso.gz\"] = \"$HASH\""
reject 'different embedded ISO' verify
bundle
dd if="$OUTPUT/CSMWrap.iso.gz" of="$WORK/truncated.gz" bs=1 \
	count=$(($(wc -c < "$OUTPUT/CSMWrap.iso.gz") - 6)) 2>/dev/null
mv "$WORK/truncated.gz" "$OUTPUT/CSMWrap.iso.gz"
HASH=$(checksum "$OUTPUT/CSMWrap.iso.gz")
update_manifest ".files[\"CSMWrap.iso.gz\"] = \"$HASH\""
reject 'truncated gzip with updated checksums' verify
bundle
printf wrong > "$OUTPUT/SHA256SUMS"
reject 'checksum list mismatch' verify
for FILTER in '.architecture = "aarch64"' '.size = 0' '.size = 67108865'; do
	bundle
	update_manifest "$FILTER"
	reject "$FILTER" verify
done
bundle
rm "$OUTPUT/CSMWrap.iso.gz"
reject 'missing artifact' verify

cat > "$WORK/mock/git" <<'EOF'
#!/bin/sh
set -eu
if [ "${3:-}" = submodule ] && [ "${4:-}" = status ]; then
	case "${CSMWRAP_TEST_GIT:-}" in
		empty) exit 0 ;;
		-|+|U) printf '%s\n' "$STATUS" | sed "1s/^ /$CSMWRAP_TEST_GIT/"; exit 0 ;;
		root) printf '%s\n' "$STATUS" | sed "s/$ROOT_COMMIT/0000000000000000000000000000000000000000/"; exit 0 ;;
		seabios) printf '%s\n' "$STATUS" | sed "s/$SEABIOS_COMMIT/0000000000000000000000000000000000000000/"; exit 0 ;;
	esac
fi
if [ "${3:-}" = diff ] && [ "${CSMWRAP_TEST_GIT:-}" = dirty ]; then
	exit 1
fi
exec "$REAL_GIT" "$@"
EOF
chmod +x "$WORK/mock/git"
for MODE in root seabios; do
	CHANGED_KEY=$(PATH="$WORK/mock:$PATH" CSMWRAP_TEST_GIT="$MODE" "$BUILD" --key)
	[ "$CHANGED_KEY" != "$KEY" ] || fail "$MODE pin did not change cache key"
	CHECKS=$((CHECKS + 1))
done
for MODE in empty - + U dirty; do
	reject "$MODE source status" env PATH="$WORK/mock:$PATH" CSMWRAP_TEST_GIT="$MODE" "$BUILD" --key
done
[ "${#KEY}" -eq 64 ] || fail 'invalid cache key'
"$REAL_GIT" -C "$ROOT/third-party/csmwrap/source" ls-files --recurse-submodules -z > "$WORK/files"
tr '\000' '\n' < "$WORK/files" > "$WORK/file-list"
grep -Fxq GNUmakefile "$WORK/file-list"
grep -Fxq seabios/scripts/buildversion.py "$WORK/file-list"
if grep -Eq '(^|/)\.git(/|$)|^seabios/\.version$' "$WORK/file-list"; then
	fail 'source list includes Git metadata or distributor version'
fi
CHECKS=$((CHECKS + 1))
printf '%s CSMWrap packaging and source checks passed.\n' "$CHECKS"
