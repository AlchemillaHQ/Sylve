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
	"fmt"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	"github.com/alchemillahq/sylve/internal/zfsutil"
)

func (s *Service) ensureSylveDatasetsOnPool(ctx context.Context, poolName string) ([]*gzfs.Dataset, error) {
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("zfs_client_not_configured")
	}
	name := poolName + "/sylve/downloads"
	dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
	if err != nil && !zfsutil.DatasetDoesNotExist(err) {
		return nil, err
	}
	if dataset == nil {
		if err := downloadstorage.RequireDatasetUnused(ctx, s.DB, name); err != nil {
			return nil, fmt.Errorf("download_namespace_provisioning_blocked: %w", err)
		}
	}
	return zfsutil.EnsureSylveNamespace(ctx, s.GZFS, poolName)
}
