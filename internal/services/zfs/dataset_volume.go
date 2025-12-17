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

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) CreateVolume(ctx context.Context, name string, parent string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := s.GZFS.ZFS.ListByType(
		ctx,
		gzfs.DatasetTypeVolume,
		true,
		"",
	)

	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == fmt.Sprintf("%s/%s", parent, name) {
			return fmt.Errorf("volume_already_exists")
		}
	}

	name = fmt.Sprintf("%s/%s", parent, name)

	if _, ok := props["size"]; !ok {
		return fmt.Errorf("size property not found")
	}

	pSize := utils.HumanFormatToSize(props["size"])
	zvol, err := s.GZFS.ZFS.CreateVolume(ctx, name, pSize, props)

	if err != nil {
		return err
	}

	if zvol == nil {
		return fmt.Errorf("failed_to_create_volume")
	}

	if zvol.Name == "" {
		return fmt.Errorf("failed_to_create_volume")
	}

	return nil
}

func (s *Service) EditVolume(ctx context.Context, name string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := s.GZFS.ZFS.ListByType(
		ctx,
		gzfs.DatasetTypeVolume,
		true,
		"",
	)

	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == name && dataset.Type == "volume" {
			return s.GZFS.ZFS.EditVolume(ctx, name, props)
		}
	}

	return fmt.Errorf("volume_with_name_%s_not_found", name)
}

func (s *Service) DeleteVolume(ctx context.Context, guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

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

	volumes, err := s.GZFS.ZFS.ListByType(
		ctx,
		gzfs.DatasetTypeVolume,
		true,
		"",
	)

	if err != nil {
		return err
	}

	for _, volume := range volumes {
		if volume.GUID == guid {
			if err := volume.Destroy(ctx, true, false); err != nil {
				return err
			}
			return nil
		}
	}

	return fmt.Errorf("volume_with_guid_%s_not_found", guid)
}

func (s *Service) FlashVolume(ctx context.Context, guid string, uuid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	datasets, err := s.GZFS.ZFS.ListByType(
		ctx,
		gzfs.DatasetTypeVolume,
		true,
		"",
	)

	if err != nil {
		return err
	}

	var volume *gzfs.Dataset

	for _, volume := range datasets {
		if volume.GUID == guid {
			volume = volume
			break
		}
	}

	if volume != nil {
		if s.IsDatasetInUse(guid, false) {
			return fmt.Errorf("dataset_in_use_by_vm")
		}

		volsizeProp, err := volume.GetProperty(ctx, "volsize")
		if err != nil {
			return fmt.Errorf("failed_to_get_volume_size_property: %w", err)
		}

		pSize := utils.StringToUint64(volsizeProp.Value)

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
				if _, err := os.Stat(fmt.Sprintf("/dev/zvol/%s", volume.Name)); err != nil {
					return fmt.Errorf("zvol_not_found: %w", err)
				} else {
					output, err := utils.RunCommand("dd", "if="+file, "of=/dev/zvol/"+volume.Name, "bs=4M")
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

	return fmt.Errorf("volume with guid %s not found", guid)
}
