// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func removeFileExplorerTree(ctx context.Context, path string) error {
	path = filepath.Clean(path)
	parent := filepath.Dir(path)
	parentFd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: parent, Err: err}
	}
	defer unix.Close(parentFd)
	var parentStat unix.Stat_t
	if err := unix.Fstat(parentFd, &parentStat); err != nil {
		return &os.PathError{Op: "stat", Path: parent, Err: err}
	}
	return removeFileExplorerAt(ctx, parentFd, parentStat.Dev, filepath.Base(path), path)
}

func removeFileExplorerAt(ctx context.Context, parentFd int, device uint64, name, display string) error {
	if err := ctx.Err(); err != nil {
		return wrapFileExplorerError(ErrFileExplorerCancelled, display, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return &os.PathError{Op: "stat", Path: display, Err: err}
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlinkat(parentFd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return &os.PathError{Op: "unlink", Path: display, Err: err}
		}
		return nil
	}
	if stat.Dev != device {
		return &FileExplorerProtectedError{Path: display, Reason: FileExplorerProtectedMountPoint}
	}

	fd, err := unix.Openat(parentFd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return &os.PathError{Op: "open", Path: display, Err: err}
	}
	directory := os.NewFile(uintptr(fd), display)
	defer directory.Close()
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return &os.PathError{Op: "stat", Path: display, Err: err}
	}
	if opened.Dev != stat.Dev || opened.Ino != stat.Ino {
		return &os.PathError{Op: "open", Path: display, Err: unix.EBUSY}
	}
	names, err := directory.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, child := range names {
		if err := removeFileExplorerAt(ctx, fd, device, child, filepath.Join(display, child)); err != nil {
			return err
		}
	}
	if err := unix.Unlinkat(parentFd, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return &os.PathError{Op: "rmdir", Path: display, Err: err}
	}
	return nil
}
