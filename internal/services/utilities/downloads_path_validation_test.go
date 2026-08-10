// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestDownloadFileRejectsNonRegularExistingPath(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	service := &Service{DB: db}

	_, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          t.TempDir(),
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if !errors.Is(err, ErrDownloadUnprocessable) {
		t.Fatalf("error=%v want unprocessable source", err)
	}
}

func TestDownloadFileReportsMissingExistingPath(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	service := &Service{DB: db}

	_, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          t.TempDir() + "/missing.img",
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if !errors.Is(err, ErrDownloadUnprocessable) {
		t.Fatalf("error=%v want unprocessable source", err)
	}
}

func TestDownloadFilePreservesExistingDestinationOnConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	destinationDir := filepath.Join(root, "downloads", "http")
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(destinationDir, "existing.img")
	if err := os.WriteFile(destination, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	service := &Service{DB: database}
	_, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          "https://example.test/existing.img",
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if !errors.Is(err, ErrDownloadConflict) {
		t.Fatalf("error=%v want destination conflict", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "keep-me" {
		t.Fatalf("destination contents=%q", contents)
	}

	var count int64
	if err := database.Model(&utilitiesModels.Downloads{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("created %d rows for conflicting destination", count)
	}
}

func TestDownloadFileKeepsPendingIdentityWhenQueueIsUnavailable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	if err := os.MkdirAll(filepath.Join(root, "downloads", "http"), 0o755); err != nil {
		t.Fatal(err)
	}

	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	service := &Service{
		DB: database,
		enqueueDownloadStartFn: func(
			context.Context,
			utilitiesServiceInterfaces.DownloadStartPayload,
		) error {
			return errors.New("queue offline")
		},
	}
	id, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          "https://example.test/retry.img",
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if id == 0 || !errors.Is(err, ErrDownloadQueueUnavailable) {
		t.Fatalf("id=%d error=%v", id, err)
	}

	var stored utilitiesModels.Downloads
	if err := database.First(&stored, id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != utilitiesModels.DownloadStatusPending ||
		stored.Error != ErrDownloadQueueUnavailable.Error() {
		t.Fatalf("unexpected durable state: %+v", stored)
	}
}

func TestUpdateDownloadKeepsStoredPathAndPreservesExplicitFalse(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	download := utilitiesModels.Downloads{
		UUID:                   "metadata-update",
		Path:                   "/managed/original.img",
		Name:                   "original.img",
		Type:                   utilitiesModels.DownloadTypePath,
		URL:                    "/source/original.img",
		Progress:               100,
		Status:                 utilitiesModels.DownloadStatusDone,
		UType:                  utilitiesModels.DownloadUTypeOther,
		AutomaticExtraction:    true,
		AutomaticRawConversion: true,
	}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database}
	name := "display-name.img"
	disabled := false
	updated, err := service.UpdateDownload(download.ID, utilitiesServiceInterfaces.UpdateDownloadRequest{
		Name:                   &name,
		AutomaticExtraction:    &disabled,
		AutomaticRawConversion: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.Path != download.Path {
		t.Fatalf("unexpected identity after metadata update: %+v", updated)
	}
	if updated.AutomaticExtraction || updated.AutomaticRawConversion {
		t.Fatalf("explicit false values were not preserved: %+v", updated)
	}
}
