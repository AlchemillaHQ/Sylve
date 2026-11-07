// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestRemapMigratedVMMetadataPoolsMovesOnlyTheVMsOwnDatasets(t *testing.T) {
	meta := &restoredVMMetadata{
		VM: vmModels.VM{RID: 40, Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeZVol, Pool: "tank", Dataset: vmModels.VMStorageDataset{Pool: "tank", Name: "tank/sylve/virtual-machines/40/zvol-1"}},
			{Type: vmModels.VMStorageTypeRaw, Pool: "tank"},
			{Type: vmModels.VMStorageTypeZVol, Pool: "fast", Dataset: vmModels.VMStorageDataset{Pool: "fast", Name: "fast/sylve/virtual-machines/40/zvol-3"}},
			{Type: vmModels.VMStorageTypeFilesystem, Pool: "tank", Dataset: vmModels.VMStorageDataset{Pool: "tank", Name: "tank/shares/media"}},
			{Type: vmModels.VMStorageTypeDiskImage, Pool: "tank"},
			{Type: vmModels.VMStorageTypeZVol, Pool: "tank", Dataset: vmModels.VMStorageDataset{Pool: "tank", Name: "tank/sylve/virtual-machines/400/zvol-6"}},
		}},
		Snapshots: []vmModels.VMSnapshot{{RootDatasets: []string{"tank/sylve/virtual-machines/40", "fast/sylve/virtual-machines/40"}}},
	}

	remapMigratedVMMetadataPools(meta, map[string]string{"tank": "data"})

	want := []struct{ pool, dataset string }{
		{"data", "data/sylve/virtual-machines/40/zvol-1"},
		{"data", ""},
		{"fast", "fast/sylve/virtual-machines/40/zvol-3"},
		{"tank", "tank/shares/media"},
		{"tank", ""},
		{"tank", "tank/sylve/virtual-machines/400/zvol-6"},
	}
	for i, storage := range meta.VM.Storages {
		if storage.Pool != want[i].pool || storage.Dataset.Name != want[i].dataset {
			t.Errorf("storage %d = pool %q dataset %q, want pool %q dataset %q",
				i, storage.Pool, storage.Dataset.Name, want[i].pool, want[i].dataset)
		}
		if storage.Dataset.Name != "" && storage.Dataset.Pool != storage.Pool {
			t.Errorf("storage %d dataset pool = %q, storage pool = %q", i, storage.Dataset.Pool, storage.Pool)
		}
	}
	if roots := meta.Snapshots[0].RootDatasets; !slices.Equal(roots, []string{
		"data/sylve/virtual-machines/40", "fast/sylve/virtual-machines/40",
	}) {
		t.Fatalf("snapshot roots = %v", roots)
	}
}

func TestRebaseMigratedJailFstabFollowsTheNewRootMountpoint(t *testing.T) {
	mountPoint := t.TempDir()
	meta := &restoredJailMetadata{
		Jail: jailModels.Jail{
			CTID:  23,
			Fstab: "/srv/media /tank/sylve/jails/23/mnt/media nullfs ro 0 0\n/srv/other /elsewhere nullfs ro 0 0",
		},
		RootMountpoint: "/tank/sylve/jails/23",
	}
	svc := &Service{}

	if err := svc.rebaseMigratedJailFstab(meta, mountPoint); err != nil {
		t.Fatal(err)
	}
	want := "/srv/media " + mountPoint + "/mnt/media nullfs ro 0 0\n/srv/other /elsewhere nullfs ro 0 0"
	if meta.Jail.Fstab != want {
		t.Fatalf("fstab = %q, want %q", meta.Jail.Fstab, want)
	}

	raw, err := os.ReadFile(filepath.Join(mountPoint, ".sylve", "jail.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Fstab          string `json:"fstab"`
		RootMountpoint string `json:"rootMountpoint"`
	}
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if written.Fstab != want || written.RootMountpoint != mountPoint {
		t.Fatalf("jail.json fstab = %q, root mountpoint = %q", written.Fstab, written.RootMountpoint)
	}

	meta.RootMountpoint = mountPoint
	meta.Jail.Fstab = "/srv/media /unchanged nullfs ro 0 0"
	if err := svc.rebaseMigratedJailFstab(meta, mountPoint); err != nil || meta.Jail.Fstab != "/srv/media /unchanged nullfs ro 0 0" {
		t.Fatalf("same mountpoint changed fstab to %q (err %v)", meta.Jail.Fstab, err)
	}
}

func TestRemapMigratedVMMetadataPoolsCollapsesMergedSnapshotRoots(t *testing.T) {
	meta := &restoredVMMetadata{
		VM:        vmModels.VM{RID: 40},
		Snapshots: []vmModels.VMSnapshot{{RootDatasets: []string{"tank/sylve/virtual-machines/40", "fast/sylve/virtual-machines/40"}}},
	}
	remapMigratedVMMetadataPools(meta, map[string]string{"tank": "data", "fast": "data"})
	if roots := meta.Snapshots[0].RootDatasets; !slices.Equal(roots, []string{"data/sylve/virtual-machines/40"}) {
		t.Fatalf("snapshot roots = %v", roots)
	}
}

func TestIntegrationMergeMigratedVMRootsMovesStagedDisksIntoRoot(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	root := pool + "/sylve/virtual-machines/731"
	staging := root + ".merge-1"
	zfstest.EnsureDataset(t, client, root+"/raw-1")
	zfstest.EnsureDataset(t, client, staging+"/raw-2")
	if _, err := client.ZFS.CreateVolume(t.Context(), staging+"/zvol-3", 1<<20, nil); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("zfs", "snapshot", "-r", staging+"@svms_before_1").CombinedOutput(); err != nil {
		t.Fatalf("snapshot staging: %v\n%s", err, output)
	}

	svc := &Service{GZFS: client}
	staged := map[string][]string{root: {staging}}
	if err := svc.MergeMigratedVMRoots(t.Context(), 731, staged); err != nil {
		t.Fatalf("merge: %v", err)
	}
	for _, name := range []string{root + "/raw-1", root + "/raw-2", root + "/zvol-3", root + "/raw-2@svms_before_1", root + "/zvol-3@svms_before_1"} {
		if output, err := exec.Command("zfs", "list", "-H", "-t", "all", name).CombinedOutput(); err != nil {
			t.Errorf("%s missing after merge: %v\n%s", name, err, output)
		}
	}
	if exec.Command("zfs", "list", "-H", staging).Run() == nil {
		t.Errorf("staging %s still exists", staging)
	}
	if err := svc.MergeMigratedVMRoots(t.Context(), 731, staged); err != nil {
		t.Fatalf("retried merge: %v", err)
	}
}
