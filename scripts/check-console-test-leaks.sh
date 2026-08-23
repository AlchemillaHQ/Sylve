#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.

set -eu

owner_property='org.alchemillahq:sylve_console_test_run'
leaked=0

pools="$(zpool list -H -o name)"
for pool in $pools; do
	case "$pool" in
	sylve-console-it-*)
		if [ "$leaked" -eq 0 ]; then
			echo "Leaked console acceptance resources:" >&2
		fi
		owner="$(zfs get -H -o value,source "$owner_property" "$pool" 2>&1 || true)"
		printf '  pool %s owner=%s\n' "$pool" "$owner" >&2
		leaked=1
		;;
	esac
done

for state_dir in /tmp/sylve-console-it-*; do
	[ -e "$state_dir" ] || continue
	if [ "$leaked" -eq 0 ]; then
		echo "Leaked console acceptance resources:" >&2
	fi
	printf '  temporary directory %s\n' "$state_dir" >&2
	leaked=1
done

for pool_link in /sylve-console-it-*; do
	[ -e "$pool_link" ] || [ -L "$pool_link" ] || continue
	if [ "$leaked" -eq 0 ]; then
		echo "Leaked console acceptance resources:" >&2
	fi
	printf '  root path %s\n' "$pool_link" >&2
	leaked=1
done

if [ "$leaked" -ne 0 ]; then
	echo "Leak reporting is read-only; no resource was removed." >&2
fi

exit "$leaked"
