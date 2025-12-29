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
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
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

	s.SignalDSChange(zvol.Pool, zvol.Name, "generic-dataset", "create")

	return nil
}

func (s *Service) EditVolume(ctx context.Context, req zfsServiceInterfaces.EditVolumeRequest) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, req.GUID, false)

	if err != nil {
		return err
	}

	if dataset.Type == gzfs.DatasetTypeVolume {
		return s.GZFS.ZFS.EditVolume(ctx, dataset.Name, req.Properties)
	}

	s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "edit")

	return fmt.Errorf("volume_with_guid_%s_not_found", req.GUID)
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

	volume, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)
	if err != nil {
		return err
	}

	if volume != nil && volume.Type == gzfs.DatasetTypeVolume {
		if err := volume.Destroy(ctx, true, false); err != nil {
			return err
		}
		return nil
	}

	s.SignalDSChange(volume.Pool, volume.Name, "generic-dataset", "delete")

	return fmt.Errorf("volume_with_guid_%s_not_found", guid)
}

func (s *Service) FlashVolume(ctx context.Context, guid string, uuid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	volume, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)
	if err != nil {
		return err
	}

	if volume != nil && volume.Type == gzfs.DatasetTypeVolume {
		if s.IsDatasetInUse(guid, false) {
			return fmt.Errorf("dataset_in_use_by_vm")
		}
		if volume.Properties == nil {
			return fmt.Errorf("volume_properties_not_found")
		}

		volSizeProp, ok := volume.Properties["volsize"]
		if !ok {
			return fmt.Errorf("volume_size_property_not_found")
		}

		pSize := utils.HumanFormatToSize(volSizeProp.Value)

		if pSize > 0 {
			file, err := s.Libvirt.FindISOByUUID(uuid, true)
			if file == "" || err != nil {
				fmt.Println(file, err)
				return fmt.Errorf("source_not_found")
			}

			fileInfo, err := os.Stat(file)
			if err != nil {
				return fmt.Errorf("failed_to_get_source_file_info: %w", err)
			}

			if fileInfo.Size() > 0 && pSize >= uint64(fileInfo.Size()) {
				if _, err := os.Stat(fmt.Sprintf("/dev/zvol/%s", volume.Name)); err != nil {
					return fmt.Errorf("zvol_not_found: %w", err)
				} else {
					output, err := utils.RunCommand(
						"camdd",
						"-i", "file="+file+",bs=4M",
						"-o", "file=/dev/zvol/"+volume.Name+",bs=4M",
						"-v",
					)

					if err != nil {
						return fmt.Errorf("failed_to_flash_volume: %w, output: %s", err, output)
					}

					s.SignalDSChange(volume.Pool, volume.Name, "generic-dataset", "flash")

					return nil
				}
			} else {
				return fmt.Errorf("source_size_exceeds_volume_size")
			}
		} else {
			return fmt.Errorf("invalid_volume_size")
		}
	}

	return fmt.Errorf("volume with guid %s not found", guid)
}
