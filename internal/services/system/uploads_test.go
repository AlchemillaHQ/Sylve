// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package system

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestFileExplorerUploadIdentityRestrictsDeletionToOwner(t *testing.T) {
	service := &Service{
		DB: testutil.NewSQLiteTestDB(t, &utilitiesModels.Upload{}),
	}
	path := filepath.Join(t.TempDir(), "uploaded.iso")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	uploadID, err := service.RegisterFileExplorerUpload(path, 7)
	if err != nil {
		t.Fatalf("register upload: %v", err)
	}
	if uploadID == "" {
		t.Fatal("expected upload ID")
	}

	if err := service.DeleteFileExplorerUpload(uploadID, 8); !errors.Is(err, ErrFileExplorerUploadNotFound) {
		t.Fatalf("foreign delete error=%v want=%v", err, ErrFileExplorerUploadNotFound)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign user affected uploaded file: %v", err)
	}

	if err := service.DeleteFileExplorerUpload(uploadID, 7); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("uploaded file still exists: %v", err)
	}
}

func TestFileExplorerUploadIdentityDoesNotFollowReplacementSymlink(t *testing.T) {
	service := &Service{
		DB: testutil.NewSQLiteTestDB(t, &utilitiesModels.Upload{}),
	}
	root := t.TempDir()
	path := filepath.Join(root, "uploaded.iso")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(path, []byte("upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}

	uploadID, err := service.RegisterFileExplorerUpload(path, 7)
	if err != nil {
		t.Fatalf("register upload: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteFileExplorerUpload(uploadID, 7); !errors.Is(err, ErrFileExplorerUploadNotFile) {
		t.Fatalf("delete error=%v want=%v", err, ErrFileExplorerUploadNotFile)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "target" {
		t.Fatalf("replacement symlink target changed to %q", content)
	}
}

func TestFileExplorerUploadIdentityDoesNotDeleteReplacementFile(t *testing.T) {
	service := &Service{
		DB: testutil.NewSQLiteTestDB(t, &utilitiesModels.Upload{}),
	}
	path := filepath.Join(t.TempDir(), "uploaded.iso")
	if err := os.WriteFile(path, []byte("upload"), 0o600); err != nil {
		t.Fatal(err)
	}

	uploadID, err := service.RegisterFileExplorerUpload(path, 7)
	if err != nil {
		t.Fatalf("register upload: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteFileExplorerUpload(uploadID, 7); !errors.Is(err, ErrFileExplorerUploadNotFound) {
		t.Fatalf("delete error=%v want=%v", err, ErrFileExplorerUploadNotFound)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "replacement" {
		t.Fatalf("replacement file changed to %q", content)
	}
}

func TestFileExplorerUploadIdentityAllowsPathReuse(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Upload{})
	service := &Service{DB: database}
	path := filepath.Join(t.TempDir(), "uploaded.iso")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}

	firstID, err := service.RegisterFileExplorerUpload(path, 7)
	if err != nil {
		t.Fatalf("register first upload: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}

	secondID, err := service.RegisterFileExplorerUpload(path, 7)
	if err != nil {
		t.Fatalf("register upload after path reuse: %v", err)
	}
	if secondID == firstID {
		t.Fatal("expected a new upload identity for the replacement file")
	}

	var count int64
	if err := database.Model(&utilitiesModels.Upload{}).Where("path = ?", path).Count(&count).Error; err != nil {
		t.Fatalf("count upload identities: %v", err)
	}
	if count != 2 {
		t.Fatalf("upload identity count=%d, want 2", count)
	}
}

func TestFileExplorerUploadIdentityPersistsAcrossServiceRestart(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Upload{})
	firstService := &Service{DB: database}
	path := filepath.Join(t.TempDir(), "uploaded.iso")
	if err := os.WriteFile(path, []byte("upload"), 0o600); err != nil {
		t.Fatal(err)
	}

	uploadID, err := firstService.RegisterFileExplorerUpload(path, 7)
	if err != nil {
		t.Fatalf("register persistent upload: %v", err)
	}

	restartedService := &Service{DB: database}
	if err := restartedService.DeleteFileExplorerUpload(uploadID, 7); err != nil {
		t.Fatalf("delete after service restart: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("persistent revert left uploaded file: %v", err)
	}
}
