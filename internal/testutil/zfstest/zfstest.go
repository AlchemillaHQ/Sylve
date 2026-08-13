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
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
)

func SkipIfUnavailable(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("requires real ZFS; skipped in short mode")
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
