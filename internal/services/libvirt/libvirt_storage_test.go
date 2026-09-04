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
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
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

type storageDBBackedSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	db *gorm.DB
}

func (s storageDBBackedSystemService) GetUsablePools(ctx context.Context) ([]*gzfs.ZPool, error) {
	var settings models.BasicSettings
	if err := s.db.WithContext(ctx).First(&settings).Error; err != nil {
		return nil, err
	}

	pools := make([]*gzfs.ZPool, 0, len(settings.Pools))
	for _, pool := range settings.Pools {
		pools = append(pools, &gzfs.ZPool{Name: pool, Free: 1 << 40})
	}
	return pools, nil
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
	commands   [][]string
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
	r.commands = append(r.commands, append([]string(nil), args...))

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
				if dataset.kind == gzfs.DatasetTypeSnapshot {
					datasetName := strings.SplitN(dataset.name, "@", 2)[0]
					matches = matches || datasetName == target || strings.HasPrefix(datasetName, target+"/")
				}
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

func TestStorageAttachApplyRawImportDoesNotDeadlockWithDBBackedPoolLookup(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
	)
	if err := db.Create(&models.BasicSettings{Pools: []string{"tank"}}).Error; err != nil {
		t.Fatalf("failed to seed basic settings: %v", err)
	}
	vm := vmModels.VM{RID: 520, Name: "vm-520"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}

	rawPath := filepath.Join(t.TempDir(), "legacy.img")
	if err := os.WriteFile(rawPath, []byte("raw-disk"), 0o600); err != nil {
		t.Fatalf("failed to seed raw file: %v", err)
	}
	mountpoint := t.TempDir()
	datasetName := "tank/sylve/virtual-machines/520/raw-1"
	service := &Service{
		DB:     db,
		System: storageDBBackedSystemService{db: db},
		GZFS: gzfs.NewClient(gzfs.Options{Runner: &storageTestZFSRunner{datasets: map[string]storageTestDataset{
			datasetName: {
				name:       datasetName,
				pool:       "tank",
				guid:       "raw-deadlock-guid",
				kind:       gzfs.DatasetTypeFilesystem,
				mountpoint: mountpoint,
			},
		}}}),
	}
	bootOrder, pool := 1, "tank"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	created, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeRaw,
		Emulation:   libvirtServiceInterfaces.AHCIHDStorageEmulation,
		Name:        "imported-raw",
		RID:         vm.RID,
		RawPath:     rawPath,
		Pool:        &pool,
		BootOrder:   &bootOrder,
	}, vm, ctx, storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, hookCtx context.Context, db *gorm.DB) (vmModels.Storage, bool, error) {
			pools, err := service.System.GetUsablePools(hookCtx)
			if err != nil {
				return storage, false, err
			}
			if len(pools) != 1 || pools[0].Name != "tank" {
				return storage, false, fmt.Errorf("unexpected_usable_pools")
			}

			dataset := vmModels.VMStorageDataset{Pool: "tank", Name: datasetName, GUID: "raw-deadlock-guid"}
			if err := db.Create(&dataset).Error; err != nil {
				return storage, false, err
			}
			storage.DatasetID = &dataset.ID
			storage.Dataset = dataset
			if err := db.Save(&storage).Error; err != nil {
				return storage, false, err
			}
			return storage, false, nil
		},
		copyFile: func(src, dst string) error {
			contents, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, contents, 0o600)
		},
		syncVMDisks: func(context.Context, *gorm.DB, uint) error { return nil },
	})
	if err != nil {
		t.Fatalf("raw import failed with a single DB connection: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected created storage metadata")
	}
	if _, err := os.Stat(filepath.Join(mountpoint, "1.img")); err != nil {
		t.Fatalf("expected imported raw image: %v", err)
	}
}

func TestStorageAttachApplyRawCopyDoesNotBlockUnrelatedDBQueries(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 521, Name: "vm-521"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}

	rawPath := filepath.Join(t.TempDir(), "legacy.img")
	if err := os.WriteFile(rawPath, []byte("raw-disk"), 0o600); err != nil {
		t.Fatalf("failed to seed raw file: %v", err)
	}
	mountpoint := t.TempDir()
	datasetName := "tank/sylve/virtual-machines/521/raw-1"
	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		datasetName: {
			name:       datasetName,
			pool:       "tank",
			guid:       "raw-availability-guid",
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: mountpoint,
		},
	})

	copyStarted := make(chan struct{})
	releaseCopy := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseCopy)
		}
	}()
	attachDone := make(chan error, 1)
	bootOrder, pool := 1, "tank"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
			AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
			StorageType: libvirtServiceInterfaces.StorageTypeRaw,
			Emulation:   libvirtServiceInterfaces.AHCIHDStorageEmulation,
			Name:        "imported-raw",
			RID:         vm.RID,
			RawPath:     rawPath,
			Pool:        &pool,
			BootOrder:   &bootOrder,
		}, vm, ctx, storageRuntimeHooks{
			createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, db *gorm.DB) (vmModels.Storage, bool, error) {
				dataset := vmModels.VMStorageDataset{Pool: "tank", Name: datasetName, GUID: "raw-availability-guid"}
				if err := db.Create(&dataset).Error; err != nil {
					return storage, false, err
				}
				storage.DatasetID = &dataset.ID
				storage.Dataset = dataset
				if err := db.Save(&storage).Error; err != nil {
					return storage, false, err
				}
				return storage, false, nil
			},
			copyFile: func(src, dst string) error {
				close(copyStarted)
				select {
				case <-releaseCopy:
				case <-ctx.Done():
					return ctx.Err()
				}
				contents, err := os.ReadFile(src)
				if err != nil {
					return err
				}
				return os.WriteFile(dst, contents, 0o600)
			},
			syncVMDisks: func(context.Context, *gorm.DB, uint) error { return nil },
		})
		attachDone <- err
	}()

	select {
	case <-copyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("raw copy did not start")
	}

	queryCtx, queryCancel := context.WithTimeout(context.Background(), time.Second)
	var storageCount int64
	queryErr := db.WithContext(queryCtx).Model(&vmModels.Storage{}).Count(&storageCount).Error
	queryCancel()
	close(releaseCopy)
	released = true

	var attachErr error
	select {
	case attachErr = <-attachDone:
	case <-time.After(2 * time.Second):
		t.Fatal("raw import did not finish after copy was released")
	}
	if queryErr != nil {
		t.Fatalf("unrelated DB query was blocked by raw copy: %v", queryErr)
	}
	if storageCount != 1 {
		t.Fatalf("expected in-progress storage metadata to be queryable, found %d rows", storageCount)
	}
	if attachErr != nil {
		t.Fatalf("raw import failed: %v", attachErr)
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
	err := service.storageImportTx(req, vm, context.Background(), db, storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, _ *gorm.DB) (vmModels.Storage, bool, error) {
			return storage, false, fmt.Errorf("boom_create_disk")
		},
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
	var tempImportPath string
	err := service.storageImportTx(req, vm, context.Background(), db, storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, db *gorm.DB) (vmModels.Storage, bool, error) {
			dataset := vmModels.VMStorageDataset{Pool: "tank", Name: datasetName, GUID: "raw-import-guid"}
			if err := db.Create(&dataset).Error; err != nil {
				return storage, false, err
			}
			storage.DatasetID = &dataset.ID
			storage.Dataset = dataset
			if err := db.Save(&storage).Error; err != nil {
				return storage, false, err
			}
			return storage, false, nil
		},
		copyFile: func(_, dst string) error {
			tempImportPath = dst
			if err := os.WriteFile(dst, []byte("partial"), 0o600); err != nil {
				return err
			}
			return fmt.Errorf("boom_copy")
		},
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
	if _, err := os.Stat(tempImportPath); !os.IsNotExist(err) {
		t.Fatalf("expected temporary import file cleanup, stat error=%v", err)
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
	err := service.storageImportTx(req, vm, context.Background(), db, storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, _ *gorm.DB) (vmModels.Storage, bool, error) {
			return storage, false, fmt.Errorf("boom_create_disk")
		},
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
	err := service.storageImportTx(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:        "duplicate",
		RID:         targetVM.RID,
		Dataset:     datasetRecord.GUID,
		Pool:        &pool,
		BootOrder:   &bootOrder,
	}, targetVM, context.Background(), db, storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, _ *gorm.DB) (vmModels.Storage, bool, error) {
			createCalled = true
			return storage, false, nil
		},
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
	err := service.storageNewTx(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:       libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType:      libvirtServiceInterfaces.StorageTypeFilesystem,
		Emulation:        libvirtServiceInterfaces.VirtIO9PStorageEmulation,
		Name:             "duplicate-share",
		RID:              vm.RID,
		Dataset:          "share-guid",
		FilesystemTarget: "shared_data",
	}, vm, context.Background(), db, storageRuntimeHooks{})
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
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 0 {
		t.Fatalf("expected dataset metadata rollback, found %d rows", got)
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
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected storage metadata cleanup, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 0 {
		t.Fatalf("expected dataset metadata cleanup, found %d rows", got)
	}
}

func TestStorageAttachApplyCleansUpFilesystemMetadataWhenSyncFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 522, Name: "vm-522"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		"tank/shares/projects": {
			name:       "tank/shares/projects",
			pool:       "tank",
			guid:       "filesystem-cleanup-guid",
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: "/mnt/projects",
		},
	})

	_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:       libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType:      libvirtServiceInterfaces.StorageTypeFilesystem,
		Emulation:        libvirtServiceInterfaces.VirtIO9PStorageEmulation,
		Name:             "projects",
		RID:              vm.RID,
		Dataset:          "filesystem-cleanup-guid",
		FilesystemTarget: "projects",
	}, vm, context.Background(), storageRuntimeHooks{
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			return fmt.Errorf("boom_sync")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_disks") {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected filesystem storage metadata cleanup, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 0 {
		t.Fatalf("expected filesystem dataset metadata cleanup, found %d rows", got)
	}
}

func TestStorageAttachApplyCleansUpDiskImageMetadataWhenSyncFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	vm := vmModels.VM{RID: 523, Name: "vm-523"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed VM: %v", err)
	}
	imagePath := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(imagePath, []byte("iso"), 0o600); err != nil {
		t.Fatalf("failed to seed disk image: %v", err)
	}
	const downloadUUID = "disk-image-cleanup"
	if err := db.Create(&utilitiesModels.Downloads{
		UUID:     downloadUUID,
		Path:     imagePath,
		Name:     filepath.Base(imagePath),
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "file://disk-image-cleanup",
		Progress: 100,
		Size:     3,
		UType:    utilitiesModels.DownloadUTypeOther,
		Status:   utilitiesModels.DownloadStatusDone,
	}).Error; err != nil {
		t.Fatalf("failed to seed download metadata: %v", err)
	}

	service := &Service{DB: db}
	bootOrder := 1
	_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeDiskImage,
		Emulation:   libvirtServiceInterfaces.AHCIHDStorageEmulation,
		Name:        "installer",
		RID:         vm.RID,
		UUID:        downloadUUID,
		BootOrder:   &bootOrder,
	}, vm, context.Background(), storageRuntimeHooks{
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			return fmt.Errorf("boom_sync")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_disks") {
		t.Fatalf("expected sync failure, got %v", err)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("expected disk-image storage metadata cleanup, found %d rows", got)
	}
	if got := mustCountRows[utilitiesModels.Downloads](t, db); got != 1 {
		t.Fatalf("expected source download metadata to remain, found %d rows", got)
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

func TestDescribeVMStorageReportsOwnership(t *testing.T) {
	tests := []struct {
		name          string
		storage       vmModels.Storage
		wantBacking   string
		wantOwnership string
		wantFlag      string
	}{
		{
			name: "managed raw",
			storage: vmModels.Storage{
				ID: 3, Type: vmModels.VMStorageTypeRaw, Pool: "tank",
			},
			wantBacking:   "tank/sylve/virtual-machines/601/raw-3",
			wantOwnership: VMStorageOwnershipManaged,
			wantFlag:      "--delete-raw-disks",
		},
		{
			name: "managed zvol",
			storage: vmModels.Storage{
				ID: 4, Type: vmModels.VMStorageTypeZVol,
				Dataset: vmModels.VMStorageDataset{Name: "tank/sylve/virtual-machines/601/zvol-4"},
			},
			wantBacking:   "tank/sylve/virtual-machines/601/zvol-4",
			wantOwnership: VMStorageOwnershipManaged,
			wantFlag:      "--delete-volumes",
		},
		{
			name: "retained image",
			storage: vmModels.Storage{
				ID: 5, Type: vmModels.VMStorageTypeDiskImage, DownloadUUID: "download-uuid",
			},
			wantBacking:   "download-uuid",
			wantOwnership: VMStorageOwnershipRetained,
		},
		{
			name: "external filesystem",
			storage: vmModels.Storage{
				ID: 6, Type: vmModels.VMStorageTypeFilesystem,
				Dataset: vmModels.VMStorageDataset{Name: "tank/shared/projects"},
			},
			wantBacking:   "tank/shared/projects",
			wantOwnership: VMStorageOwnershipExternal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribeVMStorage(601, tc.storage)
			if got.Backing != tc.wantBacking || got.Ownership != tc.wantOwnership || got.DeleteWithVMFlag != tc.wantFlag {
				t.Fatalf("storage info = %#v", got)
			}
		})
	}
}

func TestListVMStorageIsStableAndReturnsEmptySlice(t *testing.T) {
	db := newVMDeleteTestDB(t)
	vm := vmModels.VM{Name: "inventory", RID: 602}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		ID: 9, VMID: vm.ID, Name: "second", Type: vmModels.VMStorageTypeDiskImage,
		DownloadUUID: "image", Enable: true,
	}).Error; err != nil {
		t.Fatalf("seed second storage: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		ID: 8, VMID: vm.ID, Name: "first", Type: vmModels.VMStorageTypeRaw,
		Pool: "tank", Enable: true,
	}).Error; err != nil {
		t.Fatalf("seed first storage: %v", err)
	}

	service := &Service{DB: db}
	storages, err := service.ListVMStorage(vm.RID)
	if err != nil {
		t.Fatalf("list VM storage: %v", err)
	}
	if len(storages) != 2 || storages[0].ID != 8 || storages[1].ID != 9 {
		t.Fatalf("storage order = %#v", storages)
	}

	emptyVM := vmModels.VM{Name: "empty", RID: 603}
	if err := db.Create(&emptyVM).Error; err != nil {
		t.Fatalf("seed empty VM: %v", err)
	}
	empty, err := service.ListVMStorage(emptyVM.RID)
	if err != nil {
		t.Fatalf("list empty VM storage: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty storage = %#v, want non-nil empty slice", empty)
	}
}
