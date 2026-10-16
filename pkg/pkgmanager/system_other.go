// SPDX-License-Identifier: BSD-2-Clause
//go:build !freebsd

package pkgmanager

import "fmt"

func hostOSVersion() (uint32, error) {
	return 0, fmt.Errorf("jail_packages_require_freebsd")
}
