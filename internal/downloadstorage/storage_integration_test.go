// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package downloadstorage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestIntegrationStorageIdentityAndChildMountRealZFS(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	if err := database.Create(&models.BasicSettings{Pools: []string{pool}}).Error; err != nil {
		t.Fatal(err)
	}
	resolver := New(database, client)
	ref, layout, err := resolver.Reserve(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&utilitiesModels.Downloads{DownloadStorage: ref, UUID: "test", Path: filepath.Join(layout.Dir("path"), "test.iso"), URL: "source"}).Error; err != nil {
		t.Fatal(err)
	}
	child, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/sylve/downloads/child", map[string]string{"mountpoint": filepath.Join(layout.Dir("path"), "child")})
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.ValidatePath(filepath.Join(layout.Dir("path"), "child", "test.iso")); !errors.Is(err, ErrMismatch) {
		t.Fatalf("payload crossed a child mount: %v", err)
	}
	if err := layout.ValidateTree(layout.Dir("path")); !errors.Is(err, ErrMismatch) {
		t.Fatalf("recursive cleanup crossed a child mount: %v", err)
	}
	if err := child.Destroy(t.Context(), true, false); err != nil {
		t.Fatal(err)
	}
	dataset, err := client.ZFS.Get(t.Context(), pool+"/sylve/downloads", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataset.Destroy(t.Context(), true, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolver.Reserve(t.Context(), pool); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing referenced dataset recreated: %v", err)
	}
	if _, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/sylve/downloads", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(t.Context(), ref, false); !errors.Is(err, ErrMismatch) {
		t.Fatalf("replacement dataset adopted: %v", err)
	}
	if _, _, err := resolver.Reserve(t.Context(), pool); !errors.Is(err, ErrMismatch) {
		t.Fatalf("new download adopted a referenced replacement: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ref.StorageRoot, "http")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement received managed directories: %v", err)
	}
}
