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
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type storageResolveTestDataset struct {
	name       string
	pool       string
	guid       string
	mountpoint string
}

type storageResolveTestZFSRunner struct {
	datasets map[string]storageResolveTestDataset
}

func (r *storageResolveTestZFSRunner) Run(
	_ context.Context,
	_ io.Reader,
	stdout,
	_ io.Writer,
	_ string,
	args ...string,
) error {
	if stdout == nil {
		return nil
	}

	target := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "list", "-p", "-j", "-r":
			continue
		case "-o", "-t":
			i++
			continue
		default:
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			target = args[i]
		}
	}

	if target == "" {
		_, err := io.WriteString(stdout, `{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{}}`)
		return err
	}

	ds, ok := r.datasets[target]
	if !ok {
		_, err := io.WriteString(stdout, `{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{}}`)
		return err
	}

	out := fmt.Sprintf(
		`{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{"%s":{"name":"%s","pool":"%s","properties":{"guid":{"value":"%s"},"mountpoint":{"value":"%s"},"used":{"value":"0"},"available":{"value":"0"},"referenced":{"value":"0"},"compressratio":{"value":"1.00x"}}}}}`,
		ds.name,
		ds.name,
		ds.pool,
		ds.guid,
		ds.mountpoint,
	)
	_, err := io.WriteString(stdout, out)
	return err
}

func TestResolveFilesystemSourcePath_LoadsDatasetFromDBWhenRelationNotPreloaded(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VMStorageDataset{})

	storageDataset := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/shares/projects",
		GUID: "guid-storage-resolve-test",
	}
	if err := db.Create(&storageDataset).Error; err != nil {
		t.Fatalf("failed to seed storage dataset: %v", err)
	}

	svc := &Service{
		DB: db,
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner: &storageResolveTestZFSRunner{
				datasets: map[string]storageResolveTestDataset{
					"tank/shares/projects": {
						name:       "tank/shares/projects",
						pool:       "tank",
						guid:       "guid-storage-resolve-test",
						mountpoint: "/tank/shares/projects",
					},
				},
			},
		}),
	}

	storage := vmModels.Storage{
		DatasetID: &storageDataset.ID,
		// Intentionally keep Dataset relation empty to simulate non-preloaded query path.
	}

	sourcePath, err := svc.resolveFilesystemSourcePath(context.Background(), storage)
	if err != nil {
		t.Fatalf("expected filesystem source path resolution to succeed, got: %v", err)
	}

	if sourcePath != "/tank/shares/projects" {
		t.Fatalf("expected mountpoint /tank/shares/projects, got %q", sourcePath)
	}
}

type storageZVOLImportRunner struct {
	sourcePool string
	failRename bool
}

func (r *storageZVOLImportRunner) Run(
	_ context.Context,
	_ io.Reader,
	stdout,
	_ io.Writer,
	_ string,
	args ...string,
) error {
	for _, arg := range args {
		if arg == "rename" && r.failRename {
			return fmt.Errorf("boom_rename")
		}
	}

	if stdout == nil {
		return nil
	}

	// Feed a source-pool zvol so import follows the cross-pool path.
	for _, arg := range args {
		if arg == "list" {
			pool := r.sourcePool
			if strings.TrimSpace(pool) == "" {
				pool = "source"
			}

			name := fmt.Sprintf("%s/legacy-zvol", pool)
			_, err := io.WriteString(
				stdout,
				fmt.Sprintf(
					`{"output_version":{"name":"zfs","vers_major":0,"vers_minor":0},"datasets":{"%s":{"name":"%s","pool":"%s","properties":{"guid":{"value":"zvol-guid-1"},"volsize":{"value":"1073741824"},"used":{"value":"0"},"available":{"value":"0"},"referenced":{"value":"0"},"compressratio":{"value":"1.00x"}}}}}`,
					name,
					name,
					pool,
				),
			)
			return err
		}
	}

	return nil
}

func mustCountRows[T any](t *testing.T, svc *Service) int64 {
	t.Helper()

	var count int64
	if err := svc.DB.Model(new(T)).Count(&count).Error; err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}

	return count
}

func TestStorageImportRaw_RollsBackWhenCreateDiskFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})

	vm := vmModels.VM{RID: 501, Name: "vm-501"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}

	rawPath := t.TempDir() + "/legacy.img"
	if err := os.WriteFile(rawPath, []byte("raw-disk"), 0o600); err != nil {
		t.Fatalf("failed to create raw source file: %v", err)
	}

	svc := &Service{
		DB: db,
	}

	bootOrder := 1
	pool := "tank"
	req := libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:       libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType:      libvirtServiceInterfaces.StorageTypeRaw,
		Emulation:        libvirtServiceInterfaces.AHCIHDStorageEmulation,
		Name:             "imported-raw",
		RID:              vm.RID,
		RawPath:          rawPath,
		Pool:             &pool,
		BootOrder:        &bootOrder,
		FilesystemTarget: "",
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return svc.storageImportTx(req, vm, context.Background(), tx, storageRuntimeHooks{
			createVMDisk: func(_ uint, _ vmModels.Storage, _ context.Context) error {
				return fmt.Errorf("boom_create_disk")
			},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_create_vm_disk") {
		t.Fatalf("expected failed_to_create_vm_disk error, got %v", err)
	}

	if got := mustCountRows[vmModels.Storage](t, svc); got != 0 {
		t.Fatalf("expected vm_storages rollback, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, svc); got != 0 {
		t.Fatalf("expected vm_storage_datasets rollback, found %d rows", got)
	}
}

func TestStorageImportRaw_RollsBackWhenCopyFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})

	vm := vmModels.VM{RID: 502, Name: "vm-502"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}

	rawPath := t.TempDir() + "/legacy.img"
	if err := os.WriteFile(rawPath, []byte("raw-disk"), 0o600); err != nil {
		t.Fatalf("failed to create raw source file: %v", err)
	}

	svc := &Service{
		DB: db,
	}

	bootOrder := 1
	pool := "tank"
	req := libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:       libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType:      libvirtServiceInterfaces.StorageTypeRaw,
		Emulation:        libvirtServiceInterfaces.AHCIHDStorageEmulation,
		Name:             "imported-raw",
		RID:              vm.RID,
		RawPath:          rawPath,
		Pool:             &pool,
		BootOrder:        &bootOrder,
		FilesystemTarget: "",
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return svc.storageImportTx(req, vm, context.Background(), tx, storageRuntimeHooks{
			createVMDisk: func(_ uint, _ vmModels.Storage, _ context.Context) error {
				return nil
			},
			copyFile: func(_, _ string) error {
				return fmt.Errorf("boom_copy")
			},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_copy_raw_file_to_dataset") {
		t.Fatalf("expected failed_to_copy_raw_file_to_dataset error, got %v", err)
	}

	if got := mustCountRows[vmModels.Storage](t, svc); got != 0 {
		t.Fatalf("expected vm_storages rollback, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, svc); got != 0 {
		t.Fatalf("expected vm_storage_datasets rollback, found %d rows", got)
	}
}

func TestStorageImportZVOL_RollsBackWhenCreateDiskFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})

	vm := vmModels.VM{RID: 503, Name: "vm-503"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}

	svc := &Service{
		DB: db,
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner: &storageZVOLImportRunner{sourcePool: "source"},
		}),
	}

	bootOrder := 1
	pool := "target"
	req := libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:        "imported-zvol",
		RID:         vm.RID,
		Dataset:     "zvol-guid-1",
		Pool:        &pool,
		BootOrder:   &bootOrder,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return svc.storageImportTx(req, vm, context.Background(), tx, storageRuntimeHooks{
			createVMDisk: func(_ uint, _ vmModels.Storage, _ context.Context) error {
				return fmt.Errorf("boom_create_disk")
			},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_create_vm_disk") {
		t.Fatalf("expected failed_to_create_vm_disk error, got %v", err)
	}

	if got := mustCountRows[vmModels.Storage](t, svc); got != 0 {
		t.Fatalf("expected vm_storages rollback, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, svc); got != 0 {
		t.Fatalf("expected vm_storage_datasets rollback, found %d rows", got)
	}
}

func TestStorageImportZVOL_RollsBackWhenRenameFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})

	vm := vmModels.VM{RID: 506, Name: "vm-506"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed vm: %v", err)
	}

	svc := &Service{
		DB: db,
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner: &storageZVOLImportRunner{
				sourcePool: "tank",
				failRename: true,
			},
		}),
	}

	bootOrder := 1
	pool := "tank"
	req := libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:  libvirtServiceInterfaces.StorageAttachTypeImport,
		StorageType: libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:   libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:        "imported-zvol",
		RID:         vm.RID,
		Dataset:     "zvol-guid-1",
		Pool:        &pool,
		BootOrder:   &bootOrder,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return svc.storageImportTx(req, vm, context.Background(), tx, storageRuntimeHooks{})
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_rename_zvol_dataset") {
		t.Fatalf("expected failed_to_rename_zvol_dataset error, got %v", err)
	}

	if got := mustCountRows[vmModels.Storage](t, svc); got != 0 {
		t.Fatalf("expected vm_storages rollback, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, svc); got != 0 {
		t.Fatalf("expected vm_storage_datasets rollback, found %d rows", got)
	}
}

func TestStorageDetachTx_RollsBackWhenSyncFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.Storage{}, &vmModels.VMStorageDataset{})

	dataset := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/sylve/virtual-machines/504/raw-1",
		GUID: "guid-504",
	}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatalf("failed to seed storage dataset: %v", err)
	}

	storage := vmModels.Storage{
		VMID:      42,
		Name:      "disk-1",
		Type:      vmModels.VMStorageTypeRaw,
		Pool:      "tank",
		BootOrder: 0,
		DatasetID: &dataset.ID,
	}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatalf("failed to seed storage: %v", err)
	}

	svc := &Service{
		DB: db,
	}

	err := svc.storageDetachTx(libvirtServiceInterfaces.StorageDetachRequest{
		RID:       504,
		StorageId: int(storage.ID),
	}, storage.VMID, storageRuntimeHooks{
		syncVMDisks: func(_ uint) error {
			return fmt.Errorf("boom_sync")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "failed_to_sync_vm_disks") {
		t.Fatalf("expected failed_to_sync_vm_disks error, got %v", err)
	}

	if got := mustCountRows[vmModels.Storage](t, svc); got != 1 {
		t.Fatalf("expected vm_storages rollback, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, svc); got != 1 {
		t.Fatalf("expected vm_storage_datasets rollback, found %d rows", got)
	}
}

func TestStorageDetachTx_SucceedsAndRemovesRows(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.Storage{}, &vmModels.VMStorageDataset{})

	dataset := vmModels.VMStorageDataset{
		Pool: "tank",
		Name: "tank/sylve/virtual-machines/505/raw-1",
		GUID: "guid-505",
	}
	if err := db.Create(&dataset).Error; err != nil {
		t.Fatalf("failed to seed storage dataset: %v", err)
	}

	storage := vmModels.Storage{
		VMID:      43,
		Name:      "disk-1",
		Type:      vmModels.VMStorageTypeRaw,
		Pool:      "tank",
		BootOrder: 0,
		DatasetID: &dataset.ID,
	}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatalf("failed to seed storage: %v", err)
	}

	svc := &Service{
		DB: db,
	}

	err := svc.storageDetachTx(libvirtServiceInterfaces.StorageDetachRequest{
		RID:       505,
		StorageId: int(storage.ID),
	}, storage.VMID, storageRuntimeHooks{
		syncVMDisks: func(_ uint) error { return nil },
	})
	if err != nil {
		t.Fatalf("expected successful detach tx, got %v", err)
	}

	if got := mustCountRows[vmModels.Storage](t, svc); got != 0 {
		t.Fatalf("expected vm_storages deleted, found %d rows", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, svc); got != 0 {
		t.Fatalf("expected vm_storage_datasets deleted, found %d rows", got)
	}
}
