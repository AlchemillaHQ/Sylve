// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sharerules

import (
	"path/filepath"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	BlockRoot              = "root"
	BlockSystemPath        = "system_path"
	BlockBootEnvironment   = "boot_environment"
	BlockSylveData         = "sylve_data"
	BlockContainsProtected = "contains_protected_data"

	StateActive           = "active"
	StateDisabled         = "disabled"
	StateDatasetMissing   = "dataset_missing"
	StateDatasetUnmounted = "dataset_unmounted"
)

var systemTrees = []string{"/etc", "/var", "/tmp", "/dev", "/proc", "/root", "/compat"}

func Mountpoint(dataset *gzfs.Dataset) (string, bool) {
	if dataset == nil {
		return "", false
	}
	mountpoint := strings.TrimSpace(dataset.Mountpoint)
	if !filepath.IsAbs(mountpoint) {
		return "", false
	}
	return filepath.Clean(mountpoint), true
}

func SystemPath(path string) bool {
	if utils.CleanPathWithin(path, "/usr/home") {
		return false
	}
	if utils.IsBaseSystemPath(path) {
		return true
	}
	for _, tree := range systemTrees {
		if utils.CleanPathWithin(path, tree) {
			return true
		}
	}
	return false
}

func DataPath() string {
	if config.ParsedConfig == nil {
		return ""
	}
	path := strings.TrimSpace(config.ParsedConfig.DataPath)
	if path == "" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return ""
}

func ProtectedRoots(datasets []*gzfs.Dataset, dataPath string) []string {
	roots := make([]string, 0, 4)
	for _, dataset := range datasets {
		if dataset == nil || (!utils.IsSylveDataset(dataset.Name) && !utils.IsBootEnvironmentDataset(dataset.Name)) {
			continue
		}
		if mountpoint, ok := Mountpoint(dataset); ok {
			roots = append(roots, mountpoint)
		}
	}
	if dataPath != "" {
		roots = append(roots, dataPath)
	}
	return roots
}

func State(enabled bool, dataset *gzfs.Dataset, mounted bool) string {
	switch {
	case !enabled:
		return StateDisabled
	case dataset == nil:
		return StateDatasetMissing
	case !mounted:
		return StateDatasetUnmounted
	}
	return StateActive
}

func DatasetBlock(dataset *gzfs.Dataset, protectedRoots []string) string {
	if dataset == nil {
		return ""
	}
	if utils.IsBootEnvironmentDataset(dataset.Name) {
		return BlockBootEnvironment
	}
	if utils.IsSylveDataset(dataset.Name) {
		return BlockSylveData
	}
	mountpoint, ok := Mountpoint(dataset)
	if !ok {
		return ""
	}
	if mountpoint == string(filepath.Separator) {
		return BlockRoot
	}
	if SystemPath(mountpoint) {
		return BlockSystemPath
	}
	for _, root := range protectedRoots {
		if utils.CleanPathWithin(root, mountpoint) {
			return BlockContainsProtected
		}
	}
	return ""
}
