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
	"os"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func (s *Service) CreateFilesystem(ctx context.Context, name string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	parent := ""

	for k, v := range props {
		if k == "parent" {
			parent = v
			continue
		}
	}

	if parent == "" {
		return fmt.Errorf("parent_not_found")
	}

	name = fmt.Sprintf("%s/%s", parent, name)
	delete(props, "parent")

	dataset, err := s.GZFS.ZFS.CreateFilesystem(ctx, name, props)

	if err != nil {
		return err
	}

	if dataset == nil {
		return fmt.Errorf("failed_to_create_filesystem")
	}

	s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "create")

	return nil
}

func (s *Service) EditFilesystem(ctx context.Context, guid string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)

	if err != nil {
		return err
	}

	if mp, ok := props["mountpoint"]; ok && mp == "" {
		props["mountpoint"] = fmt.Sprintf("/%s", dataset.Name)
	}

	if dataset != nil {
		err := s.GZFS.ZFS.EditFilesystem(ctx, dataset.Name, props)
		if err == nil {
			s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "edit")
		}
		return err
	}

	return fmt.Errorf("filesystem with guid %s not found", guid)
}

func (s *Service) DeleteFilesystem(ctx context.Context, guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	foundFS, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)

	if err != nil {
		return err
	}

	if foundFS == nil {
		return fmt.Errorf("filesystem with guid %s not found", guid)
	}

	noDelete := []string{"sylve", "sylve/virtual-machines", "sylve/jails"}
	for _, name := range noDelete {
		if strings.HasSuffix(foundFS.Name, name) {
			return fmt.Errorf("cannot_delete_critical_filesystem")
		}
	}

	var count int64
	if err := s.DB.Model(&vmModels.VMStorageDataset{}).
		Where("guid = ?", guid).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if dataset is in use: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("dataset_in_use_by_vm")
	}

	var keylocation string

	if prop, err := foundFS.GetProperty(ctx, "keylocation"); err == nil {
		keylocation = prop.Value
	}

	if err := foundFS.Destroy(ctx, true, false); err != nil {
		return err
	}

	if keylocation != "" && keylocation != "none" && strings.HasPrefix(keylocation, "file://") {
		path := strings.TrimPrefix(keylocation, "file://")
		_ = os.Remove(path)
	}

	s.SignalDSChange(foundFS.Pool, foundFS.Name, "generic-dataset", "edit")

	return nil
}
