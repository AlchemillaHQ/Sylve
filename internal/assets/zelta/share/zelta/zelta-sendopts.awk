#!/usr/bin/awk -f
# SPDX-License-Identifier: BSD-2-Clause
#
# Copyright (c) 2025 The FreeBSD Foundation.
#
# This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
# of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
# under sponsorship from the FreeBSD Foundation.


function get_send_cmd(host) {
	return ((host == "localhost" || !host) ? "" : "ssh "host" ") "zfs send"
}

BEGIN {
	ALL_OUT = " 2>&1"
	cmd1 = get_send_cmd(ARGV[1])
	cmd2 = get_send_cmd(ARGV[2])
	cmd = cmd1 ALL_OUT " & " cmd2 ALL_OUT
	while (cmd | getline) {
		if (sub(/\].*I.*snapshot\].*/,"") && sub(/.*\[-/,"")) {
			split ($0, options, "")
			for (i in options) opt_list[options[i]]++
		}
	}
	for (i in opt_list) {
		if (opt_list[i] > 1) printf i
	}
	print ""
}
