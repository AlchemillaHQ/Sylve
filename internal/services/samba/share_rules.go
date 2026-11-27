// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	ShareDatasetRoot              = "root"
	ShareDatasetSystemPath        = "system_path"
	ShareDatasetBootEnvironment   = "boot_environment"
	ShareDatasetSylveData         = "sylve_data"
	ShareDatasetContainsProtected = "contains_protected_data"

	MaxSambaShareNameLength = 80
)

var (
	ErrDatasetNotShareable = errors.New("dataset_not_shareable")
	ErrSambaApplyFailed    = errors.New("samba_apply_failed")

	sambaSystemTrees        = []string{"/etc", "/var", "/tmp", "/dev", "/proc", "/root", "/compat"}
	sambaShareNameForbidden = `\/:*?"<>|[],`
	reservedSambaShareNames = map[string]struct{}{
		"global":   {},
		"homes":    {},
		"printers": {},
		"print$":   {},
		"ipc$":     {},
	}
)

type DatasetNotShareableError struct {
	Reason string
}

func (e *DatasetNotShareableError) Error() string {
	return ErrDatasetNotShareable.Error() + ": " + e.Reason
}

func (e *DatasetNotShareableError) Unwrap() error {
	return ErrDatasetNotShareable
}

func sambaApplyFailure(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrSambaApplyFailed, err)
}

func validateSambaShareName(name string) error {
	if name == "" || strings.TrimSpace(name) != name || !utf8.ValidString(name) {
		return fmt.Errorf("invalid_share_name")
	}
	if utf8.RuneCountInString(name) > MaxSambaShareNameLength {
		return fmt.Errorf("share_name_too_long")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(sambaShareNameForbidden, r) {
			return fmt.Errorf("invalid_share_name")
		}
	}
	if _, reserved := reservedSambaShareNames[strings.ToLower(name)]; reserved {
		return fmt.Errorf("share_name_reserved")
	}
	return nil
}

func sambaMountpoint(dataset *gzfs.Dataset) (string, bool) {
	if dataset == nil {
		return "", false
	}
	mountpoint := strings.TrimSpace(dataset.Mountpoint)
	if !filepath.IsAbs(mountpoint) {
		return "", false
	}
	return filepath.Clean(mountpoint), true
}

func sambaSystemPath(path string) bool {
	if utils.CleanPathWithin(path, "/usr/home") {
		return false
	}
	if utils.IsBaseSystemPath(path) {
		return true
	}
	for _, tree := range sambaSystemTrees {
		if utils.CleanPathWithin(path, tree) {
			return true
		}
	}
	return false
}

func sambaDataPath() string {
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

func (s *Service) filesystems(ctx context.Context) ([]*gzfs.Dataset, error) {
	if s.ZFS != nil {
		return s.ZFS.CachedDatasets(ctx, gzfs.DatasetTypeFilesystem)
	}
	if s.GZFS == nil {
		return nil, fmt.Errorf("zfs_unavailable")
	}
	return s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, true)
}

func protectedShareRoots(datasets []*gzfs.Dataset, dataPath string) []string {
	roots := make([]string, 0, 4)
	for _, dataset := range datasets {
		if dataset == nil || (!utils.IsSylveDataset(dataset.Name) && !utils.IsBootEnvironmentDataset(dataset.Name)) {
			continue
		}
		if mountpoint, ok := sambaMountpoint(dataset); ok {
			roots = append(roots, mountpoint)
		}
	}
	if dataPath != "" {
		roots = append(roots, dataPath)
	}
	return roots
}

func shareDatasetBlock(dataset *gzfs.Dataset, protectedRoots []string) string {
	if dataset == nil {
		return ""
	}
	if utils.IsBootEnvironmentDataset(dataset.Name) {
		return ShareDatasetBootEnvironment
	}
	if utils.IsSylveDataset(dataset.Name) {
		return ShareDatasetSylveData
	}
	mountpoint, ok := sambaMountpoint(dataset)
	if !ok {
		return ""
	}
	if mountpoint == string(filepath.Separator) {
		return ShareDatasetRoot
	}
	if sambaSystemPath(mountpoint) {
		return ShareDatasetSystemPath
	}
	for _, root := range protectedRoots {
		if utils.CleanPathWithin(root, mountpoint) {
			return ShareDatasetContainsProtected
		}
	}
	return ""
}

func (s *Service) requireShareableDataset(ctx context.Context, dataset *gzfs.Dataset) error {
	datasets, err := s.filesystems(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_list_datasets: %w", err)
	}
	if reason := shareDatasetBlock(dataset, protectedShareRoots(datasets, sambaDataPath())); reason != "" {
		return &DatasetNotShareableError{Reason: reason}
	}
	return nil
}
