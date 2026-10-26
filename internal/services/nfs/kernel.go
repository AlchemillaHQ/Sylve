// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func verifyDatasetMount(path, dataset string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if resolved != path {
		return errors.New("dataset mountpoint contains a symlink")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if unix.ByteSliceToString(stat.Fstypename[:]) != "zfs" || unix.ByteSliceToString(stat.Mntfromname[:]) != dataset || unix.ByteSliceToString(stat.Mntonname[:]) != path {
		return errors.New("dataset mount identity does not match")
	}
	return nil
}

func nativeMountStats() ([]unix.Statfs_t, error) {
	for range 3 {
		count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		mounts := make([]unix.Statfs_t, count+16)
		count, err = unix.Getfsstat(mounts, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		if count < len(mounts) {
			return mounts[:count], nil
		}
	}
	return nil, errors.New("mount topology kept changing during NFS inspection")
}

func nativeMounts() ([]kernelMount, error) {
	stats, err := nativeMountStats()
	if err != nil {
		return nil, err
	}
	mounts := make([]kernelMount, 0, len(stats))
	for _, stat := range stats {
		mounts = append(mounts, kernelMount{
			Name: unix.ByteSliceToString(stat.Mntfromname[:]), Path: unix.ByteSliceToString(stat.Mntonname[:]),
			Exported: stat.Flags&unix.MNT_EXPORTED != 0,
		})
	}
	return mounts, nil
}

// FreeBSD's compatibility o2export_args ABI (sys/mount.h). It is accepted by
// nmount and NFSSVC_V4ROOTEXPORT on both 14.x and 15.x. Only MNT_DELEXPORT is
// submitted: this code never constructs credentials or installs access grants.
type deleteExportArgs struct {
	Flags      int32
	Root       uint32
	Anonymous  unix.Xucred
	Address    uintptr
	AddressLen uint8
	Mask       uintptr
	MaskLen    uint8
	Index      uintptr
	NumFlavors int32
	Flavors    [5]int32
}

func clearNativeExports() error {
	stats, err := nativeMountStats()
	if err != nil {
		return err
	}
	args := deleteExportArgs{Flags: unix.MNT_DELEXPORT}
	var clearErr error
	for _, stat := range stats {
		if stat.Flags&unix.MNT_EXPORTED == 0 {
			continue
		}
		if err := deleteMountExport(stat, &args); err != nil {
			clearErr = errors.Join(clearErr, err)
		}
	}
	root := struct {
		Path   uintptr
		Export deleteExportArgs
	}{Export: args}
	// NFSSVC_V4ROOTEXPORT deliberately omits NEWSTRUCT for the compatibility ABI.
	_, _, errno := unix.Syscall(unix.SYS_NFSSVC, 0x00004000, uintptr(unsafe.Pointer(&root)), 0)
	runtime.KeepAlive(root)
	if errno != 0 && errno != unix.ENOENT && errno != unix.ENXIO && errno != unix.ENOSYS {
		clearErr = errors.Join(clearErr, fmt.Errorf("clear NFSv4 root: %w", errno))
	}
	mounts, err := nativeMounts()
	clearErr = errors.Join(clearErr, err)
	for _, mount := range mounts {
		if mount.Exported {
			clearErr = errors.Join(clearErr, fmt.Errorf("kernel export remains at %s", mount.Path))
		}
	}
	return clearErr
}

func deleteMountExport(stat unix.Statfs_t, args *deleteExportArgs) error {
	path := unix.ByteSliceToString(stat.Mntonname[:])
	var current unix.Statfs_t
	if err := unix.Statfs(path, &current); err != nil {
		return err
	}
	if current.Fsid != stat.Fsid || current.Mntfromname != stat.Mntfromname || current.Mntonname != stat.Mntonname {
		return fmt.Errorf("mount identity changed before withdrawing %s", path)
	}
	// Keep precisely the export-only option set used by mountd. Adding even an
	// fsid option makes FreeBSD call VFS_MOUNT (a filesystem remount) instead.
	values := [][]byte{
		[]byte("fstype\x00"), append([]byte(unix.ByteSliceToString(stat.Fstypename[:])), 0),
		[]byte("fspath\x00"), append([]byte(unix.ByteSliceToString(stat.Mntonname[:])), 0),
		[]byte("from\x00"), append([]byte(unix.ByteSliceToString(stat.Mntfromname[:])), 0),
		[]byte("update\x00"), nil, []byte("export\x00"), nil,
	}
	iov := make([]unix.Iovec, len(values))
	for i, value := range values {
		if len(value) > 0 {
			iov[i].Base = &value[0]
			iov[i].SetLen(len(value))
		}
	}
	iov[9].Base = (*byte)(unsafe.Pointer(args))
	iov[9].SetLen(int(unsafe.Sizeof(*args)))
	_, _, errno := unix.Syscall(unix.SYS_NMOUNT, uintptr(unsafe.Pointer(&iov[0])), uintptr(len(iov)), uintptr(stat.Flags|unix.MNT_UPDATE))
	runtime.KeepAlive(values)
	runtime.KeepAlive(iov)
	runtime.KeepAlive(args)
	runtime.KeepAlive(stat)
	if errno != 0 {
		return fmt.Errorf("withdraw %s: %w", unix.ByteSliceToString(stat.Mntonname[:]), errno)
	}
	return nil
}
