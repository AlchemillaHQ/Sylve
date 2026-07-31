// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestValidateDownloaderPostProcessOptions(t *testing.T) {
	tests := []struct {
		name       string
		sourceName string
		extract    bool
		raw        bool
		wantErr    bool
	}{
		{
			name:       "extract tar archive",
			sourceName: "base.txz",
			extract:    true,
		},
		{
			name:       "convert image",
			sourceName: "disk.qcow2",
			raw:        true,
		},
		{
			name:       "decompress then convert image",
			sourceName: "disk.img.xz",
			extract:    true,
			raw:        true,
		},
		{
			name:       "reject compressed tar then raw",
			sourceName: "base.txz",
			extract:    true,
			raw:        true,
			wantErr:    true,
		},
		{
			name:       "reject case insensitive tar then raw",
			sourceName: "BASE.TAR.GZ",
			extract:    true,
			raw:        true,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDownloaderPostProcessOptions(
				test.sourceName,
				test.extract,
				test.raw,
			)
			if test.wantErr && !errors.Is(err, ErrDownloaderPostProcessOptions) {
				t.Fatalf("error=%v want incompatible post-processing options", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDownloadFileRejectsKnownTarAndRawCombinationBeforeCreation(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	service := &Service{DB: database}
	enabled := true

	_, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:                    "https://example.test/base.txz",
		AutomaticExtraction:    &enabled,
		AutomaticRawConversion: &enabled,
		DownloadType:           utilitiesModels.DownloadUTypeBase,
	})
	if !errors.Is(err, ErrDownloaderPostProcessOptions) {
		t.Fatalf("error=%v want incompatible post-processing options", err)
	}

	var count int64
	if err := database.Model(&utilitiesModels.Downloads{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid options created %d downloader records", count)
	}
}

func TestUpdateDownloadRejectsKnownTarAndRawCombinationBeforeMutation(t *testing.T) {
	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	download := utilitiesModels.Downloads{
		UUID:                utils.GenerateRandomUUID(),
		Path:                filepath.Join(t.TempDir(), "base.txz"),
		Name:                "base.txz",
		Type:                utilitiesModels.DownloadTypePath,
		URL:                 filepath.Join(t.TempDir(), "source.txz"),
		Progress:            100,
		Status:              utilitiesModels.DownloadStatusDone,
		AutomaticExtraction: true,
	}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database}
	enabled := true

	err := service.UpdateDownload(download.ID, utilitiesServiceInterfaces.UpdateDownloadRequest{
		AutomaticRawConversion: &enabled,
	})
	if !errors.Is(err, ErrDownloaderPostProcessOptions) {
		t.Fatalf("error=%v want incompatible post-processing options", err)
	}

	var stored utilitiesModels.Downloads
	if err := database.First(&stored, download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AutomaticRawConversion {
		t.Fatal("invalid RAW option was persisted")
	}
}

func TestStartPostProcessRejectsRenamedTarAndRawCombination(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	source := filepath.Join(root, "renamed-image.bin")
	file, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	content := []byte("rootfs")
	if err := writer.WriteHeader(&tar.Header{
		Name: "etc/version",
		Mode: 0o644,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	download := utilitiesModels.Downloads{
		UUID:                   utils.GenerateRandomUUID(),
		Path:                   source,
		Name:                   "renamed-image.bin",
		Type:                   utilitiesModels.DownloadTypePath,
		URL:                    source + ".source",
		Progress:               100,
		Status:                 utilitiesModels.DownloadStatusProcessing,
		AutomaticExtraction:    true,
		AutomaticRawConversion: true,
	}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database}

	if err := service.StartPostProcess(&download.ID); err != nil {
		t.Fatalf("post-process queue result: %v", err)
	}

	var stored utilitiesModels.Downloads
	if err := database.First(&stored, download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != utilitiesModels.DownloadStatusFailed ||
		!strings.Contains(stored.Error, ErrDownloaderPostProcessOptions.Error()) {
		t.Fatalf("renamed tar was not rejected actionably: %+v", stored)
	}
	if _, err := os.Stat(strings.TrimSuffix(source, filepath.Ext(source)) + ".raw"); !os.IsNotExist(err) {
		t.Fatalf("invalid tar conversion published RAW output: %v", err)
	}
}

func TestStartPostProcessRejectsUnsupportedExtractionFormat(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	source := filepath.Join(root, "plain.txt")
	if err := os.WriteFile(source, []byte("not compressed"), 0o600); err != nil {
		t.Fatal(err)
	}

	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	download := utilitiesModels.Downloads{
		UUID:                utils.GenerateRandomUUID(),
		Path:                source,
		Name:                "plain.txt",
		Type:                utilitiesModels.DownloadTypePath,
		URL:                 source + ".source",
		Progress:            100,
		Status:              utilitiesModels.DownloadStatusProcessing,
		AutomaticExtraction: true,
	}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database}

	if err := service.StartPostProcess(&download.ID); err != nil {
		t.Fatalf("post-process queue result: %v", err)
	}

	var stored utilitiesModels.Downloads
	if err := database.First(&stored, download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != utilitiesModels.DownloadStatusFailed ||
		!strings.Contains(stored.Error, ErrDownloaderExtractionFormat.Error()) {
		t.Fatalf("unsupported extraction was not rejected actionably: %+v", stored)
	}
}
