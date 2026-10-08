// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package downloadstorage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func verifyMount(path, dataset string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if unix.ByteSliceToString(stat.Fstypename[:]) != "zfs" || unix.ByteSliceToString(stat.Mntfromname[:]) != dataset {
		return fmt.Errorf("download path is not mounted from the selected dataset")
	}
	return nil
}
