// SPDX-License-Identifier: BSD-2-Clause
//go:build freebsd

package pkgmanager

import "golang.org/x/sys/unix"

func hostOSVersion() (uint32, error) {
	return unix.SysctlUint32("kern.osreldate")
}
