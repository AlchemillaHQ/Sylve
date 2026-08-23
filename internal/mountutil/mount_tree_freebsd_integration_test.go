// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package mountutil

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"golang.org/x/sys/unix"
)

func TestIntegrationUnmountDescendantsForceBusy(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	datasetName := pool + "/sylve/jails/981"
	zfstest.EnsureDataset(t, client, datasetName)
	dataset, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatalf("get test dataset: %v", err)
	}
	busyMount := filepath.Join(dataset.Mountpoint, "busy")
	if err := os.MkdirAll(busyMount, 0o755); err != nil {
		t.Fatalf("create busy mountpoint: %v", err)
	}

	mounted := false
	t.Cleanup(func() {
		if mounted {
			_, _ = exec.Command("/sbin/umount", "-f", busyMount).CombinedOutput()
		}
	})
	if output, err := exec.Command(
		"/sbin/mount", "-t", "tmpfs", "tmpfs", busyMount,
	).CombinedOutput(); err != nil {
		t.Skipf("tmpfs mount unavailable: %v: %s", err, output)
	}
	mounted = true

	holder := exec.Command("/bin/sleep", "300")
	holder.Dir = busyMount
	if err := holder.Start(); err != nil {
		t.Fatalf("start busy-mount holder: %v", err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	})

	if err := unix.Unmount(busyMount, 0); !errors.Is(err, unix.EBUSY) {
		if err == nil {
			mounted = false
		}
		t.Fatalf("normal unmount error = %v, want EBUSY", err)
	}

	if err := UnmountDescendants(
		t.Context(), datasetName, dataset.Mountpoint,
	); err != nil {
		t.Fatalf("force busy descendant: %v", err)
	}
	mounted = false

	mounts, err := (freeBSDMountTreeOps{}).list(t.Context())
	if err != nil {
		t.Fatalf("list mounts after cleanup: %v", err)
	}
	for _, mount := range mounts {
		if mount.Target == busyMount {
			t.Fatalf("busy tmpfs remains mounted: %+v", mount)
		}
	}
}
