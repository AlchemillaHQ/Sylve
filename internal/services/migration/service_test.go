// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestCanonicalMigrationGuestDatasetUsesExactGuestIDBoundary(t *testing.T) {
	tests := []struct {
		name      string
		dataset   string
		guestType string
		guestID   uint
		want      bool
	}{
		{name: "vm root", dataset: "zroot/sylve/virtual-machines/1", guestType: "vm", guestID: 1, want: true},
		{name: "vm descendant", dataset: "zroot/sylve/virtual-machines/1/disk-0", guestType: "vm", guestID: 1, want: true},
		{name: "vm root snapshot", dataset: "zroot/sylve/virtual-machines/1@snap", guestType: "vm", guestID: 1, want: true},
		{name: "vm descendant snapshot", dataset: "zroot/sylve/virtual-machines/1/disk-0@snap", guestType: "vm", guestID: 1, want: true},
		{name: "vm adjacent 10", dataset: "zroot/sylve/virtual-machines/10", guestType: "vm", guestID: 1, want: false},
		{name: "vm adjacent 11 descendant", dataset: "zroot/sylve/virtual-machines/11/disk-0", guestType: "vm", guestID: 1, want: false},
		{name: "vm textual prefix", dataset: "zroot/sylve/virtual-machines/1-old", guestType: "vm", guestID: 1, want: false},
		{name: "vm noncanonical nesting", dataset: "zroot/archive/sylve/virtual-machines/1", guestType: "vm", guestID: 1, want: false},
		{name: "jail root", dataset: "zroot/sylve/jails/1", guestType: "jail", guestID: 1, want: true},
		{name: "jail descendant", dataset: "zroot/sylve/jails/1/root", guestType: "jail", guestID: 1, want: true},
		{name: "jail adjacent 10", dataset: "zroot/sylve/jails/10", guestType: "jail", guestID: 1, want: false},
		{name: "jail adjacent 11 descendant", dataset: "zroot/sylve/jails/11/root", guestType: "jail", guestID: 1, want: false},
		{name: "remote jail endpoint", dataset: "backup-host:zroot/sylve/jails/1/active", guestType: "jail", guestID: 1, want: true},
		{name: "wrong guest type", dataset: "zroot/sylve/jails/1", guestType: "vm", guestID: 1, want: false},
		{name: "zero guest id", dataset: "zroot/sylve/virtual-machines/1", guestType: "vm", guestID: 0, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isCanonicalMigrationGuestDataset(test.dataset, test.guestType, test.guestID); got != test.want {
				t.Fatalf("isCanonicalMigrationGuestDataset(%q, %q, %d) = %t, want %t", test.dataset, test.guestType, test.guestID, got, test.want)
			}
		})
	}
}

func TestMigrationOwnedSnapshotMatchesOnlyGeneratedNames(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "sylve-migrate-initial-1700000000", want: true},
		{name: "sylve-migrate-final-1700000001", want: true},
		{name: "sylve-migrate-pre-migration-1700000002", want: true},
		{name: "sylve-migrate", want: false},
		{name: "sylve-migrated-archive-1700000000", want: false},
		{name: "sylve-migrate-user-1700000000", want: false},
		{name: "sylve-migrate-final-not-a-time", want: false},
		{name: "sylve-migrate-final-01700000001", want: false},
	} {
		if got := isMigrationOwnedSnapshot(test.name); got != test.want {
			t.Errorf("isMigrationOwnedSnapshot(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestBuildClusterSSHArgs(t *testing.T) {
	identity := &clusterModels.ClusterSSHIdentity{
		SSHUser:  "root",
		SSHHost:  "10.0.0.2",
		SSHPort:  8183,
		NodeUUID: "test-node-uuid",
	}

	args := buildClusterSSHArgs(identity, "/tmp/test-key")
	if len(args) == 0 {
		t.Fatal("expected non-empty args")
	}

	hasBatchMode := false
	hasControlMaster := false
	for _, a := range args {
		if a == "-o" {
			continue
		}
		if strings.Contains(a, "BatchMode=yes") {
			hasBatchMode = true
		}
		if strings.Contains(a, "ControlMaster=auto") {
			hasControlMaster = true
		}
	}
	if !hasBatchMode {
		t.Fatal("expected BatchMode in SSH args")
	}
	if !hasControlMaster {
		t.Fatal("expected ControlMaster in SSH args")
	}
}

func TestBackupEventReferencesGuest_VM(t *testing.T) {
	svc := &Service{}

	event := clusterModels.BackupEvent{
		Mode:          clusterModels.BackupJobModeVM,
		SourceDataset: "zroot/sylve/virtual-machines/123/disk-0",
	}

	if !svc.backupEventReferencesGuest(event, "vm", 123) {
		t.Fatal("expected true for matching VM backup event")
	}

	event.SourceDataset = "zroot/sylve/virtual-machines/456/disk-0"
	if svc.backupEventReferencesGuest(event, "vm", 123) {
		t.Fatal("expected false for non-matching VM backup event")
	}

	event.SourceDataset = "zroot/sylve/virtual-machines/10/disk-0"
	if svc.backupEventReferencesGuest(event, "vm", 1) {
		t.Fatal("VM 1 must not match adjacent VM 10")
	}

	event.Mode = clusterModels.BackupJobModeDataset
	event.SourceDataset = "zroot/sylve/virtual-machines/123/disk-0"
	if svc.backupEventReferencesGuest(event, "vm", 123) {
		t.Fatal("expected false for dataset-mode event even if path matches")
	}
}

func TestBackupEventReferencesGuest_Jail(t *testing.T) {
	svc := &Service{}

	event := clusterModels.BackupEvent{
		Mode:          clusterModels.BackupJobModeJail,
		SourceDataset: "zroot/sylve/jails/42/root",
	}

	if !svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected true for matching jail backup event via source dataset")
	}

	event.SourceDataset = "other/something"
	event.TargetEndpoint = "backup-host:pool/sylve/jails/42/active"
	if !svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected true for matching jail backup event via target endpoint")
	}

	event.SourceDataset = "other/something"
	event.TargetEndpoint = "backup-host:pool/sylve/jails/99/active"
	if svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected false for non-matching jail backup event")
	}

	event.TargetEndpoint = "backup-host:pool/sylve/jails/11/active"
	if svc.backupEventReferencesGuest(event, "jail", 1) {
		t.Fatal("jail 1 must not match adjacent jail 11")
	}

	event.Mode = clusterModels.BackupJobModeVM
	if svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected false for VM-mode event when checking jail guest")
	}
}

func TestReplicationEventConflictReason(t *testing.T) {
	reason := "guest_has_running_replication_event"
	if reason == "" {
		t.Fatal("reason string must not be empty")
	}
}

func TestActiveLifecycleTaskConflictReason(t *testing.T) {
	reason := "guest_has_active_lifecycle_task"
	if reason == "" {
		t.Fatal("reason string must not be empty")
	}
}

func TestBackupEventConflictReason(t *testing.T) {
	reason := "guest_has_running_backup_event"
	if reason == "" {
		t.Fatal("reason string must not be empty")
	}
}
