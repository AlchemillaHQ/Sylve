#!/bin/sh

set -eu

ARCH="${ARCH:-}"
FREEBSD_VERSION="${FREEBSD_VERSION:-15.0-RELEASE}"
FREEBSD_SYSROOT="${FREEBSD_SYSROOT:-}"

if [ -z "${ARCH}" ]; then
	echo "ARCH is required (supported: amd64, arm64)" >&2
	exit 1
fi

if [ -z "${FREEBSD_SYSROOT}" ]; then
	FREEBSD_SYSROOT=".cache/freebsd/${ARCH}-${FREEBSD_VERSION}"
fi

case "${ARCH}" in
	amd64)
		SYSROOT_URL="https://download.freebsd.org/releases/amd64/${FREEBSD_VERSION}/base.txz"
		;;
	arm64)
		SYSROOT_URL="https://download.freebsd.org/releases/arm64/${FREEBSD_VERSION}/base.txz"
		;;
	*)
		echo "Unsupported ARCH: ${ARCH}" >&2
		exit 1
		;;
esac

STAMP_FILE="${FREEBSD_SYSROOT}/.sysroot-stamp"
TMP_DIR="${FREEBSD_SYSROOT}.tmp.$$"
EXTRACT_DIR="${TMP_DIR}/sysroot"
ARCHIVE="${TMP_DIR}/base.txz"

REQUIRED_PATHS="
usr/lib/libcam.so
usr/lib/libpcap.so
usr/lib/libpam.so
usr/include/camlib.h
usr/include/net/if_bridgevar.h
usr/include/netlink/netlink_snl.h
usr/include/netlink/netlink_snl_generic.h
usr/include/netlink/netlink_sysevent.h
usr/include/pcap.h
usr/include/security/pam_appl.h
usr/include/sys/pciio.h
"

validate_sysroot_dir() {
	root="$1"
	missing=0

	for rel in ${REQUIRED_PATHS}; do
		if [ ! -e "${root}/${rel}" ]; then
			echo "Missing required sysroot path: ${root}/${rel}" >&2
			missing=1
		fi
	done

	if [ "${missing}" -ne 0 ]; then
		return 1
	fi

	return 0
}

if [ -f "${STAMP_FILE}" ]; then
	stamp_arch="$(sed -n 's/^ARCH=//p' "${STAMP_FILE}" | head -n1)"
	stamp_version="$(sed -n 's/^FREEBSD_VERSION=//p' "${STAMP_FILE}" | head -n1)"
	stamp_url="$(sed -n 's/^SYSROOT_URL=//p' "${STAMP_FILE}" | head -n1)"

	if [ "${stamp_arch}" = "${ARCH}" ] && \
		[ "${stamp_version}" = "${FREEBSD_VERSION}" ] && \
		[ "${stamp_url}" = "${SYSROOT_URL}" ] && \
		validate_sysroot_dir "${FREEBSD_SYSROOT}"; then
		echo "Using cached FreeBSD sysroot at ${FREEBSD_SYSROOT}"
		exit 0
	fi

	echo "Existing sysroot metadata is stale or incomplete, rebuilding ${FREEBSD_SYSROOT}"
fi

rm -rf "${TMP_DIR}"
mkdir -p "${TMP_DIR}" "${EXTRACT_DIR}" "$(dirname "${FREEBSD_SYSROOT}")"

echo "Downloading FreeBSD sysroot: ${SYSROOT_URL}"
curl -fL --retry 3 --retry-delay 2 --retry-connrefused "${SYSROOT_URL}" -o "${ARCHIVE}"

echo "Extracting sysroot to ${EXTRACT_DIR}"
tar -xJf "${ARCHIVE}" -C "${EXTRACT_DIR}"
rm -f "${ARCHIVE}"

if ! validate_sysroot_dir "${EXTRACT_DIR}"; then
	echo "Sysroot validation failed after extraction" >&2
	rm -rf "${TMP_DIR}"
	exit 1
fi

rm -rf "${FREEBSD_SYSROOT}"
mv "${EXTRACT_DIR}" "${FREEBSD_SYSROOT}"
rm -rf "${TMP_DIR}"

cat > "${STAMP_FILE}" <<EOF
ARCH=${ARCH}
FREEBSD_VERSION=${FREEBSD_VERSION}
SYSROOT_URL=${SYSROOT_URL}
EOF

echo "Prepared FreeBSD sysroot at ${FREEBSD_SYSROOT}"
