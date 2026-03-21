# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.

DSNAME="$1"
KEEP_MIN=10
KEEP_DAYS=90
[ -z "$1" ] && exit
KEEP_DAYS=$(($(date +%s)-60*60*24*KEEP_DAYS))

zfs list -Hprtsnap -oname,creation -d1 -screation "$DSNAME" | awk -v min=$KEEP_MIN -v days=$KEEP_DAYS '
{
	if ($2<days) {
		x[++y]=$1
		if (x[y-min]) z = x[y-min]
	}
}
END {
	if (z) {
		sub(/@/, "@%", z)
		print "zfs destroy -vrn \"" z "\""
	}
}'
