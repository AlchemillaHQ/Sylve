// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/beevik/etree"
	"gorm.io/gorm"
)

var filesystemTargetNameRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func isValidFilesystemTargetName(target string) bool {
	return filesystemTargetNameRegexp.MatchString(strings.TrimSpace(target))
}

func buildVirtio9PArg(index int, target string, sourcePath string, readOnly bool) string {
	device := fmt.Sprintf("%s=%s", target, sourcePath)
	if readOnly {
		device += ",ro"
	}

	return fmt.Sprintf("-s %d:0,virtio-9p,%s", index, device)
}

func (s *Service) findFilesystemDatasetByGUID(
	ctx context.Context,
	datasetGUID string,
	poolFilter string,
) (*gzfs.Dataset, error) {
	trimmedGUID := strings.TrimSpace(datasetGUID)
	if trimmedGUID == "" {
		return nil, fmt.Errorf("filesystem_dataset_guid_required")
	}

	usablePools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	for _, pool := range usablePools {
		if pool == nil {
			continue
		}

		if strings.TrimSpace(poolFilter) != "" && pool.Name != poolFilter {
			continue
		}

		datasets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, true, pool.Name)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") {
				continue
			}
			return nil, fmt.Errorf("failed_to_list_filesystems_in_pool_%s: %w", pool.Name, err)
		}

		for _, ds := range datasets {
			if ds == nil {
				continue
			}

			if strings.TrimSpace(ds.GUID) == trimmedGUID {
				return ds, nil
			}
		}
	}

	return nil, fmt.Errorf("filesystem_dataset_not_found: %s", trimmedGUID)
}

func ensureUsableFilesystemMountpoint(dataset *gzfs.Dataset) (string, error) {
	if dataset == nil {
		return "", fmt.Errorf("filesystem_dataset_not_found")
	}

	mountpoint := strings.TrimSpace(dataset.Mountpoint)
	if mountpoint == "" || mountpoint == "-" || mountpoint == "none" || mountpoint == "legacy" {
		return "", fmt.Errorf("filesystem_dataset_mountpoint_not_usable")
	}

	return mountpoint, nil
}

func (s *Service) resolveFilesystemSourcePath(ctx context.Context, storage vmModels.Storage) (string, error) {
	if storage.DatasetID == nil || *storage.DatasetID == 0 {
		return "", fmt.Errorf("filesystem_storage_dataset_not_set")
	}

	datasetName := strings.TrimSpace(storage.Dataset.Name)
	if datasetName == "" {
		var datasetRecord vmModels.VMStorageDataset
		if err := s.DB.First(&datasetRecord, "id = ?", *storage.DatasetID).Error; err != nil {
			return "", fmt.Errorf("failed_to_find_storage_dataset_record: %w", err)
		}

		datasetName = strings.TrimSpace(datasetRecord.Name)
	}

	if datasetName == "" {
		return "", fmt.Errorf("filesystem_storage_dataset_name_missing")
	}

	datasets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, false, datasetName)
	if err != nil {
		return "", fmt.Errorf("failed_to_get_filesystem_dataset_%s: %w", datasetName, err)
	}

	if len(datasets) == 0 {
		return "", fmt.Errorf("filesystem_dataset_not_found: %s", datasetName)
	}

	mountpoint, err := ensureUsableFilesystemMountpoint(datasets[0])
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, datasetName)
	}

	return mountpoint, nil
}

func (s *Service) CreateVMDisk(rid uint, storage vmModels.Storage, ctx context.Context) error {
	usable, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var target *gzfs.ZPool

	for _, pool := range usable {
		if pool.Name == storage.Pool {
			target = pool
			break
		}
	}

	if target == nil {
		return fmt.Errorf("pool_not_found: %s", storage.Pool)
	}

	var datasets []*gzfs.Dataset

	if storage.Type == vmModels.VMStorageTypeRaw || storage.Type == vmModels.VMStorageTypeZVol {
		switch storage.Type {
		case vmModels.VMStorageTypeRaw:
			datasets, err = s.GZFS.ZFS.ListByType(
				ctx,
				gzfs.DatasetTypeFilesystem,
				false,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, rid, storage.ID),
			)
		case vmModels.VMStorageTypeZVol:
			datasets, err = s.GZFS.ZFS.ListByType(
				ctx,
				gzfs.DatasetTypeVolume,
				false,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, rid, storage.ID),
			)
		}

		if err != nil {
			if !strings.Contains(err.Error(), "dataset does not exist") {
				return fmt.Errorf("failed_to_get_datasets: %w", err)
			}
		}
	}

	// var dataset *zfs.Dataset
	var dataset *gzfs.Dataset

	if len(datasets) == 0 {
		if target.Free < uint64(storage.Size) {
			return fmt.Errorf("insufficient_space_in_pool: %s", storage.Pool)
		}

		var recordSize string
		if storage.RecordSize != 0 {
			recordSize = strconv.Itoa(storage.RecordSize)
		} else {
			recordSize = "1M"
		}

		var volblocksize string
		if storage.VolBlockSize != 0 {
			volblocksize = strconv.Itoa(storage.VolBlockSize)
		} else {
			volblocksize = "16K"
		}

		props := map[string]string{
			"compression":    "zstd",
			"logbias":        "throughput",
			"primarycache":   "metadata",
			"secondarycache": "all",
		}

		switch storage.Type {
		case vmModels.VMStorageTypeRaw:
			props["atime"] = "off"

			dataset, err = s.GZFS.ZFS.CreateFilesystem(
				ctx,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, rid, storage.ID),
				utils.MergeMaps(props, map[string]string{
					"recordsize": recordSize,
				}),
			)
		case vmModels.VMStorageTypeZVol:
			dataset, err = s.GZFS.ZFS.CreateVolume(
				ctx,
				fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, rid, storage.ID),
				uint64(storage.Size),
				utils.MergeMaps(props, map[string]string{
					"volblocksize": volblocksize,
					"volmode":      "dev",
				}),
			)
		}

		if err != nil {
			return fmt.Errorf("failed_to_create_dataset: %w", err)
		}
	} else {
		dataset = datasets[0]
	}

	if storage.Type == vmModels.VMStorageTypeRaw {
		imagePath := filepath.Join(dataset.Mountpoint, fmt.Sprintf("%d.img", storage.ID))
		if _, err := os.Stat(imagePath); err == nil {
			logger.L.Info().Msgf("Disk image %s already exists, skipping creation", imagePath)
		} else {
			if err := utils.CreateOrTruncateFile(imagePath, storage.Size); err != nil {
				_ = dataset.Destroy(ctx, true, false)
				return fmt.Errorf("failed_to_create_or_truncate_image_file: %w", err)
			}
		}
	}

	if storage.DatasetID != nil && *storage.DatasetID > 0 {
		var existingDataset vmModels.VMStorageDataset
		if err := s.DB.First(&existingDataset, "id = ?", *storage.DatasetID).Error; err == nil {
			if strings.TrimSpace(existingDataset.Name) == strings.TrimSpace(dataset.Name) {
				return nil
			}
		}
	}

	storageDataset := vmModels.VMStorageDataset{
		Pool: target.Name,
		Name: dataset.Name,
		GUID: dataset.GUID,
	}

	if err := s.DB.Create(&storageDataset).Error; err != nil {
		_ = dataset.Destroy(ctx, true, false)
		return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
	}

	storage.DatasetID = &storageDataset.ID

	if err := s.DB.Save(&storage).Error; err != nil {
		_ = dataset.Destroy(ctx, true, false)
		return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
	}

	return nil
}

func (s *Service) SyncVMDisks(rid uint) error {
	return s.syncVMDisksWithDB(s.DB, rid)
}

func (s *Service) syncVMDisksWithDB(db *gorm.DB, rid uint) error {
	if db == nil {
		return fmt.Errorf("db_not_initialized")
	}

	if err := s.requireConnection(); err != nil {
		return err
	}

	off, err := s.IsDomainShutOff(rid)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	domain, err := s.conn().DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	xml, err := s.conn().DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	bhyveCommandline := doc.FindElement("//commandline")
	if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
		root := doc.Root()
		if root.SelectAttr("xmlns:bhyve") == nil {
			root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
		}
		bhyveCommandline = root.CreateElement("bhyve:commandline")
	}

	for _, arg := range bhyveCommandline.ChildElements() {
		valAttr := arg.SelectAttr("value")
		if valAttr == nil {
			continue
		}

		val := valAttr.Value

		if val == "" {
			continue
		}

		emulations := []string{
			string(libvirtServiceInterfaces.AHCICDStorageEmulation),
			string(libvirtServiceInterfaces.AHCIHDStorageEmulation),
			string(libvirtServiceInterfaces.NVMEStorageEmulation),
			string(libvirtServiceInterfaces.VirtIOStorageEmulation),
			string(libvirtServiceInterfaces.VirtIO9PStorageEmulation),
		}

		if utils.PartialStringInSlice(val, emulations) {
			bhyveCommandline.RemoveChild(arg)
		}
	}

	var vm vmModels.VM
	if err := db.Where("rid = ?", rid).First(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}

	var storages []vmModels.Storage
	if err := db.
		Preload("Dataset").
		Where("vm_id = ?", vm.ID).
		Order("boot_order ASC").
		Find(&storages).Error; err != nil {
		return fmt.Errorf("failed_to_get_vm_storages: %w", err)
	}

	argValues := []string{}

	used := parseUsedIndicesFromElement(bhyveCommandline)
	currentIndex := 10

	for _, storage := range storages {
		if !storage.Enable {
			continue
		}

		for currentIndex < 30 && used[currentIndex] {
			currentIndex++
		}

		if currentIndex >= 30 {
			return fmt.Errorf("no free indices available")
		}

		index := currentIndex
		used[index] = true
		currentIndex++

		argCommon := fmt.Sprintf("-s %d:0,%s", index, storage.Emulation)
		var argValue string
		var diskValue string

		if storage.Type == vmModels.VMStorageTypeRaw {
			diskValue = fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
				storage.Pool,
				rid,
				storage.ID,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			diskValue = fmt.Sprintf("/dev/zvol/%s/sylve/virtual-machines/%d/zvol-%d",
				storage.Pool,
				rid,
				storage.ID,
			)
		} else if storage.Type == vmModels.VMStorageTypeDiskImage {
			diskValue, err = s.FindISOByUUID(storage.DownloadUUID, true)
			if err != nil {
				return fmt.Errorf("failed_to_get_iso_path_by_uuid: %w", err)
			}

			diskValue = fmt.Sprintf("%s,ro", diskValue)
		} else if storage.Type == vmModels.VMStorageTypeFilesystem {
			sourcePath, err := s.resolveFilesystemSourcePath(context.Background(), storage)
			if err != nil {
				return fmt.Errorf("failed_to_resolve_filesystem_share_source: %w", err)
			}

			argValue = buildVirtio9PArg(
				index,
				strings.TrimSpace(storage.FilesystemTarget),
				sourcePath,
				storage.ReadOnly,
			)
			argValues = append(argValues, argValue)
			continue
		}

		argValue = fmt.Sprintf("%s,%s", argCommon, diskValue)
		argValues = append(argValues, argValue)
	}

	err = s.CreateCloudInitISO(vm)
	if err != nil {
		logger.L.Error().Err(err).Msg("vm: sync_vm_disks: failed_to_create_cloud_init_iso")
	}

	if vm.CloudInitData != "" {
		cloudInitISOPath, err := s.GetCloudInitISOPath(vm.RID)
		if err != nil {
			logger.L.Warn().Err(err).Msg("vm: sync_vm_disks: failed_to_get_cloud_init_iso_path")
		} else if cloudInitISOPath != "" {
			for currentIndex < 30 && used[currentIndex] {
				currentIndex++
			}

			if currentIndex >= 30 {
				return fmt.Errorf("no_free_indices_available_for_cloud_init_iso")
			}

			used[currentIndex] = true

			argValue := fmt.Sprintf("-s %d:0,ahci-cd,%s,ro", currentIndex, cloudInitISOPath)
			argValues = append(argValues, argValue)
		}
	}

	for _, val := range argValues {
		argElement := bhyveCommandline.CreateElement("bhyve:arg")
		argElement.CreateAttr("value", val)
	}

	newXML, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed to serialize XML: %w", err)
	}

	if err := s.conn().DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.conn().DomainDefineXML(newXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	err = s.WriteVMJson(rid)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after disk sync")
	}

	return nil
}

func (s *Service) RemoveStorageXML(rid uint, storage vmModels.Storage) error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	domain, err := s.conn().DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	xml, err := s.conn().DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_xml: %w", err)
	}

	bhyveCommandline := doc.FindElement("//commandline")
	if bhyveCommandline == nil || bhyveCommandline.Space != "bhyve" {
		root := doc.Root()
		if root.SelectAttr("xmlns:bhyve") == nil {
			root.CreateAttr("xmlns:bhyve", "http://libvirt.org/schemas/domain/bhyve/1.0")
		}
		bhyveCommandline = root.CreateElement("bhyve:commandline")
	}

	var filePath string

	if storage.Type == vmModels.VMStorageTypeDiskImage &&
		storage.DownloadUUID != "" {
		filePath, err = s.FindISOByUUID(storage.DownloadUUID, true)
		if err != nil {
			return fmt.Errorf("failed_to_find_iso_by_uuid: %w", err)
		}
	} else if storage.Type == vmModels.VMStorageTypeRaw {
		filePath = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			rid,
			storage.ID,
			storage.ID,
		)
	} else if storage.Type == vmModels.VMStorageTypeZVol {
		filePath = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
			storage.Pool,
			rid,
			storage.ID,
		)
	} else if storage.Type == vmModels.VMStorageTypeFilesystem {
		filePath = strings.TrimSpace(storage.FilesystemTarget) + "="
	}

	if filePath == "" {
		return fmt.Errorf("unable_to_determine_storage_path")
	}

	for _, arg := range bhyveCommandline.ChildElements() {
		valAttr := arg.SelectAttr("value")
		if valAttr == nil {
			continue
		}

		val := valAttr.Value
		if val == "" {
			continue
		}

		if (storage.Type == vmModels.VMStorageTypeDiskImage ||
			storage.Type == vmModels.VMStorageTypeRaw ||
			storage.Type == vmModels.VMStorageTypeZVol) &&
			strings.Contains(val, filePath) {
			bhyveCommandline.RemoveChild(arg)
		}

		if storage.Type == vmModels.VMStorageTypeFilesystem &&
			strings.Contains(val, ",virtio-9p,") &&
			strings.Contains(val, filePath) {
			bhyveCommandline.RemoveChild(arg)
		}
	}

	out, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed_to_serialize_xml: %w", err)
	}

	if err := s.conn().DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.conn().DomainDefineXML(out); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	return nil
}

type storageRuntimeHooks struct {
	createVMDisk func(rid uint, storage vmModels.Storage, ctx context.Context) error
	syncVMDisks  func(rid uint) error
	copyFile     func(src, dst string) error
}

func (s *Service) normalizeStorageRuntimeHooks(hooks storageRuntimeHooks, db *gorm.DB) storageRuntimeHooks {
	if hooks.createVMDisk == nil {
		hooks.createVMDisk = func(rid uint, storage vmModels.Storage, ctx context.Context) error {
			return s.CreateVMDisk(rid, storage, ctx)
		}
	}

	if hooks.syncVMDisks == nil {
		hooks.syncVMDisks = func(rid uint) error {
			return s.syncVMDisksWithDB(db, rid)
		}
	}

	if hooks.copyFile == nil {
		hooks.copyFile = utils.CopyFile
	}

	return hooks
}

func (s *Service) destroyManagedStorageDataset(ctx context.Context, rid uint, storage vmModels.Storage) error {
	if s == nil || s.GZFS == nil {
		return nil
	}

	var datasetType gzfs.DatasetType
	var datasetPath string

	switch storage.Type {
	case vmModels.VMStorageTypeRaw:
		datasetType = gzfs.DatasetTypeFilesystem
		datasetPath = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", storage.Pool, rid, storage.ID)
	case vmModels.VMStorageTypeZVol:
		datasetType = gzfs.DatasetTypeVolume
		datasetPath = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", storage.Pool, rid, storage.ID)
	default:
		return nil
	}

	datasets, err := s.GZFS.ZFS.ListByType(ctx, datasetType, false, datasetPath)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") {
			return nil
		}
		return fmt.Errorf("failed_to_list_storage_dataset_for_cleanup: %w", err)
	}

	for _, ds := range datasets {
		if ds == nil {
			continue
		}

		if err := ds.Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_destroy_storage_dataset_%s: %w", ds.Name, err)
		}
	}

	return nil
}

func cleanupFailedStorageMetadata(db *gorm.DB, storageID uint, storageDatasetID uint) error {
	if db == nil || storageID == 0 {
		return nil
	}

	if err := db.Delete(&vmModels.Storage{}, storageID).Error; err != nil {
		return fmt.Errorf("failed_to_delete_storage_record_during_cleanup: %w", err)
	}

	if storageDatasetID > 0 {
		if err := db.Delete(&vmModels.VMStorageDataset{}, storageDatasetID).Error; err != nil {
			return fmt.Errorf("failed_to_delete_storage_dataset_record_during_cleanup: %w", err)
		}
	}

	return nil
}

func (s *Service) StorageDetach(req libvirtServiceInterfaces.StorageDetachRequest) error {
	if err := s.requireVMMutationOwnership(req.RID); err != nil {
		return err
	}

	off, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}

	vm, err := s.GetVMByRID(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	return s.storageDetachApply(req, vm.ID, storageRuntimeHooks{})
}

func (s *Service) storageDetachApply(
	req libvirtServiceInterfaces.StorageDetachRequest,
	vmID uint,
	hooks storageRuntimeHooks,
) error {
	hooks = s.normalizeStorageRuntimeHooks(hooks, s.DB)

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		hooks = s.normalizeStorageRuntimeHooks(hooks, tx)

		var storage vmModels.Storage
		if err := tx.
			Preload("Dataset").
			First(&storage, "id = ? AND vm_id = ?", req.StorageId, vmID).
			Error; err != nil {
			return fmt.Errorf("failed_to_find_storage_record: %w", err)
		}

		if err := tx.Delete(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_delete_storage_record: %w", err)
		}

		if storage.DatasetID != nil {
			var dataset vmModels.VMStorageDataset
			if err := tx.First(&dataset, "id = ?", *storage.DatasetID).Error; err != nil {
				return fmt.Errorf("failed_to_find_storage_dataset_record: %w", err)
			}

			if err := tx.Delete(&dataset).Error; err != nil {
				return fmt.Errorf("failed_to_delete_storage_dataset_record: %w", err)
			}
		}

		return nil
	}); err != nil {
		return err
	}

	if err := hooks.syncVMDisks(req.RID); err != nil {
		return fmt.Errorf("failed_to_sync_vm_disks: %w", err)
	}

	return nil
}

func (s *Service) GetNextBootOrderIndex(vmId int) (int, error) {
	var maxBootOrder sql.NullInt64
	err := s.DB.
		Model(&vmModels.Storage{}).
		Where("vm_id = ? AND type != ?", vmId, vmModels.VMStorageTypeFilesystem).
		Select("MAX(boot_order)").
		Scan(&maxBootOrder).Error
	if err != nil {
		return 0, fmt.Errorf("failed_to_get_max_boot_order: %w", err)
	}

	if maxBootOrder.Valid {
		return int(maxBootOrder.Int64) + 1, nil
	}

	return 0, nil
}

func (s *Service) ValidateBootOrderIndex(vmId int, bootOrder int) (bool, error) {
	var count int64
	err := s.DB.
		Model(&vmModels.Storage{}).
		Where("vm_id = ? AND type != ? AND boot_order = ?", vmId, vmModels.VMStorageTypeFilesystem, bootOrder).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
	}

	return count == 0, nil
}

func (s *Service) StorageImport(req libvirtServiceInterfaces.StorageAttachRequest, vm vmModels.VM, ctx context.Context) error {
	hooks := s.normalizeStorageRuntimeHooks(storageRuntimeHooks{}, s.DB)
	return s.storageImportTx(req, vm, ctx, s.DB, hooks)
}

func (s *Service) storageImportTx(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
	tx *gorm.DB,
	hooks storageRuntimeHooks,
) (err error) {
	hooks = s.normalizeStorageRuntimeHooks(hooks, tx)

	var storage vmModels.Storage
	var createdStorageRecord bool
	var createdStorageDatasetID uint
	var createdManagedDataset bool
	var rawTempPath string
	var renamedDatasetOriginal string
	var renamedDatasetTarget string

	defer func() {
		if err == nil {
			return
		}

		if rawTempPath != "" {
			if removeErr := os.Remove(rawTempPath); removeErr != nil && !os.IsNotExist(removeErr) {
				logger.L.Warn().Err(removeErr).Str("path", rawTempPath).Msg("failed_to_remove_temp_import_file")
			}
		}

		if renamedDatasetOriginal != "" && renamedDatasetTarget != "" && s.GZFS != nil {
			targetDatasets, listErr := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeVolume, false, renamedDatasetTarget)
			if listErr == nil && len(targetDatasets) > 0 && targetDatasets[0] != nil {
				if _, renameErr := targetDatasets[0].Rename(ctx, renamedDatasetOriginal, false); renameErr != nil {
					logger.L.Warn().
						Err(renameErr).
						Str("from", renamedDatasetTarget).
						Str("to", renamedDatasetOriginal).
						Msg("failed_to_restore_renamed_imported_zvol")
				}
			}
		}

		if createdManagedDataset {
			if cleanupErr := s.destroyManagedStorageDataset(ctx, vm.RID, storage); cleanupErr != nil {
				logger.L.Warn().Err(cleanupErr).Msg("failed_to_cleanup_storage_dataset_after_attach_failure")
			}
		}

		if createdStorageRecord {
			if cleanupErr := cleanupFailedStorageMetadata(tx, storage.ID, createdStorageDatasetID); cleanupErr != nil {
				logger.L.Warn().Err(cleanupErr).Msg("failed_to_cleanup_storage_metadata_after_attach_failure")
			}
		}
	}()

	storage.Name = req.Name
	storage.VMID = vm.ID

	if req.Pool == nil || strings.TrimSpace(*req.Pool) == "" {
		storage.Pool = ""
	} else {
		storage.Pool = *req.Pool
	}

	if storage.Pool == "" &&
		(req.StorageType == libvirtServiceInterfaces.StorageTypeRaw ||
			req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL) {
		{
			return fmt.Errorf("invalid_pool")
		}
	}

	storage.Emulation = vmModels.VMStorageEmulationType(req.Emulation)
	storage.BootOrder = *req.BootOrder
	storage.Enable = true

	if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw {
		exists, err := utils.FileExists(req.RawPath)
		if err != nil {
			return fmt.Errorf("failed_to_check_raw_path_exists: %w", err)
		}

		if !exists {
			return fmt.Errorf("raw_path_does_not_exist: %s", req.RawPath)
		}

		info, err := os.Stat(req.RawPath)
		if err != nil {
			return fmt.Errorf("failed_to_stat_raw_path: %w", err)
		}

		storage.Type = vmModels.VMStorageTypeRaw
		storage.Size = info.Size()

		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		createdStorageRecord = true

		if err := hooks.createVMDisk(vm.RID, storage, ctx); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}
		createdManagedDataset = true

		datasetPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			vm.RID,
			storage.ID,
			storage.ID,
		)

		tempDatasetPath := fmt.Sprintf("%s.importing", datasetPath)
		rawTempPath = tempDatasetPath

		if err := hooks.copyFile(req.RawPath, tempDatasetPath); err != nil {
			return fmt.Errorf("failed_to_copy_raw_file_to_dataset: %w", err)
		}

		if err := os.Rename(tempDatasetPath, datasetPath); err != nil {
			return fmt.Errorf("failed_to_replace_imported_raw_file: %w", err)
		}

		rawTempPath = ""
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
		datasets, err := s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeVolume,
			true,
			*req.Pool,
		)

		if err != nil {
			return fmt.Errorf("failed_to_get_zvols_in_pool: %w", err)
		}

		var found *gzfs.Dataset
		for _, ds := range datasets {
			if ds.GUID == req.Dataset {
				found = ds
				break
			}
		}

		if found == nil {
			return fmt.Errorf("zvol_dataset_not_found_in_pool: %s", req.Dataset)
		}

		var sourcePool string
		parts := strings.SplitN(found.Name, "/", 2)
		if len(parts) > 0 {
			sourcePool = parts[0]
		}

		var volSize int64
		volSizeProp, ok := found.Properties["volsize"]
		if ok {
			volSize, err = strconv.ParseInt(volSizeProp.Value, 10, 64)
			if err != nil {
				return fmt.Errorf("failed_to_parse_volsize: %w", err)
			}
		} else {
			return fmt.Errorf("volsize_property_not_found_in_zvol_dataset")
		}

		if volSize <= 0 {
			return fmt.Errorf("invalid_volsize: %d", volSize)
		}

		storage.Size = volSize
		storage.Type = vmModels.VMStorageTypeZVol

		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		createdStorageRecord = true

		if sourcePool == *req.Pool {
			targetDatasetPath := fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
				*req.Pool,
				vm.RID,
				storage.ID,
			)

			dataset, err := found.Rename(ctx, targetDatasetPath, false)
			if err != nil || dataset == nil {
				return fmt.Errorf("failed_to_rename_zvol_dataset: %w", err)
			}

			renamedDatasetOriginal = found.Name
			renamedDatasetTarget = targetDatasetPath

			storageDataset := vmModels.VMStorageDataset{
				Pool: *req.Pool,
				Name: dataset.Name,
				GUID: dataset.GUID,
			}

			if err := tx.Create(&storageDataset).Error; err != nil {
				return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
			}
			createdStorageDatasetID = storageDataset.ID

			storage.DatasetID = &storageDataset.ID

			if err := tx.Save(&storage).Error; err != nil {
				return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
			}
		} else {
			if err := hooks.createVMDisk(vm.RID, storage, ctx); err != nil {
				return fmt.Errorf("failed_to_create_vm_disk: %w", err)
			}
			createdManagedDataset = true

			snapshotName := fmt.Sprintf("import-snap-%d-%d", vm.RID, storage.ID)
			snapshot, err := found.Snapshot(ctx, snapshotName, false)
			if err != nil {
				return fmt.Errorf("failed_to_create_snapshot_of_imported_zvol: %w", err)
			}
			defer func() {
				if snapshot == nil {
					return
				}

				if destroyErr := snapshot.Destroy(ctx, true, false); destroyErr != nil {
					logger.L.Warn().Err(destroyErr).Msg("failed_to_destroy_import_snapshot")
				}
			}()

			targetDatasetPath := fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d",
				*req.Pool,
				vm.RID,
				storage.ID,
			)

			targetDatasets, err := s.GZFS.ZFS.ListByType(
				ctx,
				gzfs.DatasetTypeVolume,
				false,
				targetDatasetPath,
			)

			if err != nil {
				return fmt.Errorf("failed_to_get_target_zvols: %w", err)
			}

			if len(targetDatasets) == 0 {
				return fmt.Errorf("target_zvol_dataset_not_found: %s", targetDatasetPath)
			}

			targetDataset := targetDatasets[0]
			_, err = snapshot.SendToDataset(ctx, targetDataset.Name, true)
			if err != nil {
				return fmt.Errorf("failed_to_send_snapshot_to_dataset: %w", err)
			}

			if err := snapshot.Destroy(ctx, true, false); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_destroy_import_snapshot")
			}
			snapshot = nil
		}
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeDiskImage {
		imagePath, err := s.FindISOByUUID(req.UUID, true)
		if err != nil {
			return fmt.Errorf("failed_to_find_iso_by_uuid: %w", err)
		}

		info, err := os.Stat(imagePath)
		if err != nil {
			return fmt.Errorf("failed_to_stat_iso_path: %w", err)
		}

		storage.Type = vmModels.VMStorageTypeDiskImage
		storage.Size = info.Size()
		storage.DownloadUUID = req.UUID

		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		createdStorageRecord = true
	}

	return hooks.syncVMDisks(vm.RID)
}

func (s *Service) StorageNew(req libvirtServiceInterfaces.StorageAttachRequest, vm vmModels.VM, ctx context.Context) error {
	hooks := s.normalizeStorageRuntimeHooks(storageRuntimeHooks{}, s.DB)
	return s.storageNewTx(req, vm, ctx, s.DB, hooks)
}

func (s *Service) storageNewTx(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
	tx *gorm.DB,
	hooks storageRuntimeHooks,
) (err error) {
	hooks = s.normalizeStorageRuntimeHooks(hooks, tx)

	var storage vmModels.Storage
	var createdStorageRecord bool
	var createdStorageDatasetID uint
	var createdManagedDataset bool

	defer func() {
		if err == nil {
			return
		}

		if createdManagedDataset {
			if cleanupErr := s.destroyManagedStorageDataset(ctx, vm.RID, storage); cleanupErr != nil {
				logger.L.Warn().Err(cleanupErr).Msg("failed_to_cleanup_storage_dataset_after_attach_failure")
			}
		}

		if createdStorageRecord {
			if cleanupErr := cleanupFailedStorageMetadata(tx, storage.ID, createdStorageDatasetID); cleanupErr != nil {
				logger.L.Warn().Err(cleanupErr).Msg("failed_to_cleanup_storage_metadata_after_attach_failure")
			}
		}
	}()

	storage.Name = req.Name
	storage.VMID = vm.ID

	if req.Pool == nil || strings.TrimSpace(*req.Pool) == "" {
		storage.Pool = ""
	} else {
		storage.Pool = *req.Pool
	}

	storage.Emulation = vmModels.VMStorageEmulationType(req.Emulation)
	if req.Size != nil {
		storage.Size = *req.Size
	} else {
		storage.Size = 0
	}
	storage.BootOrder = *req.BootOrder
	storage.Enable = true

	if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw {
		storage.Type = vmModels.VMStorageTypeRaw

		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		createdStorageRecord = true

		if err := hooks.createVMDisk(vm.RID, storage, ctx); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}
		createdManagedDataset = true

		diskPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			storage.Pool,
			vm.RID,
			storage.ID,
			storage.ID,
		)

		exists, err := utils.FileExists(diskPath)
		if err != nil {
			return fmt.Errorf("failed_to_check_created_disk_path_exists: %w", err)
		}

		if !exists {
			return fmt.Errorf("created_disk_path_does_not_exist_after_creation: %s", diskPath)
		}
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
		storage.Type = vmModels.VMStorageTypeZVol

		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		createdStorageRecord = true

		if err := hooks.createVMDisk(vm.RID, storage, ctx); err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}
		createdManagedDataset = true
	} else if req.StorageType == libvirtServiceInterfaces.StorageTypeFilesystem {
		if !isValidFilesystemTargetName(req.FilesystemTarget) {
			return fmt.Errorf("invalid_filesystem_target_name")
		}

		dataset, err := s.findFilesystemDatasetByGUID(ctx, req.Dataset, "")
		if err != nil {
			return fmt.Errorf("failed_to_find_filesystem_dataset: %w", err)
		}

		if _, err := ensureUsableFilesystemMountpoint(dataset); err != nil {
			return fmt.Errorf("failed_to_validate_filesystem_mountpoint: %w", err)
		}

		storage.Type = vmModels.VMStorageTypeFilesystem
		storage.Emulation = vmModels.VirtIO9PStorageEmulation
		storage.Size = 0
		storage.Pool = dataset.Pool
		storage.FilesystemTarget = strings.TrimSpace(req.FilesystemTarget)
		storage.ReadOnly = req.ReadOnly != nil && *req.ReadOnly

		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		createdStorageRecord = true

		storageDataset := vmModels.VMStorageDataset{
			Pool: dataset.Pool,
			Name: dataset.Name,
			GUID: dataset.GUID,
		}

		if err := tx.Create(&storageDataset).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
		}
		createdStorageDatasetID = storageDataset.ID

		storage.DatasetID = &storageDataset.ID
		if err := tx.Save(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
		}
	}

	return hooks.syncVMDisks(vm.RID)
}

func (s *Service) StorageAttach(req libvirtServiceInterfaces.StorageAttachRequest, ctx context.Context) error {
	if err := s.requireVMMutationOwnership(req.RID); err != nil {
		return err
	}

	if req.Name == "" ||
		strings.TrimSpace(req.Name) == "" ||
		len(req.Name) == 0 ||
		len(req.Name) > 128 {
		return fmt.Errorf("invalid_storage_name")
	}

	vm, err := s.GetVMByRID(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	off, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}

	var bootOrder int
	if req.BootOrder != nil {
		bootOrder = *req.BootOrder
	} else {
		nextIndex, err := s.GetNextBootOrderIndex(int(vm.ID))
		if err != nil {
			return fmt.Errorf("failed_to_get_next_boot_order_index: %w", err)
		}
		bootOrder = nextIndex
	}

	valid, err := s.ValidateBootOrderIndex(int(vm.ID), bootOrder)
	if err != nil {
		return fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
	}

	if !valid {
		return fmt.Errorf("boot_order_index_already_in_use: %d", bootOrder)
	}

	if (req.Pool == nil || strings.TrimSpace(*req.Pool) == "") &&
		(req.StorageType == libvirtServiceInterfaces.StorageTypeRaw ||
			req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL) {
		return fmt.Errorf("invalid_pool")
	}

	if req.StorageType == libvirtServiceInterfaces.StorageTypeFilesystem {
		if req.AttachType != libvirtServiceInterfaces.StorageAttachTypeNew {
			return fmt.Errorf("invalid_attach_type_for_filesystem_storage")
		}

		if strings.TrimSpace(req.Dataset) == "" {
			return fmt.Errorf("filesystem_dataset_guid_required")
		}

		if !isValidFilesystemTargetName(req.FilesystemTarget) {
			return fmt.Errorf("invalid_filesystem_target_name")
		}
	}

	if req.StorageType != libvirtServiceInterfaces.StorageTypeDiskImage {
		if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw ||
			req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
			err = s.CreateStorageParent(vm.RID, *req.Pool, ctx)
			if err != nil {
				return fmt.Errorf("failed_to_create_storage_parent: %w", err)
			}
		}
	}

	req.BootOrder = &bootOrder

	switch req.AttachType {
	case libvirtServiceInterfaces.StorageAttachTypeImport:
		return s.StorageImport(req, vm, ctx)
	case libvirtServiceInterfaces.StorageAttachTypeNew:
		return s.StorageNew(req, vm, ctx)
	}

	return fmt.Errorf("invalid_storage_attach_type: %s", req.AttachType)
}

func (s *Service) StorageUpdate(req libvirtServiceInterfaces.StorageUpdateRequest, ctx context.Context) error {
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 {
		return fmt.Errorf("invalid_storage_name")
	}

	var current vmModels.Storage
	if err := s.DB.
		Preload("Dataset").
		First(&current, "id = ?", req.ID).Error; err != nil {
		return fmt.Errorf("failed_to_find_storage_record: %w", err)
	}

	var vm vmModels.VM
	if err := s.DB.First(&vm, "id = ?", current.VMID).Error; err != nil {
		return fmt.Errorf("failed_to_find_vm_record: %w", err)
	}
	if err := s.requireVMMutationOwnership(vm.RID); err != nil {
		return err
	}

	off, err := s.IsDomainShutOff(vm.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return fmt.Errorf("domain_state_not_shutoff: %d", vm.RID)
	}

	if req.BootOrder != nil && *req.BootOrder != current.BootOrder {
		var count int64
		if err := s.DB.
			Model(&vmModels.Storage{}).
			Where("vm_id = ? AND boot_order = ? AND id != ?", current.VMID, *req.BootOrder, current.ID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
		}

		if count > 0 {
			return fmt.Errorf("boot_order_index_already_in_use: %d", *req.BootOrder)
		}

		current.BootOrder = *req.BootOrder
	}

	if req.Size == nil &&
		current.Type != vmModels.VMStorageTypeDiskImage &&
		current.Type != vmModels.VMStorageTypeFilesystem {
		return fmt.Errorf("size_required_for_storage_type: %s", current.Type)
	}

	if req.Size != nil && *req.Size != current.Size {
		newSize := *req.Size

		if newSize < current.Size {
			return fmt.Errorf("shrinking_storage_not_supported")
		}

		growBy := newSize - current.Size

		if current.Pool != "" && growBy > 0 {
			pool, err := s.GZFS.Zpool.Get(ctx, current.Pool)
			if err != nil || pool == nil {
				return err
			}

			if pool.Free < uint64(growBy) {
				return fmt.Errorf("insufficient_space_in_pool: %s", current.Pool)
			}
		}

		switch current.Type {
		case vmModels.VMStorageTypeRaw:
			imagePath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
				current.Pool,
				vm.RID,
				current.ID,
				current.ID,
			)

			if err := utils.CreateOrResizeFile(imagePath, newSize); err != nil {
				return fmt.Errorf("failed_to_resize_raw_image_file: %w", err)
			}

		case vmModels.VMStorageTypeZVol:
			dsList, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeVolume, false, current.Dataset.Name)
			if err != nil {
				return fmt.Errorf("failed_to_get_zvol_dataset: %w", err)
			}

			if len(dsList) == 0 {
				return fmt.Errorf("zvol_dataset_not_found: %s", current.Dataset.Name)
			}

			ds := dsList[0]
			var volSize uint64

			volSizeProp, ok := ds.Properties["volsize"]
			if ok {
				volSize = gzfs.ParseSize(volSizeProp.Value)
			}

			volSize = gzfs.ParseSize(volSizeProp.Value)
			newVolSize := uint64(newSize)

			if newVolSize < volSize {
				return fmt.Errorf("new_size_must_be_greater_than_or_equal_to_current_volsize")
			}

			err = ds.SetProperties(ctx, "volsize", fmt.Sprintf("%d", newVolSize))
			if err != nil {
				return fmt.Errorf("failed_to_set_zvol_volsize: %w", err)
			}

		case vmModels.VMStorageTypeDiskImage:
			return fmt.Errorf("size_edit_not_supported_for_disk_image_storage")
		case vmModels.VMStorageTypeFilesystem:
			return fmt.Errorf("size_edit_not_supported_for_filesystem_storage")
		default:
			return fmt.Errorf("size_edit_not_supported_for_storage_type: %s", current.Type)
		}

		current.Size = newSize
	}

	current.Name = req.Name
	current.Emulation = vmModels.VMStorageEmulationType(req.Emulation)
	if current.Type == vmModels.VMStorageTypeFilesystem {
		current.Emulation = vmModels.VirtIO9PStorageEmulation
	}

	if req.FilesystemTarget != nil && current.Type == vmModels.VMStorageTypeFilesystem {
		target := strings.TrimSpace(*req.FilesystemTarget)
		if !isValidFilesystemTargetName(target) {
			return fmt.Errorf("invalid_filesystem_target_name")
		}
		current.FilesystemTarget = target
	}

	if req.ReadOnly != nil && current.Type == vmModels.VMStorageTypeFilesystem {
		current.ReadOnly = *req.ReadOnly
	}

	if req.Enable != nil {
		current.Enable = *req.Enable
	}

	if err := s.DB.Save(&current).Error; err != nil {
		return fmt.Errorf("failed_to_update_storage_record: %w", err)
	}

	if err := s.SyncVMDisks(vm.RID); err != nil {
		return fmt.Errorf("failed_to_sync_vm_disks: %w", err)
	}

	return nil
}

func (s *Service) CreateStorageParent(rid uint, poolName string, ctx context.Context) error {
	pools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var created []*gzfs.Dataset

	for _, pool := range pools {
		if poolName != "" && pool.Name != poolName {
			continue
		}

		target := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool.Name, rid)
		datasets, _ := s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeFilesystem,
			false,
			target,
		)

		if len(datasets) > 0 {
			continue
		}

		props := map[string]string{
			"compression":    "zstd",
			"logbias":        "throughput",
			"primarycache":   "metadata",
			"secondarycache": "all",
		}

		ds, err := s.GZFS.ZFS.CreateFilesystem(ctx, target, props)
		if err != nil {
			for _, createdDS := range created {
				_ = createdDS.Destroy(ctx, true, false)
			}

			return fmt.Errorf("failed_to_create_%s: %w", target, err)
		}

		created = append(created, ds)
	}

	return nil
}
