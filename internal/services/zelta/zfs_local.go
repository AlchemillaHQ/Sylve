// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
)

func (s *Service) getLocalDataset(ctx context.Context, name string) (*gzfs.Dataset, error) {
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

	name = normalizeDatasetPath(name)
	if name == "" {
		return nil, nil
	}

	ds, err := s.GZFS.ZFS.Get(ctx, name, false)
	if err != nil {
		if isGZFSDatasetNotFoundError(err) {
			return nil, nil
		}
		return nil, err
	}

	return ds, nil
}

func (s *Service) localDatasetExists(ctx context.Context, name string) (bool, error) {
	ds, err := s.getLocalDataset(ctx, name)
	if err != nil {
		return false, err
	}
	return ds != nil, nil
}

func (s *Service) destroyLocalDataset(ctx context.Context, name string, recursive bool) error {
	ds, err := s.getLocalDataset(ctx, name)
	if err != nil {
		return err
	}
	if ds == nil {
		return nil
	}
	return ds.Destroy(ctx, recursive, false)
}

func (s *Service) renameLocalDataset(ctx context.Context, from, to string) error {
	ds, err := s.getLocalDataset(ctx, from)
	if err != nil {
		return err
	}
	if ds == nil {
		return fmt.Errorf("source_dataset_not_found: %s", from)
	}

	_, err = ds.Rename(ctx, to, false)
	return err
}

func (s *Service) mountLocalDataset(ctx context.Context, name string) error {
	ds, err := s.getLocalDataset(ctx, name)
	if err != nil {
		return err
	}
	if ds == nil {
		return fmt.Errorf("dataset_not_found: %s", name)
	}

	return ds.Mount(ctx, false)
}

func (s *Service) runLocalZFSGet(ctx context.Context, property, dataset string) (string, error) {
	ds, err := s.getLocalDataset(ctx, dataset)
	if err != nil {
		return "", err
	}
	if ds == nil {
		return "", fmt.Errorf("dataset_not_found: %s", dataset)
	}

	prop, err := ds.GetProperty(ctx, strings.TrimSpace(property))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(prop.Value), nil
}

func (s *Service) ensureLocalPoolExists(ctx context.Context, pool string) error {
	if s == nil || s.GZFS == nil || s.GZFS.Zpool == nil {
		return fmt.Errorf("gzfs_not_initialized")
	}

	pool = strings.TrimSpace(pool)
	if pool == "" {
		return fmt.Errorf("pool_required")
	}

	p, err := s.GZFS.Zpool.Get(ctx, pool)
	if err != nil {
		if isGZFSPoolNotFoundError(err) {
			return fmt.Errorf("destination_dataset_pool_missing: cannot find destination root '%s'", pool)
		}
		return err
	}
	if p == nil {
		return fmt.Errorf("destination_dataset_pool_missing: cannot find destination root '%s'", pool)
	}

	return nil
}

func (s *Service) ensureLocalFilesystemPath(ctx context.Context, dataset string) error {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" {
		return fmt.Errorf("destination_dataset_required")
	}

	parts := strings.Split(dataset, "/")
	if len(parts) == 0 {
		return fmt.Errorf("destination_dataset_required")
	}

	pool := strings.TrimSpace(parts[0])
	if err := s.ensureLocalPoolExists(ctx, pool); err != nil {
		return err
	}

	if len(parts) < 2 {
		return nil
	}

	current := pool
	for idx := 1; idx < len(parts); idx++ {
		current = current + "/" + parts[idx]
		ds, err := s.getLocalDataset(ctx, current)
		if err != nil {
			return fmt.Errorf("failed_to_check_dataset_%s: %w", current, err)
		}
		if ds != nil {
			continue
		}
		if _, err := s.GZFS.ZFS.CreateFilesystem(ctx, current, map[string]string{}); err != nil {
			return fmt.Errorf("failed_to_create_dataset_%s: %w", current, err)
		}
	}

	return nil
}

func (s *Service) listLocalFilesystemDatasets(ctx context.Context) ([]string, error) {
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

	sets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, false)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(sets))
	for _, ds := range sets {
		if ds == nil {
			continue
		}
		name := normalizeDatasetPath(ds.Name)
		if name == "" {
			continue
		}
		out = append(out, name)
	}

	return out, nil
}

func isGZFSDatasetNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "dataset does not exist") ||
		strings.Contains(lower, "cannot open") ||
		strings.Contains(lower, "dataset_not_found")
}

func isGZFSPoolNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no such pool") ||
		strings.Contains(lower, "cannot open") ||
		strings.Contains(lower, "pool does not exist")
}
