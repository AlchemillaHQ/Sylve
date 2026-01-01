// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

func IsValidJailAllowedOpts(options []string) bool {
	allowedOptions := []string{
		"allow.set_hostname",
		"allow.raw_sockets",
		"allow.chflags",
		"allow.mount",
		"allow.mount.devfs",
		"allow.quotas",
		"allow.read_msgbuf",
		"allow.socket_af",
		"allow.mlock",
		"allow.nfsd",
		"allow.reserved_ports",
		"allow.unprivileged_proc_debug",
		"allow.mount.fdescfs",
		"allow.mount.fusefs",
		"allow.mount.nullfs",
		"allow.mount.procfs",
		"allow.mount.linprocfs",
		"allow.mount.linsysfs",
		"allow.mount.tmpfs",
		"allow.mount.zfs",
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
