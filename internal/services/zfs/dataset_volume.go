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

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"
)

func (s *Service) CreateVolume(name string, parent string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	defer s.Libvirt.RescanStoragePools()

	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == fmt.Sprintf("%s/%s", parent, name) && dataset.Type == "volume" {
			return fmt.Errorf("volume with name %s already exists", name)
		}
	}

	name = fmt.Sprintf("%s/%s", parent, name)

	if _, ok := props["size"]; !ok {
		return fmt.Errorf("size property not found")
	}

	pSize := utils.HumanFormatToSize(props["size"])

	_, err = zfs.CreateVolume(name, pSize, props)

	return err
}

func (s *Service) EditVolume(name string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	defer s.Libvirt.RescanStoragePools()

	datasets, err := zfs.Datasets(name)
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == name && dataset.Type == "volume" {
			return zfs.EditVolume(name, props)
		}
	}

	return fmt.Errorf("volume with name %s not found", name)
}

func (s *Service) DeleteVolume(guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	defer s.Libvirt.RescanStoragePools()

	var count int64
	if err := s.DB.Model(&vmModels.Storage{}).
		Joins("JOIN vm_storage_datasets ON vm_storage_datasets.id = vm_storages.dataset_id").
		Where("vm_storage_datasets.guid = ?", guid).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if datasets are in use: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("dataset_in_use_by_vm")
	}

	volumes, err := zfs.Volumes("")
	if err != nil {
		return err
	}

	for _, volume := range volumes {
		g, err := volume.GetProperty("guid")
		if err != nil {
			return err
		}

		if g == guid {
			err := volume.Destroy(zfs.DestroyRecursive)
			if err != nil {
				return err
			}
			return nil
		}
	}

	return fmt.Errorf("volume with guid %s not found", guid)
}

func (s *Service) FlashVolume(guid string, uuid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := zfs.Volumes("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for _, v := range properties {
			if v == guid {
				if s.IsDatasetInUse(guid, false) {
					return fmt.Errorf("dataset_in_use_by_vm")
				}

				if _, ok := properties["volsize"]; !ok {
					return fmt.Errorf("invalid_dataset")
				}

				pSize := utils.HumanFormatToSize(properties["volsize"])

				if pSize > 0 {
					file, err := s.Libvirt.FindISOByUUID(uuid, true)
					if file == "" || err != nil {
						return fmt.Errorf("iso_not_found")
					}

					fileInfo, err := os.Stat(file)
					if err != nil {
						return fmt.Errorf("failed_to_get_iso_file_info: %w", err)
					}

					if fileInfo.Size() > 0 && pSize >= uint64(fileInfo.Size()) {
						if _, err := os.Stat(fmt.Sprintf("/dev/zvol/%s", dataset.Name)); err != nil {
							return fmt.Errorf("zvol_not_found: %w", err)
						} else {
							output, err := utils.RunCommand("dd", "if="+file, "of=/dev/zvol/"+dataset.Name, "bs=4M")
							if err != nil {
								return fmt.Errorf("failed_to_flash_volume: %w, output: %s", err, output)
							}

							return nil
						}
					} else {
						return fmt.Errorf("iso_size_exceeds_volume_size")
					}
				} else {
					return fmt.Errorf("invalid_volume_size")
				}
			}
		}
	}

	return fmt.Errorf("volume with guid %s not found", guid)
}
