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
	"time"

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

func (s *Service) destroyLocalDatasetWithRetry(
	ctx context.Context,
	name string,
	recursive bool,
	attempts int,
	delay time.Duration,
) error {
	if attempts < 1 {
		attempts = 1
	}
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := s.destroyLocalDataset(ctx, name, recursive); err != nil {
			lastErr = err
			if !isLocalDatasetBusyError(err) {
				return err
			}

			if i == attempts-1 {
				break
			}

			select {
			case <-ctx.Done():
				return fmt.Errorf("destroy_dataset_retry_canceled: %w", ctx.Err())
			case <-time.After(delay):
			}

			continue
		}

		return nil
	}

	return lastErr
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

func (s *Service) promoteRestoredDataset(ctx context.Context, restorePath, destination string) (string, error) {
	restorePath = normalizeRestoreDestinationDataset(restorePath)
	destination = normalizeRestoreDestinationDataset(destination)
	if restorePath == "" || destination == "" {
		return "", fmt.Errorf("destination_dataset_required")
	}

	destinationExists, err := s.localDatasetExists(ctx, destination)
	if err != nil {
		return "", err
	}

	backupDataset := ""
	if destinationExists {
		backupDataset = fmt.Sprintf("%s.pre_restore_%d", destination, time.Now().UTC().UnixNano())
		if err := s.renameLocalDataset(ctx, destination, backupDataset); err != nil {
			return "", fmt.Errorf("failed_to_backup_destination_dataset_before_restore: %w", err)
		}
	}

	if err := s.renameLocalDataset(ctx, restorePath, destination); err != nil {
		if backupDataset != "" {
			if rollbackErr := s.renameLocalDataset(ctx, backupDataset, destination); rollbackErr != nil {
				return "", fmt.Errorf(
					"failed_to_promote_restored_dataset: %w; failed_to_restore_original_dataset: %v",
					err,
					rollbackErr,
				)
			}
		}
		return "", fmt.Errorf("failed_to_promote_restored_dataset: %w", err)
	}

	return backupDataset, nil
}

func (s *Service) rollbackPromotedDataset(ctx context.Context, destination, backupDataset string) error {
	destination = normalizeRestoreDestinationDataset(destination)
	backupDataset = normalizeRestoreDestinationDataset(backupDataset)
	if backupDataset == "" {
		return nil
	}

	destinationExists, err := s.localDatasetExists(ctx, destination)
	if err != nil {
		return err
	}
	if destinationExists {
		if err := s.destroyLocalDatasetWithRetry(ctx, destination, true, 20, 500*time.Millisecond); err != nil {
			return fmt.Errorf("failed_to_remove_failed_restored_dataset: %w", err)
		}
	}

	backupExists, err := s.localDatasetExists(ctx, backupDataset)
	if err != nil {
		return err
	}
	if !backupExists {
		return fmt.Errorf("restore_backup_dataset_missing: %s", backupDataset)
	}

	if err := s.renameLocalDataset(ctx, backupDataset, destination); err != nil {
		return fmt.Errorf("failed_to_restore_backup_dataset: %w", err)
	}

	return nil
}

func (s *Service) cleanupRestoreBackupDataset(ctx context.Context, backupDataset string) error {
	backupDataset = normalizeRestoreDestinationDataset(backupDataset)
	if backupDataset == "" {
		return nil
	}

	return s.destroyLocalDatasetWithRetry(ctx, backupDataset, true, 20, 500*time.Millisecond)
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

func isLocalDatasetBusyError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "dataset is busy") ||
		strings.Contains(lower, "resource busy")
}
