#!/bin/sh

set -eu

REQUESTED_VERSION="${FREEBSD_VERSION:-${1:-15}}"
ARCH="${ARCH:-${2:-amd64}}"

case "${ARCH}" in
	amd64)
		RELEASES_URL="https://download.freebsd.org/releases/amd64/"
		;;
	arm64)
		RELEASES_URL="https://download.freebsd.org/releases/arm64/"
		;;
	*)
		echo "Unsupported ARCH: ${ARCH}" >&2
		exit 1
		;;
esac

if printf '%s\n' "${REQUESTED_VERSION}" | grep -Eq '^[0-9]+\.[0-9]+-RELEASE$'; then
	printf '%s\n' "${REQUESTED_VERSION}"
	exit 0
fi

if ! printf '%s\n' "${REQUESTED_VERSION}" | grep -Eq '^[0-9]+$'; then
	echo "FREEBSD_VERSION must be a major release (for example, 15) or an exact release (for example, 15.1-RELEASE)" >&2
	exit 1
fi

latest_version="$({
	curl -fsSL --retry 3 --retry-delay 2 --retry-connrefused "${RELEASES_URL}"
} | sed -n "s/.*href=\"\(${REQUESTED_VERSION}\.[0-9][0-9]*-RELEASE\)\/.*/\1/p" | awk -F '[.-]' '
	!found || $2 > latest_minor {
		found = 1
		latest_minor = $2
		latest = $0
	}
	END {
		if (found) print latest
	}
')"

if [ -z "${latest_version}" ]; then
	echo "No FreeBSD ${REQUESTED_VERSION}.x release found at ${RELEASES_URL}" >&2
	exit 1
fi

printf '%s\n' "${latest_version}"
