// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newTargetInventoryCluster(
	t *testing.T,
	snapshot clusterService.GuestIdentityInventorySnapshot,
) *clusterService.Service {
	t.Helper()
	target := clusterService.NewClusterService(testutil.NewSQLiteTestDB(t), nil, nil).(*clusterService.Service)
	target.NodeID = "target-node"
	clusterService.OnNode(target, "guest-identity-inventory",
		func(context.Context, string, struct{}) (clusterService.GuestIdentityInventorySnapshot, error) {
			return snapshot, nil
		})
	return target
}

func requireTargetGuestRecordAbsent(t *testing.T, snapshot clusterService.GuestIdentityInventorySnapshot) error {
	t.Helper()
	svc := &Service{Cluster: newTargetInventoryCluster(t, snapshot)}
	return svc.requireTargetGuestRecordAbsent(t.Context(), clusterModels.ClusterNode{NodeUUID: "target-node"}, 107)
}

func TestRequireTargetGuestRecordAbsentBlocksSharedNumericID(t *testing.T) {
	err := requireTargetGuestRecordAbsent(t, clusterService.GuestIdentityInventorySnapshot{
		NodeID: "target-node",
		Report: clusterService.BuildGuestIdentityInventoryReport([]clusterService.GuestIdentityInventoryEntry{{
			NodeID: "target-node", GuestType: clusterModels.ReplicationGuestTypeJail,
			GuestID: 107, RecordID: 9, Name: "existing-jail",
		}}),
	})
	if !errors.Is(err, ErrTargetAlreadyHasGuest) {
		t.Fatalf("error = %v, want target guest conflict", err)
	}
	if !strings.Contains(err.Error(), "guest_type=jail") {
		t.Fatalf("error does not identify conflicting guest type: %v", err)
	}
}

func TestRequireTargetGuestRecordAbsentAllowsUnclaimedID(t *testing.T) {
	err := requireTargetGuestRecordAbsent(t, clusterService.GuestIdentityInventorySnapshot{
		NodeID: "target-node",
		Report: clusterService.BuildGuestIdentityInventoryReport([]clusterService.GuestIdentityInventoryEntry{{
			NodeID: "target-node", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 106, RecordID: 8, Name: "other-vm",
		}}),
	})
	if err != nil {
		t.Fatalf("unclaimed target ID was rejected: %v", err)
	}
}

func TestRequireTargetGuestRecordAbsentFailsClosedOnWrongNodeInventory(t *testing.T) {
	err := requireTargetGuestRecordAbsent(t, clusterService.GuestIdentityInventorySnapshot{
		NodeID: "different-node", Report: clusterService.BuildGuestIdentityInventoryReport(nil),
	})
	if err == nil || !strings.Contains(err.Error(), "target_identity_inventory_node_mismatch") {
		t.Fatalf("wrong-node inventory error = %v", err)
	}
}

func TestRequireTargetGuestRecordAbsentFailsClosedOnInventoryDigestMismatch(t *testing.T) {
	report := clusterService.BuildGuestIdentityInventoryReport(nil)
	report.Digest = "not-the-empty-inventory-digest"
	err := requireTargetGuestRecordAbsent(t, clusterService.GuestIdentityInventorySnapshot{
		NodeID: "target-node", Report: report,
	})
	if err == nil || !strings.Contains(err.Error(), "target_identity_inventory_digest_mismatch") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestValidateJailPreflightRejectsNonexistentCTIDBeforeRemoteChecks(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	svc := &Service{DB: db}

	reasons := svc.validateJailPreflight(context.Background(), 404, clusterModels.ClusterNode{})
	if len(reasons) != 1 || reasons[0] != "jail_not_found" {
		t.Fatalf("reasons = %v, want [jail_not_found]", reasons)
	}
}

func TestValidateJailPreflightRejectsInsufficientTargetCPU(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	enabled := true
	if err := db.Create(&jailModels.Jail{
		CTID:           23,
		Name:           "jail-23",
		Type:           jailModels.JailTypeFreeBSD,
		ResourceLimits: &enabled,
		Cores:          4,
	}).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}
	svc := &Service{DB: db}

	reasons := svc.validateJailPreflight(context.Background(), 23, clusterModels.ClusterNode{CPU: 2})
	if len(reasons) != 1 || reasons[0] != "target_cpu_capacity_insufficient: requested=4 available=2" {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestPhasePreflightRejectsUnreachableGuestBackupTarget(t *testing.T) {
	tests := []struct {
		name      string
		guestType string
		guestID   uint
		job       clusterModels.BackupJob
	}{
		{
			name:      "vm",
			guestType: clusterModels.BackupJobModeVM,
			guestID:   100,
			job: clusterModels.BackupJob{
				Mode: clusterModels.BackupJobModeVM, SourceDataset: "tank/sylve/virtual-machines/100",
				Recursive: true,
			},
		},
		{
			name:      "jail",
			guestType: clusterModels.BackupJobModeJail,
			guestID:   200,
			job: clusterModels.BackupJob{
				Mode: clusterModels.BackupJobModeJail, JailRootDataset: "tank/sylve/jails/200",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t,
				&clusterModels.ClusterNode{}, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{},
			)
			if err := db.Create(&clusterModels.ClusterNode{NodeUUID: "target-node"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&clusterModels.BackupTarget{
				ID: 1, Name: "required", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true,
			}).Error; err != nil {
				t.Fatal(err)
			}
			job := test.job
			job.ID = 1
			job.Name = "guest-backup"
			job.TargetID = 1
			job.RunnerNodeID = "source-node"
			job.Enabled = true
			if err := db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			clusterSvc := &clusterService.Service{DB: db, NodeID: "target-node"}
			clusterSvc.SetBackupTargetValidator(func(context.Context, *clusterModels.BackupTarget) error {
				return errors.New("backup target unreachable")
			})
			svc := &Service{DB: db, Cluster: clusterSvc}
			err := svc.phasePreflight(context.Background(), &migrationPayload{TargetNodeUUID: "target-node"}, taskModels.GuestLifecycleTask{
				GuestType: test.guestType, GuestID: test.guestID,
			})
			if err == nil || !strings.Contains(err.Error(), "migration_backup_target_preflight_failed") {
				t.Fatalf("preflight error = %v", err)
			}
		})
	}
}

func TestVMTargetPreflightReturnsHelperBootErrors(t *testing.T) {
	target := clusterService.NewClusterService(testutil.NewSQLiteTestDB(t), nil, nil).(*clusterService.Service)
	target.NodeID = "target-node"
	clusterService.OnNode(target, "migration-check-vm-target",
		func(_ context.Context, _ string, probe vmTargetProbe) (vmTargetResult, error) {
			if probe.BootRequirement == nil {
				t.Errorf("helper requirement omitted: %+v", probe)
			}
			return vmTargetResult{BootErrors: []string{"csmwrap_requires_two_vcpus"}}, nil
		})

	reasons := (&Service{Cluster: target}).vmTargetPreflightReasons(t.Context(), vmModels.VM{
		RID: 91, BootROM: vmModels.VMBootROMUEFICSMWrap, CPUSockets: 1, CPUCores: 1, CPUThreads: 1,
	}, clusterModels.ClusterNode{NodeUUID: "target-node"})
	if !slices.Equal(reasons, []string{"csmwrap_requires_two_vcpus"}) {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestVMTargetPreflightBlocksTargetWithoutVirtualization(t *testing.T) {
	target := clusterService.NewClusterService(testutil.NewSQLiteTestDB(t), nil, nil).(*clusterService.Service)
	target.NodeID = "target-node"
	clusterService.OnNode(target, "migration-check-vm-target",
		func(context.Context, string, vmTargetProbe) (vmTargetResult, error) {
			return vmTargetResult{VirtualizationDisabled: true}, nil
		})

	// A plain UEFI VM with nothing else to check still asks the target.
	reasons := (&Service{Cluster: target}).vmTargetPreflightReasons(t.Context(), vmModels.VM{
		RID: 92, BootROM: vmModels.VMBootROMUEFI, CPUSockets: 1, CPUCores: 1, CPUThreads: 1,
	}, clusterModels.ClusterNode{NodeUUID: "target-node"})
	if !slices.Equal(reasons, []string{"target_virtualization_disabled"}) {
		t.Fatalf("reasons = %v", reasons)
	}
}
