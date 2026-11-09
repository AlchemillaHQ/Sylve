// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/alchemillahq/sylve/pkg/utils"
)

// SF_IMMUTABLE from <sys/stat.h>; Go's syscall package doesn't export it.
const sfImmutable = 0x00020000

// Sylve reads .sylve/jail.json back on snapshot rollback, backup restore and migration, and the
// jail sees .sylve as /.sylve. So the directory and the file stay system-immutable, which root in
// a jail can't undo unless the jail has allow.chflags.
func WriteJailMetadata(mountPoint string, data []byte) error {
	root, err := os.OpenRoot(mountPoint)
	if err != nil {
		return err
	}
	defer root.Close()
	return utils.WriteIfChanged(filepath.Join(mountPoint, ".sylve", "jail.json"), data,
		func() (os.FileInfo, error) { return protectedJailMetadata(root) },
		func() ([]byte, error) { return root.ReadFile(".sylve/jail.json") },
		func() error { return writeProtectedJailMetadata(root, data) })
}

func isImmutable(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Flags&sfImmutable != 0
}

func setImmutable(f *os.File, on bool) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	flags := info.Sys().(*syscall.Stat_t).Flags
	if on {
		flags |= sfImmutable
	} else {
		flags &^= sfImmutable
	}
	return syscall.Fchflags(int(f.Fd()), int(flags))
}

func protectedJailMetadata(root *os.Root) (os.FileInfo, error) {
	dir, err := root.Lstat(".sylve")
	if err != nil {
		return nil, err
	}
	file, err := root.Lstat(".sylve/jail.json")
	if err != nil {
		return nil, err
	}
	if !dir.IsDir() || !isImmutable(dir) || !file.Mode().IsRegular() || !isImmutable(file) {
		return nil, fmt.Errorf("jail_metadata_not_protected")
	}
	return file, nil
}

func writeProtectedJailMetadata(root *os.Root, data []byte) error {
	if info, err := root.Lstat(".sylve"); err == nil && !info.IsDir() {
		if err := root.Remove(".sylve"); err != nil {
			return err
		}
	}
	if err := root.Mkdir(".sylve", 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	dir, err := root.OpenRoot(".sylve")
	if err != nil {
		return err
	}
	defer dir.Close()
	dirFile, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer dirFile.Close()
	if err := setImmutable(dirFile, false); err != nil {
		return err
	}
	if old, err := dir.OpenFile("jail.json", os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
		err = setImmutable(old, false)
		old.Close()
		if err != nil {
			return err
		}
	}
	// Older versions staged host config here; nothing reads it any more.
	if err := dir.RemoveAll("host-config"); err != nil {
		return err
	}

	tmp := ".tmp-" + utils.GenerateRandomString(12)
	f, err := dir.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	defer dir.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if err := dir.Rename(tmp, "jail.json"); err != nil {
		return err
	}
	if err := setImmutable(f, true); err != nil {
		return err
	}
	if err := setImmutable(dirFile, true); err != nil {
		return err
	}

	// The jail could touch .sylve while it was writable; with both flags set, check that the
	// names resolve to what was written.
	ourDir, err := dirFile.Stat()
	if err != nil {
		return err
	}
	ourFile, err := f.Stat()
	if err != nil {
		return err
	}
	namedDir, err := root.Lstat(".sylve")
	if err != nil {
		return err
	}
	namedFile, err := dir.Lstat("jail.json")
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	written, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if !os.SameFile(ourDir, namedDir) || !os.SameFile(ourFile, namedFile) || !bytes.Equal(written, data) {
		return fmt.Errorf("jail_metadata_changed_while_writing")
	}
	return nil
}
