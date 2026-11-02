// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
)

func (s *Service) ensureSylveDatasetsOnPool(ctx context.Context, poolName string) ([]*gzfs.Dataset, error) {
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("zfs_client_not_configured")
	}
	name := poolName + "/sylve/downloads"
	dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
	if err != nil && !storagezfs.DatasetDoesNotExist(err) {
		return nil, err
	}
	if dataset == nil {
		if err := downloadstorage.RequireDatasetUnused(ctx, s.DB, name); err != nil {
			return nil, fmt.Errorf("download_namespace_provisioning_blocked: %w", err)
		}
	}
	return storagezfs.EnsureSylveNamespace(ctx, s.GZFS, poolName)
}

func cleanupCreatedDatasets(ctx context.Context, client *gzfs.Client, datasets []*gzfs.Dataset) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var cleanupErr error
	for i := len(datasets) - 1; i >= 0; i-- {
		if datasets[i] == nil {
			continue
		}
		current, err := client.ZFS.Get(ctx, datasets[i].Name, false)
		if storagezfs.DatasetDoesNotExist(err) || err == nil && current == nil {
			continue
		}
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if current.GUID == "" || current.GUID != datasets[i].GUID || current.Type != datasets[i].Type {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("bootstrap_dataset_ownership_changed: %s", datasets[i].Name))
			continue
		}
		if err := current.Destroy(ctx, false, false); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}
