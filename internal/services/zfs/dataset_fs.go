// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
)

func (s *Service) CreateFilesystem(ctx context.Context, name string, parent string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	name = strings.TrimSpace(name)
	parent = strings.Trim(strings.TrimSpace(parent), "/")
	if name == "" || parent == "" {
		return classifyError(ErrInvalidRequest, "filesystem_name_and_parent_required")
	}
	parentDataset, err := s.GZFS.ZFS.Get(ctx, parent, false)
	if err != nil || parentDataset == nil {
		return datasetLookupError(err, "parent_dataset_%s_not_found", parent)
	}
	if parentDataset.Type != gzfs.DatasetTypeFilesystem {
		return classifyError(ErrInvalidRequest, "parent_dataset_must_be_a_filesystem")
	}

	cleanProps := make(map[string]string, len(props))
	for key, value := range props {
		if key != "parent" {
			cleanProps[key] = value
		}
	}

	name = fmt.Sprintf("%s/%s", parent, name)
	dataset, err := s.GZFS.ZFS.CreateFilesystem(ctx, name, cleanProps)

	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return classifyError(ErrConflict, "%v", err)
		}
		return err
	}

	if dataset == nil {
		return fmt.Errorf("failed_to_create_filesystem")
	}

	s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "create")

	if isEncryptionRequested(cleanProps) {
		if err := registerEncryptionKey(ctx, dataset); err != nil {
			logger.L.Warn().Err(err).Str("dataset", dataset.Name).Msg("register_encryption_key_failed")
		}
	}

	return nil
}

func (s *Service) EditFilesystem(ctx context.Context, guid string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)

	if err != nil || dataset == nil || dataset.Type != gzfs.DatasetTypeFilesystem {
		return datasetLookupError(err, "filesystem with guid %s not found", guid)
	}

	if mp, ok := props["mountpoint"]; ok && mp == "" {
		props["mountpoint"] = fmt.Sprintf("/%s", dataset.Name)
	}

	if q, ok := props["quota"]; ok && q != "" {
		props["quota"] = strings.ReplaceAll(q, " ", "")
	}

	err = s.GZFS.ZFS.EditFilesystem(ctx, dataset.Name, props)
	if err == nil {
		s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "edit")
	}
	return err
}

func (s *Service) DeleteFilesystem(ctx context.Context, guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	foundFS, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)

	if err != nil || foundFS == nil || foundFS.Type != gzfs.DatasetTypeFilesystem {
		return datasetLookupError(err, "filesystem with guid %s not found", guid)
	}

	if err := validateDatasetDeletionTargets(foundFS); err != nil {
		return err
	}

	noDelete := []string{"sylve", "sylve/virtual-machines", "sylve/jails"}
	relativeName := strings.TrimPrefix(foundFS.Name, foundFS.Pool+"/")
	for _, name := range noDelete {
		if relativeName == name {
			return classifyError(ErrConflict, "cannot_delete_critical_filesystem")
		}
	}

	allDatasets, err := s.GZFS.ZFS.List(ctx, true, "")
	if err != nil {
		return fmt.Errorf("failed to list datasets before deletion: %w", err)
	}
	affectedGUIDs := datasetGUIDsInTrees(allDatasets, []*gzfs.Dataset{foundFS})
	if len(affectedGUIDs) == 0 {
		affectedGUIDs = []string{guid}
	}

	var count int64
	if err := s.DB.Model(&vmModels.VMStorageDataset{}).
		Where("guid IN ?", affectedGUIDs).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if dataset is in use: %w", err)
	}
	if count > 0 {
		return classifyError(ErrConflict, "dataset_in_use_by_vm")
	}

	if err := foundFS.Destroy(ctx, true, false); err != nil {
		return err
	}

	cleanedRoot := false
	for _, dataset := range allDatasets {
		if dataset != nil && datasetNameInTree(dataset.Name, foundFS.Name) && dataset.IsEncrypted() {
			cleanupEncryptionKeyForDataset(dataset)
			if dataset.GUID == foundFS.GUID {
				cleanedRoot = true
			}
		}
	}
	if !cleanedRoot && foundFS.IsEncrypted() {
		cleanupEncryptionKeyForDataset(foundFS)
	}

	s.SignalDSChange(foundFS.Pool, foundFS.Name, "generic-dataset", "delete")

	return s.notifyDatasetsDeleted(ctx, affectedGUIDs)
}
