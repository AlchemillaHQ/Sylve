// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/cavaliergopher/grab/v3"
)

func TestStorageInterruptionRetainsRecordsAndDoesNotStallDefault(t *testing.T) {
	service, _ := newDownloaderUploadTestService(t)
	service.enqueueDownloadStartFn = func(context.Context, utilitiesServiceInterfaces.DownloadStartPayload) error { return nil }
	root, err := downloadstorage.DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	defaultDownload := utilitiesModels.Downloads{
		UUID: utils.GenerateRandomUUID(), Type: utilitiesModels.DownloadTypePath,
		Path: filepath.Join(root, "path", "default.iso"), URL: "/source/default.iso",
		Name: "default.iso", Status: utilitiesModels.DownloadStatusProcessing, Progress: 99,
	}
	if err := os.WriteFile(defaultDownload.Path, []byte("default"), 0600); err != nil {
		t.Fatal(err)
	}
	poolDownload := utilitiesModels.Downloads{
		DownloadStorage: utilitiesModels.DownloadStorage{StoragePool: "missing", StorageRoot: "/missing", StoragePoolGUID: "pool", StorageDatasetGUID: "dataset"},
		UUID:            utils.GenerateRandomUUID(), Type: utilitiesModels.DownloadTypeHTTP,
		Path: "/missing/http/default.iso", URL: "http://example.invalid/default.iso",
		Name: "default.iso", Status: utilitiesModels.DownloadStatusPending, Progress: 37,
	}
	for _, download := range []*utilitiesModels.Downloads{&defaultDownload, &poolDownload} {
		if err := service.DB.Create(download).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := service.SyncDownloadProgress(); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetDownloadByID(poolDownload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Progress != 37 || stored.Status != utilitiesModels.DownloadStatusPending || stored.DownloadStorage != poolDownload.DownloadStorage || stored.Error != downloadstorage.ErrUnavailable.Error() {
		t.Fatalf("interruption changed durable state: %+v", stored)
	}
	completed, err := service.GetDownloadByID(defaultDownload.ID)
	if err != nil || completed.Status != utilitiesModels.DownloadStatusDone {
		t.Fatalf("Default stalled: %+v %v", completed, err)
	}
	listed, err := service.ListDownloads()
	if err != nil || len(listed) != 2 {
		t.Fatalf("unavailable record disappeared: %+v %v", listed, err)
	}

	if err := service.DB.Model(stored).Updates(map[string]any{"status": utilitiesModels.DownloadStatusDone, "progress": 100}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveSignedDownloadTargetByID(stored.UUID, int(stored.ID)); !errors.Is(err, downloadstorage.ErrUnavailable) {
		t.Fatalf("serving fell back: %v", err)
	}
	result, err := service.DeleteDownloads([]int{int(defaultDownload.ID), int(poolDownload.ID)})
	if !errors.Is(err, downloadstorage.ErrUnavailable) || len(result.Deleted) != 0 {
		t.Fatalf("mixed storage preflight: %+v %v", result, err)
	}
	if _, err := os.Stat(defaultDownload.Path); err != nil {
		t.Fatalf("mixed deletion removed Default: %v", err)
	}
	if stored, err := service.GetDownloadByID(poolDownload.ID); err != nil || stored.Status != utilitiesModels.DownloadStatusDone {
		t.Fatalf("unavailable completion rewritten: %+v %v", stored, err)
	}
}

func TestUnavailableUploadStorageRetainsExpiredIdentity(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	upload := stageDownloaderUpload(t, service, staging, 7, "upload.iso", "payload")
	upload.DownloadStorage = utilitiesModels.DownloadStorage{StoragePool: "missing", StorageRoot: "/missing", StoragePoolGUID: "pool", StorageDatasetGUID: "dataset"}
	upload.Path = "/missing/uploads/" + DownloaderUploadFinalName(upload.ID)
	upload.CreatedAt = service.uploadNow().Add(-2 * DownloaderUploadTTL)
	if err := service.DB.Save(&upload).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupExpiredUploads(t.Context()); !errors.Is(err, downloadstorage.ErrUnavailable) {
		t.Fatalf("expiration failed open: %v", err)
	}
	if _, err := service.AbortDownloaderUpload(t.Context(), upload.ID, 7); !errors.Is(err, downloadstorage.ErrUnavailable) {
		t.Fatalf("abort failed open: %v", err)
	}
	var stored utilitiesModels.Upload
	if err := service.DB.First(&stored, "id = ?", upload.ID).Error; err != nil {
		t.Fatalf("lost expired identity: %v", err)
	}
	if stored.DownloadStorage != upload.DownloadStorage {
		t.Fatal("upload rebound to Default")
	}
}

func TestHTTPFailureDuringStorageLossRetainsTransferState(t *testing.T) {
	service, _ := newDownloaderUploadTestService(t)
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	download := utilitiesModels.Downloads{
		DownloadStorage: utilitiesModels.DownloadStorage{StoragePool: "missing", StorageRoot: root, StoragePoolGUID: "pool", StorageDatasetGUID: "dataset"},
		UUID:            utils.GenerateRandomUUID(), Type: utilitiesModels.DownloadTypeHTTP,
		Path: filepath.Join(root, "http", "image.iso"), URL: server.URL, Name: "image.iso",
		Status: utilitiesModels.DownloadStatusPending, Progress: 37,
	}
	if err := os.MkdirAll(filepath.Dir(download.Path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(download.Path, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	request, err := grab.NewRequest(download.Path, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response := grab.NewClient().Do(request)
	<-response.Done
	if response.Err() == nil {
		t.Fatal("expected failed transfer")
	}
	service.httpResponses = map[string]*grab.Response{download.UUID: response}
	// Exercise the response-error branch directly: storage can disappear after
	// the sync loop's initial validation but before it observes transfer failure.
	service.syncHTTP(&download)
	stored, err := service.GetDownloadByID(download.ID)
	if err != nil || stored.Status != utilitiesModels.DownloadStatusPending || stored.Progress != 37 || stored.DownloadStorage != download.DownloadStorage || stored.Error != downloadstorage.ErrUnavailable.Error() {
		t.Fatalf("storage loss failed the transfer or reset progress: %+v %v", stored, err)
	}
	if data, err := os.ReadFile(download.Path); err != nil || string(data) != "partial" {
		t.Fatalf("partial payload changed: %q %v", data, err)
	}
}

func TestRawConversionDoesNotReplaceExistingPayload(t *testing.T) {
	service, _ := newDownloaderUploadTestService(t)
	storage, layout, err := service.DownloadStorage.Reserve(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(layout.Dir("path"), "image.img")
	output := filepath.Join(layout.Dir("path"), "image.raw")
	for _, file := range []string{path, output} {
		if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	download := utilitiesModels.Downloads{DownloadStorage: storage, UUID: utils.GenerateRandomUUID(), Type: utilitiesModels.DownloadTypePath, Path: path, URL: "source", Name: "image.img", Status: utilitiesModels.DownloadStatusProcessing, AutomaticRawConversion: true}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.StartPostProcess(&download.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "original" {
		t.Fatalf("conversion replaced a payload: %q %v", data, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed conversion removed its input: %v", err)
	}
}
