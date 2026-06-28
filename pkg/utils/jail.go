// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import "slices"

func IsValidJailAllowedOpts(options []string) bool {
	allowedOptions := []string{
		"allow.adjtime",
		"allow.chflags",
		"allow.extattr",
		"allow.mlock",
		"allow.mount",
		"allow.mount.devfs",
		"allow.mount.fdescfs",
		"allow.mount.fusefs",
		"allow.mount.linprocfs",
		"allow.mount.linsysfs",
		"allow.mount.nullfs",
		"allow.mount.procfs",
		"allow.mount.tmpfs",
		"allow.mount.zfs",
		"allow.nfsd",
		"allow.quotas",
		"allow.raw_sockets",
		"allow.read_msgbuf",
		"allow.reserved_ports",
		"allow.routing",
		"allow.set_hostname",
		"allow.setaudit",
		"allow.settime",
		"allow.socket_af",
		"allow.suser",
		"allow.sysvipc",
		"allow.unprivileged_parent_tampering",
		"allow.unprivileged_proc_debug",
		"allow.vmm",
	}

	for _, option := range options {
		found := false
		for _, allowed := range allowedOptions {
			if option == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func FilterDevFSFromOptions(options []string) []string {
	return slices.DeleteFunc(options, func(s string) bool {
		return s == "allow.mount.devfs"
	})
}
