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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type storageTestSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	pools []*gzfs.ZPool
	err   error
}

func (s storageTestSystemService) GetUsablePools(context.Context) ([]*gzfs.ZPool, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.pools, nil
}

type storageTestDataset struct {
	name       string
	pool       string
	guid       string
	kind       gzfs.DatasetType
	mountpoint string
	volsize    string
}

type storageTestZFSRunner struct {
	datasets   map[string]storageTestDataset
	failRename bool
}

func (r *storageTestZFSRunner) Run(
	_ context.Context,
	_ io.Reader,
	stdout io.Writer,
	_ io.Writer,
	_ string,
	args ...string,
) error {
	if len(args) == 0 {
		return nil
	}

	switch args[0] {
	case "rename":
		if r.failRename {
			return fmt.Errorf("boom_rename")
		}
		if len(args) >= 3 {
			oldName, newName := args[len(args)-2], args[len(args)-1]
			if dataset, ok := r.datasets[oldName]; ok {
				delete(r.datasets, oldName)
				dataset.name = newName
				dataset.pool = strings.SplitN(newName, "/", 2)[0]
				r.datasets[newName] = dataset
			}
		}
		return nil
	case "destroy":
		if len(args) > 1 {
			delete(r.datasets, args[len(args)-1])
		}
		return nil
	case "list":
		if stdout == nil {
			return nil
		}
		return r.writeList(stdout, args)
	default:
		return nil
	}
}

func (r *storageTestZFSRunner) writeList(stdout io.Writer, args []string) error {
	target := ""
	recursive := false
	typeFilter := ""
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "-o":
			index++
		case "-t":
			if index+1 < len(args) {
				typeFilter = args[index+1]
				index++
			}
		case "-r":
			recursive = true
		case "-p", "-j":
		default:
			if !strings.HasPrefix(args[index], "-") {
				target = args[index]
			}
		}
	}

	datasets := make(map[string]map[string]any)
	for _, dataset := range r.datasets {
		if typeFilter != "" && typeFilter != storageTestZFSType(dataset.kind) {
			continue
		}
		if target != "" {
			matches := dataset.name == target
			if recursive {
				matches = matches || strings.HasPrefix(dataset.name, target+"/")
			}
			if !matches {
				continue
			}
		}

		properties := map[string]map[string]string{
			"guid":          {"value": dataset.guid},
			"mountpoint":    {"value": dataset.mountpoint},
			"used":          {"value": "0"},
			"available":     {"value": "0"},
			"referenced":    {"value": "0"},
			"compressratio": {"value": "1.00x"},
		}
		if dataset.volsize != "" {
			properties["volsize"] = map[string]string{"value": dataset.volsize}
		}
		datasets[dataset.name] = map[string]any{
			"name":       dataset.name,
			"pool":       dataset.pool,
			"type":       dataset.kind,
			"properties": properties,
		}
	}

	payload, err := json.Marshal(map[string]any{
		"output_version": map[string]any{"name": "zfs", "vers_major": 0, "vers_minor": 0},
		"datasets":       datasets,
	})
	if err != nil {
		return err
	}
	_, err = stdout.Write(payload)
	return err
}

func storageTestZFSType(kind gzfs.DatasetType) string {
	switch kind {
	case gzfs.DatasetTypeFilesystem:
		return "fs"
	case gzfs.DatasetTypeVolume:
		return "vol"
	case gzfs.DatasetTypeSnapshot:
		return "snap"
	default:
		return "all"
	}
}

func newStorageTestService(
	db *gorm.DB,
	pools []string,
	datasets map[string]storageTestDataset,
) *Service {
	usablePools := make([]*gzfs.ZPool, 0, len(pools))
	for _, pool := range pools {
		usablePools = append(usablePools, &gzfs.ZPool{Name: pool, Free: 1 << 40})
	}
	return &Service{
		DB:     db,
		System: storageTestSystemService{pools: usablePools},
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner: &storageTestZFSRunner{datasets: datasets},
		}),
	}
}

func mustCountRows[T any](t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(new(T)).Count(&count).Error; err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	return count
}

func TestResolveFilesystemSourcePathLoadsDatasetRelationFromDB(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{})
	datasetRecord := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/shares/projects",
		GUID: "filesystem-guid",
	}
	if err := db.Create(&datasetRecord).Error; err != nil {
		t.Fatalf("failed to seed dataset metadata: %v", err)
	}

	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		datasetRecord.Name: {
			name:       datasetRecord.Name,
			pool:       datasetRecord.Pool,
			guid:       datasetRecord.GUID,
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: "/mnt/projects",
		},
	})
	path, err := service.resolveFilesystemSourcePath(context.Background(), vmModels.Storage{
		DatasetID: &datasetRecord.ID,
	})
	if err != nil {
		t.Fatalf("expected source path resolution to succeed: %v", err)
	}
	if path != "/mnt/projects" {
		t.Fatalf("expected actual mountpoint, got %q", path)
	}
}

func TestResolveRawStorageImagePathUsesActualMountpoint(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{})
	datasetRecord := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/sylve/virtual-machines/501/raw-77",
		GUID: "raw-guid",
	}
	if err := db.Create(&datasetRecord).Error; err != nil {
		t.Fatalf("failed to seed dataset metadata: %v", err)
	}

	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		datasetRecord.Name: {
			name:       datasetRecord.Name,
			pool:       datasetRecord.Pool,
			guid:       datasetRecord.GUID,
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: "/custom/vm-disks/raw-77",
		},
	})
	path, err := service.resolveRawStorageImagePath(context.Background(), db, 501, vmModels.Storage{
		ID:        77,
		Type:      vmModels.VMStorageTypeRaw,
		Pool:      "tank",
		DatasetID: &datasetRecord.ID,
		Dataset:   datasetRecord,
	})
	if err != nil {
		t.Fatalf("expected raw path resolution to succeed: %v", err)
	}
	if want := "/custom/vm-disks/raw-77/77.img"; path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestFindZVOLDatasetByGUIDSearchesAllUsablePools(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	service := newStorageTestService(db, []string{"first", "source"}, map[string]storageTestDataset{
		"source/legacy-zvol": {
			name:       "source/legacy-zvol",
			pool:       "source",
			guid:       "wanted-guid",
			kind:       gzfs.DatasetTypeVolume,
			mountpoint: "-",
			volsize:    "1073741824",
		},
	})

	dataset, err := service.findZVOLDatasetByGUID(context.Background(), "wanted-guid")
	if err != nil {
		t.Fatalf("expected cross-pool lookup to succeed: %v", err)
	}
	if dataset.Name != "source/legacy-zvol" {
		t.Fatalf("unexpected dataset: %+v", dataset)
	}
}

func TestStorageImportRawRollsBackWhenDiskCreationFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 502, Name: "vm-502"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	rawPath := filepath.Join(t.TempDir(), "legacy.img")
	if err := os.WriteFile(rawPath, []byte("raw-disk"), 0o600); err != nil {
		t.Fatalf("failed to seed raw file: %v", err)
	}

	service := &Service{DB: db}
	bootOrder, pool := 1, "tank"
	req := libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeRaw,
		Emulation:   libvirtServiceInterfaces.AHCIHDStorageEmulation,
		Name:        "imported-raw",
		RID:         vm.RID,
		RawPath:     rawPath,
		Pool:        &pool,
		BootOrder:   &bootOrder,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return service.storageImportTx(req, vm, context.Background(), tx, storageRuntimeHooks{
			createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, _ *gorm.DB) (vmModels.Storage, bool, error) {
				return storage, false, fmt.Errorf("boom_create_disk")
			},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_create_vm_disk") {
		t.Fatalf("expected disk creation failure, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected storage rollback, found %d rows", got)
	}
}

func TestStorageImportRawRollsBackWhenCopyFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 503, Name: "vm-503"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	rawPath := filepath.Join(t.TempDir(), "legacy.img")
	if err := os.WriteFile(rawPath, []byte("raw-disk"), 0o600); err != nil {
		t.Fatalf("failed to seed raw file: %v", err)
	}
	mountpoint := t.TempDir()
	datasetName := "tank/sylve/virtual-machines/503/raw-1"
	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		datasetName: {
			name:       datasetName,
			pool:       "tank",
			guid:       "raw-import-guid",
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: mountpoint,
		},
	})
	bootOrder, pool := 1, "tank"
	req := libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeRaw,
		Emulation:   libvirtServiceInterfaces.AHCIHDStorageEmulation,
		Name:        "imported-raw",
		RID:         vm.RID,
		RawPath:     rawPath,
		Pool:        &pool,
		BootOrder:   &bootOrder,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return service.storageImportTx(req, vm, context.Background(), tx, storageRuntimeHooks{
			createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, tx *gorm.DB) (vmModels.Storage, bool, error) {
				dataset := vmModels.VMStorageDataset{Pool: "tank", Name: datasetName, GUID: "raw-import-guid"}
				if err := tx.Create(&dataset).Error; err != nil {
					return storage, false, err
				}
				storage.DatasetID = &dataset.ID
				storage.Dataset = dataset
				if err := tx.Save(&storage).Error; err != nil {
					return storage, false, err
				}
				return storage, false, nil
			},
			copyFile: func(_, _ string) error { return fmt.Errorf("boom_copy") },
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_copy_raw_file_to_dataset") {
		t.Fatalf("expected copy failure, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected storage rollback, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 0 {
		t.Fatalf("expected dataset metadata rollback, found %d rows", got)
	}
}

func TestStorageImportZVOLFindsSourceAcrossPoolsAndRollsBackCreateFailure(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 504, Name: "vm-504"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	service := newStorageTestService(db, []string{"target", "source"}, map[string]storageTestDataset{
		"source/legacy-zvol": {
			name:       "source/legacy-zvol",
			pool:       "source",
			guid:       "zvol-guid",
			kind:       gzfs.DatasetTypeVolume,
			mountpoint: "-",
			volsize:    "1073741824",
		},
	})
	bootOrder, pool := 1, "target"
	req := libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:        "imported-zvol",
		RID:         vm.RID,
		Dataset:     "zvol-guid",
		Pool:        &pool,
		BootOrder:   &bootOrder,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return service.storageImportTx(req, vm, context.Background(), tx, storageRuntimeHooks{
			createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, _ *gorm.DB) (vmModels.Storage, bool, error) {
				return storage, false, fmt.Errorf("boom_create_disk")
			},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_create_vm_disk") {
		t.Fatalf("expected create failure after cross-pool lookup, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected storage rollback, found %d rows", got)
	}
}

func TestStorageImportZVOLRejectsDatasetAlreadyReferencedByAnotherVM(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	sourceVM := vmModels.VM{RID: 505, Name: "source-vm"}
	targetVM := vmModels.VM{RID: 506, Name: "target-vm"}
	if err := db.Create(&sourceVM).Error; err != nil {
		t.Fatalf("failed to seed source VM: %v", err)
	}
	if err := db.Create(&targetVM).Error; err != nil {
		t.Fatalf("failed to seed target VM: %v", err)
	}
	datasetRecord := vmModels.VMStorageDataset{Pool: "source", Name: "source/in-use", GUID: "in-use-guid"}
	if err := db.Create(&datasetRecord).Error; err != nil {
		t.Fatalf("failed to seed dataset metadata: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID:      sourceVM.ID,
		Name:      "in-use",
		Type:      vmModels.VMStorageTypeZVol,
		DatasetID: &datasetRecord.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed source storage: %v", err)
	}

	service := newStorageTestService(db, []string{"source", "target"}, map[string]storageTestDataset{
		datasetRecord.Name: {
			name:       datasetRecord.Name,
			pool:       datasetRecord.Pool,
			guid:       datasetRecord.GUID,
			kind:       gzfs.DatasetTypeVolume,
			mountpoint: "-",
			volsize:    "1073741824",
		},
	})
	bootOrder, pool := 1, "target"
	createCalled := false
	err := db.Transaction(func(tx *gorm.DB) error {
		return service.storageImportTx(libvirtServiceInterfaces.StorageAttachRequest{
			AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
			StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
			Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
			Name:        "duplicate",
			RID:         targetVM.RID,
			Dataset:     datasetRecord.GUID,
			Pool:        &pool,
			BootOrder:   &bootOrder,
		}, targetVM, context.Background(), tx, storageRuntimeHooks{
			createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, _ *gorm.DB) (vmModels.Storage, bool, error) {
				createCalled = true
				return storage, false, nil
			},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "zvol_dataset_already_attached") {
		t.Fatalf("expected in-use rejection, got %v", err)
	}
	if createCalled {
		t.Fatal("physical destination creation ran for an in-use source dataset")
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 1 {
		t.Fatalf("expected existing reference to remain, found %d rows", got)
	}
}

func TestStorageNewFilesystemRejectsDuplicateTargetEvenWhenExistingShareDisabled(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 507, Name: "vm-507"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID:             vm.ID,
		Name:             "existing-share",
		Type:             vmModels.VMStorageTypeFilesystem,
		Emulation:        vmModels.VirtIO9PStorageEmulation,
		FilesystemTarget: "shared_data",
		Enable:           false,
	}).Error; err != nil {
		t.Fatalf("failed to seed storage: %v", err)
	}

	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		"tank/shares/data": {
			name:       "tank/shares/data",
			pool:       "tank",
			guid:       "share-guid",
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: "/mnt/shared-data",
		},
	})
	err := db.Transaction(func(tx *gorm.DB) error {
		return service.storageNewTx(libvirtServiceInterfaces.StorageAttachRequest{
			AttachType:       libvirtServiceInterfaces.StorageAttachTypeNew,
			StorageType:      libvirtServiceInterfaces.StorageTypeFilesystem,
			Emulation:        libvirtServiceInterfaces.VirtIO9PStorageEmulation,
			Name:             "duplicate-share",
			RID:              vm.RID,
			Dataset:          "share-guid",
			FilesystemTarget: "shared_data",
		}, vm, context.Background(), tx, storageRuntimeHooks{})
	})
	if err == nil || !strings.Contains(err.Error(), "filesystem_target_already_in_use") {
		t.Fatalf("expected duplicate target rejection, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 1 {
		t.Fatalf("expected only original share, found %d rows", got)
	}
}

func TestStorageAttachApplyRollsBackRowsWhenSyncFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 508, Name: "vm-508"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	service := &Service{DB: db}
	bootOrder, pool, size := 1, "tank", int64(1<<30)
	_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:        "disk",
		RID:         vm.RID,
		Pool:        &pool,
		Size:        &size,
		BootOrder:   &bootOrder,
	}, vm, context.Background(), storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, _ *gorm.DB) (vmModels.Storage, bool, error) {
			return storage, false, nil
		},
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			return fmt.Errorf("boom_sync")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_disks") {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected attachment metadata rollback, found %d rows", got)
	}
}

func TestStorageAttachApplyDestroysNewManagedDatasetWhenSyncFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 509, Name: "vm-509"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	runner := &storageTestZFSRunner{datasets: make(map[string]storageTestDataset)}
	service := &Service{
		DB:     db,
		System: storageTestSystemService{pools: []*gzfs.ZPool{{Name: "tank", Free: 1 << 40}}},
		GZFS:   gzfs.NewClient(gzfs.Options{Runner: runner}),
	}
	bootOrder, pool, size := 1, "tank", int64(1<<30)
	_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:        "disk",
		RID:         vm.RID,
		Pool:        &pool,
		Size:        &size,
		BootOrder:   &bootOrder,
	}, vm, context.Background(), storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, tx *gorm.DB) (vmModels.Storage, bool, error) {
			datasetName := fmt.Sprintf("tank/sylve/virtual-machines/%d/zvol-%d", vm.RID, storage.ID)
			dataset := vmModels.VMStorageDataset{Pool: "tank", Name: datasetName, GUID: "created-guid"}
			if err := tx.Create(&dataset).Error; err != nil {
				return storage, false, err
			}
			storage.DatasetID = &dataset.ID
			storage.Dataset = dataset
			if err := tx.Save(&storage).Error; err != nil {
				return storage, false, err
			}
			runner.datasets[datasetName] = storageTestDataset{
				name: datasetName, pool: "tank", guid: dataset.GUID,
				kind: gzfs.DatasetTypeVolume, mountpoint: "-", volsize: fmt.Sprint(size),
			}
			return storage, true, nil
		},
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			return fmt.Errorf("boom_sync")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_disks") {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if len(runner.datasets) != 0 {
		t.Fatalf("expected newly created dataset cleanup, remaining=%+v", runner.datasets)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected metadata rollback, found %d rows", got)
	}
}

func TestStorageAttachApplyRestoresSamePoolZVOLNameWhenSyncFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 510, Name: "vm-510"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	const sourceName = "tank/unmanaged/source-zvol"
	runner := &storageTestZFSRunner{datasets: map[string]storageTestDataset{
		sourceName: {
			name: sourceName, pool: "tank", guid: "rename-guid",
			kind: gzfs.DatasetTypeVolume, mountpoint: "-", volsize: "1073741824",
		},
	}}
	service := &Service{
		DB:     db,
		System: storageTestSystemService{pools: []*gzfs.ZPool{{Name: "tank", Free: 1 << 40}}},
		GZFS:   gzfs.NewClient(gzfs.Options{Runner: runner}),
	}
	bootOrder, pool := 1, "tank"
	_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:        "imported",
		RID:         vm.RID,
		Pool:        &pool,
		Dataset:     "rename-guid",
		BootOrder:   &bootOrder,
	}, vm, context.Background(), storageRuntimeHooks{
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			return fmt.Errorf("boom_sync")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_disks") {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if _, ok := runner.datasets[sourceName]; !ok {
		t.Fatalf("expected source ZVOL name to be restored, datasets=%+v", runner.datasets)
	}
	if len(runner.datasets) != 1 {
		t.Fatalf("expected only original source dataset, datasets=%+v", runner.datasets)
	}
}

func seedDetachStorage(t *testing.T, db *gorm.DB, vmID uint, rid uint) vmModels.Storage {
	t.Helper()
	dataset := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: fmt.Sprintf("tank/sylve/virtual-machines/%d/raw-1", rid),
		GUID: fmt.Sprintf("guid-%d", rid),
	}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatalf("failed to seed dataset metadata: %v", err)
	}
	storage := vmModels.Storage{
		VMID:      vmID,
		Name:      "disk-1",
		Type:      vmModels.VMStorageTypeRaw,
		Pool:      "tank",
		Enable:    true,
		DatasetID: &dataset.ID,
	}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatalf("failed to seed storage: %v", err)
	}
	return storage
}

func TestStorageDetachApplyKeepsRowsWhenSyncFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	storage := seedDetachStorage(t, db, 42, 511)
	service := &Service{DB: db}

	err := service.storageDetachApply(context.Background(), libvirtServiceInterfaces.StorageDetachRequest{
		RID:       511,
		StorageID: storage.ID,
	}, storage.VMID, storageRuntimeHooks{
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			return fmt.Errorf("boom_sync")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_disks") {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 1 {
		t.Fatalf("expected storage row retained, found %d", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 1 {
		t.Fatalf("expected dataset metadata retained, found %d", got)
	}
}

func TestStorageDetachApplyRejectsStorageOwnedByDifferentVM(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	storage := seedDetachStorage(t, db, 42, 512)
	service := &Service{DB: db}

	err := service.storageDetachApply(context.Background(), libvirtServiceInterfaces.StorageDetachRequest{
		RID:       512,
		StorageID: storage.ID,
	}, 99, storageRuntimeHooks{
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			t.Fatal("sync must not run for a storage belonging to another VM")
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_find_storage_record") {
		t.Fatalf("expected membership failure, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 1 {
		t.Fatalf("expected storage row retained, found %d", got)
	}
}

func TestStorageDetachApplyDeletesOnlyMetadataOnSuccess(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	storage := seedDetachStorage(t, db, 43, 513)
	service := &Service{DB: db}

	err := service.storageDetachApply(context.Background(), libvirtServiceInterfaces.StorageDetachRequest{
		RID:       513,
		StorageID: storage.ID,
	}, storage.VMID, storageRuntimeHooks{
		syncVMDisks: func(context.Context, *gorm.DB, uint) error { return nil },
	})
	if err != nil {
		t.Fatalf("expected successful detach: %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected storage metadata deleted, found %d", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 0 {
		t.Fatalf("expected dataset metadata deleted, found %d", got)
	}
}
