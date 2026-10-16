// SPDX-License-Identifier: BSD-2-Clause
//go:build !freebsd

package pkgmanager

import (
	"context"
	"fmt"
)

func ValidatePrivateRoot(context.Context, Target) (bool, error) {
	return false, fmt.Errorf("jail_packages_require_freebsd")
}

func ValidatePackagePaths(context.Context, Target, []string) error {
	return fmt.Errorf("jail_packages_require_freebsd")
}
