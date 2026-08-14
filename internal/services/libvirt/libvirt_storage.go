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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/beevik/etree"
	"gorm.io/gorm"
)

var filesystemTargetNameRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const vmStorageCleanupTimeout = 2 * time.Minute

func detachedVMStorageContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), vmStorageCleanupTimeout)
}

func isValidFilesystemTargetName(target string) bool {
	return filesystemTargetNameRegexp.MatchString(strings.TrimSpace(target))
}

func (s *Service) findFilesystemDatasetByGUID(
	ctx context.Context,
	datasetGUID string,
	poolFilter string,
) (*gzfs.Dataset, error) {
	if s == nil || s.System == nil {
		return nil, fmt.Errorf("system_service_not_initialized")
	}
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

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

func (s *Service) findZVOLDatasetByGUID(ctx context.Context, datasetGUID string) (*gzfs.Dataset, error) {
	if s == nil || s.System == nil {
		return nil, fmt.Errorf("system_service_not_initialized")
	}
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("gzfs_not_initialized")
	}

	trimmedGUID := strings.TrimSpace(datasetGUID)
	if trimmedGUID == "" {
		return nil, fmt.Errorf("zvol_dataset_guid_required")
	}

	usablePools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	for _, pool := range usablePools {
		if pool == nil || strings.TrimSpace(pool.Name) == "" {
			continue
		}

		datasets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeVolume, true, pool.Name)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") {
				continue
			}
			return nil, fmt.Errorf("failed_to_list_zvols_in_pool_%s: %w", pool.Name, err)
		}

		for _, dataset := range datasets {
			if dataset != nil && strings.TrimSpace(dataset.GUID) == trimmedGUID {
				return dataset, nil
			}
		}
	}

	return nil, fmt.Errorf("zvol_dataset_not_found: %s", trimmedGUID)
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
	return s.resolveFilesystemSourcePathWithDB(ctx, s.DB, storage)
}

func (s *Service) resolveFilesystemSourcePathWithDB(
	ctx context.Context,
	db *gorm.DB,
	storage vmModels.Storage,
) (string, error) {
	if db == nil {
		return "", fmt.Errorf("db_not_initialized")
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return "", fmt.Errorf("gzfs_not_initialized")
	}
	if storage.DatasetID == nil || *storage.DatasetID == 0 {
		return "", fmt.Errorf("filesystem_storage_dataset_not_set")
	}

	datasetName := strings.TrimSpace(storage.Dataset.Name)
	if datasetName == "" {
		var datasetRecord vmModels.VMStorageDataset
		if err := db.First(&datasetRecord, "id = ?", *storage.DatasetID).Error; err != nil {
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

func (s *Service) resolveRawStorageImagePath(
	ctx context.Context,
	db *gorm.DB,
	rid uint,
	storage vmModels.Storage,
) (string, error) {
	if storage.Type != vmModels.VMStorageTypeRaw {
		return "", fmt.Errorf("storage_is_not_raw")
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return "", fmt.Errorf("gzfs_not_initialized")
	}

	var mountpoint string
	if storage.DatasetID != nil && *storage.DatasetID > 0 {
		var err error
		mountpoint, err = s.resolveFilesystemSourcePathWithDB(ctx, db, storage)
		if err != nil {
			return "", fmt.Errorf("failed_to_resolve_raw_dataset_mountpoint: %w", err)
		}
	} else {
		datasetName := strings.TrimSpace(storage.Dataset.Name)
		if datasetName == "" {
			datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", storage.Pool, rid, storage.ID)
		}
		datasets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeFilesystem, false, datasetName)
		if err != nil {
			return "", fmt.Errorf("failed_to_get_raw_dataset: %w", err)
		}
		if len(datasets) == 0 || datasets[0] == nil {
			return "", fmt.Errorf("filesystem_dataset_not_found: %s", datasetName)
		}
		mountpoint, err = ensureUsableFilesystemMountpoint(datasets[0])
		if err != nil {
			return "", fmt.Errorf("failed_to_resolve_raw_dataset_mountpoint: %w", err)
		}
	}

	imageID := storage.ID
	if id := storageIDFromDataset(storage.Dataset.Name, "raw"); id != 0 {
		imageID = uint(id)
	}

	return filepath.Join(mountpoint, fmt.Sprintf("%d.img", imageID)), nil
}

func (s *Service) CreateVMDisk(rid uint, storage vmModels.Storage, ctx context.Context) error {
	_, _, err := s.createVMDiskWithDB(rid, storage, ctx, s.DB, false)
	return err
}

func (s *Service) createVMDiskWithDB(
	rid uint,
	storage vmModels.Storage,
	ctx context.Context,
	db *gorm.DB,
	rejectExisting bool,
) (vmModels.Storage, bool, error) {
	if db == nil {
		return storage, false, fmt.Errorf("db_not_initialized")
	}
	if s == nil || s.System == nil {
		return storage, false, fmt.Errorf("system_service_not_initialized")
	}
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return storage, false, fmt.Errorf("gzfs_not_initialized")
	}

	usable, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return storage, false, fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var target *gzfs.ZPool
	for _, pool := range usable {
		if pool != nil && strings.TrimSpace(pool.Name) == strings.TrimSpace(storage.Pool) {
			target = pool
			break
		}
	}
	if target == nil {
		return storage, false, fmt.Errorf("pool_not_found: %s", storage.Pool)
	}

	datasetName := strings.TrimSpace(storage.Dataset.Name)
	var datasetType gzfs.DatasetType
	switch storage.Type {
	case vmModels.VMStorageTypeRaw:
		datasetType = gzfs.DatasetTypeFilesystem
		if datasetName == "" {
			datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", target.Name, rid, storage.ID)
		}
	case vmModels.VMStorageTypeZVol:
		datasetType = gzfs.DatasetTypeVolume
		if datasetName == "" {
			datasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", target.Name, rid, storage.ID)
		}
	default:
		return storage, false, fmt.Errorf("invalid_storage_type: %s", storage.Type)
	}

	datasets, err := s.GZFS.ZFS.ListByType(ctx, datasetType, false, datasetName)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "dataset does not exist") {
		return storage, false, fmt.Errorf("failed_to_get_datasets: %w", err)
	}
	if rejectExisting && len(datasets) > 0 {
		return storage, false, fmt.Errorf("storage_dataset_already_exists: %s", datasetName)
	}

	var dataset *gzfs.Dataset
	createdDataset := false
	if len(datasets) > 0 {
		dataset = datasets[0]
	} else {
		if storage.Size <= 0 {
			return storage, false, fmt.Errorf("invalid_size")
		}
		if target.Free < uint64(storage.Size) {
			return storage, false, fmt.Errorf("insufficient_space_in_pool: %s", storage.Pool)
		}

		recordSize := "1M"
		if storage.RecordSize > 0 {
			recordSize = strconv.Itoa(storage.RecordSize)
		}
		volBlockSize := "16K"
		if storage.VolBlockSize > 0 {
			volBlockSize = strconv.Itoa(storage.VolBlockSize)
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
			dataset, err = s.GZFS.ZFS.CreateFilesystem(ctx, datasetName, utils.MergeMaps(props, map[string]string{
				"recordsize": recordSize,
			}))
		case vmModels.VMStorageTypeZVol:
			dataset, err = s.GZFS.ZFS.CreateVolume(ctx, datasetName, uint64(storage.Size), utils.MergeMaps(props, map[string]string{
				"volblocksize": volBlockSize,
				"volmode":      "dev",
				"sparse":       "on",
			}))
		}
		if err != nil {
			return storage, false, fmt.Errorf("failed_to_create_dataset: %w", err)
		}
		if dataset == nil {
			return storage, false, fmt.Errorf("failed_to_create_dataset: empty_result")
		}
		createdDataset = true
	}

	cleanupCreatedDataset := func(cause error) (vmModels.Storage, bool, error) {
		if !createdDataset || dataset == nil {
			return storage, createdDataset, cause
		}
		cleanupCtx, cancel := detachedVMStorageContext(ctx)
		defer cancel()
		if cleanupErr := dataset.Destroy(cleanupCtx, true, false); cleanupErr != nil {
			return storage, createdDataset, errors.Join(cause, fmt.Errorf("failed_to_cleanup_created_storage_dataset: %w", cleanupErr))
		}
		return storage, false, cause
	}

	if storage.Type == vmModels.VMStorageTypeRaw {
		if err := dataset.Mount(ctx, false); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "already mounted") {
			return cleanupCreatedDataset(fmt.Errorf("failed_to_mount_raw_child_dataset: %w", err))
		}
		mountpoint, err := ensureUsableFilesystemMountpoint(dataset)
		if err != nil {
			return cleanupCreatedDataset(err)
		}
		imageID := storage.ID
		if id := storageIDFromDataset(dataset.Name, "raw"); id != 0 {
			imageID = uint(id)
		}
		imagePath := filepath.Join(mountpoint, fmt.Sprintf("%d.img", imageID))
		if _, err := os.Stat(imagePath); errors.Is(err, os.ErrNotExist) {
			if err := utils.CreateOrTruncateFile(imagePath, storage.Size); err != nil {
				return cleanupCreatedDataset(fmt.Errorf("failed_to_create_or_truncate_image_file: %w", err))
			}
		} else if err != nil {
			return cleanupCreatedDataset(fmt.Errorf("failed_to_stat_raw_image_file: %w", err))
		}
	}

	if storage.DatasetID != nil && *storage.DatasetID > 0 {
		var existing vmModels.VMStorageDataset
		if err := db.First(&existing, "id = ?", *storage.DatasetID).Error; err == nil &&
			strings.TrimSpace(existing.Name) == strings.TrimSpace(dataset.Name) {
			storage.Dataset = existing
			return storage, createdDataset, nil
		}
	}

	storageDataset := vmModels.VMStorageDataset{Pool: target.Name, Name: dataset.Name, GUID: dataset.GUID}
	if err := db.Create(&storageDataset).Error; err != nil {
		return cleanupCreatedDataset(fmt.Errorf("failed_to_create_storage_dataset_record: %w", err))
	}
	storage.DatasetID = &storageDataset.ID
	storage.Dataset = storageDataset
	if err := db.Save(&storage).Error; err != nil {
		_ = db.Delete(&storageDataset).Error
		return cleanupCreatedDataset(fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err))
	}

	return storage, createdDataset, nil
}

func (s *Service) SyncVMDisks(rid uint) error {
	return s.syncVMDisksWithDB(context.Background(), s.DB, rid)
}

func (s *Service) syncVMDisksWithDB(ctx context.Context, db *gorm.DB, rid uint) error {
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

	root := doc.Root()
	devicesEl := root.FindElement("devices")
	if devicesEl == nil {
		devicesEl = root.CreateElement("devices")
	}

	for _, el := range devicesEl.FindElements("filesystem") {
		devicesEl.RemoveChild(el)
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

	used := parseUsedIndicesFromDocument(doc)
	currentIndex := 10

	for _, storage := range storages {
		if !storage.Enable {
			continue
		}

		if storage.Type == vmModels.VMStorageTypeFilesystem {
			sourcePath, err := s.resolveFilesystemSourcePathWithDB(ctx, db, storage)
			if err != nil {
				return fmt.Errorf("failed_to_resolve_filesystem_share_source: %w", err)
			}

			fsEl := devicesEl.CreateElement("filesystem")
			fsEl.CreateAttr("type", "mount")

			srcEl := fsEl.CreateElement("source")
			srcEl.CreateAttr("dir", sourcePath)

			tgtEl := fsEl.CreateElement("target")
			tgtEl.CreateAttr("dir", strings.TrimSpace(storage.FilesystemTarget))

			if storage.ReadOnly {
				fsEl.CreateElement("readonly")
			}

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
			diskValue, err = s.resolveRawStorageImagePath(ctx, db, rid, storage)
			if err != nil {
				return fmt.Errorf("failed_to_resolve_raw_storage_path: %w", err)
			}
		} else if storage.Type == vmModels.VMStorageTypeZVol {
			if storage.Dataset.Name != "" {
				diskValue = "/dev/zvol/" + storage.Dataset.Name
			} else {
				diskValue = fmt.Sprintf("/dev/zvol/%s/sylve/virtual-machines/%d/zvol-%d",
					storage.Pool,
					rid,
					storage.ID,
				)
			}
		} else if storage.Type == vmModels.VMStorageTypeDiskImage {
			diskValue, err = s.FindISOByUUID(storage.DownloadUUID, true)
			if err != nil {
				return fmt.Errorf("failed_to_get_iso_path_by_uuid: %w", err)
			}

			diskValue = fmt.Sprintf("%s,ro", diskValue)
		}

		argValue = fmt.Sprintf("%s,%s", argCommon, diskValue)
		argValues = append(argValues, argValue)
	}

	if err := s.CreateCloudInitISO(vm); err != nil {
		return fmt.Errorf("failed_to_create_cloud_init_iso: %w", err)
	}

	if vmHasCloudInitConfiguration(vm) {
		cloudInitISOPath, err := s.GetCloudInitISOPath(vm.RID)
		if err != nil {
			return fmt.Errorf("failed_to_get_cloud_init_iso_path: %w", err)
		}
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

	for _, val := range argValues {
		argElement := bhyveCommandline.CreateElement("bhyve:arg")
		argElement.CreateAttr("value", val)
	}

	newXML, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed to serialize XML: %w", err)
	}

	if _, err := s.conn().DomainDefineXML(newXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	if err := s.writeVMJsonWithDB(db, rid); err != nil {
		return fmt.Errorf("failed_to_write_vm_json_after_disk_sync: %w", err)
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

	if storage.Type == vmModels.VMStorageTypeFilesystem {
		root := doc.Root()
		devicesEl := root.FindElement("devices")
		if devicesEl != nil {
			targetName := strings.TrimSpace(storage.FilesystemTarget)
			for _, el := range devicesEl.FindElements("filesystem") {
				tgtEl := el.FindElement("target")
				if tgtEl != nil {
					if tgtEl.SelectAttrValue("dir", "") == targetName {
						devicesEl.RemoveChild(el)
					}
				}
			}
		}
	}

	out, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed_to_serialize_xml: %w", err)
	}

	if _, err := s.conn().DomainDefineXML(out); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	return nil
}

type storageRuntimeHooks struct {
	createVMDisk func(
		rid uint,
		storage vmModels.Storage,
		ctx context.Context,
		db *gorm.DB,
	) (vmModels.Storage, bool, error)
	syncVMDisks func(ctx context.Context, db *gorm.DB, rid uint) error
	copyFile    func(src, dst string) error
}

func (s *Service) normalizeStorageRuntimeHooks(hooks storageRuntimeHooks) storageRuntimeHooks {
	if hooks.createVMDisk == nil {
		hooks.createVMDisk = func(
			rid uint,
			storage vmModels.Storage,
			ctx context.Context,
			db *gorm.DB,
		) (vmModels.Storage, bool, error) {
			return s.createVMDiskWithDB(rid, storage, ctx, db, true)
		}
	}

	if hooks.syncVMDisks == nil {
		hooks.syncVMDisks = func(ctx context.Context, db *gorm.DB, rid uint) error {
			return s.syncVMDisksWithDB(ctx, db, rid)
		}
	}

	if hooks.copyFile == nil {
		hooks.copyFile = utils.CopyFile
	}

	return hooks
}

func (s *Service) restoreVMStorageMutation(rid uint, oldXML string) error {
	var restoreErr error
	if strings.TrimSpace(oldXML) != "" {
		if _, err := s.conn().DomainDefineXML(oldXML); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_domain_xml: %w", err))
		}
	}
	if err := s.WriteVMJson(rid); err != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_vm_json: %w", err))
	}
	return restoreErr
}

func (s *Service) destroyManagedStorageDataset(ctx context.Context, rid uint, storage vmModels.Storage) error {
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return fmt.Errorf("gzfs_not_initialized")
	}

	var datasetType gzfs.DatasetType
	var datasetPath string

	datasetPath = storage.Dataset.Name
	if datasetPath == "" {
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
	} else {
		switch storage.Type {
		case vmModels.VMStorageTypeRaw:
			datasetType = gzfs.DatasetTypeFilesystem
		case vmModels.VMStorageTypeZVol:
			datasetType = gzfs.DatasetTypeVolume
		default:
			return nil
		}
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

func (s *Service) StorageDetach(
	req libvirtServiceInterfaces.StorageDetachRequest,
	ctx context.Context,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if req.RID == 0 {
		return fmt.Errorf("invalid_rid")
	}
	if req.StorageID == 0 {
		return fmt.Errorf("invalid_storage_id")
	}

	vm, err := s.GetVMByRID(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}
	var storage vmModels.Storage
	if err := s.DB.Select("id").First(&storage, "id = ? AND vm_id = ?", req.StorageID, vm.ID).Error; err != nil {
		return fmt.Errorf("failed_to_find_storage_record: %w", err)
	}
	if err := s.requireVMStorageTopologyMutable(req.RID); err != nil {
		return err
	}
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

	oldXML, err := s.GetVMXML(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_capture_domain_xml: %w", err)
	}

	err = s.storageDetachApply(ctx, req, vm.ID, storageRuntimeHooks{})
	if err == nil {
		return nil
	}

	if restoreErr := s.restoreVMStorageMutation(req.RID, oldXML); restoreErr != nil {
		return errors.Join(err, fmt.Errorf("storage_reconciliation_failed: %w", restoreErr))
	}
	return err
}

func (s *Service) storageDetachApply(
	ctx context.Context,
	req libvirtServiceInterfaces.StorageDetachRequest,
	vmID uint,
	hooks storageRuntimeHooks,
) error {
	hooks = s.normalizeStorageRuntimeHooks(hooks)

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var storage vmModels.Storage
		if err := tx.
			Preload("Dataset").
			First(&storage, "id = ? AND vm_id = ?", req.StorageID, vmID).
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

		if err := hooks.syncVMDisks(ctx, tx, req.RID); err != nil {
			return fmt.Errorf("failed_to_sync_vm_disks: %w", err)
		}

		return nil
	}); err != nil {
		return err
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

type storageAttachMutationState struct {
	storage                vmModels.Storage
	createdManagedDataset  bool
	rawTempPath            string
	renamedDatasetOriginal string
	renamedDatasetTarget   string
}

func (s *Service) cleanupStorageAttachFailure(
	ctx context.Context,
	vm vmModels.VM,
	state *storageAttachMutationState,
) error {
	if state == nil {
		return nil
	}

	cleanupCtx, cancel := detachedVMStorageContext(ctx)
	defer cancel()
	var cleanupErr error

	if state.rawTempPath != "" {
		if err := os.Remove(state.rawTempPath); err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("failed_to_remove_temp_import_file: %w", err))
		}
	}

	if state.renamedDatasetOriginal != "" && state.renamedDatasetTarget != "" &&
		s != nil && s.GZFS != nil && s.GZFS.ZFS != nil {
		targets, err := s.GZFS.ZFS.ListByType(
			cleanupCtx,
			gzfs.DatasetTypeVolume,
			false,
			state.renamedDatasetTarget,
		)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("failed_to_find_renamed_imported_zvol: %w", err))
		} else if len(targets) > 0 && targets[0] != nil {
			if _, err := targets[0].Rename(cleanupCtx, state.renamedDatasetOriginal, false); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("failed_to_restore_renamed_imported_zvol: %w", err))
			}
		}
	}

	if state.createdManagedDataset {
		if err := s.destroyManagedStorageDataset(cleanupCtx, vm.RID, state.storage); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("failed_to_cleanup_storage_dataset: %w", err))
		}
	}

	return cleanupErr
}

func baseStorageForAttach(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
) vmModels.Storage {
	bootOrder := 0
	if req.BootOrder != nil {
		bootOrder = *req.BootOrder
	}
	storage := vmModels.Storage{
		Name:      strings.TrimSpace(req.Name),
		VMID:      vm.ID,
		Enable:    true,
		Emulation: vmModels.VMStorageEmulationType(req.Emulation),
		BootOrder: bootOrder,
	}
	if req.Pool != nil {
		storage.Pool = strings.TrimSpace(*req.Pool)
	}
	if req.RecordSize != nil {
		storage.RecordSize = *req.RecordSize
	}
	if req.VolBlockSize != nil {
		storage.VolBlockSize = *req.VolBlockSize
	}
	return storage
}

func (s *Service) StorageImport(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
) error {
	req.RID = vm.RID
	req.AttachType = libvirtServiceInterfaces.StorageAttachTypeImport
	_, err := s.StorageAttach(req, ctx)
	return err
}

func (s *Service) storageImportTx(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
	tx *gorm.DB,
	hooks storageRuntimeHooks,
) error {
	state := &storageAttachMutationState{}
	hooks = s.normalizeStorageRuntimeHooks(hooks)
	err := s.storageImportTxWithState(req, vm, ctx, tx, hooks, state)
	if err == nil {
		err = hooks.syncVMDisks(ctx, tx, vm.RID)
	}
	if err != nil {
		if cleanupErr := s.cleanupStorageAttachFailure(ctx, vm, state); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("storage_cleanup_failed: %w", cleanupErr))
		}
	}
	return err
}

func (s *Service) storageImportTxWithState(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
	tx *gorm.DB,
	hooks storageRuntimeHooks,
	state *storageAttachMutationState,
) (err error) {
	storage := baseStorageForAttach(req, vm)

	switch req.StorageType {
	case libvirtServiceInterfaces.StorageTypeRaw:
		info, err := os.Stat(req.RawPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("raw_path_does_not_exist: %s", req.RawPath)
			}
			return fmt.Errorf("failed_to_stat_raw_path: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("raw_path_must_be_regular_file")
		}

		storage.Type = vmModels.VMStorageTypeRaw
		storage.Size = info.Size()
		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		storage, state.createdManagedDataset, err = hooks.createVMDisk(vm.RID, storage, ctx, tx)
		state.storage = storage
		if err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}

		diskPath, err := s.resolveRawStorageImagePath(ctx, tx, vm.RID, storage)
		if err != nil {
			return err
		}
		state.rawTempPath = diskPath + ".importing"
		if err := hooks.copyFile(req.RawPath, state.rawTempPath); err != nil {
			return fmt.Errorf("failed_to_copy_raw_file_to_dataset: %w", err)
		}
		if err := os.Rename(state.rawTempPath, diskPath); err != nil {
			return fmt.Errorf("failed_to_replace_imported_raw_file: %w", err)
		}
		state.rawTempPath = ""

	case libvirtServiceInterfaces.StorageTypeZVOL:
		found, err := s.findZVOLDatasetByGUID(ctx, req.Dataset)
		if err != nil {
			return err
		}
		var references int64
		if err := tx.Model(&vmModels.Storage{}).
			Joins("JOIN vm_storage_datasets ON vm_storage_datasets.id = vm_storages.dataset_id").
			Where("vm_storage_datasets.guid = ?", strings.TrimSpace(req.Dataset)).
			Count(&references).Error; err != nil {
			return fmt.Errorf("failed_to_check_zvol_dataset_usage: %w", err)
		}
		if references > 0 {
			return fmt.Errorf("zvol_dataset_already_attached")
		}

		volSizeProperty, ok := found.Properties["volsize"]
		if !ok {
			return fmt.Errorf("volsize_property_not_found_in_zvol_dataset")
		}
		volSize, err := strconv.ParseInt(volSizeProperty.Value, 10, 64)
		if err != nil || volSize <= 0 {
			return fmt.Errorf("invalid_volsize: %s", volSizeProperty.Value)
		}
		storage.Type = vmModels.VMStorageTypeZVol
		storage.Size = volSize
		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		state.storage = storage

		sourcePool := strings.SplitN(found.Name, "/", 2)[0]
		targetPath := fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", storage.Pool, vm.RID, storage.ID)
		if sourcePool == storage.Pool {
			state.renamedDatasetOriginal = found.Name
			state.renamedDatasetTarget = targetPath
			renamed, err := found.Rename(ctx, targetPath, false)
			if err != nil {
				return fmt.Errorf("failed_to_rename_zvol_dataset: %w", err)
			}
			if renamed == nil {
				return fmt.Errorf("failed_to_rename_zvol_dataset: empty_result")
			}

			datasetRecord := vmModels.VMStorageDataset{
				Pool: storage.Pool,
				Name: renamed.Name,
				GUID: renamed.GUID,
			}
			if err := tx.Create(&datasetRecord).Error; err != nil {
				return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
			}
			storage.DatasetID = &datasetRecord.ID
			storage.Dataset = datasetRecord
			if err := tx.Save(&storage).Error; err != nil {
				return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
			}
			state.storage = storage
		} else {
			storage, state.createdManagedDataset, err = hooks.createVMDisk(vm.RID, storage, ctx, tx)
			state.storage = storage
			if err != nil {
				return fmt.Errorf("failed_to_create_vm_disk: %w", err)
			}

			snapshotName := fmt.Sprintf("sylve-import-%d-%d-%d", vm.RID, storage.ID, time.Now().UTC().UnixNano())
			snapshot, err := found.Snapshot(ctx, snapshotName, false)
			if err != nil {
				return fmt.Errorf("failed_to_create_snapshot_of_imported_zvol: %w", err)
			}
			defer func() {
				if snapshot == nil {
					return
				}
				cleanupCtx, cancel := detachedVMStorageContext(ctx)
				defer cancel()
				if cleanupErr := snapshot.Destroy(cleanupCtx, true, false); cleanupErr != nil {
					err = errors.Join(err, fmt.Errorf("failed_to_destroy_import_snapshot: %w", cleanupErr))
				}
			}()

			targets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeVolume, false, targetPath)
			if err != nil {
				return fmt.Errorf("failed_to_get_target_zvols: %w", err)
			}
			if len(targets) == 0 || targets[0] == nil {
				return fmt.Errorf("target_zvol_dataset_not_found: %s", targetPath)
			}
			if _, err := snapshot.SendToDataset(ctx, targets[0].Name, true); err != nil {
				return fmt.Errorf("failed_to_send_snapshot_to_dataset: %w", err)
			}

			refreshed, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeVolume, false, targetPath)
			if err != nil || len(refreshed) == 0 || refreshed[0] == nil {
				return fmt.Errorf("failed_to_refresh_imported_zvol: %w", err)
			}
			if storage.DatasetID == nil {
				return fmt.Errorf("storage_dataset_record_missing_after_import")
			}
			storage.Dataset.Name = refreshed[0].Name
			storage.Dataset.GUID = refreshed[0].GUID
			storage.Dataset.Pool = storage.Pool
			if err := tx.Save(&storage.Dataset).Error; err != nil {
				return fmt.Errorf("failed_to_refresh_storage_dataset_record: %w", err)
			}
			state.storage = storage
		}

	case libvirtServiceInterfaces.StorageTypeDiskImage:
		imagePath, err := s.FindISOByUUID(req.UUID, true)
		if err != nil {
			return fmt.Errorf("failed_to_find_iso_by_uuid: %w", err)
		}
		info, err := os.Stat(imagePath)
		if err != nil {
			return fmt.Errorf("failed_to_stat_iso_path: %w", err)
		}
		storage.Type = vmModels.VMStorageTypeDiskImage
		storage.Pool = ""
		storage.Size = info.Size()
		storage.DownloadUUID = req.UUID
		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}
		state.storage = storage

	default:
		return fmt.Errorf("invalid_storage_type: %s", req.StorageType)
	}

	return nil
}

func (s *Service) StorageNew(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
) error {
	req.RID = vm.RID
	req.AttachType = libvirtServiceInterfaces.StorageAttachTypeNew
	_, err := s.StorageAttach(req, ctx)
	return err
}

func (s *Service) storageNewTx(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
	tx *gorm.DB,
	hooks storageRuntimeHooks,
) error {
	state := &storageAttachMutationState{}
	hooks = s.normalizeStorageRuntimeHooks(hooks)
	err := s.storageNewTxWithState(req, vm, ctx, tx, hooks, state)
	if err == nil {
		err = hooks.syncVMDisks(ctx, tx, vm.RID)
	}
	if err != nil {
		if cleanupErr := s.cleanupStorageAttachFailure(ctx, vm, state); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("storage_cleanup_failed: %w", cleanupErr))
		}
	}
	return err
}

func (s *Service) storageNewTxWithState(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
	tx *gorm.DB,
	hooks storageRuntimeHooks,
	state *storageAttachMutationState,
) error {
	storage := baseStorageForAttach(req, vm)
	if req.Size != nil {
		storage.Size = *req.Size
	}

	switch req.StorageType {
	case libvirtServiceInterfaces.StorageTypeRaw, libvirtServiceInterfaces.StorageTypeZVOL:
		if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw {
			storage.Type = vmModels.VMStorageTypeRaw
		} else {
			storage.Type = vmModels.VMStorageTypeZVol
		}
		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		var err error
		storage, state.createdManagedDataset, err = hooks.createVMDisk(vm.RID, storage, ctx, tx)
		state.storage = storage
		if err != nil {
			return fmt.Errorf("failed_to_create_vm_disk: %w", err)
		}
		if storage.Type == vmModels.VMStorageTypeRaw {
			diskPath, err := s.resolveRawStorageImagePath(ctx, tx, vm.RID, storage)
			if err != nil {
				return err
			}
			if info, err := os.Stat(diskPath); err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("created_disk_path_does_not_exist_after_creation: %s", diskPath)
			}
		}

	case libvirtServiceInterfaces.StorageTypeFilesystem:
		dataset, err := s.findFilesystemDatasetByGUID(ctx, req.Dataset, "")
		if err != nil {
			return fmt.Errorf("failed_to_find_filesystem_dataset: %w", err)
		}
		if _, err := ensureUsableFilesystemMountpoint(dataset); err != nil {
			return fmt.Errorf("failed_to_validate_filesystem_mountpoint: %w", err)
		}

		target := strings.TrimSpace(req.FilesystemTarget)
		var targetCount int64
		if err := tx.Model(&vmModels.Storage{}).
			Where("vm_id = ? AND type = ? AND filesystem_target = ?", vm.ID, vmModels.VMStorageTypeFilesystem, target).
			Count(&targetCount).Error; err != nil {
			return fmt.Errorf("failed_to_check_filesystem_target_usage: %w", err)
		}
		if targetCount > 0 {
			return fmt.Errorf("filesystem_target_already_in_use")
		}

		storage.Type = vmModels.VMStorageTypeFilesystem
		storage.Emulation = vmModels.VirtIO9PStorageEmulation
		storage.Size = 0
		storage.Pool = dataset.Pool
		storage.FilesystemTarget = target
		storage.ReadOnly = req.ReadOnly != nil && *req.ReadOnly
		if err := tx.Create(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_record: %w", err)
		}

		datasetRecord := vmModels.VMStorageDataset{Pool: dataset.Pool, Name: dataset.Name, GUID: dataset.GUID}
		if err := tx.Create(&datasetRecord).Error; err != nil {
			return fmt.Errorf("failed_to_create_storage_dataset_record: %w", err)
		}
		storage.DatasetID = &datasetRecord.ID
		storage.Dataset = datasetRecord
		if err := tx.Save(&storage).Error; err != nil {
			return fmt.Errorf("failed_to_update_storage_with_dataset_id: %w", err)
		}
		state.storage = storage

	default:
		return fmt.Errorf("invalid_storage_type: %s", req.StorageType)
	}

	return nil
}

func (s *Service) storageAttachApply(
	req libvirtServiceInterfaces.StorageAttachRequest,
	vm vmModels.VM,
	ctx context.Context,
	hooks storageRuntimeHooks,
) (vmModels.Storage, error) {
	state := &storageAttachMutationState{}
	hooks = s.normalizeStorageRuntimeHooks(hooks)

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		switch req.AttachType {
		case libvirtServiceInterfaces.StorageAttachTypeImport:
			err = s.storageImportTxWithState(req, vm, ctx, tx, hooks, state)
		case libvirtServiceInterfaces.StorageAttachTypeNew:
			err = s.storageNewTxWithState(req, vm, ctx, tx, hooks, state)
		default:
			err = fmt.Errorf("invalid_storage_attach_type: %s", req.AttachType)
		}
		if err != nil {
			return err
		}
		if err := hooks.syncVMDisks(ctx, tx, vm.RID); err != nil {
			return fmt.Errorf("failed_to_sync_vm_disks: %w", err)
		}
		return nil
	})
	if err != nil {
		if cleanupErr := s.cleanupStorageAttachFailure(ctx, vm, state); cleanupErr != nil {
			return vmModels.Storage{}, errors.Join(err, fmt.Errorf("storage_cleanup_failed: %w", cleanupErr))
		}
		return vmModels.Storage{}, err
	}

	if state.storage.ID == 0 {
		return vmModels.Storage{}, fmt.Errorf("created_storage_result_missing")
	}
	return state.storage, nil
}

func (s *Service) StorageAttach(
	req libvirtServiceInterfaces.StorageAttachRequest,
	ctx context.Context,
) (*vmModels.Storage, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if req.RID == 0 {
		return nil, fmt.Errorf("invalid_rid")
	}
	if err := s.requireVMStorageTopologyMutable(req.RID); err != nil {
		return nil, err
	}
	if err := s.requireVMMutationOwnership(req.RID); err != nil {
		return nil, err
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 128 {
		return nil, fmt.Errorf("invalid_storage_name")
	}
	req.RawPath = strings.TrimSpace(req.RawPath)
	req.Dataset = strings.TrimSpace(req.Dataset)
	req.UUID = strings.TrimSpace(req.UUID)
	req.FilesystemTarget = strings.TrimSpace(req.FilesystemTarget)
	if req.Pool != nil {
		pool := strings.TrimSpace(*req.Pool)
		req.Pool = &pool
	}
	if req.RecordSize != nil && *req.RecordSize <= 0 {
		return nil, fmt.Errorf("invalid_record_size")
	}
	if req.VolBlockSize != nil && *req.VolBlockSize <= 0 {
		return nil, fmt.Errorf("invalid_volblock_size")
	}
	if req.BootOrder != nil && *req.BootOrder < 0 {
		return nil, fmt.Errorf("invalid_boot_order")
	}

	switch req.StorageType {
	case libvirtServiceInterfaces.StorageTypeRaw,
		libvirtServiceInterfaces.StorageTypeZVOL,
		libvirtServiceInterfaces.StorageTypeDiskImage,
		libvirtServiceInterfaces.StorageTypeFilesystem:
	default:
		return nil, fmt.Errorf("invalid_storage_type: %s", req.StorageType)
	}
	switch req.Emulation {
	case libvirtServiceInterfaces.VirtIOStorageEmulation,
		libvirtServiceInterfaces.VirtIO9PStorageEmulation,
		libvirtServiceInterfaces.AHCIHDStorageEmulation,
		libvirtServiceInterfaces.AHCICDStorageEmulation,
		libvirtServiceInterfaces.NVMEStorageEmulation:
	default:
		return nil, fmt.Errorf("invalid_storage_emulation: %s", req.Emulation)
	}
	if req.StorageType == libvirtServiceInterfaces.StorageTypeFilesystem {
		if req.Emulation != libvirtServiceInterfaces.VirtIO9PStorageEmulation {
			return nil, fmt.Errorf("invalid_storage_emulation")
		}
	} else if req.Emulation == libvirtServiceInterfaces.VirtIO9PStorageEmulation {
		return nil, fmt.Errorf("invalid_storage_emulation")
	}

	switch req.AttachType {
	case libvirtServiceInterfaces.StorageAttachTypeImport:
		if req.StorageType == libvirtServiceInterfaces.StorageTypeFilesystem {
			return nil, fmt.Errorf("invalid_attach_type_for_filesystem_storage")
		}
	case libvirtServiceInterfaces.StorageAttachTypeNew:
		if req.StorageType == libvirtServiceInterfaces.StorageTypeDiskImage {
			return nil, fmt.Errorf("invalid_attach_type_for_image_storage")
		}
	default:
		return nil, fmt.Errorf("invalid_storage_attach_type: %s", req.AttachType)
	}

	if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw ||
		req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
		if req.Pool == nil || *req.Pool == "" {
			return nil, fmt.Errorf("invalid_pool")
		}
	}
	if req.AttachType == libvirtServiceInterfaces.StorageAttachTypeNew &&
		(req.StorageType == libvirtServiceInterfaces.StorageTypeRaw ||
			req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL) &&
		(req.Size == nil || *req.Size <= 0) {
		return nil, fmt.Errorf("invalid_size")
	}
	if req.AttachType == libvirtServiceInterfaces.StorageAttachTypeImport &&
		req.StorageType == libvirtServiceInterfaces.StorageTypeRaw {
		if req.RawPath == "" || !filepath.IsAbs(req.RawPath) {
			return nil, fmt.Errorf("invalid_raw_path")
		}
		info, err := os.Stat(req.RawPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("raw_path_does_not_exist: %s", req.RawPath)
			}
			return nil, fmt.Errorf("failed_to_stat_raw_path: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("raw_path_must_be_regular_file")
		}
	}
	if req.AttachType == libvirtServiceInterfaces.StorageAttachTypeImport &&
		req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL && req.Dataset == "" {
		return nil, fmt.Errorf("zvol_dataset_guid_required")
	}
	if req.StorageType == libvirtServiceInterfaces.StorageTypeDiskImage && req.UUID == "" {
		return nil, fmt.Errorf("download_uuid_required")
	}
	if req.StorageType == libvirtServiceInterfaces.StorageTypeFilesystem {
		if req.Dataset == "" {
			return nil, fmt.Errorf("filesystem_dataset_guid_required")
		}
		if !isValidFilesystemTargetName(req.FilesystemTarget) {
			return nil, fmt.Errorf("invalid_filesystem_target_name")
		}
	}

	vm, err := s.GetVMByRID(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	off, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}

	if !off {
		return nil, fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}

	var bootOrder int
	if req.StorageType == libvirtServiceInterfaces.StorageTypeFilesystem {
		bootOrder = 0
	} else if req.BootOrder != nil {
		bootOrder = *req.BootOrder
	} else {
		nextIndex, err := s.GetNextBootOrderIndex(int(vm.ID))
		if err != nil {
			return nil, fmt.Errorf("failed_to_get_next_boot_order_index: %w", err)
		}
		bootOrder = nextIndex
	}

	if req.StorageType != libvirtServiceInterfaces.StorageTypeFilesystem {
		valid, err := s.ValidateBootOrderIndex(int(vm.ID), bootOrder)
		if err != nil {
			return nil, fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
		}
		if !valid {
			return nil, fmt.Errorf("boot_order_index_already_in_use: %d", bootOrder)
		}
	}

	if req.StorageType == libvirtServiceInterfaces.StorageTypeRaw ||
		req.StorageType == libvirtServiceInterfaces.StorageTypeZVOL {
		if err := s.CreateStorageParent(vm.RID, *req.Pool, ctx); err != nil {
			return nil, fmt.Errorf("failed_to_create_storage_parent: %w", err)
		}
	}

	req.BootOrder = &bootOrder
	oldXML, err := s.GetVMXML(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_capture_domain_xml: %w", err)
	}

	created, err := s.storageAttachApply(req, vm, ctx, storageRuntimeHooks{})
	if err == nil {
		return &created, nil
	}
	if restoreErr := s.restoreVMStorageMutation(req.RID, oldXML); restoreErr != nil {
		return nil, errors.Join(err, fmt.Errorf("storage_reconciliation_failed: %w", restoreErr))
	}
	return nil, err
}

func (s *Service) StorageUpdate(
	req libvirtServiceInterfaces.StorageUpdateRequest,
	ctx context.Context,
) (*vmModels.Storage, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if req.RID == 0 {
		return nil, fmt.Errorf("invalid_rid")
	}
	if req.ID == 0 {
		return nil, fmt.Errorf("invalid_storage_id")
	}
	if req.Name == nil && req.Size == nil && req.Emulation == nil && req.BootOrder == nil &&
		req.Enable == nil && req.FilesystemTarget == nil && req.ReadOnly == nil {
		return nil, fmt.Errorf("empty_storage_update")
	}

	vm, err := s.GetVMByRID(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}
	var current vmModels.Storage
	if err := s.DB.Preload("Dataset").
		First(&current, "id = ? AND vm_id = ?", req.ID, vm.ID).Error; err != nil {
		return nil, fmt.Errorf("failed_to_find_storage_record: %w", err)
	}
	if err := s.requireVMStorageTopologyMutable(req.RID); err != nil {
		return nil, err
	}
	if err := s.requireVMMutationOwnership(req.RID); err != nil {
		return nil, err
	}

	off, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !off {
		return nil, fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 128 {
			return nil, fmt.Errorf("invalid_storage_name")
		}
		req.Name = &name
	}
	if req.Size != nil && *req.Size <= 0 {
		return nil, fmt.Errorf("invalid_size")
	}
	if req.BootOrder != nil && *req.BootOrder < 0 {
		return nil, fmt.Errorf("invalid_boot_order")
	}
	if req.Emulation != nil {
		switch *req.Emulation {
		case libvirtServiceInterfaces.VirtIOStorageEmulation,
			libvirtServiceInterfaces.VirtIO9PStorageEmulation,
			libvirtServiceInterfaces.AHCIHDStorageEmulation,
			libvirtServiceInterfaces.AHCICDStorageEmulation,
			libvirtServiceInterfaces.NVMEStorageEmulation:
		default:
			return nil, fmt.Errorf("invalid_storage_emulation")
		}
		if current.Type == vmModels.VMStorageTypeFilesystem &&
			*req.Emulation != libvirtServiceInterfaces.VirtIO9PStorageEmulation {
			return nil, fmt.Errorf("invalid_storage_emulation")
		}
		if current.Type != vmModels.VMStorageTypeFilesystem &&
			*req.Emulation == libvirtServiceInterfaces.VirtIO9PStorageEmulation {
			return nil, fmt.Errorf("invalid_storage_emulation")
		}
	}
	if current.Type == vmModels.VMStorageTypeFilesystem {
		if req.FilesystemTarget != nil {
			target := strings.TrimSpace(*req.FilesystemTarget)
			if !isValidFilesystemTargetName(target) {
				return nil, fmt.Errorf("invalid_filesystem_target_name")
			}
			var count int64
			if err := s.DB.Model(&vmModels.Storage{}).
				Where("vm_id = ? AND type = ? AND filesystem_target = ? AND id != ?",
					vm.ID, vmModels.VMStorageTypeFilesystem, target, current.ID).
				Count(&count).Error; err != nil {
				return nil, fmt.Errorf("failed_to_check_filesystem_target_usage: %w", err)
			}
			if count > 0 {
				return nil, fmt.Errorf("filesystem_target_already_in_use")
			}
			req.FilesystemTarget = &target
		}
	} else if req.FilesystemTarget != nil || req.ReadOnly != nil {
		return nil, fmt.Errorf("invalid_request: filesystem fields require filesystem storage")
	}
	if current.Type == vmModels.VMStorageTypeFilesystem && req.BootOrder != nil {
		return nil, fmt.Errorf("invalid_request: filesystem storage has no boot order")
	}
	if req.BootOrder != nil && *req.BootOrder != current.BootOrder {
		var count int64
		if err := s.DB.Model(&vmModels.Storage{}).
			Where("vm_id = ? AND type != ? AND boot_order = ? AND id != ?",
				vm.ID, vmModels.VMStorageTypeFilesystem, *req.BootOrder, current.ID).
			Count(&count).Error; err != nil {
			return nil, fmt.Errorf("failed_to_validate_boot_order_index: %w", err)
		}
		if count > 0 {
			return nil, fmt.Errorf("boot_order_index_already_in_use: %d", *req.BootOrder)
		}
	}
	if req.Size != nil && *req.Size < current.Size {
		return nil, fmt.Errorf("shrinking_storage_not_supported")
	}

	oldXML, err := s.GetVMXML(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_capture_domain_xml: %w", err)
	}

	physicalGrowthApplied := false
	physicalSize := current.Size
	var committed vmModels.Storage
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		var updated vmModels.Storage
		if err := tx.Preload("Dataset").
			First(&updated, "id = ? AND vm_id = ?", req.ID, vm.ID).Error; err != nil {
			return fmt.Errorf("failed_to_find_storage_record: %w", err)
		}

		if req.Size != nil && *req.Size != updated.Size {
			newSize := *req.Size
			growBy := newSize - updated.Size
			if updated.Type == vmModels.VMStorageTypeDiskImage {
				return fmt.Errorf("size_edit_not_supported_for_disk_image_storage")
			}
			if updated.Type == vmModels.VMStorageTypeFilesystem {
				return fmt.Errorf("size_edit_not_supported_for_filesystem_storage")
			}
			if s.GZFS == nil || s.GZFS.ZFS == nil || s.GZFS.Zpool == nil {
				return fmt.Errorf("gzfs_not_initialized")
			}
			pool, err := s.GZFS.Zpool.Get(ctx, updated.Pool)
			if err != nil {
				return fmt.Errorf("failed_to_get_storage_pool: %w", err)
			}
			if pool == nil {
				return fmt.Errorf("pool_not_found: %s", updated.Pool)
			}
			if growBy > 0 && pool.Free < uint64(growBy) {
				return fmt.Errorf("insufficient_space_in_pool: %s", updated.Pool)
			}

			switch updated.Type {
			case vmModels.VMStorageTypeRaw:
				imagePath, err := s.resolveRawStorageImagePath(ctx, tx, vm.RID, updated)
				if err != nil {
					return err
				}
				if err := utils.CreateOrResizeFile(imagePath, newSize); err != nil {
					return fmt.Errorf("failed_to_resize_raw_image_file: %w", err)
				}
				physicalSize = newSize
			case vmModels.VMStorageTypeZVol:
				datasets, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeVolume, false, updated.Dataset.Name)
				if err != nil {
					return fmt.Errorf("failed_to_get_zvol_dataset: %w", err)
				}
				if len(datasets) == 0 || datasets[0] == nil {
					return fmt.Errorf("zvol_dataset_not_found: %s", updated.Dataset.Name)
				}
				property, ok := datasets[0].Properties["volsize"]
				if !ok {
					return fmt.Errorf("volsize_property_not_found_in_zvol_dataset")
				}
				currentVolSize := gzfs.ParseSize(property.Value)
				if uint64(newSize) < currentVolSize {
					return fmt.Errorf("new_size_must_be_greater_than_or_equal_to_current_volsize")
				}
				if err := datasets[0].SetProperties(ctx, "volsize", strconv.FormatInt(newSize, 10)); err != nil {
					return fmt.Errorf("failed_to_set_zvol_volsize: %w", err)
				}
				physicalSize = newSize
			default:
				return fmt.Errorf("size_edit_not_supported_for_storage_type: %s", updated.Type)
			}
			physicalGrowthApplied = true
			updated.Size = physicalSize
		}

		if req.Name != nil {
			updated.Name = *req.Name
		}
		if req.Emulation != nil {
			updated.Emulation = vmModels.VMStorageEmulationType(*req.Emulation)
		}
		if updated.Type == vmModels.VMStorageTypeFilesystem {
			updated.Emulation = vmModels.VirtIO9PStorageEmulation
			if req.FilesystemTarget != nil {
				updated.FilesystemTarget = *req.FilesystemTarget
			}
			if req.ReadOnly != nil {
				updated.ReadOnly = *req.ReadOnly
			}
		}
		if req.BootOrder != nil {
			updated.BootOrder = *req.BootOrder
		}
		if req.Enable != nil {
			updated.Enable = *req.Enable
		}
		if updated.Type == vmModels.VMStorageTypeDiskImage && updated.DatasetID == nil {
			updated.Pool = ""
			updated.Dataset = vmModels.VMStorageDataset{}
		}

		if err := tx.Save(&updated).Error; err != nil {
			return fmt.Errorf("failed_to_update_storage_record: %w", err)
		}
		if err := s.syncVMDisksWithDB(ctx, tx, vm.RID); err != nil {
			return fmt.Errorf("failed_to_sync_vm_disks: %w", err)
		}
		committed = updated
		return nil
	})
	if err != nil {
		var reconciliationErr error
		if physicalGrowthApplied {
			if sizeErr := s.DB.Session(&gorm.Session{SkipHooks: true}).
				Model(&vmModels.Storage{}).
				Where("id = ? AND vm_id = ?", req.ID, vm.ID).
				UpdateColumn("size", physicalSize).Error; sizeErr != nil {
				reconciliationErr = errors.Join(
					reconciliationErr,
					fmt.Errorf("failed_to_reconcile_physical_storage_size: %w", sizeErr),
				)
			}
		}
		if restoreErr := s.restoreVMStorageMutation(req.RID, oldXML); restoreErr != nil {
			reconciliationErr = errors.Join(reconciliationErr, restoreErr)
		}
		if reconciliationErr != nil {
			return nil, errors.Join(err, fmt.Errorf("storage_reconciliation_failed: %w", reconciliationErr))
		}
		return nil, err
	}

	if committed.ID == 0 {
		return nil, fmt.Errorf("updated_storage_result_missing")
	}
	return &committed, nil
}

func (s *Service) CreateStorageParent(rid uint, poolName string, ctx context.Context) error {
	if s == nil || s.System == nil {
		return fmt.Errorf("system_service_not_initialized")
	}
	if s.GZFS == nil || s.GZFS.ZFS == nil {
		return fmt.Errorf("gzfs_not_initialized")
	}
	poolName = strings.TrimSpace(poolName)
	if rid == 0 || poolName == "" {
		return fmt.Errorf("invalid_pool")
	}

	pools, err := s.System.GetUsablePools(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_usable_pools: %w", err)
	}

	var created []*gzfs.Dataset
	matched := false

	for _, pool := range pools {
		if pool == nil || pool.Name != poolName {
			continue
		}
		matched = true

		target := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool.Name, rid)
		datasets, listErr := s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeFilesystem,
			false,
			target,
		)
		if listErr != nil && !strings.Contains(strings.ToLower(listErr.Error()), "dataset does not exist") {
			return fmt.Errorf("failed_to_get_storage_parent: %w", listErr)
		}

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
			cleanupCtx, cancel := detachedVMStorageContext(ctx)
			defer cancel()
			for _, createdDS := range created {
				_ = createdDS.Destroy(cleanupCtx, true, false)
			}

			return fmt.Errorf("failed_to_create_%s: %w", target, err)
		}
		if ds == nil {
			return fmt.Errorf("failed_to_create_%s: empty_result", target)
		}

		created = append(created, ds)
	}
	if !matched {
		return fmt.Errorf("pool_not_found: %s", poolName)
	}

	return nil
}
