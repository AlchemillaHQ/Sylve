// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mount

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const maxUnmountPasses = 8

type MountInfo struct {
	Source   string
	Target   string
	FSType   string
	ReadOnly bool
}

func List(ctx context.Context) ([]MountInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return (nativeMountTreeOps{}).list(ctx)
}

type mountTreeOps interface {
	list(context.Context) ([]MountInfo, error)
	unmount(target string, force bool) error
	isBusy(error) bool
	isGone(error) bool
}

// UnmountDescendants removes live filesystem mounts below one exact ZFS
// mountpoint. Every target receives a normal unmount first. An EBUSY result is
// retried with the host's forced-unmount flag.
//
// An absent ZFS anchor is accepted only when there are no descendant mounts.
// This keeps an intentionally unmounted standby idempotent without sweeping
// mounts from an unrelated filesystem exposed at the same lexical path.
func UnmountDescendants(
	ctx context.Context,
	dataset string,
	mountpoint string,
) error {
	return unmountDescendants(ctx, dataset, mountpoint, nativeMountTreeOps{})
}

func unmountDescendants(
	ctx context.Context,
	dataset string,
	mountpoint string,
	ops mountTreeOps,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	dataset = strings.TrimSpace(dataset)
	if dataset == "" || strings.ContainsRune(dataset, '\x00') {
		return fmt.Errorf("mount_tree_dataset_invalid")
	}

	root, err := normalizeRoot(mountpoint)
	if err != nil {
		return err
	}
	if ops == nil {
		return fmt.Errorf("mount_tree_ops_unavailable")
	}

	var lastErrors []error
	for pass := 0; pass < maxUnmountPasses; pass++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("mount_tree_cleanup_canceled: %w", err)
		}

		mounts, err := ops.list(ctx)
		if err != nil {
			return fmt.Errorf("mount_tree_list_failed: %w", err)
		}
		descendants := descendantMounts(mounts, root)
		if len(descendants) == 0 {
			return nil
		}
		if !hasZFSAnchor(mounts, dataset, root) {
			return residualMountError(
				dataset,
				root,
				descendants,
				[]error{fmt.Errorf("mount_tree_zfs_anchor_missing")},
			)
		}

		sortMountsForUnmount(descendants)
		progress := false
		lastErrors = lastErrors[:0]
		for _, mount := range descendants {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("mount_tree_cleanup_canceled: %w", err)
			}

			unmountErr := ops.unmount(mount.Target, false)
			if unmountErr == nil || ops.isGone(unmountErr) {
				progress = true
				continue
			}
			if ops.isBusy(unmountErr) {
				forceErr := ops.unmount(mount.Target, true)
				if forceErr == nil || ops.isGone(forceErr) {
					progress = true
					continue
				}
				lastErrors = append(lastErrors, fmt.Errorf(
					"force_unmount_%s_failed: %w",
					mount.Target,
					forceErr,
				))
				continue
			}

			lastErrors = append(lastErrors, fmt.Errorf(
				"unmount_%s_failed: %w",
				mount.Target,
				unmountErr,
			))
		}

		mounts, err = ops.list(ctx)
		if err != nil {
			return fmt.Errorf("mount_tree_rescan_failed: %w", err)
		}
		remaining := descendantMounts(mounts, root)
		if len(remaining) == 0 {
			return nil
		}
		if !progress {
			return residualMountError(dataset, root, remaining, lastErrors)
		}
	}

	mounts, err := ops.list(ctx)
	if err != nil {
		return fmt.Errorf("mount_tree_final_rescan_failed: %w", err)
	}
	return residualMountError(dataset, root, descendantMounts(mounts, root), lastErrors)
}

func normalizeRoot(root string) (string, error) {
	if strings.ContainsRune(root, '\x00') {
		return "", fmt.Errorf("mount_tree_root_invalid")
	}
	root = filepath.Clean(strings.TrimSpace(root))
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return "", fmt.Errorf("mount_tree_root_invalid: %s", root)
	}
	return root, nil
}

func normalizedMountTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" || strings.ContainsRune(target, '\x00') {
		return ""
	}
	return filepath.Clean(target)
}

func isStrictDescendant(target, root string) bool {
	target = normalizedMountTarget(target)
	return target != "" && strings.HasPrefix(target, root+string(filepath.Separator))
}

func descendantMounts(mounts []MountInfo, root string) []MountInfo {
	result := make([]MountInfo, 0)
	for _, mount := range mounts {
		target := normalizedMountTarget(mount.Target)
		if !isStrictDescendant(target, root) {
			continue
		}
		mount.Target = target
		result = append(result, mount)
	}
	return result
}

func hasZFSAnchor(mounts []MountInfo, dataset, root string) bool {
	for _, mount := range mounts {
		if strings.EqualFold(strings.TrimSpace(mount.FSType), "zfs") &&
			strings.TrimSpace(mount.Source) == dataset &&
			normalizedMountTarget(mount.Target) == root {
			return true
		}
	}
	return false
}

func mountDepth(target string) int {
	target = strings.Trim(filepath.Clean(target), string(filepath.Separator))
	if target == "" {
		return 0
	}
	return len(strings.Split(target, string(filepath.Separator)))
}

func sortMountsForUnmount(mounts []MountInfo) {
	sort.SliceStable(mounts, func(i, j int) bool {
		leftDepth := mountDepth(mounts[i].Target)
		rightDepth := mountDepth(mounts[j].Target)
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return mounts[i].Target < mounts[j].Target
	})
}

func residualMountError(
	dataset string,
	root string,
	mounts []MountInfo,
	causes []error,
) error {
	sortMountsForUnmount(mounts)
	details := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		details = append(details, fmt.Sprintf(
			"target=%s source=%s type=%s",
			mount.Target,
			mount.Source,
			mount.FSType,
		))
	}
	base := fmt.Errorf(
		"mount_tree_cleanup_incomplete: dataset=%s mountpoint=%s residual=[%s]",
		dataset,
		root,
		strings.Join(details, "; "),
	)
	if len(causes) == 0 {
		return base
	}
	joined := make([]error, 0, len(causes)+1)
	joined = append(joined, base)
	joined = append(joined, causes...)
	return errors.Join(joined...)
}

type nativeMountTreeOps struct{}

func (nativeMountTreeOps) list(ctx context.Context) ([]MountInfo, error) {
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

		mounts := make([]MountInfo, 0, count)
		for _, stat := range buffer[:count] {
			mounts = append(mounts, MountInfo{
				Source:   unix.ByteSliceToString(stat.Mntfromname[:]),
				Target:   unix.ByteSliceToString(stat.Mntonname[:]),
				FSType:   unix.ByteSliceToString(stat.Fstypename[:]),
				ReadOnly: stat.Flags&unix.MNT_RDONLY != 0,
			})
		}
		return mounts, nil
	}
}

func (nativeMountTreeOps) unmount(target string, force bool) error {
	flags := 0
	if force {
		flags = unix.MNT_FORCE
	}
	return unix.Unmount(target, flags)
}

func (nativeMountTreeOps) isBusy(err error) bool {
	return errors.Is(err, unix.EBUSY)
}

func (nativeMountTreeOps) isGone(err error) bool {
	return errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT)
}
