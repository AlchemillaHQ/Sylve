// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newDownloadDeleteTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	for _, kind := range []string{"http", "path", "torrents", "extracted", "uploads"} {
		if err := os.MkdirAll(config.GetDownloadsPath(kind), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Upload{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
		&vmModels.VM{},
		&vmModels.Storage{},
	)
	return &Service{
		DB:                   database,
		inflight:             make(map[uint]struct{}),
		downloadStartRunning: make(map[uint]struct{}),
		downloadDeleting:     make(map[uint]struct{}),
		downloadStartQueued:  make(map[uint]struct{}),
	}
}

func seedHTTPDownloadForDeletion(t *testing.T, service *Service, name string) utilitiesModels.Downloads {
	t.Helper()
	path := filepath.Join(config.GetDownloadsPath("http"), name)
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     "delete-" + name,
		Path:     path,
		Name:     name,
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.invalid/" + name,
		Progress: 100,
		Size:     7,
		Status:   utilitiesModels.DownloadStatusDone,
		UType:    utilitiesModels.DownloadUTypeOther,
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	file := utilitiesModels.DownloadedFile{
		DownloadID: int(download.ID),
		Name:       name,
		Size:       7,
	}
	if err := service.DB.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	return download
}

func TestDeleteDownloadsStrictMissingPreflightAndDeduplicatedCleanup(t *testing.T) {
	service := newDownloadDeleteTestService(t)
	download := seedHTTPDownloadForDeletion(t, service, "strict.iso")

	result, err := service.DeleteDownloads([]int{int(download.ID), 99999})
	if !errors.Is(err, ErrDownloadNotFound) {
		t.Fatalf("error=%v want ErrDownloadNotFound", err)
	}
	if len(result.Deleted) != 0 || len(result.Failed) != 1 || result.Failed[0].ID != 99999 {
		t.Fatalf("unexpected strict preflight result: %+v", result)
	}
	if _, err := os.Stat(download.Path); err != nil {
		t.Fatalf("strict preflight removed payload: %v", err)
	}

	result, err = service.DeleteDownloads([]int{int(download.ID), int(download.ID)})
	if err != nil {
		t.Fatalf("deduplicated deletion failed: %v", err)
	}
	if len(result.Deleted) != 1 || len(result.Failed) != 0 {
		t.Fatalf("unexpected deletion result: %+v", result)
	}
	if _, err := os.Stat(download.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("payload was retained: %v", err)
	}
	var fileCount int64
	if err := service.DB.Model(&utilitiesModels.DownloadedFile{}).
		Where("download_id = ?", download.ID).
		Count(&fileCount).Error; err != nil {
		t.Fatal(err)
	}
	if fileCount != 0 {
		t.Fatalf("downloaded file rows retained: %d", fileCount)
	}
}

func TestDeleteDownloadsRejectsDisabledVMStorageReference(t *testing.T) {
	service := newDownloadDeleteTestService(t)
	download := seedHTTPDownloadForDeletion(t, service, "referenced.iso")
	vm := vmModels.VM{Name: "Referenced VM", RID: 4100}
	if err := service.DB.Create(&vm).Error; err != nil {
		t.Fatal(err)
	}
	storage := vmModels.Storage{
		Type:         vmModels.VMStorageTypeDiskImage,
		Name:         "Installer",
		DownloadUUID: download.UUID,
		Enable:       false,
		VMID:         vm.ID,
	}
	if err := service.DB.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}

	result, err := service.DeleteDownloads([]int{int(download.ID)})
	if !errors.Is(err, ErrDownloadInUse) {
		t.Fatalf("error=%v want ErrDownloadInUse", err)
	}
	if len(result.Failed) != 1 || len(result.Failed[0].VMReferences) != 1 {
		t.Fatalf("missing VM reference details: %+v", result)
	}
	if _, err := os.Stat(download.Path); err != nil {
		t.Fatalf("in-use payload was removed: %v", err)
	}
}

func TestDeleteDownloadsRejectsActivePathWorkWithoutRemovingSource(t *testing.T) {
	service := newDownloadDeleteTestService(t)
	sourcePath := filepath.Join(filepath.Dir(config.GetDownloadsPath("path")), "source.img")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	managedPath := filepath.Join(config.GetDownloadsPath("path"), "active.img")
	if err := os.WriteFile(managedPath, []byte("managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     "active-path",
		Path:     managedPath,
		Name:     "active.img",
		Type:     utilitiesModels.DownloadTypePath,
		URL:      sourcePath,
		Progress: 0,
		Size:     7,
		Status:   utilitiesModels.DownloadStatusPending,
		UType:    utilitiesModels.DownloadUTypeOther,
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service.downloadStartRunning[download.ID] = struct{}{}

	result, err := service.DeleteDownloads([]int{int(download.ID)})
	if !errors.Is(err, ErrDownloadActive) {
		t.Fatalf("error=%v want ErrDownloadActive", err)
	}
	if len(result.Failed) != 1 || result.Failed[0].Code != "download_active" {
		t.Fatalf("unexpected active result: %+v", result)
	}
	for _, path := range []string{sourcePath, managedPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("active deletion removed %s: %v", path, err)
		}
	}

	delete(service.downloadStartRunning, download.ID)
	result, err = service.DeleteDownloads([]int{int(download.ID)})
	if err != nil || len(result.Deleted) != 1 {
		t.Fatalf("delete inactive path download: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("ordinary source path was removed: %v", err)
	}
	if _, err := os.Stat(managedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed path was retained: %v", err)
	}
}

func TestDeleteDownloadsRemovesDetachedTorrentPayload(t *testing.T) {
	service := newDownloadDeleteTestService(t)
	const uuid = "detached-torrent"
	torrentPath := filepath.Join(config.GetDownloadsPath("torrents"), uuid)
	if err := os.MkdirAll(torrentPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(torrentPath, "disk.iso"), []byte("torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     uuid,
		Path:     torrentPath,
		Name:     "disk.iso",
		Type:     utilitiesModels.DownloadTypeTorrent,
		URL:      "magnet:?xt=urn:btih:detached-torrent",
		Progress: 100,
		Size:     7,
		Status:   utilitiesModels.DownloadStatusDone,
		UType:    utilitiesModels.DownloadUTypeOther,
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}

	result, err := service.DeleteDownloads([]int{int(download.ID)})
	if err != nil {
		t.Fatalf("delete detached torrent: %v", err)
	}
	if len(result.Deleted) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(torrentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("torrent payload retained: %v", err)
	}
}
