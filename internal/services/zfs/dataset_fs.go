// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"fmt"
	"os"

	"github.com/alchemillahq/sylve/pkg/zfs"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
)

func (s *Service) CreateFilesystem(name string, props map[string]string) error {
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

	_, err := zfs.CreateFilesystem(name, props)

	if err != nil {
		return err
	}

	datasets, err := zfs.Datasets(name)
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == name {
			return nil
		}
	}

	return fmt.Errorf("failed to create filesystem %s", name)
}

func (s *Service) EditFilesystem(guid string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		property, err := dataset.GetProperty("guid")
		if err != nil {
			return err
		}

		if property == guid {
			return zfs.EditFilesystem(dataset.Name, props)
		}
	}

	return fmt.Errorf("filesystem with guid %s not found", guid)
}

func (s *Service) DeleteFilesystem(guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var count int64
	if err := s.DB.Model(&vmModels.Storage{}).Where("dataset = ?", guid).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if dataset is in use: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("dataset_in_use_by_vm")
	}

	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		var keylocation string
		found := false

		for k, v := range properties {
			if v == guid {
				found = true
			}
			if k == "keylocation" {
				keylocation = v
			}
		}

		if found {
			if err := dataset.Destroy(zfs.DestroyRecursive); err != nil {
				return err
			}

			if keylocation != "" && keylocation != "none" {
				keylocation = keylocation[7:]
				if _, err := os.Stat(keylocation); err == nil {
					if err := os.Remove(keylocation); err != nil {
						return err
					}
				} else {
					return fmt.Errorf("keylocation_file_not_found: %s", keylocation)
				}
			}

			return nil
		}
	}

	return fmt.Errorf("filesystem with guid %s not found", guid)
}
