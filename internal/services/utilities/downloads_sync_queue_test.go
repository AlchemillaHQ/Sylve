// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"context"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestListDownloads_EnqueueSyncOnceWhileQueued(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})

	enqueueCalls := 0
	service := &Service{
		DB: db,
		enqueueNoPayloadFn: func(ctx context.Context, name string) error {
			if name != "utils-download-sync" {
				t.Fatalf("unexpected queue job name: %s", name)
			}
			enqueueCalls++
			return nil
		},
	}

	pending := utilitiesModels.Downloads{
		UUID:     "sync-once-uuid",
		Path:     "/tmp/sync-once-path",
		Name:     "sync-once.img",
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.com/sync-once.img",
		Progress: 0,
		Status:   utilitiesModels.DownloadStatusPending,
	}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatalf("failed to seed pending download: %v", err)
	}

	if _, err := service.ListDownloads(); err != nil {
		t.Fatalf("ListDownloads #1 failed: %v", err)
	}
	if _, err := service.ListDownloads(); err != nil {
		t.Fatalf("ListDownloads #2 failed: %v", err)
	}

	if enqueueCalls != 1 {
		t.Fatalf("expected one enqueue call while sync is queued, got %d", enqueueCalls)
	}

	service.clearDownloadSyncQueued()

	if _, err := service.ListDownloads(); err != nil {
		t.Fatalf("ListDownloads #3 failed: %v", err)
	}

	if enqueueCalls != 2 {
		t.Fatalf("expected second enqueue after queue flag reset, got %d", enqueueCalls)
	}
}

func TestListDownloads_DoesNotEnqueueSyncWhenNoPending(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})

	enqueueCalls := 0
	service := &Service{
		DB: db,
		enqueueNoPayloadFn: func(ctx context.Context, name string) error {
			enqueueCalls++
			return nil
		},
	}

	done := utilitiesModels.Downloads{
		UUID:     "sync-done-uuid",
		Path:     "/tmp/sync-done-path",
		Name:     "sync-done.img",
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "/tmp/source.img",
		Progress: 100,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&done).Error; err != nil {
		t.Fatalf("failed to seed done download: %v", err)
	}

	if _, err := service.ListDownloads(); err != nil {
		t.Fatalf("ListDownloads failed: %v", err)
	}

	if enqueueCalls != 0 {
		t.Fatalf("expected no enqueue calls for done downloads, got %d", enqueueCalls)
	}
}
