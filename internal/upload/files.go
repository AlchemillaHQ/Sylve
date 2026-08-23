// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package upload

import (
	"errors"
	"os"
	"syscall"
)

var (
	ErrNotRegularFile = errors.New("upload_not_regular_file")
	ErrFileReplaced   = errors.New("upload_file_replaced")
)

type FileIdentity struct {
	Device int64
	Inode  int64
}

func IdentityFromFileInfo(info os.FileInfo) (FileIdentity, error) {
	if info == nil || !info.Mode().IsRegular() {
		return FileIdentity{}, ErrNotRegularFile
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return FileIdentity{}, errors.New("upload_file_identity_unavailable")
	}

	return FileIdentity{
		Device: int64(stat.Dev),
		Inode:  int64(stat.Ino),
	}, nil
}

func MatchesFileInfo(identity FileIdentity, info os.FileInfo) bool {
	if identity.Device == 0 && identity.Inode == 0 {
		return false
	}

	actual, err := IdentityFromFileInfo(info)
	if err != nil {
		return false
	}
	return actual == identity
}

// RemoveIfSame removes path only when it still identifies the regular file
// recorded at upload time.
func RemoveIfSame(path string, identity FileIdentity) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, ErrNotRegularFile
	}
	if !MatchesFileInfo(identity, info) {
		return false, ErrFileReplaced
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
