// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/zfsutil"
)

func jailRootDatasetIdentity(jail *jailModels.Jail) (string, jailModels.Storage, error) {
	if jail == nil {
		return "", jailModels.Storage{}, fmt.Errorf("jail_not_found")
	}

	var base jailModels.Storage
	baseCount := 0
	for _, storage := range jail.Storages {
		if !storage.IsBase {
			continue
		}
		base = storage
		baseCount++
	}
	if baseCount == 0 {
		return "", jailModels.Storage{}, fmt.Errorf("jail_base_storage_not_found")
	}
	if baseCount != 1 {
		return "", jailModels.Storage{}, fmt.Errorf("jail_base_storage_ambiguous")
	}

	base.Pool = strings.TrimSpace(base.Pool)
	if base.Pool == "" {
		return "", jailModels.Storage{}, fmt.Errorf("jail_base_pool_not_found")
	}
	if strings.Contains(base.Pool, "/") {
		return "", jailModels.Storage{}, fmt.Errorf("jail_base_pool_invalid")
	}

	return fmt.Sprintf("%s/sylve/jails/%d", base.Pool, jail.CTID), base, nil
}

func (s *Service) resolveFilesystemDatasetMountpoint(
	ctx context.Context,
	datasetName string,
	expectedGUID string,
) (string, *gzfs.Dataset, error) {
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return "", nil, fmt.Errorf("gzfs_not_initialized")
	}

	datasetName = strings.TrimSpace(datasetName)
	dataset, err := s.GZFS.ZFS.Get(ctx, datasetName, false)
	if err != nil {
		return "", nil, fmt.Errorf("failed_to_get_filesystem_dataset: %s: %w", datasetName, err)
	}
	if dataset == nil {
		return "", nil, fmt.Errorf("filesystem_dataset_not_found: %s", datasetName)
	}
	mountPoint, err := validateFilesystemDatasetMountpoint(dataset, datasetName, expectedGUID)
	if err != nil {
		return "", nil, err
	}
	return mountPoint, dataset, nil
}

func validateFilesystemDatasetMountpoint(
	dataset *gzfs.Dataset,
	expectedName string,
	expectedGUID string,
) (string, error) {
	if dataset == nil {
		return "", fmt.Errorf("filesystem_dataset_not_found: %s", expectedName)
	}
	if dataset.Name != expectedName {
		return "", fmt.Errorf(
			"filesystem_dataset_identity_mismatch: expected=%s actual=%s",
			expectedName,
			dataset.Name,
		)
	}
	if expectedGUID = strings.TrimSpace(expectedGUID); expectedGUID != "" &&
		strings.TrimSpace(dataset.GUID) != expectedGUID {
		return "", fmt.Errorf(
			"filesystem_dataset_identity_mismatch: dataset=%s expected_guid=%s actual_guid=%s",
			expectedName,
			expectedGUID,
			dataset.GUID,
		)
	}

	mountPoint, err := zfsutil.FilesystemMountpoint(dataset)
	if err != nil {
		return "", fmt.Errorf("filesystem_dataset_mountpoint_not_usable: %s: %w", expectedName, err)
	}
	return mountPoint, nil
}

func (s *Service) resolveJailRoot(
	ctx context.Context,
	jail *jailModels.Jail,
) (string, error) {
	datasetName, storage, err := jailRootDatasetIdentity(jail)
	if err != nil {
		return "", err
	}

	mountPoint, _, err := s.resolveFilesystemDatasetMountpoint(ctx, datasetName, storage.GUID)
	if err != nil {
		return "", fmt.Errorf("jail_dataset_mountpoint_not_usable: %w", err)
	}
	return mountPoint, nil
}

func (s *Service) resolveBootstrapMountpoint(
	ctx context.Context,
	identity bootstrapIdentity,
) (string, error) {
	dataset, err := s.getBootstrapDataset(ctx, identity)
	if err != nil {
		return "", err
	}
	if dataset == nil {
		return "", fmt.Errorf("bootstrap_dataset_does_not_exist")
	}
	mountPoint, err := validateFilesystemDatasetMountpoint(dataset, identity.Dataset, "")
	if err != nil {
		return "", fmt.Errorf("bootstrap_mountpoint_not_usable: %w", err)
	}
	return mountPoint, nil
}
