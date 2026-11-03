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

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
)

func TestFindISOByUUID_HTTPType_UsesCanonicalPathAfterDisplayRename(t *testing.T) {
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

	origInspect := inspectDiskImageFormat
	inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
		if path == httpPath {
			return &qemuimg.ImageInfo{Format: "raw"}, nil
		}
		return nil, fmt.Errorf("unexpected path: %s", path)
	}
	t.Cleanup(func() {
		inspectDiskImageFormat = origInspect
	})

	download := utilitiesModels.Downloads{
		UUID:     uuid,
		Path:     httpPath,
		Name:     "Renamed installer image",
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

func TestFindISOByUUID_HTTPType_PrefersExtractedMediaWhenMainIsCompressed(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const uuid = "http-download-compressed-main"
	const fileName = "FreeBSD-14.3-RELEASE-amd64-BASIC-CLOUDINIT-zfs"

	httpDir := config.GetDownloadsPath("http")
	if err := os.MkdirAll(httpDir, 0o755); err != nil {
		t.Fatalf("failed to create http downloads dir: %v", err)
	}

	mainPath := filepath.Join(httpDir, fileName)
	if err := os.WriteFile(mainPath, []byte("xz-bytes"), 0o644); err != nil {
		t.Fatalf("failed to create main compressed file: %v", err)
	}

	extractedDir := filepath.Join(config.GetDownloadsPath("extracted"), uuid)
	if err := os.MkdirAll(extractedDir, 0o755); err != nil {
		t.Fatalf("failed to create extracted dir: %v", err)
	}

	extractedPath := filepath.Join(extractedDir, "FreeBSD-14")
	if err := os.WriteFile(extractedPath, []byte("raw-image"), 0o644); err != nil {
		t.Fatalf("failed to create extracted raw file: %v", err)
	}

	origInspect := inspectDiskImageFormat
	origSniff := sniffMediaMIME
	inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
		if path == mainPath || path == extractedPath {
			return &qemuimg.ImageInfo{Format: "raw"}, nil
		}
		return nil, fmt.Errorf("unexpected path: %s", path)
	}
	sniffMediaMIME = func(path string) (string, error) {
		if path == mainPath {
			return "application/x-xz", nil
		}
		return "", fmt.Errorf("unknown format")
	}
	t.Cleanup(func() {
		inspectDiskImageFormat = origInspect
		sniffMediaMIME = origSniff
	})

	download := utilitiesModels.Downloads{
		UUID:                uuid,
		Path:                mainPath,
		Name:                fileName,
		Type:                utilitiesModels.DownloadTypeHTTP,
		URL:                 "https://example.invalid/" + fileName + ".raw.xz",
		Progress:            100,
		Size:                8,
		UType:               utilitiesModels.DownloadUTypeCloudInit,
		Status:              utilitiesModels.DownloadStatusDone,
		AutomaticExtraction: true,
		ExtractedPath:       extractedPath,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	isoPath, err := svc.FindISOByUUID(uuid, true)
	if err != nil {
		t.Fatalf("expected extracted raw media to resolve, got error: %v", err)
	}

	if isoPath != extractedPath {
		t.Fatalf("expected extracted path %q, got %q", extractedPath, isoPath)
	}
}

func TestFindISOByUUID_HTTPType_RejectsCompressedMainWithoutExtraction(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const uuid = "http-download-reject-compressed-main"
	const fileName = "FreeBSD-14.3-RELEASE-amd64-BASIC-CLOUDINIT-zfs"

	httpDir := config.GetDownloadsPath("http")
	if err := os.MkdirAll(httpDir, 0o755); err != nil {
		t.Fatalf("failed to create http downloads dir: %v", err)
	}

	mainPath := filepath.Join(httpDir, fileName)
	if err := os.WriteFile(mainPath, []byte("xz-bytes"), 0o644); err != nil {
		t.Fatalf("failed to create main compressed file: %v", err)
	}

	origInspect := inspectDiskImageFormat
	origSniff := sniffMediaMIME
	inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
		if path == mainPath {
			return &qemuimg.ImageInfo{Format: "raw"}, nil
		}
		return nil, fmt.Errorf("unexpected path: %s", path)
	}
	sniffMediaMIME = func(path string) (string, error) {
		if path == mainPath {
			return "application/x-xz", nil
		}
		return "", fmt.Errorf("unknown format")
	}
	t.Cleanup(func() {
		inspectDiskImageFormat = origInspect
		sniffMediaMIME = origSniff
	})

	download := utilitiesModels.Downloads{
		UUID:     uuid,
		Path:     mainPath,
		Name:     fileName,
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.invalid/" + fileName + ".raw.xz",
		Progress: 100,
		Size:     8,
		UType:    utilitiesModels.DownloadUTypeCloudInit,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	_, err := svc.FindISOByUUID(uuid, true)
	if err == nil {
		t.Fatalf("expected compressed main payload to be rejected")
	}

	if !strings.Contains(err.Error(), "iso_or_img_not_found") {
		t.Fatalf("expected iso_or_img_not_found error, got: %v", err)
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

	origInspect := inspectDiskImageFormat
	inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
		if path == rawPath {
			return &qemuimg.ImageInfo{Format: "raw"}, nil
		}
		return nil, fmt.Errorf("unexpected path: %s", path)
	}
	t.Cleanup(func() {
		inspectDiskImageFormat = origInspect
	})

	isoPath, err := svc.FindISOByUUID(uuid, true)
	if err != nil {
		t.Fatalf("expected extracted raw to resolve, got error: %v", err)
	}

	if isoPath != rawPath {
		t.Fatalf("expected %q, got %q", rawPath, isoPath)
	}
}

func TestFindISOByUUID_TorrentSkipsNonImageFilesThatQemuTreatsAsRaw(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const uuid = "torrent-with-readme-before-image"
	torrentRoot := filepath.Join(config.GetDownloadsPath("torrents"), uuid)
	if err := os.MkdirAll(torrentRoot, 0o755); err != nil {
		t.Fatalf("failed to create torrent directory: %v", err)
	}

	readmePath := filepath.Join(torrentRoot, "README.txt")
	imagePath := filepath.Join(torrentRoot, "appliance.qcow2")
	if err := os.WriteFile(readmePath, []byte("not a disk image"), 0o644); err != nil {
		t.Fatalf("failed to create README: %v", err)
	}
	if err := os.WriteFile(imagePath, []byte("qcow2 image"), 0o644); err != nil {
		t.Fatalf("failed to create disk image: %v", err)
	}

	download := utilitiesModels.Downloads{
		UUID:     uuid,
		Path:     torrentRoot,
		Name:     "appliance-release",
		Type:     utilitiesModels.DownloadTypeTorrent,
		URL:      "magnet:?xt=urn:btih:appliance",
		Progress: 100,
		Size:     27,
		UType:    utilitiesModels.DownloadUTypeOther,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}
	files := []utilitiesModels.DownloadedFile{
		{DownloadID: int(download.ID), Name: "README.txt", Size: 16},
		{DownloadID: int(download.ID), Name: "appliance.qcow2", Size: 11},
	}
	if err := db.Create(&files).Error; err != nil {
		t.Fatalf("failed to seed torrent files: %v", err)
	}

	origInspect := inspectDiskImageFormat
	origSniff := sniffMediaMIME
	inspectedReadme := false
	inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
		switch path {
		case readmePath:
			inspectedReadme = true
			return &qemuimg.ImageInfo{Format: "raw"}, nil
		case imagePath:
			return &qemuimg.ImageInfo{Format: "qcow2"}, nil
		default:
			return nil, fmt.Errorf("unexpected path: %s", path)
		}
	}
	sniffMediaMIME = func(string) (string, error) {
		return "", fmt.Errorf("unknown format")
	}
	t.Cleanup(func() {
		inspectDiskImageFormat = origInspect
		sniffMediaMIME = origSniff
	})

	resolvedPath, err := svc.FindISOByUUID(uuid, true)
	if err != nil {
		t.Fatalf("expected torrent disk image to resolve, got error: %v", err)
	}
	if resolvedPath != imagePath {
		t.Fatalf("expected %q, got %q", imagePath, resolvedPath)
	}
	if inspectedReadme {
		t.Fatal("non-image torrent file was passed to qemu-img")
	}
}

func TestFlashCloudInitMediaToDisk(t *testing.T) {
	tests := []struct {
		name      string
		rid       uint
		diskID    uint
		mediaUUID string
		file      string
		content   string
		format    string
		convert   bool // non-raw media goes through qemu-img conversion, raw media is flashed directly
	}{
		{name: "converts non-raw media", rid: 901, diskID: 11, mediaUUID: "cloud-media-qcow2", file: "cloud-image.img", content: "qcow2", format: "qcow2", convert: true},
		{name: "flashes raw media", rid: 902, diskID: 12, mediaUUID: "cloud-media-raw", file: "cloud-image.raw", content: "raw", format: "raw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SYLVE_DATA_PATH", t.TempDir())
			db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
			const poolName = "tank"

			mediaPath := filepath.Join(config.GetDownloadsPath("path"), tt.file)
			if err := os.MkdirAll(filepath.Dir(mediaPath), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(mediaPath, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("failed to create media file: %v", err)
			}
			if err := db.Create(&utilitiesModels.Downloads{
				UUID: tt.mediaUUID, Path: mediaPath, Name: filepath.Base(mediaPath), Type: utilitiesModels.DownloadTypePath,
				URL: "https://example.invalid/" + tt.file, Progress: 100, Size: int64(len(tt.content)),
				UType: utilitiesModels.DownloadUTypeCloudInit, Status: utilitiesModels.DownloadStatusDone,
			}).Error; err != nil {
				t.Fatalf("failed to seed download row: %v", err)
			}

			datasetName := fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", poolName, tt.rid, tt.diskID)
			mountpoint := filepath.Join(t.TempDir(), fmt.Sprintf("raw-%d", tt.diskID))
			diskPath := filepath.Join(mountpoint, fmt.Sprintf("%d.img", tt.diskID))
			if err := os.MkdirAll(mountpoint, 0o755); err != nil {
				t.Fatalf("failed to create disk parent dir: %v", err)
			}
			if err := os.WriteFile(diskPath, []byte{}, 0o644); err != nil {
				t.Fatalf("failed to create disk file: %v", err)
			}

			vm := vmModels.VM{
				RID:               tt.rid,
				CloudInitData:     "users: []",
				CloudInitMetaData: fmt.Sprintf("instance-id: vm-%d", tt.rid),
				Storages: []vmModels.Storage{
					{
						ID: tt.diskID, Type: vmModels.VMStorageTypeRaw, Enable: true, Size: 64 * 1024 * 1024,
						Dataset: vmModels.VMStorageDataset{Pool: poolName, Name: datasetName},
					},
					{ID: tt.diskID + 1, Type: vmModels.VMStorageTypeDiskImage, Enable: true, DownloadUUID: tt.mediaUUID},
				},
			}
			svc := newStorageTestService(db, []string{poolName}, map[string]storageTestDataset{
				datasetName: {name: datasetName, pool: poolName, kind: gzfs.DatasetTypeFilesystem, mountpoint: mountpoint},
			})

			origInspect, origFlash, origConvert := inspectDiskImageFormat, flashImageToDiskCtx, convertDiskImageToRaw
			t.Cleanup(func() {
				inspectDiskImageFormat, flashImageToDiskCtx, convertDiskImageToRaw = origInspect, origFlash, origConvert
			})
			inspectDiskImageFormat = func(path string) (*qemuimg.ImageInfo, error) {
				if path != mediaPath {
					t.Fatalf("unexpected inspect path: got %q, want %q", path, mediaPath)
				}
				return &qemuimg.ImageInfo{Format: tt.format, VirtualSize: 16 * 1024 * 1024}, nil
			}
			called := false
			convertDiskImageToRaw = func(src, dst string, outFmt qemuimg.DiskFormat) error {
				if !tt.convert {
					t.Fatalf("convert path should not be used for raw media")
				}
				if src != mediaPath || dst != diskPath || outFmt != qemuimg.FormatRaw {
					t.Fatalf("unexpected convert(%q, %q, %q), want (%q, %q, %q)", src, dst, outFmt, mediaPath, diskPath, qemuimg.FormatRaw)
				}
				called = true
				return nil
			}
			flashImageToDiskCtx = func(_ context.Context, src, dst string) error {
				if tt.convert {
					t.Fatalf("flash path should not be used for non-raw media")
				}
				if src != mediaPath || dst != diskPath {
					t.Fatalf("unexpected flash(%q, %q), want (%q, %q)", src, dst, mediaPath, diskPath)
				}
				called = true
				return nil
			}

			if err := svc.FlashCloudInitMediaToDisk(vm); err != nil {
				t.Fatalf("expected cloud-init media to reach the disk, got %v", err)
			}
			if !called {
				t.Fatalf("expected the %s path to be used", map[bool]string{true: "convert", false: "flash"}[tt.convert])
			}
		})
	}
}
