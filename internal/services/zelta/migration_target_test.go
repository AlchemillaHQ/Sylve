// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestIntegrationActivationChangesOnlyDifferingProperties(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	source := pool + "/source"
	target := pool + "/sylve/virtual-machines/733"
	zfstest.EnsureDataset(t, client, source+"/raw-1")
	zfstest.EnsureDataset(t, client, source+"/raw-2")
	zfstest.EnsureDataset(t, client, pool+"/sylve/virtual-machines")
	zfs := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("zfs", args...).CombinedOutput(); err != nil {
			t.Fatalf("zfs %v: %v\n%s", args, err, output)
		}
	}
	zfs("snapshot", "-r", source+"@sylve-migrate-initial-1700000000")
	svc := &Service{GZFS: client}
	send := exec.Command("zfs", "send", "-R", source+"@sylve-migrate-initial-1700000000")
	stream, err := send.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := send.Start(); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReceiveMigrationStream(t.Context(), target, stream); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := send.Wait(); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Like a replica: one child readonly and the root mounted elsewhere.
	zfs("set", "readonly=on", target+"/raw-2")
	zfs("set", "mountpoint="+filepath.Join(t.TempDir(), "elsewhere"), target)
	// Properties that are already right must not be set: a set would make
	// their source local.
	untouched := map[[2]string]string{}
	for _, key := range [][2]string{{target, "readonly"}, {target + "/raw-1", "readonly"}, {target + "/raw-1", "canmount"}} {
		untouched[key] = zfsGetPropertyWithSource(t, key[0], key[1])
	}

	if err := svc.prepareReplicatedDatasetForActivation(t.Context(), target); err != nil {
		t.Fatalf("activate: %v", err)
	}

	for key, before := range untouched {
		if after := zfsGetPropertyWithSource(t, key[0], key[1]); after != before || strings.HasSuffix(after, " local") {
			t.Errorf("%s %s = %q, want it untouched (%q)", key[0], key[1], after, before)
		}
	}
	for _, check := range []struct{ dataset, property, want string }{
		{target, "canmount", "on local"},
		{target + "/raw-2", "readonly", "off local"},
		{target, "mounted", "yes -"},
		{target + "/raw-1", "mounted", "yes -"},
		{target + "/raw-2", "mounted", "yes -"},
	} {
		if got := zfsGetPropertyWithSource(t, check.dataset, check.property); got != check.want {
			t.Errorf("%s %s = %q, want %q", check.dataset, check.property, got, check.want)
		}
	}
	if got := zfsGetPropertyWithSource(t, target, "mountpoint"); !strings.Contains(got, "inherited from") {
		t.Errorf("root mountpoint = %q, want inherited", got)
	}
}

func TestIntegrationMigrationTargetReceivesListsCleansAndPrepares(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	source := pool + "/source"
	target := pool + "/sylve/virtual-machines/731"
	zfstest.EnsureDataset(t, client, source+"/raw-1")
	zfstest.EnsureDataset(t, client, pool+"/sylve/virtual-machines")
	const snapshot = "sylve-migrate-initial-1700000000"
	if output, err := exec.Command("zfs", "snapshot", "-r", source+"@"+snapshot).CombinedOutput(); err != nil {
		t.Fatalf("snapshot source: %v\n%s", err, output)
	}
	svc := &Service{GZFS: client}

	send := exec.Command("zfs", "send", "-R", source+"@"+snapshot)
	stream, err := send.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := send.Start(); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReceiveMigrationStream(t.Context(), target, stream); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := send.Wait(); err != nil {
		t.Fatalf("send: %v", err)
	}

	exists, snapshots, err := svc.MigrationTargetDataset(t.Context(), target)
	if err != nil || !exists || !slices.Equal(snapshots, []string{snapshot}) {
		t.Fatalf("exists %t, snapshots %v, err %v", exists, snapshots, err)
	}
	if output, err := exec.Command("zfs", "get", "-H", "-o", "value", "canmount", target).Output(); err != nil || string(output) != "noauto\n" {
		t.Fatalf("received canmount = %q, err %v", output, err)
	}

	if err := svc.CleanupMigrationSnapshots(t.Context(), target); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, snapshots, _ := svc.MigrationTargetDataset(t.Context(), target); len(snapshots) != 0 {
		t.Fatalf("snapshots after cleanup = %v", snapshots)
	}

	if err := svc.PrepareMigrationReceive(t.Context(), target); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if exists, _, err := svc.MigrationTargetDataset(t.Context(), target); err != nil || exists {
		t.Fatalf("after prepare exists = %t, err %v", exists, err)
	}
	if err := svc.PrepareMigrationReceive(t.Context(), target); err != nil {
		t.Fatalf("prepare of a missing dataset: %v", err)
	}
	if err := svc.CleanupMigrationSnapshots(t.Context(), target); err != nil {
		t.Fatalf("cleanup of a missing dataset: %v", err)
	}
}
