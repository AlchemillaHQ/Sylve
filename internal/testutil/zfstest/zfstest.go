// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfstest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
)

func SkipIfUnavailable(t testing.TB) {
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
	if os.Geteuid() != 0 {
		t.Skip("must be root to create ZFS pools")
	}
}

func Pool(t testing.TB) (poolName string, client *gzfs.Client, cleanup func()) {
	t.Helper()
	SkipIfUnavailable(t)

	dir := t.TempDir()
	poolName = fmt.Sprintf("sylve-test-%d", time.Now().UnixNano())

	f, err := os.CreateTemp("", "sylve-zfs-vdev-*")
	if err != nil {
		t.Fatalf("create vdev file: %v", err)
	}
	vdevPath := f.Name()
	if err := f.Truncate(200 * 1024 * 1024); err != nil {
		f.Close()
		t.Fatalf("truncate vdev: %v", err)
	}
	f.Close()

	cmd := exec.Command("zpool", "create", "-m", dir, poolName, vdevPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(vdevPath)
		t.Fatalf("zpool create %s: %v\noutput: %s", poolName, err, string(out))
	}

	client = gzfs.NewClient(gzfs.Options{})

	cleanup = func() {
		ctx := context.Background()
		exec.CommandContext(ctx, "zpool", "export", poolName).CombinedOutput()
		exec.CommandContext(ctx, "zpool", "destroy", "-f", poolName).CombinedOutput()
		os.Remove(vdevPath)
	}

	return poolName, client, cleanup
}

func EnsureDataset(t testing.TB, client *gzfs.Client, name string) {
	t.Helper()
	ctx := context.Background()
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i+1], "/")
		_, err := client.ZFS.CreateFilesystem(ctx, parent, nil)
		if err != nil {
			errStr := err.Error()
			if !strings.Contains(errStr, "already exists") && !strings.Contains(errStr, "dataset already exists") {
				if i == len(parts)-1 {
					t.Fatalf("CreateFilesystem(%q): %v", parent, err)
				}
			}
		}
	}
}

func EnsureVolume(t testing.TB, client *gzfs.Client, name string, sizeMB int) {
	t.Helper()
	ctx := context.Background()
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		EnsureDataset(t, client, name[:idx])
	}
	if _, err := client.ZFS.CreateVolume(ctx, name, uint64(sizeMB)*1024*1024, nil); err != nil {
		t.Fatalf("CreateVolume(%q): %v", name, err)
	}
}
