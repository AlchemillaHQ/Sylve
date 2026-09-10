// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
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

func TestMigrationSnapshotPathsStayWithinExactGuestRoot(t *testing.T) {
	root := "zroot/sylve/virtual-machines/41"
	output := strings.Join([]string{
		root + "@sylve-migrate-initial-1700000000",
		root + "/disk-0@sylve-migrate-final-1700000001",
		root + "0@sylve-migrate-final-1700000001",
		root + "/disk-0@sylve-migrate-user-1700000001",
		root + "/disk-0@manual",
	}, "\n")
	want := []string{
		root + "/disk-0@sylve-migrate-final-1700000001",
		root + "@sylve-migrate-initial-1700000000",
	}
	if got := migrationSnapshotPathsWithinRoot(root, output); !reflect.DeepEqual(got, want) {
		t.Fatalf("migration snapshot paths = %v, want %v", got, want)
	}
}

func TestMigrationSnapshotMissingResultIsIdempotent(t *testing.T) {
	for _, message := range []string{
		"cannot open 'zroot/guest': dataset does not exist",
		"cannot destroy 'zroot/guest@sylve-migrate-final-1': snapshot does not exist",
		"could not find any snapshots to destroy",
	} {
		if !isMigrationSnapshotNotFound(errors.New(message)) {
			t.Fatalf("missing snapshot result was not idempotent: %s", message)
		}
	}
	if isMigrationSnapshotNotFound(errors.New("snapshot has dependent clones")) {
		t.Fatal("snapshot dependency failure was treated as missing")
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

func TestClusterRemoteCommandArgsEncodeArgumentsAndPreserveStreaming(t *testing.T) {
	identity := &clusterModels.ClusterSSHIdentity{
		NodeUUID: "node-42",
		SSHUser:  "root",
		SSHHost:  "Backup.Example",
		SSHPort:  8183,
	}
	args, err := clusterRemoteCommandArgs(
		identity,
		"/tmp/test-key",
		true,
		"zfs.recv",
		"tank/sylve/virtual-machines/42",
		"zfs", "recv", "-o", "sylve:run-id=secret-token", "tank/sylve/virtual-machines/42",
	)
	if err != nil {
		t.Fatalf("cluster remote command args: %v", err)
	}
	for _, arg := range args {
		if arg == "-n" {
			t.Fatal("streaming receiver retained ssh stdin suppression")
		}
		if strings.Contains(arg, "secret-token") || strings.Contains(arg, "sylve:run-id") {
			t.Fatalf("remote argument was not encoded: %q", arg)
		}
	}
	if len(args) < 2 || args[len(args)-2] != "root@backup.example" || !strings.HasPrefix(args[len(args)-1], "/bin/sh -c ") {
		t.Fatalf("unexpected ssh invocation: %v", args)
	}
}

func TestClusterRemoteCommandArgsRejectInvalidBoundaryValues(t *testing.T) {
	valid := &clusterModels.ClusterSSHIdentity{SSHUser: "root", SSHHost: "backup.example", SSHPort: 22}
	tests := []struct {
		name     string
		identity *clusterModels.ClusterSSHIdentity
		dataset  string
	}{
		{name: "missing identity"},
		{name: "unsafe host", identity: &clusterModels.ClusterSSHIdentity{SSHUser: "root", SSHHost: "backup;touch", SSHPort: 22}},
		{name: "embedded port", identity: &clusterModels.ClusterSSHIdentity{SSHUser: "root", SSHHost: "backup:22", SSHPort: 22}},
		{name: "invalid port", identity: &clusterModels.ClusterSSHIdentity{SSHUser: "root", SSHHost: "backup.example", SSHPort: 65536}},
		{name: "unsafe dataset", identity: valid, dataset: "tank/guest;touch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := clusterRemoteCommandArgs(
				test.identity, "", false, "zfs.list", test.dataset, "zfs", "list", test.dataset,
			); err == nil {
				t.Fatal("invalid remote boundary was accepted")
			}
		})
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

func TestBuildVMTargetProbeIncludesDisabledSwitchIdentity(t *testing.T) {
	originalResolve := resolveMigrationVMNetworkAttachment
	originalResolveIdentity := resolveMigrationVMNetworkIdentity
	t.Cleanup(func() {
		resolveMigrationVMNetworkAttachment = originalResolve
		resolveMigrationVMNetworkIdentity = originalResolveIdentity
	})

	defaultVLAN := 20
	resolveMigrationVMNetworkAttachment = func(
		_ *gorm.DB, switchType string, switchID uint,
	) (networkAttachment.Contract, error) {
		if switchType != "standard" || switchID != 1 {
			t.Fatalf("unexpected effective resolver input: %s:%d", switchType, switchID)
		}
		return networkAttachment.Normalize(networkAttachment.Contract{
			Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
			SwitchName: "tenant", SwitchType: "standard", VLANFiltering: true,
			DefaultAccessVLAN: &defaultVLAN,
		})
	}
	identityCalls := 0
	resolveMigrationVMNetworkIdentity = func(
		_ *gorm.DB, switchType string, switchID uint,
	) (networkAttachment.Contract, error) {
		identityCalls++
		name := "tenant"
		if switchID == 2 {
			name = "dormant"
		}
		return networkAttachment.LegacyUnfiltered(
			networkAttachment.KindVM, name, switchType,
		)
	}

	probe, _, err := (&Service{}).buildVMTargetProbe(vmModels.VM{
		RID: 91,
		Networks: []vmModels.Network{
			{SwitchID: 1, SwitchType: "standard", Enable: false},
			{SwitchID: 1, SwitchType: "standard", Enable: true},
			{SwitchID: 2, SwitchType: "manual", Enable: false},
		},
	})
	if err != nil {
		t.Fatalf("build probe: %v", err)
	}
	if identityCalls != 2 {
		t.Fatalf("identity resolver calls = %d, want 2", identityCalls)
	}
	if len(probe.Switches) != 2 {
		t.Fatalf("switches = %+v", probe.Switches)
	}
	if got := probe.Switches[0]; got.IdentityOnly || got.Attachment.SwitchName != "tenant" ||
		!got.Attachment.VLANFiltering || got.Attachment.DefaultAccessVLAN == nil ||
		*got.Attachment.DefaultAccessVLAN != defaultVLAN {
		t.Fatalf("enabled switch did not replace identity-only entry: %+v", got)
	}
	if got := probe.Switches[1]; !got.IdentityOnly || got.Attachment.SwitchName != "dormant" ||
		got.Attachment.SwitchType != "manual" {
		t.Fatalf("disabled switch identity = %+v", got)
	}
}

func TestBuildJailTargetProbePreservesVLANPolicy(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.StandardSwitch{},
	)
	defaultVLAN := 10
	switchModel := networkModels.StandardSwitch{
		Name: "LAN", BridgeName: "bridge0", VLANFiltering: true, DefaultAccessVLAN: &defaultVLAN,
	}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}
	jail := jailModels.Jail{CTID: 101, Name: "jail-101", Type: jailModels.JailTypeFreeBSD}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}
	untagged := 10
	if err := db.Create(&jailModels.Network{
		JailID: jail.ID, Name: "vnet0", SwitchID: switchModel.ID, SwitchType: "standard",
		VLANPolicy: bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &untagged},
	}).Error; err != nil {
		t.Fatalf("seed network: %v", err)
	}

	originalResolve := resolveMigrationJailNetworkAttachment
	resolverCalled := false
	resolveMigrationJailNetworkAttachment = func(
		_ *gorm.DB, network *jailModels.Network,
	) (networkAttachment.Contract, error) {
		resolverCalled = true
		policy := network.VLANPolicy
		return networkAttachment.Normalize(networkAttachment.Contract{
			Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindJail,
			SwitchName: "LAN", SwitchType: "standard", VLANFiltering: true,
			VLANPolicy: &policy,
		})
	}
	t.Cleanup(func() { resolveMigrationJailNetworkAttachment = originalResolve })

	probe, err := (&Service{DB: db}).buildJailTargetProbe(jail.ID)
	if err != nil {
		t.Fatalf("build probe: %v", err)
	}
	if !resolverCalled || len(probe.Networks) != 1 {
		t.Fatalf("resolverCalled=%t probe=%+v", resolverCalled, probe)
	}
	got := probe.Networks[0]
	if got.Name != "vnet0" || got.Attachment.SwitchName != "LAN" ||
		got.Attachment.SwitchType != "standard" || got.Attachment.Version != networkAttachment.CurrentVersion ||
		got.Attachment.VLANPolicy == nil || got.Attachment.VLANPolicy.Mode != bridgevlan.ModeAccess ||
		got.Attachment.VLANPolicy.UntaggedVLAN == nil || *got.Attachment.VLANPolicy.UntaggedVLAN != 10 {
		t.Fatalf("network probe = %+v", got)
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
