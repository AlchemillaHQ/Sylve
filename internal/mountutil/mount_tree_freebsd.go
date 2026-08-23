// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package mountutil

import (
	"context"
	"errors"

	"golang.org/x/sys/unix"
)

type freeBSDMountTreeOps struct{}

func newMountTreeOps() mountTreeOps {
	return freeBSDMountTreeOps{}
}

func (freeBSDMountTreeOps) list(ctx context.Context) ([]mountInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for {
		count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		buffer := make([]unix.Statfs_t, count+16)
		count, err = unix.Getfsstat(buffer, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		// getfsstat reports the number copied, so a full buffer may have
		// truncated mounts added between the sizing and read calls.
		if count == len(buffer) {
			continue
		}

		mounts := make([]mountInfo, 0, count)
		for _, stat := range buffer[:count] {
			mounts = append(mounts, mountInfo{
				Source: unix.ByteSliceToString(stat.Mntfromname[:]),
				Target: unix.ByteSliceToString(stat.Mntonname[:]),
				FSType: unix.ByteSliceToString(stat.Fstypename[:]),
			})
		}
		return mounts, nil
	}
}

func (freeBSDMountTreeOps) unmount(target string, force bool) error {
	flags := 0
	if force {
		flags = unix.MNT_FORCE
	}
	return unix.Unmount(target, flags)
}

func (freeBSDMountTreeOps) isBusy(err error) bool {
	return errors.Is(err, unix.EBUSY)
}

func (freeBSDMountTreeOps) isGone(err error) bool {
	return errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT)
}
