// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"context"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

func TestReconcileBackupTargetProvisionOperationCompletesPreparedCreate(t *testing.T) {
	h := newFakeSSHHarness(t)
	h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		"zfs version": {{ExitCode: 0}},
		"zfs list -H -o name -t filesystem -d 0 tank/backups": {
			{Stderr: "cannot open 'tank/backups': dataset does not exist\n", ExitCode: 1},
			{Stdout: "tank/backups\n", ExitCode: 0},
		},
		"zpool list -H -o name tank": {{Stdout: "tank\n", ExitCode: 0}},
		"zfs create -p tank/backups": {{ExitCode: 0}},
	}})
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupTarget{}, &clusterModels.BackupTargetProvisionOperation{}, &clusterModels.BackupJob{},
	)
	cluster := &clusterService.Service{DB: database}
	service := newTestZeltaService(database)
	service.Cluster = cluster
	candidate := &clusterModels.BackupTarget{
		Name: "target", SSHHost: "root@backup", SSHPort: 22, SSHKey: "private-key",
		BackupRoot: "tank/backups", CreateBackupRoot: true, Enabled: true,
	}
	operation, err := cluster.PrepareBackupTargetProvisionCreate(candidate, "provision:reconcile", true)
	if err != nil {
		t.Fatalf("prepare operation: %v", err)
	}
	var count int64
	if err := database.Model(&clusterModels.BackupTarget{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("target visible before reconcile count=%d err=%v", count, err)
	}
	if err := service.ReconcileBackupTargetProvisionOperations(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var target clusterModels.BackupTarget
	if err := database.First(&target, operation.TargetID).Error; err != nil {
		t.Fatalf("load committed target: %v", err)
	}
	if clusterModels.BackupTargetConfigurationFingerprint(&target) != operation.ProposedFingerprint {
		t.Fatalf("committed target mismatch: %+v", target)
	}
	var stored clusterModels.BackupTargetProvisionOperation
	if err := database.First(&stored, "token = ?", operation.Token).Error; err != nil {
		t.Fatalf("load operation: %v", err)
	}
	if stored.State != clusterModels.BackupTargetProvisionStateCompleted {
		t.Fatalf("operation state=%q", stored.State)
	}
	assertFakeSSHCallSequence(t, h.Calls(), []string{
		"zfs version",
		"zfs list -H -o name -t filesystem -d 0 tank/backups",
		"zpool list -H -o name tank",
		"zfs create -p tank/backups",
		"zfs list -H -o name -t filesystem -d 0 tank/backups",
	})
}

func TestReconcileBackupTargetProvisionCompletesAfterCrashFollowingRemoteCreate(t *testing.T) {
	h := newFakeSSHHarness(t)
	h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		"zfs version": {{ExitCode: 0}},
		"zfs list -H -o name -t filesystem -d 0 tank/backups": {
			{Stdout: "tank/backups\n", ExitCode: 0},
		},
	}})
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupTarget{}, &clusterModels.BackupTargetProvisionOperation{}, &clusterModels.BackupJob{},
	)
	cluster := &clusterService.Service{DB: database}
	service := newTestZeltaService(database)
	service.Cluster = cluster
	operation, err := cluster.PrepareBackupTargetProvisionCreate(&clusterModels.BackupTarget{
		Name: "target", SSHHost: "root@backup", SSHKey: "key", BackupRoot: "tank/backups",
		CreateBackupRoot: true, Enabled: true,
	}, "provision:after-create-crash", true)
	if err != nil {
		t.Fatalf("prepare operation: %v", err)
	}
	if err := service.ReconcileBackupTargetProvisionOperations(context.Background()); err != nil {
		t.Fatalf("reconcile existing remote root: %v", err)
	}
	var target clusterModels.BackupTarget
	if err := database.First(&target, operation.TargetID).Error; err != nil {
		t.Fatalf("load committed target: %v", err)
	}
	assertFakeSSHCallSequence(t, h.Calls(), []string{
		"zfs version",
		"zfs list -H -o name -t filesystem -d 0 tank/backups",
	})
}

func TestReconcileBackupTargetProvisionKeepsTransientFailurePending(t *testing.T) {
	h := newFakeSSHHarness(t)
	h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		"zfs version": {{Stderr: "connection refused", ExitCode: 255}},
	}})
	database := newZeltaServiceTestDB(t,
		&clusterModels.BackupTarget{}, &clusterModels.BackupTargetProvisionOperation{}, &clusterModels.BackupJob{},
	)
	cluster := &clusterService.Service{DB: database}
	service := newTestZeltaService(database)
	service.Cluster = cluster
	operation, err := cluster.PrepareBackupTargetProvisionCreate(&clusterModels.BackupTarget{
		Name: "target", SSHHost: "root@backup", SSHKey: "key", BackupRoot: "tank/backups",
		CreateBackupRoot: true, Enabled: true,
	}, "provision:pending", true)
	if err != nil {
		t.Fatalf("prepare operation: %v", err)
	}
	if err := service.ReconcileBackupTargetProvisionOperations(context.Background()); err == nil {
		t.Fatal("expected transient reconciliation error")
	}
	stored, err := cluster.GetBackupTargetProvisionOperation(operation.Token)
	if err != nil {
		t.Fatalf("load operation: %v", err)
	}
	if stored.State != clusterModels.BackupTargetProvisionStatePending {
		t.Fatalf("transient failure state=%q", stored.State)
	}
	var count int64
	if err := database.Model(&clusterModels.BackupTarget{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("transient failure target count=%d err=%v", count, err)
	}
}
