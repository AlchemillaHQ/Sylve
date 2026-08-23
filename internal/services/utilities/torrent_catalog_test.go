// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestPersistCompletedTorrentFilesPublishesCatalogAndStateAtomically(t *testing.T) {
	service := newSigningTestService(t)
	downloadUUID := utils.GenerateRandomUUID()
	downloadRoot := filepath.Join(config.GetDownloadsPath("torrents"), downloadUUID)
	if err := os.MkdirAll(filepath.Join(downloadRoot, "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"release/installer.iso": "installer",
		"release/checksums.txt": "checksums",
	} {
		if err := os.WriteFile(filepath.Join(downloadRoot, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	download := utilitiesModels.Downloads{
		UUID:     downloadUUID,
		Path:     downloadRoot,
		Name:     "release",
		Type:     utilitiesModels.DownloadTypeTorrent,
		URL:      "magnet:?xt=urn:btih:catalog",
		Progress: 99,
		Size:     18,
		Status:   utilitiesModels.DownloadStatusProcessing,
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}

	err := service.persistCompletedTorrentFiles(&download, []completedTorrentFile{
		{Path: "release/installer.iso", Size: 9},
		{Path: "release/checksums.txt", Size: 9},
	})
	if err != nil {
		t.Fatalf("persist completed torrent: %v", err)
	}

	var stored utilitiesModels.Downloads
	if err := service.DB.Preload("Files").First(&stored, download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != utilitiesModels.DownloadStatusDone || stored.Progress != 100 {
		t.Fatalf("stored state=%s progress=%d", stored.Status, stored.Progress)
	}
	if len(stored.Files) != 2 {
		t.Fatalf("catalog=%+v", stored.Files)
	}
}

func TestPersistCompletedTorrentFilesRejectsEscapingPathWithoutPublishingDone(t *testing.T) {
	service := newSigningTestService(t)
	downloadUUID := utils.GenerateRandomUUID()
	downloadRoot := filepath.Join(config.GetDownloadsPath("torrents"), downloadUUID)
	if err := os.MkdirAll(downloadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{
		UUID:     downloadUUID,
		Path:     downloadRoot,
		Name:     "unsafe",
		Type:     utilitiesModels.DownloadTypeTorrent,
		URL:      "magnet:?xt=urn:btih:unsafe-catalog",
		Progress: 99,
		Status:   utilitiesModels.DownloadStatusProcessing,
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}

	if err := service.persistCompletedTorrentFiles(&download, []completedTorrentFile{
		{Path: "../outside.iso", Size: 1},
	}); err == nil {
		t.Fatal("expected unsafe torrent catalog to fail")
	}

	var stored utilitiesModels.Downloads
	if err := service.DB.First(&stored, download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status == utilitiesModels.DownloadStatusDone {
		t.Fatal("unsafe catalog published a completed download")
	}
}
