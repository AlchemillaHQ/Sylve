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

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) CreateVolume(ctx context.Context, name string, parent string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	name = strings.TrimSpace(name)
	parent = strings.Trim(strings.TrimSpace(parent), "/")
	if name == "" || parent == "" {
		return classifyError(ErrInvalidRequest, "volume_name_and_parent_required")
	}
	parentDataset, err := s.GZFS.ZFS.Get(ctx, parent, false)
	if err != nil || parentDataset == nil {
		return datasetLookupError(err, "parent_dataset_%s_not_found", parent)
	}
	if parentDataset.Type != gzfs.DatasetTypeFilesystem {
		return classifyError(ErrInvalidRequest, "parent_dataset_must_be_a_filesystem")
	}

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
			return classifyError(ErrConflict, "volume_already_exists")
		}
	}

	name = fmt.Sprintf("%s/%s", parent, name)

	if _, ok := props["size"]; !ok {
		return classifyError(ErrInvalidRequest, "size property not found")
	}

	pSize := utils.HumanFormatToSize(props["size"])
	if pSize == 0 {
		return classifyError(ErrInvalidRequest, "invalid_volume_size")
	}
	zvol, err := s.GZFS.ZFS.CreateVolume(ctx, name, pSize, props)

	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return classifyError(ErrConflict, "%v", err)
		}
		return err
	}

	if zvol == nil {
		return fmt.Errorf("failed_to_create_volume")
	}

	if zvol.Name == "" {
		return fmt.Errorf("failed_to_create_volume")
	}

	s.SignalDSChange(zvol.Pool, zvol.Name, "generic-dataset", "create")

	if isEncryptionRequested(props) {
		if err := registerEncryptionKey(ctx, zvol); err != nil {
			logger.L.Warn().Err(err).Str("dataset", zvol.Name).Msg("register_encryption_key_failed")
		}
	}

	return nil
}

func (s *Service) EditVolume(ctx context.Context, guid string, props map[string]string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)

	if err != nil || dataset == nil || dataset.Type != gzfs.DatasetTypeVolume {
		return datasetLookupError(err, "volume_with_guid_%s_not_found", guid)
	}

	if err := s.GZFS.ZFS.EditVolume(ctx, dataset.Name, props); err != nil {
		return err
	}
	s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "edit")
	return nil
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
		return classifyError(ErrConflict, "dataset_in_use_by_vm")
	}

	volume, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)
	if err != nil {
		return datasetLookupError(err, "volume_with_guid_%s_not_found", guid)
	}

	if volume != nil && volume.Type == gzfs.DatasetTypeVolume {
		wasEncrypted := volume.IsEncrypted()

		if err := volume.Destroy(ctx, true, false); err != nil {
			return err
		}

		if wasEncrypted {
			cleanupEncryptionKeyForDataset(volume)
		}

		s.SignalDSChange(volume.Pool, volume.Name, "generic-dataset", "delete")
		return s.notifyDatasetsDeleted(ctx, []string{guid})
	}

	return classifyError(ErrDatasetNotFound, "volume_with_guid_%s_not_found", guid)
}

func (s *Service) FlashVolume(ctx context.Context, guid string, uuid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return classifyError(ErrInvalidRequest, "source_uuid_required")
	}

	volume, err := s.GZFS.ZFS.GetByGUID(ctx, guid, false)
	if err != nil {
		return datasetLookupError(err, "volume with guid %s not found", guid)
	}

	if volume != nil && volume.Type == gzfs.DatasetTypeVolume {
		if s.IsDatasetInUse(guid, false) {
			return classifyError(ErrConflict, "dataset_in_use_by_vm")
		}
		if volume.Properties == nil {
			return classifyError(ErrConflict, "volume_properties_not_found")
		}

		volSizeProp, ok := volume.Properties["volsize"]
		if !ok {
			return classifyError(ErrConflict, "volume_size_property_not_found")
		}

		pSize := utils.HumanFormatToSize(volSizeProp.Value)

		if pSize > 0 {
			file, err := s.Libvirt.FindISOByUUID(uuid, true)
			if file == "" || err != nil {
				return classifyError(ErrSourceNotFound, "source_not_found")
			}

			fileInfo, err := os.Stat(file)
			if err != nil {
				if os.IsNotExist(err) {
					return classifyError(ErrSourceNotFound, "source_not_found")
				}
				return fmt.Errorf("failed_to_get_source_file_info: %w", err)
			}

			if fileInfo.Size() > 0 && pSize >= uint64(fileInfo.Size()) {
				if _, err := os.Stat(fmt.Sprintf("/dev/zvol/%s", volume.Name)); err != nil {
					return classifyError(ErrConflict, "zvol_not_found: %v", err)
				} else {
					output, err := utils.RunCommand(
						"/usr/sbin/camdd",
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
				return classifyError(ErrConflict, "source_size_exceeds_volume_size")
			}
		} else {
			return classifyError(ErrConflict, "invalid_volume_size")
		}
	}

	return classifyError(ErrDatasetNotFound, "volume with guid %s not found", guid)
}
