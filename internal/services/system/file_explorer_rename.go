// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	sysRenameat2      = 602
	atRenameNoReplace = 0x1
)

var renameat2Unavailable atomic.Bool

func renameat2NoReplace(oldPath, newPath string) error {
	if renameat2Unavailable.Load() {
		return syscall.ENOSYS
	}
	from, err := syscall.BytePtrFromString(oldPath)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(newPath)
	if err != nil {
		return err
	}
	cwd := unix.AT_FDCWD
	_, _, errno := syscall.Syscall6(
		sysRenameat2,
		uintptr(cwd),
		uintptr(unsafe.Pointer(from)),
		uintptr(cwd),
		uintptr(unsafe.Pointer(to)),
		atRenameNoReplace,
		0,
	)
	if errno == syscall.ENOSYS {
		renameat2Unavailable.Store(true)
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func renameNoReplace(oldPath, newPath string) error {
	err := renameat2NoReplace(oldPath, newPath)
	if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EOPNOTSUPP) {
		if err != nil {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
		}
		return nil
	}

	info, err := os.Lstat(oldPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.Mkdir(newPath, 0o700); err != nil {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: errors.Unwrap(err)}
		}
		if err := syscall.Rename(oldPath, newPath); err != nil {
			_ = syscall.Rmdir(newPath)
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
		}
		return nil
	}

	cwd := unix.AT_FDCWD
	if err := unix.Linkat(cwd, oldPath, cwd, newPath, 0); err != nil {
		if errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.EXDEV) {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
		}
		if _, statErr := os.Lstat(newPath); statErr == nil {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EEXIST}
		}
		return os.Rename(oldPath, newPath)
	}
	if err := os.Remove(oldPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	return nil
}
