// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsutil

import (
	"fmt"
	"path"
	"strings"

	"github.com/alchemillahq/gzfs"
)

func NormalizeMountpoint(raw string) (string, error) {
	for _, char := range raw {
		if char < ' ' || char > '~' {
			return "", fmt.Errorf("filesystem_dataset_mountpoint_not_usable")
		}
	}
	mountpoint := strings.TrimSpace(raw)
	switch strings.ToLower(mountpoint) {
	case "", "-", "none", "legacy":
		return "", fmt.Errorf("filesystem_dataset_mountpoint_not_usable")
	}

	mountpoint = path.Clean(mountpoint)
	if !path.IsAbs(mountpoint) || mountpoint == "/" {
		return "", fmt.Errorf("filesystem_dataset_mountpoint_not_usable")
	}
	for _, char := range mountpoint {
		if char == '/' || char == '.' || char == '_' || char == '-' ||
			char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' {
			continue
		}
		return "", fmt.Errorf("filesystem_dataset_mountpoint_not_usable")
	}

	return mountpoint, nil
}

func FilesystemMountpoint(dataset *gzfs.Dataset) (string, error) {
	if dataset == nil {
		return "", fmt.Errorf("filesystem_dataset_not_found")
	}
	if dataset.Type != gzfs.DatasetTypeFilesystem {
		return "", fmt.Errorf(
			"filesystem_dataset_not_filesystem: dataset=%s type=%s",
			dataset.Name,
			dataset.Type,
		)
	}

	mountpoint, err := NormalizeMountpoint(dataset.Mountpoint)
	if err != nil || mountpoint != dataset.Mountpoint {
		return "", fmt.Errorf(
			"filesystem_dataset_mountpoint_not_usable: dataset=%s mountpoint=%s",
			dataset.Name,
			dataset.Mountpoint,
		)
	}

	return dataset.Mountpoint, nil
}
