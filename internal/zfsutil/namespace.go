// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsutil

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
)

var requiredSylveDatasetSuffixes = []string{
	"sylve",
	"sylve/virtual-machines",
	"sylve/jails",
	"sylve/bootstraps",
}

func EnsureSylveNamespace(
	ctx context.Context,
	client *gzfs.Client,
	poolName string,
) ([]*gzfs.Dataset, error) {
	if client == nil || client.ZFS == nil || client.Zpool == nil {
		return nil, fmt.Errorf("zfs_client_not_configured")
	}
	if strings.TrimSpace(poolName) == "" || strings.Contains(poolName, "/") {
		return nil, fmt.Errorf("invalid_pool_name_%q", poolName)
	}

	pool, err := client.Zpool.Get(ctx, poolName)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_pool_%s: %w", poolName, err)
	}
	if pool == nil || pool.Name != poolName {
		return nil, fmt.Errorf("pool_not_found_%s", poolName)
	}
	if altroot := nonDefaultAltroot(pool.Properties); altroot != "" {
		return nil, fmt.Errorf("pool_altroot_not_supported: pool=%s altroot=%s", poolName, altroot)
	}

	root, err := client.ZFS.Get(ctx, poolName, false)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_pool_root_dataset_%s: %w", poolName, err)
	}
	if err := validateNamespaceFilesystem(root, poolName, poolName); err != nil {
		return nil, err
	}

	var created []*gzfs.Dataset
	for _, suffix := range requiredSylveDatasetSuffixes {
		datasetName := poolName + "/" + suffix
		dataset, getErr := client.ZFS.Get(ctx, datasetName, false)
		if getErr != nil && !datasetDoesNotExist(getErr) {
			return created, fmt.Errorf("failed_to_check_dataset_%s: %w", datasetName, getErr)
		}

		if dataset != nil {
			if err := validateNamespaceFilesystem(dataset, datasetName, poolName); err != nil {
				return created, err
			}
			continue
		}

		createdDataset, createErr := client.ZFS.CreateFilesystem(ctx, datasetName, nil)
		if createErr != nil {
			return created, fmt.Errorf("failed_to_create_dataset_%s: %w", datasetName, createErr)
		}
		if createdDataset == nil || createdDataset.Name != datasetName {
			return created, fmt.Errorf("created_dataset_identity_mismatch_%s", datasetName)
		}

		created = append(created, createdDataset)
		if err := validateNamespaceFilesystem(createdDataset, datasetName, poolName); err != nil {
			return created, err
		}
	}

	return created, nil
}

func validateNamespaceFilesystem(dataset *gzfs.Dataset, expectedName, poolName string) error {
	if dataset == nil || dataset.Name != expectedName {
		return fmt.Errorf("filesystem_dataset_identity_mismatch: expected=%s", expectedName)
	}
	if dataset.Type != gzfs.DatasetTypeFilesystem {
		return fmt.Errorf(
			"managed_dataset_not_filesystem: dataset=%s type=%s",
			expectedName,
			dataset.Type,
		)
	}
	if dataset.Pool != "" && dataset.Pool != poolName {
		return fmt.Errorf(
			"filesystem_dataset_pool_mismatch: dataset=%s expected=%s actual=%s",
			expectedName,
			poolName,
			dataset.Pool,
		)
	}
	if _, err := FilesystemMountpoint(dataset); err != nil {
		return fmt.Errorf("managed_dataset_mountpoint_not_usable_%s: %w", expectedName, err)
	}
	return nil
}

func nonDefaultAltroot(properties map[string]gzfs.ZFSProperty) string {
	for name, property := range properties {
		if !strings.EqualFold(name, "altroot") {
			continue
		}

		value := strings.TrimSpace(property.Value)
		if value != "" && value != "-" {
			return value
		}
	}
	return ""
}

func datasetDoesNotExist(err error) bool {
	if err == nil {
		return false
	}

	var commandErr *gzfs.CmdError
	if errors.As(err, &commandErr) {
		detail := strings.ToLower(strings.TrimSpace(commandErr.Stderr))
		return strings.Contains(detail, "does not exist") ||
			(strings.Contains(detail, "no such") && strings.Contains(detail, "dataset"))
	}

	detail := strings.ToLower(err.Error())
	return strings.Contains(detail, "dataset") && strings.Contains(detail, "does not exist")
}
