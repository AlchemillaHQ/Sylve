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
	"strings"
	"unicode/utf8"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/sharerules"
)

const (
	MaxSambaShareNameLength = 80
)

var (
	ErrDatasetNotShareable = errors.New("dataset_not_shareable")
	ErrSambaApplyFailed    = errors.New("samba_apply_failed")

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

func (s *Service) filesystems(ctx context.Context) ([]*gzfs.Dataset, error) {
	if s.ZFS != nil {
		return s.ZFS.CachedDatasets(ctx, gzfs.DatasetTypeFilesystem)
	}
	if s.GZFS == nil {
		return nil, fmt.Errorf("zfs_unavailable")
	}
	return s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, true)
}

func (s *Service) requireShareableDataset(ctx context.Context, dataset *gzfs.Dataset) error {
	datasets, err := s.filesystems(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_list_datasets: %w", err)
	}
	if reason := sharerules.DatasetBlock(dataset, sharerules.ProtectedRoots(datasets, sharerules.DataPath())); reason != "" {
		return &DatasetNotShareableError{Reason: reason}
	}
	return nil
}
