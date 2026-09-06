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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
	"gorm.io/gorm"
)

func TestInspectStorageImageSource(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "router.img")
	if err := os.WriteFile(sourcePath, []byte("image"), 0o600); err != nil {
		t.Fatalf("seed source image: %v", err)
	}

	originalInspect := inspectDiskImageFormat
	originalSniff := sniffMediaMIME
	t.Cleanup(func() {
		inspectDiskImageFormat = originalInspect
		sniffMediaMIME = originalSniff
	})

	tests := []struct {
		name            string
		mime            string
		info            *qemuimg.ImageInfo
		inspectErr      error
		wantFormat      qemuimg.DiskFormat
		wantVirtualSize int64
		wantErr         string
	}{
		{
			name:            "qcow2",
			mime:            "application/octet-stream",
			info:            &qemuimg.ImageInfo{Format: "qcow2", VirtualSize: 4096},
			wantFormat:      qemuimg.FormatQCOW2,
			wantVirtualSize: 4096,
		},
		{
			name:            "raw size falls back to file size",
			mime:            "application/octet-stream",
			info:            &qemuimg.ImageInfo{Format: "raw"},
			wantFormat:      qemuimg.FormatRaw,
			wantVirtualSize: 5,
		},
		{
			name:    "optical media",
			mime:    "application/x-iso9660-image",
			info:    &qemuimg.ImageInfo{Format: "raw", VirtualSize: 4096},
			wantErr: "optical_media_requires_read_only_attachment",
		},
		{
			name:    "backing chain",
			mime:    "application/octet-stream",
			info:    &qemuimg.ImageInfo{Format: "qcow2", VirtualSize: 4096, BackingFilename: "base.raw"},
			wantErr: "unsafe_disk_image_backing_chain",
		},
		{
			name:    "unsupported format",
			mime:    "application/octet-stream",
			info:    &qemuimg.ImageInfo{Format: "bochs", VirtualSize: 4096},
			wantErr: "unsupported_disk_image_format",
		},
		{
			name:       "probe failure",
			mime:       "application/octet-stream",
			inspectErr: fmt.Errorf("probe failed"),
			wantErr:    "unsupported_disk_image_format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sniffMediaMIME = func(string) (string, error) {
				return tt.mime, nil
			}
			inspectDiskImageFormat = func(string) (*qemuimg.ImageInfo, error) {
				return tt.info, tt.inspectErr
			}

			source, err := inspectStorageImageSource(sourcePath)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("inspect image: %v", err)
			}
			if source.path != sourcePath ||
				source.format != tt.wantFormat ||
				source.virtualSize != tt.wantVirtualSize {
				t.Fatalf("unexpected source: %#v", source)
			}
		})
	}
}

func TestWriteStorageImageToTargetChoosesCopyOrConversion(t *testing.T) {
	originalFlash := flashImageToDiskCtx
	originalConvert := convertDiskImageToRawCtx
	t.Cleanup(func() {
		flashImageToDiskCtx = originalFlash
		convertDiskImageToRawCtx = originalConvert
	})

	var copied, converted int
	var receivedConversionContext context.Context
	conversionContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	flashImageToDiskCtx = func(context.Context, string, string) error {
		copied++
		return nil
	}
	convertDiskImageToRawCtx = func(ctx context.Context, _, _ string, format qemuimg.DiskFormat) error {
		receivedConversionContext = ctx
		if format != qemuimg.FormatRaw {
			t.Fatalf("conversion output format = %q", format)
		}
		converted++
		return nil
	}

	if err := writeStorageImageToTarget(
		context.Background(),
		"/downloads/router.raw",
		"/dev/zvol/tank/router",
		qemuimg.FormatRaw,
	); err != nil {
		t.Fatalf("copy raw image: %v", err)
	}
	if copied != 1 || converted != 0 {
		t.Fatalf("after raw import copied=%d converted=%d", copied, converted)
	}

	if err := writeStorageImageToTarget(
		conversionContext,
		"/downloads/router.qcow2",
		"/dev/zvol/tank/router",
		qemuimg.FormatQCOW2,
	); err != nil {
		t.Fatalf("convert qcow2 image: %v", err)
	}
	if copied != 1 || converted != 1 {
		t.Fatalf("after qcow2 import copied=%d converted=%d", copied, converted)
	}
	if receivedConversionContext != conversionContext {
		t.Fatal("conversion did not receive the request context")
	}
}

func imageTestZVOLCreator(
	rid uint,
	guid string,
	managed bool,
) func(uint, vmModels.Storage, context.Context, *gorm.DB) (vmModels.Storage, bool, error) {
	return func(_ uint, storage vmModels.Storage, _ context.Context, db *gorm.DB) (vmModels.Storage, bool, error) {
		dataset := vmModels.VMStorageDataset{
			Pool: "tank",
			Name: fmt.Sprintf("tank/sylve/virtual-machines/%d/zvol-%d", rid, storage.ID),
			GUID: guid,
		}
		if err := db.Create(&dataset).Error; err != nil {
			return storage, false, err
		}
		storage.DatasetID = &dataset.ID
		storage.Dataset = dataset
		if err := db.Save(&storage).Error; err != nil {
			return storage, false, err
		}
		return storage, managed, nil
	}
}

func TestStorageAttachApplyWritesImageBeforeSync(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 530, Name: "vm-530"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	service := &Service{DB: db}
	pool := "tank"
	size := int64(8 << 30)
	sourcePath := "/downloads/router.qcow2"
	wroteImage := false

	created, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:        libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType:       libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:         libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:              "router",
		RID:               vm.RID,
		Pool:              &pool,
		Size:              &size,
		ImageSourcePath:   sourcePath,
		ImageSourceFormat: string(qemuimg.FormatQCOW2),
	}, vm, context.Background(), storageRuntimeHooks{
		createVMDisk: imageTestZVOLCreator(vm.RID, "image-zvol-guid", false),
		writeImage: func(_ context.Context, source, target string, format qemuimg.DiskFormat) error {
			if source != sourcePath {
				t.Fatalf("source path = %q", source)
			}
			wantTarget := "/dev/zvol/tank/sylve/virtual-machines/530/zvol-1"
			if target != wantTarget {
				t.Fatalf("target path = %q, want %q", target, wantTarget)
			}
			if format != qemuimg.FormatQCOW2 {
				t.Fatalf("source format = %q", format)
			}
			wroteImage = true
			return nil
		},
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			if !wroteImage {
				return fmt.Errorf("sync ran before image write")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("attach image-backed storage: %v", err)
	}
	if !wroteImage {
		t.Fatal("image write did not run")
	}
	if created.Type != vmModels.VMStorageTypeZVol ||
		created.Size != size ||
		created.Dataset.Name == "" {
		t.Fatalf("unexpected created storage: %#v", created)
	}
}

func TestStorageAttachApplyCleansMetadataWhenImageWriteFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 531, Name: "vm-531"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	service := &Service{DB: db}
	pool := "tank"
	size := int64(8 << 30)
	syncCalled := false
	_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:        libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType:       libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:         libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:              "router",
		RID:               vm.RID,
		Pool:              &pool,
		Size:              &size,
		ImageSourcePath:   "/downloads/router.qcow2",
		ImageSourceFormat: string(qemuimg.FormatQCOW2),
	}, vm, context.Background(), storageRuntimeHooks{
		createVMDisk: imageTestZVOLCreator(vm.RID, "failed-image-zvol-guid", false),
		writeImage: func(context.Context, string, string, qemuimg.DiskFormat) error {
			return fmt.Errorf("boom_write")
		},
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			syncCalled = true
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "boom_write") {
		t.Fatalf("expected image write failure, got %v", err)
	}
	if syncCalled {
		t.Fatal("disk sync ran after image write failure")
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("storage rows after rollback = %d", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 0 {
		t.Fatalf("dataset rows after rollback = %d", got)
	}
}

func TestStorageAttachApplyCleansManagedDiskAfterCancellation(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 535, Name: "vm-535"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	datasets := make(map[string]storageTestDataset)
	service := newStorageTestService(db, []string{"tank"}, datasets)
	createDisk := imageTestZVOLCreator(vm.RID, "cancelled-image-zvol-guid", true)
	sourcePath := filepath.Join(t.TempDir(), "router.qcow2")
	if err := os.WriteFile(sourcePath, []byte("source image"), 0o600); err != nil {
		t.Fatalf("seed source image: %v", err)
	}

	pool := "tank"
	size := int64(8 << 30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	syncCalled := false

	_, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:        libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType:       libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:         libvirtServiceInterfaces.NVMEStorageEmulation,
		Name:              "router",
		RID:               vm.RID,
		Pool:              &pool,
		Size:              &size,
		ImageSourcePath:   sourcePath,
		ImageSourceFormat: string(qemuimg.FormatQCOW2),
	}, vm, ctx, storageRuntimeHooks{
		createVMDisk: func(rid uint, storage vmModels.Storage, createCtx context.Context, tx *gorm.DB) (vmModels.Storage, bool, error) {
			created, managed, createErr := createDisk(rid, storage, createCtx, tx)
			if createErr == nil {
				datasets[created.Dataset.Name] = storageTestDataset{
					name: created.Dataset.Name, pool: "tank", guid: created.Dataset.GUID,
					kind: gzfs.DatasetTypeVolume, mountpoint: "-", volsize: fmt.Sprint(size),
				}
			}
			return created, managed, createErr
		},
		writeImage: func(writeCtx context.Context, _, _ string, _ qemuimg.DiskFormat) error {
			cancel()
			<-writeCtx.Done()
			return writeCtx.Err()
		},
		syncVMDisks: func(context.Context, *gorm.DB, uint) error {
			syncCalled = true
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if syncCalled {
		t.Fatal("disk sync ran after image write cancellation")
	}
	if len(datasets) != 0 {
		t.Fatalf("managed dataset remains after cancellation: %+v", datasets)
	}
	if got := mustCountRows[vmModels.Storage](t, db); got != 0 {
		t.Fatalf("storage rows after cancellation = %d", got)
	}
	if got := mustCountRows[vmModels.VMStorageDataset](t, db); got != 0 {
		t.Fatalf("dataset rows after cancellation = %d", got)
	}
	contents, readErr := os.ReadFile(sourcePath)
	if readErr != nil || string(contents) != "source image" {
		t.Fatalf("source image changed during cleanup: contents=%q err=%v", contents, readErr)
	}
}

func TestCreateStorageFromImageRequiresCompletedDownload(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	imagePath := filepath.Join(t.TempDir(), "pending.qcow2")
	if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
		t.Fatalf("seed source image: %v", err)
	}
	if err := db.Create(&utilitiesModels.Downloads{
		UUID:     "pending-image",
		Path:     imagePath,
		Name:     filepath.Base(imagePath),
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "file://pending-image",
		Progress: 50,
		Size:     5,
		UType:    utilitiesModels.DownloadUTypeOther,
		Status:   utilitiesModels.DownloadStatusProcessing,
	}).Error; err != nil {
		t.Fatalf("seed download: %v", err)
	}

	service := &Service{DB: db}
	_, err := service.CreateStorageFromImage(libvirtServiceInterfaces.CreateStorageFromImageRequest{
		RID:          532,
		DownloadUUID: "pending-image",
		Name:         "router",
		Pool:         "tank",
		StorageType:  libvirtServiceInterfaces.StorageTypeZVOL,
		Emulation:    libvirtServiceInterfaces.NVMEStorageEmulation,
	}, context.Background())
	if err == nil || !strings.Contains(err.Error(), "download_not_ready") {
		t.Fatalf("expected incomplete download rejection, got %v", err)
	}
}

func TestCreateStorageFromImageRejectsTargetSmallerThanVirtualSize(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	imagePath := filepath.Join(t.TempDir(), "router.qcow2")
	if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
		t.Fatalf("seed source image: %v", err)
	}
	if err := db.Create(&utilitiesModels.Downloads{
		UUID:     "completed-image",
		Path:     imagePath,
		Name:     filepath.Base(imagePath),
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "file://completed-image",
		Progress: 100,
		Size:     5,
		UType:    utilitiesModels.DownloadUTypeOther,
		Status:   utilitiesModels.DownloadStatusDone,
	}).Error; err != nil {
		t.Fatalf("seed download: %v", err)
	}

	originalInspect := inspectDiskImageFormat
	originalSniff := sniffMediaMIME
	t.Cleanup(func() {
		inspectDiskImageFormat = originalInspect
		sniffMediaMIME = originalSniff
	})
	inspectDiskImageFormat = func(string) (*qemuimg.ImageInfo, error) {
		return &qemuimg.ImageInfo{Format: "qcow2", VirtualSize: 4 << 30}, nil
	}
	sniffMediaMIME = func(string) (string, error) {
		return "application/octet-stream", nil
	}

	service := &Service{DB: db}
	targetSize := int64(2 << 30)
	_, err := service.CreateStorageFromImage(libvirtServiceInterfaces.CreateStorageFromImageRequest{
		RID:          533,
		DownloadUUID: "completed-image",
		Name:         "router",
		Pool:         "tank",
		StorageType:  libvirtServiceInterfaces.StorageTypeZVOL,
		Size:         &targetSize,
		Emulation:    libvirtServiceInterfaces.NVMEStorageEmulation,
	}, context.Background())
	if err == nil || !strings.Contains(err.Error(), "target_size_too_small") {
		t.Fatalf("expected undersized target rejection, got %v", err)
	}
}

func TestStorageAttachApplyAtomicallyReplacesManagedRAWWithImage(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	vm := vmModels.VM{RID: 534, Name: "vm-534"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	mountpoint := t.TempDir()
	datasetName := "tank/sylve/virtual-machines/534/raw-1"
	service := newStorageTestService(db, []string{"tank"}, map[string]storageTestDataset{
		datasetName: {
			name:       datasetName,
			pool:       "tank",
			guid:       "image-raw-guid",
			kind:       gzfs.DatasetTypeFilesystem,
			mountpoint: mountpoint,
		},
	})
	pool := "tank"
	size := int64(32)
	targetPath := filepath.Join(mountpoint, "1.img")
	wroteImage := false

	created, err := service.storageAttachApply(libvirtServiceInterfaces.StorageAttachRequest{
		AttachType:        libvirtServiceInterfaces.StorageAttachTypeNew,
		StorageType:       libvirtServiceInterfaces.StorageTypeRaw,
		Emulation:         libvirtServiceInterfaces.VirtIOStorageEmulation,
		Name:              "router",
		RID:               vm.RID,
		Pool:              &pool,
		Size:              &size,
		ImageSourcePath:   "/downloads/router.raw",
		ImageSourceFormat: string(qemuimg.FormatRaw),
	}, vm, context.Background(), storageRuntimeHooks{
		createVMDisk: func(_ uint, storage vmModels.Storage, _ context.Context, db *gorm.DB) (vmModels.Storage, bool, error) {
			dataset := vmModels.VMStorageDataset{
				Pool: "tank",
				Name: datasetName,
				GUID: "image-raw-guid",
			}
			if err := db.Create(&dataset).Error; err != nil {
				return storage, false, err
			}
			storage.DatasetID = &dataset.ID
			storage.Dataset = dataset
			if err := db.Save(&storage).Error; err != nil {
				return storage, false, err
			}
			if err := os.WriteFile(targetPath, []byte("stale allocated contents"), 0o600); err != nil {
				return storage, false, err
			}
			return storage, false, nil
		},
		writeImage: func(_ context.Context, _, target string, format qemuimg.DiskFormat) error {
			if target != targetPath+".importing" {
				return fmt.Errorf("write target = %q", target)
			}
			if format != qemuimg.FormatRaw {
				return fmt.Errorf("source format = %q", format)
			}
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			if info.Size() != 0 {
				return fmt.Errorf("temporary target was not truncated: %d", info.Size())
			}
			wroteImage = true
			return os.WriteFile(target, []byte("converted"), 0o600)
		},
		syncVMDisks: func(context.Context, *gorm.DB, uint) error { return nil },
	})
	if err != nil {
		t.Fatalf("attach image-backed RAW storage: %v", err)
	}
	if !wroteImage || created.Type != vmModels.VMStorageTypeRaw || created.Size != size {
		t.Fatalf("unexpected created storage: %#v wrote=%t", created, wroteImage)
	}

	contents, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read final RAW image: %v", err)
	}
	if len(contents) != int(size) || string(contents[:9]) != "converted" {
		t.Fatalf("unexpected final RAW contents: length=%d prefix=%q", len(contents), contents[:9])
	}
	if _, err := os.Stat(targetPath + ".importing"); !os.IsNotExist(err) {
		t.Fatalf("temporary import path still exists: %v", err)
	}
}
