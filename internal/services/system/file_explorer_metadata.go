// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"io/fs"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	sysAclGetLink  = 425
	sysAclSetLink  = 426
	aclMaxEntries  = 254
	aclTypeAccess  = 2
	aclTypeDefault = 3
	aclTypeNFS4    = 4
)

type fileExplorerACLEntry struct {
	Tag       uint32
	ID        uint32
	Perm      uint32
	EntryType uint16
	Flags     uint16
}

type fileExplorerACL struct {
	MaxCount uint32
	Count    uint32
	Spare    [4]int32
	Entries  [aclMaxEntries]fileExplorerACLEntry
}

func fileExplorerACLCall(number uintptr, path string, kind int, acl *fileExplorerACL) error {
	pointer, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall(number, uintptr(unsafe.Pointer(pointer)), uintptr(kind), uintptr(unsafe.Pointer(acl)))
	if errno != 0 {
		return errno
	}
	return nil
}

func copyFileExplorerACL(source, target string, directory bool) {
	kinds := []int{aclTypeNFS4, aclTypeAccess}
	if directory {
		kinds = append(kinds, aclTypeDefault)
	}
	for _, kind := range kinds {
		acl := fileExplorerACL{MaxCount: aclMaxEntries}
		if err := fileExplorerACLCall(sysAclGetLink, source, kind, &acl); err != nil {
			continue
		}
		_ = fileExplorerACLCall(sysAclSetLink, target, kind, &acl)
		if kind == aclTypeNFS4 {
			return
		}
	}
}

func copyFileExplorerExtattrs(source, target string) {
	size, err := unix.ExtattrListLink(source, unix.EXTATTR_NAMESPACE_USER, 0, 0)
	if err != nil || size <= 0 {
		return
	}
	list := make([]byte, size)
	size, err = unix.ExtattrListLink(source, unix.EXTATTR_NAMESPACE_USER, uintptr(unsafe.Pointer(&list[0])), len(list))
	if err != nil {
		return
	}
	list = list[:size]
	for index := 0; index < len(list); {
		length := int(list[index])
		if index+1+length > len(list) {
			return
		}
		name := string(list[index+1 : index+1+length])
		index += 1 + length

		valueSize, err := unix.ExtattrGetLink(source, unix.EXTATTR_NAMESPACE_USER, name, 0, 0)
		if err != nil {
			continue
		}
		value := make([]byte, valueSize+1)
		valueSize, err = unix.ExtattrGetLink(source, unix.EXTATTR_NAMESPACE_USER, name, uintptr(unsafe.Pointer(&value[0])), valueSize)
		if err != nil {
			continue
		}
		_, _ = unix.ExtattrSetLink(target, unix.EXTATTR_NAMESPACE_USER, name, uintptr(unsafe.Pointer(&value[0])), valueSize)
	}
}

func applyFileExplorerMetadata(source, target string, info fs.FileInfo) error {
	stat, _ := info.Sys().(*syscall.Stat_t)
	if stat != nil {
		_ = os.Lchown(target, int(stat.Uid), int(stat.Gid))
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if err := os.Chmod(target, info.Mode()&fileExplorerModeBits); err != nil {
		return wrapFileExplorerIOError(target, err)
	}
	copyFileExplorerACL(source, target, info.IsDir())
	copyFileExplorerExtattrs(source, target)
	_ = os.Chtimes(target, info.ModTime(), info.ModTime())
	return nil
}

type fileExplorerPendingFlags struct {
	path  string
	flags uint32
}

func (c *fileExplorerCopier) deferFlags(target string, info fs.FileInfo) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && stat.Flags != 0 && info.Mode()&fs.ModeSymlink == 0 {
		c.flags = append(c.flags, fileExplorerPendingFlags{path: target, flags: stat.Flags})
	}
}

func (c *fileExplorerCopier) applyFlags() {
	for _, pending := range c.flags {
		_ = unix.Chflags(pending.path, int(pending.flags))
	}
	c.flags = nil
}

func (c *fileExplorerCopier) discardFlags() {
	c.flags = nil
}
