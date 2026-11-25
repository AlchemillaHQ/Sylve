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
	"slices"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
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

func (s *Service) EditVolume(ctx context.Context, guid string, props map[string]string, inherit []string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	if len(props) == 0 && len(inherit) == 0 {
		return classifyError(ErrInvalidRequest, "no_properties_to_edit")
	}
	for _, property := range inherit {
		if _, set := props[property]; set {
			return classifyError(ErrInvalidRequest, "property_%s_cannot_be_set_and_inherited", property)
		}
	}

	dataset, err := s.DatasetByGUID(ctx, guid)

	if err != nil || dataset == nil || dataset.Type != gzfs.DatasetTypeVolume {
		return datasetLookupError(err, "volume_with_guid_%s_not_found", guid)
	}

	shrinks := false
	if value, ok := props["volsize"]; ok {
		next := utils.HumanFormatToSize(value)
		if next == 0 {
			return classifyError(ErrInvalidRequest, "invalid_volume_size")
		}
		current, _ := strconv.ParseUint(dataset.Properties["volsize"].Value, 10, 64)
		shrinks = current > 0 && next < current
	}
	mode, setsMode := props["volmode"]
	changesMode := setsMode && mode != dataset.Properties["volmode"].Value
	if shrinks || changesMode || slices.Contains(inherit, "volmode") {
		if err := s.volumeInUse(ctx, dataset); err != nil {
			return err
		}
	}

	if len(props) > 0 {
		if err := s.GZFS.ZFS.EditVolume(ctx, dataset.Name, props); err != nil {
			return err
		}
		s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "edit")
	}
	if len(inherit) > 0 {
		if err := s.GZFS.ZFS.InheritProperties(ctx, dataset.Name, inherit...); err != nil {
			return err
		}
		s.SignalDSChange(dataset.Pool, dataset.Name, "generic-dataset", "edit")
	}
	return nil
}

func (s *Service) volumeInUse(ctx context.Context, volume *gzfs.Dataset) error {
	if s.IsDatasetInUse(volume.GUID, false) {
		return classifyError(ErrConflict, "volume_%s_in_use", volume.Name)
	}
	return s.requireVolumesNotShared(ctx, []*gzfs.Dataset{volume})
}

func (s *Service) requireVolumesNotShared(ctx context.Context, datasets []*gzfs.Dataset) error {
	var names []string
	for _, dataset := range datasets {
		if dataset != nil && dataset.Type == gzfs.DatasetTypeVolume {
			names = append(names, dataset.Name)
		}
	}
	shared, err := s.iscsiTargetsForVolumes(ctx, names)
	if err != nil {
		return err
	}
	for _, name := range names {
		if target, ok := shared[name]; ok {
			return classifyError(ErrConflict, "volume_%s_shared_over_iscsi_by_%s", name, target)
		}
	}
	return nil
}

func (s *Service) iscsiTargetsForVolumes(ctx context.Context, names []string) (map[string]string, error) {
	if len(names) == 0 || s.DB == nil || !s.DB.Migrator().HasTable(&iscsiModels.ISCSITargetLUN{}) {
		return nil, nil
	}
	var luns []iscsiModels.ISCSITargetLUN
	if err := s.DB.WithContext(ctx).Find(&luns).Error; err != nil {
		return nil, fmt.Errorf("failed_to_check_iscsi_targets: %w", err)
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	targetIDs := map[string]uint{}
	var ids []uint
	for _, lun := range luns {
		if _, ok := wanted[lun.ZVol]; !ok {
			continue
		}
		if _, seen := targetIDs[lun.ZVol]; !seen {
			targetIDs[lun.ZVol] = lun.TargetID
			ids = append(ids, lun.TargetID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var targets []iscsiModels.ISCSITarget
	if s.DB.Migrator().HasTable(&iscsiModels.ISCSITarget{}) {
		if err := s.DB.WithContext(ctx).Find(&targets, ids).Error; err != nil {
			return nil, fmt.Errorf("failed_to_check_iscsi_targets: %w", err)
		}
	}
	targetNames := make(map[uint]string, len(targets))
	for _, target := range targets {
		targetNames[target.ID] = target.TargetName
	}
	shared := make(map[string]string, len(targetIDs))
	for name, id := range targetIDs {
		if targetName := targetNames[id]; targetName != "" {
			shared[name] = targetName
		} else {
			shared[name] = fmt.Sprintf("target-%d", id)
		}
	}
	return shared, nil
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

	volume, err := s.DatasetByGUID(ctx, guid)
	if err != nil {
		return datasetLookupError(err, "volume_with_guid_%s_not_found", guid)
	}

	if volume != nil && volume.Type == gzfs.DatasetTypeVolume {
		if err := s.requireVolumesNotShared(ctx, []*gzfs.Dataset{volume}); err != nil {
			return err
		}
		release, err := s.guardDownloadStorageMutation(ctx, volume.Name)
		if err != nil {
			return err
		}
		defer release()
		wasEncrypted := volume.IsEncrypted()

		if err := volume.Destroy(ctx, true, false); err != nil {
			return s.reconcileFailedDeletion(ctx, []*gzfs.Dataset{volume}, err)
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

	volume, err := s.DatasetByGUID(ctx, guid)
	if err != nil {
		return datasetLookupError(err, "volume with guid %s not found", guid)
	}
	if volume == nil || volume.Type != gzfs.DatasetTypeVolume {
		return classifyError(ErrDatasetNotFound, "volume with guid %s not found", guid)
	}

	release, err := s.guardDownloadStorageMutation(ctx, volume.Name)
	if err != nil {
		return err
	}
	defer release()
	if s.IsDatasetInUse(guid, false) {
		return classifyError(ErrConflict, "dataset_in_use_by_vm")
	}
	if err := s.requireVolumesNotShared(ctx, []*gzfs.Dataset{volume}); err != nil {
		return err
	}
	if volume.Properties == nil {
		return classifyError(ErrConflict, "volume_properties_not_found")
	}
	if volume.Properties["readonly"].Value == "on" {
		return classifyError(ErrConflict, "volume_%s_is_read_only", volume.Name)
	}

	volSizeProp, ok := volume.Properties["volsize"]
	if !ok {
		return classifyError(ErrConflict, "volume_size_property_not_found")
	}
	capacity := utils.HumanFormatToSize(volSizeProp.Value)
	if capacity == 0 {
		return classifyError(ErrConflict, "invalid_volume_size")
	}

	var download utilitiesModels.Downloads
	if err := s.DB.WithContext(ctx).Where("uuid = ?", uuid).First(&download).Error; err != nil {
		return classifyError(ErrSourceNotFound, "source_not_found: %w", err)
	}
	defer s.DownloadStorage.ReadLock(download.StoragePool)()
	file, err := s.Libvirt.FindISOByUUID(uuid, true)
	if err != nil {
		return classifyError(ErrSourceNotFound, "source_not_found: %w", err)
	}
	if file == "" {
		return classifyError(ErrSourceNotFound, "source_not_found")
	}
	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {
			return classifyError(ErrSourceNotFound, "source_not_found")
		}
		return fmt.Errorf("failed_to_get_source_file_info: %w", err)
	}

	device := "/dev/zvol/" + volume.Name
	if _, err := os.Stat(device); err != nil {
		return classifyError(ErrConflict, "zvol_not_found: %v", err)
	}
	if err := writeImageToVolume(context.WithoutCancel(ctx), file, device, capacity); err != nil {
		return err
	}
	s.SignalDSChange(volume.Pool, volume.Name, "generic-dataset", "flash")
	return nil
}

func writeImageToVolume(ctx context.Context, source, device string, capacity uint64) error {
	info, err := qemuimg.Info(source)
	if err != nil || info == nil {
		return classifyError(ErrInvalidRequest, "unsupported_image_format")
	}
	format := qemuimg.DiskFormat(strings.ToLower(strings.TrimSpace(info.Format)))
	if !format.Valid() {
		return classifyError(ErrInvalidRequest, "unsupported_image_format_%s", info.Format)
	}
	if strings.TrimSpace(info.BackingFilename) != "" || strings.TrimSpace(info.FullBackingFilename) != "" {
		return classifyError(ErrInvalidRequest, "image_has_backing_file")
	}

	size := info.VirtualSize
	if size <= 0 {
		if stat, err := os.Stat(source); err == nil {
			size = stat.Size()
		}
	}
	if size <= 0 {
		return classifyError(ErrInvalidRequest, "invalid_image_size")
	}
	if uint64(size) > capacity {
		return classifyError(ErrConflict, "source_size_exceeds_volume_size: image needs %d bytes, volume has %d", size, capacity)
	}

	if format == qemuimg.FormatRaw {
		err = utils.FlashImageToDiskCtx(ctx, source, device)
	} else {
		err = qemuimg.ConvertContext(ctx, source, device, qemuimg.FormatRaw)
	}
	if err != nil {
		return fmt.Errorf("failed_to_flash_volume: %w", err)
	}
	return nil
}
