// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
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
