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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
)

func TestFindISOByUUID_HTTPType_ResolvesRawImageWithoutExtensionUsingProbe(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const uuid = "http-download-no-ext-raw"
	const fileName = "ubuntu-25.10-server-cloudimg-amd64"
	httpDir := config.GetDownloadsPath("http")
	if err := os.MkdirAll(httpDir, 0o755); err != nil {
		t.Fatalf("failed to create http downloads dir: %v", err)
	}

	httpPath := filepath.Join(httpDir, fileName)
	if err := os.WriteFile(httpPath, []byte("raw-image"), 0o644); err != nil {
		t.Fatalf("failed to create http download file: %v", err)
	}

	origProbe := findISOProbeRawImage
	findISOProbeRawImage = func(path string) bool {
		return path == httpPath
	}
	t.Cleanup(func() {
		findISOProbeRawImage = origProbe
	})

	download := utilitiesModels.Downloads{
		UUID:     uuid,
		Path:     httpPath,
		Name:     fileName,
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.invalid/" + fileName,
		Progress: 100,
		Size:     9,
		UType:    utilitiesModels.DownloadUTypeCloudInit,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	isoPath, err := svc.FindISOByUUID(uuid, true)
	if err != nil {
		t.Fatalf("expected raw image without extension to resolve, got error: %v", err)
	}

	if isoPath != httpPath {
		t.Fatalf("expected %q, got %q", httpPath, isoPath)
	}
}

func TestFindISOByUUID_PathType_ResolvesExtractedFile(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const uuid = "path-download-extracted-file"
	extractedDir := filepath.Join(config.GetDownloadsPath("extracted"), uuid)
	if err := os.MkdirAll(extractedDir, 0o755); err != nil {
		t.Fatalf("failed to create extracted dir: %v", err)
	}

	extractedISO := filepath.Join(extractedDir, "decompressed.iso")
	if err := os.WriteFile(extractedISO, []byte("iso"), 0o644); err != nil {
		t.Fatalf("failed to create extracted iso: %v", err)
	}

	download := utilitiesModels.Downloads{
		UUID:          uuid,
		Path:          filepath.Join(config.GetDownloadsPath("path"), "compressed.iso.xz"),
		Name:          "compressed.iso.xz",
		Type:          utilitiesModels.DownloadTypePath,
		URL:           "/source/compressed.iso.xz",
		Progress:      100,
		Size:          3,
		UType:         utilitiesModels.DownloadUTypeOther,
		Status:        utilitiesModels.DownloadStatusDone,
		ExtractedPath: extractedISO,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	isoPath, err := svc.FindISOByUUID(uuid, false)
	if err != nil {
		t.Fatalf("expected extracted iso to resolve, got error: %v", err)
	}

	if isoPath != extractedISO {
		t.Fatalf("expected %q, got %q", extractedISO, isoPath)
	}
}

func TestFindISOByUUID_PathType_ResolvesExtractedDirectoryFallback(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const uuid = "path-download-extracted-dir"
	extractedDir := filepath.Join(config.GetDownloadsPath("extracted"), uuid)
	if err := os.MkdirAll(extractedDir, 0o755); err != nil {
		t.Fatalf("failed to create extracted dir: %v", err)
	}

	rawPath := filepath.Join(extractedDir, "converted.raw")
	if err := os.WriteFile(rawPath, []byte("raw"), 0o644); err != nil {
		t.Fatalf("failed to create extracted raw file: %v", err)
	}

	download := utilitiesModels.Downloads{
		UUID:     uuid,
		Path:     filepath.Join(config.GetDownloadsPath("path"), "compressed.qcow2.gz"),
		Name:     "compressed.qcow2.gz",
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "/source/compressed.qcow2.gz",
		Progress: 100,
		Size:     3,
		UType:    utilitiesModels.DownloadUTypeOther,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	isoPath, err := svc.FindISOByUUID(uuid, true)
	if err != nil {
		t.Fatalf("expected extracted raw to resolve, got error: %v", err)
	}

	if isoPath != rawPath {
		t.Fatalf("expected %q, got %q", rawPath, isoPath)
	}
}

func TestFlashCloudInitMediaToDisk_ConvertsNonRawMedia(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const rid uint = 901
	const diskID uint = 11
	const mediaUUID = "cloud-media-qcow2"

	mediaPath := filepath.Join(t.TempDir(), "cloud-image.img")
	if err := os.WriteFile(mediaPath, []byte("qcow2"), 0o644); err != nil {
		t.Fatalf("failed to create media file: %v", err)
	}

	download := utilitiesModels.Downloads{
		UUID:     mediaUUID,
		Path:     mediaPath,
		Name:     filepath.Base(mediaPath),
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "https://example.invalid/cloud-image.img",
		Progress: 100,
		Size:     5,
		UType:    utilitiesModels.DownloadUTypeCloudInit,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	poolRoot := filepath.Join(t.TempDir(), "pool-qcow2")
	poolName := strings.TrimPrefix(poolRoot, "/")
	diskPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img", poolName, rid, diskID, diskID)

	if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
		t.Fatalf("failed to create disk parent dir: %v", err)
	}
	if err := os.WriteFile(diskPath, []byte{}, 0o644); err != nil {
		t.Fatalf("failed to create disk file: %v", err)
	}

	vm := vmModels.VM{
		RID:               rid,
		CloudInitData:     "users: []",
		CloudInitMetaData: "instance-id: vm-901",
		Storages: []vmModels.Storage{
			{
				ID:      diskID,
				Type:    vmModels.VMStorageTypeRaw,
				Enable:  true,
				Size:    64 * 1024 * 1024,
				Dataset: vmModels.VMStorageDataset{Pool: poolName},
			},
			{
				ID:           diskID + 1,
				Type:         vmModels.VMStorageTypeDiskImage,
				Enable:       true,
				DownloadUUID: mediaUUID,
			},
		},
	}

	origInspect := inspectDiskImageFormat
	origFlash := flashImageToDiskCtx
	origConvert := convertDiskImageToRaw
	t.Cleanup(func() {
		inspectDiskImageFormat = origInspect
		flashImageToDiskCtx = origFlash
		convertDiskImageToRaw = origConvert
	})

	inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
		if path != mediaPath {
			t.Fatalf("unexpected inspect path: got %q, want %q", path, mediaPath)
		}
		return &qemuimg.ImageInfo{
			Format:      "qcow2",
			VirtualSize: 16 * 1024 * 1024,
		}, nil
	}

	flashImageToDiskCtx = func(_ context.Context, _ string, _ string) error {
		t.Fatalf("flash path should not be used for non-raw media")
		return nil
	}

	convertCalled := false
	convertDiskImageToRaw = func(src, dst string, outFmt qemuimg.DiskFormat) error {
		convertCalled = true
		if src != mediaPath {
			t.Fatalf("unexpected convert src: got %q, want %q", src, mediaPath)
		}
		if dst != diskPath {
			t.Fatalf("unexpected convert dst: got %q, want %q", dst, diskPath)
		}
		if outFmt != qemuimg.FormatRaw {
			t.Fatalf("unexpected convert output format: got %q, want %q", outFmt, qemuimg.FormatRaw)
		}
		return nil
	}

	if err := svc.FlashCloudInitMediaToDisk(vm); err != nil {
		t.Fatalf("expected non-raw cloud-init media conversion to succeed, got %v", err)
	}

	if !convertCalled {
		t.Fatalf("expected qemu-img conversion path to be used for non-raw media")
	}
}

func TestFlashCloudInitMediaToDisk_FlashesRawMedia(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const rid uint = 902
	const diskID uint = 12
	const mediaUUID = "cloud-media-raw"

	mediaPath := filepath.Join(t.TempDir(), "cloud-image.raw")
	if err := os.WriteFile(mediaPath, []byte("raw"), 0o644); err != nil {
		t.Fatalf("failed to create media file: %v", err)
	}

	download := utilitiesModels.Downloads{
		UUID:     mediaUUID,
		Path:     mediaPath,
		Name:     filepath.Base(mediaPath),
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "https://example.invalid/cloud-image.raw",
		Progress: 100,
		Size:     3,
		UType:    utilitiesModels.DownloadUTypeCloudInit,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	poolRoot := filepath.Join(t.TempDir(), "pool-raw")
	poolName := strings.TrimPrefix(poolRoot, "/")
	diskPath := fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img", poolName, rid, diskID, diskID)

	if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
		t.Fatalf("failed to create disk parent dir: %v", err)
	}
	if err := os.WriteFile(diskPath, []byte{}, 0o644); err != nil {
		t.Fatalf("failed to create disk file: %v", err)
	}

	vm := vmModels.VM{
		RID:               rid,
		CloudInitData:     "users: []",
		CloudInitMetaData: "instance-id: vm-902",
		Storages: []vmModels.Storage{
			{
				ID:      diskID,
				Type:    vmModels.VMStorageTypeRaw,
				Enable:  true,
				Size:    64 * 1024 * 1024,
				Dataset: vmModels.VMStorageDataset{Pool: poolName},
			},
			{
				ID:           diskID + 1,
				Type:         vmModels.VMStorageTypeDiskImage,
				Enable:       true,
				DownloadUUID: mediaUUID,
			},
		},
	}

	origInspect := inspectDiskImageFormat
	origFlash := flashImageToDiskCtx
	origConvert := convertDiskImageToRaw
	t.Cleanup(func() {
		inspectDiskImageFormat = origInspect
		flashImageToDiskCtx = origFlash
		convertDiskImageToRaw = origConvert
	})

	inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
		if path != mediaPath {
			t.Fatalf("unexpected inspect path: got %q, want %q", path, mediaPath)
		}
		return &qemuimg.ImageInfo{
			Format:      "raw",
			VirtualSize: 16 * 1024 * 1024,
		}, nil
	}

	convertDiskImageToRaw = func(_, _ string, _ qemuimg.DiskFormat) error {
		t.Fatalf("convert path should not be used for raw media")
		return nil
	}

	flashCalled := false
	flashImageToDiskCtx = func(_ context.Context, src, dst string) error {
		flashCalled = true
		if src != mediaPath {
			t.Fatalf("unexpected flash src: got %q, want %q", src, mediaPath)
		}
		if dst != diskPath {
			t.Fatalf("unexpected flash dst: got %q, want %q", dst, diskPath)
		}
		return nil
	}

	if err := svc.FlashCloudInitMediaToDisk(vm); err != nil {
		t.Fatalf("expected raw cloud-init media flash to succeed, got %v", err)
	}

	if !flashCalled {
		t.Fatalf("expected raw media flash path to be used")
	}
}
