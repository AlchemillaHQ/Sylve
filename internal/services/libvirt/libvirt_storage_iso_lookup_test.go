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
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

// TestFindISOByUUIDWithDB_UsesGivenHandleInsideTransaction is a regression
// test for the StorageUpdate/syncVMDisksWithDB deadlock: it reproduces the
// deadlock-prone environment (SQLite MaxOpenConns=1, running inside an open
// transaction) and asserts the ISO lookup completes promptly instead of
// blocking.
func TestFindISOByUUIDWithDB_UsesGivenHandleInsideTransaction(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{})
	svc := &Service{DB: db}

	const uuid = "storage-update-iso-lookup"
	httpDir := config.GetDownloadsPath("http")
	if err := os.MkdirAll(httpDir, 0o755); err != nil {
		t.Fatalf("failed to create http downloads dir: %v", err)
	}
	isoPath := filepath.Join(httpDir, "test.iso")
	if err := os.WriteFile(isoPath, []byte("iso"), 0o644); err != nil {
		t.Fatalf("failed to create iso file: %v", err)
	}

	download := utilitiesModels.Downloads{
		UUID:     uuid,
		Path:     isoPath,
		Name:     "test.iso",
		Type:     utilitiesModels.DownloadTypeHTTP,
		URL:      "https://example.invalid/test.iso",
		Progress: 100,
		Size:     3,
		UType:    utilitiesModels.DownloadUTypeOther,
		Status:   utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&download).Error; err != nil {
		t.Fatalf("failed to seed download row: %v", err)
	}

	txErr := db.Transaction(func(tx *gorm.DB) error {
		type result struct {
			path string
			err  error
		}
		done := make(chan result, 1)

		// Run the lookup on its own goroutine so a deadlock (blocking
		// forever waiting for a second SQLite connection) fails this test
		// instead of hanging the whole test binary.
		go func() {
			path, err := svc.findISOByUUIDWithDB(tx, uuid, true)
			done <- result{path, err}
		}()

		select {
		case r := <-done:
			if r.err != nil {
				return r.err
			}
			if r.path != isoPath {
				t.Fatalf("isoPath = %q, want %q", r.path, isoPath)
			}
			return nil
		case <-time.After(2 * time.Second):
			t.Fatal("findISOByUUIDWithDB(tx, ...) deadlocked when called from inside " +
				"an open transaction with a single-connection pool; this is the " +
				"StorageUpdate/syncVMDisksWithDB hang this test guards against")
			return nil
		}
	})
	if txErr != nil {
		t.Fatalf("transaction failed: %v", txErr)
	}
}
