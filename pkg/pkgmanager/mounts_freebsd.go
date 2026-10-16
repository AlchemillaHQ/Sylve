// SPDX-License-Identifier: BSD-2-Clause
//go:build freebsd

package pkgmanager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

func ValidatePrivateRoot(ctx context.Context, target Target) (bool, error) {
	if target.Dataset == "" || target.GUID == "" || target.Root == "/" || !filepath.IsAbs(target.Root) {
		return false, fmt.Errorf("package_root_unavailable")
	}
	cmd := exec.CommandContext(ctx, "/sbin/zfs", "get", "-H", "-o", "value", "guid", target.Dataset)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != target.GUID {
		return false, fmt.Errorf("jail_dataset_identity_changed")
	}
	critical := []string{"bin", "sbin", "lib", "libexec", "rescue", "etc", "usr", "usr/local", "usr/local/etc", "var/db/pkg", "var/cache/pkg", "boot"}
	for _, relative := range critical {
		current := target.Root
		for _, part := range strings.Split(relative, "/") {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return false, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return false, fmt.Errorf("package_system_path_symlink_unsupported")
			}
		}
	}
	mounts, err := packageMounts()
	if err != nil {
		return false, err
	}
	anchor, rootMounted, snapshotSafe := false, false, true
	for _, mount := range mounts {
		where := filepath.Clean(unix.ByteSliceToString(mount.Mntonname[:]))
		source := unix.ByteSliceToString(mount.Mntfromname[:])
		kind := unix.ByteSliceToString(mount.Fstypename[:])
		if where == target.Root {
			rootMounted = true
			anchor = kind == "zfs" && source == target.Dataset
			continue
		}
		if !strings.HasPrefix(where, target.Root+"/") {
			continue
		}
		if kind == "zfs" && strings.HasPrefix(source, target.Dataset+"/") {
			continue
		}
		if kind == "devfs" || kind == "procfs" || kind == "fdescfs" {
			continue
		}
		snapshotSafe = false
		for _, relative := range critical {
			system := filepath.Join(target.Root, relative)
			if where == system || strings.HasPrefix(where, system+"/") || strings.HasPrefix(system, where+"/") {
				return false, fmt.Errorf("package_shared_system_mount_unsupported")
			}
		}
	}
	if !rootMounted {
		return false, fmt.Errorf("package_root_not_mounted")
	}
	if !anchor {
		return false, fmt.Errorf("package_root_mount_changed")
	}
	return snapshotSafe, nil
}

func ValidatePackagePaths(ctx context.Context, target Target, files []string) error {
	mounts, err := packageMounts()
	if err != nil {
		return err
	}
	type mountedPath struct {
		where   string
		private bool
	}
	paths := make([]mountedPath, 0, len(mounts))
	for _, mount := range mounts {
		where := filepath.Clean(unix.ByteSliceToString(mount.Mntonname[:]))
		source := unix.ByteSliceToString(mount.Mntfromname[:])
		private := unix.ByteSliceToString(mount.Fstypename[:]) == "zfs" && (source == target.Dataset || strings.HasPrefix(source, target.Dataset+"/"))
		paths = append(paths, mountedPath{where, private})
	}
	sort.SliceStable(paths, func(i, j int) bool { return len(paths[i].where) > len(paths[j].where) })
	checked := map[string]bool{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file == "" {
			continue
		}
		if !filepath.IsAbs(file) || strings.ContainsAny(file, "\x00\r\n") {
			return fmt.Errorf("invalid_package_file_path")
		}
		full := filepath.Join(target.Root, filepath.Clean(file))
		if !strings.HasPrefix(full, target.Root+"/") {
			return fmt.Errorf("invalid_package_file_path")
		}
		private := false
		for _, mount := range paths {
			if full == mount.where || strings.HasPrefix(full, mount.where+"/") {
				private = mount.private
				break
			}
		}
		if !private {
			return fmt.Errorf("package_file_on_shared_mount_unsupported: %s", file)
		}
		parent := filepath.Dir(full)
		for parent != target.Root && strings.HasPrefix(parent, target.Root+"/") {
			if checked[parent] {
				break
			}
			info, err := os.Lstat(parent)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("package_file_parent_symlink_unsupported: %s", file)
			}
			checked[parent] = true
			parent = filepath.Dir(parent)
		}
	}
	return nil
}

func packageMounts() ([]unix.Statfs_t, error) {
	count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	mounts := make([]unix.Statfs_t, count+32)
	count, err = unix.Getfsstat(mounts, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	if count == len(mounts) {
		return nil, fmt.Errorf("package_mount_state_changed")
	}
	return mounts[:count], nil
}
