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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
)

func zfsSkipIfNotAvailable(t *testing.T) {
	t.Helper()
	if os.Getenv("SYLVE_SKIP_ZFS_TESTS") == "1" {
		t.Skip("SYLVE_SKIP_ZFS_TESTS=1")
	}
	if _, err := exec.LookPath("zpool"); err != nil {
		t.Skip("zpool binary not found")
	}
	if _, err := exec.LookPath("zfs"); err != nil {
		t.Skip("zfs binary not found")
	}
	// Must run as root to create zpools
	if os.Geteuid() != 0 {
		t.Skip("must be root to create ZFS pools")
	}
}

// zfsTestSetup creates a file-backed ZFS pool in a temp directory, returning the
// pool name and a cleanup function. The pool is completely isolated from the
// system's zroot pool.
func zfsTestSetup(t *testing.T) (poolName string, client *gzfs.Client, cleanup func()) {
	t.Helper()
	zfsSkipIfNotAvailable(t)

	dir := t.TempDir()
	vdevPath := filepath.Join(dir, "vdev.img")
	poolName = fmt.Sprintf("sylve-test-%d", time.Now().UnixNano())

	// create a sparse file for the vdev
	f, err := os.CreateTemp("", "sylve-zfs-vdev-*")
	if err != nil {
		t.Fatalf("create vdev file: %v", err)
	}
	vdevPath = f.Name()
	if err := f.Truncate(200 * 1024 * 1024); err != nil { // 200MB
		f.Close()
		t.Fatalf("truncate vdev: %v", err)
	}
	f.Close()

	// create the pool
	cmd := exec.Command("zpool", "create", "-m", dir, poolName, vdevPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(vdevPath)
		t.Fatalf("zpool create %s: %v\noutput: %s", poolName, err, string(out))
	}

	client = gzfs.NewClient(gzfs.Options{})

	cleanup = func() {
		ctx := context.Background()
		// try to export the pool gracefully
		export := exec.CommandContext(ctx, "zpool", "export", poolName)
		export.CombinedOutput()
		// if export fails, try destroy
		destroy := exec.CommandContext(ctx, "zpool", "destroy", "-f", poolName)
		destroy.CombinedOutput()
		os.Remove(vdevPath)
	}

	return poolName, client, cleanup
}

func ensureZFSDataset(t *testing.T, client *gzfs.Client, name string) {
	t.Helper()
	ctx := context.Background()
	// create parent datasets if needed
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i+1], "/")
		_, err := client.ZFS.CreateFilesystem(ctx, parent, nil)
		if err != nil {
			errStr := err.Error()
			if !strings.Contains(errStr, "already exists") && !strings.Contains(errStr, "dataset already exists") {
				// only create intermediate dirs, fail on the final one
				if i == len(parts)-1 {
					t.Fatalf("CreateFilesystem(%q): %v", parent, err)
				}
			}
		}
	}
}

func TestZFSGetLocalDataset(t *testing.T) {
	pool, client, cleanup := zfsTestSetup(t)
	defer cleanup()
	ds := pool + "/get-test"
	ctx := context.Background()

	ensureZFSDataset(t, client, ds)
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
	pool, client, cleanup := zfsTestSetup(t)
	defer cleanup()
	ds := pool + "/exists-test"
	ctx := context.Background()

	ensureZFSDataset(t, client, ds)
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

func TestZFSDestroyLocalDataset(t *testing.T) {
	pool, client, cleanup := zfsTestSetup(t)
	defer cleanup()
	ds := pool + "/destroy-test"
	ctx := context.Background()

	ensureZFSDataset(t, client, ds)
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
	pool, client, cleanup := zfsTestSetup(t)
	defer cleanup()
	src := pool + "/rename-src"
	dst := pool + "/rename-dst"
	ctx := context.Background()

	ensureZFSDataset(t, client, src)
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
	pool, client, cleanup := zfsTestSetup(t)
	defer cleanup()
	ds := pool + "/mount-test"
	ctx := context.Background()

	ensureZFSDataset(t, client, ds)
	s := &Service{GZFS: client}

	if err := s.unmountLocalDataset(ctx, ds); err != nil {
		t.Fatalf("unmountLocalDataset: %v", err)
	}

	if err := s.mountLocalDataset(ctx, ds); err != nil {
		t.Fatalf("mountLocalDataset: %v", err)
	}
}

func TestZFSEnsureLocalPoolExists(t *testing.T) {
	pool, client, cleanup := zfsTestSetup(t)
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
	pool, client, cleanup := zfsTestSetup(t)
	defer cleanup()
	ctx := context.Background()

	ensureZFSDataset(t, client, pool+"/list1")
	ensureZFSDataset(t, client, pool+"/list2")
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
