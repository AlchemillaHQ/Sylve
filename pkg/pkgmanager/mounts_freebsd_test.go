// SPDX-License-Identifier: BSD-2-Clause
//go:build freebsd

package pkgmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestIntegrationJailPackagesPrivateFilesystem(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	datasetName := pool + "/sylve/jails/999"
	zfstest.EnsureDataset(t, client, datasetName)
	dataset, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{JID: 1, Name: "fixture", Root: dataset.Mountpoint, Dataset: dataset.Name, GUID: dataset.GUID}
	for _, dir := range []string{"etc", "usr/local/etc", "var/db/pkg", "var/cache/pkg", "boot", "data"} {
		if err = os.MkdirAll(filepath.Join(target.Root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if safe, err := ValidatePrivateRoot(t.Context(), target); err != nil || !safe {
		t.Fatalf("private root safe=%v err=%v", safe, err)
	}
	changed := target
	changed.GUID = "not-the-dataset-guid"
	if _, err := ValidatePrivateRoot(t.Context(), changed); err == nil {
		t.Fatal("accepted changed dataset GUID")
	}
	mounted := map[string]bool{}
	t.Cleanup(func() {
		for path, active := range mounted {
			if active {
				if out, err := exec.Command("/sbin/umount", path).CombinedOutput(); err != nil {
					zfstest.PreserveDedicatedPool(t, pool)
					t.Errorf("unmount fixture: %v %s", err, out)
				}
			}
		}
	})
	mount := func(relative string) string {
		t.Helper()
		path := filepath.Join(target.Root, relative)
		if out, err := exec.Command("/sbin/mount", "-t", "tmpfs", "tmpfs", path).CombinedOutput(); err != nil {
			t.Fatalf("mount fixture: %v %s", err, out)
		}
		mounted[path] = true
		return path
	}
	unmount := func(path string) {
		t.Helper()
		if out, err := exec.Command("/sbin/umount", path).CombinedOutput(); err != nil {
			t.Fatalf("unmount fixture: %v %s", err, out)
		}
		mounted[path] = false
	}
	data := mount("data")
	if safe, err := ValidatePrivateRoot(t.Context(), target); err != nil || safe {
		t.Fatalf("external data coverage safe=%v err=%v", safe, err)
	}
	if err := ValidatePackagePaths(t.Context(), target, []string{"/usr/local/bin/app"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePackagePaths(t.Context(), target, []string{"/data/application"}); err == nil {
		t.Fatal("accepted package files on shared/external mount")
	}
	unmount(data)
	system := mount("usr/local")
	if _, err := ValidatePrivateRoot(t.Context(), target); err == nil {
		t.Fatal("accepted shared system mount")
	}
	unmount(system)
	if err = os.Remove(filepath.Join(target.Root, "usr/local/etc")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/etc", filepath.Join(target.Root, "usr/local/etc")); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePrivateRoot(t.Context(), target); err == nil {
		t.Fatal("accepted symlinked package-system path")
	}
	if out, err := exec.Command("/sbin/zfs", "unmount", datasetName).CombinedOutput(); err != nil {
		t.Fatalf("unmount fixture dataset: %v %s", err, out)
	}
	target.JID, target.Offline = 0, true
	offline := Client{Target: target}
	if _, err := offline.Inventory(t.Context()); err == nil || err.Error() != "package_root_not_mounted" {
		t.Fatalf("unmounted root not rejected: %v", err)
	}
	if out, err := exec.Command("/sbin/zfs", "get", "-H", "-o", "value", "mounted", datasetName).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "no" {
		t.Fatalf("offline inspection mounted the dataset: %s %v", out, err)
	}
}
