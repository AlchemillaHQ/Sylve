// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestZFSGetLocalDataset(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ds := pool + "/get-test"
	ctx := context.Background()

	zfstest.EnsureDataset(t, client, ds)
	s := &Service{GZFS: client}

	result, err := s.getLocalDataset(ctx, ds)
	if err != nil {
		t.Fatalf("getLocalDataset: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil dataset")
	}

	result, err = s.getLocalDataset(ctx, pool+"/does-not-exist")
	if err != nil {
		t.Fatalf("getLocalDataset non-existent: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil for non-existent dataset")
	}
}

func TestZFSLocalDatasetExists(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ds := pool + "/exists-test"
	ctx := context.Background()

	zfstest.EnsureDataset(t, client, ds)
	s := &Service{GZFS: client}

	exists, err := s.localDatasetExists(ctx, ds)
	if err != nil {
		t.Fatalf("localDatasetExists: %v", err)
	}
	if !exists {
		t.Fatal("expected dataset to exist")
	}

	exists, err = s.localDatasetExists(ctx, pool+"/nope")
	if err != nil {
		t.Fatalf("localDatasetExists non-existent: %v", err)
	}
	if exists {
		t.Fatal("expected non-existent to not exist")
	}
}

func TestRestoreStagingDatasetExistsFailsClosedWithDependentClone(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()
	staging := pool + "/live.restoring"
	clone := pool + "/external-clone"
	zfstest.EnsureDataset(t, client, staging)

	for _, args := range [][]string{
		{"snapshot", staging + "@preserve"},
		{"clone", staging + "@preserve", clone},
	} {
		output, err := exec.Command("zfs", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("zfs %s: %v\noutput: %s", strings.Join(args, " "), err, output)
		}
	}

	s := &Service{GZFS: client}
	err := s.requireRestoreStagingDatasetAvailable(ctx, staging)
	if err == nil {
		t.Fatal("expected existing restore staging dataset to fail closed")
	}
	if !strings.Contains(err.Error(), "restore_staging_dataset_exists_requires_manual_cleanup") {
		t.Fatalf("unexpected staging error: %v", err)
	}

	for _, dataset := range []string{staging, clone} {
		exists, existsErr := s.localDatasetExists(ctx, dataset)
		if existsErr != nil {
			t.Fatalf("check %s: %v", dataset, existsErr)
		}
		if !exists {
			t.Fatalf("%s was destroyed by staging preflight", dataset)
		}
	}
	originOut, err := exec.Command("zfs", "get", "-H", "-o", "value", "origin", clone).CombinedOutput()
	if err != nil {
		t.Fatalf("read clone origin: %v\noutput: %s", err, originOut)
	}
	if got, want := strings.TrimSpace(string(originOut)), staging+"@preserve"; got != want {
		t.Fatalf("clone origin = %q, want %q", got, want)
	}
}

func TestRestoreBackupCleanupPreservesArchiveWithDependentClone(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()
	archive := pool + "/live_restore-backup-owned"
	clone := pool + "/dependent-clone"
	zfstest.EnsureDataset(t, client, archive+"/child")

	for _, args := range [][]string{
		{"snapshot", archive + "@preserve"},
		{"clone", archive + "@preserve", clone},
	} {
		output, err := exec.Command("zfs", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("zfs %s: %v\noutput: %s", strings.Join(args, " "), err, output)
		}
	}

	s := &Service{GZFS: client}
	if err := s.cleanupRestoreBackupDataset(ctx, archive); err == nil {
		t.Fatal("expected ordinary recursive cleanup to be blocked by the dependent clone")
	}
	for _, dataset := range []string{archive, archive + "/child", clone} {
		exists, err := s.localDatasetExists(ctx, dataset)
		if err != nil {
			t.Fatalf("check %s: %v", dataset, err)
		}
		if !exists {
			t.Fatalf("%s was destroyed despite the dependent-clone cleanup failure", dataset)
		}
	}
}

func TestZFSDestroyLocalDataset(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ds := pool + "/destroy-test"
	ctx := context.Background()

	zfstest.EnsureDataset(t, client, ds)
	s := &Service{GZFS: client}

	exists, _ := s.localDatasetExists(ctx, ds)
	if !exists {
		t.Fatal("expected dataset to exist before destroy")
	}

	if err := s.destroyLocalDataset(ctx, ds, false); err != nil {
		t.Fatalf("destroyLocalDataset: %v", err)
	}

	exists, _ = s.localDatasetExists(ctx, ds)
	if exists {
		t.Fatal("expected dataset to be gone after destroy")
	}
}

func TestZFSRenameLocalDataset(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	src := pool + "/rename-src"
	dst := pool + "/rename-dst"
	ctx := context.Background()

	zfstest.EnsureDataset(t, client, src)
	s := &Service{GZFS: client}

	if err := s.renameLocalDataset(ctx, src, dst); err != nil {
		t.Fatalf("renameLocalDataset: %v", err)
	}

	exists, _ := s.localDatasetExists(ctx, src)
	if exists {
		t.Fatal("source should not exist after rename")
	}
	exists, _ = s.localDatasetExists(ctx, dst)
	if !exists {
		t.Fatal("destination should exist after rename")
	}
}

func TestZFSMountUnmountLocalDataset(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ds := pool + "/mount-test"
	ctx := context.Background()

	zfstest.EnsureDataset(t, client, ds)
	s := &Service{GZFS: client}

	if err := s.unmountLocalDataset(ctx, ds); err != nil {
		t.Fatalf("unmountLocalDataset: %v", err)
	}

	if err := s.mountLocalDataset(ctx, ds); err != nil {
		t.Fatalf("mountLocalDataset: %v", err)
	}
}

func TestZFSEnsureLocalPoolExists(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	s := &Service{GZFS: client}

	if err := s.ensureLocalPoolExists(context.Background(), pool); err != nil {
		t.Fatalf("ensureLocalPoolExists(%q): %v", pool, err)
	}
	if err := s.ensureLocalPoolExists(context.Background(), "nonexistent-pool-zzz"); err == nil {
		t.Fatal("expected error for non-existent pool")
	}
}

func TestZFSListLocalDatasets(t *testing.T) {
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	ctx := context.Background()

	zfstest.EnsureDataset(t, client, pool+"/list1")
	zfstest.EnsureDataset(t, client, pool+"/list2")
	s := &Service{GZFS: client}

	filesystems, err := s.listLocalFilesystemDatasets(ctx)
	if err != nil {
		t.Fatalf("listLocalFilesystemDatasets: %v", err)
	}
	found := false
	for _, fs := range filesystems {
		if fs == pool+"/list1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find %q in filesystem list (got %d entries)", pool+"/list1", len(filesystems))
	}
}

func TestIsLocalDatasetBusyError(t *testing.T) {
	if isLocalDatasetBusyError(nil) {
		t.Fatal("nil should not be busy")
	}
	err := errFromStr("dataset is busy")
	if !isLocalDatasetBusyError(err) {
		t.Fatal("'dataset is busy' should be detected")
	}
	err = errFromStr("resource busy")
	if !isLocalDatasetBusyError(err) {
		t.Fatal("'resource busy' should be detected")
	}
	err = errFromStr("some other error")
	if isLocalDatasetBusyError(err) {
		t.Fatal("unrelated error should not be busy")
	}
}

func TestIsLocalDatasetNotMountedError(t *testing.T) {
	if isLocalDatasetNotMountedError(nil) {
		t.Fatal("nil should not be not-mounted")
	}
	if !isLocalDatasetNotMountedError(errFromStr("filesystem is not currently mounted")) {
		t.Fatal("not-currently-mounted error should be detected")
	}
	if isLocalDatasetNotMountedError(errFromStr("dataset is busy")) {
		t.Fatal("busy error should not be not-mounted")
	}
}

func TestIsLocalDatasetHasDependentClonesError(t *testing.T) {
	if isLocalDatasetHasDependentClonesError(nil) {
		t.Fatal("nil should not be clones error")
	}
	err := errFromStr("dependent clones exist")
	if !isLocalDatasetHasDependentClonesError(err) {
		t.Fatal("'dependent clones' should be detected")
	}
	err = errFromStr("some other error")
	if isLocalDatasetHasDependentClonesError(err) {
		t.Fatal("unrelated error should not be clones error")
	}
}
