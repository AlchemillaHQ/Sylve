#!/bin/sh
# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.

set -eu

owner_property='org.alchemilla:sylve-test-owner'
leaked=0

pools="$(zpool list -H -o name)"
for pool in $pools; do
	case "$pool" in
	sylve-test-*)
		if [ "$leaked" -eq 0 ]; then
			echo "Leaked ZFS test pools:" >&2
		fi
		owner="$(zfs get -H -o value,source "$owner_property" "$pool" 2>&1 || true)"
		printf '  %s owner=%s\n' "$pool" "$owner" >&2
		leaked=1
		;;
	esac
done

state_reported=0
for state_dir in /tmp/sylve-zfstest-*; do
	[ -e "$state_dir" ] || continue
	if [ "$state_reported" -eq 0 ]; then
		echo "Leaked ZFS test fixture state:" >&2
		state_reported=1
	fi
	printf '  %s\n' "$state_dir" >&2
	leaked=1
done

exit "$leaked"
