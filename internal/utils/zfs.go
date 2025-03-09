// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"strings"
	zfsServiceInterfaces "sylve/internal/interfaces/services/zfs"
)

func ParseZpoolListOutput(output string) (*zfsServiceInterfaces.Zpool, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	zpool := &zfsServiceInterfaces.Zpool{}

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 7 {
			continue
		}

		zpool.Name = parts[0]
		zpool.Health = parts[1]
		zpool.Allocated = StringToUint64(parts[2])
		zpool.Size = StringToUint64(parts[3])
		zpool.Free = StringToUint64(parts[4])
		zpool.ReadOnly = parts[5] == "on"
		zpool.Freeing = StringToUint64(parts[6])
		zpool.Leaked = StringToUint64(parts[7])
		zpool.DedupRatio = StringToFloat64(parts[8])
	}

	return zpool, nil
}
